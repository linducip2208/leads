// Command benchmark-search measures pipeline throughput without Redis:
//
//	go run ./cmd/benchmark-search --count=1000 --mode=http
//	go run ./cmd/benchmark-search --count=10000 --mode=mock
//
// Modes:
//   - http: full crawl against an in-process fixture farm (N distinct sites)
//   - mock: synthetic source-only candidates (no network; DB pipeline only)
//
// A throwaway tenant is created and deleted afterwards (unless --keep).
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"sync/atomic"
	"time"

	"leadforge/internal/fixsrv"
	"leadforge/internal/platform"
	"leadforge/internal/search"
	"leadforge/internal/source"
)

func main() {
	count := flag.Int("count", 1000, "candidates to process")
	mode := flag.String("mode", "http", "http|mock")
	keep := flag.Bool("keep", false, "keep the benchmark tenant")
	workers := flag.Int("workers", 8, "process workers")
	flag.Parse()
	if *count <= 0 {
		*count = 1000
	}

	ctx := context.Background()
	cont, err := platform.NewContainer(ctx)
	if err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer cont.Close()
	cont.Cfg.SearchProcessWorkers = *workers
	cont.Cfg.CrawlerAllowPrivate = true // fixture farm is loopback

	var seeds []string
	var fx *fixsrv.Server
	reg := source.DefaultRegistry("")
	if *mode == "http" {
		fx, err = fixsrv.New()
		if err != nil {
			slog.Error("fixture farm failed", "err", err)
			os.Exit(1)
		}
		defer fx.Close()
		for i := 0; i < *count; i++ {
			seeds = append(seeds, fmt.Sprintf("%s/co/biz%05d", fx.URL, i))
		}
	} else {
		reg = source.NewRegistry(source.NewMockSource(*count))
	}

	name := fmt.Sprintf("bench-%d", time.Now().Unix())
	var tenantID, userID string
	if err := cont.PG.QueryRow(ctx, `INSERT INTO tenants (name, slug) VALUES ($1,$1) RETURNING id::text`, name).Scan(&tenantID); err != nil {
		slog.Error("tenant", "err", err)
		os.Exit(1)
	}
	if err := cont.PG.QueryRow(ctx, `INSERT INTO users (tenant_id, email, password_hash, name, status) VALUES ($1,$2,'x','Bench','active') RETURNING id::text`,
		tenantID, name+"@bench.local").Scan(&userID); err != nil {
		slog.Error("user", "err", err)
		os.Exit(1)
	}
	if !*keep {
		defer func() {
			_, _ = cont.PG.Exec(context.Background(), `DELETE FROM tenants WHERE id=$1::uuid`, tenantID)
		}()
	}

	filters := `{"sources":["manual"]}`
	if *mode == "mock" {
		filters = `{"sources":["mock"]}`
	}
	var searchID string
	seedsJSON, _ := jsonMarshalStrings(seeds)
	if *mode == "http" {
		filters = `{"sources":["manual"],"seed_urls":` + seedsJSON + `}`
	}
	if err := cont.PG.QueryRow(ctx, `
		INSERT INTO lead_searches (tenant_id, user_id, keyword, query, filters, limit_count, status)
		VALUES ($1,$2,'benchmark','benchmark', $3, $4,'queued') RETURNING id::text`,
		tenantID, userID, filters, *count).Scan(&searchID); err != nil {
		slog.Error("search", "err", err)
		os.Exit(1)
	}

	// sampler: peak goroutines + RSS
	var peakG, peakMem int64
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				if g := int64(runtime.NumGoroutine()); g > atomic.LoadInt64(&peakG) {
					atomic.StoreInt64(&peakG, g)
				}
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				if m := int64(ms.Alloc); m > atomic.LoadInt64(&peakMem) {
					atomic.StoreInt64(&peakMem, m)
				}
			}
		}
	}()

	deps := search.NewDeps(cont.PG, cont.Cfg, cont.Log, "")
	deps.Registry = reg
	runner := search.NewRunner(deps)
	t0 := time.Now()
	if err := runner.Run(ctx, searchID); err != nil {
		slog.Error("run", "err", err)
		os.Exit(1)
	}
	dur := time.Since(t0)
	close(stop)

	var st string
	var found, saved, dup, crawled, qualified, failed int
	_ = cont.PG.QueryRow(ctx, `SELECT status, found_count, saved_count, duplicate_count, crawled_count, qualified_count, failed_count
		FROM lead_searches WHERE id=$1`, searchID).Scan(&st, &found, &saved, &dup, &crawled, &qualified, &failed)
	secs := dur.Seconds()
	rate := func(n int) float64 {
		if secs == 0 {
			return 0
		}
		return float64(n) / secs
	}
	fmt.Printf("\nmode=%s count=%d status=%s duration=%s\n", *mode, *count, st, dur.Round(time.Millisecond))
	fmt.Printf("found=%d saved=%d dup=%d crawled=%d qualified=%d failed=%d\n", found, saved, dup, crawled, qualified, failed)
	fmt.Printf("throughput: %.1f candidates/s, %.1f leads/s\n", rate(found), rate(saved))
	fmt.Printf("peak: %d goroutines, %.1f MB RSS\n", atomic.LoadInt64(&peakG), float64(atomic.LoadInt64(&peakMem))/(1<<20))
}

func jsonMarshalStrings(in []string) (string, error) {
	out := "["
	for i, s := range in {
		if i > 0 {
			out += ","
		}
		out += `"` + s + `"`
	}
	return out + "]", nil
}
