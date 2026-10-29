# Development Guide

## First-Time Setup

```bash
cp .env-example .env       # fill in dev values
go mod tidy                # download all dependencies
make docker-up             # start local Valkey on :6379
make run                   # start server on :8080
```

The `.env-example` has Turnstile test keys (`1x0000000000000000000000000000000AA`) that always pass. In dev, Turnstile verification is skipped when the key matches the always-pass pattern.

## Running Tests

```bash
make test-unit              # fast, no Docker
make docker-up && make test-integration  # Valkey required
make test-contract          # requires RP-1 and O&M fakes
```

Integration tests use testcontainers-go to spin up a real Valkey. They do NOT mock Valkey — the real sliding TTLs, counters, and Lua scripts are tested.

## Environment Variables (minimum for local dev)

| Var | Dev value |
|---|---|
| `VALKEY_URL` | `redis://:localdev@localhost:6379/0` |
| `SBFF_HMAC_KEY` | Any 32+ byte string |
| `SBFF_TURNSTILE_SECRET` | `1x0000000000000000000000000000000AA` (always-pass) |
| `CAPTCHA_SITE_KEY` | `1x00000000000000000000AA` |
| `REALM_PROVISIONER_BASE_URL` | `http://localhost:9001` (fake or dev IAM) |
| `ORG_MEMBERSHIP_BASE_URL` | `http://localhost:9002` (fake or dev IAM) |

## Adding a New Endpoint

1. Add the handler to `internal/adapter/inbound/http/handlers.go`
2. Add request/response DTOs to `dto.go`
3. Register the route in `router.go`
4. Add service logic to the appropriate service in `internal/core/service/`
5. If it needs a new port interface, add to `internal/core/port/`
6. If it needs a new Valkey key, document in `CLAUDE.md` and `LLD §4.1`

## Adding a New Outbound Client

1. Create `internal/adapter/outbound/<name>/client.go`
2. Add `var _ port.X = (*Client)(nil)` compile-time check
3. Add the port interface to `internal/core/port/`
4. Wire in `cmd/server/main.go`

## Common Pitfalls

**Password in logs:** `ValidatePassword` receives the plain password. Never log `in.Password` or any field containing it. The redaction test in `test/unit/` asserts no password appears in any log line.

**Wrong CAPTCHA order:** CAPTCHA must run before rate-limit counters are incremented (SB-INV-5). Moving the rate limit check before CAPTCHA would let bots burn real users' limits.

**I-1 with Idempotency-Key:** Do NOT add an `Idempotency-Key` header to I-1 calls. O&M doesn't have an idempotency ledger; idempotency is by `tenant_id` state.

**plan: "trial":** Never send `"plan": "trial"` to RP-1 or I-1. RP-1 rejects it with `400 INVALID_REQUEST` (BFF bug, will page). The plan list and fallback contain only `starter`/`pro`/`enterprise`.

**Resubmit with changed email while allocated:** If the submitted email HMAC differs from the stored handoff and state is `allocated`, the old handoff must be deleted and a new `signup_id` + `tenant_id` allocated. This prevents RP from replaying a result built with the old email.

**Lock release:** Always `defer s.handoffs.ReleaseLock(ctx, signupID, token)` immediately after acquiring — use `//nolint:errcheck` on the defer since there's nothing to do if release fails (lock expires in 15s regardless).

**Slug attempt count:** `slug_attempts` is in the handoff record. Check `h.SlugAttempts >= maxSlugAttempts` (5) before accepting SB-4.

## Test Patterns

**Unit tests** — hand-written fakes implementing port interfaces. No Valkey, no HTTP. Test domain logic, state machine transitions, slug derivation, password validation, HMAC.

**Integration tests** — real Valkey via testcontainers. Test the full SB-1 → SB-2 → SB-3 flow including TTLs, counters, and lock behaviour. Fake HTTP servers for RP-1 and O&M using `httptest.NewServer`.

**Contract tests** — verify the exact request/response shape the BFF sends to RP-1 and O&M. Assert: plan is never `"trial"`, RP-1 gets `Idempotency-Key`, I-1 does not, RP-1 and I-1 carry identical plan/slug/name.

**Security tests** — verify rate limiting, CSRF rejection on wrong Origin, session isolation between users, spoofed `X-Forwarded-For` doesn't affect rate-limit key.

## Observability

- Metrics: `bff_signup_*` prefix (see LLD §11.2). No label carries email, IP, or tenant id.
- Traces: `traceparent` propagated to Turnstile, Valkey, RP-1, I-1.
- Logs: `request_id`, `trace_id`, `signup_id`, `tenant_id`, `state`, `stage`, `outcome`. Never log password, seed, CAPTCHA token, CSRF token, cookie, or full email.
- Dashboard: "Signup Funnel" in Grafana — sessions → submissions → CAPTCHA pass → RP-1 success → I-1 success.
