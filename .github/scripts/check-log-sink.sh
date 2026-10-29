#!/usr/bin/env bash
# Enforces the Enterprise Platform Observability Standard requirement that all
# logs, metrics, and traces pass through platform-gincommon only.
#
# Rules checked:
#
#   LOG-1  No direct use of "log" standard-library package outside cmd/.
#          The standard library log package bypasses OTel trace-ID injection.
#
#   LOG-2  No fmt.Print* / fmt.Fprintf(os.Stderr|os.Stdout) outside cmd/server/main.go.
#          The single permitted exception is the pre-logger safety net in main()
#          (the comment "single permitted exception" marks it).
#
#   LOG-3  No third-party logger imports (zap direct, zerolog, logrus, slog).
#          Logs must go through gincommon's logger.NewLogger sink.
#
#   METRICS-1  No prometheus.New* outside internal/adapter/outbound/metrics/.
#              All metrics must be built via gincommon.NewCounterVec /
#              NewHistogramVec / NewSharedHistogramVec (rule 8).
#
#   TRACES-1  No raw otel.SetTracerProvider or trace.NewSpanContext outside
#             cmd/server/main.go (which uses gincommon.InitTracingWithConfig).
#
# Mirrors the pattern used by iam-org-membership.

set -euo pipefail
cd "$(dirname "$0")/../.."

python3 - <<'PYEOF'
import glob, re, sys

errors = []

# ── Collect all non-test Go source files ─────────────────────────────────────
all_go = [
    p for p in glob.glob("**/*.go", recursive=True)
    if not p.endswith("_test.go") and "/vendor/" not in p
]

def is_cmd_main(path):
    return path.startswith("cmd/server/main.go")

def is_cmd(path):
    return path.startswith("cmd/")

def is_metrics_pkg(path):
    return "internal/adapter/outbound/metrics/" in path

# ── LOG-1: standard library "log" package import outside cmd/ ────────────────
for path in all_go:
    if is_cmd(path):
        continue
    text = open(path, encoding="utf-8").read()
    # Match import of bare "log" (not "log/slog" etc. — all are banned)
    if re.search(r'\n\s+"log"\s*\n|\n\s+"log/slog"\s*\n', text):
        errors.append(
            f"{path}: imports standard library 'log' package — "
            f"all logs must pass through gincommon's logger (LOG-1)"
        )

# ── LOG-2: fmt.Print*/fmt.Fprintf outside cmd/server/main.go ─────────────────
FORBIDDEN_FMT = re.compile(
    r'\bfmt\.(Print|Println|Printf|Fprintf|Fprintln)\s*\('
)
for path in all_go:
    text = open(path, encoding="utf-8").read()

    if is_cmd_main(path):
        # In cmd/server/main.go, fmt.Fprintf is ONLY permitted inside the main()
        # function body (the pre-logger safety net). Any fmt.Print* inside run()
        # or other functions must use the gincommon logger instead.
        #
        # Strategy: extract the main() function body (everything between
        # "func main() {" and the next top-level "func ") and allow fmt.* there.
        # Flag any occurrence outside main().
        main_body_match = re.search(r'\nfunc main\(\)[^{]*\{(.*?)\n^func ', text, re.DOTALL | re.MULTILINE)
        main_body_span = main_body_match.span(1) if main_body_match else (0, 0)

        for m in FORBIDDEN_FMT.finditer(text):
            # Allow if the call is within the main() function body.
            if main_body_span[0] <= m.start() <= main_body_span[1]:
                continue
            line_no = text[:m.start()].count("\n") + 1
            errors.append(
                f"{path}:{line_no}: fmt.{m.group(1)} — only the pre-logger "
                f"fmt.Fprintf inside main() is permitted; use log.Error/Info/Warn (LOG-2)"
            )
        continue

    for m in FORBIDDEN_FMT.finditer(text):
        line_no = text[:m.start()].count("\n") + 1
        errors.append(
            f"{path}:{line_no}: fmt.{m.group(1)} — "
            f"all logs must pass through gincommon's logger; never fmt.Print* (LOG-2)"
        )

# ── LOG-3: third-party logger imports ────────────────────────────────────────
BANNED_LOGGERS = [
    (r'"go\.uber\.org/zap"',         "go.uber.org/zap"),
    (r'"github\.com/rs/zerolog',     "github.com/rs/zerolog"),
    (r'"github\.com/sirupsen/logrus"',"github.com/sirupsen/logrus"),
    (r'"log/slog"',                  "log/slog"),
]
for path in all_go:
    text = open(path, encoding="utf-8").read()
    for pattern, name in BANNED_LOGGERS:
        if re.search(pattern, text):
            errors.append(
                f"{path}: imports '{name}' — "
                f"all logs must pass through gincommon's logger (LOG-3)"
            )

# ── METRICS-1: raw prometheus constructors outside metrics package ────────────
PROM_CTOR = re.compile(
    r'\bprometheus\.New(Counter|CounterVec|Gauge|GaugeVec|Histogram|HistogramVec|Summary|SummaryVec)\b'
)
for path in all_go:
    if is_metrics_pkg(path):
        continue
    text = open(path, encoding="utf-8").read()
    for m in PROM_CTOR.finditer(text):
        line_no = text[:m.start()].count("\n") + 1
        errors.append(
            f"{path}:{line_no}: prometheus.New{m.group(1)} — "
            f"all metrics must be built via gincommon.NewCounterVec / "
            f"NewHistogramVec / NewSharedHistogramVec in the metrics package (METRICS-1)"
        )

# ── TRACES-1: raw otel outside cmd/server/main.go ───────────────────────────
OTEL_RAW = re.compile(
    r'\botel\.SetTracerProvider\b|\btrace\.NewSpanContext\b|\botlptrace\b'
)
for path in all_go:
    if is_cmd_main(path):
        continue
    text = open(path, encoding="utf-8").read()
    for m in OTEL_RAW.finditer(text):
        line_no = text[:m.start()].count("\n") + 1
        errors.append(
            f"{path}:{line_no}: raw OpenTelemetry API — "
            f"all tracing must go through gincommon.InitTracingWithConfig "
            f"and gincommon.ObservabilityMiddlewares (TRACES-1)"
        )

if errors:
    for e in errors:
        print(f"::error::{e}")
    print(f"\n{len(errors)} violation(s) — logs/metrics/traces must pass through platform-gincommon only.")
    sys.exit(1)

print("  ✔  All logs, metrics, and traces pass through platform-gincommon (LOG-1/2/3, METRICS-1, TRACES-1)")
PYEOF
