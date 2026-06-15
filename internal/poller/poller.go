// SPDX-License-Identifier: Apache-2.0

package poller

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/jasondillingham/threadwatch/internal/github"
	"github.com/jasondillingham/threadwatch/internal/obs"
	"github.com/jasondillingham/threadwatch/internal/storage"
)

// Fetcher is the interface the poller needs from the GitHub side; the
// concrete *github.Client satisfies it.
type Fetcher interface {
	FetchThread(ctx context.Context, ref github.ThreadRef, prev map[string]string) (github.FetchResult, error)
}

// Poller polls every thread in storage on a ticker and surfaces new
// events via the storage helpers. One Poller per process; Run blocks
// until ctx is canceled.
type Poller struct {
	DB      *storage.DB
	Fetcher Fetcher
	Logger  *slog.Logger
	Metrics *obs.Metrics

	Interval time.Duration

	refresh chan struct{}
}

// New builds a Poller. interval must be a sensible polling cadence (the
// config layer enforces >= 30s).
func New(db *storage.DB, fetcher Fetcher, logger *slog.Logger, metrics *obs.Metrics, interval time.Duration) *Poller {
	return &Poller{
		DB:       db,
		Fetcher:  fetcher,
		Logger:   logger,
		Metrics:  metrics,
		Interval: interval,
		refresh:  make(chan struct{}, 1),
	}
}

// Refresh signals the run loop to poll all threads immediately. It is
// non-blocking: if a refresh is already queued, the second call is a
// no-op (the queued refresh covers it).
func (p *Poller) Refresh() {
	select {
	case p.refresh <- struct{}{}:
	default:
	}
}

// Run blocks until ctx is canceled. It polls immediately on entry so a
// fresh deploy doesn't sit for `Interval` before doing useful work.
func (p *Poller) Run(ctx context.Context) {
	p.Logger.Info("poller starting", "interval", p.Interval)
	p.tickOnce(ctx)

	ticker := time.NewTicker(p.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			p.Logger.Info("poller exiting")
			return
		case <-ticker.C:
			p.tickOnce(ctx)
		case <-p.refresh:
			p.Logger.Info("poller refresh requested")
			p.tickOnce(ctx)
		}
	}
}

// tickOnce polls every thread once. Errors are logged + metric'd but
// don't stop the loop.
func (p *Poller) tickOnce(ctx context.Context) {
	threads, err := p.DB.ListThreads(ctx)
	if err != nil {
		p.Logger.Error("list threads", "err", err)
		return
	}
	for _, t := range threads {
		p.pollThread(ctx, t)
	}
}

func (p *Poller) pollThread(ctx context.Context, t storage.Thread) {
	start := time.Now()
	label := t.Label

	prevETags, err := p.DB.LoadETags(ctx, t.ID)
	if err != nil {
		p.Logger.Error("load etags", "thread", label, "err", err)
		p.Metrics.PollErrorsTotal.WithLabelValues(label, "storage").Inc()
		return
	}

	seen, err := p.DB.SeenIDs(ctx, t.ID)
	if err != nil {
		p.Logger.Error("load seen ids", "thread", label, "err", err)
		p.Metrics.PollErrorsTotal.WithLabelValues(label, "storage").Inc()
		return
	}

	ref := github.ThreadRef{Owner: t.Owner, Repo: t.Repo, Number: t.Number}
	result, err := p.Fetcher.FetchThread(ctx, ref, prevETags)
	dur := time.Since(start).Seconds()
	p.Metrics.PollDurationSeconds.WithLabelValues(label).Observe(dur)

	// Record per-endpoint status into both metrics and the polls table.
	for endpoint, status := range result.EndpointStatuses {
		p.Metrics.PollsTotal.WithLabelValues(label, endpoint, strconv.Itoa(status)).Inc()
		errMsg := ""
		if err != nil && status >= 400 {
			errMsg = err.Error()
		}
		if saveErr := p.DB.SavePollOutcome(ctx, t.ID, endpoint, storage.PollOutcome{
			ETag:       result.ETags[endpoint],
			StatusCode: status,
			Err:        errMsg,
		}); saveErr != nil {
			p.Logger.Warn("save poll outcome", "thread", label, "endpoint", endpoint, "err", saveErr)
		}
	}

	// Surface rate-limit headers when populated.
	if !result.RateLimit.Reset.IsZero() {
		p.Metrics.GHRateLimitRemaining.Set(float64(result.RateLimit.Remaining))
		p.Metrics.GHRateLimitReset.Set(float64(result.RateLimit.Reset.Unix()))
	}

	if err != nil {
		p.Metrics.PollErrorsTotal.WithLabelValues(label, classifyError(err)).Inc()
		p.Logger.Warn("fetch thread", "thread", label, "err", err)
		return
	}

	// Synthesise the Existing view from what storage knows and what the
	// fetcher just learned.
	existing := Existing{
		State:       t.State,
		Title:       t.Title,
		Kind:        t.Kind,
		LastEventAt: t.LastEventAt,
		SeenIDs:     seen,
	}
	diff := Diff(existing, result.Snapshot)

	// Apply: insert events, update thread row.
	rows := make([]storage.NewEventRow, len(diff.NewEvents))
	for i, e := range diff.NewEvents {
		rows[i] = storage.NewEventRow{
			SourceKind:  e.SourceKind,
			SourceID:    e.SourceID,
			EventType:   e.EventType,
			Actor:       e.Actor,
			OccurredAt:  e.OccurredAt,
			BodyExcerpt: e.BodyExcerpt,
			URL:         e.URL,
		}
	}
	inserted, err := p.DB.InsertEventsIfNew(ctx, t.ID, rows)
	if err != nil {
		p.Logger.Error("insert events", "thread", label, "err", err)
		p.Metrics.PollErrorsTotal.WithLabelValues(label, "storage").Inc()
		return
	}
	if inserted > 0 {
		// Aggregate by source_kind for the events_inserted_total metric.
		byKind := map[string]int{}
		for _, e := range diff.NewEvents {
			byKind[e.SourceKind]++
		}
		for k, n := range byKind {
			p.Metrics.EventsInsertedTotal.WithLabelValues(label, k).Add(float64(n))
		}
	}

	if err := p.DB.ApplyThreadUpdate(ctx, t.ID,
		diff.Update.State, diff.Update.Title, diff.Update.Kind, diff.Update.LastEventAt,
	); err != nil {
		p.Logger.Error("apply thread update", "thread", label, "err", err)
		p.Metrics.PollErrorsTotal.WithLabelValues(label, "storage").Inc()
		return
	}

	if inserted > 0 {
		p.Logger.Info("polled with new events",
			"thread", label, "inserted", inserted, "duration_ms", int(dur*1000))
	} else {
		p.Logger.Debug("polled, no new events", "thread", label, "duration_ms", int(dur*1000))
	}
}

func classifyError(err error) string {
	switch {
	case errors.Is(err, github.ErrNotFound):
		return "not_found"
	case errors.Is(err, github.ErrUnauthorized):
		return "auth"
	case errors.Is(err, github.ErrRateLimited):
		return "rate_limited"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "context"
	default:
		return "transient"
	}
}
