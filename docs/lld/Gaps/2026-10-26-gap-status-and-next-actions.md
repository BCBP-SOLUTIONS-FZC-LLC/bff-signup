# bff-signup — Gap Status & Per-Service Action List

**Date:** 2026-10-26  
**Author:** Sharmila  
**Source docs:** `2026-10-23-tl-discussion.md`, `signup-bff-open-questions-tl.md`  
**Status:** Post cross-service review (bff-signup + iam-org-membership + iam-event-consumer + BFF HLD v1.0 + IAM HLD v1.55)

---

## Deferred to Next Phase (do not action now)

| Gap | Reason |
|---|---|
| **Gap 1 — On-call model (BFF-6)** | Process/PagerDuty setup. Alerts infra already wired in code (`team: web-growth`, runbooks referenced). D-1 TL decision (who is L1?) + PagerDuty service creation deferred. |
| **Gap 3 — Keycloak trial realm password policy (N-10)** | BFF enforcement is done (min=12, max=128, classes=3, not-email — `config.go:152-159`). Waiting on IAM to confirm Keycloak realm matches. Deferred to next phase. |

---

## Open Questions — Resolution Status (A-1 to A-4)

All 4 questions resolved by IAM reply 2026-10-22. IAM/RP side PRs are merged or in review. BFF actions below.

### A-1 — Pod-level admission (namespace → pod-level SPIFFE)
- **O&M:** Fixed (PR #33 rev 2.37) — pod-level by SPIFFE principal + exact path.
- **RP:** Fixed (PR #27 v1.35) — same.
- **BFF remaining:** SA `bff-signup` + label `app.kubernetes.io/name: bff-signup` already in Helm chart. **SRE action:** Enable Istio sidecar injection + mTLS in `bff` namespace. Then flip `istio.authorizationPolicy.enabled: true` in BFF Helm values.

### A-2 — RP `gateway` namespace blanket allow removed
- **RP:** Fixed (PR #28 v1.36). No BFF action.

### A-3 — Slug pre-check before RP-1 (orphaned Keycloak users)
- **BFF:** ✅ Done. `CheckSlugFree` (PUB-1) implemented in `orgmembership/client.go:58` and called in `handoff_orchestrator.go:72` before RP-1. Fail-open on 5xx.
- **RP:** `orphaned-signup-reap` job (covers all orphan causes, not just slug conflicts). Status unknown — confirm with RP team.
- **O&M:** `IAMSignupProvisioningRejected` alert should exist. Confirm it's in O&M's alert rules.

### A-4 — Terms acceptance audit record via RP-1/RP-2
- **BFF:** ✅ Done. `TermsAcceptance` struct in `port/realm_provisioner.go:7`, included in `ProvisionTrialRequest` and `ProvisionDirectRequest`. Server timestamps in `signup_service.go:116`. IP + UserAgent forwarded correctly.
- **RP:** PR #32 v1.41 in review — must merge before BFF goes to staging.
- **Audit Log:** PR #10 rev 0.31 merged 2026-10-23.

---

## Per-Service Action List

### bff-signup (this repo)

| # | Action | Priority | Notes |
|---|---|---|---|
| B-1 | **Add `x-tenant-roles: iam-system` header to I-1 call** | **HIGH — BUG** | `orgmembership/client.go:ProvisionTenant()` sends `x-tenant-id` + `x-user-id` but not `x-tenant-roles`. O&M's `RequireSystemRole` middleware will reject with 403. Add `httpReq.Header.Set("x-tenant-roles", "iam-system")`. |
| B-2 | **Add `licensed_seats` to I-1 request** | Medium | O&M I-1 handler binds `licensed_seats`. BFF `ProvisionTenantRequest` does not send it. Add field (default 0 / trial value) or confirm O&M treats it as optional. |
| B-3 | Enable Istio AP in Helm once SRE enables sidecar | SRE-gated | `values.yaml istio.authorizationPolicy.enabled: false` → flip to `true` when Istio mTLS active in `bff` namespace. |
| B-4 | Gap 4: RP-2 caller decision | Awaiting TL | BFF code already calls RP-2. Once TL confirms BFF is the caller, IAM HLD §10.7 + §4.2 diagram must be updated. No BFF code change needed. |
| B-5 | Gap 5 BFF-4: enable ingress when SRE provisions route | SRE-gated | `ingress.enabled: false` → `true` + set `gateway.name` once DNS/TLS/WAF provisioned. |

### iam-org-membership

| # | Action | Priority | Notes |
|---|---|---|---|
| O-1 | Confirm `IAMSignupProvisioningRejected` alert is deployed | Medium | A-3 decision requires this warning alert when backstop DLQs a signup. Not confirmed in O&M alert files. |
| O-2 | Gap 5 BFF-2: SRE to add `TenantIdentityProvisioned` to SNS subscription filter on `tenant-orgm-q` | **HIGH — go-live blocker** | Consumer (`handleTenantIdentityProvisioned`) is implemented. Filter missing = backstop never fires. |
| O-3 | Fix `DirectPaidSignup` dead-read of `plan` field | Phase 1.5 | O&M's `applyProjection` reads a `plan` field RP's payload never sends. Falls through to `prevPlan` harmlessly. Fix when Phase 1.5 activates. |

### iam-realm-provisioner

| # | Action | Priority | Notes |
|---|---|---|---|
| R-1 | Merge A-4 `terms_acceptance` support (PR #32 v1.41) | **HIGH** | BFF already sends the field. RP must accept it before staging. |
| R-2 | Confirm `orphaned-signup-reap` job status (A-3) | Medium | Should cover all orphan cases (not just slug conflicts). |
| R-3 | Gap 4: if Billing also calls RP-2, add Billing pod-level rule | Awaiting TL decision | Only needed if TL decides both BFF + Billing call RP-2. |

### SRE (Gap 5 — all blocking at prod deploy)

| # | Action | Priority | Notes |
|---|---|---|---|
| S-1 | **BFF-4: Provision `signup.bcbpsolutions.com`** | **Raise ticket now (long lead time)** | DNS A/CNAME → ALB, ACM TLS cert, AWS WAF managed rules + IP reputation, Envoy HTTPRoute (no jwt_authn/ext_authz), Istio sidecar injection + mTLS in `bff` namespace. |
| S-2 | **BFF-2: Add `TenantIdentityProvisioned` to SNS filter on `tenant-orgm-q`** | **Blocking** | In dev, staging, and prod. Verify via `iam_org_membership_tenant_provisioning_backstop_total` metric after test signup. |
| S-3 | BFF-1: Confirm prod SPIFFE trust domain | Before prod deploy | If not `cluster.local`, update `bffCaller.istio.trustDomain` in RP `values-prod.yaml` and BFF Helm chart. |
| S-4 | Enable Istio mTLS in `bff` namespace + BFF sidecar injection | Before staging | Required for A-1 pod-level admission to work. Then flip `istio.authorizationPolicy.enabled: true`. |

### iam-event-consumer

No signup gap actions. Backstop for signup provisioning lives in O&M, not this service.

---

## Gap 2 — Unverified Keycloak User Cleanup (N-3, non-blocker)

**Owner: IAM team.**  
IAM to confirm: does a job exist that expires/deletes unverified Keycloak users in the `trial` realm after N days? If not, IAM to build it. No BFF code change required.

---

## Gap 4 — RP-2 Caller Conflict Summary

| Source | Says |
|---|---|
| BFF HLD §7.2, IAM HLD §8.10.6, IAM HLD §10.8 | **BFF** calls RP-2 after Billing confirms payment |
| IAM HLD §10.7 bullet, §4.2 diagram | **Billing** calls RP-2 on Stripe webhook |
| IAM HLD §17.2 | Explicitly open — "owning team and sign-off on `provision-direct` contracts" unresolved |
| Current BFF code | BFF calls RP-2 (matches majority narrative) |

**Decision needed (D-2):** TL + Billing lead + IAM to confirm in writing. Most text agrees BFF is the caller. §10.7 + diagram are likely stale from an older model. Once confirmed, IAM HLD §10.7 bullet and §4.2 diagram edge must be corrected.
