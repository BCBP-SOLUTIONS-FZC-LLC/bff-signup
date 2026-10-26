# Runbooks: bff-signup alerts

Runbooks for every alert in `deploy/monitoring/app-alerts.yml`, `deploy/monitoring/slo-rules.yml`
and `deploy/helm/templates/prometheusrule.yaml`.

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
1. Check `stage` and `reason` labels on `signup_bff_provisioning_failed_total`.
2. `stage=realm_provisioner` → check RP health and Keycloak.
3. `stage=org_membership` → check O&M health and Postgres.

### SignupOrgMembershipLag
**Meaning.** Signups stuck in `tenant_pending` — O&M's I-1 not completing.

**Triage.**
1. Check O&M pod health.
2. Verify `TenantIdentityProvisioned` is in the SNS subscription filter on `tenant-orgm-q` (BFF-2 SRE).
3. Check `iam_org_membership_tenant_provisioning_backstop_total` — if it's increasing, the backstop is working.

### SignupContractError
**Meaning.** RP-1 or I-1 returned an unexpected error (e.g. `INVALID_REQUEST` for plan). BFF bug or IAM contract change.

**Triage.** Check logs for `contract_error` reason. Compare BFF's request payload against RP/O&M LLD.

### SignupRealmProvisionerUnavailable
**Meaning.** RP-1 failing >10 times in 5 minutes.

**Triage.** Check Realm Provisioner pods and Keycloak health.

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
**Meaning.** Signup availability burning at 14.4× budget — exhausts in ~1 hour. Page immediately.

**Triage.** Check all availability alerts above. Follow incident runbook.

### SignupSLOSlowBurn
**Meaning.** Signup availability burning at 2× budget — exhausts in ~3 days.

**Triage.** Investigate elevated error rate. Not an immediate outage but needs same-day resolution.

---

## Infrastructure

### SignupValkeyDown
**Meaning.** Readiness probe failing — Valkey unreachable. All signup endpoints return 503.

**Triage.**
1. Check Valkey pod: `kubectl get pods -n bff -l app=valkey`.
2. Check `VALKEY_URL` env var is correct.
3. Check network policy allows BFF → Valkey.
