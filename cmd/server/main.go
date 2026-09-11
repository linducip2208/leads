// Command server runs the LeadForge web application.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"leadforge/internal/auth"
	"leadforge/internal/csrf"
	"leadforge/internal/platform"
	"leadforge/internal/queue"
	"leadforge/internal/search"
	"leadforge/internal/session"
	"leadforge/internal/web"
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

	sess := session.NewManager(cont.PG, cont.Cfg.SessionSecret, cont.Cfg.SessionTTL, cont.Cfg.IsProd())
	csrfMgr := csrf.New(cont.Cfg.SessionSecret)
	authSvc := auth.NewService(cont.PG)

	ws := web.New(web.Config{
		AppName: cont.Cfg.AppName, Env: cont.Cfg.Env, Addr: cont.Cfg.Addr,
		AppURL: cont.Cfg.AppURL, RedisAddr: cont.Cfg.RedisAddr, Secret: cont.Cfg.SessionSecret,
		EncryptionSecret: cont.Cfg.EncryptionSecret(), AllowInsecureWebhooks: cont.Cfg.AllowInsecureWebhooks,
		InboundKey: cont.Cfg.InboundKey, MaxSearches: cont.Cfg.MaxConcurrentSearches,
		SearchWorkers: cont.Cfg.SearchProcessWorkers,
		HasGoogleKey:  os.Getenv("GOOGLE_PLACES_API_KEY") != "",
		CrawlGlobal:   cont.Cfg.CrawlerGlobalWorkers,
		BrowserOn:     cont.Cfg.BrowserEnabled, BrowserWorkers: cont.Cfg.BrowserWorkers,
		CrawlWorkers: cont.Cfg.CrawlerWorkers, CrawlDomain: cont.Cfg.CrawlerDomainConcurrency,
		CrawlTimeout: cont.Cfg.CrawlerTimeout.String(), CrawlMaxPages: cont.Cfg.CrawlerMaxPagesPerSite,
		CrawlMaxDepth: cont.Cfg.CrawlerMaxDepth,
		PprofEnabled:  cont.Cfg.PprofEnabled,
	}, cont.Log, cont.PG, authSvc, sess, csrfMgr)
	ws.Queue = queue.NewClient(cont.Cfg.RedisAddr)
	defer ws.Queue.Close()
	ws.Runner = search.NewRunner(search.NewDeps(cont.PG, cont.Cfg, cont.Log, os.Getenv("GOOGLE_PLACES_API_KEY")))
	ws.Crawler = ws.Runner.Manager()

	srv := &http.Server{
		Addr:              cont.Cfg.Addr,
		Handler:           ws.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      300 * time.Second, // SSE progress streams
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	// background maintenance: sweep expired sessions and tokens
	var sweepStopped atomic.Bool
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		sess.Sweep(ctx)
		for {
			select {
			case <-ctx.Done():
				sweepStopped.Store(true)
				return
			case <-t.C:
				sess.Sweep(context.WithoutCancel(ctx))
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		cont.Log.Info("http server listening", "addr", cont.Cfg.Addr, "app_url", cont.Cfg.AppURL)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			cont.Log.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		cont.Log.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		cont.Log.Error("graceful shutdown failed", "err", err)
	}
	if ws.Runner != nil && ws.Runner.Manager() != nil {
		ws.Runner.Manager().Close()
	}
	for !sweepStopped.Load() {
		time.Sleep(10 * time.Millisecond)
	}
	cont.Log.Info("server stopped")
}
