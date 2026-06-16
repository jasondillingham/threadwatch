# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project aims
to follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- golangci-lint (v2) configuration and a CI `lint` job gating `build-and-push`.
- Trivy filesystem and image scans in CI; builds fail on HIGH/CRITICAL.
- `release.yml`: on `v*` tags, build/push the multi-arch image, scan the
  digest, cosign keyless-sign it, publish the Helm chart as an OCI artifact,
  and cut a GitHub Release.
- Helm chart values-reference README and a chart icon.

### Changed
- `cmd/threadwatch` refactored to the `run() error` pattern so deferred
  cleanup (signal stop, DB close) runs on startup-failure paths instead of
  being skipped by `os.Exit`.

## [0.1.0-alpha.1] - 2026-06-15

Initial walking skeleton (Checkpoints A–C). Deployed to k3s.

### Added
- **Checkpoint A** — service boots, `/healthz` + `/readyz`, multi-arch
  container image, Helm chart.
- **Checkpoint B** — SQLite storage with migrations, server-rendered index
  page.
- **Checkpoint C** — GitHub poller using ETag conditional requests, per-thread
  event timeline, Prometheus metrics, and a (token-gated) refresh API.

[Unreleased]: https://github.com/jasondillingham/threadwatch/compare/v0.1.0-alpha.1...HEAD
[0.1.0-alpha.1]: https://github.com/jasondillingham/threadwatch/releases/tag/v0.1.0-alpha.1
