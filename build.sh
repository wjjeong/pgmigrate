#!/usr/bin/env bash
# Windows(amd64) exe와 darwin 바이너리를 dist/ 에 만든다.
set -euo pipefail
cd "$(dirname "$0")"
V="$(git describe --always --dirty 2>/dev/null || echo dev)"
build() { # GOOS GOARCH out
  GOTOOLCHAIN=local CGO_ENABLED=0 GOOS="$1" GOARCH="$2" \
    go build -trimpath -ldflags "-s -w -X main.version=$V" -o "$3" ./cmd/pgmigrate
}
mkdir -p dist
build windows amd64 dist/pgmigrate.exe
build darwin "$(go env GOARCH)" dist/pgmigrate-darwin
echo "built: dist/pgmigrate.exe dist/pgmigrate-darwin ($V)"
