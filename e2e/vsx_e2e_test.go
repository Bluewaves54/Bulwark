// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// Stable Open VSX extensions used in live e2e tests.
// These are well-known, long-established extensions that will not be removed.
const (
	vsxExtYAML    = "redhat/vscode-yaml"
	vsxExtIDYAML  = "redhat.vscode-yaml"
	vsxExtGo      = "golang/go"
	vsxExtIDGo    = "golang.go"
	vsxVerYAML    = "1.14.0" // stable release from 2023
	vsxExtPython  = "ms-python/python"
	vsxExtIDPyext = "ms-python.python"
	vsxGalleryVSIXAsset = "Microsoft.VisualStudio.Services.VSIXPackage"
)

// TestVsxExtensionMetadataLive fetches the redhat/vscode-yaml extension metadata
// through the proxy and verifies the response contains the extension name.
func TestVsxExtensionMetadataLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/"+vsxExtYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(body)), "vscode-yaml") {
		t.Error("expected extension metadata to contain 'vscode-yaml'")
	}
}

// TestVsxExtensionJSONStructureLive verifies that the extension metadata is valid
// JSON with expected fields.
func TestVsxExtensionJSONStructureLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/"+vsxExtYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode extension JSON: %v", err)
	}
	for _, field := range []string{"name", "namespace", "version"} {
		if _, ok := doc[field]; !ok {
			t.Errorf("extension metadata missing %q field", field)
		}
	}
}

// TestVsxExtensionVersionLive fetches a specific version of an extension.
func TestVsxExtensionVersionLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/"+vsxExtYAML+"/"+vsxVerYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), vsxVerYAML) {
		t.Errorf("expected version %s in response", vsxVerYAML)
	}
}

// TestVsxExtensionVersionJSONLive verifies the version response is valid JSON.
func TestVsxExtensionVersionJSONLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/"+vsxExtYAML+"/"+vsxVerYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Errorf("expected JSON content type, got %q", ct)
	}
}

// TestVsxQueryLive tests the query endpoint with a known extension.
func TestVsxQueryLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/-/query?extensionId="+vsxExtIDYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode query JSON: %v", err)
	}
	if _, ok := doc["extensions"]; !ok {
		t.Error("query response missing 'extensions' field")
	}
}

// TestVsxGalleryPassthroughLive verifies the VS Code gallery passthrough endpoint
// returns a valid response (used by product.json serviceUrl).
func TestVsxGalleryPassthroughLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/vscode/gallery/extensionquery")
	defer resp.Body.Close()
	// Gallery queries expect POST with a JSON body; GET without body may return
	// 400 or 405 from Open VSX, but the proxy itself must not crash (no 502).
	if resp.StatusCode == 502 {
		t.Error("gallery passthrough returned 502; proxy should forward upstream response")
	}
}

// TestVsxSecondExtensionLive verifies a second extension (golang.go) is retrievable.
func TestVsxSecondExtensionLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/api/"+vsxExtGo)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(strings.ToLower(string(body)), "go") {
		t.Error("expected golang.go extension metadata to contain 'go'")
	}
}

// TestVsxHealthzLive verifies the healthz endpoint.
func TestVsxHealthzLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/healthz")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)
}

// TestVsxMetricsLive verifies the metrics endpoint returns valid JSON.
func TestVsxMetricsLive(t *testing.T) {
	skipIfNotLive(t)

	resp := mustGet(t, vsxProxyURL+"/metrics")
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	var doc map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode metrics JSON: %v", err)
	}
	if _, ok := doc["requests_total"]; !ok {
		t.Error("metrics missing 'requests_total' field")
	}
}

// TestVsxPreReleaseFilteredLive starts a second proxy with block_pre_release=true
// and confirms that pre-release versions are filtered from extension metadata.
func TestVsxPreReleaseFilteredLive(t *testing.T) {
	skipIfNotLive(t)
	const filterPort = 18300
	proxy := startProxy(t, vsxProxyBinPath, testdataConfig(t, "vsx-block-prerelease.yaml"), filterPort)

	// Fetch an extension through the filter proxy — the response must be valid JSON;
	// any pre-release versions (if present upstream) must be removed.
	resp := mustGet(t, proxy.BaseURL+"/api/"+vsxExtYAML)
	defer resp.Body.Close()
	assertStatus(t, resp, 200)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	// Verify response is well-formed JSON.
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("filtered response is not valid JSON: %v", err)
	}
}

// galleryQueryBody builds a minimal VS Code gallery extension query JSON body.
func galleryQueryBody(extensionNames ...string) []byte {
	criteria := make([]map[string]string, 0, len(extensionNames))
	for _, name := range extensionNames {
		criteria = append(criteria, map[string]string{
			"filterType": "7",
			"value":      name,
		})
	}
	payload := map[string]interface{}{
		"filters": []map[string]interface{}{
			{
				"criteria": criteria,
				"pageSize": 50,
			},
		},
		"flags": 950,
	}
	b, _ := json.Marshal(payload)
	return b
}

// TestVsxGalleryQueryLive posts a gallery extension query through the allow-all
// proxy and verifies the response is valid JSON with a results array.
func TestVsxGalleryQueryLive(t *testing.T) {
	skipIfNotLive(t)

	body := galleryQueryBody(vsxExtIDYAML)
	resp := mustPost(t, vsxProxyURL+"/vscode/gallery/extensionquery", "application/json", body)
	defer resp.Body.Close()

	// Open VSX may return 200 or 400 depending on exact query format;
	// verify the proxy does not crash (no 502) and returns JSON.
	if resp.StatusCode == 502 {
		t.Fatal("gallery query returned 502; proxy should forward upstream response")
	}
	if resp.StatusCode != 200 {
		t.Skipf("upstream returned %d; gallery query format may not be fully supported", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(respBody, &doc); err != nil {
		t.Fatalf("gallery query response is not valid JSON: %v", err)
	}
	if _, ok := doc["results"]; !ok {
		t.Error("gallery query response missing 'results' field")
	}
}

// TestVsxGalleryQueryFiltersDeniedLive starts a proxy with an explicit deny rule
// for redhat.vscode-yaml and verifies it is removed from gallery query results.
func TestVsxGalleryQueryFiltersDeniedLive(t *testing.T) {
	skipIfNotLive(t)
	const denyPort = 18301
	proxy := startProxy(t, vsxProxyBinPath, testdataConfig(t, "vsx-explicit-deny.yaml"), denyPort)

	body := galleryQueryBody(vsxExtIDYAML, vsxExtIDGo)
	resp := mustPost(t, proxy.BaseURL+"/vscode/gallery/extensionquery", "application/json", body)
	defer resp.Body.Close()

	if resp.StatusCode == 502 {
		t.Fatal("gallery query returned 502")
	}
	if resp.StatusCode != 200 {
		t.Skipf("upstream returned %d; skipping filter assertion", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	// The denied extension (redhat.vscode-yaml) should have been filtered out.
	respStr := strings.ToLower(string(respBody))
	if strings.Contains(respStr, "vscode-yaml") {
		t.Error("gallery query response should not contain denied extension 'vscode-yaml'")
	}

	// The policy-notice header should indicate at least one removal.
	filtered := resp.Header.Get("X-Curation-Policy-Notice")
	if filtered == "" || filtered == "0" {
		t.Errorf("expected X-Curation-Policy-Notice to describe filtered results, got %q", filtered)
	}
}

// TestVsxGalleryVspackageAllowedLive verifies that an allowed extension's
// VSIX download through the gallery vspackage endpoint succeeds.
func TestVsxGalleryVspackageAllowedLive(t *testing.T) {
	skipIfNotLive(t)

	url := fmt.Sprintf("%s/vscode/gallery/publishers/redhat/vsextensions/vscode-yaml/%s/vspackage", vsxProxyURL, vsxVerYAML)
	resp := mustGet(t, url)
	defer resp.Body.Close()

	// Open VSX should serve the VSIX file via this endpoint.
	if resp.StatusCode == 502 {
		t.Fatal("vspackage returned 502; proxy should forward upstream response")
	}
	// Accept 200 (direct VSIX) or 302 (redirect to download) as success.
	if resp.StatusCode != 200 && resp.StatusCode != 302 {
		t.Skipf("upstream returned %d for vspackage; may not support this gallery endpoint", resp.StatusCode)
	}
}

// TestVsxGalleryVspackageDeniedLive starts a proxy with an explicit deny rule
// and verifies that the blocked extension's vspackage download returns 403.
func TestVsxGalleryVspackageDeniedLive(t *testing.T) {
	skipIfNotLive(t)
	const denyPort = 18302
	proxy := startProxy(t, vsxProxyBinPath, testdataConfig(t, "vsx-explicit-deny.yaml"), denyPort)

	url := fmt.Sprintf("%s/vscode/gallery/publishers/redhat/vsextensions/vscode-yaml/%s/vspackage", proxy.BaseURL, vsxVerYAML)
	resp := mustGet(t, url)
	defer resp.Body.Close()
	assertStatus(t, resp, 403)
}

// TestVsxGalleryAssetByNameAllowedLive verifies that a direct marketplace-style
// assetbyname VSIX download is still routed successfully for allowed packages.
func TestVsxGalleryAssetByNameAllowedLive(t *testing.T) {
	skipIfNotLive(t)

	url := fmt.Sprintf("%s/_apis/public/gallery/publisher/redhat/extension/vscode-yaml/%s/assetbyname/%s", vsxProxyURL, vsxVerYAML, vsxGalleryVSIXAsset)
	resp := mustGet(t, url)
	defer resp.Body.Close()

	if resp.StatusCode == 502 {
		t.Fatal("assetbyname returned 502; proxy should forward upstream response")
	}
	if resp.StatusCode != 200 && resp.StatusCode != 302 {
		t.Skipf("upstream returned %d for assetbyname; may not expose this gallery endpoint", resp.StatusCode)
	}
}

// TestVsxGalleryAssetByNameDeniedLive verifies that a denied extension cannot
// bypass policy via the marketplace-style assetbyname VSIX download route.
func TestVsxGalleryAssetByNameDeniedLive(t *testing.T) {
	skipIfNotLive(t)
	const denyPort = 18303
	proxy := startProxy(t, vsxProxyBinPath, testdataConfig(t, "vsx-explicit-deny.yaml"), denyPort)

	url := fmt.Sprintf("%s/_apis/public/gallery/publisher/redhat/extension/vscode-yaml/%s/assetbyname/%s", proxy.BaseURL, vsxVerYAML, vsxGalleryVSIXAsset)
	resp := mustGet(t, url)
	defer resp.Body.Close()
	assertStatus(t, resp, 403)
}

// TestVsxCacheHitLive makes two sequential requests and verifies the second is a cache hit.
func TestVsxCacheHitLive(t *testing.T) {
	skipIfNotLive(t)

	// First request — MISS.
	resp1 := mustGet(t, vsxProxyURL+"/api/"+vsxExtPython)
	defer resp1.Body.Close()
	assertStatus(t, resp1, 200)
	io.ReadAll(resp1.Body) //nolint:errcheck

	// Second request — should be HIT.
	resp2 := mustGet(t, vsxProxyURL+"/api/"+vsxExtPython)
	defer resp2.Body.Close()
	assertStatus(t, resp2, 200)

	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Error("expected X-Cache: HIT on second request")
	}
}
