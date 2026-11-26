#!/usr/bin/env bash
# One command starts both the Go API and the Vite dev server.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/env.sh
source "$ROOT/scripts/env.sh"
setup_node
setup_pnpm
install_web

mkdir -p "$ROOT/.cache/bin" "$ROOT/.cache/go-build" "$ROOT/.cache/tmp" "$ROOT/data"
export GOCACHE="${GOCACHE:-$ROOT/.cache/go-build}"
export GOTMPDIR="${GOTMPDIR:-$ROOT/.cache/tmp}"
export GOMODCACHE="${GOMODCACHE:-$ROOT/.cache/gomod}"

if curl -fsS --max-time 1 http://127.0.0.1:8080/health >/dev/null 2>&1; then
  echo "error: port 8080 is already serving /health; stop the other process first" >&2
  exit 1
fi

echo "[build] compiling Go backend"
(
  cd "$ROOT"
  go build -o "$ROOT/.cache/bin/server" ./cmd/server
)

cleanup() {
  local code=$?
  trap - EXIT INT TERM
  if [ -n "${BACK_PID:-}" ]; then kill "$BACK_PID" 2>/dev/null || true; fi
  if [ -n "${FRONT_PID:-}" ]; then kill "$FRONT_PID" 2>/dev/null || true; fi
  wait 2>/dev/null || true
  exit "$code"
}
trap cleanup EXIT INT TERM

echo "[start] backend  http://127.0.0.1:8080"
(
  cd "$ROOT"
  exec "$ROOT/.cache/bin/server"
) &
BACK_PID=$!

for _ in $(seq 1 50); do
  if curl -fsS --max-time 1 http://127.0.0.1:8080/health >/dev/null 2>&1; then break; fi
  if ! kill -0 "$BACK_PID" 2>/dev/null; then
    echo "error: backend exited during startup" >&2
    exit 1
  fi
  sleep 0.1
done

echo "[start] frontend http://127.0.0.1:5173"
(
  cd "$ROOT/web"
  if [ "$PNPM_KIND" = "binary" ]; then
    exec "$PNPM_CMD" --dir "$ROOT/web" run dev
  else
    exec "$NODE_BIN" "$PNPM_CMD" --dir "$ROOT/web" run dev
  fi
) &
FRONT_PID=$!

echo "[ready] open http://127.0.0.1:5173  (Ctrl-C stops both)"
wait
