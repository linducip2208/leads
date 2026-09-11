// Command worker runs Asynq background jobs: lead searches, maintenance.
package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/hibiken/asynq"

	"leadforge/internal/auth"
	"leadforge/internal/mail"
	"leadforge/internal/outreach"
	"leadforge/internal/platform"
	"leadforge/internal/queue"
	"leadforge/internal/search"
	"leadforge/internal/session"
	"leadforge/internal/webhookstore"
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
	if err := auth.NewService(cont.PG).EnsureSystemRoles(ctx); err != nil {
		cont.Log.Warn("ensure system roles", "err", err)
	}

	qcEmit := queue.NewClient(cont.Cfg.RedisAddr)
	defer qcEmit.Close()
	searchDeps := search.NewDeps(cont.PG, cont.Cfg, cont.Log, os.Getenv("GOOGLE_PLACES_API_KEY"))
	searchDeps.Emit = func(ctx context.Context, tenantID, event string, payload map[string]any) {
		if err := webhookstore.Emit(ctx, cont.PG, tenantID, event, payload,
			func(webhookID, ev string, body []byte) error {
				return qcEmit.EnqueueWebhook(ctx, webhookID, ev, body)
			}); err != nil {
			cont.Log.Warn("webhook event enqueue failed", "event", event, "tenant_id", tenantID, "err", err)
		}
	}
	runner := search.NewRunner(searchDeps)
	sess := session.NewManager(cont.PG, cont.Cfg.SessionSecret, cont.Cfg.SessionTTL, cont.Cfg.IsProd())

	mux := asynq.NewServeMux()
	mux.HandleFunc(queue.TypeSearchRun, func(ctx context.Context, t *asynq.Task) error {
		var p queue.SearchPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil || p.SearchID == "" {
			return nil // poison payload: drop
		}
		cont.Log.Info("search run started", "search_id", p.SearchID)
		if err := runner.Run(ctx, p.SearchID); err != nil {
			cont.Log.Error("search run failed", "search_id", p.SearchID, "err", err)
			markSearchFailed(ctx, cont, p.SearchID, err.Error())
			return err
		}
		cont.Log.Info("search run finished", "search_id", p.SearchID)
		return nil
	})
	mux.HandleFunc(queue.TypeSweep, func(ctx context.Context, _ *asynq.Task) error {
		sess.Sweep(ctx)
		return nil
	})
	outDeps := &outreach.Deps{Pool: cont.PG, Log: cont.Log, Secret: cont.Cfg.EncryptionSecret(), AppURL: cont.Cfg.AppURL}
	mux.HandleFunc(queue.TypeOutreachTick, func(ctx context.Context, _ *asynq.Task) error {
		// promote due scheduled campaigns
		_, _ = cont.PG.Exec(ctx, `UPDATE campaigns SET status='running', started_at=COALESCE(started_at,now()) WHERE status='scheduled' AND scheduled_at <= now()`)
		// enqueue one send batch per running campaign
		rows, err := cont.PG.Query(ctx, `SELECT id::text FROM campaigns WHERE status='running'`)
		if err != nil {
			return err
		}
		defer rows.Close()
		qc := queue.NewClient(cont.Cfg.RedisAddr)
		defer qc.Close()
		var enqueueErr error
		for rows.Next() {
			var cid string
			if err := rows.Scan(&cid); err == nil {
				if err := qc.EnqueueOutreachSend(ctx, cid); err != nil && enqueueErr == nil {
					enqueueErr = err
				}
			}
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return enqueueErr
	})
	mux.HandleFunc(queue.TypeOutreachSend, func(ctx context.Context, t *asynq.Task) error {
		var p queue.OutreachPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil || p.CampaignID == "" {
			return nil
		}
		sent, err := outreach.RunBatch(ctx, outDeps, mail.SMTPSender{}, p.CampaignID)
		if err != nil {
			cont.Log.Error("outreach batch failed", "campaign", p.CampaignID, "err", err)
			return err
		}
		cont.Log.Info("outreach batch done", "campaign", p.CampaignID, "sent", sent)
		// auto-complete drained campaigns + emit
		var open int
		_ = cont.PG.QueryRow(ctx, `SELECT COUNT(*) FROM campaign_contacts WHERE campaign_id=$1::uuid AND status IN ('pending','active')`, p.CampaignID).Scan(&open)
		if open == 0 {
			var tid string
			_ = cont.PG.QueryRow(ctx, `UPDATE campaigns SET status='completed', completed_at=now() WHERE id=$1::uuid AND status='running' RETURNING tenant_id::text`, p.CampaignID).Scan(&tid)
			if tid != "" {
				if err := webhookstore.Emit(ctx, cont.PG, tid, "campaign.completed", map[string]any{"campaign_id": p.CampaignID},
					func(wid, ev string, body []byte) error {
						return qcEmit.EnqueueWebhook(ctx, wid, ev, body)
					}); err != nil {
					cont.Log.Warn("campaign completion webhook enqueue failed", "campaign_id", p.CampaignID, "err", err)
				}
			}
		}
		return nil
	})
	mux.HandleFunc(queue.TypeLeadRefresh, func(ctx context.Context, t *asynq.Task) error {
		var p queue.RefreshPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil || p.LeadID == "" {
			return nil
		}
		return runner.RefreshLead(ctx, p.LeadID)
	})
	mux.HandleFunc(queue.TypeLeadBulkRefresh, func(ctx context.Context, t *asynq.Task) error {
		var p queue.BulkRefreshPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil {
			return nil
		}
		for _, id := range p.LeadIDs {
			if err := runner.RefreshLead(ctx, id); err != nil {
				cont.Log.Warn("bulk refresh item failed", "lead", id, "err", err)
			}
		}
		return nil
	})
	mux.HandleFunc(queue.TypeRefreshStale, func(ctx context.Context, _ *asynq.Task) error {
		n := runner.RefreshStale(ctx, 50)
		cont.Log.Info("stale refresh done", "refreshed", n)
		return nil
	})
	mux.HandleFunc(queue.TypeWebhookDeliver, func(ctx context.Context, t *asynq.Task) error {
		var p queue.WebhookPayload
		if err := json.Unmarshal(t.Payload(), &p); err != nil || p.WebhookID == "" {
			return nil
		}
		return webhookstore.Deliver(ctx, cont.PG, cont.Cfg.EncryptionSecret(), p.WebhookID, p.Event, p.Body, cont.Cfg.AllowInsecureWebhooks)
	})
	mux.HandleFunc(queue.TypeWatchdog, func(ctx context.Context, _ *asynq.Task) error {
		staleMin := cont.Cfg.WatchdogStaleMinutes
		if staleMin <= 0 {
			staleMin = 15
		}
		res, err := cont.PG.Exec(ctx, `
			UPDATE lead_searches SET status='failed', error='worker lost (watchdog)', finished_at=now(), updated_at=now()
			WHERE status='running' AND COALESCE(last_heartbeat, updated_at, started_at) < now() - (make_interval(mins => $1))`, staleMin)
		if err == nil && res.RowsAffected() > 0 {
			cont.Log.Warn("watchdog marked stalled searches failed", "n", res.RowsAffected())
		}
		return err
	})

	srv := asynq.NewServer(queue.RedisOpt(cont.Cfg.RedisAddr), asynq.Config{
		Concurrency:     20,
		Queues:          cont.Cfg.WorkerQueues(),
		ShutdownTimeout: 30 * time.Second,
		Logger:          newSlogAdapter(cont.Log),
		ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
			cont.Log.Error("task error", "type", task.Type(), "err", err)
		}),
	})

	// liveness heartbeat for /admin/health
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		workerBeatKey := "leadforge:worker:hb:" + strconv.Itoa(os.Getpid())
		for {
			beatCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = queue.Beat(beatCtx, cont.Cfg.RedisAddr, "leadforge:worker:hb", 30*time.Second)
			_ = queue.Beat(beatCtx, cont.Cfg.RedisAddr, workerBeatKey, 30*time.Second)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()

	cont.Log.Info("worker listening", "redis_configured", cont.Cfg.RedisAddr != "")
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(mux) }()
	select {
	case err := <-runErr:
		if err != nil {
			cont.Log.Error("worker failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		cont.Log.Info("worker shutdown signal received")
		srv.Shutdown()
		if err := <-runErr; err != nil {
			cont.Log.Error("worker shutdown failed", "err", err)
		}
	}
	runner.Manager().Close()
}

func markSearchFailed(ctx context.Context, cont *platform.Container, searchID, msg string) {
	if len(msg) > 500 {
		msg = msg[:500]
	}
	_, _ = cont.PG.Exec(ctx, `
		UPDATE lead_searches SET status='failed', error=$2, finished_at=now(), updated_at=now()
		WHERE id=$1 AND status IN ('queued','running','paused')`, searchID, msg)
}

// slogAdapter adapts slog to asynq's logger interface.
type slogAdapter struct{ log *slog.Logger }

func newSlogAdapter(l *slog.Logger) *slogAdapter { return &slogAdapter{log: l} }

func (a *slogAdapter) Debug(args ...any) { a.log.Debug("asynq", "args", args) }
func (a *slogAdapter) Info(args ...any)  { a.log.Info("asynq", "args", args) }
func (a *slogAdapter) Warn(args ...any)  { a.log.Warn("asynq", "args", args) }
func (a *slogAdapter) Error(args ...any) { a.log.Error("asynq", "args", args) }
func (a *slogAdapter) Fatal(args ...any) { a.log.Error("asynq fatal", "args", args); os.Exit(1) }
