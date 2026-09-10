package platform

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"leadforge/db/migrations"
)

// Migrate applies all pending *.up.sql migrations in order.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	ups, err := migrationFiles(".up.sql")
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}
	for _, name := range ups {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version=$1)`, name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(version) VALUES($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		slog.Info("migration applied", "version", name)
	}
	return nil
}

// MigrateDown reverts the most recently applied migration using its .down.sql file.
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	var version string
	err := pool.QueryRow(ctx,
		`SELECT version FROM schema_migrations ORDER BY applied_at DESC, version DESC LIMIT 1`).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no migrations to roll back")
	}
	if err != nil {
		return err
	}
	downName := version[:len(version)-7] + ".down.sql"
	body, err := migrations.FS.ReadFile(downName)
	if err != nil {
		return fmt.Errorf("read %s: %w", downName, err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, string(body)); err != nil {
		_ = tx.Rollback(ctx)
		return fmt.Errorf("revert %s: %w", version, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version=$1`, version); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func migrationFiles(suffix string) ([]string, error) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var names []string
	for _, e := range entries {
		name := e.Name()
		if len(name) > len(suffix) && name[len(name)-len(suffix):] == suffix {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}
