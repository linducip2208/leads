// Command scheduler enqueues periodic maintenance tasks.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"leadforge/internal/platform"
	"leadforge/internal/queue"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cont, err := platform.NewContainer(ctx)
	if err != nil {
		slog.Error("startup failed", "err", err)
		os.Exit(1)
	}
	defer cont.Close()

	sched := asynq.NewScheduler(queue.RedisOpt(cont.Cfg.RedisAddr), &asynq.SchedulerOpts{})
	sweepTask := asynq.NewTask(queue.TypeSweep, nil)
	if _, err := sched.Register("@every 6h", sweepTask, asynq.Queue(queue.QMaintenance)); err != nil {
		cont.Log.Error("register sweep", "err", err)
		os.Exit(1)
	}
	watchTask := asynq.NewTask(queue.TypeWatchdog, nil)
	if _, err := sched.Register("@every 5m", watchTask, asynq.Queue(queue.QMaintenance)); err != nil {
		cont.Log.Error("register watchdog", "err", err)
		os.Exit(1)
	}
	tickTask := asynq.NewTask(queue.TypeOutreachTick, nil)
	if _, err := sched.Register("@every 1m", tickTask, asynq.Queue(queue.QOutreach)); err != nil {
		cont.Log.Error("register outreach tick", "err", err)
		os.Exit(1)
	}

	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			_ = queue.Beat(context.WithoutCancel(ctx), cont.Cfg.RedisAddr, "leadforge:scheduler:hb", 30*time.Second)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	cont.Log.Info("scheduler started")
	if err := sched.Run(); err != nil {
		cont.Log.Error("scheduler failed", "err", err)
		os.Exit(1)
	}
}
