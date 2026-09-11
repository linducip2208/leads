package web

import (
	"net/http"
	"net/http/pprof"

	"leadforge/internal/role"
)

// pprofRoutes registers runtime diagnostics only when explicitly enabled.
// Every handler is protected by the same platform-admin permission as the
// other operational dashboards; there is no public pprof endpoint.
func (s *Server) pprofRoutes() {
	admin := func(next http.HandlerFunc) http.Handler {
		return s.requirePerm(role.AdminPlatform, next)
	}

	s.Router.Handle("GET", "/admin/pprof/", admin(pprof.Index))
	s.Router.Handle("GET", "/admin/pprof/cmdline", admin(pprof.Cmdline))
	s.Router.Handle("GET", "/admin/pprof/profile", admin(pprof.Profile))
	s.Router.Handle("GET", "/admin/pprof/symbol", admin(pprof.Symbol))
	s.Router.Handle("GET", "/admin/pprof/trace", admin(pprof.Trace))
	for _, name := range []string{"allocs", "block", "goroutine", "heap", "mutex", "threadcreate"} {
		h := pprof.Handler(name).ServeHTTP
		s.Router.Handle("GET", "/admin/pprof/"+name, admin(h))
	}
}
