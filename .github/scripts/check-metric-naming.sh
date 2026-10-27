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
#     outside gincommon's validated constructors, because the "bff" domain is
#     not yet in the platform registry vocabulary. The naming convention
#     bff_signup_<snake_case> is enforced here instead.
#   - Counter names MUST end in _total.
#   - Histogram names MUST end in _seconds.
#   - No gauge names end in _total.
#
# PENDING: once "bff" is added to the Platform Observability Registry, switch
# the prometheus.NewCounterVec/NewHistogramVec calls in business.go to
# gincommon.NewCounterVec/NewHistogramVec (which enforce the prefix in the
# library itself), and update the validator below accordingly.
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

# registerServiceMetrics — must use bff_signup_* names with correct suffixes
tier3 = re.findall(r'prometheus\.New(CounterVec|Counter|GaugeVec|Gauge|HistogramVec)\(\s*prometheus\.\w+Opts\{[^}]*Name:\s*"([^"]+)"', body_of("registerServiceMetrics"), re.DOTALL)
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

total = len(shared_names) + len(tier3)
if errors:
    for e in errors:
        print(f"::error::{e}")
    print(f"\n{len(errors)} violation(s) across {total} collectors checked.")
    sys.exit(1)

print(f"  ✔  {total} collectors — naming, suffix and construction rules OK (bff_signup_* Tier 3; platform_* Tier 1)")
PYEOF