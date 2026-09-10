package webapp

import (
	"net/http"
	"strings"
)

// Router is a tiny method+pattern router on net/http with path params,
// deliberately dependency-free.
type Router struct {
	mux      *http.ServeMux
	prefixes []prefixRoute
}

type prefixRoute struct {
	prefix string
	h      http.Handler
}

// NewRouter builds a router.
func NewRouter() *Router {
	return &Router{mux: http.NewServeMux()}
}

// HandleFunc registers an exact method+path route, e.g. "GET /leads".
// Patterns ending in "/{$}" match only the exact path.
func (rt *Router) HandleFunc(method, pattern string, h http.HandlerFunc) {
	rt.mux.HandleFunc(method+" "+pattern, h)
}

// Handle registers an http.Handler route.
func (rt *Router) Handle(method, pattern string, h http.Handler) {
	rt.mux.Handle(method+" "+pattern, h)
}

// MountPrefix routes any request whose path begins with prefix (used for /static/).
func (rt *Router) MountPrefix(prefix string, h http.Handler) {
	rt.prefixes = append(rt.prefixes, prefixRoute{prefix, h})
}

// ServeHTTP dispatches.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	for _, pr := range rt.prefixes {
		if strings.HasPrefix(path, pr.prefix) {
			pr.h.ServeHTTP(w, r)
			return
		}
	}
	// normalize trailing slash (except root)
	if len(path) > 1 && strings.HasSuffix(path, "/") {
		http.Redirect(w, r, strings.TrimRight(path, "/"), http.StatusMovedPermanently)
		return
	}
	rt.mux.ServeHTTP(w, r)
}
