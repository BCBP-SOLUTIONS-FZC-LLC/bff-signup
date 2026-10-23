# Signup BFF — Open Questions for TL Decision

Date: 2026-10-22 · Author: Sharmila · LLD ref: `signup-bff-lld.md` v0.5
**Resolved: 2026-10-22 — all 4 items answered by IAM reply (`iam-to-signup-bff-reply-a1-2026-10-22.md`). BFF actions added below each item.**

Team assignments and new BFF action items added to `signup-bff-action-tracker.md`.

---

### A-1 — O&M admission level (namespace vs pod-level)

**Context:** LLD §10.1 specified pod-level admission for `bff-signup` only. O&M implemented namespace-level (`bff` in `existingCallerNamespaces`), giving the whole `bff` namespace blanket access to all O&M internal routes. RP implemented pod-level correctly. O&M LLD rev 2.30 now documents this as the official state.

**Risk:** Only `bff-signup` runs in `bff` today — no real risk now. But any future service added to `bff` inherits full O&M internal access with no explicit grant.

- [ ] **A (recommended):** Accept namespace-level. Update BFF LLD §10.1 to match O&M rev 2.30. Rule: no other service may be placed in `bff` without an O&M security review.
- [x] **B:** Ask O&M to tighten to pod-level — SPIFFE principal + path restriction, matching RP's `bffCaller` approach.

**Decision (IAM reply, 22 Oct):** Option B. The context above was wrong on both counts. O&M had **never** admitted `bff` at all — I-1 would have been denied in every env. RP had admitted the **`gateway` namespace** (not `bff`), meaning every gateway pod had access to all RP internal routes while the BFF was blocked. Both are corrected to pod-level by SPIFFE principal + exact path (O&M: PR #33 rev 2.37; RP: PR #27 v1.35). The "no other service in `bff`" caveat is no longer needed — both charts refuse to render without an explicit pod selector.

**BFF actions (A-1):**
- Update BFF LLD §10.1: "pod-level, by SPIFFE principal and exact path, on both O&M and RP." Drop the namespace-wide-grant rule.
- Ensure pods run as SA `bff-signup` in namespace `bff` and carry label `app.kubernetes.io/name: bff-signup`.
- Enable Istio sidecar injection with mTLS in the `bff` namespace (Helm chart + SRE). A pod without a sidecar has no SPIFFE principal and is denied at the Istio L7 layer.
- Call only `POST /api/v1/internal/tenants` on O&M (I-1) and only `POST /api/v1/internal/tenants/provision-trial` (RP-1) / `provision-direct` (RP-2) on RP. Any other path is denied.

---



### A-2 — RP still has `gateway` in existingCallerNamespaces (security gap)

**Context:** RP LLD rev 1.27 added `gateway` to `existingCallerNamespaces`. The BFF moved to `bff` (BFF-1 decision) but the old `gateway` blanket allow was never removed. Any pod in `gateway` currently has unrestricted access to all RP internal routes — including provision-trial, convert, realm read. The new `bffCaller` pod-level rule already covers the BFF correctly.

- [x] **A (recommended):** Urgent fix — ask RP team to remove `gateway` from `existingCallerNamespaces` in all three env values files before any environment is deployed.
- [ ] **B:** Confirm intentional — `gateway` namespace is trusted and the blanket allow is acceptable by design. Document it in RP LLD §10.

**Decision (IAM reply, 22 Oct):** Option A, and went further. `gateway` is removed from RP's `existingCallerNamespaces`. The NetworkPolicy rule for `gateway` is now rendered only when RP's gateway route is explicitly enabled (currently off in every env); the chart refuses to render if a gateway rule is listed without the route enabled. Merged: RP LLD v1.36 (PR #28). Nothing changes for the BFF — the pod-level rule from A-1 already covers it.

---



### A-3 — Abandoned slug_conflict signup (orphaned Keycloak user)

**Context:** If a user closes the browser after getting `409 slug_taken` (without picking a new slug via SB-4), the result is: Keycloak user exists, no tenant row, event stuck in O&M DLQ. The backstop cannot recover this — only SB-4 with a new slug can. User is left with an orphaned Keycloak account and cannot sign up again with the same email.

- [x] **A (recommended):** Add a slug availability pre-check before RP-1. BFF calls O&M's existing `GET /api/v1/public/tenants/resolve?identifier=<slug>` before creating the Keycloak user. Eliminates most orphans. Small race window remains.
- [ ] **B:** IAM DLQ cleanup job — a scheduled job deletes orphaned Keycloak users from DLQ'd `slug_already_taken` events after N days. Requires IAM team work.
- [ ] **C:** Accept and document. Known edge case. DLQ alert treats `slug_already_taken` as expected, not an incident. Support resolves manually on request.

**Decision (IAM reply, 22 Oct):** Option A + corrected B + alerting half of C. IAM confirmed the problem is worse than A-3 described: orphaned users can never re-register (RP-1 returns `409 USER_EMAIL_CONFLICT` on the same email) and `trial-expiry` never reaps them because `trial_ends_at` is never set without a `TrialStarted` event. The problem is not limited to slug conflicts — any I-1 failure the backstop cannot recover leaves the same orphan.

- **A (BFF, server-side):** Call PUB-1 (`GET /api/v1/public/tenants/resolve?identifier=<slug>`) before RP-1. O&M now admits `bff/bff-signup` on this path (PR #34 rev 2.38). On `found: true`, return `slug_taken` before any Keycloak user is created. A 5xx from PUB-1 means "couldn't check" — do not block the signup.
- **Corrected B (RP, not DLQ-coupled):** RP's new `orphaned-signup-reap` job finds trial signups that never received `TrialStarted` within 48 hours, deletes the Keycloak user (freeing the email), and scrubs RP's record. Covers all orphan causes, not just slug conflicts. No IAM DLQ coupling.
- **Alerting (O&M):** New `IAMSignupProvisioningRejected` warning alert fires when the backstop dead-letters a signup. Severity: warning only — one signup needs support follow-up, not an incident. Runbook: no action unless the user contacts support within 48 h; support can complete the original signup via I-1 with the same `tenant_id` and a free slug.

**BFF actions (A-3):**
- Add server-side PUB-1 pre-check before RP-1; return `slug_taken` on `found: true`.
- Treat any PUB-1 5xx as "couldn't check" — proceed to RP-1, do not fail the signup.
- Keep SB-4 for I-1 `409 slug_already_taken`; reuse the same `tenant_id` on the retry.
- Update slug_taken UX copy to note that a slug can be unavailable even if it appears unused, because offboarded tenants keep their slug (O&M invariant T-6).

---



### A-4 — Terms acceptance audit record

**Context:** The signup form collects `accept_terms: true` and `terms_version: "2026-09"`. The BFF validates these but stores nothing — no audit trail. GDPR and most SaaS legal frameworks require a verifiable record. Confirmed from O&M LLD: I-1 DTO has no `terms_version` field today.

- [x] **A (variant):** Send `terms_acceptance` on RP-1/RP-2 (not I-1). RP writes the record durably to Audit Log in the same transaction that creates the owner account. O&M takes nothing.
- [ ] **B:** BFF writes a `terms.accepted` event to the Audit Log (best-effort, fail-open) after I-1 succeeds. No O&M change needed.
- [ ] **C:** Legal has confirmed not required at this stage. Document and revisit before first paid plan launch.

**Decision (IAM reply, 22 Oct):** Variant of A. Not via I-1/O&M — O&M holds almost no personal data, and a backstop-created tenant would have no record. Not via B — the BFF isn't an admitted Audit Log identity and a fail-open write isn't verifiable proof. Instead: BFF sends `terms_acceptance` on every RP-1/RP-2 call; RP records it durably in `pending_audit_entries` (same path RP uses for IdP config changes) in the same transaction as the owner account. Delivered to Audit Log as `legal.terms.accepted` (`TermsAccepted`). Interim Legal guidance: 7-year retention, **not** redacted by GDPR erasure.

Pending: Audit Log entry type (PR #10 rev 0.31, merged 23 Oct) merges before RP's change (PR #32 v1.41, in review).

**Contract — `terms_acceptance` on RP-1 and RP-2 (required):**

```jsonc
"terms_acceptance": {
  "version":     "2026-09",              // 1–32 chars [A-Za-z0-9._-], starts with letter or digit
  "accepted_at": "2026-10-22T09:15:02Z", // RFC 3339 UTC — server time when BFF validated accept_terms: true
  "ip_address":  "203.0.113.7",          // end-user IP (IPv4 or IPv6), from trusted proxy headers only
  "user_agent":  "Mozilla/5.0 …"         // browser User-Agent, max 512 chars UTF-8, no control characters
}
```

- Missing or invalid fields → `400 INVALID_REQUEST`. `accepted_at` must be within 24 h before to 10 min after RP's clock.
- Retry with same Idempotency-Key replays the original answer even after 24 h.
- One record per owner + terms version — a retried RP-1 never creates a second record; a later new-version acceptance creates a new record.
- No terms fields on I-1.

**BFF actions (A-4):**
- Send `terms_acceptance` on every RP-1 call (and RP-2 for direct-paid), including retries with the same Idempotency-Key and identical values.
- Derive `ip_address` from `X-Forwarded-For` believing only hops added by your own trusted proxies (the gateway); do not take the leftmost value blindly.
- Truncate `user_agent` to 512 bytes of valid UTF-8 (no control characters) before sending.
- Continue refusing the signup if `accept_terms != true` or `terms_version` is not the current published version.
- Send nothing on I-1.

---

## Open item: RP-2 caller confirmation (IAM × Billing × Signup)

IAM HLD §10.7 says Billing calls RP-2 (`provision-direct`) on payment success. BFF documents (BFF-5) say the BFF calls it using the `billing_subscription_id` returned by Billing. Today only the BFF is admitted to RP-2.

**Action:** Confirm which service(s) call RP-2. If Billing also calls it, Billing needs its own RP-2 rule and the HLD must name both. **Owner: Signup + Billing + IAM to align.**