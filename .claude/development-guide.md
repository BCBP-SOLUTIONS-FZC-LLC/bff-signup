# Development Guide

## First-Time Setup

```bash
make setup                 # copy .env-example → .env + install pre-commit hook
go mod tidy                # download all dependencies
make docker-up             # start local Valkey on :6379
make run                   # start server on :8080
```

The `.env-example` has Cloudflare's Turnstile test keys (`1x0000000000000000000000000000000AA`), for which Cloudflare always returns success. To skip the Turnstile call entirely, set `CAPTCHA_SKIP=true` (accepted only when `APP_ENV` is `dev`, `test` or `local`).

## Running Tests

```bash
make test-unit              # fast, no Docker; includes contract tests (test/unit/service/contract_test.go)
make docker-up && make test-integration  # Valkey required
make test-e2e               # Docker only: in-process router + services, Valkey testcontainer, Go mocks of O&M and RP
```

Integration tests use testcontainers-go to spin up a real Valkey. They do NOT mock Valkey — the real TTLs, fixed-window counters, and Lua scripts are tested. Before pushing, run `/ci-local` (CI's pinned golangci-lint v2, swag-check, coverage gate).

## Environment Variables (minimum for local dev)

| Var | Dev value |
|---|---|
| `VALKEY_URL` | `redis://:localdev@localhost:6379/0` |
| `SBFF_HMAC_KEY` | Any 32+ byte string |
| `SBFF_TURNSTILE_SECRET` | `1x0000000000000000000000000000000AA` (always-pass) |
| `CAPTCHA_SITE_KEY` | `1x00000000000000000000AA` |
| `REALM_PROVISIONER_BASE_URL` | `http://localhost:9001` (fake or dev IAM) |
| `ORG_MEMBERSHIP_BASE_URL` | `http://localhost:9002` (fake or dev IAM) |

Paid signup (SB-5) stays off locally unless you set `PAID_SIGNUP_ENABLED=true` and `BILLING_BASE_URL` (then `BILLING_TIMEOUT_MS ≥ 15000` and `PAID_REQUEST_DEADLINE_MS ≥ BILLING + RP + I1 timeouts` are enforced).

## Adding a New Endpoint

1. Add the handler to `internal/adapter/inbound/http/handlers.go`
2. Add request/response DTOs to `dto.go`
3. Register the route in `router.go`
4. Add service logic to the appropriate service in `internal/core/service/`
5. If it needs a new port interface, add to `internal/core/port/`
6. If it needs a new Valkey key, document in `.claude/CLAUDE.md` and `LLD §4.1`
7. Add swag annotations and run `make swag` (generated `docs/swagger/` cannot be edited by hand)

## Adding a New Outbound Client

1. Create `internal/adapter/outbound/<name>/client.go`
2. Add `var _ port.X = (*Client)(nil)` compile-time check
3. Add the port interface to `internal/core/port/`
4. Wire in `cmd/server/main.go` / `wiring.go`
5. Call `metrics.ObserveDependencyCall(...)` right after `httpClient.Do()` (`make metrics-lint` enforces it), add it to the dependency table in `docs/observability/README.md` and the `operation` values in `metrics/registry.go`, then run `make metrics-inventory` (the inventory is generated)
6. Register the package in `.go-arch-lint.yml`

## Common Pitfalls

**Password in logs:** `ValidatePassword` receives the plain password. Never log `in.Password` or any field containing it — nor the payment token or Billing's `client_secret`. No redaction test exists yet (LLD §14.1 target), so review every new log call.

**Wrong CAPTCHA order:** CAPTCHA must run before rate-limit counters are incremented (SB-INV-5). Moving the rate limit check before CAPTCHA would let bots burn real users' limits.

**I-1 with Idempotency-Key:** Do NOT add an `Idempotency-Key` header to I-1 calls. O&M doesn't have an idempotency ledger; idempotency is by `tenant_id` state.

**plan: "trial":** Never send `"plan": "trial"` to RP-1 or I-1. RP-1 rejects it with `400 INVALID_REQUEST` (BFF bug, will page). The plan list and fallback contain only `starter`/`pro`/`enterprise`.

**Resubmit with changed email while allocated:** If the submitted email HMAC differs from the stored handoff and state is `allocated`, the old handoff must be deleted; keep the session's `signup_id` and allocate a new `tenant_id`. This prevents RP from replaying a result built with the old email.

**SB-5 and tenant_id:** never allocate a new `tenant_id` while a paid attempt is in progress or past payment — Billing keys the charge on `charge:<tenant_id>`, so a new tenant_id is a new charge (LLD §19.4). Keep the handoff on any unknown Billing outcome.

**Lock release:** Always `defer s.handoffs.ReleaseLock(ctx, signupID, token)` immediately after acquiring — use `//nolint:errcheck` on the defer since there's nothing to do if release fails (lock expires in 15s regardless).

**Slug attempt count:** `slug_attempts` is in the handoff record. Check `h.SlugAttempts >= maxSlugAttempts` (5) before accepting SB-4.

## Test Patterns

**Unit tests** — hand-written fakes implementing port interfaces. No Valkey, no HTTP. Test domain logic, state machine transitions, slug derivation, password validation, HMAC.

**Integration tests** (`test/integration/`) — real Valkey via testcontainers: TTLs, counters, lock behaviour.

**E2E tests** (`test/e2e/`, `make test-e2e`) — the full SB-1 → SB-5 flows through the real router and services, with a Valkey testcontainer and Go mocks of O&M and RP.

**Adapter tests** (`test/unit/adapter/`) — `httptest` stubs per outbound adapter; `billing_test.go` covers every B-I1 status and `errorCode`.

**Contract tests** — verify the exact request/response shape the BFF sends to RP-1 and O&M. Assert: plan is never `"trial"`, RP-1 gets `Idempotency-Key`, I-1 does not, RP-1 and I-1 carry identical plan/slug/name.

**Security tests** — verify rate limiting, CSRF rejection on wrong Origin, session isolation between users, spoofed `X-Forwarded-For` doesn't affect rate-limit key.

## Observability

- Metrics: `bff_signup_*` prefix (see LLD §11.2); `mode` is `trial` or `paid`. No label carries email, IP, or tenant id.
- Traces: `traceparent` propagated to Turnstile, Valkey, RP-1/RP-2, I-1, Billing.
- Logs: `request_id`, `trace_id`, `signup_id`, `tenant_id`, `state`, `stage`, `outcome`. Never log password, seed, CAPTCHA token, CSRF token, cookie, full email, payment token or `client_secret`.
- Dashboard: "Signup Funnel" in Grafana — sessions → submissions → CAPTCHA pass → RP-1 success → I-1 success.
