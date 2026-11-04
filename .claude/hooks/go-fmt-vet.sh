#!/usr/bin/env bash
# PostToolUse: gofmt + go vet the package of an edited .go file.
# Exit 2 sends stderr back to Claude so it fixes the problem immediately.
set -uo pipefail
f=$(jq -r '.tool_input.file_path // empty')
[[ "$f" == *.go && -f "$f" ]] || exit 0
root="${CLAUDE_PROJECT_DIR:-$(pwd)}"
case "$f" in "$root"/*) ;; *) exit 0 ;; esac

gofmt -w "$f"

pkg="./$(dirname "${f#$root/}")"
if ! out=$(cd "$root" && go vet -tags=integration,e2e "$pkg" 2>&1); then
  echo "go vet failed for $pkg:" >&2
  echo "$out" | head -30 >&2
  exit 2
fi
exit 0
