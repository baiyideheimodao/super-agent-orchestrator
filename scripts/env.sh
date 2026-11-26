#!/usr/bin/env bash
# Shared tool discovery for local scripts. Supports a normal Node/pnpm install
# and the bundled runtime used by the development harness.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

setup_node() {
  if [ -n "${NODE_BIN:-}" ] && [ -x "${NODE_BIN}" ]; then
    :
  elif command -v node >/dev/null 2>&1; then
    NODE_BIN="$(command -v node)"
  else
    local bundled="$HOME/.dsh/dsh-runtimes/dsh-primary-runtime/dependencies/node/bin/node"
    if [ -x "$bundled" ]; then
      NODE_BIN="$bundled"
    else
      echo "error: Node.js 20+ is required (set NODE_BIN to its path)" >&2
      exit 1
    fi
  fi
  export NODE_BIN
  export PATH="$(dirname "$NODE_BIN"):$PATH"
  export npm_config_cache="${npm_config_cache:-$ROOT/.cache/npm}"
}

setup_pnpm() {
  local bundled="$HOME/.dsh/dsh-runtimes/dsh-primary-runtime/dependencies/pnpm/bin/pnpm.mjs"
  if command -v pnpm >/dev/null 2>&1; then
    PNPM_KIND="binary"
    PNPM_CMD="$(command -v pnpm)"
  elif [ -f "$bundled" ]; then
    PNPM_KIND="script"
    PNPM_CMD="$bundled"
  else
    echo "error: pnpm is required (https://pnpm.io/installation)" >&2
    exit 1
  fi
}

pnpm_web() {
  if [ "$PNPM_KIND" = "binary" ]; then
    "$PNPM_CMD" --dir "$ROOT/web" "$@"
  else
    "$NODE_BIN" "$PNPM_CMD" --dir "$ROOT/web" "$@"
  fi
}

install_web() {
  if [ ! -d "$ROOT/web/node_modules" ]; then
    echo "[setup] installing frontend dependencies"
    if [ "$PNPM_KIND" = "script" ]; then
      pnpm_web install --frozen-lockfile --store-dir "$ROOT/.cache/pnpm-store"
    else
      pnpm_web install --frozen-lockfile
    fi
  fi
}
