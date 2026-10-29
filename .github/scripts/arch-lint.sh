#!/usr/bin/env bash
# Enforces Clean Architecture dependency rules defined in .go-arch-lint.yml.
# Prevents adapter code from being imported by core/domain or core/service.
set -euo pipefail

if ! command -v go-arch-lint >/dev/null; then
  go install github.com/fe3dback/go-arch-lint@v1.15.0
fi
go-arch-lint check --project-path .
