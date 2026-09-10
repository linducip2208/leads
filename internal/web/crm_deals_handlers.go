package web

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"leadforge/internal/flash"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/deals"
)

func (s *Server) handleDealList(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "open"
	}
	q := `SELECT dl.id::text, dl.title, COALESCE(c.name,''), COALESCE(ps.name, dl.status),
		dl.value::text || ' ' || dl.currency, dl.probability,
		COALESCE(to_char(dl.expected_close_at,'DD Mon YYYY'),''), COALESCE(u.name,''), dl.status
		FROM deals dl
		LEFT JOIN companies c ON c.id = dl.company_id
		LEFT JOIN pipeline_stages ps ON ps.id = dl.stage_id
		LEFT JOIN users u ON u.id = dl.owner_id
		WHERE dl.tenant_id=$1`
	args := []any{id.TenantID}
	if status != "" {
		q += " AND dl.status=$2"
		args = append(args, status)
	}
	q += " ORDER BY dl.updated_at DESC LIMIT 200"
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load deals.", Err: err})
		return
	}
	defer rows.Close()
	d := &deals.ListData{Status: status}
	for rows.Next() {
		var it deals.Item
		if err := rows.Scan(&it.ID, &it.Title, &it.Company, &it.Stage, &it.Value, &it.Prob, &it.Close, &it.Owner, &it.Status); err == nil {
			d.Items = append(d.Items, it)
		}
	}
	p := s.page(w, r, "Deals", "/deals")
	layouts.AppShell(s.Ren, id, p, deals.List(p, d)).Render(r.Context(), w)
}

func (s *Server) dealFormOptions(r *http.Request, pipelineID string) (companies, stages, owners, leadOpts []deals.Option) {
	tid := webappIdentity(r).TenantID
	crows, _ := s.PG.Query(r.Context(), `SELECT id::text, name FROM companies WHERE tenant_id=$1 ORDER BY name LIMIT 500`, tid)
	if crows != nil {
		defer crows.Close()
		for crows.Next() {
			var o deals.Option
			if err := crows.Scan(&o.Value, &o.Label); err == nil {
				companies = append(companies, o)
			}
		}
	}
	if pipelineID == "" {
		pipelineID = s.ensurePipelineCtx(r.Context(), tid)
	}
	srows, _ := s.PG.Query(r.Context(), `SELECT id::text, name FROM pipeline_stages WHERE pipeline_id=$1::uuid ORDER BY position`, pipelineID)
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var o deals.Option
			if err := srows.Scan(&o.Value, &o.Label); err == nil {
				stages = append(stages, o)
			}
		}
	}
	for _, kv := range s.ownerKVs(r) {
		owners = append(owners, deals.Option{Value: kv.Value, Label: kv.Label})
	}
	lrows, _ := s.PG.Query(r.Context(), `
		SELECT l.id::text, c.name FROM leads l JOIN companies c ON c.id=l.company_id
		WHERE l.tenant_id=$1 AND l.status<>'archived' ORDER BY l.created_at DESC LIMIT 200`, tid)
	if lrows != nil {
		defer lrows.Close()
		for lrows.Next() {
			var o deals.Option
			if err := lrows.Scan(&o.Value, &o.Label); err == nil {
				leadOpts = append(leadOpts, o)
			}
		}
	}
	return companies, stages, owners, leadOpts
}

func (s *Server) handleDealNew(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	pid := s.ensurePipelineCtx(r.Context(), id.TenantID)
	companies, stages, owners, _ := s.dealFormOptions(r, pid)
	d := &deals.NewData{Companies: companies, Stages: stages, Owners: owners}
	d.Form.Currency = "IDR"
	if len(stages) > 0 {
		d.Form.StageID = stages[0].Value
	}
	if leadID := r.URL.Query().Get("lead"); leadID != "" {
		var title, companyID, companyName, contactID string
		_ = s.PG.QueryRow(r.Context(), `
			SELECT c.name || ' — deal', l.company_id::text, c.name, l.primary_contact_id::text
			FROM leads l JOIN companies c ON c.id=l.company_id
			WHERE l.id=$1::uuid AND l.tenant_id=$2`, leadID, id.TenantID).Scan(&title, &companyID, &companyName, &contactID)
		if companyID != "" {
			d.Form.LeadID, d.Form.Title, d.Form.CompanyID, d.Form.CompanyName, d.Form.ContactID = leadID, title, companyID, companyName, contactID
		}
	}
	p := s.page(w, r, "New Deal", "/deals")
	layouts.AppShell(s.Ren, id, p, deals.New(p, d)).Render(r.Context(), w)
}

func (s *Server) handleDealCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	title := strings.TrimSpace(r.FormValue("title"))
	companyID := strings.TrimSpace(r.FormValue("company_id"))
	stageID := strings.TrimSpace(r.FormValue("stage_id"))
	if title == "" || companyID == "" || stageID == "" {
		pid := s.ensurePipelineCtx(r.Context(), id.TenantID)
		companies, stages, owners, _ := s.dealFormOptions(r, pid)
		p := s.page(w, r, "New Deal", "/deals")
		dd := &deals.NewData{Companies: companies, Stages: stages, Owners: owners}
		dd.Form.Error = "Title, company and stage are required."
		dd.Form.Title = title
		layouts.AppShell(s.Ren, id, p, deals.New(p, dd)).Render(r.Context(), w)
		return
	}
	value, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("value")), 64)
	prob, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("probability")))
	currency := strings.ToUpper(strings.TrimSpace(r.FormValue("currency")))
	if currency == "" {
		currency = "IDR"
	}
	var pipelineID, contactID, leadID, ownerID, expected any
	_ = s.PG.QueryRow(r.Context(), `SELECT pipeline_id::text FROM pipeline_stages WHERE id=$1::uuid`, stageID).Scan(&pipelineID)
	if c := strings.TrimSpace(r.FormValue("contact_id")); c != "" {
		contactID = c
	} else {
		_ = s.PG.QueryRow(r.Context(), `SELECT id::text FROM contacts WHERE company_id=$1::uuid AND tenant_id=$2 ORDER BY created_at LIMIT 1`, companyID, id.TenantID).Scan(&contactID)
	}
	if l := strings.TrimSpace(r.FormValue("lead_id")); l != "" {
		leadID = l
	} else {
		_ = s.PG.QueryRow(r.Context(), `SELECT id::text FROM leads WHERE company_id=$1::uuid AND tenant_id=$2 LIMIT 1`, companyID, id.TenantID).Scan(&leadID)
	}
	if o := strings.TrimSpace(r.FormValue("owner_id")); o != "" {
		ownerID = o
	} else {
		ownerID = id.UserID
	}
	if e := strings.TrimSpace(r.FormValue("expected_close")); e != "" {
		expected = e
	}
	var dealID string
	err := s.PG.QueryRow(r.Context(), `
		INSERT INTO deals (tenant_id, pipeline_id, stage_id, company_id, contact_id, lead_id, title, value, currency, probability, expected_close_at, owner_id)
		VALUES ($1,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8,$9,$10,$11::date,$12::uuid) RETURNING id::text`,
		id.TenantID, pipelineID, stageID, companyID, contactID, leadID, title, value, currency, prob, expected, ownerID).Scan(&dealID)
	if err != nil {
		s.Log.Error("create deal", "err", err)
		webapp.RedirectFlash(w, r, "/deals/new", flash.Error, "Could not create the deal.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO deal_activities (tenant_id, deal_id, kind, body, user_id)
		VALUES ($1,$2::uuid,'created',$3,$4)`, id.TenantID, dealID, "Deal created", id.UserID)
	http.Redirect(w, r, "/deals/"+dealID, http.StatusSeeOther)
}

func (s *Server) handleDealDetail(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	did := r.PathValue("id")
	d := &deals.DetailData{ID: did}
	var expected *time.Time
	var created time.Time
	err := s.PG.QueryRow(r.Context(), `
		SELECT dl.title, COALESCE(c.name,''), dl.company_id::text, COALESCE(ct.full_name,''),
			dl.value::text, dl.currency, dl.probability, COALESCE(ps.name, dl.status), dl.stage_id::text,
			dl.pipeline_id::text, dl.expected_close_at, COALESCE(u.name,''), dl.status,
			COALESCE(dl.lost_reason,''), dl.created_at
		FROM deals dl
		LEFT JOIN companies c ON c.id = dl.company_id
		LEFT JOIN contacts ct ON ct.id = dl.contact_id
		LEFT JOIN pipeline_stages ps ON ps.id = dl.stage_id
		LEFT JOIN users u ON u.id = dl.owner_id
		WHERE dl.id=$1::uuid AND dl.tenant_id=$2`,
		did, id.TenantID).Scan(&d.Title, &d.Company, &d.CompanyID, &d.Contact, &d.Value,
		&d.Currency, &d.Prob, &d.Stage, &d.StageID, &d.PipelineID, &expected, &d.Owner,
		&d.Status, &d.LostReason, &created)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	d.ValueNum = d.Value
	d.Value += " " + d.Currency
	if expected != nil {
		d.Expected = expected.Format("02 Jan 2006")
		d.ExpectedISO = expected.Format("2006-01-02")
	}
	d.Created = created.Format("02 Jan 2006")
	srows, _ := s.PG.Query(r.Context(), `SELECT id::text, name FROM pipeline_stages WHERE pipeline_id=$1::uuid ORDER BY position`, d.PipelineID)
	if srows != nil {
		defer srows.Close()
		for srows.Next() {
			var o deals.Option
			if err := srows.Scan(&o.Value, &o.Label); err == nil {
				d.Stages = append(d.Stages, o)
			}
		}
	}
	for _, kv := range s.ownerKVs(r) {
		d.Owners = append(d.Owners, deals.Option{Value: kv.Value, Label: kv.Label})
	}
	trows, _ := s.PG.Query(r.Context(), `
		SELECT da.kind, da.body, COALESCE(u.name,'System'), to_char(da.created_at,'DD Mon HH24:MI')
		FROM deal_activities da LEFT JOIN users u ON u.id = da.user_id
		WHERE da.deal_id=$1::uuid ORDER BY da.created_at DESC LIMIT 30`, did)
	if trows != nil {
		defer trows.Close()
		for trows.Next() {
			var t deals.TimeRow
			if err := trows.Scan(&t.Kind, &t.Body, &t.Who, &t.When); err == nil {
				d.Timeline = append(d.Timeline, t)
			}
		}
	}
	p := s.page(w, r, d.Title, "/deals")
	layouts.AppShell(s.Ren, id, p, deals.Detail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleDealUpdate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	did := r.PathValue("id")
	value, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("value")), 64)
	prob, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("probability")))
	status := strings.TrimSpace(r.FormValue("status"))
	if status != "open" && status != "won" && status != "lost" {
		status = "open"
	}
	var expected any
	if e := strings.TrimSpace(r.FormValue("expected_close")); e != "" {
		expected = e
	}
	lost := strings.TrimSpace(r.FormValue("lost_reason"))
	res, err := s.PG.Exec(r.Context(), `
		UPDATE deals SET value=$3, probability=$4, status=$5, expected_close_at=$6::date,
			lost_reason=$7, closed_at = CASE WHEN $5 IN ('won','lost') THEN COALESCE(closed_at, now()) ELSE NULL END,
			updated_at=now()
		WHERE id=$1::uuid AND tenant_id=$2`, did, id.TenantID, value, prob, status, expected, lost)
	if err != nil || res.RowsAffected() == 0 {
		webapp.RedirectFlash(w, r, "/deals/"+did, flash.Error, "Could not update the deal.")
		return
	}
	webapp.RedirectFlash(w, r, "/deals/"+did, flash.Success, "Deal updated.")
}

func (s *Server) handleDealStage(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	did := r.PathValue("id")
	stageID := strings.TrimSpace(r.FormValue("stage_id"))
	var kind, sname, pipelineID string
	err := s.PG.QueryRow(r.Context(), `
		SELECT ps.kind, ps.name, ps.pipeline_id::text FROM pipeline_stages ps
		JOIN pipelines p ON p.id = ps.pipeline_id
		WHERE ps.id=$1::uuid AND p.tenant_id=$2`, stageID, id.TenantID).Scan(&kind, &sname, &pipelineID)
	if err != nil {
		webapp.RedirectFlash(w, r, "/deals/"+did, flash.Error, "Invalid stage.")
		return
	}
	status := "open"
	var closed any
	if kind == "won" || kind == "lost" {
		status = kind
		closed = time.Now()
	}
	_, err = s.PG.Exec(r.Context(), `
		UPDATE deals SET stage_id=$3::uuid, pipeline_id=$4::uuid, status=$5, closed_at=$6::timestamptz, updated_at=now()
		WHERE id=$1::uuid AND tenant_id=$2`, did, id.TenantID, stageID, pipelineID, status, closed)
	if err != nil {
		webapp.RedirectFlash(w, r, "/deals/"+did, flash.Error, "Could not move the deal.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO deal_activities (tenant_id, deal_id, kind, body, user_id)
		VALUES ($1,$2::uuid,'stage_changed',$3,$4)`, id.TenantID, did, "Moved to "+sname, id.UserID)
	webapp.RedirectFlash(w, r, "/deals/"+did, flash.Success, "Deal moved to "+sname+".")
}
