#!/usr/bin/env bash
# SOURCE_DATE_EPOCH + buildx cache targets before docker/build-push-action.
# Mirrors iam-org-membership/.github/scripts/docker-build-prep.sh.
set -euo pipefail

echo "::group::Docker build — preparation"
bash .github/scripts/set-source-date-epoch.sh
bash .github/scripts/set-build-cache-targets.sh
echo "::endgroup::"
