# Metric inventory: bff-signup

Conforms to the **Enterprise Platform Observability Standard** three-tier taxonomy.
Every series carries const labels `domain` (`bff`), `service` (`bff-signup`) and `environment` — injected
centrally via `gincommon` ConstLabels (rule 8; never left to call sites).
**Authoritative source of truth:** `internal/adapter/outbound/metrics/business.go`.
CI enforcement: `make check-metric-naming` (`check-metric-naming.sh`) + `make metrics-lint` (`metrics-registry-lint.sh`).

---

## Tier 1: platform metrics — shared across ALL domains

Emitted via `gincommon.NewSharedHistogramVec` / `gincommon.NewSharedCounterVec`.
Names come from the Platform Observability Registry. Required labels: `domain`, `service`, `environment`.

| Metric | Type | Extra labels | Status | Meaning |
|---|---|---|---|---|
| `platform_dependency_request_seconds` | histogram | `dependency`, `operation`, `outcome` | **Proposed** — shadow-emitted; Tier 3 predecessor is authoritative until ratified | Wall time of one outbound HTTP call, shared semantics across IAM/Billing/Workflow etc. |

> **Proposed constraint:** `metrics-registry-lint.sh` enforces that no alert, recording rule, SLO, or HPA
> expression references `platform_dependency_request_seconds` until its `PlatformRegistry` entry flips to
> `StatusCanonical` in `registry.go`.

---

## Tier 2: domain metrics — shared across multiple BFF services

Pattern: `bff_*`. **Currently unused.** The BFF domain has a single service (`bff-signup`).
If a second BFF service emerges, common concepts (e.g. session lifecycle) may be promoted to `bff_*` at that time.
Any new `bff_*` metric requires domain-registry approval before adoption (standard rule 12).

---

## Tier 3: service-specific metrics

Pattern: `bff_signup_*`. Owned by the `bff-signup` team.
Emitted via `gincommon.NewCounterVec` / `gincommon.NewHistogramVec` which enforce the `bff_signup_` prefix.
High-cardinality labels (`user_id`, `email`, `tenant_id`, `request_id`, `session_id`, `ip`) are **prohibited**.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `bff_signup_form_started_total` | counter | `mode` | SB-1 sessions successfully created (`mode=trial\|paid`). Baseline for signup funnel. |
| `bff_signup_form_submitted_total` | counter | `mode` | SB-2 requests received before any processing (`mode=trial\|paid`). Paired with `bff_signup_provisioning_succeeded_total` for funnel drop-off. |
| `bff_signup_provisioning_succeeded_total` | counter | `mode`, `strategy` | Signups that reached `state=completed`. `strategy`: `inline` (I-1 within SB-2), `resume` (SB-3 poll), `slug_change` (SB-4). |
| `bff_signup_provisioning_failed_total` | counter | `mode`, `stage`, `reason` | Signups stuck in `state=failed` after the 15-min resume window. `stage`: `eligibility\|realm_provisioner\|org_membership`. `reason`: `unavailable\|contract_error\|exhausted`. |
| `bff_signup_captcha_challenged_total` | counter | `result` | Turnstile siteverify calls. `result`: `pass\|fail\|error`. |
| `bff_signup_rate_limited_total` | counter | `scope` | Requests rejected by abuse controls. `scope`: `ip\|email\|session\|poll`. |
| `bff_signup_handoff_resumed_total` | counter | `trigger` | I-1 retry attempts on an existing handoff. `trigger`: `poll` (SB-3), `resubmit` (SB-2 same-session retry), `slug_change` (SB-4). |
| `bff_signup_dependency_call_duration_seconds` | histogram | `dependency`, `outcome` | Wall time of every outbound HTTP call. Emitted by all 4 adapters (orgmembership, realmprovisioner, turnstile, catalog). `outcome`: `success\|error`. _Authoritative alerting/SLO source until `platform_dependency_request_seconds` is ratified._ |
| `bff_signup_password_hash_duration_seconds` | histogram | — | PBKDF2-SHA512 hashing time per SB-2 request (~100–200 ms CPU). Drives HPA sizing and SLO-2 budget. |

### Dependency instrumentation

`bff_signup_dependency_call_duration_seconds` is populated by every outbound HTTP adapter immediately after
`httpClient.Do()`, before error handling — capturing latency for both successful and failed calls:

| Adapter | `dependency` label | `operation` (Tier 1 shadow) |
|---|---|---|
| `orgmembership/client.go` | `org_membership` | `provision_tenant`, `trial_eligibility`, `slug_resolve` |
| `realmprovisioner/client.go` | `realm_provisioner` | `provision_trial`, `provision_direct` |
| `turnstile/client.go` | `turnstile` | `siteverify` |
| `catalog/client.go` | `catalog` | `plans` |

CI enforces this: `check-metric-naming.sh` fails if any adapter file omits `ObserveDependencyCall`.

---

## Backward compatibility plan for `platform_dependency_request_seconds`

Per standard §Backward Compatibility:
1. **Now:** Both metrics emitted in parallel (`bff_signup_dependency_call_duration_seconds` authoritative; `platform_*` shadow).
2. **On ratification:** Flip `PlatformRegistry.Status` to `StatusCanonical` in `registry.go`.
3. **Migrate:** Update `app-alerts.yml`, `slo-rules.yml`, dashboards to reference `platform_dependency_request_seconds`.
4. **Deprecate:** Add deprecation notice to `bff_signup_dependency_call_duration_seconds`.
5. **Remove:** After approved sunset period, remove Tier 3 predecessor.
