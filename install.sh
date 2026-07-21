#!/usr/bin/env sh
set -eu

# Install the native MIOSA CLI from GitHub Releases.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | INSTALL_DIR=/usr/local/bin sh
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | MIOSA_CLI_VERSION=1.2.1 sh

REPO="${MIOSA_CLI_REPO:-Miosa-osa/miosa-cli-go}"
INSTALL_DIR="${INSTALL_DIR:-}"
VERSION="${MIOSA_CLI_VERSION:-latest}"

err() {
  printf 'miosa install: %s\n' "$*" >&2
}

need() {
  if ! command -v "$1" >/dev/null 2>&1; then
    err "missing required command: $1"
    exit 1
  fi
}

detect_os() {
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    *) err "unsupported OS: $(uname -s)"; exit 1 ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64' ;;
    arm64 | aarch64) printf 'arm64' ;;
    *) err "unsupported architecture: $(uname -m)"; exit 1 ;;
  esac
}

default_install_dir() {
  if [ "$(id -u)" = "0" ]; then
    printf '/usr/local/bin'
  else
    printf '%s/.local/bin' "$HOME"
  fi
}

download() {
  url="$1"
  out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$out"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url"
  else
    err "missing required command: curl or wget"
    exit 1
  fi
}

verify_checksum() {
  archive="$1"
  checksum_file="$2"
  expected="$(awk -v name="$(basename "$archive")" '$2 == name { print $1; exit }' "$checksum_file")"
  if [ -z "$expected" ]; then
    err "release checksum does not list $(basename "$archive")"
    exit 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive" | awk '{ print $1 }')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive" | awk '{ print $1 }')"
  else
    err "missing required command: sha256sum or shasum"
    exit 1
  fi

  if [ "$actual" != "$expected" ]; then
    err "checksum verification failed for $(basename "$archive")"
    exit 1
  fi
}

latest_version() {
  need sed
  tmp="$1"
  download "https://api.github.com/repos/$REPO/releases/latest" "$tmp"
  tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp" | head -n 1)"
  if [ -z "$tag" ]; then
    err "could not resolve latest release for $REPO"
    exit 1
  fi
  printf '%s' "$tag"
}

asset_version() {
  case "$1" in
    v*) printf '%s' "${1#v}" ;;
    *) printf '%s' "$1" ;;
  esac
}

main() {
  need tar
  need mktemp

  os="$(detect_os)"
  arch="$(detect_arch)"
  install_dir="$INSTALL_DIR"
  if [ -z "$install_dir" ]; then
    install_dir="$(default_install_dir)"
  fi

  workdir="$(mktemp -d)"
  trap 'rm -rf "$workdir"' EXIT INT TERM

  if [ "$VERSION" = "latest" ]; then
    tag="$(latest_version "$workdir/latest.json")"
  else
    tag="$VERSION"
  fi

  version_for_asset="$(asset_version "$tag")"
  asset="miosa_${version_for_asset}_${os}_${arch}.tar.gz"
  url="https://github.com/$REPO/releases/download/$tag/$asset"

  printf 'Installing miosa %s for %s/%s\n' "$tag" "$os" "$arch"
  printf 'Downloading %s\n' "$url"
  download "$url" "$workdir/$asset"
  download "https://github.com/$REPO/releases/download/$tag/checksums.txt" "$workdir/checksums.txt"
  verify_checksum "$workdir/$asset" "$workdir/checksums.txt"

  tar -xzf "$workdir/$asset" -C "$workdir"
  if [ ! -f "$workdir/miosa" ]; then
    err "release archive did not contain a miosa binary"
    exit 1
  fi

  mkdir -p "$install_dir"
  chmod +x "$workdir/miosa"
  mv "$workdir/miosa" "$install_dir/miosa"

  printf 'Installed %s\n' "$install_dir/miosa"
  if ! command -v miosa >/dev/null 2>&1; then
    printf 'Add %s to PATH, then run: miosa version\n' "$install_dir"
  else
    "$install_dir/miosa" version || true
  fi
}

main "$@"
