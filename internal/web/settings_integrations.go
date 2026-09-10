package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"leadforge/internal/crypto"
	"leadforge/internal/flash"
	"leadforge/internal/source"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/settings"
)

// slug → lead_sources.type mapping.
func sourceTypeFor(slug string) string {
	switch slug {
	case "google_places", "manual", "csv":
		return slug
	case "website_search":
		return "website_search"
	case "public_directory":
		return "public_directory"
	case "custom_api":
		return "custom_api"
	default:
		return "api"
	}
}

func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	// tenant prefs
	type pref struct {
		active bool
		hasKey bool
	}
	prefs := map[string]*pref{}
	rows, _ := s.PG.Query(r.Context(), `SELECT slug, is_active, config FROM lead_sources WHERE tenant_id=$1`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var slug string
			var active bool
			var cfg []byte
			if err := rows.Scan(&slug, &active, &cfg); err == nil {
				p := &pref{active: active}
				var m map[string]string
				if json.Unmarshal(cfg, &m) == nil {
					if _, ok := m["api_key_enc"]; ok {
						p.hasKey = true
					} else if k, ok := m["api_key"]; ok && k != "" {
						p.hasKey = true
					}
				}
				prefs[slug] = p
			}
		}
	}
	// last errors per source (7d, tenant)
	errs := map[string]string{}
	erows, _ := s.PG.Query(r.Context(), `
		SELECT source_slug, last_error FROM search_source_stats sss
		JOIN lead_searches ls ON ls.id = sss.search_id
		WHERE ls.tenant_id=$1 AND sss.errors > 0 AND ls.created_at > now() - interval '7 days'
		ORDER BY ls.created_at DESC`, id.TenantID)
	if erows != nil {
		defer erows.Close()
		for erows.Next() {
			var slug, msg string
			if err := erows.Scan(&slug, &msg); err == nil {
				if _, ok := errs[slug]; !ok {
					errs[slug] = msg
				}
			}
		}
	}
	reg := source.DefaultRegistry("")
	d := &settings.IntegrationsData{}
	for _, info := range reg.Infos() {
		if info.Slug == "mock" {
			continue
		}
		row := settings.SourceRow{
			Slug: info.Slug, Name: info.Name, Description: info.Description,
			Priority: info.Priority, Confidence: info.Confidence,
			NeedsKey: info.RequiresCredential, Enabled: true,
		}
		if p, ok := prefs[info.Slug]; ok {
			row.Enabled = p.active
			row.Configured = p.hasKey
		}
		// google also honors the global env key
		if info.Slug == "google_places" && s.Cfg.HasGoogleKey {
			row.Configured = true
		}
		switch {
		case !row.Enabled:
			row.Status = "Disabled"
		case info.RequiresCredential && !row.Configured:
			row.Status = "Not Configured"
		case row.Configured || !info.RequiresCredential:
			row.Status = "Configured"
		}
		if msg, ok := errs[info.Slug]; ok && row.Enabled {
			row.Status = "Error"
			row.LastError = msg
		}
		d.Sources = append(d.Sources, row)
	}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	p := s.page(w, r, "Integrations", "/settings/integrations")
	layouts.AppShell(s.Ren, id, p, settings.Integrations(p, d)).Render(r.Context(), w)
}

func (s *Server) handleIntegrationToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	slug := r.PathValue("slug")
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO lead_sources (tenant_id, slug, name, type, is_active)
		VALUES ($1,$2,$2,$3, false)
		ON CONFLICT (tenant_id, slug) DO UPDATE SET is_active = NOT lead_sources.is_active`,
		id.TenantID, slug, sourceTypeFor(slug))
	// NOTE: first toggle from default-enabled creates a DISABLED row; toggling
	// again re-enables. Deterministic from the visible state.
	http.Redirect(w, r, "/settings/integrations", http.StatusSeeOther)
}

func (s *Server) handleGoogleKeySave(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	key := strings.TrimSpace(r.FormValue("api_key"))
	if key == "" {
		s.handleIntegrations(w, r, "Paste an API key first.")
		return
	}
	enc, err := crypto.Encrypt(s.Cfg.Secret, key)
	if err != nil {
		s.handleIntegrations(w, r, "Could not encrypt the key.")
		return
	}
	cfg, _ := json.Marshal(map[string]string{"api_key_enc": enc})
	_, err = s.PG.Exec(r.Context(), `
		INSERT INTO lead_sources (tenant_id, slug, name, type, config, is_active)
		VALUES ($1,'google_places','Google Places','google_places',$2,true)
		ON CONFLICT (tenant_id, slug) DO UPDATE SET config=$2, is_active=true`, id.TenantID, string(cfg))
	if err != nil {
		s.handleIntegrations(w, r, "Could not save the key.")
		return
	}
	webapp.RedirectFlash(w, r, "/settings/integrations", flash.Success, "Google Places enabled.")
}

func (s *Server) handleGoogleKeyClear(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM lead_sources WHERE tenant_id=$1 AND slug='google_places'`, id.TenantID)
	http.Redirect(w, r, "/settings/integrations", http.StatusSeeOther)
}
