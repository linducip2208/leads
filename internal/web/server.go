package web

import (
	"log/slog"
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/auth"
	"leadforge/internal/csrf"
	"leadforge/internal/flash"
	"leadforge/internal/httpx"
	"leadforge/internal/queue"
	"leadforge/internal/search"
	"leadforge/internal/session"
	"leadforge/internal/webapp"
	"leadforge/web/layouts"
)

// Server holds dependencies for all web handlers.
type Server struct {
	Cfg     Config
	Log     *slog.Logger
	PG      *pgxpool.Pool
	Auth    *auth.Service
	Session *session.Manager
	CSRF    *csrf.Manager
	Router  *webapp.Router
	Ren     *webapp.Renderer
	Queue   *queue.Client
	Runner  *search.Runner
}

// Config is the web server config subset.
type Config struct {
	AppName       string
	Env           string
	Addr          string
	AppURL        string
	RedisAddr     string
	CrawlWorkers  int
	CrawlDomain   int
	CrawlTimeout  string
	CrawlMaxPages int
	CrawlMaxDepth int
}

// New builds the server with all routes registered.
func New(cfg Config, log *slog.Logger, pool *pgxpool.Pool, authSvc *auth.Service, sess *session.Manager, csrfMgr *csrf.Manager) *Server {
	s := &Server{
		Cfg:     cfg,
		Log:     log,
		PG:      pool,
		Auth:    authSvc,
		Session: sess,
		CSRF:    csrfMgr,
		Router:  webapp.NewRouter(),
		Ren:     &webapp.Renderer{AppName: cfg.AppName, Env: cfg.Env},
	}
	s.routes()
	return s
}

// Handler returns the root handler with global middleware.
func (s *Server) Handler() http.Handler {
	var h http.Handler = s.Router
	h = httpx.Chain(h,
		httpx.RequestID,
		httpx.SecureHeaders(),
		httpx.BodyLimit(32<<20),
		httpx.Logger(s.Log),
	)
	return h
}

// requireAuth resolves session and injects identity; redirects anonymous users.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessData, _ := s.Session.Get(r.Context(), r)
		if sessData == nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		u, err := s.Auth.GetUser(r.Context(), sessData.UserID)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		perms, err := s.Auth.Permissions(r.Context(), u)
		if err != nil {
			s.Log.Error("load permissions", "err", err)
			perms = nil
		}
		id := &webapp.Identity{
			UserID:       u.ID,
			TenantID:     u.TenantID,
			Name:         u.Name,
			Email:        u.Email,
			IsSuperAdmin: u.IsSuperAdmin,
			Perms:        perms,
		}
		// CSRF for state-changing requests
		if csrf.SecureMethods(r) {
			sessionID := sessData.UserID // bind token to user id
			if !s.CSRF.Verify(r, sessionID) {
				s.renderError(w, r, &webapp.HTTPError{Status: 403, Title: "Request blocked", Message: "Your session expired or the form token was missing. Please go back and try again."})
				return
			}
		}
		ctx := webapp.WithIdentity(r.Context(), id)
		next(w, r.WithContext(ctx))
	}
}

// requirePerm checks a permission for authenticated users.
func (s *Server) requirePerm(perm string, next http.HandlerFunc) http.HandlerFunc {
	return s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		id := webapp.IdentityFrom(r.Context())
		if !id.Can(perm) {
			s.renderError(w, r, webapp.ErrForbidden)
			return
		}
		next(w, r)
	})
}

// page builds Page metadata with CSRF token and flashes, clearing the flash cookie.
func (s *Server) page(w http.ResponseWriter, r *http.Request, title, activeNav string) webapp.Page {
	id := webapp.IdentityFrom(r.Context())
	tok := ""
	if id != nil {
		tok = s.CSRF.Token(id.UserID)
	}
	flash.ClearCookie(w)
	return webapp.Page{Title: title, ActiveNav: activeNav, CSRFToken: tok, Flash: flash.Pop(r)}
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, he *webapp.HTTPError) {
	if he.Err != nil {
		s.Log.Error("http error", "status", he.Status, "err", he.Err, "request_id", httpx.GetRequestID(r.Context()))
	}
	w.WriteHeader(he.Status)
	layouts.ErrorLayout(s.Ren, webapp.ErrorPageData{Code: he.Status, Title: he.Title, Message: he.Message}).Render(r.Context(), w)
}
