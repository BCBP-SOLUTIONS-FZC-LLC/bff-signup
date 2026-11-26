---
name: ci-local
description: Run this repo's full CI gate locally, using CI's pinned tool versions, and report one pass/fail table. Use before pushing or opening a PR.
disable-model-invocation: true
argument-hint: "[quick]  (quick = skip race/coverage tests and e2e)"
---

# /ci-local — bff-signup CI gate, locally

Reproduce what `.github/workflows/validate-quality.yml`, `validate-test.yml` and
`changelog-check.yml` check on a PR, so failures show up before the push.

Run every step even when an earlier one fails (except step 1), then report all
results together. Do **not** fix anything unless the user asks; this skill reports.

If `$ARGUMENTS` contains `quick`, skip steps 6 and 7 and say so in the report.

## 1. Pinned golangci-lint (v2, not the machine's v1)

The local `golangci-lint` may be v1, which refuses the repo's v2 config. Read the
version CI pins and install it into a per-version tool dir (once):

```bash
VER=$(grep -oE 'golangci-lint@v[0-9.]+' .github/workflows/validate-quality.yml | head -1 | cut -d@ -f2)
TOOLS="$HOME/.cache/bff-signup-tools/golangci-lint-$VER"
[ -x "$TOOLS/golangci-lint" ] || GOBIN="$TOOLS" go install "github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$VER"
export PATH="$TOOLS:$PATH"
golangci-lint version
```

Prefix every later `make` call with `PATH="$TOOLS:$PATH"` (shell state does not
persist between Bash calls). If the install fails (offline), stop and tell the user.

## 2. Static gate (validate-quality.yml)

Run each, recording pass/fail:

```bash
make tidy && git diff --exit-code go.mod go.sum   # tidy must be a no-op
make fmt-check
make vet
make lint                                         # default build + integration tag
make arch-lint
make metrics-lint                                 # naming + registry guard
make log-sink-lint
make vuln-check                                   # govulncheck; needs network
```

Dockerfile digest pin (same check as CI):

```bash
! grep -E '^FROM [^ @]+:[^ @]+( AS .+)?$' Dockerfile
```

## 3. Swagger is current

CI only checks that the files exist and parse. Check that they are regenerated:

```bash
make swag && git diff --exit-code -- docs/swagger/
```

A diff means handler annotations or DTOs changed without `make swag`. Report the
changed files. The regenerated files stay in the working tree; tell the user.

## 4. Helm

```bash
make helm-lint
```

If `helm` is not installed, mark the step SKIPPED (not failed).

## 5. CHANGELOG (changelog-check.yml)

If the branch changes `internal/`, `cmd/`, `deploy/`, `pkg/` or `api/` against
`main`, `CHANGELOG.md` must change too:

```bash
BASE=$(git merge-base origin/main HEAD 2>/dev/null || git merge-base main HEAD)
CHANGED=$( { git diff --name-only "$BASE"; git ls-files --others --exclude-standard; } | sort -u)
echo "$CHANGED" | grep -qE '^(internal|cmd|deploy|pkg|api)/' && ! echo "$CHANGED" | grep -qx CHANGELOG.md && echo FAIL || echo OK
```

This includes uncommitted work, so it checks what the PR would contain.

## 6. Tests with race detector + coverage gate (validate-test.yml)

Needs Docker (Valkey testcontainers). If `docker ps` fails, mark SKIPPED.

```bash
make test-ci
GITHUB_OUTPUT=/dev/null bash .github/scripts/coverage-gate.sh
```

The real gate is `coverage-gate.sh` (97% by default; `COVERAGE_THRESHOLD` overrides). Report the total and, if it fails, the lowest-covered changed files:

```bash
go tool cover -func=coverage.out | grep -F -f <(git diff --name-only "$BASE" -- '*.go' | sed 's|^|/|') | sort -k3 -n | head -10
```

## 7. E2E suite

```bash
go test -tags=e2e -count=1 -timeout 300s ./test/e2e/
```

Needs Docker; SKIPPED otherwise.

## 8. Build

```bash
make build
```

## Report

One table, then details only for failures:

| Step | Result | Note |
|---|---|---|
| golangci-lint vX | PASS / FAIL / SKIPPED | … |
| … | | |

For each failure: the command, the relevant 10–20 lines of output (not the full
log), and the likely fix (e.g. "run `make swag` and commit `docs/swagger/`").
End with one line: ready to push, or what blocks it.
