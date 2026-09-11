package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"leadforge/internal/crawler"
	"leadforge/internal/platform/config"
	"leadforge/internal/queue"
)

func main() {
	fmt.Println("LeadForge Production Check")
	cfg, err := config.Load()
	if err != nil {
		fail("configuration", err)
		return
	}
	pass("configuration")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		fail("PostgreSQL URL", err)
	} else {
		poolCfg.MaxConns = 1
		poolCfg.MinConns = 0
		pool, e := pgxpool.NewWithConfig(ctx, poolCfg)
		if e != nil {
			fail("PostgreSQL", e)
		} else {
			if e = pool.Ping(ctx); e != nil {
				fail("PostgreSQL", e)
			} else {
				pass("PostgreSQL")
			}
			var n int
			if e = pool.QueryRow(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&n); e != nil {
				fail("database migrations", e)
			} else if n == 0 {
				warn("database migrations", "schema_migrations is empty")
			} else {
				pass("database migrations")
			}
			pool.Close()
		}
	}
	if err := queue.PingRedis(ctx, cfg.RedisAddr); err != nil {
		fail("Redis", err)
	} else {
		pass("Redis")
	}
	if len(cfg.SessionSecret) >= 32 {
		pass("Session secret")
	} else {
		fail("Session secret", fmt.Errorf("must be at least 32 bytes"))
	}
	if len(cfg.EncryptionSecret()) >= 32 {
		pass("Encryption key")
	} else {
		fail("Encryption key", fmt.Errorf("must be at least 32 bytes"))
	}
	if cfg.BrowserEnabled {
		p := crawler.NewBrowserPool(crawler.BrowserOptions{Enabled: true, Workers: 1, Timeout: 8 * time.Second, MaxPages: 1})
		if p.Available() {
			pass("Chrome")
			p.Close()
		} else {
			warn("Chrome", "unavailable — HTTP-only crawler")
		}
	} else {
		warn("Chrome", "disabled — HTTP-only crawler")
	}
	if os.Getenv("GOOGLE_PLACES_API_KEY") == "" {
		warn("Google Places", "not configured")
	} else {
		pass("Google Places configuration")
	}
	if cfg.SMTPHost == "" {
		warn("SMTP", "not configured")
	} else {
		pass("SMTP configuration")
	}
	fmt.Printf("[INFO] Go %s\n", runtime.Version())
}

func pass(name string)            { fmt.Printf("[PASS] %s\n", name) }
func warn(name, msg string)       { fmt.Printf("[WARN] %s — %s\n", name, msg) }
func fail(name string, err error) { fmt.Printf("[FAIL] %s — %s\n", name, err) }
