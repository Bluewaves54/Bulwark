#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# Ad-hoc manual QE test script for PKGuard curation proxies.
# Runs real package-manager commands against each proxy rule type.
set -euo pipefail

PASS=0; FAIL=0; ERRORS=""

# Colours (if terminal supports it)
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'

check() {
  local num="$1" desc="$2" expected="$3"
  shift 3
  echo -n "  [$num] $desc ... "
  local out rc
  out=$("$@" 2>&1) && rc=0 || rc=$?
  if [ "$expected" = "pass" ] && [ $rc -eq 0 ]; then
    echo -e "${GREEN}PASS${NC} (exit=$rc)"
    PASS=$((PASS+1))
  elif [ "$expected" = "fail" ] && [ $rc -ne 0 ]; then
    echo -e "${GREEN}PASS${NC} (correctly denied, exit=$rc)"
    PASS=$((PASS+1))
  else
    echo -e "${RED}FAIL${NC} (expected=$expected exit=$rc)"
    ERRORS="$ERRORS\n  [$num] $desc expected=$expected actual_exit=$rc"
    FAIL=$((FAIL+1))
  fi
}

# For curl-based checks: pass → HTTP 200, fail → HTTP 403/404
curl_check() {
  local num="$1" desc="$2" expected="$3" url="$4"
  echo -n "  [$num] $desc ... "
  local code
  code=$(curl -s -o /dev/null -w "%{http_code}" "$url" 2>&1) || code="000"
  if [ "$expected" = "pass" ] && [ "$code" = "200" ]; then
    echo -e "${GREEN}PASS${NC} (HTTP $code)"
    PASS=$((PASS+1))
  elif [ "$expected" = "fail" ] && [ "$code" = "403" ]; then
    echo -e "${GREEN}PASS${NC} (correctly denied HTTP $code)"
    PASS=$((PASS+1))
  else
    echo -e "${RED}FAIL${NC} (expected=$expected HTTP=$code)"
    ERRORS="$ERRORS\n  [$num] $desc expected=$expected http=$code"
    FAIL=$((FAIL+1))
  fi
}

TMP=$(mktemp -d)
trap "rm -rf $TMP" EXIT

######################################################################
echo ""
echo "============================================================"
echo "  NPM AD-HOC TESTS (using npm)"
echo "============================================================"

# --- Allow-all proxy (19100) ---
echo -e "\n${YELLOW}--- npm-proxy (allow-all) ---${NC}"
d="$TMP/npm-aa-1"; mkdir -p "$d"
check 1 "npm install lodash (should PASS)" pass npm install --registry http://localhost:19100 --prefix "$d" --no-save --no-audit --no-fund lodash

d="$TMP/npm-aa-2"; mkdir -p "$d"
check 2 "npm install @types/node (scoped, should PASS)" pass npm install --registry http://localhost:19100 --prefix "$d" --no-save --no-audit --no-fund @types/node

curl_check 3 "healthz endpoint" pass "http://localhost:19100/healthz"
curl_check 4 "metrics endpoint" pass "http://localhost:19100/metrics"

# --- Age block proxy (19101) ---
echo -e "\n${YELLOW}--- npm-proxy-age-block (ms is age-blocked 10000 days) ---${NC}"
d="$TMP/npm-age-1"; mkdir -p "$d"
check 5 "npm install ms (should FAIL - too young)" fail npm install --registry http://localhost:19101 --prefix "$d" --no-save --no-audit --no-fund ms

d="$TMP/npm-age-2"; mkdir -p "$d"
check 6 "npm install ms@2.1.3 exact (should FAIL - still age blocked)" fail npm install --registry http://localhost:19101 --prefix "$d" --no-save --no-audit --no-fund ms@2.1.3

curl_check 7 "npm tarball URL for ms (should FAIL - age check)" fail "http://localhost:19101/ms/-/ms-2.1.3.tgz"

d="$TMP/npm-age-3"; mkdir -p "$d"
check 8 "npm install lodash (different pkg, should PASS)" pass npm install --registry http://localhost:19101 --prefix "$d" --no-save --no-audit --no-fund lodash

# --- Age pinned proxy (19102) ---
echo -e "\n${YELLOW}--- npm-proxy-age-pinned (ms age-blocked but 2.1.3 pinned) ---${NC}"
d="$TMP/npm-pin-1"; mkdir -p "$d"
check 9 "npm install ms@2.1.3 (pinned version, should PASS)" pass npm install --registry http://localhost:19102 --prefix "$d" --no-save --no-audit --no-fund ms@2.1.3

d="$TMP/npm-pin-2"; mkdir -p "$d"
check 10 "npm install ms@2.1.2 (not pinned, should FAIL)" fail npm install --registry http://localhost:19102 --prefix "$d" --no-save --no-audit --no-fund ms@2.1.2

# --- Block pre-release proxy (19103) ---
echo -e "\n${YELLOW}--- npm-proxy-block-prerelease (react pre-releases blocked) ---${NC}"
d="$TMP/npm-pre-1"; mkdir -p "$d"
check 11 "npm install react@19.0.0-rc.1 (pre-release, should FAIL)" fail npm install --registry http://localhost:19103 --prefix "$d" --no-save --no-audit --no-fund react@19.0.0-rc.1

d="$TMP/npm-pre-2"; mkdir -p "$d"
check 12 "npm install react@18.3.1 (stable, should PASS)" pass npm install --registry http://localhost:19103 --prefix "$d" --no-save --no-audit --no-fund react@18.3.1

# --- Explicit deny proxy (19104) ---
echo -e "\n${YELLOW}--- npm-proxy-explicit-deny (ms denied entirely) ---${NC}"
d="$TMP/npm-deny-1"; mkdir -p "$d"
check 13 "npm install ms (denied, should FAIL)" fail npm install --registry http://localhost:19104 --prefix "$d" --no-save --no-audit --no-fund ms

d="$TMP/npm-deny-2"; mkdir -p "$d"
check 14 "npm install lodash (not denied, should PASS)" pass npm install --registry http://localhost:19104 --prefix "$d" --no-save --no-audit --no-fund lodash

# --- Global defaults proxy (19105) ---
echo -e "\n${YELLOW}--- npm-proxy-global-defaults (global age 10000d + pre-release block, ms bypasses age) ---${NC}"
d="$TMP/npm-gd-1"; mkdir -p "$d"
check 15 "npm install lodash (global age blocks, should FAIL)" fail npm install --registry http://localhost:19105 --prefix "$d" --no-save --no-audit --no-fund lodash

d="$TMP/npm-gd-2"; mkdir -p "$d"
check 16 "npm install ms@2.1.3 (bypass_age_filter, should PASS)" pass npm install --registry http://localhost:19105 --prefix "$d" --no-save --no-audit --no-fund ms@2.1.3

# --- Version pattern proxy (19106) ---
echo -e "\n${YELLOW}--- npm-proxy-version-pattern (deny -alpha|-beta|-rc.) ---${NC}"
d="$TMP/npm-vp-1"; mkdir -p "$d"
check 17 "npm install webpack@5.0.0-rc.6 (matches pattern, should FAIL)" fail npm install --registry http://localhost:19106 --prefix "$d" --no-save --no-audit --no-fund webpack@5.0.0-rc.6

d="$TMP/npm-vp-2"; mkdir -p "$d"
check 18 "npm install webpack@5.98.0 (stable, should PASS)" pass npm install --registry http://localhost:19106 --prefix "$d" --no-save --no-audit --no-fund webpack@5.98.0

# --- Install scripts proxy (19107) ---
echo -e "\n${YELLOW}--- npm-proxy-install-scripts (deny packages with install scripts) ---${NC}"
d="$TMP/npm-is-1"; mkdir -p "$d"
check 19 "npm install esbuild (has postinstall, should FAIL)" fail npm install --registry http://localhost:19107 --prefix "$d" --no-save --no-audit --no-fund esbuild

d="$TMP/npm-is-2"; mkdir -p "$d"
check 20 "npm install lodash (no scripts, should PASS)" pass npm install --registry http://localhost:19107 --prefix "$d" --no-save --no-audit --no-fund lodash

# --- Trusted packages proxy (19108) ---
echo -e "\n${YELLOW}--- npm-proxy-trusted-packages (@types/* trusted, rest age-blocked) ---${NC}"
d="$TMP/npm-tp-1"; mkdir -p "$d"
check 21 "npm install @types/ms (trusted, should PASS)" pass npm install --registry http://localhost:19108 --prefix "$d" --no-save --no-audit --no-fund @types/ms

d="$TMP/npm-tp-2"; mkdir -p "$d"
check 22 "npm install ms (not trusted, age blocked, should FAIL)" fail npm install --registry http://localhost:19108 --prefix "$d" --no-save --no-audit --no-fund ms

# --- Real-life proxy (19109) ---
echo -e "\n${YELLOW}--- npm-proxy-real-life (production config) ---${NC}"
d="$TMP/npm-rl-1"; mkdir -p "$d"
check 23 "npm install @types/node (trusted, should PASS)" pass npm install --registry http://localhost:19109 --prefix "$d" --no-save --no-audit --no-fund @types/node

d="$TMP/npm-rl-2"; mkdir -p "$d"
check 24 "npm install @babel/core (trusted, should PASS)" pass npm install --registry http://localhost:19109 --prefix "$d" --no-save --no-audit --no-fund @babel/core

d="$TMP/npm-rl-3"; mkdir -p "$d"
check 25 "npm install event-stream (denied, should FAIL)" fail npm install --registry http://localhost:19109 --prefix "$d" --no-save --no-audit --no-fund event-stream

d="$TMP/npm-rl-4"; mkdir -p "$d"
check 26 "npm install esbuild (scripts allowed, should PASS)" pass npm install --registry http://localhost:19109 --prefix "$d" --no-save --no-audit --no-fund esbuild

d="$TMP/npm-rl-5"; mkdir -p "$d"
check 27 "npm install webpack@5.0.0-canary.1 (version pattern deny, should FAIL)" fail npm install --registry http://localhost:19109 --prefix "$d" --no-save --no-audit --no-fund webpack@5.0.0-canary.1

######################################################################
echo ""
echo "============================================================"
echo "  PNPM AD-HOC TESTS"
echo "============================================================"

# Use npx pnpm as fallback if pnpm not installed
PNPM="pnpm"
if ! command -v pnpm &>/dev/null; then
  echo "(pnpm not installed, installing via npm...)"
  npm install -g pnpm 2>/dev/null || true
fi
if ! command -v pnpm &>/dev/null; then
  PNPM="npx pnpm"
fi

echo -e "\n${YELLOW}--- pnpm via allow-all (19100) ---${NC}"
d="$TMP/pnpm-aa"; mkdir -p "$d"; cd "$d"; echo '{}' > package.json
check 28 "pnpm add debug (should PASS)" pass $PNPM add --registry http://localhost:19100 debug
cd "$TMP"

echo -e "\n${YELLOW}--- pnpm via age-block (19101) ---${NC}"
d="$TMP/pnpm-ab"; mkdir -p "$d"; cd "$d"; echo '{}' > package.json
check 29 "pnpm add ms (should FAIL - age blocked)" fail $PNPM add --registry http://localhost:19101 ms
cd "$TMP"

echo -e "\n${YELLOW}--- pnpm via explicit-deny (19104) ---${NC}"
d="$TMP/pnpm-deny"; mkdir -p "$d"; cd "$d"; echo '{}' > package.json
check 30 "pnpm add ms (should FAIL - denied)" fail $PNPM add --registry http://localhost:19104 ms
cd "$TMP"

echo -e "\n${YELLOW}--- pnpm via real-life (19109) ---${NC}"
d="$TMP/pnpm-rl"; mkdir -p "$d"; cd "$d"; echo '{}' > package.json
check 31 "pnpm add @types/node (trusted, should PASS)" pass $PNPM add --registry http://localhost:19109 @types/node
cd "$TMP"

d="$TMP/pnpm-rl2"; mkdir -p "$d"; cd "$d"; echo '{}' > package.json
check 32 "pnpm add event-stream (denied, should FAIL)" fail $PNPM add --registry http://localhost:19109 event-stream
cd "$TMP"

######################################################################
echo ""
echo "============================================================"
echo "  PIP AD-HOC TESTS"
echo "============================================================"

echo -e "\n${YELLOW}--- pypi-proxy (allow-all, 19000) ---${NC}"
d="$TMP/pip-aa"
check 33 "pip install certifi (should PASS)" pass pip install --index-url http://localhost:19000/simple --trusted-host localhost --no-cache-dir --target "$d" certifi

curl_check 34 "PyPI JSON API /pypi/pip/json" pass "http://localhost:19000/pypi/pip/json"
curl_check 35 "pypi healthz" pass "http://localhost:19000/healthz"

echo -e "\n${YELLOW}--- pypi-proxy-age-block (19001, urllib3 age-blocked) ---${NC}"
d="$TMP/pip-age1"
check 36 "pip install urllib3 (should FAIL - age blocked)" fail pip install --index-url http://localhost:19001/simple --trusted-host localhost --no-cache-dir --target "$d" urllib3

d="$TMP/pip-age2"
check 37 "pip install certifi (different pkg, should PASS)" pass pip install --index-url http://localhost:19001/simple --trusted-host localhost --no-cache-dir --target "$d" certifi

echo -e "\n${YELLOW}--- pypi-proxy-age-pinned (19002, urllib3 age-blocked, 2.0.7 pinned) ---${NC}"
d="$TMP/pip-pin1"
check 38 "pip install urllib3==2.0.7 (pinned, should PASS)" pass pip install --index-url http://localhost:19002/simple --trusted-host localhost --no-cache-dir --target "$d" "urllib3==2.0.7"

d="$TMP/pip-pin2"
check 39 "pip install urllib3==2.0.6 (not pinned, should FAIL)" fail pip install --index-url http://localhost:19002/simple --trusted-host localhost --no-cache-dir --target "$d" "urllib3==2.0.6"

echo -e "\n${YELLOW}--- pypi-proxy-block-prerelease (19003, packaging pre-releases blocked) ---${NC}"
d="$TMP/pip-pre1"
check 40 "pip install packaging==26.0rc1 (pre-release, should FAIL)" fail pip install --index-url http://localhost:19003/simple --trusted-host localhost --no-cache-dir --target "$d" --pre "packaging==26.0rc1"

d="$TMP/pip-pre2"
check 41 "pip install packaging==24.2 (stable, should PASS)" pass pip install --index-url http://localhost:19003/simple --trusted-host localhost --no-cache-dir --target "$d" "packaging==24.2"

echo -e "\n${YELLOW}--- pypi-proxy-explicit-deny (19004, urllib3 denied) ---${NC}"
d="$TMP/pip-deny1"
check 42 "pip install urllib3 (denied, should FAIL)" fail pip install --index-url http://localhost:19004/simple --trusted-host localhost --no-cache-dir --target "$d" urllib3

d="$TMP/pip-deny2"
check 43 "pip install six (not denied, should PASS)" pass pip install --index-url http://localhost:19004/simple --trusted-host localhost --no-cache-dir --target "$d" six

echo -e "\n${YELLOW}--- pypi-proxy-global-defaults (19005, global age 10000d + pre-release, urllib3 bypasses age) ---${NC}"
d="$TMP/pip-gd1"
check 44 "pip install certifi (global age blocks, should FAIL)" fail pip install --index-url http://localhost:19005/simple --trusted-host localhost --no-cache-dir --target "$d" certifi

d="$TMP/pip-gd2"
check 45 "pip install urllib3==2.0.7 (bypass age, should PASS)" pass pip install --index-url http://localhost:19005/simple --trusted-host localhost --no-cache-dir --target "$d" "urllib3==2.0.7"

echo -e "\n${YELLOW}--- pypi-proxy-version-pattern (19006, deny rc/a/b/.dev) ---${NC}"
d="$TMP/pip-vp1"
check 46 "pip install packaging==26.0rc1 (matches pattern, should FAIL)" fail pip install --index-url http://localhost:19006/simple --trusted-host localhost --no-cache-dir --target "$d" --pre "packaging==26.0rc1"

d="$TMP/pip-vp2"
check 47 "pip install packaging==24.2 (stable, should PASS)" pass pip install --index-url http://localhost:19006/simple --trusted-host localhost --no-cache-dir --target "$d" "packaging==24.2"

echo -e "\n${YELLOW}--- pypi-proxy-real-life (19007, production config) ---${NC}"
d="$TMP/pip-rl1"
check 48 "pip install setuptools (trusted, should PASS)" pass pip install --index-url http://localhost:19007/simple --trusted-host localhost --no-cache-dir --target "$d" setuptools

d="$TMP/pip-rl2"
check 49 "pip install wheel (trusted, should PASS)" pass pip install --index-url http://localhost:19007/simple --trusted-host localhost --no-cache-dir --target "$d" wheel

d="$TMP/pip-rl3"
check 50 "pip install python3-dateutil (typosquat denied, should FAIL)" fail pip install --index-url http://localhost:19007/simple --trusted-host localhost --no-cache-dir --target "$d" python3-dateutil

######################################################################
echo ""
echo "============================================================"
echo "  UV AD-HOC TESTS"
echo "============================================================"

# Try to use uv if available, otherwise install it
if ! command -v uv &>/dev/null; then
  echo "(uv not installed, installing via pip...)"
  pip install uv 2>/dev/null || true
fi

if command -v uv &>/dev/null; then
  echo -e "\n${YELLOW}--- uv via allow-all (19000) ---${NC}"
  d="$TMP/uv-aa"
  check 51 "uv pip install idna (should PASS)" pass uv pip install --index-url http://localhost:19000/simple --no-cache --target "$d" idna

  echo -e "\n${YELLOW}--- uv via age-block (19001) ---${NC}"
  d="$TMP/uv-ab"
  check 52 "uv pip install urllib3 (should FAIL - age blocked)" fail uv pip install --index-url http://localhost:19001/simple --no-cache --target "$d" urllib3

  echo -e "\n${YELLOW}--- uv via explicit-deny (19004) ---${NC}"
  d="$TMP/uv-deny"
  check 53 "uv pip install urllib3 (should FAIL - denied)" fail uv pip install --index-url http://localhost:19004/simple --no-cache --target "$d" urllib3

  d="$TMP/uv-deny2"
  check 54 "uv pip install six (should PASS)" pass uv pip install --index-url http://localhost:19004/simple --no-cache --target "$d" six

  echo -e "\n${YELLOW}--- uv via real-life (19007) ---${NC}"
  d="$TMP/uv-rl1"
  check 55 "uv pip install setuptools (trusted, should PASS)" pass uv pip install --index-url http://localhost:19007/simple --no-cache --target "$d" setuptools

  d="$TMP/uv-rl2"
  check 56 "uv pip install python3-dateutil (typosquat, should FAIL)" fail uv pip install --index-url http://localhost:19007/simple --no-cache --target "$d" python3-dateutil
else
  echo "(uv not available, skipping uv tests)"
fi

######################################################################
echo ""
echo "============================================================"
echo "  MAVEN AD-HOC TESTS (curl-based)"
echo "============================================================"

echo -e "\n${YELLOW}--- maven-proxy (allow-all, 19200) ---${NC}"
curl_check 57 "maven: junit:junit:4.13.2 POM" pass "http://localhost:19200/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 58 "maven: junit:junit:4.13.2 JAR" pass "http://localhost:19200/junit/junit/4.13.2/junit-4.13.2.jar"
curl_check 59 "maven: maven-metadata.xml" pass "http://localhost:19200/junit/junit/maven-metadata.xml"
curl_check 60 "maven: healthz" pass "http://localhost:19200/healthz"
curl_check 61 "maven: metrics" pass "http://localhost:19200/metrics"

echo -e "\n${YELLOW}--- maven-proxy-age-block (19201, junit:junit age-blocked) ---${NC}"
curl_check 62 "maven: junit:junit:4.13.2 POM (should FAIL)" fail "http://localhost:19201/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 63 "maven: junit:junit:4.13.2 JAR (should FAIL)" fail "http://localhost:19201/junit/junit/4.13.2/junit-4.13.2.jar"
curl_check 64 "maven: commons-io POM (not blocked, should PASS)" pass "http://localhost:19201/commons-io/commons-io/2.18.0/commons-io-2.18.0.pom"

echo -e "\n${YELLOW}--- maven-proxy-age-pinned (19202, junit age-blocked, 4.13.2 pinned) ---${NC}"
curl_check 65 "maven: junit:4.13.2 POM (pinned, should PASS)" pass "http://localhost:19202/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 66 "maven: junit:4.12 POM (not pinned, should FAIL)" fail "http://localhost:19202/junit/junit/4.12/junit-4.12.pom"

echo -e "\n${YELLOW}--- maven-proxy-block-snapshots (19203, snapshots blocked for junit) ---${NC}"
curl_check 67 "maven: junit:4.13.2-SNAPSHOT POM (should FAIL)" fail "http://localhost:19203/junit/junit/4.13.2-SNAPSHOT/junit-4.13.2-SNAPSHOT.pom"
curl_check 68 "maven: junit:4.13.2 POM (stable, should PASS)" pass "http://localhost:19203/junit/junit/4.13.2/junit-4.13.2.pom"

echo -e "\n${YELLOW}--- maven-proxy-block-prerelease (19204, junit pre-releases blocked) ---${NC}"
curl_check 69 "maven: junit:4.13-beta-3 POM (pre-release, should FAIL)" fail "http://localhost:19204/junit/junit/4.13-beta-3/junit-4.13-beta-3.pom"
curl_check 70 "maven: junit:4.13.2 POM (stable, should PASS)" pass "http://localhost:19204/junit/junit/4.13.2/junit-4.13.2.pom"

echo -e "\n${YELLOW}--- maven-proxy-explicit-deny (19205, junit denied) ---${NC}"
curl_check 71 "maven: junit:4.13.2 POM (denied, should FAIL)" fail "http://localhost:19205/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 72 "maven: commons-io POM (not denied, should PASS)" pass "http://localhost:19205/commons-io/commons-io/2.18.0/commons-io-2.18.0.pom"

echo -e "\n${YELLOW}--- maven-proxy-global-defaults (19206, global age 10000d, commons-io bypasses) ---${NC}"
curl_check 73 "maven: junit:4.13.2 POM (global age blocks, should FAIL)" fail "http://localhost:19206/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 74 "maven: commons-io POM (bypass age, should PASS)" pass "http://localhost:19206/commons-io/commons-io/2.18.0/commons-io-2.18.0.pom"

echo -e "\n${YELLOW}--- maven-proxy-version-pattern (19207, deny -M/-RC/-alpha/-beta) ---${NC}"
curl_check 75 "maven: junit:4.13-beta-3 (matches pattern, should FAIL)" fail "http://localhost:19207/junit/junit/4.13-beta-3/junit-4.13-beta-3.pom"
curl_check 76 "maven: junit:4.13.2 (stable, should PASS)" pass "http://localhost:19207/junit/junit/4.13.2/junit-4.13.2.pom"

echo -e "\n${YELLOW}--- maven-proxy-real-life (19208, production config) ---${NC}"
curl_check 77 "maven: commons-io:2.18.0 POM (trusted, should PASS)" pass "http://localhost:19208/commons-io/commons-io/2.18.0/commons-io-2.18.0.pom"
curl_check 78 "maven: junit:4.13.2 POM (denied, should FAIL)" fail "http://localhost:19208/junit/junit/4.13.2/junit-4.13.2.pom"
curl_check 79 "maven: junit:4.13.2-SNAPSHOT (snapshot blocked, should FAIL)" fail "http://localhost:19208/junit/junit/4.13.2-SNAPSHOT/junit-4.13.2-SNAPSHOT.pom"

######################################################################
echo ""
echo "============================================================"
echo "  SUMMARY"
echo "============================================================"
echo -e "  ${GREEN}PASSED: $PASS${NC}"
echo -e "  ${RED}FAILED: $FAIL${NC}"
if [ $FAIL -gt 0 ]; then
  echo -e "\n  Failures:$ERRORS"
  echo ""
  echo "Some ad-hoc tests FAILED."
  exit 1
else
  echo ""
  echo "All ad-hoc tests PASSED!"
  exit 0
fi
