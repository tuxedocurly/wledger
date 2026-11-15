#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

GO_BIN="${GO_BIN:-go}"
TMP_BIN="$(mktemp "${TMPDIR:-/tmp}/wledger-verify.XXXXXX")"
trap 'rm -f "$TMP_BIN"' EXIT

echo "==> Running go vet ./..."
"$GO_BIN" vet ./...

echo "==> Building static pure-Go binary (CGO_ENABLED=0)..."
CGO_ENABLED=0 "$GO_BIN" build -trimpath -ldflags="-s -w" -o "$TMP_BIN" ./cmd/server

echo "==> Running pure-Go tests (CGO_ENABLED=0)..."
CGO_ENABLED=0 "$GO_BIN" test -count=1 ./...

if [ -d "website/node_modules" ] && command -v npm >/dev/null 2>&1; then
  echo "==> Building documentation website..."
  (cd website && npm run build)
else
  echo "==> Skipping website build (website/node_modules or npm not present)."
fi

echo "Build verification successful."
