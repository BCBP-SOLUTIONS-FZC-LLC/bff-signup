# API and Events

## Public Endpoints (LLD §5.2)

Base: `https://signup.bcbpsolutions.com/api/v1/signup`

| # | Method + Path | Auth | Purpose |
|---|---|---|---|
| SB-1 | `GET /session` | None | Start session: sets cookie, returns CSRF token + plans + rules |
| SB-2 | `POST /trial` | Cookie + CSRF | Submit trial form; runs full provisioning handoff |
| SB-3 | `GET /{signup_id}/status` | Cookie (session-bound) | Poll provisioning status; resumes I-1 if tenant_pending |
| SB-4 | `POST /{signup_id}/slug` | Cookie + CSRF | Pick a new slug after 409 slug_taken |
| SB-5 | `POST /paid` | Cookie + CSRF | Direct paid signup (Phase 1.5, §19); returns 404 until `PAID_SIGNUP_ENABLED=true` |
| SB-H | `GET /healthz`, `GET /readyz` | None | Liveness; readiness (Valkey ping) |
| SB-M | `GET /metrics` | None (monitoring ns only) | Prometheus metrics on port 9090 |

## SB-1 Response

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

## SB-2 Request

```json
{
  "company_name": "Acme Co", "slug": "acme",
  "admin_name": "Dana Owner", "admin_email": "owner@acme.example",
  "password": "••••••••••••", "plan": "pro",
  "accept_terms": true, "terms_version": "2026-09",
  "captcha_token": "0.xxxx"
}
```

SB-2 Responses: `201 completed`, `202 provisioning {poll_after_ms:2000}`, `409 slug_taken {signup_id, suggestions[]}`.

## Outbound Contracts (internal mesh, mTLS)

All internal calls carry: `x-tenant-id`, `x-user-id: 00000000-0000-0000-0000-0000000000a1` (system principal), `traceparent`, `x-request-id`.

| Step | Call | Key header | Path |
|---|---|---|---|
| E | `POST /api/v1/internal/trial-eligibility` {email_hash} → O&M | — (read-only) | Trial |
| 1 | `POST /api/v1/internal/tenants/provision-trial` → RP (RP-1) | `Idempotency-Key: signup:<tenant_id>:rp1` | Trial |
| 2 | `POST /api/v1/internal/tenants` → O&M (I-1) | — (idempotent on tenant_id) | Both |
| B | Billing `AuthorizeAndCharge` → `{billing_subscription_id}` (§19) | `Idempotency-Key: charge:<tenant_id>` | Paid |
| RP2 | `POST /api/v1/internal/tenants/provision-direct` → RP (RP-2) (§19) | `Idempotency-Key: signup:<tenant_id>:rp2` | Paid |

**No Idempotency-Key on I-1** — O&M is idempotent by state on `tenant_id` (ON CONFLICT DO NOTHING).

## Error Codes (public wire shape)

| `error` | HTTP | When |
|---|---|---|
| `validation_failed` | 400 | Field validation |
| `session_expired` | 401 | Missing/expired cookie |
| `csrf_invalid` | 403 | CSRF mismatch or wrong Origin |
| `captcha_failed` | 403 | Turnstile rejected |
| `signup_not_found` | 404 | Unknown signup_id or wrong session |
| `email_unavailable` | 409 | Email already in Keycloak (RP-1 CR-1) |
| `trial_not_eligible` | 409 | Email already used a trial (O&M CR-4) |
| `slug_taken` | 409 | Slug owned by another tenant (O&M I-1) |
| `invalid_state` | 409 | Action invalid in current handoff state |
| `rate_limited` | 429 | Rate limit exceeded (+ Retry-After) |
| `internal_error` | 500 | BFF bug or IAM contract mismatch |
| `dependency_unavailable` | 503 | Valkey, Turnstile, eligibility, or RP-1 down |

## Upstream RFC 9457 Error Codes (UPPER_SNAKE_CASE)

Both RP and O&M return RFC 9457 `application/problem+json`. Branch on `errorCode`, never on `detail`.

| Upstream | errorCode | BFF action |
|---|---|---|
| RP-1 409 | `EMAIL_ALREADY_REGISTERED` | Delete handoff → `409 email_unavailable` |
| RP-1 409 | `OPERATION_IN_PROGRESS` | Retry same key |
| RP-1 400 | `INVALID_REQUEST` | Page — BFF bug |
| I-1 409 | `SLUG_ALREADY_TAKEN` | `slug_conflict` state → `409 slug_taken` |
| Eligibility 409 | `TRIAL_ALREADY_USED` | Delete handoff → `409 trial_not_eligible` |

## Events (BFF publishes nothing)

The BFF is not an event producer or consumer (LLD §7). Events caused by its calls are emitted by RP and O&M:

| Event | Producer | Trigger |
|---|---|---|
| `TenantIdentityProvisioned` | Realm Provisioner | After RP-1 creates the Keycloak user |
| `TenantCreated`, `TrialStarted`, `TenantRoleGranted` | Org & Membership | After I-1 creates the tenant row |

`TenantIdentityProvisioned` drives the **O&M backstop** — if the BFF's I-1 call fails and the user abandons, O&M's `tenant-orgm-q` consumer creates the tenant from this event. SNS subscription filter for `tenant-orgm-q` must include `TenantIdentityProvisioned` (SRE action, BFF-2 SRE).

## SB-5 Request (Phase 1.5, LLD §19)

```json
{
  "company_name": "Acme Bidding Co", "slug": "acme",
  "admin_name": "Dana Owner", "admin_email": "owner@acme.example",
  "password": "••••••••••••", "plan": "pro",
  "seats": 5,
  "payment_token": "pm_stripe_token_from_browser",
  "accept_terms": true, "terms_version": "2026-09",
  "captcha_token": "0.xxxx"
}
```

SB-5 responses: `201 completed`, `202 provisioning {poll_after_ms:2000}`, `409 slug_taken {signup_id, suggestions[]}`.

`payment_token` is a Stripe payment-method reference tokenised in the browser — no PAN ever reaches the BFF (HLD §10.1). Returns `404` until `PAID_SIGNUP_ENABLED=true`.
