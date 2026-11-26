# Platform Observability Standard — bff-signup

How this service implements the Enterprise Platform Observability Standard. The standard itself, the
registry and its linter live in platform-gincommon; this page covers what is specific to bff-signup.

| Piece | Location |
|---|---|
| Metric inventory (generated) | [`metric-registry.md`](metric-registry.md) — `make metrics-inventory` |
| Instrumentation | `internal/adapter/outbound/metrics/business.go` |
| Local registry submission (Tier 1 Proposed) | `internal/adapter/outbound/metrics/registry.go` |
| Identity (`domain`, `service`, `environment`) | `cmd/server/identity.go`, injected by platform-gincommon |
| Alerts | `deploy/monitoring/app-alerts.yml` |
| SLO recording rules + burn-rate alerts | `deploy/monitoring/slo-rules.yml` |
| Deployed alerts + SLO rules | `deploy/helm/templates/prometheusrule.yaml` (generated copy of the two files above) |
| Custom metrics for HPA | `deploy/monitoring/prometheus-adapter-rule.yaml` (HPA is CPU-only today) |
| Dashboard | `deploy/monitoring/dashboards/signup-health.json` |
| Alert runbooks | [`runbooks.md`](runbooks.md) |

## Identity

| Label | Value | Source |
|---|---|---|
| `domain` | `bff` | `gincommon.Config{Domain: "bff"}` in `cmd/server/main.go`; `bff` is in the registry's domain vocabulary |
| `service` | `bff-signup` | `gincommon.Config.ServiceName` |
| `environment` | `local`, `dev`, `test`, `staging` or `prod` | `resolveAppEnv()`: `APP_ENV`, else `ENVIRONMENT`, else `dev` |

Instrumentation code never sets these: platform-gincommon injects them as const labels on every
collector it builds (standard rule 8). No collector carries a `version` label; the build version
is only on `platform_build_info`.

## Metric tiers

| Tier | Namespace | In this service | How it is built |
|---|---|---|---|
| 1 — platform-shared | `platform_*` | HTTP server metrics and `platform_build_info` (emitted by platform-gincommon); `platform_dependency_request_seconds` (recorded here) | From the central registry: `gincommon.NewSharedHistogramVec`. Names, labels and buckets are the registry's. |
| 2 — domain-shared | `bff_*` | None. bff-signup is the only BFF service, so nothing is shared within the domain. | — |
| 3 — service-specific | `bff_signup_*` | 9 metrics: funnel, provisioning, abuse controls, dependency latency, PBKDF2 time | `gincommon.NewCounterVec` / `NewHistogramVec`, which enforce the `bff_signup_` prefix, `_total`/`_seconds` suffixes and prohibited labels. |

### Proposed Tier 1 metrics and how they may be used

`platform_dependency_request_seconds` and the `platform_http_*` metrics are **Proposed** in the
Platform Observability Registry (awaiting governance ratification).

- `platform_dependency_request_seconds` is shadow-emitted only. Alerts, SLOs, recording rules and
  HPA references use its Tier 3 predecessor `bff_signup_dependency_call_duration_seconds`
  (same buckets: the registry's `dependency_request` preset). `metrics-registry-lint.sh` fails if
  an `expr` uses the Proposed name.
- `platform_http_requests_total` is the only HTTP request counter (there is no Tier 3 one), so
  SLO-1 uses it, like iam-org-membership does. Every alert built on it carries
  `metric_status: proposed`; CI fails an alert that uses a Proposed metric without that label.

### Dependency instrumentation

Every outbound HTTP adapter calls `metrics.ObserveDependencyCall(dependency, operation, …)`
immediately after `httpClient.Do()`, recording both `bff_signup_dependency_call_duration_seconds`
and `platform_dependency_request_seconds` (`check-metric-naming.sh` fails if an adapter omits it):

| Adapter | `dependency` | `operation` |
|---|---|---|
| `orgmembership/client.go` | `org_membership` | `provision_tenant`, `trial_eligibility`, `slug_resolve` |
| `realmprovisioner/client.go` | `realm_provisioner` | `provision_trial`, `provision_direct` |
| `turnstile/client.go` | `turnstile` | `siteverify` |
| `catalog/client.go` | `catalog` | `plans` |
| `billing/client.go` | `billing` | `authorize_and_charge` |

`outcome` is `success` or `error` (transport failure or 5xx). The operation values are listed in
`registry.go` for the ratification review; `TestStandard_RegistryOperationsMatchAdapters` keeps
them in step.

### Migration of `platform_dependency_request_seconds` (standard §Backward Compatibility)

1. **Now:** both emitted in parallel; `bff_signup_dependency_call_duration_seconds` is authoritative.
2. **On ratification:** flip the entry in `registry.go` to `StatusCanonical`.
3. **Migrate** dashboards, alerts, recording rules, SLOs and any HPA reference to the `platform_*` name.
4. **Deprecate** `bff_signup_dependency_call_duration_seconds` (help text + CHANGELOG).
5. **Remove** it after the approved sunset period.

## Logs, metrics and traces go through platform-gincommon only

| Signal | Path | Enforced by `make log-sink-lint` |
|---|---|---|
| Logs | gincommon logger (`log.Info/Warn/Error/Debug`); access logs and panic recovery from `ObservabilityMiddlewares` | LOG-1…4: no `log`/`slog`/zap/zerolog/logrus, no `fmt.Print*` (except the pre-logger message in `main()`), no `gin.Default/Logger/Recovery` |
| Metrics | gincommon constructors and type aliases, `gincommon.MetricsHandler` for `/metrics` | METRICS-1/2: no `prometheus.New*`, no `github.com/prometheus/*` import |
| Traces | server span from `ObservabilityMiddlewares`; all other spans and propagation via `internal/adapter/outbound/tracing` on `gincommon.NewTracer` / `InjectTraceContext` | TRACES-1…4: no tracer-provider setup outside `main.go`, no OpenTelemetry import outside the tracing package, every `http.Client` uses `tracing.Transport`, every Valkey client has the tracing hook |

Spans for outbound calls: `"<METHOD> <dependency>"` for HTTP (with `traceparent`, except to Turnstile) and `valkey <command>` for Valkey. Attributes are IDs, states and protocol fields only.

## CI enforcement (`make metrics-lint`)

| Check | Script | What it enforces |
|---|---|---|
| Tier 3 naming | `check-metric-naming.sh` | `bff_signup_` prefix, `_total`/`_seconds` suffixes, label vocabulary, no prohibited labels, every adapter instrumented |
| Local registry | `metrics-registry-lint.sh` | `registry.go` Proposed metrics never appear in an alert, recording rule, SLO or adapter rule `expr` |
| Standard linter | `metricslint.sh` → platform-gincommon `metricslint check` | A real `/metrics` scrape: namespace classification, naming, required labels (`domain`, `service`, `environment`), prohibited labels, shared-metric registry compliance, label vocabulary |
| Rule references | `metricslint refs` + `metric-refs-exist.py` | No legacy/removed names in `deploy/` or `docs/`; every metric an alert, recording rule, adapter rule or dashboard queries is actually emitted (by this service, a linked platform library, or an allow-listed external source) |
| Rule files | `monitoring-rules-sync.py` | The Helm `PrometheusRule` matches `app-alerts.yml` + `slo-rules.yml`; Proposed-metric alerts are labelled; every alert links to a runbook heading |
| Inventory | `metricslint.sh` + `metrics-inventory.py` | `metric-registry.md` matches the registry and the scrape |

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
