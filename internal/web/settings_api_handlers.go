package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/audit"
	"leadforge/internal/crypto"
	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/internal/webhookstore"
	"leadforge/web/layouts"
	"leadforge/web/pages/settings"
)

func (s *Server) settingsAPIKeysRoutes() {
	s.Router.HandleFunc("GET", "/settings/api-keys", s.requirePerm(role.IntegrationMange, func(w http.ResponseWriter, r *http.Request) {
		s.handleAPIKeys(w, r, "", "", "")
	}))
	s.Router.HandleFunc("POST", "/settings/api-keys", s.requirePerm(role.IntegrationMange, s.handleAPIKeyCreate))
	s.Router.HandleFunc("POST", "/settings/api-keys/{id}/revoke", s.requirePerm(role.IntegrationMange, s.handleAPIKeyRevoke))
	s.Router.HandleFunc("GET", "/settings/webhooks", s.requirePerm(role.IntegrationMange, func(w http.ResponseWriter, r *http.Request) {
		s.handleWebhooks(w, r)
	}))
	s.Router.HandleFunc("POST", "/settings/webhooks", s.requirePerm(role.IntegrationMange, s.handleWebhookCreate))
	s.Router.HandleFunc("POST", "/settings/webhooks/{id}/toggle", s.requirePerm(role.IntegrationMange, s.handleWebhookToggle))
	s.Router.HandleFunc("POST", "/settings/webhooks/{id}/delete", s.requirePerm(role.IntegrationMange, s.handleWebhookDelete))
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request, newKey, newName, errMsg string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, name, key_prefix, scopes, last_used_at, expires_at, created_at
		FROM api_keys WHERE tenant_id=$1 AND revoked_at IS NULL ORDER BY created_at DESC`, id.TenantID)
	d := &settings.APIKeysData{NewKey: newKey, NewName: newName, Error: errMsg}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var k settings.APIKeyRow
			var scopes []string
			var lastUsed, expires, created *time.Time
			if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &scopes, &lastUsed, &expires, &created); err == nil {
				k.Scopes = strings.Join(scopes, ", ")
				if lastUsed != nil {
					k.LastUsed = lastUsed.Format("02 Jan 15:04")
				} else {
					k.LastUsed = "never"
				}
				if expires != nil {
					k.Expires = expires.Format("02 Jan 2006")
				} else {
					k.Expires = "never"
				}
				if created != nil {
					k.Created = created.Format("02 Jan 2006")
				}
				d.Keys = append(d.Keys, k)
			}
		}
	}
	p := s.page(w, r, "API Keys", "/settings/api-keys")
	layouts.AppShell(s.Ren, id, p, settings.APIKeys(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAPIKeyCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.handleAPIKeys(w, r, "", "", "Name is required.")
		return
	}
	var scopes []string
	if r.FormValue("scope_read") != "" {
		scopes = append(scopes, "read")
	}
	if r.FormValue("scope_write") != "" {
		scopes = append(scopes, "write")
	}
	if len(scopes) == 0 {
		scopes = []string{"read"}
	}
	var expires any
	if days, err := strconv.Atoi(strings.TrimSpace(r.FormValue("expires_days"))); err == nil && days > 0 {
		t := time.Now().AddDate(0, 0, days)
		expires = t
	}
	raw := randomAPIKey()
	_, err := s.PG.Exec(r.Context(), `
		INSERT INTO api_keys (tenant_id, user_id, name, key_prefix, key_hash, scopes, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id.TenantID, id.UserID, name, raw[:12], hashAPIKey(raw), scopes, expires)
	if err != nil {
		s.handleAPIKeys(w, r, "", "", "Could not create the key.")
		return
	}
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "apikey.create", "api_key", name, audit.IP(r))
	s.handleAPIKeys(w, r, raw, name, "")
}

func (s *Server) handleAPIKeyRevoke(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `UPDATE api_keys SET revoked_at=now() WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "apikey.revoke", "api_key", r.PathValue("id"), audit.IP(r))
	http.Redirect(w, r, "/settings/api-keys", http.StatusSeeOther)
}

func (s *Server) handleWebhooks(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, url, events, is_active, to_char(created_at,'DD Mon YYYY')
		FROM webhooks WHERE tenant_id=$1 ORDER BY created_at DESC`, id.TenantID)
	d := &settings.WebhooksData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var h settings.WebhookRow
			var events []string
			if err := rows.Scan(&h.ID, &h.URL, &events, &h.Active, &h.Created); err == nil {
				h.Events = strings.Join(events, ", ")
				d.Hooks = append(d.Hooks, h)
			}
		}
	}
	drows, _ := s.PG.Query(r.Context(), `
		SELECT wd.event, wd.status, COALESCE(wd.response_code::text,''), to_char(wd.created_at,'DD Mon HH24:MI'), COALESCE(wd.error,'')
		FROM webhook_deliveries wd JOIN webhooks wh ON wh.id=wd.webhook_id
		WHERE wh.tenant_id=$1 ORDER BY wd.created_at DESC LIMIT 20`, id.TenantID)
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var dl settings.DeliveryRow
			if err := drows.Scan(&dl.Event, &dl.Status, &dl.Code, &dl.When, &dl.Error); err == nil {
				d.Deliveries = append(d.Deliveries, dl)
			}
		}
	}
	p := s.page(w, r, "Webhooks", "/settings/webhooks")
	layouts.AppShell(s.Ren, id, p, settings.Webhooks(p, d)).Render(r.Context(), w)
}

func (s *Server) handleWebhookCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	target := strings.TrimSpace(r.FormValue("url"))
	if target == "" || (!strings.HasPrefix(target, "https://") && !(s.Cfg.AllowInsecureWebhooks && strings.HasPrefix(target, "http://"))) {
		s.handleWebhooks(w, r, "A valid HTTPS URL is required.")
		return
	}
	var events []string
	if r.FormValue("ev_search") != "" {
		events = append(events, "search.completed")
	}
	if r.FormValue("ev_qualified") != "" {
		events = append(events, "lead.qualified")
	}
	if r.FormValue("ev_campaign") != "" {
		events = append(events, "campaign.completed")
	}
	if len(events) == 0 {
		s.handleWebhooks(w, r, "Select at least one event.")
		return
	}
	secret := strings.TrimSpace(r.FormValue("secret"))
	if secret == "" {
		secret = webhookstore.RandomSecret()
	}
	enc, err := crypto.Encrypt(s.Cfg.EncryptionSecret, secret)
	if err != nil {
		s.handleWebhooks(w, r, "Could not store the secret.")
		return
	}
	_, err = s.PG.Exec(r.Context(), `INSERT INTO webhooks (tenant_id, url, events, secret_enc) VALUES ($1,$2,$3,$4)`,
		id.TenantID, target, events, []byte(enc))
	if err != nil {
		s.handleWebhooks(w, r, "Could not save the webhook.")
		return
	}
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "webhook.create", "webhook", target, audit.IP(r))
	webapp.RedirectFlash(w, r, "/settings/webhooks", flash.Success, "Webhook created.")
}

func (s *Server) handleWebhookToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `UPDATE webhooks SET is_active = NOT is_active WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/settings/webhooks", http.StatusSeeOther)
}

func (s *Server) handleWebhookDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM webhooks WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "webhook.delete", "webhook", r.PathValue("id"), audit.IP(r))
	http.Redirect(w, r, "/settings/webhooks", http.StatusSeeOther)
}
