package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/scoring"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	pageICP "leadforge/web/pages/icp"
	pageScoring "leadforge/web/pages/scoring"
)

func (s *Server) intelRoutes() {
	s.Router.HandleFunc("GET", "/scoring", s.requirePerm(role.LeadRead, s.handleScoring))
	s.Router.HandleFunc("POST", "/scoring/rules", s.requirePerm(role.LeadUpdate, s.handleScoringCreate))
	s.Router.HandleFunc("POST", "/scoring/rules/{id}/toggle", s.requirePerm(role.LeadUpdate, s.handleScoringToggle))
	s.Router.HandleFunc("POST", "/scoring/rules/{id}/delete", s.requirePerm(role.LeadUpdate, s.handleScoringDelete))
	s.Router.HandleFunc("GET", "/icp", s.requirePerm(role.LeadRead, s.handleICP))
	s.Router.HandleFunc("POST", "/icp", s.requirePerm(role.LeadUpdate, s.handleICPUpsert))
	s.Router.HandleFunc("POST", "/icp/{id}/delete", s.requirePerm(role.LeadUpdate, s.handleICPDelete))
	s.Router.HandleFunc("GET", "/segments", s.requirePerm(role.SegmentManage, func(w http.ResponseWriter, r *http.Request) {
		s.handleSegmentList(w, r)
	}))
	s.Router.HandleFunc("POST", "/segments", s.requirePerm(role.SegmentManage, s.handleSegmentCreate))
	s.Router.HandleFunc("GET", "/segments/{id}", s.requirePerm(role.SegmentManage, s.handleSegmentDetail))
	s.Router.HandleFunc("POST", "/segments/{id}/refresh", s.requirePerm(role.SegmentManage, s.handleSegmentRefresh))
	s.Router.HandleFunc("POST", "/segments/{id}/delete", s.requirePerm(role.SegmentManage, s.handleSegmentDelete))
	s.Router.HandleFunc("GET", "/lists", s.requirePerm(role.SegmentManage, func(w http.ResponseWriter, r *http.Request) {
		s.handleLists(w, r)
	}))
	s.Router.HandleFunc("POST", "/lists", s.requirePerm(role.SegmentManage, s.handleListCreate))
	s.Router.HandleFunc("GET", "/lists/{id}", s.requirePerm(role.SegmentManage, s.handleListDetail))
	s.Router.HandleFunc("POST", "/lists/{id}/remove", s.requirePerm(role.SegmentManage, s.handleListRemove))
	s.Router.HandleFunc("POST", "/lists/{id}/delete", s.requirePerm(role.SegmentManage, s.handleListDelete))
	s.Router.HandleFunc("GET", "/enrichment", s.requirePerm(role.LeadRead, s.handleEnrichment))
}

// ---- scoring ----

func (s *Server) handleScoring(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &pageScoring.PageData{}
	for _, dr := range scoring.DefaultRules() {
		d.Defaults = append(d.Defaults, pageScoring.Rule{Name: dr.Name, Signal: dr.Signal, Operator: dr.Operator, Weight: dr.Weight, Builtin: true})
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, name, signal, operator, value, weight, is_active
		FROM scoring_rules WHERE tenant_id=$1 ORDER BY position, created_at`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var rr pageScoring.Rule
			if err := rows.Scan(&rr.ID, &rr.Name, &rr.Signal, &rr.Operator, &rr.Value, &rr.Weight, &rr.Active); err == nil {
				d.Custom = append(d.Custom, rr)
			}
		}
	}
	p := s.page(w, r, "Lead Scoring", "/scoring")
	layouts.AppShell(s.Ren, id, p, pageScoring.Page(p, d)).Render(r.Context(), w)
}

var validRuleOp = map[string]bool{"exists": true, "equals": true, "contains": true, "gte": true, "lte": true, "in": true}

func (s *Server) handleScoringCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	signal := strings.TrimSpace(r.FormValue("signal"))
	op := strings.TrimSpace(r.FormValue("operator"))
	val := strings.TrimSpace(r.FormValue("value"))
	weight, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("weight")))
	if name == "" || signal == "" || !validRuleOp[op] {
		webapp.RedirectFlash(w, r, "/scoring", flash.Error, "Name, signal and a valid operator are required.")
		return
	}
	if weight < -100 || weight > 100 {
		webapp.RedirectFlash(w, r, "/scoring", flash.Error, "Points must be between -100 and 100.")
		return
	}
	_, err := s.PG.Exec(r.Context(), `
		INSERT INTO scoring_rules (tenant_id, name, signal, operator, value, weight, position)
		SELECT $1,$2,$3,$4,$5,$6,COALESCE(MAX(position)+1,0) FROM scoring_rules WHERE tenant_id=$1`,
		id.TenantID, name, signal, op, val, weight)
	if err != nil {
		webapp.RedirectFlash(w, r, "/scoring", flash.Error, "Could not save the rule.")
		return
	}
	webapp.RedirectFlash(w, r, "/scoring", flash.Success, "Rule added. It applies to newly scored leads.")
}

func (s *Server) handleScoringToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `UPDATE scoring_rules SET is_active = NOT is_active WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/scoring", http.StatusSeeOther)
}

func (s *Server) handleScoringDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM scoring_rules WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/scoring", http.StatusSeeOther)
}

// ---- ICP ----

func (s *Server) handleICP(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &pageICP.PageData{}
	rows, _ := s.PG.Query(r.Context(), `SELECT id::text, name, criteria, is_default, created_at FROM icps WHERE tenant_id=$1 ORDER BY created_at`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var it pageICP.Item
			var raw []byte
			var created time.Time
			var isDef bool
			if err := rows.Scan(&it.ID, &it.Name, &raw, &isDef, &created); err == nil {
				it.IsDefault = isDef
				it.Summary = icpSummary(raw)
				d.Items = append(d.Items, it)
			}
		}
	}
	if editID := r.URL.Query().Get("edit"); editID != "" {
		var raw []byte
		var name string
		var isDef bool
		if err := s.PG.QueryRow(r.Context(), `SELECT name, criteria, is_default FROM icps WHERE id=$1 AND tenant_id=$2`, editID, id.TenantID).Scan(&name, &raw, &isDef); err == nil {
			d.Form.ID, d.Form.Name, d.Form.IsDefault = editID, name, isDef
			var c map[string]any
			if json.Unmarshal(raw, &c) == nil {
				d.Form.Industry = strVal(c, "industry")
				d.Form.Country = strVal(c, "country")
				d.Form.Province = strVal(c, "province")
				d.Form.City = strVal(c, "city")
				d.Form.Employee = strVal(c, "employee_range")
				d.Form.Keywords = strVal(c, "keywords")
				if f, ok := c["min_score"].(float64); ok {
					d.Form.MinScore = int(f)
				}
			}
		}
	}
	p := s.page(w, r, "ICP", "/icp")
	layouts.AppShell(s.Ren, id, p, pageICP.Page(p, d)).Render(r.Context(), w)
}

func (s *Server) handleICPUpsert(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	upid := strings.TrimSpace(r.FormValue("id"))
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		webapp.RedirectFlash(w, r, "/icp", flash.Error, "Name is required.")
		return
	}
	minScore, _ := strconv.Atoi(r.FormValue("min_score"))
	crit, _ := json.Marshal(map[string]any{
		"industry":       strings.TrimSpace(r.FormValue("industry")),
		"country":        strings.TrimSpace(r.FormValue("country")),
		"province":       strings.TrimSpace(r.FormValue("province")),
		"city":           strings.TrimSpace(r.FormValue("city")),
		"employee_range": strings.TrimSpace(r.FormValue("employee_range")),
		"keywords":       strings.TrimSpace(r.FormValue("keywords")),
		"min_score":      minScore,
	})
	isDef := r.FormValue("is_default") != ""
	tx, err := s.PG.Begin(r.Context())
	if err != nil {
		webapp.RedirectFlash(w, r, "/icp", flash.Error, "Could not save the ICP.")
		return
	}
	defer tx.Rollback(r.Context())
	if isDef {
		_, _ = tx.Exec(r.Context(), `UPDATE icps SET is_default=false WHERE tenant_id=$1`, id.TenantID)
	}
	if upid == "" {
		_, err = tx.Exec(r.Context(), `INSERT INTO icps (tenant_id, name, criteria, is_default) VALUES ($1,$2,$3,$4)`,
			id.TenantID, name, string(crit), isDef)
	} else {
		_, err = tx.Exec(r.Context(), `UPDATE icps SET name=$3, criteria=$4, is_default=$5 WHERE id=$1 AND tenant_id=$2`,
			upid, id.TenantID, name, string(crit), isDef)
	}
	if err != nil {
		webapp.RedirectFlash(w, r, "/icp", flash.Error, "Could not save the ICP.")
		return
	}
	_ = tx.Commit(r.Context())
	webapp.RedirectFlash(w, r, "/icp", flash.Success, "ICP saved.")
}

func (s *Server) handleICPDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM icps WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/icp", http.StatusSeeOther)
}

func icpSummary(raw []byte) string {
	var c map[string]any
	if json.Unmarshal(raw, &c) != nil {
		return ""
	}
	var parts []string
	for _, k := range []string{"industry", "city", "province", "country", "employee_range", "keywords"} {
		if v := strVal(c, k); v != "" {
			parts = append(parts, v)
		}
	}
	if f, ok := c["min_score"].(float64); ok && f > 0 {
		parts = append(parts, strconv.Itoa(int(f))+"+")
	}
	return strings.Join(parts, " · ")
}

func strVal(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}
