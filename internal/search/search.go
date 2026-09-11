// Package search orchestrates the lead-finder pipeline:
//
//	discovery (sources) → raw_leads → normalize → dedupe → crawl/enrich →
//	companies + contacts → rule scoring → leads + progress counters.
//
// Used by the asynq worker and (inline) by CSV imports. All writes are
// tenant-scoped; one failed candidate never fails the whole search.
package search

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/crawler"
	"leadforge/internal/crypto"
	"leadforge/internal/enrichment"
	"leadforge/internal/platform/config"
	"leadforge/internal/scoring"
	"leadforge/internal/source"
	"leadforge/internal/verify"
)

// Filters mirrors the finder form (stored on lead_searches.filters).
type Filters struct {
	Country     string   `json:"country"`
	Province    string   `json:"province"`
	City        string   `json:"city"`
	CompanySize string   `json:"company_size"`
	HasWebsite  bool     `json:"has_website"`
	HasEmail    bool     `json:"has_email"`
	HasPhone    bool     `json:"has_phone"`
	HasWhatsApp bool     `json:"has_whatsapp"`
	MinScore    int      `json:"min_score"`
	SeedURLs    []string `json:"seed_urls"`
	Sources     []string `json:"sources"`
}

// Row is a lead_searches record.
type Row struct {
	ID       string
	TenantID string
	UserID   string
	Keyword  string
	Query    string
	Industry string
	Location string
	Country  string
	Filters  Filters
	Limit    int
	Status   string
}

// Deps are pipeline dependencies.
type Deps struct {
	Pool     *pgxpool.Pool
	Cfg      *config.Config
	Log      *slog.Logger
	Guard    crawler.Guard
	Robots   *crawler.RobotsChecker
	Crawler  *crawler.Manager
	Registry *source.Registry
	Verifier verify.Verifier
	Enricher enrichment.Provider
	// Emit fans out platform events (search.completed, lead.qualified) to
	// webhooks. Nil disables (tests, benchmarks, imports).
	Emit func(ctx context.Context, tenantID, event string, payload map[string]any)
}

// SourceStat tracks per-source discovery telemetry for the search report.
type SourceStat struct {
	Candidates int
	Accepted   int
	Errors     int
	LastError  string
	DurationMs int64
}

// Job is one running search with live counters.
type Job struct {
	Search    Row
	Rules     []scoring.Rule
	GoogleKey string
	SourceOff map[string]bool

	found, saved, dup, crawled, enriched, qualified, failed  atomic.Int64
	discovered, filtered, created, matched, contactable, hot atomic.Int64

	seenMu sync.Mutex
	seen   map[string]bool

	srcMu    sync.Mutex
	srcErrs  []string
	statMu   sync.Mutex
	srcStats map[string]SourceStat

	stop      atomic.Bool  // halt crawling (cancel/fatal)
	lastCheck atomic.Int64 // unix nano of last status poll

	deps *Deps
}

// bumpSrc records an accepted candidate for a source.
func (j *Job) bumpSrc(slug string, accepted bool) {
	j.statMu.Lock()
	st := j.srcStats[slug]
	st.Accepted++
	if accepted {
		// accepted into the pipeline (post seen-filter)
	}
	j.srcStats[slug] = st
	j.statMu.Unlock()
}

// bumpSrcErr records a source-level error.
func (j *Job) bumpSrcErr(slug, msg string) {
	j.statMu.Lock()
	st := j.srcStats[slug]
	st.Errors++
	st.LastError = truncErr(msg)
	j.srcStats[slug] = st
	j.statMu.Unlock()
}

func truncErr(s string) string {
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

// Runner executes searches.
type Runner struct {
	deps *Deps
}

// NewRunner builds a runner.
func NewRunner(d *Deps) *Runner { return &Runner{deps: d} }

// Manager exposes the crawler manager (metrics, cooldowns) for admin views.
func (r *Runner) Manager() *crawler.Manager { return r.deps.Crawler }

// NewDeps builds pipeline dependencies shared by server, worker and tests.
func NewDeps(pool *pgxpool.Pool, cfg *config.Config, log *slog.Logger, googleAPIKey string) *Deps {
	guard := crawler.Guard{AllowPrivate: cfg.CrawlerAllowPrivate}
	robots := crawler.NewRobotsChecker(guard, crawler.Options{
		Timeout: cfg.CrawlerTimeout, MaxBodyBytes: 1 << 20,
		UserAgent: "LeadForgeBot/1.0",
	})
	return &Deps{
		Pool: pool, Cfg: cfg, Log: log,
		Guard: guard, Robots: robots,
		Crawler:  crawler.NewManager(cfg, pool),
		Registry: source.DefaultRegistry(googleAPIKey),
		Verifier: verify.SyntaxVerifier{},
		Enricher: &enrichment.WebsiteProvider{Guard: guard, Robots: robots, Cfg: cfg},
	}
}

// workerID identifies this worker process for heartbeats.
func workerID() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "worker"
	}
	return h + "-" + itoa(os.Getpid())
}

// Load reads the search row + tenant scoring rules + source credentials.
func (r *Runner) Load(ctx context.Context, searchID string) (*Job, error) {
	var j Job
	j.deps = r.deps
	j.seen = map[string]bool{}
	j.srcStats = map[string]SourceStat{}
	j.SourceOff = map[string]bool{}
	var filtersRaw []byte
	err := r.deps.Pool.QueryRow(ctx, `
		SELECT id::text, tenant_id::text, user_id::text,
			COALESCE(keyword,''), COALESCE(query,''), COALESCE(industry,''),
			COALESCE(location,''), COALESCE(country,''),
			filters, limit_count, status
		FROM lead_searches WHERE id=$1`, searchID).
		Scan(&j.Search.ID, &j.Search.TenantID, &j.Search.UserID, &j.Search.Keyword,
			&j.Search.Query, &j.Search.Industry, &j.Search.Location, &j.Search.Country,
			&filtersRaw, &j.Search.Limit, &j.Search.Status)
	if err != nil {
		return nil, err
	}
	if len(filtersRaw) > 0 {
		_ = json.Unmarshal(filtersRaw, &j.Search.Filters)
	}
	if j.Search.Limit <= 0 {
		j.Search.Limit = 100
	}
	rules, err := scoring.LoadTenantRules(ctx, r.deps.Pool, j.Search.TenantID)
	if err == nil {
		j.Rules = rules
	}
	// tenant source prefs: encrypted keys + enabled flags from lead_sources
	rows, err := r.deps.Pool.Query(ctx, `SELECT slug, is_active, config FROM lead_sources WHERE tenant_id=$1`, j.Search.TenantID)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var slug string
			var active bool
			var cfgRaw []byte
			if err := rows.Scan(&slug, &active, &cfgRaw); err != nil {
				continue
			}
			if !active {
				j.SourceOff[slug] = true
			}
			if slug == "google_places" && len(cfgRaw) > 0 {
				var cfg map[string]string
				if json.Unmarshal(cfgRaw, &cfg) == nil {
					if enc, ok := cfg["api_key_enc"]; ok && enc != "" {
						if key, err := crypto.Decrypt(r.deps.Cfg.EncryptionSecret(), enc); err == nil {
							j.GoogleKey = key
						}
					} else if k, ok := cfg["api_key"]; ok {
						j.GoogleKey = k
					}
				}
			}
		}
	}
	return &j, nil
}

// Claim marks a queued search running with attempt + worker tracking.
func (r *Runner) Claim(ctx context.Context, j *Job) bool {
	wid := workerID()
	var ok bool
	_ = r.deps.Pool.QueryRow(ctx, `
		UPDATE lead_searches SET status='running', started_at=COALESCE(started_at, now()),
			run_attempt=run_attempt+1, worker_id=$2, last_heartbeat=now(), updated_at=now()
		WHERE id=$1 AND status='queued' RETURNING true`, j.Search.ID, wid).Scan(&ok)
	if ok {
		j.Search.Status = "running"
	}
	return ok
}

// Control polls the desired state: "" = keep going, otherwise paused/cancelled.
func (r *Runner) Control(ctx context.Context, j *Job) string {
	now := time.Now().UnixNano()
	if now-j.lastCheck.Load() < 2e9 && j.Search.Status == "running" {
		return ""
	}
	j.lastCheck.Store(now)
	var st string
	if err := r.deps.Pool.QueryRow(ctx, `SELECT status FROM lead_searches WHERE id=$1`, j.Search.ID).Scan(&st); err != nil {
		return ""
	}
	j.Search.Status = st
	if st == "paused" || st == "cancelled" || st == "failed" {
		return st
	}
	return ""
}

// WaitWhilePaused blocks during pause (flushing counters), returning false if
// the search was cancelled underneath. In-flight candidates may finish.
func (r *Runner) WaitWhilePaused(ctx context.Context, j *Job) bool {
	for {
		r.Flush(ctx, j)
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
		st := r.Control(ctx, j)
		if st == "cancelled" || st == "failed" {
			return false
		}
		if st == "" {
			return true
		}
	}
}

// Flush writes counters + heartbeat + source stats to the search row.
func (r *Runner) Flush(ctx context.Context, j *Job) {
	_, _ = r.deps.Pool.Exec(ctx, `
		UPDATE lead_searches SET found_count=$2, saved_count=$3, duplicate_count=$4,
			crawled_count=$5, enriched_count=$6, qualified_count=$7, failed_count=$8,
			discovered_count=$9, companies_created=$10, companies_matched=$11,
			contactable_count=$12, hot_count=$13, filtered_count=$14,
			last_heartbeat=now(), updated_at=now() WHERE id=$1`,
		j.Search.ID, j.found.Load(), j.saved.Load(), j.dup.Load(),
		j.crawled.Load(), j.enriched.Load(), j.qualified.Load(), j.failed.Load(),
		j.discovered.Load(), j.created.Load(), j.matched.Load(),
		j.contactable.Load(), j.hot.Load(), j.filtered.Load())
	j.statMu.Lock()
	stats := make(map[string]SourceStat, len(j.srcStats))
	for k, v := range j.srcStats {
		stats[k] = v
	}
	j.statMu.Unlock()
	for slug, st := range stats {
		_, _ = r.deps.Pool.Exec(ctx, `
			INSERT INTO search_source_stats (search_id, source_slug, candidates, accepted, errors, last_error, duration_ms)
			VALUES ($1,$2,$3,$4,$5,$6,$7)
			ON CONFLICT (search_id, source_slug) DO UPDATE SET
				candidates=EXCLUDED.candidates, accepted=EXCLUDED.accepted, errors=EXCLUDED.errors,
				last_error=EXCLUDED.last_error, duration_ms=EXCLUDED.duration_ms`,
			j.Search.ID, slug, st.Candidates, st.Accepted, st.Errors, st.LastError, st.DurationMs)
	}
}

// Finish closes the search with a terminal status + usage + metrics.
func (r *Runner) Finish(ctx context.Context, j *Job, status, errMsg string) {
	r.Flush(ctx, j)
	_, _ = r.deps.Pool.Exec(ctx, `
		UPDATE lead_searches SET status=$2, error=$3, finished_at=now(),
			duration_ms=EXTRACT(EPOCH FROM (now()-COALESCE(started_at,now())))*1000,
			updated_at=now() WHERE id=$1`, j.Search.ID, status, errMsg)
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO usage_events (tenant_id, user_id, kind, quantity, meta)
		VALUES ($1,$2,'search',1,$3)`, j.Search.TenantID, nullUUID(j.Search.UserID), `{"search_id":"`+j.Search.ID+`","status":"`+status+`"}`)
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO usage_events (tenant_id, user_id, kind, quantity, meta)
		VALUES ($1,$2,'crawl',$3,$4)`, j.Search.TenantID, nullUUID(j.Search.UserID),
		j.crawled.Load(), `{"search_id":"`+j.Search.ID+`"}`)
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO usage_events (tenant_id, user_id, kind, quantity, meta)
		VALUES ($1,$2,'enrichment',$3,$4)`, j.Search.TenantID, nullUUID(j.Search.UserID),
		j.enriched.Load(), `{"search_id":"`+j.Search.ID+`"}`)
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO crawler_metrics (window_s, pages, success, failed, avg_ms)
		SELECT COALESCE(EXTRACT(EPOCH FROM (now()-s.started_at)),0)::int, $2, $3, $4, 0
		FROM lead_searches s WHERE s.id=$1`,
		j.Search.ID, j.crawled.Load(), j.crawled.Load(), j.failed.Load())
	if r.deps.Emit != nil && status == "completed" {
		r.deps.Emit(ctx, j.Search.TenantID, "search.completed", map[string]any{
			"search_id": j.Search.ID, "found": j.found.Load(), "saved": j.saved.Load(),
			"qualified": j.qualified.Load(), "failed": j.failed.Load(),
		})
	}
	j.Search.Status = status
}

func nullUUID(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Progress returns a snapshot for SSE/UI.
type Progress struct {
	Status      string `json:"status"`
	Discovered  int64  `json:"discovered"`
	Found       int64  `json:"found"`
	Saved       int64  `json:"saved"`
	Duplicate   int64  `json:"duplicate"`
	Created     int64  `json:"created"`
	Matched     int64  `json:"matched"`
	Crawled     int64  `json:"crawled"`
	Enriched    int64  `json:"enriched"`
	Contactable int64  `json:"contactable"`
	Qualified   int64  `json:"qualified"`
	Hot         int64  `json:"hot"`
	Filtered    int64  `json:"filtered"`
	Failed      int64  `json:"failed"`
	Limit       int    `json:"limit"`
	Error       string `json:"error,omitempty"`
}

// Snapshot reads live progress.
func Snapshot(ctx context.Context, pool *pgxpool.Pool, tenantID, searchID string) (*Progress, error) {
	var p Progress
	err := pool.QueryRow(ctx, `
		SELECT status, discovered_count, found_count, saved_count, duplicate_count,
			companies_created, companies_matched, crawled_count, enriched_count,
			contactable_count, qualified_count, hot_count, filtered_count,
			failed_count, limit_count, COALESCE(error,'')
		FROM lead_searches WHERE id=$1 AND tenant_id=$2`,
		searchID, tenantID).Scan(&p.Status, &p.Discovered, &p.Found, &p.Saved, &p.Duplicate,
		&p.Created, &p.Matched, &p.Crawled, &p.Enriched, &p.Contactable,
		&p.Qualified, &p.Hot, &p.Filtered, &p.Failed, &p.Limit, &p.Error)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
