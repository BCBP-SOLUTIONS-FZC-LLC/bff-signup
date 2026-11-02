# bff-signup

Public trial-signup BFF for the XpertPMS platform — the only internet-facing service that accepts unauthenticated requests.

**Repository:** `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`  
**Module:** Go 1.26.10 · private module · pre-production (see [`VERSIONING.md`](VERSIONING.md))

---

## Mental model

This service is the **public onboarding intake** for self-service trial signups. It enforces abuse controls (CAPTCHA, rate limiting, CSRF), validates all inputs, hashes the password into Keycloak's format, and orchestrates the provisioning handoff across two IAM services.

| Concern | Owner |
|---|---|
| Trial signup form API (SB-1…SB-4) | **This service** |
| Direct paid signup (SB-5) | **This service** (stub; activates with `PAID_SIGNUP_ENABLED=true`) |
| Abuse controls (CAPTCHA, rate limits, CSRF, WAF) | **This service** + ALB |
| Password hashing (PBKDF2-SHA512) | **This service** — authoritative at signup |
| Keycloak user creation | Realm Provisioner (RP-1) |
| Tenant row creation, one-trial ledger | Org & Membership (I-1, eligibility) |
| Session, handoff, rate-limit counters | Ephemeral Valkey (no DB) |
| Login, tokens, MFA, email verification | Keycloak (built-in flows) |
| Billing, subscriptions | Billing service (Phase 1.5 — stub wired, route reserved; see §19 LLD) |
| Signup page UI | Web frontend app |

**This service owns no durable business data.** All Valkey state expires within 24 hours. There is nothing to erase on a GDPR request.

---

## Why this service exists

The IAM internal routes (`/api/v1/internal/…`) can create Keycloak users and tenants without any login token. Without a dedicated public BFF:

- Any internet client could call IAM internals directly — no CAPTCHA, no rate limiting, no CSRF.
- The password would travel in plain text across the network instead of being hashed at the edge.
- The multi-step provisioning handoff (eligibility → RP-1 → I-1) would be duplicated across every form implementation.

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
| SB-5 | `POST /api/v1/signup/paid` | Cookie + `X-CSRF-Token` | Submit direct paid form (Phase 1.5); returns `404` until `PAID_SIGNUP_ENABLED=true` |

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
| `409 slug_taken` | Slug owned by another tenant. `{signup_id, suggestions:[]}` — submit SB-4 to continue |

---

## Validation rules

All validation runs server-side before any external call.

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

```json
{
  "error": "validation_failed",
  "status": 400,
  "request_id": "req-xyz",
  "details": { "fields": { "admin_email": "admin_email.invalid" } }
}
```

### Error codes

| `error` | HTTP | Meaning |
|---|---|---|
| `validation_failed` | 400 | One or more fields invalid |
| `session_expired` | 401 | Missing or expired session cookie |
| `csrf_invalid` | 403 | Wrong CSRF token or Origin header |
| `captcha_failed` | 403 | Turnstile rejected the token |
| `signup_not_found` | 404 | Unknown `signup_id` or not this session |
| `email_unavailable` | 409 | Email already registered in Keycloak |
| `trial_not_eligible` | 409 | Email has already used a free trial |
| `slug_taken` | 409 | Slug owned by another tenant (+ `suggestions[]`) |
| `invalid_state` | 409 | Action invalid for current provisioning state |
| `rate_limited` | 429 | Rate limit exceeded (+ `Retry-After`) |
| `internal_error` | 500 | BFF bug or IAM contract mismatch |
| `dependency_unavailable` | 503 | Valkey, Turnstile, eligibility, or RP-1 unavailable |

---

## Rate limits

| Scope | Limit | Window |
|---|---|---|
| New signups per IP | 3 | 1 hour |
| Attempts per email | 5 | 24 hours |
| Session starts per IP | 20 | 1 hour |
| Status polls per session | 60 | 1 minute |

Counters increment **after** CAPTCHA passes — a real user failing a CAPTCHA challenge does not burn an attempt. Valkey unavailable → `503` (fail-closed, no counters = no rate limiting).

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
│       ├── inbound/http/   ← Gin router, handlers (SB-1…5), middleware
│       └── outbound/       ← Valkey, RP-1, RP-2, O&M, Turnstile, Catalog, PBKDF2 hasher
├── pkg/signupctx/       ← SignupContext{Session, ClientIP}
├── test/                ← All tests (unit, integration, fixtures)
├── deploy/helm/         ← Helm chart (dev/staging/prod values)
└── docs/lld/            ← LLD v0.6 + open questions + action tracker
```

**Dependency rule:** `domain ← port ← service ← adapter ← cmd`. Enforced by `go-arch-lint`.

### Provisioning handoff

```
SB-2: CAPTCHA → rate-limit → validate → hash → eligibility check → RP-1 → I-1
         ↓ I-1 fails after retries
SB-3: poll → resume I-1 (15-min window) → O&M backstop on browser abandon
         ↓ slug conflict
SB-4: new slug → retry I-1 (same tenant_id, same Keycloak user)
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for full sequence diagrams and the handoff state machine.

### Cross-service dependencies

| Service | Operation | Path | Protocol | Timeout | Metric label | Phase |
|---|---|---|---|---|---|---|
| **Realm Provisioner** | RP-1 provision-trial | `POST /internal/tenants/provision-trial` | mTLS (Istio) | 900 ms | `realm_provisioner` | 1 |
| **Realm Provisioner** | RP-2 provision-direct (stub) | `POST /internal/tenants/provision-direct` | mTLS (Istio) | 900 ms | `realm_provisioner` | 1.5 |
| **Org & Membership** | I-1 provision-tenant | `POST /internal/tenants` | mTLS (Istio) | 300 ms | `org_membership` | 1 |
| **Org & Membership** | Eligibility check (CR-4) | `POST /internal/trial-eligibility` | mTLS (Istio) | 150 ms | `org_membership` | 1 |
| **Org & Membership** | PUB-1 slug pre-check | `GET /public/tenants/resolve` | mTLS (Istio) | 300 ms | `org_membership` | 1 |
| **Billing** | AuthorizeAndCharge (stub) | TBD | mTLS (Istio) | 5000 ms | — | 1.5 |
| **Cloudflare Turnstile** | CAPTCHA siteverify | `POST /turnstile/v0/siteverify` | HTTPS (internet) | 300 ms | `turnstile` | 1 |
| **Valkey** | Session / handoff / rate-limit | key-value ops | TLS AUTH | 50 ms | — | 1 |
| **Catalog / Admin Config** | Plan list | `GET /internal/plans` | mTLS (Istio) | 3 s (5-min cache) | `catalog` | 1 (optional) |

All internal calls carry system-principal headers (`x-user-id: …00a1`, `x-tenant-id`, `Idempotency-Key` where required). The BFF never reads a JWT — it has no authenticated users.

Every outbound HTTP call is timed by `bff_signup_dependency_call_duration_seconds{dependency, outcome}` and shadowed on the Tier-1 Proposed `platform_dependency_request_seconds{dependency, operation, outcome}`. Timeouts are configurable via `RP_TIMEOUT_MS`, `I1_TIMEOUT_MS`, `ELIGIBILITY_TIMEOUT_MS`, `TURNSTILE_TIMEOUT_MS` (see `.env-example`).

---

## API documentation (Swagger UI)

The service ships with an OpenAPI 3.0 spec generated by [`swaggo/swag`](https://github.com/swaggo/swag).

### Accessing the UI

| Environment | URL | Notes |
|---|---|---|
| **Local dev** | `http://localhost:8080/swagger/index.html` | Served when `APP_ENV=dev` or `test` |
| **Staging** | Not exposed (public-facing host) | `DocsEnabled=false` in `values-staging.yaml` |
| **Production** | Not exposed | `DocsEnabled=false` in `values-prod.yaml` |

The Swagger UI uses a BCBP-branded dark theme (`swagger_theme.go`) and a custom initializer (`swagger_initializer.go`) that pre-fills the production host.

### Regenerating the spec

```bash
make swag         # regenerates docs/swagger/ from @annotations in handlers.go + swagger_info.go
```

The generated files (`docs/swagger/docs.go`, `swagger.json`, `swagger.yaml`) are committed to the repository. The CI step `check-swagger-stale.sh` fails a PR whose annotations diverge from the committed spec — always run `make swag` before pushing annotation changes.

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

All timeout and rate-limit variables (`RP_TIMEOUT_MS`, `I1_TIMEOUT_MS`, `ELIGIBILITY_TIMEOUT_MS`, `REQUEST_DEADLINE_MS`, `RATE_*`) are optional with production-safe defaults. See `.env-example` for the full list.

The service **panics at startup** if any required env var is missing or invalid (e.g., `PBKDF2_ITERATIONS < 210000`, `SBFF_HMAC_KEY < 32 bytes`). `CAPTCHA_SKIP=true` is rejected by `config.Validate()` when `APP_ENV` is not `dev`, `test`, or `local`.

---

## Testing

| Layer | Command | Docker |
|---|---|---|
| Unit (no external deps) | `make test-unit` | No |
| Integration (real Valkey) | `make test-integration` | Yes |
| Contract (RP-1, I-1 shapes) | `make test-unit` (`test/unit/service/contract_test.go`) | No |
| All + race detector + coverage | `make test-ci` | Yes |
| Load (k6) | `make test-load` | No |

CI enforces **≥ 95% statement coverage** (unit + integration merged). Coverage threshold in `.github/scripts/coverage-gate.sh`.

### Additional quality gates

| Gate | Command | What it checks |
|---|---|---|
| Architecture lint | `make arch-lint` | Layer boundaries (domain ← port ← service ← adapter ← cmd) |
| Metric naming | `make metrics-lint` | Tier classification, `_total`/`_seconds` suffixes, adapter instrumentation, label vocabulary |
| Observability sink | `make log-sink-lint` | All logs/metrics/traces go through platform-gincommon only |
| Vuln check | `make vuln-check` | Known CVEs in call paths (govulncheck) |
| Swagger staleness | CI only | `docs/swagger/` matches `@annotations` in handlers |

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

**Go-live prerequisites** (see `docs/lld/signup-bff-action-tracker.md`):
- SRE: mesh trust domain confirmed, SNS filter updated, `signup.bcbpsolutions.com` provisioned
- TL: sign-off on LLD v0.6 open questions A-1…A-4
- Web/Growth lead: on-call rotation named

The service code, deployment configuration, and CI pipeline are **production-ready**. The Phase 1 trial signup path is fully implemented, tested (≥ 95% coverage), and hardened. Phase 1.5 (paid signup) remains a stub — `POST /api/v1/signup/paid` returns `404` until `PAID_SIGNUP_ENABLED=true` and a real Billing adapter replaces `buildBilling()` in `wiring.go`.

---

## Related documents

| Document | Purpose |
|---|---|
| [`docs/lld/signup-bff-lld.md`](docs/lld/signup-bff-lld.md) | Full LLD v0.6 — the runtime contract in detail |
| [`docs/lld/signup-bff-open-questions-tl.md`](docs/lld/signup-bff-open-questions-tl.md) | 4 TL decisions needed before sign-off |
| [`docs/lld/signup-bff-action-tracker.md`](docs/lld/signup-bff-action-tracker.md) | Team assignments + go-live blockers |
| [`ARCHITECTURE.md`](ARCHITECTURE.md) | Layer model, sequence diagrams, state machine |
| [`CHANGELOG.md`](CHANGELOG.md) | Version history |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Dev setup, testing, PR process |
| [`VERSIONING.md`](VERSIONING.md) | Versioning policy and release process |
