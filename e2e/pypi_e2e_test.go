// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"io"
	"strings"
	"testing"
)

// Stable PyPI packages used in live e2e tests.
// These are ancient releases that will never be yanked.
const (
	pypiPkgPip     = "pip"
	pypiVerPip     = "22.3.1"
	pypiPkgCertifi = "certifi"
	pypiPkgUrllib3 = "urllib3"
)

// TestPyPISimpleIndexLive fetches the /simple/pip/ page through the proxy and
// verifies that the stable release is present in the response.
func TestPyPISimpleIndexLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, pypiProxyURL+"/simple/"+pypiPkgPip+"/")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), pypiVerPip) {
		t.Errorf("expected pip %s in simple index, not found in response", pypiVerPip)
	}
}

// TestPyPIPackageJSONLive fetches /pypi/pip/json through the proxy and checks
// that the stable version is present in the response body.
func TestPyPIPackageJSONLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, pypiProxyURL+"/pypi/"+pypiPkgPip+"/json")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), pypiVerPip) {
		t.Errorf("expected pip %s in JSON response", pypiVerPip)
	}
}

// TestPyPIPreReleaseFilteredLive starts a second proxy with block_pre_release=true
// and confirms that pre-release file links are absent from the simple index.
func TestPyPIPreReleaseFilteredLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18200
	proxy := startProxy(t, pypiProxyBinPath, testdataConfig(t, "pypi-block-prerelease.yaml"), filterPort)

	// urllib3 has known rc releases (e.g. 1.26.0rc1, 1.26.0rc2) that should be absent after filtering.
	resp := mustGet(t, proxy.BaseURL+"/simple/"+pypiPkgUrllib3+"/")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := strings.ToLower(string(body))
	// 1.26.0rc1 is a known pre-release that must not appear in the filtered response.
	if strings.Contains(bodyStr, "1.26.0rc1") {
		t.Error("filtered response still contains urllib3 pre-release 1.26.0rc1")
	}
	if resp.Header.Get("X-Curation-Policy-Notice") == "" {
		t.Error("expected X-Curation-Policy-Notice header when versions are filtered")
	}
}

// TestPyPIPEP691JSONFormatLive sends an Accept header requesting the PEP 691
// JSON format and verifies the proxy returns JSON content.
func TestPyPIPEP691JSONFormatLive(t *testing.T) {
	skipIfNotLive(t)

	const pep691Accept = "application/vnd.pypi.simple.v1+json"
	resp := mustGetWithHeader(t, pypiProxyURL+"/simple/"+pypiPkgUrllib3+"/", "Accept", pep691Accept)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/vnd.pypi.simple") && !strings.Contains(ct, "application/json") {
		t.Errorf("expected PEP 691 JSON content type, got %q", ct)
	}
}

// TestPyPICertifiSimpleIndexLive verifies that certifi is retrievable end to end.
func TestPyPICertifiSimpleIndexLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, pypiProxyURL+"/simple/"+pypiPkgCertifi+"/")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)
}

// TestPyPIAgeBlockFiltersAllVersionsLive starts a proxy with min_package_age_days=10000
// for urllib3 and verifies that the proxy returns 403 when ALL versions are blocked.
// With a 10000-day minimum age no urllib3 version qualifies, so the proxy returns
// a 403 error with a policy reason.
func TestPyPIAgeBlockFiltersAllVersionsLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18205
	proxy := startProxy(t, pypiProxyBinPath, testdataConfig(t, "pypi-min-age-block.yaml"), filterPort)

	resp := mustGet(t, proxy.BaseURL+"/simple/"+pypiPkgUrllib3+"/")
	defer resp.Body.Close()
	assertStatus(t, resp, 403)

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "PKGuard") {
		t.Error("expected 403 body to contain PKGuard policy reason")
	}
}
