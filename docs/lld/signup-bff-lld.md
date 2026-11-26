# Signup BFF Service — Low-Level Design

## Tender Management SaaS Platform — Edge / BFF Tier

| Field | Value |
|---|---|
| Document type | Low-Level Design (LLD) |
| Service | `bff-signup` (Signup Service / "Signup BFF") |
| Repository / Go module | `BCBP-SOLUTIONS-FZC-LLC/bff-signup` → `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup` (BFF-6, decided) |
| Status | **Code complete for Phase 1: trial (SB-1…SB-4) and direct-paid (SB-5).** Not deployed to any environment; no release tag. SB-5 is aligned to Billing B-I1 (BL-OQ-8) and stays off (`PAID_SIGNUP_ENABLED=false`) until Billing go-live step 4 (§19.5). **Open before production:** SRE infra BFF-1/2/4 (§16.2). **Deferred:** on-call/PagerDuty (BFF-6, pending procurement), Keycloak existing-realm policy sweep (N-10a, pending Keycloak phase). |
| Version | 1.3 |
| Date | 2026-11-27 |
| Base HLD | `bff-hld-tender-saas-v1.0.md` (Edge / BFF Tier HLD v1.0) + *Signup BFF — TL Responses: HLD Gaps (BFF-1 to BFF-6)*, 21 Oct 2026 |
| Scope | **Phase 1** — trial signup (SB-1…SB-4, BFF HLD §7.1) and direct-paid signup (SB-5, §19; moved into Phase 1 by Commercial Domain HLD v0.43) |
| Sibling LLDs | `iam-lld-realm-provisioner-service.md` **rev 1.37** (RP-1 callee; payload expanded), `iam-lld-org-membership-service.md` **rev 2.33** (I-1 + PUB-1 callee; backstop live) |
| Owner database | **None.** Ephemeral Valkey only (§4) |
| Namespace | `bff` (never `iam`) — service account `bff-signup` (BFF-1, decided) |
| Owning team | Web / growth |
| Deployment stage | Not started — nothing deployed to any environment |
| Audience | Web / growth engineering (owner), IAM platform engineering (RP-1 / I-1 owners), IAM platform security (mesh trust), SRE |

### Revision history

| Version | Date | Changes |
|---|---|---|
| 1.3 | 2026-11-27 | **Production-readiness review fixes.** Paid path: trial and paid handoffs never overwrite each other on one session (`409 other_signup_in_progress`); I-1 gets `email_hash` on the trial path only (it made O&M answer `409 TRIAL_ALREADY_USED` to paid customers who once had a trial); Billing errors delete the handoff only when nothing payable exists (declined, expired, price not available); RP-2 freeze (`rp2_attempted`) so `signup:<tenant_id>:rp2` is never reused with another slug/name/email; RP-2 `REALM_ALREADY_DEDICATED` → slug conflict, `OPERATION_IN_PROGRESS` → retryable 503; seats 1–1000; RP-2 `email_verification_sent` read; SB-3 `action` for payment states. Timeouts: `RP_TIMEOUT_MS` 900 → 2000 (RP-1 p95 1.5 s), new `RP2_TIMEOUT_MS` 8000 (RP-2 p95 4 s), `REQUEST_DEADLINE_MS` 2500 → 5000 (validated against each call chain), handoff lock TTL = longest deadline + 5 s (was 15 s, shorter than SB-5). Hardening: client IP validated, terms User-Agent sanitised, Turnstile errors not leaked and `internal-error` = 503, CR-4 400/403 not retried, HSTS. Catalog adapter on CAT-I2. Chart: Turnstile `ServiceEntry` template (was missing), AuthorizationPolicy on in staging/prod, HTTPRoute timeouts (35 s `/paid`, 10 s others) and hostname from `env.PUBLIC_HOST` (was null), probes not routed publicly, egress scoped to destinations incl. istiod, no SA token. §19.4/§19.6 updated. |
| 1.2 | 2026-11-26 | **BL-OQ-8: SB-5 aligned to Billing B-I1** (Billing LLD rev 0.51 §5.4.2; hand-off `commercial-billing-service/docs/handoff/bl-oq-8-signup-bff.md`). Wire fields renamed to `plan_code` / `licensed_seats` / `payment_method_ref`; identity headers dropped (caller identity is the mesh SPIFFE ID, BIL-AUTH-3). New state `payment_action_required` and 3-D Secure re-POST flow. A repeat SB-5 resumes the same `tenant_id`, so every B-I1 retry reuses `charge:<tenant_id>` and the same body; the handoff is kept on unknown outcomes (timeout, 5xx, `REQUEST_IN_PROGRESS`) and on RP-2 failure after payment. `PAYMENT_DECLINED` → `422`, `CHARGE_EXPIRED` → `410`, `Retry-After` passed through on `503`. `BILLING_TIMEOUT_MS` ≥ 15000 enforced; new `PAID_REQUEST_DEADLINE_MS` (default 30000) for SB-5, also used as gincommon's outer `RequestTimeout` (§19.5). **Fixes found in the 2026-11-27 docs audit:** SB-5 no longer allocates a new `tenant_id` past payment (`409 details_changed`, §19.4); a PUB-1 slug conflict keeps the handoff in `allocated` / `payment_confirmed` so a resubmit with a new slug works (SB-4 could not, no Keycloak user); SB-5 reads `RATE_*`, emits `mode="paid"` metrics and slides the session cookie. **Docs aligned with the code:** status/scope (no longer "Phase 1.5"), RFC 9457 error shape and UPPER_SNAKE `errorCode`s (§5, §17), outbound headers (§5.6), §9.1 state machine (`rejected` is unused; no `allocated → payment_pending`), package layout (§3), dependency rules (§3.2), handoff record fields (§4.2), test layout (§14). |
| 1.1 | 2026-10-31 | **Spec and behaviour aligned after E2E testing.** Code now follows the spec: `Retry-After` header on every `429`; eligibility and RP-1 retried once on `5xx`/timeout, RP-1 also once on `409 operation_in_progress` (§9.3, §17.2). Spec text corrected to match intended behaviour: allocated resubmit with different details keeps the session's `signup_id` and issues a new `tenant_id` (§5.4); a slug conflict is not a provisioning-failure metric (§9.5); an expired session cookie gives `401` before `404` (§5.5); Turnstile is not retried because its token is single-use (§9.3). |
| 1.0 | 2026-10-28 | **Production hardening pass — Phase 1 code complete.** Security: CSRF and email HMAC now use `subtle.ConstantTimeCompare`; `X-Frame-Options: DENY` and `Referrer-Policy: strict-origin-when-cross-origin` added to all responses; `ReadHeaderTimeout: 5s` added (Slowloris); `CAPTCHA_SKIP=true` rejected outside dev/test/local by `config.Validate()`; rate limiter converted from sliding-window to fixed-window Lua script; handleResubmit acquires distributed lock before deleting StateAllocated handoff (race safety). Correctness: SB-4 now returns 409 with suggestions (was returning 202); `SuggestSlugs` capped at 50 iterations; session TTL and all four rate-limit thresholds are now configurable from env vars (were hardcoded constants); eligibility timeout wired from `ELIGIBILITY_TIMEOUT_MS`; config bounds validation added for all timeouts and `SESSION_TTL_SECONDS`; Valkey errors in SB-3/SB-4 now return 503 (were 404). Observability: `bff_signup_dependency_call_duration_seconds` was registered but never called — all four outbound adapters (orgmembership, realmprovisioner, turnstile, catalog) now call `ObserveDependencyCall` after `httpClient.Do()`; startup now emits structured `log.Info` at each init phase; all logs/metrics/traces pass through platform-gincommon (enforced by new CI lint). Deployment: `terminationGracePeriodSeconds: 35` and `topologySpreadConstraints` added; `values-prod.yaml` now self-contained; duplicate YAML keys removed. CI: `check-log-sink.sh` and `log-sink-lint` Makefile target added; `check-metric-naming.sh` enhanced with adapter-instrumentation enforcement and label-vocabulary checks; coverage gate corrected to 95%. 20+ tests added. Go 1.26.10. `prometheus/common` v0.73.0. |
| 0.9 | 2026-10-26 | **Deferred items marked.** Four items formally deferred to future phases after cross-service gap review (BFF + O&M + RP): (1) BFF-6 on-call / PagerDuty — deferred until PagerDuty procured; (2) N-10a Keycloak existing-realm policy sweep — deferred to Keycloak phase; (3) Phase 1.5 direct-paid signup — deferred until Billing service is built; (4) SRE infra gaps (BFF-1/2/4) — deferred to infra phase. §16.1 and §16.2 updated with DEFERRED labels and reasons. §19 updated with deferred activation note. BFF code fixes applied: `x-tenant-roles: iam-system` header added to I-1 and eligibility calls; `licensed_seats` field added to `ProvisionTenantRequest`, `Handoff`, and `callI1Once`; paid signup handoff wires `LicensedSeats` from `in.Seats`. |
| 0.8 | 2026-10-23 | **All go-live blockers cleared; backstop fully functional.** RP-1: `TrialTenantProvisionedPayload` expanded — `KeycloakUserID` renamed to `OwnerUserID`, `Slug`/`Name`/`Plan` added (`event.go:69–75`, `provisioning_service.go:403`); schema and AsyncAPI spec updated. RP-4: `validPlan()` wired into RP-1 handler (one line). O&M rev 2.33: `TrialTenantProvisioned` handler activated as backstop with three idempotency layers (processOnce dedup, tenantExists check, ON CONFLICT); `iam_org_membership_tenant_provisioning_backstop_total` metric live; `IAMSignupProvisioningRejected` alert added; docs corrected (BFF produces zero events). BFF: terms version validation tightened — `TermsVersion` now checked against `CurrentTermsVersion`; SB-1 response now includes `current_terms_version` so browser knows which version to echo back. §7 event table updated (payload now live). §9.4 backstop status updated from "pending" to "live". §18.1 and §18.2 stale "open" notes cleared. Sibling LLD versions: RP rev 1.37, O&M rev 2.33. |
| 0.7 | 2026-10-23 | **IAM reply (A-1…A-4) applied; BFF code gaps closed.** Sibling LLD versions updated: RP rev 1.31 → 1.37, O&M rev 2.30 → 2.38. A-1: pod-level SPIFFE admission corrected on both O&M and RP (PR #33/#27); §10.1 table updated. A-2: `gateway` removed from RP `existingCallerNamespaces` (PR #28). A-3 (BFF-2a): PUB-1 server-side slug pre-check implemented (`CheckSlugFree` in `orgmembership/client.go`, called in `handoff_orchestrator.go` before RP-1); §5.6 step table and §8.1 sequence diagram updated; §9.4 "Remaining open" note removed. A-4 (N-5): `terms_acceptance` added to `port.ProvisionTrialRequest` and sent on every RP-1 call (`version`, `accepted_at`, `ip_address`, `user_agent` ≤512 bytes); §5.6 RP-1 request body updated; §18.1 updated. N-7: `handoffs.Get` error in `signup_service.go` no longer silently ignored. N-11 (timeout budget): `runOp` 50 ms Valkey op deadline + 1 retry; 2,500 ms SB-2 `context.WithTimeout` in `SubmitTrial` handler. N-8/N-9 confirmed already implemented. N-2/N-4 closed. §16 restructured: open = SRE prod tasks + BFF-6 + N-3/N-10; resolved = all else. |
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
19. Direct-Paid Signup (SB-5)
20. Appendix — Glossary and References
21. Review and Sign-off

---

## 1. Document Overview

The Signup BFF is the platform's **public, unauthenticated tenant-onboarding intake**. It serves the signup form's API, enforces abuse controls (rate limit, CAPTCHA), and runs the **provisioning handoff**: it calls Realm Provisioner **RP-1** to create the owner in Keycloak, then Org & Membership **I-1** to create the tenant. On the direct-paid path (SB-5, §19) it calls Billing **B-I1** to charge, then Realm Provisioner **RP-2**, then I-1. It owns no business data, makes no Keycloak Admin API call, issues no token, and produces or consumes no bus event (BFF HLD §3.2, §9).

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

- Public JSON API for the signup form: trial (SB-1…SB-4, §5) and direct paid (SB-5, §19).
- Server-side validation of company, admin, slug, plan and password-policy inputs.
- Abuse controls: per-IP and per-email rate limits, CAPTCHA verification, CSRF protection.
- Allocation of the caller-supplied `tenant_id` (UUIDv7) and the tenant slug.
- Hashing the password into the `hashed_password_seed` format RP-1 accepts (C-4).
- The provisioning handoff: trial eligibility check → PUB-1 → RP-1 → I-1; paid Billing B-I1 → PUB-1 → RP-2 → I-1 (§19). Both with retry, resume and slug-conflict handling.
- Funnel metrics, traces and redacted logs.

### 2.2 Out of scope (owned elsewhere)

| Concern | Owner |
|---|---|
| Login, logout, tokens, sessions, MFA | Keycloak / Envoy |
| Email verification, **forgot password**, invitation acceptance | Keycloak (built-in flows) |
| Keycloak Admin API | Realm Provisioner only (RP-INV-1) |
| `tenants`, memberships, roles, trial end date | Org & Membership |
| One-trial-per-email ledger | Org & Membership (`trial_signup_ledger`) |
| Payment, subscriptions | Billing (B-I1, §19) |
| Signup page UI | Web frontend app (assumed — N-6) |

### 2.3 Core invariants

| # | Invariant |
|---|---|
| SB-INV-1 | **No durable business data.** Nothing about a tenant, user or payment is written to a store the BFF owns beyond the TTL-bounded Valkey keys in §4. |
| SB-INV-2 | **The plain password never leaves process memory.** It is not logged, not stored in Valkey, not sent to any service; only `hashed_password_seed` is sent to RP-1 / RP-2. Likewise the payment token (only an HMAC is stored) and Billing's `client_secret` (returned to the browser, never logged or stored). |
| SB-INV-3 | **One `tenant_id` per signup attempt.** A `tenant_id` is allocated once per `signup_id` and reused for every retry, resume and slug change, so RP and I-1 always agree and never duplicate. On SB-5 this also keeps the Billing key `charge:<tenant_id>` stable, so a retry can never start a second charge (§19.4). |
| SB-INV-4 | **RP (RP-1 or RP-2) before I-1, always.** `owner_user_id` sent to I-1 is the `keycloak_user_id` RP-1 (trial) or RP-2 (paid) returned — never invented by the BFF (C-6). |
| SB-INV-5 | **CAPTCHA and rate limit run before any internal call or password hashing**, so a bot cannot reach IAM or burn CPU. Exceptions: a same-email resume of an existing identity calls nothing downstream and skips both; an SB-5 payment resume runs CAPTCHA but does not use rate-limit budget (§19.4). |
| SB-INV-6 | **No Keycloak and no admin-plane access.** The BFF's mesh identity is allow-listed only for the internal routes in §5.6 (BFF-1). |
| SB-INV-7 | **Client-side checks are UX only**, with one exception: the password policy. Because the credential reaches Keycloak pre-hashed (C-4), the BFF's password check is the only one at signup (§10.6). For everything else RP-1 and I-1 remain authoritative; their rejection is final. |
| SB-INV-8 | **RP-1 and I-1 carry identical `plan`, slug and name.** O&M keeps the values from whichever creates the tenant first — the BFF's I-1 or the backstop driven by RP's `TenantIdentityProvisioned` (live, expanded payload) — and ignores later differing values. `plan` is always `starter`, `pro` or `enterprise`, never `trial` (RP-1 rejects anything else). The only permitted difference is a slug changed through SB-4 after `409 slug_taken`, which is safe because RP's event with the original slug is permanently rejected by O&M (§9.4). |

---

## 3. Architecture and Package Layout

Hexagonal layout, matching the IAM services (ports in `core/port`, adapters in `adapter/`).

```mermaid
flowchart LR
    BROWSER["Browser<br/>(signup page)"] --> ENVOY["Envoy Gateway<br/>(envoy-gateway-system)"]
    ENVOY --> HTTP["inbound/http<br/>router · middleware · handlers"]
    HTTP --> SVC["core/service<br/>SignupService · PaidSignupService · HandoffOrchestrator"]
    SVC --> PORTS["core/port interfaces"]
    PORTS --> CAP["captcha/turnstile"]
    PORTS --> VK["valkey<br/>sessions · handoff · rate limits"]
    PORTS --> CAT["catalog<br/>plan list"]
    PORTS --> ELIG["orgmembership<br/>trial-eligibility (CR-4)"]
    PORTS --> RP["realmprovisioner<br/>RP-1 · RP-2"]
    PORTS --> OM["orgmembership<br/>I-1 · PUB-1"]
    PORTS --> BIL["billing<br/>B-I1 (SB-5)"]
```

```
bff-signup/
├── cmd/server/main.go                 # wiring, graceful shutdown
│             wiring.go               # client factories; buildBilling() = B-I1 adapter when
│                                     # PAID_SIGNUP_ENABLED, else BillingStub
├── internal/
│   ├── config/                        # env config, Validate() fail-fast; paid settings §19.5
│   ├── core/
│   │   ├── domain/                    # pure types, no I/O
│   │   │   ├── signup.go              # SignupRequest, validation rules
│   │   │   ├── slug.go                # derive, normalize, validate, suggest (N-1)
│   │   │   ├── handoff.go             # Handoff record + state machine (§9.1)
│   │   │   │                          # paid: payment_pending, payment_action_required,
│   │   │   │                          # payment_confirmed; SignupType (trial|paid),
│   │   │   │                          # BillingSubscriptionID, PaymentRefHMAC
│   │   │   ├── plan.go
│   │   │   └── errors.go              # domain error codes (§17)
│   │   ├── port/
│   │   │   ├── realm_provisioner.go   # ProvisionTrial (RP-1) + ProvisionDirect (RP-2, §19)
│   │   │   ├── org_membership.go      # ProvisionTenant (I-1), CheckEligibility (CR-4), CheckSlugFree (PUB-1)
│   │   │   ├── captcha.go             # CaptchaVerifier.Verify(ctx, token, ip)
│   │   │   ├── catalog.go             # Plans(ctx)
│   │   │   ├── handoff_store.go       # Get/Create/Update/Lock (Valkey)
│   │   │   ├── session_store.go
│   │   │   ├── rate_limiter.go
│   │   │   ├── password_hasher.go     # Hash(password) → seed (C-4)
│   │   │   ├── billing.go             # BillingClient (B-I1) + ChargeOutcome + BillingStub (§19)
│   │   │   └── clock.go               # Clock + IDGenerator
│   │   └── service/
│   │       ├── session_service.go     # SB-1
│   │       ├── signup_service.go      # SB-2 trial entry, validation, abuse checks
│   │       ├── paid_signup_service.go # SB-5 direct paid signup: B-I1 → PUB-1 → RP-2 → I-1 (§19)
│   │       ├── handoff_orchestrator.go# eligibility → PUB-1 → RP-1 → I-1, resume, slug change
│   │       └── status_service.go      # SB-3, SB-4
│   └── adapter/
│       ├── inbound/http/
│       │   ├── router.go              # routes, noStoreHeaders; POST /paid 404 unless PAID_SIGNUP_ENABLED
│       │   ├── handlers.go            # Session · SubmitTrial · SubmitPaid · GetStatus · ChangeSlug
│       │   ├── errors.go              # domain error → RFC 9457 problem (§17)
│       │   ├── dto.go, strict_json.go # request/response shapes; unknown JSON fields rejected
│       │   └── middleware/            # session, csrf, clientip, body_limit, content_type
│       └── outbound/
│           ├── realmprovisioner/      # RP-1 + RP-2 (ProvisionDirect, §19)
│           ├── orgmembership/         # I-1, eligibility (CR-4), PUB-1
│           ├── billing/               # Billing B-I1 authorize-and-charge (§19.1)
│           ├── catalog/
│           ├── turnstile/
│           ├── valkey/                # sessions, handoff + lock, rate limiter
│           ├── metrics/               # Prometheus collectors (§11)
│           └── hasher/                # PBKDF2-SHA512
├── pkg/signupctx/                     # request-scoped context helpers
├── deploy/helm/, deploy/monitoring/
├── test/{unit,integration,e2e,fixtures}/
└── .github/workflows/, .github/scripts/
```

### 3.1 Shared library dependencies

| Library | Used for | Notes |
|---|---|---|
| `platform-gincommon/v2` (v2.7.0) | PanicRecovery, RequestID, Tracing, Metrics, Logging, request timeout, RFC 9457 problem responses (`AbortWithProblem`) | **Not** `RequireAuth` / `ContextMiddleware` on public routes — no identity exists at signup. The only `platform-*` module in `go.mod`. |
| `platform-models` | Not used | — |
| `platform-events` | Not used (no bus participation) | — |
| `platform-pgcommon` | Not used (no database) | — |

### 3.2 Dependency rules (CI-enforced, `go-arch-lint`)

- `core/domain` imports nothing internal.
- `core/service` imports `domain`, `port` and the metrics package (`internal/adapter/outbound/metrics`, the `observability` component) only.
- Inbound HTTP imports `service`, `port`, `domain`, `signupctx`, `middleware` and the generated Swagger package; outbound adapters import `port`, `domain` and the metrics package. Adapters never import each other.
- Only `cmd/server` wires adapters to ports.
- No package imports a Keycloak client library (SB-INV-6). This is a review rule: `.go-arch-lint.yml` sets `depOnAnyVendor: true`, so go-arch-lint does not check vendor imports.

---

## 4. Data Model (Ephemeral State)

There is **no database**. A dedicated **Amazon ElastiCache for Valkey** node (`cache.t3.micro`, HLD §13), not shared with the IAM cache, holds only the keys below. TLS and AUTH on, **no snapshots / backups**: losing it loses only in-flight signups and counters (§15).

### 4.1 Key catalogue

| Key | Type | Value | TTL | Written by |
|---|---|---|---|---|
| `sbff:sess:<sid>` | hash | `csrf_hash`, `signup_id`, `created_at` | 30 min (sliding) | SB-1 |
| `sbff:handoff:<signup_id>` | string (JSON) | Handoff record (§4.2) | 30 min, refreshed on each state change; 10 min after `completed` | SB-2, SB-3, SB-4, SB-5 |
| `sbff:lock:<signup_id>` | string | owner token | longest request deadline + 5 s, at least 15 s (`SET NX PX`; 35 s with paid signup on) | SB-2, SB-3, SB-4, SB-5 |
| `sbff:rl:ip:<ip>` | counter | new signup attempts from this IP | 1 h | SB-2, SB-5 (not on an SB-5 payment resume) |
| `sbff:rl:email:<hmac(email)>` | counter | attempts for this email | 24 h | SB-2, SB-5 (not on an SB-5 payment resume) |
| `sbff:rl:sess:<ip>` | counter | sessions started | 1 h | SB-1 |
| `sbff:rl:poll:<sid>` | counter | status polls | 1 min | SB-3 |

`<sid>` is a 256-bit random session id. Email keys use **HMAC-SHA256** with a server secret (`SBFF_HMAC_KEY`, from OpenBao), so Valkey never holds a readable email.

### 4.2 Handoff record

```jsonc
{
  "signup_id":        "01JABCD...",          // ULID, bound to the session
  "tenant_id":        "0192f0c4-...-7...",   // UUIDv7, allocated once (SB-INV-3)
  "type":             "trial",               // "trial" (SB-2) | "paid" (SB-5)
  "state":            "identity_created",    // §9.1
  "slug":             "acme",
  "company_name":     "Acme Bidding Co",
  "plan":             "pro",
  "default_locale":   "en-US",
  "admin_email_hmac": "b64...",              // never the email itself
  "keycloak_user_id": "5717...",             // set after RP-1 / RP-2
  "billing_subscription_id": "sub_...",      // SB-5 only: from B-I1 (also kept while 3-D Secure is pending)
  "payment_ref_hmac": "9f2c...",             // SB-5 only: HMAC of the payment token, never the token
  "licensed_seats":   10,                    // SB-5 only: seats charged, forwarded to I-1
  "slug_attempts":    1,
  "i1_attempts":      2,
  "last_error":       "org_membership_unavailable",
  "created_at":       "2026-10-16T09:00:00Z",
  "updated_at":       "2026-10-16T09:00:02Z"
}
```

**Not stored:** password, `hashed_password_seed`, admin email, admin name, CAPTCHA token, payment token (only `payment_ref_hmac`), Billing `client_secret`. Only what I-1, SB-3/SB-4 or an SB-5 resume needs is kept. Company name and slug are organisational data, held at most 30 minutes.

---

## 5. API Contract

### 5.1 Conventions

- Public routes under `/api/v1/signup`, served on `https://signup.bcbpsolutions.com`, same origin as the page (BFF-4, decided). Breaking changes ship under `/api/v2`.
- `application/json`, UTF-8, request body limit **16 KB**. Timestamps RFC 3339 UTC.
- Every response: `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`, `X-Request-Id`.
- Errors are RFC 9457 problem details (`application/problem+json`): `type`, `title`, `status`, `detail`, `traceId`, `errorCode` (UPPER_SNAKE, e.g. `SLUG_TAKEN`), plus extension members at the top level (e.g. `signup_id`, `suggestions`, `retry_after_seconds`, `decline_reason`) (§17).
- State-changing routes (`POST`) require the session cookie **and** `X-CSRF-Token` (§10.4).
- No JWT, no `x-user-id` / `x-tenant-id` accepted from the browser; any such inbound header is stripped by Envoy and ignored by the BFF.

### 5.2 Endpoint catalogue — public (inbound)

| # | Method + Path | Purpose | CSRF |
|---|---|---|---|
| SB-1 | `GET /api/v1/signup/session` | Start a signup session: sets cookie, returns CSRF token, CAPTCHA site key, plans, password and slug rules | — |
| SB-2 | `POST /api/v1/signup/trial` | Submit the trial form; runs abuse checks and the handoff. `201` done / `202` still finishing / `409` slug conflict | Yes |
| SB-3 | `GET /api/v1/signup/{signup_id}/status` | Poll after `202`; resumes an unfinished I-1 | — (session-bound) |
| SB-4 | `POST /api/v1/signup/{signup_id}/slug` | Pick a new slug after `409 slug_taken`; retries I-1 with the same `tenant_id` | Yes |
| SB-5 | `POST /api/v1/signup/paid` | Direct paid signup (§19): Billing B-I1 → PUB-1 → RP-2 → I-1. `201` done / `202` provisioning or `payment_action_required` (3-D Secure) / `409` / `410 CHARGE_EXPIRED` / `422 PAYMENT_DECLINED` / `503` + `Retry-After`. Returns `404` until `PAID_SIGNUP_ENABLED=true`. Same session + CSRF guards as SB-2 | Yes |
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

// 409 Conflict — slug taken; user must choose another (§8.3). RFC 9457 problem body.
{ "type": "…", "title": "Conflict", "status": 409, "errorCode": "SLUG_TAKEN", "detail": "…",
  "signup_id": "01JABCD...", "suggestions": ["acme-bidding", "acme-2", "acme-hq"], "message": "…" }
```

Other errors (`errorCode`): `400 VALIDATION_FAILED`, `401 SESSION_EXPIRED`, `403 CSRF_INVALID`, `403 CAPTCHA_FAILED`, `409 EMAIL_UNAVAILABLE`, `409 TRIAL_NOT_ELIGIBLE`, `409 INVALID_STATE`, `429 RATE_LIMITED` (with `Retry-After`), `503 DEPENDENCY_UNAVAILABLE` (§17).

**Re-submit behaviour (G-1):** a second SB-2 in the same session reuses the session's `signup_id`. The BFF compares the submitted email HMAC, slug, company name and plan with the handoff:

| Handoff state | Same details | Different details |
|---|---|---|
| none / `rejected` | New attempt | New attempt |
| `allocated` (RP-1 not confirmed) | Retry with the **same** `tenant_id` and RP-1 key (password needed again) | **Discard** the handoff; keep the session's `signup_id` (it is bound to the session) and issue a new `tenant_id` (a new RP-1 key), so RP cannot replay a result made with the old email |
| `identity_created` / `tenant_pending` / `slug_conflict` | Resume (password ignored, no new CAPTCHA or rate-limit hit) | `409 invalid_state` `details.reason = "details_changed"`; the UI tells the user to check the original email or start a new session |
| `completed` | Return the original `201` | `409 invalid_state` (`details_changed`) |

Turnstile tokens are single-use: the frontend must fetch a fresh token before every SB-2 (including a retry after `503`).

### 5.5 SB-3 `GET /api/v1/signup/{signup_id}/status` and SB-4 `POST /api/v1/signup/{signup_id}/slug`

Both require the session whose `signup_id` matches the path; otherwise `404 signup_not_found` (no hint whether it exists). SB-3 is limited to 60 polls per minute per session. The session cookie (30 min, sliding) and the handoff (30 min) expire together; a request with an expired cookie gets `401 session_expired` before any handoff lookup, so `404 signup_not_found` after expiry is only seen with a still-valid session.

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

IAM calls (RP-1, RP-2, I-1, eligibility) carry:

| Header | Value | Source |
|---|---|---|
| `x-tenant-id` | the handoff's `tenant_id` (eligibility: the nil UUID — no tenant yet) | RP RLS-5 / O&M IAPI-3 (C-2) |
| `x-user-id` | `00000000-0000-0000-0000-0000000000a1` (system principal) | RP AUTH-5 / O&M IAPI-2 (C-2) |
| `x-tenant-roles` | `iam-system` | RP / O&M internal-route role check |
| `Idempotency-Key` | `signup:<tenant_id>:rp1` (RP-1) / `signup:<tenant_id>:rp2` (RP-2) — stable across retries, ≤ 256 chars (RP §9.2, §10.3). **Not sent to O&M**: O&M has no idempotency ledger; I-1 is idempotent by state on `tenant_id` (O&M §9.2, I1-1) | C-2 |
| `traceparent`, `x-request-id` | propagated | platform tracing |

PUB-1 carries only `x-user-id`. Billing B-I1 carries only `x-tenant-id` and `Idempotency-Key: charge:<tenant_id>`; caller identity is the mesh SPIFFE ID (BIL-AUTH-3, §19.1).

| Step | Call | Owner | Idempotency-Key | Status |
|---|---|---|---|---|
| E | `POST /api/v1/internal/trial-eligibility` | Org & Membership | — (read) | **Done (CR-4)** |
| P | `GET /api/v1/public/tenants/resolve?identifier=<slug>` (PUB-1 pre-check) | Org & Membership | — (read, fail-open) | **Done (A-3)** |
| 1 | `POST /api/v1/internal/tenants/provision-trial` (RP-1) | Realm Provisioner | `rp1` | **Done** — `409 email_already_registered` (CR-1), `terms_acceptance` (A-4) |
| 2 | `POST /api/v1/internal/tenants` (I-1) | Org & Membership | — (idempotent on `tenant_id`) | Committed |
| RP-2 | `POST /api/v1/internal/tenants/provision-direct` (SB-5) | Realm Provisioner | `rp2` | Built (RP PR #27); returns `keycloak_user_id` — §19 |
| B-I1 | `POST /api/v1/internal/subscriptions/authorize-and-charge` (SB-5) | Billing | `charge:<tenant_id>` | Contract final (Billing LLD rev 0.51 §5.4.2) — §19.1 |

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

**P — PUB-1 slug pre-check (A-3, done):**

`GET /api/v1/public/tenants/resolve?identifier=<slug>` — called server-side before RP-1 to avoid creating an orphaned Keycloak user for a slug that is already taken. Fail-open: any non-200 or network error returns nil and the signup proceeds, relying on I-1's unique constraint as the authoritative guard. On `{"found": true}` the BFF returns `409 slug_taken` with suggestions before any Keycloak user is created.

**1 — RP-1** (RP LLD §5.4; `terms_acceptance` added — A-4):

```jsonc
// Request
{ "tenant_id": "0192f0c4-...", "tenant_name": "Acme Bidding Co", "tenant_slug": "acme",
  "admin_email": "owner@acme.example", "admin_name": "Dana Owner",
  "hashed_password_seed": "<see §10.6 — C-4>", "plan": "pro",
  "terms_acceptance": {
    "version":     "2026-09",
    "accepted_at": "2026-10-23T09:15:02Z",  // server time when BFF validated accept_terms: true
    "ip_address":  "203.0.113.7",            // end-user IP from trusted proxy headers only
    "user_agent":  "Mozilla/5.0 …"           // max 512 bytes UTF-8, truncated at rune boundary
  }
}
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
| `202` | Keycloak user created, tenant still being created; poll SB-3. SB-5 also: `payment_action_required` (3-D Secure, §19.3) |
| `400` | `VALIDATION_FAILED` |
| `401` | `SESSION_EXPIRED` (no/expired cookie) |
| `403` | `CSRF_INVALID`, `CAPTCHA_FAILED` |
| `404` | `SIGNUP_NOT_FOUND`; `/paid` when `PAID_SIGNUP_ENABLED=false` |
| `409` | `EMAIL_UNAVAILABLE`, `TRIAL_NOT_ELIGIBLE`, `SLUG_TAKEN`, `INVALID_STATE` |
| `410` | `CHARGE_EXPIRED` (SB-5: 3-D Secure not completed within 30 min) |
| `413` | body over 16 KiB |
| `422` | `PAYMENT_DECLINED` (SB-5, + `decline_reason`) |
| `429` | `RATE_LIMITED` (+ `Retry-After`) |
| `500` | `INTERNAL_ERROR` (our bug, or an unexpected 4xx from IAM or Billing) |
| `503` | `DEPENDENCY_UNAVAILABLE` (Valkey, CAPTCHA provider, RP-1/RP-2 or Billing down; + `Retry-After` when known) |

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

Valkey (§4) is a **state store**, not a cache: sessions, handoffs, counters and locks. Valkey unavailable ⇒ SB-1…SB-5 return `503 DEPENDENCY_UNAVAILABLE` (**fail-closed**, N-7). Rationale: without counters the rate limit is gone, and without the handoff record SB-INV-3 cannot be guaranteed.

---

## 7. Event Architecture

**None.** The Signup BFF publishes nothing and subscribes to nothing (BFF HLD §9). Events caused by its calls are emitted by others:

| Event | Producer | Caused by | Notes |
|---|---|---|---|
| `TenantIdentityProvisioned` | Realm Provisioner (outbox) | RP-1, after Keycloak user created | Payload: `{tenant_id, realm, owner_user_id, slug, name, plan}` — expanded 23 Oct 2026 (RP-1 done). Consumed by O&M backstop (live, rev 2.33) and Audit Log (`tenant.trial_provisioned`, 3yr retention). |
| `TenantCreated`, `TrialStarted` | Org & Membership | I-1 (BFF call or backstop) | — |
| `TenantRoleGranted` (owner) | Org & Membership | I-1 | — |

O&M's `tenant-orgm-q` consumer of `TenantIdentityProvisioned` is the **backstop** (BFF-2, live — O&M rev 2.33): if no tenant row exists it runs idempotent I-1 from the payload; if a row exists it skips. Three idempotency layers: `processOnce` dedup, `tenantExists` check, `ON CONFLICT (id) DO NOTHING`. **SNS filter:** `TenantIdentityProvisioned` already in the `tenant-orgm-q` subscription filter.

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
    Note over S: validate · PBKDF2-SHA512 → hashed_password_seed (in memory only)
    S->>V: SET NX sbff:lock:<signup_id> PX <lock TTL>
    S->>V: create handoff {tenant_id=UUIDv7, state=allocated}
    S->>OM: POST /internal/trial-eligibility {email_hash}  (CR-4, done)
    OM-->>S: {eligible:true}
    S->>OM: GET /public/tenants/resolve?identifier=<slug>  (PUB-1 pre-check, A-3)
    OM-->>S: {found:false} → proceed  |  {found:true} → 409 SLUG_TAKEN (no Keycloak user; handoff stays allocated)
    S->>RP: RP-1 provision-trial + terms_acceptance + x-tenant-id, x-user-id=…00a1, Idempotency-Key signup:<tid>:rp1
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
    Note over S,OM: If the browser leaves: handoff expires (30 min).<br/>Backstop = O&M creates the tenant from TenantIdentityProvisioned (BFF-2, live — RP-1+OM-1 done 23 Oct 2026).
```

**Early verification (G-3).** RP-1 sends the verification email before I-1 completes. While the handoff is `tenant_pending`, the SB-3 response carries `"message": "workspace_setting_up"` so the page can say *"Your workspace is still being set up — you can verify your email, but wait for this page before signing in."* If the user signs in before the tenant row exists, AuthZ Enrichment finds no membership (O&M I-8 invariant I8-3: returns `404` → AuthZ → `403` to the browser — hard fail, no grace period). This window normally closes within seconds via the BFF retry or the backstop. The BFF's `tenant_pending` warning message is sufficient UX coverage (N-12 closed).

After **15 minutes** in `tenant_pending` (≈ 40 resume attempts), SB-3 reports `status:"failed"` with a support reference (`trace_id`), and `bff_signup_provisioning_failed_total{stage="org_membership",reason="exhausted"}` increments (alerted, §11.5).

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

| State | Keycloak user? | Tenant row? | Who moves it on | Path |
|---|---|---|---|---|
| `allocated` | No | No | SB-2 resubmit (same details retry with the same `tenant_id`; changed details start fresh) | Trial |
| `payment_pending` | No | No | SB-5 — Billing call in flight, or outcome unknown (resubmit retries B-I1 with the same key) | Paid (§19) |
| `payment_action_required` | No | No | SB-5 re-POST after the browser completes 3-D Secure | Paid (§19.3) |
| `payment_confirmed` | No | No | SB-5 resubmit — skips Billing, runs PUB-1 and RP-2 (slug and email may change) | Paid (§19.4) |
| `identity_created` | **Yes** | No | SB-2 inline I-1 / SB-5 after RP-2 | Both |
| `tenant_pending` | **Yes** | No | SB-3 polls; O&M backstop (BFF-2) | Both |
| `slug_conflict` | **Yes** | No | SB-4 (reached only from I-1 `409`, so a Keycloak user always exists) | Both |
| `completed` | Yes | Yes | — | Both |
| `failed` | Yes | No | — (relies on the backstop) | Both |
| `rejected` | — | — | Declared in `handoff.go` but never set: a rejection deletes the handoff | — |

Once a paid handoff has passed `payment_confirmed`, a repeat SB-5 never allocates a new `tenant_id`: the same email gets the current state, other details get `409 INVALID_STATE` (`reason: details_changed`), as on SB-2 (§19.4).

### 9.2 Idempotency

| Layer | Mechanism |
|---|---|
| Browser double-click | One `signup_id` per session; `sbff:lock:<signup_id>` (`SET NX PX`, TTL = longest request deadline + 5 s, so it outlives an SB-5 waiting on Billing). A second concurrent request gets `409 invalid_state` with `details.retry_after_ms` and the client re-polls. |
| RP-1 | `Idempotency-Key: signup:<tenant_id>:rp1`. A replay returns the stored `201`; a concurrent duplicate returns `409 operation_in_progress` → wait and retry with the same key (RP §8.0, §9.2). |
| I-1 | Idempotent on `tenant_id` (`ON CONFLICT (id) DO NOTHING`, I1-1) → `200` on replay. No `Idempotency-Key` (O&M has none, O&M §9.2). |
| Deterministic key | The RP-1 key is derived from `tenant_id`, not random, so a retry after a Valkey blip still reuses it (C-2). |

### 9.3 Timeouts and retries (assumed — N-11)

Server request deadline for SB-2: **2,500 ms**. SLO target (HLD §3.4): 1.5 s p99 on the no-retry path.

| Step | Per-attempt timeout | Retries | Retry on | Notes |
|---|---|---|---|---|
| Valkey ops | 50 ms | 1 | network error | Fail-closed after |
| Turnstile | 300 ms | 0 | — | `503 dependency_unavailable` at once. No retry: a token is single-use and may already be spent when a call times out, so a retry could turn the `503` into a false `403 captcha_failed`; the frontend sends a fresh token (§5.4) |
| Eligibility (CR-4) | 150 ms | 1 | 5xx / timeout | Then `503` (no Keycloak effect yet) |
| Password hash | ~100–200 ms CPU | — | — | Runs after CAPTCHA (SB-INV-5) |
| RP-1 | 2000 ms (`RP_TIMEOUT_MS`; RP p95 ≤ 1.5 s) | 1 | 502, 503, timeout, `409 operation_in_progress` | Same key; then `503`, handoff stays `allocated` |
| I-1 (inline) | 300 ms | 2 (backoff 100 ms, 250 ms) | 5xx, timeout | Only while the deadline allows; then `202` |
| I-1 (SB-3 resume) | 500 ms | 0 per poll | — | Poll cadence is the retry loop |

Retries use jittered exponential backoff and never exceed the remaining request deadline.

### 9.4 Partial-failure policy (BFF-2, decided — option B amended)

1. **Inline retry** of I-1 within SB-2 (§9.3).
2. **Resume** via SB-3 polling while the browser stays (up to 15 min).
3. **Backstop (live — RP-1 + OM-1 done, 23 Oct 2026):** RP-1 emits `TenantIdentityProvisioned {tenant_id, realm, owner_user_id, slug, name, plan}` from its outbox. O&M's `tenant-orgm-q` consumer receives it and runs idempotent I-1 if no tenant row exists. Three idempotency layers prevent duplicate creation. No BFF change needed.

| Backstop behaviour (O&M) | Effect on the BFF |
|---|---|
| I-1 idempotent on `tenant_id`; first writer wins | BFF retries and the backstop race safely (SB-INV-8 keeps their values identical) |
| Payload I-1 can never accept (validation failure, slug held by another tenant) → straight to DLQ, nothing created | A `slug_conflict` signup is **not** completed by the backstop; only SB-4 completes it |
| Transient failures retried; EVT-17: an event for a tenant with no row yet is retried, not dropped | Covers browser abandonment, pod restarts and Valkey loss in `tenant_pending` |
| An offboarded tenant is never recreated | — |
| Metric `iam_org_membership_tenant_provisioning_backstop_total{result}` | Added to the Signup Funnel dashboard (§11.5) |

No BFF-owned reconciliation job and no BFF database (SB-INV-1).

**BFF-2a resolved (A-3, 23 Oct 2026):** orphaned Keycloak user risk is addressed by three layers: (1) BFF PUB-1 server-side pre-check before RP-1 eliminates most cases; (2) RP `orphaned-signup-reap` job reaps after 48 h; (3) O&M `IAMSignupProvisioningRejected` warning alert on backstop dead-letter. The I-1 unique constraint remains authoritative. See §5.6 step P and §8.1.

### 9.5 Failure taxonomy

| Failure | User sees | Internal state | Metric `stage/reason` |
|---|---|---|---|
| Valkey down | `503` | none | `valkey/unavailable` |
| Turnstile down | `503` | none | `captcha/unavailable` |
| Eligibility down | `503` | `allocated` | `eligibility/unavailable` |
| RP-1 `502`/`503` after retries | `503`, resubmit | `allocated` | `realm_provisioner/unavailable` |
| RP-1 unexpected `4xx` (`400`, `403 tenant_mismatch`) | `500` | `allocated` | `realm_provisioner/contract_error` (paged) |
| I-1 `5xx` after retries | `202` | `tenant_pending` | `org_membership/unavailable` |
| I-1 `409 slug_already_taken` | `409 slug_taken` | `slug_conflict` | none — a slug conflict is a user outcome, not a provisioning failure; it is counted in `bff_signup_form_submitted_total` only |
| I-1 unexpected `4xx` | `500` | `identity_created` | `org_membership/contract_error` (paged) |
| Pod dies mid-handoff | Next poll / resubmit resumes | last persisted state | `handoff/resumed` |

---

## 10. Security

### 10.1 Network and mesh identity (BFF-1, decided — option A)

- The mesh is **Istio** on EKS. The pod runs as service account **`bff-signup`** in namespace **`bff`**; Istio identity **`spiffe://<trust-domain>/ns/bff/sa/bff-signup`**. The IAM charts default `trustDomain` to `cluster.local`; the production value is an **SRE open value**.
- **Ingress to the BFF:** NetworkPolicy and an Istio `AuthorizationPolicy` allow only the Envoy Gateway proxy namespace **`envoy-gateway-system`** (API port 8080) and **`monitoring`** (metrics port 9090) (G-5).
- **Egress from the BFF:** NetworkPolicy rules name their destinations (`networkPolicy.egress`): DNS in `kube-system`; Valkey only on the cache subnet CIDRs; port 8080 only to namespaces `iam` and `billing`; OTel only in `observability`; istiod (15012) for the sidecar; 443 only to public addresses (private ranges excluded). Destinations: DNS, the ElastiCache Valkey endpoint, the Realm Provisioner, Org & Membership and Billing (`commercial-billing.billing:8080`, SB-5) API ports (8080, via the Istio sidecar), the Catalog Service (N-2), the OTel collector, and HTTPS to Turnstile. An Istio `ServiceEntry` for `challenges.cloudflare.com:443` is required if the mesh runs `outboundTrafficPolicy: REGISTRY_ONLY` (G-6).
- **IAM side** (IAM platform security + SRE): pod-level admission on both Realm Provisioner and Org & Membership — NetworkPolicy for namespace `bff` + pods labelled `app.kubernetes.io/name: bff-signup`, and an Istio `AuthorizationPolicy` keyed on principal `<trust-domain>/ns/bff/sa/bff-signup` with exact-path allow-lists. Admitted routes:

| Service | Method + path | Status |
|---|---|---|
| Realm Provisioner | `POST /api/v1/internal/tenants/provision-trial` (RP-1) | Done (PR #27 v1.35) |
| Realm Provisioner | `POST /api/v1/internal/tenants/provision-direct` (RP-2, SB-5) | Done (PR #27 v1.35) |
| Org & Membership | `POST /api/v1/internal/tenants` (I-1) | Done (PR #33 rev 2.37) |
| Org & Membership | `POST /api/v1/internal/trial-eligibility` (eligibility, CR-4) | Done (PR #33 rev 2.37) |
| Org & Membership | `GET /api/v1/public/tenants/resolve` (PUB-1 slug pre-check, A-3) | Done (PR #34 rev 2.38) |
| Billing | `POST /api/v1/internal/subscriptions/authorize-and-charge` (B-I1, SB-5) | Billing chart admits `bff`/`bff-signup` on 8080 (BIL-AUTH-3, Billing LLD §10.2) |

Every other path on both services is denied by Istio. Both charts refuse to render if the pod selector is empty.

**IAM chart state — resolved (F-1, CR-3, F-2, TL-1 closed; updated 23 Oct 2026):**

| Service | NetworkPolicy (L4) | Istio AuthorizationPolicy (L7) |
|---|---|---|
| Realm Provisioner | Namespace `bff` + pods labelled `app.kubernetes.io/name: bff-signup`, API port only (PR #27 v1.35) | Principal `cluster.local/ns/bff/sa/bff-signup`; `POST /api/v1/internal/tenants/provision-trial` (RP-1) and `POST /api/v1/internal/tenants/provision-direct` (RP-2) only, exact paths |
| Org & Membership | Same namespace + pod selector (PR #33 rev 2.37) | Same principal; `POST /api/v1/internal/tenants` (I-1) and `GET /api/v1/public/tenants/resolve` (PUB-1 pre-check) only, exact paths |

Both charts refuse to render if the pod selector is empty — no accidental namespace-wide grant is possible. `gateway` removed from RP `existingCallerNamespaces`; RP NetworkPolicy for `gateway` now conditional on the gateway route being enabled (PR #28 v1.36).

**F-2 (closed):** RP handler docs no longer list the BFF as a caller of RP-3 or RP-11 (removed 22 Oct 2026).

### 10.2 Abuse controls

| Control | Rule | Enforced |
|---|---|---|
| Per-IP new signups | 3 / hour (HLD §3.4) | BFF, Valkey `sbff:rl:ip` |
| Per-email attempts | 5 / 24 h | BFF, Valkey `sbff:rl:email` |
| Session starts | 20 / IP / hour | BFF, `sbff:rl:sess` |
| Status polls | 60 / session / minute | BFF, `sbff:rl:poll` |
| CAPTCHA | Every SB-2 and SB-5 (including SB-5 payment resumes), invisible mode | Turnstile (BFF-3) |
| WAF | AWS managed rules + IP reputation on the signup host | ALB (SRE) |
| Body size | 16 KB | BFF middleware |

**When counters increment (G-2):** the per-IP and per-email counters increment only **after** the CAPTCHA passes, so a real user who fails a challenge does not lose an attempt; bots are stopped by the CAPTCHA itself. Resumes of an existing `signup_id` never count, and neither does an SB-5 payment resume (§19.4). SB-2 and SB-5 read the same `RATE_*` limits. **Risk for TL:** 3 signups per IP per hour is strict for offices behind one NAT IP; the limit is configurable (§12) if web / growth wants to raise it.

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

- Validated against `password_policy` (N-10), then hashed in memory with **PBKDF2 (Password-Based Key Derivation Function 2)-SHA512, 210,000 iterations, 16-byte random salt**.
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
- Receive a card number: SB-5 accepts only a provider payment-method token (`payment_token` → `payment_method_ref`), never card data.

---

## 11. Observability

### 11.1 SLOs (HLD §3.4)

| SLO | Target | Measured by | Rules |
|---|---|---|---|
| SLO-1 Availability | 99.9% monthly non-5xx on `POST /trial` and `/paid` | `platform_http_requests_total{route=~"/api/v1/signup/(trial\|paid)", status_class="5xx"}` (platform-gincommon, Proposed) | `bff:signup_error_ratio:rate_5m` / `:rate_1h`; `SignupSLOFastBurn` (14.4×, 5 m), `SignupSLOSlowBurn` (2×, 1 h), both `metric_status: proposed` |
| SLO-2 Provisioning success | ≥ 99% of submitted signups complete | `bff_signup_provisioning_succeeded_total` / `bff_signup_form_submitted_total` (Tier 3) | `bff:provisioning_success_ratio:rate_1h`; `SignupProvisioningSLOBreach` |
| SB-2 trial latency (no-retry path) | p99 ≤ 1.5 s | `platform_http_request_duration_seconds{route="/api/v1/signup/trial"}` (Proposed) | Dashboard panel only; no alert until the metric is ratified |
| Rate limit | `RATE_SIGNUPS_PER_IP_PER_HOUR` (3) | `bff_signup_rate_limited_total{scope="ip"}` | Dashboard |

Rules live in `deploy/monitoring/slo-rules.yml` and `app-alerts.yml`; the Helm `PrometheusRule` deploys both.

### 11.2 Prometheus metrics

Metrics comply with the **Enterprise Platform Observability Standard** (three-tier taxonomy). Every series carries const labels `domain=bff`, `service=bff-signup`, `environment` injected by `platform-gincommon` (never at call sites). All logs, metrics, and traces pass through `platform-gincommon` exclusively — enforced by `make log-sink-lint` in CI.

**Tier 1 (platform-shared, Proposed):**

| Metric | Type | Labels |
|---|---|---|
| `platform_dependency_request_seconds` | histogram | `dependency`, `operation`, `outcome` |

Proposed — shadow-emitted only. No alerts or SLOs reference it until it reaches `StatusCanonical` in `internal/adapter/outbound/metrics/registry.go` (migration steps: `docs/observability/README.md`).

platform-gincommon also emits the Tier 1 HTTP server metrics (`platform_http_requests_total`, `platform_http_request_duration_seconds`, …; Proposed) and `platform_build_info`. SLO-1 uses `platform_http_requests_total`, because no Tier 3 request counter exists; its alerts carry `metric_status: proposed`, as in iam-org-membership.

**Tier 2 (`bff_*`):** none — bff-signup is the only BFF service.

**Tier 3 (service-specific, authoritative):**

| Metric | Type | Labels |
|---|---|---|
| `bff_signup_form_started_total` | counter | `mode` |
| `bff_signup_form_submitted_total` | counter | `mode` |
| `bff_signup_provisioning_succeeded_total` | counter | `mode`, `strategy` (`inline`, `resume`, `slug_change`) |
| `bff_signup_provisioning_failed_total` | counter | `mode`, `stage`, `reason` |
| `bff_signup_rate_limited_total` | counter | `scope` (`ip`, `email`, `session`, `poll`) |
| `bff_signup_captcha_challenged_total` | counter | `result` (`pass`, `fail`, `error`) |
| `bff_signup_handoff_resumed_total` | counter | `trigger` (`poll`, `resubmit`, `slug_change`) |
| `bff_signup_dependency_call_duration_seconds` | histogram | `dependency`, `outcome` — **authoritative until Tier 1 is ratified**; buckets = the registry's `dependency_request` preset (to 30 s, covering Billing's 15 s timeout) |
| `bff_signup_password_hash_duration_seconds` | histogram | — |

`mode` is `trial` (SB-2) or `paid` (SB-5); `form_started` is always `trial`, because SB-1 runs before the path is chosen. `provisioning_failed` is recorded with `stage="org_membership"` only (I-1 exhausted or contract error). No label carries a tenant id, email, or IP (high-cardinality labels are prohibited by the standard and enforced by CI).

The generated inventory is `docs/observability/metric-registry.md`. CI (`make metrics-lint`) runs platform-gincommon's `metricslint` on a real scrape (tiers, naming, required and prohibited labels, registry compliance, label vocabulary) and checks that every metric an alert, recording rule, adapter rule or dashboard queries is actually emitted (`docs/observability/README.md` § CI enforcement).

**Dependency instrumentation:** All five outbound adapters call `metrics.ObserveDependencyCall(dependency, operation, duration, resp, err)` immediately after `httpClient.Do()`, recording latency and outcome for both successful and failed calls. The label map: `orgmembership` → `org_membership` (`provision_tenant`, `trial_eligibility`, `slug_resolve`); `realmprovisioner` → `realm_provisioner` (`provision_trial`, `provision_direct`); `turnstile` → `turnstile` (`siteverify`); `catalog` → `catalog` (`plans`); `billing` → `billing` (`authorize_and_charge`). CI fails if any adapter omits this call.

### 11.3 Tracing

OpenTelemetry via `platform-gincommon` only: the server span comes from `gincommon.ObservabilityMiddlewares`, and every other span from `internal/adapter/outbound/tracing`, which uses gincommon's tracer and propagator (`NewTracer`, `InjectTraceContext`). Each outbound HTTP call (RP-1/RP-2, I-1, eligibility, PUB-1, Billing B-I1, Catalog) gets a client span `"<METHOD> <dependency>"` and propagates `traceparent`, so one trace runs from the browser request into Realm Provisioner, Keycloak, Org & Membership and Billing. Turnstile gets a client span but no `traceparent` (third party). Each Valkey command or pipeline gets a client span (`valkey <command>`). Span attributes include `signup.id`, `signup.type`, `tenant.id`, `handoff.state`, HTTP method/host/path/status and the Valkey command — never email, name, password, payment data, URL queries or Valkey keys. `make log-sink-lint` enforces that no other package imports OpenTelemetry or Prometheus and that every HTTP and Valkey client is traced.

### 11.4 Structured logs

JSON logs with `request_id`, `trace_id`, `signup_id`, `tenant_id`, `state`, `stage`, `outcome`. **Redacted / never logged:** password, `hashed_password_seed`, `captcha_token`, CSRF token, cookie, full email (only `email_domain` and the HMAC), admin name, payment token, Billing `client_secret`. No log-redaction test exists yet (§14.1 target).

### 11.5 Dashboards and alerts

**"bff-signup — Signup Health"** Grafana dashboard (`deploy/monitoring/dashboards/signup-health.json`): funnel by mode, SLO-1 and SLO-2, provisioning failures, dependency latency p95 and error ratio (incl. Billing), CAPTCHA, rate limiting, handoff resumes, PBKDF2 time, submit latency p99.

Alerts (`deploy/monitoring/app-alerts.yml` + `slo-rules.yml`, deployed by the Helm `PrometheusRule`; runbooks in `docs/observability/runbooks.md`):

| Alert | Condition | Route |
|---|---|---|
| `SignupBFFDown` / `SignupBFFReplicasMissing` | no healthy pod 2 min / < 2 healthy for 5 min | Web / growth |
| `SignupProvisioningFailureRate` | `failed / submitted > 5%` for 10 min | Web / growth on-call |
| `SignupOrgMembershipLag` | any `stage="org_membership", reason="exhausted"` in 15 min | Web / growth → IAM |
| `SignupContractError` | any `reason="contract_error"` (includes RP `400 INVALID_REQUEST` for `plan`) | Web / growth + IAM (page) |
| `SignupRealmProvisionerUnavailable` | > 20% of RP-1/RP-2 calls erroring for 5 min | Web / growth → IAM |
| `SignupBillingUnavailable` | > 20% of Billing B-I1 calls erroring for 10 min | Web / growth → Commercial |
| `SignupBackstopActivityHigh` | `iam_org_membership_tenant_provisioning_backstop_total{result="created"}` > 5 in 15 min | Web / growth |
| `SignupCaptchaErrorRate` | `result="error"` > 5% for 5 min | Web / growth |
| `SignupValkeyDown` | readiness failing | Web / growth + SRE |
| `SignupSLOFastBurn` / `SignupSLOSlowBurn` | SLO-1 burn 14.4× (5 m) / 2× (1 h); `metric_status: proposed` | Web / growth |
| `SignupProvisioningSLOBreach` | SLO-2 < 99% over 1 h with traffic | Web / growth |

**On-call is deferred (BFF-6).** Alert infra is wired and ready (`team: web-growth`, runbooks linked). PagerDuty/OpsGenie service creation and L1 owner naming are deferred until PagerDuty is procured. See §16.1.

---

## 12. Configuration

```yaml
# Design view. The chart sets these as `env:` entries in deploy/helm/values*.yaml.
service:
  name: bff-signup
  namespace: bff
  serviceAccount: bff-signup               # BFF-1 (decided)
publicHost: signup.bcbpsolutions.com       # BFF-4 (decided)

http:
  port: 8080
  metricsPort: 9090
  requestDeadlineMs: 5000                  # every route except SB-5; ≥ eligibility + RP-1 + 2×I-1
  paidRequestDeadlineMs: 30000             # SB-5 only; also gincommon's outer RequestTimeout when paid is on (§19.5)
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
    timeoutMs: 2000                      # RP-1 (p95 ≤ 1.5 s)
    rp2TimeoutMs: 8000                   # RP-2 (p95 ≤ 4 s)
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
  billing:                                 # SB-5, §19.5
    baseUrl: http://commercial-billing.billing.svc.cluster.local:8080
    timeoutMs: 15000                       # ≥ 15000 enforced when paid signup is on
  paidSignupEnabled: false                 # true only at Billing go-live step 4
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
| Istio `ServiceEntry` | `challenges.cloudflare.com:443` (Turnstile), `templates/serviceentry.yaml`, on in staging/prod (`REGISTRY_ONLY`), exported to this namespace only |
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
- PBKDF2 (Password-Based Key Derivation Function 2) output verified against a known vector and against Keycloak's format.
- Error mapping tables (§9.5, §17).
- **Redaction test (target, not yet written):** run a full SB-2 and SB-5 with a fake logger and assert no password, seed, payment token, `client_secret` or email appears in any log line or span attribute.

### 14.2 Integration tests (testcontainers-go)

- Real Valkey: sessions, counters, locks, TTLs, fail-closed on Valkey stop.
- `test/integration/` covers Valkey only. The full SB-1…SB-5 flows run in `test/e2e/` (in-process router and services, Valkey testcontainer, Go mocks of O&M and RP), and `httptest` stubs per adapter in `test/unit/adapter/` (Billing B-I1 included).

### 14.3 Contract tests

- Consumer-driven contracts (Pact or JSON-schema) for RP-1, I-1 and eligibility, published to the IAM repos' CI so a breaking change there fails their build. Billing B-I1 needs a Pact consumer contract too (§19.6). Not built yet: today these cases run as in-repo tests (`test/unit/service/contract_test.go`, `test/unit/adapter/`).
- **Plan contract:** every `plan` the BFF can send (catalogue and `plansFallback`) is one of `starter`/`pro`/`enterprise`; a test asserts the BFF never sends `trial`.
- **Same-values contract:** for every completed signup, RP-1 and I-1 bodies carry identical `plan`, `name`, and slug (except after SB-4) (SB-INV-8).
- RP-1 error parsing branches on RFC 9457 `status` + `errorCode`, never `detail`.
- Header contract: RP-1/RP-2 carry `x-tenant-id`, `x-user-id = …00a1`, `x-tenant-roles: iam-system` and a stable `Idempotency-Key`; I-1 and eligibility carry the same headers without a key; Billing carries only `x-tenant-id` and `Idempotency-Key: charge:<tenant_id>` (§5.6).

### 14.4 E2E (staging)

1. Happy path: form → `201` → verification email received → first login lands in the tenant.
2. O&M stopped mid-flow → `202` → O&M restarted → SB-3 completes; exactly one tenant and one Keycloak user exist.
3. Slug conflict → SB-4 → completed with the new slug; one Keycloak user.
4. Duplicate email → `409 email_unavailable`; no new Keycloak user.
5. Repeat trial (CR-4) → `409 trial_not_eligible`; no Keycloak user created.
6. Double-click submit → one tenant.
7. Pod killed during RP-1 → resubmit → same `tenant_id`, one Keycloak user.
8. Resubmit with a different email while `allocated` → new `tenant_id`; one Keycloak user per email (G-1).
9. BFF I-1 blocked, browser closed → the backstop creates the tenant from `TenantIdentityProvisioned` within minutes (once RP-1 + OM-1 open items are closed).
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

Status as of 26 Oct 2026 (v0.9). All go-live blockers cleared. BFF code complete. Backstop fully functional.
Four items formally deferred to future phases — see DEFERRED labels below.

---

### 16.1 Still open / Deferred

All items here are deferred to a future phase. BFF code needs no changes for any of these.

| Gap ID | Item | Responsible | When to pick up | Status |
|---|---|---|---|---|
| **BFF-6 / D-1** | On-call rotation not set up. Alert infra coded and ready to route. TL names L1 owner → get IAM escalation contact → create `bff-signup` service in PagerDuty/OpsGenie L1→L2 → wire `IAMSignupProvisioningRejected` + BFF 5xx alerts → link §9.4 runbook. | Web / Growth TL | When PagerDuty is procured | 🟡 **DEFERRED** |
| **BFF-RP2 / D-2** | IAM HLD §10.7 and §4.2 diagram credit Billing as RP-2 caller — stale. BFF LLD and RP auth policy already enforce BFF-as-caller. TL confirms BFF is the caller → IAM corrects §10.7 + §4.2 diagram. | Web/Growth TL + Billing lead + IAM | Before paid go-live (§19.5 step 4) | 🟡 **DEFERRED** |
| **N-10a** | RP-8 sweep for existing trial realms. Runbook written (`docs/runbook.md §10`). New realms already correct (N-10 closed). Sweep only needed for realms created before the fix. | Realm Provisioner | During Keycloak maintenance phase | 🟡 **DEFERRED** |

---

### 16.2 Infra gaps

🟡 **ALL ITEMS IN THIS SECTION ARE DEFERRED TO THE INFRA PHASE.**
No BFF code changes required. SRE executes these when the infra phase begins (before prod deploy).

| Gap ID | Gap | What closes it | Owner | Blocks prod deploy? |
|---|---|---|---|---|
| **BFF-4 SRE** | `signup.bcbpsolutions.com` public endpoint not provisioned. Service unreachable from internet. Longest lead time — raise ticket early. | SRE provisions: DNS A/CNAME → ALB, ACM TLS cert, AWS WAF managed rules + IP reputation list, Envoy HTTPRoute (no `jwt_authn`/`ext_authz`), Istio sidecar injection + mTLS in `bff` namespace, `ServiceEntry` for Turnstile if mesh runs `REGISTRY_ONLY`. Then flip `ingress.enabled: true` + `gateway.name` in BFF Helm. | SRE | **Yes** |
| **BFF-2 SRE** | SNS subscription filter for `tenant-orgm-q` not verified in all envs. `TenantIdentityProvisioned` must be in the filter or the O&M backstop never fires. | SRE adds `TenantIdentityProvisioned` to SNS subscription filter on `tenant-orgm-q` in dev / staging / prod. Verify via `iam_org_membership_tenant_provisioning_backstop_total` metric > 0 after a test signup. | SRE | **Yes** |
| **BFF-1 value** | Prod SPIFFE mesh trust domain not confirmed. BFF and IAM charts default to `cluster.local`. If prod differs, `bffCaller.istio.trustDomain` must be updated in RP `values-prod.yaml` — otherwise every RP-1 and I-1 call fails `403 forbidden_peer`. | SRE confirms trust domain. If `cluster.local` confirmed, no change. If different, update RP `values-prod.yaml`. | SRE | **Yes** |

---

### 16.3 Closed gaps

All items below are resolved. Organised by service.

**BFF service — code**

| Gap ID | What was fixed | Service | Files fixed | Date |
|---|---|---|---|---|
| N-7 | Valkey fail-closed 503 — `handoffs.Get` error was silently ignored; now returns `ErrDependencyUnavailable`. All Valkey adapters propagate errors correctly. | BFF | `service/signup_service.go`, `adapter/outbound/valkey/*` | 23 Oct 2026 |
| N-8 | Client IP extraction — reads `x-envoy-external-address` only; never parses `X-Forwarded-For` blindly. Already implemented; confirmed. | BFF | `middleware/clientip.go` | Existing |
| N-9 | CSRF — synchronizer token (32-byte random, SHA-256 hash stored, constant-time compare), `SameSite=Strict`, Origin check on every POST. Already implemented; confirmed. | BFF | `middleware/csrf.go`, `middleware/session.go` | Existing |
| N-11 | Timeout budget — `runOp` helper: 50 ms child context + 1 retry on every Valkey op. `SubmitTrial` handler applies 2,500 ms `context.WithTimeout`. | BFF | `valkey/store.go`, `handlers.go` | 23 Oct 2026 |
| BFF-A3-IMPL | PUB-1 server-side slug pre-check before RP-1. `CheckSlugFree` added to port + implemented in O&M client. Called in orchestrator between eligibility and RP-1. Fail-open on 5xx. | BFF | `port/org_membership.go`, `orgmembership/client.go`, `handoff_orchestrator.go` | 23 Oct 2026 |
| BFF-A3-UX | `slug_taken` response now includes `message` explaining offboarded tenants keep their slug. | BFF | `dto.go`, `handlers.go` (SB-2 + SB-4) | 23 Oct 2026 |
| BFF-A4-IMPL | `terms_acceptance` sent on every RP-1 call (`version`, `accepted_at` server time, `ip_address` from trusted proxy, `user_agent` ≤512 bytes). Nothing sent on I-1. | BFF | `port/realm_provisioner.go`, `signup_service.go`, `handoff_orchestrator.go`, `handlers.go` | 23 Oct 2026 |
| N-TERMS-VAL | Terms version not validated against current published version. `validate()` now checks `TermsVersion == currentTermsVersion`. SB-1 returns `current_terms_version` so browser knows which version to send. | BFF | `signup_service.go`, `handlers.go`, `dto.go` | 23 Oct 2026 |
| BFF-A1-HELM | Helm chart pod label and SA confirmed correct: `serviceAccount.name: bff-signup`, `app.kubernetes.io/name: bff-signup` via `selectorLabels`. Matches IAM `signupBffCaller.podSelector`. | BFF | `deploy/helm/values.yaml`, `_helpers.tpl` | 23 Oct 2026 |
| N-1, N-2, N-4, N-6, N-12 | No-action resolutions: slug rules in §5.8, static catalog ships in code, Keycloak native resend, frontend owns page, I-8→403 login guard. | BFF / O&M | — | 23 Oct 2026 |

**Realm Provisioner — code**

| Gap ID | What was fixed | Service | Files fixed | Date |
|---|---|---|---|---|
| RP-1 | `TrialTenantProvisionedPayload` expanded: `KeycloakUserID` → `OwnerUserID`; added `Slug`, `Name`, `Plan`. Enqueue site wired from `ProvisionTrialRequest` (all fields already in scope). Schema and AsyncAPI updated. Backstop end-to-end now functional. | Realm Provisioner | `event.go:69–75`, `provisioning_service.go:403`, `trial_tenant_provisioned.json`, `api/asyncapi.yaml:526–533` | 23 Oct 2026 |
| RP-4 | `validPlan()` wired into RP-1 handler after slug guard. Same function already used by RP-2. RP-1 now rejects invalid plan values with `400 INVALID_REQUEST`. | Realm Provisioner | `internal_handler.go` (~line 84) | 23 Oct 2026 |
| RP-5 | AsyncAPI spec payload updated (included in RP-1 changes). | Realm Provisioner | `api/asyncapi.yaml:526–533` | 23 Oct 2026 |
| N-10 | Keycloak `trial` realm password policy — 6 missing rules added to `realm-template.json:10`: `length(12)`, `maxLength(128)`, `upperCase(1)`, `lowerCase(1)`, `digits(1)`, `notEmail`. New realms correct. All 20 packages pass. | Realm Provisioner | `realm-template.json:10` | 23 Oct 2026 |
| N-10a | RP-8 sweep runbook written — `docs/runbook.md §10`: staging first → Keycloak verification → production run → rollback position. 21/21 packages pass. **Sweep not yet executed** — team runs when ready. Existing realms retain old policy until the sweep is run. | Realm Provisioner | `docs/runbook.md §10` | 23 Oct 2026 |
| N-3 Path B | `orphaned_signup_reap.go` extended with Path B. `HasUnverifiedUsers` added to Keycloak port + adapter. For rows where `trial_ends_at IS NOT NULL AND created_at < NOW() - ORPHAN_SIGNUP_AFTER`: calls Keycloak to check `email_verified`; deletes unverified users → frees email within 48h. `tenant_realms` row kept intact so RP-C1/RP-C2 still runs normal trial-expiry for O&M cleanup. Metric changed to `CounterVec{path="A"\|"B"}`. 21/21 packages pass. | Realm Provisioner | `cmd/reconciler/jobs/orphaned_signup_reap.go`, Keycloak port + adapter | 23 Oct 2026 |
| RP-3 | `orphaned-signup-reap` reconciler job — built and registered. `cmd/reconciler/jobs/orphaned_signup_reap.go` added; registered in `cmd/reconciler/main.go`; `ORPHAN_SIGNUP_AFTER` env var (default 48h); `OrphanedSignupReapedTotal` metric wired. Covers Path A (RP-1 succeeded, I-1 never completed → no `TrialStarted` received after 48h → delete Keycloak user, free email). All 20 packages pass. | Realm Provisioner | `cmd/reconciler/jobs/orphaned_signup_reap.go` (new), `cmd/reconciler/main.go` | 23 Oct 2026 |
| RP-2 | `terms_acceptance` wired into RP-1 — field added to `provisionTrialRequest`; wired into `ProvisionTrialRequest`; clock-skew validation (±24h / +10min); `legal.terms.accepted` enqueued in same `RunInTx` as `tenant_realms` upsert. All 20 packages pass. | Realm Provisioner | `internal_handler.go`, `pending_audit_entries` outbox | 23 Oct 2026 |
| CR-1 | RP-1 returns `409 errorCode: EMAIL_ALREADY_REGISTERED` (RFC 9457) on duplicate email. | Realm Provisioner | `errors.go`, `errors_test.go` | 22 Oct 2026 |
| CR-2 | `hashed_password_seed` mapped to Keycloak `secretData`/`credentialData`; `trial` realm hash policy set to PBKDF2-SHA512, 210,000 iterations. | Realm Provisioner | `admin_users.go`, `realm-template.json` | 22 Oct 2026 |
| CR-3 | Pod-level `bffCaller` AuthorizationPolicy + NetworkPolicy for `bff/bff-signup` → RP-1/RP-2 exact paths. `gateway` removed from `existingCallerNamespaces`. | Realm Provisioner | RP Helm chart, `authorizationpolicy.yaml`, `networkpolicy.yaml` | 23 Oct 2026 |

**Org & Membership — code**

| Gap ID | What was fixed | Service | Files fixed | Date |
|---|---|---|---|---|
| OM-1 | `TrialTenantProvisioned` handler changed from deliberate no-op to active backstop. Three idempotency layers: `processOnce` dedup (EVT-14 guard), `tenantExists` check, `ON CONFLICT (id) DO NOTHING`. New `TenantBackstopProvisioner` port + `BackstopProvision()` method. Safe before RP-1: falls through to `skipped` on empty payload. | Org & Membership | `consumer/membership_event_consumer.go:319`, `provisioning_service.go`, `main.go` | 23 Oct 2026 |
| OM-2 | Backstop metric `iam_org_membership_tenant_provisioning_backstop_total{result="created"\|"skipped"}` registered and prewarmed. | Org & Membership | `metrics/business.go` | 23 Oct 2026 |
| OM-3 | `IAMSignupProvisioningRejected` warning alert added. Severity: warning, never a page. Full runbook inline. | Org & Membership | `deploy/monitoring/app-alerts.yml` | 23 Oct 2026 |
| OM-4 | Docs corrected: `TenantIdentityProvisioned` → `TrialTenantProvisioned` across LLD, README, AsyncAPI. BFF produces zero events; producer is Realm Provisioner. | Org & Membership | O&M LLD rev 2.33, `README.md`, `api-caching-events.md` | 23 Oct 2026 |
| CR-4 | `trial_signup_ledger` table (migration 000004); `POST /api/v1/internal/trial-eligibility` endpoint; `RecordTrialSignup` atomic in I-1 tx. | Org & Membership | Migration, handler, service, repo | 22 Oct 2026 |

**Design decisions — all closed**

| Gap ID | Decision | Date |
|---|---|---|
| TL-1 / A-1 | Admission level: pod-level by SPIFFE principal + exact path on both O&M and RP. Prior namespace-level state was incorrect — corrected (O&M PR #33; RP PR #27). | 23 Oct 2026 |
| A-2 | `gateway` removed from RP `existingCallerNamespaces`; NetworkPolicy conditional on route being enabled (RP PR #28). | 23 Oct 2026 |
| A-3 / BFF-2a | Orphaned signup: (1) BFF PUB-1 server-side pre-check ✅, (2) RP `orphaned-signup-reap` job (pending RP-3), (3) O&M `IAMSignupProvisioningRejected` alert ✅ (OM-3 done). | 23 Oct 2026 |
| A-4 / N-5 | Terms acceptance: BFF sends `terms_acceptance` on RP-1 ✅; RP writes `legal.terms.accepted` to Audit Log (7-year retention, GDPR-safe). RP PR #32 pending (non-blocking). | 23 Oct 2026 |
| BFF-1…6 | Mesh identity, partial failure, CAPTCHA, domain, billing deferral, repo name — all decided. | 21 Oct 2026 |
| C-1…C-7 | I-1 body, headers/key, duplicate email, password seed, O&M namespace, `owner_user_id` wording, one-trial check — all resolved. | 22 Oct 2026 |
| F-1, F-2 | `gateway` admission bug fixed; BFF removed from RP-3/RP-11 caller docs. | 22–23 Oct 2026 |

**From TL discussion doc (`docs/lld/Gaps/2026-10-23-tl-discussion.md`) — items now closed**

The TL discussion doc listed 5 gaps. The following prerequisites referenced in that doc are now resolved and closed here:

| TL Doc Reference | What is closed | Where in §16.3 |
|---|---|---|
| Gap 1 (BFF-6) — Step 4 prerequisite | `IAMSignupProvisioningRejected` O&M alert live — alert is ready to be routed once BFF-6 on-call is set up | OM-3 |
| Gap 2 (N-3) — A-3 decision | PUB-1 server-side slug pre-check implemented in BFF (eliminates most orphan cases) | BFF-A3-IMPL |
| Gap 4 (BFF-RP2) — A-4 decision | `terms_acceptance` sent on every RP-1 call from BFF; RP receiving it (silently ignored until PR #32) | BFF-A4-IMPL |
| Gap 5 (SRE) — A-1/A-2 decisions | Admission rules corrected: pod-level SPIFFE on both RP and O&M; `gateway` removed from RP | TL-1 / A-1 / A-2 |

The 5 gaps themselves (BFF-6, N-3, BFF-RP2, N-10, SRE infra) remain open in §16.1 and §16.2 above.

---

## 17. Appendix — Error Taxonomy

Public wire shape — RFC 9457 problem details (`application/problem+json`), written by `gincommon.AbortWithProblem` (`internal/adapter/inbound/http/errors.go`). The domain error becomes `errorCode` in UPPER_SNAKE; structured details become top-level extension members:

```json
{ "type": "…", "title": "Conflict", "status": 409, "detail": "slug is taken", "errorCode": "SLUG_TAKEN",
  "traceId": "abc123", "signup_id": "01JABCD...", "suggestions": ["acme-2"], "message": "That workspace address is taken. …" }
```

platform-gincommon owns the format: the BFF defines no error DTO (Swagger `@Failure` annotations reference `ProblemResponse = problem.Problem`, gincommon's own type, as the IAM services do), and `internal/adapter/inbound/http/errors.go` only maps domain sentinels to gincommon's constructors (`NewBadRequestError`, `NewConflictError`, `NewBusinessRuleError`, …, plus one service type, `signup/charge-expired`). When the request context itself ends and the error is the context error or `DEPENDENCY_UNAVAILABLE` (what a cut-off call returns), `HandleError` writes nothing: an expired gincommon `RequestTimeout` gets gincommon's `503 REQUEST_TIMEOUT` problem (counted on `platform_http_request_timeouts_total`), and a client disconnect is recorded as `499`. A handler's own shorter deadline still maps normally (`503 DEPENDENCY_UNAVAILABLE`).

Realm Provisioner, Org & Membership and Billing also return RFC 9457 with `UPPER_SNAKE_CASE` codes; the BFF adapters branch on `status` + `errorCode`, never on `detail`.

### 17.1 Public error codes

| `errorCode` | HTTP | Meaning | Extension members | UI guidance |
|---|---|---|---|---|
| `VALIDATION_FAILED` | 400 | One or more fields invalid | `fields: {name: code}` | Highlight fields |
| `SESSION_EXPIRED` | 401 | Missing or expired session cookie | — | Restart from SB-1 |
| `CSRF_INVALID` | 403 | Missing/wrong CSRF token or `Origin` | — | Reload form |
| `CAPTCHA_FAILED` | 403 | Turnstile rejected the token | — | Retry CAPTCHA |
| `SIGNUP_NOT_FOUND` | 404 | Unknown `signup_id` or not this session | — | Restart |
| `EMAIL_UNAVAILABLE` | 409 | Email cannot be used (already registered) | — | "Use another email or sign in" |
| `TRIAL_NOT_ELIGIBLE` | 409 | Trial already used for this email | — | Same text as above |
| `SLUG_TAKEN` | 409 | Slug owned by another tenant | `signup_id`, `suggestions[]`, `message` | Show slug picker. `message` explains that an address can appear free but still be taken (offboarded workspaces keep their address — O&M invariant T-6). From the PUB-1 pre-check (no Keycloak user yet) the frontend resubmits SB-2 / SB-5 with the new slug; from I-1 (handoff in `slug_conflict`) it calls SB-4. |
| `INVALID_STATE` | 409 | Action not valid in the current handoff state, a request already in flight, or details changed after identity/payment | `state?`, `retry_after_ms?`, `reason?` (`details_changed`) | Poll SB-3, or restart with the original details |
| `CHARGE_EXPIRED` | 410 | SB-5: 3-D Secure not completed within Billing's 30 min | — | Start again |
| `PAYMENT_DECLINED` | 422 | SB-5: Billing declined the payment | `decline_reason` | Ask for another payment method |
| `RATE_LIMITED` | 429 | Limit exceeded | `scope` | Respect `Retry-After` |
| `INTERNAL_ERROR` | 500 | Unexpected error, or an IAM/Billing contract mismatch | — | Show support reference |
| `DEPENDENCY_UNAVAILABLE` | 503 | Valkey, CAPTCHA, eligibility, RP-1/RP-2 or Billing unavailable | `retry_after_seconds?` (+ `Retry-After` header) | "Try again in a minute"; SB-5 resubmits the same body |

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
| Turnstile `success:false` | — | `403 CAPTCHA_FAILED` |
| RP-2 | Same branches as RP-1, except `409 OPERATION_IN_PROGRESS` → `409 INVALID_STATE`; any error keeps the paid handoff in `payment_confirmed` | see RP-1 |
| Billing B-I1 | See §19.1 | see §19.1 |

---

## 18. Integration Details

### 18.1 Realm Provisioner (RP-1)

Per RP LLD §18.1 (rev 1.37): the BFF pre-allocates `tenant_id`, sends the same value in the body and `x-tenant-id`, and RP persists only the `tenant_realms` row; identity goes to Keycloak. RP sends the verification email (24-hour link). The BFF depends on: the RP-1 body (§5.6), `201` response with `keycloak_user_id`, idempotent replay (§9.2), and `409 email_already_registered` (CR-1, done — `errorCode: EMAIL_ALREADY_REGISTERED`). RP-1 emits `TenantIdentityProvisioned {tenant_id, realm, owner_user_id, slug, name, plan}` from its outbox after the Keycloak user exists — payload expanded 23 Oct 2026 (RP-1 done). RP-1 now validates `plan ∈ {starter, pro, enterprise}` server-side (RP-4 done, 23 Oct 2026).

**A-4 (done, 23 Oct 2026):** every RP-1 call now includes `terms_acceptance` (`version`, `accepted_at` RFC 3339 UTC server time, `ip_address` from trusted proxy headers, `user_agent` ≤512 bytes). RP writes a `legal.terms.accepted` record durably in the same transaction as the Keycloak user, delivered to Audit Log as `TermsAccepted` — 7-year retention, not redacted by GDPR erasure. Missing or invalid fields → `400 INVALID_REQUEST`. Retry with the same `Idempotency-Key` replays the original answer and never creates a second record.

**Merge order (pending — not a deploy blocker):** RP ignores unknown JSON fields before PR #32 (v1.41) merges, so the BFF can be deployed now — RP-1 calls succeed and `terms_acceptance` is silently ignored. The Audit Log record will not be written until Audit Log PR #10 (rev 0.31) merges first, then RP PR #32. Track with IAM to confirm both PRs are live before the first paid or production signup.

### 18.2 Org & Membership (I-1, eligibility)

Per O&M LLD (rev 2.33): idempotent on `tenant_id`, `409 slug_already_taken` on a slug owned by another tenant, every tenant created trial-shaped, `trial_ends_at` computed by O&M, `TenantCreated` / `TrialStarted` / `TenantRoleGranted` emitted in the same transaction. The `TenantIdentityProvisioned` backstop handler is **live (OM-1 done, 23 Oct 2026)** — `tenant-orgm-q` consumer runs idempotent I-1 when payload carries the expanded fields; falls through to `skipped` on empty payload (backward-compatible). Metric `iam_org_membership_tenant_provisioning_backstop_total{result}` live; `IAMSignupProvisioningRejected` warning alert active (OM-2, OM-3 done). The eligibility endpoint (CR-4) is built — `trial_signup_ledger` table (migration 000004), `POST /api/v1/internal/trial-eligibility` handler, `IsTrialEligible` / `RecordTrialSignup` on the tenant repository, and `RecordTrialSignup` called atomically inside the I-1 provisioning transaction.

**A-3 / PUB-1 (done, 23 Oct 2026):** O&M now admits `bff/bff-signup` on `GET /api/v1/public/tenants/resolve` (PR #34 rev 2.38). The BFF calls this server-side before RP-1 as a slug pre-check (step P, §5.6). Fail-open: any non-200 or network error → proceed to RP-1. On `{"found": true}` → `409 slug_taken` returned to browser before any Keycloak user is created.

### 18.3 Cross-service dependency table

| Dependency | Direction | Protocol | If down |
|---|---|---|---|
| Envoy (public) | inbound | HTTP | No signups (platform outage) |
| ElastiCache Valkey (BFF) | outbound | RESP/TLS | `503`, fail-closed |
| Turnstile | outbound | HTTPS via NAT | `503` |
| Catalog Service | outbound | mTLS | Cached / static plan list |
| O&M eligibility | outbound | mTLS | `503` before any Keycloak effect |
| O&M PUB-1 (slug pre-check) | outbound | mTLS | fail-open — proceed to RP-1, rely on I-1 constraint |
| Realm Provisioner RP-1 | outbound | mTLS | `503`, user resubmits |
| Realm Provisioner RP-2 | outbound | mTLS | error returned; paid handoff kept in `payment_confirmed`, resubmit skips Billing |
| Billing B-I1 | outbound | mTLS | `503` + `Retry-After`; handoff kept, resubmit reuses the key |
| Org & Membership I-1 | outbound | mTLS | `202` + resume + backstop |

### 18.4 Response to Realm Provisioner: RP-1 `plan` validation (TL item, 21 Oct 2026)

| RP asks | BFF answer |
|---|---|
| 1. Is the `plan` sent to RP-1 always `starter`, `pro` or `enterprise`? | **Yes.** The user picks a plan tier on the form (HLD §8.2.1 step 1). "Start free trial" selects **RP-1** (trial) vs RP-2 (paid, Phase 1.5); it is never a plan value. SB-2 rejects any other value with `400 validation_failed` before any call (§5.4), and the plan list and fallback contain only these three codes (§12). |
| 2. Does the BFF send the same `plan`, slug and name to I-1? | **Yes** (SB-INV-8). Both bodies are built from the same handoff record. The only exception is a slug changed via SB-4 after `409 slug_taken`; RP's event with the original slug is then rejected by O&M as non-acceptable, so the values can never conflict. |
| 3. Does the BFF currently send anything else? | **No.** Nothing is built yet; this LLD is the first specification, and a contract test enforces it (§14.3). |

---

## 19. Direct-Paid Signup (SB-5)

**Status: code aligned to Billing B-I1 (BL-OQ-8, 2026-11-26). Activation follows the Billing go-live order (§19.5); `PAID_SIGNUP_ENABLED` stays `false` until step 4.**

D-2 resolved (Commercial Domain HLD §8.3, CD-9 closed): the BFF, not Billing, calls RP-2 and I-1 after Billing confirms the charge.

### 19.1 Billing B-I1 contract (Billing LLD rev 0.51 §5.4.2)

`POST /api/v1/internal/subscriptions/authorize-and-charge`, over Istio mTLS. Billing admits only `cluster.local/ns/bff/sa/bff-signup` (BIL-AUTH-3, read from `X-Forwarded-Client-Cert`); the BFF sends no identity headers.

| BFF (`port.ChargeRequest`) | B-I1 |
|---|---|
| `TenantID` | body `tenant_id` **and** header `x-tenant-id` (equal) |
| `Plan` | `plan_code` |
| `Seats` | `licensed_seats` |
| `PaymentToken` | `payment_method_ref` (browser provider token, `pm_…`; no card data) |
| `IdempotencyKey` | header `Idempotency-Key: charge:<tenant_id>` |

Only the four required fields are sent. B-I1 retries must repeat the body exactly, and the optional fields (company name, contact) could change between attempts.

| B-I1 answer | Adapter → port | SB-5 does | Browser sees |
|---|---|---|---|
| `201` / `200` + `Idempotent-Replayed` | `ChargeConfirmed` | `payment_confirmed` → RP-2 → I-1 | `201` / `202 provisioning` |
| `402 PAYMENT_ACTION_REQUIRED` | `ChargeActionRequired` | `payment_action_required`, handoff kept | `202 {status: payment_action_required, action: {type, client_secret}, expires_at}` |
| `402 PAYMENT_DECLINED` | `ErrPaymentDeclined` + `decline_reason` | delete handoff | `422 PAYMENT_DECLINED` + `decline_reason` |
| `410 CHARGE_EXPIRED` | `ErrChargeExpired` | delete handoff | `410 CHARGE_EXPIRED` (start again) |
| `409 SUBSCRIPTION_ALREADY_EXISTS` | `ChargeAlreadyExists` | handoff kept; **not** trusted (see 19.4) | `409 INVALID_STATE` |
| `409 REQUEST_IN_PROGRESS`, `500` / `502` / `503` / `504`, timeout | `ErrDependencyUnavailable` (+ `retry_after_seconds`) | handoff kept | `503` + `Retry-After` |
| `422 PRICE_NOT_AVAILABLE` | `ErrValidationFailed` | delete handoff | `400` |
| `400` / `403` / any other status or `errorCode` | `ErrInternal` | handoff **kept** (an unexpected reply may follow a charge; the retry reuses the key) | `500` |

### 19.2 SB-5 flow

```
[repeat on same session, same email, identity exists] → report state (no CAPTCHA, no budget)
CAPTCHA → rate limit (RATE_*; skipped when resuming a payment) → validate → hash
→ lock; re-read handoff:
     none / not paid            → allocate (type=paid, payment_pending, new tenant_id)
     payment_pending | payment_action_required | payment_confirmed → resume (§19.4)
     past payment (identity_created … failed) → same email: report state; else 409 INVALID_STATE
→ B-I1 {tenant_id, plan_code, licensed_seats, payment_method_ref}, key charge:<tenant_id>   (skipped if payment_confirmed)
     ← 402 action required → payment_action_required, return 202 to the browser
     ← 201 / 200          → payment_confirmed
→ PUB-1 slug pre-check (fail-open; taken → 409 SLUG_TAKEN, handoff stays payment_confirmed)
→ RP-2 provision-direct {…, billing_subscription_id}, key signup:<tenant_id>:rp2   (error → handoff stays payment_confirmed)
→ identity_created → I-1 → completed | tenant_pending (SB-3 polls) | slug_conflict (SB-4)
```

Every SB-5 response re-issues the session cookie (sliding TTL), so a 3-D Secure re-POST within Billing's 30-minute window still has a session.

### 19.3 3-D Secure

1. SB-5 returns `202 {status: "payment_action_required", action: {type: "3ds", client_secret}, expires_at}`. `client_secret` is never logged or stored.
2. The browser completes 3-D Secure with the provider's JS.
3. The browser re-POSTs SB-5 with the **same body** and a fresh `captcha_token`. The BFF finds the handoff, reuses its `tenant_id`, and calls B-I1 again with the same key and body. Billing answers `201`, `402 PAYMENT_DECLINED`, or `402 PAYMENT_ACTION_REQUIRED` again (still pending).
4. If 3-D Secure is not completed within 30 min, Billing answers `410 CHARGE_EXPIRED`; the browser starts again.

The re-POST avoids a separate confirm route that would need the hashed password seed in Valkey (§4.2 keeps it out).

### 19.4 Retries and resumption (SB-INV-3)

- A resubmit on a session whose paid handoff is `payment_pending` or `payment_action_required` **resumes** it. The email, plan, seats and payment method must match (the payment method is checked through `payment_ref_hmac`, an HMAC of the token; the token itself is never stored). Otherwise `409 INVALID_STATE`, and nothing is sent to Billing. Slug, company name and locale may change, because B-I1 does not carry them and RP-2 has not run yet.
- `payment_confirmed` (paid, RP-2 not done, e.g. after a PUB-1 slug conflict, RP-2 `email_unavailable` or `5xx`) also resumes and skips Billing. Plan and seats must match; the email and slug may change.
- Past payment (`identity_created`, `tenant_pending`, `slug_conflict`, `completed`, `failed`) a resubmit never allocates a new `tenant_id`: the same email gets the current state; another email gets `409 INVALID_STATE` (`reason: details_changed`), as on SB-2. Without this, a second charge would start under a new key.
- A resume still needs CAPTCHA (it calls Billing, SB-INV-5) but does not use rate-limit budget. A resume whose details cannot match is refused before the password is hashed.
- **RP-2 freeze.** Before the first RP-2 call the handoff records `rp2_attempted`. RP replays `signup:<tenant_id>:rp2` by key, so from then on slug, company name and email must stay unchanged (`409 INVALID_STATE`, `reason: details_changed`); otherwise the realm and the tenant could disagree (SB-INV-8). A definitive RP refusal (`EMAIL_ALREADY_REGISTERED`, `REALM_ALREADY_DEDICATED` → `409 SLUG_TAKEN` with suggestions) lifts the freeze, since RP lets a failed key be reclaimed. RP-2 `OPERATION_IN_PROGRESS` → `503` + `Retry-After: 2`.
- **One signup type per session.** A trial handoff (`type: trial`) and a paid one never overwrite each other: SB-2 on a session holding a paid handoff, or SB-5 on one holding a trial handoff, gets `409 INVALID_STATE` (`reason: other_signup_in_progress`). Handoffs written before `type` existed count as trial.
- **I-1 on the paid path omits `email_hash`.** O&M claims the one-trial ledger with it; a paid signup is not a trial, and anyone who once had a trial would otherwise get `409 TRIAL_ALREADY_USED` after paying.
- SB-3 tells the browser what moves a paid handoff on: `action_required` with `action: complete_payment` (3-D Secure pending) or `resubmit` (`payment_pending`, `payment_confirmed`, and trial `allocated`).
- `409 SUBSCRIPTION_ALREADY_EXISTS` is never used to provision. The BFF only calls B-I1 with this attempt's own key and body, so that subscription is not this attempt's record, and it may be unpaid.

### 19.5 Configuration and go-live (Billing LLD §18.7)

| Setting | Value | Rule |
|---|---|---|
| `BILLING_BASE_URL` | `http://commercial-billing.billing.svc.cluster.local:8080` | required when enabled |
| `BILLING_TIMEOUT_MS` | `15000` | ≥ 15000 when enabled (B-I1 p95 6 s, p99 12 s) |
| `RP2_TIMEOUT_MS` | `8000` | RP-2 call timeout (RP publishes p95 ≤ 4 s) |
| `PAID_REQUEST_DEADLINE_MS` | `30000` | SB-5 only; ≥ `BILLING_TIMEOUT_MS + RP2_TIMEOUT_MS + 2×I1_TIMEOUT_MS`. With paid on, it is also gincommon's outer `RequestTimeout` and sets the server `WriteTimeout` (+ 500 ms); other routes keep their own `REQUEST_DEADLINE_MS` handler deadline |
| `PAID_SIGNUP_ENABLED` | `false` | set `true` per environment at step 4 only |

Network: Billing admits pods labelled `app.kubernetes.io/name: bff-signup` in namespace `bff` on 8080; this chart's egress already allows TCP 8080.

Go-live order per environment: (1) SRE-3 deployed → (2) this alignment merged and promoted → (3) Billing `FEATURE_DIRECT_SIGNUP=true`, Pact green, Test-Clock E2E green in staging → (4) BFF Helm `PAID_SIGNUP_ENABLED=true`. Billing's step (5), BL-OQ-2 (Core's recency guard), is Billing-side and recorded done (2026-11-19).

### 19.6 Remaining work

1. **Pact consumer contract** (Billing LLD §14.3) for `201`, `200` replay, `402 PAYMENT_DECLINED`, `402 PAYMENT_ACTION_REQUIRED`, `409 SUBSCRIPTION_ALREADY_EXISTS`, `503`. This repo has no Pact tooling yet; the cases are covered by `test/unit/adapter/billing_test.go` against a stub server.
2. ~~Envoy Gateway route timeout~~ — done: the chart's `HTTPRoute` gives `POST /api/v1/signup/paid` a 35 s request timeout (`ingress.timeouts.paid`) and other routes 10 s.
3. **Signup front end:** handle `202 payment_action_required` (3-D Secure, then re-POST), `422 PAYMENT_DECLINED`, `410 CHARGE_EXPIRED`, and `503` + `Retry-After` (resubmit the same body).
4. **Test-Clock E2E in staging**, including one 3-D Secure card: BFF → Billing → RP-2 → paid tenant.
5. Confirm the RP-2 request field set and error codes against RP LLD §5.3. RP-2 returns `keycloak_user_id` and `email_verification_sent` (both read).
6. **Log-redaction test** for SB-5 (payment token, `client_secret`), §14.1.
7. **Paid-orphan reconciliation** (with Billing): a subscription paid but never provisioned survives in Valkey only ~30 min and only in its session. Agree a Billing-side void/refund or alert for subscriptions never matched by `TenantConverted` before paid go-live.
8. **To confirm before go-live:** Istio sidecar injection on namespace `bff` (Billing's caller check and the IAM principal rules depend on it); `networkPolicy.egress.valkeyCIDRs` per environment; O&M (I-1, CR-4, PUB-1) and Turnstile latency against the 300 ms / 150 ms timeouts; RP decision on the realm name after an SB-4 slug change on the paid path.

---

## 20. Appendix — Glossary and References

### 20.1 Glossary

| Term | Meaning |
|---|---|
| Handoff | The eligibility → PUB-1 → RP-1 → I-1 (trial) or B-I1 → PUB-1 → RP-2 → I-1 (paid) sequence for one signup |
| `signup_id` | ULID identifying one signup attempt, bound to a session |
| `tenant_id` | UUIDv7 allocated by the BFF, used by RP-1/RP-2, I-1 and (as `charge:<tenant_id>`) Billing |
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
- `commercial-lld-billing-service.md` rev 0.51 — §5.4.2 (B-I1), §10.2 (network), §14.3 (Pact), §18.7 (go-live order); hand-off `commercial-billing-service/docs/handoff/bl-oq-8-signup-bff.md`

---

## 21. Review and Sign-off

| Role | Scope of sign-off | Name | Status |
|---|---|---|---|
| Web / growth TL | Whole LLD | | ☐ |
| IAM — Realm Provisioner | §5.6, §10.6, §16.1 CR-1, CR-2, CR-3 (RP), F-2 | | ☐ |
| IAM — Org & Membership | §5.6, §9.4, §16.1 CR-3 (O&M), CR-4, C-6 doc fix | | ☐ |
| IAM platform security | §10.1 identity and allow-lists | | ☐ |
| SRE | Trust domain, DNS/TLS/WAF/Envoy, ServiceEntry, ElastiCache, SNS filter | | ☐ |

