# Architecture

This document describes the internal structure, dependency rules, and runtime data flows of `bff-signup`.

`bff-signup` is the platform's **public, unauthenticated trial-signup intake** — the only internet-facing service that accepts requests without a JWT. It enforces abuse controls (CAPTCHA, rate limits, CSRF) and runs the provisioning handoff: eligibility check → RP-1 (Keycloak user) → I-1 (tenant row). It owns no business data and no database; state is ephemeral Valkey only.

---

## Layer model

Clean Architecture with hexagonal adapter layout. Inner layers have **zero knowledge** of outer layers; dependencies always point inward. Enforced by `go-arch-lint` (`.go-arch-lint.yml`).

```mermaid
graph TD
    subgraph cmd["Composition Root — cmd/server/"]
        main["main.go\nconfig validation → Valkey ping → stores → clients → services → router → HTTP server\ngraceful shutdown: HTTP drain → Valkey close"]
        wiring["wiring.go\nbuildValkeyClient · buildValkeyStores\nbuildRealmProvisioner · buildOrgMembership\nbuildTurnstile · buildPasswordHasher · buildCatalog"]
    end

    subgraph adapters_in["Inbound Adapters — internal/adapter/inbound/http/"]
        router["router.go\nSB-1 GET /session · SB-2 POST /trial\nSB-3 GET /{id}/status · SB-4 POST /{id}/slug\nGET /healthz · GET /readyz"]
        handlers["handlers.go\nSession · SubmitTrial · GetStatus · ChangeSlug"]
        middleware["middleware/\nsession.go — cookie extract + Set-Cookie (SameSite=Strict)\ncsrf.go — X-CSRF-Token extract + Origin validation\nclientip.go — x-envoy-external-address (never X-Forwarded-For)"]
        dto["dto.go — request/response DTOs\nerrors.go — domain error → HTTP status mapping"]
    end

    subgraph adapters_out["Outbound Adapters — internal/adapter/outbound/"]
        valkey_s["valkey/\nsession_store.go — sbff:sess: (30 min sliding)\nhandoff_store.go — sbff:handoff: + distributed lock (SET NX PX 15s Lua release)\nrate_limiter.go — sbff:rl:ip/email/sess/poll counters"]
        rp["realmprovisioner/\nRP-1 POST /internal/tenants/provision-trial\nIdempotency-Key: signup:tenantID:rp1\nRFC 9457 errorCode branching (never detail)"]
        om["orgmembership/\nI-1 POST /internal/tenants (idempotent on tenant_id)\neligibility POST /internal/trial-eligibility\nRFC 9457 errorCode branching"]
        turnstile["turnstile/\nCloudflare siteverify\nhostname verification after success"]
        catalog["catalog/\n5-min in-process cache\nstatic plansFallback on failure"]
        hasher["hasher/\nPBKDF2-SHA512 · 210,000 iterations · 16-byte salt\noutput: HashedPasswordSeed JSON → RP-1\nplain password zeroed after use (SB-INV-2)"]
    end

    subgraph core["Core — internal/core/"]
        service["service/\nsession_service.go — SB-1: create session, CSRF validation\nsignup_service.go — SB-2: CAPTCHA → rate-limit → validate → hash → orchestrate\nhandoff_orchestrator.go — eligibility → RP-1 → I-1 inline (retry) → 202 on exhaust\nstatus_service.go — SB-3: resume I-1 · SB-4: change slug"]
        port["port/\nSessionStore · HandoffStore · RateLimiter\nRealmProvisioner · OrgMembership\nCaptchaVerifier · Catalog · PasswordHasher\nClock · IDGenerator · BillingClient (stub §19)"]
        domain["domain/\nerrors.go — sentinel errors + DomainError{Cause,Message,Details}\nhandoff.go — Handoff record + HandoffState (7 states)\nplan.go — Plan type (starter|pro|enterprise only)\nsignup.go — field validators (company, name, email, password)\nslug.go — derive · validate · suggest"]
    end

    subgraph pkg_grp["Shared — pkg/"]
        signupctx["signupctx/\nSignupContext{Session, ClientIP}\nWithContext · FromContext · MustFromContext"]
    end

    subgraph tests["Tests — test/"]
        unit["unit/\ndomain/ — slug · password · handoff state machine\nservice/ — signup flow · CAPTCHA order · plan invariants"]
        integration["integration/ (build tag: integration)\nReal Valkey via docker-compose / CI service container:\nsession CRUD · handoff state update · lock acquire/release · rate counters"]
        fixtures["fixtures/\nFakeSessionStore · FakeHandoffStore · FakeRateLimiter\nFakeCaptchaVerifier · FakeRealmProvisioner · FakeOrgMembership\nFakeClock · FakeIDGenerator · FakePasswordHasher"]
    end

    main    --> wiring
    main    --> router
    router  --> handlers
    router  --> middleware
    handlers --> service
    service --> port
    service --> domain
    valkey_s -.->|implements| port
    rp       -.->|implements| port
    om       -.->|implements| port
    turnstile -.->|implements| port
    catalog  -.->|implements| port
    hasher   -.->|implements| port
    middleware --> signupctx
```

---

## Dependency rules

```
domain   ← imports nothing internal
port     ← domain only
service  ← domain + port  (never adapters)
inbound  ← domain + port + service + signupctx
outbound ← domain + port
cmd      ← everything (only cmd wires adapters to ports)
```

No package imports a Keycloak client library (**SB-INV-6**). Enforced at CI by `go-arch-lint`.

---

## Request flows

### SB-2 happy path — POST /trial

```mermaid
sequenceDiagram
    actor B as Browser
    participant E as Envoy (envoy-gateway-system)
    participant S as bff-signup
    participant V as Valkey
    participant T as Turnstile
    participant OM as Org & Membership
    participant RP as Realm Provisioner

    B->>E: POST /api/v1/signup/trial (cookie + X-CSRF-Token + form)
    E->>S: x-envoy-external-address = client IP
    Note over S: session + CSRF check
    S->>T: siteverify(token, remoteIP) ← CAPTCHA first (SB-INV-5)
    T-->>S: success
    S->>V: INCR rl:ip, INCR rl:email  ← rate limits after CAPTCHA (G-2)
    S->>V: SET NX lock:signup_id PX 15000
    S->>V: SET handoff {state=allocated, tenant_id=UUIDv7}
    S->>OM: POST /internal/trial-eligibility {email_hash}
    OM-->>S: 200 eligible
    Note over S: PBKDF2-SHA512 → hashed_password_seed (memory only, SB-INV-2)
    S->>RP: POST /internal/tenants/provision-trial + Idempotency-Key
    RP-->>S: 201 {keycloak_user_id}
    S->>V: handoff.state = identity_created
    S->>OM: POST /internal/tenants {tenant_id, slug, plan, owner_user_id}
    OM-->>S: 201
    S->>V: handoff.state = completed (TTL 10 min)
    S-->>B: 201 {status:"completed", next:"check_email"}
```

### I-1 failure — 202 + resume via SB-3

When I-1 fails after RP-1 succeeds: inline retries (×2, 100ms/250ms) → `tenant_pending` → `202`. Browser polls SB-3; StatusService acquires lock and retries I-1 once per poll. After 15 min → `failed`. O&M backstop (`TenantIdentityProvisioned` consumer) recovers abandoned signups independently.

---

## Handoff state machine

```mermaid
stateDiagram-v2
    [*] --> allocated : SB-2 passes checks
    allocated --> rejected : ineligible / email unavailable
    allocated --> identity_created : RP-1 201
    identity_created --> completed : I-1 201/200
    identity_created --> tenant_pending : I-1 5xx after retries
    identity_created --> slug_conflict : I-1 409 SLUG_ALREADY_TAKEN
    tenant_pending --> completed : SB-3 resume
    tenant_pending --> failed : 15 min exhausted
    slug_conflict --> identity_created : SB-4 new slug
    completed --> [*] : TTL 10 min
```

---

## Security architecture

| Control | Mechanism | Layer |
|---|---|---|
| Bot prevention | Cloudflare Turnstile (invisible, every SB-2) | Outbound adapter |
| CSRF | Synchronizer token (SHA-256 in session, `X-CSRF-Token` header, constant-time compare) | Middleware |
| Origin check | `Origin` header must equal `signup.bcbpsolutions.com` on every POST | Middleware |
| Rate limiting | Per-IP: 3/hour; per-email: 5/24h; per-session: 20 sessions/IP/hour; polls: 60/min | Valkey counters |
| Client IP | `x-envoy-external-address` only — never `X-Forwarded-For` | Middleware |
| Password | PBKDF2-SHA512, 210,000 iterations, 16-byte salt; plain text never leaves memory | Hasher adapter |
| Email privacy | HMAC-SHA256(email, SBFF_HMAC_KEY) in Valkey — raw email never stored | Service layer |
| Mesh identity | SPIFFE `spiffe://<trust>/ns/bff/sa/bff-signup`; Istio mTLS to RP-1 and I-1 | Istio sidecar |
| Session isolation | SB-3/SB-4 reject `signup_id` not owned by caller's session | Service layer |

---

## Valkey key catalogue (LLD §4.1)

| Key | TTL | Purpose |
|---|---|---|
| `sbff:sess:<sid>` | 30 min sliding | Session: csrf_hash, signup_id |
| `sbff:handoff:<signup_id>` | 30 min (10 min after completed) | Provisioning state |
| `sbff:lock:<signup_id>` | 15 s | Distributed lock (SET NX PX) |
| `sbff:rl:ip:<ip>` | 1 h | Per-IP signup counter |
| `sbff:rl:email:<hmac>` | 24 h | Per-email attempt counter |
| `sbff:rl:sess:<ip>` | 1 h | Per-IP session counter |
| `sbff:rl:poll:<sid>` | 1 min | Per-session poll counter |

Valkey failure → `503 dependency_unavailable` (fail-closed). No snapshots or backups — losing Valkey loses only in-flight signups and counters; the O&M backstop recovers any `identity_created` signups.

---

## Deployment topology

- **Namespace:** `bff` | **ServiceAccount:** `bff-signup`
- **Replicas:** 2 min, 5 max | **HPA:** CPU 70% (PBKDF2 is CPU-bound)
- **Resources:** `250m/1 CPU`, `128Mi/256Mi` memory
- **Ingress:** only from `envoy-gateway-system` (API :8080) and `monitoring` (:9090)
- **Egress:** DNS, Valkey :6379, IAM services :8080, Turnstile :443, OTel :4317
- **Secrets:** External Secrets Operator → OpenBao (`secret/data/bff/bff-signup/*`)
