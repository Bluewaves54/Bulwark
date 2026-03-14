#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# build-release.sh — Cross-compile PKGuard binaries for all supported platforms.
#
# Usage:
#   ./scripts/build-release.sh              # build all platforms
#   ./scripts/build-release.sh v1.2.3       # embed version string
#
# Output: dist/<module>-<os>-<arch>[.exe]   (one binary per combination)
#         dist/checksums.txt                  (SHA-256 checksums)
#
# Platforms: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64,
#            windows/amd64, windows/arm64

set -euo pipefail

VERSION="${1:-dev}"
DIST_DIR="dist"
MODULES=(npm-pkguard pypi-pkguard maven-pkguard)
CONFIGS=(config.yaml config-best-practices.yaml)

# OS/arch matrix
PLATFORMS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)

LDFLAGS="-s -w -X main.version=${VERSION}"

rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

echo "=== PKGuard Release Build ==="
echo "Version: ${VERSION}"
echo "Output:  ${DIST_DIR}/"
echo ""

for mod in "${MODULES[@]}"; do
  for platform in "${PLATFORMS[@]}"; do
    os="${platform%/*}"
    arch="${platform#*/}"

    binary="${mod}-${os}-${arch}"
    if [[ "${os}" == "windows" ]]; then
      binary="${binary}.exe"
    fi

    echo "  Building ${binary} ..."
    (
      cd "${mod}" && \
      CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" \
        go build -trimpath -ldflags="${LDFLAGS}" -o "../${DIST_DIR}/${binary}" .
    )
  done

  # Bundle config files alongside binaries.
  for cfg in "${CONFIGS[@]}"; do
    if [[ -f "${mod}/${cfg}" ]]; then
      cp "${mod}/${cfg}" "${DIST_DIR}/${mod}-${cfg}"
    fi
  done
done

echo ""
echo "Generating checksums ..."
(cd "${DIST_DIR}" && shasum -a 256 -- * > checksums.txt)

echo ""
echo "=== Build complete ==="
echo "Files:"
ls -lh "${DIST_DIR}/"
