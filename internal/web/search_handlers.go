package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/search"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/searches"
)

func (s *Server) searchRoutes() {
	s.Router.HandleFunc("GET", "/searches", s.requirePerm(role.LeadRead, s.handleSearchList))
	s.Router.HandleFunc("GET", "/searches/saved", s.requirePerm(role.LeadRead, s.handleSavedList))
	s.Router.HandleFunc("GET", "/searches/{id}", s.requirePerm(role.LeadRead, s.handleSearchDetail))
	s.Router.HandleFunc("GET", "/searches/{id}/events", s.requirePerm(role.LeadRead, s.handleSearchEvents))
	s.Router.HandleFunc("GET", "/searches/{id}/latest", s.requirePerm(role.LeadRead, s.handleSearchLatest))
	s.Router.HandleFunc("POST", "/searches/{id}/pause", s.requirePerm(role.SearchCreate, s.handleSearchPause))
	s.Router.HandleFunc("POST", "/searches/{id}/resume", s.requirePerm(role.SearchCreate, s.handleSearchResume))
	s.Router.HandleFunc("POST", "/searches/{id}/cancel", s.requirePerm(role.SearchCreate, s.handleSearchCancel))
	s.Router.HandleFunc("POST", "/searches/{id}/save", s.requirePerm(role.SearchCreate, s.handleSearchSave))
	s.Router.HandleFunc("POST", "/saved-searches/{id}/run", s.requirePerm(role.SearchCreate, s.handleSavedRun))
	s.Router.HandleFunc("POST", "/saved-searches/{id}/delete", s.requirePerm(role.SearchCreate, s.handleSavedDelete))
}

func (s *Server) handleSearchList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT s.id::text, COALESCE(NULLIF(s.keyword,''), s.query), COALESCE(s.industry,''),
			COALESCE(s.location,''), s.status, s.limit_count, s.found_count, s.saved_count,
			s.qualified_count, s.created_at, s.started_at, s.finished_at, COALESCE(u.name,'')
		FROM lead_searches s LEFT JOIN users u ON u.id = s.user_id
		WHERE s.tenant_id=$1 ORDER BY s.created_at DESC LIMIT 100`, id.TenantID)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load searches.", Err: err})
		return
	}
	defer rows.Close()
	d := &searches.ListData{}
	for rows.Next() {
		var it searches.Item
		var created time.Time
		var started, finished *time.Time
		var requested, found, saved, qualified int
		if err := rows.Scan(&it.ID, &it.Query, &it.Industry, &it.Location, &it.Status,
			&requested, &found, &saved, &qualified, &created, &started, &finished, &it.CreatedBy); err == nil {
			it.Requested, it.Found, it.Unique, it.Qualified = requested, found, saved, qualified
			it.Created = created.Format("02 Jan 15:04")
			it.Duration = formatDuration(started, finished, created)
			d.Items = append(d.Items, it)
		}
	}
	p := s.page(w, r, "Search History", "/searches")
	layouts.AppShell(s.Ren, id, p, searches.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleSearchDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	sid := r.PathValue("id")
	var it searches.Item
	var created time.Time
	var started, finished *time.Time
	var requested int
	var filtersRaw []byte
	err := s.PG.QueryRow(r.Context(), `
		SELECT s.id::text, COALESCE(NULLIF(s.keyword,''), s.query), COALESCE(s.industry,''),
			COALESCE(s.location,''), s.status, s.limit_count, s.created_at, s.started_at, s.finished_at,
			COALESCE(u.name,''), s.filters
		FROM lead_searches s LEFT JOIN users u ON u.id = s.user_id
		WHERE s.id=$1 AND s.tenant_id=$2`, sid, id.TenantID).
		Scan(&it.ID, &it.Query, &it.Industry, &it.Location, &it.Status, &requested,
			&created, &started, &finished, &it.CreatedBy, &filtersRaw)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	it.Requested = requested
	it.Created = created.Format("02 Jan 2006 15:04")
	it.Duration = formatDuration(started, finished, created)
	prog, err := search.Snapshot(r.Context(), s.PG, id.TenantID, sid)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	it.Found, it.Unique, it.Qualified = int(prog.Found), int(prog.Saved), int(prog.Qualified)
	minScore, _ := strconv.Atoi(r.URL.Query().Get("min_score"))
	if minScore == 0 {
		var f search.Filters
		if json.Unmarshal(filtersRaw, &f) == nil {
			minScore = f.MinScore
		}
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT l.id::text, c.name, COALESCE(c.city,''), l.lead_score, l.status
		FROM leads l JOIN companies c ON c.id = l.company_id
		WHERE l.tenant_id=$1 AND l.source_search_id=$2 AND l.lead_score >= $3 AND l.status <> 'archived'
		ORDER BY l.lead_score DESC LIMIT 50`, id.TenantID, sid, minScore)
	d := &searches.DetailData{Search: it, Progress: prog, MinScore: minScore}
	d.Funnel = []searches.FunnelStep{
		{Label: "Discovered", Value: int(prog.Discovered)},
		{Label: "Unique", Value: int(prog.Saved + prog.Matched)},
		{Label: "Crawled", Value: int(prog.Crawled)},
		{Label: "Enriched", Value: int(prog.Enriched)},
		{Label: "Contactable", Value: int(prog.Contactable)},
		{Label: "Qualified", Value: int(prog.Qualified)},
		{Label: "Hot", Value: int(prog.Hot)},
	}
	// failure breakdown
	frows, _ := s.PG.Query(r.Context(), `
		SELECT COALESCE(NULLIF(fail_reason,''),'UNKNOWN'), COUNT(*) FROM raw_leads
		WHERE search_id=$1 AND status='failed' GROUP BY 1 ORDER BY 2 DESC`, sid)
	if frows != nil {
		defer frows.Close()
		for frows.Next() {
			var fr searches.FailRow
			if err := frows.Scan(&fr.Reason, &fr.Count); err == nil {
				d.Failures = append(d.Failures, fr)
			}
		}
	}
	// quality stats over converted leads
	var total, hot, contact int
	var avg float64
	_ = s.PG.QueryRow(r.Context(), `
		SELECT COUNT(*), COUNT(*) FILTER (WHERE l.lead_score >= 90),
			COUNT(*) FILTER (WHERE ct.email <> '' OR COALESCE(c.phone,'') <> ''),
			COALESCE(AVG(l.lead_score),0)
		FROM leads l JOIN companies c ON c.id = l.company_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		WHERE l.tenant_id=$1 AND l.source_search_id=$2 AND l.status <> 'archived'`,
		id.TenantID, sid).Scan(&total, &hot, &contact, &avg)
	d.Quality.AvgScore = avg
	if total > 0 {
		d.Quality.HotPct = float64(hot) / float64(total) * 100
		d.Quality.ContactablePct = float64(contact) / float64(total) * 100
	}
	qry := func(sql string) []string {
		var out []string
		rr, _ := s.PG.Query(r.Context(), sql, id.TenantID, sid)
		if rr == nil {
			return out
		}
		defer rr.Close()
		for rr.Next() {
			var v string
			var n int
			if err := rr.Scan(&v, &n); err == nil && v != "" {
				out = append(out, v+" ("+itoa(n)+")")
			}
		}
		return out
	}
	d.Quality.TopIndustries = qry(`SELECT COALESCE(NULLIF(c.industry,''),'—'), COUNT(*) FROM leads l JOIN companies c ON c.id=l.company_id WHERE l.tenant_id=$1 AND l.source_search_id=$2 AND l.status<>'archived' GROUP BY 1 ORDER BY 2 DESC LIMIT 5`)
	d.Quality.TopCities = qry(`SELECT COALESCE(NULLIF(c.city,''),'—'), COUNT(*) FROM leads l JOIN companies c ON c.id=l.company_id WHERE l.tenant_id=$1 AND l.source_search_id=$2 AND l.status<>'archived' GROUP BY 1 ORDER BY 2 DESC LIMIT 5`)
	// per-source debug
	srows, _ := s.PG.Query(r.Context(), `
		SELECT source_slug, candidates, accepted, errors, COALESCE(last_error,'')
		FROM search_source_stats WHERE search_id=$1 ORDER BY candidates DESC`, sid)
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var sr searches.SrcRow
			if err := srows.Scan(&sr.Slug, &sr.Candidates, &sr.Accepted, &sr.Errors, &sr.LastError); err == nil {
				d.Sources = append(d.Sources, sr)
			}
		}
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var rr searches.ResultRow
			if err := rows.Scan(&rr.LeadID, &rr.Company, &rr.City, &rr.Score, &rr.Status); err == nil {
				d.Results = append(d.Results, rr)
			}
		}
	}
	p := s.page(w, r, it.Query, "/searches")
	layouts.AppShell(s.Ren, id, p, searches.Detail(p, d)).Render(r.Context(), w)
}

// handleSearchLatest returns the newest converted leads as an HTMX fragment.
func (s *Server) handleSearchLatest(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	sid := r.PathValue("id")
	rows, _ := s.PG.Query(r.Context(), `
		SELECT l.id::text, c.name, l.lead_score, to_char(l.created_at,'HH24:MI:SS')
		FROM leads l JOIN companies c ON c.id = l.company_id
		WHERE l.tenant_id=$1 AND l.source_search_id=$2 AND l.status <> 'archived'
		ORDER BY l.created_at DESC LIMIT 5`, id.TenantID, sid)
	var items []searches.LatestRow
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var lr searches.LatestRow
			if err := rows.Scan(&lr.LeadID, &lr.Company, &lr.Score, &lr.When); err == nil {
				items = append(items, lr)
			}
		}
	}
	searches.LatestRows(items).Render(r.Context(), w)
}

// handleSearchEvents streams progress via SSE.
func (s *Server) handleSearchEvents(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	sid := r.PathValue("id")
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	deadline := time.After(10 * time.Minute)
	for {
		prog, err := search.Snapshot(r.Context(), s.PG, id.TenantID, sid)
		if err != nil {
			return
		}
		body, _ := json.Marshal(prog)
		fmt.Fprintf(w, "event: progress\ndata: %s\n\n", body)
		fl.Flush()
		if prog.Status == "completed" || prog.Status == "failed" || prog.Status == "cancelled" {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-deadline:
			return
		case <-t.C:
		}
	}
}

func (s *Server) setSearchStatus(w http.ResponseWriter, r *http.Request, from []string, to, msg string) {
	id := webappIdentity(r)
	sid := r.PathValue("id")
	res, err := s.PG.Exec(r.Context(), `
		UPDATE lead_searches SET status=$3, updated_at=now(),
			finished_at = CASE WHEN $3 IN ('completed','failed','cancelled') THEN now() ELSE finished_at END
		WHERE id=$1 AND tenant_id=$2 AND status = ANY($4)`, sid, id.TenantID, to, from)
	if err != nil || res.RowsAffected() == 0 {
		webapp.RedirectFlash(w, r, "/searches/"+sid, flash.Error, "Could not change search status.")
		return
	}
	webapp.RedirectFlash(w, r, "/searches/"+sid, flash.Success, msg)
}

func (s *Server) handleSearchPause(w http.ResponseWriter, r *http.Request) {
	s.setSearchStatus(w, r, []string{"running"}, "paused", "Search paused. Processed leads are kept.")
}

func (s *Server) handleSearchResume(w http.ResponseWriter, r *http.Request) {
	s.setSearchStatus(w, r, []string{"paused", "queued"}, "running", "Search resumed.")
}

func (s *Server) handleSearchCancel(w http.ResponseWriter, r *http.Request) {
	s.setSearchStatus(w, r, []string{"queued", "running", "paused"}, "cancelled", "Search cancelled. Processed leads are kept.")
}

func (s *Server) handleSearchSave(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	sid := r.PathValue("id")
	var keyword, query, industry, location, country string
	var filtersRaw []byte
	var limit int
	err := s.PG.QueryRow(r.Context(), `
		SELECT keyword, query, industry, location, country, filters, limit_count
		FROM lead_searches WHERE id=$1 AND tenant_id=$2`, sid, id.TenantID).
		Scan(&keyword, &query, &industry, &location, &country, &filtersRaw, &limit)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var params map[string]any
	_ = json.Unmarshal(filtersRaw, &params)
	if params == nil {
		params = map[string]any{}
	}
	params["keyword"], params["industry"] = keyword, industry
	params["location"], params["country"], params["limit"] = location, country, limit
	praw, _ := json.Marshal(params)
	name := keyword
	if name == "" {
		name = query
	}
	if name == "" {
		name = "Saved search"
	}
	_, err = s.PG.Exec(r.Context(), `
		INSERT INTO saved_searches (tenant_id, user_id, name, params) VALUES ($1,$2,$3,$4)`,
		id.TenantID, id.UserID, name, string(praw))
	if err != nil {
		webapp.RedirectFlash(w, r, "/searches/"+sid, flash.Error, "Could not save this search.")
		return
	}
	webapp.RedirectFlash(w, r, "/searches/saved", flash.Success, "Search saved.")
}

func (s *Server) handleSavedList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	rows, err := s.PG.Query(r.Context(), `
		SELECT id::text, name, params, created_at FROM saved_searches
		WHERE tenant_id=$1 ORDER BY created_at DESC`, id.TenantID)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load saved searches.", Err: err})
		return
	}
	defer rows.Close()
	d := &searches.SavedData{}
	for rows.Next() {
		var it searches.SavedItem
		var raw []byte
		var created time.Time
		if err := rows.Scan(&it.ID, &it.Name, &raw, &created); err == nil {
			it.Created = created.Format("02 Jan 2006")
			it.Summary = savedSummary(raw)
			d.Items = append(d.Items, it)
		}
	}
	p := s.page(w, r, "Saved Searches", "/searches/saved")
	layouts.AppShell(s.Ren, id, p, searches.Saved(p, d)).Render(r.Context(), w)
}

func (s *Server) handleSavedRun(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	savedID := r.PathValue("id")
	var name string
	var raw []byte
	if err := s.PG.QueryRow(r.Context(), `SELECT name, params FROM saved_searches WHERE id=$1 AND tenant_id=$2`,
		savedID, id.TenantID).Scan(&name, &raw); err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	var p map[string]any
	_ = json.Unmarshal(raw, &p)
	str := func(k string) string {
		if v, ok := p[k].(string); ok {
			return v
		}
		return ""
	}
	limit := 100
	if f, ok := p["limit"].(float64); ok && f > 0 {
		limit = int(f)
	}
	filters := search.Filters{
		Country: str("country"), Province: str("province"), City: str("city"),
		CompanySize: str("company_size"), MinScore: int(numVal(p, "min_score")),
	}
	if arr, ok := p["seed_urls"].([]any); ok {
		for _, v := range arr {
			if u, ok := v.(string); ok && u != "" {
				filters.SeedURLs = append(filters.SeedURLs, u)
			}
		}
	}
	if arr, ok := p["sources"].([]any); ok {
		for _, v := range arr {
			if u, ok := v.(string); ok && u != "" {
				filters.Sources = append(filters.Sources, u)
			}
		}
	}
	for _, k := range []string{"has_website", "has_email", "has_phone", "has_whatsapp"} {
		if b, ok := p[k].(bool); ok && b {
			switch k {
			case "has_website":
				filters.HasWebsite = true
			case "has_email":
				filters.HasEmail = true
			case "has_phone":
				filters.HasPhone = true
			case "has_whatsapp":
				filters.HasWhatsApp = true
			}
		}
	}
	fraw, _ := json.Marshal(filters)
	keyword := str("keyword")
	location := str("location")
	if location == "" {
		location = stringsTrimJoin([]string{str("city"), str("province")}, ", ")
	}
	var searchID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, industry, location, country, filters, limit_count, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued') RETURNING id::text`,
		id.TenantID, id.UserID, keyword, str("query"), str("industry"), location, str("country"), string(fraw), limit).Scan(&searchID)
	if err != nil {
		webapp.RedirectFlash(w, r, "/searches/saved", flash.Error, "Could not start the search.")
		return
	}
	if s.Queue == nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='queue unavailable' WHERE id=$1`, searchID)
		webapp.RedirectFlash(w, r, "/searches/saved", flash.Error, "Search queue is unavailable.")
		return
	}
	if err := s.Queue.EnqueueSearch(r.Context(), searchID); err != nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='enqueue failed' WHERE id=$1`, searchID)
		webapp.RedirectFlash(w, r, "/searches/saved", flash.Error, "Could not queue the search job.")
		return
	}
	http.Redirect(w, r, "/searches/"+searchID, http.StatusSeeOther)
}

func (s *Server) handleSavedDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM saved_searches WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/searches/saved", http.StatusSeeOther)
}

func savedSummary(raw []byte) string {
	var p map[string]any
	if json.Unmarshal(raw, &p) != nil {
		return ""
	}
	parts := []string{}
	str := func(k string) string {
		if v, ok := p[k].(string); ok && v != "" {
			return v
		}
		return ""
	}
	for _, k := range []string{"keyword", "industry", "city", "province", "country"} {
		if v := str(k); v != "" {
			parts = append(parts, v)
		}
	}
	if f, ok := p["limit"].(float64); ok && f > 0 {
		parts = append(parts, "limit "+strconv.Itoa(int(f)))
	}
	out := ""
	for i, pt := range parts {
		if i > 0 {
			out += " · "
		}
		out += pt
	}
	return out
}

func numVal(p map[string]any, k string) float64 {
	if f, ok := p[k].(float64); ok {
		return f
	}
	return 0
}

func stringsTrimJoin(parts []string, sep string) string {
	var out []string
	for _, pt := range parts {
		if pt = strings.TrimSpace(pt); pt != "" {
			out = append(out, pt)
		}
	}
	res := ""
	for i, o := range out {
		if i > 0 {
			res += sep
		}
		res += o
	}
	return res
}

func formatDuration(started, finished *time.Time, created time.Time) string {
	if started == nil {
		return "—"
	}
	end := time.Now()
	if finished != nil {
		end = *finished
	}
	d := end.Sub(*started)
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	if d < time.Hour {
		return strconv.Itoa(int(d.Minutes())) + "m"
	}
	return strconv.Itoa(int(d.Hours())) + "h"
}
