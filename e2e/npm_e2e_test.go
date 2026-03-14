// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// Stable npm packages used in live e2e tests.
const (
	npmPkgLodash  = "lodash"
	npmVerLodash  = "4.17.21"
	npmPkgMs      = "ms"
	npmPkgScoped  = "@types/node"
	npmPkgEsbuild = "esbuild"
	npmVerEsbuild = "0.19.12"
)

// TestNpmPackumentLive fetches the lodash packument through the proxy and verifies
// that the stable release is present.
func TestNpmPackumentLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, npmProxyURL+"/"+npmPkgLodash)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), npmVerLodash) {
		t.Errorf("expected lodash %s in packument, not found", npmVerLodash)
	}
}

// TestNpmPackumentJSONStructureLive verifies that the returned packument is valid JSON
// with the expected "name" field.
func TestNpmPackumentJSONStructureLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, npmProxyURL+"/"+npmPkgLodash)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode packument JSON: %v", err)
	}
	if _, ok := doc["name"]; !ok {
		t.Error("packument missing 'name' field")
	}
	if _, ok := doc["versions"]; !ok {
		t.Error("packument missing 'versions' field")
	}
}

// TestNpmScopedPackumentLive fetches a scoped package packument and verifies it is valid.
func TestNpmScopedPackumentLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, npmProxyURL+"/"+npmPkgScoped)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") && !strings.Contains(ct, "application/vnd.npm.install-v1+json") {
		t.Errorf("unexpected content type for packument: %q", ct)
	}
}

// TestNpmPreReleaseFilteredLive starts a second proxy with block_pre_release=true
// and confirms that pre-release versions are absent from a packument.
func TestNpmPreReleaseFilteredLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18201
	proxy := startProxy(t, npmProxyBinPath, testdataConfig(t, "npm-block-prerelease.yaml"), filterPort)

	// ms has beta/alpha releases that should be removed.
	resp := mustGet(t, proxy.BaseURL+"/"+npmPkgMs)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := strings.ToLower(string(body))
	for _, pre := range []string{"-beta.", "-alpha.", "-rc."} {
		if strings.Contains(bodyStr, pre) {
			t.Errorf("filtered packument still contains pre-release marker %q", pre)
		}
	}
}

// TestNpmMsPackumentLive verifies that the ms package is retrievable end to end.
func TestNpmMsPackumentLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, npmProxyURL+"/"+npmPkgMs)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)
}

// TestNpmAgeBlockFiltersAllVersionsLive starts a proxy with min_package_age_days=10000
// for the ms package and verifies that the proxy returns 403 when ALL versions are blocked.
// With a 10000-day minimum age no version of ms qualifies, so the proxy returns
// a structured 403 error with a policy reason.
func TestNpmAgeBlockFiltersAllVersionsLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18204
	proxy := startProxy(t, npmProxyBinPath, testdataConfig(t, "npm-min-age-block.yaml"), filterPort)

	resp := mustGet(t, proxy.BaseURL+"/"+npmPkgMs)
	defer resp.Body.Close()
	assertStatus(t, resp, 403)

	// Body should contain a policy reason.
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PKGuard") {
		t.Error("expected 403 body to contain PKGuard policy reason")
	}
}

// TestNpmInstallScriptsBlockLive starts a proxy with install_scripts deny
// and confirms that esbuild (has postinstall) versions are filtered from the
// packument while lodash (no scripts) versions remain.
func TestNpmInstallScriptsBlockLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18206
	proxy := startProxy(t, npmProxyBinPath, testdataConfig(t, "npm-install-scripts-block.yaml"), filterPort)

	// esbuild has install scripts on many versions. The filtered proxy should
	// return fewer versions than the allow-all proxy.
	resp := mustGet(t, proxy.BaseURL+"/"+npmPkgEsbuild)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var filteredDoc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&filteredDoc); err != nil {
		t.Fatalf("decode packument: %v", err)
	}

	var filteredVersions map[string]json.RawMessage
	if versionsRaw, ok := filteredDoc["versions"]; ok {
		if err := json.Unmarshal(versionsRaw, &filteredVersions); err != nil {
			t.Fatalf("decode filtered versions: %v", err)
		}
	} else {
		t.Fatal("filtered esbuild packument missing versions field")
	}

	respAll := mustGet(t, npmProxyURL+"/"+npmPkgEsbuild)
	defer respAll.Body.Close()
	assertStatus(t, respAll, 200)

	var allDoc map[string]json.RawMessage
	if err := json.NewDecoder(respAll.Body).Decode(&allDoc); err != nil {
		t.Fatalf("decode allow-all packument: %v", err)
	}

	var allVersions map[string]json.RawMessage
	if versionsRaw, ok := allDoc["versions"]; ok {
		if err := json.Unmarshal(versionsRaw, &allVersions); err != nil {
			t.Fatalf("decode allow-all versions: %v", err)
		}
	} else {
		t.Fatal("allow-all esbuild packument missing versions field")
	}

	if len(filteredVersions) >= len(allVersions) {
		t.Fatalf("expected script filter to remove at least one esbuild version; filtered=%d allow-all=%d", len(filteredVersions), len(allVersions))
	}

	// When the known script-bearing version is present upstream, it must be removed.
	if _, existsInAll := allVersions[npmVerEsbuild]; existsInAll {
		if _, existsFiltered := filteredVersions[npmVerEsbuild]; existsFiltered {
			t.Errorf("expected esbuild %s to be filtered due to install scripts", npmVerEsbuild)
		}
	}

	// lodash has no install scripts — versions should remain.
	resp2 := mustGet(t, proxy.BaseURL+"/"+npmPkgLodash)
	defer resp2.Body.Close()
	assertStatus(t, resp2, 200)

	body, err := io.ReadAll(resp2.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), npmVerLodash) {
		t.Errorf("lodash %s should remain after install scripts filter", npmVerLodash)
	}
}

// TestNpmTrustedPackageBypassesAllRulesLive starts a proxy where everything is
// blocked (age 10000 + pre-release block) but @types/* is trusted. The trusted
// scoped package should still have versions while ms (untrusted) should not.
func TestNpmTrustedPackageBypassesAllRulesLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18207
	proxy := startProxy(t, npmProxyBinPath, testdataConfig(t, "npm-trusted-packages.yaml"), filterPort)

	// @types/node is trusted — should have versions despite block-everything rule.
	resp := mustGet(t, proxy.BaseURL+"/"+npmPkgScoped)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode packument: %v", err)
	}
	if versionsRaw, ok := doc["versions"]; ok {
		var versions map[string]json.RawMessage
		if err := json.Unmarshal(versionsRaw, &versions); err == nil && len(versions) == 0 {
			t.Error("trusted @types/node should have versions despite block-everything rule")
		}
	}

	// ms is NOT trusted — all versions blocked by age rule → 403.
	resp2 := mustGet(t, proxy.BaseURL+"/"+npmPkgMs)
	defer resp2.Body.Close()
	assertStatus(t, resp2, 403)

	body2, _ := io.ReadAll(resp2.Body)
	if !strings.Contains(string(body2), "PKGuard") {
		t.Error("expected 403 body to contain PKGuard policy reason for untrusted ms")
	}
}
