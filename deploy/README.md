# Atlas Kubernetes Deployment

Manifests for the `atlas-api` application tier, matching the required set from
[docs/deployment.md](../docs/deployment.md#kubernetes-deployment).

## Scope

These manifests cover **atlas-api only**. `docs/deployment.md`'s "Kubernetes Architecture"
section recommends StatefulSets for TimescaleDB and Redis, but the required manifest list
doesn't include one, and hand-rolling stateful database manifests is its own project. In
practice, production TimescaleDB/Redis are expected to run via a managed cloud service (e.g.
a managed Postgres/TimescaleDB offering, managed Redis) or an existing, maintained Helm chart
(e.g. the Bitnami charts) — not something this repo should reinvent. Point `DATABASE_HOST`
and `REDIS_HOST` in `configmap.yaml` at wherever those end up living.

## Files

| File | Purpose |
|---|---|
| `namespace.yaml` | Creates the `atlas` namespace |
| `configmap.yaml` | Non-secret environment configuration (see `internal/config/config.go` for the full env var list) |
| `secret.yaml` | **Template only** — see the warning in the file. Do not commit real secrets. |
| `deployment.yaml` | `atlas-api` Deployment: 2 replicas, resource limits, `/health` liveness+readiness probes, non-root security context |
| `service.yaml` | ClusterIP service exposing port 8080 |
| `ingress.yaml` | External routing — replace `atlas.example.com` and the cert-manager/ingress-class annotations with real values |
| `hpa.yaml` | Horizontal autoscaling on CPU/memory (2–6 replicas) |
| `pdb.yaml` | Keeps at least 1 pod available during voluntary disruptions (node drains, upgrades) |

## Apply

```bash
kubectl apply -f deploy/namespace.yaml
kubectl create secret generic atlas-api-secrets -n atlas \
  --from-literal=DATABASE_PASSWORD='...' \
  --from-literal=DISCORD_WEBHOOK_URL='...'
kubectl apply -f deploy/ -n atlas
```

(The explicit `kubectl create secret` step is deliberate — `secret.yaml`'s placeholder values
should never actually be applied as-is.)

## Not yet done

- No CI push/deploy stage wires an image into `deployment.yaml`'s `REGISTRY/atlas-api:latest`
  placeholder — `.github/workflows/ci.yml` stops at build validation. Pushing to a registry and
  deploying needs real registry/cluster credentials this repo doesn't have configured.
- No TimescaleDB/Redis manifests, per the Scope note above.
- No Kustomize/Helm layering for per-environment (dev/test/prod) differences — these are flat
  manifests, matching what `docs/deployment.md` literally asks for.

See [RUNBOOK.md](RUNBOOK.md) for backup and disaster recovery procedures.
