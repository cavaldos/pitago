#!/usr/bin/env bash
# Test CI/CD locally before pushing: script/test-cicd.sh
# Mirrors .github/workflows/ci.yml (fmt + layers + vet + test -race + wire +
# build) plus the release.yml cross-compile matrix (build only, no publish).
set -euo pipefail
cd "$(dirname "$0")/.."

step() { echo "==> $*"; }
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"

step "gofmt gate"
bash script/check-fmt.sh

step "layer boundaries"
bash script/check-layers.sh

step "go vet ./..."
go vet ./...

step "go test ./... -race"
go test ./... -race -count=1

step "pi wire contract against the fake pi server"
bash script/test-wire.sh

step "local build"
CGO_ENABLED=0 go build -trimpath \
  -ldflags "-s -w -X main.version=$VERSION" \
  -o /tmp/pitago-cicd-check ./src

step "release cross-compile check"
for target in "linux amd64" "darwin amd64" "darwin arm64" "windows amd64"; do
  set -- $target
  out="/tmp/pitago-cicd-check-$1-$2"
  [[ "$1" == windows ]] && out="$out.exe"
  GOOS="$1" GOARCH="$2" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$out" ./src
  echo "  ok: $1/$2"
done

echo "CICD check passed ($VERSION)"
