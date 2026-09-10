package web

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"leadforge/internal/crypto"
	"leadforge/internal/flash"
	"leadforge/internal/mail"
	"leadforge/internal/role"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	"leadforge/web/pages/settings"
)

func (s *Server) settingsRoutes() {
	s.Router.HandleFunc("GET", "/settings/workspace", s.requirePerm(role.TenantManage, func(w http.ResponseWriter, r *http.Request) {
		s.handleWorkspace(w, r)
	}))
	s.Router.HandleFunc("POST", "/settings/workspace", s.requirePerm(role.TenantManage, s.handleWorkspaceSave))
	s.Router.HandleFunc("GET", "/settings/email-accounts", s.requirePerm(role.IntegrationMange, func(w http.ResponseWriter, r *http.Request) {
		s.handleEmailAccounts(w, r)
	}))
	s.Router.HandleFunc("POST", "/settings/email-accounts", s.requirePerm(role.IntegrationMange, s.handleEmailAccountAdd))
	s.Router.HandleFunc("POST", "/settings/email-accounts/{id}/test", s.requirePerm(role.IntegrationMange, s.handleEmailAccountTest))
	s.Router.HandleFunc("POST", "/settings/email-accounts/{id}/toggle", s.requirePerm(role.IntegrationMange, s.handleEmailAccountToggle))
	s.Router.HandleFunc("POST", "/settings/email-accounts/{id}/delete", s.requirePerm(role.IntegrationMange, s.handleEmailAccountDelete))
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	d := &settings.WorkspaceData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	_ = s.PG.QueryRow(r.Context(), `SELECT name, slug FROM tenants WHERE id=$1`, id.TenantID).Scan(&d.Name, &d.Slug)
	p := s.page(w, r, "Workspace", "/settings/workspace")
	layouts.AppShell(s.Ren, id, p, settings.Workspace(p, d)).Render(r.Context(), w)
}

func (s *Server) handleWorkspaceSave(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.handleWorkspace(w, r, "Name is required.")
		return
	}
	_, _ = s.PG.Exec(r.Context(), `UPDATE tenants SET name=$2, updated_at=now() WHERE id=$1`, id.TenantID, name)
	webapp.RedirectFlash(w, r, "/settings/workspace", flash.Success, "Workspace updated.")
}

func (s *Server) handleEmailAccounts(w http.ResponseWriter, r *http.Request, errMsg ...string) {
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT id::text, name, from_email, provider, daily_limit, hourly_limit, is_active, status, last_error
		FROM email_accounts WHERE tenant_id=$1 ORDER BY created_at`, id.TenantID)
	d := &settings.EmailAccountsData{}
	if len(errMsg) > 0 {
		d.Error = errMsg[0]
	}
	if rows != nil {
		defer rows.Close()
		for rows.Next() {
			var a settings.EmailAccountRow
			if err := rows.Scan(&a.ID, &a.Name, &a.FromEmail, &a.Provider, &a.Daily, &a.Hourly, &a.Active, &a.Status, &a.LastError); err == nil {
				d.Accounts = append(d.Accounts, a)
			}
		}
	}
	p := s.page(w, r, "Email Accounts", "/settings/email-accounts")
	layouts.AppShell(s.Ren, id, p, settings.EmailAccounts(p, d)).Render(r.Context(), w)
}

func (s *Server) handleEmailAccountAdd(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	name := strings.TrimSpace(r.FormValue("name"))
	host := strings.TrimSpace(r.FormValue("smtp_host"))
	from := strings.TrimSpace(r.FormValue("from_email"))
	if name == "" || host == "" || from == "" {
		s.handleEmailAccounts(w, r, "Name, SMTP host and from email are required.")
		return
	}
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("smtp_port")))
	if port <= 0 {
		port = 587
	}
	enc := strings.TrimSpace(r.FormValue("smtp_encryption"))
	if enc != "ssl" && enc != "none" {
		enc = "starttls"
	}
	hourly, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("hourly_limit")))
	daily, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("daily_limit")))
	if hourly <= 0 {
		hourly = 20
	}
	if daily <= 0 {
		daily = 200
	}
	var passEnc []byte
	if pw := r.FormValue("smtp_password"); pw != "" {
		encStr, err := crypto.Encrypt(s.Cfg.Secret, pw)
		if err != nil {
			s.handleEmailAccounts(w, r, "Could not encrypt the password.")
			return
		}
		passEnc = []byte(encStr)
	}
	_, err := s.PG.Exec(r.Context(), `
		INSERT INTO email_accounts (tenant_id, name, provider, from_name, from_email, smtp_host, smtp_port, smtp_encryption, smtp_username, smtp_password_enc, daily_limit, hourly_limit)
		VALUES ($1,$2,'smtp',$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
		id.TenantID, name, strings.TrimSpace(r.FormValue("from_name")), from,
		host, port, enc, strings.TrimSpace(r.FormValue("smtp_username")), passEnc, daily, hourly)
	if err != nil {
		s.handleEmailAccounts(w, r, "Could not save the account.")
		return
	}
	webapp.RedirectFlash(w, r, "/settings/email-accounts", flash.Success, "Account added. Use Test to verify delivery.")
}

// loadMailAccount decrypts an email account for sending/testing.
func (s *Server) loadMailAccount(ctx context.Context, tenantID, accountID string) (mail.Account, string, error) {
	var a mail.Account
	var name string
	var passEnc []byte
	err := s.PG.QueryRow(ctx, `
		SELECT name, from_name, from_email, smtp_host, smtp_port, smtp_encryption,
			COALESCE(smtp_username,''), smtp_password_enc
		FROM email_accounts WHERE id=$1 AND tenant_id=$2 AND is_active`,
		accountID, tenantID).Scan(&name, &a.FromName, &a.FromEmail, &a.Host, &a.Port,
		&a.Encryption, &a.Username, &passEnc)
	if err != nil {
		return a, "", err
	}
	if len(passEnc) > 0 {
		pw, err := crypto.Decrypt(s.Cfg.Secret, string(passEnc))
		if err != nil {
			return a, "", err
		}
		a.Password = pw
	}
	return a, name, nil
}

func (s *Server) handleEmailAccountTest(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	aid := r.PathValue("id")
	acc, _, err := s.loadMailAccount(r.Context(), id.TenantID, aid)
	if err != nil {
		webapp.RedirectFlash(w, r, "/settings/email-accounts", flash.Error, "Account not found or disabled.")
		return
	}
	if err := (mail.SMTPSender{}).Check(acc); err != nil {
		msg := err.Error()
		if len(msg) > 200 {
			msg = msg[:200]
		}
		_, _ = s.PG.Exec(r.Context(), `UPDATE email_accounts SET status='error', last_error=$3 WHERE id=$1 AND tenant_id=$2`, aid, id.TenantID, msg)
		webapp.RedirectFlash(w, r, "/settings/email-accounts", flash.Error, "Connection failed: "+msg)
		return
	}
	_, _ = s.PG.Exec(r.Context(), `UPDATE email_accounts SET status='connected', last_error='' WHERE id=$1 AND tenant_id=$2`, aid, id.TenantID)
	webapp.RedirectFlash(w, r, "/settings/email-accounts", flash.Success, "Connected — credentials accepted by "+acc.Host+".")
}

func (s *Server) handleEmailAccountToggle(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `UPDATE email_accounts SET is_active = NOT is_active WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/settings/email-accounts", http.StatusSeeOther)
}

func (s *Server) handleEmailAccountDelete(w http.ResponseWriter, r *http.Request) {
	id := webappIdentity(r)
	_, _ = s.PG.Exec(r.Context(), `DELETE FROM email_accounts WHERE id=$1 AND tenant_id=$2`, r.PathValue("id"), id.TenantID)
	http.Redirect(w, r, "/settings/email-accounts", http.StatusSeeOther)
}
