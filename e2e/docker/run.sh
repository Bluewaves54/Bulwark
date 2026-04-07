#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Run Docker-based e2e tests in phases.
# Usage: ./run.sh [--python-only | --node-only | --java-only | --vsx-only] [--cleanup-images] [--cleanup-builder-cache]
#
# Prerequisites: Docker and Docker Compose must be installed.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${SCRIPT_DIR}"

TARGET="all"
CLEANUP_IMAGES=false
CLEANUP_BUILDER_CACHE=false

while [ "$#" -gt 0 ]; do
    case "$1" in
        --python-only) TARGET="python" ;;
        --node-only)   TARGET="node" ;;
        --java-only)   TARGET="java" ;;
        --vsx-only)    TARGET="vsx" ;;
        --cleanup-images) CLEANUP_IMAGES=true ;;
        --cleanup-builder-cache) CLEANUP_BUILDER_CACHE=true ;;
        --cleanup-all)
            CLEANUP_IMAGES=true
            CLEANUP_BUILDER_CACHE=true
            ;;
        all|"") TARGET="all" ;;
        *)
            echo "Usage: $0 [--python-only | --node-only | --java-only | --vsx-only] [--cleanup-images] [--cleanup-builder-cache | --cleanup-all]"
            exit 1
            ;;
    esac
    shift
done

cleanup() {
    docker compose down --remove-orphans 2>/dev/null || true
}

cleanup_artifacts() {
    if [ "${CLEANUP_IMAGES}" = true ]; then
        local compose_project
        compose_project="${COMPOSE_PROJECT_NAME:-$(basename "${SCRIPT_DIR}")}"

        echo ""
        echo "Cleaning up compose-built images for project: ${compose_project}"

        # Remove only images built by this compose project.
        local image_ids
        image_ids=$(docker image ls --filter "label=com.docker.compose.project=${compose_project}" --format "{{.ID}}" | sort -u)
        if [ -n "${image_ids}" ]; then
            # shellcheck disable=SC2086
            docker image rm -f ${image_ids} 2>/dev/null || true
        fi
    fi

    if [ "${CLEANUP_BUILDER_CACHE}" = true ]; then
        echo ""
        echo "Pruning Docker builder cache"
        docker builder prune -f >/dev/null 2>&1 || true
    fi
}

run_phase() {
    local label=$1
    local proxy_service=$2
    local test_service=$3
    shift 3

    echo ""
    echo "--- Phase: ${label} ---"

    cleanup
    docker compose up -d --build "${proxy_service}"
    docker compose run --rm --no-deps "$@" "${test_service}"
}

run_python_phases() {
    docker compose build test-python

    run_phase "PyPI baseline" "pypi-proxy" "test-python" \
        -e E2E_PHASE=baseline \
        -e PYPI_PROXY_URL=http://pypi-proxy:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI min-age deny" "pypi-proxy-age-block" "test-python" \
        -e E2E_PHASE=min-age-block \
        -e PYPI_PROXY_URL=http://pypi-proxy-age-block:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL=http://pypi-proxy-age-block:8080 \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI min-age pinned" "pypi-proxy-age-pinned" "test-python" \
        -e E2E_PHASE=min-age-pinned \
        -e PYPI_PROXY_URL=http://pypi-proxy-age-pinned:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL=http://pypi-proxy-age-pinned:8080 \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI block pre-release" "pypi-proxy-block-prerelease" "test-python" \
        -e E2E_PHASE=prerelease \
        -e PYPI_PROXY_URL=http://pypi-proxy-block-prerelease:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL=http://pypi-proxy-block-prerelease:8080 \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI explicit deny" "pypi-proxy-explicit-deny" "test-python" \
        -e E2E_PHASE=explicit-deny \
        -e PYPI_PROXY_URL=http://pypi-proxy-explicit-deny:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL=http://pypi-proxy-explicit-deny:8080 \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI global defaults" "pypi-proxy-global-defaults" "test-python" \
        -e E2E_PHASE=global-defaults \
        -e PYPI_PROXY_URL=http://pypi-proxy-global-defaults:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL=http://pypi-proxy-global-defaults:8080 \
        -e PYPI_VERSION_PATTERN_PROXY_URL=

    run_phase "PyPI version patterns" "pypi-proxy-version-pattern" "test-python" \
        -e E2E_PHASE=version-pattern \
        -e PYPI_PROXY_URL=http://pypi-proxy-version-pattern:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL=http://pypi-proxy-version-pattern:8080

    run_phase "PyPI real-life" "pypi-proxy-real-life" "test-python" \
        -e E2E_PHASE=real-life \
        -e PYPI_PROXY_URL=http://pypi-proxy-real-life:8080 \
        -e PYPI_AGE_BLOCK_PROXY_URL= \
        -e PYPI_AGE_PINNED_PROXY_URL= \
        -e PYPI_BLOCK_PRERELEASE_PROXY_URL= \
        -e PYPI_EXPLICIT_DENY_PROXY_URL= \
        -e PYPI_GLOBAL_DEFAULTS_PROXY_URL= \
        -e PYPI_VERSION_PATTERN_PROXY_URL= \
        -e PYPI_REAL_LIFE_PROXY_URL=http://pypi-proxy-real-life:8080
}

run_node_phases() {
    docker compose build test-node

    run_phase "npm baseline" "npm-proxy" "test-node" \
        -e E2E_PHASE=baseline \
        -e NPM_PROXY_URL=http://npm-proxy:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm min-age deny" "npm-proxy-age-block" "test-node" \
        -e E2E_PHASE=min-age-block \
        -e NPM_PROXY_URL=http://npm-proxy-age-block:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL=http://npm-proxy-age-block:8080 \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm min-age pinned" "npm-proxy-age-pinned" "test-node" \
        -e E2E_PHASE=min-age-pinned \
        -e NPM_PROXY_URL=http://npm-proxy-age-pinned:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL=http://npm-proxy-age-pinned:8080 \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm block pre-release" "npm-proxy-block-prerelease" "test-node" \
        -e E2E_PHASE=prerelease \
        -e NPM_PROXY_URL=http://npm-proxy-block-prerelease:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL=http://npm-proxy-block-prerelease:8080 \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm explicit deny" "npm-proxy-explicit-deny" "test-node" \
        -e E2E_PHASE=explicit-deny \
        -e NPM_PROXY_URL=http://npm-proxy-explicit-deny:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL=http://npm-proxy-explicit-deny:8080 \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm global defaults" "npm-proxy-global-defaults" "test-node" \
        -e E2E_PHASE=global-defaults \
        -e NPM_PROXY_URL=http://npm-proxy-global-defaults:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL=http://npm-proxy-global-defaults:8080 \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm version patterns" "npm-proxy-version-pattern" "test-node" \
        -e E2E_PHASE=version-pattern \
        -e NPM_PROXY_URL=http://npm-proxy-version-pattern:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL=http://npm-proxy-version-pattern:8080 \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=

    run_phase "npm install scripts" "npm-proxy-install-scripts" "test-node" \
        -e E2E_PHASE=install-scripts \
        -e NPM_PROXY_URL=http://npm-proxy-install-scripts:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL=http://npm-proxy-install-scripts:8080

    run_phase "npm trusted packages" "npm-proxy-trusted-packages" "test-node" \
        -e E2E_PHASE=trusted-packages \
        -e NPM_PROXY_URL=http://npm-proxy-trusted-packages:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL= \
        -e NPM_TRUSTED_PACKAGES_PROXY_URL=http://npm-proxy-trusted-packages:8080

    run_phase "npm real-life" "npm-proxy-real-life" "test-node" \
        -e E2E_PHASE=real-life \
        -e NPM_PROXY_URL=http://npm-proxy-real-life:8080 \
        -e NPM_AGE_BLOCK_PROXY_URL= \
        -e NPM_AGE_PINNED_PROXY_URL= \
        -e NPM_BLOCK_PRERELEASE_PROXY_URL= \
        -e NPM_EXPLICIT_DENY_PROXY_URL= \
        -e NPM_GLOBAL_DEFAULTS_PROXY_URL= \
        -e NPM_VERSION_PATTERN_PROXY_URL= \
        -e NPM_INSTALL_SCRIPTS_PROXY_URL= \
        -e NPM_TRUSTED_PACKAGES_PROXY_URL= \
        -e NPM_REAL_LIFE_PROXY_URL=http://npm-proxy-real-life:8080
}

run_java_phases() {
    docker compose build test-java

    run_phase "Maven baseline" "maven-proxy" "test-java" \
        -e E2E_PHASE=baseline \
        -e MAVEN_PROXY_URL=http://maven-proxy:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven min-age deny" "maven-proxy-age-block" "test-java" \
        -e E2E_PHASE=min-age-block \
        -e MAVEN_PROXY_URL=http://maven-proxy-age-block:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL=http://maven-proxy-age-block:8080 \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven min-age pinned" "maven-proxy-age-pinned" "test-java" \
        -e E2E_PHASE=min-age-pinned \
        -e MAVEN_PROXY_URL=http://maven-proxy-age-pinned:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL=http://maven-proxy-age-pinned:8080 \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven block snapshots" "maven-proxy-block-snapshots" "test-java" \
        -e E2E_PHASE=block-snapshots \
        -e MAVEN_PROXY_URL=http://maven-proxy-block-snapshots:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL=http://maven-proxy-block-snapshots:8080 \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven block pre-release" "maven-proxy-block-prerelease" "test-java" \
        -e E2E_PHASE=prerelease \
        -e MAVEN_PROXY_URL=http://maven-proxy-block-prerelease:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL=http://maven-proxy-block-prerelease:8080 \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven explicit deny" "maven-proxy-explicit-deny" "test-java" \
        -e E2E_PHASE=explicit-deny \
        -e MAVEN_PROXY_URL=http://maven-proxy-explicit-deny:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL=http://maven-proxy-explicit-deny:8080 \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven global defaults" "maven-proxy-global-defaults" "test-java" \
        -e E2E_PHASE=global-defaults \
        -e MAVEN_PROXY_URL=http://maven-proxy-global-defaults:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL=http://maven-proxy-global-defaults:8080 \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=

    run_phase "Maven version patterns" "maven-proxy-version-pattern" "test-java" \
        -e E2E_PHASE=version-pattern \
        -e MAVEN_PROXY_URL=http://maven-proxy-version-pattern:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL=http://maven-proxy-version-pattern:8080

    run_phase "Maven real-life" "maven-proxy-real-life" "test-java" \
        -e E2E_PHASE=real-life \
        -e MAVEN_PROXY_URL=http://maven-proxy-real-life:8080 \
        -e MAVEN_AGE_BLOCK_PROXY_URL= \
        -e MAVEN_AGE_PINNED_PROXY_URL= \
        -e MAVEN_BLOCK_SNAPSHOTS_PROXY_URL= \
        -e MAVEN_BLOCK_PRERELEASE_PROXY_URL= \
        -e MAVEN_EXPLICIT_DENY_PROXY_URL= \
        -e MAVEN_GLOBAL_DEFAULTS_PROXY_URL= \
        -e MAVEN_VERSION_PATTERN_PROXY_URL= \
        -e MAVEN_REAL_LIFE_PROXY_URL=http://maven-proxy-real-life:8080
}

run_vsx_phases() {
    echo ""
    echo "--- Pre-building VSX test client image ---"
    docker compose build test-vsx

    run_phase "VSX baseline" "vsx-proxy" "test-vsx" \
        -e E2E_PHASE=baseline \
        -e VSX_PROXY_URL=http://vsx-proxy:18003 \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL=

    run_phase "VSX explicit-deny" "vsx-proxy-explicit-deny" "test-vsx" \
        -e E2E_PHASE=explicit-deny \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL=http://vsx-proxy-explicit-deny:18003 \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL=

    run_phase "VSX prerelease" "vsx-proxy-block-prerelease" "test-vsx" \
        -e E2E_PHASE=prerelease \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL=http://vsx-proxy-block-prerelease:18003 \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL=

    run_phase "VSX prerelease-pkg" "vsx-proxy-block-prerelease-pkg" "test-vsx" \
        -e E2E_PHASE=prerelease-pkg \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL=http://vsx-proxy-block-prerelease-pkg:18003 \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL=

    run_phase "VSX global-defaults" "vsx-proxy-global-defaults" "test-vsx" \
        -e E2E_PHASE=global-defaults \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL=http://vsx-proxy-global-defaults:18003 \
        -e VSX_PROXY_REAL_LIFE_URL=

    run_phase "VSX real-life" "vsx-proxy-real-life" "test-vsx" \
        -e E2E_PHASE=real-life \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL=http://vsx-proxy-real-life:18003 \
        -e VSX_PROXY_BEST_PRACTICES_URL=

    run_phase "VSX best-practices" "vsx-proxy-best-practices" "test-vsx" \
        -e E2E_PHASE=best-practices \
        -e VSX_PROXY_URL= \
        -e VSX_PROXY_DENY_URL= \
        -e VSX_PROXY_PRERELEASE_URL= \
        -e VSX_PROXY_PRERELEASE_PKG_URL= \
        -e VSX_PROXY_DEFAULTS_URL= \
        -e VSX_PROXY_REAL_LIFE_URL= \
        -e VSX_PROXY_BEST_PRACTICES_URL=http://vsx-proxy-best-practices:18003
}

echo "============================================"
echo " Bulwark E2E Tests (Docker)"
echo "============================================"
echo ""

trap cleanup EXIT

set +e
case "${TARGET}" in
    python) run_python_phases ;;
    node)   run_node_phases ;;
    java)   run_java_phases ;;
    vsx)    run_vsx_phases ;;
    all)
        run_python_phases
        run_node_phases
        run_java_phases
        run_vsx_phases
        ;;
esac
EXIT_CODE=$?
set -e

if [ $EXIT_CODE -eq 0 ]; then
    echo ""
    echo "All e2e tests passed."
else
    echo ""
    echo "Some e2e tests failed (exit code: $EXIT_CODE)."
fi

cleanup_artifacts

exit $EXIT_CODE
