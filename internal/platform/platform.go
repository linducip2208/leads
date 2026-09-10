package platform

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"leadforge/internal/platform/config"
)

var requestIDSeq atomic.Uint64

// Container bundles shared infrastructure: config, logger, pg pool.
type Container struct {
	Cfg *config.Config
	Log *slog.Logger
	PG  *pgxpool.Pool
}

func NewContainer(ctx context.Context) (*Container, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	log := newLogger(cfg)
	pool, err := newPool(ctx, cfg, log)
	if err != nil {
		return nil, err
	}
	return &Container{Cfg: cfg, Log: log, PG: pool}, nil
}

func (c *Container) Close() {
	if c.PG != nil {
		c.PG.Close()
	}
}

func newLogger(cfg *config.Config) *slog.Logger {
	level := slog.LevelInfo
	if cfg.Env != "production" {
		level = slog.LevelDebug
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	base := slog.New(h)
	return base.With("app", cfg.AppName, "env", cfg.Env)
}

func newPool(ctx context.Context, cfg *config.Config, log *slog.Logger) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = cfg.DBMaxConns
	poolCfg.MinConns = cfg.DBMinConns
	poolCfg.MaxConnLifetime = cfg.DBMaxConnLifetime
	poolCfg.MaxConnIdleTime = 15 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	log.Info("postgres connected")
	return pool, nil
}
