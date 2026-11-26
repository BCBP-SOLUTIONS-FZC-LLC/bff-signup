# CLAUDE.md

This file provides guidance to Claude Code when working with **bff-signup**, the public signup BFF service (trial and direct-paid).

## What This Repo Is

`bff-signup` is the platform's **public, unauthenticated tenant-onboarding intake** (Go module: `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`, Go 1.26.11). It serves the signup form's JSON API, enforces abuse controls (rate limit, CAPTCHA, CSRF), and runs the provisioning handoff.

**Trial path (SB-2):**
1. Trial eligibility check → O&M `POST /api/v1/internal/trial-eligibility` (CR-4)
2. PUB-1 slug pre-check → O&M `GET /api/v1/public/tenants/resolve` (fail-open)
3. RP-1 → Realm Provisioner `POST /api/v1/internal/tenants/provision-trial` (creates Keycloak user in shared trial realm)
4. I-1 → Org & Membership `POST /api/v1/internal/tenants` (creates tenant row)

**Direct paid path (SB-5, real Billing B-I1 adapter; `/paid` returns 404 until `PAID_SIGNUP_ENABLED=true`):**
1. Billing B-I1 → `POST /api/v1/internal/subscriptions/authorize-and-charge` (key `charge:<tenant_id>`) → `billing_subscription_id`. `402 PAYMENT_ACTION_REQUIRED` → `202 payment_action_required`; the browser completes 3-D Secure and re-POSTs the same body.
2. PUB-1 slug pre-check (fail-open; a conflict keeps the handoff in `payment_confirmed`)
3. RP-2 → Realm Provisioner `POST /api/v1/internal/tenants/provision-direct` (creates dedicated realm + Keycloak user)
4. I-1 → Org & Membership `POST /api/v1/internal/tenants` (creates tenant row)

**Owns:** ephemeral Valkey state only (sessions, handoffs, rate-limit counters). No database. No event bus participation.

**Does NOT own:** Keycloak admin, JWT issuance, tenant data, memberships, billing.

**Namespace / identity:** `bff` namespace, service account `bff-signup`, SPIFFE `spiffe://<trust-domain>/ns/bff/sa/bff-signup` (BFF-1).

**Status:** code complete for Phase 1; not deployed to any environment; everything is under CHANGELOG `[Unreleased]`.

**Paid go-live (LLD §19.5):** after SRE-3, and Billing running `FEATURE_DIRECT_SIGNUP=true` with Pact and the Test-Clock E2E green in staging, set Helm `PAID_SIGNUP_ENABLED=true`. `BILLING_BASE_URL`, `BILLING_TIMEOUT_MS=15000` and `PAID_REQUEST_DEADLINE_MS=30000` are already in `values.yaml`.

**LLD:** `docs/lld/signup-bff-lld.md` v1.3 — canonical reference for all design decisions. §19 documents SB-5 against Billing B-I1, the 3-D Secure flow, retries and the go-live order.

---

## Common Commands

```bash
make setup           # Copy .env-example → .env + install pre-commit hook (run once)
make tidy            # go mod tidy
make fmt             # gofmt -w
make fmt-check       # Verify formatting (CI)
make vet             # go vet ./...
make lint            # golangci-lint (v2; CI pins v2.12.1 — use /ci-local if your local binary is v1)
make arch-lint       # go-arch-lint (layering rules)
make metrics-lint    # observability standard: naming, labels, registry (gincommon metricslint), rule refs exist, Helm/flat rules in sync, inventory
make metrics-inventory # regenerate docs/observability/metric-registry.md (generated; do not hand-edit)
make log-sink-lint   # enforce all logs/metrics/traces go through platform-gincommon
make mod-verify      # go mod verify
make vuln-check      # govulncheck

make test            # unit + integration tests
make test-ci         # -race + merged coverage.out (CI then runs coverage-gate.sh, ≥ 97%)
make test-unit       # unit tests only (no Docker); includes contract tests (test/unit/service/contract_test.go)
make test-integration # integration tests (requires Docker Valkey)
make test-e2e        # e2e in-process (Docker only); E2E_BASE_URL targets a running service
make test-load       # k6 load tests (script test/load/signup.js not yet written)
make race            # all tests with -race

make build           # compile → bin/bff-signup
make run             # go run ./cmd/server (sources .env)
make cover           # coverage HTML report
make cover-func      # coverage by function
make swag            # regenerate docs/swagger/ from @annotations
make swag-check      # fail if docs/swagger/ is stale

make docker-up       # start local Valkey (docker-compose)
make docker-down     # stop local Valkey
make docker-reset    # stop + wipe + restart

make helm-lint       # lint Helm chart
make helm-template   # render chart with dev values

make ci              # full local CI gate (tidy + fmt + vet + lint + arch-lint + metrics-lint + log-sink-lint + test-ci + build)
```

---

## Architecture Rules (enforced by go-arch-lint)

- `core/domain` imports nothing internal.
- `core/service` imports `domain`, `port` and the metrics package (`internal/adapter/outbound/metrics`) only.
- Inbound http imports `service`, `port`, `domain`, `signupctx`, `middleware`; outbound adapters import `port`, `domain` and the metrics package; adapters never import each other.
- Only `cmd/server` wires adapters to ports.
- No package imports a Keycloak client library (SB-INV-6). This is a review rule: `.go-arch-lint.yml` sets `depOnAnyVendor: true`, so go-arch-lint does not check vendor imports.

---

## Critical Invariants (from LLD §2.3)

- **SB-INV-2:** The plain password never leaves process memory. Not logged, not in Valkey, not in traces. Only `hashed_password_seed` (PBKDF2-SHA512 JSON) is sent to RP-1 / RP-2. The same goes for the payment token (only its HMAC is stored) and Billing's `client_secret` (returned to the browser, never logged or stored).
- **SB-INV-3:** One `tenant_id` per signup attempt — allocated once, reused for every retry/resume/slug change. On SB-5 this keeps the Billing key `charge:<tenant_id>` stable: a retry or 3-D Secure re-POST resumes the same handoff, and past payment a resubmit never allocates a new `tenant_id`.
- **SB-INV-4:** RP before I-1, always. `owner_user_id` sent to I-1 is the `keycloak_user_id` returned by RP-1 (trial) or RP-2 (paid).
- **SB-INV-5:** CAPTCHA and rate limit run before any internal call or password hashing. Exceptions: a same-email resume of an existing identity calls nothing downstream and skips both; an SB-5 payment resume runs CAPTCHA but does not use rate-limit budget.
- **SB-INV-8:** RP and I-1 carry identical `plan`, slug and name. `plan` is always `starter`/`pro`/`enterprise`, never `"trial"`.
- **SB-5:** `payment_token` is a provider browser token — no PAN ever reaches the BFF. B-I1 key `charge:<tenant_id>`; retries repeat the key and body. The handoff is kept when the Billing outcome is unknown (timeout, 500/502/503/504, `REQUEST_IN_PROGRESS`) and after payment; it is deleted only on `PAYMENT_DECLINED`, `CHARGE_EXPIRED` or a rejected request. `SUBSCRIPTION_ALREADY_EXISTS` is never used to provision. RP-2 Idempotency-Key: `signup:<tenant_id>:rp2`; once RP-2 was called, slug/name/email are frozen (`rp2_attempted`). A trial and a paid handoff never overwrite each other on one session (`409 other_signup_in_progress`). I-1 gets `email_hash` on the trial path only.

## Additional implementation invariants

- **Rate limiter:** Fixed-window Lua script (`INCR` → `EXPIRE` only on count==1). Never use `INCR + EXPIRE` pipeline (that creates a sliding window).
- **CSRF comparison:** Always use `subtle.ConstantTimeCompare` — not `!=` string comparison. Same for email and payment-ref HMACs (`hmacEqual()` in signup_service.go).
- **CAPTCHA_SKIP:** Only valid when `APP_ENV ∈ {dev, test, local}`. `config.Validate()` rejects it in all other environments.
- **Observability sink:** all logs, metrics and traces go through platform-gincommon only (`make log-sink-lint`):
  - Logs: `log.Info/Warn/Error/Debug` (gincommon logger). Never `fmt.Print*`, the standard `log`/`slog`, zap/zerolog/logrus, or `gin.Default/Logger/Recovery` (the only exception: the pre-logger `fmt.Fprintf` in `main()`).
  - Metrics: `metrics.*` helpers, built with gincommon constructors and type aliases (`gincommon.CounterVec`, …). No `github.com/prometheus/*` import anywhere in non-test code.
  - Traces: `internal/adapter/outbound/tracing` only — `tracing.Annotate` (span attributes), `tracing.StartClientSpan`, `tracing.Transport(dependency, propagate)` (outbound HTTP span + `traceparent`; `propagate=false` for Turnstile). It is the only non-test package that may import OpenTelemetry. Every `http.Client` uses `tracing.Transport`; every Valkey client gets `AddHook(tracingHook{})`.
- **Dependency metrics:** Every outbound HTTP call must be wrapped with `metrics.ObserveDependencyCall(dependency, operation, duration, resp, err)` immediately after `httpClient.Do()`. Enforced by `make metrics-lint`.
- **Session TTL:** Read from `SESSION_TTL_SECONDS` env var (default 1800). Hardcoding 1800 is a bug.
- **Rate limits:** Read from `RATE_*` env vars (e.g., `RATE_SIGNUPS_PER_IP_PER_HOUR`); SB-2 through its constructor, SB-5 through `WithRateLimits`. Hardcoding limits as constants is a bug.
- **Deadlines and lock:** the handoff lock TTL is `cfg.HandoffLockTTL()` (longest deadline + 5 s) — never shorter than a request holding it. Handlers set their own deadline (`REQUEST_DEADLINE_MS`; SB-5 `PAID_REQUEST_DEADLINE_MS`). gincommon's outer `RequestTimeout` and the server `WriteTimeout` follow `cfg.MaxRequestDeadline()`; an outer timeout shorter than SB-5's would cut SB-5 off.

---

## Open items

See LLD §16 (open before production: SRE infra BFF-1/2/4; deferred: BFF-6 on-call, N-10a realm policy sweep) and §19.6 (SB-5 remaining work: Pact contract, gateway timeout, front end, staging Test-Clock E2E).
