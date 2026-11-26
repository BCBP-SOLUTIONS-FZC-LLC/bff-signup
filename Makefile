# -----------------------------
# CONFIG
# -----------------------------
-include .env

APP_NAME      ?= bff-signup
APP_ENV       ?= dev
GO            ?= go
BUILD_VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

export APP_NAME APP_ENV BUILD_VERSION

# Test package groups
TEST_UNIT_PKGS := ./test/unit/...
TEST_INT_PKGS  := ./test/integration/...
TEST_E2E_PKGS  := ./test/e2e/...

# White-box (package-internal) tests that need no Docker —
# included in unit runs and coverage (cmd/ env_test.go lives here).
TEST_INTERNAL_PKGS := ./internal/... ./cmd/...

# Source packages measured for coverage (excludes test helpers).
COVER_PKG_LIST := $(shell $(GO) list ./internal/... ./pkg/... | tr '\n' ',' | sed 's/,$$//')

# Build tags used across test suites — vet and lint run a second pass so
# tagged files are always checked.
ALL_TEST_TAGS := integration

# golangci-lint: .golangci.yml is a v2 config. Use the installed binary when it
# is v2 (CI installs v2.12.1); otherwise run CI's pinned version, so a local v1
# install cannot fail every commit (.githooks/pre-commit runs make lint).
GOLANGCI_LINT_VERSION := v2.12.1
GOLANGCI_LINT := $(shell golangci-lint version 2>/dev/null | grep -qE 'version v?2\.' && echo golangci-lint || echo "$(GO) run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)")

# -----------------------------
# SETUP
# -----------------------------

.PHONY: setup
setup: install-hooks
	@test -f .env || cp .env-example .env
	@echo "Environment ready (.env)"

# -----------------------------
# GIT HOOKS
# -----------------------------

.PHONY: install-hooks
install-hooks:
	@mkdir -p .git/hooks
	@cp .githooks/pre-commit .git/hooks/pre-commit
	@cp .githooks/pre-push .git/hooks/pre-push
	@chmod +x .git/hooks/pre-commit .git/hooks/pre-push
	@echo "Installed git hooks"

.PHONY: help
help:
	@echo "Available commands:"
	@echo "  make setup            - copy .env-example to .env if missing; install git hooks"
	@echo "  make install-hooks    - install local git hooks (.githooks -> .git/hooks); also run by make setup"
	@echo "  make tidy             - go mod tidy"
	@echo "  make fmt              - go fmt ./..."
	@echo "  make trivy            - build the image and Trivy-scan it like CI (CRITICAL/HIGH/UNKNOWN, fixed only, .trivyignore); run by .githooks/pre-push"
	@echo "  make vet              - go vet (default build + every test build tag)"
	@echo "  make lint             - run golangci-lint (default build + every test build tag)"
	@echo "  make arch-lint        - enforce .go-arch-lint.yml component boundaries"
	@echo "  make metrics-lint     - observability standard: naming, labels, registry, rule references, inventory"
	@echo "  make metrics-inventory - regenerate docs/observability/metric-registry.md"
	@echo "  make test             - unit + integration tests (requires Docker for Valkey)"
	@echo "  make test-ci          - test with race detector + coverage (used in CI)"
	@echo "  make test-unit        - unit tests only (no Docker required)"
	@echo "  make test-integration - Valkey integration tests (requires Docker)"
	@echo "  make test-e2e         - end-to-end tests (needs Docker; starts Valkey, mocks and the service itself; run as its own CI step)"
	@echo "  make test-smoke       - image size + startup gate (requires Docker)"
	@echo "  make race             - all tests with -race flag"
	@echo "  make run              - run the server locally (go run, sources .env)"
	@echo "  make build            - compile server binary to bin/"
	@echo "  make cover            - coverage profile + open HTML report"
	@echo "  make cover-func       - coverage summary by function"
	@echo "  make ci               - tidy + fmt-check + vet + lint + arch-lint + test-ci + build"
	@echo "  make docker-up        - start local Valkey"
	@echo "  make docker-down      - stop local containers"
	@echo "  make docker-reset     - stop containers and drop volumes"
	@echo "  make helm-lint        - lint Helm chart"
	@echo "  make helm-template    - render Helm templates with dev values"
	@echo "  make fmt-check        - verify gofmt formatting (no changes applied)"
	@echo "  make mod-verify       - go mod verify (check module download integrity)"
	@echo "  make vuln-check       - govulncheck on internal and pkg packages"
	@echo "  make pin-base-images  - fetch SHA digests and pin Dockerfile base images"
	@echo "  make godoc            - serve package docs locally via pkgsite"
	@echo "  make clean            - remove build artefacts"

# -----------------------------
# GO BASICS
# -----------------------------

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: fmt
fmt:
	$(GO) fmt ./...

.PHONY: vet
vet:
	$(GO) vet ./...
	$(GO) vet -tags=$(ALL_TEST_TAGS) ./...

# -----------------------------
# LINT
# -----------------------------

.PHONY: lint
lint:
	@echo "Running linter ($(GOLANGCI_LINT))..."
	$(GOLANGCI_LINT) run ./...
	$(GOLANGCI_LINT) run --build-tags=$(ALL_TEST_TAGS) ./...

# arch-lint: enforce .go-arch-lint.yml component boundaries — the same script
# CI runs (validate-test.yml).
.PHONY: arch-lint
arch-lint:
	bash .github/scripts/arch-lint.sh

# metrics-lint: the Enterprise Platform Observability Standard checks CI runs.
#   check-metric-naming.sh   bff_signup_* naming, suffixes, labels, adapter instrumentation
#   metrics-registry-lint.sh local-registry Proposed metrics are never alert/SLO sources
#   metricslint.sh           platform-gincommon's metricslint (scrape check + rule refs),
#                            rule references exist, Helm/flat rules in sync, Proposed-metric
#                            alerts labelled, metric inventory up to date
.PHONY: metrics-lint
metrics-lint:
	bash .github/scripts/check-metric-naming.sh
	bash .github/scripts/metrics-registry-lint.sh
	bash .github/scripts/metricslint.sh

# metrics-inventory: regenerate docs/observability/metric-registry.md from the
# pinned registry and a real scrape (metrics-lint fails when it is stale).
.PHONY: metrics-inventory
metrics-inventory:
	METRICS_INVENTORY_WRITE=1 bash .github/scripts/metricslint.sh

# log-sink-lint: enforce the Enterprise Platform Observability Standard requirement
# that all logs, metrics, and traces pass through platform-gincommon only.
# No standard library log, no fmt.Print* outside cmd/main(), no raw prometheus.
.PHONY: log-sink-lint
log-sink-lint:
	bash .github/scripts/check-log-sink.sh

# -----------------------------
# TESTS
# -----------------------------

# -- Private per-suite targets (run in parallel by test-ci / test) ------------
# Each writes its own coverage profile so profiles can be merged afterward.

.PHONY: _test-unit
_test-unit: | .coverage
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) \
	  -race -count=1 -timeout 120s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/unit.out

.PHONY: _test-integration
_test-integration: | .coverage
	bash -c 'set -a && [ -f .env ] && source .env; set +a && \
	  $(GO) test $(TEST_INT_PKGS) \
	  -tags=integration -race -count=1 -timeout 300s \
	  -coverpkg=$(COVER_PKG_LIST) \
	  -coverprofile=.coverage/integration.out'

# Merge per-suite profiles into a single coverage.out.
# Takes the MAX count per block so any suite covering a block is reflected.
.PHONY: _merge-coverage
_merge-coverage:
	@python3 scripts/merge_coverage.py \
	  .coverage/unit.out .coverage/integration.out \
	  > coverage.out
	@echo "==> coverage.out merged from all suites (max-count strategy)"

# -- test: parallel run (verbose, no coverage) --------------------------------
# Runs both suites concurrently; wall-clock = slowest suite.
.PHONY: test
test:
	$(MAKE) -j2 _test-unit-plain _test-integration-plain

# Verbose, no-coverage variants used by 'make test' above.
.PHONY: _test-unit-plain _test-integration-plain
_test-unit-plain:
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) \
	  -count=1 -timeout 120s -v
_test-integration-plain:
	$(GO) test $(TEST_INT_PKGS) \
	  -tags=integration -count=1 -timeout 300s -v

# -- test-ci: parallel + race + coverage (used by CI / 'make ci') -------------
# Both suites run concurrently; profiles are merged into coverage.out.
.PHONY: test-ci
test-ci: | .coverage
	$(MAKE) -j2 _test-unit _test-integration
	$(MAKE) _merge-coverage

# -- test-unit: unit + white-box internal tests (no Docker) -------------------
.PHONY: test-unit
test-unit:
	$(GO) test $(TEST_UNIT_PKGS) $(TEST_INTERNAL_PKGS) \
	  -count=1 -timeout 60s -v

.PHONY: test-integration
test-integration:
	$(GO) test $(TEST_INT_PKGS) -tags=integration -count=1 -timeout 300s -v

.PHONY: test-e2e
test-e2e:
	$(GO) test $(TEST_E2E_PKGS) -tags=e2e -count=1 -timeout 300s -v

.PHONY: test-load
test-load:
	k6 run test/load/signup.js

# test-smoke: image size + startup gate checks against the CI-built image.
# Mirrors what the CI 'smoke' job runs (.github/scripts/smoke-tests.sh).
.PHONY: test-smoke
test-smoke:
	docker build --tag bff-signup-ci-test .
	bash .github/scripts/smoke-tests.sh

# -----------------------------
# RUN
# -----------------------------

.PHONY: run
run:
	@-lsof -ti :$${APP_PORT:-8080} | xargs kill -9 2>/dev/null; true
	bash -c 'set -a && source .env && set +a && BUILD_VERSION=$(BUILD_VERSION) $(GO) run ./cmd/server'

# -----------------------------
# BUILD
# -----------------------------

.PHONY: build
build:
	@echo "Building server binary..."
	@mkdir -p bin
	$(GO) build -trimpath -o bin/$(APP_NAME) ./cmd/server
	@echo "Verifying all packages compile..."
	$(GO) build ./internal/... ./pkg/...

# -----------------------------
# DOCKER (LOCAL VALKEY)
# -----------------------------

.PHONY: docker-up
docker-up:
	@echo "Starting local Valkey..."
	docker compose up -d valkey

.PHONY: docker-down
docker-down:
	@echo "Stopping local containers..."
	docker compose down

.PHONY: docker-reset
docker-reset:
	@echo "Stopping containers and removing volumes..."
	docker compose down -v

# -----------------------------
# HELM
# -----------------------------

.PHONY: helm-lint
helm-lint:
	helm lint deploy/helm/ -f deploy/helm/values-dev.yaml

.PHONY: helm-template
helm-template:
	helm template $(APP_NAME) deploy/helm/ -f deploy/helm/values-dev.yaml

# -----------------------------
# CI
# -----------------------------

.PHONY: ci
ci: tidy fmt-check vet lint arch-lint metrics-lint log-sink-lint test-ci build

# -----------------------------
# COVERAGE
# -----------------------------

.PHONY: cover
cover: test-ci
	$(GO) tool cover -html=coverage.out

.PHONY: cover-func
cover-func: test-ci
	$(GO) tool cover -func=coverage.out

# -----------------------------
# DEBUG HELPERS
# -----------------------------

# race: quick parallel race-detector run without coverage overhead.
.PHONY: race
race:
	$(MAKE) -j2 _test-unit _test-integration

# -----------------------------
# CHECKS (mirror what CI runs; safe to call locally before pushing)
# -----------------------------

# fmt-check: verify formatting without modifying files (mirrors CI gofmt step).
.PHONY: fmt-check
fmt-check:
	@unformatted=$$(gofmt -l cmd/ internal/ pkg/ test/); \
	if [ -n "$$unformatted" ]; then \
		echo "FAIL: unformatted files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi
	@echo "gofmt: all files formatted"

# mod-verify: check that downloaded module zips match go.sum hashes.
.PHONY: mod-verify
mod-verify:
	$(GO) mod verify

# vuln-check: scan library packages for known vulnerabilities.
.PHONY: vuln-check
vuln-check:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@latest ./internal/... ./pkg/...

# -----------------------------
# HOOKS AND DOCS
# -----------------------------

# -----------------------------
# SWAGGER
# -----------------------------

# swag: generate OpenAPI spec from handler annotations into docs/swagger/.
# Re-run whenever annotations change. Output is checked into the repo.
# Swagger UI served at GET /swagger/*any on port 8080 when APP_ENV != prod.
.PHONY: swag
swag:
	@echo "Generating Swagger docs..."
	$(GO) run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
	  -g swagger_info.go \
	  -d cmd/server,internal/adapter/inbound/http \
	  --output docs/swagger \
	  --parseDependency \
	  --parseInternal
	@echo "Swagger docs written to docs/swagger/ — open http://localhost:8080/swagger/index.html"

.PHONY: swag-check
swag-check:
	@echo "Checking Swagger docs are up to date..."
	@cp -r docs/swagger docs/swagger.bak 2>/dev/null || true
	$(GO) run github.com/swaggo/swag/cmd/swag@v1.16.6 init \
	  -g swagger_info.go \
	  -d cmd/server,internal/adapter/inbound/http \
	  --output docs/swagger \
	  --parseDependency \
	  --parseInternal
	@if ! diff -rq docs/swagger docs/swagger.bak >/dev/null 2>&1; then \
	  echo "::error::Swagger docs are stale — run 'make swag' and commit docs/swagger/"; \
	  rm -rf docs/swagger.bak; exit 1; \
	fi
	@rm -rf docs/swagger.bak
	@echo "Swagger docs are up to date."

# godoc: serve package documentation locally using pkgsite.
.PHONY: godoc
godoc:
	@echo "Starting pkgsite at http://localhost:8080 -- press Ctrl-C to stop"
	$(GO) run golang.org/x/pkgsite/cmd/pkgsite@latest -open .

.PHONY: pin-base-images
pin-base-images:
	@echo "Fetching SHA digests for Dockerfile base images..."
	@GOLANG_DIGEST=$$(docker buildx imagetools inspect golang:1.23-alpine --format '{{.Manifest.Digest}}') && \
	 DISTROLESS_DIGEST=$$(docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot --format '{{.Manifest.Digest}}') && \
	 sed -E -i.bak \
	   -e "s|FROM golang:1.23-alpine(@sha256:[0-9a-f]+)?|FROM golang:1.23-alpine@$$GOLANG_DIGEST|" \
	   -e "s|FROM gcr.io/distroless/static-debian12:nonroot(@sha256:[0-9a-f]+)?|FROM gcr.io/distroless/static-debian12:nonroot@$$DISTROLESS_DIGEST|" \
	   Dockerfile && rm -f Dockerfile.bak && \
	 echo "golang:1.23-alpine $$GOLANG_DIGEST" > .docker-digests && \
	 echo "gcr.io/distroless/static-debian12:nonroot $$DISTROLESS_DIGEST" >> .docker-digests && \
	 echo "Digests written to .docker-digests -- commit both Dockerfile and .docker-digests"

# -----------------------------
# CLEAN
# -----------------------------

# Directory for per-suite coverage profiles (created on demand).
.coverage:
	@mkdir -p .coverage

.PHONY: clean
clean:
	rm -rf bin .coverage
	rm -f coverage.out coverage.html *.coverprofile

# -----------------------------
# TRIVY (same check as ci.yml's "Trivy CVE scan"; run by .githooks/pre-push)
# -----------------------------

TRIVY_IMAGE ?= bff-signup-trivy
GO_PRIVATE_TOKEN ?= $(shell gh auth token 2>/dev/null)
export GO_PRIVATE_TOKEN

.PHONY: trivy
trivy:
	@command -v trivy >/dev/null || { echo "trivy not found (brew install trivy)"; exit 1; }
	@test -n "$$GO_PRIVATE_TOKEN" || { echo "GO_PRIVATE_TOKEN is empty: export it or run 'gh auth login'"; exit 1; }
	docker build --platform linux/amd64 \
	  --secret id=go_private_token,env=GO_PRIVATE_TOKEN \
	  -t $(TRIVY_IMAGE) .
	trivy image --severity CRITICAL,HIGH,UNKNOWN --ignore-unfixed --ignorefile .trivyignore --exit-code 1 $(TRIVY_IMAGE)
