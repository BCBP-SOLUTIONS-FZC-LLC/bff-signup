# Signup BFF — Open Questions for TL Decision

Date: 2026-10-22 · Author: Sharmila · LLD ref: `signup-bff-lld.md` v0.5

These 4 items need a TL decision before anything moves. TL to tick one option per item.
Team assignments and closed items are tracked separately in `signup-bff-action-tracker.md`.

---

### A-1 — O&M admission level (namespace vs pod-level)

**Context:** LLD §10.1 specified pod-level admission for `bff-signup` only. O&M implemented namespace-level (`bff` in `existingCallerNamespaces`), giving the whole `bff` namespace blanket access to all O&M internal routes. RP implemented pod-level correctly. O&M LLD rev 2.30 now documents this as the official state.

**Risk:** Only `bff-signup` runs in `bff` today — no real risk now. But any future service added to `bff` inherits full O&M internal access with no explicit grant.

- [ ] **A (recommended):** Accept namespace-level. Update BFF LLD §10.1 to match O&M rev 2.30. Rule: no other service may be placed in `bff` without an O&M security review.
- [ ] **B:** Ask O&M to tighten to pod-level — SPIFFE principal + path restriction, matching RP's `bffCaller` approach.

---



### A-2 — RP still has `gateway` in existingCallerNamespaces (security gap)

**Context:** RP LLD rev 1.27 added `gateway` to `existingCallerNamespaces`. The BFF moved to `bff` (BFF-1 decision) but the old `gateway` blanket allow was never removed. Any pod in `gateway` currently has unrestricted access to all RP internal routes — including provision-trial, convert, realm read. The new `bffCaller` pod-level rule already covers the BFF correctly.

- [ ] **A (recommended):** Urgent fix — ask RP team to remove `gateway` from `existingCallerNamespaces` in all three env values files before any environment is deployed.
- [ ] **B:** Confirm intentional — `gateway` namespace is trusted and the blanket allow is acceptable by design. Document it in RP LLD §10.

---



### A-3 — Abandoned slug_conflict signup (orphaned Keycloak user)

**Context:** If a user closes the browser after getting `409 slug_taken` (without picking a new slug via SB-4), the result is: Keycloak user exists, no tenant row, event stuck in O&M DLQ. The backstop cannot recover this — only SB-4 with a new slug can. User is left with an orphaned Keycloak account and cannot sign up again with the same email.

- [ ] **A (recommended):** Add a slug availability pre-check before RP-1. BFF calls O&M's existing `GET /api/v1/public/tenants/resolve?identifier=<slug>` before creating the Keycloak user. Eliminates most orphans. Small race window remains.
- [ ] **B:** IAM DLQ cleanup job — a scheduled job deletes orphaned Keycloak users from DLQ'd `slug_already_taken` events after N days. Requires IAM team work.
- [ ] **C:** Accept and document. Known edge case. DLQ alert treats `slug_already_taken` as expected, not an incident. Support resolves manually on request.

---



### A-4 — Terms acceptance audit record

**Context:** The signup form collects `accept_terms: true` and `terms_version: "2026-09"`. The BFF validates these but stores nothing — no audit trail. GDPR and most SaaS legal frameworks require a verifiable record. Confirmed from O&M LLD: I-1 DTO has no `terms_version` field today.

- [ ] **A:** Add `terms_version` + acceptance timestamp to the I-1 request body. O&M stores it on the tenant row. Requires O&M DTO change + migration.
- [ ] **B:** BFF writes a `terms.accepted` event to the Audit Log (best-effort, fail-open) after I-1 succeeds. No O&M change needed.
- [ ] **C:** Legal has confirmed not required at this stage. Document and revisit before first paid plan launch.

**Note:** If A or B is chosen, confirm exact requirement with Legal before building.