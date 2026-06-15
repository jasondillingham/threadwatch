// SPDX-License-Identifier: Apache-2.0

package httpserver

import (
	"fmt"
	"net/http"
	"strconv"
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
		Version   string
		PageTitle string
		Threads   []indexThread
	}{
		Version:   s.Version,
		PageTitle: "Tracked threads",
		Threads:   enriched,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.indexTmpl.ExecuteTemplate(w, "base", data); err != nil {
		s.Logger.Error("render index", "err", err)
	}
}

// handleThread renders the per-thread event timeline page at /threads/{id}.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		http.NotFound(w, r)
		return
	}

	thread, err := s.DB.GetThread(r.Context(), id)
	if err != nil {
		if err == storage.ErrNotFound {
			http.NotFound(w, r)
			return
		}
		s.Logger.Error("get thread", "id", id, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	events, err := s.DB.ListEvents(r.Context(), id, 200)
	if err != nil {
		s.Logger.Error("list events", "id", id, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	data := struct {
		Version   string
		PageTitle string
		Thread    storage.Thread
		Events    []storage.Event
		GitHubURL string
	}{
		Version:   s.Version,
		PageTitle: thread.Label,
		Thread:    thread,
		Events:    events,
		GitHubURL: fmt.Sprintf("https://github.com/%s/%s/issues/%d",
			thread.Owner, thread.Repo, thread.Number),
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.threadTmpl.ExecuteTemplate(w, "base", data); err != nil {
		s.Logger.Error("render thread", "id", id, "err", err)
	}
}

// handleRefresh forwards to the configured OnRefresh callback if the
// X-Refresh-Token header matches.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Refresh-Token") != s.RefreshToken {
		http.NotFound(w, r) // intentionally 404 (don't advertise existence)
		return
	}
	s.OnRefresh()
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("refresh queued\n"))
}
