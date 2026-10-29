#!/usr/bin/env bash
# Sets SOURCE_DATE_EPOCH to the timestamp of the latest git commit for
# reproducible Docker image builds (same source → same layer digests).
set -euo pipefail

SOURCE_DATE_EPOCH=$(git log -1 --format='%ct')
echo "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}"
echo "SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH}" >> "$GITHUB_OUTPUT"
