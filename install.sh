#!/bin/sh
set -eu

die() {
	echo "sumi install: $*" >&2
	exit 1
}

fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL "$1" -o "$2"
		return $?
	fi
	if command -v wget >/dev/null 2>&1; then
		wget -qO "$2" "$1"
		return $?
	fi
	die "curl or wget is required"
}

repo=abcdlsj/sumi

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
linux | darwin) ;;
*) die "unsupported os: $os" ;;
esac

arch=$(uname -m)
case "$arch" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported arch: $arch" ;;
esac

asset="sumi_${os}_${arch}.tar.gz"
base="https://github.com/${repo}/releases"
url="${base}/latest/download/${asset}"
sum_url="${base}/latest/download/checksums.txt"

tmp=$(mktemp -d 2>/dev/null || mktemp -d -t sumi)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

fetch "$url" "$tmp/$asset" || die "download failed: $url"
if fetch "$sum_url" "$tmp/checksums.txt" 2>/dev/null; then
	awk -v f="$asset" '$2 == f { print; found = 1 } END { exit !found }' "$tmp/checksums.txt" >"$tmp/checksums.one" || die "missing checksum for $asset"
	if command -v sha256sum >/dev/null 2>&1; then
		(cd "$tmp" && sha256sum -c checksums.one >/dev/null)
	elif command -v shasum >/dev/null 2>&1; then
		(cd "$tmp" && shasum -a 256 -c checksums.one >/dev/null)
	else
		echo "sumi install: warning: sha256sum or shasum not found, skipping checksum" >&2
	fi
fi

tar -xzf "$tmp/$asset" -C "$tmp"
[ -f "$tmp/sumi" ] || die "archive did not contain sumi"

if [ "$os" = "darwin" ] && [ -d /opt/homebrew/bin ] && [ -w /opt/homebrew/bin ]; then
	bin=/opt/homebrew/bin
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
	bin=/usr/local/bin
else
	bin=$HOME/.local/bin
fi

mkdir -p "$bin"
[ -w "$bin" ] || die "$bin is not writable"

cp "$tmp/sumi" "$bin/sumi"
chmod 755 "$bin/sumi"

echo "sumi installed to $bin/sumi"
"$bin/sumi" version || true

case ":$PATH:" in
*":$bin:"*) ;;
*) echo "sumi install: warning: $bin is not in PATH" >&2 ;;
esac
