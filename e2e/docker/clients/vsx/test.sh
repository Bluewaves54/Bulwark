#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
# VSX Bulwark E2E test script — curl-based since VSX has no CLI package manager.
set -euo pipefail

PASS=0
FAIL=0
TESTS=0

pass() { PASS=$((PASS+1)); TESTS=$((TESTS+1)); echo "  ✅ PASS: $1"; }
fail() { FAIL=$((FAIL+1)); TESTS=$((TESTS+1)); echo "  ❌ FAIL: $1"; }

phase_enabled() { [[ -z "${E2E_PHASE:-}" ]] || [[ "${E2E_PHASE}" == "$1" ]]; }

wait_for_proxy() {
  local url="$1"
  echo "Waiting for proxy at ${url}/healthz ..."
  for i in $(seq 1 60); do
    if curl -sf "${url}/healthz" >/dev/null 2>&1; then
      echo "  Proxy is ready."
      return 0
    fi
    sleep 1
  done
  echo "  ERROR: proxy did not become ready within 60 seconds."
  return 1
}

# ─── Phase: baseline (allow-all) ─────────────────────────────────────────────
if phase_enabled "baseline"; then
  echo ""
  echo "=== Phase: baseline (allow-all) ==="
  PROXY="${VSX_PROXY_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 1: Health endpoint returns 200.
    echo "Test 1: /healthz returns 200"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/healthz")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "/healthz returns 200"
    else
      fail "/healthz returned ${HTTP_CODE}"
    fi

    # Test 2: Extension metadata for a well-known extension returns 200.
    echo "Test 2: GET /api/redhat/java returns 200 with allVersions"
    RESP=$(curl -sf "${PROXY}/api/redhat/java" 2>/dev/null || echo "CURL_FAILED")
    if [[ "$RESP" == "CURL_FAILED" ]]; then
      fail "GET /api/redhat/java failed"
    else
      HAS_VERSIONS=$(echo "$RESP" | jq -r '.allVersions | length' 2>/dev/null || echo "0")
      if [[ "$HAS_VERSIONS" -gt "0" ]]; then
        pass "GET /api/redhat/java returned ${HAS_VERSIONS} versions"
      else
        fail "GET /api/redhat/java returned no versions"
      fi
    fi

    # Test 3: Query endpoint returns 200.
    echo "Test 3: GET /api/-/query returns 200"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/-/query?namespaceName=redhat&extensionName=java")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "GET /api/-/query returns 200"
    else
      fail "GET /api/-/query returned ${HTTP_CODE}"
    fi

    # Test 4: Search passthrough returns 200.
    echo "Test 4: GET /api/-/search?query=java returns 200"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/-/search?query=java" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "GET /api/-/search?query=java returns 200"
    else
      fail "GET /api/-/search?query=java returned ${HTTP_CODE}"
    fi

    # Test 5: Metrics endpoint returns JSON counters.
    echo "Test 5: /metrics returns valid JSON"
    METRICS=$(curl -sf "${PROXY}/metrics" 2>/dev/null || echo "CURL_FAILED")
    if echo "$METRICS" | jq -e '.requests_total' >/dev/null 2>&1; then
      pass "/metrics returns valid JSON with requests_total"
    else
      fail "/metrics response invalid: ${METRICS}"
    fi
  fi
fi

# ─── Phase: explicit-deny ────────────────────────────────────────────────────
if phase_enabled "explicit-deny"; then
  echo ""
  echo "=== Phase: explicit-deny ==="
  PROXY="${VSX_PROXY_DENY_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 6: Denied extension returns 403.
    echo "Test 6: GET /api/redhat/java returns 403 (denied)"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${PROXY}/api/redhat/java")
    if [[ "$HTTP_CODE" == "403" ]]; then
      pass "GET /api/redhat/java denied with 403"
    else
      fail "GET /api/redhat/java returned ${HTTP_CODE}, expected 403"
    fi

    # Test 7: Non-denied extension still works.
    echo "Test 7: GET /api/vscodevim/vim returns 200 (allowed)"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/vscodevim/vim" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "GET /api/vscodevim/vim allowed with 200"
    else
      fail "GET /api/vscodevim/vim returned ${HTTP_CODE}, expected 200"
    fi
  fi
fi

# ─── Phase: prerelease ───────────────────────────────────────────────────────
if phase_enabled "prerelease"; then
  echo ""
  echo "=== Phase: prerelease (global block) ==="
  PROXY="${VSX_PROXY_PRERELEASE_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 8: Extension metadata is returned (versions may be filtered).
    echo "Test 8: GET /api/redhat/java returns 200 with pre-releases filtered"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/redhat/java" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "extension metadata returned (pre-releases filtered if any)"
    else
      fail "GET /api/redhat/java returned ${HTTP_CODE}"
    fi
  fi
fi

# ─── Phase: prerelease-pkg ───────────────────────────────────────────────────
if phase_enabled "prerelease-pkg"; then
  echo ""
  echo "=== Phase: prerelease-pkg (per-package block) ==="
  PROXY="${VSX_PROXY_PRERELEASE_PKG_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 9: Targeted extension has pre-releases filtered.
    echo "Test 9: GET /api/redhat/java returns 200"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/redhat/java" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "targeted extension returned 200"
    else
      fail "GET /api/redhat/java returned ${HTTP_CODE}"
    fi

    # Test 10: Non-targeted extension is unaffected.
    echo "Test 10: GET /api/vscodevim/vim returns 200 (unaffected)"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/vscodevim/vim" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "non-targeted extension returned 200"
    else
      fail "GET /api/vscodevim/vim returned ${HTTP_CODE}"
    fi
  fi
fi

# ─── Phase: global-defaults ──────────────────────────────────────────────────
if phase_enabled "global-defaults"; then
  echo ""
  echo "=== Phase: global-defaults ==="
  PROXY="${VSX_PROXY_DEFAULTS_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 11: Bypassed extension (redhat.*) is allowed.
    echo "Test 11: GET /api/redhat/java returns 200 (bypass)"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/redhat/java" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "bypassed extension allowed with 200"
    else
      fail "GET /api/redhat/java returned ${HTTP_CODE}"
    fi
  fi
fi

# ─── Phase: real-life ────────────────────────────────────────────────────────
if phase_enabled "real-life"; then
  echo ""
  echo "=== Phase: real-life (combined config) ==="
  PROXY="${VSX_PROXY_REAL_LIFE_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Test 12: Trusted extension is allowed.
    echo "Test 12: GET /api/redhat/java returns 200 (trusted)"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/redhat/java" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "trusted extension allowed with 200"
    else
      fail "GET /api/redhat/java returned ${HTTP_CODE}"
    fi

    # Test 13: Query endpoint works with combined config.
    echo "Test 13: POST /api/-/query returns 200"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "${PROXY}/api/-/query" \
      -H "Content-Type: application/json" \
      -d '{"namespaceName":"redhat","extensionName":"java"}')
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "POST /api/-/query returns 200"
    else
      fail "POST /api/-/query returned ${HTTP_CODE}"
    fi
  fi
fi

# ─── Phase: best-practices ───────────────────────────────────────────────────
if phase_enabled "best-practices"; then
  echo ""
  echo "=== Phase: best-practices (IOC block list + JSON error visibility) ==="
  PROXY="${VSX_PROXY_BEST_PRACTICES_URL:-}"
  if [[ -n "$PROXY" ]]; then
    wait_for_proxy "$PROXY"

    # Tests: Five representative Glassworm IOC namespaces must return 403 with
    # a JSON error body containing the [Bulwark] reason string.
    # Because the proxy blocks at the package-evaluation level (before hitting
    # upstream), these work even though the malicious extensions no longer
    # exist on Open VSX (removed after the Glassworm campaign was disclosed).
    for NS in "oigotm" "pfrfrprf" "twilkbilk" "otoboss" "daeumer-web"; do
      echo "Test: GET /api/${NS}/test-extension blocked by Glassworm deny list"
      HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${PROXY}/api/${NS}/test-extension")
      if [[ "$HTTP_CODE" == "403" ]]; then
        BODY=$(curl -s "${PROXY}/api/${NS}/test-extension")
        ERROR_MSG=$(echo "$BODY" | jq -r '.error' 2>/dev/null || echo "")
        if [[ "$ERROR_MSG" == *"[Bulwark]"* ]]; then
          pass "Glassworm IOC ${NS}.test-extension: 403 with visible reason"
        else
          fail "Glassworm IOC ${NS}.test-extension: 403 but JSON reason missing: ${BODY}"
        fi
      else
        fail "Glassworm IOC ${NS}.test-extension: returned ${HTTP_CODE}, expected 403"
      fi
    done

    # Test: Trusted extensions are never blocked by the Glassworm deny list.
    echo "Test: GET /api/esbenp/prettier-vscode returns 200 (trusted)"
    HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" "${PROXY}/api/esbenp/prettier-vscode" 2>/dev/null || echo "000")
    if [[ "$HTTP_CODE" == "200" ]]; then
      pass "Trusted extension esbenp.prettier-vscode returned 200"
    else
      fail "Trusted extension esbenp.prettier-vscode returned ${HTTP_CODE}, expected 200"
    fi

    # Test: 403 block response has Content-Type: application/json so VS Code
    # can parse and display the reason to the user.
    echo "Test: IOC block response has Content-Type application/json"
    CT=$(curl -s -o /dev/null -w "%{content_type}" "${PROXY}/api/oigotm/command-palette-extension")
    if [[ "$CT" == *"application/json"* ]]; then
      pass "403 response has Content-Type: application/json"
    else
      fail "403 response Content-Type was: ${CT} (expected application/json)"
    fi

    # Test: VSIX download endpoint for an IOC namespace also returns 403 JSON.
    echo "Test: GET /api/pfrfrprf/malware/1.0.0/file/pfrfrprf.malware-1.0.0.vsix returns 403 JSON"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${PROXY}/api/pfrfrprf/malware/1.0.0/file/pfrfrprf.malware-1.0.0.vsix")
    if [[ "$HTTP_CODE" == "403" ]]; then
      VSIX_BODY=$(curl -s "${PROXY}/api/pfrfrprf/malware/1.0.0/file/pfrfrprf.malware-1.0.0.vsix")
      VSIX_ERROR=$(echo "$VSIX_BODY" | jq -r '.error' 2>/dev/null || echo "")
      if [[ "$VSIX_ERROR" == *"[Bulwark]"* ]]; then
        pass "VSIX download for pfrfrprf.malware: 403 with JSON reason"
      else
        fail "VSIX download for pfrfrprf.malware: 403 but JSON reason missing: ${VSIX_BODY}"
      fi
    else
      fail "VSIX download for pfrfrprf.malware: returned ${HTTP_CODE}, expected 403"
    fi

    # Test: Version metadata endpoint for an IOC namespace also returns 403 JSON.
    echo "Test: GET /api/twilkbilk/something/1.0.0 returns 403 JSON"
    HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" "${PROXY}/api/twilkbilk/something/1.0.0")
    if [[ "$HTTP_CODE" == "403" ]]; then
      VER_BODY=$(curl -s "${PROXY}/api/twilkbilk/something/1.0.0")
      VER_ERROR=$(echo "$VER_BODY" | jq -r '.error' 2>/dev/null || echo "")
      if [[ "$VER_ERROR" == *"[Bulwark]"* ]]; then
        pass "Version endpoint for twilkbilk.something: 403 with JSON reason"
      else
        fail "Version endpoint for twilkbilk.something: 403 but JSON reason missing: ${VER_BODY}"
      fi
    else
      fail "Version endpoint for twilkbilk.something: returned ${HTTP_CODE}, expected 403"
    fi
  fi
fi

# ─── Summary ─────────────────────────────────────────────────────────────────
echo ""
echo "========================================="
echo "VSX E2E RESULTS: ${PASS} passed, ${FAIL} failed, ${TESTS} total"
echo "========================================="
if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
