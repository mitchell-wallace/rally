#!/usr/bin/env bash
set -euo pipefail

# Tool-specific env var name
TOOL_NAME="rally"
ENV_VAR="${TOOL_NAME^^}_VERSION"
REPO="mitchell-wallace/${TOOL_NAME}"

# Version resolution: positional arg > env var > latest
VERSION="${1:-${!ENV_VAR:-}}"

if [ -z "${VERSION}" ]; then
  # Fetch latest release tag from GitHub API
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep '"tag_name"' | sed 's/.*"v\?\([^"]*\)".*/\1/')"
fi

# Strip leading 'v' if present for consistency
VERSION="${VERSION#v}"

os_name="$(uname -s)"
arch_name="$(uname -m)"

case "$os_name" in
  Linux) os="linux" ;;
  Darwin) os="darwin" ;;
  *)
    echo "unsupported OS: $os_name" >&2
    exit 1
    ;;
esac

case "$arch_name" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *)
    echo "unsupported architecture: $arch_name" >&2
    exit 1
    ;;
esac

dest_dir="${HOME}/.local/bin"
dest_path="${dest_dir}/${TOOL_NAME}"
tmp_dir="$(mktemp -d)"
archive_path="${tmp_dir}/${TOOL_NAME}.tar.gz"
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/v${VERSION}/${TOOL_NAME}_${os}_${arch}.tar.gz"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

echo "Installing ${TOOL_NAME} v${VERSION}..."

mkdir -p "$dest_dir"
curl -fsSL "$DOWNLOAD_URL" -o "$archive_path"
tar -xzf "$archive_path" -C "$tmp_dir"
install -m 0755 "${tmp_dir}/${TOOL_NAME}" "$dest_path"
if "$dest_path" version >/dev/null 2>&1; then
  "$dest_path" version
else
  "$dest_path" --version
fi
