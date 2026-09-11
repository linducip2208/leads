// Package outreach runs campaign send batches with safety enforcement:
// suppression list, invalid/disposable filtering, stop-on-reply, per-account
// hourly/daily limits. One failed contact never fails the batch.
package outreach

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/crypto"
	"leadforge/internal/mail"
	"leadforge/internal/metrics"
)

// Deps are outreach dependencies.
type Deps struct {
	Pool   *pgxpool.Pool
	Log    *slog.Logger
	Secret string
	AppURL string
}

// UnsubToken signs a contact id for one-click unsubscribe links.
func UnsubToken(secret, tenantID, contactID string) string {
	mac := sha256.Sum256([]byte(secret + "|unsub|" + tenantID + "|" + contactID))
	return contactID + "." + hex.EncodeToString(mac[:])
}

// VerifyUnsubToken checks the token and returns tenant + contact ids.
func VerifyUnsubToken(secret, tenantID, token string) (string, bool) {
	contactID, sig, ok := splitToken(token)
	if !ok {
		return "", false
	}
	mac := sha256.Sum256([]byte(secret + "|unsub|" + tenantID + "|" + contactID))
	want := hex.EncodeToString(mac[:])
	if len(sig) != len(want) || !hmacEqual(sig, want) {
		return "", false
	}
	return contactID, true
}

func splitToken(t string) (id, sig string, ok bool) {
	i := strings.LastIndex(t, ".")
	if i <= 0 || i+1 >= len(t) {
		return "", "", false
	}
	return t[:i], t[i+1:], true
}

func hmacEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}

func msgID(domain, campaignID, stepID, contactID string) string {
	seed := campaignID + ":" + stepID + ":" + contactID
	sum := sha256.Sum256([]byte(seed))
	return "<" + hex.EncodeToString(sum[:16]) + "@" + domain + ">"
}

func mailDomain(email string) string {
	if i := strings.LastIndex(email, "@"); i >= 0 {
		return email[i+1:]
	}
	return "localhost"
}

func isHardBounce(err error) bool {
	s := strings.ToLower(err.Error())
	for _, k := range []string{"550", "5.1.1", "5.2.2", "5.1.10", "mailbox unavailable", "user unknown", "recipient rejected", "blocked", "bounced", "undeliverable"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// RunBatch sends due emails for one running campaign. Returns sent count.
func RunBatch(ctx context.Context, d *Deps, sender mail.Sender, campaignID string) (int, error) {
	var tenantID, accountID, campaignName string
	var status string
	if err := d.Pool.QueryRow(ctx, `
		SELECT tenant_id::text, email_account_id::text, name, status FROM campaigns WHERE id=$1::uuid`,
		campaignID).Scan(&tenantID, &accountID, &campaignName, &status); err != nil {
		return 0, err
	}
	if status != "running" {
		return 0, nil
	}
	// A worker can disappear after claiming a contact. Requeue only claims
	// that have been abandoned long enough to avoid racing a slow SMTP send.
	_, _ = d.Pool.Exec(ctx, `
		UPDATE campaign_contacts
		SET status='pending', send_started_at=NULL, send_worker_id=''
		WHERE campaign_id=$1::uuid AND status='sending'
		  AND send_started_at < now() - interval '15 minutes'`, campaignID)
	acc, err := loadAccount(ctx, d, tenantID, accountID)
	if err != nil {
		return 0, fmt.Errorf("mail account: %w", err)
	}
	type step struct {
		ID        string
		Kind      string
		DayOff    int
		WaitDays  int
		Subject   string
		Body      string
		StopReply bool
	}
	rows, err := d.Pool.Query(ctx, `
		SELECT id::text, kind, day_offset, wait_days, subject, body, is_reply_stop
		FROM campaign_steps WHERE campaign_id=$1::uuid ORDER BY position`, campaignID)
	if err != nil {
		return 0, err
	}
	var steps []step
	for rows.Next() {
		var st step
		if err := rows.Scan(&st.ID, &st.Kind, &st.DayOff, &st.WaitDays, &st.Subject, &st.Body, &st.StopReply); err == nil {
			steps = append(steps, st)
		}
	}
	rows.Close()
	if len(steps) == 0 {
		return 0, fmt.Errorf("campaign has no steps")
	}

	crows, err := d.Pool.Query(ctx, `
		SELECT cc.id::text, cc.contact_id::text, cc.current_step, ct.email, ct.email_status,
			COALESCE(ct.first_name,''), COALESCE(NULLIF(ct.full_name,''), ct.email), COALESCE(c.name,''),
			cc.replied_at IS NOT NULL
		FROM campaign_contacts cc
		JOIN contacts ct ON ct.id = cc.contact_id
		LEFT JOIN companies c ON c.id = ct.company_id
		WHERE cc.campaign_id=$1::uuid AND cc.status IN ('pending','active')
			AND (cc.next_send_at IS NULL OR cc.next_send_at <= now())
		ORDER BY cc.created_at LIMIT 100`, campaignID)
	if err != nil {
		return 0, err
	}
	type contact struct {
		ccID, contactID string
		step            int
		email, estatus  string
		first, full     string
		company         string
		replied         bool
	}
	var contacts []contact
	for crows.Next() {
		var c contact
		if err := crows.Scan(&c.ccID, &c.contactID, &c.step, &c.email, &c.estatus, &c.first, &c.full, &c.company, &c.replied); err == nil {
			contacts = append(contacts, c)
		}
	}
	crows.Close()

	sent := 0
	sendWorkerID := fmt.Sprintf("pid-%d", os.Getpid())
	for _, c := range contacts {
		if ctx.Err() != nil {
			break
		}
		// stop-on-reply
		if c.replied {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='replied' WHERE id=$1::uuid`, c.ccID)
			continue
		}
		// suppression + invalid filtering
		var suppressed bool
		_ = d.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM suppression_list WHERE tenant_id=$1 AND email=$2)`,
			tenantID, c.email).Scan(&suppressed)
		if suppressed {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='unsubscribed' WHERE id=$1::uuid`, c.ccID)
			continue
		}
		if c.estatus == "invalid" || c.estatus == "disposable" || c.email == "" {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='bounced' WHERE id=$1::uuid`, c.ccID)
			continue
		}
		// account limits
		if limited(ctx, d, tenantID, accountID, acc) {
			d.Log.Info("outreach limits hit", "campaign", campaignID)
			break
		}
		// walk steps: waits accumulate, first email step sends
		idx := c.step
		var st *step
		for idx < len(steps) {
			if steps[idx].Kind == "wait" {
				idx++
				continue
			}
			st = &steps[idx]
			break
		}
		if st == nil {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='completed' WHERE id=$1::uuid`, c.ccID)
			continue
		}
		// stop-on-reply for this step: skip contacts with any reply in campaign
		if st.StopReply {
			var hasReply bool
			_ = d.Pool.QueryRow(ctx, `SELECT replied_at IS NOT NULL FROM campaign_contacts WHERE id=$1::uuid`, c.ccID).Scan(&hasReply)
			if hasReply {
				_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='replied' WHERE id=$1::uuid`, c.ccID)
				continue
			}
		}
		vars := mail.ContactVars(c.first, c.full, c.company, c.email, map[string]string{
			"unsubscribe_url": strings.TrimRight(d.AppURL, "/") + "/u/" + tenantID + "." + UnsubToken(d.Secret, tenantID, c.contactID),
		})
		subject := mail.Render(st.Subject, vars)
		body := mail.Render(st.Body, vars)
		if !strings.Contains(strings.ToLower(body), "unsub") {
			body += "\n\nUnsubscribe: " + vars["unsubscribe_url"]
		}
		msg := mail.Message{
			To: c.email, Subject: subject, Text: body,
			Unsub: vars["unsubscribe_url"], MsgID: msgID(mailDomain(acc.FromEmail), campaignID, st.ID, c.contactID),
		}
		// Claim immediately before SMTP. The conditional update is the
		// cross-worker idempotency gate; no network call occurs in a DB tx.
		var claimed bool
		if err := d.Pool.QueryRow(ctx, `
			UPDATE campaign_contacts
			SET status='sending', send_started_at=now(), send_worker_id=$2
			WHERE id=$1::uuid AND status IN ('pending','active')
			RETURNING true`, c.ccID, sendWorkerID).Scan(&claimed); err != nil || !claimed {
			continue
		}
		var messageStatus string
		if err := d.Pool.QueryRow(ctx, `
			INSERT INTO email_messages
				(tenant_id, campaign_id, step_id, email_account_id, contact_id, direction,
				 to_email, subject, body_text, status, message_id)
			VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'out',$6,$7,$8,'sending',$9)
			ON CONFLICT (tenant_id, message_id) WHERE direction='out' AND message_id <> ''
			DO UPDATE SET status = CASE WHEN email_messages.status='sent' THEN 'sent' ELSE 'sending' END,
				error='', subject=EXCLUDED.subject, body_text=EXCLUDED.body_text
			RETURNING status`, tenantID, campaignID, st.ID, accountID, c.contactID, c.email, subject, body, msg.MsgID).Scan(&messageStatus); err != nil {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='failed', send_started_at=NULL, send_worker_id='' WHERE id=$1::uuid`, c.ccID)
			d.Log.Warn("email delivery record failed", "campaign", campaignID, "contact", c.contactID, "err", err)
			continue
		}
		if messageStatus == "sent" {
			// The SMTP call succeeded in an earlier worker; finish the local
			// state transition without sending the same Message-ID again.
			_, _ = d.Pool.Exec(ctx, `UPDATE email_messages SET status='sent' WHERE tenant_id=$1 AND message_id=$2`, tenantID, msg.MsgID)
		} else if err := sender.Send(acc, msg); err != nil {
			metrics.EmailFailed.Add(1)
			d.Log.Warn("send failed", "to", c.email, "err", err)
			if isHardBounce(err) {
				_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='bounced', send_started_at=NULL, send_worker_id='' WHERE id=$1::uuid`, c.ccID)
				_, _ = d.Pool.Exec(ctx, `UPDATE contacts SET email_status='invalid' WHERE id=$1::uuid`, c.contactID)
				_, _ = d.Pool.Exec(ctx, `INSERT INTO suppression_list (tenant_id, email, reason) VALUES ($1,$2,'bounce') ON CONFLICT DO NOTHING`, tenantID, c.email)
				_, _ = d.Pool.Exec(ctx, `UPDATE email_messages SET status='bounced', error=$3 WHERE tenant_id=$1 AND message_id=$2`, tenantID, msg.MsgID, trunc(err.Error()))
			} else {
				_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='failed', send_started_at=NULL, send_worker_id='' WHERE id=$1::uuid`, c.ccID)
				_, _ = d.Pool.Exec(ctx, `UPDATE email_messages SET status='failed', error=$3 WHERE tenant_id=$1 AND message_id=$2`, tenantID, msg.MsgID, trunc(err.Error()))
			}
			continue
		} else {
			metrics.EmailSent.Add(1)
			_, _ = d.Pool.Exec(ctx, `UPDATE email_messages SET status='sent', sent_at=now(), error='' WHERE tenant_id=$1 AND message_id=$2`, tenantID, msg.MsgID)
		}
		// advance to next email step
		nextIdx := idx + 1
		nextDays := 0
		for nextIdx < len(steps) && steps[nextIdx].Kind == "wait" {
			nextDays += steps[nextIdx].WaitDays
			nextIdx++
		}
		if nextIdx < len(steps) {
			nextDays += steps[nextIdx].DayOff
		}
		if nextIdx >= len(steps) {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='completed', current_step=$2, send_started_at=NULL, send_worker_id='' WHERE id=$1::uuid`, c.ccID, idx)
		} else {
			_, _ = d.Pool.Exec(ctx, `UPDATE campaign_contacts SET status='active', current_step=$2, next_send_at=now()+($3::int||' days')::interval, send_started_at=NULL, send_worker_id='' WHERE id=$1::uuid`, c.ccID, nextIdx, nextDays)
		}
		sent++
	}
	refreshCounts(ctx, d, campaignID)
	return sent, nil
}

func trunc(s string) string {
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

func loadAccount(ctx context.Context, d *Deps, tenantID, accountID string) (mail.Account, error) {
	var a mail.Account
	var passEnc []byte
	err := d.Pool.QueryRow(ctx, `
		SELECT from_name, from_email, smtp_host, smtp_port, smtp_encryption,
			COALESCE(smtp_username,''), smtp_password_enc
		FROM email_accounts WHERE id=$1 AND tenant_id=$2 AND is_active`, accountID, tenantID).
		Scan(&a.FromName, &a.FromEmail, &a.Host, &a.Port, &a.Encryption, &a.Username, &passEnc)
	if err != nil {
		return a, err
	}
	if len(passEnc) > 0 {
		pw, err := crypto.Decrypt(d.Secret, string(passEnc))
		if err != nil {
			return a, err
		}
		a.Password = pw
	}
	return a, nil
}

func limited(ctx context.Context, d *Deps, tenantID, accountID string, acc mail.Account) bool {
	var hourly, daily int
	_ = d.Pool.QueryRow(ctx, `SELECT hourly_limit, daily_limit FROM email_accounts WHERE id=$1`, accountID).Scan(&hourly, &daily)
	var h, day int
	_ = d.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_messages WHERE email_account_id=$1 AND status='sent' AND sent_at > now() - interval '1 hour'`, accountID).Scan(&h)
	_ = d.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM email_messages WHERE email_account_id=$1 AND status='sent' AND sent_at > now() - interval '1 day'`, accountID).Scan(&day)
	_ = tenantID
	if hourly > 0 && h >= hourly {
		return true
	}
	if daily > 0 && day >= daily {
		return true
	}
	return false
}

func refreshCounts(ctx context.Context, d *Deps, campaignID string) {
	_, _ = d.Pool.Exec(ctx, `
		UPDATE campaigns SET
			total_contacts = (SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid),
			sent_count = (SELECT COUNT(*) FROM email_messages WHERE campaign_id=$1::uuid AND status='sent'),
			reply_count = (SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid AND status='replied'),
			bounce_count = (SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid AND status='bounced'),
			unsubscribe_count = (SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid AND status='unsubscribed')
		WHERE id=$1::uuid`, campaignID)
}
