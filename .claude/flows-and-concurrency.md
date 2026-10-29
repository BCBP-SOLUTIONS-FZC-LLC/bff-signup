# Flows and Concurrency

## Handoff State Machine (LLD §9.1)

Trial path (SB-2):
```
[start] → allocated → identity_created → completed
                   ↘ rejected         ↗ tenant_pending → completed
                                      ↘ failed
                   identity_created → slug_conflict → identity_created (SB-4)
```

Paid path (SB-5, Phase 1.5 §19):
```
[start] → allocated → payment_pending → payment_confirmed → identity_created → completed
                                                          ↘ slug_conflict (PUB-1)
                   identity_created → tenant_pending → completed / failed (same as trial)
```

| State | Keycloak user? | Tenant row? | Who moves it | Path |
|---|---|---|---|---|
| `allocated` | No | No | SB-2 or SB-5 | Both |
| `payment_pending` | No | No | SB-5 — Billing call in flight | Paid (§19) |
| `payment_confirmed` | No | No | SB-5 — `billing_subscription_id` set | Paid (§19) |
| `identity_created` | Yes | No | SB-2 post RP-1 / SB-5 post RP-2 | Both |
| `tenant_pending` | Yes | No | SB-3 polling; O&M backstop | Both |
| `slug_conflict` | Yes | No | SB-4 | Both |
| `completed` | Yes | Yes | — | Both |
| `rejected` / `failed` | No / Yes | No | — | Both |

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

## Paid Signup Flow (SB-5, Phase 1.5 §19)

Active only when `PAID_SIGNUP_ENABLED=true`. D-2 resolved: BFF owns RP-2 and I-1, Billing only charges.

1. CAPTCHA (same guard as trial, SB-INV-5)
2. Rate limits — IncrIP + IncrEmail (same counters as trial)
3. Validate all fields (+ `seats ≥ 1`, `payment_token` non-empty)
4. Hash password PBKDF2-SHA512 (SB-INV-2)
5. Allocate handoff (type=`paid`, state=`payment_pending`)
6. Billing.AuthorizeAndCharge `{tenant_id, payment_token, plan, seats}` → `billing_subscription_id`
   (idempotency key: `charge:<tenant_id>`)
7. Handoff state = `payment_confirmed`
8. PUB-1 slug pre-check (fail-open, same as trial)
9. RP-2 provision-direct `{tenant_id, plan, billing_subscription_id, admin credentials}`
   → `keycloak_user_id` (Idempotency-Key: `signup:<tenant_id>:rp2`)
10. Handoff state = `identity_created`
11. I-1 → O&M (same inline retry logic as trial)
12a. Success → state=`completed`, return `201`
12b. Slug conflict → state=`slug_conflict`, return `409 slug_taken`
12c. Failure → state=`tenant_pending`, return `202` (SB-3 polling, same as trial)

**No eligibility check** on the paid path — eligibility (CR-4) is trial-only.
**Billing failure** → handoff deleted; user can retry. No charge if Billing errors.

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
