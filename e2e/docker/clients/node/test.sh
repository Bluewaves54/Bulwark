#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# E2E tests: real npm / yarn / pnpm installs through the npm Bulwark.
# Expects NPM_PROXY_URL to be set (e.g. http://npm-proxy:8080).
#
# Rule coverage:
#   allow-all baseline          — Tests 1-9
#   min_package_age_days (deny) — Tests 10-11  (unpinned, caret)
#   min_package_age_days (pass) — Tests 12-13  (exact pin, manifest exact)
#   block_pre_release (deny)    — Test 14 (explicit rc version denied by packument filter)
#   block_pre_release (pass)    — Test 15 (stable version allowed)
#   explicit deny (action:deny) — Test 16 (entire package denied)
#   explicit deny (pass)        — Test 17 (unblocked package still works)
#   global defaults (deny)      — Test 18 (age-blocked by global defaults)
#   global defaults (pass)      — Test 19 (bypass_age_filter exemption)
#   version_patterns deny       — Test 20 (rc version denied by pattern)
#   version_patterns pass       — Test 21 (stable version not matched)
#   install_scripts deny        — Test 22 (package with postinstall blocked)
#   install_scripts pass        — Test 23 (package without scripts allowed)
#   trusted_packages pass       — Test 24 (trusted @types/ms bypasses all rules)
#   trusted_packages deny       — Test 25 (untrusted ms blocked by rules)
#   real-life config pass       — Tests 26-30 (multi-rule production config)
#   real-life config deny       — Tests 31-33 (multi-rule production config)

set -euo pipefail

PASS=0
FAIL=0
TESTS=0
E2E_PHASE="${E2E_PHASE:-full}"

pass() { PASS=$((PASS + 1)); TESTS=$((TESTS + 1)); echo "  PASS: $1"; }
fail() { FAIL=$((FAIL + 1)); TESTS=$((TESTS + 1)); echo "  FAIL: $1"; }
phase_enabled() { [ "${E2E_PHASE}" = "full" ] || [ "${E2E_PHASE}" = "$1" ]; }

echo "============================================"
echo " npm Client E2E Tests"
echo " Proxy: ${NPM_PROXY_URL}"
echo " Phase: ${E2E_PHASE}"
echo "============================================"

# ─── Wait for proxy to be healthy ──────────────────────────────────────────────

echo ""
echo "Waiting for npm proxy to become healthy..."
for i in $(seq 1 30); do
    if curl -sf "${NPM_PROXY_URL}/healthz" > /dev/null 2>&1; then
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

if phase_enabled "baseline"; then

# ─── Test 1: npm install an unscoped package ──────────────────────────────────

echo ""
echo "--- Test: npm install lodash ---"
mkdir -p /tmp/npm-lodash && cd /tmp/npm-lodash
npm init -y > /dev/null 2>&1
if npm install --registry "${NPM_PROXY_URL}" lodash 2>&1; then
    if node -e "const _ = require('lodash'); console.log(_.VERSION)" 2>&1; then
        pass "npm install lodash"
    else
        fail "npm install lodash (require failed)"
    fi
else
    fail "npm install lodash (install failed)"
fi

# ─── Test 2: npm install a pinned version ─────────────────────────────────────

echo ""
echo "--- Test: npm install ms@2.1.3 (pinned) ---"
mkdir -p /tmp/npm-ms && cd /tmp/npm-ms
npm init -y > /dev/null 2>&1
if npm install --registry "${NPM_PROXY_URL}" ms@2.1.3 2>&1; then
    actual=$(node -e "console.log(require('ms/package.json').version)" 2>&1)
    if [ "$actual" = "2.1.3" ]; then
        pass "npm install ms@2.1.3"
    else
        fail "npm install ms@2.1.3 (got version $actual)"
    fi
else
    fail "npm install ms@2.1.3 (install failed)"
fi

# ─── Test 3: npm install a scoped package ─────────────────────────────────────

echo ""
echo "--- Test: npm install @types/node ---"
mkdir -p /tmp/npm-scoped && cd /tmp/npm-scoped
npm init -y > /dev/null 2>&1
if npm install --registry "${NPM_PROXY_URL}" @types/node 2>&1; then
    if [ -d node_modules/@types/node ]; then
        pass "npm install @types/node"
    else
        fail "npm install @types/node (directory missing)"
    fi
else
    fail "npm install @types/node (install failed)"
fi

# ─── Test 4: yarn install ─────────────────────────────────────────────────────

echo ""
echo "--- Test: yarn add is-odd ---"
mkdir -p /tmp/yarn-isodd && cd /tmp/yarn-isodd
npm init -y > /dev/null 2>&1
if yarn add is-odd --registry "${NPM_PROXY_URL}" 2>&1; then
    if node -e "console.log(require('is-odd')(3))" 2>&1; then
        pass "yarn add is-odd"
    else
        fail "yarn add is-odd (require failed)"
    fi
else
    fail "yarn add is-odd (install failed)"
fi

# ─── Test 5: pnpm install ─────────────────────────────────────────────────────

echo ""
echo "--- Test: pnpm add debug ---"
mkdir -p /tmp/pnpm-debug && cd /tmp/pnpm-debug
npm init -y > /dev/null 2>&1
if pnpm add debug --registry "${NPM_PROXY_URL}" 2>&1; then
    if node -e "require('debug'); console.log('ok')" 2>&1; then
        pass "pnpm add debug"
    else
        fail "pnpm add debug (require failed)"
    fi
else
    fail "pnpm add debug (install failed)"
fi

# ─── Test 6: npm install with package.json dependencies ───────────────────────

echo ""
echo "--- Test: npm install from package.json ---"
mkdir -p /tmp/npm-pkgjson && cd /tmp/npm-pkgjson
cat > package.json <<EOF
{
  "name": "e2e-test",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "ms": "2.1.3",
    "inherits": "2.0.4"
  }
}
EOF
if npm install --registry "${NPM_PROXY_URL}" 2>&1; then
    ok=true
    node -e "require('ms')" 2>/dev/null || ok=false
    node -e "require('inherits')" 2>/dev/null || ok=false
    if [ "$ok" = "true" ]; then
        pass "npm install from package.json"
    else
        fail "npm install from package.json (require failed)"
    fi
else
    fail "npm install from package.json (install failed)"
fi

# ─── Test 7: packument JSON structure ─────────────────────────────────────────

echo ""
echo "--- Test: packument JSON is valid ---"
body=$(curl -sf "${NPM_PROXY_URL}/lodash" 2>&1)
if echo "$body" | node -e "const d=JSON.parse(require('fs').readFileSync(0,'utf8')); if(!d.name||!d.versions) process.exit(1)" 2>&1; then
    pass "packument JSON structure valid"
else
    fail "packument JSON structure invalid"
fi

# ─── Test 8: healthz and metrics endpoints ────────────────────────────────────

echo ""
echo "--- Test: proxy healthz endpoint ---"
status=$(curl -s -o /dev/null -w "%{http_code}" "${NPM_PROXY_URL}/healthz")
if [ "$status" = "200" ]; then
    pass "healthz returns 200"
else
    fail "healthz returned $status"
fi

echo ""
echo "--- Test: proxy metrics endpoint ---"
metrics=$(curl -sf "${NPM_PROXY_URL}/metrics" 2>&1)
if echo "$metrics" | grep -q "requests_total"; then
    pass "metrics endpoint returns request counters"
else
    fail "metrics endpoint missing request counters"
fi
fi

# ─── Tests 10-13: min_package_age_days ────────────────────────────────────────

AGE_BLOCK_URL="${NPM_AGE_BLOCK_PROXY_URL:-}"
AGE_PINNED_URL="${NPM_AGE_PINNED_PROXY_URL:-}"

if phase_enabled "min-age-block" && [ -n "${AGE_BLOCK_URL}" ]; then
    wait_for_proxy "${AGE_BLOCK_URL}" "npm age-block proxy"

    echo ""
    echo "--- Test: min-age block denies unpinned ms (unpinned version format) ---"
    mkdir -p /tmp/npm-age-block-unpinned && cd /tmp/npm-age-block-unpinned
    npm init -y > /dev/null 2>&1
    if npm install --registry "${AGE_BLOCK_URL}" ms 2>&1; then
        fail "min-age block denies unpinned ms (unexpectedly installed)"
    else
        pass "min-age block denies unpinned ms"
    fi

    echo ""
    echo "--- Test: min-age block denies caret pin ms@^2.1.0 (caret range format) ---"
    mkdir -p /tmp/npm-age-block-caret && cd /tmp/npm-age-block-caret
    npm init -y > /dev/null 2>&1
    if npm install --registry "${AGE_BLOCK_URL}" "ms@^2.1.0" 2>&1; then
        fail "min-age block denies caret pin ms@^2.1.0 (unexpectedly installed)"
    else
        pass "min-age block denies caret pin ms@^2.1.0"
    fi
fi

if phase_enabled "min-age-pinned" && [ -n "${AGE_PINNED_URL}" ]; then
    wait_for_proxy "${AGE_PINNED_URL}" "npm age-pinned proxy"

    echo ""
    echo "--- Test: min-age + pinned allows exact ms@2.1.3 (exact pin bypasses age) ---"
    mkdir -p /tmp/npm-age-pinned-exact && cd /tmp/npm-age-pinned-exact
    npm init -y > /dev/null 2>&1
    if npm install --registry "${AGE_PINNED_URL}" ms@2.1.3 2>&1; then
        actual=$(node -e "console.log(require('ms/package.json').version)" 2>&1)
        if [ "$actual" = "2.1.3" ]; then
            pass "min-age + pinned allows exact ms@2.1.3"
        else
            fail "min-age + pinned exact got version $actual"
        fi
    else
        fail "min-age + pinned allows exact ms@2.1.3 (install failed)"
    fi

    echo ""
    echo "--- Test: min-age + pinned allows package.json exact pin (manifest format) ---"
    mkdir -p /tmp/npm-age-pinned-manifest && cd /tmp/npm-age-pinned-manifest
    cat > package.json <<EOF
{
  "name": "e2e-age-pinned",
  "version": "1.0.0",
  "private": true,
  "dependencies": {
    "ms": "2.1.3"
  }
}
EOF
    if npm install --registry "${AGE_PINNED_URL}" 2>&1; then
        pass "min-age + pinned allows package.json exact pin"
    else
        fail "min-age + pinned allows package.json exact pin (install failed)"
    fi
fi

# ─── Tests 14-15: block_pre_release per-package rule ─────────────────────────
# react@19.0.0-rc.1 is a known pre-release; react@18.3.1 is a known stable release.
# The proxy filters the packument: pre-release versions are removed.
# npm install with an exact pre-release spec fails when the version is absent.

PRERELEASE_URL="${NPM_BLOCK_PRERELEASE_PROXY_URL:-}"

if phase_enabled "prerelease" && [ -n "${PRERELEASE_URL}" ]; then
    wait_for_proxy "${PRERELEASE_URL}" "npm block-prerelease proxy"

    echo ""
    echo "--- Test: block_pre_release denies react@19.0.0-rc.1 (explicit rc pin) ---"
    mkdir -p /tmp/npm-prerelease-deny && cd /tmp/npm-prerelease-deny
    npm init -y > /dev/null 2>&1
    # The proxy removes rc versions from the packument; npm cannot resolve the exact version.
    if npm install --registry "${PRERELEASE_URL}" "react@19.0.0-rc.1" 2>&1; then
        fail "block_pre_release denies react@19.0.0-rc.1 (unexpectedly installed)"
    else
        pass "block_pre_release denies react@19.0.0-rc.1"
    fi

    echo ""
    echo "--- Test: block_pre_release allows react@18.3.1 (exact stable pin) ---"
    mkdir -p /tmp/npm-prerelease-pass && cd /tmp/npm-prerelease-pass
    npm init -y > /dev/null 2>&1
    if npm install --registry "${PRERELEASE_URL}" "react@18.3.1" 2>&1; then
        actual=$(node -e "console.log(require('react/package.json').version)" 2>&1)
        if [ "$actual" = "18.3.1" ]; then
            pass "block_pre_release allows react@18.3.1"
        else
            fail "block_pre_release allows react@18.3.1 (got version $actual)"
        fi
    else
        fail "block_pre_release allows react@18.3.1 (install failed)"
    fi
fi

# ─── Tests 16-17: explicit deny rule (action: deny) ──────────────────────────

DENY_URL="${NPM_EXPLICIT_DENY_PROXY_URL:-}"

if phase_enabled "explicit-deny" && [ -n "${DENY_URL}" ]; then
    wait_for_proxy "${DENY_URL}" "npm explicit-deny proxy"

    echo ""
    echo "--- Test: explicit deny blocks ms entirely (action: deny) ---"
    mkdir -p /tmp/npm-explicit-deny && cd /tmp/npm-explicit-deny
    npm init -y > /dev/null 2>&1
    # The deny rule produces an empty packument; npm cannot install any version.
    if npm install --registry "${DENY_URL}" ms 2>&1; then
        fail "explicit deny blocks ms (unexpectedly installed)"
    else
        pass "explicit deny blocks ms entirely"
    fi

    echo ""
    echo "--- Test: explicit deny still allows non-blocked package (lodash) ---"
    mkdir -p /tmp/npm-explicit-deny-pass && cd /tmp/npm-explicit-deny-pass
    npm init -y > /dev/null 2>&1
    if npm install --registry "${DENY_URL}" lodash 2>&1; then
        if node -e "require('lodash'); console.log('ok')" 2>&1; then
            pass "explicit deny still allows non-blocked package (lodash)"
        else
            fail "explicit deny still allows non-blocked package (lodash require failed)"
        fi
    else
        fail "explicit deny still allows non-blocked package (lodash install failed)"
    fi
fi

# ─── Tests 18-19: global defaults (block_pre_releases + min_package_age_days) ─
# ms has bypass_age_filter rule; lodash (no matching rule) gets blocked by global age.

GLOBAL_URL="${NPM_GLOBAL_DEFAULTS_PROXY_URL:-}"

if phase_enabled "global-defaults" && [ -n "${GLOBAL_URL}" ]; then
    wait_for_proxy "${GLOBAL_URL}" "npm global-defaults proxy"

    echo ""
    echo "--- Test: global defaults block lodash (no-rule package, global age block) ---"
    mkdir -p /tmp/npm-global-deny && cd /tmp/npm-global-deny
    npm init -y > /dev/null 2>&1
    # lodash has no specific rule, so global min_package_age_days:10000 applies.
    if npm install --registry "${GLOBAL_URL}" lodash 2>&1; then
        fail "global defaults block lodash (unexpectedly installed)"
    else
        pass "global defaults block lodash"
    fi

    echo ""
    echo "--- Test: bypass_age_filter allows ms@2.1.3 despite global age block ---"
    mkdir -p /tmp/npm-global-bypass && cd /tmp/npm-global-bypass
    npm init -y > /dev/null 2>&1
    # ms has bypass_age_filter:true rule, so it passes through the global age block.
    if npm install --registry "${GLOBAL_URL}" ms@2.1.3 2>&1; then
        actual=$(node -e "console.log(require('ms/package.json').version)" 2>&1)
        if [ "$actual" = "2.1.3" ]; then
            pass "bypass_age_filter allows ms@2.1.3 despite global age block"
        else
            fail "bypass_age_filter allows ms@2.1.3 (got version $actual)"
        fi
    else
        fail "bypass_age_filter allows ms@2.1.3 (install failed)"
    fi
fi

# ─── Tests 20-21: version_patterns deny rule ──────────────────────────────────
# Rule pattern: "-alpha|-beta|-rc\."
# webpack@5.0.0-rc.6 is a known pre-release matching "-rc\."; webpack@5.105.4 is stable.

VPATTERN_URL="${NPM_VERSION_PATTERN_PROXY_URL:-}"

if phase_enabled "version-pattern" && [ -n "${VPATTERN_URL}" ]; then
    wait_for_proxy "${VPATTERN_URL}" "npm version-pattern proxy"

    echo ""
    echo "--- Test: version_patterns denies webpack@5.0.0-rc.6 (rc pattern match) ---"
    mkdir -p /tmp/npm-vpattern-deny && cd /tmp/npm-vpattern-deny
    npm init -y > /dev/null 2>&1
    if npm install --registry "${VPATTERN_URL}" "webpack@5.0.0-rc.6" 2>&1; then
        fail "version_patterns denies webpack@5.0.0-rc.6 (unexpectedly installed)"
    else
        pass "version_patterns denies webpack@5.0.0-rc.6"
    fi

    echo ""
    echo "--- Test: version_patterns allows webpack@5.105.4 (no pattern match) ---"
    mkdir -p /tmp/npm-vpattern-pass && cd /tmp/npm-vpattern-pass
    npm init -y > /dev/null 2>&1
    if npm install --registry "${VPATTERN_URL}" "webpack@5.105.4" 2>&1; then
        actual=$(node -e "console.log(require('webpack/package.json').version)" 2>&1)
        if [ "$actual" = "5.105.4" ]; then
            pass "version_patterns allows webpack@5.105.4"
        else
            fail "version_patterns allows webpack@5.105.4 (got version $actual)"
        fi
    else
        fail "version_patterns allows webpack@5.105.4 (install failed)"
    fi
fi

# ─── Tests 22-23: install_scripts deny rule ───────────────────────────────────
# esbuild@0.19.12 has a postinstall script; lodash does not.
# The proxy filters packument entries that have dangerous install scripts.

SCRIPTS_URL="${NPM_INSTALL_SCRIPTS_PROXY_URL:-}"

if phase_enabled "install-scripts" && [ -n "${SCRIPTS_URL}" ]; then
    wait_for_proxy "${SCRIPTS_URL}" "npm install-scripts proxy"

    echo ""
    echo "--- Test: install_scripts deny blocks esbuild@0.19.12 (has postinstall) ---"
    mkdir -p /tmp/npm-scripts-deny && cd /tmp/npm-scripts-deny
    npm init -y > /dev/null 2>&1
    # esbuild 0.19.12 has postinstall: "node install.js" — the proxy removes it from packument.
    if npm install --registry "${SCRIPTS_URL}" "esbuild@0.19.12" 2>&1; then
        fail "install_scripts deny blocks esbuild@0.19.12 (unexpectedly installed)"
    else
        pass "install_scripts deny blocks esbuild@0.19.12"
    fi

    echo ""
    echo "--- Test: install_scripts deny allows lodash (no install scripts) ---"
    mkdir -p /tmp/npm-scripts-pass && cd /tmp/npm-scripts-pass
    npm init -y > /dev/null 2>&1
    if npm install --registry "${SCRIPTS_URL}" lodash 2>&1; then
        if node -e "require('lodash'); console.log('ok')" 2>&1; then
            pass "install_scripts deny allows lodash"
        else
            fail "install_scripts deny allows lodash (require failed)"
        fi
    else
        fail "install_scripts deny allows lodash (install failed)"
    fi
fi

# ─── Tests 24-25: trusted_packages rule ───────────────────────────────────────
# @types/* is trusted and bypasses all rules (age, install scripts, etc.).
# ms is not trusted and gets blocked by the block-everything age rule.

TRUSTED_URL="${NPM_TRUSTED_PACKAGES_PROXY_URL:-}"

if phase_enabled "trusted-packages" && [ -n "${TRUSTED_URL}" ]; then
    wait_for_proxy "${TRUSTED_URL}" "npm trusted-packages proxy"

    echo ""
    echo "--- Test: trusted_packages allows @types/ms despite block-everything rules ---"
    mkdir -p /tmp/npm-trusted-pass && cd /tmp/npm-trusted-pass
    npm init -y > /dev/null 2>&1
    # @types/* is in trusted_packages — all rules (age, install scripts) are bypassed.
    if npm install --registry "${TRUSTED_URL}" @types/ms 2>&1; then
        if [ -d node_modules/@types/ms ]; then
            pass "trusted_packages allows @types/ms"
        else
            fail "trusted_packages allows @types/ms (directory missing)"
        fi
    else
        fail "trusted_packages allows @types/ms (install failed)"
    fi

    echo ""
    echo "--- Test: untrusted ms blocked by block-everything age rule ---"
    mkdir -p /tmp/npm-trusted-deny && cd /tmp/npm-trusted-deny
    npm init -y > /dev/null 2>&1
    # ms is not in trusted_packages, so the block-everything rule (age 10000) applies.
    if npm install --registry "${TRUSTED_URL}" ms 2>&1; then
        fail "untrusted ms blocked by rules (unexpectedly installed)"
    else
        pass "untrusted ms blocked by block-everything age rule"
    fi
fi

# ─── Tests 26-33: real-life production config ─────────────────────────────────
# A single proxy with multiple rules active simultaneously:
#   - trusted_packages: @types/*, @babel/* (bypass everything)
#   - install_scripts: deny (except esbuild in allowed_with_scripts)
#   - defaults: min_package_age_days=7, block_pre_releases=true
#   - explicit deny: event-stream (known malicious)
#   - version_patterns: block -canary, -nightly
#
# Package selection rationale:
#   @types/ms      — trusted scope, zero deps              → should install
#   @babel/parser  — trusted scope, zero deps               → should install (trusted bypasses scripts)
#   lodash@4.17.21 — old stable, no scripts               → should install (passes age, no scripts)
#   esbuild        — has postinstall but in allowlist      → should install
#   ms@2.1.3       — old stable, no scripts               → should install (passes age)
#   event-stream   — explicitly denied (malicious)         → should FAIL
#   react@19.0.0-rc.1 — pre-release blocked by defaults   → should FAIL
#   bcrypt         — has install scripts, not in allowlist  → should FAIL

REAL_LIFE_URL="${NPM_REAL_LIFE_PROXY_URL:-}"

if phase_enabled "real-life" && [ -n "${REAL_LIFE_URL}" ]; then
    wait_for_proxy "${REAL_LIFE_URL}" "npm real-life proxy"

    echo ""
    echo "--- Test 26: trusted @types/ms bypasses all rules (real-life) ---"
    mkdir -p /tmp/npm-rl-types && cd /tmp/npm-rl-types
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" @types/ms 2>&1; then
        if [ -d node_modules/@types/ms ]; then
            pass "real-life: trusted @types/ms installs"
        else
            fail "real-life: trusted @types/ms (directory missing)"
        fi
    else
        fail "real-life: trusted @types/ms (install failed)"
    fi

    echo ""
    echo "--- Test 27: trusted @babel/parser bypasses install scripts check (real-life) ---"
    mkdir -p /tmp/npm-rl-babel && cd /tmp/npm-rl-babel
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" @babel/parser 2>&1; then
        if [ -d node_modules/@babel/parser ]; then
            pass "real-life: trusted @babel/parser installs"
        else
            fail "real-life: trusted @babel/parser (directory missing)"
        fi
    else
        fail "real-life: trusted @babel/parser (install failed)"
    fi

    echo ""
    echo "--- Test 28: old stable lodash@4.17.21 passes age check (real-life) ---"
    mkdir -p /tmp/npm-rl-lodash && cd /tmp/npm-rl-lodash
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" lodash@4.17.21 2>&1; then
        actual=$(node -e "console.log(require('lodash/package.json').version)" 2>&1)
        if [ "$actual" = "4.17.21" ]; then
            pass "real-life: lodash@4.17.21 installs"
        else
            fail "real-life: lodash@4.17.21 (got version $actual)"
        fi
    else
        fail "real-life: lodash@4.17.21 (install failed)"
    fi

    echo ""
    echo "--- Test 29: esbuild allowed despite install scripts (allowed_with_scripts) ---"
    mkdir -p /tmp/npm-rl-esbuild && cd /tmp/npm-rl-esbuild
    npm init -y > /dev/null 2>&1
    # esbuild has postinstall but is in allowed_with_scripts list.
    if npm install --registry "${REAL_LIFE_URL}" esbuild@0.19.12 2>&1; then
        pass "real-life: esbuild@0.19.12 installs (allowed_with_scripts)"
    else
        fail "real-life: esbuild@0.19.12 (install failed)"
    fi

    echo ""
    echo "--- Test 30: old stable ms@2.1.3 passes all checks (real-life) ---"
    mkdir -p /tmp/npm-rl-ms && cd /tmp/npm-rl-ms
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" ms@2.1.3 2>&1; then
        actual=$(node -e "console.log(require('ms/package.json').version)" 2>&1)
        if [ "$actual" = "2.1.3" ]; then
            pass "real-life: ms@2.1.3 installs"
        else
            fail "real-life: ms@2.1.3 (got version $actual)"
        fi
    else
        fail "real-life: ms@2.1.3 (install failed)"
    fi

    echo ""
    echo "--- Test 31: event-stream blocked by explicit deny (known malicious) ---"
    mkdir -p /tmp/npm-rl-evtstream && cd /tmp/npm-rl-evtstream
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" event-stream 2>&1; then
        fail "real-life: event-stream (unexpectedly installed)"
    else
        pass "real-life: event-stream blocked (explicit deny)"
    fi

    echo ""
    echo "--- Test 32: react pre-release blocked by block_pre_releases (real-life) ---"
    mkdir -p /tmp/npm-rl-react-rc && cd /tmp/npm-rl-react-rc
    npm init -y > /dev/null 2>&1
    if npm install --registry "${REAL_LIFE_URL}" "react@19.0.0-rc.1" 2>&1; then
        fail "real-life: react@19.0.0-rc.1 (unexpectedly installed)"
    else
        pass "real-life: react@19.0.0-rc.1 blocked (pre-release)"
    fi

    echo ""
    echo "--- Test 33: bcrypt blocked by install scripts (has install, not in allowlist) ---"
    mkdir -p /tmp/npm-rl-bcrypt && cd /tmp/npm-rl-bcrypt
    npm init -y > /dev/null 2>&1
    # bcrypt has install scripts (install lifecycle); not in allowed_with_scripts.
    if npm install --registry "${REAL_LIFE_URL}" "bcrypt@5.1.1" 2>&1; then
        fail "real-life: bcrypt@5.1.1 (unexpectedly installed)"
    else
        pass "real-life: bcrypt@5.1.1 blocked (install scripts)"
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
