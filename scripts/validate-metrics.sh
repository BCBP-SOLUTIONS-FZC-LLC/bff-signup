#!/usr/bin/env bash
# validate-metrics.sh — enforce Signup BFF metric naming rules.
#
# All BFF metrics must use the signup_bff_ prefix (Tier 3, service-specific).
# This service has no platform_* (Tier 1) or iam_* (Tier 2) metrics.
#
# Checks:
#   1. Counter metrics MUST end with _total
#   2. Histogram metrics MUST end with _seconds
#   3. All metric names MUST use the signup_bff_ prefix
#   4. No high-cardinality labels (email, ip, tenant_id, user_id, signup_id, session_id)
#   5. Alert rule expressions in PrometheusRule must reference signup_bff_* names
#
# Exit 0 = all checks pass. Exit 1 = one or more violations found.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
METRICS_FILE="$ROOT/internal/adapter/outbound/metrics/metrics.go"
PROMETHEUS_RULE="$ROOT/deploy/helm/templates/prometheusrule.yaml"
PASS=true

err() { echo "FAIL: $*" >&2; PASS=false; }

# The metrics file may not exist yet early in development — skip gracefully.
if [ ! -f "$METRICS_FILE" ]; then
  echo "SKIP: $METRICS_FILE not found (metrics not yet implemented)"
  exit 0
fi

# ── 1. Counter suffix (_total) ────────────────────────────────────────────────
while IFS= read -r name; do
  if [[ "$name" != *_total ]]; then
    err "Counter metric \"$name\" must end with _total"
  fi
done < <(grep -E 'prometheus\.NewCounter(Vec)?' "$METRICS_FILE" -A5 | awk '/Name:/{match($0,/"[^"]+"/); if(RLENGTH>0) print substr($0,RSTART+1,RLENGTH-2)}')

# ── 2. Histogram suffix (_seconds) ────────────────────────────────────────────
while IFS= read -r name; do
  if [[ "$name" != *_seconds ]]; then
    err "Histogram metric \"$name\" must end with _seconds"
  fi
done < <(grep -E 'prometheus\.NewHistogram(Vec)?' "$METRICS_FILE" -A5 | awk '/Name:/{match($0,/"[^"]+"/); if(RLENGTH>0) print substr($0,RSTART+1,RLENGTH-2)}')

# ── 3. signup_bff_ prefix required on all metrics ─────────────────────────────
while IFS= read -r name; do
  if [[ "$name" != signup_bff_* ]]; then
    err "Metric \"$name\" must use the signup_bff_ prefix (Tier 3 service-specific)"
  fi
done < <(grep -E 'prometheus\.New(Counter|Histogram|Gauge)(Vec)?' "$METRICS_FILE" -A5 | awk '/Name:/{match($0,/"[^"]+"/); if(RLENGTH>0) print substr($0,RSTART+1,RLENGTH-2)}')

# ── 4. No high-cardinality labels ─────────────────────────────────────────────
# These labels would create an unbounded number of time series.
HIGH_CARDINALITY_LABELS=("email" "ip" "client_ip" "tenant_id" "user_id" "signup_id" "session_id" "trace_id" "request_id")
for label in "${HIGH_CARDINALITY_LABELS[@]}"; do
  if grep -qE "\"${label}\"" "$METRICS_FILE"; then
    err "High-cardinality label \"${label}\" found in $METRICS_FILE — prohibited (use low-cardinality labels only)"
  fi
done

# ── 5. PrometheusRule alert expressions reference valid names ─────────────────
if [ -f "$PROMETHEUS_RULE" ]; then
  # Extract metric names from PromQL expressions in the alert file
  # and verify they use the signup_bff_ prefix.
  while IFS= read -r metric; do
    if [[ "$metric" != signup_bff_* ]] && [[ "$metric" != "up" ]]; then
      err "Alert rule references non-bff metric \"$metric\" in $PROMETHEUS_RULE"
    fi
  done < <(grep -oE '[a-z_][a-z0-9_]*{' "$PROMETHEUS_RULE" 2>/dev/null | tr -d '{' | sort -u)
fi

# ── Result ────────────────────────────────────────────────────────────────────
if $PASS; then
  echo "OK: all signup_bff_ metric naming checks passed"
  exit 0
else
  echo ""
  echo "All Signup BFF metrics must use the signup_bff_ prefix with _total (counters) or _seconds (histograms)."
  exit 1
fi
