#!/usr/bin/env sh
set -eu

# Install the native MIOSA CLI.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | sh
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | INSTALL_DIR=/usr/local/bin sh
#   curl -fsSL https://raw.githubusercontent.com/Miosa-osa/miosa-cli-go/main/install.sh | MIOSA_CLI_VERSION=1.2.0 sh
#
# On a terminal the installer then signs you in ('miosa login' in the browser)
# unless a key is already set up; MIOSA_INSTALL_LOGIN=0 skips that. Without a
# terminal (an agent, CI) it prints the two-step sign-in instead.
#
# Set MIOSA_CLI_DOWNLOAD_BASE (for example https://dl.miosa.ai/cli) to install
# from the MIOSA release origin instead. That path falls back to GitHub if the
# origin is unavailable. GitHub remains the default until the origin is
# accepted.

REPO="${MIOSA_CLI_REPO:-Miosa-osa/miosa-cli-go}"
MIOSA_BASE="${MIOSA_CLI_DOWNLOAD_BASE:-}"
INSTALL_DIR="${INSTALL_DIR:-}"
VERSION="${MIOSA_CLI_VERSION:-latest}"
INSTALL_LOGIN="${MIOSA_INSTALL_LOGIN:-1}"

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
  # Opt-in MIOSA origin: latest.json carries "version".
  if [ -n "$MIOSA_BASE" ] && download "$MIOSA_BASE/latest.json" "$tmp"; then
    tag="$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp" | head -n 1)"
    if [ -n "$tag" ]; then
      printf '%s' "$tag"
      return 0
    fi
  fi
  download "https://api.github.com/repos/$REPO/releases/latest" "$tmp"
  tag="$(sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$tmp" | head -n 1)"
  if [ -z "$tag" ]; then
    err "could not resolve latest release for $REPO"
    exit 1
  fi
  printf '%s' "$tag"
}

miosa_release_base() {
  printf '%s/v%s' "$MIOSA_BASE" "$(asset_version "$1")"
}

github_release_base() {
  printf 'https://github.com/%s/releases/download/v%s' "$REPO" "$(asset_version "$1")"
}

asset_version() {
  case "$1" in
    v*) printf '%s' "${1#v}" ;;
    *) printf '%s' "$1" ;;
  esac
}

release_tag() {
  case "$1" in
    v*) printf '%s' "$1" ;;
    *) printf 'v%s' "$1" ;;
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
  tag="$(release_tag "$tag")"
  asset="miosa_${version_for_asset}_${os}_${arch}.tar.gz"

  printf 'Installing miosa %s for %s/%s\n' "$tag" "$os" "$arch"
  base="$(github_release_base "$tag")"
  if [ -n "$MIOSA_BASE" ]; then
    mbase="$(miosa_release_base "$tag")"
    printf 'Trying %s\n' "$mbase/$asset"
    if download "$mbase/$asset" "$workdir/$asset" && download "$mbase/checksums.txt" "$workdir/checksums.txt"; then
      base="$mbase"
    else
      printf 'MIOSA origin unavailable; using %s\n' "$base"
      download "$base/$asset" "$workdir/$asset"
      download "$base/checksums.txt" "$workdir/checksums.txt"
    fi
  else
    printf 'Downloading %s\n' "$base/$asset"
    download "$base/$asset" "$workdir/$asset"
    download "$base/checksums.txt" "$workdir/checksums.txt"
  fi
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
  sign_in "$install_dir/miosa"
}

# sign_in finishes the install the way a person expects: signed in. It never
# blocks a script: without a terminal it only says how an agent signs in.
sign_in() {
  bin="$1"
  if [ "$INSTALL_LOGIN" = "0" ] || [ -n "${MIOSA_API_KEY:-}" ] || [ -n "${CI:-}" ]; then
    return 0
  fi
  if "$bin" whoami --check >/dev/null 2>&1; then
    printf 'Already signed in.\n'
    return 0
  fi
  if [ -t 1 ] && { : </dev/tty; } 2>/dev/null; then
    printf 'Signing you in...\n'
    "$bin" login </dev/tty || printf 'Sign in later with: miosa login\n'
  else
    printf 'Sign in with: miosa login\n'
    printf 'An agent without a terminal: miosa login --no-wait --json (a person approves the link), then miosa login --wait\n'
  fi
}

main "$@"
