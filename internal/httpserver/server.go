// SPDX-License-Identifier: Apache-2.0

// Package httpserver wires the HTTP routes that serve threadwatch's UI and
// JSON API. It owns the slog access-log middleware and the recovery
// middleware that keeps a single panicking handler from killing the server.
package httpserver

import (
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/jasondillingham/threadwatch/internal/storage"
	"github.com/jasondillingham/threadwatch/web"
)

// Server is the configured HTTP server.
type Server struct {
	DB      *storage.DB
	Logger  *slog.Logger
	Version string

	tmpl *template.Template
	mux  *http.ServeMux
}

// New builds the Server, parses templates, and registers routes.
func New(db *storage.DB, logger *slog.Logger, version string) (*Server, error) {
	t, err := template.ParseFS(web.Templates, "templates/*.html")
	if err != nil {
		return nil, err
	}

	s := &Server{
		DB:      db,
		Logger:  logger,
		Version: version,
		tmpl:    t,
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

	staticFS, _ := fs.Sub(web.Static, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticFS))))

	mux.HandleFunc("GET /", s.handleIndex)
	return mux
}

// accessLog logs one structured line per request. /healthz is skipped to
// avoid dominating the logs with kubelet probes.
func (s *Server) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &recordingResponseWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(rw, r)
		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
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
