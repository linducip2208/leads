package web

import (
	"net/http"
	"strings"

	"github.com/alexedwards/argon2id"

	"leadforge/internal/audit"
	"leadforge/internal/flash"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/team"
)

func (s *Server) teamRoutes() {
	s.Router.HandleFunc("GET", "/settings/members", s.requirePerm(role.TeamManage, func(w http.ResponseWriter, r *http.Request) {
		s.handleMembers(w, r)
	}))
	s.Router.HandleFunc("POST", "/settings/members", s.requirePerm(role.TeamManage, s.handleMemberAdd))
	s.Router.HandleFunc("POST", "/settings/members/{id}/role", s.requirePerm(role.TeamManage, s.handleMemberRole))
	s.Router.HandleFunc("POST", "/settings/members/{id}/toggle", s.requirePerm(role.TeamManage, s.handleMemberToggle))
	s.Router.HandleFunc("GET", "/settings/roles", s.requirePerm(role.TeamManage, func(w http.ResponseWriter, r *http.Request) {
		s.handleRoles(w, r)
	}))
	s.Router.HandleFunc("POST", "/settings/roles", s.requirePerm(role.TeamManage, s.handleRoleCreate))
	s.Router.HandleFunc("POST", "/settings/roles/{id}/delete", s.requirePerm(role.TeamManage, s.handleRoleDelete))
}

func (s *Server) tenantRoleOptions(r *http.Request) []team.Option {
	rows, err := s.PG.Query(r.Context(), `
		SELECT id::text, name FROM roles WHERE tenant_id IS NULL OR tenant_id=$1 ORDER BY name`, webappIdentity(r).TenantID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []team.Option
	for rows.Next() {
		var o team.Option
		if err := rows.Scan(&o.Value, &o.Label); err == nil {
			out = append(out, o)
		}
	}
	return out
}

func (s *Server) handleMembers(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT u.id::text, u.name, u.email, u.status, to_char(u.created_at,'DD Mon YYYY'),
			COALESCE(string_agg(r.name, ', ' ORDER BY r.name), '')
		FROM users u LEFT JOIN user_roles ur ON ur.user_id = u.id
		LEFT JOIN roles r ON r.id = ur.role_id
		WHERE u.tenant_id=$1 GROUP BY u.id ORDER BY u.created_at`, id.TenantID)
	d := &team.MembersData{Roles: s.tenantRoleOptions(r)}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var m team.Member
			if err := rows.Scan(&m.ID, &m.Name, &m.Email, &m.Status, &m.Joined, &m.Roles); err == nil {
				d.Members = append(d.Members, m)
			}
		}
	}
	p := s.page(w, r, "Members", "/settings/members")
	layouts.AppShell(s.Ren, id, p, team.Members(p, d)).Render(r.Context(), w)
}

func (s *Server) handleMemberAdd(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	password := r.FormValue("password")
	roleID := strings.TrimSpace(r.FormValue("role_id"))
	if name == "" || email == "" || len(password) < 8 {
		s.handleMembers(w, r, "Name, valid email and password (min 8) are required.")
		return
	}
	// role must belong to this tenant or be a system template
	var roleTenant *string
	if roleID != "" {
		if err := s.PG.QueryRow(r.Context(), `SELECT tenant_id::text FROM roles WHERE id=$1`, roleID).Scan(&roleTenant); err != nil {
			s.handleMembers(w, r, "Unknown role.")
			return
		}
		if roleTenant != nil && *roleTenant != id.TenantID {
			s.handleMembers(w, r, "Role belongs to another workspace.")
			return
		}
	}
	hash, err := argon2id.CreateHash(password, argon2id.DefaultParams)
	if err != nil {
		s.handleMembers(w, r, "Could not create the member.")
		return
	}
	tx, err := s.PG.Begin(r.Context())
	if err != nil {
		s.handleMembers(w, r, "Could not create the member.")
		return
	}
	defer tx.Rollback(r.Context())
	var userID string
	if err := tx.QueryRow(r.Context(), `INSERT INTO users (tenant_id, email, password_hash, name, status) VALUES ($1,$2,$3,$4,'active') RETURNING id::text`,
		id.TenantID, email, hash, name).Scan(&userID); err != nil {
		s.handleMembers(w, r, "Email is already registered.")
		return
	}
	if roleID != "" {
		_, _ = tx.Exec(r.Context(), `INSERT INTO user_roles (user_id, role_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, userID, roleID)
	}
	_ = tx.Commit(r.Context())
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "member.add", "user", userID, audit.IP(r))
	webapp.RedirectFlash(w, r, "/settings/members", flash.Success, "Member added.")
}

func (s *Server) handleMemberRole(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	uid, roleID := r.PathValue("id"), strings.TrimSpace(r.FormValue("role_id"))
	if roleID == "" {
		http.Redirect(w, r, "/settings/members", http.StatusSeeOther)
		return
	}
	// both user and role must be in this tenant (or role a system template)
	var n int
	_ = s.PG.QueryRow(r.Context(), `SELECT COUNT(*) FROM users WHERE id=$1 AND tenant_id=$2`, uid, id.TenantID).Scan(&n)
	var roleTenant *string
	var roleSlug string
	_ = s.PG.QueryRow(r.Context(), `SELECT tenant_id::text, slug FROM roles WHERE id=$1`, roleID).Scan(&roleTenant, &roleSlug)
	if n == 0 || roleSlug == role.RoleSuperAdmin || (roleTenant != nil && *roleTenant != id.TenantID) {
		webapp.RedirectFlash(w, r, "/settings/members", flash.Error, "Invalid member or role.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM user_roles WHERE user_id=$1 AND role_id IN (SELECT id FROM roles WHERE tenant_id=$2 OR tenant_id IS NULL)`, uid, id.TenantID)
	_, _ = s.PG.Exec(r.Context(), `INSERT INTO user_roles (user_id, role_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, uid, roleID)
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "member.role", "user", uid, audit.IP(r))
	http.Redirect(w, r, "/settings/members", http.StatusSeeOther)
}

func (s *Server) handleMemberToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	uid := r.PathValue("id")
	if uid == id.UserID {
		webapp.RedirectFlash(w, r, "/settings/members", flash.Error, "You cannot suspend yourself.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `
		UPDATE users SET status = CASE WHEN status='active' THEN 'suspended' ELSE 'active' END
		WHERE id=$1 AND tenant_id=$2`, uid, id.TenantID)
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "member.toggle", "user", uid, audit.IP(r))
	http.Redirect(w, r, "/settings/members", http.StatusSeeOther)
}

func (s *Server) handleRoles(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT r.id::text, r.slug, r.name, r.is_system,
			(SELECT COUNT(*) FROM user_roles ur JOIN users u ON u.id=ur.user_id WHERE ur.role_id=r.id AND (u.tenant_id=$1 OR r.tenant_id IS NULL)),
			COALESCE(string_agg(p.slug, ', ' ORDER BY p.slug), '')
		FROM roles r LEFT JOIN role_permissions rp ON rp.role_id=r.id
		LEFT JOIN permissions p ON p.id=rp.permission_id
		WHERE r.tenant_id IS NULL OR r.tenant_id=$1
		GROUP BY r.id ORDER BY r.is_system DESC, r.name`, id.TenantID)
	d := &team.RolesData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var rr team.RoleRow
			var perms string
			if err := rows.Scan(&rr.ID, &rr.Slug, &rr.Name, &rr.System, &rr.Members, &perms); err == nil {
				if perms != "" {
					rr.Perms = strings.Split(perms, ", ")
				}
				d.Roles = append(d.Roles, rr)
			}
		}
	}
	prows, _ := s.PG.Query(r.Context(), `SELECT slug FROM permissions ORDER BY slug`)
	if prows != nil {
		defer prows.Close()
		for prows.Next() {
			var p string
			if err := prows.Scan(&p); err == nil {
				d.Perms = append(d.Perms, p)
			}
		}
	}
	p := s.page(w, r, "Roles", "/settings/roles")
	layouts.AppShell(s.Ren, id, p, team.Roles(p, d)).Render(r.Context(), w)
}

func (s *Server) handleRoleCreate(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	slug := strings.ToLower(strings.TrimSpace(r.FormValue("slug")))
	perms := r.Form["perms"]
	if name == "" || slug == "" || slug == role.RoleSuperAdmin || slug == "*" {
		s.handleRoles(w, r, "Name and slug are required.")
		return
	}
	for _, p := range perms {
		if p == "*" || p == role.AdminPlatform {
			s.handleRoles(w, r, "Platform administrator permissions are reserved for platform staff.")
			return
		}
	}
	tx, err := s.PG.Begin(r.Context())
	if err != nil {
		s.handleRoles(w, r, "Could not create the role.")
		return
	}
	defer tx.Rollback(r.Context())
	var roleID string
	if err := tx.QueryRow(r.Context(), `INSERT INTO roles (tenant_id, slug, name, is_system) VALUES ($1,$2,$3,false) RETURNING id::text`,
		id.TenantID, slug, name).Scan(&roleID); err != nil {
		s.handleRoles(w, r, "Slug is already used.")
		return
	}
	for _, p := range perms {
		_, _ = tx.Exec(r.Context(), `INSERT INTO role_permissions (role_id, permission_id) SELECT $1, id FROM permissions WHERE slug=$2 ON CONFLICT DO NOTHING`, roleID, p)
	}
	_ = tx.Commit(r.Context())
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "role.create", "role", roleID, audit.IP(r))
	webapp.RedirectFlash(w, r, "/settings/roles", flash.Success, "Role created.")
}

func (s *Server) handleRoleDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM roles WHERE id=$1 AND tenant_id=$2 AND is_system=false`, r.PathValue("id"), id.TenantID)
	audit.Log(r.Context(), s.PG, id.TenantID, id.UserID, "role.delete", "role", r.PathValue("id"), audit.IP(r))
	http.Redirect(w, r, "/settings/roles", http.StatusSeeOther)
}
