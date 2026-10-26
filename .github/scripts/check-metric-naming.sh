#!/usr/bin/env bash
# Enforces that all Prometheus metric names registered by bff-signup use the
# required signup_bff_ prefix (LLD §11, observability naming convention).
set -euo pipefail

REQUIRED_PREFIX="signup_bff_"
VIOLATIONS=""

while IFS= read -r -d '' file; do
  while IFS= read -r line; do
    # Match prometheus.NewCounterVec/NewGaugeVec/NewHistogramVec/NewSummaryVec Name fields
    if echo "$line" | grep -qE 'Name:[[:space:]]+"[^"]+\"'; then
      name=$(echo "$line" | grep -oE '"[^"]+"' | head -1 | tr -d '"')
      if [[ -n "$name" && "$name" != "${REQUIRED_PREFIX}"* ]]; then
        VIOLATIONS="${VIOLATIONS}\n  ${file}: metric name '${name}' does not start with '${REQUIRED_PREFIX}'"
      fi
    fi
  done < "$file"
done < <(find internal cmd -name '*.go' -print0)

if [[ -n "$VIOLATIONS" ]]; then
  echo "::error::Metric naming violation — all metrics must use the '${REQUIRED_PREFIX}' prefix (LLD §11)."
  printf "%b\n" "$VIOLATIONS"
  exit 1
fi

echo "Metric naming check passed — all metrics use '${REQUIRED_PREFIX}' prefix."
