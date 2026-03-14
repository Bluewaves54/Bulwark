#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# E2E tests: real pip / pip3 / uv installs through the PyPI PKGuard.
# Expects PYPI_PROXY_URL to be set (e.g. http://pypi-proxy:8080).
#
# Rule coverage:
#   allow-all baseline          — Tests 1-8
#   min_package_age_days (deny) — Tests 9-10  (unpinned, ranged)
#   min_package_age_days (pass) — Tests 11-12 (exact pin, requirements pin)
#   block_pre_release (deny)    — Test 13 (exact rc version denied)
#   block_pre_release (pass)    — Test 14 (stable version allowed)
#   explicit deny (action:deny) — Test 15 (entire package denied)
#   explicit deny (pass)        — Test 16 (unblocked package still works)
#   global defaults (deny)      — Test 17 (age-blocked by global defaults)
#   global defaults (pass)      — Test 18 (bypass_age_filter exemption)
#   version_patterns deny       — Test 19 (rc version denied by pattern)
#   version_patterns pass       — Test 20 (stable version not matched)
#   real-life config pass       — Tests 21-24 (multi-rule production config)
#   real-life config deny       — Tests 25-27 (multi-rule production config)

set -euo pipefail

PASS=0
FAIL=0
TESTS=0
E2E_PHASE="${E2E_PHASE:-full}"

pass() { PASS=$((PASS + 1)); TESTS=$((TESTS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); TESTS=$((TESTS + 1)); echo "  FAIL: $1"; }
phase_enabled() { [ "${E2E_PHASE}" = "full" ] || [ "${E2E_PHASE}" = "$1" ]; }

echo "============================================"
echo " PyPI Client E2E Tests"
echo " Proxy: ${PYPI_PROXY_URL}"
echo " Phase: ${E2E_PHASE}"
echo "============================================"

# ─── Wait for proxy to be healthy ──────────────────────────────────────────────

echo ""
echo "Waiting for PyPI proxy to become healthy..."
for i in $(seq 1 30); do
    if curl -sf "${PYPI_PROXY_URL}/healthz" > /dev/null 2>&1; then
        echo "Proxy is healthy."
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "FATAL: Proxy did not become healthy within 60s"
        exit 1
    fi
    sleep 2
done

wait_for_proxy() {
    local base_url=$1
    local label=$2
    echo ""
    echo "Waiting for ${label} to become healthy..."
    for i in $(seq 1 30); do
        if curl -sf "${base_url}/healthz" > /dev/null 2>&1; then
            echo "${label} is healthy."
            return 0
        fi
        if [ "$i" -eq 30 ]; then
            echo "FATAL: ${label} did not become healthy within 60s"
            return 1
        fi
        sleep 2
    done
}

# The proxy simple index URL for pip (PyPI Simple API endpoint).
SIMPLE_URL="${PYPI_PROXY_URL}/simple/"
PROXY_HOST=$(echo "${PYPI_PROXY_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
PIP_TRUST_ARGS=(--trusted-host "${PROXY_HOST}")

if phase_enabled "baseline"; then

# ─── Test 1: pip install a well-known package ─────────────────────────────────

echo ""
echo "--- Test: pip install certifi ---"
if pip install --index-url "${SIMPLE_URL}" "${PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-certifi certifi 2>&1; then
    if python -c "import sys; sys.path.insert(0, '/tmp/pip-certifi'); import certifi; print(certifi.__version__)" 2>&1; then
        pass "pip install certifi"
    else
        fail "pip install certifi (import failed)"
    fi
else
    fail "pip install certifi (install failed)"
fi

# ─── Test 2: pip3 install a different package ─────────────────────────────────

echo ""
echo "--- Test: pip3 install six ---"
if pip3 install --index-url "${SIMPLE_URL}" "${PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip3-six six 2>&1; then
    if python3 -c "import sys; sys.path.insert(0, '/tmp/pip3-six'); import six; print(six.__version__)" 2>&1; then
        pass "pip3 install six"
    else
        fail "pip3 install six (import failed)"
    fi
else
    fail "pip3 install six (install failed)"
fi

# ─── Test 3: pip install a pinned version ─────────────────────────────────────

echo ""
echo "--- Test: pip install urllib3==2.0.7 (pinned) ---"
if pip install --index-url "${SIMPLE_URL}" "${PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-urllib3 "urllib3==2.0.7" 2>&1; then
    actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-urllib3'); import urllib3; print(urllib3.__version__)" 2>&1)
    if [ "$actual" = "2.0.7" ]; then
        pass "pip install urllib3==2.0.7"
    else
        fail "pip install urllib3==2.0.7 (got version $actual)"
    fi
else
    fail "pip install urllib3==2.0.7 (install failed)"
fi

# ─── Test 4: uv pip install ───────────────────────────────────────────────────

echo ""
echo "--- Test: uv pip install idna ---"
if uv pip install --index-url "${SIMPLE_URL}" --no-deps --target /tmp/uv-idna idna 2>&1; then
    if python -c "import sys; sys.path.insert(0, '/tmp/uv-idna'); import idna; print(idna.__version__)" 2>&1; then
        pass "uv pip install idna"
    else
        fail "uv pip install idna (import failed)"
    fi
else
    fail "uv pip install idna (install failed)"
fi

# ─── Test 5: pip install with requirements file ───────────────────────────────

echo ""
echo "--- Test: pip install from requirements.txt ---"
cat > /tmp/requirements.txt <<EOF
charset-normalizer==3.3.2
EOF
if pip install --index-url "${SIMPLE_URL}" "${PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-reqs -r /tmp/requirements.txt 2>&1; then
    actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-reqs'); import charset_normalizer; print(charset_normalizer.__version__)" 2>&1)
    if [ "$actual" = "3.3.2" ]; then
        pass "pip install from requirements.txt"
    else
        fail "pip install from requirements.txt (got version $actual)"
    fi
else
    fail "pip install from requirements.txt (install failed)"
fi

# ─── Test 6: pip search via JSON API ──────────────────────────────────────────

echo ""
echo "--- Test: PyPI JSON API accessible ---"
status=$(curl -s -o /dev/null -w "%{http_code}" "${PYPI_PROXY_URL}/pypi/pip/json")
if [ "$status" = "200" ]; then
    pass "PyPI JSON API /pypi/pip/json returns 200"
else
    fail "PyPI JSON API /pypi/pip/json returned $status"
fi

# ─── Test 7: pip install a package with dashes/underscores (normalization) ────

echo ""
echo "--- Test: pip install python-dateutil (dash-name normalization) ---"
if pip install --index-url "${SIMPLE_URL}" "${PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-dateutil python-dateutil 2>&1; then
    if python -c "import sys; sys.path.insert(0, '/tmp/pip-dateutil'); import dateutil; print('ok')" 2>&1; then
        pass "pip install python-dateutil"
    else
        fail "pip install python-dateutil (import failed)"
    fi
else
    fail "pip install python-dateutil (install failed)"
fi

# ─── Test 8: healthz and metrics endpoints ────────────────────────────────────

echo ""
echo "--- Test: proxy healthz endpoint ---"
status=$(curl -s -o /dev/null -w "%{http_code}" "${PYPI_PROXY_URL}/healthz")
if [ "$status" = "200" ]; then
    pass "healthz returns 200"
else
    fail "healthz returned $status"
fi

echo ""
echo "--- Test: proxy metrics endpoint ---"
metrics=$(curl -sf "${PYPI_PROXY_URL}/metrics" 2>&1)
if echo "$metrics" | grep -q "requests_total"; then
    pass "metrics endpoint returns request counters"
else
    fail "metrics endpoint missing request counters"
fi
fi

# ─── Tests 9-12: min_package_age_days ─────────────────────────────────────────

AGE_BLOCK_URL="${PYPI_AGE_BLOCK_PROXY_URL:-}"
AGE_PINNED_URL="${PYPI_AGE_PINNED_PROXY_URL:-}"

if phase_enabled "min-age-block" && [ -n "${AGE_BLOCK_URL}" ]; then
    wait_for_proxy "${AGE_BLOCK_URL}" "PyPI age-block proxy"
    BLOCK_SIMPLE_URL="${AGE_BLOCK_URL}/simple/"
    BLOCK_PROXY_HOST=$(echo "${AGE_BLOCK_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    BLOCK_PIP_TRUST_ARGS=(--trusted-host "${BLOCK_PROXY_HOST}")

    echo ""
    echo "--- Test: min-age block denies unpinned urllib3 (unpinned version format) ---"
    if pip install --index-url "${BLOCK_SIMPLE_URL}" "${BLOCK_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-age-block-unpinned urllib3 2>&1; then
        fail "min-age block denies unpinned urllib3 (unexpectedly installed)"
    else
        pass "min-age block denies unpinned urllib3"
    fi

    echo ""
    echo "--- Test: min-age block denies ranged urllib3 spec (range version format) ---"
    if pip install --index-url "${BLOCK_SIMPLE_URL}" "${BLOCK_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-age-block-range "urllib3>=2.0,<3.0" 2>&1; then
        fail "min-age block denies ranged urllib3 spec (unexpectedly installed)"
    else
        pass "min-age block denies ranged urllib3 spec"
    fi
fi

if phase_enabled "min-age-pinned" && [ -n "${AGE_PINNED_URL}" ]; then
    wait_for_proxy "${AGE_PINNED_URL}" "PyPI age-pinned proxy"
    PINNED_SIMPLE_URL="${AGE_PINNED_URL}/simple/"
    PINNED_PROXY_HOST=$(echo "${AGE_PINNED_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    PINNED_PIP_TRUST_ARGS=(--trusted-host "${PINNED_PROXY_HOST}")

    echo ""
    echo "--- Test: min-age + pinned allows exact urllib3==2.0.7 (exact pin bypasses age) ---"
    if pip install --index-url "${PINNED_SIMPLE_URL}" "${PINNED_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-age-pinned-exact "urllib3==2.0.7" 2>&1; then
        actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-age-pinned-exact'); import urllib3; print(urllib3.__version__)" 2>&1)
        if [ "$actual" = "2.0.7" ]; then
            pass "min-age + pinned allows exact urllib3==2.0.7"
        else
            fail "min-age + pinned exact got version $actual"
        fi
    else
        fail "min-age + pinned allows exact urllib3==2.0.7 (install failed)"
    fi

    echo ""
    echo "--- Test: min-age + pinned allows requirements exact pin (requirements file format) ---"
    cat > /tmp/requirements-age-pinned.txt <<EOF
urllib3==2.0.7
EOF
    if pip install --index-url "${PINNED_SIMPLE_URL}" "${PINNED_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-age-pinned-req -r /tmp/requirements-age-pinned.txt 2>&1; then
        pass "min-age + pinned allows requirements exact pin"
    else
        fail "min-age + pinned allows requirements exact pin (install failed)"
    fi
fi

# ─── Tests 13-14: block_pre_release per-package rule ─────────────────────────
# packaging 26.0rc1 is a known pre-release; 24.2 is a known stable release.

PRERELEASE_URL="${PYPI_BLOCK_PRERELEASE_PROXY_URL:-}"

if phase_enabled "prerelease" && [ -n "${PRERELEASE_URL}" ]; then
    wait_for_proxy "${PRERELEASE_URL}" "PyPI block-prerelease proxy"
    PRE_SIMPLE_URL="${PRERELEASE_URL}/simple/"
    PRE_PROXY_HOST=$(echo "${PRERELEASE_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    PRE_PIP_TRUST_ARGS=(--trusted-host "${PRE_PROXY_HOST}")

    echo ""
    echo "--- Test: block_pre_release denies packaging==26.0rc1 (explicit rc pin) ---"
    # The proxy removes rc versions from the simple index; pip cannot find the dist.
    if pip install --index-url "${PRE_SIMPLE_URL}" "${PRE_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-prerelease-deny "packaging==26.0rc1" 2>&1; then
        fail "block_pre_release denies packaging==26.0rc1 (unexpectedly installed)"
    else
        pass "block_pre_release denies packaging==26.0rc1"
    fi

    echo ""
    echo "--- Test: block_pre_release allows packaging==24.2 (exact stable pin) ---"
    if pip install --index-url "${PRE_SIMPLE_URL}" "${PRE_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-prerelease-pass "packaging==24.2" 2>&1; then
        actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-prerelease-pass'); import packaging; print(packaging.__version__)" 2>&1)
        if [ "$actual" = "24.2" ]; then
            pass "block_pre_release allows packaging==24.2"
        else
            fail "block_pre_release allows packaging==24.2 (got version $actual)"
        fi
    else
        fail "block_pre_release allows packaging==24.2 (install failed)"
    fi
fi

# ─── Tests 15-16: explicit deny rule (action: deny) ──────────────────────────

DENY_URL="${PYPI_EXPLICIT_DENY_PROXY_URL:-}"

if phase_enabled "explicit-deny" && [ -n "${DENY_URL}" ]; then
    wait_for_proxy "${DENY_URL}" "PyPI explicit-deny proxy"
    DENY_SIMPLE_URL="${DENY_URL}/simple/"
    DENY_PROXY_HOST=$(echo "${DENY_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    DENY_PIP_TRUST_ARGS=(--trusted-host "${DENY_PROXY_HOST}")

    echo ""
    echo "--- Test: explicit deny blocks urllib3 entirely (action: deny) ---"
    # The deny rule removes ALL versions from the index; no version can be installed.
    if pip install --index-url "${DENY_SIMPLE_URL}" "${DENY_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-explicit-deny urllib3 2>&1; then
        fail "explicit deny blocks urllib3 (unexpectedly installed)"
    else
        pass "explicit deny blocks urllib3 entirely"
    fi

    echo ""
    echo "--- Test: explicit deny still allows non-blocked package (six) ---"
    if pip install --index-url "${DENY_SIMPLE_URL}" "${DENY_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-explicit-deny-pass six 2>&1; then
        if python -c "import sys; sys.path.insert(0, '/tmp/pip-explicit-deny-pass'); import six; print(six.__version__)" 2>&1; then
            pass "explicit deny still allows non-blocked package (six)"
        else
            fail "explicit deny still allows non-blocked package (six import failed)"
        fi
    else
        fail "explicit deny still allows non-blocked package (six install failed)"
    fi
fi

# ─── Tests 17-18: global defaults (block_pre_releases + min_package_age_days) ─
# urllib3 has bypass_age_filter rule; certifi (no matching rule) gets blocked by global age.

GLOBAL_URL="${PYPI_GLOBAL_DEFAULTS_PROXY_URL:-}"

if phase_enabled "global-defaults" && [ -n "${GLOBAL_URL}" ]; then
    wait_for_proxy "${GLOBAL_URL}" "PyPI global-defaults proxy"
    GLOBAL_SIMPLE_URL="${GLOBAL_URL}/simple/"
    GLOBAL_PROXY_HOST=$(echo "${GLOBAL_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    GLOBAL_PIP_TRUST_ARGS=(--trusted-host "${GLOBAL_PROXY_HOST}")

    echo ""
    echo "--- Test: global defaults block certifi (no-rule package, global age block) ---"
    # certifi has no specific rule, so global min_package_age_days:10000 applies, blocking it.
    if pip install --index-url "${GLOBAL_SIMPLE_URL}" "${GLOBAL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-global-deny certifi 2>&1; then
        fail "global defaults block certifi (unexpectedly installed)"
    else
        pass "global defaults block certifi"
    fi

    echo ""
    echo "--- Test: bypass_age_filter allows urllib3 despite global age block ---"
    # urllib3 has bypass_age_filter:true rule, so it passes through the global age block.
    if pip install --index-url "${GLOBAL_SIMPLE_URL}" "${GLOBAL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-global-bypass "urllib3==2.0.7" 2>&1; then
        actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-global-bypass'); import urllib3; print(urllib3.__version__)" 2>&1)
        if [ "$actual" = "2.0.7" ]; then
            pass "bypass_age_filter allows urllib3 despite global age block"
        else
            fail "bypass_age_filter allows urllib3 (got version $actual)"
        fi
    else
        fail "bypass_age_filter allows urllib3 (install failed)"
    fi
fi

# ─── Tests 19-20: version_patterns deny rule ──────────────────────────────────
# Rule denies any version matching rc\d|a\d+$|b\d+$|\.dev\d
# packaging 26.0rc1 matches rc\d; packaging 24.2 does not match.

VPATTERN_URL="${PYPI_VERSION_PATTERN_PROXY_URL:-}"

if phase_enabled "version-pattern" && [ -n "${VPATTERN_URL}" ]; then
    wait_for_proxy "${VPATTERN_URL}" "PyPI version-pattern proxy"
    VP_SIMPLE_URL="${VPATTERN_URL}/simple/"
    VP_PROXY_HOST=$(echo "${VPATTERN_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    VP_PIP_TRUST_ARGS=(--trusted-host "${VP_PROXY_HOST}")

    echo ""
    echo "--- Test: version_patterns denies packaging==26.0rc1 (rc pattern match) ---"
    if pip install --index-url "${VP_SIMPLE_URL}" "${VP_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-vpattern-deny "packaging==26.0rc1" 2>&1; then
        fail "version_patterns denies packaging==26.0rc1 (unexpectedly installed)"
    else
        pass "version_patterns denies packaging==26.0rc1"
    fi

    echo ""
    echo "--- Test: version_patterns allows packaging==24.2 (no pattern match) ---"
    if pip install --index-url "${VP_SIMPLE_URL}" "${VP_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-vpattern-pass "packaging==24.2" 2>&1; then
        actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-vpattern-pass'); import packaging; print(packaging.__version__)" 2>&1)
        if [ "$actual" = "24.2" ]; then
            pass "version_patterns allows packaging==24.2"
        else
            fail "version_patterns allows packaging==24.2 (got version $actual)"
        fi
    else
        fail "version_patterns allows packaging==24.2 (install failed)"
    fi
fi

# ─── Tests 21-27: real-life production config ─────────────────────────────────
# A single proxy with multiple rules active simultaneously:
#   - trusted_packages: setuptools, pip, wheel (bypass everything)
#   - defaults: min_package_age_days=7, block_pre_releases=true
#   - explicit deny: python3-dateutil (known typosquat)
#   - version_patterns: block dev/alpha/beta builds
#
# Package selection rationale:
#   setuptools       — trusted, bypasses all checks      → should install
#   requests==2.31.0 — old stable, passes age             → should install
#   certifi==2024.8.30— old stable, passes age            → should install
#   six              — old stable, passes everything       → should install
#   python3-dateutil — explicitly denied (typosquat)       → should FAIL
#   flask>=999.0.0   — no such version exists              → should FAIL (range resolves nothing)
#   jinja2==3.1.0a1  — alpha version blocked by pattern    → should FAIL

REAL_LIFE_URL="${PYPI_REAL_LIFE_PROXY_URL:-}"

if phase_enabled "real-life" && [ -n "${REAL_LIFE_URL}" ]; then
    wait_for_proxy "${REAL_LIFE_URL}" "PyPI real-life proxy"

    RL_SIMPLE_URL="${REAL_LIFE_URL}/simple/"
    RL_PROXY_HOST=$(echo "${REAL_LIFE_URL}" | sed -E 's#^https?://([^/:]+).*$#\1#')
    RL_PIP_TRUST_ARGS=(--trusted-host "${RL_PROXY_HOST}")

    echo ""
    echo "--- Test 21: trusted setuptools bypasses all rules (real-life) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-setuptools setuptools 2>&1; then
        if python -c "import sys; sys.path.insert(0, '/tmp/pip-rl-setuptools'); import setuptools; print(setuptools.__version__)" 2>&1; then
            pass "real-life: trusted setuptools installs"
        else
            fail "real-life: trusted setuptools (import failed)"
        fi
    else
        fail "real-life: trusted setuptools (install failed)"
    fi

    echo ""
    echo "--- Test 22: old stable requests==2.31.0 passes age check (real-life) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-requests "requests==2.31.0" 2>&1; then
        actual=$(python -c "import sys; sys.path.insert(0, '/tmp/pip-rl-requests'); from importlib.metadata import version; print(version('requests'))" 2>&1)
        if [ "$actual" = "2.31.0" ]; then
            pass "real-life: requests==2.31.0 installs"
        else
            fail "real-life: requests==2.31.0 (got version $actual)"
        fi
    else
        fail "real-life: requests==2.31.0 (install failed)"
    fi

    echo ""
    echo "--- Test 23: old stable certifi passes age check (real-life) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-certifi certifi 2>&1; then
        if python -c "import sys; sys.path.insert(0, '/tmp/pip-rl-certifi'); import certifi; print(certifi.__version__)" 2>&1; then
            pass "real-life: certifi installs"
        else
            fail "real-life: certifi (import failed)"
        fi
    else
        fail "real-life: certifi (install failed)"
    fi

    echo ""
    echo "--- Test 24: old stable six passes all checks (real-life) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-six six 2>&1; then
        if python -c "import sys; sys.path.insert(0, '/tmp/pip-rl-six'); import six; print(six.__version__)" 2>&1; then
            pass "real-life: six installs"
        else
            fail "real-life: six (import failed)"
        fi
    else
        fail "real-life: six (install failed)"
    fi

    echo ""
    echo "--- Test 25: python3-dateutil blocked by explicit deny (typosquat) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-dateutil-typo python3-dateutil 2>&1; then
        fail "real-life: python3-dateutil (unexpectedly installed)"
    else
        pass "real-life: python3-dateutil blocked (explicit deny)"
    fi

    echo ""
    echo "--- Test 26: pre-release flask blocked by block_pre_releases (real-life) ---"
    # Try to install a pre-release version of flask. pip respects --pre flag.
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-flask-pre --pre "flask==2.0.0rc1" 2>&1; then
        fail "real-life: flask==2.0.0rc1 (unexpectedly installed)"
    else
        pass "real-life: flask==2.0.0rc1 blocked (pre-release)"
    fi

    echo ""
    echo "--- Test 27: dev version of packaging blocked by version pattern (real-life) ---"
    if pip install --index-url "${RL_SIMPLE_URL}" "${RL_PIP_TRUST_ARGS[@]}" --no-deps --target /tmp/pip-rl-pkg-dev "packaging==21.0.dev0" 2>&1; then
        fail "real-life: packaging==21.0.dev0 (unexpectedly installed)"
    else
        pass "real-life: packaging==21.0.dev0 blocked (version pattern)"
    fi
fi

# ─── Summary ──────────────────────────────────────────────────────────────────

echo ""
echo "============================================"
echo " Results: ${PASS} passed, ${FAIL} failed (${TESTS} total)"
echo "============================================"

if [ "$FAIL" -gt 0 ]; then
    exit 1
fi
exit 0
