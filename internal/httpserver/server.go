// SPDX-License-Identifier: Apache-2.0

// Package httpserver wires the HTTP routes that serve threadwatch's UI and
// JSON API. It owns the slog access-log middleware and the recovery
// middleware that keeps a single panicking handler from killing the server.
package httpserver

import (
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jasondillingham/threadwatch/internal/obs"
	"github.com/jasondillingham/threadwatch/internal/storage"
	"github.com/jasondillingham/threadwatch/web"
)

// Server is the configured HTTP server.
type Server struct {
	DB           *storage.DB
	Logger       *slog.Logger
	Metrics      *obs.Metrics
	Version      string
	RefreshToken string // empty disables the refresh endpoint
	OnRefresh    func() // invoked by /api/threads/refresh; usually poller.Refresh

	// Each page lives in its own *template.Template so collisions on
	// shared block names ("content", "title") are impossible.
	indexTmpl  *template.Template
	threadTmpl *template.Template

	mux *http.ServeMux
}

// New builds the Server, parses templates, and registers routes.
func New(db *storage.DB, logger *slog.Logger, metrics *obs.Metrics, version, refreshToken string, onRefresh func()) (*Server, error) {
	indexTmpl, err := template.ParseFS(web.Templates, "templates/base.html", "templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("parse index template: %w", err)
	}
	threadTmpl, err := template.ParseFS(web.Templates, "templates/base.html", "templates/thread.html")
	if err != nil {
		return nil, fmt.Errorf("parse thread template: %w", err)
	}

	s := &Server{
		DB:           db,
		Logger:       logger,
		Metrics:      metrics,
		Version:      version,
		RefreshToken: refreshToken,
		OnRefresh:    onRefresh,
		indexTmpl:    indexTmpl,
		threadTmpl:   threadTmpl,
	}
	s.mux = s.routes()
	return s, nil
}

// Handler returns the http.Handler suitable for http.Server.Handler. Wraps
// the mux in the access-log + recovery middleware.
func (s *Server) Handler() http.Handler {
	return s.recover(s.accessLog(s.mux))
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	if s.Metrics != nil {
		mux.Handle("GET /metrics", s.Metrics.Handler())
	}

	staticFS, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /", s.handleIndex)
	mux.HandleFunc("GET /threads/{id}", s.handleThread)

	// Refresh endpoint: only registered when REFRESH_TOKEN is set. When
	// unset, the path simply 404s through the default mux.
	if s.RefreshToken != "" && s.OnRefresh != nil {
		mux.HandleFunc("POST /api/threads/refresh", s.handleRefresh)
	}

	return mux
}

// accessLog logs one structured line per request. /healthz, /readyz and
// /metrics are skipped to avoid dominating the logs with kubelet and
// scraper traffic.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recordingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			return
		}
		s.Logger.Info("http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
			"bytes", rw.bytes,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote", r.RemoteAddr,
		)
	})
}

// recover catches handler panics, logs them, and returns 500.
func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				s.Logger.Error("panic in handler",
					"path", r.URL.Path,
					"panic", p,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type recordingResponseWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *recordingResponseWriter) WriteHeader(s int) {
	r.status = s
	r.ResponseWriter.WriteHeader(s)
}
func (r *recordingResponseWriter) Write(b []byte) (int, error) {
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}
