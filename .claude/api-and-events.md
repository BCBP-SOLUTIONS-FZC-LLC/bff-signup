# API and Events

## Public Endpoints (LLD §5.2)

Base: `https://signup.bcbpsolutions.com/api/v1/signup`

| # | Method + Path | Auth | Purpose |
|---|---|---|---|
| SB-1 | `GET /session` | None | Start session: sets cookie, returns CSRF token + plans + rules |
| SB-2 | `POST /trial` | Cookie + CSRF | Submit trial form; runs full provisioning handoff |
| SB-3 | `GET /{signup_id}/status` | Cookie (session-bound) | Poll provisioning status; resumes I-1 if tenant_pending |
| SB-4 | `POST /{signup_id}/slug` | Cookie + CSRF | Pick a new slug after 409 slug_taken |
| SB-5 | `POST /paid` | Cookie + CSRF | Direct paid signup (§19; Billing B-I1 → PUB-1 → RP-2 → I-1); returns 404 until `PAID_SIGNUP_ENABLED=true` |
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

SB-2 Responses: `201 completed`, `202 provisioning {poll_after_ms:2000}`, `409 SLUG_TAKEN {signup_id, suggestions[]}` (from I-1: SB-4; from the PUB-1 pre-check: resubmit SB-2 with a new slug), `409 INVALID_STATE {reason: details_changed}`.

## Outbound Contracts (internal mesh, mTLS)

IAM calls (RP-1, RP-2, I-1, eligibility) carry `x-tenant-id` (eligibility: nil UUID), `x-user-id: 00000000-0000-0000-0000-0000000000a1` (system principal), `x-tenant-roles: iam-system`, `traceparent`, `x-request-id`. PUB-1 carries only `x-user-id`. Billing B-I1 carries only `x-tenant-id` and `Idempotency-Key: charge:<tenant_id>`; caller identity is the mesh SPIFFE ID (BIL-AUTH-3).

| Step | Call | Key header | Path |
|---|---|---|---|
| E | `POST /api/v1/internal/trial-eligibility` {email_hash} → O&M | — (read-only) | Trial |
| P | `GET /api/v1/public/tenants/resolve?identifier=<slug>` → O&M (PUB-1, fail-open) | — | Both |
| 1 | `POST /api/v1/internal/tenants/provision-trial` → RP (RP-1) | `Idempotency-Key: signup:<tenant_id>:rp1` | Trial |
| 2 | `POST /api/v1/internal/tenants` → O&M (I-1) | — (idempotent on tenant_id) | Both |
| B | `POST /api/v1/internal/subscriptions/authorize-and-charge` → Billing (B-I1) {tenant_id, plan_code, licensed_seats, payment_method_ref} | `Idempotency-Key: charge:<tenant_id>` (+ `x-tenant-id` only) | Paid |
| RP2 | `POST /api/v1/internal/tenants/provision-direct` → RP (RP-2) (§19) | `Idempotency-Key: signup:<tenant_id>:rp2` | Paid |

**No Idempotency-Key on I-1** — O&M is idempotent by state on `tenant_id` (ON CONFLICT DO NOTHING).

## Error Codes (public wire shape)

RFC 9457 `application/problem+json`: `type`, `title`, `status`, `detail`, `traceId`, `errorCode` (UPPER_SNAKE), extension members at the top level.

| `errorCode` | HTTP | When |
|---|---|---|
| `VALIDATION_FAILED` | 400 | Field validation; SB-5 `PRICE_NOT_AVAILABLE` from Billing |
| `SESSION_EXPIRED` | 401 | Missing/expired cookie |
| `CSRF_INVALID` | 403 | CSRF mismatch or wrong Origin |
| `CAPTCHA_FAILED` | 403 | Turnstile rejected |
| `SIGNUP_NOT_FOUND` | 404 | Unknown signup_id or wrong session |
| `EMAIL_UNAVAILABLE` | 409 | Email already in Keycloak (RP-1 CR-1 / RP-2) |
| `TRIAL_NOT_ELIGIBLE` | 409 | Email already used a trial (O&M CR-4) |
| `SLUG_TAKEN` | 409 | Slug owned by another tenant (PUB-1 or I-1) |
| `INVALID_STATE` | 409 | Action invalid in current handoff state; request in flight (`retry_after_ms`); details changed (`reason: details_changed`); SB-5 payment in progress with other details |
| `CHARGE_EXPIRED` | 410 | SB-5: 3-D Secure not completed within 30 min |
| `PAYMENT_DECLINED` | 422 | SB-5: Billing declined (+ `decline_reason`) |
| `RATE_LIMITED` | 429 | Rate limit exceeded (+ Retry-After) |
| `INTERNAL_ERROR` | 500 | BFF bug, or IAM/Billing contract mismatch |
| `DEPENDENCY_UNAVAILABLE` | 503 | Valkey, Turnstile, eligibility, RP-1/RP-2 or Billing down (+ Retry-After when known) |

## Upstream RFC 9457 Error Codes (UPPER_SNAKE_CASE)

RP, O&M and Billing return RFC 9457 `application/problem+json`. Branch on `errorCode`, never on `detail`.

| Upstream | errorCode | BFF action |
|---|---|---|
| RP-1 409 | `EMAIL_ALREADY_REGISTERED` | Delete handoff → `409 email_unavailable` |
| RP-1 409 | `OPERATION_IN_PROGRESS` | Retry once with the same key |
| RP-2 409 | `OPERATION_IN_PROGRESS` | `409 INVALID_STATE` (handoff kept in `payment_confirmed`) |
| RP-1 400 | `INVALID_REQUEST` | Page — BFF bug |
| I-1 409 | `SLUG_ALREADY_TAKEN` | `slug_conflict` state → `409 SLUG_TAKEN` |
| Eligibility 409 | `TRIAL_ALREADY_USED` | Delete handoff → `409 TRIAL_NOT_ELIGIBLE` |
| B-I1 201 / 200 replay | — | `payment_confirmed` → RP-2 |
| B-I1 402 | `PAYMENT_ACTION_REQUIRED` | `payment_action_required` → `202` + 3-D Secure action |
| B-I1 402 | `PAYMENT_DECLINED` | Delete handoff → `422 PAYMENT_DECLINED` + `decline_reason` |
| B-I1 410 | `CHARGE_EXPIRED` | Delete handoff → `410 CHARGE_EXPIRED` |
| B-I1 409 | `SUBSCRIPTION_ALREADY_EXISTS` | Keep handoff → `409 INVALID_STATE` (never provision from it) |
| B-I1 409 / 500 / 502 / 503 / 504 / timeout | `REQUEST_IN_PROGRESS`, `PROVIDER_UNAVAILABLE`, `DB_UNAVAILABLE` | Keep handoff → `503` + `Retry-After` |
| B-I1 422 | `PRICE_NOT_AVAILABLE` | Delete handoff → `400 VALIDATION_FAILED` |
| B-I1 400 / 403 / other | `VALIDATION_ERROR`, `TENANT_MISMATCH`, `INSUFFICIENT_ROLE`, anything unexpected | Keep handoff → `500` (BFF bug or mesh config; a retry reuses the key) |

## Events (BFF publishes nothing)

The BFF is not an event producer or consumer (LLD §7). Events caused by its calls are emitted by RP and O&M:

| Event | Producer | Trigger |
|---|---|---|
| `TenantIdentityProvisioned` | Realm Provisioner | After RP-1 / RP-2 creates the Keycloak user |
| `TenantCreated`, `TrialStarted`, `TenantRoleGranted` | Org & Membership | After I-1 creates the tenant row |

`TenantIdentityProvisioned` drives the **O&M backstop** — if the BFF's I-1 call fails and the user abandons, O&M's `tenant-orgm-q` consumer creates the tenant from this event. SNS subscription filter for `tenant-orgm-q` must include `TenantIdentityProvisioned` (SRE action, BFF-2 SRE).

## SB-5 Request (LLD §19)

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

SB-5 responses:
- `201 {status:"completed", next:"check_email"}`
- `202 {status:"provisioning", poll_after_ms:2000}`
- `202 {status:"payment_action_required", action:{type:"3ds", client_secret}, expires_at}` — complete 3-D Secure, then re-POST the same body with a fresh `captcha_token`
- `409 SLUG_TAKEN` (+ suggestions; resubmit SB-5 with a new slug if paid but not provisioned, else SB-4) / `409 INVALID_STATE`
- `410 CHARGE_EXPIRED`, `422 PAYMENT_DECLINED` (+ `decline_reason`), `503` + `Retry-After` (resubmit the same body)

`payment_token` is the payment provider's browser token (`pm_…`), sent to Billing as `payment_method_ref` — no PAN ever reaches the BFF (HLD §10.1). Only its HMAC is stored. Returns `404` until `PAID_SIGNUP_ENABLED=true`.
