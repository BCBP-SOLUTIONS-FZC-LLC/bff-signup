# Operations

## Deployment

- Namespace: `bff` | ServiceAccount: `bff-signup`
- Helm chart: `deploy/helm/` with `values-{dev,staging,prod}.yaml`
- Replicas: 2 min, 5 max (CPU HPA at 70%)
- Container: `250m` CPU request / `1` CPU limit (PBKDF2 headroom), `128Mi` / `256Mi` memory

## Secrets (External Secrets → OpenBao)

| Secret key | OpenBao path | Purpose |
|---|---|---|
| `VALKEY_URL` | `secret/data/bff/bff-signup/valkey` | Valkey connection string |
| `SBFF_HMAC_KEY` | `secret/data/bff/bff-signup/hmac` | Email HMAC key (≥32 bytes) |
| `SBFF_TURNSTILE_SECRET` | `secret/data/bff/bff-signup/turnstile` | Turnstile server-side secret |
| `CAPTCHA_SITE_KEY` | `secret/data/bff/bff-signup/captcha_site_key` | Turnstile public site key |

Never in env files, images, or git.

## Alerts (LLD §11.5)

| Alert | Condition | Route |
|---|---|---|
| Provisioning failure rate | `failed/submitted > 5%` for 10 min | Web/Growth on-call |
| O&M lag | any `stage=org_membership, reason=exhausted` in 15 min | Web/Growth → IAM |
| Contract error | any `reason=contract_error` | Web/Growth + IAM (page) |
| Backstop activity rising | `iam_org_membership_tenant_provisioning_backstop_total{result="created"}` | Web/Growth |
| RP-1 unavailable | `stage=realm_provisioner` > 10 in 5 min | Web/Growth → IAM |
| CAPTCHA errors | `result=error` > 5% for 5 min | Web/Growth |
| Valkey down | readiness failing on all pods | Web/Growth + SRE |
| SLO burn | 2% monthly error budget in 1 h | Web/Growth |

**On-call rotation:** Web/Growth primary, IAM escalation for RP-1/O&M failures. Names TBD (BFF-6, open).

## SLOs

| SLO | Target |
|---|---|
| SB-2 p99 latency (no-retry path) | ≤ 1.5 s |
| Availability | 99.9% monthly (non-5xx on SB-1/SB-2) |
| Rate limit | 3 signups/IP/hour |

## Blockers Before Go-Live

| Item | Owner | Status |
|---|---|---|
| Mesh trust domain confirmed | SRE | Open |
| SNS filter for `tenant-orgm-q` includes `TenantIdentityProvisioned` | SRE | Open |
| DNS, TLS, WAF, Envoy HTTPRoute for `signup.bcbpsolutions.com` | SRE | Open |
| On-call rotation names | Web/Growth lead | Open |
| TL decisions A-1 to A-4 | TL | Open — see `docs/lld/signup-bff-open-questions-tl.md` |

## Runbook: Provisioning Stuck in tenant_pending

1. Check `signup_bff_provisioning_failed_total{stage="org_membership"}` — is I-1 returning errors?
2. Check O&M service health and logs.
3. If O&M is up: check if the tenant row already exists (the backstop may have created it).
4. If the backstop hasn't fired: check the SNS subscription filter for `tenant-orgm-q` — `TenantIdentityProvisioned` must be included.
5. The user can retry by revisiting the signup page — the handoff will resume if within 15 minutes.

## Runbook: CAPTCHA Provider Errors

1. Check `signup_bff_captcha_challenged_total{result="error"}` — is this Turnstile being unreachable?
2. Check if `challenges.cloudflare.com:443` is reachable from the BFF pod.
3. If mesh runs `REGISTRY_ONLY`, confirm the Istio `ServiceEntry` for Turnstile is deployed (values-prod.yaml `serviceEntry.enabled: true`).

## Valkey Key Reference (LLD §4.1)

| Key pattern | TTL | Purpose |
|---|---|---|
| `sbff:sess:<sid>` | 30 min (sliding) | Session: csrf_hash, signup_id |
| `sbff:handoff:<signup_id>` | 30 min (10 min after completed) | Provisioning state machine |
| `sbff:lock:<signup_id>` | 15 s | Distributed lock |
| `sbff:rl:ip:<ip>` | 1 h | Per-IP signup counter |
| `sbff:rl:email:<hmac>` | 24 h | Per-email attempt counter |
| `sbff:rl:sess:<ip>` | 1 h | Per-IP session counter |
| `sbff:rl:poll:<sid>` | 1 min | Per-session poll counter |

Losing Valkey loses only in-flight signups and counters — no business data. In-flight signups with `identity_created` state will be recovered by the O&M backstop.
