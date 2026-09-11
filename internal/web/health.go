package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"leadforge/internal/metrics"
	"leadforge/internal/queue"
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
	// Liveness must remain cheap and available while dependencies restart.
	hr.Postgres = "unknown"
	hr.Redis = "unknown"
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	hr.MemoryMB = int64(ms.Alloc / (1 << 20))
	hr.Gorouti = runtime.NumGoroutine()

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(hr)
}

func (s *Server) handleMetrics(w http.ResponseWriter, _ *http.Request) {
	snap := metrics.Snap()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = fmt.Fprintf(w, "leadforge_http_requests_total %d\n", snap.HTTPRequests)
	_, _ = fmt.Fprintf(w, "leadforge_http_request_duration_seconds_total %.6f\n", float64(snap.HTTPDurationMs)/1000)
	_, _ = fmt.Fprintf(w, "leadforge_searches_total %d\n", snap.Searches)
	_, _ = fmt.Fprintf(w, "leadforge_candidates_total %d\n", snap.Candidates)
	_, _ = fmt.Fprintf(w, "leadforge_crawls_total %d\n", snap.Crawls)
	_, _ = fmt.Fprintf(w, "leadforge_crawls_failed_total %d\n", snap.CrawlFailed)
	_, _ = fmt.Fprintf(w, "leadforge_leads_created_total %d\n", snap.LeadsCreated)
	_, _ = fmt.Fprintf(w, "leadforge_duplicates_total %d\n", snap.Duplicates)
	_, _ = fmt.Fprintf(w, "leadforge_email_sent_total %d\n", snap.EmailSent)
	_, _ = fmt.Fprintf(w, "leadforge_email_failed_total %d\n", snap.EmailFailed)
	_, _ = fmt.Fprintf(w, "leadforge_webhooks_failed_total %d\n", snap.WebhooksFailed)
	_, _ = fmt.Fprintf(w, "leadforge_browser_fallback_total %d\n", snap.BrowserFallback)
	if queues, err := queue.Inspect(s.Cfg.RedisAddr); err == nil {
		depth := 0
		for _, q := range queues {
			depth += q.Pending + q.Active + q.Retry
		}
		_, _ = fmt.Fprintf(w, "leadforge_queue_depth %d\n", depth)
	}
	_, _ = fmt.Fprintf(w, "leadforge_goroutines %d\n", runtime.NumGoroutine())
}

// handleReady fails when a critical dependency is unavailable.
func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	pgOK := s.PG.Ping(ctx) == nil
	redisOK := queue.PingRedis(ctx, s.Cfg.RedisAddr) == nil
	w.Header().Set("Content-Type", "application/json")
	if !pgOK || !redisOK {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "not ready", "postgres": pgOK, "redis": redisOK,
		})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ready", "postgres": true, "redis": true,
	})
}

func (s *Server) handleLanding(w http.ResponseWriter, r *http.Request) {
	landing.Page(landing.Data{AppName: s.Cfg.AppName}).Render(r.Context(), w)
}
