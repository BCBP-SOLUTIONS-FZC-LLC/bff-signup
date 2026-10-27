# Metric inventory: bff-signup

Every series carries const labels `domain` (`bff`), `service` (`bff-signup`) and `environment`.
All metrics use the required `bff_signup_` prefix (LLD §11, enforced in CI).

## Tier 3: service-specific metrics

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `bff_signup_request_duration_seconds` | histogram | `route`, `code` | HTTP request latency by route and status code |
| `bff_signup_form_submitted_total` | counter | `mode` | Signup form submissions (`mode=trial\|paid`) |
| `bff_signup_provisioning_succeeded_total` | counter | `mode` | Signups that reached `completed` state |
| `bff_signup_provisioning_failed_total` | counter | `mode`, `stage`, `reason` | Provisioning failures by stage (`realm_provisioner`, `org_membership`) and reason |
| `bff_signup_captcha_challenged_total` | counter | `result` | CAPTCHA verifications (`result=pass\|fail\|error`) |
| `bff_signup_rate_limited_total` | counter | `scope` | Rate-limited requests (`scope=ip\|email\|session\|poll`) |
| `bff_signup_session_created_total` | counter | — | SB-1 sessions created |
| `bff_signup_slug_conflict_total` | counter | — | Slug conflicts returned to browser (SB-2 / SB-4) |
