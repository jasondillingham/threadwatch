// SPDX-License-Identifier: Apache-2.0

package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics owns threadwatch's Prometheus collectors and exposes both a
// Registry (so other packages can register their own) and the HTTP
// handler that serves /metrics.
type Metrics struct {
	Registry *prometheus.Registry

	PollsTotal           *prometheus.CounterVec
	PollErrorsTotal      *prometheus.CounterVec
	EventsInsertedTotal  *prometheus.CounterVec
	GHRateLimitRemaining prometheus.Gauge
	GHRateLimitReset     prometheus.Gauge
	PollDurationSeconds  *prometheus.HistogramVec
}

// NewMetrics builds a Metrics with a private Registry (avoids polluting
// the global default).
func NewMetrics() *Metrics {
	reg := prometheus.NewRegistry()

	m := &Metrics{
		Registry: reg,

		PollsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "threadwatch_polls_total",
			Help: "Number of GitHub API calls made by the poller, by thread label, endpoint, and HTTP status code.",
		}, []string{"thread", "endpoint", "status"}),

		PollErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "threadwatch_poll_errors_total",
			Help: "Number of poller errors, classified by thread and error kind.",
		}, []string{"thread", "kind"}),

		EventsInsertedTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "threadwatch_events_inserted_total",
			Help: "Number of new events recorded, by thread label and source_kind.",
		}, []string{"thread", "source_kind"}),

		GHRateLimitRemaining: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "threadwatch_github_rate_limit_remaining",
			Help: "Remaining requests in the current GitHub rate-limit window (most recent value seen).",
		}),

		GHRateLimitReset: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "threadwatch_github_rate_limit_reset_seconds",
			Help: "Unix epoch seconds when the GitHub rate-limit window resets (most recent value seen).",
		}),

		PollDurationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "threadwatch_poll_duration_seconds",
			Help:    "Wall time of one full FetchThread call, by thread label.",
			Buckets: prometheus.DefBuckets,
		}, []string{"thread"}),
	}

	reg.MustRegister(
		m.PollsTotal,
		m.PollErrorsTotal,
		m.EventsInsertedTotal,
		m.GHRateLimitRemaining,
		m.GHRateLimitReset,
		m.PollDurationSeconds,
	)
	return m
}

// Handler returns the /metrics http.Handler using the package's private
// registry.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.Registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
		Registry:          m.Registry,
	})
}
