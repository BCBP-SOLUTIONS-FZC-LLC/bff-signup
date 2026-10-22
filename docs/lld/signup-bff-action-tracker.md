# Signup BFF — Action Tracker

Date: 2026-10-22 · LLD ref: `signup-bff-lld.md` v0.5
TL decisions tracked separately in `signup-bff-open-questions-tl.md`.

---

## Section B — Team assignments (no TL decision needed)

| # | What | Owner | Blocks go-live? | Done? |
|---|---|---|---|---|
| BFF-1 | Confirm Istio mesh trust domain for prod. LLD assumes `cluster.local`. If different, update `bffCaller.istio.trustDomain` in RP `values-prod.yaml` and BFF Helm chart. | SRE | **Yes** | [ ] |
| BFF-2 SRE | Add `TenantIdentityProvisioned` to SNS subscription filter for `tenant-orgm-q` in prod, staging and dev. Verify with `iam_org_membership_tenant_provisioning_backstop_total` metric. | SRE | **Yes** | [ ] |
| BFF-4 SRE | Provision for `signup.bcbpsolutions.com`: DNS A/CNAME → ALB, ACM TLS cert, AWS WAF managed rules + IP reputation, Envoy HTTPRoute (no `jwt_authn`/`ext_authz`), Istio `ServiceEntry` for `challenges.cloudflare.com:443` if mesh runs `REGISTRY_ONLY`. | SRE | **Yes** | [ ] |
| BFF-6 | Name on-call rotation: (1) primary BFF owner, (2) IAM escalation contact for RP-1/O&M failures. Wire into PagerDuty/OpsGenie. | Web / Growth lead | **Yes** | [ ] |
| N-10 | IAM to confirm Keycloak `trial` realm matches BFF password policy: PBKDF2-SHA512, 210,000 iterations, min 12, max 128, 3 classes, not-equal-email. Add joint-release rule to IAM runbook. | IAM + Web / Growth | No | [ ] |
| IAM LLDs | Update RP LLD (currently rev 1.31) and O&M LLD (currently rev 2.30) to document `TenantIdentityProvisioned` and the backstop consumer — currently in code (RP #19/#20, O&M #25) but missing from both LLDs. | IAM team | No | [ ] |
| BFF LLD | After TL decisions and IAM LLD updates: update BFF LLD §17.2 O&M error codes to `UPPER_SNAKE_CASE` (e.g. `SLUG_ALREADY_TAKEN`, `TRIAL_ALREADY_USED`), update sibling LLD version refs (RP v1.29 → 1.31, O&M rev 2.28 → 2.30), close N-12 in §16.2. | Sharmila | No | [ ] |

---

## Section C — Closed (acknowledged, no action needed)

| # | Question | Answer | Closed by |
|---|---|---|---|
| N-12 | Login before tenant row exists — what does the user see? | I-8 returns `404` → AuthZ Enrichment → `403` to browser. Hard fail, no grace period, no redirect loop. BFF's `tenant_pending` + "workspace_setting_up" UI warning already covers this correctly. No IAM code change needed. | O&M LLD rev 2.30, I-8 invariant I8-3 |
| TL-1 (pending) | O&M admission level | Namespace-level is the as-shipped state (O&M rev 2.30 §10.2). TL decision in A-1 formally closes this. | O&M LLD rev 2.30 |
