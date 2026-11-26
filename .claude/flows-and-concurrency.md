# Flows and Concurrency

## Handoff State Machine (LLD §9.1)

Same diagram as LLD §9.1, ARCHITECTURE.md and `docs/architecture/mermaid/handoff-state-machine.mmd`.

```mermaid
stateDiagram-v2
    [*] --> allocated : SB-2 passes CAPTCHA, rate limit, validation · tenant_id allocated
    [*] --> payment_pending : SB-5 passes CAPTCHA, rate limit, validation · tenant_id allocated

    %% Trial path (SB-2)
    allocated --> allocated : eligibility / RP-1 5xx or timeout after one retry (503); PUB-1 slug taken (409, no Keycloak user; resubmit with a new slug)
    allocated --> identity_created : RP-1 201
    allocated --> [*] : eligibility 409 TRIAL_ALREADY_USED / RP-1 409 EMAIL_ALREADY_REGISTERED / resubmit with other details (handoff deleted)

    %% Paid path (SB-5, §19)
    payment_pending --> payment_pending : B-I1 timeout / 500-504 / REQUEST_IN_PROGRESS (503 + Retry-After), 409 SUBSCRIPTION_ALREADY_EXISTS or an unexpected reply (500)
    payment_pending --> payment_action_required : B-I1 402 PAYMENT_ACTION_REQUIRED (202 to browser)
    payment_pending --> payment_confirmed : B-I1 201 / 200 replay
    payment_action_required --> payment_action_required : re-POST; still pending, timeout or 5xx
    payment_action_required --> payment_confirmed : re-POST after 3-D Secure; B-I1 201
    payment_pending --> [*] : B-I1 402 PAYMENT_DECLINED / 410 CHARGE_EXPIRED / 422 PRICE_NOT_AVAILABLE (handoff deleted)
    payment_action_required --> [*] : B-I1 402 PAYMENT_DECLINED / 410 CHARGE_EXPIRED (handoff deleted)
    payment_confirmed --> payment_confirmed : PUB-1 / RP-2 slug taken (409) or RP-2 error; resubmit skips Billing (details frozen once RP-2 was called)
    payment_confirmed --> identity_created : RP-2 201

    %% Shared I-1 path
    identity_created --> completed : I-1 201/200
    identity_created --> tenant_pending : I-1 5xx/timeout after 2 inline retries
    identity_created --> slug_conflict : I-1 409 SLUG_ALREADY_TAKEN
    tenant_pending --> completed : SB-3 resume succeeds
    tenant_pending --> failed : 15 min exhausted
    slug_conflict --> identity_created : SB-4 new slug (max 5 attempts)

    completed --> [*] : TTL 10 min
    failed --> [*] : TTL 30 min
```

| State | Keycloak user? | Tenant row? | Who moves it | Path |
|---|---|---|---|---|
| `allocated` | No | No | SB-2 resubmit (same details retry; changed details start fresh) | Trial |
| `payment_pending` | No | No | SB-5 — Billing call in flight or outcome unknown (resubmit retries B-I1, same key) | Paid (§19) |
| `payment_action_required` | No | No | SB-5 re-POST after 3-D Secure | Paid (§19.3) |
| `payment_confirmed` | No | No | SB-5 resubmit — skips Billing, runs PUB-1 + RP-2 | Paid (§19.4) |
| `identity_created` | Yes | No | SB-2 after RP-1 / SB-5 after RP-2 | Both |
| `tenant_pending` | Yes | No | SB-3 polling; O&M backstop | Both |
| `slug_conflict` | Yes | No | SB-4 (only reached from I-1 409) | Both |
| `completed` | Yes | Yes | — | Both |
| `failed` | Yes | No | — (backstop) | Both |
| `rejected` | — | — | declared, never set (rejections delete the handoff) | — |

**`failed` after 15 min** in `tenant_pending` — SB-3 stops polling, alerts fire. The backstop may still recover it.

## Happy Path (SB-2)

0. Same-email resume probe: `identity_created`/`tenant_pending`/`slug_conflict` on this session → return current state (no CAPTCHA, no budget)
1. CAPTCHA (Turnstile, 300ms timeout, 0 retries — the token is single-use, LLD §9.3)
2. Rate limits — IncrIP + IncrEmail (after CAPTCHA)
3. Validate all fields
4. Check for existing handoff (resubmit matrix below)
5. Hash password PBKDF2-SHA512 (~100–200ms CPU)
6. Allocate handoff, acquire distributed lock (SET NX PX = config.HandoffLockTTL: longest deadline + 5 s, ≥ 15 s)
7. Persist handoff state=allocated to Valkey
8. Eligibility check → O&M (150ms, 1 retry on 5xx)
8b. PUB-1 slug pre-check (fail-open; taken → 409 SLUG_TAKEN, handoff stays `allocated`, browser resubmits with a new slug)
9. RP-1 → Realm Provisioner (2000ms, 1 retry on 502/503/timeout/OPERATION_IN_PROGRESS)
10. Persist state=identity_created + keycloak_user_id
11. I-1 → O&M (300ms, 2 retries with 100ms/250ms backoff)
12a. Success → state=completed, return 201
12b. Slug conflict → state=slug_conflict, return 409 slug_taken
12c. Failure → state=tenant_pending, return 202

Total SB-2 budget: 2,500ms. SLO p99: 1.5s on no-retry path.

## Paid Signup Flow (SB-5, LLD §19)

Route live only when `PAID_SIGNUP_ENABLED=true`. D-2 resolved: BFF owns RP-2 and I-1, Billing only charges. Request deadline `PAID_REQUEST_DEADLINE_MS` (30 s; B-I1 p99 is 12 s).

```
[repeat on same session, same email, identity exists] → report state (no CAPTCHA, no budget)
CAPTCHA → rate limit (RATE_*; skipped when resuming a payment) → validate (+ seats 1–1000, payment_token) → hash
→ lock; re-read handoff:
     none / not paid            → allocate (type=paid, payment_pending, new tenant_id)
     payment_pending | payment_action_required | payment_confirmed → resume
     past payment (identity_created … failed) → same email: report state; else 409 INVALID_STATE details_changed
→ B-I1 {tenant_id, plan_code, licensed_seats, payment_method_ref}, key charge:<tenant_id>   (skipped if payment_confirmed)
     ← 402 action required → payment_action_required, return 202 {action:{type:"3ds", client_secret}, expires_at}
     ← 201 / 200          → payment_confirmed
→ PUB-1 slug pre-check (fail-open; taken → 409 SLUG_TAKEN, handoff stays payment_confirmed)
→ RP-2 provision-direct {…, billing_subscription_id}, key signup:<tenant_id>:rp2   (error → handoff stays payment_confirmed)
→ identity_created → I-1 (same inline retries as trial) → completed (201) | tenant_pending (202) | slug_conflict (409, SB-4)
```

**Resume rules (§19.4):** on `payment_pending` / `payment_action_required` the email, plan, seats and payment method (`payment_ref_hmac`) must match, else `409 INVALID_STATE` and nothing goes to Billing; slug, name and locale may change. On `payment_confirmed`, plan and seats must match; email and slug may change. A resume needs CAPTCHA but uses no rate-limit budget.

**Billing outcomes:** unknown (timeout, 500/502/503/504, `REQUEST_IN_PROGRESS`) → handoff kept, `503` + `Retry-After`, resubmit repeats the same tenant_id, key and body. `409 SUBSCRIPTION_ALREADY_EXISTS` → handoff kept, `409 INVALID_STATE` (never provisioned from). `PAYMENT_DECLINED` (`422`), `CHARGE_EXPIRED` (`410`), `PRICE_NOT_AVAILABLE` (`400`) → handoff deleted, the next submit starts fresh. `400`/`403`/any other reply (`500`) → handoff kept (it may follow a charge).

**No eligibility check** on the paid path — eligibility (CR-4) is trial-only.

## Resubmit Matrix (G-1, LLD §5.4)

| Handoff state | Same details | Different details |
|---|---|---|
| none / `rejected` | New attempt | New attempt |
| `allocated` | Retry (same tenant_id, same RP-1 key; password needed again) | Discard handoff; keep the session's signup_id, new tenant_id |
| `identity_created` / `tenant_pending` / `slug_conflict` | Resume (no new CAPTCHA/rate-limit hit) | `409 invalid_state details_changed` |
| `completed` | Return original 201 | `409 invalid_state details_changed` |

"Same details" = email HMAC + slug + company_name + plan all match. SB-5 has its own rules (paid flow above).

## Concurrency Controls

**Distributed lock:** `sbff:lock:<signup_id>` SET NX PX `config.HandoffLockTTL()` (longest request deadline + 5 s, at least 15 s — 35 s with paid on, so it outlives an SB-5 waiting on Billing). A second concurrent SB-2, SB-3, SB-4 or SB-5 for the same signup_id gets `409 invalid_state {retry_after_ms:500}`. Released via Lua script (atomic token check).

**Browser double-click:** one `signup_id` per session, lock prevents both from proceeding. The slower one is told to retry.

**RP-1 idempotency:** `Idempotency-Key: signup:<tenant_id>:rp1` is stable across retries (derived from tenant_id, not random). A Valkey blip does not lose the key.

**Billing idempotency:** `Idempotency-Key: charge:<tenant_id>` — the same tenant_id is reused on every SB-5 retry and 3-D Secure re-POST, so Billing returns the stored outcome instead of charging twice.

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
SB-2, SB-3 or SB-5 gets 409 SLUG_ALREADY_TAKEN from I-1 (a PUB-1 pre-check conflict is different: no Keycloak user exists, so the browser resubmits SB-2/SB-5 with the new slug)
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
| New signups per IP (SB-2, SB-5) | `sbff:rl:ip:<ip>` | 3 | 1 hour |
| Attempts per email | `sbff:rl:email:<hmac(email)>` | 5 | 24 hours |
| Session starts per IP | `sbff:rl:sess:<ip>` | 20 | 1 hour |
| Status polls per session | `sbff:rl:poll:<sid>` | 60 | 1 minute |

Counters increment **after** CAPTCHA (bots stopped at CAPTCHA, real users don't lose attempts on CAPTCHA failure). Resume attempts (SB-3/SB-4 on an existing handoff, same-email resubmits, SB-5 payment resumes) never count. Limits come from `RATE_*` env vars for both SB-2 and SB-5.

## Fail-Closed Valkey Policy

Valkey unavailable → `503 DEPENDENCY_UNAVAILABLE` on SB-1…SB-5.

Rationale: without counters the rate limit is gone; without the handoff record SB-INV-3 (one tenant_id per attempt) cannot be guaranteed.
