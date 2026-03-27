#!/bin/sh
set -eu

owner="mitchell-wallace"
repo="rally"

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
dest_path="${dest_dir}/rally"
tmp_dir="$(mktemp -d)"
archive_path="${tmp_dir}/rally.tar.gz"
asset_url="https://github.com/${owner}/${repo}/releases/latest/download/rally_${os}_${arch}.tar.gz"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$dest_dir"
curl -fsSL "$asset_url" -o "$archive_path"
tar -xzf "$archive_path" -C "$tmp_dir"
install -m 0755 "${tmp_dir}/rally" "$dest_path"
"$dest_path" --version
