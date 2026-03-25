#!/usr/bin/env bash
set -euo pipefail

if [[ "${EUID}" -ne 0 ]]; then
  echo "run as root"
  exit 1
fi

BIN_SRC=""
CFG_SRC=""
VERSION="latest"
SVC_NAME="mink"
REPO="${REPO:-abcdlsj/mink}"
APP_USER="${APP_USER:-mink}"
APP_GROUP="${APP_GROUP:-${APP_USER}}"
APP_HOME="${APP_HOME:-}"

usage() {
  cat <<'EOF'
usage:
  sudo bash deploy/install-systemd.sh
  sudo bash deploy/install-systemd.sh --config /path/to/config.toml
  sudo bash deploy/install-systemd.sh --config /path/to/config.toml --home /opt/mink
  sudo bash deploy/install-systemd.sh --config /path/to/config.toml --version v0.2.11
  sudo bash deploy/install-systemd.sh --binary /path/to/mink --config /path/to/config.toml
  sudo bash deploy/install-systemd.sh --binary /path/to/mink --config /path/to/config.toml --service mink

example:
  sudo bash deploy/install-systemd.sh
  sudo bash deploy/install-systemd.sh --config /root/.mink/config.toml --home /opt/mink
EOF
}

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

prompt_value() {
  local prompt="$1"
  local def="$2"
  local val=""
  if [[ -t 0 ]]; then
    read -r -p "${prompt} [${def}]: " val
  fi
  if [[ -n "${val}" ]]; then
    printf '%s\n' "${val}"
    return
  fi
  printf '%s\n' "${def}"
}

if [[ -z "${APP_HOME}" ]]; then
  APP_HOME="$(prompt_value "install home" "/opt/${SVC_NAME}")"
fi

if [[ -z "${CFG_SRC}" && -f /root/.mink/config.toml ]]; then
  CFG_SRC="/root/.mink/config.toml"
fi

if [[ -z "${CFG_SRC}" ]]; then
  CFG_SRC="$(prompt_value "config path" "/root/.mink/config.toml")"
fi

BIN_DIR="${APP_HOME}/bin"
CFG_DIR="${APP_HOME}/.mink"
BIN_DST="${BIN_DIR}/mink"
CFG_DST="${CFG_DIR}/config.toml"
UNIT_PATH="/etc/systemd/system/${SVC_NAME}.service"
TMP_DIR=""

if [[ ! -f "${CFG_SRC}" ]]; then
  echo "config not found: ${CFG_SRC}"
  exit 1
fi

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

if ! getent group "${APP_GROUP}" >/dev/null 2>&1; then
  groupadd -r "${APP_GROUP}"
fi

if ! id -u "${APP_USER}" >/dev/null 2>&1; then
  useradd -r -g "${APP_GROUP}" -m -d "${APP_HOME}" -s /bin/bash "${APP_USER}"
fi

mkdir -p "${BIN_DIR}" "${CFG_DIR}"
install -m 0755 "${BIN_SRC}" "${BIN_DST}"
install -m 0600 "${CFG_SRC}" "${CFG_DST}"
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
  binary: ${BIN_DST}
  config: ${CFG_DST}
  unit:   ${UNIT_PATH}
  repo:   ${REPO}
  ver:    ${VERSION}

logs:
  journalctl -u ${SVC_NAME} -f
EOF
