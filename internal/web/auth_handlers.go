package web

import (
	"errors"
	"net/http"
	"strings"

	"leadforge/internal/auth"
	"leadforge/internal/flash"
	"leadforge/internal/httpx"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
	authpages "leadforge/web/pages/auth"
)

var loginLimiter = httpx.NewRateLimiter(30, 10*60*1000*1000*1000) // 30 per 10min

func (s *Server) authRoutes() {
	s.Router.HandleFunc("GET", "/login", s.handleLoginPage)
	s.Router.HandleFunc("POST", "/login", s.handleLoginSubmit)
	s.Router.HandleFunc("GET", "/register", s.handleRegisterPage)
	s.Router.HandleFunc("POST", "/register", s.handleRegisterSubmit)
	s.Router.HandleFunc("GET", "/logout", s.handleLogout)
	s.Router.HandleFunc("POST", "/logout", s.handleLogout)
	s.Router.HandleFunc("GET", "/forgot-password", s.handleForgotPage)
	s.Router.HandleFunc("POST", "/forgot-password", s.handleForgotSubmit)
	s.Router.HandleFunc("GET", "/reset-password", s.handleResetPage)
	s.Router.HandleFunc("POST", "/reset-password", s.handleResetSubmit)
	s.Router.HandleFunc("GET", "/verify-email", s.handleVerifyEmail)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if d, _ := s.Session.Get(r.Context(), r); d != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	p := s.authPage(w, r, "Sign in")
	layouts.AuthLayout(s.Ren, "Sign in", authpages.LoginPage(p, "", "")).Render(r.Context(), w)
}

func (s *Server) handleLoginSubmit(w http.ResponseWriter, r *http.Request) {
	if !loginLimiter.Allow(httpx.ClientIP(r)) {
		s.renderHTTPError(w, r, &webapp.HTTPError{Status: 429, Title: "Too many attempts", Message: "Please wait a few minutes and try again."})
		return
	}
	email := r.FormValue("email")
	password := r.FormValue("password")
	u, err := s.Auth.Login(r.Context(), email, password)
	if err != nil {
		msg := "We couldn't sign you in. Check your email and password."
		if errors.Is(err, auth.ErrUserSuspended) {
			msg = "This account has been suspended. Contact your workspace admin."
		}
		p := s.authPage(w, r, "Sign in")
		layouts.AuthLayout(s.Ren, "Sign in", authpages.LoginPage(p, email, msg)).Render(r.Context(), w)
		return
	}
	if err := s.Session.Start(w, r, u.ID, u.TenantID); err != nil {
		s.renderHTTPError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "We couldn't start your session. Please try again."})
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleRegisterPage(w http.ResponseWriter, r *http.Request) {
	p := s.authPage(w, r, "Create account")
	layouts.AuthLayout(s.Ren, "Create account", authpages.RegisterPage(p, "", "", "", "")).Render(r.Context(), w)
}

func (s *Server) handleRegisterSubmit(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	org := strings.TrimSpace(r.FormValue("workspace"))
	fail := func(msg string) {
		p := s.authPage(w, r, "Create account")
		layouts.AuthLayout(s.Ren, "Create account", authpages.RegisterPage(p, name, email, org, msg)).Render(r.Context(), w)
	}
	if name == "" || email == "" || org == "" {
		fail("All fields are required.")
		return
	}
	u, err := s.Auth.Register(r.Context(), name, email, password, org)
	if err != nil {
		msg := "We couldn't create your account. Try a stronger password (min 8 characters)."
		if errors.Is(err, auth.ErrEmailTaken) {
			msg = "That email is already registered. Try signing in instead."
		}
		fail(msg)
		return
	}
	// best-effort verification email
	if tok, err := s.Auth.CreateEmailVerification(r.Context(), u.ID); err == nil && tok != "" {
		s.mailVerifyLink(u.Email, tok)
	}
	if err := s.Session.Start(w, r, u.ID, u.TenantID); err != nil {
		s.renderHTTPError(w, r, &webapp.HTTPError{Status: 500, Title: "Something went wrong", Message: "We couldn't start your session. Please try again."})
		return
	}
	flash.Set(w, flash.Success, "Welcome to LeadForge! Your workspace is ready.")
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.Session.Destroy(w, r)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleForgotPage(w http.ResponseWriter, r *http.Request) {
	p := s.authPage(w, r, "Forgot password")
	layouts.AuthLayout(s.Ren, "Forgot password", authpages.ForgotPage(p, "")).Render(r.Context(), w)
}

func (s *Server) handleForgotSubmit(w http.ResponseWriter, r *http.Request) {
	email := strings.TrimSpace(r.FormValue("email"))
	token, _ := s.Auth.CreatePasswordReset(r.Context(), email)
	if token != "" {
		s.mailResetLink(email, token)
	}
	// identical response whether or not the account exists
	layouts.AuthLayout(s.Ren, "Check your email", authpages.ForgotSentPage(email)).Render(r.Context(), w)
}

func (s *Server) handleResetPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	p := s.authPage(w, r, "Reset password")
	layouts.AuthLayout(s.Ren, "Reset password", authpages.ResetPage(p, token, "")).Render(r.Context(), w)
}

func (s *Server) handleResetSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("token")
	password := r.FormValue("password")
	if err := s.Auth.ResetPassword(r.Context(), token, password); err != nil {
		msg := "We couldn't reset your password. Use at least 8 characters."
		if errors.Is(err, auth.ErrInvalidToken) {
			msg = "This reset link is invalid or has expired. Request a new one."
		}
		p := s.authPage(w, r, "Reset password")
		layouts.AuthLayout(s.Ren, "Reset password", authpages.ResetPage(p, token, msg)).Render(r.Context(), w)
		return
	}
	flash.Set(w, flash.Success, "Password updated. You can sign in now.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) handleVerifyEmail(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if err := s.Auth.MarkEmailVerified(r.Context(), token); err != nil {
		flash.Set(w, flash.Error, "That verification link is invalid or has expired.")
	} else {
		flash.Set(w, flash.Success, "Email verified. Thank you!")
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) authPage(w http.ResponseWriter, r *http.Request, title string) authpages.PageData {
	flash.ClearCookie(w)
	return authpages.PageData{
		Title:     title,
		Flash:     toFlash(flash.Pop(r)),
		CSRFToken: s.CSRF.Token("auth:" + title),
	}
}

func toFlash(msgs []flash.Message) []authpages.FlashMsg {
	out := make([]authpages.FlashMsg, len(msgs))
	for i, m := range msgs {
		out[i] = authpages.FlashMsg{Kind: m.Kind, Text: m.Text}
	}
	return out
}

func (s *Server) renderHTTPError(w http.ResponseWriter, r *http.Request, he *webapp.HTTPError) {
	s.renderError(w, r, he)
}

func (s *Server) mailVerifyLink(email, token string) {
	s.Log.Info("verification email", "to", email, "link", s.appURL()+"/verify-email?token="+token)
}

func (s *Server) mailResetLink(email, token string) {
	s.Log.Info("password reset email", "to", email, "link", s.appURL()+"/reset-password?token="+token)
}

func (s *Server) appURL() string {
	if s.Cfg.AppURL != "" {
		return s.Cfg.AppURL
	}
	return "http://localhost:8080"
}
