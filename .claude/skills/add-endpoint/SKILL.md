---
name: add-endpoint
description: Checklist-driven workflow for adding or changing an HTTP endpoint in bff-signup (domain → port → service → handler/DTO/router → swagger → metrics → tests → changelog). Use when adding a route under /api/v1/signup or materially changing a handler's contract.
argument-hint: "<METHOD> <path> — <purpose>"
---

# Add or change an endpoint in bff-signup

Target: `$ARGUMENTS`

Work through the steps in order and skip any that don't apply. Before you start, read `.claude/CLAUDE.md`, `CONTRIBUTING.md` ("Adding a new endpoint"), and the relevant section of `docs/lld/signup-bff-lld.md`. If the LLD doesn't describe this endpoint, stop and ask the user. Endpoints are LLD-driven.

## 1. Design check
- Assign the SB-n identifier and confirm the method, path, auth (cookie only, or cookie + `X-CSRF-Token`), and the request/response shapes.
- Decide which existing error codes it can return (see the README error table) and whether it needs a new `domain` sentinel error.
- Decide where it sits in the ordering invariants. Anything that triggers internal calls must run CAPTCHA, then rate limits, then validation first (SB-INV-5).

## 2. Core (inside → out)
1. **domain**: add types, validators and sentinel errors in `internal/core/domain/`. This layer has no internal imports.
2. **port**: if a new dependency is needed, add an interface in `internal/core/port/` and a hand-written fake in `test/fixtures/`.
3. **service**: add logic in `internal/core/service/`. Inject a `Clock` or `IDGenerator` rather than calling `time.Now()` or `ulid.Make()`. Read limits and TTLs from config.

## 3. Inbound adapter (`internal/adapter/inbound/http/`)
1. Add DTOs to `dto.go`, with `json` tags and `binding` tags where relevant.
2. Add the handler to `handlers.go`, with full **swag annotations** (`@Summary @Description @Tags @Accept @Produce @Param @Success @Failure @Router`) that match the existing handlers' style.
3. Add error mapping in `errors.go` for new domain errors, producing the `{error,status,request_id,details}` shape.
4. Register the route in `router.go`. Every state-changing route goes through `requireValidatedSession` (session + CSRF + Origin). Path params like `signup_id` must be validated as ULIDs.

## 4. Outbound (only if needed)
Follow `CONTRIBUTING.md` → "Adding a new outbound HTTP client":
- Add a `var _ port.X = (*Client)(nil)` check.
- Call `metrics.ObserveDependencyCall` right after `Do()`.
- Take the timeout from config.
- Add the wiring in `cmd/server/wiring.go`.
- Add a NetworkPolicy egress rule.

## 5. Config and Valkey (only if needed)
- **Env var:** add it to `.env-example`, `config.go` (with `Validate()`) and `deploy/helm/values.yaml`.
- **Valkey key:** add an `sbff:` key builder in `valkey/store.go`, add it to the key catalogue in `ARCHITECTURE.md` and LLD §4.1, and add an integration test.

## 6. Observability
- New metrics go only in `internal/adapter/outbound/metrics/`, named `bff_signup_*_total` or `*_seconds`, with no high-cardinality labels.
- Log through the gincommon `log.*` only. Never log the password, raw email, cookie or CSRF token.

## 7. Tests
- Unit tests in `test/unit/` covering domain validators, the service happy path and every error branch, and an HTTP test for status codes, CSRF/session rejection and the error shape.
- Integration tests in `test/integration/` (build tag `integration`) if Valkey behaviour changes.
- Coverage gate: `make test-ci` requires at least 95%.

## 8. Docs and generated artifacts
- Run `make swag`. Don't hand-edit `docs/swagger/`.
- Update the README API table (and the response-shape examples if needed) plus `.claude/api-and-events.md`.
- Add a `CHANGELOG.md` entry under `[Unreleased]`. CI's `changelog-check` enforces this.

## 9. Verify
Run these and report the results faithfully:
```bash
make fmt-check vet arch-lint metrics-lint log-sink-lint
make test-unit
make swag-check
```
Run `make lint` if `golangci-lint` is installed. Run `make test-integration` if Valkey is up (`make docker-up`).

Finally, delegate to the `security-reviewer` and `arch-guard` subagents, and summarise their findings for the user.
