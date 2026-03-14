#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# install.sh — One-click PKGuard installer for macOS and Linux.
#
# Downloads the correct binary for your OS/architecture and runs the built-in
# setup command that installs the proxy, configures your package manager, and
# creates an autostart entry.
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Bluewaves54/PKGuard/main/scripts/install.sh | bash
#   curl -fsSL ... | bash -s -- npm          # install only the npm proxy
#   curl -fsSL ... | bash -s -- pypi maven   # install pypi and maven proxies
#
# Supported ecosystems: npm, pypi, maven
# Supported platforms:  linux/amd64, linux/arm64, darwin/amd64, darwin/arm64

set -euo pipefail

# --- Configuration ---
REPO="Bluewaves54/PKGuard"
API_URL="https://api.github.com/repos/${REPO}/releases/latest"
DOWNLOAD_BASE="https://github.com/${REPO}/releases/download"

# --- Detect platform ---
detect_platform() {
  local os arch
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"

  case "${os}" in
    linux)  os="linux" ;;
    darwin) os="darwin" ;;
    *)
      echo "Error: Unsupported operating system: ${os}" >&2
      echo "This installer supports macOS and Linux. For Windows, use install.ps1." >&2
      exit 1
      ;;
  esac

  case "${arch}" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *)
      echo "Error: Unsupported architecture: ${arch}" >&2
      exit 1
      ;;
  esac

  echo "${os}/${arch}"
}

# --- Get latest release tag ---
get_latest_version() {
  if command -v curl &>/dev/null; then
    curl -fsSL "${API_URL}" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
  elif command -v wget &>/dev/null; then
    wget -qO- "${API_URL}" | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"//;s/".*//'
  else
    echo "Error: curl or wget is required." >&2
    exit 1
  fi
}

# --- Download a file ---
download() {
  local url="$1" dest="$2"
  if command -v curl &>/dev/null; then
    curl -fsSL -o "${dest}" "${url}"
  else
    wget -qO "${dest}" "${url}"
  fi
}

# --- Main ---
main() {
  local ecosystems=("$@")
  if [[ ${#ecosystems[@]} -eq 0 ]]; then
    ecosystems=(npm pypi maven)
  fi

  # Validate ecosystem names.
  for eco in "${ecosystems[@]}"; do
    case "${eco}" in
      npm|pypi|maven) ;;
      *)
        echo "Error: Unknown ecosystem '${eco}'. Valid: npm, pypi, maven" >&2
        exit 1
        ;;
    esac
  done

  local platform
  platform="$(detect_platform)"
  local os="${platform%/*}"
  local arch="${platform#*/}"

  echo "=== PKGuard Installer ==="
  echo "Platform: ${os}/${arch}"
  echo "Ecosystems: ${ecosystems[*]}"
  echo ""

  echo "Fetching latest release version ..."
  local version
  version="$(get_latest_version)"
  if [[ -z "${version}" ]]; then
    echo "Error: Could not determine latest release version." >&2
    echo "Check https://github.com/${REPO}/releases for available versions." >&2
    exit 1
  fi
  echo "Version: ${version}"
  echo ""

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' EXIT

  for eco in "${ecosystems[@]}"; do
    local binary_name="${eco}-pkguard-${os}-${arch}"
    local download_url="${DOWNLOAD_BASE}/${version}/${binary_name}"
    local dest="${tmpdir}/${eco}-pkguard"

    echo "Downloading ${binary_name} ..."
    if ! download "${download_url}" "${dest}"; then
      echo "Error: Failed to download ${download_url}" >&2
      echo "Verify the release exists at https://github.com/${REPO}/releases/tag/${version}" >&2
      exit 1
    fi

    chmod +x "${dest}"

    echo "Running setup for ${eco}-pkguard ..."
    "${dest}" -setup
    echo ""
  done

  echo "=== Installation complete ==="
  echo ""
  echo "Installed proxies will start automatically on login."
  echo ""
  echo "To reconfigure rules, edit the config files in ~/.pkguard/<ecosystem>/config.yaml"
  echo "To uninstall a proxy, run: ~/.pkguard/bin/<ecosystem>-pkguard -uninstall"
}

main "$@"
