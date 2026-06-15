// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"net/http"
	"time"

	"github.com/jasondillingham/threadwatch/internal/storage"
)

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := s.DB.SQL().PingContext(r.Context()); err != nil {
		s.Logger.Warn("readyz: db ping failed", "err", err)
		http.Error(w, "db unreachable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("ok\n"))
}

// indexThread decorates a storage.Thread with derived display fields the
// template wants to render without doing arithmetic.
type indexThread struct {
	storage.Thread
	DaysQuiet int
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.ListThreads(r.Context())
	if err != nil {
		s.Logger.Error("list threads", "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	now := time.Now().UTC()
	enriched := make([]indexThread, 0, len(rows))
	for _, t := range rows {
		it := indexThread{Thread: t}
		if t.LastEventAt != nil {
			it.DaysQuiet = int(now.Sub(*t.LastEventAt).Hours()) / 24
		}
		enriched = append(enriched, it)
	}

	data := struct {
		Version string
		Threads []indexThread
	}{
		Version: s.Version,
		Threads: enriched,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, "base", data); err != nil {
		s.Logger.Error("render index", "err", err)
	}
}
