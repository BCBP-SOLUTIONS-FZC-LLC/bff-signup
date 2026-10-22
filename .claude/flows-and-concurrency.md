# Flows and Concurrency

## Handoff State Machine (LLD §9.1)

```
[start] → allocated → identity_created → completed
                   ↘ rejected         ↗ tenant_pending → completed
                                      ↘ failed
                   identity_created → slug_conflict → identity_created (SB-4)
```

| State | Keycloak user? | Tenant row? | Who moves it |
|---|---|---|---|
| `allocated` | No | No | SB-2 only (holds the password) |
| `identity_created` | Yes | No | SB-2 inline I-1 |
| `tenant_pending` | Yes | No | SB-3 polling; O&M backstop |
| `slug_conflict` | Yes | No | SB-4 |
| `completed` | Yes | Yes | — |
| `rejected` / `failed` | No / Yes | No | — |

**`failed` after 15 min** in `tenant_pending` — SB-3 stops polling, alerts fire. The backstop may still recover it.

## Happy Path (SB-2)

1. CAPTCHA (Turnstile, 300ms timeout, 1 retry)
2. Rate limits — IncrIP + IncrEmail (after CAPTCHA)
3. Validate all fields
4. Check for existing handoff (resubmit matrix below)
5. Hash password PBKDF2-SHA512 (~100–200ms CPU)
6. Allocate handoff, acquire distributed lock (SET NX PX 15000)
7. Persist handoff state=allocated to Valkey
8. Eligibility check → O&M (150ms, 1 retry on 5xx)
9. RP-1 → Realm Provisioner (900ms, 1 retry on 502/503/timeout/OPERATION_IN_PROGRESS)
10. Persist state=identity_created + keycloak_user_id
11. I-1 → O&M (300ms, 2 retries with 100ms/250ms backoff)
12a. Success → state=completed, return 201
12b. Slug conflict → state=slug_conflict, return 409 slug_taken
12c. Failure → state=tenant_pending, return 202

Total SB-2 budget: 2,500ms. SLO p99: 1.5s on no-retry path.

## Resubmit Matrix (G-1, LLD §5.4)

| Handoff state | Same details | Different details |
|---|---|---|
| none / `rejected` | New attempt | New attempt |
| `allocated` | Retry (same tenant_id, same RP-1 key; password needed again) | Discard handoff, new signup_id + tenant_id |
| `identity_created` / `tenant_pending` / `slug_conflict` | Resume (no new CAPTCHA/rate-limit hit) | `409 invalid_state details_changed` |
| `completed` | Return original 201 | `409 invalid_state details_changed` |

"Same details" = email HMAC + slug + company_name + plan all match.

## Concurrency Controls

**Distributed lock:** `sbff:lock:<signup_id>` SET NX PX 15000. A second concurrent SB-2, SB-3 or SB-4 for the same signup_id gets `409 invalid_state {retry_after_ms:500}`. Released via Lua script (atomic token check).

**Browser double-click:** one `signup_id` per session, lock prevents both from proceeding. The slower one is told to retry.

**RP-1 idempotency:** `Idempotency-Key: signup:<tenant_id>:rp1` is stable across retries (derived from tenant_id, not random). A Valkey blip does not lose the key.

**I-1 idempotency:** O&M uses `ON CONFLICT (tenant_id) DO NOTHING` → 200 on replay. No key needed.

**BFF retries vs backstop race:** both use the same `tenant_id`, I-1 is idempotent — they race safely. First writer wins, second does nothing.

## Resume Flow (SB-3, LLD §8.2)

```
Browser polls GET /{signup_id}/status every poll_after_ms (2s → 4s → 8s, cap 15s)
  → StatusService acquires lock
  → ResumeI1: calls callI1Once (500ms timeout, 0 in-poll retries)
  → Updates handoff state
  → Returns 200 {status: "provisioning"|"completed"|"action_required"|"failed"}
```

After 15 min (≈40 polls): state=failed, SB-3 returns `status:"failed"` with trace_id.

## Slug Conflict Flow (SB-4, LLD §8.3)

```
SB-2 or SB-3 gets 409 SLUG_ALREADY_TAKEN from I-1
→ handoff.state = slug_conflict, suggest 3 alternatives
→ Browser shows slug picker, user submits SB-4
→ StatusService.ChangeSlug: validates slug, increments slug_attempts (max 5)
→ Sets state=identity_created, slug=newSlug
→ Calls callI1Once with new slug (RP-1 NOT called again — trial realms don't use slug)
```

**Key:** the Keycloak user is not recreated — RP-1 doesn't use the slug for trial tenants.

## Rate Limits

| Counter | Key | Limit | Window |
|---|---|---|---|
| New signups per IP | `sbff:rl:ip:<ip>` | 3 | 1 hour |
| Attempts per email | `sbff:rl:email:<hmac(email)>` | 5 | 24 hours |
| Session starts per IP | `sbff:rl:sess:<ip>` | 20 | 1 hour |
| Status polls per session | `sbff:rl:poll:<sid>` | 60 | 1 minute |

Counters increment **after** CAPTCHA (bots stopped at CAPTCHA, real users don't lose attempts on CAPTCHA failure). Resume attempts (SB-3/SB-4 on existing handoff) never count.

## Fail-Closed Valkey Policy

Valkey unavailable → `503 dependency_unavailable` on SB-1/SB-2/SB-3/SB-4.

Rationale: without counters the rate limit is gone; without the handoff record SB-INV-3 (one tenant_id per attempt) cannot be guaranteed.
