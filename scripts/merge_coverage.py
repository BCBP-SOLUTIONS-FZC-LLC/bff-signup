#!/usr/bin/env python3
"""Merge multiple Go coverage profiles, taking the max count per block.

This ensures a block covered in any test suite (unit, integration, contract)
is shown as covered in the merged output sent to the coverage gate.

Usage:
    merge_coverage.py <profile1> [<profile2> ...] > coverage.out

Example (Makefile test-ci target):
    go test -coverprofile=coverage.unit.out ./internal/core/...
    go test -tags integration -coverprofile=coverage.integration.out ./test/integration/...
    python3 scripts/merge_coverage.py coverage.unit.out coverage.integration.out > coverage.out
"""
import sys
from collections import defaultdict

counts: dict[str, int] = defaultdict(int)

for fname in sys.argv[1:]:
    try:
        for line in open(fname):
            line = line.strip()
            if line.startswith("mode:") or not line:
                continue
            # Format: pkg/file.go:startLine.startCol,endLine.endCol numStmts count
            parts = line.rsplit(" ", 2)
            if len(parts) != 3:
                continue
            key = parts[0] + " " + parts[1]
            try:
                n = int(parts[2])
            except ValueError:
                continue
            counts[key] = max(counts[key], n)
    except FileNotFoundError:
        pass  # silently skip missing profiles

print("mode: atomic")
for k, n in sorted(counts.items()):
    print(k, n)
