#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release_dir="${1:-$root/dist}"
expected_version="${2:-dev}"
if [[ ! -f "$release_dir/SHA256SUMS" ]]; then
  "$root/scripts/build-release.sh" dev "$release_dir"
fi
release_dir="$(cd "$release_dir" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/satchel-installer-test.XXXXXX")"
trap 'rm -rf "$work"' EXIT

SATCHEL_DOWNLOAD_BASE="file://$release_dir" \
SATCHEL_INSTALL_DIR="$work/bin" \
  "$root/scripts/install.sh"
actual_version="$("$work/bin/satchel" version)"
if [[ "$actual_version" != "satchel $expected_version" ]]; then
  printf 'Unexpected installed version: %s (want satchel %s)\n' "$actual_version" "$expected_version" >&2
  exit 1
fi

cp -R "$release_dir" "$work/release"
sed -E 's/^[0-9a-fA-F]{64}/0000000000000000000000000000000000000000000000000000000000000000/' \
  "$work/release/SHA256SUMS" > "$work/release/SHA256SUMS.tmp"
mv "$work/release/SHA256SUMS.tmp" "$work/release/SHA256SUMS"
if SATCHEL_DOWNLOAD_BASE="file://$work/release" \
  SATCHEL_INSTALL_DIR="$work/bad-bin" \
  "$root/scripts/install.sh" >"$work/corrupt.out" 2>&1; then
  printf 'Corrupted checksum unexpectedly installed\n' >&2
  exit 1
fi
grep -q 'Checksum mismatch' "$work/corrupt.out"
test ! -e "$work/bad-bin/satchel"
printf 'Installer success and checksum-rejection checks passed\n'
