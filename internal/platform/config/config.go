package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application settings, loaded from environment variables
// with production-safe defaults for local development.
type Config struct {
	AppName string
	Env     string // local | development | production
	AppURL  string
	Addr    string

	DatabaseURL string
	RedisAddr   string

	SessionSecret string
	SessionTTL    time.Duration

	CrawlerWorkers           int
	CrawlerDomainConcurrency int
	CrawlerTimeout           time.Duration

	AIEnabled bool

	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string

	MaxRequestBytes int64
	ShardCount      int
}

func Load() (*Config, error) {
	c := &Config{
		AppName:         env("APP_NAME", "LeadForge"),
		Env:             env("APP_ENV", "local"),
		AppURL:          env("APP_URL", "http://localhost:8080"),
		Addr:            env("APP_ADDR", ":"+env("APP_PORT", "8080")),
		DatabaseURL:     env("DATABASE_URL", "postgres://postgres@127.0.0.1:5432/leadforge?sslmode=disable"),
		RedisAddr:       env("REDIS_ADDR", "127.0.0.1:6379"),
		SessionTTL:      30 * 24 * time.Hour,
		MaxRequestBytes: int64(envInt("MAX_REQUEST_MB", 32)) << 20,
	}

	c.SessionSecret = env("SESSION_SECRET", "")
	if c.SessionSecret == "" {
		if c.IsProd() {
			return nil, fmt.Errorf("SESSION_SECRET is required in production")
		}
		c.SessionSecret = "dev-only-insecure-secret-change-me"
	}

	c.CrawlerWorkers = envInt("CRAWLER_WORKERS", 50)
	c.CrawlerDomainConcurrency = envInt("CRAWLER_DOMAIN_CONCURRENCY", 2)
	c.CrawlerTimeout = time.Duration(envInt("CRAWLER_TIMEOUT_S", 15)) * time.Second

	c.AIEnabled = envBool("AI_ENABLED", false)

	c.SMTPHost = env("SMTP_HOST", "")
	c.SMTPPort = envInt("SMTP_PORT", 587)
	c.SMTPUsername = env("SMTP_USERNAME", "")
	c.SMTPPassword = env("SMTP_PASSWORD", "")
	c.SMTPFrom = env("SMTP_FROM", "")

	return c, nil
}

func (c *Config) IsProd() bool { return c.Env == "production" }

func (c *Config) WorkerQueues() map[string]int {
	// weighted concurrency per queue
	return map[string]int{
		"critical":     30,
		"search":       10,
		"crawler":      c.CrawlerWorkers,
		"enrichment":   10,
		"verification": 10,
		"outreach":     10,
		"ai":           2,
		"export":       3,
		"maintenance":  2,
	}
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}
