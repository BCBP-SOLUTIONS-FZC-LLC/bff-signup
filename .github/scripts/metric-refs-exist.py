#!/usr/bin/env python3
"""Fail when an alert, recording rule, adapter rule or dashboard queries a
metric that nothing emits (bff-signup; complements `metricslint refs`, which
only flags names the registry knows as legacy or removed).

A queried name is accepted when it is:
  * emitted by this service (the /metrics scrape of TestStandard_AllCollectorsRegistered),
  * a non-removed registry metric owned by a library this service links
    (e.g. gincommon's platform_http_* server metrics, which only appear in a
    scrape after a real request),
  * a recording-rule output defined in deploy/ (namespace:metric:aggregation), or
  * on EXTERNAL below (emitted by another service or by Kubernetes/Prometheus).

Usage: metric-refs-exist.py <registry.json> <scrape.txt> <owners,csv>
"""
import glob
import json
import re
import sys

EXTERNAL = {
    "up",                                                   # Prometheus scrape health
    "kube_pod_container_ready",                             # kube-state-metrics
    "iam_org_membership_tenant_provisioning_backstop_total",  # O&M backstop (BFF-2)
}
FILES = (
    glob.glob("deploy/monitoring/*.yml") + glob.glob("deploy/monitoring/*.yaml")
    + glob.glob("deploy/helm/templates/prometheusrule.yaml")
    + glob.glob("deploy/monitoring/dashboards/*.json")
)
# PromQL keywords, functions and label names that look like identifiers.
NOT_METRICS = {
    "sum", "rate", "increase", "irate", "by", "without", "on", "ignoring", "group_left",
    "group_right", "and", "or", "unless", "bool", "absent", "histogram_quantile",
    "clamp_min", "clamp_max", "avg", "max", "min", "count", "topk", "bottomk", "offset",
    "vector", "scalar", "time", "le", "label_replace", "sort", "sort_desc", "abs",
}
SUFFIXES = ("_bucket", "_sum", "_count")


def emitted_names(scrape_path):
    names = set()
    for line in open(scrape_path, encoding="utf-8"):
        m = re.match(r"# TYPE (\S+) (\S+)", line)
        if m:
            names.add(m.group(1))
    return names


def exprs(path):
    text = open(path, encoding="utf-8").read()
    if path.endswith(".json"):
        out = []

        def walk(o):
            if isinstance(o, dict):
                for k, v in o.items():
                    if k == "expr" and isinstance(v, str):
                        out.append((1, v))
                    else:
                        walk(v)
            elif isinstance(o, list):
                for v in o:
                    walk(v)
        walk(json.loads(text))
        return out
    out, block, start = [], None, 0
    for n, line in enumerate(text.splitlines(), 1):
        s = line.strip()
        key = re.match(r"(expr|seriesQuery|metricsQuery|matches)\s*:\s*(.*)", s)
        if key:
            if block is not None:
                out.append((start, "\n".join(block)))
            block, start = [key.group(2)], n
            indent = len(line) - len(line.lstrip())
            continue
        if block is not None:
            cur = len(line) - len(line.lstrip())
            if s and cur <= indent:
                out.append((start, "\n".join(block)))
                block = None
            elif not s.startswith("#"):
                block.append(line)
    if block is not None:
        out.append((start, "\n".join(block)))
    return out


def main():
    registry_path, scrape_path, owners = sys.argv[1], sys.argv[2], set(sys.argv[3].split(","))
    owners.add("platform-gincommon")
    registry = json.load(open(registry_path, encoding="utf-8"))
    known = emitted_names(scrape_path) | EXTERNAL
    for m in registry["metrics"]:
        if m.get("owner") in owners and m.get("status") != "removed":
            known.add(m["name"])
    recorded = set()
    for path in FILES:
        recorded |= set(re.findall(r"record:\s*([A-Za-z_:][A-Za-z0-9_:]*)", open(path, encoding="utf-8").read()))

    errors = []
    for path in FILES:
        for line, expr in exprs(path):
            expr = re.sub(r"\{[^}]*\}", "{}", expr)          # drop label matchers
            expr = re.sub(r'"[^"]*"|\'[^\']*\'', '""', expr)  # drop strings
            expr = re.sub(r"\[[^\]]*\]", "[]", expr)          # drop ranges
            expr = re.sub(r"\{\{.*?\}\}", "", expr)           # drop Helm/Go templates
            for name in set(re.findall(r"(?<![A-Za-z0-9_:.$<])([a-z_][a-z0-9_:]*)(?=\s*(?:\{|\[|\)|$|\s|[-+*/<>=!]))", expr)):
                if name in NOT_METRICS or name in recorded or ":" in name:
                    continue
                base = name
                for suf in SUFFIXES:
                    if name.endswith(suf) and name[: -len(suf)] in known:
                        base = name[: -len(suf)]
                if base in known:
                    continue
                if "_" not in name:
                    continue  # PromQL keyword or template variable, not a metric family
                errors.append(f"{path}:{line}: queries {name}, which nothing emits")
    if errors:
        for e in sorted(set(errors)):
            print(f"::error::{e}")
        print(f"\n{len(set(errors))} reference(s) to metrics nothing emits.")
        sys.exit(1)


if __name__ == "__main__":
    main()
