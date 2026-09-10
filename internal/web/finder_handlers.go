package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/search"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/finder"
)

func (s *Server) finderRoutes() {
	s.Router.HandleFunc("GET", "/finder", s.requirePerm(role.SearchCreate, s.handleFinder))
	s.Router.HandleFunc("POST", "/finder/search", s.requirePerm(role.SearchCreate, s.handleFinderSearch))
}

func finderDefaults() finder.FormData {
	return finder.FormData{
		Country: "Indonesia", ResultLimit: 100,
		Sources: []string{"website_search", "public_directory"},
	}
}

func (s *Server) handleFinder(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Lead Finder", "/finder")
	layouts.AppShell(s.Ren, webappIdentity(r), p, finder.Page(p, &finder.PageData{
		ICPs:    s.icpOptions(r),
		Limits:  finderLimits(),
		Sources: finderSources(),
		Form:    finderDefaults(),
	})).Render(r.Context(), w)
}

func (s *Server) handleFinderSearch(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		webapp.RedirectFlash(w, r, "/finder", flash.Error, "Could not read the form. Try again.")
		return
	}
	form := finder.FormData{
		Keyword:     strings.TrimSpace(r.FormValue("keyword")),
		Industry:    strings.TrimSpace(r.FormValue("industry")),
		Country:     strings.TrimSpace(r.FormValue("country")),
		Province:    strings.TrimSpace(r.FormValue("province")),
		City:        strings.TrimSpace(r.FormValue("city")),
		CompanySize: strings.TrimSpace(r.FormValue("company_size")),
		HasWebsite:  r.FormValue("has_website") != "",
		HasEmail:    r.FormValue("has_email") != "",
		HasPhone:    r.FormValue("has_phone") != "",
		HasWhatsApp: r.FormValue("has_whatsapp") != "",
		ICPID:       strings.TrimSpace(r.FormValue("icp_id")),
		SeedURLs:    strings.TrimSpace(r.FormValue("seed_urls")),
		Sources:     r.Form["sources"],
		CustomLimit: strings.TrimSpace(r.FormValue("custom_limit")),
	}
	form.MinScore, _ = strconv.Atoi(r.FormValue("min_score"))
	form.ResultLimit, _ = strconv.Atoi(r.FormValue("result_limit"))
	if form.ResultLimit <= 0 {
		form.ResultLimit = 100
	}
	if cl, err := strconv.Atoi(form.CustomLimit); err == nil && cl > 0 {
		form.ResultLimit = cl
	}
	if form.ResultLimit > 50000 {
		form.ResultLimit = 50000
	}
	fail := func(msg string) {
		form.Error = msg
		p := s.page(w, r, "Lead Finder", "/finder")
		layouts.AppShell(s.Ren, webappIdentity(r), p, finder.Page(p, &finder.PageData{
			ICPs: s.icpOptions(r), Limits: finderLimits(), Sources: finderSources(), Form: form,
		})).Render(r.Context(), w)
	}

	// ICP fills blanks
	if form.ICPID != "" {
		s.applyICP(r, &form)
	}

	seeds := parseSeedURLs(form.SeedURLs)
	if form.Keyword == "" && form.Industry == "" && len(seeds) == 0 {
		fail("Enter a keyword, an industry, or at least one seed URL.")
		return
	}
	// pasted seed URLs always enable the manual source
	if len(seeds) > 0 && !hasSlug(form.Sources, "manual") {
		form.Sources = append(form.Sources, "manual")
	}
	if len(form.Sources) == 0 {
		fail("Select at least one discovery source or paste seed URLs.")
		return
	}

	id := webappIdentity(r)
	filters := search.Filters{
		Country: form.Country, Province: form.Province, City: form.City,
		CompanySize: form.CompanySize, HasWebsite: form.HasWebsite, HasEmail: form.HasEmail,
		HasPhone: form.HasPhone, HasWhatsApp: form.HasWhatsApp, MinScore: form.MinScore,
		SeedURLs: seeds, Sources: form.Sources,
	}
	fraw, _ := json.Marshal(filters)
	location := strings.Trim(strings.Join(nonEmpty(form.City, form.Province), ", "), ", ")
	query := strings.TrimSpace(strings.Join(nonEmpty(form.Keyword, form.Industry, location), " "))

	var searchID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, industry, location, country, filters, limit_count, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,'queued') RETURNING id::text`,
		id.TenantID, id.UserID, form.Keyword, query, form.Industry, location, form.Country, string(fraw), form.ResultLimit).Scan(&searchID)
	if err != nil {
		s.Log.Error("create search", "err", err)
		fail("Could not create the search. Try again.")
		return
	}
	if s.Queue == nil {
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='queue unavailable' WHERE id=$1`, searchID)
		fail("Search queue is unavailable. Start the worker/redis and try again.")
		return
	}
	if err := s.Queue.EnqueueSearch(r.Context(), searchID); err != nil {
		s.Log.Error("enqueue search", "err", err)
		_, _ = s.PG.Exec(r.Context(), `UPDATE lead_searches SET status='failed', error='enqueue failed' WHERE id=$1`, searchID)
		fail("Could not queue the search job. Is Redis running?")
		return
	}
	http.Redirect(w, r, "/searches/"+searchID, http.StatusSeeOther)
}

func (s *Server) icpOptions(r *http.Request) []finder.Option {
	rows, err := s.PG.Query(r.Context(), `SELECT id::text, name FROM icps WHERE tenant_id=$1 ORDER BY name`, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []finder.Option
	for rows.Next() {
		var o finder.Option
		if err := rows.Scan(&o.Value, &o.Label); err == nil {
			out = append(out, o)
		}
	}
	return out
}

// applyICP fills empty form fields from the ICP criteria.
func (s *Server) applyICP(r *http.Request, form *finder.FormData) {
	var raw []byte
	err := s.PG.QueryRow(r.Context(), `SELECT criteria FROM icps WHERE id=$1 AND tenant_id=$2`,
		form.ICPID, webappIdentity(r).TenantID).Scan(&raw)
	if err != nil || len(raw) == 0 {
		return
	}
	var c struct {
		Industry string `json:"industry"`
		Country  string `json:"country"`
		Province string `json:"province"`
		City     string `json:"city"`
		MinScore int    `json:"min_score"`
	}
	if json.Unmarshal(raw, &c) != nil {
		return
	}
	if form.Industry == "" {
		form.Industry = c.Industry
	}
	if form.Country == "" {
		form.Country = c.Country
	}
	if form.Province == "" {
		form.Province = c.Province
	}
	if form.City == "" {
		form.City = c.City
	}
	if form.MinScore == 0 {
		form.MinScore = c.MinScore
	}
}

func finderLimits() []finder.Option {
	return []finder.Option{
		{Value: "100", Label: "100"},
		{Value: "500", Label: "500"},
		{Value: "1000", Label: "1,000"},
		{Value: "5000", Label: "5,000"},
		{Value: "10000", Label: "10,000"},
		{Value: "50000", Label: "50,000"},
	}
}

func finderSources() []finder.Option {
	return []finder.Option{
		{Value: "website_search", Label: "Web search discovery"},
		{Value: "public_directory", Label: "Public directory (OpenStreetMap)"},
	}
}

func hasSlug(slugs []string, want string) bool {
	for _, s := range slugs {
		if s == want {
			return true
		}
	}
	return false
}

func parseSeedURLs(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		for _, part := range strings.Split(line, ",") {
			if u := strings.TrimSpace(part); u != "" {
				out = append(out, u)
			}
		}
	}
	return out
}

func nonEmpty(vals ...string) []string {
	var out []string
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}
