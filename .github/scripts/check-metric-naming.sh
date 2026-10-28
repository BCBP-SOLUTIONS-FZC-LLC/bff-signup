#!/usr/bin/env bash
# Enforces the Enterprise Platform Observability Standard's construction and
# naming rules against internal/adapter/outbound/metrics/ — the sole package
# that builds Prometheus collectors in this service.
#
# Rules:
#   - No collector is built with prometheus.New* in non-metrics-package code:
#     every instrumentation call site uses the nil-safe helpers in
#     internal/adapter/outbound/metrics/, never raw prometheus constructors
#     outside that package (rule 8: const labels injected centrally).
#   - Tier 1 platform_* names come from gincommon.NewSharedHistogramVec/
#     NewSharedCounterVec (registerShared in business.go).
#   - Tier 3 bff_signup_* names are the only Prometheus collectors built
# which validate the bff_signup_ prefix — "bff" domain registered in
# platform-gincommon v2.2.0 (PR #49). Naming convention
# bff_signup_<snake_case> is enforced by gincommon + by this script.
#   - Counter names MUST end in _total.
#   - Histogram names MUST end in _seconds.
#   - No gauge names end in _total.
#
set -euo pipefail

DIR="internal/adapter/outbound/metrics"
test -d "$DIR" || {
  echo "::error file=${DIR}::metrics package not found — bff_signup_* collectors must live in ${DIR}"
  exit 1
}

python3 - "$DIR" <<'PYEOF'
import glob
import re
import sys

pkg = sys.argv[1]
errors = []

# ── Rule: no prometheus.New* outside the metrics package ─────────────────────
for path in sorted(glob.glob("internal/**/*.go", recursive=True) + glob.glob("cmd/**/*.go", recursive=True)):
    if path.startswith(f"{pkg}/") or path.endswith("_test.go"):
        continue
    text = open(path, encoding="utf-8").read()
    for m in re.finditer(r"prometheus\.New(Counter|CounterVec|Gauge|GaugeVec|Histogram|HistogramVec|Summary|SummaryVec)\(", text):
        line = text[: m.start()].count("\n") + 1
        errors.append(f"{path}:{line}: prometheus.New{m.group(1)} — instrumentation must call metrics package helpers (rule 8)")

# ── Validate the metrics package itself ──────────────────────────────────────
sources = {p: open(p, encoding="utf-8").read()
           for p in sorted(glob.glob(f"{pkg}/*.go"))
           if not p.endswith("_test.go")}

def body_of(fn_name):
    for text in sources.values():
        m = re.search(rf"func {fn_name}\(.*?\)[^{{]*\{{(.*?)\n\}}", text, re.DOTALL)
        if m:
            return m.group(1)
    errors.append(f"{pkg}/business.go: could not locate {fn_name}()")
    return ""

# registerShared — must use only gincommon.NewShared*Vec with platform_* names
shared_names = re.findall(r'gincommon\.NewShared\w+\(nil, "([^"]+)"\)', body_of("registerShared"))
for name in shared_names:
    if not name.startswith("platform_") and not (name.startswith("iam_") and not name.startswith("iam_bff_")):
        errors.append(f'"{name}": registerShared() must build only platform_* or domain-shared names')

# registerServiceMetrics — must use gincommon.New*Vec with bff_signup_* names
# "bff" registered in platform-gincommon v2.2.0; gincommon constructors now enforce prefix.
tier3 = re.findall(r'gincommon\.New(CounterVec|Counter|GaugeVec|Gauge|HistogramVec)\(nil,\s*"([^"]+)"', body_of("registerServiceMetrics"))
if not tier3:
    errors.append(f"{pkg}/business.go: registerServiceMetrics() builds no Tier 3 collectors")
for kind, name in tier3:
    if not re.fullmatch(r"bff_signup_[a-z0-9_]+", name):
        errors.append(f'"{name}": Tier 3 collectors must be bff_signup_<snake_case> (Tier 3 pattern: <domain>_<service>_<metric>)')
    if kind.startswith("Counter") and not name.endswith("_total"):
        errors.append(f'"{name}": {kind} must end in _total')
    if kind.startswith("Histogram") and not name.endswith("_seconds"):
        errors.append(f'"{name}": {kind} must end in _seconds')
    if kind.startswith("Gauge") and name.endswith("_total"):
        errors.append(f'"{name}": a gauge must not end in _total')

# ── Label vocabulary: high-cardinality labels must NEVER appear ─────────────
# Rule: platform and domain shared metrics must use approved label vocabularies.
# Service-specific (Tier 3) must also never use high-cardinality identifiers.
PROHIBITED_LABELS = {
    "user_id", "email", "tenant_id", "request_id",
    "event_id", "session_id", "signup_id", "ip", "ip_address",
}
# Scan all WithLabelValues call sites in call-site code (service/adapter layers)
# for label values that look like high-cardinality identifiers being passed
# into Inc*/Observe* helpers.  We cannot recover the label names at call-site
# level, but we CAN enforce the approved label NAMES in the metric declarations.
# Extract label names from gincommon.New*Vec(..., "label1", "label2") calls.
for text in sources.values():
    for m in re.finditer(
        r'gincommon\.New(?:CounterVec|HistogramVec|GaugeVec)\(nil,\s*"[^"]+",\s*"[^"]+",\s*((?:"[^"]+"(?:,\s*)?)+)\)',
        text,
    ):
        label_names = re.findall(r'"([^"]+)"', m.group(1))
        for lname in label_names:
            if lname in PROHIBITED_LABELS:
                line = text[:m.start()].count("\n") + 1
                errors.append(
                    f'label "{lname}" is a high-cardinality identifier — '
                    f"prohibited on all tiers (rule: no user_id/email/tenant_id/... as label)"
                )

# ── Tier 2 boundary: warn on any bff_* that is NOT bff_signup_* ─────────────
# The bff domain currently has a single service (bff-signup). Any bff_* metric
# that does not carry the bff_signup_ prefix would be a Tier 2 domain-shared
# metric, which requires domain-registry approval before adoption.
for text in sources.values():
    for m in re.finditer(r'gincommon\.New\w+\(nil,\s*"(bff_(?!signup_)[^"]+)"', text):
        line = text[:m.start()].count("\n") + 1
        errors.append(
            f'"{m.group(1)}": bff_<x>_* without bff_signup_ prefix is a Tier 2 domain-shared metric — '
            f"requires domain-registry approval (standard rule 12; currently no Tier 2 bff_* metrics are approved)"
        )

# ── Verify ObserveDependencyCall is called from each outbound adapter ────────
# The bff_signup_dependency_call_duration_seconds histogram must be populated
# by every outbound HTTP adapter, not only by the metrics package itself.
EXPECTED_INSTRUMENTED_ADAPTERS = [
    "internal/adapter/outbound/orgmembership/client.go",
    "internal/adapter/outbound/realmprovisioner/client.go",
    "internal/adapter/outbound/turnstile/client.go",
    "internal/adapter/outbound/catalog/client.go",
]
for adapter_path in EXPECTED_INSTRUMENTED_ADAPTERS:
    try:
        adapter_text = open(adapter_path, encoding="utf-8").read()
    except FileNotFoundError:
        errors.append(f"{adapter_path}: adapter file not found")
        continue
    if "ObserveDependencyCall" not in adapter_text:
        errors.append(
            f"{adapter_path}: ObserveDependencyCall not called — "
            f"bff_signup_dependency_call_duration_seconds will be empty for this adapter "
            f"(standard: every outbound HTTP adapter must be instrumented)"
        )

total = len(shared_names) + len(tier3)
if errors:
    for e in errors:
        print(f"::error::{e}")
    print(f"\n{len(errors)} violation(s) across {total} collectors checked.")
    sys.exit(1)

print(f"  ✔  {total} collectors — naming, suffix, construction, label vocabulary and adapter instrumentation rules OK")
PYEOF