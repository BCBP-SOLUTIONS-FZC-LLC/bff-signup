#!/usr/bin/env bash
# validate-metrics.sh — local developer check for Signup BFF metric naming.
#
# Enforces the Enterprise Platform Observability Standard:
#   Tier 1 platform_* — shared across domains; built via gincommon.NewShared*Vec
#   Tier 3 bff_signup_* — service-specific; pattern: <domain>_<service>_<metric>
#
# This script is the developer-friendly version of .github/scripts/check-metric-naming.sh.
# The CI script is authoritative; this one provides friendlier output locally.
#
# Checks:
#   1. Counter metrics MUST end with _total
#   2. Histogram metrics MUST end with _seconds
#   3. Tier 3 metric names MUST use the bff_signup_ prefix
#   4. No high-cardinality labels (email, ip, tenant_id, user_id, signup_id, session_id)
#   5. No Proposed platform_* metrics in PrometheusRule expr: blocks
#      (delegated to .github/scripts/metrics-registry-lint.sh)
#
# Exit 0 = all checks pass. Exit 1 = one or more violations found.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
METRICS_BUSINESS="$ROOT/internal/adapter/outbound/metrics/business.go"
PROMETHEUS_RULE="$ROOT/deploy/helm/templates/prometheusrule.yaml"
PASS=true

err() { echo "::error::$*" >&2; PASS=false; }

# The metrics package may not exist yet early in development — skip gracefully.
if [ ! -f "$METRICS_BUSINESS" ]; then
  echo "SKIP: $METRICS_BUSINESS not found"
  exit 0
fi

# ── 1 + 2 + 3: suffix and prefix rules via the CI script ─────────────────────
# Delegate to the authoritative CI script which has the full parser.
if ! bash "$ROOT/.github/scripts/check-metric-naming.sh" 2>&1; then
  PASS=false
fi

# ── 4. No high-cardinality labels ─────────────────────────────────────────────
HIGH_CARDINALITY_LABELS=("email" "client_ip" "tenant_id" "user_id" "signup_id" "session_id" "trace_id" "request_id")
for label in "${HIGH_CARDINALITY_LABELS[@]}"; do
  if grep -qE '"'"${label}"'"' "$METRICS_BUSINESS"; then
    err "High-cardinality label \"${label}\" found in business.go — prohibited (standard label governance)"
  fi
done

# ── 5. Registry lint (Proposed metrics not in alert expr) ─────────────────────
if ! bash "$ROOT/.github/scripts/metrics-registry-lint.sh" 2>&1; then
  PASS=false
fi

# ── Result ────────────────────────────────────────────────────────────────────
if $PASS; then
  echo "OK: all bff_signup_* metric naming checks passed (Tier 3 bff_signup_* + platform_* Tier 1)"
  exit 0
else
  echo ""
  echo "Tier 3 metrics must use the bff_signup_* prefix with _total (counters) or _seconds (histograms)."
  echo "See: Enterprise Platform Observability Standard + internal/adapter/outbound/metrics/business.go"
  exit 1
fi
