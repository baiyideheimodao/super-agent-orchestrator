#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/env.sh
source "$ROOT/scripts/env.sh"
setup_node
setup_pnpm
install_web

mkdir -p "$ROOT/.cache/go-build" "$ROOT/.cache/tmp"
export GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"
export GOTMPDIR="${GOTMPDIR:-$ROOT/.cache/tmp}"
export GOMODCACHE="${GOMODCACHE:-$ROOT/.cache/gomod}"

cd "$ROOT"
gofmt -l cmd internal | tee /tmp/super-agent-gofmt.out
if [ -s /tmp/super-agent-gofmt.out ]; then
  echo "error: unformatted Go files" >&2
  exit 1
fi

go vet ./...
go test ./...
pnpm_web build
echo "[ok] Go tests and frontend production build passed"
