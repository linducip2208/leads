package web

import (
	"net/http"
	"time"

	"leadforge/web/pages/dashboard"
)

func (s *Server) routes() {
	s.authRoutes()
	s.finderRoutes()
	s.searchRoutes()
	s.leadRoutes()
	s.companyRoutes()
	s.peopleRoutes()
	s.importRoutes()
	s.crmRoutes()
	s.intelRoutes()
	s.adminRoutes()

	// static
	s.Router.MountPrefix("/static/", http.StripPrefix("/static/", s.staticHandler()))

	// health
	s.Router.HandleFunc("GET", "/health", s.handleHealth)
	s.Router.HandleFunc("GET", "/ready", s.handleReady)

	// landing
	s.Router.HandleFunc("GET", "/landing", s.handleLanding)

	// app (auth)
	s.Router.HandleFunc("GET", "/{$}", s.requireAuth(s.handleDashboard))
	s.Router.HandleFunc("GET", "/dashboard", s.requireAuth(s.handleDashboard))

	// global search results
	s.Router.HandleFunc("GET", "/search", s.requireAuth(s.handleGlobalSearch))

	// notifications & help (minimal working pages)
	s.Router.HandleFunc("GET", "/notifications", s.requireAuth(s.handleNotifications))
	s.Router.HandleFunc("GET", "/help", s.requireAuth(s.handleHelp))
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Dashboard", "/")
	id := webappIdentity(r)
	d := dashboard.Load(r.Context(), s.PG, id.TenantID, id.Name)
	dashboard.Page(p, d).Render(r.Context(), w)
}

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Notifications", "")
	id := webappIdentity(r)
	rows, _ := s.PG.Query(r.Context(), `
		SELECT title, body, link, read_at IS NOT NULL, created_at
		FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT 100`, id.UserID)
	defer rows.Close()
	type n struct {
		Title, Body, Link string
		Read              bool
		Created           string
	}
	var items []dashboard.NotificationsPageItem
	for rows.Next() {
		var it dashboard.NotificationsPageItem
		var created time.Time
		if err := rows.Scan(&it.Title, &it.Body, &it.Link, &it.Read, &created); err == nil {
			it.Created = created.Format("02 Jan 15:04")
			items = append(items, it)
		}
	}
	dashboard.NotificationsPage(p, items).Render(r.Context(), w)
}

func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	p := s.page(w, r, "Help", "")
	dashboard.HelpPage(p).Render(r.Context(), w)
}

func (s *Server) handleGlobalSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	p := s.page(w, r, "Search", "")
	p.SearchQuery = q
	var results []dashboard.SearchResult
	if q != "" {
		results = dashboard.GlobalSearch(r.Context(), s.PG, webappIdentity(r).TenantID, q)
	}
	dashboard.SearchResultsPage(p, q, results).Render(r.Context(), w)
}
