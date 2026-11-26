# Runbooks: bff-signup alerts

Runbooks for every alert in `deploy/monitoring/app-alerts.yml` and `deploy/monitoring/slo-rules.yml`
(deployed together by `deploy/helm/templates/prometheusrule.yaml`). Each alert's `runbook`
annotation links to its heading here. Alerts labelled `metric_status: proposed` are built on a
metric the Platform Observability Registry has not ratified yet (`platform_http_requests_total`).

Common starting points:
- Pods: `kubectl get pods -n bff -l app.kubernetes.io/name=bff-signup`
- Logs: `kubectl logs -n bff -l app.kubernetes.io/name=bff-signup --tail=100`
- Readiness: `GET /readyz` — 200 = Valkey reachable; 503 = Valkey down (fail-closed)

---

## Availability

### SignupBFFDown
**Meaning.** No pod scraped as healthy for 2 minutes. Signup is completely unavailable.

**Triage.**
1. Check pod status: `kubectl describe pod -n bff`
2. Startup panics from bad configuration: missing `VALKEY_URL`, `SBFF_HMAC_KEY`, etc.
3. Valkey unreachable: `/readyz` returns 503.

**Mitigate.** Roll back last deploy or fix configuration.

### SignupBFFReplicasMissing
**Meaning.** Fewer than 2 replicas healthy. One more disruption takes signup down.

**Triage.** Check node pressure and pending pods. Review HPA: `kubectl get hpa -n bff`.

---

## Provisioning

### SignupProvisioningFailureRate
**Meaning.** >5% of signups are failing at the provisioning stage for 10 minutes.

**Triage.**
1. Check the `mode` (trial/paid), `stage` and `reason` labels on `bff_signup_provisioning_failed_total`. Today only `stage=org_membership` is recorded (I-1 exhausted or contract error).
2. Check the dashboard's dependency error ratio panel: RP (`realm_provisioner`), O&M (`org_membership`) and Billing (`billing`).

### SignupOrgMembershipLag
**Meaning.** Signups stuck in `tenant_pending` — O&M's I-1 not completing.

**Triage.**
1. Check O&M pod health.
2. Verify `TenantIdentityProvisioned` is in the SNS subscription filter on `tenant-orgm-q` (BFF-2 SRE).
3. Check `iam_org_membership_tenant_provisioning_backstop_total` — if it's increasing, the backstop is working.

### SignupContractError
**Meaning.** RP or I-1 returned an unexpected error (e.g. `INVALID_REQUEST` for plan). BFF bug or IAM contract change.

**Triage.** Check logs for `contract_error` reason. Compare BFF's request payload against RP/O&M LLD.

### SignupRealmProvisionerUnavailable
**Meaning.** More than 20% of RP-1/RP-2 calls failed (transport error or 5xx) for 5 minutes, from `bff_signup_dependency_call_duration_seconds_count{dependency="realm_provisioner"}`. Users get 503 and can resubmit; the same `tenant_id` and RP key are reused.

**Triage.** Check Realm Provisioner pods and Keycloak health; check the mesh AuthorizationPolicy admits `bff/bff-signup`.

### SignupBillingUnavailable
**Meaning.** More than 20% of Billing B-I1 authorize-and-charge calls failed (transport error or 5xx) for 10 minutes. Paid signups get `503` + `Retry-After`; their handoffs are kept and a resubmit reuses `charge:<tenant_id>`, so nothing is charged twice (LLD §19.4).

**Triage.**
1. Dashboard: Billing latency p95 against the 15 s `BILLING_TIMEOUT_MS`; a p95 near 15 s means timeouts, not errors.
2. Check `commercial-billing` pods in namespace `billing`, and its `PROVIDER_UNAVAILABLE` / `DB_UNAVAILABLE` rate.
3. A Billing `403 INSUFFICIENT_ROLE` surfaces as `500` to users (not counted here): check the mesh identity and the client-certificate forwarding.
4. Escalate to the Commercial (Billing) team.

### SignupBackstopActivityHigh
**Meaning.** O&M backstop created tenants — BFF's inline I-1 calls are failing.

**Triage.** Check O&M health and BFF→O&M connectivity. Backstop working means data is safe; investigate root cause.

---

## Abuse controls

### SignupCaptchaErrorRate
**Meaning.** >5% of CAPTCHA verifications are erroring (Turnstile unreachable, not just failing).

**Triage.**
1. Check Istio `ServiceEntry` for `challenges.cloudflare.com:443` if mesh runs `REGISTRY_ONLY`.
2. Check Cloudflare status page.

---

## SLO

### SignupSLOFastBurn
**Meaning.** More than 1.44% of `POST /trial` and `/paid` requests returned 5xx over 5 minutes (14.4× the 0.1% budget; at that rate a 30-day budget lasts about 2 days). Page immediately. `metric_status: proposed` (source `platform_http_requests_total`).

**Triage.** Check all availability alerts above. Follow incident runbook.

### SignupSLOSlowBurn
**Meaning.** More than 0.2% 5xx on `POST /trial` and `/paid` over 1 hour (2× budget; a 30-day budget lasts about 15 days). `metric_status: proposed`.

**Triage.** Investigate elevated error rate. Not an immediate outage but needs same-day resolution.

### SignupProvisioningSLOBreach
**Meaning.** Fewer than 99% of submitted signups (SB-2 + SB-5) completed over the last hour (SLO-2), with at least ~36 submissions an hour.

**Triage.** Usually follows a provisioning or dependency alert above. Check `bff_signup_provisioning_failed_total` and the dependency panels; on the paid path also `SignupBillingUnavailable`. Note a 3-D Secure signup waiting on the customer counts as not yet completed.

---

## Infrastructure

### SignupValkeyDown
**Meaning.** Readiness probe failing — Valkey unreachable. All signup endpoints return 503.

**Triage.**
1. Check Valkey pod: `kubectl get pods -n bff -l app=valkey`.
2. Check `VALKEY_URL` env var is correct.
3. Check network policy allows BFF → Valkey.
