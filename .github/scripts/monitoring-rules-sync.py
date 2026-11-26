#!/usr/bin/env python3
"""Two checks on the alert and SLO rules.

1. Fail when the deployed PrometheusRule (deploy/helm/templates/prometheusrule.yaml)
and the flat rule files (deploy/monitoring/app-alerts.yml + slo-rules.yml) drift
apart: same groups, same alert/record names, same expressions. The Helm copy may
only differ in its templated job/namespace selectors and Helm escaping.
(They drifted before: the chart kept querying signup_bff_* names nothing emits.)

2. With a registry path (argv[1]): an alert whose expression uses a Proposed
   registry metric (directly, or through a recording rule built on one) must
   carry the label `metric_status: proposed`, so routing can treat it as
   provisional until governance ratifies the metric (the iam-org-membership
   convention). Ratified (approved) metrics need no label.
"""
import json
import re
import sys

FLAT = ("deploy/monitoring/app-alerts.yml", "deploy/monitoring/slo-rules.yml")
HELM = "deploy/helm/templates/prometheusrule.yaml"


def rules(text):
    """Return [(group, name, normalised expr)] in file order."""
    text = re.sub(r'\{\{ "(\{\{ [^}]* \}\})" \}\}', r"\1", text)          # Helm-escaped Prometheus templates
    text = re.sub(r"job=\{\{[^}]*\}\}", 'job="bff-signup"', text)        # templated job selector
    text = re.sub(r"namespace=\{\{[^}]*\}\},\s*", "", text)               # templated namespace selector
    out, group, name, expr, in_expr, indent = [], None, None, [], False, 0
    for line in text.splitlines() + ["- name: __end__"]:
        s = line.strip()
        cur = len(line) - len(line.lstrip())
        if in_expr and s and cur <= indent:
            out.append((group, name, " ".join(" ".join(expr).split())))
            in_expr = False
        m = re.match(r"- name:\s*(\S+)", s)
        if m:
            group = m.group(1)
            continue
        m = re.match(r"- (alert|record):\s*(\S+)", s)
        if m:
            name, indent = m.group(2), cur
            continue
        m = re.match(r"expr:\s*\|?\s*(.*)", s)
        if m and not in_expr:
            in_expr, expr = True, [m.group(1)]
            continue
        if in_expr and s and not s.startswith("#"):
            expr.append(s)
    return out


def blocks(text):
    """Return {(kind, name): block text} for every alert/record in a rule file."""
    out, cur, buf, indent = {}, None, [], 0
    for line in text.splitlines() + ["- __end__: x"]:
        s, ind = line.strip(), len(line) - len(line.lstrip())
        m = re.match(r"- (alert|record):\s*(\S+)", s)
        if (m or (s.startswith("- ") and ind <= indent)) and cur:
            out[cur] = "\n".join(buf)
            cur = None
        if m:
            cur, buf, indent = (m.group(1), m.group(2)), [], ind
        elif cur:
            buf.append(line)
    return out


def check_proposed_labels(registry_path):
    proposed = {
        m["name"] for m in json.load(open(registry_path, encoding="utf-8"))["metrics"]
        if m.get("status") == "proposed"
    }
    allb = {}
    for path in FLAT:
        allb.update(blocks(open(path, encoding="utf-8").read()))

    def uses_proposed(text):
        code = "\n".join(l for l in text.splitlines() if not l.strip().startswith("#"))
        return any(re.search(rf"\b{re.escape(p)}(_bucket|_count|_sum)?\b", code) for p in proposed)

    tainted = {name for (kind, name), b in allb.items() if kind == "record" and uses_proposed(b)}
    errors = []
    for (kind, name), b in allb.items():
        if kind != "alert":
            continue
        expr = b.split("labels:")[0]
        if uses_proposed(expr) or any(re.search(rf"\b{re.escape(r)}\b", expr) for r in tainted):
            if not re.search(r"metric_status:\s*proposed", b):
                errors.append(f"alert {name} uses a Proposed registry metric but lacks the label metric_status: proposed")
    for e in errors:
        print(f"::error::{e}")
    if errors:
        sys.exit(1)
    print("proposed-metric alerts are labelled metric_status: proposed.")


def check_runbooks():
    """Every alert needs a runbook annotation pointing at a heading in runbooks.md."""
    runbooks = open("docs/observability/runbooks.md", encoding="utf-8").read()
    headings = {h.lower() for h in re.findall(r"^### (\S+)", runbooks, re.M)}
    errors = []
    for path in FLAT:
        for (kind, name), b in blocks(open(path, encoding="utf-8").read()).items():
            if kind != "alert":
                continue
            m = re.search(r'runbook:\s*"docs/observability/runbooks\.md#([^"]+)"', b)
            if not m:
                errors.append(f"{path}: alert {name} has no runbook annotation")
            elif m.group(1) not in headings:
                errors.append(f"{path}: alert {name} links to #{m.group(1)}, which runbooks.md lacks")
    for e in errors:
        print(f"::error::{e}")
    if errors:
        sys.exit(1)
    print("every alert links to a runbook heading.")


def main():
    if len(sys.argv) > 1:
        check_proposed_labels(sys.argv[1])
    check_runbooks()
    flat = []
    for path in FLAT:
        flat += rules(open(path, encoding="utf-8").read())
    helm = rules(open(HELM, encoding="utf-8").read())
    if flat == helm:
        print(f"monitoring rules in sync ({len(flat)} rules).")
        return
    fa, ha = set(flat), set(helm)
    for r in sorted(fa - ha):
        print(f"::error file={HELM}::missing or different in the Helm PrometheusRule: {r[0]}/{r[1]}")
    for r in sorted(ha - fa):
        print(f"::error file={HELM}::not in the flat rule files: {r[0]}/{r[1]}")
    if fa == ha:
        print(f"::error file={HELM}::same rules, different order")
    sys.exit(1)


if __name__ == "__main__":
    main()
