# Checkpoint D — Polish pass

threadwatch is functionally complete after Checkpoint C. A pod is running on
k3s, polling every 15 minutes, surfacing events in a web UI, and exposing
Prometheus metrics. This document captures everything left for "resume-ready V1"
and is structured so a future session can resume cold.

## State as of 2026-06-15 (post Checkpoint C)

| Layer | State |
|---|---|
| Source | `~/Documents/Code/threadwatch/` on `main`, public at github.com/jasondillingham/threadwatch |
| Deployed | `helm -n threadwatch list` shows release `threadwatch` v0.1.0-alpha.1 revision 3, pod Running on thor |
| GitOps wrapper | **Missing.** `~/Documents/Homelab/k8s/threadwatch/` does not exist yet |
| Image | `ghcr.io/jasondillingham/threadwatch:sha-4a6ed1c` (multi-arch amd64+arm64) |
| Public access | **Not exposed.** Only reachable via `kubectl port-forward` |
| GitHub auth | **Unauthenticated.** Polling under the 60 req/hr quota (last poll left 45/60 remaining) |
| Tag | None. Untagged on `main` |
| Chart published | No. Only present in repo at `charts/threadwatch/` |

## Progress update — 2026-06-16

Worked on branch `checkpoint-d/supply-chain` (threadwatch) + `feat/headlamp-dashboard`
(homelab k8s). All commits are local — **not yet pushed**; Jason pushes.

| Item | Status |
|---|---|
| 1.1 GitHub PAT | **Wrapper built, blocked on Jason.** Uses ESO+Bitwarden (homelab standard), not raw `kubectl create secret`. Files at `~/Documents/Homelab/k8s/threadwatch/`. Needs: mint fine-grained public-repo-read PAT → store in Bitwarden as `threadwatch_GITHUB_TOKEN` → paste the item UUID → fill `01-externalsecret.yaml` `remoteRef.key` → `./deploy.sh` → verify `github_token_set=true` + `rate_limit_remaining≈4990`. |
| 1.2 GitOps wrapper | ✅ **Done** (built as the home for 1.1's ExternalSecret). `k8s/threadwatch/`: README, values.yaml (pins `sha-4a6ed1c`, `github.existingSecret`), deploy.sh, 01-externalsecret.yaml. Uncommitted in the private homelab repo. |
| 1.3 NPM proxy | ⏳ Pending (Jason — NPM admin UI). Note: cluster already serves `*.k8s.dillinghamhouse.com` via traefik+VIP 10.66.0.160, so a traefik Ingress is an alternative to an NPM hop. |
| 2.4 golangci-lint | ✅ **Done.** `.golangci.yml` is **v2 schema** (repo's golangci-lint is v2.12.2 — the doc's v1 assumption was wrong); action is `golangci-lint-action@v9` (v6 only supports v1). Fixed all 17 findings incl. a `run() error` refactor of `cmd/threadwatch`. `max-same-issues: 0` so CI never hides repeats. |
| 2.5 Trivy | ✅ **Done.** `security` job (`trivy fs`) gates build; `trivy image` scans the pushed digest. Both verified locally = 0 HIGH/CRITICAL. |
| 2.6 cosign | ✅ **Done** (in release.yml — signs the immutable digest, keyless OIDC). |
| 2.7 release.yml | ✅ **Workflow done.** Build→scan→cosign→OCI chart push→GitHub release on `v*` tags. ⏳ First tag is Jason's: push `v0.0.1-rc.1` to dry-run the chain first (tag triggers have no branch dry-run), then `v0.1.0`. **Caught + fixed a real bug:** the chart's `image.tag` defaults to `.Chart.AppVersion`, so the image must be tagged with the v-stripped version or the published chart `ImagePullBackOff`s for installers (invisible locally because the homelab wrapper pins `sha-`). |
| 3.8 screenshots + diagram | Diagram ✅ (Mermaid in README). ⏳ Screenshots are Jason's (need a browser): `kubectl -n threadwatch port-forward deploy/threadwatch 8080:8080`, capture index + thread pages into `docs/screenshots/`. |
| 3.9 chart README | ✅ **Done** — full values table. |
| 3.10 CHANGELOG | ✅ **Done** — Unreleased + 0.1.0-alpha.1 backfill. |
| 3.11 CONTRIBUTING | ✅ **Done** — DCO, make-based dev loop, conventions. |
| 3.12 chart icon | ✅ **Done** — `icon.svg` + `Chart.yaml`; clears the helm-lint INFO. |

**Bonus:** corrected the stale README Status (claimed Checkpoint A "wired"/B-C "next" — all done & deployed).

**After the first release tag (one-time, Jason, web UI):** flip the new
`ghcr.io/jasondillingham/charts/threadwatch` package to **Public** (defaults
private → 401 on `helm pull`). The **image** package is already public; the
chart package doesn't exist until `helm push` runs. The published chart
contains only already-public repo files (no PAT, no homelab specifics — those
live in the private `k8s/threadwatch/` wrapper / runtime Secrets).

**Follow-up #13 — intermittent GitHub 504s.** Logs show occasional 504
"Hello future GitHubber" HTML responses on fetches; that's GitHub's own gateway
hiccup, distinct from the rate limit. Re-check after the PAT lands; if it
persists, add retry/backoff in `internal/github`. Not a V1 blocker.

**Adjacent:** Headlamp cluster dashboard deployed this session (homelab
`feat/headlamp-dashboard`) at `https://headlamp.k8s.dillinghamhouse.com` —
read-only by default, `edit`-role on-demand elevation. Unrelated to threadwatch
but done in the same session.

## Test status

| Package | Coverage |
|---|---|
| `internal/poller` | 93.3% |
| `internal/github` | 82.6% |
| `internal/storage`, `internal/httpserver`, `internal/config`, `internal/obs` | 0% (V2) |

The planner's "test the diff logic exhaustively, everything else is replaceable"
shipped. Other packages get tests in Checkpoint D opportunistically (notably the
storage helpers, since they're the next-most-likely place for a regression).

## Checkpoint D scope, grouped by value

### Tier 1 — biggest deltas to the running tool

These three change what threadwatch is for an operator: rate-limit headroom,
URL-accessibility, and "the deployment is recorded in GitOps."

1. **GitHub PAT in a Secret** (rate limit 60/hr → 5000/hr)
   - Create a fine-grained PAT (public-repo read scope only) at https://github.com/settings/personal-access-tokens
   - Store via Bitwarden Secrets Manager: `threadwatch_GITHUB_TOKEN`. Render to `~/dockers/threadwatch/...` only if we ever deploy via Docker; for k3s, write a Kubernetes Secret instead
   - `kubectl -n threadwatch create secret generic threadwatch-github --from-literal=token=<PAT>` (or via External Secrets when wired)
   - `helm upgrade --set github.existingSecret=threadwatch-github`
   - Verify with `kubectl -n threadwatch logs` — `github_token_set=true` should appear in the startup line
   - Verify metric `threadwatch_github_rate_limit_remaining` jumps to ~4990

2. **Homelab GitOps wrapper at `~/Documents/Homelab/k8s/threadwatch/`**
   - Three files:
     - `README.md` — pointer to the chart, the values-override pattern, deploy commands
     - `values.yaml` — homelab-specific overrides (image tag pin, `github.existingSecret: threadwatch-github`, optional ingress when NPM eventually delegates)
     - `Makefile` or `deploy.sh` — `helm upgrade --install threadwatch oci://ghcr.io/jasondillingham/charts/threadwatch --version <X> -f values.yaml` (once the chart is published to OCI — see Tier 2 item 2)
   - Commit + push to the private homelab repo
   - When ArgoCD lands in the cluster (per the homelab `k8s/TODO.md`), an `Application` resource picks up this directory and the running release becomes GitOps-tracked

3. **NPM proxy host `threadwatch.dillinghamhouse.com`**
   - NPM admin → Hosts → Proxy Hosts → Add
   - Forward Hostname: `10.43.147.84` (the threadwatch Service ClusterIP) or use the cluster DNS once NPM is namespace-aware
   - Wildcard cert is already in place; force SSL, HTTP/2, HSTS, block exploits
   - pfSense already has wildcard DNS for `*.dillinghamhouse.com` pointing at NPM (10.66.0.20), so no DNS change needed
   - Done when `https://threadwatch.dillinghamhouse.com` returns the index page

### Tier 2 — supply-chain + release plumbing

These are the items that make the repo "resume readable" rather than "personal
side project."

4. **golangci-lint config**
   - Add `.golangci.yml` with the shipping list: `errcheck`, `govet`, `staticcheck`, `revive`, `gosec`, `gocritic`, `unparam`, `misspell`, `nilerr`, `wastedassign`
   - Add a `lint` job to `.github/workflows/main.yml` that runs `golangci-lint-action@v6`
   - Fix what it surfaces (probably a handful of nits in `internal/poller/poller.go` and `internal/storage/events.go`)

5. **Trivy filesystem + image scan**
   - In `.github/workflows/main.yml` add two trivy steps:
     - `trivy fs --severity HIGH,CRITICAL --exit-code 1 .` (deps + Dockerfile)
     - `trivy image --severity HIGH,CRITICAL --exit-code 1 <built image>`
   - Fail CI on HIGH/CRITICAL only; allow MEDIUM/LOW
   - When a transitive dep CVE blocks, document the rationale or wait for the bump

6. **Cosign keyless image signing on tags**
   - In `release.yml`, after the build step, add `cosign sign --yes <image>` using OIDC
   - Workflow needs `id-token: write` permission (it already does)
   - Gate the step on `github.event_name != 'pull_request'` so forks don't fail

7. **`release.yml` workflow + first tag**
   - Triggers on `v*` tags
   - Builds + pushes `:vX.Y.Z` and `:latest`
   - Pushes the Helm chart as an OCI artifact: `helm push <chart>.tgz oci://ghcr.io/jasondillingham/charts`
   - Creates the GitHub Release with auto-generated notes from commits since last tag
   - Optionally: cosign sign + SBOM via `goreleaser` (if we go that direction)
   - Tag `v0.1.0` once the workflow exists and is verified on a dry-run branch

### Tier 3 — documentation

8. **README screenshots from the running instance**
   - One overview screenshot of the index page (all 4 threads, last-event column, days-quiet column)
   - One screenshot of a thread detail page (timeline with event types)
   - One short architecture diagram (Markdown via Mermaid is fine): config -> poller -> github -> diff -> storage -> http
   - Drop into `docs/screenshots/` and reference from README

9. **`charts/threadwatch/README.md`** — chart values reference
   - The standard chart-README pattern: table of every value, default, description
   - Generate via `helm-docs` (`helm.sh/helm-docs`) or write by hand for now (~100 lines)

10. **`CHANGELOG.md`** at repo root
    - "Keep a Changelog" format
    - Backfill: 0.1.0-alpha.1 covers Checkpoints A-C
    - Going forward, each PR adds a line

11. **`CONTRIBUTING.md`** at repo root
    - DCO sign-off requirement
    - Local dev: `make run`, `make test`, `make docker`
    - PR conventions (commit message format, what tests to add)
    - Pointer to the design decisions section in README

12. **Chart `icon` field** — Helm lint emits an `[INFO] Chart.yaml: icon is recommended`
    - Pick an SVG or PNG; commit to `charts/threadwatch/icon.png` or use a hosted URL
    - One-liner in `Chart.yaml`: `icon: https://raw.githubusercontent.com/jasondillingham/threadwatch/main/charts/threadwatch/icon.png`

### Tier 4 — defer or optional

These are explicitly NOT V1 blockers. They appear here so a future session
doesn't relitigate the decision.

- **OpenAPI spec** at `api/openapi.yaml` + Redoc at `/docs` — planner suggested this; deferred since the API surface is tiny and a hand-written spec goes stale immediately
- **Hand-written `web/static/tailwind.css` → generated by standalone Tailwind CLI** — planner's right call but the manual CSS works fine and a swap is mechanical when ready; non-blocking
- **storage / httpserver unit tests** — the storage layer is exercised end-to-end on every poll; explicit unit tests are easy adds but the diff layer was the high-risk place
- **Prometheus ServiceMonitor template + Grafana dashboard JSON** — only useful when prom-operator is installed in the cluster; `.Values.metrics.serviceMonitor.enabled` already templated for when it's needed
- **htmx-driven refresh on the index page** — the page works fine on a hard reload for now
- **JSON API endpoints (`/api/threads`, `/api/threads/{id}`, `/api/threads/{id}/events`)** — planner suggested adding these; deferred since no consumer needs them yet
- **`POST /api/threads/refresh` is registered** but `REFRESH_TOKEN` is unset by default so the endpoint 404s. When you want force-refresh, set the env var via Helm values

## "Where things live" reference for the next session

```
~/Documents/Code/threadwatch/                  ← Go source, chart, CI
├── cmd/threadwatch/main.go                    ← wiring
├── internal/
│   ├── config/                                ← env + threads.yaml
│   ├── github/                                ← HTTP client + fetcher
│   ├── obs/                                   ← slog + Prometheus
│   ├── poller/                                ← Diff (the correctness core)
│   ├── storage/                               ← SQLite + migrations
│   └── httpserver/                            ← HTTP routes + templates
├── web/
│   ├── templates/{base,index,thread}.html
│   └── static/tailwind.css                    ← hand-written for now
├── charts/threadwatch/                        ← the Helm chart (the resume artifact)
├── .github/workflows/main.yml                 ← build, test, helm lint, multi-arch push
├── Dockerfile                                 ← distroless static, nonroot
└── docs/CHECKPOINT_D.md                       ← this file

~/Documents/Homelab/k8s/threadwatch/           ← MISSING; create in Tier 1 item 2
├── README.md
├── values.yaml                                ← local overrides
└── (Makefile or deploy.sh)

ghcr.io/jasondillingham/threadwatch            ← container image
ghcr.io/jasondillingham/charts/threadwatch     ← OCI Helm chart (after Tier 2 item 7)

k3s cluster, namespace `threadwatch`           ← live deployment
```

## Resume-day commands

When restarting work on threadwatch:

```bash
cd ~/Documents/Code/threadwatch
git status                                     # confirm clean working tree
git log --oneline -5                           # last commits + checkpoint markers
helm -n threadwatch list                       # confirm the running release
kubectl -n threadwatch logs deploy/threadwatch | tail -20  # recent activity
gh run list --repo jasondillingham/threadwatch --limit 3   # CI history

cat docs/CHECKPOINT_D.md                       # this file
```
