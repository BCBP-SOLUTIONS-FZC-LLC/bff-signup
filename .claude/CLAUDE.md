# CLAUDE.md

This file provides guidance to Claude Code when working with **bff-signup**, the public trial-signup BFF service.

## What This Repo Is

`bff-signup` is the platform's **public, unauthenticated tenant-onboarding intake** (Go module: `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`, Go 1.26.10). It serves the signup form's JSON API, enforces abuse controls (rate limit, CAPTCHA, CSRF), and runs the provisioning handoff.

**Trial path (SB-2, Phase 1 — active):**
1. Trial eligibility check → O&M `POST /internal/trial-eligibility` (CR-4)
2. RP-1 → Realm Provisioner `POST /internal/tenants/provision-trial` (creates Keycloak user in shared trial realm)
3. I-1 → Org & Membership `POST /internal/tenants` (creates tenant row)

**Direct paid path (SB-5, Phase 1.5 — stub wired, returns 404 until `PAID_SIGNUP_ENABLED=true`):**
1. Billing → `AuthorizeAndCharge` → `billing_subscription_id`
2. RP-2 → Realm Provisioner `POST /internal/tenants/provision-direct` (creates dedicated realm + Keycloak user)
3. I-1 → Org & Membership `POST /internal/tenants` (creates tenant row)

**Owns:** ephemeral Valkey state only (sessions, handoffs, rate-limit counters). No database. No event bus participation.

**Does NOT own:** Keycloak admin, JWT issuance, tenant data, memberships, billing.

**Namespace / identity:** `bff` namespace, service account `bff-signup`, SPIFFE `spiffe://<trust-domain>/ns/bff/sa/bff-signup` (BFF-1).

**Phase 1.5 activation:** set `PAID_SIGNUP_ENABLED=true` and `BILLING_BASE_URL=<url>` in Helm values, then replace `buildBilling()` in `cmd/server/wiring.go` with the real HTTP adapter.

**LLD:** `docs/lld/signup-bff-lld.md` v0.6 — canonical reference for all design decisions. §19 documents Phase 1.5 stub status and remaining activation steps.

---

## Common Commands

```bash
make setup           # Copy .env-example → .env (run once)
make tidy            # go mod tidy
make fmt             # gofmt -w
make fmt-check       # Verify formatting (CI)
make vet             # go vet ./...
make lint            # golangci-lint
make arch-lint       # go-arch-lint (layering rules)
make metrics-lint    # metric naming + adapter instrumentation + registry guard
make log-sink-lint   # enforce all logs/metrics/traces go through platform-gincommon
make mod-verify      # go mod verify
make vuln-check      # govulncheck

make test            # unit + integration tests
make test-ci         # with -race + coverage (CI, ≥ 95% gate)
make test-unit       # unit tests only (no Docker)
make test-integration # integration tests (requires Docker Valkey)
make test-contract   # contract tests (RP-1, I-1, eligibility)
make test-e2e        # end-to-end tests against staging
make test-load       # k6 load tests
make race            # all tests with -race

make build           # compile → bin/server
make run             # go run ./cmd/server (sources .env)
make cover           # coverage HTML report
make cover-func      # coverage by function
make swag            # regenerate docs/swagger/ from @annotations

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
- `core/service` imports `domain` and `port` only.
- `adapter/*` imports `port` and `domain`; adapters never import each other.
- Only `cmd/server` wires adapters to ports.
- No package imports a Keycloak client library (enforces SB-INV-6).

---

## Critical Invariants (from LLD §2.3)

- **SB-INV-2:** The plain password never leaves process memory. Not logged, not in Valkey, not in traces. Only `hashed_password_seed` (PBKDF2-SHA512 JSON) is sent to RP-1 / RP-2.
- **SB-INV-3:** One `tenant_id` per signup attempt — allocated once, reused for every retry/resume/slug change.
- **SB-INV-4:** RP before I-1, always. `owner_user_id` sent to I-1 is the `keycloak_user_id` returned by RP-1 (trial) or RP-2 (paid).
- **SB-INV-5:** CAPTCHA and rate limit run before any internal call or password hashing.
- **SB-INV-8:** RP and I-1 carry identical `plan`, slug and name. `plan` is always `starter`/`pro`/`enterprise`, never `"trial"`.
- **Phase 1.5:** `payment_token` is a Stripe browser token — no PAN ever reaches the BFF. Billing failure deletes the handoff (no orphaned charges). RP-2 Idempotency-Key format: `signup:<tenant_id>:rp2`.

## Additional implementation invariants

- **Rate limiter:** Fixed-window Lua script (`INCR` → `EXPIRE` only on count==1). Never use `INCR + EXPIRE` pipeline (that creates a sliding window).
- **CSRF comparison:** Always use `subtle.ConstantTimeCompare` — not `!=` string comparison. Same for email HMAC (`hmacEqual()` in signup_service.go).
- **CAPTCHA_SKIP:** Only valid when `APP_ENV ∈ {dev, test, local}`. `config.Validate()` rejects it in all other environments.
- **Observability sink:** All logs via `log.Info/Warn/Error/Debug` (gincommon logger). All metrics via `metrics.*` helpers. All traces via gincommon middleware. Never use `fmt.Println`, standard `"log"` package, or raw `prometheus.New*` outside `internal/adapter/outbound/metrics/`. Enforced by `make log-sink-lint`.
- **Dependency metrics:** Every outbound HTTP call must be wrapped with `metrics.ObserveDependencyCall(dependency, operation, duration, resp, err)` immediately after `httpClient.Do()`. Enforced by `make metrics-lint`.
- **Session TTL:** Read from `SESSION_TTL_SECONDS` env var (default 1800). Hardcoding 1800 is a bug.
- **Rate limits:** Read from `RATE_*` env vars (e.g., `RATE_SIGNUPS_PER_IP_PER_HOUR`). Hardcoding limits as constants is a bug.

---

## Key Open Questions (pending TL decision)

See `docs/lld/signup-bff-open-questions-tl.md` — 4 items in Section A need TL sign-off.
Remaining team actions tracked in `docs/lld/signup-bff-action-tracker.md`.
