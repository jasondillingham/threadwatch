// SPDX-License-Identifier: Apache-2.0

// Command threadwatch is a self-hosted GitHub thread monitor.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasondillingham/threadwatch/internal/config"
	"github.com/jasondillingham/threadwatch/internal/github"
	"github.com/jasondillingham/threadwatch/internal/httpserver"
	"github.com/jasondillingham/threadwatch/internal/obs"
	"github.com/jasondillingham/threadwatch/internal/poller"
	"github.com/jasondillingham/threadwatch/internal/storage"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	logger := obs.NewLogger()
	if err := run(logger); err != nil {
		logger.Error("fatal", "err", err)
		os.Exit(1)
	}
}

// run wires up the application and blocks until shutdown. Returning an error
// instead of calling os.Exit inline lets deferred cleanup (signal stop,
// db.Close) actually run on the startup-failure paths.
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}
	logger.Info("starting",
		"version", version, "commit", commit, "build_date", buildDate,
		"listen", cfg.ListenAddr, "db", cfg.DatabasePath,
		"threads_config", cfg.ThreadsPath, "threads_declared", len(cfg.Threads),
		"poll_interval", cfg.PollInterval,
		"github_token_set", cfg.GitHubToken != "",
		"refresh_endpoint_enabled", cfg.RefreshToken != "",
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Storage.
	db, err := storage.Open(ctx, cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Reconcile declared threads with the DB. Idempotent.
	for _, t := range cfg.Threads {
		if _, err := db.UpsertThread(ctx, storage.Thread{
			Label: t.Label, Owner: t.Owner, Repo: t.Repo, Number: t.Number,
		}); err != nil {
			return fmt.Errorf("upsert thread %s/%s#%d: %w", t.Owner, t.Repo, t.Number, err)
		}
	}
	logger.Info("threads reconciled", "count", len(cfg.Threads))

	// Metrics.
	metrics := obs.NewMetrics()

	// GitHub client.
	ua := fmt.Sprintf("threadwatch/%s (+https://github.com/jasondillingham/threadwatch)", version)
	gh := github.NewClient(cfg.GitHubToken, ua)

	// Poller — owns the background polling loop.
	p := poller.New(db, gh, logger, metrics, cfg.PollInterval)

	// HTTP.
	srv, err := httpserver.New(db, logger, metrics, version, cfg.RefreshToken, p.Refresh)
	if err != nil {
		return fmt.Errorf("httpserver new: %w", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("http listening", "addr", cfg.ListenAddr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server crashed", "err", err)
			stop()
		}
	}()

	// Poller runs in its own goroutine; cancellation propagates from ctx.
	go p.Run(ctx)

	<-ctx.Done()
	logger.Info("shutdown requested")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}
