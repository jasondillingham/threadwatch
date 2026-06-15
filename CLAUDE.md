# threadwatch — context for Claude Code sessions

Self-hosted GitHub thread monitor: polls a configured list of issues/PRs every
15 minutes, surfaces new activity in a small web UI, exposes Prometheus metrics.
Lives publicly at https://github.com/jasondillingham/threadwatch.

## Start here

**Read [`docs/CHECKPOINT_D.md`](docs/CHECKPOINT_D.md) first.** It has the full
project state, what's left to do (tier-ranked), where every component lives, and
the resume-day commands. This file is the orientation; that file is the work
list.

## Where the project stands

Checkpoints A–C are complete and the tool is deployed:

- **A** — boots, `/healthz`, multi-arch image, minimal Helm chart
- **B** — SQLite storage with embedded migrations, ConfigMap-driven thread list,
  index page rendering configured threads
- **C** — GitHub fetcher with ETag + rate limit, diff logic (93.3% coverage),
  poller loop, thread detail page, Prometheus metrics, refresh API
- **D** — polish: PAT secret, GitOps wrapper, NPM ingress, golangci-lint,
  Trivy, Cosign, release.yml + v0.1.0 tag, OCI chart push, docs polish

Live on a k3s cluster (`threadwatch` namespace, release `threadwatch`, image
`ghcr.io/jasondillingham/threadwatch`).

## Architectural decisions worth knowing before changing things

These are documented in the repo README's "Design decisions" section; the short
version is here so you don't re-litigate any of them by accident:

- **SQLite, not Postgres.** One writer (the poller goroutine) + many readers
  (HTTP handlers) is exactly SQLite's WAL sweet spot. RWO PVC, `Recreate`
  deployment strategy on every chart upgrade.
- **In-process poller, not a CronJob.** Shared logger / connection pool /
  metrics registry, and `POST /api/threads/refresh` is a channel send rather
  than a Kubernetes API call. The poller has its own `defer recover()` so its
  panic doesn't take down `/healthz`.
- **REST + ETag conditional requests, not GraphQL.** Simpler to explain in
  interviews. The math fits comfortably in the 5000 req/hr authenticated quota.
- **Hand-written `web/static/tailwind.css`, not the Tailwind CLI.** Temporary —
  Checkpoint D Tier 4 will swap. The class API stays identical so it's
  mechanical when you do it.
- **Per-page `*template.Template` trees**, not a single parsed FS. Avoids the
  classic `{{define "content"}}` and `{{define "title"}}` collision when two
  pages share base.html. Hard-learned during the Checkpoint C smoke test.
- **The diff logic is the correctness core.** `internal/poller/diff.go`. Pure
  function, no I/O. If you touch it, run the table tests; if you add a new
  event source, add a case to the table.

## Conventions

- **Commits**: signed off via DCO trailer; use the `Signed-off-by` line with
  the `jasonmdillingham@gmail.com` address.
- **Author identity for git commits**: noreply (`31942663+jasondillingham@users.noreply.github.com`)
  — never the personal `jdillingham@caycemill.com` address in public history.
- **Branch**: `main` is the default; CI builds on push to main and PRs against main.
- **Releases**: tag `vX.Y.Z` triggers `release.yml` (Tier 2 item; not yet built).

## Daily commands

```bash
# Local dev loop
make run                     # go run ./cmd/threadwatch with default config
make test                    # go test ./... -race -count=1
make docker                  # build the image as ghcr.io/jasondillingham/threadwatch:dev
make helm-lint               # lint the chart

# Inspecting the live deployment
helm -n threadwatch list
kubectl -n threadwatch logs deploy/threadwatch --tail 30
kubectl -n threadwatch port-forward svc/threadwatch 8080:80
open http://localhost:8080
```

## When in doubt

Read `docs/CHECKPOINT_D.md`. It's where the next-action map and the
"where things live" reference are kept current.
