#!/usr/bin/env bash
# Cross-compiles Yata for each release target, bundles the runtime assets the
# app reads from disk, and zips each into dist/. Also extracts the release notes
# from CHANGELOG.md. The frontend must already be built (static/dashboard.js) —
# the release workflow runs `npm run build` first.
#
#   scripts/package.sh <version>        e.g. scripts/package.sh Beta-20260710
set -euo pipefail

VERSION="${1:?usage: package.sh <version>}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Build identity, shown in Settings -> Updates. The version string alone cannot
# identify a build: the :dev image moves with every merge, so several different
# builds report the same version. YATA_COMMIT lets CI pass the SHA it tested;
# otherwise fall back to the checkout's own HEAD.
COMMIT="${YATA_COMMIT:-$(git rev-parse HEAD 2>/dev/null || echo "")}"
BUILT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VPKG="github.com/Yata-Dash/Yata-Dash/internal/version"
LDFLAGS="-s -w -X ${VPKG}.commit=${COMMIT} -X ${VPKG}.buildDate=${BUILT}"

DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST"

# Release targets — see docs: the Docker image covers linux; these binaries
# cover everyone not using Docker.
TARGETS=(windows/amd64 linux/amd64 linux/arm64 darwin/arm64)

# Everything the app needs beside the binary (it reads these from disk).
ASSETS=(static templates defs test_data.json README.md LICENSE CHANGELOG.md docker-compose.yml)

for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  bin="yata"; [ "$os" = "windows" ] && bin="yata.exe"
  name="yata-${VERSION}-${os}-${arch}"
  stage="$DIST/$name"
  mkdir -p "$stage"
  echo "building $name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -ldflags="$LDFLAGS" -o "$stage/$bin" ./cmd/yata
  for a in "${ASSETS[@]}"; do
    [ -e "$a" ] && cp -r "$a" "$stage/"
  done
  ( cd "$DIST" && zip -qr "$name.zip" "$name" )
  rm -rf "$stage"
done

# Release notes: the version's own changelog section. A release without one
# fails here rather than shipping the Unreleased notes, which keep changing.
# The heading must be the whole line (a stray "## [Beta-…] draft" doesn't count).
# Plain string equality, no regex on the version: it contains '-', which breaks
# naive regex bracket classes.
extract() {
  awk -v h="$1" '
    { line = $0; sub(/\r$/, "", line) }
    line == "## [" h "]" { grab=1; next }
    grab && (substr($0,1,4) == "## [" || substr($0,1,4) == "<!--") { exit }
    grab { print }
  ' CHANGELOG.md
}
NOTES="$DIST/RELEASE_NOTES.md"
{ extract "$VERSION"; } > "$NOTES" || true
if [ ! -s "$NOTES" ]; then
  echo "CHANGELOG.md has no '## [$VERSION]' section: rename Unreleased to it before releasing" >&2
  exit 1
fi

echo "packaged $(ls -1 "$DIST"/*.zip | wc -l) zips:"
ls -1 "$DIST"/*.zip
