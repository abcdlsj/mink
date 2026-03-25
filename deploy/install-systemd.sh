#!/usr/bin/env bash
set -euo pipefail

BIN_SRC=""
CFG_SRC=""
VERSION="latest"
SVC_NAME="mink"
REPO="${REPO:-abcdlsj/mink}"
APP_USER="${APP_USER:-mink}"
APP_GROUP="${APP_GROUP:-${APP_USER}}"
APP_HOME="${APP_HOME:-/opt/mink}"
MODE="install"

usage() {
  cat <<'EOF'
usage:
  sudo bash deploy/install-systemd.sh install [options]
  sudo bash deploy/install-systemd.sh upgrade [options]
  sudo bash deploy/install-systemd.sh [options]

example:
  sudo bash deploy/install-systemd.sh install --config /path/to/config.toml
  sudo bash deploy/install-systemd.sh upgrade --version v0.2.11

notes:
  - default mode: install
  - default home: /opt/mink
  - install mode needs --config if /opt/mink/.mink/config.toml and /root/.mink/config.toml are both missing
  - upgrade mode only updates binary and restarts service; it does not overwrite config
EOF
}

if [[ $# -gt 0 ]]; then
  case "$1" in
    install|upgrade)
      MODE="$1"
      shift
      ;;
  esac
fi

while [[ $# -gt 0 ]]; do
  case "$1" in
    --binary)
      BIN_SRC="${2:-}"
      shift 2
      ;;
    --config)
      CFG_SRC="${2:-}"
      shift 2
      ;;
    --version)
      VERSION="${2:-}"
      shift 2
      ;;
    --service)
      SVC_NAME="${2:-}"
      shift 2
      ;;
    --home)
      APP_HOME="${2:-}"
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown arg: $1"
      usage
      exit 1
      ;;
  esac
done

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root"
  exit 1
fi

BIN_DIR="${APP_HOME}/bin"
CFG_DIR="${APP_HOME}/.mink"
BIN_DST="${BIN_DIR}/mink"
CFG_DST="${CFG_DIR}/config.toml"
UNIT_PATH="/etc/systemd/system/${SVC_NAME}.service"
TMP_DIR=""

cleanup() {
  if [[ -n "${TMP_DIR}" && -d "${TMP_DIR}" ]]; then
    rm -rf "${TMP_DIR}"
  fi
}

trap cleanup EXIT

norm_arch() {
  case "$1" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "unsupported arch: $1"
      exit 1
      ;;
  esac
}

norm_os() {
  case "$1" in
    Linux|linux) echo "linux" ;;
    *)
      echo "unsupported os: $1"
      exit 1
      ;;
  esac
}

download() {
  local url="$1"
  local out="$2"
  if command -v wget >/dev/null 2>&1; then
    wget -O "${out}" "${url}"
    return
  fi
  if command -v curl >/dev/null 2>&1; then
    curl -fL "${url}" -o "${out}"
    return
  fi
  echo "need wget or curl"
  exit 1
}

same_file() {
  local a="$1"
  local b="$2"
  [[ -e "${a}" && -e "${b}" ]] || return 1
  [[ "$(realpath "${a}")" == "$(realpath "${b}")" ]]
}

if [[ -z "${BIN_SRC}" ]]; then
  os="$(norm_os "$(uname -s)")"
  arch="$(norm_arch "$(uname -m)")"
  asset="mink-${os}-${arch}"
  if [[ "${VERSION}" == "latest" ]]; then
    url="https://github.com/${REPO}/releases/latest/download/${asset}"
  else
    url="https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
  fi
  TMP_DIR="$(mktemp -d)"
  BIN_SRC="${TMP_DIR}/mink"
  echo "downloading ${url}"
  download "${url}" "${BIN_SRC}"
fi

if [[ ! -f "${BIN_SRC}" ]]; then
  echo "binary not found: ${BIN_SRC}"
  exit 1
fi

if [[ "${MODE}" == "install" ]]; then
  if [[ "${APP_HOME}" != /* ]]; then
    echo "invalid --home: must be an absolute path"
    exit 1
  fi
  case "${APP_HOME}" in
    /|/root)
      echo "refuse unsafe --home: ${APP_HOME}"
      echo "use a dedicated app directory, e.g. /opt/mink"
      exit 1
      ;;
  esac

  if [[ -z "${CFG_SRC}" && -f "${CFG_DST}" ]]; then
    CFG_SRC="${CFG_DST}"
  fi
  if [[ -z "${CFG_SRC}" && -f /root/.mink/config.toml ]]; then
    CFG_SRC="/root/.mink/config.toml"
  fi
  if [[ -z "${CFG_SRC}" || ! -f "${CFG_SRC}" ]]; then
    echo "config not found. provide --config /path/to/config.toml"
    exit 1
  fi

  if ! getent group "${APP_GROUP}" >/dev/null 2>&1; then
    groupadd -r "${APP_GROUP}"
  fi

  if ! id -u "${APP_USER}" >/dev/null 2>&1; then
    useradd -r -g "${APP_GROUP}" -m -d "${APP_HOME}" -s /bin/bash "${APP_USER}"
  fi

  mkdir -p "${BIN_DIR}" "${CFG_DIR}"
  if ! same_file "${BIN_SRC}" "${BIN_DST}"; then
    install -m 0755 "${BIN_SRC}" "${BIN_DST}"
  fi
  if ! same_file "${CFG_SRC}" "${CFG_DST}"; then
    install -m 0600 "${CFG_SRC}" "${CFG_DST}"
  fi
  chown -R "${APP_USER}:${APP_GROUP}" "${APP_HOME}"

  cat >"${UNIT_PATH}" <<EOF
[Unit]
Description=Mink AI Agent (Telegram)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER}
Group=${APP_GROUP}
WorkingDirectory=${APP_HOME}
Environment=HOME=${APP_HOME}
Environment=MINK_LOG_LEVEL=info
ExecStart=${BIN_DST} tg
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

  systemctl daemon-reload
  systemctl enable "${SVC_NAME}"
  systemctl restart "${SVC_NAME}"
  systemctl status "${SVC_NAME}" --no-pager

  cat <<EOF
installed:
  mode:   install
  binary: ${BIN_DST}
  config: ${CFG_DST}
  unit:   ${UNIT_PATH}
  repo:   ${REPO}
  ver:    ${VERSION}

logs:
  journalctl -u ${SVC_NAME} -f
EOF
  exit 0
fi

if [[ "${MODE}" == "upgrade" ]]; then
  if [[ ! -f "${UNIT_PATH}" ]]; then
    echo "service unit not found: ${UNIT_PATH}"
    echo "run install mode first"
    exit 1
  fi

  UNIT_BIN="$(awk -F= '/^ExecStart=/{print $2; exit}' "${UNIT_PATH}" | awk '{print $1}')"
  if [[ -n "${UNIT_BIN}" ]]; then
    BIN_DST="${UNIT_BIN}"
  fi
  BIN_DIR="$(dirname "${BIN_DST}")"
  mkdir -p "${BIN_DIR}"
  if ! same_file "${BIN_SRC}" "${BIN_DST}"; then
    install -m 0755 "${BIN_SRC}" "${BIN_DST}"
  fi

  if id -u "${APP_USER}" >/dev/null 2>&1; then
    chown "${APP_USER}:${APP_GROUP}" "${BIN_DST}" || true
  fi

  systemctl restart "${SVC_NAME}"
  systemctl status "${SVC_NAME}" --no-pager

  cat <<EOF
installed:
  mode:   upgrade
  binary: ${BIN_DST}
  unit:   ${UNIT_PATH}
  repo:   ${REPO}
  ver:    ${VERSION}

logs:
  journalctl -u ${SVC_NAME} -f
EOF
  exit 0
fi

echo "unknown mode: ${MODE}"
usage
exit 1
