# Signup BFF — Action Tracker

Date: 2026-10-22 · LLD ref: `signup-bff-lld.md` v0.5
TL decisions tracked separately in `signup-bff-open-questions-tl.md` (all 4 resolved 2026-10-22 by IAM reply).

---

## Section B — Team assignments (no TL decision needed)

| # | What | Owner | Blocks go-live? | Done? |
|---|---|---|---|---|
| BFF-1 | Confirm Istio mesh trust domain for prod. LLD assumes `cluster.local`. If different, update `bffCaller.istio.trustDomain` in RP `values-prod.yaml` and BFF Helm chart. | SRE | **Yes** | [ ] |
| BFF-2 SRE | Add `TenantIdentityProvisioned` to SNS subscription filter for `tenant-orgm-q` in prod, staging and dev. Verify with `iam_org_membership_tenant_provisioning_backstop_total` metric. | SRE | **Yes** | [ ] |
| BFF-4 SRE | Provision for `signup.bcbpsolutions.com`: DNS A/CNAME → ALB, ACM TLS cert, AWS WAF managed rules + IP reputation, Envoy HTTPRoute (no `jwt_authn`/`ext_authz`), Istio `ServiceEntry` for `challenges.cloudflare.com:443` if mesh runs `REGISTRY_ONLY`. Also: enable Istio sidecar injection with mTLS in the `bff` namespace (required by A-1 pod-level admission). | SRE | **Yes** | [ ] |
| BFF-6 | Name on-call rotation: (1) primary BFF owner, (2) IAM escalation contact for RP-1/O&M failures. Wire into PagerDuty/OpsGenie. | Web / Growth lead | **Yes** | [ ] |
| BFF-A1-LLD | Update BFF LLD §10.1: "pod-level, by SPIFFE principal and exact path, on both O&M and RP." Drop the "no other service in `bff` without O&M security review" rule — the IAM charts enforce this now. Refs: O&M PR #33 rev 2.37, RP PR #27 v1.35. | Sharmila | No | [x] §10.1 routes table updated: added RP-2 and PUB-1 paths; prose updated to "pod-level by SPIFFE + exact path". |
| BFF-A1-HELM | Confirm BFF Helm chart: pods run as SA `bff-signup` in namespace `bff` and carry label `app.kubernetes.io/name: bff-signup`. If the pod selector label differs, notify IAM (`signupBffCaller.podSelector` in O&M and RP charts must match). | Sharmila | **Yes** | [x] `values.yaml`: `serviceAccount.name: bff-signup`; `_helpers.tpl` `selectorLabels`: `app.kubernetes.io/name: bff-signup`. Matches IAM chart `signupBffCaller.podSelector`. |
| BFF-A3-IMPL | Implement server-side PUB-1 pre-check before RP-1: `GET /api/v1/public/tenants/resolve?identifier=<slug>`. Return `slug_taken` on `found: true`. Treat any 5xx from PUB-1 as "couldn't check" — proceed to RP-1, do not block the signup. O&M now admits `bff/bff-signup` on this path (PR #34 rev 2.38). | Sharmila | **Yes** | [x] `orgmembership/client.go` `CheckSlugFree`, `handoff_orchestrator.go` before RP-1, `port/org_membership.go`, `test/fixtures/fakes.go` |
| BFF-A3-UX | Update slug_taken UX copy to note that a slug can appear available but still be taken (offboarded tenants keep their slug, O&M invariant T-6). | Web / Growth | No | [x] `SlugTakenResponse.Message` field added to DTO; message set in both SB-2 and SB-4 slug_taken paths in `handlers.go`. LLD §17.1 updated. |
| BFF-A4-IMPL | Add `terms_acceptance` object to every RP-1 call (and RP-2 for direct-paid), including retries with the same Idempotency-Key. Fields: `version`, `accepted_at` (server time, RFC 3339 UTC), `ip_address` (end-user, from trusted proxy headers only — not leftmost `X-Forwarded-For` blindly), `user_agent` (max 512 bytes UTF-8, no control characters, truncate before sending). Send nothing on I-1. | Sharmila | **Yes** | [x] `port/realm_provisioner.go` `TermsAcceptance`, `signup_service.go` builds it, `handoff_orchestrator.go` passes it, `handlers.go` extracts User-Agent + truncates |
| BFF-A4-WAIT | RP ignores unknown JSON fields — BFF can deploy with `terms_acceptance` now (RP-1 succeeds, field silently ignored). Audit record won't be written until Audit Log PR #10 (rev 0.31) merges first, then RP PR #32 (v1.41). Confirm both PRs live with IAM before first production signup. Not a deploy blocker. | Sharmila | No | [x] Clarified in LLD §18.1. Tracking with IAM. |
| BFF-RP2-CALLER | Confirm with Billing and IAM: does the BFF or Billing (or both) call RP-2 (`provision-direct`)? IAM HLD §10.7 says Billing; BFF-5 says BFF. Today only the BFF is admitted. If Billing also calls it, Billing needs its own rule and the HLD must name both. | Sharmila + Billing lead | **Yes** | [ ] |
| N-10 | IAM to confirm Keycloak `trial` realm matches BFF password policy: PBKDF2-SHA512, 210,000 iterations, min 12, max 128, 3 classes, not-equal-email. Add joint-release rule to IAM runbook. | IAM + Web / Growth | No | [ ] |
| IAM LLDs | Update RP LLD (currently rev 1.31) and O&M LLD (currently rev 2.30) to document `TenantIdentityProvisioned` and the backstop consumer — currently in code (RP #19/#20, O&M #25) but missing from both LLDs. | IAM team | No | [ ] |
| BFF LLD | TL decisions are now resolved. Update BFF LLD: §17.2 O&M error codes to `UPPER_SNAKE_CASE` (e.g. `SLUG_ALREADY_TAKEN`, `TRIAL_ALREADY_USED`), sibling LLD version refs (RP v1.29 → 1.37, O&M rev 2.28 → 2.38), close N-12 in §16.2. Also pick up BFF-A1-LLD above. | Sharmila | No | [x] All done: §17.2 codes already `UPPER_SNAKE_CASE`; versions updated RP 1.37 / O&M 2.38 in v0.7; N-12 in §16.2; §10.1 done (BFF-A1-LLD). |

---

## Section C — Closed (acknowledged, no action needed)

| # | Question | Answer | Closed by |
|---|---|---|---|
| N-12 | Login before tenant row exists — what does the user see? | I-8 returns `404` → AuthZ Enrichment → `403` to browser. Hard fail, no grace period, no redirect loop. BFF's `tenant_pending` + "workspace_setting_up" UI warning already covers this correctly. No IAM code change needed. | O&M LLD rev 2.30, I-8 invariant I8-3 |
| TL-1 | O&M admission level | **Pod-level** by SPIFFE principal + exact path on both O&M and RP (A-1 Option B, IAM reply 22 Oct). Prior state (O&M rev 2.30 §10.2, namespace-level) was incorrect — O&M had never admitted `bff` at all. Now corrected: O&M PR #33 rev 2.37, RP PR #27 v1.35. | IAM reply 2026-10-22 |
