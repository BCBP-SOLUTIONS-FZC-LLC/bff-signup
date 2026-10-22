# Signup BFF Service — Low-Level Design

## Tender Management SaaS Platform — Edge / BFF Tier

| Field | Value |
|---|---|
| Document type | Low-Level Design (LLD) |
| Service | `bff-signup` (Signup Service / "Signup BFF") |
| Repository / Go module | `BCBP-SOLUTIONS-FZC-LLC/bff-signup` → `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup` (BFF-6, decided) |
| Status | **Ready for sign-off** — all TL decisions applied (BFF-1…BFF-6 repo, C-1…C-7, TL responses 21 Oct 2026); remaining action items in §16.2 (IAM and SRE blockers listed) |
| Version | 0.6 |
| Date | 2026-10-22 |
| Base HLD | `bff-hld-tender-saas-v1.0.md` (Edge / BFF Tier HLD v1.0) + *Signup BFF — TL Responses: HLD Gaps (BFF-1 to BFF-6)*, 21 Oct 2026 |
| Scope | **Phase 1 — trial signup only** (BFF HLD §7.1, §15). Direct-paid signup (Phase 1.5) is reserved, not specified (§19) |
| Sibling LLDs | `iam-lld-realm-provisioner-service.md` **rev 1.31** (RP-1 callee), `iam-lld-org-membership-service.md` **rev 2.30** (I-1 callee) |
| Owner database | **None.** Ephemeral Valkey only (§4) |
| Namespace | `bff` (never `iam`) — service account `bff-signup` (BFF-1, decided) |
| Owning team | Web / growth |
| Deployment stage | Not started — nothing deployed to any environment |
| Audience | Web / growth engineering (owner), IAM platform engineering (RP-1 / I-1 owners), IAM platform security (mesh trust), SRE |

### Revision history

| Version | Date | Changes |
|---|---|---|
| 0.6 | 2026-10-22 | **Pre-coding final pass.** Sibling LLD versions updated: RP rev 1.29 → 1.31, O&M rev 2.28 → 2.30. §5.6 eligibility response corrected — not-eligible is `409 errorCode: TRIAL_ALREADY_USED` (RFC 9457), not `{eligible:false}`. §17 intro updated: O&M now also returns RFC 9457 (rev 2.29+). §17.2 upstream error codes updated to `UPPER_SNAKE_CASE` RFC 9457 format for both RP and O&M. §8.1 G-3 note updated — N-12 resolved (I-8 → 404 → AuthZ → 403). N-12 closed in §16.2. §20.2 references updated. Pending TL decisions (A-1 to A-4) tracked in `signup-bff-open-questions-tl.md`. |
| 0.5 | 2026-10-22 | **IAM implementation verified; stale references cleared.** CR-1 (email_already_registered), CR-2 (hashed_password_seed + realm hash policy), CR-3 (RP bffCaller AuthorizationPolicy + NetworkPolicy; O&M bff added to existingCallerNamespaces), CR-4 (trial_signup_ledger + POST /internal/trial-eligibility), F-2 (BFF removed from RP-3/RP-11 docs) — all confirmed done in code. §5.6 eligibility and RP-1 status updated from "proposed" to "done". §8.4 "(C-3, proposed)" corrected. §10.1 current-state table replaced with resolved state. F-1 and F-2 moved from §16.2 open to resolved. §16.3 CRs marked done. §18.1 and §18.2 updated. New open item TL-1 added (§16.2): O&M used namespace-level admission (`bff` in existingCallerNamespaces) vs the LLD's pod-level requirement — TL decision needed. |
| 0.4 | 2026-10-22 | **All TL decisions confirmed and cross-checked against TL responses (21 Oct 2026).** Status updated from "Draft for TL review" to "Ready for sign-off". BFF-6 repo decision added to the status line (was applied in v0.3 but omitted from the summary). §16.2 open-items register verified accurate — all remaining items are IAM/SRE action items, not unresolved design decisions. No substantive content changes; all gap resolutions were correctly applied in v0.3. |
| 0.3 | 2026-10-21 | **TL decisions applied.** Namespace `bff`, service account and repo `bff-signup` (BFF-1, BFF-6). Backstop switched to O&M's `TenantIdentityProvisioned` consumer, now built (RP #19/#20, O&M #25, Audit Log #5); `TrialTenantProvisioned` retired (BFF-2). Turnstile, `signup.bcbpsolutions.com`, Billing deferred confirmed (BFF-3…5). RP-1 `plan` validation adopted: BFF sends only `starter`/`pro`/`enterprise` and identical `plan`/`slug`/`name` to RP-1 and I-1 (new SB-INV-8, §18.4). RP-1 errors now RFC 9457 (`status` + `errorCode`). Review fixes G-1…G-10: details-changed resubmit, rate limit counted after CAPTCHA, early-verification UX, BFF password check authoritative, Envoy namespace `envoy-gateway-system`, Turnstile `ServiceEntry`, ElastiCache Valkey, current error envelope, CPU-only HPA, sign-off table (§21). |
| 0.2 | 2026-10-16 | **C-1…C-7 resolved.** C-1, C-2, C-5, C-6 resolved from the reference LLDs (RP v1.29, O&M rev 2.28); C-3, C-4, C-7 resolved to the recommended option as change requests to IAM (§16.1). I-1 no longer sends an `Idempotency-Key` (O&M is idempotent by state, O&M §9.2). BFF password check made authoritative under C-4. Slug rule confirmed as a subset of O&M's DNS-label rule. |
| 0.1 | 2026-10-16 | Initial draft. Built on the HLD, the RP-1 contract (RP LLD §5.3–§5.4, §8.0–§8.1, §9.2, §17, §18.1) and the I-1 contract (O&M LLD §5.3, I-1 spec, §8.1). Open items BFF-1…6, C-1…7, N-1…11 recorded in §16 with working assumptions. Checked against RP LLD v1.29 and O&M LLD rev 2.28: mesh is Istio (trust domain `cluster.local`); RP already admits the `gateway` namespace (RP v1.27) but O&M does not (new finding F-1, §10.1); RP lists the BFF as a caller of RP-3/RP-11 (F-2). |

---

## Table of Contents

1. Document Overview
2. Service Responsibilities and Boundaries
3. Architecture and Package Layout
4. Data Model (Ephemeral State)
5. API Contract
6. Caching Design
7. Event Architecture
8. Key Request Flows
9. Concurrency, Consistency, and Failure Handling
10. Security
11. Observability
12. Configuration
13. Deployment and Scaling
14. Testing Strategy
15. Data Lifecycle, Privacy, and DR
16. Open Questions Register
17. Appendix — Error Taxonomy
18. Integration Details
19. Phase 1.5 Reservation — Direct-Paid Signup
20. Appendix — Glossary and References
21. Review and Sign-off

---

## 1. Document Overview

The Signup BFF is the platform's **public, unauthenticated tenant-onboarding intake**. It serves the signup form's API, enforces abuse controls (rate limit, CAPTCHA), and runs the **provisioning handoff**: it calls Realm Provisioner **RP-1** to create the owner in Keycloak, then Org & Membership **I-1** to create the tenant. It owns no business data, makes no Keycloak Admin API call, issues no token, and produces or consumes no bus event (BFF HLD §3.2, §9).

This LLD turns the HLD into a buildable specification: endpoints, payloads, Valkey keys, the handoff state machine, retry and timeout rules, security controls, metrics, configuration, and tests.

### 1.1 Relationship to the HLD

| HLD section | Where it is specified here |
|---|---|
| §4 Architecture | §3 package layout, §13 Kubernetes resources |
| §6 Statelessness | §4 Valkey key catalogue |
| §7.1 Trial signup | §5 SB-1…SB-4, §8.1 |
| §7.3 Two-call consistency | §8.2–§8.3, §9 handoff state machine |
| §8 Integration contracts | §5.6 outbound contracts, §18 |
| §10 Security | §10 |
| §11 Observability | §11 |
| §16 Open decisions | §16 (with C-1…7 and N-1…11 added) |

### 1.2 Corrections to the HLD carried in this LLD

These follow the IAM LLDs, which are code-verified, and are proposed for HLD v1.1:

| HLD statement | This LLD | Gap |
|---|---|---|
| §7.1 step 7: I-1 body `{id, plan:"trial", trial_ends_at, …}` | `{tenant_id, slug, name, plan: starter\|pro\|enterprise, owner_user_id, default_locale}`; O&M computes `trial_ends_at` | C-1 |
| §7.1 step 3: CAPTCHA only on duplicate email | CAPTCHA on **every** submission (duplicates are only known after RP-1) | BFF-3 |
| §6: "password-reset-style probing counters" | Duplicate-email / per-email attempt counter; password reset is Keycloak's | — |
| §4.2: two calls into `iam` | Three — eligibility check before RP-1 is now built (CR-4) | C-7 → CR-4 |

---

## 2. Service Responsibilities and Boundaries

### 2.1 In scope

- Public JSON API for the trial signup form (SB-1…SB-4, §5).
- Server-side validation of company, admin, slug, plan and password-policy inputs.
- Abuse controls: per-IP and per-email rate limits, CAPTCHA verification, CSRF protection.
- Allocation of the caller-supplied `tenant_id` (UUIDv7) and the tenant slug.
- Hashing the password into the `hashed_password_seed` format RP-1 accepts (C-4).
- The provisioning handoff: eligibility check → RP-1 → I-1, with retry, resume and slug-conflict handling.
- Funnel metrics, traces and redacted logs.

### 2.2 Out of scope (owned elsewhere)

| Concern | Owner |
|---|---|
| Login, logout, tokens, sessions, MFA | Keycloak / Envoy |
| Email verification, **forgot password**, invitation acceptance | Keycloak (built-in flows) |
| Keycloak Admin API | Realm Provisioner only (RP-INV-1) |
| `tenants`, memberships, roles, trial end date | Org & Membership |
| One-trial-per-email ledger | Org & Membership (`trial_signup_ledger`) |
| Payment, subscriptions | Billing (Phase 1.5) |
| Signup page UI | Web frontend app (assumed — N-6) |

### 2.3 Core invariants

| # | Invariant |
|---|---|
| SB-INV-1 | **No durable business data.** Nothing about a tenant, user or payment is written to a store the BFF owns beyond the TTL-bounded Valkey keys in §4. |
| SB-INV-2 | **The plain password never leaves process memory.** It is not logged, not stored in Valkey, not sent to any service; only `hashed_password_seed` is sent to RP-1. |
| SB-INV-3 | **One `tenant_id` per signup attempt.** A `tenant_id` is allocated once per `signup_id` and reused for every retry, resume and slug change, so RP-1 and I-1 always agree and never duplicate. |
| SB-INV-4 | **RP-1 before I-1, always.** `owner_user_id` sent to I-1 is the `keycloak_user_id` RP-1 returned — never invented by the BFF (C-6). |
| SB-INV-5 | **CAPTCHA and rate limit run before any internal call or password hashing**, so a bot cannot reach IAM or burn CPU. |
| SB-INV-6 | **No Keycloak and no admin-plane access.** The BFF's mesh identity is allow-listed only for the internal routes in §5.6 (BFF-1). |
| SB-INV-7 | **Client-side checks are UX only**, with one exception: the password policy. Because the credential reaches Keycloak pre-hashed (C-4), the BFF's password check is the only one at signup (§10.6). For everything else RP-1 and I-1 remain authoritative; their rejection is final. |
| SB-INV-8 | **RP-1 and I-1 carry identical `plan`, slug and name.** O&M keeps the values from whichever creates the tenant first — the BFF's I-1 or the backstop driven by RP's `TenantIdentityProvisioned` — and ignores later differing values. `plan` is always `starter`, `pro` or `enterprise`, never `trial` (RP-1 rejects anything else). The only permitted difference is a slug changed through SB-4 after `409 slug_taken`, which is safe because RP's event with the original slug is permanently rejected by O&M (§9.4). |

---

## 3. Architecture and Package Layout

Hexagonal layout, matching the IAM services (ports in `core/port`, adapters in `adapter/`).

```mermaid
flowchart LR
    BROWSER["Browser<br/>(signup page)"] --> ENVOY["Envoy Gateway<br/>(envoy-gateway-system)"]
    ENVOY --> HTTP["inbound/http<br/>router · middleware · handlers"]
    HTTP --> SVC["core/service<br/>SignupService · HandoffOrchestrator"]
    SVC --> PORTS["core/port interfaces"]
    PORTS --> CAP["captcha/turnstile"]
    PORTS --> VK["valkey<br/>sessions · handoff · rate limits"]
    PORTS --> CAT["catalog<br/>plan list"]
    PORTS --> ELIG["orgmembership<br/>trial-eligibility (CR-4)"]
    PORTS --> RP["realmprovisioner<br/>RP-1"]
    PORTS --> OM["orgmembership<br/>I-1"]
```

```
bff-signup/
├── cmd/server/main.go                 # wiring, graceful shutdown
├── internal/
│   ├── config/                        # env + file config, validation, fail-fast
│   ├── core/
│   │   ├── domain/                    # pure types, no I/O
│   │   │   ├── signup.go              # SignupRequest, validation rules
│   │   │   ├── slug.go                # derive, normalize, validate, suggest (N-1)
│   │   │   ├── handoff.go             # Handoff record + state machine (§9.1)
│   │   │   ├── plan.go
│   │   │   └── errors.go              # domain error codes (§17)
│   │   ├── port/
│   │   │   ├── realm_provisioner.go   # ProvisionTrial(ctx, req, key) (RP-1)
│   │   │   ├── org_membership.go      # ProvisionTenant(ctx, req, key) (I-1)
│   │   │   ├── trial_eligibility.go   # CheckEligibility(ctx, emailHash) (CR-4)
│   │   │   ├── captcha.go             # CaptchaVerifier.Verify(ctx, token, ip)
│   │   │   ├── catalog.go             # Plans(ctx)
│   │   │   ├── handoff_store.go       # Get/Create/Update/Lock (Valkey)
│   │   │   ├── session_store.go
│   │   │   ├── rate_limiter.go
│   │   │   ├── password_hasher.go     # Hash(password) → seed (C-4)
│   │   │   ├── billing.go             # STUB — Phase 1.5 (§19)
│   │   │   └── clock.go, idgen.go
│   │   └── service/
│   │       ├── session_service.go     # SB-1
│   │       ├── signup_service.go      # SB-2 entry, validation, abuse checks
│   │       ├── handoff_orchestrator.go# eligibility → RP-1 → I-1, resume
│   │       └── status_service.go      # SB-3, SB-4
│   └── adapter/
│       ├── inbound/http/
│       │   ├── router.go
│       │   ├── handlers.go
│       │   ├── dto.go
│       │   └── middleware/            # session, csrf, clientip, bodylimit, nostore
│       └── outbound/
│           ├── realmprovisioner/      # HTTP client (Istio sidecar does mTLS)
│           ├── orgmembership/         # HTTP client (I-1 + eligibility)
│           ├── catalog/
│           ├── turnstile/
│           ├── valkey/
│           └── hasher/                # PBKDF2-SHA512
├── deploy/helm/
├── test/{integration,contract,e2e,load}/
└── .github/workflows/
```

### 3.1 Shared library dependencies

| Library | Used for | Notes |
|---|---|---|
| `platform-gincommon` | PanicRecovery, RequestID, Tracing, Metrics, Logging, `ErrorResponse` | **Not** `RequireAuth` / `ContextMiddleware` on public routes — no identity exists at signup. Use a `PublicMiddlewares` set (or compose the stack minus auth). |
| `platform-models` | Shared error/response types | — |
| `platform-events` | Not used (no bus participation) | — |
| `platform-pgcommon` | Not used (no database) | — |

### 3.2 Dependency rules (CI-enforced, `go-arch-lint`)

- `core/domain` imports nothing internal.
- `core/service` imports `domain` and `port` only.
- `adapter/*` imports `port` and `domain`; adapters never import each other.
- Only `cmd/server` wires adapters to ports.
- No package imports a Keycloak client library (enforces SB-INV-6).

---

## 4. Data Model (Ephemeral State)

There is **no database**. A dedicated **Amazon ElastiCache for Valkey** node (`cache.t3.micro`, HLD §13), not shared with the IAM cache, holds only the keys below. TLS and AUTH on, **no snapshots / backups**: losing it loses only in-flight signups and counters (§15).

### 4.1 Key catalogue

| Key | Type | Value | TTL | Written by |
|---|---|---|---|---|
| `sbff:sess:<sid>` | hash | `csrf_hash`, `signup_id`, `created_at` | 30 min (sliding) | SB-1 |
| `sbff:handoff:<signup_id>` | string (JSON) | Handoff record (§4.2) | 30 min, refreshed on each state change; 10 min after `completed` | SB-2, SB-3, SB-4 |
| `sbff:lock:<signup_id>` | string | owner token | 15 s (`SET NX PX`) | SB-2, SB-3, SB-4 |
| `sbff:rl:ip:<ip>` | counter | new signup attempts from this IP | 1 h | SB-2 |
| `sbff:rl:email:<hmac(email)>` | counter | attempts for this email | 24 h | SB-2 |
| `sbff:rl:sess:<ip>` | counter | sessions started | 1 h | SB-1 |
| `sbff:rl:poll:<sid>` | counter | status polls | 1 min | SB-3 |

`<sid>` is a 256-bit random session id. Email keys use **HMAC-SHA256** with a server secret (`SBFF_HMAC_KEY`, from OpenBao), so Valkey never holds a readable email.

### 4.2 Handoff record

```jsonc
{
  "signup_id":        "01JABCD...",          // ULID, bound to the session
  "tenant_id":        "0192f0c4-...-7...",   // UUIDv7, allocated once (SB-INV-3)
  "state":            "identity_created",    // §9.1
  "slug":             "acme",
  "company_name":     "Acme Bidding Co",
  "plan":             "pro",
  "default_locale":   "en-US",
  "admin_email_hmac": "b64...",              // never the email itself
  "keycloak_user_id": "5717...",             // set after RP-1
  "slug_attempts":    1,
  "i1_attempts":      2,
  "last_error":       "org_membership_unavailable",
  "created_at":       "2026-10-16T09:00:00Z",
  "updated_at":       "2026-10-16T09:00:02Z"
}
```

**Not stored:** password, `hashed_password_seed`, admin email, admin name, CAPTCHA token. Only what I-1 or SB-3/SB-4 needs is kept. Company name and slug are organisational data, held at most 30 minutes.

---

## 5. API Contract

### 5.1 Conventions

- Public routes under `/api/v1/signup`, served on `https://signup.bcbpsolutions.com`, same origin as the page (BFF-4, decided). Breaking changes ship under `/api/v2`.
- `application/json`, UTF-8, request body limit **16 KB**. Timestamps RFC 3339 UTC.
- Every response: `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Request-Id`.
- Errors use the `gincommon.ErrorResponse` shape: `{ error, status, trace_id, request_id, details? }` (§17).
- State-changing routes (`POST`) require the session cookie **and** `X-CSRF-Token` (§10.4).
- No JWT, no `x-user-id` / `x-tenant-id` accepted from the browser; any such inbound header is stripped by Envoy and ignored by the BFF.

### 5.2 Endpoint catalogue — public (inbound)

| # | Method + Path | Purpose | CSRF |
|---|---|---|---|
| SB-1 | `GET /api/v1/signup/session` | Start a signup session: sets cookie, returns CSRF token, CAPTCHA site key, plans, password and slug rules | — |
| SB-2 | `POST /api/v1/signup/trial` | Submit the trial form; runs abuse checks and the handoff. `201` done / `202` still finishing / `409` slug conflict | Yes |
| SB-3 | `GET /api/v1/signup/{signup_id}/status` | Poll after `202`; resumes an unfinished I-1 | — (session-bound) |
| SB-4 | `POST /api/v1/signup/{signup_id}/slug` | Pick a new slug after `409 slug_taken`; retries I-1 with the same `tenant_id` | Yes |
| SB-H | `GET /healthz`, `GET /readyz` | Liveness; readiness (Valkey + config) | — |
| SB-M | `GET /metrics` | Prometheus, separate port, monitoring namespace only | — |

### 5.3 SB-1 `GET /api/v1/signup/session`

Creates `sbff:sess:<sid>` and a fresh `signup_id`. Rate-limited to 20 sessions per IP per hour.

```jsonc
// Response 200
// Set-Cookie: sbff_sid=<sid>; Path=/api/v1/signup; HttpOnly; Secure; SameSite=Strict; Max-Age=1800
{
  "signup_id": "01JABCD...",
  "csrf_token": "b64url-32-bytes",
  "captcha": { "provider": "turnstile", "site_key": "0x4AAA..." },
  "plans": [
    { "code": "starter",    "name": "Starter",    "trial_days": 14 },
    { "code": "pro",        "name": "Pro",        "trial_days": 14 },
    { "code": "enterprise", "name": "Enterprise", "trial_days": 14 }
  ],
  "password_policy": { "min_length": 12, "max_length": 128, "require_classes": 3, "not_email": true },
  "slug_rules": { "pattern": "^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$", "min": 3, "max": 30 }
}
```

`trial_days` values are illustrative — they come from the plan catalogue (N-2). The `password_policy` must mirror the Keycloak `trial` realm policy (N-10).

### 5.4 SB-2 `POST /api/v1/signup/trial`

```jsonc
// Request (headers: Cookie sbff_sid, X-CSRF-Token)
{
  "company_name":   "Acme Bidding Co",
  "slug":           "acme",              // optional; derived from company_name if absent (N-1)
  "admin_name":     "Dana Owner",
  "admin_email":    "owner@acme.example",
  "password":       "••••••••••••",
  "plan":           "pro",
  "accept_terms":   true,
  "terms_version":  "2026-09",
  "captcha_token":  "0.xxxx"
}
```

**Validation (server-side, before any external call):**

| Field | Rule | Error detail |
|---|---|---|
| `company_name` | trimmed, 2–100 chars, no control chars | `company_name.invalid` |
| `slug` | if present: lowercased, matches `slug_rules`, no `--`, not reserved (§5.8); if absent: derived | `slug.invalid`, `slug.reserved` |
| `admin_name` | trimmed, 1–100 chars, no control chars | `admin_name.invalid` |
| `admin_email` | ≤ 254 chars, syntactically valid, domain lowercased | `admin_email.invalid` |
| `password` | `password_policy`; ≠ email; ≤ 128 chars | `password.policy` |
| `plan` | one of the catalogue codes (never `"trial"`) | `plan.invalid` |
| `accept_terms` | must be `true` | `terms.required` |
| `terms_version` | non-empty, known version | `terms.version` |
| `captcha_token` | non-empty | `captcha.required` |

`default_locale` is not a field: it is derived from `Accept-Language` (allow-listed), default `en-US`.

**Responses:**

```jsonc
// 201 Created — tenant fully provisioned
{ "signup_id": "01JABCD...", "status": "completed", "email_verification_sent": true, "next": "check_email" }

// 202 Accepted — Keycloak user created, tenant still being created (§8.2)
{ "signup_id": "01JABCD...", "status": "provisioning", "poll_after_ms": 2000 }

// 409 Conflict — slug taken; user must choose another (§8.3)
{ "error": "slug_taken", "status": 409, "trace_id": "…", "request_id": "…",
  "details": { "signup_id": "01JABCD...", "suggestions": ["acme-bidding", "acme-2", "acme-hq"] } }
```

Other errors: `400 validation_failed`, `401 session_expired`, `403 csrf_invalid`, `403 captcha_failed`, `409 email_unavailable`, `409 trial_not_eligible`, `429 rate_limited` (with `Retry-After`), `503 dependency_unavailable` (§17).

**Re-submit behaviour (G-1):** a second SB-2 in the same session reuses the session's `signup_id`. The BFF compares the submitted email HMAC, slug, company name and plan with the handoff:

| Handoff state | Same details | Different details |
|---|---|---|
| none / `rejected` | New attempt | New attempt |
| `allocated` (RP-1 not confirmed) | Retry with the **same** `tenant_id` and RP-1 key (password needed again) | **Discard** the handoff; issue a new `signup_id` and `tenant_id` (a new RP-1 key), so RP cannot replay a result made with the old email |
| `identity_created` / `tenant_pending` / `slug_conflict` | Resume (password ignored, no new CAPTCHA or rate-limit hit) | `409 invalid_state` `details.reason = "details_changed"`; the UI tells the user to check the original email or start a new session |
| `completed` | Return the original `201` | `409 invalid_state` (`details_changed`) |

Turnstile tokens are single-use: the frontend must fetch a fresh token before every SB-2 (including a retry after `503`).

### 5.5 SB-3 `GET /api/v1/signup/{signup_id}/status` and SB-4 `POST /api/v1/signup/{signup_id}/slug`

Both require the session whose `signup_id` matches the path; otherwise `404 signup_not_found` (no hint whether it exists). SB-3 is limited to 60 polls per minute per session.

```jsonc
// SB-3 Response 200
{ "signup_id": "01JABCD...", "status": "provisioning" | "completed" | "action_required" | "failed",
  "action": "choose_slug",            // only when status = action_required
  "poll_after_ms": 2000 }             // only when status = provisioning
```

If the handoff is in `tenant_pending`, SB-3 takes the lock and retries I-1 once before answering (§8.2).

```jsonc
// SB-4 Request (headers: Cookie, X-CSRF-Token)
{ "slug": "acme-bidding" }
// Responses: 201 completed (same body as SB-2) · 202 provisioning · 409 slug_taken (new suggestions)
//            409 invalid_state (handoff not in slug_conflict) · 400 validation_failed
```

SB-4 is allowed at most **5** slug attempts per signup (`slug_attempts`).

### 5.6 Outbound contracts (internal mesh, mTLS)

All internal calls carry:

| Header | Value | Source |
|---|---|---|
| `x-tenant-id` | the handoff's `tenant_id` | RP RLS-5 / O&M IAPI-3 (C-2) |
| `x-user-id` | `00000000-0000-0000-0000-0000000000a1` (system principal) | RP AUTH-5 / O&M IAPI-2 (C-2) |
| `Idempotency-Key` | `signup:<tenant_id>:rp1` — **RP-1 only**, stable across retries, ≤ 256 chars (RP §9.2, §10.3). **Not sent to O&M**: O&M has no idempotency ledger; I-1 is idempotent by state on `tenant_id` (O&M §9.2, I1-1) | C-2 |
| `traceparent`, `x-request-id` | propagated | platform tracing |

| Step | Call | Owner | Idempotency-Key | Status |
|---|---|---|---|---|
| E | `POST /api/v1/internal/trial-eligibility` | Org & Membership | — (read) | **Done (CR-4)** |
| 1 | `POST /api/v1/internal/tenants/provision-trial` (RP-1) | Realm Provisioner | `rp1` | Committed; `409 email_already_registered` done (CR-1) |
| 2 | `POST /api/v1/internal/tenants` (I-1) | Org & Membership | — (idempotent on `tenant_id`) | Committed |

**E — trial eligibility (done, CR-4):**

```jsonc
// Request
{ "email_hash": "b64(sha256(lower(email)))" }   // hash format must match O&M's trial_signup_ledger
// Response 200
// Response 200 — eligible
{ "eligible": true }
// Response 409 — not eligible (RFC 9457, O&M rev 2.30)
{ "status": 409, "errorCode": "TRIAL_ALREADY_USED", "detail": "this email has already been used for a trial" }
```

**1 — RP-1** (RP LLD §5.4; body unchanged):

```jsonc
// Request
{ "tenant_id": "0192f0c4-...", "tenant_name": "Acme Bidding Co", "tenant_slug": "acme",
  "admin_email": "owner@acme.example", "admin_name": "Dana Owner",
  "hashed_password_seed": "<see §10.6 — C-4>", "plan": "pro" }
// Response 201
{ "tenant_id": "0192f0c4-...", "realm": "trial", "strategy": "shared",
  "keycloak_user_id": "5717...", "email_verification_sent": true }
```

**2 — I-1** (O&M LLD I-1 spec — the code-verified shape, C-1):

```jsonc
// Request
{ "tenant_id": "0192f0c4-...", "slug": "acme", "name": "Acme Bidding Co",
  "plan": "pro", "owner_user_id": "5717...", "default_locale": "en-US" }
// Response 201 (created) or 200 (idempotent replay)
{ "tenant_id": "0192f0c4-..." }
```

**CAPTCHA** — Cloudflare Turnstile `siteverify` (BFF-3, decided): `{secret, response: captcha_token, remoteip, idempotency_key}` → `success`, `hostname`, `action`, `error-codes`. The BFF also checks `hostname` equals the signup host.

**Plan catalogue** — Catalog Service plan list (assumed, N-2), cached (§6).

### 5.7 Status codes (public)

| Code | Meaning in this service |
|---|---|
| `200` | SB-1 session, SB-3 status |
| `201` | Signup completed |
| `202` | Keycloak user created, tenant still being created; poll SB-3 |
| `400` | `validation_failed` |
| `401` | `session_expired` (no/expired cookie) |
| `403` | `csrf_invalid`, `captcha_failed` |
| `404` | `signup_not_found` |
| `409` | `email_unavailable`, `trial_not_eligible`, `slug_taken`, `invalid_state` |
| `429` | `rate_limited` (+ `Retry-After`) |
| `500` | `internal_error` (our bug, or an unexpected 4xx from IAM) |
| `503` | `dependency_unavailable` (Valkey, CAPTCHA provider, or RP-1 down after retries) |

### 5.8 Slug rules (assumed — N-1)

- **Derive** (when `slug` absent): lowercase → ASCII-fold → non-`[a-z0-9]` runs become `-` → trim `-` → truncate to 30 → if < 3 chars, append `-` + 4 random `[a-z0-9]`.
- **Validate:** `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`, no `--`. This is a strict subset of O&M's rule (`^[a-z0-9]([a-z0-9-]{1,61}[a-z0-9])?$`, 3–63, O&M §10.3), so anything the BFF accepts O&M also accepts; 30 keeps `tenant-<slug>` short.
- **Reserved** (config list): `admin`, `api`, `app`, `auth`, `billing`, `help`, `login`, `signup`, `status`, `support`, `trial`, `www`, `mail`, `static`, `cdn`, `internal`, `system`, `root`, `tenant`, `keycloak`.
- **Suggestions** on conflict: `<slug>-<word from company name>`, `<slug>-<n>`, `<slug>-<4 random>`; up to 3, each re-validated.
- The slug becomes the dedicated realm name `tenant-<slug>` at conversion (RP §5.4.1), and it is immutable in O&M (T-1), so it is validated strictly here.

---

## 6. Caching Design

| Cache | Where | TTL | Invalidation | On failure |
|---|---|---|---|---|
| Plan catalogue | In-process (per pod) | 5 min, single-flight refresh | Time-based only | Serve last good copy; if none, fall back to the static `plans` list in config (§12) |
| Turnstile site key / policy config | Config | Deploy-time | Redeploy | — |

Valkey (§4) is a **state store**, not a cache: sessions, handoffs, counters and locks. Valkey unavailable ⇒ SB-1/SB-2/SB-3/SB-4 return `503 dependency_unavailable` (**fail-closed**, N-7). Rationale: without counters the rate limit is gone, and without the handoff record SB-INV-3 cannot be guaranteed.

---

## 7. Event Architecture

**None.** The Signup BFF publishes nothing and subscribes to nothing (BFF HLD §9). Events caused by its calls are emitted by others:

| Event | Producer | Caused by | Notes |
|---|---|---|---|
| `TenantIdentityProvisioned` | Realm Provisioner (outbox) | RP-1 (and RP-2 in Phase 1.5), after the admin's Keycloak user exists | Payload used by O&M: `tenant_id`, `owner_user_id`, `slug`, `name`, `plan`. Replaces the retired `TrialTenantProvisioned` (RP #19/#20) |
| `TenantCreated`, `TrialStarted` | Org & Membership | I-1 (BFF call or backstop) | — |
| `TenantRoleGranted` (owner) | Org & Membership | I-1 | — |

O&M's `tenant-orgm-q` consumer of `TenantIdentityProvisioned` is the **backstop** (BFF-2, built in O&M #25): if no tenant row exists it runs I-1 from the payload; if a row exists it does nothing. This is why the BFF needs no reconciliation job (§9.4). **SRE dependency:** the production SNS filter for `tenant-orgm-q` must include `TenantIdentityProvisioned`.

---

## 8. Key Request Flows

### 8.1 Trial signup — happy path

```mermaid
sequenceDiagram
    autonumber
    actor B as Browser
    participant E as Envoy (public)
    participant S as Signup BFF
    participant V as Valkey
    participant T as Turnstile
    participant OM as Org & Membership
    participant RP as Realm Provisioner

    B->>E: GET /api/v1/signup/session
    E->>S: forward
    S->>V: SET sbff:sess:<sid> {csrf_hash, signup_id} EX 1800
    S-->>B: 200 {signup_id, csrf_token, captcha, plans, rules} + Set-Cookie

    B->>E: POST /api/v1/signup/trial (cookie, X-CSRF-Token, form, captcha_token)
    E->>S: forward (x-envoy-external-address = client IP)
    Note over S: session + CSRF check · body validation
    S->>T: siteverify(token, remoteip)
    T-->>S: success
    S->>V: INCR sbff:rl:ip:<ip>, INCR sbff:rl:email:<hmac> (after CAPTCHA, G-2)
    S->>V: SET NX sbff:lock:<signup_id> PX 15000
    S->>V: create handoff {tenant_id=UUIDv7, state=allocated}
    S->>OM: POST /internal/trial-eligibility {email_hash}  (CR-4, done)
    OM-->>S: {eligible:true}
    Note over S: PBKDF2-SHA512 → hashed_password_seed (in memory only)
    S->>RP: RP-1 provision-trial + x-tenant-id, x-user-id=…00a1, Idempotency-Key signup:<tid>:rp1
    RP-->>S: 201 {keycloak_user_id, email_verification_sent:true}
    S->>V: handoff.state=identity_created, keycloak_user_id
    S->>OM: I-1 POST /internal/tenants {tenant_id, slug, name, plan, owner_user_id, default_locale}
    OM-->>S: 201 {tenant_id}
    S->>V: handoff.state=completed (TTL 10 min) · DEL lock
    S-->>B: 201 {status:"completed", next:"check_email"}
    Note over B: Email verification, MFA, onboarding → Keycloak + app (BFF out of the flow)
```

### 8.2 I-1 fails after RP-1 succeeds — `202` and resume

```mermaid
sequenceDiagram
    autonumber
    actor B as Browser
    participant S as Signup BFF
    participant V as Valkey
    participant OM as Org & Membership

    Note over S: RP-1 already returned 201 · handoff.state=identity_created
    loop up to 3 attempts within the request deadline (§9.3)
        S->>OM: I-1 (same tenant_id — no Idempotency-Key, idempotent by state)
        OM-->>S: 503 / timeout
    end
    S->>V: handoff.state=tenant_pending, i1_attempts=3
    S-->>B: 202 {status:"provisioning", poll_after_ms:2000}
    loop every poll_after_ms (backoff 2s → 4s → 8s, cap 15s)
        B->>S: GET /api/v1/signup/{signup_id}/status
        S->>V: SET NX lock
        S->>OM: I-1 retry (same tenant_id)
        alt 201 / 200
            S->>V: state=completed
            S-->>B: 200 {status:"completed"}
        else still failing
            S-->>B: 200 {status:"provisioning"}
        end
    end
    Note over S,OM: If the browser leaves: handoff expires (30 min).<br/>Backstop = O&M creates the tenant from TenantIdentityProvisioned (BFF-2).
```

**Early verification (G-3).** RP-1 sends the verification email before I-1 completes. While the handoff is `tenant_pending`, the SB-3 response carries `"message": "workspace_setting_up"` so the page can say *"Your workspace is still being set up — you can verify your email, but wait for this page before signing in."* If the user signs in before the tenant row exists, AuthZ Enrichment finds no membership (O&M I-8 invariant I8-3: returns `404` → AuthZ → `403` to the browser — hard fail, no grace period). This window normally closes within seconds via the BFF retry or the backstop. The BFF's `tenant_pending` warning message is sufficient UX coverage (N-12 closed).

After **15 minutes** in `tenant_pending` (≈ 40 resume attempts), SB-3 reports `status:"failed"` with a support reference (`trace_id`), and `signup_bff_provisioning_failed_total{stage="org_membership",reason="exhausted"}` increments (alerted, §11.5).

### 8.3 Slug already taken

```mermaid
sequenceDiagram
    autonumber
    actor B as Browser
    participant S as Signup BFF
    participant V as Valkey
    participant OM as Org & Membership

    S->>OM: I-1 {slug:"acme", ...}
    OM-->>S: 409 slug_already_taken
    S->>V: handoff.state=slug_conflict
    S-->>B: 409 slug_taken {signup_id, suggestions[]}
    B->>S: POST /api/v1/signup/{signup_id}/slug {slug:"acme-bidding"} + CSRF
    Note over S: validate slug · slug_attempts ≤ 5
    S->>OM: I-1 {same tenant_id, slug:"acme-bidding", same owner_user_id}
    OM-->>S: 201
    S->>V: state=completed
    S-->>B: 201 {status:"completed"}
```

The Keycloak user is **not** recreated: RP-1 does not use the slug for a trial tenant (RP §5.4.1), so only I-1 is retried.

### 8.4 Rejections before any Keycloak effect

| Situation | Detected at | Response | Handoff |
|---|---|---|---|
| Validation fails | BFF | `400 validation_failed` | not created |
| CSRF / session bad | BFF | `403` / `401` | not created |
| Rate limit exceeded | Valkey counter | `429 rate_limited` | not created |
| CAPTCHA fails | Turnstile | `403 captcha_failed` | not created |
| Trial already used | Eligibility (CR-4) | `409 trial_not_eligible` | deleted |
| Email already registered | RP-1 `409 email_already_registered` (CR-1, done) | `409 email_unavailable` | deleted |

---

## 9. Concurrency, Consistency, and Failure Handling

### 9.1 Handoff state machine

```mermaid
stateDiagram-v2
    [*] --> allocated: SB-2 passes checks · tenant_id allocated
    allocated --> rejected: ineligible / email_unavailable
    allocated --> allocated: RP-1 5xx after retries (503 to user; resubmit)
    allocated --> identity_created: RP-1 201
    identity_created --> completed: I-1 201/200
    identity_created --> tenant_pending: I-1 5xx/timeout after retries
    identity_created --> slug_conflict: I-1 409 slug_already_taken
    tenant_pending --> completed: SB-3 resume succeeds
    tenant_pending --> failed: 15 min exhausted
    slug_conflict --> identity_created: SB-4 new slug
    completed --> [*]: TTL 10 min
    rejected --> [*]
    failed --> [*]: TTL
```

| State | Keycloak user? | Tenant row? | Who moves it on |
|---|---|---|---|
| `allocated` | No | No | SB-2 (only SB-2 holds the password) |
| `identity_created` | **Yes** | No | SB-2 inline I-1 |
| `tenant_pending` | **Yes** | No | SB-3 polls; O&M backstop (BFF-2) |
| `slug_conflict` | **Yes** | No | SB-4 |
| `completed` | Yes | Yes | — |
| `rejected` / `failed` | No / Yes | No | — (`failed` relies on the backstop) |

### 9.2 Idempotency

| Layer | Mechanism |
|---|---|
| Browser double-click | One `signup_id` per session; `sbff:lock:<signup_id>` (`SET NX PX 15000`). A second concurrent request gets `409 invalid_state` with `details.retry_after_ms` and the client re-polls. |
| RP-1 | `Idempotency-Key: signup:<tenant_id>:rp1`. A replay returns the stored `201`; a concurrent duplicate returns `409 operation_in_progress` → wait and retry with the same key (RP §8.0, §9.2). |
| I-1 | Idempotent on `tenant_id` (`ON CONFLICT (id) DO NOTHING`, I1-1) → `200` on replay. No `Idempotency-Key` (O&M has none, O&M §9.2). |
| Deterministic key | The RP-1 key is derived from `tenant_id`, not random, so a retry after a Valkey blip still reuses it (C-2). |

### 9.3 Timeouts and retries (assumed — N-11)

Server request deadline for SB-2: **2,500 ms**. SLO target (HLD §3.4): 1.5 s p99 on the no-retry path.

| Step | Per-attempt timeout | Retries | Retry on | Notes |
|---|---|---|---|---|
| Valkey ops | 50 ms | 1 | network error | Fail-closed after |
| Turnstile | 300 ms | 1 | 5xx / timeout | Then `503 dependency_unavailable` |
| Eligibility (CR-4) | 150 ms | 1 | 5xx / timeout | Then `503` (no Keycloak effect yet) |
| Password hash | ~100–200 ms CPU | — | — | Runs after CAPTCHA (SB-INV-5) |
| RP-1 | 900 ms | 1 | 502, 503, timeout, `409 operation_in_progress` | Same key; then `503`, handoff stays `allocated` |
| I-1 (inline) | 300 ms | 2 (backoff 100 ms, 250 ms) | 5xx, timeout | Only while the deadline allows; then `202` |
| I-1 (SB-3 resume) | 500 ms | 0 per poll | — | Poll cadence is the retry loop |

Retries use jittered exponential backoff and never exceed the remaining request deadline.

### 9.4 Partial-failure policy (BFF-2, decided — option B amended)

1. **Inline retry** of I-1 within SB-2 (§9.3).
2. **Resume** via SB-3 polling while the browser stays (up to 15 min).
3. **Backstop (built, IAM-side):** RP-1 emits `TenantIdentityProvisioned {tenant_id, owner_user_id, slug, name, plan}` from its outbox once the Keycloak user exists. O&M's `tenant-orgm-q` consumer runs the same idempotent I-1 if no tenant row exists (O&M #25).

| Backstop behaviour (O&M) | Effect on the BFF |
|---|---|
| I-1 idempotent on `tenant_id`; first writer wins | BFF retries and the backstop race safely (SB-INV-8 keeps their values identical) |
| Payload I-1 can never accept (validation failure, slug held by another tenant) → straight to DLQ, nothing created | A `slug_conflict` signup is **not** completed by the backstop; only SB-4 completes it |
| Transient failures retried; EVT-17: an event for a tenant with no row yet is retried, not dropped | Covers browser abandonment, pod restarts and Valkey loss in `tenant_pending` |
| An offboarded tenant is never recreated | — |
| Metric `iam_org_membership_tenant_provisioning_backstop_total{result}` | Added to the Signup Funnel dashboard (§11.5) |

No BFF-owned reconciliation job and no BFF database (SB-INV-1).

**Remaining open (BFF-2a, §16):** a signup abandoned in `slug_conflict` leaves a Keycloak user with no tenant, and its `TenantIdentityProvisioned` sits in O&M's DLQ. Options: a slug-availability pre-check before RP-1 (removes most cases), or an IAM cleanup of DLQ'd `slug_already_taken` events after N days. The DLQ alert should treat `slug_already_taken` as expected, not an incident.

### 9.5 Failure taxonomy

| Failure | User sees | Internal state | Metric `stage/reason` |
|---|---|---|---|
| Valkey down | `503` | none | `valkey/unavailable` |
| Turnstile down | `503` | none | `captcha/unavailable` |
| Eligibility down | `503` | `allocated` | `eligibility/unavailable` |
| RP-1 `502`/`503` after retries | `503`, resubmit | `allocated` | `realm_provisioner/unavailable` |
| RP-1 unexpected `4xx` (`400`, `403 tenant_mismatch`) | `500` | `allocated` | `realm_provisioner/contract_error` (paged) |
| I-1 `5xx` after retries | `202` | `tenant_pending` | `org_membership/unavailable` |
| I-1 `409 slug_already_taken` | `409 slug_taken` | `slug_conflict` | `org_membership/slug_taken` |
| I-1 unexpected `4xx` | `500` | `identity_created` | `org_membership/contract_error` (paged) |
| Pod dies mid-handoff | Next poll / resubmit resumes | last persisted state | `handoff/resumed` |

---

## 10. Security

### 10.1 Network and mesh identity (BFF-1, decided — option A)

- The mesh is **Istio** on EKS. The pod runs as service account **`bff-signup`** in namespace **`bff`**; Istio identity **`spiffe://<trust-domain>/ns/bff/sa/bff-signup`**. The IAM charts default `trustDomain` to `cluster.local`; the production value is an **SRE open value**.
- **Ingress to the BFF:** NetworkPolicy and an Istio `AuthorizationPolicy` allow only the Envoy Gateway proxy namespace **`envoy-gateway-system`** (API port 8080) and **`monitoring`** (metrics port 9090) (G-5).
- **Egress from the BFF:** NetworkPolicy allows only DNS, the ElastiCache Valkey endpoint, the Realm Provisioner and Org & Membership API ports (8080, via the Istio sidecar), the Catalog Service (N-2), the OTel collector, and HTTPS to Turnstile. An Istio `ServiceEntry` for `challenges.cloudflare.com:443` is required if the mesh runs `outboundTrafficPolicy: REGISTRY_ONLY` (G-6).
- **IAM side** (IAM platform security + SRE): on both Realm Provisioner and Org & Membership, a NetworkPolicy rule admitting **only the `bff-signup` pod** in `bff` (not the whole namespace), and an Istio `AuthorizationPolicy` rule for principal `<trust-domain>/ns/bff/sa/bff-signup` allowing **only**:

| Service | Method + path |
|---|---|
| Realm Provisioner | `POST /api/v1/internal/tenants/provision-trial` |
| Org & Membership | `POST /api/v1/internal/tenants` |
| Org & Membership | `POST /api/v1/internal/trial-eligibility` (CR-4, done) |

All other internal routes stay denied to the BFF. O&M LLD §10.2 is amended to name `bff/bff-signup` (C-5).

**IAM chart state — resolved (F-1, CR-3, F-2 closed 22 Oct 2026):**

| Service | Change | How implemented |
|---|---|---|
| Realm Provisioner | `bff/bff-signup` admitted on RP-1 path only | New `bffCaller` block in `authorizationpolicy.yaml` (pod-level SPIFFE principal, `POST /api/v1/internal/tenants/provision-trial` only); `bff` namespace added to `networkpolicy.yaml` `additionalIngressRules` in all three env values files |
| Org & Membership | `bff` namespace admitted for I-1 and trial-eligibility | `bff` added to `existingCallerNamespaces` in all three env values files (**⚠ TL-1 — see §16.2**: this is namespace-level, not the pod-level rule specified above; only one service runs in `bff` today so the blast radius is the same, but it differs from the spec) |

**F-2 (closed):** RP handler docs no longer list the BFF as a caller of RP-3 or RP-11 (removed 22 Oct 2026).

### 10.2 Abuse controls

| Control | Rule | Enforced |
|---|---|---|
| Per-IP new signups | 3 / hour (HLD §3.4) | BFF, Valkey `sbff:rl:ip` |
| Per-email attempts | 5 / 24 h | BFF, Valkey `sbff:rl:email` |
| Session starts | 20 / IP / hour | BFF, `sbff:rl:sess` |
| Status polls | 60 / session / minute | BFF, `sbff:rl:poll` |
| CAPTCHA | Every SB-2, invisible mode | Turnstile (BFF-3) |
| WAF | AWS managed rules + IP reputation on the signup host | ALB (SRE) |
| Body size | 16 KB | BFF middleware |

**When counters increment (G-2):** the per-IP and per-email counters increment only **after** the CAPTCHA passes, so a real user who fails a challenge does not lose an attempt; bots are stopped by the CAPTCHA itself. Resumes of an existing `signup_id` never count. **Risk for TL:** 3 signups per IP per hour is strict for offices behind one NAT IP; the limit is configurable (§12) if web / growth wants to raise it.

**Client IP (N-8):** Envoy is configured with `xff_num_trusted_hops: 1` (the ALB) and sets `x-envoy-external-address`; the BFF reads only that header. It never parses `X-Forwarded-For` itself.

### 10.3 Enumeration posture

- `409 email_unavailable` reveals that an email cannot be used. It is reached only after CAPTCHA and is rate-limited per IP and per email (accepted for Phase 1; C-3 option B would remove it).
- `404 signup_not_found` is returned for both "doesn't exist" and "not your session".
- `trial_not_eligible` shows the same neutral UI text as `email_unavailable`.

### 10.4 CSRF, CORS and cookies (N-9, BFF-4)

- Same-origin page and API on `signup.bcbpsolutions.com` (BFF-4, decided) ⇒ **no CORS** headers are emitted.
- Session cookie `sbff_sid`: `HttpOnly; Secure; SameSite=Strict; Path=/api/v1/signup`, host-only, 30 min.
- CSRF: synchronizer token issued by SB-1, stored as SHA-256 in the session, sent as `X-CSRF-Token`, compared in constant time. `Origin` must equal the signup host on every `POST`.

### 10.5 Input handling

- Strict JSON decoding (`DisallowUnknownFields`), size limit, UTF-8 only, NFC normalisation for names, control-character rejection.
- Free text (`company_name`, `admin_name`) is never interpreted; it is passed as data to RP-1 and I-1.

### 10.6 Password handling (C-4, resolved — option A)

- Validated against `password_policy` (N-10), then hashed in memory with **PBKDF2-SHA512, 210,000 iterations, 16-byte random salt**.
- `hashed_password_seed` is the Keycloak credential representation, serialized as JSON:

```json
{
  "algorithm": "pbkdf2-sha512",
  "iterations": 210000,
  "salt": "<base64 16 bytes>",
  "hash": "<base64 64 bytes>"
}
```

- RP-1 maps it to Keycloak `secretData` / `credentialData`; the trial realm's hash policy must match (IAM sign-off).
- **Consequence:** Keycloak cannot check its password policy (length, blacklist, not-username) against a pre-hashed credential. Under C-4 the **BFF's `password_policy` check is the authoritative one at signup**; it must be a faithful copy of the trial realm policy (N-10). Keycloak enforces its policy on later password changes and resets as usual.
- The plain password and the seed are zeroed after use where practical, never logged, never in Valkey, never in traces (SB-INV-2).
- Hashing is CPU-heavy (~100–200 ms per signup); it runs only after CAPTCHA and rate limiting, and pod CPU limits are sized for it (§13).

### 10.7 Secrets

All via External Secrets Operator from OpenBao (`secret/data/bff/bff-signup/*`): Turnstile secret, `SBFF_HMAC_KEY`, Valkey password. Never in env files, images or git. Mesh certificates are issued by the mesh, not stored by the BFF.

### 10.8 What the BFF cannot do

- Call Keycloak or any `/admin` API (no client library, no route, no credential).
- Call any IAM internal route other than those in §10.1.
- Issue or validate a JWT.
- Receive a card number (Phase 1 has no payment; Phase 1.5 stays tokenized-only).

---

## 11. Observability

### 11.1 SLOs (HLD §3.4)

| SLO | Target | Measured by |
|---|---|---|
| SB-2 trial latency (no-retry path) | p99 ≤ 1.5 s | `signup_bff_request_duration_seconds{route="/trial"}` |
| Availability | 99.9% monthly | non-5xx ratio on SB-1/SB-2 |
| Rate limit | 3 signups / IP / hour | `signup_bff_rate_limited_total{scope="ip"}` |

### 11.2 Prometheus metrics

| Metric | Type | Labels |
|---|---|---|
| `signup_bff_form_started_total` | counter | `mode` |
| `signup_bff_form_submitted_total` | counter | `mode` |
| `signup_bff_provisioning_succeeded_total` | counter | `mode`, `path` (`inline`, `resume`, `slug_change`) |
| `signup_bff_provisioning_failed_total` | counter | `mode`, `stage`, `reason` |
| `signup_bff_rate_limited_total` | counter | `scope` (`ip`, `email`, `session`, `poll`) |
| `signup_bff_captcha_challenged_total` | counter | `result` (`pass`, `fail`, `error`) |
| `signup_bff_handoff_resumed_total` | counter | `trigger` (`poll`, `resubmit`, `slug_change`) |
| `signup_bff_request_duration_seconds` | histogram | `route`, `code` |
| `signup_bff_dependency_duration_seconds` | histogram | `dependency`, `outcome` |
| `signup_bff_password_hash_duration_seconds` | histogram | — |

`mode` is always `trial` in Phase 1. No label carries a tenant id, email or IP.

### 11.3 Tracing

OpenTelemetry via `platform-gincommon`; `traceparent` is propagated to Turnstile (where supported), Valkey spans, RP-1 and I-1, so one trace runs from the browser request into Realm Provisioner, Keycloak and Org & Membership. Span attributes include `signup.id`, `tenant.id` and `handoff.state` — never email, name or password.

### 11.4 Structured logs

JSON logs with `request_id`, `trace_id`, `signup_id`, `tenant_id`, `state`, `stage`, `outcome`. **Redacted / never logged:** password, `hashed_password_seed`, `captcha_token`, CSRF token, cookie, full email (only `email_domain` and the HMAC), admin name. A log-redaction unit test guards this (§14).

### 11.5 Dashboards and alerts

**"Signup Funnel"** Grafana dashboard (next to "IAM Overview"): sessions → submissions → CAPTCHA pass → RP-1 success → I-1 success, plus failures by `stage` and the `202` resume rate.

| Alert | Condition | Route |
|---|---|---|
| Provisioning failure rate | `failed / submitted > 5%` for 10 min | Web / growth on-call |
| O&M lag | any `stage="org_membership", reason="exhausted"` in 15 min | Web / growth → IAM |
| Contract error | any `reason="contract_error"` (includes RP-1 `400 INVALID_REQUEST` for `plan`) | Web / growth + IAM (page) |
| Backstop activity | `iam_org_membership_tenant_provisioning_backstop_total{result="created"}` rising (the BFF is failing I-1 more than expected) | Web / growth |
| RP-1 unavailable | `stage="realm_provisioner"` > 10 in 5 min | Web / growth → IAM |
| CAPTCHA provider errors | `result="error"` > 5% for 5 min | Web / growth |
| Valkey down | readiness failing on all pods | Web / growth + SRE |
| SLO burn | 2% of monthly error budget in 1 h | Web / growth |

**On-call is open (BFF-6).** The routes above assume web / growth primary with IAM escalation; this must be confirmed with names before go-live, or the alerts page nobody.

---

## 12. Configuration

```yaml
# values-prod.yaml (excerpt)
service:
  name: bff-signup
  namespace: bff
  serviceAccount: bff-signup               # BFF-1 (decided)
publicHost: signup.bcbpsolutions.com       # BFF-4 (decided)

http:
  port: 8080
  metricsPort: 9090
  requestDeadlineMs: 2500
  bodyLimitBytes: 16384

session:
  ttlSeconds: 1800
  cookieName: sbff_sid

rateLimits:
  signupsPerIpPerHour: 3                   # HLD §3.4
  attemptsPerEmailPerDay: 5
  sessionsPerIpPerHour: 20
  pollsPerSessionPerMinute: 60
  failClosed: true                         # N-7

handoff:
  ttlSeconds: 1800
  completedTtlSeconds: 600
  lockMs: 15000
  maxSlugAttempts: 5
  resumeWindowSeconds: 900

upstreams:
  realmProvisioner:
    baseUrl: http://iam-realm-provisioner.iam.svc.cluster.local:8080   # Istio sidecar adds mTLS (RP API port 8080)
    timeoutMs: 900
    retries: 1
  orgMembership:
    baseUrl: http://iam-org-membership.iam.svc.cluster.local:8080
    i1TimeoutMs: 300
    i1InlineRetries: 2
    eligibilityEnabled: true               # CR-4 (done — O&M endpoint shipped)
    eligibilityTimeoutMs: 150
  catalog:
    baseUrl: http://catalog.<ns>.svc.cluster.local:8080    # N-2 (namespace TBC)
    cacheTtlSeconds: 300
  systemPrincipal: "00000000-0000-0000-0000-0000000000a1"

captcha:
  provider: turnstile                      # BFF-3 (decided)
  siteKey: "<public site key>"
  timeoutMs: 300
  expectedHostname: signup.bcbpsolutions.com

password:
  minLength: 12
  maxLength: 128
  requireClasses: 3
  pbkdf2Iterations: 210000                 # C-4 — must match Keycloak trial realm
  saltBytes: 16

slug:
  reserved: [admin, api, app, auth, billing, help, login, signup, status, support,
             trial, www, mail, static, cdn, internal, system, root, tenant, keycloak]

plansFallback:                             # used only if Catalog is unreachable and cache empty.
                                           # Codes must stay exactly starter|pro|enterprise (RP-1 / I-1 / event schema)
  - { code: starter,    name: Starter }
  - { code: pro,        name: Pro }
  - { code: enterprise, name: Enterprise }

terms:
  currentVersion: "2026-09"                # N-5

valkey:
  addr: <elasticache-valkey-endpoint>:6379  # G-7: ElastiCache for Valkey
  tls: true

secrets:                                   # External Secrets → OpenBao
  turnstileSecret: secret/data/bff/bff-signup/turnstile
  hmacKey:         secret/data/bff/bff-signup/hmac
  valkeyPassword:  secret/data/bff/bff-signup/valkey
```

**Fail-fast:** the service refuses to start if any secret is missing, `publicHost` is empty, `pbkdf2Iterations < 210000`, or the pod has no Istio sidecar (mTLS to IAM is provided by the mesh, so in-cluster upstream URLs are plain `http` to the sidecar; Turnstile is called over `https`).

---

## 13. Deployment and Scaling

### 13.1 Kubernetes resources

| Resource | Spec |
|---|---|
| Deployment `bff-signup` (ns `bff`) | Istio sidecar injected, 2 replicas, `runAsNonRoot`, read-only root FS, no privilege escalation, distroless image |
| Container resources | requests `250m` CPU / `128Mi`; limits `1` CPU / `256Mi` (PBKDF2 headroom) |
| HPA | 2 → 5 replicas on CPU 70% (G-9: CPU-only; hashing is CPU-bound, and RPS scaling would need a custom-metrics adapter) |
| PodDisruptionBudget | `minAvailable: 1` |
| Probes | liveness `/healthz`; readiness `/readyz` (Valkey ping + config loaded) |
| Service | ClusterIP, port 8080 (API), 9090 (metrics) |
| HTTPRoute (Envoy) | host `signup.bcbpsolutions.com`, prefix `/api/v1/signup`, **no `jwt_authn` / `ext_authz`** (public carve-out) |
| NetworkPolicy + Istio `AuthorizationPolicy` | ingress from `envoy-gateway-system` and `monitoring` only; egress per §10.1 |
| Istio `ServiceEntry` | `challenges.cloudflare.com:443` (Turnstile), if `REGISTRY_ONLY` |
| Valkey | Amazon ElastiCache for Valkey, `cache.t3.micro` (HLD §13), TLS + AUTH, no snapshots (G-7) |
| ExternalSecret | the three secrets in §12 |

### 13.2 Scaling

Signup volume is low and bursty (HLD §13). CPU is dominated by password hashing (~150 ms per signup), so one `1`-CPU pod handles roughly 5–6 hashes per second; two pods cover MVP bursts with margin. Load test before launch (§14.6) and revisit when web / growth has funnel projections.

### 13.3 Helm chart and CI/CD

- Chart in `deploy/helm/` with `values-{dev,staging,prod}.yaml`.
- GitHub Actions: `lint → go vet (all build tags) → unit → integration (testcontainers Valkey) → contract → build image → scan → helm lint → deploy dev → e2e → manual promote staging/prod` (same pattern as IAM HLD §15.5).
- No database migrations.

### 13.4 Environments

| Env | CAPTCHA | IAM upstreams |
|---|---|---|
| dev | Turnstile test keys (always pass) | Fakes or dev IAM |
| staging | Real Turnstile, staging keys | Staging RP + O&M + Keycloak |
| prod | Real Turnstile | Prod |

---

## 14. Testing Strategy

### 14.1 Unit tests

- Validation table tests for every field in §5.4.
- Slug derivation, validation, reserved words and suggestions.
- Handoff state machine: every allowed and every rejected transition (§9.1).
- PBKDF2 output verified against a known vector and against Keycloak's format.
- Error mapping tables (§9.5, §17).
- **Redaction test:** run a full SB-2 with a fake logger and assert no password, seed, token or email appears in any log line or span attribute.

### 14.2 Integration tests (testcontainers-go)

- Real Valkey: sessions, counters, locks, TTLs, fail-closed on Valkey stop.
- `httptest` fakes for RP-1, I-1, eligibility, Turnstile, Catalog, scripted for success, 409s, 5xx and timeouts.

### 14.3 Contract tests

- Consumer-driven contracts (Pact or JSON-schema) for RP-1, I-1 and eligibility, published to the IAM repos' CI so a breaking change there fails their build.
- **Plan contract:** every `plan` the BFF can send (catalogue and `plansFallback`) is one of `starter`/`pro`/`enterprise`; a test asserts the BFF never sends `trial`.
- **Same-values contract:** for every completed signup, RP-1 and I-1 bodies carry identical `plan`, `name`, and slug (except after SB-4) (SB-INV-8).
- RP-1 error parsing branches on RFC 9457 `status` + `errorCode`, never `detail`.
- Header contract: every outbound call carries `x-tenant-id`, `x-user-id = …00a1`, and a stable `Idempotency-Key`.

### 14.4 E2E (staging)

1. Happy path: form → `201` → verification email received → first login lands in the tenant.
2. O&M stopped mid-flow → `202` → O&M restarted → SB-3 completes; exactly one tenant and one Keycloak user exist.
3. Slug conflict → SB-4 → completed with the new slug; one Keycloak user.
4. Duplicate email → `409 email_unavailable`; no new Keycloak user.
5. Repeat trial (CR-4) → `409 trial_not_eligible`; no Keycloak user created.
6. Double-click submit → one tenant.
7. Pod killed during RP-1 → resubmit → same `tenant_id`, one Keycloak user.
8. Resubmit with a different email while `allocated` → new `tenant_id`; one Keycloak user per email (G-1).
9. BFF I-1 blocked, browser closed → the backstop creates the tenant from `TenantIdentityProvisioned` within minutes.
10. Valkey stopped mid-signup (chaos) → `503` fail-closed; after recovery, the backstop has completed any `identity_created` signup.

### 14.5 Security tests

- Fourth signup from one IP in an hour → `429`.
- Missing / wrong CSRF token, wrong `Origin` → `403`.
- Another session's `signup_id` on SB-3/SB-4 → `404`.
- Spoofed `X-Forwarded-For` does not change the rate-limit key.
- Inbound `x-user-id` / `x-tenant-id` headers from the browser are ignored.
- OWASP ZAP baseline scan against staging.
- From the BFF pod, every IAM internal route other than the allow-listed ones returns `403` (verifies BFF-1).

### 14.6 Load tests (k6)

- 10 signups/s burst for 5 min against staging with fakes for IAM: p99 within SLO, CPU under limit, no Valkey errors.
- Verify HPA scale-out and that hashing CPU does not starve health probes.

---

## 15. Data Lifecycle, Privacy, and DR

### 15.1 PII disposition

| Data | Where it goes | Held by BFF |
|---|---|---|
| Admin email | RP-1 → Keycloak | Memory only; HMAC in Valkey ≤ 24 h |
| Admin name | RP-1 → Keycloak | Memory only |
| Password | Hashed in memory → RP-1 → Keycloak | Never stored |
| Company name, slug | RP-1 and I-1 → O&M | Valkey handoff ≤ 30 min |
| `keycloak_user_id` | I-1 → O&M | Valkey handoff ≤ 30 min |
| Client IP | Rate-limit key | Valkey ≤ 1 h |
| Terms acceptance (`terms_version`, time) | **Open — N-5** (I-1 field or Audit Log) | Not stored |

There is nothing to erase on a GDPR request: every BFF-held value expires within 24 hours, Valkey has no persistence or backups, and logs carry no direct PII (§11.4).

### 15.2 Disaster recovery (HLD §12)

| Component | RTO | RPO | Recovery |
|---|---|---|---|
| Signup BFF | 5 min | N/A | Redeploy / reschedule |
| BFF Valkey | Immediate (starts empty) | N/A | Counters reset; in-flight signups resubmit or rely on the O&M backstop |

The BFF is not part of the platform's backup or realm-export procedures.

---

## 16. Open Questions Register

Status as of 22 Oct 2026. **Decided** = TL decision; **Resolved** = settled from the reference LLDs or the recommended option; **Open** = this draft uses the assumption shown. All design decisions are now closed; remaining items in §16.2 are IAM/SRE delivery prerequisites for go-live.

### 16.1 Decided and resolved

| # | Item | Outcome | Sections |
|---|---|---|---|
| BFF-1 | Mesh identity | **Decided A.** SA `bff-signup` in `bff`, Istio SPIFFE identity, pod-only NetworkPolicy, route allow-list on RP and O&M | §10.1, §13.1 |
| BFF-2 | Partial failure | **Decided B (amended).** Inline retry + SB-3 resume + O&M backstop on `TenantIdentityProvisioned` — **IAM side built** | §7, §8.2, §9.4 |
| BFF-3 | CAPTCHA | **Decided A.** Cloudflare Turnstile, invisible, every submission | §5.6, §10.2 |
| BFF-4 | Domain | **Decided A.** `https://signup.bcbpsolutions.com`, page and API same origin | §5.1, §10.4 |
| BFF-5 | Billing | **Decided A.** Deferred to Phase 1.5; `BillingClient` stub | §19 |
| BFF-6 (repo) | Repository | **Decided.** `BCBP-SOLUTIONS-FZC-LLC/bff-signup` | Header |
| RP plan note | RP-1 `plan` validation | **Adopted.** Only `starter`/`pro`/`enterprise`; same values to RP-1 and I-1 (SB-INV-8) | §2.3, §18.4 |
| C-1 | I-1 body | **Resolved** — O&M code-verified shape | §5.6 |
| C-2 | Headers / key | **Resolved** — `x-tenant-id`, `x-user-id = …00a1`; Idempotency-Key on RP-1 only | §5.6, §9.2 |
| C-3 | Duplicate email | **Resolved** — recommended A, CR-1 | §17.2 |
| C-4 | Password seed | **Resolved** — recommended A, CR-2; BFF policy check authoritative | §10.6 |
| C-5 | O&M namespace rule | **Resolved** — admit `bff/bff-signup` (TL corrected namespace), CR-3 | §10.1 |
| C-6 | `owner_user_id` wording | **Resolved** — doc fix in O&M | SB-INV-4 |
| C-7 | One-trial check | **Resolved** — recommended A, CR-4 (O&M also adds the missing ledger) | §5.6, §8.1 |
| F-1 | RP `gateway` admission / O&M blocking BFF | **Closed (22 Oct 2026).** CR-3 done — RP uses pod-level `bffCaller`; O&M adds `bff` to `existingCallerNamespaces` (⚠ TL-1: namespace vs pod-level, §16.2) | §10.1 |
| F-2 | RP lists BFF as caller of RP-3/RP-11 | **Closed (22 Oct 2026).** Removed from handler docs | §10.1 |
| CR-1 | `409 email_already_registered` | **Done (22 Oct 2026).** `errorCode: EMAIL_ALREADY_REGISTERED`, RFC 9457 | §17.2, §18.1 |
| CR-2 | `hashed_password_seed` + realm hash policy | **Done (22 Oct 2026).** `admin_users.go` + `realm-template.json` | §10.6 |
| CR-3 | BFF admission on RP and O&M | **Done (22 Oct 2026).** See F-1 above | §10.1 |
| CR-4 | `trial_signup_ledger` + trial-eligibility endpoint | **Done (22 Oct 2026).** Migration 000004, handler, repo | §5.6, §8.1 |

### 16.2 Still open

| # | Question | Assumption in this draft | Owner | Blocks go-live? |
|---|---|---|---|---|
| **TL-1** | **O&M CR-3 used namespace-level admission** (`bff` in `existingCallerNamespaces`) not the pod-level rule the LLD specifies. Today only `bff-signup` runs in `bff`, so blast radius is identical, but it diverges from the spec. **TL to decide:** accept namespace-level (update LLD §10.1 spec) or ask O&M to add a pod-specific `bffCaller` block matching RP's approach | Namespace-level accepted pending TL decision | Web / growth TL + IAM O&M | No |
| BFF-1 value | Mesh trust domain | `cluster.local` (IAM chart default) | SRE | Yes |
| BFF-2 SRE | SNS filter for `tenant-orgm-q` must include `TenantIdentityProvisioned` | Added before go-live | SRE | **Yes** (backstop off without it) |
| BFF-2a | Abandoned `slug_conflict` signup (Keycloak user, DLQ'd event) | Slug pre-check before RP-1, or IAM DLQ cleanup | Web / growth + IAM | No |
| BFF-4 SRE | DNS, TLS, WAF, Envoy route for `signup.bcbpsolutions.com` | Provisioned by SRE | SRE | Yes |
| BFF-6 | On-call model and rotation | Web / growth primary, IAM escalation | Web / growth lead | **Yes** |
| N-1 | Slug ownership and rules | BFF derives, user may edit, §5.8 | Web / growth | No |
| N-2 | Plan list source | Catalog Service + cache + static fallback | Web / growth | No |
| N-3 | Unverified signups never verified | IAM cleans up after N days | IAM | No |
| N-4 | Resend verification email | Keycloak's own flow; no BFF endpoint | IAM | No |
| N-5 | Terms acceptance record | Storage open (I-1 field or Audit Log) | Legal + IAM | Legal call |
| N-6 | Who builds the page | BFF JSON-only; web frontend builds UI | Web / growth | No |
| N-7 | Valkey down | Fail-closed `503` | Web / growth | No |
| N-8 | Rate-limit location, client IP | BFF; Envoy `xff_num_trusted_hops: 1` | Web / growth + SRE | No |
| N-9 | CSRF | Synchronizer token + `SameSite=Strict` + `Origin` | Web / growth | No |
| N-10 | Password policy source | Copy of trial realm policy (authoritative at signup) | Web / growth + IAM | No |
| N-11 | Timeout budget | §9.3 | Web / growth | No |
| N-12 | Login before the tenant row exists (G-3) | **Closed.** I-8 → `404` → AuthZ Enrichment → `403`. Hard fail, no grace period. BFF `tenant_pending` UI warning covers this. (O&M LLD rev 2.30, I8-3) | — | — |

### 16.3 Change requests to IAM — all done (22 Oct 2026)

| CR | Owner | Change | Status |
|---|---|---|---|
| CR-1 | Realm Provisioner | RP-1 returns `409` with `errorCode: EMAIL_ALREADY_REGISTERED` (RFC 9457) when the email exists in the `trial` realm | ✅ Done — `errors.go` + `errors_test.go` updated |
| CR-2 | Realm Provisioner + Keycloak config | `hashed_password_seed` mapped to `secretData`/`credentialData`; `trial` realm hash policy set to `pbkdf2-sha512`, 210,000 iterations in `realm-template.json` | ✅ Done — `admin_users.go` + `realm-template.json` updated |
| CR-3 | RP + O&M + IAM security | RP: pod-level `bffCaller` AuthorizationPolicy + NetworkPolicy for `bff/bff-signup` → RP-1 path only. O&M: `bff` added to `existingCallerNamespaces` (⚠ TL-1 — namespace-level, not pod-level) | ✅ Done — see TL-1 in §16.2 |
| CR-4 | Org & Membership | `trial_signup_ledger` table (migration 000004); `POST /api/v1/internal/trial-eligibility` endpoint; `RecordTrialSignup` called atomically in I-1 tx | ✅ Done — migration, handler, service, repo all added |
| Doc | Org & Membership | I-1 prose: `owner_user_id` is RP-1's `keycloak_user_id`, not a JWT claim | ✅ Done — O&M LLD §I-1 corrected |
| Doc | BFF HLD v1.1 | §7.1 I-1 body (C-1); CAPTCHA on every submission (BFF-3); namespace `bff`; `TenantIdentityProvisioned`; §6 wording | Pending — HLD owner to raise v1.1 |

---

## 17. Appendix — Error Taxonomy

Public wire shape — the current `gincommon.ErrorResponse` envelope (G-8, as O&M rev 2.30 documents it):

```json
{ "error": "slug_taken", "code": "slug_taken", "message": "That workspace address is taken.", "status": 409,
  "trace_id": "abc123", "request_id": "req-xyz", "details": { "suggestions": ["acme-2"] } }
```

The platform plans to move to RFC 9457 Problem Details (CD-31); the BFF follows when `platform-gincommon` does. **Both Realm Provisioner (rev 1.31) and Org & Membership (rev 2.29+) now return RFC 9457** (`application/problem+json`, `{status, errorCode, detail, …}`) with `UPPER_SNAKE_CASE` error codes; the BFF adapters branch on `status` + `errorCode`, never on `detail`.

### 17.1 Public error codes

| `error` | HTTP | Meaning | `details` | UI guidance |
|---|---|---|---|---|
| `validation_failed` | 400 | One or more fields invalid | `fields: {name: code}` | Highlight fields |
| `session_expired` | 401 | Missing or expired session cookie | — | Restart from SB-1 |
| `csrf_invalid` | 403 | Missing/wrong CSRF token or `Origin` | — | Reload form |
| `captcha_failed` | 403 | Turnstile rejected the token | — | Retry CAPTCHA |
| `signup_not_found` | 404 | Unknown `signup_id` or not this session | — | Restart |
| `email_unavailable` | 409 | Email cannot be used (already registered) | — | "Use another email or sign in" |
| `trial_not_eligible` | 409 | Trial already used for this email | — | Same text as above |
| `slug_taken` | 409 | Slug owned by another tenant | `signup_id`, `suggestions[]` | Show slug picker → SB-4 |
| `invalid_state` | 409 | Action not valid in the current handoff state, or a request already in flight | `state`, `retry_after_ms?` | Poll SB-3 |
| `rate_limited` | 429 | Limit exceeded | `scope` | Respect `Retry-After` |
| `internal_error` | 500 | Unexpected error or IAM contract mismatch | — | Show support reference |
| `dependency_unavailable` | 503 | Valkey, CAPTCHA, eligibility or RP-1 unavailable | `dependency` | "Try again in a minute" |

### 17.2 Upstream → public mapping

| Upstream response | Action | Public result |
|---|---|---|
| RP-1 `201` | Continue to I-1 | — |
| RP-1 `409 errorCode: OPERATION_IN_PROGRESS` | Wait, retry same key | — / `503` |
| RP-1 `409 errorCode: EMAIL_ALREADY_REGISTERED` *(CR-1, done)* | Delete handoff | `409 email_unavailable` |
| RP-1 `502 errorCode: KEYCLOAK_UNAVAILABLE`, `503 *` | Retry once | `503 dependency_unavailable` |
| RP-1 `400 errorCode: INVALID_REQUEST` (e.g. `plan` not `starter`/`pro`/`enterprise`) | No retry, page — means a BFF bug (§18.4) | `500 internal_error` |
| RP-1 `403 errorCode: TENANT_MISMATCH`, other `4xx` | No retry, page | `500 internal_error` |
| I-1 `201` / `200` | Complete | `201` |
| I-1 `409 errorCode: SLUG_ALREADY_TAKEN` | `slug_conflict` | `409 slug_taken` |
| I-1 `5xx` / timeout | Retry inline, then `tenant_pending` | `202` |
| I-1 other `4xx` | No retry, page | `500 internal_error` |
| Eligibility `409 errorCode: TRIAL_ALREADY_USED` *(CR-4, done)* | Delete handoff | `409 trial_not_eligible` |
| Turnstile `success:false` | — | `403 captcha_failed` |

---

## 18. Integration Details

### 18.1 Realm Provisioner (RP-1)

Per RP LLD §18.1: the BFF pre-allocates `tenant_id`, sends the same value in the body and `x-tenant-id`, and RP persists only the `tenant_realms` row; identity goes to Keycloak. RP sends the verification email (24-hour link). The BFF depends on: the RP-1 body (§5.6), `201` response with `keycloak_user_id`, idempotent replay (§9.2), and `409 email_already_registered` (CR-1, done — `errorCode: EMAIL_ALREADY_REGISTERED`). RP-1 validates `plan` ∈ {`starter`, `pro`, `enterprise`} (RP #19) and emits `TenantIdentityProvisioned` after the Keycloak user exists.

### 18.2 Org & Membership (I-1, eligibility)

Per O&M LLD I-1: idempotent on `tenant_id`, `409 slug_already_taken` on a slug owned by another tenant, every tenant created trial-shaped, `trial_ends_at` computed by O&M, `TenantCreated` / `TrialStarted` / `TenantRoleGranted` emitted in the same transaction. The backstop consumer of `TenantIdentityProvisioned` is built (O&M #25). The eligibility endpoint (CR-4) is now built — `trial_signup_ledger` table (migration 000004), `POST /api/v1/internal/trial-eligibility` handler, `IsTrialEligible` / `RecordTrialSignup` on the tenant repository, and `RecordTrialSignup` called atomically inside the I-1 provisioning transaction.

### 18.3 Cross-service dependency table

| Dependency | Direction | Protocol | If down |
|---|---|---|---|
| Envoy (public) | inbound | HTTP | No signups (platform outage) |
| ElastiCache Valkey (BFF) | outbound | RESP/TLS | `503`, fail-closed |
| Turnstile | outbound | HTTPS via NAT | `503` |
| Catalog Service | outbound | mTLS | Cached / static plan list |
| O&M eligibility | outbound | mTLS | `503` before any Keycloak effect |
| Realm Provisioner | outbound | mTLS | `503`, user resubmits |
| Org & Membership I-1 | outbound | mTLS | `202` + resume + backstop |

### 18.4 Response to Realm Provisioner: RP-1 `plan` validation (TL item, 21 Oct 2026)

| RP asks | BFF answer |
|---|---|
| 1. Is the `plan` sent to RP-1 always `starter`, `pro` or `enterprise`? | **Yes.** The user picks a plan tier on the form (HLD §8.2.1 step 1). "Start free trial" selects **RP-1** (trial) vs RP-2 (paid, Phase 1.5); it is never a plan value. SB-2 rejects any other value with `400 validation_failed` before any call (§5.4), and the plan list and fallback contain only these three codes (§12). |
| 2. Does the BFF send the same `plan`, slug and name to I-1? | **Yes** (SB-INV-8). Both bodies are built from the same handoff record. The only exception is a slug changed via SB-4 after `409 slug_taken`; RP's event with the original slug is then rejected by O&M as non-acceptable, so the values can never conflict. |
| 3. Does the BFF currently send anything else? | **No.** Nothing is built yet; this LLD is the first specification, and a contract test enforces it (§14.3). |

---

## 19. Phase 1.5 Reservation — Direct-Paid Signup

Not specified in this draft (BFF-5). Reserved so Phase 1 code does not block it:

- Route `POST /api/v1/signup/paid` reserved (returns `404` until enabled).
- Port `BillingClient` with a stub adapter that returns `ErrNotImplemented`.
- Handoff states to add: `payment_pending`, `payment_confirmed` before `allocated → identity_created`; RP-2 replaces RP-1 and needs `billing_subscription_id`.
- Must resolve before Phase 1.5: Billing authorize/charge contract, double-charge-safe idempotency, "charge accepted" vs "payment confirmed" (Stripe webhook, IAM HLD §10.7), and the O&M direct-paid ordering open item (O&M LLD §8.1b).
- Cards stay tokenized in the browser by Stripe; no PAN ever reaches the BFF (HLD §10.1).

---

## 20. Appendix — Glossary and References

### 20.1 Glossary

| Term | Meaning |
|---|---|
| Handoff | The eligibility → RP-1 → I-1 sequence for one signup |
| `signup_id` | ULID identifying one signup attempt, bound to a session |
| `tenant_id` | UUIDv7 allocated by the BFF, used by both RP-1 and I-1 |
| System principal | `…00a1` ("iam-system"), the `x-user-id` for internal provisioning calls |
| Backstop | IAM-side mechanism that completes a signup the BFF could not finish |
| SPIFFE ID | Workload identity used for mesh mTLS |
| HMAC | Keyed hash used so Valkey never holds a readable email |

### 20.2 References

- `bff-hld-tender-saas-v1.0.md` — §3, §4, §6, §7, §8, §10, §11, §12, §13, §16
- `iam-lld-realm-provisioner-service.md` rev 1.31 — §5.1–§5.5 (RP-1, field disposition), §8.0–§8.1, §9.2, §17, §18.1; RFC 9457 adopted (rev 1.30); bffCaller AuthorizationPolicy added (rev 1.31)
- `iam-lld-org-membership-service.md` rev 2.30 — §5.3 (I-1, IAPI-1…3), I-1 spec and invariants I1-1…I1-5, §8.1, §10.2; RFC 9457 adopted (rev 2.29); bff namespace admitted + CR-4 trial-eligibility endpoint (rev 2.30)
- `claude/signup-bff-gap.md` and "Signup BFF — Open Gaps for TL Review" — the option analysis behind §16
- *Signup BFF — TL Responses: HLD Gaps (BFF-1 to BFF-6)*, 21 Oct 2026 — decisions applied in v0.3
- IAM PRs: iam-realm-provisioner #19, #20; iam-org-membership #25; iam-audit-log #5 (`TenantIdentityProvisioned`)

---

## 21. Review and Sign-off

| Role | Scope of sign-off | Name | Status |
|---|---|---|---|
| Web / growth TL | Whole LLD | | ☐ |
| IAM — Realm Provisioner | §5.6, §10.6, §16.1 CR-1, CR-2, CR-3 (RP), F-2 | | ☐ |
| IAM — Org & Membership | §5.6, §9.4, §16.1 CR-3 (O&M), CR-4, C-6 doc fix | | ☐ |
| IAM platform security | §10.1 identity and allow-lists | | ☐ |
| SRE | Trust domain, DNS/TLS/WAF/Envoy, ServiceEntry, ElastiCache, SNS filter | | ☐ |

