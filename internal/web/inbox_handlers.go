package web

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/mail"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/inbox"
)

func (s *Server) handleInbox(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	filter := r.URL.Query().Get("status")
	if filter == "" {
		filter = "open"
	}
	q := `SELECT cv.id::text, COALESCE(NULLIF(ct.full_name,''), ct.email), ct.email,
		COALESCE(NULLIF(cv.subject,''), '(no subject)'), cv.unread_count, cv.status,
		to_char(cv.last_message_at,'DD Mon HH24:MI')
		FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
		WHERE cv.tenant_id=$1`
	args := []any{id.TenantID}
	if filter != "" {
		q += " AND cv.status=$2"
		args = append(args, filter)
	}
	q += " ORDER BY cv.last_message_at DESC LIMIT 100"
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load inbox.", Err: err})
		return
	}
	defer rows.Close()
	d := &inbox.ListData{Filter: filter}
	for rows.Next() {
		var it inbox.Item
		if err := rows.Scan(&it.ID, &it.Contact, &it.Email, &it.Subject, &it.Unread, &it.Status, &it.Updated); err == nil {
			d.Items = append(d.Items, it)
		}
	}
	p := s.page(w, r, "Inbox", "/inbox")
	layouts.AppShell(s.Ren, id, p, inbox.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleInboxDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	d := &inbox.DetailData{ID: cid}
	var contactID string
	err := s.PG.QueryRow(r.Context(), `
		SELECT cv.contact_id::text, COALESCE(NULLIF(ct.full_name,''), ct.email), ct.email,
			COALESCE(NULLIF(cv.subject,''),'(no subject)'), cv.status
		FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
		WHERE cv.id=$1::uuid AND cv.tenant_id=$2`, cid, id.TenantID).
		Scan(&contactID, &d.Contact, &d.Email, &d.Subject, &d.Status)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	d.ContactID = contactID
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, direction, from_email, to_email, subject,
			COALESCE(NULLIF(body_text,''), body_html), to_char(created_at,'DD Mon HH24:MI')
		FROM email_messages WHERE conversation_id=$1::uuid ORDER BY created_at`, cid)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m inbox.Message
			if err := rows.Scan(&m.ID, &m.Direction, &m.From, &m.To, &m.Subject, &m.Body, &m.When); err == nil {
				d.Messages = append(d.Messages, m)
			}
		}
	}
	p := s.page(w, r, d.Contact, "/inbox")
	layouts.AppShell(s.Ren, id, p, inbox.Detail(p, d)).Render(r.Context(), w)
}

// defaultAccountID returns the first active sending account.
func (s *Server) defaultAccountID(ctx context.Context, tenantID string) string {
	var id string
	_ = s.PG.QueryRow(ctx, `SELECT id::text FROM email_accounts WHERE tenant_id=$1 AND is_active ORDER BY created_at LIMIT 1`, tenantID).Scan(&id)
	return id
}

func (s *Server) handleInboxReply(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	if subject == "" || strings.TrimSpace(body) == "" {
		webapp.RedirectFlash(w, r, "/inbox/"+cid, flash.Error, "Subject and message are required.")
		return
	}
	var tenantID, contactID, contactEmail, contactName string
	err := s.PG.QueryRow(r.Context(), `
		SELECT cv.tenant_id::text, cv.contact_id::text, ct.email, COALESCE(ct.full_name,'')
		FROM conversations cv JOIN contacts ct ON ct.id = cv.contact_id
		WHERE cv.id=$1::uuid AND cv.tenant_id=$2`, cid, id.TenantID).
		Scan(&tenantID, &contactID, &contactEmail, &contactName)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	accID := s.defaultAccountID(r.Context(), tenantID)
	if accID == "" {
		webapp.RedirectFlash(w, r, "/inbox/"+cid, flash.Error, "No sending account — add one under Settings → Email Accounts.")
		return
	}
	acc, _, err := s.loadMailAccount(r.Context(), tenantID, accID)
	if err != nil {
		webapp.RedirectFlash(w, r, "/inbox/"+cid, flash.Error, "Sending account unavailable.")
		return
	}
	msg := mail.Message{To: contactEmail, Subject: subject, Text: body, MsgID: ""}
	if err := (mail.SMTPSender{}).Send(acc, msg); err != nil {
		webapp.RedirectFlash(w, r, "/inbox/"+cid, flash.Error, "Send failed: "+shortErr(err))
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO email_messages (tenant_id, email_account_id, contact_id, conversation_id, direction, from_email, to_email, subject, body_text, status, sent_at)
		VALUES ($1,$2::uuid,$3::uuid,$4::uuid,'out',$5,$6,$7,$8,'sent',now())`,
		tenantID, accID, contactID, cid, acc.FromEmail, contactEmail, subject, body)
	_, _ = s.PG.Exec(r.Context(), `UPDATE conversations SET last_message_at=now(), unread_count=0, subject=$2 WHERE id=$1::uuid`, cid, subject)
	webapp.RedirectFlash(w, r, "/inbox/"+cid, flash.Success, "Reply sent.")
}

func (s *Server) handleInboxClose(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	status := "closed"
	if r.FormValue("reopen") != "" {
		status = "open"
	}
	_, _ = s.PG.Exec(r.Context(), `UPDATE conversations SET status=$3 WHERE id=$1::uuid AND tenant_id=$2`, r.PathValue("id"), id.TenantID, status)
	http.Redirect(w, r, "/inbox/"+r.PathValue("id"), http.StatusSeeOther)
}

func (s *Server) handleInboxRead(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `UPDATE conversations SET unread_count=0 WHERE id=$1::uuid AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/inbox/"+r.PathValue("id"), http.StatusSeeOther)
}

// handleInbound receives inbound email webhooks: POST /webhooks/inbound?key=...
// JSON: {"to":"sales@tenant.com","from":"lead@x.com","subject":"…","body":"…"}.
// The recipient address is matched to a sending account to scope the tenant.
// Replies flip campaign contacts to replied (stop-on-reply).
func (s *Server) handleInbound(w http.ResponseWriter, r *http.Request) {
	// auth: HMAC header (preferred) or legacy ?key= (backward compatible)
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.validInboundAuth(r, body) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var in struct {
		To      string `json:"to"`
		From    string `json:"from"`
		Subject string `json:"subject"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	in.To = strings.TrimSpace(in.To)
	in.From = strings.TrimSpace(in.From)
	if in.To == "" || in.From == "" {
		http.Error(w, "to/from required", http.StatusBadRequest)
		return
	}
	var tenantID, accountID string
	err = s.PG.QueryRow(r.Context(), `SELECT tenant_id::text, id::text FROM email_accounts WHERE from_email=$1 LIMIT 1`, in.To).Scan(&tenantID, &accountID)
	if err != nil {
		// unknown recipient: accept but ignore (avoid sender enumeration)
		w.WriteHeader(http.StatusOK)
		return
	}
	var contactID, contactName string
	_ = s.PG.QueryRow(r.Context(), `SELECT id::text, COALESCE(full_name,'') FROM contacts WHERE tenant_id=$1 AND email=$2 LIMIT 1`,
		tenantID, strings.ToLower(in.From)).Scan(&contactID, &contactName)
	if contactID == "" {
		w.WriteHeader(http.StatusOK)
		return
	}
	var convID string
	_ = s.PG.QueryRow(r.Context(), `
		SELECT id::text FROM conversations WHERE tenant_id=$1 AND contact_id=$2::uuid AND status='open'
		ORDER BY last_message_at DESC LIMIT 1`, tenantID, contactID).Scan(&convID)
	if convID == "" {
		_ = s.PG.QueryRow(r.Context(), `
			INSERT INTO conversations (tenant_id, contact_id, subject, unread_count)
			VALUES ($1,$2::uuid,$3,1) RETURNING id::text`, tenantID, contactID, in.Subject).Scan(&convID)
	} else {
		_, _ = s.PG.Exec(r.Context(), `
			UPDATE conversations SET last_message_at=now(), unread_count=unread_count+1, subject=$2 WHERE id=$1::uuid`,
			convID, in.Subject)
	}
	if convID != "" {
		_, _ = s.PG.Exec(r.Context(), `
			INSERT INTO email_messages (tenant_id, email_account_id, contact_id, conversation_id, direction, from_email, to_email, subject, body_text, status)
			VALUES ($1,$2::uuid,$3::uuid,$4::uuid,'in',$5,$6,$7,$8,'sent')`,
			tenantID, accountID, contactID, convID, in.From, in.To, in.Subject, in.Body)
	}
	// stop-on-reply across campaigns
	_, _ = s.PG.Exec(r.Context(), `
		UPDATE campaign_contacts SET status='replied', replied_at=now()
		WHERE contact_id=$1::uuid AND status IN ('pending','active','paused')`, contactID)
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO activities (tenant_id, kind, subject, user_id)
		SELECT $1,'reply.received',$2,NULL WHERE $3 <> ''`, tenantID, "Reply from "+contactName, contactID)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) inboundKey() string {
	return s.Cfg.InboundKey
}

// validInboundAuth accepts (a) HMAC-SHA256 hex of the raw body in
// X-Signature-256 with X-Timestamp (±5 min replay window), or (b) the legacy
// ?key= shared secret. Empty configured key leaves (b) open by suffix match
// of nothing — i.e. dev only; production must set INBOUND_WEBHOOK_KEY.
func (s *Server) validInboundAuth(r *http.Request, body []byte) bool {
	want := s.inboundKey()
	if sig := r.Header.Get("X-Signature-256"); sig != "" && want != "" {
		ts := r.Header.Get("X-Timestamp")
		var skew int64 = -1
		if ts != "" {
			if t, err := strconv.ParseInt(ts, 10, 64); err == nil {
				skew = nowUnix() - t
				if skew < 0 {
					skew = -skew
				}
			}
		}
		if skew >= 0 && skew <= 300 {
			mac := hmacSHA256(want, ts+"."+string(body))
			if subtleEqual(sig, mac) {
				return true
			}
		}
		return false
	}
	// Query-string shared-secret authentication is retained for local legacy
	// adapters only. Production inbound delivery must be timestamped HMAC.
	if strings.EqualFold(s.Cfg.Env, "production") {
		return false
	}
	if want != "" && r.URL.Query().Get("key") != want {
		return false
	}
	return true
}

func hmacSHA256(key, msg string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(msg))
	return hex.EncodeToString(m.Sum(nil))
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func nowUnix() int64 { return time.Now().Unix() }

func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		return s[:160]
	}
	return s
}
