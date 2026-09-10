package search

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"leadforge/internal/fixsrv"
	"leadforge/internal/platform/config"
)

// TestPipelineE2E runs the full flow against fixture websites:
// discover → raw → normalize → dedupe → crawl → enrich → score → store.
// Needs a reachable Postgres (TEST_DATABASE_URL or local default); skips otherwise.
func TestPipelineE2E(t *testing.T) {
	pool := testPool(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fx, err := fixsrv.New()
	if err != nil {
		t.Skipf("no loopback: %v", err)
	}
	defer fx.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)

	var tenantID, userID string
	if err := tx.QueryRow(ctx, `INSERT INTO tenants (name, slug) VALUES ('e2e-pipe','e2e-pipe') RETURNING id::text`).Scan(&tenantID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `INSERT INTO users (tenant_id, email, password_hash, name, status) VALUES ($1,'e2e@pipe.local','x','E2E','active') RETURNING id::text`, tenantID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	// NOTE: pipeline runs outside tx (worker-style); cleanup at the end.
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1::uuid`, tenantID)
	}()

	var searchID string
	// NOTE: distinct host spellings (127.0.0.1 vs localhost) model distinct
	// company domains; same-host paths would correctly dedupe by domain.
	localURL := strings.Replace(fx.URL, "127.0.0.1", "localhost", 1)
	if err := pool.QueryRow(ctx, `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, country, filters, limit_count, status)
		VALUES ($1,$2,'software','software Jakarta','Indonesia',
			'{"city":"Jakarta","country":"Indonesia","sources":["manual"],"seed_urls":["`+fx.URL+`/co/acme","`+localURL+`/co/beta"]}',
			100,'queued') RETURNING id::text`, tenantID, userID).Scan(&searchID); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		CrawlerWorkers: 4, CrawlerDomainConcurrency: 2, CrawlerTimeout: 15 * time.Second,
		CrawlerMaxPagesPerSite: 5, CrawlerMaxDepth: 2, CrawlerMaxBodyBytes: 5 << 20,
		CrawlerAllowPrivate: true, SearchProcessWorkers: 4, CrawlerSiteWorkers: 2,
		CrawlerGlobalWorkers: 10, SourceConcurrency: 4, EnrichmentWorkers: 4,
		EnrichFreshDays: 30, VerifyFreshDays: 30, CrawlFreshDays: 14, SessionSecret: "test",
	}
	deps := NewDeps(pool, cfg, slog.New(slog.NewTextHandler(os.Stderr, nil)), "")
	runner := NewRunner(deps)
	if err := runner.Run(ctx, searchID); err != nil {
		t.Fatal(err)
	}

	var st string
	var found, saved, crawled, qualified int
	if err := pool.QueryRow(ctx, `SELECT status, found_count, saved_count, crawled_count, qualified_count
		FROM lead_searches WHERE id=$1`, searchID).Scan(&st, &found, &saved, &crawled, &qualified); err != nil {
		t.Fatal(err)
	}
	if st != "completed" {
		t.Fatalf("status = %s", st)
	}
	if found != 2 || saved != 2 {
		t.Fatalf("found=%d saved=%d, want 2/2", found, saved)
	}
	if crawled != 2 {
		t.Fatalf("crawled=%d, want 2", crawled)
	}
	var nRaw, nCo, nCt, nLead, nScore int
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM raw_leads WHERE search_id=$1`, searchID).Scan(&nRaw)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM companies WHERE tenant_id=$1`, tenantID).Scan(&nCo)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM contacts WHERE tenant_id=$1`, tenantID).Scan(&nCt)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM leads WHERE tenant_id=$1`, tenantID).Scan(&nLead)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM lead_scores ls JOIN leads l ON l.id=ls.lead_id WHERE l.tenant_id=$1`, tenantID).Scan(&nScore)
	if nRaw != 2 || nCo != 2 || nCt < 2 || nLead != 2 || nScore != 2 {
		t.Fatalf("raw=%d co=%d ct=%d leads=%d scores=%d", nRaw, nCo, nCt, nLead, nScore)
	}
	if qualified < 1 {
		t.Fatalf("qualified=%d, want >=1", qualified)
	}
	// dedupe re-run: same seeds must match, not duplicate companies
	var search2 string
	if err := pool.QueryRow(ctx, `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, filters, limit_count, status)
		VALUES ($1,$2,'software','software','{"sources":["manual"],"seed_urls":["`+fx.URL+`/co/acme"]}',100,'queued')
		RETURNING id::text`, tenantID, userID).Scan(&search2); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(ctx, search2); err != nil {
		t.Fatal(err)
	}
	var dup, coAfter int
	_ = pool.QueryRow(ctx, `SELECT duplicate_count FROM lead_searches WHERE id=$1`, search2).Scan(&dup)
	_ = pool.QueryRow(ctx, `SELECT COUNT(*) FROM companies WHERE tenant_id=$1`, tenantID).Scan(&coAfter)
	if dup != 1 || coAfter != 2 {
		t.Fatalf("dedupe re-run: dup=%d companies=%d, want 1/2", dup, coAfter)
	}
}
