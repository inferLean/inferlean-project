#!/usr/bin/env sh
set -eu

REPO="${INFERLEAN_REPO:-inferLean/inferlean-project}"
REQUESTED_VERSION="${1:-${INFERLEAN_VERSION:-latest}}"
INSTALL_DIR="${INFERLEAN_INSTALL_DIR:-}"
TARGET_BINARY_NAME="inferlean"

log() {
  printf '%s\n' "$*" >&2
}

fail() {
  log "error: $*"
  exit 1
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || fail "missing required command: $1"
}

detect_os() {
  case "$(uname -s)" in
    Darwin) printf 'darwin' ;;
    Linux) printf 'linux' ;;
    *) fail "unsupported OS: $(uname -s). supported: macOS, Linux" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64 | amd64) printf 'amd64' ;;
    arm64 | aarch64) printf 'arm64' ;;
    *) fail "unsupported architecture: $(uname -m). supported: x86_64/amd64, arm64/aarch64" ;;
  esac
}

has_any_token() {
  file_name="$1"
  shift
  for token in "$@"; do
    case "$file_name" in
      *"$token"*) return 0 ;;
    esac
  done
  return 1
}

install_with_permissions() {
  src="$1"
  dst="$2"
  dst_dir="$(dirname "$dst")"

  if mkdir -p "$dst_dir" 2>/dev/null; then
    install -m 0755 "$src" "$dst"
    return 0
  fi

  require_cmd sudo
  sudo mkdir -p "$dst_dir"
  sudo install -m 0755 "$src" "$dst"
}

require_cmd curl
require_cmd sed
require_cmd grep
require_cmd mktemp
require_cmd tar
require_cmd find
require_cmd install

os="$(detect_os)"
arch="$(detect_arch)"

os_tokens="linux"
if [ "$os" = "darwin" ]; then
  os_tokens="darwin macos osx"
fi

arch_tokens="amd64 x86_64 x64"
if [ "$arch" = "arm64" ]; then
  arch_tokens="arm64 aarch64"
fi

if [ "$REQUESTED_VERSION" = "latest" ]; then
  release_api_url="https://api.github.com/repos/$REPO/releases/latest"
else
  release_api_url="https://api.github.com/repos/$REPO/releases/tags/$REQUESTED_VERSION"
fi

release_json="$(curl -fsSL "$release_api_url")" || fail "could not fetch release metadata from $release_api_url"

asset_urls="$(printf '%s' "$release_json" \
  | grep -o '"browser_download_url":[[:space:]]*"[^"]*"' \
  | sed -E 's/.*"browser_download_url":[[:space:]]*"([^"]*)"/\1/' || true)"

[ -n "$asset_urls" ] || fail "no downloadable assets found for $REPO ($REQUESTED_VERSION)"

asset_url=""
for url in $asset_urls; do
  name="$(basename "$url" | tr '[:upper:]' '[:lower:]')"
  case "$name" in
    *.tar.gz | *.tgz | *.zip) ;;
    *) continue ;;
  esac
  case "$name" in
    *checksum* | *checksums* | *sha256* | *sha-256*) continue ;;
  esac

  os_match=1
  for token in $os_tokens; do
    if has_any_token "$name" "$token"; then
      os_match=0
      break
    fi
  done
  [ "$os_match" -eq 0 ] || continue

  arch_match=1
  for token in $arch_tokens; do
    if has_any_token "$name" "$token"; then
      arch_match=0
      break
    fi
  done
  [ "$arch_match" -eq 0 ] || continue

  asset_url="$url"
  break
done

[ -n "$asset_url" ] || fail "no release asset matched os=$os arch=$arch"

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/inferlean-install.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

asset_name="$(basename "$asset_url")"
asset_path="$tmp_dir/$asset_name"

log "downloading $asset_name"
curl -fsSL "$asset_url" -o "$asset_path"

extract_dir="$tmp_dir/extract"
mkdir -p "$extract_dir"

case "$asset_name" in
  *.tar.gz | *.tgz)
    tar -xzf "$asset_path" -C "$extract_dir"
    ;;
  *.zip)
    if command -v unzip >/dev/null 2>&1; then
      unzip -q "$asset_path" -d "$extract_dir"
    else
      fail "asset is zip but unzip is not installed"
    fi
    ;;
  *)
    fail "unsupported asset format: $asset_name"
    ;;
esac

binary_path="$(find "$extract_dir" -type f \( -name inferlean -o -name inferLean \) | head -n 1)"
[ -n "$binary_path" ] || fail "could not find inferlean binary in release asset"
chmod +x "$binary_path"

if [ -z "$INSTALL_DIR" ]; then
  if [ -w "/usr/local/bin" ] || { [ ! -e "/usr/local/bin" ] && [ -w "/usr/local" ]; }; then
    INSTALL_DIR="/usr/local/bin"
  else
    [ -n "${HOME:-}" ] || fail "HOME is not set and /usr/local/bin is not writable"
    INSTALL_DIR="$HOME/.local/bin"
  fi
fi

destination="$INSTALL_DIR/$TARGET_BINARY_NAME"
install_with_permissions "$binary_path" "$destination"

log "installed to $destination"
if ! command -v "$TARGET_BINARY_NAME" >/dev/null 2>&1; then
  case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *)
      log "note: $INSTALL_DIR is not in PATH."
      log "add it with: export PATH=\"$INSTALL_DIR:\$PATH\""
      ;;
  esac
fi

"$destination" --help >/dev/null 2>&1 || true
log "done. run: $TARGET_BINARY_NAME --help"
