#!/bin/sh
# Stop the design demo and remove its isolated database and runtime state.
# Never touches the main dev environment (3000/5173, sumi_dev).
set -eu

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

DESIGN_DB="${SUMI_DESIGN_DB:-sumi_design_dev}"
DESIGN_SERVER_PORT="${SUMI_DESIGN_SERVER_PORT:-3001}"
DESIGN_WEB_PORT="${SUMI_DESIGN_WEB_PORT:-5174}"
RUNTIME="design-lab/demo/.runtime"
COMPUTER_ROOT="${SUMI_DESIGN_COMPUTER_ROOT:-$HOME/.sumi-design-lab/computer}"

# Stop only processes bound to the design ports.
for port in "$DESIGN_SERVER_PORT" "$DESIGN_WEB_PORT"; do
  pids="$(lsof -ti "tcp:$port" 2>/dev/null || true)"
  if [ -n "$pids" ]; then
    for pid in $pids; do
      kill "$pid" 2>/dev/null || true
    done
  fi
done

if command -v brew >/dev/null 2>&1; then
  pg_bin="$(brew --prefix postgresql@17)/bin"
  "$pg_bin/dropdb" --if-exists --force "$DESIGN_DB" || true
fi

if [ -d "$RUNTIME" ]; then
  target="$(cd "$RUNTIME" && pwd)"
  expected="$REPO_ROOT/design-lab/demo/.runtime"
  case "$target" in
    "$expected"|"$expected"/*) ;;
    *)
      echo "refusing to remove unexpected runtime path: $target" >&2
      exit 1
      ;;
  esac
rm -rf "$target"
fi

if [ -d "$COMPUTER_ROOT" ]; then
  target="$(cd "$COMPUTER_ROOT" && pwd)"
  expected="$HOME/.sumi-design-lab/computer"
  case "$target" in
    "$expected"|"$expected"/*) ;;
    *)
      echo "refusing to remove unexpected computer root: $target" >&2
      exit 1
      ;;
  esac
  rm -rf "$target"
fi

echo "design demo cleaned (db: $DESIGN_DB, runtime: $RUNTIME, computer: $COMPUTER_ROOT)"
