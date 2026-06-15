// SPDX-License-Identifier: Apache-2.0

// Package obs provides observability primitives: structured logging,
// Prometheus metrics, and (eventually) tracing.
package obs

import (
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a slog.Logger configured from environment.
//
// Honors:
//   - LOG_LEVEL: debug, info (default), warn, error
//   - LOG_FORMAT: json (default), text
func NewLogger() *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))
	format := strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT")))

	opts := &slog.HandlerOptions{Level: level}

	var h slog.Handler
	switch format {
	case "text":
		h = slog.NewTextHandler(os.Stdout, opts)
	default:
		h = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(h)
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
