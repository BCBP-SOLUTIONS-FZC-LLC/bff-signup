# CLAUDE.md

This file provides guidance to Claude Code when working with **bff-signup**, the public trial-signup BFF service.

## What This Repo Is

`bff-signup` is the platform's **public, unauthenticated tenant-onboarding intake** (Go module: `github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup`, Go 1.23). It serves the signup form's JSON API, enforces abuse controls (rate limit, CAPTCHA, CSRF), and runs the provisioning handoff:

1. Trial eligibility check → O&M `POST /internal/trial-eligibility` (CR-4)
2. RP-1 → Realm Provisioner `POST /internal/tenants/provision-trial` (creates Keycloak user)
3. I-1 → Org & Membership `POST /internal/tenants` (creates tenant row)

**Owns:** ephemeral Valkey state only (sessions, handoffs, rate-limit counters). No database. No event bus participation.

**Does NOT own:** Keycloak admin, JWT issuance, tenant data, memberships, billing.

**Namespace / identity:** `bff` namespace, service account `bff-signup`, SPIFFE `spiffe://<trust-domain>/ns/bff/sa/bff-signup` (BFF-1).

**Phase:** Phase 1 — trial signup only. Direct-paid (Phase 1.5) is stubbed in `port/billing.go`.

**LLD:** `docs/lld/signup-bff-lld.md` v0.6 — canonical reference for all design decisions.

---

## Common Commands

```bash
make setup           # Copy .env-example → .env (run once)
make tidy            # go mod tidy
make fmt             # gofmt -w
make fmt-check       # Verify formatting (CI)
make vet             # go vet ./...
make lint            # golangci-lint
make arch-lint       # go-arch-lint (layering rules)
make mod-verify      # go mod verify
make vuln-check      # govulncheck

make test            # unit + integration tests
make test-ci         # with -race + coverage (CI)
make test-unit       # unit tests only (no Docker)
make test-integration # integration tests (requires Docker Valkey)
make test-contract   # contract tests (RP-1, I-1, eligibility)
make test-e2e        # end-to-end tests against staging
make test-load       # k6 load tests
make race            # all tests with -race

make build           # compile → bin/server
make run             # go run ./cmd/server (sources .env)
make cover           # coverage HTML report
make cover-func      # coverage by function

make docker-up       # start local Valkey (docker-compose)
make docker-down     # stop local Valkey
make docker-reset    # stop + wipe + restart

make helm-lint       # lint Helm chart
make helm-template   # render chart with dev values

make ci              # full local CI gate (tidy + fmt + vet + lint + test + build)
```

---

## Architecture Rules (enforced by go-arch-lint)

- `core/domain` imports nothing internal.
- `core/service` imports `domain` and `port` only.
- `adapter/*` imports `port` and `domain`; adapters never import each other.
- Only `cmd/server` wires adapters to ports.
- No package imports a Keycloak client library (enforces SB-INV-6).

---

## Critical Invariants (from LLD §2.3)

- **SB-INV-2:** The plain password never leaves process memory. Not logged, not in Valkey, not in traces. Only `hashed_password_seed` (PBKDF2-SHA512 JSON) is sent to RP-1.
- **SB-INV-3:** One `tenant_id` per signup attempt — allocated once, reused for every retry/resume/slug change.
- **SB-INV-4:** RP-1 before I-1, always. `owner_user_id` sent to I-1 is the `keycloak_user_id` RP-1 returned.
- **SB-INV-5:** CAPTCHA and rate limit run before any internal call or password hashing.
- **SB-INV-8:** RP-1 and I-1 carry identical `plan`, slug and name. `plan` is always `starter`/`pro`/`enterprise`, never `"trial"`.

---

## Key Open Questions (pending TL decision)

See `docs/lld/signup-bff-open-questions-tl.md` — 4 items in Section A need TL sign-off.
Remaining team actions tracked in `docs/lld/signup-bff-action-tracker.md`.
