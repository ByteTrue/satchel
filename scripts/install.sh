#!/usr/bin/env bash
set -euo pipefail

repository="${SATCHEL_REPOSITORY:-ByteTrue/satchel}"
version="${SATCHEL_VERSION:-latest}"
install_dir="${SATCHEL_INSTALL_DIR:-$HOME/.local/bin}"
download_base="${SATCHEL_DOWNLOAD_BASE:-}"
work="$(mktemp -d "${TMPDIR:-/tmp}/satchel-install.XXXXXX")"
staging=""
trap 'rm -rf "$work"; if [[ -n "$staging" ]]; then rm -f "$staging"; fi' EXIT

need() {
  command -v "$1" >/dev/null 2>&1 || { printf 'Missing required command: %s\n' "$1" >&2; exit 1; }
}
need curl
need tar
need install

fetch() {
  curl -fsSL --retry 3 --connect-timeout 10 --max-time 300 "$1" -o "$2"
}

if [[ -z "$download_base" ]]; then
  if [[ "$version" == "latest" ]]; then
    latest_url="$(curl -fsSL --retry 3 --connect-timeout 10 --max-time 30 -o /dev/null -w '%{url_effective}' "https://github.com/$repository/releases/latest")"
    version="${latest_url##*/}"
  fi
  case "$version" in
    v*[!A-Za-z0-9._-]*|''|*[!A-Za-z0-9._-]*)
      printf 'Invalid release tag: %s\n' "$version" >&2
      exit 1
      ;;
    v*) ;;
    *) printf 'SATCHEL_VERSION must be a tag such as v0.1.0 or latest\n' >&2; exit 1 ;;
  esac
  download_base="https://github.com/$repository/releases/download/$version"
fi

case "$(uname -s)" in
  Darwin) platform="darwin" ;;
  Linux) platform="linux" ;;
  *) printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) architecture="amd64" ;;
  arm64|aarch64) architecture="arm64" ;;
  *) printf 'Unsupported CPU architecture: %s\n' "$(uname -m)" >&2; exit 1 ;;
esac

asset="satchel_${platform}_${architecture}.tar.gz"
printf 'Downloading %s\n' "$asset"
fetch "$download_base/$asset" "$work/$asset"
fetch "$download_base/SHA256SUMS" "$work/SHA256SUMS"
expected="$(awk -v file="$asset" '$2 == file || $2 == "*" file { print $1; exit }' "$work/SHA256SUMS")"
if [[ ! "$expected" =~ ^[0-9a-fA-F]{64}$ ]]; then
  printf 'SHA256SUMS has no valid entry for %s\n' "$asset" >&2
  exit 1
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$work/$asset" | awk '{print $1}')"
else
  actual="$(shasum -a 256 "$work/$asset" | awk '{print $1}')"
fi
if [[ "$actual" != "$expected" ]]; then
  printf 'Checksum mismatch for %s; refusing to install\n' "$asset" >&2
  exit 1
fi

tar -xzf "$work/$asset" -C "$work" satchel
mkdir -p "$install_dir"
staging="$install_dir/.satchel.tmp.$$"
install -m 0755 "$work/satchel" "$staging"
mv -f "$staging" "$install_dir/satchel"
staging=""
"$install_dir/satchel" version
printf 'Installed satchel to %s\n' "$install_dir/satchel"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) printf "Add it to PATH with:\n  export PATH=\"%s:\$PATH\"\n" "$install_dir" ;;
esac
printf 'Next: satchel setup\n'
