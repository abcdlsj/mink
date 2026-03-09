#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/.." && pwd)"
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
esac

MINK_HOME="${MINK_HOME:-$HOME/.mink}"
MINK_LOG_DIR="${MINK_LOG_DIR:-$MINK_HOME}"
MINK_BIN="${MINK_BIN:-$(go env GOPATH)/bin/mink}"
MINK_WORKDIR="${MINK_WORKDIR:-$REPO_ROOT}"
PLIST_SRC="$REPO_ROOT/deploy/com.mink.agent.plist.tpl"
PLIST_DST="$HOME/Library/LaunchAgents/com.mink.agent.plist"
SYSTEMD_SRC="$REPO_ROOT/deploy/mink.service.tpl"
SYSTEMD_DIR="$HOME/.config/systemd/user"
SYSTEMD_DST="$SYSTEMD_DIR/mink.service"
LAUNCHD_LABEL="com.mink.agent"

usage() {
  cat <<USAGE
Usage: $(basename "$0") <command>

Commands:
  install        Build and install daemon service for current platform
  uninstall      Remove daemon service
  start          Start daemon service
  stop           Stop daemon service
  restart        Restart daemon service
  status         Show daemon service status
  reload         Reload daemon config via daemon socket
  devbuild       Rebuild local source and hot-upgrade running daemon
  upgrade        Download latest release and hot-upgrade running daemon
  paths          Print important paths
USAGE
}

need_cmd() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "missing required command: $1" >&2
    exit 1
  }
}

platform() {
  case "$OS" in
    darwin) echo "darwin" ;;
    linux) echo "linux" ;;
    *)
      echo "unsupported platform: $OS" >&2
      exit 1
      ;;
  esac
}

ensure_dirs() {
  mkdir -p "$MINK_HOME" "$MINK_LOG_DIR"
}

build_binary() {
  ensure_dirs
  if [[ -f "$REPO_ROOT/Makefile" ]]; then
    (
      cd "$REPO_ROOT"
      make install
    )
  else
    need_cmd go
    (
      cd "$REPO_ROOT"
      go install ./cmd/mink/
    )
  fi
  if [[ ! -x "$MINK_BIN" ]]; then
    echo "mink binary not found at $MINK_BIN after build" >&2
    exit 1
  fi
}

render_file() {
  local src="$1"
  local dst="$2"
  mkdir -p "$(dirname "$dst")"
  sed \
    -e "s|MINK_BIN|$MINK_BIN|g" \
    -e "s|MINK_LOG_DIR|$MINK_LOG_DIR|g" \
    -e "s|MINK_WORKDIR|$MINK_WORKDIR|g" \
    -e "s|MINK_HOME|$HOME|g" \
    "$src" > "$dst"
}

launchd_bootout() {
  launchctl bootout "gui/$(id -u)" "$PLIST_DST" >/dev/null 2>&1 || true
}

install_darwin() {
  need_cmd launchctl
  build_binary
  render_file "$PLIST_SRC" "$PLIST_DST"
  launchd_bootout
  launchctl bootstrap "gui/$(id -u)" "$PLIST_DST"
  launchctl kickstart -k "gui/$(id -u)/$LAUNCHD_LABEL"
  echo "installed launchd agent: $PLIST_DST"
}

install_linux() {
  need_cmd systemctl
  build_binary
  render_file "$SYSTEMD_SRC" "$SYSTEMD_DST"
  systemctl --user daemon-reload
  systemctl --user enable --now mink.service
  echo "installed systemd user service: $SYSTEMD_DST"
}

uninstall_darwin() {
  need_cmd launchctl
  launchd_bootout
  rm -f "$PLIST_DST"
  echo "removed launchd agent"
}

uninstall_linux() {
  need_cmd systemctl
  systemctl --user disable --now mink.service >/dev/null 2>&1 || true
  rm -f "$SYSTEMD_DST"
  systemctl --user daemon-reload
  echo "removed systemd user service"
}

start_darwin() {
  need_cmd launchctl
  [[ -f "$PLIST_DST" ]] || { echo "launchd plist not installed: $PLIST_DST" >&2; exit 1; }
  launchd_bootout
  launchctl bootstrap "gui/$(id -u)" "$PLIST_DST"
  launchctl kickstart -k "gui/$(id -u)/$LAUNCHD_LABEL"
}

start_linux() {
  need_cmd systemctl
  systemctl --user start mink.service
}

stop_darwin() {
  need_cmd launchctl
  launchd_bootout
}

stop_linux() {
  need_cmd systemctl
  systemctl --user stop mink.service
}

status_darwin() {
  need_cmd launchctl
  launchctl print "gui/$(id -u)/$LAUNCHD_LABEL"
}

status_linux() {
  need_cmd systemctl
  systemctl --user status mink.service --no-pager
}

reload_daemon() {
  "$MINK_BIN" reload
}

restart_service() {
  case "$(platform)" in
    darwin)
      start_darwin
      ;;
    linux)
      need_cmd systemctl
      systemctl --user restart mink.service
      ;;
  esac
}

devbuild_daemon() {
  if "$MINK_BIN" devbuild; then
    return 0
  fi
  echo "daemon not running, starting service instead"
  restart_service
}

upgrade_daemon() {
  "$MINK_BIN" upgrade
}

print_paths() {
  cat <<PATHS
platform=$(platform)
repo_root=$REPO_ROOT
mink_bin=$MINK_BIN
mink_home=$MINK_HOME
mink_log_dir=$MINK_LOG_DIR
mink_workdir=$MINK_WORKDIR
plist_dst=$PLIST_DST
systemd_dst=$SYSTEMD_DST
PATHS
}

main() {
  local cmd="${1:-}"
  case "$cmd" in
    install)
      case "$(platform)" in
        darwin) install_darwin ;;
        linux) install_linux ;;
      esac
      ;;
    uninstall)
      case "$(platform)" in
        darwin) uninstall_darwin ;;
        linux) uninstall_linux ;;
      esac
      ;;
    start)
      case "$(platform)" in
        darwin) start_darwin ;;
        linux) start_linux ;;
      esac
      ;;
    stop)
      case "$(platform)" in
        darwin) stop_darwin ;;
        linux) stop_linux ;;
      esac
      ;;
    restart)
      restart_service
      ;;
    status)
      case "$(platform)" in
        darwin) status_darwin ;;
        linux) status_linux ;;
      esac
      ;;
    reload)
      reload_daemon
      ;;
    devbuild)
      devbuild_daemon
      ;;
    upgrade|update)
      upgrade_daemon
      ;;
    paths)
      print_paths
      ;;
    ""|-h|--help|help)
      usage
      ;;
    *)
      echo "unknown command: $cmd" >&2
      usage
      exit 1
      ;;
  esac
}

main "$@"
