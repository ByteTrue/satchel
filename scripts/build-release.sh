#!/usr/bin/env bash
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="${1:-0.1.0-dev}"
out="${2:-$root/dist}"
mkdir -p "$out"
out="$(cd "$out" && pwd)"
work="$(mktemp -d "$out/.build.XXXXXX")"
trap 'rm -rf "$work"' EXIT

cd "$root"
license_dir="$work/THIRD_PARTY_LICENSES"
mkdir -p "$license_dir"
cp "$(go env GOROOT)/LICENSE" "$license_dir/Go-LICENSE"
while IFS='|' read -r module dir; do
  [[ -n "$dir" ]] || continue
  safe="${module//\//_}"
  found=0
  for candidate in "$dir"/LICENSE* "$dir"/NOTICE*; do
    if [[ -f "$candidate" ]]; then
      cp "$candidate" "$license_dir/${safe}-$(basename "$candidate")"
      found=1
    fi
  done
  if [[ "$found" -eq 0 ]]; then
    printf 'No LICENSE or NOTICE found for %s in %s\n' "$module" "$dir" >&2
    exit 1
  fi
done < <(go list -m -f '{{if not .Main}}{{.Path}}|{{.Dir}}{{end}}' all)

for target in darwin:amd64 darwin:arm64 linux:amd64 linux:arm64 windows:amd64 windows:arm64; do
  os="${target%%:*}"
  arch="${target##*:}"
  binary="satchel"
  if [[ "$os" == "windows" ]]; then
    binary="satchel.exe"
  fi
  rm -f "$work/satchel" "$work/satchel.exe"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags="-s -w -X satchel/internal/cli.Version=$version" \
    -o "$work/$binary" ./cmd/satchel
  cp README.md THIRD_PARTY_NOTICES.md "$work/"
  if [[ "$os" == "windows" ]]; then
    archive="$out/satchel_${os}_${arch}.zip"
    rm -f "$archive"
    (cd "$work" && zip -q -r "$archive" .)
  else
    archive="$out/satchel_${os}_${arch}.tar.gz"
    tar -czf "$archive" -C "$work" "$binary" README.md THIRD_PARTY_NOTICES.md THIRD_PARTY_LICENSES
  fi
done

cd "$out"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum satchel_*.tar.gz satchel_*.zip > SHA256SUMS
else
  shasum -a 256 satchel_*.tar.gz satchel_*.zip > SHA256SUMS
fi
printf 'Built six release archives in %s\n' "$out"
