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

Source: `deploy/monitoring/app-alerts.yml` + `slo-rules.yml`; deployed by the Helm `PrometheusRule` (a generated copy — CI fails if they drift). Runbooks: `docs/observability/runbooks.md`.

| Alert | Condition | Route |
|---|---|---|
| `SignupBFFDown` / `SignupBFFReplicasMissing` | no healthy pod 2 min / < 2 healthy 5 min | Web/Growth |
| `SignupProvisioningFailureRate` | `failed/submitted > 5%` for 10 min | Web/Growth on-call |
| `SignupOrgMembershipLag` | any `stage=org_membership, reason=exhausted` in 15 min | Web/Growth → IAM |
| `SignupContractError` | any `reason=contract_error` | Web/Growth + IAM (page) |
| `SignupRealmProvisionerUnavailable` | > 20% RP-1/RP-2 call errors for 5 min | Web/Growth → IAM |
| `SignupBillingUnavailable` | > 20% Billing B-I1 call errors for 10 min | Web/Growth → Commercial |
| `SignupBackstopActivityHigh` | `iam_org_membership_tenant_provisioning_backstop_total{result="created"}` > 5 in 15 min | Web/Growth |
| `SignupCaptchaErrorRate` | `result=error` > 5% for 5 min | Web/Growth |
| `SignupValkeyDown` | readiness failing | Web/Growth + SRE |
| `SignupSLOFastBurn` / `SignupSLOSlowBurn` | SLO-1 (5xx on POST /trial + /paid) burn 14.4× / 2×; `metric_status: proposed` | Web/Growth |
| `SignupProvisioningSLOBreach` | SLO-2 < 99% over 1 h | Web/Growth |

**On-call rotation:** Web/Growth primary, IAM escalation for RP/O&M failures, Commercial (Billing) escalation for B-I1 failures. Names TBD (BFF-6, deferred until PagerDuty is procured).

## SLOs

| SLO | Target |
|---|---|
| SB-2 p99 latency (no-retry path) | ≤ 1.5 s (`platform_http_request_duration_seconds`, dashboard only) |
| SLO-1 Availability | 99.9% monthly (non-5xx on POST /trial + /paid; `platform_http_requests_total`) |
| SLO-2 Provisioning success | ≥ 99% of submitted signups complete |
| Rate limit | 3 signups/IP/hour |

## Blockers Before Go-Live

| Item | Owner | Status |
|---|---|---|
| Mesh trust domain confirmed | SRE | Open |
| SNS filter for `tenant-orgm-q` includes `TenantIdentityProvisioned` | SRE | Open |
| DNS, TLS, WAF, Envoy HTTPRoute for `signup.bcbpsolutions.com` | SRE | Open |
| On-call rotation names | Web/Growth lead | Deferred (BFF-6) |
| Paid signup: Billing go-live order (SRE-3, Billing `FEATURE_DIRECT_SIGNUP`, Pact, Test-Clock E2E) | Billing + Web/Growth | Open — LLD §19.5 |

## Runbook: Provisioning Stuck in tenant_pending

1. Check `bff_signup_provisioning_failed_total{stage="org_membership"}` — is I-1 returning errors?
2. Check O&M service health and logs.
3. If O&M is up: check if the tenant row already exists (the backstop may have created it).
4. If the backstop hasn't fired: check the SNS subscription filter for `tenant-orgm-q` — `TenantIdentityProvisioned` must be included.
5. The user can retry by revisiting the signup page — the handoff will resume if within 15 minutes.

## Runbook: CAPTCHA Provider Errors

1. Check `bff_signup_captcha_challenged_total{result="error"}` — is this Turnstile being unreachable?
2. Check if `challenges.cloudflare.com:443` is reachable from the BFF pod.
3. If mesh runs `REGISTRY_ONLY`, confirm the Istio `ServiceEntry` for Turnstile is deployed (values-prod.yaml `serviceEntry.enabled: true`).

## Paid signup (SB-5, LLD §19)

| Setting | Value | Notes |
|---|---|---|
| `PAID_SIGNUP_ENABLED` | `false` | `/paid` returns 404 until set `true` at Billing go-live step 4 |
| `BILLING_BASE_URL` | `http://commercial-billing.billing.svc.cluster.local:8080` | Required when enabled |
| `RP2_TIMEOUT_MS` | `8000` | RP-2 timeout (RP p95 ≤ 4 s); `RP_TIMEOUT_MS` (RP-1) is 2000 |
| `BILLING_TIMEOUT_MS` | `15000` | Startup rejects < 15000 when enabled (B-I1 p99 12 s) |
| `PAID_REQUEST_DEADLINE_MS` | `30000` | SB-5 deadline; also gincommon's outer request timeout and the server `WriteTimeout` (+500 ms) when enabled |

- Billing admits `bff`/`bff-signup` on 8080 (BIL-AUTH-3); this chart's egress already allows 8080. The Envoy Gateway route timeout for `/paid` must be ≥ 30 s (not in this chart).
- Billing down or slow → `503` + `Retry-After`; the handoff is kept and a resubmit reuses the same `charge:<tenant_id>` key, so nothing is charged twice. Watch `bff_signup_dependency_call_duration_seconds{dependency="billing"}`.
- `403 INSUFFICIENT_ROLE` from Billing (seen as `500` to the user) means the mesh identity is wrong or the sidecar is not forwarding the client certificate.

## Valkey Key Reference (LLD §4.1)

| Key pattern | TTL | Purpose |
|---|---|---|
| `sbff:sess:<sid>` | 30 min (sliding) | Session: csrf_hash, signup_id |
| `sbff:handoff:<signup_id>` | 30 min (10 min after completed) | Provisioning state machine (trial and paid) |
| `sbff:lock:<signup_id>` | 15 s | Distributed lock |
| `sbff:rl:ip:<ip>` | 1 h | Per-IP signup counter |
| `sbff:rl:email:<hmac>` | 24 h | Per-email attempt counter |
| `sbff:rl:sess:<ip>` | 1 h | Per-IP session counter |
| `sbff:rl:poll:<sid>` | 1 min | Per-session poll counter |

Losing Valkey loses only in-flight signups and counters — no business data. In-flight signups with `identity_created` state will be recovered by the O&M backstop. A paid signup lost between Billing confirmation and RP-2 leaves a paid subscription without a tenant: reconcile with Billing by `tenant_id`.
