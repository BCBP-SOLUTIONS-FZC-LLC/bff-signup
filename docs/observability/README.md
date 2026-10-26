# Platform Observability Standard — bff-signup

How this service implements the platform observability standard. This page covers what is specific to bff-signup.

| Piece | Location |
|---|---|
| Metric inventory | [`metric-registry.md`](metric-registry.md) |
| Identity (`domain`, `service`, `environment`) | `cmd/server/identity.go` → `resolveAppEnv()` |
| Alerts | `deploy/monitoring/app-alerts.yml`, `deploy/helm/templates/prometheusrule.yaml` |
| SLO recording rules | `deploy/monitoring/slo-rules.yml` |
| HPA custom metrics | `deploy/monitoring/prometheus-adapter-rule.yaml` |
| Dashboard | `deploy/monitoring/dashboards/signup-health.json` |
| Alert runbooks | [`runbooks.md`](runbooks.md) |

## Identity

| Label | Value | Source |
|---|---|---|
| `domain` | `bff` | `APP_ENV`-derived |
| `service` | `bff-signup` | `APP_NAME` env var |
| `environment` | `local`, `dev`, `test`, `staging` or `prod` | `resolveAppEnv()`: `APP_ENV`, else `ENVIRONMENT`, else `dev` |

## Metric tiers

| Tier | Namespace | Count |
|---|---|---|
| Tier 3 (service-specific) | `signup_bff_` | All custom metrics |

All custom metrics use the `signup_bff_` prefix (enforced by `.github/scripts/check-metric-naming.sh`).

## Ports

| Port | Purpose |
|---|---|
| `8080` | Public API (signup form endpoints) |
| `9090` | Prometheus metrics scrape (`/metrics`), monitoring namespace only |

## Probes

| Probe | Path | What it checks |
|---|---|---|
| Liveness | `/healthz` | Service is alive |
| Readiness | `/readyz` | Valkey reachable (fail-closed — all endpoints return 503 if Valkey is down) |
