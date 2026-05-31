#!/usr/bin/env bash
set -euo pipefail

# Rally is distributed with its companion work-queue binary, laps. This
# installer fetches both so laps is available as a first-class companion
# alongside rally. Laps remains independently usable.
RALLY_REPO="mitchell-wallace/rally"
LAPS_REPO="mitchell-wallace/laps"

# Version resolution for rally: positional arg > RALLY_VERSION env var > latest.
RALLY_VERSION="${1:-${RALLY_VERSION:-}}"
# Laps tracks its own latest release unless pinned via LAPS_VERSION.
LAPS_VERSION="${LAPS_VERSION:-}"

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
tmp_dir="$(mktemp -d)"

cleanup() {
  rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$dest_dir"

# resolve_version <repo> <requested>: echo a concrete version, falling back to
# the latest GitHub release when none was requested.
resolve_version() {
  local repo="$1" requested="$2" version
  version="$requested"
  if [ -z "$version" ]; then
    version="$(curl -fsSL "https://api.github.com/repos/${repo}/releases/latest" \
      | grep '"tag_name"' | sed 's/.*"v\?\([^"]*\)".*/\1/')"
  fi
  # Strip leading 'v' if present for consistency.
  echo "${version#v}"
}

# install_tool <name> <repo> <version>: download and install a release binary.
install_tool() {
  local name="$1" repo="$2" version="$3"
  local dest_path="${dest_dir}/${name}"
  local archive_path="${tmp_dir}/${name}.tar.gz"
  local url="https://github.com/${repo}/releases/download/v${version}/${name}_${os}_${arch}.tar.gz"

  echo "Installing ${name} v${version}..."
  curl -fsSL "$url" -o "$archive_path"
  tar -xzf "$archive_path" -C "$tmp_dir"
  install -m 0755 "${tmp_dir}/${name}" "$dest_path"
  if "$dest_path" version >/dev/null 2>&1; then
    "$dest_path" version
  else
    "$dest_path" --version
  fi
}

rally_version="$(resolve_version "$RALLY_REPO" "$RALLY_VERSION")"
install_tool "rally" "$RALLY_REPO" "$rally_version"

# Install the bundled laps companion. A laps failure is non-fatal: rally is
# already installed, and laps can be installed independently later.
if laps_version="$(resolve_version "$LAPS_REPO" "$LAPS_VERSION")" && [ -n "$laps_version" ]; then
  install_tool "laps" "$LAPS_REPO" "$laps_version" || \
    echo "warning: could not install laps; run 'rally update' to retry" >&2
else
  echo "warning: could not resolve a laps release; run 'rally update' to retry" >&2
fi
