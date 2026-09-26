#!/usr/bin/env bash
# gofmt gate: every tracked Go file must be gofmt-clean.
# script/check-fmt.sh — run in CI and before commit.
set -euo pipefail
cd "$(dirname "$0")/.."

# -l lists the files that differ from gofmt output. Empty output is a pass.
dirty="$(gofmt -l . 2>&1)"
if [ -n "$dirty" ]; then
  echo "GOFMT VIOLATION: the following files are not gofmt-clean:"
  echo "$dirty"
  echo "fix with: gofmt -w <file>"
  exit 1
fi
echo "gofmt OK: all Go files are formatted"
