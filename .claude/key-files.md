# Key Files

Per-file notes on non-obvious behaviour, wiring decisions, and invariants.

- **`cmd/server/main.go`** — composition root. DI order: config validation → Valkey ping → session/handoff/rate-limiter stores → RP-1/O&M/Turnstile/Catalog/hasher clients → HandoffOrchestrator → 3 services → Handlers → Router → HTTP server → graceful shutdown (HTTP drain 30 s → Valkey close). Config validation panics on startup for missing required env vars (`VALKEY_URL`, `SBFF_HMAC_KEY`, `SBFF_TURNSTILE_SECRET`, `REALM_PROVISIONER_BASE_URL`, `ORG_MEMBERSHIP_BASE_URL`) and rejects `PBKDF2_ITERATIONS < 210000`. The `staticCatalog` fallback is used when `CATALOG_BASE_URL` is empty. `defaultIDGen` uses oklog/ulid for `signup_id` (ULID) and google/uuid v7 for `tenant_id` (UUIDv7).

- **`internal/config/config.go`** — all configuration from env vars, with `Validate()` called before any connection. `PasswordPolicy()` and `SlugRules()` return the fixed domain structs used in SB-1 and SB-2. The password policy here is the **only** enforcement at signup — Keycloak cannot re-check a pre-hashed credential (LLD §10.6, C-4). If Keycloak's trial realm policy ever changes, this config must change in the same release.

- **`internal/core/domain/errors.go`** — sentinel errors + `DomainError{Cause, Message, Details}`. HTTP mapping lives in `adapter/inbound/http/errors.go`. Always use `errors.As(err, &de)` to unwrap; never string-match on `.Error()`.

- **`internal/core/domain/handoff.go`** — `Handoff` record (Valkey §4.2) and `HandoffState` type with the 7 states: `allocated → identity_created → completed/tenant_pending/slug_conflict`; `tenant_pending → completed/failed`; `slug_conflict → identity_created`. `IsTerminal()`, `CanResume()`, `CanChangeSlug()` are the gate checks used by services.

- **`internal/core/domain/slug.go`** — `DeriveSlug` (ASCII-fold → lowercase → non-alphanumeric runs to `-` → trim → truncate to 30), `ValidateSlug` (pattern `^[a-z0-9](?:[a-z0-9-]{1,28}[a-z0-9])$`, reserved word check), `SuggestSlugs` (up to 3 alternatives for slug conflict). The BFF's slug rule is a strict subset of O&M's rule so anything BFF accepts, O&M also accepts.

- **`internal/core/domain/signup.go`** — field validators (`ValidateCompanyName`, `ValidateAdminName`, `ValidateAdminEmail`, `ValidatePassword`). `ValidatePassword` is the authoritative policy check — it is the ONLY check because the password is hashed before reaching Keycloak.

- **`internal/core/port/`** — all interfaces. Key ones: `HandoffStore` (Create/Get/Update/Delete + AcquireLock/ReleaseLock), `SessionStore` (Create/Get/Refresh/Delete), `RateLimiter` (IncrIP/IncrEmail/IncrSession/IncrPoll), `RealmProvisioner` (ProvisionTrial), `OrgMembership` (ProvisionTenant + CheckEligibility), `PasswordHasher` (Hash), `Catalog` (Plans). `IDGenerator` has `NewULID()` and `NewUUIDv7()` — both are on a single interface wired in `cmd/server/main.go`.

- **`internal/core/service/handoff_orchestrator.go`** — the core provisioning sequence. `Run()` = eligibility check → RP-1 → inline I-1 with retries. `runI1` retries I-1 up to `maxI1InlineRetries=2` with 100ms/250ms backoff; on exhaustion sets `tenant_pending` and returns (caller sends 202). `ResumeI1()` is called from SB-3 during polling — checks the 15-minute `resumeWindowSeconds` window and calls `callI1Once`. `ChangeSlug()` is called from SB-4 — increments `slug_attempts` (max 5), updates the handoff slug, calls `callI1Once`. All state transitions are written to Valkey via `handoffs.Update(ctx, h)` before returning.

- **`internal/core/service/signup_service.go`** — SB-2 entry point. Sequence: CAPTCHA first (SB-INV-5) → rate limits (after CAPTCHA, G-2) → validation → resubmit/resume check → hash password → allocate handoff → acquire distributed lock → persist handoff → orchestrate. `emailHMAC` uses HMAC-SHA256 with `SBFF_HMAC_KEY` so Valkey never holds a readable email. `handleResubmit` implements the G-1 resubmit matrix from LLD §5.4.

- **`internal/core/service/session_service.go`** — SB-1 (create session) and CSRF/session validation used as middleware. `ValidateCSRF` checks the session exists and the CSRF token SHA-256 matches. `hashCSRF` is SHA-256 of the token string.

- **`internal/core/service/status_service.go`** — SB-3 (poll status) and SB-4 (change slug). SB-3 acquires the distributed lock before attempting I-1 resume so concurrent polls don't race. Both SB-3 and SB-4 check that `sessionSignupID == pathSignupID` before any Valkey access — if they don't match, return `404 signup_not_found` with no hint (LLD §5.5).

- **`internal/adapter/inbound/http/router.go`** — single source of truth for routes. `requireValidatedSession` middleware does session + CSRF validation for POSTs, session-only for GETs. `noStoreHeaders()` sets `Cache-Control: no-store` and `X-Content-Type-Options: nosniff` on every response. POST routes require `ValidateOrigin` before session validation.

- **`internal/adapter/inbound/http/handlers.go`** — all 4 endpoint handlers. `acceptLocale` derives `default_locale` from `Accept-Language` (allow-listed). Slug taken (409) has a custom response shape with `suggestions[]`; all other errors go through `handleError`.

- **`internal/adapter/inbound/http/middleware/session.go`** — `SetSessionCookie` manually appends `; SameSite=Strict` because gin defaults to `SameSite=Lax`. Cookie path is `/api/v1/signup`.

- **`internal/adapter/inbound/http/middleware/clientip.go`** — reads `x-envoy-external-address` set by Envoy (never parses `X-Forwarded-For` directly, LLD §10.2 N-8). Falls back to `c.ClientIP()` in local dev.

- **`internal/adapter/outbound/valkey/handoff_store.go`** — `AcquireLock` uses `SET NX PX 15000`. `ReleaseLock` uses a Lua script to atomically delete only if the token matches (prevents a slow request from releasing another request's lock). `Update` uses `completedTTL=10min` when state is `completed`, otherwise `handoffTTL=30min`.

- **`internal/adapter/outbound/realmprovisioner/client.go`** — branches on RFC 9457 `errorCode` field (never `detail`). `EMAIL_ALREADY_REGISTERED` → `ErrEmailUnavailable`. `OPERATION_IN_PROGRESS` → `ErrInvalidState` (retry with same key). `INVALID_REQUEST` → `ErrInternal` (BFF bug, page).

- **`internal/adapter/outbound/orgmembership/client.go`** — `CheckEligibility` treats HTTP 200 as eligible, `409 TRIAL_ALREADY_USED` as `ErrTrialNotEligible`. `ProvisionTenant` treats HTTP 200 and 201 both as success (200 = idempotent replay). `409 SLUG_ALREADY_TAKEN` → `ErrSlugTaken`. No `Idempotency-Key` header — O&M I-1 is idempotent by state on `tenant_id` (LLD §9.2).

- **`internal/adapter/outbound/hasher/pbkdf2.go`** — PBKDF2-SHA512, 210,000 iterations, 16-byte random salt. Output is `HashedPasswordSeed` JSON — RP-1 maps this to Keycloak `secretData`/`credentialData`. Zeroes the derived key after encoding. Iteration count validated `>= 210000` in `config.Validate()`.

- **`internal/adapter/outbound/catalog/client.go`** — in-process 5-minute cache with mutex. On Catalog Service failure, serves the last good copy or the static `plansFallback` from config. The fallback codes must always be exactly `starter`/`pro`/`enterprise` (never `"trial"`).

- **`deploy/helm/`** — Helm chart with `values-{dev,staging,prod}.yaml`. NetworkPolicy ingress: only `envoy-gateway-system` (API port 8080) and `monitoring` (metrics port 9090). HPA is CPU-only (PBKDF2 is CPU-bound; RPS-based scaling needs a custom-metrics adapter). PDB `minAvailable: 1`. ServiceEntry for Turnstile is off by default — enable in prod if mesh uses `REGISTRY_ONLY`.

- **`internal/core/port/billing.go`** — real `BillingClient` interface: `AuthorizeAndCharge(ChargeRequest) → *ChargeResponse`. `BillingStub` returns `ErrNotImplemented` and is the Phase 1 wiring. When building the real HTTP adapter, implement the interface and swap `buildBilling()` in `wiring.go`. `ChargeRequest.IdempotencyKey` format: `"charge:<tenant_id>"`.

- **`internal/core/port/realm_provisioner.go`** — now owns both RP-1 (`ProvisionTrial`) and RP-2 (`ProvisionDirect`). `ProvisionDirectRequest` mirrors `ProvisionTrialRequest` plus `BillingSubscriptionID`; confirm exact RP-2 field set against RP LLD §5.3 before activating Phase 1.5.

- **`internal/core/service/paid_signup_service.go`** — SB-5 entry point (Phase 1.5, LLD §19). Same CAPTCHA-first, rate-limit, validate sequence as `signup_service.go`, then: Billing charge → state=`payment_confirmed` → PUB-1 slug pre-check (fail-open) → RP-2 → state=`identity_created` → reuse `HandoffOrchestrator.runI1` for I-1. `emailHMAC` is now a package-level function shared by both signup services. Only wired when `PAID_SIGNUP_ENABLED=true`.

- **`internal/adapter/inbound/http/router.go`** — `PaidSignupEnabled` field in `RouterConfig` controls whether `POST /paid` is live or returns `404`. The 404 path is registered unconditionally so the route is never unmatched.
