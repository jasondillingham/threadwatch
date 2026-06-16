# threadwatch Helm chart

Deploys [threadwatch](https://github.com/jasondillingham/threadwatch) — a
self-hosted monitor that polls GitHub issues/PRs and surfaces new activity in a
small web UI plus Prometheus metrics.

## Install

```bash
helm install threadwatch \
  oci://ghcr.io/jasondillingham/charts/threadwatch \
  --version 0.1.0 \
  -f my-values.yaml
```

Or from a checkout of the repo:

```bash
helm install threadwatch ./charts/threadwatch -f my-values.yaml
```

## Topology notes

- **Single replica, `Recreate` strategy.** The SQLite database lives on a
  ReadWriteOnce PVC, so the chart runs one pod and terminates the old one
  before starting the new one (`replicaCount: 1`). Setting `replicaCount > 1`
  is unsupported — multiple writers to one SQLite file will corrupt it.
- **Ingress off by default.** The reference deployment is fronted by an
  external reverse proxy (NPM), so `ingress.enabled: false`. Flip it on for a
  Traefik + cert-manager setup.
- **GitHub token is optional.** Without one the poller works but is capped at
  GitHub's 60 req/hr unauthenticated quota; supply `github.existingSecret` to
  lift it to 5000/hr.

## Values

| Key | Default | Description |
|---|---|---|
| `image.repository` | `ghcr.io/jasondillingham/threadwatch` | Container image. |
| `image.tag` | `""` | Image tag. **Empty → uses `.Chart.AppVersion`** (the published image tag matches the chart's appVersion). |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `replicaCount` | `1` | Pod count. Keep at 1 — SQLite is single-writer. |
| `resources.requests.cpu` | `25m` | CPU request. |
| `resources.requests.memory` | `32Mi` | Memory request. |
| `resources.limits.cpu` | `200m` | CPU limit. |
| `resources.limits.memory` | `128Mi` | Memory limit. |
| `service.type` | `ClusterIP` | Service type. |
| `service.port` | `80` | Service port. |
| `service.targetPort` | `8080` | Container port the service targets. |
| `ingress.enabled` | `false` | Create an Ingress. |
| `ingress.className` | `""` | IngressClass name. |
| `ingress.host` | `threadwatch.local` | Ingress hostname. |
| `ingress.annotations` | `{}` | Ingress annotations. |
| `ingress.tls.enabled` | `false` | Enable TLS on the Ingress. |
| `ingress.tls.secretName` | `""` | TLS secret name (when `ingress.tls.enabled`). |
| `persistence.enabled` | `true` | Create a PVC for the SQLite DB at `/data`. |
| `persistence.storageClass` | `""` | StorageClass (empty → cluster default). |
| `persistence.size` | `1Gi` | PVC size. |
| `persistence.accessModes` | `[ReadWriteOnce]` | PVC access modes. |
| `persistence.existingClaim` | `""` | Use an existing PVC instead of creating one. |
| `probes.enabled` | `true` | Enable liveness/readiness probes. |
| `probes.liveness.path` | `/healthz` | Liveness probe path. |
| `probes.liveness.initialDelaySeconds` | `5` | Liveness initial delay. |
| `probes.liveness.periodSeconds` | `30` | Liveness period. |
| `probes.readiness.path` | `/readyz` | Readiness probe path. |
| `probes.readiness.initialDelaySeconds` | `5` | Readiness initial delay. |
| `probes.readiness.periodSeconds` | `10` | Readiness period. |
| `containerSecurityContext` | nonroot, RO rootfs, drop ALL caps | Hardened container context (runAsUser/Group `65532`, `readOnlyRootFilesystem: true`, `allowPrivilegeEscalation: false`). |
| `podSecurityContext.fsGroup` | `65532` | Pod fsGroup (lets the nonroot user write the PVC). |
| `nodeSelector` | `{}` | Pod nodeSelector. |
| `tolerations` | `[]` | Pod tolerations. |
| `affinity` | `{}` | Pod affinity. |
| `log.level` | `info` | Log level (`debug`/`info`/`warn`/`error`). |
| `log.format` | `json` | Log format (`json`/`text`). |
| `github.existingSecret` | `""` | Secret holding the GitHub token; sets `GITHUB_TOKEN`. |
| `github.tokenKey` | `token` | Key within `github.existingSecret`. |
| `config.pollInterval` | `15m` | How often each thread is polled. |
| `config.threads` | 4 example threads | List of watched threads; each is `{label, owner, repo, number}`. |

### `config.threads` example

```yaml
config:
  pollInterval: 15m
  threads:
    - label: "cert-manager: DNS01 throttling"
      owner: cert-manager
      repo: cert-manager
      number: 8776
```

Each entry is rendered into a ConfigMap and mounted at
`/etc/threadwatch/threads.yaml`. Polling is per-thread.

### GitHub token

```bash
kubectl create secret generic threadwatch-github --from-literal=token=<PAT>
helm upgrade threadwatch ... --set github.existingSecret=threadwatch-github
```

A fine-grained PAT with **public-repo read** is enough — the token only raises
the rate limit, it doesn't need access to anything private.
