package web

import (
	"net/http"
	"strings"

	"leadforge/internal/audit"
	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/admin"
)

func (s *Server) adminPlatformRoutes() {
	s.Router.HandleFunc("GET", "/admin/tenants", s.requirePerm(role.AdminPlatform, s.handleAdminTenants))
	s.Router.HandleFunc("GET", "/admin/tenants/{id}", s.requirePerm(role.AdminPlatform, s.handleAdminTenantDetail))
	s.Router.HandleFunc("POST", "/admin/tenants/{id}/status", s.requirePerm(role.AdminPlatform, s.handleAdminTenantStatus))
	s.Router.HandleFunc("POST", "/admin/tenants/{id}/plan", s.requirePerm(role.AdminPlatform, s.handleAdminTenantPlan))
	s.Router.HandleFunc("GET", "/admin/plans", s.requirePerm(role.AdminPlatform, s.handleAdminPlans))
	s.Router.HandleFunc("GET", "/admin/users", s.requirePerm(role.AdminPlatform, s.handleAdminUsers))
	s.Router.HandleFunc("POST", "/admin/users/{id}/toggle", s.requirePerm(role.AdminPlatform, s.handleAdminUserToggle))
	s.Router.HandleFunc("GET", "/admin/usage", s.requirePerm(role.AdminPlatform, s.handleAdminUsage))
	s.Router.HandleFunc("GET", "/admin/logs", s.requirePerm(role.AdminPlatform, s.handleAdminLogs))
}

func (s *Server) handleAdminTenants(w http.ResponseWriter, r *http.Request) {
	rows, err := s.PG.Query(r.Context(), `
		SELECT t.id::text, t.name, t.slug, t.status, COALESCE(p.name,'—'), to_char(t.created_at,'DD Mon YYYY'),
			(SELECT COUNT(*) FROM users u WHERE u.tenant_id=t.id),
			(SELECT COUNT(*) FROM leads l WHERE l.tenant_id=t.id)
		FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id=t.id LEFT JOIN plans p ON p.id=s.plan_id
		ORDER BY t.created_at DESC LIMIT 200`)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load tenants.", Err: err})
		return
	}
	defer rows.Close()
	d := &admin.TenantsData{}
	for rows.Next() {
		var t admin.TenantRow
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.Status, &t.Plan, &t.Created, &t.Users, &t.Leads); err == nil {
			d.Tenants = append(d.Tenants, t)
		}
	}
	p := s.page(w, r, "Tenants", "/admin/tenants")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Tenants(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminTenantDetail(w http.ResponseWriter, r *http.Request) {
	tid := r.PathValue("id")
	d := &admin.TenantDetailData{}
	var planID *string
	err := s.PG.QueryRow(r.Context(), `
		SELECT t.id::text, t.name, t.slug, t.status, COALESCE(p.name,'—'), to_char(t.created_at,'DD Mon YYYY'),
			(SELECT COUNT(*) FROM users u WHERE u.tenant_id=t.id),
			(SELECT COUNT(*) FROM leads l WHERE l.tenant_id=t.id), s.plan_id::text
		FROM tenants t LEFT JOIN subscriptions s ON s.tenant_id=t.id LEFT JOIN plans p ON p.id=s.plan_id
		WHERE t.id=$1`, tid).Scan(&d.Tenant.ID, &d.Tenant.Name, &d.Tenant.Slug, &d.Tenant.Status,
		&d.Tenant.Plan, &d.Tenant.Created, &d.Tenant.Users, &d.Tenant.Leads, &planID)
	if err != nil {
		s.renderError(w, r, webapp.ErrNotFound)
		return
	}
	prows, _ := s.PG.Query(r.Context(), `SELECT id::text, slug, name, price_monthly::text || ' ' || currency, limits FROM plans WHERE is_active ORDER BY position`)
	if prows != nil {
		defer prows.Close()
		for prows.Next() {
			var pl admin.PlanRow
			var limits []byte
			if err := prows.Scan(&pl.ID, &pl.Slug, &pl.Name, &pl.Price, &limits); err == nil {
				pl.Limits = shortLimits(string(limits))
				pl.Current = planID != nil && *planID == pl.ID
				d.Plans = append(d.Plans, pl)
			}
		}
	}
	p := s.page(w, r, d.Tenant.Name, "/admin/tenants")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.TenantDetail(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminTenantStatus(w http.ResponseWriter, r *http.Request) {
	tid := r.PathValue("id")
	status := strings.TrimSpace(r.FormValue("status"))
	if status != "active" && status != "suspended" {
		http.Redirect(w, r, "/admin/tenants/"+tid, http.StatusSeeOther)
		return
	}
	_, _ = s.PG.Exec(r.Context(), `UPDATE tenants SET status=$2, updated_at=now() WHERE id=$1`, tid, status)
	audit.Log(r.Context(), s.PG, "", webappIdentity(r).UserID, "tenant.status", "tenant", tid, audit.IP(r))
	http.Redirect(w, r, "/admin/tenants/"+tid, http.StatusSeeOther)
}

func (s *Server) handleAdminTenantPlan(w http.ResponseWriter, r *http.Request) {
	tid := r.PathValue("id")
	planID := strings.TrimSpace(r.FormValue("plan_id"))
	_, _ = s.PG.Exec(r.Context(), `
		INSERT INTO subscriptions (tenant_id, plan_id, status) VALUES ($1,$2::uuid,'active')
		ON CONFLICT (tenant_id) DO UPDATE SET plan_id=$2::uuid, status='active'`, tid, planID)
	audit.Log(r.Context(), s.PG, "", webappIdentity(r).UserID, "tenant.plan", "tenant", tid, audit.IP(r))
	webapp.RedirectFlash(w, r, "/admin/tenants/"+tid, flash.Success, "Plan assigned.")
}

func (s *Server) handleAdminPlans(w http.ResponseWriter, r *http.Request) {
	rows, _ := s.PG.Query(r.Context(), `SELECT id::text, slug, name, price_monthly::text || ' ' || currency, limits FROM plans ORDER BY position`)
	d := &admin.PlansData{}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var pl admin.PlanRow
			var limits []byte
			if err := rows.Scan(&pl.ID, &pl.Slug, &pl.Name, &pl.Price, &limits); err == nil {
				pl.Limits = shortLimits(string(limits))
				d.Plans = append(d.Plans, pl)
			}
		}
	}
	p := s.page(w, r, "Plans", "/admin/plans")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Plans(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	rows, err := s.PG.Query(r.Context(), `
		SELECT u.id::text, u.name, u.email, COALESCE(t.name,'—'), u.status, u.is_super_admin, to_char(u.created_at,'DD Mon YYYY')
		FROM users u LEFT JOIN tenants t ON t.id=u.tenant_id ORDER BY u.created_at DESC LIMIT 200`)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load users.", Err: err})
		return
	}
	defer rows.Close()
	d := &admin.UsersData{}
	for rows.Next() {
		var u admin.AdminUserRow
		if err := rows.Scan(&u.ID, &u.Name, &u.Email, &u.Tenant, &u.Status, &u.Super, &u.Joined); err == nil {
			d.Users = append(d.Users, u)
		}
	}
	p := s.page(w, r, "Users", "/admin/users")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Users(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminUserToggle(w http.ResponseWriter, r *http.Request) {
	uid := r.PathValue("id")
	if uid == webappIdentity(r).UserID {
		webapp.RedirectFlash(w, r, "/admin/users", flash.Error, "You cannot suspend yourself.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		UPDATE users SET status = CASE WHEN status='active' THEN 'suspended' ELSE 'active' END WHERE id=$1`, uid)
	audit.Log(r.Context(), s.PG, "", webappIdentity(r).UserID, "user.toggle", "user", uid, audit.IP(r))
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (s *Server) handleAdminUsage(w http.ResponseWriter, r *http.Request) {
	rows, err := s.PG.Query(r.Context(), `
		SELECT t.name,
			COALESCE(SUM((u.kind='search')::int * u.quantity),0),
			COALESCE(SUM((u.kind='crawl')::int * u.quantity),0),
			COALESCE(SUM((u.kind='enrichment')::int * u.quantity),0),
			COALESCE(SUM((u.kind='email')::int * u.quantity),0),
			COALESCE((SELECT SUM(amount) FROM credit_transactions ct WHERE ct.tenant_id=t.id),0)
		FROM tenants t LEFT JOIN usage_events u ON u.tenant_id=t.id AND u.created_at > date_trunc('month', now())
		GROUP BY t.id ORDER BY 2 DESC LIMIT 100`)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load usage.", Err: err})
		return
	}
	defer rows.Close()
	d := &admin.UsageData{}
	for rows.Next() {
		var u admin.UsageRow
		if err := rows.Scan(&u.Tenant, &u.Search, &u.Crawl, &u.Enrich, &u.Email, &u.Credits); err == nil {
			d.Rows = append(d.Rows, u)
		}
	}
	p := s.page(w, r, "Usage", "/admin/usage")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Usage(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminLogs(w http.ResponseWriter, r *http.Request) {
	action := strings.TrimSpace(r.URL.Query().Get("action"))
	q := `SELECT a.action, COALESCE(a.entity_type,''), COALESCE(a.entity_id,''), COALESCE(u.name,'system'),
		COALESCE(t.name,''), COALESCE(a.ip,''), to_char(a.created_at,'DD Mon HH24:MI')
		FROM audit_logs a LEFT JOIN users u ON u.id=a.user_id LEFT JOIN tenants t ON t.id=a.tenant_id`
	var args []any
	if action != "" {
		q += " WHERE a.action ILIKE $1"
		args = append(args, "%"+action+"%")
	}
	q += " ORDER BY a.created_at DESC LIMIT 200"
	rows, err := s.PG.Query(r.Context(), q, args...)
	if err != nil {
		s.renderError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "Could not load logs.", Err: err})
		return
	}
	defer rows.Close()
	d := &admin.LogsData{Action: action}
	for rows.Next() {
		var l admin.LogRow
		if err := rows.Scan(&l.Action, &l.Entity, &l.Target, &l.User, &l.Tenant, &l.IP, &l.When); err == nil {
			d.Logs = append(d.Logs, l)
		}
	}
	p := s.page(w, r, "Audit Logs", "/admin/logs")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Logs(p, d)).Render(r.Context(), w)
}

func shortLimits(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) > 140 {
		return raw[:140] + "…"
	}
	return raw
}
