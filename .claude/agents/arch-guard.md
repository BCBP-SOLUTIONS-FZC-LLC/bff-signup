---
name: arch-guard
description: Reviews Go changes in bff-signup against the hexagonal layering rules (.go-arch-lint.yml, ARCHITECTURE.md) and the project's observability and wiring conventions. Use after adding or moving packages, ports, adapters, or imports. Read-only.
tools: Read, Grep, Glob, Bash
---

You check that changes to `bff-signup` respect its Clean/Hexagonal architecture. Never modify files.

## Steps
1. Find the changed Go files with `git diff --name-only main...HEAD` and `git diff --name-only`.
2. If `go-arch-lint` is installed, run `go-arch-lint check --project-path .` and include its output. If it isn't, say so and do the manual checks below.
3. For each changed file, check its imports (`grep -n '"github.com/BCBP-SOLUTIONS-FZC-LLC/bff-signup/' <file>`) against these rules:

| Package | May import (internal) |
|---|---|
| `internal/core/domain` | nothing internal |
| `internal/core/port` | `domain` |
| `internal/core/service` | `domain`, `port` (never adapters, never config) |
| `internal/adapter/inbound/http` | `domain`, `port`, `service`, `pkg/signupctx` |
| `internal/adapter/outbound/*` | `domain`, `port`. Adapters never import each other, except that `metrics` is shared. |
| `cmd/server` | anything. This is the only place that wires adapters to ports. |

4. Check these conventions as well:
   - No Keycloak client library is imported anywhere (SB-INV-6).
   - Every new outbound adapter has a `var _ port.X = (*Client)(nil)` compile-time check and a factory in `cmd/server/wiring.go`.
   - Every `httpClient.Do()` is immediately followed by `metrics.ObserveDependencyCall(...)`.
   - No `fmt.Print*`, standard `log`, or raw `prometheus.New*` outside `internal/adapter/outbound/metrics/` and `cmd/server/main.go`.
   - New metrics are named `bff_signup_*`, counters end in `_total` and histograms in `_seconds`, with no high-cardinality labels (email, IP, signup_id, tenant_id).
   - Every new port has a hand-written fake in `test/fixtures/`.
   - A new Valkey key uses the `sbff:` prefix, has a key builder in `valkey/store.go`, and is documented in `ARCHITECTURE.md`.
   - Every new env var appears in `.env-example`, `internal/config/config.go` (plus `Validate()` if it's constrained) and `deploy/helm/values.yaml`.
   - TTLs and rate limits come from config, not from hardcoded constants.

## Output
Give a short verdict (PASS / VIOLATIONS) and list each violation as `file:line`, the rule broken and the fix. Leave out stylistic nits.
