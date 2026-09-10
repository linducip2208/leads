package web

import (
	"net/http"
	"strings"

	"leadforge/internal/outreach"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/campaigns"
)

// ---- templates ----

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, name, subject, to_char(created_at,'DD Mon YYYY') FROM email_templates
		WHERE tenant_id=$1 ORDER BY created_at DESC`, id.TenantID)
	d := &campaigns.TemplatesData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var t campaigns.TemplateItem
			if err := rows.Scan(&t.ID, &t.Name, &t.Subject, &t.Created); err == nil {
				d.Items = append(d.Items, t)
			}
		}
	}
	p := s.page(w, r, "Templates", "/templates")
	layouts.AppShell(s.Ren, id, p, campaigns.Templates(p, d)).Render(r.Context(), w)
}

func (s *Server) handleTemplateCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	subject := strings.TrimSpace(r.FormValue("subject"))
	body := r.FormValue("body")
	if name == "" || subject == "" {
		s.handleTemplates(w, r, "Name and subject are required.")
		return
	}
	_, err := s.PG.Exec(r.Context(), `INSERT INTO email_templates (tenant_id, name, subject, body) VALUES ($1,$2,$3,$4)`,
		id.TenantID, name, subject, body)
	if err != nil {
		s.handleTemplates(w, r, "Could not save the template.")
		return
	}
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

func (s *Server) handleTemplateDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM email_templates WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/templates", http.StatusSeeOther)
}

// ---- public unsubscribe ----

func (s *Server) handleUnsubPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	contactID, tenantID := s.parseUnsub(token)
	if contactID == "" {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var email string
	_ = s.PG.QueryRow(r.Context(), `SELECT email FROM contacts WHERE id=$1::uuid AND tenant_id=$2`, contactID, tenantID).Scan(&email)
	layouts.UnsubPage(s.Ren, token, email).Render(r.Context(), w)
}

func (s *Server) handleUnsubSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	contactID, tenantID := s.parseUnsub(token)
	if contactID == "" {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var email string
	_ = s.PG.QueryRow(r.Context(), `SELECT email FROM contacts WHERE id=$1::uuid AND tenant_id=$2`, contactID, tenantID).Scan(&email)
	if email != "" {
		_, _ = s.PG.Exec(r.Context(), `INSERT INTO suppression_list (tenant_id, email, reason) VALUES ($1,$2,'unsubscribe') ON CONFLICT DO NOTHING`, tenantID, email)
		_, _ = s.PG.Exec(r.Context(), `UPDATE campaign_contacts SET status='unsubscribed' WHERE contact_id=$1::uuid AND status IN ('pending','active','paused')`, contactID)
	}
	layouts.UnsubDone(s.Ren).Render(r.Context(), w)
}

// parseUnsub validates token format tenant.contact.sig (tenant embedded to scope).
func (s *Server) parseUnsub(token string) (contactID, tenantID string) {
	// token = tenantID.contactID.sig
	first := strings.Index(token, ".")
	last := strings.LastIndex(token, ".")
	if first <= 0 || last <= first {
		return "", ""
	}
	tenantID = token[:first]
	rest := token[first+1:]
	cid, ok := outreach.VerifyUnsubToken(s.Cfg.Secret, tenantID, rest)
	if !ok {
		return "", ""
	}
	return cid, tenantID
}

// UnsubURLFor builds the full unsubscribe URL for a contact.
func (s *Server) UnsubURLFor(tenantID, contactID string) string {
	return strings.TrimRight(s.Cfg.AppURL, "/") + "/u/" + tenantID + "." + outreach.UnsubToken(s.Cfg.Secret, tenantID, contactID)
}
