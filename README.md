# bff-signup

Public signup BFF (trial and direct-paid) for the XpertPMS platform — the only internet-facing service that accepts unauthenticated requests.

**Repository:** `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`  
**Module:** Go 1.26.11 · private module · pre-production (see [`VERSIONING.md`](VERSIONING.md))

---

## Mental model

This service is the **public onboarding intake** for self-service signups: free trial (SB-2) and direct paid (SB-5). It enforces abuse controls (CAPTCHA, rate limiting, CSRF), validates all inputs, hashes the password into Keycloak's format, and orchestrates the provisioning handoff across two IAM services (and, for paid signups, Billing).

| Concern | Owner |
|---|---|
| Trial signup form API (SB-1…SB-4) | **This service** |
| Direct paid signup (SB-5) | **This service** — real Billing B-I1 adapter; `/paid` returns `404` until `PAID_SIGNUP_ENABLED=true` (Billing go-live step 4, LLD §19.5) |
| Abuse controls (CAPTCHA, rate limits, CSRF, WAF) | **This service** + ALB |
| Password hashing (PBKDF2-SHA512) | **This service** — authoritative at signup |
| Keycloak user creation | Realm Provisioner (RP-1 trial, RP-2 paid) |
| Tenant row creation, one-trial ledger, slug lookup | Org & Membership (I-1, eligibility, PUB-1) |
| Session, handoff, rate-limit counters | Ephemeral Valkey (no DB) |
| Login, tokens, MFA, email verification | Keycloak (built-in flows) |
| Charging, subscriptions | Billing service — B-I1 authorize-and-charge (LLD §19) |
| Signup page UI | Web frontend app |

**This service owns no durable business data.** All Valkey state expires within 24 hours. There is nothing to erase on a GDPR request.

---

## Why this service exists

The IAM internal routes (`/api/v1/internal/…`) can create Keycloak users and tenants without any login token. Without a dedicated public BFF:

- Any internet client could call IAM internals directly — no CAPTCHA, no rate limiting, no CSRF.
- The password would travel in plain text across the network instead of being hashed at the edge.
- The multi-step provisioning handoff (eligibility → PUB-1 → RP-1 → I-1, or Billing → PUB-1 → RP-2 → I-1) would be duplicated across every form implementation.

This service acts as the **single, auditable gate** between the public internet and IAM provisioning internals.

---

## API overview

All routes served at `https://signup.bcbpsolutions.com`.  
Probes: `GET /healthz`, `GET /readyz` (Valkey ping).  
Metrics: `GET /metrics` on port 9090 (monitoring namespace only).

### Public endpoints

| # | Method + Path | Auth | Purpose |
|---|---|---|---|
| SB-1 | `GET /api/v1/signup/session` | None | Start session: sets `sbff_sid` cookie, returns CSRF token, plan list, password policy, slug rules |
| SB-2 | `POST /api/v1/signup/trial` | Cookie + `X-CSRF-Token` | Submit trial form; runs CAPTCHA → rate limit → validate → hash → provision |
| SB-3 | `GET /api/v1/signup/{signup_id}/status` | Cookie (session-bound) | Poll provisioning status; resumes I-1 if `tenant_pending` |
| SB-4 | `POST /api/v1/signup/{signup_id}/slug` | Cookie + `X-CSRF-Token` | Pick a new slug after `409 slug_taken`; retries I-1 |
| SB-5 | `POST /api/v1/signup/paid` | Cookie + `X-CSRF-Token` | Submit direct paid form: CAPTCHA → rate limit → validate → hash → Billing B-I1 → PUB-1 → RP-2 → I-1; `202 payment_action_required` for 3-D Secure; `404` until `PAID_SIGNUP_ENABLED=true` |

### SB-1 response shape

```json
{
  "signup_id": "01JABCD...",
  "csrf_token": "b64url-32-bytes",
  "captcha": { "provider": "turnstile", "site_key": "0x..." },
  "plans": [
    { "code": "starter", "name": "Starter", "trial_days": 14 },
    { "code": "pro",     "name": "Pro",     "trial_days": 14 },
    { "code": "enterprise", "name": "Enterprise", "trial_days": 14 }
  ],
  "password_policy": { "min_length": 12, "max_length": 128, "require_classes": 3, "not_email": true },
  "slug_rules": { "pattern": "^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$", "min": 3, "max": 30 }
}
```

### SB-2 request shape

```json
{
  "company_name": "Acme Bidding Co",
  "slug": "acme",
  "admin_name": "Dana Owner",
  "admin_email": "owner@acme.example",
  "password": "••••••••••••",
  "plan": "pro",
  "accept_terms": true,
  "terms_version": "2026-09",
  "captcha_token": "0.xxxx"
}
```

### SB-2 responses

| Status | Meaning |
|---|---|
| `201 Created` | Provisioning complete. `{status:"completed", next:"check_email"}` |
| `202 Accepted` | RP-1 done, I-1 still in progress. Poll SB-3. `{status:"provisioning", poll_after_ms:2000}` |
| `409 SLUG_TAKEN` | Slug owned by another tenant. `{signup_id, suggestions:[]}` — if the handoff reached I-1 (`slug_conflict`), submit SB-4; if the PUB-1 pre-check caught it (no account created yet), resubmit SB-2 with the new slug |

### SB-5 request shape

SB-2's fields plus `seats` (≥ 1) and `payment_token` (the payment provider's browser token, `pm_…`; no card data ever reaches the BFF).

### SB-5 responses

| Status | Meaning |
|---|---|
| `201 Created` | Charged and fully provisioned. `{status:"completed", next:"check_email"}` |
| `202 Accepted` | `{status:"provisioning", poll_after_ms:2000}` — poll SB-3; or `{status:"payment_action_required", action:{type:"3ds", client_secret}, expires_at}` — complete 3-D Secure with the provider's JS, then re-POST the **same body** with a fresh `captcha_token` |
| `409` | `SLUG_TAKEN` (resubmit SB-5 with a new slug if paid but not yet provisioned, else SB-4); `INVALID_STATE` (a payment is in progress, or details changed after payment) |
| `410 CHARGE_EXPIRED` | 3-D Secure not completed within 30 min — start again |
| `422 PAYMENT_DECLINED` | `decline_reason` — ask for another payment method |
| `503` | Billing or another dependency unavailable — resubmit the same body after `Retry-After` (the same `tenant_id` and Billing key are reused, so nothing is charged twice) |

See LLD §19 for the Billing contract, retries and the go-live order.

---

## Validation rules

All validation runs server-side, after CAPTCHA and before any internal service call or password hashing.

| Field | Rule |
|---|---|
| `company_name` | Trimmed, 2–100 chars, no control chars |
| `slug` | Optional — derived if absent. Pattern `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`, 3–30 chars, no reserved words |
| `admin_name` | Trimmed, 1–100 chars, no control chars |
| `admin_email` | ≤ 254 chars, RFC 5322 syntactically valid, domain lowercased |
| `password` | 12–128 chars, ≥ 3 character classes (upper/lower/digit/special), must not equal email |
| `plan` | One of `starter`, `pro`, `enterprise` — never `"trial"` |
| `accept_terms` | Must be `true` |
| `terms_version` | Non-empty, known version (current: `2026-09`) |
| `captcha_token` | Non-empty |

**Password validation is authoritative.** Because the password is hashed (PBKDF2-SHA512) before reaching Keycloak, Keycloak cannot re-check it at signup. The BFF's policy check is the only gate (SB-INV-7). The policy must stay in sync with the Keycloak `trial` realm hash policy — see CONTRIBUTING.md.

### Error response shape

Errors are RFC 9457 problem details (`application/problem+json`). Extra details are top-level members.

```json
{
  "type": "…",
  "title": "Bad Request",
  "status": 400,
  "detail": "admin_email is invalid",
  "errorCode": "VALIDATION_FAILED",
  "traceId": "…"
}
```

### Error codes

| `errorCode` | HTTP | Meaning |
|---|---|---|
| `VALIDATION_FAILED` | 400 | One or more fields invalid |
| `SESSION_EXPIRED` | 401 | Missing or expired session cookie |
| `CSRF_INVALID` | 403 | Wrong CSRF token or Origin header |
| `CAPTCHA_FAILED` | 403 | Turnstile rejected the token |
| `SIGNUP_NOT_FOUND` | 404 | Unknown `signup_id` or not this session |
| `EMAIL_UNAVAILABLE` | 409 | Email already registered in Keycloak |
| `TRIAL_NOT_ELIGIBLE` | 409 | Email has already used a free trial |
| `SLUG_TAKEN` | 409 | Slug owned by another tenant (+ `suggestions[]`) |
| `INVALID_STATE` | 409 | Action invalid for the current provisioning state, or details changed (`reason: details_changed`) |
| `CHARGE_EXPIRED` | 410 | SB-5: 3-D Secure not completed within 30 min |
| `PAYMENT_DECLINED` | 422 | SB-5: payment declined (+ `decline_reason`) |
| `RATE_LIMITED` | 429 | Rate limit exceeded (+ `Retry-After`) |
| `INTERNAL_ERROR` | 500 | BFF bug, or an IAM/Billing contract mismatch |
| `DEPENDENCY_UNAVAILABLE` | 503 | Valkey, Turnstile, eligibility, RP-1/RP-2 or Billing unavailable (+ `Retry-After` when known) |

---

## Rate limits

| Scope | Limit | Window |
|---|---|---|
| New signups per IP (SB-2, SB-5) | 3 | 1 hour |
| Attempts per email | 5 | 24 hours |
| Session starts per IP | 20 | 1 hour |
| Status polls per session | 60 | 1 minute |

Counters increment **after** CAPTCHA passes — a real user failing a CAPTCHA challenge does not burn an attempt. Resuming an existing signup (including an SB-5 3-D Secure re-POST) does not count. All limits are set by `RATE_*` env vars. Valkey unavailable → `503` (fail-closed, no counters = no rate limiting).

---

## Architecture

```
bff-signup/
├── cmd/server/          ← Composition root (main.go) + client factories (wiring.go)
├── internal/
│   ├── config/          ← Fail-fast env config
│   ├── core/
│   │   ├── domain/      ← Errors, Handoff state machine, Plan, slug rules, password validation
│   │   ├── port/        ← All port interfaces
│   │   └── service/     ← Session, Signup, PaidSignup, HandoffOrchestrator, Status
│   └── adapter/
│       ├── inbound/http/   ← Gin router, handlers (SB-1…5), RFC 9457 error mapping, middleware
│       └── outbound/       ← Valkey, RP-1/RP-2, O&M (I-1, eligibility, PUB-1), Billing (B-I1), Turnstile, Catalog, PBKDF2 hasher, metrics
├── pkg/signupctx/       ← SignupContext{Session, ClientIP}
├── test/                ← unit, integration (Valkey), e2e (in-process), fixtures
├── deploy/helm/         ← Helm chart (dev/staging/prod values); deploy/monitoring/ alerts + dashboard
└── docs/                ← LLD v1.4, architecture diagrams, observability, generated Swagger
```

**Dependency rule:** `domain ← port ← service ← adapter ← cmd`, with one exception: `core/service` may import the metrics package. Enforced by `go-arch-lint` (`.go-arch-lint.yml`).

### Provisioning handoff

```
SB-2: CAPTCHA → rate-limit → validate → hash → eligibility → PUB-1 pre-check → RP-1 → I-1
SB-5: CAPTCHA → rate-limit → validate → hash → Billing B-I1 → PUB-1 pre-check → RP-2 → I-1
         ↓ I-1 fails after retries
SB-3: poll → resume I-1 (15-min window) → O&M backstop on browser abandon
         ↓ slug conflict
SB-4: new slug → retry I-1 (same tenant_id, same Keycloak user)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for full sequence diagrams and the handoff state machine.

### Cross-service dependencies

| Service | Operation | Path | Protocol | Timeout | Metric label | Phase |
|---|---|---|---|---|---|---|
| **Realm Provisioner** | RP-1 provision-trial | `POST /api/v1/internal/tenants/provision-trial` | mTLS (Istio) | 2000 ms (`RP_TIMEOUT_MS`; RP p95 ≤ 1.5 s) | `realm_provisioner` | Trial |
| **Realm Provisioner** | RP-2 provision-direct | `POST /api/v1/internal/tenants/provision-direct` | mTLS (Istio) | 8000 ms (`RP2_TIMEOUT_MS`; RP p95 ≤ 4 s) | `realm_provisioner` | Paid |
| **Org & Membership** | I-1 provision-tenant | `POST /api/v1/internal/tenants` | mTLS (Istio) | 300 ms | `org_membership` | Both |
| **Org & Membership** | Eligibility check (CR-4) | `POST /api/v1/internal/trial-eligibility` | mTLS (Istio) | 150 ms | `org_membership` | Trial |
| **Org & Membership** | PUB-1 slug pre-check | `GET /api/v1/public/tenants/resolve` | mTLS (Istio) | 300 ms | `org_membership` | Both |
| **Billing** | B-I1 authorize-and-charge | `POST /api/v1/internal/subscriptions/authorize-and-charge` | mTLS (Istio) | 15000 ms (≥ 15000 enforced when enabled) | `billing` | Paid |
| **Cloudflare Turnstile** | CAPTCHA siteverify | `POST /turnstile/v0/siteverify` | HTTPS (internet) | 300 ms | `turnstile` | 1 |
| **Valkey** | Session / handoff / rate-limit | key-value ops | TLS AUTH | 50 ms | — | 1 |
| **Catalog / Admin Config** | Plan list | `GET /api/v1/plans` | mTLS (Istio) | 3 s (5-min cache) | `catalog` | Both (optional) |

RP-1, RP-2, I-1 and eligibility carry `x-tenant-id`, `x-user-id: …00a1` and `x-tenant-roles: iam-system`; RP-1/RP-2 add `Idempotency-Key`. PUB-1 carries `x-user-id`. Billing B-I1 gets only `x-tenant-id` and `Idempotency-Key: charge:<tenant_id>` (caller identity is the mesh SPIFFE ID). The BFF never reads a JWT — it has no authenticated users.

Every outbound HTTP call is timed by `bff_signup_dependency_call_duration_seconds{dependency, outcome}` and shadowed on the Tier-1 Proposed `platform_dependency_request_seconds{dependency, operation, outcome}`. Timeouts are configurable via `RP_TIMEOUT_MS`, `I1_TIMEOUT_MS`, `ELIGIBILITY_TIMEOUT_MS`, `TURNSTILE_TIMEOUT_MS`, `BILLING_TIMEOUT_MS` (see `.env-example`).

---

## API documentation (Swagger UI)

The service ships with a Swagger 2.0 spec generated by [`swaggo/swag`](https://github.com/swaggo/swag).

### Accessing the UI

| Environment | URL | Notes |
|---|---|---|
| **Local dev** | `http://localhost:8080/swagger/index.html` | Served when `APP_ENV` is `dev`, `test` or `local` |
| **Staging** | Not exposed (public-facing host) | `DocsEnabled=false` in `values-staging.yaml` |
| **Production** | Not exposed | `DocsEnabled=false` in `values-prod.yaml` |

The Swagger UI uses a BCBP-branded dark theme (`swagger_theme.go`) and a custom initializer (`swagger_initializer.go`) that pre-fills the production host.

### Regenerating the spec

```bash
make swag         # regenerates docs/swagger/ from @annotations in handlers.go + swagger_info.go
```

The generated files (`docs/swagger/docs.go`, `swagger.json`, `swagger.yaml`) are committed to the repository. Run `make swag-check` before pushing: it regenerates the spec and fails if `docs/swagger/` differs. CI's `check-swagger-stale.sh` only checks that the files exist and that the JSON parses.

### Security definitions in the spec

| Definition | Location | Description |
|---|---|---|
| `SessionCookie` | Cookie `sbff_sid` | Set by SB-1; HttpOnly, Secure, SameSite=Strict, 30-min sliding TTL |
| `CSRFToken` | Header `X-CSRF-Token` | Returned by SB-1; required on all POSTs |

---

## Local development

```bash
git clone https://github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup
cd bff-signup
make setup          # copies .env-example → .env + installs pre-commit hook
go mod tidy         # download all dependencies
make docker-up      # start local Valkey on :6379
make run            # start server on :8080
```

### Minimum environment variables (`.env-example`)

| Variable | Dev value | Notes |
|---|---|---|
| `VALKEY_URL` | `redis://:localdev@localhost:6379/0` | Required |
| `SBFF_HMAC_KEY` | Any 32+ byte string | Required; min 32 bytes |
| `SBFF_TURNSTILE_SECRET` | `1x0000000000000000000000000000000AA` | Always-pass test key |
| `CAPTCHA_SITE_KEY` | `1x00000000000000000000AA` | Required |
| `REALM_PROVISIONER_BASE_URL` | `http://localhost:9001` | Required |
| `ORG_MEMBERSHIP_BASE_URL` | `http://localhost:9002` | Required |
| `PUBLIC_HOST` | `signup.bcbpsolutions.com` | Required; used for Origin validation |
| `CAPTCHA_SKIP` | `false` | Set `true` locally to bypass Turnstile; **never in staging/prod** |
| `SESSION_TTL_SECONDS` | `1800` | Sliding session TTL |
| `PBKDF2_ITERATIONS` | `210000` | Minimum enforced; lower values rejected at startup |

All timeout and rate-limit variables (`RP_TIMEOUT_MS`, `RP2_TIMEOUT_MS`, `I1_TIMEOUT_MS`, `ELIGIBILITY_TIMEOUT_MS`, `REQUEST_DEADLINE_MS` (default 5000; must cover eligibility + RP-1 + 2×I-1), `RATE_*`) are optional with production-safe defaults. See `.env-example` for the full list.

Paid signup (SB-5) settings: `PAID_SIGNUP_ENABLED` (default `false`), `BILLING_BASE_URL` (required when enabled), `BILLING_TIMEOUT_MS` (default 15000; startup rejects < 15000 when enabled), `PAID_REQUEST_DEADLINE_MS` (default 30000; must be ≥ `BILLING_TIMEOUT_MS + RP2_TIMEOUT_MS + 2×I1_TIMEOUT_MS`).

The service **panics at startup** if a required env var is missing, and **exits 1** if one is invalid (e.g., `PBKDF2_ITERATIONS < 210000`, `SBFF_HMAC_KEY < 32 bytes`). `CAPTCHA_SKIP=true` is rejected by `config.Validate()` when `APP_ENV` is not `dev`, `test`, or `local`.

---

## Testing

| Layer | Command | Docker |
|---|---|---|
| Unit (no external deps) | `make test-unit` | No |
| Integration (real Valkey) | `make test-integration` | Yes |
| Contract (RP-1, I-1 shapes) | `make test-unit` (`test/unit/service/contract_test.go`) | No |
| End-to-end (in-process router + services, Valkey testcontainer, Go mocks of O&M and RP) | `make test-e2e` | Yes |
| All + race detector + coverage | `make test-ci` | Yes |
| Load (k6) | `make test-load` (script `test/load/signup.js` not yet written) | No |

CI enforces **≥ 97% statement coverage** on the merged profile (`COVERAGE_THRESHOLD` default in `.github/scripts/coverage-gate.sh`). Last recorded: 98.6%.

### Additional quality gates

| Gate | Command | What it checks |
|---|---|---|
| Architecture lint | `make arch-lint` | Layer boundaries (domain ← port ← service ← adapter ← cmd) |
| Metric naming | `make metrics-lint` | Tier classification, `_total`/`_seconds` suffixes, adapter instrumentation, label vocabulary |
| Observability sink | `make log-sink-lint` | All logs/metrics/traces go through platform-gincommon only |
| Vuln check | `make vuln-check` | Known CVEs in call paths (govulncheck) |
| Swagger staleness | `make swag-check` | `docs/swagger/` matches `@annotations` in handlers (CI only checks the files exist and parse) |

---

## Deployment

- **Namespace:** `bff` | **ServiceAccount:** `bff-signup`  
- **Replicas:** 2 min, 5 max | **HPA:** CPU 70% (PBKDF2 is CPU-bound, ~150ms per signup)
- **Image:** `ghcr.io/bcbp-solutions-fzc-llc/bff-signup:<sha>`
- **Secrets:** External Secrets Operator → OpenBao (`secret/data/bff/bff-signup/*`)

```bash
helm upgrade bff-signup ./deploy/helm \
  --install \
  --namespace bff \
  --set image.tag=<sha> \
  --set existingSecret=bff-signup-prod-secrets \
  -f deploy/helm/values-prod.yaml
```

**Go-live prerequisites** (LLD §16.2):
- SRE: mesh trust domain confirmed (BFF-1), SNS filter for the O&M backstop (BFF-2), `signup.bcbpsolutions.com` provisioned (BFF-4)
- On-call rotation (BFF-6) — deferred until PagerDuty is procured
- Paid signup: Billing go-live order (LLD §19.5) — then set `PAID_SIGNUP_ENABLED=true`

**Status:** code complete for Phase 1 and not deployed to any environment. The trial path (SB-1…SB-4) is implemented and tested (≥ 97% coverage gate). Direct paid signup (SB-5) is implemented against Billing B-I1 and returns `404` until `PAID_SIGNUP_ENABLED=true` is set at Billing go-live step 4.

---

## Related documents

| Document | Purpose |
|---|---|
| [`docs/lld/signup-bff-lld.md`](docs/lld/signup-bff-lld.md) | Full LLD v1.4 — the runtime contract in detail (§16 open items, §19 paid signup) |
| [`docs/architecture/`](docs/architecture/) | Mermaid diagram sources |
| [`docs/observability/`](docs/observability/) | Metric registry, runbooks |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Layer model, sequence diagrams, state machine |
| [`CHANGELOG.md`](CHANGELOG.md) | Version history |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Dev setup, testing, PR process |
| [`VERSIONING.md`](VERSIONING.md) | Versioning policy and release process |
