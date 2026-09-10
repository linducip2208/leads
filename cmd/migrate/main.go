package main

import (
	"context"
	"log/slog"
	"os"

	"leadforge/internal/auth"
	"leadforge/internal/platform"
)

func main() {
	ctx := context.Background()
	cont, err := platform.NewContainer(ctx)
	if err != nil {
		slog.Error("connect", "err", err)
		os.Exit(1)
	}
	defer cont.Close()

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "up":
		if err := platform.Migrate(ctx, cont.PG); err != nil {
			cont.Log.Error("migrate failed", "err", err)
			os.Exit(1)
		}
		if err := auth.NewService(cont.PG).EnsureSystemRoles(ctx); err != nil {
			cont.Log.Error("seed system roles failed", "err", err)
			os.Exit(1)
		}
		cont.Log.Info("migrations up to date")
	case "down":
		if err := platform.MigrateDown(ctx, cont.PG); err != nil {
			cont.Log.Error("rollback failed", "err", err)
			os.Exit(1)
		}
		cont.Log.Info("last migration reverted")
	default:
		cont.Log.Error("unknown command", "cmd", cmd)
		os.Exit(2)
	}
}
