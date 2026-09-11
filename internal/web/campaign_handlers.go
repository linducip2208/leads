package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/outreach"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/campaigns"
)

func (s *Server) campaignRoutes() {
	s.Router.HandleFunc("GET", "/campaigns", s.requirePerm(role.CampaignCreate, s.handleCampaignList))
	s.Router.HandleFunc("GET", "/campaigns/new", s.requirePerm(role.CampaignCreate, s.handleCampaignNew))
	s.Router.HandleFunc("POST", "/campaigns", s.requirePerm(role.CampaignCreate, s.handleCampaignCreate))
	s.Router.HandleFunc("GET", "/campaigns/{id}", s.requirePerm(role.CampaignCreate, s.handleCampaignDetail))
	s.Router.HandleFunc("POST", "/campaigns/{id}/launch", s.requirePerm(role.CampaignLaunch, s.handleCampaignLaunch))
	s.Router.HandleFunc("POST", "/campaigns/{id}/pause", s.requirePerm(role.CampaignLaunch, s.handleCampaignPause))
	s.Router.HandleFunc("POST", "/campaigns/{id}/cancel", s.requirePerm(role.CampaignLaunch, s.handleCampaignCancel))
	s.Router.HandleFunc("POST", "/campaigns/{id}/account", s.requirePerm(role.CampaignCreate, s.handleCampaignAccount))
	s.Router.HandleFunc("POST", "/campaigns/{id}/steps", s.requirePerm(role.CampaignCreate, s.handleStepAdd))
	s.Router.HandleFunc("POST", "/campaigns/{id}/steps/{step}/delete", s.requirePerm(role.CampaignCreate, s.handleStepDelete))
	// templates
	s.Router.HandleFunc("GET", "/templates", s.requirePerm(role.CampaignCreate, func(w http.ResponseWriter, r *http.Request) {
		s.handleTemplates(w, r)
	}))
	s.Router.HandleFunc("POST", "/templates", s.requirePerm(role.CampaignCreate, s.handleTemplateCreate))
	s.Router.HandleFunc("POST", "/templates/{id}/delete", s.requirePerm(role.CampaignCreate, s.handleTemplateDelete))
	// public unsubscribe (no auth)
	s.Router.HandleFunc("GET", "/u/{token}", s.handleUnsubPage)
	s.Router.HandleFunc("POST", "/u/{token}", s.handleUnsubSubmit)
	// inbox
	s.Router.HandleFunc("GET", "/inbox", s.requirePerm(role.LeadRead, s.handleInbox))
	s.Router.HandleFunc("GET", "/inbox/{id}", s.requirePerm(role.LeadRead, s.handleInboxDetail))
	s.Router.HandleFunc("POST", "/inbox/{id}/reply", s.requirePerm(role.LeadRead, s.handleInboxReply))
	s.Router.HandleFunc("POST", "/inbox/{id}/close", s.requirePerm(role.LeadRead, s.handleInboxClose))
	s.Router.HandleFunc("POST", "/inbox/{id}/read", s.requirePerm(role.LeadRead, s.handleInboxRead))
	// inbound email webhook (shared secret via ?key=)
	s.Router.HandleFunc("POST", "/webhooks/inbound", s.handleInbound)
}

func (s *Server) handleCampaignList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT c.id::text, c.name, c.status, c.total_contacts, c.sent_count, c.reply_count,
			c.bounce_count, c.unsubscribe_count, to_char(c.created_at,'DD Mon HH24:MI'), COALESCE(ea.name,'')
		FROM campaigns c LEFT JOIN email_accounts ea ON ea.id = c.email_account_id
		WHERE c.tenant_id=$1 ORDER BY c.created_at DESC LIMIT 100`, id.TenantID)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load campaigns.", Err: err})
		return
	}
	defer rows.Close()
	d := &campaigns.ListData{}
	for rows.Next() {
		var it campaigns.Item
		if err := rows.Scan(&it.ID, &it.Name, &it.Status, &it.Total, &it.Sent, &it.Replies, &it.Bounces, &it.Unsubs, &it.Created, &it.Account); err == nil {
			d.Items = append(d.Items, it)
		}
	}
	p := s.page(w, r, "Campaigns", "/campaigns")
	layouts.AppShell(s.Ren, id, p, campaigns.List(p, d)).Render(r.Context(), w)
}

func (s *Server) campaignFormOptions(r *http.Request) (segs, lists, tmpls, accs []campaigns.Option) {
	tid := webappIdentity(r).TenantID
	q := func(sql string) []campaigns.Option {
		var out []campaigns.Option
		rows, err := s.PG.Query(r.Context(), sql, tid)
		if err != nil {
			return nil
		}
		defer rows.Close()
		for rows.Next() {
			var o campaigns.Option
			if err := rows.Scan(&o.Value, &o.Label); err == nil {
				out = append(out, o)
			}
		}
		return out
	}
	segs = q(`SELECT id::text, name FROM segments WHERE tenant_id=$1 ORDER BY name`)
	lists = q(`SELECT id::text, name FROM lists WHERE tenant_id=$1 ORDER BY name`)
	tmpls = q(`SELECT id::text, name FROM email_templates WHERE tenant_id=$1 ORDER BY name`)
	accs = q(`SELECT id::text, name || ' <' || from_email || '>' FROM email_accounts WHERE tenant_id=$1 AND is_active ORDER BY name`)
	return segs, lists, tmpls, accs
}

func (s *Server) handleCampaignNew(w http.ResponseWriter, r *http.Request) {
	segs, lists, tmpls, accs := s.campaignFormOptions(r)
	p := s.page(w, r, "New Campaign", "/campaigns")
	layouts.AppShell(s.Ren, webappIdentity(r), p, campaigns.New(p, &campaigns.NewData{
		Segments: segs, Lists: lists, Templates: tmpls, Accounts: accs,
	})).Render(r.Context(), w)
}

func (s *Server) handleCampaignCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	aud := strings.TrimSpace(r.FormValue("audience"))
	var audID any
	if aud == "list" {
		aud = "list"
		if v := strings.TrimSpace(r.FormValue("list_id")); v != "" {
			audID = v
		}
	} else {
		aud = "segment"
		if v := strings.TrimSpace(r.FormValue("segment_id")); v != "" {
			audID = v
		}
	}
	accountID := strings.TrimSpace(r.FormValue("account_id"))
	fail := func(msg string) {
		segs, lists, tmpls, accs := s.campaignFormOptions(r)
		p := s.page(w, r, "New Campaign", "/campaigns")
		dd := &campaigns.NewData{Segments: segs, Lists: lists, Templates: tmpls, Accounts: accs}
		dd.Form.Error = msg
		dd.Form.Name = name
		layouts.AppShell(s.Ren, id, p, campaigns.New(p, dd)).Render(r.Context(), w)
	}
	if name == "" {
		fail("Name is required.")
		return
	}
	if audID == nil {
		fail("Select an audience segment or list.")
		return
	}
	if accountID == "" {
		fail("Select a sending account (add one under Settings → Email Accounts).")
		return
	}
	var valid bool
	if err := s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM email_accounts WHERE id=$1::uuid AND tenant_id=$2 AND is_active)`, accountID, id.TenantID).Scan(&valid); err != nil || !valid {
		fail("Select a valid active sending account.")
		return
	}
	if aud == "list" {
		_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM lists WHERE id=$1::uuid AND tenant_id=$2)`, audID, id.TenantID).Scan(&valid)
	} else {
		_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM segments WHERE id=$1::uuid AND tenant_id=$2)`, audID, id.TenantID).Scan(&valid)
	}
	if !valid {
		fail("Select a valid audience.")
		return
	}
	var scheduled any
	if v := strings.TrimSpace(r.FormValue("scheduled_at")); v != "" {
		if t, err := time.Parse("2006-01-02T15:04", v); err == nil {
			scheduled = t
		}
	}
	var cid string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO campaigns (tenant_id, name, status, audience_type, audience_id, email_account_id, scheduled_at, created_by)
		VALUES ($1,$2,'draft',$3,$4::uuid,$5::uuid,$6::timestamptz,$7) RETURNING id::text`,
		id.TenantID, name, aud, audID, accountID, scheduled, id.UserID).Scan(&cid)
	if err != nil {
		s.Log.Error("create campaign", "err", err)
		fail("Could not create the campaign.")
		return
	}
	// step 1 from template (or blank email step)
	subject, body := "Hello {{first_name}}", "Hi {{first_name}},\n\nWanted to reach out.\n\n{{unsubscribe_url}}"
	if tid := strings.TrimSpace(r.FormValue("template_id")); tid != "" {
		_ = s.PG.QueryRow(r.Context(), `SELECT subject, body FROM email_templates WHERE id=$1 AND tenant_id=$2`, tid, id.TenantID).Scan(&subject, &body)
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO campaign_steps (campaign_id, position, day_offset, kind, subject, body, is_reply_stop)
		VALUES ($1::uuid, 0, 0, 'email', $2, $3, true)`, cid, subject, body)
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

func (s *Server) handleCampaignDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	d := &campaigns.DetailData{ID: cid}
	var scheduled, started *time.Time
	var audType string
	var audID *string
	err := s.PG.QueryRow(r.Context(), `
		SELECT c.name, c.status, COALESCE(ea.name,''), c.email_account_id::text,
			c.audience_type, c.audience_id::text, c.total_contacts, c.sent_count,
			c.open_count, c.click_count, c.reply_count, c.bounce_count, c.unsubscribe_count,
			c.scheduled_at, c.started_at
		FROM campaigns c LEFT JOIN email_accounts ea ON ea.id = c.email_account_id
		WHERE c.id=$1::uuid AND c.tenant_id=$2`,
		cid, id.TenantID).Scan(&d.Name, &d.Status, &d.Account, &d.AccountID, &audType, &audID,
		&d.Total, &d.Sent, &d.Opens, &d.Clicks, &d.Replies, &d.Bounces, &d.Unsubs, &scheduled, &started)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	if scheduled != nil {
		d.Scheduled = scheduled.Format("02 Jan 15:04")
	}
	if started != nil {
		d.Started = started.Format("02 Jan 15:04")
	}
	if audID != nil {
		table := "segments"
		if audType == "list" {
			table = "lists"
		}
		_ = s.PG.QueryRow(r.Context(), `SELECT name FROM `+table+` WHERE id=$1::uuid AND tenant_id=$2`, *audID, id.TenantID).Scan(&d.Audience)
		d.Audience = audType + ": " + d.Audience
	}
	srows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, position, kind, day_offset, wait_days, subject, body, is_reply_stop
		FROM campaign_steps WHERE campaign_id=$1::uuid ORDER BY position`, cid)
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var st campaigns.Step
			if err := srows.Scan(&st.ID, &st.Position, &st.Kind, &st.DayOff, &st.WaitDays, &st.Subject, &st.Body, &st.StopReply); err == nil {
				d.Steps = append(d.Steps, st)
			}
		}
	}
	crows, _ := s.PG.Query(r.Context(), `
		SELECT cc.id::text, COALESCE(NULLIF(ct.full_name,''), ct.email), ct.email, cc.status, cc.current_step,
			COALESCE(to_char(cc.next_send_at,'DD Mon HH24:MI'),'')
		FROM campaign_contacts cc JOIN contacts ct ON ct.id = cc.contact_id
		WHERE cc.campaign_id=$1::uuid AND cc.tenant_id=$2 ORDER BY cc.created_at LIMIT 100`, cid, id.TenantID)
	if crows != nil {
		defer crows.Close()
		for crows.Next() {
			var c campaigns.ContactRow
			if err := crows.Scan(&c.ID, &c.Name, &c.Email, &c.Status, &c.Step, &c.NextSend); err == nil {
				d.Contacts = append(d.Contacts, c)
			}
		}
	}
	_, _, tmpls, accs := s.campaignFormOptions(r)
	_ = tmpls
	d.Accounts = accs
	p := s.page(w, r, d.Name, "/campaigns")
	layouts.AppShell(s.Ren, id, p, campaigns.Detail(p, d)).Render(r.Context(), w)
}

// buildAudience materializes segment/list members into campaign_contacts.
func (s *Server) buildAudience(ctx context.Context, tenantID, cid string) (int, error) {
	return buildAudienceTx(ctx, s.PG, tenantID, cid)
}

func (s *Server) handleCampaignLaunch(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	var status string
	_ = s.PG.QueryRow(r.Context(), `SELECT status FROM campaigns WHERE id=$1::uuid AND tenant_id=$2`, cid, id.TenantID).Scan(&status)
	if status != "draft" && status != "paused" && status != "scheduled" {
		webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Error, "Only drafts, paused or scheduled campaigns can launch.")
		return
	}
	n, err := s.buildAudience(r.Context(), id.TenantID, cid)
	if err != nil {
		s.Log.Error("build audience", "err", err)
		webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Error, "Could not build the audience.")
		return
	}
	now := "running"
	var scheduledAt *time.Time
	_ = s.PG.QueryRow(r.Context(), `SELECT scheduled_at FROM campaigns WHERE id=$1::uuid AND tenant_id=$2`, cid, id.TenantID).Scan(&scheduledAt)
	if scheduledAt != nil && scheduledAt.After(time.Now()) {
		now = "scheduled"
	}
	_, _ = s.PG.Exec(r.Context(), `
		UPDATE campaigns SET status=$2, started_at=COALESCE(started_at, now()),
			total_contacts=(SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid)
		WHERE id=$1::uuid AND tenant_id=$3`, cid, now, id.TenantID)
	if s.Queue != nil && now == "running" {
		_ = s.Queue.EnqueueOutreachSend(r.Context(), cid)
	}
	webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Success, "Campaign launched — "+strconv.Itoa(n)+" contacts queued.")
}

func (s *Server) handleCampaignPause(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	_, _ = s.PG.Exec(r.Context(), `UPDATE campaigns SET status='paused' WHERE id=$1::uuid AND tenant_id=$2 AND status='running'`, cid, id.TenantID)
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

func (s *Server) handleCampaignCancel(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	_, _ = s.PG.Exec(r.Context(), `UPDATE campaigns SET status='cancelled', completed_at=now() WHERE id=$1::uuid AND tenant_id=$2 AND status IN ('draft','scheduled','running','paused')`, cid, id.TenantID)
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

func (s *Server) handleCampaignAccount(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	aid := strings.TrimSpace(r.FormValue("account_id"))
	var valid bool
	_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM email_accounts WHERE id=$1::uuid AND tenant_id=$2 AND is_active)`, aid, id.TenantID).Scan(&valid)
	if !valid {
		webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Error, "Select a valid active sending account.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `UPDATE campaigns SET email_account_id=$3::uuid WHERE id=$1::uuid AND tenant_id=$2 AND status IN ('draft','paused','scheduled')`,
		cid, id.TenantID, aid)
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

func (s *Server) handleStepAdd(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	var owned bool
	_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM campaigns WHERE id=$1::uuid AND tenant_id=$2 AND status IN ('draft','paused','scheduled'))`,
		cid, id.TenantID).Scan(&owned)
	if !owned {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	kind := strings.TrimSpace(r.FormValue("kind"))
	if kind != "wait" {
		kind = "email"
	}
	dayOff, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("day_offset")))
	waitDays, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("wait_days")))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	if kind == "email" && subject == "" {
		webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Error, "Email steps need a subject.")
		return
	}
	_, err := s.PG.Exec(r.Context(), `
		INSERT INTO campaign_steps (campaign_id, position, day_offset, wait_days, kind, subject, body, is_reply_stop)
		SELECT $1::uuid, COALESCE(MAX(position)+1,0), $2, $3, $4, $5, $6, true
		FROM campaign_steps WHERE campaign_id=$1::uuid`,
		cid, dayOff, waitDays, kind, subject, body)
	if err != nil {
		webapp.RedirectFlash(w, r, "/campaigns/"+cid, flash.Error, "Could not add the step.")
		return
	}
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

func (s *Server) handleStepDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	_, _ = s.PG.Exec(r.Context(), `
		DELETE FROM campaign_steps WHERE id=$1::uuid AND campaign_id=$2::uuid
		AND EXISTS (SELECT 1 FROM campaigns WHERE id=$2::uuid AND tenant_id=$3 AND status IN ('draft','paused','scheduled'))`,
		r.PathValue("step"), cid, id.TenantID)
	http.Redirect(w, r, "/campaigns/"+cid, http.StatusSeeOther)
}

var _ = outreach.UnsubToken
