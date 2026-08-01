#!/bin/sh
set -eu

case "${1:-}" in
  "")
    mise run db-start
    mise run dev
    ;;
  clean)
    if [ "$#" -ne 1 ]; then
      echo "usage: mise run dev-seed clean" >&2
      exit 2
    fi
    if pgrep -f 'node scripts/dev-seed\.mjs$' >/dev/null 2>&1; then
      echo "dev-seed is running; stop it before cleaning its database and local state" >&2
      exit 1
    fi
    if ! command -v brew >/dev/null 2>&1; then
      echo "dev-seed clean is for macOS with Homebrew; remove the sumi_dev database using your system PostgreSQL tools" >&2
      exit 1
    fi
    brew services start postgresql@17
    pg_bin="$(brew --prefix postgresql@17)/bin"
    "$pg_bin/dropdb" --if-exists --force sumi_dev
    node scripts/dev-seed.mjs clean
    echo "Development seed data was removed. Run 'mise run dev-seed' to recreate it."
    ;;
  *)
    echo "usage: mise run dev-seed [clean]" >&2
    exit 2
    ;;
esac
