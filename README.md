# bff-signup

Public trial-signup BFF for the XpertPMS platform — the only internet-facing service that accepts unauthenticated requests.

**Repository:** `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`  
**Module:** Go 1.23 · private module · not yet deployed to any environment (see [`VERSIONING.md`](VERSIONING.md))

---

## Mental model

This service is the **public onboarding intake** for self-service trial signups. It enforces abuse controls (CAPTCHA, rate limiting, CSRF), validates all inputs, hashes the password into Keycloak's format, and orchestrates the provisioning handoff across two IAM services.

| Concern | Owner |
|---|---|
| Trial signup form API (SB-1…SB-4) | **This service** |
| Abuse controls (CAPTCHA, rate limits, CSRF, WAF) | **This service** + ALB |
| Password hashing (PBKDF2-SHA512) | **This service** — authoritative at signup |
| Keycloak user creation | Realm Provisioner (RP-1) |
| Tenant row creation, one-trial ledger | Org & Membership (I-1, eligibility) |
| Session, handoff, rate-limit counters | Ephemeral Valkey (no DB) |
| Login, tokens, MFA, email verification | Keycloak (built-in flows) |
| Billing, subscriptions | Billing service (Phase 1.5 — not built) |
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
│   │   └── service/     ← Session, Signup, HandoffOrchestrator, Status
│   └── adapter/
│       ├── inbound/http/   ← Gin router, handlers (SB-1…4), middleware
│       └── outbound/       ← Valkey, RP-1, O&M, Turnstile, Catalog, PBKDF2 hasher
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

| Variable | Dev value |
|---|---|
| `VALKEY_URL` | `redis://:localdev@localhost:6379/0` |
| `SBFF_HMAC_KEY` | Any 32+ byte string |
| `SBFF_TURNSTILE_SECRET` | `1x0000000000000000000000000000000AA` (always-pass test key) |
| `CAPTCHA_SITE_KEY` | `1x00000000000000000000AA` |
| `REALM_PROVISIONER_BASE_URL` | `http://localhost:9001` |
| `ORG_MEMBERSHIP_BASE_URL` | `http://localhost:9002` |

The service panics at startup if any required env var is missing or `PBKDF2_ITERATIONS < 210000`.

---

## Testing

| Layer | Command | Docker |
|---|---|---|
| Unit (no external deps) | `make test-unit` | No |
| Integration (real Valkey) | `make test-integration` | Yes |
| Contract (RP-1, I-1 shapes) | `make test-contract` | No |
| All + race detector + coverage | `make test-ci` | Yes |
| Load (k6) | `make test-load` | No |

CI enforces **≥ 80% statement coverage** (unit + integration merged). Coverage threshold in `.github/scripts/coverage-gate.sh`.

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

**Go-live prerequisites** (not yet met — see `docs/lld/signup-bff-action-tracker.md`):
- SRE: mesh trust domain confirmed, SNS filter updated, `signup.bcbpsolutions.com` provisioned
- TL: sign-off on LLD v0.6 open questions A-1…A-4
- Web/Growth lead: on-call rotation named

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
