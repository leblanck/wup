#!/usr/bin/env bash
# Build wup for both macOS architectures, tar them up, and print the
# sha256 sums you paste into the Homebrew formula.
#
#   ./release.sh 0.1.0
#
set -euo pipefail

VERSION="${1:?usage: ./release.sh <version, e.g. 0.1.0>}"
APP=wup

rm -rf dist
mkdir -p dist

for arch in arm64 amd64; do
  echo "building darwin/$arch ..."
  # CGO_ENABLED=1 is REQUIRED on macOS: gopsutil reads CPU times via a cgo
  # call (host_processor_info). Built with cgo off, the CPU metric is stuck
  # at 0. Run this on a Mac — clang cross-compiles both arches natively.
  GOOS=darwin GOARCH="$arch" CGO_ENABLED=1 \
    go build -trimpath -ldflags "-s -w" -o "dist/$APP" .
  tar -czf "dist/${APP}_${VERSION}_darwin_${arch}.tar.gz" -C dist "$APP"
  rm "dist/$APP"
done

echo
echo "tarballs:"
ls -1 dist/*.tar.gz
echo
echo "sha256 (paste into Formula/wup.rb):"
shasum -a 256 dist/*.tar.gz
echo
echo "next: create the GitHub release and upload both tarballs, e.g."
echo "  gh release create v${VERSION} dist/*.tar.gz --title v${VERSION} --notes \"\""