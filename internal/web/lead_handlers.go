package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/leads"
)

func (s *Server) leadRoutes() {
	s.Router.HandleFunc("GET", "/leads", s.requirePerm(role.LeadRead, s.handleLeadList))
	s.Router.HandleFunc("GET", "/leads/{id}", s.requirePerm(role.LeadRead, s.handleLeadDetail))
	s.Router.HandleFunc("GET", "/leads/{id}/drawer", s.requirePerm(role.LeadRead, s.handleLeadDrawer))
	s.Router.HandleFunc("POST", "/leads/{id}", s.requirePerm(role.LeadUpdate, s.handleLeadUpdate))
	s.Router.HandleFunc("POST", "/leads/{id}/refresh", s.requirePerm(role.LeadUpdate, s.handleLeadRefresh))
	s.Router.HandleFunc("POST", "/leads/bulk", s.requirePerm(role.LeadUpdate, s.handleLeadBulk))
}

const leadPageSize = 50

func parseLeadFilter(r *http.Request) leads.Filter {
	q := r.URL.Query()
	ms, _ := strconv.Atoi(q.Get("min_score"))
	return leads.Filter{
		Tab:         firstOr(q.Get("tab"), "all"),
		Keyword:     strings.TrimSpace(q.Get("q")),
		Industry:    strings.TrimSpace(q.Get("industry")),
		Country:     strings.TrimSpace(q.Get("country")),
		Province:    strings.TrimSpace(q.Get("province")),
		City:        strings.TrimSpace(q.Get("city")),
		Source:      strings.TrimSpace(q.Get("source")),
		Owner:       strings.TrimSpace(q.Get("owner")),
		MinScore:    ms,
		HasWebsite:  q.Get("has_website") != "",
		HasEmail:    q.Get("has_email") != "",
		HasPhone:    q.Get("has_phone") != "",
		HasWhatsApp: q.Get("has_whatsapp") != "",
		From:        strings.TrimSpace(q.Get("from")),
		To:          strings.TrimSpace(q.Get("to")),
		SearchID:    strings.TrimSpace(q.Get("search")),
	}
}

func firstOr(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// leadWhere builds the WHERE clause (args start at $2; $1 is tenant).
func leadWhere(f leads.Filter, skipStatus bool) (string, []any) {
	w := "l.tenant_id = $1"
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		w += " AND " + cond
	}
	// $1 is reserved for tenant_id, so first filter arg is $2.
	ph := func() string { return "$" + strconv.Itoa(len(args)+2) }
	if !skipStatus {
		switch f.Tab {
		case "all":
		case "archived":
			w += " AND l.status = 'archived'"
		default:
			add("l.status = "+ph(), f.Tab)
		}
		if f.Tab == "all" {
			// archived hidden on "all"? spec tabs include Archived separately; keep all = non-archived
			w += " AND l.status <> 'archived'"
		}
	}
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		args = append(args, like)
		n := strconv.Itoa(len(args) + 1) // +1: $1 is tenant_id
		w += " AND (c.name ILIKE $" + n + " OR c.domain ILIKE $" + n + " OR ct.full_name ILIKE $" + n + " OR ct.email ILIKE $" + n + ")"
	}
	if f.Industry != "" {
		add("c.industry = "+ph(), f.Industry)
	}
	if f.Country != "" {
		add("c.country ILIKE "+ph(), "%"+f.Country+"%")
	}
	if f.Province != "" {
		add("c.province ILIKE "+ph(), "%"+f.Province+"%")
	}
	if f.City != "" {
		add("c.city ILIKE "+ph(), "%"+f.City+"%")
	}
	if f.Source != "" {
		add("l.source = "+ph(), f.Source)
	}
	if f.Owner != "" {
		if f.Owner == "unassigned" {
			w += " AND l.owner_id IS NULL"
		} else {
			add("l.owner_id = "+ph()+"::uuid", f.Owner)
		}
	}
	if f.MinScore > 0 {
		add("l.lead_score >= "+ph(), f.MinScore)
	}
	if f.HasWebsite {
		w += " AND NULLIF(c.website,'') IS NOT NULL"
	}
	if f.HasEmail {
		w += " AND ct.email <> ''"
	}
	if f.HasPhone {
		w += " AND (NULLIF(c.phone,'') IS NOT NULL OR NULLIF(ct.phone,'') IS NOT NULL)"
	}
	if f.HasWhatsApp {
		w += " AND NULLIF(c.whatsapp,'') IS NOT NULL"
	}
	if f.From != "" {
		add("l.created_at >= "+ph()+"::date", f.From)
	}
	if f.To != "" {
		add("l.created_at < ("+ph()+"::date + interval '1 day')", f.To)
	}
	if f.SearchID != "" {
		add("l.source_search_id = "+ph()+"::uuid", f.SearchID)
	}
	return w, args
}

func (s *Server) handleLeadList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	f := parseLeadFilter(r)
	where, fargs := leadWhere(f, false)
	args := append([]any{id.TenantID}, fargs...)

	// tab counts (same filters, no status)
	cwhere, cargs := leadWhere(f, true)
	cargsFull := append([]any{id.TenantID}, cargs...)
	tabDefs := []struct{ v, l string }{
		{"all", "All"}, {"new", "New"}, {"qualified", "Qualified"}, {"hot", "Hot"},
		{"contacted", "Contacted"}, {"archived", "Archived"},
	}
	var tabs []leads.TabCount
	for _, td := range tabDefs {
		q := `SELECT COUNT(*) FROM leads l
			LEFT JOIN companies c ON c.id = l.company_id
			LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
			WHERE ` + cwhere
		if td.v == "archived" {
			q += " AND l.status='archived'"
		} else if td.v == "all" {
			q += " AND l.status<>'archived'"
		} else {
			q += " AND l.status='" + td.v + "'"
		}
		var n int
		_ = s.PG.QueryRow(r.Context(), q, cargsFull...).Scan(&n)
		tabs = append(tabs, leads.TabCount{Value: td.v, Label: td.l, Count: n, Active: f.Tab == td.v})
	}

	// keyset pagination
	cursorQ := `SELECT l.id::text, c.name, c.id::text, COALESCE(ct.full_name,''), COALESCE(c.industry,''),
			COALESCE(c.city,''), COALESCE(c.province,''), COALESCE(ct.email,''), COALESCE(NULLIF(ct.phone,''), c.phone,''),
			l.lead_score, l.status, COALESCE(u.name,''), l.source,
			COALESCE(to_char(l.last_activity_at,'DD Mon HH24:MI'), to_char(l.created_at,'DD Mon')),
			to_char(l.created_at,'DD Mon'), l.created_at, l.data_quality,
			(SELECT COUNT(*) FROM lead_opportunities lo WHERE lo.lead_id = l.id),
			(ct.email <> '' OR COALESCE(NULLIF(ct.phone,''), c.phone,'') <> '')
		FROM leads l
		JOIN companies c ON c.id = l.company_id
		LEFT JOIN contacts ct ON ct.id = l.primary_contact_id
		LEFT JOIN users u ON u.id = l.owner_id
		WHERE ` + where
	qargs := append([]any{}, args...)
	if cur := r.URL.Query().Get("cursor"); cur != "" {
		if ts, lid, ok := splitCursor(cur); ok {
			qargs = append(qargs, ts, lid)
			cursorQ += " AND (l.created_at < $" + strconv.Itoa(len(qargs)-1) + " OR (l.created_at = $" + strconv.Itoa(len(qargs)-1) + " AND l.id < $" + strconv.Itoa(len(qargs)) + "::uuid))"
		}
	}
	cursorQ += " ORDER BY l.created_at DESC, l.id DESC LIMIT " + strconv.Itoa(leadPageSize+1)
	rows, err := s.PG.Query(r.Context(), cursorQ, qargs...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load leads.", Err: err})
		return
	}
	defer rows.Close()
	d := &leads.ListData{Filter: f, Tabs: tabs, Owners: s.ownerOptions(r), Sources: s.distinctOptions(r, `SELECT DISTINCT source FROM leads WHERE tenant_id=$1 AND source<>'' ORDER BY 1`), Industries: s.distinctOptions(r, `SELECT DISTINCT industry FROM companies WHERE tenant_id=$1 AND industry<>'' ORDER BY 1`), Lists: s.listOptions(r)}
	var lastCreated time.Time
	var lastID string
	for rows.Next() {
		var it leads.Item
		var created time.Time
		if err := rows.Scan(&it.ID, &it.Company, &it.CompanyID, &it.Contact, &it.Industry, &it.City,
			&it.Province, &it.Email, &it.Phone, &it.Score, &it.Status, &it.Owner, &it.Source,
			&it.LastActivity, &it.Created, &created, &it.Quality, &it.Opps, &it.Contactable); err == nil {
			d.Items = append(d.Items, it)
			lastCreated, lastID = created, it.ID
		}
	}
	if len(d.Items) > leadPageSize {
		d.Items = d.Items[:leadPageSize]
		d.HasMore = true
		d.NextCursor = lastCreated.UTC().Format(time.RFC3339) + "|" + lastID
	}
	p := s.page(w, r, "Leads", "/leads")
	layouts.AppShell(s.Ren, id, p, leads.List(p, d)).Render(r.Context(), w)
}

func (s *Server) ownerOptions(r *http.Request) []leads.Option {
	var out []leads.Option
	for _, kv := range s.ownerKVs(r) {
		out = append(out, leads.Option{Value: kv.Value, Label: kv.Label})
	}
	return out
}

func (s *Server) distinctOptions(r *http.Request, q string) []leads.Option {
	var out []leads.Option
	for _, kv := range s.distinctKVs(r, q) {
		out = append(out, leads.Option{Value: kv.Value, Label: kv.Label})
	}
	return out
}

func (s *Server) listOptions(r *http.Request) []leads.Option {
	var out []leads.Option
	for _, kv := range s.listKVs(r) {
		out = append(out, leads.Option{Value: kv.Value, Label: kv.Label})
	}
	return out
}
