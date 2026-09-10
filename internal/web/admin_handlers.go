package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"runtime"
	"time"

	"leadforge/internal/metrics"
	"leadforge/internal/queue"
	"leadforge/internal/role"
	"leadforge/web/layouts"
	"leadforge/web/pages/admin"
)

func (s *Server) adminRoutes() {
	s.Router.HandleFunc("GET", "/admin/health", s.requirePerm(role.AdminPlatform, s.handleAdminHealth))
	s.Router.HandleFunc("GET", "/admin/queue", s.requirePerm(role.AdminPlatform, s.handleAdminQueue))
	s.Router.HandleFunc("GET", "/admin/crawlers", s.requirePerm(role.AdminPlatform, s.handleAdminCrawlers))
	s.Router.HandleFunc("GET", "/admin/sources", s.requirePerm(role.AdminPlatform, s.handleAdminSources))
	s.Router.HandleFunc("GET", "/admin/metrics.json", s.requirePerm(role.AdminPlatform, s.handleAdminMetrics))
}

func hbAge(ctx context.Context, addr, key string) (status, detail string) {
	t, ok := queue.LastBeat(ctx, addr, key)
	if !ok {
		return "down", "no heartbeat"
	}
	ago := time.Since(t)
	if ago > 60*time.Second {
		return "down", "last beat " + ago.Round(time.Second).String() + " ago"
	}
	return "ok", "beat " + ago.Round(time.Second).String() + " ago"
}

func (s *Server) handleAdminHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := &admin.HealthData{}
	// postgres
	pgT := time.Now()
	pgErr := s.PG.Ping(ctx)
	pgMs := time.Since(pgT).Milliseconds()
	if pgErr != nil {
		d.Rows = append(d.Rows, admin.HealthRow{Name: "PostgreSQL", Status: "down", Detail: pgErr.Error()})
	} else {
		var dbSize string
		_ = s.PG.QueryRow(ctx, `SELECT pg_size_pretty(pg_database_size(current_database()))`).Scan(&dbSize)
		d.Rows = append(d.Rows, admin.HealthRow{Name: "PostgreSQL", Status: "ok", Detail: dbSize, Latency: itoa64(pgMs) + " ms"})
	}
	// redis
	rdT := time.Now()
	if err := queue.PingRedis(ctx, s.Cfg.RedisAddr); err != nil {
		d.Rows = append(d.Rows, admin.HealthRow{Name: "Redis", Status: "down", Detail: err.Error()})
	} else {
		d.Rows = append(d.Rows, admin.HealthRow{Name: "Redis", Status: "ok", Detail: s.Cfg.RedisAddr, Latency: itoa64(time.Since(rdT).Milliseconds()) + " ms"})
	}
	// worker + scheduler
	wst, wdet := hbAge(ctx, s.Cfg.RedisAddr, "leadforge:worker:hb")
	d.Rows = append(d.Rows, admin.HealthRow{Name: "Worker", Status: wst, Detail: wdet})
	sst, sdet := hbAge(ctx, s.Cfg.RedisAddr, "leadforge:scheduler:hb")
	d.Rows = append(d.Rows, admin.HealthRow{Name: "Scheduler", Status: sst, Detail: sdet})
	// queues rollup
	if stats, err := queue.Inspect(s.Cfg.RedisAddr); err == nil {
		pending, failed := 0, 0
		for _, q := range stats {
			pending += q.Pending + q.Active + q.Retry
			failed += q.Failed
		}
		st, det := "ok", "no backlog"
		if pending > 0 {
			det = itoa(pending) + " tasks in flight"
		}
		if failed > 50 {
			st = "down"
			det += ", " + itoa(failed) + " failed today"
		}
		d.Rows = append(d.Rows, admin.HealthRow{Name: "Queues", Status: st, Detail: det})
	}
	d.Uptime = time.Since(startTime).Round(time.Second).String()
	d.GoVersion = runtime.Version()
	d.Goroutines = runtime.NumGoroutine()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	d.MemoryMB = int64(ms.Alloc / (1 << 20))
	p := s.page(w, r, "System Health", "/admin/health")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Health(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminQueue(w http.ResponseWriter, r *http.Request) {
	d := &admin.QueueData{RedisAddr: s.Cfg.RedisAddr}
	if stats, err := queue.Inspect(s.Cfg.RedisAddr); err == nil {
		for _, q := range stats {
			d.Rows = append(d.Rows, admin.QueueRow{
				Queue: q.Queue, Pending: q.Pending, Active: q.Active,
				Scheduled: q.Scheduled, Retry: q.Retry, Failed: q.Failed, Processed: q.Processed,
			})
		}
	}
	if t, ok := queue.LastBeat(r.Context(), s.Cfg.RedisAddr, "leadforge:worker:hb"); ok {
		d.WorkerHB = "alive (" + time.Since(t).Round(time.Second).String() + " ago)"
	} else {
		d.WorkerHB = "not running"
	}
	if t, ok := queue.LastBeat(r.Context(), s.Cfg.RedisAddr, "leadforge:scheduler:hb"); ok {
		d.SchedHB = "alive (" + time.Since(t).Round(time.Second).String() + " ago)"
	} else {
		d.SchedHB = "not running"
	}
	p := s.page(w, r, "Queue", "/admin/queue")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Queue(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminCrawlers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := &admin.CrawlerData{
		Workers: s.Cfg.CrawlWorkers, DomainConc: s.Cfg.CrawlDomain, Timeout: s.Cfg.CrawlTimeout,
		GlobalCap: s.Cfg.CrawlGlobal, BrowserOn: s.Cfg.BrowserOn, BrowserN: s.Cfg.BrowserWorkers,
	}
	if s.Crawler != nil {
		snap := s.Crawler.Snapshot()
		d.GlobalUse = snap.GlobalUse
		if snap.GlobalCap > 0 {
			d.GlobalCap = snap.GlobalCap
		}
		if s.Crawler.Browser != nil {
			d.BrowserOn = s.Cfg.BrowserOn && s.Crawler.Browser.Available()
		}
		d.RobotsHit = int(snap.RobotsHit)
		d.SSRFHit = int(snap.SSRFHit)
		for _, c := range s.Crawler.Cooldowns() {
			d.Cooldowns = append(d.Cooldowns, admin.CooldownRow{Domain: c.Domain, Until: c.Until.Format("15:04:05")})
		}
	}
	_ = s.PG.QueryRow(ctx, `SELECT COUNT(*) FROM crawl_jobs WHERE status='pending'`).Scan(&d.PendingJobs)
	d.QueueDepth = d.PendingJobs
	_ = s.PG.QueryRow(ctx, `
		SELECT COALESCE(SUM(pages)*1.0/NULLIF(GREATEST(SUM(window_s),60),0)*60,0) FROM crawler_metrics
		WHERE at > now() - interval '1 hour'`).Scan(&d.PagesMin)
	_ = s.PG.QueryRow(ctx, `
		SELECT COALESCE(SUM(success)*100.0/NULLIF(SUM(success+failed),0),0) FROM crawler_metrics
		WHERE at > now() - interval '24 hours'`).Scan(&d.SuccessRate)
	_ = s.PG.QueryRow(ctx, `SELECT COALESCE(AVG(avg_ms),0) FROM crawler_metrics WHERE at > now() - interval '1 hour'`).Scan(&d.AvgMs)
	rows, _ := s.PG.Query(ctx, `SELECT url FROM crawl_jobs WHERE created_at > now() - interval '10 minutes' ORDER BY created_at DESC LIMIT 20`)
	if rows != nil {
		defer rows.Close()
		seen := map[string]bool{}
		for rows.Next() {
			var u string
			if err := rows.Scan(&u); err == nil {
				if h := hostOf(u); h != "" && !seen[h] {
					seen[h] = true
					d.ActiveHosts = append(d.ActiveHosts, h)
				}
			}
		}
	}
	erows, _ := s.PG.Query(ctx, `SELECT url, error, to_char(created_at,'DD Mon HH24:MI') FROM crawl_jobs WHERE status='failed' ORDER BY created_at DESC LIMIT 10`)
	if erows != nil {
		defer erows.Close()
		for erows.Next() {
			var e admin.CrawlError
			if err := erows.Scan(&e.URL, &e.Error, &e.When); err == nil {
				d.Errors = append(d.Errors, e)
			}
		}
	}
	p := s.page(w, r, "Crawlers", "/admin/crawlers")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Crawlers(p, d)).Render(r.Context(), w)
}

func (s *Server) handleAdminSources(w http.ResponseWriter, r *http.Request) {
	rows, err := s.PG.Query(r.Context(), `
		SELECT COALESCE(source_slug,'unknown'),
			COUNT(*) FILTER (WHERE discovered_at > now() - interval '7 days'),
			COUNT(*) FILTER (WHERE status IN ('matched','converted') AND discovered_at > now() - interval '7 days'),
			COUNT(*) FILTER (WHERE status='failed' AND discovered_at > now() - interval '7 days'),
			COALESCE(MAX(discovered_at) FILTER (WHERE status IN ('matched','converted')), now() - interval '30 days'),
			COALESCE(MAX(fail_reason) FILTER (WHERE status='failed'), '')
		FROM raw_leads GROUP BY 1 ORDER BY 2 DESC`)
	var out []admin.SourceStatRow
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var it admin.SourceStatRow
			var lastOK time.Time
			if err := rows.Scan(&it.Slug, &it.Candidates, &it.Accepted, &it.Errors, &lastOK, &it.LastError); err == nil {
				total := it.Accepted + it.Errors
				if total > 0 {
					it.SuccessPct = float64(it.Accepted) / float64(total) * 100
				}
				if it.Accepted > 0 {
					it.LastOK = lastOK.Format("02 Jan 15:04")
				} else {
					it.LastOK = "—"
				}
				out = append(out, it)
			}
		}
	}
	p := s.page(w, r, "Sources", "/admin/sources")
	layouts.AppShell(s.Ren, webappIdentity(r), p, admin.Sources(p, out)).Render(r.Context(), w)
}

func (s *Server) handleAdminMetrics(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"metrics": metrics.Snap(), "uptime_s": int64(time.Since(startTime).Seconds())}
	if s.Crawler != nil {
		out["crawler"] = s.Crawler.Snapshot()
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
