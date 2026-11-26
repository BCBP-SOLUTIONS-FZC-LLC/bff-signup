# Changelog

All notable changes to `bff-signup` are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

### Changed — SB-5 aligned to Billing B-I1 (BL-OQ-8, 2026-11-26)

Billing published the final B-I1 contract (Billing LLD rev 0.51 §5.4.2). LLD §19 rewritten.

- Billing adapter: body fields are now `plan_code`, `licensed_seats`, `payment_method_ref`; `x-user-id` / `x-tenant-roles` dropped (caller identity is the mesh SPIFFE ID). Handles `402 PAYMENT_ACTION_REQUIRED` (3-D Secure), `402 PAYMENT_DECLINED` with `decline_reason`, `410 CHARGE_EXPIRED`, `409 REQUEST_IN_PROGRESS` and `Retry-After`, `422 PRICE_NOT_AVAILABLE`, `403`, and `500`.
- **Fixed:** a retry after a Billing timeout or `5xx` used to allocate a new `tenant_id`, so a new `charge:<tenant_id>` key, and could start a second charge. SB-5 now resumes the session's paid handoff and repeats the same key and body. The handoff is kept on unknown outcomes and after payment (including RP-2 `email_unavailable`, which used to orphan a paid subscription).
- New handoff state `payment_action_required`; SB-5 returns `202 {status: payment_action_required, action, expires_at}` and the browser re-POSTs after 3-D Secure.
- New errors: `PAYMENT_DECLINED` → `422`, `CHARGE_EXPIRED` → `410`. `503` now carries `Retry-After` when the dependency gives one.
- Config: `BILLING_TIMEOUT_MS` default 15000, enforced ≥ 15000 when paid signup is on; new `PAID_REQUEST_DEADLINE_MS` (default 30000) for SB-5 only, and the server `WriteTimeout` follows it. Helm `BILLING_BASE_URL` set to the Billing cluster URL; `PAID_SIGNUP_ENABLED` stays `false`.

### Fixed — production-readiness review (2026-11-27)

Paid signup (could charge twice, or leave a customer paid without an account):
- Trial (SB-2) and paid (SB-5) submits on one session no longer overwrite each other's handoff (`409 INVALID_STATE`, `reason: other_signup_in_progress`); trial handoffs now record `type: trial`.
- I-1 no longer sends `email_hash` on the paid path: O&M claimed the one-trial ledger and answered `409 TRIAL_ALREADY_USED` to anyone who once had a trial, after they had paid. I-1 `TRIAL_ALREADY_USED` / `TENANT_OFFBOARDED` are now mapped.
- Billing errors delete the handoff only on `PAYMENT_DECLINED`, `CHARGE_EXPIRED` or `PRICE_NOT_AVAILABLE`; unexpected replies keep it, so a retry reuses `charge:<tenant_id>`.
- RP-2: own timeout `RP2_TIMEOUT_MS` (8000; RP-2 p95 is 4 s, the old 900 ms nearly always timed out); `rp2_attempted` freezes slug/name/email so the RP-2 key is never reused with another body; `REALM_ALREADY_DEDICATED` → slug conflict with suggestions; `OPERATION_IN_PROGRESS` → retryable 503; `email_verification_sent` is read.
- The handoff lock lives for the longest request deadline + 5 s (was 15 s, shorter than a paid request).
- Seats limited to 1–1000 (Billing's limit); a resume that cannot match is refused before PBKDF2; SB-3 returns `action: complete_payment` / `resubmit` for payment states.

Trial and platform:
- `RP_TIMEOUT_MS` 900 → 2000 (RP-1 p95 is 1.5 s) and `REQUEST_DEADLINE_MS` 2500 → 5000; `Validate()` checks both deadlines cover their call chains. This also removed the e2e flakiness (requests exceeded 2.5 s under load).
- Client IP from `x-envoy-external-address` must parse as an IP; the terms User-Agent is sanitised (never empty, no control characters); Turnstile transport errors are no longer in the 503 detail and Cloudflare `internal-error` is a 503; CR-4 400/401/403 are contract errors, not retried 503s; HSTS header.
- Catalog adapter uses CAT-I2 (`GET /api/v1/internal/plans`, `iam-system` headers, `display_name` / `trial_duration_days`).

Chart:
- Added the Turnstile `ServiceEntry` template — prod enabled it but no template existed, so every CAPTCHA call would fail under `REGISTRY_ONLY`.
- Istio AuthorizationPolicy enabled in staging and prod.
- HTTPRoute: request timeouts (35 s for `/paid`, 10 s otherwise; Envoy's default 15 s cut off SB-5), hostname from `env.PUBLIC_HOST` (`.Values.publicHost` did not exist and rendered `null`), `/healthz` and `/readyz` no longer routed publicly.
- NetworkPolicy egress names its destinations (kube-system DNS, Valkey CIDRs, `iam`/`billing` namespaces, OTel, istiod 15012 — the sidecar could not reach istiod before — and public-only 443).
- No service-account token mounted; optional `image.digest`.
- `helm-check.sh` validates CRDs against the CRD schema catalog (it failed on PrometheusRule/ServiceMonitor before) and renders prod with the HTTPRoute.

### Changed — logs, metrics and traces go through platform-gincommon only (2026-11-27)

- **Fixed: no distributed tracing past the BFF.** The five outbound HTTP clients were bare `http.Client`s: no client span and no `traceparent`, although LLD §11.3 said traces reached RP, O&M and Keycloak. Valkey had no spans either. New `internal/adapter/outbound/tracing` (built on `gincommon.NewTracer` / `RecordSpanError` / `InjectTraceContext`): `tracing.Transport(dependency, propagate)` on every client (Turnstile without propagation), a go-redis hook for Valkey commands and pipelines (no keys recorded), and `tracing.Annotate` for request-span attributes. SB-5 now annotates its span like SB-2.
- Services no longer import OpenTelemetry; the metrics package no longer imports Prometheus (gincommon's `CounterVec` / `HistogramVec` aliases; the unused `mustShared` reuse path is gone). `prometheus/client_golang` is now an indirect dependency.
- `check-log-sink.sh` gains METRICS-2 (no `github.com/prometheus/*` import), TRACES-2 (OpenTelemetry only in the tracing package), TRACES-3 (every `http.Client` uses `tracing.Transport`; no `http.DefaultClient`/`http.Get`), TRACES-4 (every Valkey client has the tracing hook) and LOG-4 (no `gin.Default/Logger/Recovery`). `.go-arch-lint.yml` adds the tracing package to the `observability` component.

### Changed — Enterprise Platform Observability Standard: monitoring fixed, CI enforcement completed (2026-11-27)

- **Fixed: the deployed alerts never fired.** The Helm `PrometheusRule` still queried `signup_bff_*` names the service stopped emitting, and `SignupRealmProvisionerUnavailable` filtered on `stage="realm_provisioner"`, which is never recorded. The Helm rule is now generated from `deploy/monitoring/app-alerts.yml` + `slo-rules.yml` (it also deploys the SLO rules now), and the RP alert uses the RP dependency error ratio.
- **SLO-1** measures 5xx on `POST /trial` and `/paid` from `platform_http_requests_total` (platform-gincommon) instead of dependency errors as a proxy; its burn-rate alerts carry `metric_status: proposed`, as in iam-org-membership. New `SignupProvisioningSLOBreach` (SLO-2) and `SignupBillingUnavailable` alerts. Every alert links to a runbook heading.
- Prometheus Adapter rule repointed from the non-existent `signup_bff_request_duration_seconds_count` to `platform_http_requests_total` (HPA stays CPU-only, G-9).
- Dashboard `signup-health.json` had no panels; it now has 11 (funnel, SLOs, failures, dependency latency/errors incl. Billing, CAPTCHA, rate limits, resumes, PBKDF2, submit latency).
- `bff_signup_dependency_call_duration_seconds` buckets now match the registry's `dependency_request` preset (to 30 s); Billing B-I1 (p95 6 s) previously landed in `+Inf`. Help texts cover SB-5; `registry.go` lists `authorize_and_charge`.
- CI (`make metrics-lint`, `validate-quality.yml`): platform-gincommon's `metricslint check` on a real scrape and `metricslint refs`; new checks that every queried metric is emitted, that the Helm and flat rules match, that Proposed-metric alerts are labelled, and that alerts link to runbooks; `docs/observability/metric-registry.md` is generated (`make metrics-inventory`) and drift-checked. `metrics-registry-lint.sh` now also covers the Helm and adapter rules.

### Changed — RFC 9457 adoption finished, aligned with the IAM services (2026-11-27)

- Removed the Swagger-only `ErrorResponse` and `SlugTakenResponse` DTOs. Every `@Failure` annotation references `ProblemResponse = problem.Problem`, platform-gincommon's own type (as iam-org-membership does); `docs/swagger/` regenerated. `/readyz`'s 503 is documented as the status map it returns.
- `HandleError` leaves request-context endings to gincommon, as iam-org-membership does: an expired gincommon `RequestTimeout` is answered by gincommon's `503 REQUEST_TIMEOUT` problem and counted on `platform_http_request_timeouts_total` (before, the BFF wrote its own 503); a client disconnect is recorded as `499`.
- No change to any status code or `errorCode` a client sees for business errors. `errors.go` remains the single domain → gincommon mapping, like the IAM services' `domainErrorToProblem`.

### Changed — dependencies updated to latest (2026-11-27)

- Direct: `platform-gincommon/v2` v2.2.0 → v2.7.0 (all additive: new `domain` values and metric-registry entries, go1.26.11 / `x/crypto` v0.59.0 security toolchain already in use here), `redis/go-redis/v9` v9.23.0 → v9.24.0; test-only `alicebob/miniredis/v2` v2.40.0 → v2.41.0, `testcontainers-go` v0.44.0 → v0.45.0.
- Indirect modules upgraded with them (`go get -u -t ./...`, also with the `integration` and `e2e` tags). Every module compiled into the service or its tests is now at its latest version; `govulncheck` reports no vulnerabilities.

### Fixed — paid and trial signup issues found in the docs audit (2026-11-27)

- **SB-5 was cut off at 2.5 s.** gincommon's outer `RequestTimeout` was `REQUEST_DEADLINE_MS`, so `PAID_REQUEST_DEADLINE_MS` had no effect. The outer timeout is now `cfg.MaxRequestDeadline()` (30 s with paid on); every other handler still sets its own `REQUEST_DEADLINE_MS` deadline.
- **SB-5 could charge twice** when a session past payment (`identity_created`, `tenant_pending`, `slug_conflict`, `completed`, `failed`) resubmitted with another email: a new `tenant_id` was allocated. It now returns `409 INVALID_STATE` (`reason: details_changed`), or the current state for the same email.
- **PUB-1 slug conflict was a dead end** on both paths: the handoff went to `slug_conflict` with no Keycloak user, SB-4 refused it and a resubmit returned the old conflict. The handoff now stays `allocated` (SB-2) or `payment_confirmed` (SB-5), so a resubmit with a new slug works, and on SB-5 reuses the paid subscription.
- SB-5 reads the `RATE_*` limits (`WithRateLimits`) instead of hard-coded 3/5, emits `form_submitted`/`rate_limited` and `mode="paid"` provisioning metrics, and re-issues the session cookie so a 3-D Secure re-POST keeps its session.

### Changed — docs aligned with the code (2026-11-27)

- LLD v1.2, README, ARCHITECTURE, `docs/architecture/` diagrams, `docs/observability/metric-registry.md` and `.claude/*.md` now describe SB-5 as implemented (not a "Phase 1.5 stub"), the RFC 9457 error shape with UPPER_SNAKE `errorCode`s, the 10-state handoff machine (one diagram in LLD §9.1, ARCHITECTURE and the `.mmd` source), outbound headers per call, the go-arch-lint rules as configured, the 97% coverage gate, Go 1.26.11, and the test layout. References to the non-existent open-questions and action-tracker files were removed.

### Added — Billing HTTP adapter for Phase 1 paid signup (SGN-2, 2026-11-14)

Commercial Domain HLD v0.43 retired Phase 1.5; paid signup is now required for Phase 1.

- `internal/adapter/outbound/billing/client.go` — real HTTP adapter for `AuthorizeAndCharge` (POST `/api/v1/internal/subscriptions/authorize-and-charge`). Handles 201/200 (success), 402 (payment declined → `ErrDependencyUnavailable`), 409 `SUBSCRIPTION_ALREADY_EXISTS` (idempotent replay), 400 (BFF contract error), 5xx (dependency unavailable).
- `cmd/server/wiring.go` — `buildBilling()` now wires the real adapter when `PAID_SIGNUP_ENABLED=true` and `BILLING_BASE_URL` are both set; stub remains active otherwise (the `/paid` route still returns 404 until enabled).
- `deploy/helm/values.yaml` — added `PAID_SIGNUP_ENABLED: "false"`, `BILLING_BASE_URL: ""`, `BILLING_TIMEOUT_MS: "5000"` with activation note.
- `.go-arch-lint.yml` — `billing` registered in the `adapters_outbound` component.
- Metrics — `billing` added as a tracked dependency in both `bff_signup_dependency_call_duration_seconds` (Tier 3) and `platform_dependency_request_seconds` (Tier 1 Proposed).
- 13 billing adapter unit tests covering all response paths; `TestStandard_Register_IsIdempotent` and `TestRateLimiter_DefensiveTTLFallback` integration test added.
- Coverage raised from 98.0% to 98.7%.

**Activation prerequisite:** SRE-3 (Core migration `000004`) must be deployed first. Billing LLD endpoint path and schema must be confirmed before setting `PAID_SIGNUP_ENABLED=true`.

### Changed — E2E suite runs in CI (2026-11-02)

Review item from Sharmila on PR #2: the E2E suite was not part of CI because it needed a running service, Valkey and mocks started by hand.

- `test/e2e/harness_test.go` starts everything in the test process: a Valkey testcontainer, the real router and services behind `httptest`, and Go versions of the O&M and RP-1 mocks (`mocks_test.go`), as in `iam-org-membership`.
- `make test-e2e` now needs only Docker. Setting `E2E_BASE_URL` still runs the suite against a service started by hand.
- New `Tests — e2e` step in `validate-test.yml` (job timeout 15 → 20 minutes). Captcha-mode, paid-mode, log-file and Valkey-down tests stay skipped in CI.
- New test dependency: `testcontainers-go`.

### Fixed — Test-gap fixes from manual API testing (2026-10-30)

Branch `test/gap-fix`. 15 gaps found in manual testing (185 of 194 cases run), each fixed in its own commit.

- **SB2-07** — unknown or duplicate JSON fields in the request body are now rejected (`strict_json.go`).
- **SB2-H10** — company name is normalised before validation (`NormaliseCompanyName` in `domain/signup.go`); a body of exactly 16 KiB is accepted, larger returns 413.
- **SB2-H11 / SB2-H12** — POST requests without `Content-Type: application/json` return 400 (`middleware/content_type.go`).
- **SB2-48** — I-1 contract errors fail fast instead of retrying (orchestrator `runI1`).
- **SB2-56 / I-03** — a concurrent double submit of the same signup returns 409 with `retry_after_ms` instead of racing (`handleResubmit` lock).
- **SB2-I01** — resume probe runs before CAPTCHA and rate limits, so a resume is not blocked by them.
- **SB3-14** — Valkey error on status lookup returns 503, not 404.
- **SB4-12** — slug change maps only dependency-unavailable to `tenant_pending`.
- **S-04** — gin trusted proxies disabled; forged `X-Forwarded-For` no longer bypasses per-IP rate limits (`engine.go`).
- **SB2-66** — structured logs for submit, slug change and paid submit carry request_id, trace_id, signup_id, tenant_id and state.
- **I-02** — session TTL and cookie Max-Age slide on SB-3 and SB-2.
- **S-05** — password bytes are zeroed after PBKDF2.
- **O-03** — spans carry `signup.id`, `tenant.id` and `handoff.state` (direct OpenTelemetry import, as instructed by the service owner).

### Fixed — Review finding on the RP-1 retry wait (2026-11-02)

- **RP-1 retry wait** (`handoff_orchestrator.go`, `provisionRP1`) — when the request context ended while waiting to retry a 409 `OPERATION_IN_PROGRESS`, the internal `ErrOperationInProgress` was returned as it was and the user got a 500. It now returns `ErrDependencyUnavailable` (503), like every other RP-1 failure. Two unit tests cover a cancelled context and a deadline that expires during the wait.

### Fixed — Spec gaps rectified after E2E testing (2026-10-31)

Code now follows the LLD (SB1-02, SB2-34, SB2-RP01, SB2-43, SB2-44, OBS-41, OBS-51, OBS-55):

- **`Retry-After` header on every 429** (`errors.go`), copied from `details.retry_after_seconds`; the body field stays.
- **Bounded retries before any user exists** (`handoff_orchestrator.go`, LLD §9.3 / §17.2): eligibility is retried once on 5xx or timeout; RP-1 is retried once on 502/503/timeout and once on 409 `OPERATION_IN_PROGRESS`, always with the same Idempotency-Key; the user then sees 503. New domain error `ErrOperationInProgress` (RP-1 client) keeps this separate from other 409s.

Spec text aligned to the intended behaviour (LLD v1.1; the code was already right) — **decisions for the reviewer**:

1. A resubmit with different details while `allocated` keeps the session's `signup_id` and issues a new `tenant_id` (SB2-51; §5.4 contradicted itself).
2. A slug conflict is not a `provisioning_failed_total` reason, so the failure-rate alert is not triggered by a user outcome; it is counted in `form_submitted_total` only (SB2-69).
3. After expiry SB-3 returns `401 session_expired` before any handoff lookup (SB3-13, OBS-33).
4. Turnstile is **not** retried (LLD §9.3 said once; §5.4 says tokens are single-use). A retry after a timeout could fail on a spent token and show a false `403 captcha_failed` instead of `503`. The code already made one call; the LLD row was corrected and an E2E test (SB2-42) checks exactly one call.

Known effect: during a real RP-1 or O&M outage, dependency-call metrics show about twice the failed calls because of the retry.

### Added

- End-to-end tests (`test/e2e`, build tag `e2e`, `make test-e2e`): 170 of the 194 test cases (the fixed gaps and most manually tested cases) (session, validation, handoff, resubmit, status, slug change, health, metrics, logs, downstream headers). See `test/e2e/README.md` for what stays manual.
- Swagger: SB-2 now documents 413 and the in-progress 409.

### Known / accepted

- `govulncheck` reports GO-2026-5932; not reachable (only `pbkdf2` is used). No dependency updates available.
- Open for the service owner: no preStop pause before shutdown, wide NetworkPolicy egress, `APP_ENV` defaults to `dev` if unset, Istio AuthorizationPolicy and ingress disabled by default in `values.yaml`.

### Changed — Production hardening: security, correctness, observability (2026-10-28)

All changes on branch `bff-signup-initial`. Zero new features — every change is hardening, correctness, or observability.

#### Security

- **CSRF constant-time comparison** (`session_service.go`) — `hashCSRF(token) != sess.CSRFHash` replaced with `subtle.ConstantTimeCompare`; eliminates timing side-channel on CSRF validation.
- **Email HMAC constant-time comparison** (`signup_service.go`) — all `existing.AdminEmailHMAC == emailHMAC` comparisons replaced with `hmacEqual()` using `subtle.ConstantTimeCompare`.
- **Security response headers** (`router.go`) — `noStoreHeaders()` now sets `X-Frame-Options: DENY` (clickjacking) and `Referrer-Policy: strict-origin-when-cross-origin` (prevents signup URL / CAPTCHA token leaking to third-party origins via Referer header). Existing `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` unchanged.
- **Slowloris prevention** (`main.go`) — `ReadHeaderTimeout: 5s` added to the API `http.Server`.
- **CAPTCHA_SKIP blocked outside dev/test/local** (`config.go`) — `Validate()` now rejects `CAPTCHA_SKIP=true` when `APP_ENV` is not `dev`, `test`, or `local`. Config bounds also added for all timeouts and `SESSION_TTL_SECONDS`.
- **CAPTCHA bypass logged at startup** (`main.go`) — `log.Warn("captcha.skip.active")` emitted when `noopCaptcha` is wired, so operators see it immediately in structured logs.
- **SameSite=Strict cookie** (`middleware/session.go`) — `c.SetSameSite(http.SameSiteStrictMode)` called before `c.SetCookie` instead of fragile manual header concatenation.
- **Rate limiter: fixed-window** (`valkey/rate_limiter.go`) — replaced `INCR + EXPIRE` pipeline (sliding window) with a Lua script that sets `EXPIRE` only on `count==1`. Fixes exploit where an attacker could reset the 1-hour window indefinitely by spacing requests before expiry. `resetAt` now uses actual remaining TTL from Valkey's `TTL` command.
- **Rate limiter type assertions checked** (`valkey/rate_limiter.go`) — silent `_, ok` assertions replaced with explicit `ok1`/`ok2` guards; returns error on type mismatch instead of silently resetting counter to 0.
- **handleResubmit race fix** (`signup_service.go`) — when an existing `StateAllocated` handoff has different details, the service now acquires the distributed lock before deleting, preventing deletion during an active RP-1 run.

#### Correctness

- **SB-4 now returns 409 with slug suggestions** (`status_service.go`, `handlers.go`) — `ChangeSlug` was silently swallowing `ErrSlugTaken` and returning `202 "provisioning"` when a replacement slug was also taken. Now returns `409` with suggestions, matching SB-2 behaviour (A-3). `StatusResult.SlugSuggestions` field added.
- **SuggestSlugs: 50-attempt iteration cap** (`domain/slug.go`) — random-suffix loop was unbounded; for a 30-char base slug every truncated candidate equals the base, so `appendUniq` rejects all duplicates and the loop never exits. Cap added.
- **DeriveSlug: non-ASCII company names** (`domain/slug.go`) — all-non-ASCII names (Arabic, CJK, Cyrillic) no longer produce `"-xxxx"` (invalid slug pattern). Returns a random alphanumeric slug instead.
- **Resume window uses `time.Duration`** (`handoff_orchestrator.go`) — replaced `float64` seconds comparison with `const resumeWindow = 15 * time.Minute`.
- **Session TTL from config** (`valkey/session_store.go`, `handlers.go`) — `SESSION_TTL_SECONDS` env var is now consumed. Previously the Valkey TTL was hardcoded at 30 min and the cookie max-age at 1800.
- **Rate limits from config** (`service/*.go`) — `RATE_SIGNUPS_PER_IP_PER_HOUR`, `RATE_ATTEMPTS_PER_EMAIL_PER_DAY`, `RATE_SESSIONS_PER_IP_PER_HOUR`, `RATE_POLLS_PER_SESSION_PER_MIN` were loaded by `Config` but never consumed — all four services used hardcoded constants. Now wired via constructor parameters.
- **Eligibility timeout wired** (`cmd/server/main.go`, `handoff_orchestrator.go`) — `ELIGIBILITY_TIMEOUT_MS` config field was dead. `HandoffOrchestrator` now accepts `eligibilityTimeout time.Duration` and wraps the eligibility call in `context.WithTimeout`.
- **I-1 response verified** (`handoff_orchestrator.go`) — `_ = resp` (discarded I-1 response) replaced with tenant-ID consistency check.
- **GetStatus/ChangeSlug: Valkey error → 503** (`status_service.go`) — `handoffs.Get()` error was mapped to `ErrSignupNotFound` (404); now correctly maps to `ErrDependencyUnavailable` (503).
- **handleResubmit hash errors propagated** (`signup_service.go`) — `seed, _ := s.hasher.Hash(...)` and `sj, _ := seedJSON(seed)` silently discarded errors on the resubmit path; now returns `ErrInternal`.
- **PaidSignupService: orchestrator injected** (`paid_signup_service.go`) — inline `&HandoffOrchestrator{...}` construction replaced with injected `*HandoffOrchestrator`.
- **Catalog singleflight** (`catalog/client.go`) — `singleflight.Group` added to prevent thundering-herd on cache expiry; concurrent cache-miss callers share one upstream fetch.
- **Body limit middleware** (`middleware/body_limit.go`) — 16 KB hard cap on all request bodies: Content-Length fast-path + `http.MaxBytesReader` slow-path for chunked bodies.
- **Helm: terminationGracePeriodSeconds** (`deploy/helm/templates/deployment.yaml`) — explicitly set to 35 s (5 s above Go shutdown drain) so the relationship is not coincidental with the K8s default.
- **Helm: topologySpreadConstraints** (`deploy/helm/templates/deployment.yaml`) — `maxSkew: 1` on `kubernetes.io/hostname` ensures the two replicas land on different nodes.
- **Helm: duplicate YAML keys removed** (`deploy/helm/values.yaml`) — duplicate `existingSecret` / `envFromSecret` top-level keys and duplicate `serviceEntry` key removed; YAML parsers silently ignored the first occurrence.
- **`values-prod.yaml` self-contained** — `SESSION_TTL_SECONDS`, `TERMS_CURRENT_VERSION`, `SLUG_RESERVED`, and all `RATE_*` overrides now explicit in the prod values file.
- **`.env-example` complete** — all 15+ configurable env vars now documented including `SESSION_TTL_SECONDS`, `CAPTCHA_SKIP` (with warning), all timeout vars, rate limit vars, `TERMS_CURRENT_VERSION`, and Phase 1.5 stubs.

#### Observability

- **Dependency metrics wired to all 4 adapters** — `ObserveDependencyCall` was registered in `business.go` but **never called** from any outbound adapter; `bff_signup_dependency_call_duration_seconds` was empty in production and the SLO recording rules computed over zero data. All four adapters now instrument immediately after `httpClient.Do()`:
  - `orgmembership`: `provision_tenant`, `trial_eligibility`, `slug_resolve`
  - `realmprovisioner`: `provision_trial`, `provision_direct`
  - `turnstile`: `siteverify`
  - `catalog`: `plans`
- **Startup logs structured** (`main.go`) — all startup failures after logger init now emit `log.Error()` before returning. New `log.Info()` at each successful init phase: config loaded (with key params), tracing init, metrics registered, Valkey ready.
- **Metric registry doc** (`docs/observability/metric-registry.md`) — rewritten to correctly document three tiers (Tier 1 Proposed, Tier 2 unused, Tier 3 service-specific), all 9 collectors, adapter instrumentation map, label vocabulary, and backward-compatibility migration plan.
- **Runbook metric name corrected** (`docs/observability/runbooks.md`) — `signup_bff_provisioning_failed_total` → `bff_signup_provisioning_failed_total`.
- **All logs/metrics/traces through platform-gincommon** — verified and enforced by CI: no standard library `"log"` package, no `fmt.Print*` outside `main()`, no raw `prometheus.New*` outside metrics package, no raw OTel API outside `main.go`.

#### CI / Quality gates

- **`check-log-sink.sh`** — new CI script enforcing LOG-1 (no `"log"` import), LOG-2 (no `fmt.Print*` outside `main()`), LOG-3 (no third-party loggers), METRICS-1 (no raw `prometheus.New*`), TRACES-1 (no raw OTel API).
- **`check-metric-naming.sh` enhanced** — added: high-cardinality label prohibition, Tier 2 boundary guard, adapter instrumentation enforcement (CI fails if any adapter omits `ObserveDependencyCall`).
- **`log-sink-lint` Makefile target** — `make log-sink-lint` runs `check-log-sink.sh`.
- **`metrics-lint` Makefile target fixed** — now runs `check-metric-naming.sh` + `metrics-registry-lint.sh` (was pointing to non-existent `scripts/validate-metrics.sh`).
- **`make ci` extended** — now includes `metrics-lint` and `log-sink-lint` in the full local CI gate.
- **Coverage gate corrected** — `coverage-gate.sh` comment and `README.md` both now say 95% (actual enforcement was always 95%; comments incorrectly said 80%).
- **`validate-quality.yml`** — new CI step: "Observability sink lint".

#### Tests added

- Config: bounds validation (9 sub-cases: RPTimeout, I1Timeout, EligibilityTimeout, RequestDeadline, SessionTTL), CAPTCHA_SKIP rejected in prod, CAPTCHA_SKIP allowed in dev/test/local.
- Service: handleResubmit lock-failure path (verifies handoff survives lock contention), SB-4 slug conflict returns 409 with suggestions, GetStatus/ChangeSlug return 503 on Valkey error.
- HTTP: body limit (Content-Length fast-path, exact 16 KB boundary, chunked slow-path), SameSite=Strict cookie, AcceptLanguage empty/unknown fallback, SB-4 409-with-suggestions handler test.
- Domain: DeriveSlug non-ASCII (Arabic/CJK/Cyrillic), SuggestSlugs 50-attempt cap, punycode email domain accepted, raw non-ASCII domain rejected.
- Valkey: fixed-window TTL not reset on subsequent increments (3 new rate limiter tests).
- Middleware: `SetSessionCookie` SameSite attribute, body limit all paths.

#### Dependency update

- `github.com/prometheus/common` v0.72.1 → **v0.73.0** (indirect; updated via `go get -u ./...` + `go mod tidy`).

---

### Added — Phase 1.5 Direct-Paid Signup reservation (2026-10-24)

**Phase 1.5 stub fully wired — activates with `PAID_SIGNUP_ENABLED=true`:**

- `internal/core/port/billing.go` — replaced `NotImplemented` stub with real `BillingClient` interface: `AuthorizeAndCharge(ChargeRequest) → *ChargeResponse{BillingSubscriptionID}` + `BillingStub` (returns `ErrNotImplemented`). `ChargeRequest` carries `{TenantID, PaymentToken, Plan, Seats, IdempotencyKey}`.
- `internal/core/port/realm_provisioner.go` — extended `RealmProvisioner` with `ProvisionDirect` (RP-2). Added `ProvisionDirectRequest` (mirrors RP-1 + `BillingSubscriptionID`) and `ProvisionDirectResponse`.
- `internal/adapter/outbound/realmprovisioner/client.go` — implemented `ProvisionDirect` HTTP call to `POST /api/v1/internal/tenants/provision-direct`, Idempotency-Key `signup:<tenant_id>:rp2`.
- `internal/core/domain/handoff.go` — added `StatePaymentPending`, `StatePaymentConfirmed`, `SignupType` (`"trial"` | `"paid"`), `Type` field, and `BillingSubscriptionID` field to `Handoff`.
- `internal/core/service/paid_signup_service.go` — new `PaidSignupService` (SB-5): CAPTCHA → rate limit → validate → Billing charge → RP-2 → I-1 inline (reuses `HandoffOrchestrator.runI1`).
- `internal/core/service/signup_service.go` — extracted `emailHMAC(email, key)` to a package-level function shared by both signup services.
- `internal/adapter/inbound/http/handlers.go` — added `SubmitPaid` handler; `Handlers` gains `paidSignup *service.PaidSignupService`.
- `internal/adapter/inbound/http/router.go` — `POST /api/v1/signup/paid` wired; returns `404` unless `PaidSignupEnabled=true`. Added `PaidSignupEnabled` to `RouterConfig`.
- `internal/adapter/inbound/http/dto.go` — added `PaidSignupRequest` (+ `seats int`, `payment_token string`) and `PaidSignupResponse`.
- `internal/config/config.go` — added `BillingURL`, `BillingTimeoutMs` (default 5000 ms), `PaidSignupEnabled` (env `PAID_SIGNUP_ENABLED`). Added `BillingTimeout()` and `envBool()`.
- `cmd/server/main.go` — wires `PaidSignupService`, passes `PaidSignupEnabled` to router.
- `cmd/server/wiring.go` — added `buildBilling()` returning `port.BillingStub{}`.
- `test/fixtures/fakes.go` — added `FakeBillingClient`, `FakeRealmProvisioner.ProvisionDirect` and `DirectCalls`.

**D-2 resolved (Commercial Domain HLD §8.3, CD-9 closed):** BFF (not Billing) calls RP-2 and I-1 after Billing confirms the charge. Flow: Billing → RP-2 → I-1.

---

### Added — Initial implementation (2026-10-22)

**Service foundation:**

- `go.mod` — module `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`, Go 1.26.10.
- `cmd/server/main.go` — composition root with fail-fast config validation, Valkey ping, DI, graceful shutdown (HTTP drain → Valkey close).
- `cmd/server/wiring.go` — factory functions for all upstream clients (Valkey stores, RP-1, O&M, Turnstile, PBKDF2 hasher, Catalog).
- `cmd/server/env_test.go` — startup tests: missing required env vars panic, `PBKDF2_ITERATIONS < 210000` rejected, `SBFF_HMAC_KEY < 32 bytes` rejected, plan fallback never contains `"trial"`.
- `internal/config/config.go` — fail-fast env config with `Validate()`. `PasswordPolicy()` and `SlugRules()` return domain types for SB-1 response.

**Domain layer (`internal/core/domain/`):**

- `errors.go` — sentinel errors (`ErrSessionExpired`, `ErrCSRFInvalid`, `ErrCaptchaFailed`, `ErrValidationFailed`, `ErrSignupNotFound`, `ErrEmailUnavailable`, `ErrTrialNotEligible`, `ErrSlugTaken`, `ErrInvalidState`, `ErrRateLimited`, `ErrDependencyUnavailable`, `ErrInternal`) + `DomainError{Cause, Message, Details}`.
- `handoff.go` — `Handoff` record (7-state machine: `allocated → identity_created → completed/tenant_pending/slug_conflict`). `IsTerminal()`, `CanResume()`, `CanChangeSlug()`.
- `plan.go` — `Plan` type (`starter`|`pro`|`enterprise`). `ValidPlan()` rejects any other value including `"trial"` (SB-INV-8).
- `signup.go` — field validators: `ValidateCompanyName`, `ValidateAdminName`, `ValidateAdminEmail`, `ValidatePassword` (authoritative policy check at signup — SB-INV-7), `NormaliseEmail`.
- `slug.go` — `DeriveSlug` (ASCII-fold → lowercase → hyphens), `ValidateSlug` (3–30 chars, pattern, reserved words), `SuggestSlugs` (up to 3 alternatives on conflict).

**Port interfaces (`internal/core/port/`):**

- `session_store.go`, `handoff_store.go`, `rate_limiter.go`, `captcha.go`, `catalog.go`, `realm_provisioner.go`, `org_membership.go`, `password_hasher.go`, `clock.go` (Clock + IDGenerator), `billing.go` (Phase 1.5 stub).

**Service layer (`internal/core/service/`):**

- `session_service.go` — SB-1: create session (256-bit random SID, CSRF token), CSRF validation, session retrieval.
- `signup_service.go` — SB-2: CAPTCHA first (SB-INV-5) → rate limits → validation → resubmit matrix (G-1) → PBKDF2 hash → distributed lock → handoff → orchestrate. HMAC-SHA256 email key (SB-INV-2).
- `handoff_orchestrator.go` — eligibility (CR-4) → RP-1 → inline I-1 (×2 retries, 100ms/250ms) → `tenant_pending` on exhaust. `ResumeI1` for SB-3 (15-min window). `ChangeSlug` for SB-4 (max 5 attempts).
- `status_service.go` — SB-3: poll with distributed lock + I-1 resume. SB-4: validate + `ChangeSlug`.

**Inbound HTTP adapter (`internal/adapter/inbound/http/`):**

- `router.go` — routes SB-1…4, `/healthz`, `/readyz`. `requireValidatedSession` middleware (session + CSRF for POST, session-only for GET). `noStoreHeaders()` on all responses.
- `handlers.go` — `Session`, `SubmitTrial`, `GetStatus`, `ChangeSlug`. `acceptLocale` derives `default_locale` from allow-listed `Accept-Language`.
- `dto.go`, `errors.go` — request/response DTOs, `domainStatus()` mapping.
- `middleware/session.go` — cookie extract + `SetSessionCookie` with `SameSite=Strict`.
- `middleware/csrf.go` — `X-CSRF-Token` extract + `ValidateOrigin`.
- `middleware/clientip.go` — reads `x-envoy-external-address` only (never `X-Forwarded-For`).

**Outbound adapters:**

- `valkey/session_store.go`, `handoff_store.go`, `rate_limiter.go`, `store.go` — Valkey state store. Distributed lock via `SET NX PX` + Lua release script. All key builders in `store.go`.
- `realmprovisioner/client.go` — RP-1 HTTP client. RFC 9457 `errorCode` branching: `EMAIL_ALREADY_REGISTERED`, `OPERATION_IN_PROGRESS`, `INVALID_REQUEST`.
- `orgmembership/client.go` — I-1 + trial-eligibility HTTP clients. RFC 9457 `errorCode` branching: `SLUG_ALREADY_TAKEN`, `TRIAL_ALREADY_USED`. No `Idempotency-Key` on I-1 (idempotent by `tenant_id` state).
- `turnstile/client.go` — Cloudflare siteverify + hostname check.
- `catalog/client.go` — 5-min in-process cache + mutex + static fallback.
- `hasher/pbkdf2.go` — PBKDF2-SHA512, 210,000 iterations, 16-byte random salt. Output is `HashedPasswordSeed` JSON; derived key zeroed after encoding.

**Shared package:**

- `pkg/signupctx/context.go` — `SignupContext{Session, ClientIP}`, `WithContext`, `FromContext`, `MustFromContext`.

**Infrastructure:**

- `deploy/helm/` — Helm chart with `values-{dev,staging,prod}.yaml`. CPU-only HPA (PBKDF2 is CPU-bound). NetworkPolicy ingress restricted to `envoy-gateway-system` and `monitoring`. PDB `minAvailable: 1`.
- `.github/workflows/ci.yml`, `validate-quality.yml`, `validate-test.yml` — parallel CI: quality gate (fmt/vet/lint/arch-lint/vuln-check/Dockerfile pin), test gate (unit + integration with real Valkey + coverage ≥ 80%), image build, Trivy scan, Helm lint.
- `.github/scripts/` — `arch-lint.sh`, `coverage-gate.sh` (80% threshold), `smoke-tests.sh` (50 MB image limit + startup gate).
- `.githooks/pre-commit` — tidy + fmt-check + vet + lint + arch-lint before every commit.
- `.go-arch-lint.yml` — layering rules.
- `.golangci.yml`, `.dockerignore`, `.trivyignore`, `.docker-digests`.
- `scripts/merge_coverage.py` — merge unit + integration coverage profiles.
- `scripts/validate-metrics.sh` — enforce `bff_signup_` prefix + `_total`/`_seconds` suffixes.
- `.claude/` — `CLAUDE.md`, `key-files.md`, `api-and-events.md`, `flows-and-concurrency.md`, `development-guide.md`, `operations.md`.

**Tests:**

- `test/fixtures/fakes.go` — hand-written fakes for all port interfaces.
- `test/unit/domain/` — `slug_test.go`, `handoff_test.go`, `password_test.go`.
- `test/unit/service/signup_service_test.go` — happy path, CAPTCHA order (SB-INV-5), plan invariant (SB-INV-8), slug conflict, eligibility blocks RP-1 (SB-INV-4).
- `test/integration/valkey_test.go` — session CRUD, handoff state update, lock contention, rate counter TTL.

**Documentation:**

- `docs/lld/signup-bff-lld.md` v0.6 — full LLD (21 sections).
- `docs/lld/signup-bff-open-questions-tl.md` — 4 TL decisions still needed (A-1…A-4).
- `docs/lld/signup-bff-action-tracker.md` — team assignments and closed items.
- `ARCHITECTURE.md`, `CHANGELOG.md`, `CONTRIBUTING.md`.

---

## [0.1.0] — not yet released

Initial pre-release. No environment has been deployed and no tag exists (VERSIONING.md). Open before production: SRE infra BFF-1/2/4 (LLD §16.2).
