#!/usr/bin/env bash
# Build the fake pi RPC server and run the wire-contract tests against it.
# script/test-wire.sh
#
# The two live-pi tests in src/pirpc (TestRPCNoLLM, TestRPCCommands) skip
# whenever pi is missing, so on CI the whole JSONL contract was unverified.
# This script puts tests/fakepi on PI_BIN — the same env var production
# honours — which makes those tests run in CI too, and adds tests/wire,
# which pins the exact payload of every command sender.
set -euo pipefail
cd "$(dirname "$0")/.."

FAKEPI_DIR="${FAKEPI_DIR:-${TMPDIR:-/tmp}/pitago-fakepi}"
mkdir -p "$FAKEPI_DIR"
FAKEPI="$FAKEPI_DIR/pi"

echo "==> building fake pi (stdlib only, no module downloads)"
go build -o "$FAKEPI" ./tests/fakepi

export PI_BIN="$FAKEPI"
# Per-test transcripts are the caller's business; a stale one from a previous
# run would only confuse the assertions.
unset FAKEPI_LOG FAKEPI_FAIL || true

echo "==> wire contract (tests/wire) with PI_BIN=$PI_BIN"
go test ./tests/wire/ -count=1 -v

echo "==> live-pi RPC tests against the fake (src/pirpc no longer skips)"
go test ./src/pirpc/ -count=1 -run 'TestRPCNoLLM|TestRPCCommands'

echo "wire check passed"
