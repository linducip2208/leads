package web

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"leadforge/web/pages/landing"
)

// healthReport is returned by /health and /ready.
type healthReport struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	UptimeS  int64  `json:"uptime_seconds"`
	Postgres string `json:"postgres"`
	Redis    string `json:"redis"`
	MemoryMB int64  `json:"memory_mb"`
	Gorouti  int    `json:"goroutines"`
}

var startTime = time.Now()

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	hr := healthReport{
		Status:  "ok",
		Version: "1.0.0",
		UptimeS: int64(time.Since(startTime).Seconds()),
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.PG.Ping(ctx); err != nil {
		hr.Postgres = "down"
		hr.Status = "degraded"
	} else {
		hr.Postgres = "ok"
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	hr.MemoryMB = int64(ms.Alloc / (1 << 20))
	hr.Gorouti = runtime.NumGoroutine()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hr)
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	landing.Page(landing.Data{AppName: s.Cfg.AppName}).Render(r.Context(), w)
}
