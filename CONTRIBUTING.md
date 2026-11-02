# Contributing

This is the public-facing trial-signup BFF for the XpertPMS platform. This guide covers development setup, how to extend the service, test requirements, and the PR process.

## Prerequisites

- Go 1.23+ (matches `go.mod`)
- Docker (required for Valkey integration tests via `testcontainers-go` or `docker-compose`)
- `golangci-lint` and `go-arch-lint` — install via `go install` (no separate version manager needed)
- `GOPRIVATE=github.com/BCBP-SOLUTIONS-FZC-LLC/*` (for private platform libraries)

## Development setup

```bash
git clone https://github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup
cd bff-signup
make setup          # copies .env-example → .env + installs .githooks/pre-commit
make tidy           # go mod tidy
make docker-up      # start local Valkey on :6379
make lint           # verify linter passes
make test-unit      # unit tests (no Docker needed)
make test-integration  # integration tests (requires Docker)
make run            # start server on :8080
```

`make setup` installs `.githooks/pre-commit`, which runs `tidy`, `fmt-check`, `vet`, `lint`, and `arch-lint` before every commit.

## Project layout

```
cmd/server/         ← Composition root (main.go) + client factories (wiring.go)
internal/
  config/           ← Fail-fast env config + PasswordPolicy, SlugRules
  core/domain/      ← Errors, Handoff state machine, Plan, slug rules, password validation
  core/port/        ← All port interfaces (no external deps)
  core/service/     ← Session, Signup, HandoffOrchestrator, Status
  adapter/inbound/http/    ← Gin router, handlers (SB-1…4), DTOs, middleware
  adapter/outbound/
    valkey/         ← Session store, Handoff store, Rate limiter
    realmprovisioner/ ← RP-1 HTTP client
    orgmembership/  ← I-1 + trial-eligibility HTTP client
    turnstile/      ← Cloudflare CAPTCHA verifier
    catalog/        ← Plan list (5-min cache + static fallback)
    hasher/         ← PBKDF2-SHA512 password hasher
pkg/signupctx/      ← SignupContext{Session, ClientIP} threaded through Go context
test/
  fixtures/         ← Hand-written fakes for all port interfaces
  unit/             ← Domain logic, service layer (no external deps)
  integration/      ← Real Valkey tests (build tag: integration)
deploy/helm/        ← Helm chart (dev/staging/prod values)
docs/lld/           ← LLD and open questions docs
```

**Dependency rule:** `domain ← port ← service ← adapter ← cmd`. Nothing in `core/` may import `adapter/`. Enforced by `go-arch-lint` in CI.

## Security-critical areas

Changes in these areas require **extra care and co-review** (see CODEOWNERS):

| Area | Why critical |
|---|---|
| `internal/adapter/outbound/hasher/` | PBKDF2 parameters must stay in sync with Keycloak `trial` realm hash policy. A mismatch means users can sign up but never log in. |
| `internal/core/service/signup_service.go` | CAPTCHA **must** run before rate-limit counters (SB-INV-5). Password **must** never be logged, stored, or returned. |
| `internal/adapter/inbound/http/middleware/csrf.go` | Origin check and CSRF token validation must apply to all POST routes. |
| `internal/config/config.go` | `SBFF_HMAC_KEY` is the secret used to HMAC email addresses; `PBKDF2_ITERATIONS` must be validated ≥ 210000. |
| Plan values anywhere | `plan` sent to RP-1 or I-1 must always be `starter`/`pro`/`enterprise`, never `"trial"` (SB-INV-8). |

## Extending the service

### Adding a new endpoint

1. Add the handler to `internal/adapter/inbound/http/handlers.go`.
2. Add request/response DTOs to `dto.go`.
3. Register the route in `router.go`.
4. Add service logic to the appropriate service in `internal/core/service/`.
5. If a new port interface is needed, add to `internal/core/port/`.
6. If a new Valkey key is introduced, document it in `ARCHITECTURE.md` and `docs/lld/signup-bff-lld.md §4.1`.
7. Add unit tests in `test/unit/`, integration tests if Valkey behaviour is involved.

### Adding a new outbound HTTP client

1. Create `internal/adapter/outbound/<name>/client.go`.
2. Add `var _ port.X = (*Client)(nil)` compile-time interface check.
3. Add the port interface to `internal/core/port/`.
4. Wire in `cmd/server/wiring.go` (factory function) and `cmd/server/main.go`.
5. Add egress NetworkPolicy rule in `deploy/helm/templates/networkpolicy.yaml`.

### Adding a new Valkey key type

1. Add the key builder function to `internal/adapter/outbound/valkey/store.go`.
2. Implement the store method in the appropriate `*_store.go` file.
3. Add the key to the Valkey key catalogue table in `ARCHITECTURE.md` and `docs/lld/signup-bff-lld.md §4.1`.
4. Add integration tests in `test/integration/valkey_test.go`.

### Adding a new environment variable

1. Add it to `.env-example` with an inline comment.
2. Add it to `internal/config/config.go` with a `mustEnv()` call if required, or `envOr()` if optional.
3. Add a validation check in `config.Validate()` if the value has constraints.
4. Update `deploy/helm/values.yaml` `env:` section.
5. Update the `.claude/CLAUDE.md` environment variables table.

### Changing the password policy

The BFF's policy is the **only** enforcement at signup (Keycloak cannot check a pre-hashed credential). Any change must be accompanied by a matching Keycloak `trial` realm policy update in the same release. Co-review from `@iam-team` is required (see CODEOWNERS).

## Testing requirements

| Layer | Location | Docker | Notes |
|---|---|---|---|
| Unit | `test/unit/` | No | Hand-written fakes; no Valkey, no HTTP |
| Integration | `test/integration/` | Yes | Real Valkey (`//go:build integration`) |
| Contract | `test/unit/service/contract_test.go` | No | RP-1, I-1, eligibility request/response shapes (run by `make test-unit`) |
| E2E | `test/e2e/` | `make test-e2e` | Needs the service, Valkey and mocks running locally (build tag `e2e`); see `test/e2e/README.md`. Covers the gap fixes; more cases to follow |
| Smoke | `.github/scripts/smoke-tests.sh` | — | Image size + startup gate |

Run unit tests without Docker:

```bash
make test-unit
```

Run all tests including integration (requires `make docker-up` first):

```bash
make test
```

Run with race detector before submitting a PR (`make ci` does this automatically):

```bash
make race
```

### Coverage gate

CI enforces **≥ 80% statement coverage** on the merged `coverage.out` (unit + integration profiles merged by `scripts/merge_coverage.py`). This threshold is lower than DB services because the BFF has no database layer to test.

```bash
make cover-func   # per-function summary
make cover        # HTML report
```

### Critical test invariants

Every PR must keep these test cases passing:

| Invariant | Test file |
|---|---|
| CAPTCHA runs before rate-limit counters (SB-INV-5) | `test/unit/service/signup_service_test.go` |
| `plan:"trial"` is rejected (SB-INV-8) | `test/unit/service/signup_service_test.go` |
| RP-1 and I-1 receive identical plan/slug/name (SB-INV-8) | `test/unit/service/signup_service_test.go` |
| Eligibility check blocks RP-1 if not eligible (SB-INV-4) | `test/unit/service/signup_service_test.go` |
| Password validator is the only check at signup (SB-INV-7) | `test/unit/domain/password_test.go` |
| Distributed lock prevents double-submit | `test/integration/valkey_test.go` |

## Linting

```bash
make lint          # golangci-lint
make arch-lint     # go-arch-lint layering rules
make metrics-lint  # bff_signup_ prefix + _total/_seconds suffix check
```

All three run in CI. Add a package doc comment to every new package.

## Documentation update checklist

When your change touches a contract or data flow, update:

| What changed | Documents to update |
|---|---|
| New HTTP endpoint | `ARCHITECTURE.md` request flows + `docs/lld/signup-bff-lld.md §5` |
| New Valkey key type | `ARCHITECTURE.md` key catalogue + LLD §4.1 |
| New outbound contract | LLD §5.6, §17.2, §18 + contract tests |
| New environment variable | `.env-example` + `deploy/helm/values.yaml` + `.claude/CLAUDE.md` |
| Changed handoff state machine | `ARCHITECTURE.md` state diagram + LLD §9.1 |
| New rate limit | LLD §10.2 + `scripts/validate-metrics.sh` if a new metric is added |
| Any security-sensitive change | `SECURITY.md` if a new attack surface is introduced |
| `CHANGELOG.md` `[Unreleased]` | Always — describe what changed in one short paragraph |

## Branch naming

- `feat/<short-topic>` — new capability
- `fix/<short-topic>` — bug fix
- `security/<short-topic>` — security fix (co-review required)
- `docs/<short-topic>` — documentation only
- `chore/<short-topic>` — tooling, CI, dependencies, no behaviour change

Base new branches off the latest `main`.

## Pull request checklist

- [ ] `make ci` passes locally — runs `tidy` + `fmt-check` + `vet` + `lint` + `arch-lint` + `test-ci` (race) + `build`
- [ ] `make race` passes with zero data races
- [ ] Security-critical invariants listed above still pass
- [ ] New behaviour covered by tests in `test/unit/` or `test/integration/`
- [ ] `CHANGELOG.md` `[Unreleased]` section updated
- [ ] Documentation update checklist above reviewed
- [ ] No password, `hashed_password_seed`, CAPTCHA token, CSRF token, or email logged anywhere (redaction test still passes)

## Commit style

Short imperative subject line (≤ 72 chars). Reference the area:

```
fix(handoff): reset slug_attempts on SB-4 after slug_conflict state
feat(eligibility): add pre-check before RP-1 (BFF-2a Option A)
security(csrf): enforce SameSite=Strict on session cookie set
chore(deps): go mod tidy — upgrade go-redis v9.7.0 → v9.8.0
docs(lld): close N-12 — I-8 returns 404 → AuthZ → 403 hard fail
test(slug): add test for SuggestSlugs deduplication
```

## Deployment

Not yet deployed to any environment. See `docs/lld/signup-bff-action-tracker.md` for the SRE and TL actions required before go-live.

On push to `main`:
1. `validate-test` (race + ≥ 80% coverage) and `validate-quality` (fmt/vet/lint/vuln/arch-lint) run in parallel.
2. Docker image is built and cached.
3. Trivy CVE scan + smoke tests (image size ≤ 50 MB + startup gate) run gated on validate-test + build.
4. On success the image is pushed to GHCR tagged with the Git SHA.
5. Helm chart promotion to staging/prod is manual until go-live prerequisites are met.
