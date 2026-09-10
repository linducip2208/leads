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
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/internal/crawler"
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
	Registry *source.Registry
	Verifier verify.Verifier
	Enricher enrichment.Provider
}

// Job is one running search with live counters.
type Job struct {
	Search Row
	Rules  []scoring.Rule

	found, saved, dup, crawled, enriched, qualified, failed atomic.Int64

	seenMu sync.Mutex
	seen   map[string]bool

	srcMu   sync.Mutex
	srcErrs []string

	stop      atomic.Bool  // halt crawling (cancel/fatal)
	lastCheck atomic.Int64 // unix nano of last status poll

	deps *Deps
}

// Runner executes searches.
type Runner struct {
	deps *Deps
}

// NewRunner builds a runner.
func NewRunner(d *Deps) *Runner { return &Runner{deps: d} }

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
		Registry: source.DefaultRegistry(googleAPIKey),
		Verifier: verify.SyntaxVerifier{},
		Enricher: &enrichment.WebsiteProvider{Guard: guard, Robots: robots, Cfg: cfg},
	}
}

// Load reads the search row + tenant scoring rules.
func (r *Runner) Load(ctx context.Context, searchID string) (*Job, error) {
	var j Job
	j.deps = r.deps
	j.seen = map[string]bool{}
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
	return &j, nil
}

// Claim marks a queued search running (idempotent-ish: only from queued).
func (r *Runner) Claim(ctx context.Context, j *Job) bool {
	var ok bool
	_ = r.deps.Pool.QueryRow(ctx, `
		UPDATE lead_searches SET status='running', started_at=COALESCE(started_at, now()), updated_at=now()
		WHERE id=$1 AND status='queued' RETURNING true`, j.Search.ID).Scan(&ok)
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
// the search was cancelled underneath.
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

// Flush writes counters to the search row (keeps watchdog + UI fresh).
func (r *Runner) Flush(ctx context.Context, j *Job) {
	_, _ = r.deps.Pool.Exec(ctx, `
		UPDATE lead_searches SET found_count=$2, saved_count=$3, duplicate_count=$4,
			crawled_count=$5, enriched_count=$6, qualified_count=$7, failed_count=$8,
			updated_at=now() WHERE id=$1`,
		j.Search.ID, j.found.Load(), j.saved.Load(), j.dup.Load(),
		j.crawled.Load(), j.enriched.Load(), j.qualified.Load(), j.failed.Load())
}

// Finish closes the search with a terminal status.
func (r *Runner) Finish(ctx context.Context, j *Job, status, errMsg string) {
	r.Flush(ctx, j)
	_, _ = r.deps.Pool.Exec(ctx, `
		UPDATE lead_searches SET status=$2, error=$3, finished_at=now(),
			duration_ms=EXTRACT(EPOCH FROM (now()-COALESCE(started_at,now())))*1000,
			updated_at=now() WHERE id=$1`, j.Search.ID, status, errMsg)
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO usage_events (tenant_id, user_id, kind, quantity, meta)
		VALUES ($1,$2,'search',1,$3)`, j.Search.TenantID, nullUUID(j.Search.UserID), "{"+`"search_id":"`+j.Search.ID+`","status":"`+status+`"`+"}")
	_, _ = r.deps.Pool.Exec(ctx, `
		INSERT INTO crawler_metrics (window_s, pages, success, failed, avg_ms)
		SELECT COALESCE(EXTRACT(EPOCH FROM (now()-s.started_at)),0)::int, $2, $3, $4, 0
		FROM lead_searches s WHERE s.id=$1`,
		j.Search.ID, j.crawled.Load(), j.crawled.Load(), j.failed.Load())
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
	Status    string `json:"status"`
	Found     int64  `json:"found"`
	Saved     int64  `json:"saved"`
	Duplicate int64  `json:"duplicate"`
	Crawled   int64  `json:"crawled"`
	Enriched  int64  `json:"enriched"`
	Qualified int64  `json:"qualified"`
	Failed    int64  `json:"failed"`
	Limit     int    `json:"limit"`
	Error     string `json:"error,omitempty"`
}

// Snapshot reads live progress.
func Snapshot(ctx context.Context, pool *pgxpool.Pool, tenantID, searchID string) (*Progress, error) {
	var p Progress
	err := pool.QueryRow(ctx, `
		SELECT status, found_count, saved_count, duplicate_count, crawled_count,
			enriched_count, qualified_count, failed_count, limit_count, COALESCE(error,'')
		FROM lead_searches WHERE id=$1 AND tenant_id=$2`,
		searchID, tenantID).Scan(&p.Status, &p.Found, &p.Saved, &p.Duplicate,
		&p.Crawled, &p.Enriched, &p.Qualified, &p.Failed, &p.Limit, &p.Error)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
