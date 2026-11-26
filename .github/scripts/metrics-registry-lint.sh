#!/usr/bin/env bash
# Enforces the Enterprise Platform Observability Standard's governance
# rules 11/12 ("services SHALL NOT invent new platform_*/domain-shared
# metrics unilaterally"; "require observability governance approval
# before adoption"): a platform_* metric whose PlatformRegistry status
# (internal/adapter/outbound/metrics/registry.go) is StatusProposed — i.e.
# submitted but not yet ratified — must never be the live query target of
# a Prometheus alert, recording rule, or SLO expression anywhere in this
# repo. Mentioning a Proposed metric name in a comment (documenting the
# shadow-emission/migration plan) is fine and expected; using it inside an
# `expr:` is not, until the registry entry's Status flips to
# StatusCanonical.
#
# Mirrors iam-org-membership/.github/scripts/metrics-registry-lint.sh.
set -euo pipefail

cd "$(dirname "$0")/../.."

REGISTRY_GO="internal/adapter/outbound/metrics/registry.go"
RULE_FILES=(
  deploy/monitoring/app-alerts.yml
  deploy/monitoring/slo-rules.yml
  deploy/helm/templates/prometheusrule.yaml
  deploy/monitoring/prometheus-adapter-rule.yaml
)

if [ ! -f "$REGISTRY_GO" ]; then
  echo "OK: no $REGISTRY_GO in this repo — nothing to check"
  exit 0
fi

python3 - "$REGISTRY_GO" "${RULE_FILES[@]}" <<'PYEOF'
import re
import sys

registry_go = sys.argv[1]
rule_files  = sys.argv[2:]

# --- Parse Proposed names from registry.go ---
text = open(registry_go, encoding="utf-8").read()

# Find StatusProposed entries: extract the Name field of each block whose
# Status line says "StatusProposed".
blocks = re.split(r"\{", text)[1:]  # split on struct literal opens
proposed = set()
for block in blocks:
    if 'StatusProposed' not in block:
        continue
    m = re.search(r'Name:\s+"([^"]+)"', block)
    if m:
        proposed.add(m.group(1))

if not proposed:
    print("OK: no Proposed metrics in registry — nothing to guard")
    sys.exit(0)

print(f"Proposed (not-yet-canonical) metrics guarded: {', '.join(sorted(proposed))}")

# --- Scan rule files for expr: blocks referencing Proposed names -------
errors = []
for path in rule_files:
    try:
        content = open(path, encoding="utf-8").read()
    except FileNotFoundError:
        continue

    in_expr = False
    for lineno, line in enumerate(content.splitlines(), 1):
        stripped = line.strip()
        # A line like "expr: |" or "expr: <inline>" opens an expression block.
        if re.match(r"expr\s*:", stripped):
            in_expr = True
        elif in_expr:
            # Indented lines after expr: | belong to the expression.
            # A line at zero indent (or a new YAML key) closes it.
            if not line.startswith(" ") and not line.startswith("\t") and stripped:
                in_expr = False

        if not in_expr:
            continue

        # Skip comment lines inside the expr block.
        if stripped.startswith("#"):
            continue

        for name in proposed:
            if name in line:
                errors.append(
                    f"{path}:{lineno}: Proposed metric '{name}' used in expr "
                    f"(rule 12: only Canonical metrics may be alerting/SLO sources; "
                    f"flip registry.go entry to StatusCanonical after governance approval)"
                )

if errors:
    for e in errors:
        print(f"::error::{e}")
    print(f"\n{len(errors)} violation(s) — Proposed metrics must not be alerting/SLO sources.")
    sys.exit(1)

print("OK — no Proposed metrics used in expr blocks.")
PYEOF