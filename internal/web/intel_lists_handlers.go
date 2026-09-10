package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	pageEnrich "leadforge/web/pages/enrichment"
	pageLists "leadforge/web/pages/lists"
	pageSegments "leadforge/web/pages/segments"
)

// segFilter is the stored segment filter shape.
type segFilter struct {
	MinScore    int    `json:"min_score"`
	Industry    string `json:"industry"`
	City        string `json:"city"`
	HasWhatsApp bool   `json:"has_whatsapp"`
	NoWebsite   bool   `json:"no_website"`
}

// segWhere builds member-match SQL (args start at $2; $1 is tenant).
func segWhere(f segFilter) (string, []any) {
	w := "l.tenant_id = $1 AND l.status <> 'archived'"
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		w += " AND " + cond
	}
	ph := func() string { return "$" + strconv.Itoa(len(args)+1) }
	if f.MinScore > 0 {
		add("l.lead_score >= "+ph(), f.MinScore)
	}
	if f.Industry != "" {
		add("c.industry ILIKE "+ph(), "%"+f.Industry+"%")
	}
	if f.City != "" {
		args = append(args, "%"+f.City+"%", "%"+f.City+"%")
		n := len(args)
		w += " AND (c.city ILIKE $" + strconv.Itoa(n-1) + " OR c.province ILIKE $" + strconv.Itoa(n) + ")"
	}
	if f.HasWhatsApp {
		w += " AND NULLIF(c.whatsapp,'') IS NOT NULL"
	}
	if f.NoWebsite {
		w += " AND NULLIF(c.website,'') IS NULL"
	}
	return w, args
}

func segSummary(f segFilter) string {
	var parts []string
	if f.Industry != "" {
		parts = append(parts, f.Industry)
	}
	if f.City != "" {
		parts = append(parts, f.City)
	}
	if f.MinScore > 0 {
		parts = append(parts, "score "+strconv.Itoa(f.MinScore)+"+")
	}
	if f.HasWhatsApp {
		parts = append(parts, "has WhatsApp")
	}
	if f.NoWebsite {
		parts = append(parts, "no website")
	}
	if len(parts) == 0 {
		return "all leads"
	}
	return strings.Join(parts, " · ")
}

func (s *Server) handleSegmentList(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT sg.id::text, sg.name, sg.filters, sg.created_at,
			(SELECT COUNT(*) FROM segment_members sm WHERE sm.segment_id = sg.id)
		FROM segments sg WHERE sg.tenant_id=$1 ORDER BY sg.created_at DESC`, id.TenantID)
	d := &pageSegments.PageData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var it pageSegments.Item
			var raw []byte
			var created time.Time
			if err := rows.Scan(&it.ID, &it.Name, &raw, &created, &it.Members); err == nil {
				var f segFilter
				_ = json.Unmarshal(raw, &f)
				it.Summary = segSummary(f)
				it.Dynamic = true
				d.Items = append(d.Items, it)
			}
		}
	}
	p := s.page(w, r, "Segments", "/segments")
	layouts.AppShell(s.Ren, id, p, pageSegments.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleSegmentCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.handleSegmentList(w, r, "Name is required.")
		return
	}
	minScore, _ := strconv.Atoi(r.FormValue("min_score"))
	f := segFilter{
		MinScore: minScore, Industry: strings.TrimSpace(r.FormValue("industry")),
		City:        strings.TrimSpace(r.FormValue("city")),
		HasWhatsApp: r.FormValue("has_whatsapp") != "",
		NoWebsite:   r.FormValue("no_website") != "",
	}
	fraw, _ := json.Marshal(f)
	var segID string
	if err := s.PG.QueryRow(r.Context(), `
		INSERT INTO segments (tenant_id, name, filters, is_dynamic, created_by)
		VALUES ($1,$2,$3,true,$4) RETURNING id::text`, id.TenantID, name, string(fraw), id.UserID).Scan(&segID); err != nil {
		s.handleSegmentList(w, r, "Could not create the segment.")
		return
	}
	s.refreshSegment(r, id.TenantID, segID, f)
	http.Redirect(w, r, "/segments/"+segID, http.StatusSeeOther)
}

// refreshSegment recomputes membership for a dynamic segment.
func (s *Server) refreshSegment(r *http.Request, tenantID, segID string, f segFilter) int {
	where, args := segWhere(f)
	full := append([]any{tenantID, segID}, args...)
	// shift placeholders in where ($1 tenant stays; member args start at $3)
	shifted := shiftPlaceholders(where, 1)
	var n int
	_ = s.PG.QueryRow(r.Context(), `
		WITH matched AS (
			SELECT l.id FROM leads l JOIN companies c ON c.id = l.company_id
			WHERE `+shifted+`
		), del AS (DELETE FROM segment_members WHERE segment_id=$2::uuid)
		INSERT INTO segment_members (segment_id, lead_id)
		SELECT $2::uuid, id FROM matched ON CONFLICT DO NOTHING RETURNING 1`, full...).Scan(&n)
	// count actual members
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM segment_members WHERE segment_id=$1::uuid`, segID).Scan(&n)
	return n
}

// shiftPlaceholders bumps $2..$N by one (room for segment id at $2).
func shiftPlaceholders(whr string, by int) string {
	// placeholders are $1..$9 here; rewrite from high to low to avoid collisions
	for i := 9; i >= 2; i-- {
		whr = strings.ReplaceAll(whr, "$"+strconv.Itoa(i), "$"+strconv.Itoa(i+by))
	}
	return whr
}

func (s *Server) handleSegmentDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	segID := r.PathValue("id")
	var name string
	var raw []byte
	if err := s.PG.QueryRow(r.Context(), `SELECT name, filters FROM segments WHERE id=$1::uuid AND tenant_id=$2`,
		segID, id.TenantID).Scan(&name, &raw); err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var f segFilter
	_ = json.Unmarshal(raw, &f)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT l.id::text, c.name, COALESCE(c.city,''), l.lead_score, l.status
		FROM segment_members sm JOIN leads l ON l.id = sm.lead_id JOIN companies c ON c.id = l.company_id
		WHERE sm.segment_id=$1::uuid ORDER BY l.lead_score DESC LIMIT 200`, segID)
	d := &pageSegments.DetailData{}
	d.Segment.ID, d.Segment.Name, d.Segment.Summary = segID, name, segSummary(f)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m pageSegments.MemberRow
			if err := rows.Scan(&m.LeadID, &m.Company, &m.City, &m.Score, &m.Status); err == nil {
				d.Members = append(d.Members, m)
			}
		}
	}
	d.Segment.Members = len(d.Members)
	p := s.page(w, r, name, "/segments")
	layouts.AppShell(s.Ren, id, p, pageSegments.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleSegmentRefresh(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	segID := r.PathValue("id")
	var raw []byte
	if err := s.PG.QueryRow(r.Context(), `SELECT filters FROM segments WHERE id=$1::uuid AND tenant_id=$2`,
		segID, id.TenantID).Scan(&raw); err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var f segFilter
	_ = json.Unmarshal(raw, &f)
	n := s.refreshSegment(r, id.TenantID, segID, f)
	webapp.RedirectFlash(w, r, "/segments/"+segID, flash.Success, "Segment refreshed ("+strconv.Itoa(n)+" members).")
}

func (s *Server) handleSegmentDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM segments WHERE id=$1::uuid AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/segments", http.StatusSeeOther)
}

// ---- lists ----

func (s *Server) handleLists(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT l.id::text, l.name, l.description, l.created_at,
			(SELECT COUNT(*) FROM list_leads ll WHERE ll.list_id = l.id)
		FROM lists l WHERE l.tenant_id=$1 ORDER BY l.created_at DESC`, id.TenantID)
	d := &pageLists.PageData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var it pageLists.Item
			var created time.Time
			if err := rows.Scan(&it.ID, &it.Name, &it.Desc, &created, &it.Members); err == nil {
				it.Created = created.Format("02 Jan 2006")
				d.Items = append(d.Items, it)
			}
		}
	}
	p := s.page(w, r, "Lists", "/lists")
	layouts.AppShell(s.Ren, id, p, pageLists.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleListCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.handleLists(w, r, "Name is required.")
		return
	}
	var listID string
	if err := s.PG.QueryRow(r.Context(), `
		INSERT INTO lists (tenant_id, name, description, created_by)
		VALUES ($1,$2,$3,$4) RETURNING id::text`,
		id.TenantID, name, strings.TrimSpace(r.FormValue("description")), id.UserID).Scan(&listID); err != nil {
		s.handleLists(w, r, "Could not create the list.")
		return
	}
	http.Redirect(w, r, "/lists/"+listID, http.StatusSeeOther)
}

func (s *Server) handleListDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	listID := r.PathValue("id")
	var name, desc string
	if err := s.PG.QueryRow(r.Context(), `SELECT name, description FROM lists WHERE id=$1::uuid AND tenant_id=$2`,
		listID, id.TenantID).Scan(&name, &desc); err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT l.id::text, c.name, COALESCE(c.city,''), l.lead_score, l.status
		FROM list_leads ll JOIN leads l ON l.id = ll.lead_id JOIN companies c ON c.id = l.company_id
		WHERE ll.list_id=$1::uuid AND l.tenant_id=$2 ORDER BY l.lead_score DESC LIMIT 500`, listID, id.TenantID)
	d := &pageLists.DetailData{}
	d.List.ID, d.List.Name, d.List.Desc = listID, name, desc
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m pageLists.MemberRow
			if err := rows.Scan(&m.LeadID, &m.Company, &m.City, &m.Score, &m.Status); err == nil {
				d.Members = append(d.Members, m)
			}
		}
	}
	d.List.Members = len(d.Members)
	p := s.page(w, r, name, "/lists")
	layouts.AppShell(s.Ren, id, p, pageLists.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleListRemove(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	listID := r.PathValue("id")
	leadID := strings.TrimSpace(r.FormValue("lead_id"))
	_, _ = s.PG.Exec(r.Context(), `
		DELETE FROM list_leads WHERE list_id=$1::uuid AND lead_id=$2::uuid
		AND EXISTS (SELECT 1 FROM lists WHERE id=$1::uuid AND tenant_id=$3)`, listID, leadID, id.TenantID)
	http.Redirect(w, r, "/lists/"+listID, http.StatusSeeOther)
}

func (s *Server) handleListDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM lists WHERE id=$1::uuid AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/lists", http.StatusSeeOther)
}

// ---- enrichment ----

func (s *Server) handleEnrichment(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	d := &pageEnrich.PageData{}
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM lead_enrichments WHERE tenant_id=$1`, id.TenantID).Scan(statVal(d, "Total runs"))
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM lead_enrichments WHERE tenant_id=$1 AND status='completed'`, id.TenantID).Scan(statVal(d, "Completed"))
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM lead_enrichments WHERE tenant_id=$1 AND status='failed'`, id.TenantID).Scan(statVal(d, "Failed"))
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(DISTINCT company_id) FROM lead_enrichments WHERE tenant_id=$1`, id.TenantID).Scan(statVal(d, "Companies"))
	rows, _ := s.PG.Query(r.Context(), `
		SELECT e.kind, e.status, e.data, to_char(e.finished_at, 'DD Mon HH24:MI'),
			c.name, e.company_id::text, to_char(e.created_at,'DD Mon HH24:MI')
		FROM lead_enrichments e JOIN companies c ON c.id = e.company_id
		WHERE e.tenant_id=$1 ORDER BY e.created_at DESC LIMIT 100`, id.TenantID)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var run pageEnrich.Run
			var data []byte
			var finished *string
			var when string
			if err := rows.Scan(&run.Kind, &run.Status, &data, &finished, &run.Company, &run.CompanyID, &when); err == nil {
				if finished != nil {
					run.When = *finished
				} else {
					run.When = when
				}
				var m map[string]any
				if json.Unmarshal(data, &m) == nil {
					if c, ok := m["confidence"].(float64); ok {
						run.Confidence = int(c)
					}
				}
				d.Runs = append(d.Runs, run)
			}
		}
	}
	p := s.page(w, r, "Enrichment", "/enrichment")
	layouts.AppShell(s.Ren, id, p, pageEnrich.Page(p, d)).Render(r.Context(), w)
}

// statVal appends a named stat and returns a scan target for its value.
func statVal(d *pageEnrich.PageData, label string) *int {
	d.Stats = append(d.Stats, pageEnrich.Stat{Label: label})
	return &d.Stats[len(d.Stats)-1].Value
}
