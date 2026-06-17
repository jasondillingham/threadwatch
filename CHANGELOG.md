# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- `last_event_at` round-trip: `ApplyThreadUpdate` wrote the timestamp as RFC3339
  while the readers parsed SQLite `datetime` format, so it silently dropped to
  `nil` and the UI's "days quiet" column always showed "never". Aligned the
  write format and made the reader accept both layouts (heals existing rows).

### Added
- Unit tests for `internal/storage` — dedup core, upsert idempotency, list
  ordering, thread-update no-downgrade, ETag round-trip (0% → ~77% coverage).

## [0.1.1] - 2026-06-16

### Changed
- GitHub client now retries transient failures (5xx and transport errors) with
  bounded exponential backoff, so flaky upstream gateway responses (e.g. 504s)
  no longer skip a poll cycle. Tunable via `MaxRetries` / `RetryBackoff`.

## [0.1.0] - 2026-06-16

First release. A self-hosted GitHub thread monitor: it polls configured
issues/PRs on a schedule, stores their state in SQLite, and surfaces new
activity (comments, reviews, state changes) in a small web UI with Prometheus
metrics. Ships as a multi-arch container image and a Helm chart, behind a
hardened CI/release pipeline. Running on k3s.

### Added
- Service boot with `/healthz` + `/readyz`, structured logging, and a Helm chart.
- SQLite storage with migrations and a server-rendered index page.
- GitHub poller using ETag conditional requests, a per-thread event timeline,
  Prometheus metrics, and a token-gated refresh API.
- Multi-arch (amd64/arm64) container image.
- CI: golangci-lint (v2) gate, Trivy filesystem + image scans (fail on
  HIGH/CRITICAL), and race-enabled tests.
- Release automation on `v*` tags: build/push the image, scan the digest,
  cosign keyless-sign it, publish the Helm chart as an OCI artifact, and cut a
  GitHub Release.
- Chart values-reference README and a chart icon.

[Unreleased]: https://github.com/jasondillingham/threadwatch/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/jasondillingham/threadwatch/releases/tag/v0.1.1
[0.1.0]: https://github.com/jasondillingham/threadwatch/releases/tag/v0.1.0
