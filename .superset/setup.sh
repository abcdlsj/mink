#!/usr/bin/env bash
set -euo pipefail

# Bring a fresh Sumi worktree to a buildable state. Cargo and pnpm caches are
# shared across worktrees, so this is fast after the first install.
mise install
mise run install
