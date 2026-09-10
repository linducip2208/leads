package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/lead"
	"leadforge/internal/role"
	"leadforge/internal/verify"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/people"
)

func (s *Server) peopleRoutes() {
	s.Router.HandleFunc("GET", "/people", s.requirePerm(role.LeadRead, s.handlePeopleList))
	s.Router.HandleFunc("GET", "/people/new", s.requirePerm(role.LeadCreate, s.handlePeopleNew))
	s.Router.HandleFunc("POST", "/people", s.requirePerm(role.LeadCreate, s.handlePeopleCreate))
	s.Router.HandleFunc("GET", "/people/{id}", s.requirePerm(role.LeadRead, s.handlePeopleDetail))
	s.Router.HandleFunc("POST", "/people/{id}", s.requirePerm(role.LeadUpdate, s.handlePeopleUpdate))
}

func (s *Server) handlePeopleList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	q := r.URL.Query()
	f := people.Filter{
		Keyword: strings.TrimSpace(q.Get("q")), Company: strings.TrimSpace(q.Get("company")),
		Title: strings.TrimSpace(q.Get("title")), Owner: strings.TrimSpace(q.Get("owner")),
	}
	whr := "ct.tenant_id = $1"
	var args []any
	args = append(args, id.TenantID)
	if f.Keyword != "" {
		args = append(args, "%"+f.Keyword+"%")
		n := strconv.Itoa(len(args))
		whr += " AND (ct.full_name ILIKE $" + n + " OR ct.email ILIKE $" + n + ")"
	}
	if f.Company != "" {
		args = append(args, "%"+f.Company+"%")
		whr += " AND c.name ILIKE $" + strconv.Itoa(len(args))
	}
	if f.Title != "" {
		args = append(args, "%"+f.Title+"%")
		whr += " AND ct.job_title ILIKE $" + strconv.Itoa(len(args))
	}
	query := `SELECT ct.id::text, COALESCE(NULLIF(ct.full_name,''), ct.email, '—'), COALESCE(c.name,''),
		COALESCE(ct.job_title,''), COALESCE(ct.email,''), ct.email_status, COALESCE(ct.phone,''),
		ct.lead_score, ct.source, ct.created_at
		FROM contacts ct LEFT JOIN companies c ON c.id = ct.company_id
		WHERE ` + whr
	if cur := q.Get("cursor"); cur != "" {
		if ts, lid, ok := splitCursor(cur); ok {
			args = append(args, ts, lid)
			n := len(args)
			query += " AND (ct.created_at < $" + strconv.Itoa(n-1) + " OR (ct.created_at = $" + strconv.Itoa(n-1) + " AND ct.id < $" + strconv.Itoa(n) + "::uuid))"
		}
	}
	query += " ORDER BY ct.created_at DESC, ct.id DESC LIMIT 51"
	rows, err := s.PG.Query(r.Context(), query, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load contacts.", Err: err})
		return
	}
	defer rows.Close()
	d := &people.ListData{Filter: f}
	var lastCreated time.Time
	var lastID string
	for rows.Next() {
		var it people.Item
		var created time.Time
		if err := rows.Scan(&it.ID, &it.Name, &it.Company, &it.Title, &it.Email, &it.EStatus, &it.Phone, &it.Score, &it.Source, &created); err == nil {
			d.Items = append(d.Items, it)
			lastCreated, lastID = created, it.ID
		}
	}
	if len(d.Items) > 50 {
		d.Items = d.Items[:50]
		d.HasMore = true
		d.NextCursor = lastCreated.UTC().Format(time.RFC3339) + "|" + lastID
	}
	p := s.page(w, r, "People", "/people")
	layouts.AppShell(s.Ren, id, p, people.List(p, d)).Render(r.Context(), w)
}

func (s *Server) handlePeopleNew(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Add Contact", "/people")
	layouts.AppShell(s.Ren, webappIdentity(r), p, people.New(p, &people.DetailData{Companies: s.companyOptions(r)})).Render(r.Context(), w)
}

func (s *Server) companyOptions(r *http.Request) []people.Option {
	rows, err := s.PG.Query(r.Context(), `SELECT id::text, name FROM companies WHERE tenant_id=$1 ORDER BY name LIMIT 500`, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []people.Option
	for rows.Next() {
		var o people.Option
		if err := rows.Scan(&o.Value, &o.Label); err == nil {
			out = append(out, o)
		}
	}
	return out
}

func (s *Server) handlePeopleCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	email := lead.NormalizeEmail(strings.TrimSpace(r.FormValue("email")))
	first := strings.TrimSpace(r.FormValue("first_name"))
	last := strings.TrimSpace(r.FormValue("last_name"))
	full := strings.TrimSpace(first + " " + last)
	if full == "" && email == "" {
		p := s.page(w, r, "Add Contact", "/people")
		dd := &people.DetailData{Companies: s.companyOptions(r)}
		dd.Form.Error = "Name or email is required."
		layouts.AppShell(s.Ren, id, p, people.New(p, dd)).Render(r.Context(), w)
		return
	}
	var companyVal any
	if c := strings.TrimSpace(r.FormValue("company_id")); c != "" {
		companyVal = c
	}
	vres, _ := verify.SyntaxVerifier{}.Verify(r.Context(), email)
	var contactID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO contacts (tenant_id, company_id, first_name, last_name, full_name, job_title, email, email_status, phone, source)
		VALUES ($1,$2::uuid,$3,$4,$5,$6,NULLIF($7,''),$8,NULLIF($9,''),'manual') RETURNING id::text`,
		id.TenantID, companyVal, first, last, full,
		strings.TrimSpace(r.FormValue("job_title")), email, vres.Status,
		strings.TrimSpace(r.FormValue("phone"))).Scan(&contactID)
	if err != nil {
		p := s.page(w, r, "Add Contact", "/people")
		dd := &people.DetailData{Companies: s.companyOptions(r)}
		dd.Form.Error = "Could not create the contact (email may already exist)."
		layouts.AppShell(s.Ren, id, p, people.New(p, dd)).Render(r.Context(), w)
		return
	}
	http.Redirect(w, r, "/people/"+contactID, http.StatusSeeOther)
}

func (s *Server) handlePeopleDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	d := &people.DetailData{ID: cid}
	var companyID *string
	var created time.Time
	err := s.PG.QueryRow(r.Context(), `
		SELECT ct.full_name, COALESCE(c.name,''), ct.company_id::text, ct.first_name, ct.last_name,
			COALESCE(ct.job_title,''), COALESCE(ct.email,''), COALESCE(ct.phone,''), COALESCE(ct.linkedin_url,''),
			ct.lead_score, ct.source, ct.created_at
		FROM contacts ct LEFT JOIN companies c ON c.id = ct.company_id
		WHERE ct.id=$1 AND ct.tenant_id=$2`,
		cid, id.TenantID).Scan(&d.Name, &d.Company, &companyID, &d.Form.FirstName, &d.Form.LastName,
		&d.Form.JobTitle, &d.Form.Email, &d.Form.Phone, &d.Form.LinkedIn, &d.Score, &d.Source, &created)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	if companyID != nil {
		d.CompanyID = *companyID
	}
	d.Created = created.Format("02 Jan 2006")
	if d.Name == "" {
		d.Name = d.Form.Email
	}
	p := s.page(w, r, d.Name, "/people")
	layouts.AppShell(s.Ren, id, p, people.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handlePeopleUpdate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	cid := r.PathValue("id")
	first := strings.TrimSpace(r.FormValue("first_name"))
	last := strings.TrimSpace(r.FormValue("last_name"))
	full := strings.TrimSpace(first + " " + last)
	email := lead.NormalizeEmail(strings.TrimSpace(r.FormValue("email")))
	if full == "" {
		full = email
	}
	vres, _ := verify.SyntaxVerifier{}.Verify(r.Context(), email)
	res, err := s.PG.Exec(r.Context(), `
		UPDATE contacts SET first_name=$3, last_name=$4, full_name=$5, job_title=$6, email=NULLIF($7,''),
			email_status=$8, phone=NULLIF($9,''), linkedin_url=NULLIF($10,''), updated_at=now()
		WHERE id=$1 AND tenant_id=$2`,
		cid, id.TenantID, first, last, full, strings.TrimSpace(r.FormValue("job_title")),
		email, vres.Status, strings.TrimSpace(r.FormValue("phone")), strings.TrimSpace(r.FormValue("linkedin_url")))
	if err != nil || res.RowsAffected() == 0 {
		webapp.RedirectFlash(w, r, "/people/"+cid, flash.Error, "Could not save changes.")
		return
	}
	webapp.RedirectFlash(w, r, "/people/"+cid, flash.Success, "Contact updated.")
}
