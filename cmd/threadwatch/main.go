// SPDX-License-Identifier: Apache-2.0

// Command threadwatch is a self-hosted GitHub thread monitor.
//
// V1 polls a configured list of issues and pull requests on a schedule
// and surfaces new activity (comments, reviews, state changes) via a
// small web UI and JSON API. See README.md for the design.
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jasondillingham/threadwatch/internal/config"
	"github.com/jasondillingham/threadwatch/internal/httpserver"
	"github.com/jasondillingham/threadwatch/internal/obs"
	"github.com/jasondillingham/threadwatch/internal/storage"
)

// Build-time metadata, set via -ldflags.
var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	logger := obs.NewLogger()

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load", "err", err)
		os.Exit(1)
	}
	logger.Info("starting",
		"version", version, "commit", commit, "build_date", buildDate,
		"listen", cfg.ListenAddr, "db", cfg.DatabasePath,
		"threads_config", cfg.ThreadsPath, "threads_declared", len(cfg.Threads),
		"poll_interval", cfg.PollInterval,
	)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.DatabasePath)
	if err != nil {
		logger.Error("storage open", "err", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	// Reconcile the declared threads with the DB. Idempotent.
	for _, t := range cfg.Threads {
		if _, err := db.UpsertThread(ctx, storage.Thread{
			Label:  t.Label,
			Owner:  t.Owner,
			Repo:   t.Repo,
			Number: t.Number,
		}); err != nil {
			logger.Error("upsert thread", "owner", t.Owner, "repo", t.Repo, "number", t.Number, "err", err)
			os.Exit(1)
		}
	}
	logger.Info("threads reconciled", "count", len(cfg.Threads))

	srv, err := httpserver.New(db, logger, version)
	if err != nil {
		logger.Error("httpserver new", "err", err)
		os.Exit(1)
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

	<-ctx.Done()
	logger.Info("shutdown requested")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
