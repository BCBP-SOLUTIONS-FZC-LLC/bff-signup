# bff-signup — TL Discussion: Open Gaps Before Go-Live

**Date:** 2026-10-23
**Author:** Sharmila
**Purpose:** Items that need TL awareness or a decision before the service can go live. All BFF design decisions and code are closed. What remains is process, IAM confirmation, and prod infrastructure.

---

## Summary

| # | Item | Category | Go-live blocker? | Who decides / acts |
|---|---|---|---|---|
| 1 | On-call model not set up | Product / Process | **Yes** | Web/Growth TL |
| 2 | Unverified Keycloak user cleanup not confirmed | IAM dependency | No | IAM team |
| 3 | Keycloak password policy not confirmed | IAM dependency | No | IAM team |
| 4 | RP-2 caller not confirmed (BFF or Billing?) | Cross-team alignment | **Yes** | Signup + Billing + IAM |
| 5 | Prod SRE infra not provisioned | Infra / SRE | **Yes (at prod deploy)** | SRE |

---

## Gap 1 — On-call model not set up (BFF-6)

### What is missing

No one is defined as the on-call owner for `bff-signup`. If `signup.bcbpsolutions.com` fails in production, there is no page, no escalation path, and no runbook wired to an alerting tool.

### What goes wrong if not fixed before go-live

- A signup outage at night or on a weekend goes unnoticed until a user complains.
- When the root cause is inside IAM (RP-1 or O&M failure), there is no defined path to escalate to the IAM team — the BFF owner is stuck.
- The `IAMSignupProvisioningRejected` O&M alert (fires when the backstop dead-letters a signup) has nowhere to route and no one receives it. Affected users are permanently orphaned until someone manually checks.

### What needs to happen (5 steps)

**Step 1 — TL names the L1 on-call owner**
Who picks up the page first when signup is broken? This is a Web/Growth team member or rotation. TL decides and announces.

**Step 2 — Ask IAM for their L2 escalation contact**
When signup fails because RP-1 or O&M is the root cause, the BFF owner needs someone to escalate to. One Slack message to IAM: *"Who do we page for RP-1 / O&M failures affecting signup?"*

**Step 3 — Create the bff-signup service in PagerDuty / OpsGenie**
```
Service: bff-signup
L1 (0 min):  <primary BFF owner from Step 1>
L2 (15 min): <IAM contact from Step 2>  ← only if L1 hasn't acked
```

**Step 4 — Wire two alerts into that service**

| Alert | Where it comes from | What it means |
|---|---|---|
| `IAMSignupProvisioningRejected` | O&M metrics (already defined in A-3 decision) | Backstop dead-lettered a signup — one user can't complete signup and needs support |
| BFF 5xx spike | BFF metrics port `:9090` | Signups are failing at the API level |

**Step 5 — Add the runbook URL to the PagerDuty service**
The backstop runbook is in LLD §9.4. Paste the link into the PagerDuty service description so the on-call engineer sees it immediately when paged. No new runbook to write.

### Time to fix
Under 1 hour total — once TL answers Step 1 and IAM answers Step 2, Steps 3–5 are mechanical.

---

## Gap 2 — Unverified Keycloak user cleanup not confirmed (N-3)

### What is missing

IAM has not confirmed that a cleanup job exists for Keycloak users in the `trial` realm who never verified their email (i.e. started signup but abandoned it before clicking the verification link).

### What goes wrong if not fixed

A user starts signup → Keycloak user is created (RP-1 succeeds) → user closes the browser before verifying email → user tries to sign up again a week later → RP-1 returns `409 USER_EMAIL_CONFLICT` → **the user can never sign up again with that email address.**

The RP `orphaned-signup-reap` job (from A-3) handles the case where I-1 never succeeds. But it does not handle the case where the user verified their email, logged in, but never completed the tenant setup — or the simpler case of just abandoning signup before email verification. Those users need a separate cleanup path.

### What needs to happen

IAM team to confirm:
1. Does a job exist that expires / deletes unverified Keycloak users in the `trial` realm after N days?
2. If yes: what is N? Does it cover users who started signup but never verified?
3. If no: IAM to build it or confirm the timeline.

**No BFF code change is needed.** This is purely an IAM-side confirmation.

---

## Gap 3 — Keycloak `trial` realm password policy not confirmed (N-10)

### What is missing

The BFF enforces its own password policy at signup time:
- Algorithm: PBKDF2-SHA512
- Iterations: 210,000
- Min length: 12 characters
- Max length: 128 characters
- Required classes: 3 (uppercase, lowercase, digit, or special — any 3)
- Must not equal the email address

IAM has not confirmed that the Keycloak `trial` realm is configured with the same policy.

### What goes wrong if not fixed

Two scenarios:

**Scenario A — BFF is stricter than Keycloak:**
A password the BFF accepts may be weaker than intended if Keycloak's realm policy is even more permissive. The PBKDF2 hash sent to RP-1 is correct, but if someone manages to set a password through another path (Keycloak admin, future flow), the policy won't match.

**Scenario B — Keycloak is stricter than BFF:**
A password the BFF accepts and hashes may be rejected by Keycloak internally when creating the user credential, causing a silent RP-1 failure that is hard to diagnose.

Either mismatch creates a policy split that is only discovered during testing or after go-live.

### What needs to happen

IAM to:
1. Verify the `trial` realm in Keycloak admin has the same policy as BFF enforcement above.
2. Add a joint-release rule to the IAM runbook: any change to the Keycloak `trial` realm password policy must be co-ordinated with the BFF team (or reflected in a BFF config update).

**No BFF code change is needed.**

---

## Gap 4 — RP-2 caller not confirmed (cross-team)

### What is missing

There is a conflict in the documentation:
- **IAM HLD §10.7** says Billing calls RP-2 (`POST /internal/tenants/provision-direct`) on payment success.
- **BFF LLD BFF-5** says the BFF calls RP-2 using the `billing_subscription_id` returned by Billing.

Today **only the BFF is admitted to RP-2** in the IAM charts. Billing has no RP-2 rule.

### What goes wrong if not confirmed

- If Billing is the caller and no one updates the IAM charts, Billing's call to RP-2 will be denied by Istio (`403 forbidden_peer`) in every environment — direct-paid signup silently fails at the first production attempt.
- If both should call RP-2 and nobody adds Billing's rule, same result.
- This is Phase 1.5 (direct-paid) but needs to be resolved before that phase starts — not discovered on the day of Billing integration.

### What needs to happen

A three-way alignment between **Signup BFF**, **Billing**, and **IAM**:
1. Confirm: does the BFF call RP-2, or does Billing call it, or both?
2. If Billing also calls RP-2 → IAM adds a pod-level rule for Billing's service account + RP-2 exact path.
3. Update IAM HLD §10.7 to name the actual caller(s).

**This is Phase 1.5 but should be answered now** so IAM can plan the chart change before Billing integration begins.

---

## Gap 5 — Prod SRE infra not provisioned (BFF-1 value, BFF-2 SRE, BFF-4 SRE)

### What is missing

These are production environment provisioning steps. They are **not blocking development** and require no BFF code changes. They become blocking the moment the service moves to staging or production.

| Item | What SRE needs to provision |
|---|---|
| BFF-1 value | Confirm the prod SPIFFE mesh trust domain. If it is not `cluster.local`, update `bffCaller.istio.trustDomain` in RP `values-prod.yaml` and the BFF Helm chart. |
| BFF-2 SRE | Add `TenantIdentityProvisioned` to the SNS subscription filter on `tenant-orgm-q` in dev, staging, and prod. Without this the O&M backstop consumer never receives the event and the backstop does not function. Verified by `iam_org_membership_tenant_provisioning_backstop_total` metric increasing after a test signup. |
| BFF-4 SRE | Provision `signup.bcbpsolutions.com`: DNS A/CNAME → ALB, ACM TLS cert, AWS WAF managed rules + IP reputation list, Envoy HTTPRoute (no `jwt_authn`/`ext_authz`), Istio sidecar injection + mTLS in `bff` namespace, Istio `ServiceEntry` for `challenges.cloudflare.com:443` if mesh runs `REGISTRY_ONLY`. |

### What goes wrong if not done at prod deploy

- **BFF-1 value:** If trust domain differs from `cluster.local` and is not updated, every RP-1 and I-1 call fails with `403 forbidden_peer` — Istio rejects the SPIFFE principal.
- **BFF-2 SRE:** The `TenantIdentityProvisioned` event never reaches the O&M backstop consumer. Any signup where I-1 times out inline is permanently stuck — no retry, no tenant row created.
- **BFF-4 SRE:** The service is not reachable from the internet at all. Signup form cannot be loaded.

### Action

Raise a SRE ticket for BFF-4 SRE now (long lead time for DNS + WAF + cert). BFF-1 value and BFF-2 SRE can be done closer to the prod deploy date.

---

## Decision requests from TL

| # | Decision needed | From |
|---|---|---|
| D-1 | Who is the L1 on-call owner for `bff-signup`? | Web/Growth TL |
| D-2 | Does the BFF or Billing (or both) call RP-2 for direct-paid signup? | TL + Billing lead + IAM |
| D-3 | Raise SRE ticket for BFF-4 SRE infra now (long lead time)? | TL → SRE |

---

## References

| Document | Location |
|---|---|
| BFF LLD | `docs/lld/signup-bff-lld.md` |
| Open questions (A-1 to A-4, all resolved) | `docs/lld/signup-bff-open-questions-tl.md` |
| Action tracker | `docs/lld/signup-bff-action-tracker.md` |
| IAM reply (A-1 to A-4) | `~/Downloads/iam-to-signup-bff-reply-a1-2026-10-22.md` |
