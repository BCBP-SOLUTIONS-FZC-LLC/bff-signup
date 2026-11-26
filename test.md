# bff-signup — API Test Guide

Curl-based test playbook for every signup endpoint. Each block includes:
- **Curl** — copy-paste ready
- **Response** — expected status + shape
- **Common errors** — status codes to expect on misuse

Server assumed at `http://localhost:8080`.

---

## Setup

```bash
BASE=http://localhost:8080
SESSION_ID=""   # populated after SB-1
CSRF_TOKEN=""   # populated after SB-1
SIGNUP_ID=""    # populated after SB-2
```

---

## SB-1 — GET /api/v1/signup/session (start signup session)

```bash
curl -si -X GET "$BASE/api/v1/signup/session" \
  -H "Origin: https://signup.bcbpsolutions.com"
```

**Expected 200:**
```json
{
  "signup_id": "<ulid>",
  "csrf_token": "<32-byte-hex>",
  "captcha": { "provider": "turnstile", "site_key": "..." },
  "plans": [
    { "code": "starter", "name": "Starter", "trial_days": 14 },
    { "code": "pro", "name": "Pro", "trial_days": 14 }
  ],
  "password_policy": { "min_length": 12, "max_length": 128, "require_classes": 3, "not_email": true },
  "slug_rules": { "pattern": "^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$", "min": 3, "max": 30 },
  "current_terms_version": "2026-09"
}
```

Save values:
```bash
SESSION_ID=$(curl -si "$BASE/api/v1/signup/session" | grep Set-Cookie | grep-oE 'sbff_sess=[^;]+')
CSRF_TOKEN=$(curl -s "$BASE/api/v1/signup/session" | jq -r '.csrf_token')
SIGNUP_ID=$(curl -s "$BASE/api/v1/signup/session" | jq -r '.signup_id')
```

---

## SB-2 — POST /api/v1/signup/trial (submit trial signup)

```bash
curl -si -X POST "$BASE/api/v1/signup/trial" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -H "Cookie: $SESSION_ID" \
  -d '{
    "company_name": "Acme Corp",
    "slug": "acme",
    "admin_name": "Alice Admin",
    "admin_email": "alice@acme.example.com",
    "password": "Acme$ecure123!",
    "plan": "starter",
    "accept_terms": true,
    "terms_version": "2026-09",
    "captcha_token": "test-token"
  }'
```

**Expected 201 (completed inline):**
```json
{ "signup_id": "<ulid>", "status": "completed", "email_verification_sent": true }
```

**Expected 202 (I-1 pending):**
```json
{ "signup_id": "<ulid>", "status": "tenant_pending", "poll_after_ms": 1000 }
```

**Expected 409 (slug taken):**
```json
{ "error": "slug_taken", "details": { "signup_id": "<ulid>", "suggestions": ["acme-1","acme-corp"] } }
```

**Common errors:**
| Status | Cause |
|---|---|
| 400 | Missing/invalid field, terms not accepted, wrong terms_version |
| 401 | Missing or expired session cookie |
| 403 | CSRF token missing or invalid |
| 409 | Email already used for trial (`trial_not_eligible`) or slug taken |
| 429 | Rate limited (3 signups/IP/hour or 5 attempts/email/day) |

---

## SB-3 — GET /api/v1/signup/{id}/status (poll provisioning)

```bash
curl -si -X GET "$BASE/api/v1/signup/$SIGNUP_ID/status" \
  -H "Cookie: $SESSION_ID"
```

**Expected responses:**
```json
{ "signup_id": "<ulid>", "status": "completed" }
{ "signup_id": "<ulid>", "status": "tenant_pending", "poll_after_ms": 1000 }
{ "signup_id": "<ulid>", "status": "slug_conflict", "action": "change_slug" }
```

---

## SB-4 — POST /api/v1/signup/{id}/slug (change slug after conflict)

```bash
curl -si -X POST "$BASE/api/v1/signup/$SIGNUP_ID/slug" \
  -H "Content-Type: application/json" \
  -H "X-CSRF-Token: $CSRF_TOKEN" \
  -H "Cookie: $SESSION_ID" \
  -d '{ "slug": "acme-corp" }'
```

**Expected 200:**
```json
{ "signup_id": "<ulid>", "status": "completed" }
```

---

## Infra probes

```bash
curl -si "$BASE/healthz"   # 200 OK — service alive
curl -si "$BASE/readyz"    # 200 OK — Valkey reachable; 503 — Valkey down
```

---

## Common error shapes

All errors are RFC 9457 problem details (`Content-Type: application/problem+json`), written by platform-gincommon. Error-specific fields (`signup_id`, `suggestions`, `retry_after_seconds`, `decline_reason`, …) are top-level members:
```json
{ "type": "…", "title": "Conflict", "status": 409, "detail": "…", "errorCode": "SLUG_TAKEN", "traceId": "…",
  "signup_id": "<ulid>", "suggestions": ["acme-2"] }
```

| `errorCode` | Meaning |
|---|---|
| `VALIDATION_FAILED` | Field validation error |
| `CAPTCHA_FAILED` | CAPTCHA verification failed |
| `TRIAL_NOT_ELIGIBLE` | Email already used for a trial |
| `SLUG_TAKEN` | Slug already taken by another tenant |
| `RATE_LIMITED` | Too many requests from this IP or email |
| `DEPENDENCY_UNAVAILABLE` | RP / O&M / Billing / Valkey temporarily unreachable |
| `REQUEST_TIMEOUT` | 503 written by gincommon when the request deadline expires |
| `INTERNAL_ERROR` | Unexpected error — check logs |

The full list is in README "Error codes" and LLD §17.1.
