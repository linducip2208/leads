package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"leadforge/internal/flash"
	"leadforge/internal/scoring"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/leads"
)

// loadLeadDetail fetches everything for detail + drawer views (tenant-scoped).
func (s *Server) loadLeadDetail(r *http.Request, leadID string) (*leads.DetailData, bool) {
	id := webappIdentity(r)
	d := &leads.DetailData{}
	var ownerID *string
	var searchID *string
	var createdStr string
	err := s.PG.QueryRow(r.Context(), `
		SELECT l.id::text, c.name, c.id::text, COALESCE(c.website,''), COALESCE(c.domain,''),
			COALESCE(NULLIF(c.city,''), c.province, ''), COALESCE(c.industry,''),
			l.lead_score, l.status, COALESCE(u.name,''), l.owner_id::text,
			l.source, l.source_search_id::text, to_char(l.created_at,'DD Mon YYYY HH24:MI'),
			COALESCE(ct.full_name,''), COALESCE(ct.job_title,''), COALESCE(ct.email,''), COALESCE(ct.phone,'')
		FROM leads l
		JOIN companies c ON c.id = l.company_id
		LEFT JOIN users u ON u.id = l.owner_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		WHERE l.id=$1 AND l.tenant_id=$2`, leadID, id.TenantID).
		Scan(&d.ID, &d.Company, &d.CompanyID, &d.Website, &d.Domain, &d.Location, &d.Industry,
			&d.Score, &d.Status, &d.Owner, &ownerID, &d.Source, &searchID, &createdStr,
			&d.Contact.Name, &d.Contact.Title, &d.Contact.Email, &d.Contact.Phone)
	if err != nil {
		return nil, false
	}
	d.Created = createdStr
	if ownerID != nil {
		d.OwnerID = *ownerID
	}
	if searchID != nil {
		d.SearchID = *searchID
	}
	d.ScoreLabel = scoring.Label(d.Score)

	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, COALESCE(NULLIF(full_name,''), email), COALESCE(job_title,''),
			COALESCE(email,''), COALESCE(NULLIF(phone,''), mobile, ''), COALESCE(linkedin_url,''), lead_score
		FROM contacts WHERE tenant_id=$1 AND company_id=$2 ORDER BY created_at LIMIT 20`, id.TenantID, d.CompanyID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var c leads.ContactRow
			if err := rows.Scan(&c.ID, &c.Name, &c.Title, &c.Email, &c.Phone, &c.LinkedIn, &c.Score); err == nil {
				d.Contacts = append(d.Contacts, c)
			}
		}
	}
	if len(d.Contacts) > 0 && d.Contact.Name == "" {
		d.Contact = d.Contacts[0]
	}
	if d.Contact.Email != "" {
		d.HasEmail, d.Email = true, d.Contact.Email
	} else if len(d.Contacts) > 0 && d.Contacts[0].Email != "" {
		d.HasEmail, d.Email = true, d.Contacts[0].Email
	}
	// whatsapp from company
	var wa string
	_ = s.PG.QueryRow(r.Context(), `SELECT COALESCE(NULLIF(whatsapp,''), phone, '') FROM companies WHERE id=$1 AND tenant_id=$2`, d.CompanyID, id.TenantID).Scan(&wa)
	if digits := digitsOnly(wa); digits != "" {
		d.HasWhatsApp = true
		d.WhatsAppLink = "https://wa.me/" + digits
	}
	// score breakdown
	var bb []byte
	_ = s.PG.QueryRow(r.Context(), `SELECT breakdown FROM lead_scores WHERE lead_id=$1`, d.ID).Scan(&bb)
	if len(bb) > 0 {
		_ = json.Unmarshal(bb, &d.Breakdown)
	}
	// activities
	arows, _ := s.PG.Query(r.Context(), `
		SELECT a.kind, COALESCE(NULLIF(a.subject,''), a.kind), COALESCE(u.name,'System'), to_char(a.created_at,'DD Mon HH24:MI')
		FROM activities a LEFT JOIN users u ON u.id = a.user_id
		WHERE a.tenant_id=$1 AND a.lead_id=$2 ORDER BY a.created_at DESC LIMIT 20`, id.TenantID, d.ID)
	if arows != nil {
		defer arows.Close()
		for arows.Next() {
			var a leads.ActivityRow
			if err := arows.Scan(&a.Kind, &a.Subject, &a.Who, &a.When); err == nil {
				d.Activities = append(d.Activities, a)
			}
		}
	}
	// deals
	drows, _ := s.PG.Query(r.Context(), `
		SELECT dl.id::text, dl.title, COALESCE(ps.name, dl.status), dl.value::text, dl.status
		FROM deals dl LEFT JOIN pipeline_stages ps ON ps.id = dl.stage_id
		WHERE dl.tenant_id=$1 AND dl.lead_id=$2 ORDER BY dl.created_at DESC LIMIT 10`, id.TenantID, d.ID)
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var dl leads.DealRow
			if err := drows.Scan(&dl.ID, &dl.Title, &dl.Stage, &dl.Value, &dl.Status); err == nil {
				d.Deals = append(d.Deals, dl)
			}
		}
	}
	// enrichments
	erows, _ := s.PG.Query(r.Context(), `		SELECT kind, status, data, to_char(created_at,'DD Mon HH24:MI')
		FROM lead_enrichments WHERE tenant_id=$1 AND company_id=$2 ORDER BY created_at DESC LIMIT 5`, id.TenantID, d.CompanyID)
	if erows != nil {
		defer erows.Close()
		for erows.Next() {
			var e leads.EnrichRow
			var data []byte
			var when string
			if err := erows.Scan(&e.Kind, &e.Status, &data, &when); err == nil {
				e.When = when
				var m map[string]any
				if json.Unmarshal(data, &m) == nil {
					if c, ok := m["confidence"].(float64); ok {
						e.Confidence = int(c)
					}
				}
				d.Enrichments = append(d.Enrichments, e)
			}
		}
	}
	orows, _ := s.PG.Query(r.Context(), `
		SELECT ps.name, lo.score, lo.reason
		FROM lead_opportunities lo JOIN products_services ps ON ps.id = lo.product_id
		WHERE lo.lead_id=$1 AND ps.tenant_id=$2 ORDER BY lo.score DESC`, d.ID, id.TenantID)
	if orows != nil {
		defer orows.Close()
		for orows.Next() {
			var o leads.OppRow
			if err := orows.Scan(&o.Title, &o.Confidence, &o.Reason); err == nil {
				d.Opportunities = append(d.Opportunities, o)
			}
		}
	}
	d.Owners = s.ownerOptions(r)
	return d, true
}

func (s *Server) handleLeadDetail(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadLeadDetail(r, r.PathValue("id"))
	if !ok {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	p := s.page(w, r, d.Company, "/leads")
	layouts.AppShell(s.Ren, webappIdentity(r), p, leads.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleLeadDrawer(w http.ResponseWriter, r *http.Request) {
	d, ok := s.loadLeadDetail(r, r.PathValue("id"))
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	p := s.page(w, r, d.Company, "/leads")
	leads.DrawerFragment(p, d).Render(r.Context(), w)
}

var validLeadStatus = map[string]bool{"new": true, "qualified": true, "hot": true, "contacted": true, "archived": true}

func (s *Server) handleLeadUpdate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	leadID := r.PathValue("id")
	status := strings.TrimSpace(r.FormValue("status"))
	owner := strings.TrimSpace(r.FormValue("owner_id"))
	if status != "" && !validLeadStatus[status] {
		webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Error, "Invalid status.")
		return
	}
	var ownerVal any
	if owner != "" {
		ownerVal = owner
	}
	res, err := s.PG.Exec(r.Context(), `
		UPDATE leads SET status=COALESCE(NULLIF($3,''), status), owner_id=$4::uuid,
			archived_at = CASE WHEN $3='archived' THEN now() WHEN $3<>'' THEN NULL ELSE archived_at END,
			last_activity_at=now(), updated_at=now()
		WHERE id=$1 AND tenant_id=$2`, leadID, id.TenantID, status, ownerVal)
	if err != nil || res.RowsAffected() == 0 {
		webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Error, "Could not update the lead.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO activities (tenant_id, kind, subject, lead_id, user_id)
		VALUES ($1,'lead.updated',$2,$3,$4)`, id.TenantID, "Lead updated", leadID, id.UserID)
	webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Success, "Lead updated.")
}

func (s *Server) handleLeadRefresh(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	leadID := r.PathValue("id")
	var exists bool
	_ = s.PG.QueryRow(r.Context(), `SELECT EXISTS(SELECT 1 FROM leads WHERE id=$1 AND tenant_id=$2)`, leadID, id.TenantID).Scan(&exists)
	if !exists {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	if s.Queue == nil {
		webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Error, "Queue unavailable.")
		return
	}
	if err := s.Queue.EnqueueLeadRefresh(r.Context(), leadID); err != nil {
		webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Error, "Could not queue the refresh.")
		return
	}
	webapp.RedirectFlash(w, r, "/leads/"+leadID, flash.Success, "Refresh queued — data updates shortly.")
}

func (s *Server) handleLeadBulk(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	if err := r.ParseForm(); err != nil {
		http.Redirect(w, r, "/leads", http.StatusSeeOther)
		return
	}
	ids := r.Form["ids"]
	redirect := r.FormValue("redirect")
	if redirect == "" {
		redirect = "/leads"
	}
	if len(ids) == 0 {
		webapp.RedirectFlash(w, r, redirect, flash.Error, "No leads selected.")
		return
	}
	if len(ids) > 500 {
		ids = ids[:500]
	}
	action := r.FormValue("action")
	listID := strings.TrimSpace(r.FormValue("list_id"))
	if action == "refresh" {
		if s.Queue == nil {
			webapp.RedirectFlash(w, r, redirect, flash.Error, "Queue unavailable.")
			return
		}
		if err := s.Queue.EnqueueLeadBulkRefresh(r.Context(), ids); err != nil {
			webapp.RedirectFlash(w, r, redirect, flash.Error, "Could not queue the refresh.")
			return
		}
		webapp.RedirectFlash(w, r, redirect, flash.Success, "Bulk refresh queued.")
		return
	}
	switch action {
	case "archive", "restore", "status_qualified", "status_contacted", "status_new":
		status := strings.TrimPrefix(action, "status_")
		if action == "archive" {
			status = "archived"
		}
		if action == "restore" {
			status = "new"
		}
		_, _ = s.PG.Exec(r.Context(), `
			UPDATE leads SET status=$3, archived_at = CASE WHEN $3='archived' THEN now() ELSE NULL END,
				last_activity_at=now(), updated_at=now()
			WHERE tenant_id=$1 AND id = ANY($2::uuid[])`, id.TenantID, ids, status)
		if listID != "" && id.Can("segment.manage") {
			_, _ = s.PG.Exec(r.Context(), `
				INSERT INTO list_leads (list_id, lead_id)
				SELECT $2::uuid, unnest($3::uuid[])
				WHERE EXISTS (SELECT 1 FROM lists WHERE id=$2::uuid AND tenant_id=$1)
				ON CONFLICT DO NOTHING`, id.TenantID, listID, ids)
		}
		webapp.RedirectFlash(w, r, redirect, flash.Success, "Bulk update applied.")
	default:
		webapp.RedirectFlash(w, r, redirect, flash.Error, "Unknown bulk action.")
	}
}

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else if r == '+' && b.Len() == 0 {
			// skip plus; wa.me wants bare digits
		}
	}
	return b.String()
}
