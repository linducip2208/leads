package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/lead"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/companies"
)

func (s *Server) companyRoutes() {
	s.Router.HandleFunc("GET", "/companies", s.requirePerm(role.LeadRead, s.handleCompanyList))
	s.Router.HandleFunc("GET", "/companies/new", s.requirePerm(role.LeadCreate, s.handleCompanyNew))
	s.Router.HandleFunc("POST", "/companies", s.requirePerm(role.LeadCreate, s.handleCompanyCreate))
	s.Router.HandleFunc("GET", "/companies/{id}", s.requirePerm(role.LeadRead, s.handleCompanyDetail))
	s.Router.HandleFunc("POST", "/companies/{id}", s.requirePerm(role.LeadUpdate, s.handleCompanyUpdate))
}

func (s *Server) handleCompanyList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	q := r.URL.Query()
	ms, _ := strconv.Atoi(q.Get("min_score"))
	f := companies.Filter{
		Keyword: strings.TrimSpace(q.Get("q")), Industry: strings.TrimSpace(q.Get("industry")),
		City: strings.TrimSpace(q.Get("city")), Owner: strings.TrimSpace(q.Get("owner")), MinScore: ms,
	}
	whr := "c.tenant_id = $1"
	var args []any
	args = append(args, id.TenantID)
	add := func(cond string, v any) {
		args = append(args, v)
		whr += " AND " + cond
	}
	ph := func() string { return "$" + strconv.Itoa(len(args)+1) }
	if f.Keyword != "" {
		like := "%" + f.Keyword + "%"
		args = append(args, like)
		n := strconv.Itoa(len(args))
		whr += " AND (c.name ILIKE $" + n + " OR c.domain ILIKE $" + n + ")"
	}
	if f.Industry != "" {
		add("c.industry = "+ph(), f.Industry)
	}
	if f.City != "" {
		args = append(args, "%"+f.City+"%", "%"+f.City+"%")
		n := len(args)
		whr += " AND (c.city ILIKE $" + strconv.Itoa(n-1) + " OR c.province ILIKE $" + strconv.Itoa(n) + ")"
	}
	if f.Owner != "" {
		add("c.owner_id = "+ph()+"::uuid", f.Owner)
	}
	if f.MinScore > 0 {
		add("c.lead_score >= "+ph(), f.MinScore)
	}
	query := `SELECT c.id::text, c.name, COALESCE(c.industry,''), COALESCE(c.city,''), COALESCE(c.province,''),
		COALESCE(c.website,''), (SELECT COUNT(*) FROM contacts ct WHERE ct.company_id=c.id),
		c.lead_score, COALESCE(u.name,''), c.source,
		COALESCE(to_char((SELECT MAX(a.created_at) FROM activities a WHERE a.company_id=c.id),'DD Mon HH24:MI'), to_char(c.created_at,'DD Mon')),
		c.created_at
		FROM companies c LEFT JOIN users u ON u.id = c.owner_id
		WHERE ` + whr
	if cur := q.Get("cursor"); cur != "" {
		if ts, lid, ok := splitCursor(cur); ok {
			args = append(args, ts, lid)
			n := len(args)
			query += " AND (c.created_at < $" + strconv.Itoa(n-1) + " OR (c.created_at = $" + strconv.Itoa(n-1) + " AND c.id < $" + strconv.Itoa(n) + "::uuid))"
		}
	}
	query += " ORDER BY c.created_at DESC, c.id DESC LIMIT 51"
	rows, err := s.PG.Query(r.Context(), query, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load companies.", Err: err})
		return
	}
	defer rows.Close()
	d := &companies.ListData{Filter: f}
	for _, kv := range s.ownerKVs(r) {
		d.Owners = append(d.Owners, companies.Option{Value: kv.Value, Label: kv.Label})
	}
	for _, kv := range s.distinctKVs(r, `SELECT DISTINCT industry FROM companies WHERE tenant_id=$1 AND industry<>'' ORDER BY 1`) {
		d.Industries = append(d.Industries, companies.Option{Value: kv.Value, Label: kv.Label})
	}
	var lastCreated time.Time
	var lastID string
	for rows.Next() {
		var it companies.Item
		var created time.Time
		var nContacts int
		if err := rows.Scan(&it.ID, &it.Name, &it.Industry, &it.City, &it.Province, &it.Website,
			&nContacts, &it.Score, &it.Owner, &it.Source, &it.LastActivity, &created); err == nil {
			it.Contacts = nContacts
			d.Items = append(d.Items, it)
			lastCreated, lastID = created, it.ID
		}
	}
	if len(d.Items) > 50 {
		d.Items = d.Items[:50]
		d.HasMore = true
		d.NextCursor = lastCreated.UTC().Format(time.RFC3339) + "|" + lastID
	}
	p := s.page(w, r, "Companies", "/companies")
	layouts.AppShell(s.Ren, id, p, companies.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handleCompanyNew(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Add Company", "/companies")
	layouts.AppShell(s.Ren, webappIdentity(r), p, companies.New(p, &companies.DetailData{})).Render(r.Context(), w)
}

func (s *Server) handleCompanyCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	website := strings.TrimSpace(r.FormValue("website"))
	if name == "" {
		p := s.page(w, r, "Add Company", "/companies")
		dd := &companies.DetailData{}
		dd.Form.Error = "Name is required."
		layouts.AppShell(s.Ren, id, p, companies.New(p, dd)).Render(r.Context(), w)
		return
	}
	web, domain := lead.NormalizeWebsite(website)
	var companyID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO companies (tenant_id, name, domain, website, industry, country, province, city, address, phone, source)
		VALUES ($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10,'manual') RETURNING id::text`,
		id.TenantID, lead.NormalizeName(name), domain, web,
		strings.TrimSpace(r.FormValue("industry")), "Indonesia",
		strings.TrimSpace(r.FormValue("province")), strings.TrimSpace(r.FormValue("city")),
		"", strings.TrimSpace(r.FormValue("phone"))).Scan(&companyID)
	if err != nil {
		p := s.page(w, r, "Add Company", "/companies")
		dd := &companies.DetailData{}
		dd.Form.Error = "Could not create the company (possible duplicate domain)."
		layouts.AppShell(s.Ren, id, p, companies.New(p, dd)).Render(r.Context(), w)
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO leads (tenant_id, company_id, status, source, owner_id)
		VALUES ($1,$2,'new','manual',$3) ON CONFLICT (tenant_id, company_id) DO NOTHING`,
		id.TenantID, companyID, id.UserID)
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO activities (tenant_id, kind, subject, company_id, user_id)
		VALUES ($1,'lead.created',$2,$3,$4)`, id.TenantID, "Company created manually", companyID, id.UserID)
	http.Redirect(w, r, "/companies/"+companyID, http.StatusSeeOther)
}

func (s *Server) handleCompanyDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	d := &companies.DetailData{ID: cid}
	var ownerID *string
	var created time.Time
	var enriched *time.Time
	err := s.PG.QueryRow(r.Context(), `
		SELECT c.name, c.lead_score, c.source, c.created_at, c.last_enriched_at,
			COALESCE(c.industry,''), COALESCE(c.website,''), COALESCE(c.phone,''), COALESCE(c.country,''),
			COALESCE(c.province,''), COALESCE(c.city,''), COALESCE(c.address,''), c.owner_id::text
		FROM companies c WHERE c.id=$1 AND c.tenant_id=$2`,
		cid, id.TenantID).Scan(&d.Name, &d.Score, &d.Source, &created, &enriched,
		&d.Form.Industry, &d.Form.Website, &d.Form.Phone, &d.Form.Country,
		&d.Form.Province, &d.Form.City, &d.Form.Address, &ownerID)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	d.Form.Name = d.Name
	if ownerID != nil {
		d.Form.OwnerID = *ownerID
	}
	d.Created = created.Format("02 Jan 2006")
	if enriched != nil {
		d.EnrichedAt = enriched.Format("02 Jan 15:04")
	}
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, COALESCE(NULLIF(full_name,''), email), COALESCE(job_title,''), COALESCE(email,''), COALESCE(phone,'')
		FROM contacts WHERE tenant_id=$1 AND company_id=$2 ORDER BY created_at LIMIT 20`, id.TenantID, cid)
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var c companies.ContactRow
			if err := rows.Scan(&c.ID, &c.Name, &c.Title, &c.Email, &c.Phone); err == nil {
				d.Contacts = append(d.Contacts, c)
			}
		}
	}
	lrows, _ := s.PG.Query(r.Context(), `SELECT id::text, lead_score, status FROM leads WHERE tenant_id=$1 AND company_id=$2`, id.TenantID, cid)
	if lrows != nil {
		defer lrows.Close()
		for lrows.Next() {
			var l companies.LeadRow
			if err := lrows.Scan(&l.ID, &l.Score, &l.Status); err == nil {
				d.Leads = append(d.Leads, l)
			}
		}
	}
	drows, _ := s.PG.Query(r.Context(), `
		SELECT dl.id::text, dl.title, COALESCE(ps.name, dl.status), dl.value::text, dl.status
		FROM deals dl LEFT JOIN pipeline_stages ps ON ps.id = dl.stage_id
		WHERE dl.tenant_id=$1 AND dl.company_id=$2 ORDER BY dl.created_at DESC LIMIT 10`, id.TenantID, cid)
	if drows != nil {
		defer drows.Close()
		for drows.Next() {
			var dl companies.DealRow
			if err := drows.Scan(&dl.ID, &dl.Title, &dl.Stage, &dl.Value, &dl.Status); err == nil {
				d.Deals = append(d.Deals, dl)
			}
		}
	}
	for _, kv := range s.ownerKVs(r) {
		d.Owners = append(d.Owners, companies.Option{Value: kv.Value, Label: kv.Label})
	}
	p := s.page(w, r, d.Name, "/companies")
	layouts.AppShell(s.Ren, id, p, companies.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleCompanyUpdate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	web, domain := lead.NormalizeWebsite(strings.TrimSpace(r.FormValue("website")))
	var ownerVal any
	if o := strings.TrimSpace(r.FormValue("owner_id")); o != "" {
		ownerVal = o
	}
	res, err := s.PG.Exec(r.Context(), `
		UPDATE companies SET name=$3, industry=$4, website=$5, domain=NULLIF($6,''),
			phone=$7, country=$8, province=$9, city=$10, address=$11, owner_id=$12::uuid, updated_at=now()
		WHERE id=$1 AND tenant_id=$2`,
		cid, id.TenantID, lead.NormalizeName(strings.TrimSpace(r.FormValue("name"))),
		strings.TrimSpace(r.FormValue("industry")), web, domain,
		strings.TrimSpace(r.FormValue("phone")), strings.TrimSpace(r.FormValue("country")),
		strings.TrimSpace(r.FormValue("province")), strings.TrimSpace(r.FormValue("city")),
		strings.TrimSpace(r.FormValue("address")), ownerVal)
	if err != nil || res.RowsAffected() == 0 {
		webapp.RedirectFlash(w, r, "/companies/"+cid, flash.Error, "Could not save changes.")
		return
	}
	webapp.RedirectFlash(w, r, "/companies/"+cid, flash.Success, "Company updated.")
}
