package web

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"leadforge/internal/webapp"
)

func webappIdentity(r *http.Request) *webapp.Identity {
	return webapp.IdentityFrom(r.Context())
}

type timeT struct{ time.Time }

// staticHandler serves files from /static with caching and safe paths.
func (s *Server) staticHandler() http.Handler {
	fs := http.FileServer(http.Dir("static"))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// basic path traversal guard (http.Dir already guards, but avoid work)
		if strings.Contains(r.URL.Path, "..") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fs.ServeHTTP(w, r)
	})
}

var _ = context.Background
var _ = filepath.Join
var _ = os.Getenv
