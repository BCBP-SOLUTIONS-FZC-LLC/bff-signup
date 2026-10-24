# Changelog

All notable changes to `bff-signup` are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

---

## [Unreleased]

### Added — Phase 1.5 Direct-Paid Signup reservation (2026-10-24)

**Phase 1.5 stub fully wired — activates with `PAID_SIGNUP_ENABLED=true`:**

- `internal/core/port/billing.go` — replaced `NotImplemented` stub with real `BillingClient` interface: `AuthorizeAndCharge(ChargeRequest) → *ChargeResponse{BillingSubscriptionID}` + `BillingStub` (returns `ErrNotImplemented`). `ChargeRequest` carries `{TenantID, PaymentToken, Plan, Seats, IdempotencyKey}`.
- `internal/core/port/realm_provisioner.go` — extended `RealmProvisioner` with `ProvisionDirect` (RP-2). Added `ProvisionDirectRequest` (mirrors RP-1 + `BillingSubscriptionID`) and `ProvisionDirectResponse`.
- `internal/adapter/outbound/realmprovisioner/client.go` — implemented `ProvisionDirect` HTTP call to `POST /api/v1/internal/tenants/provision-direct`, Idempotency-Key `signup:<tenant_id>:rp2`.
- `internal/core/domain/handoff.go` — added `StatePaymentPending`, `StatePaymentConfirmed`, `SignupType` (`"trial"` | `"paid"`), `Type` field, and `BillingSubscriptionID` field to `Handoff`.
- `internal/core/service/paid_signup_service.go` — new `PaidSignupService` (SB-5): CAPTCHA → rate limit → validate → Billing charge → RP-2 → I-1 inline (reuses `HandoffOrchestrator.runI1`).
- `internal/core/service/signup_service.go` — extracted `emailHMAC(email, key)` to a package-level function shared by both signup services.
- `internal/adapter/inbound/http/handlers.go` — added `SubmitPaid` handler; `Handlers` gains `paidSignup *service.PaidSignupService`.
- `internal/adapter/inbound/http/router.go` — `POST /api/v1/signup/paid` wired; returns `404` unless `PaidSignupEnabled=true`. Added `PaidSignupEnabled` to `RouterConfig`.
- `internal/adapter/inbound/http/dto.go` — added `PaidSignupRequest` (+ `seats int`, `payment_token string`) and `PaidSignupResponse`.
- `internal/config/config.go` — added `BillingURL`, `BillingTimeoutMs` (default 5000 ms), `PaidSignupEnabled` (env `PAID_SIGNUP_ENABLED`). Added `BillingTimeout()` and `envBool()`.
- `cmd/server/main.go` — wires `PaidSignupService`, passes `PaidSignupEnabled` to router.
- `cmd/server/wiring.go` — added `buildBilling()` returning `port.BillingStub{}`.
- `test/fixtures/fakes.go` — added `FakeBillingClient`, `FakeRealmProvisioner.ProvisionDirect` and `DirectCalls`.

**D-2 resolved (Commercial Domain HLD §8.3, CD-9 closed):** BFF (not Billing) calls RP-2 and I-1 after Billing confirms the charge. Flow: Billing → RP-2 → I-1.

---

### Added — Initial implementation (2026-10-22)

**Service foundation:**

- `go.mod` — module `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`, Go 1.23.
- `cmd/server/main.go` — composition root with fail-fast config validation, Valkey ping, DI, graceful shutdown (HTTP drain → Valkey close).
- `cmd/server/wiring.go` — factory functions for all upstream clients (Valkey stores, RP-1, O&M, Turnstile, PBKDF2 hasher, Catalog).
- `cmd/server/env_test.go` — startup tests: missing required env vars panic, `PBKDF2_ITERATIONS < 210000` rejected, `SBFF_HMAC_KEY < 32 bytes` rejected, plan fallback never contains `"trial"`.
- `internal/config/config.go` — fail-fast env config with `Validate()`. `PasswordPolicy()` and `SlugRules()` return domain types for SB-1 response.

**Domain layer (`internal/core/domain/`):**

- `errors.go` — sentinel errors (`ErrSessionExpired`, `ErrCSRFInvalid`, `ErrCaptchaFailed`, `ErrValidationFailed`, `ErrSignupNotFound`, `ErrEmailUnavailable`, `ErrTrialNotEligible`, `ErrSlugTaken`, `ErrInvalidState`, `ErrRateLimited`, `ErrDependencyUnavailable`, `ErrInternal`) + `DomainError{Cause, Message, Details}`.
- `handoff.go` — `Handoff` record (7-state machine: `allocated → identity_created → completed/tenant_pending/slug_conflict`). `IsTerminal()`, `CanResume()`, `CanChangeSlug()`.
- `plan.go` — `Plan` type (`starter`|`pro`|`enterprise`). `ValidPlan()` rejects any other value including `"trial"` (SB-INV-8).
- `signup.go` — field validators: `ValidateCompanyName`, `ValidateAdminName`, `ValidateAdminEmail`, `ValidatePassword` (authoritative policy check at signup — SB-INV-7), `NormaliseEmail`.
- `slug.go` — `DeriveSlug` (ASCII-fold → lowercase → hyphens), `ValidateSlug` (3–30 chars, pattern, reserved words), `SuggestSlugs` (up to 3 alternatives on conflict).

**Port interfaces (`internal/core/port/`):**

- `session_store.go`, `handoff_store.go`, `rate_limiter.go`, `captcha.go`, `catalog.go`, `realm_provisioner.go`, `org_membership.go`, `password_hasher.go`, `clock.go` (Clock + IDGenerator), `billing.go` (Phase 1.5 stub).

**Service layer (`internal/core/service/`):**

- `session_service.go` — SB-1: create session (256-bit random SID, CSRF token), CSRF validation, session retrieval.
- `signup_service.go` — SB-2: CAPTCHA first (SB-INV-5) → rate limits → validation → resubmit matrix (G-1) → PBKDF2 hash → distributed lock → handoff → orchestrate. HMAC-SHA256 email key (SB-INV-2).
- `handoff_orchestrator.go` — eligibility (CR-4) → RP-1 → inline I-1 (×2 retries, 100ms/250ms) → `tenant_pending` on exhaust. `ResumeI1` for SB-3 (15-min window). `ChangeSlug` for SB-4 (max 5 attempts).
- `status_service.go` — SB-3: poll with distributed lock + I-1 resume. SB-4: validate + `ChangeSlug`.

**Inbound HTTP adapter (`internal/adapter/inbound/http/`):**

- `router.go` — routes SB-1…4, `/healthz`, `/readyz`. `requireValidatedSession` middleware (session + CSRF for POST, session-only for GET). `noStoreHeaders()` on all responses.
- `handlers.go` — `Session`, `SubmitTrial`, `GetStatus`, `ChangeSlug`. `acceptLocale` derives `default_locale` from allow-listed `Accept-Language`.
- `dto.go`, `errors.go` — request/response DTOs, `domainStatus()` mapping.
- `middleware/session.go` — cookie extract + `SetSessionCookie` with `SameSite=Strict`.
- `middleware/csrf.go` — `X-CSRF-Token` extract + `ValidateOrigin`.
- `middleware/clientip.go` — reads `x-envoy-external-address` only (never `X-Forwarded-For`).

**Outbound adapters:**

- `valkey/session_store.go`, `handoff_store.go`, `rate_limiter.go`, `store.go` — Valkey state store. Distributed lock via `SET NX PX` + Lua release script. All key builders in `store.go`.
- `realmprovisioner/client.go` — RP-1 HTTP client. RFC 9457 `errorCode` branching: `EMAIL_ALREADY_REGISTERED`, `OPERATION_IN_PROGRESS`, `INVALID_REQUEST`.
- `orgmembership/client.go` — I-1 + trial-eligibility HTTP clients. RFC 9457 `errorCode` branching: `SLUG_ALREADY_TAKEN`, `TRIAL_ALREADY_USED`. No `Idempotency-Key` on I-1 (idempotent by `tenant_id` state).
- `turnstile/client.go` — Cloudflare siteverify + hostname check.
- `catalog/client.go` — 5-min in-process cache + mutex + static fallback.
- `hasher/pbkdf2.go` — PBKDF2-SHA512, 210,000 iterations, 16-byte random salt. Output is `HashedPasswordSeed` JSON; derived key zeroed after encoding.

**Shared package:**

- `pkg/signupctx/context.go` — `SignupContext{Session, ClientIP}`, `WithContext`, `FromContext`, `MustFromContext`.

**Infrastructure:**

- `deploy/helm/` — Helm chart with `values-{dev,staging,prod}.yaml`. CPU-only HPA (PBKDF2 is CPU-bound). NetworkPolicy ingress restricted to `envoy-gateway-system` and `monitoring`. PDB `minAvailable: 1`.
- `.github/workflows/ci.yml`, `validate-quality.yml`, `validate-test.yml` — parallel CI: quality gate (fmt/vet/lint/arch-lint/vuln-check/Dockerfile pin), test gate (unit + integration with real Valkey + coverage ≥ 80%), image build, Trivy scan, Helm lint.
- `.github/scripts/` — `arch-lint.sh`, `coverage-gate.sh` (80% threshold), `smoke-tests.sh` (50 MB image limit + startup gate).
- `.githooks/pre-commit` — tidy + fmt-check + vet + lint + arch-lint before every commit.
- `.go-arch-lint.yml` — layering rules.
- `.golangci.yml`, `.dockerignore`, `.trivyignore`, `.docker-digests`.
- `scripts/merge_coverage.py` — merge unit + integration coverage profiles.
- `scripts/validate-metrics.sh` — enforce `signup_bff_` prefix + `_total`/`_seconds` suffixes.
- `.claude/` — `CLAUDE.md`, `key-files.md`, `api-and-events.md`, `flows-and-concurrency.md`, `development-guide.md`, `operations.md`.

**Tests:**

- `test/fixtures/fakes.go` — hand-written fakes for all port interfaces.
- `test/unit/domain/` — `slug_test.go`, `handoff_test.go`, `password_test.go`.
- `test/unit/service/signup_service_test.go` — happy path, CAPTCHA order (SB-INV-5), plan invariant (SB-INV-8), slug conflict, eligibility blocks RP-1 (SB-INV-4).
- `test/integration/valkey_test.go` — session CRUD, handoff state update, lock contention, rate counter TTL.

**Documentation:**

- `docs/lld/signup-bff-lld.md` v0.6 — full LLD (21 sections).
- `docs/lld/signup-bff-open-questions-tl.md` — 4 TL decisions still needed (A-1…A-4).
- `docs/lld/signup-bff-action-tracker.md` — team assignments and closed items.
- `ARCHITECTURE.md`, `CHANGELOG.md`, `CONTRIBUTING.md`.

---

## [0.1.0] — not yet released

Initial pre-release. No environment has been deployed. Pending TL sign-off on LLD v0.6 and SRE provisioning of `signup.bcbpsolutions.com`.
