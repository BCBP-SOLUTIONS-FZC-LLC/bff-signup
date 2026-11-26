# Versioning and releases

This repository is a **deployed Go microservice** — it has no `pkg/` public API surface for another service to `go get`. What gets versioned is the **container image** (`ghcr.io/bcbp-solutions-fzc-llc/bff-signup`), the **Helm chart** (`deploy/helm/`), and the **runtime contract** — the REST API and operational surface that the signup form, IAM services, and operators depend on. Versions are published with **Git tags** and described in [CHANGELOG.md](./CHANGELOG.md).

## Semantic versioning (SemVer)

We use [SemVer 2.0.0](https://semver.org/): `MAJOR.MINOR.PATCH` (e.g. `v0.2.0`).

| Bump | When you change | Examples |
|---|---|---|
| **MAJOR** | Breaking change in the runtime contract | Removing/renaming a public endpoint or response field, narrowing an error code, removing a required env var, incompatible `values.yaml` restructure, changing the session cookie name or CSRF header name |
| **MINOR** | New backward-compatible capability | A new optional endpoint, new optional response field, new optional env var, new Helm value, new Prometheus metric, new rate-limit scope |
| **PATCH** | Backward-compatible fix | Bug fix, PBKDF2 iteration count increase (password policy sync), dependency bump with no observable behaviour change, documentation correction |

### What counts as the runtime contract

| In scope (SemVer applies) | Out of scope (may change without MAJOR) |
|---|---|
| REST endpoints: paths, methods, request/response shapes for SB-1…SB-4, `/healthz`, `/readyz` | `internal/*` package structure, Go identifiers — nothing here is imported |
| Error codes (`error` field in the error response envelope) and their HTTP status | Error `message` strings — these are for humans, not machines |
| Session cookie name (`sbff_sid`), CSRF header name (`X-CSRF-Token`) | Cookie `Max-Age`, `Path` — tunable |
| Required env var **names and semantics** (`.env-example`) | Env var **defaults** — tunable without a MAJOR |
| `deploy/helm/values.yaml` top-level key names callers actually set (`image.*`, `env.*`, `service.*`, `autoscaling.*`, `existingSecret`) | Chart internals (`_helpers.tpl`, template structure) |
| Prometheus metric **names** (`bff_signup_*`) — dashboards and alerts key off these | Metric label cardinality beyond what is documented |

### Guarantees

- **Pre-`v1.0.0` (current status):** per [SemVer §4](https://semver.org/#spec-item-4), anything may change at any time while the major version is `0`. The table above still indicates what is *more* disruptive within `v0.x`, but callers should not assume compatibility across a MINOR bump the way they could once `v1.0.0` ships.
- **MAJOR (once `v1` ships):** breaking changes to the runtime contract ship as `v2.0.0` with migration notes in CHANGELOG.
- **MINOR:** safe to redeploy without changing the signup form or Helm values (unless you opt into a new capability).
- **PATCH:** drop-in image replacement; upgrade recommended for security fixes.

## Supported releases

| Version | Status | Notes |
|---|---|---|
| `main` (unreleased) | Active development | Not yet deployed to any environment |

This service has not yet shipped a tagged release. `v0.1.0` will be the first once the go-live prerequisites in `docs/lld/signup-bff-action-tracker.md` are met.

## Consume a release

This service is **not** consumed via `go get`. It is consumed as a **container image**, deployed via the **Helm chart** in `deploy/helm/`.

### Image tag scheme

| Tag | Use |
|---|---|
| `ghcr.io/bcbp-solutions-fzc-llc/bff-signup:<git-sha>` | Every push to `main` (CI) |
| `ghcr.io/bcbp-solutions-fzc-llc/bff-signup:vMAJOR.MINOR.PATCH` | Tagged release (recommended for production) |
| `ghcr.io/bcbp-solutions-fzc-llc/bff-signup:latest` | Latest stable release — dev/experiment only, never production |

### Deploy via Helm

```bash
helm upgrade bff-signup ./deploy/helm \
  --install \
  --namespace bff \
  --set image.tag=v0.1.0 \
  --set existingSecret=bff-signup-prod-secrets \
  -f deploy/helm/values-prod.yaml
```

## Maintainer release process

1. **Merge** all changes for the release to `main`. `CHANGELOG.md [Unreleased]` should already be current (CONTRIBUTING.md requires it per PR).

2. **Update `CHANGELOG.md`:** move `[Unreleased]` entries into `## [X.Y.Z] - YYYY-MM-DD`.

3. **Bump `deploy/helm/Chart.yaml`** `version`/`appVersion` to match `X.Y.Z`.

4. **Run `make ci` locally:**
   ```bash
   make ci   # tidy + fmt-check + vet + lint + arch-lint + test-ci (race + coverage) + build
   ```

5. **Create and push an annotated tag:**
   ```bash
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

6. **CI runs automatically:** validate-quality + validate-test → Docker build → Trivy CVE scan → push to GHCR → GitHub Release created.

7. **Notify:** Web/Growth team and SRE if a new env var, Helm value change, or IAM contract change requires coordinated rollout.

## Compatibility matrix

| bff-signup | Go (`go.mod`) | `platform-gincommon` |
|---|---|---|
| `main` (unreleased) | `1.26.11` | `v2.7.0` |

Key runtime dependencies in `go.mod`: `go-redis/v9` (Valkey), `gin-gonic/gin`, `oklog/ulid/v2`, `google/uuid`, `golang.org/x/crypto` (PBKDF2). None are re-exported.

## Related files

| File | Purpose |
|---|---|
| [CHANGELOG.md](./CHANGELOG.md) | User-facing history per version |
| [README.md](./README.md) | Mental model, API contract, local dev |
| [CONTRIBUTING.md](./CONTRIBUTING.md) | Dev setup, testing, PR process, deployment pipeline |
| [ARCHITECTURE.md](./ARCHITECTURE.md) | Layer model, sequence diagrams, state machine |
| [docs/lld/signup-bff-lld.md](./docs/lld/signup-bff-lld.md) | Full LLD — runtime contract in detail |
| [deploy/helm/Chart.yaml](./deploy/helm/Chart.yaml) | Helm chart version / app version |
| [go.mod](./go.mod) | Module path and minimum Go version — not a consumable package |
