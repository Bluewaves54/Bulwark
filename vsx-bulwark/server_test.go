// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Bulwark/common/config"
)

// testErrorLogger returns a discard-level error logger for test helpers.
func testErrorLogger() *slog.Logger {
	l, _, _ := createLogger("text", "error", "")
	return l
}

const (
	testExtPython  = "ms-python.python"
	testExtEslint  = "dbaeumer.vscode-eslint"
	testExtEvil    = "evil.malware-loader"
	testNsPython   = "ms-python"
	testNsDbaeumer = "dbaeumer"
	testNamePython = "python"
	testNameEslint = "vscode-eslint"
	testVersionOne = "2024.1.1"
	testVersionPre = "2024.2.0-pre.1"
	testVersionOld = "2023.1.0"
	testVersion200 = "2.0.0"

	testPathExtPython     = "/api/ms-python/python"
	testPathExtPythonVer  = "/api/ms-python/python/2024.1.1"
	testPathExtPythonVsix = "/api/ms-python/python/2024.1.1/file/ms-python.python-2024.1.1.vsix"
	testPathReadyz        = "/readyz"

	hdrPolicyNotice = "X-Curation-Policy-Notice"

	testUpstreamURL     = "https://open-vsx.org"
	testUpstreamInvalid = "://invalid"
	testTokenValue      = "my-vsx-token"

	testRuleBlockPre = "block-pre"
	testRuleAge7d    = "age-7d"
	testRuleDeny     = "block-evil"

	testTimeOld2021 = "2021-01-01T00:00:00Z"
	testTimeOld2020 = "2020-01-01T00:00:00Z"
	testTimeRecent  = "2025-03-15T00:00:00Z"

	testFakeVsix      = "fake-vsix-content"
	testFakeVsixShort = "fake-vsix"

	testErrGetExt      = "GET extension: %v"
	testErrFilterExt   = "filterExtensionResponse: %v"
	testErrTempDir     = "TempDir: %v"
	testErrWriteString = "WriteString: %v"
	testErrInitServer  = "initServer: %v"
	testFmtWant200     = "want 200, got %d"
)

func buildTestServer(t *testing.T, upstreamURL string, policy config.PolicyConfig) *testServerResult {
	t.Helper()
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy:   policy,
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return &testServerResult{srv: srv, url: ts.URL}
}

type testServerResult struct {
	srv *Server
	url string
}

func mockExtensionResponse(namespace, name string, versions map[string]string) []byte {
	allVersions := make(map[string]string)
	var latestVersion, latestTimestamp string
	for ver, ts := range versions {
		allVersions[ver] = fmt.Sprintf("https://open-vsx.org/api/%s/%s/%s", namespace, name, ver)
		latestVersion = ver
		latestTimestamp = ts
	}

	resp := map[string]interface{}{
		"namespace":   namespace,
		"name":        name,
		"allVersions": allVersions,
		"version":     latestVersion,
		"timestamp":   latestTimestamp,
		"license":     "MIT",
		"preRelease":  false,
	}
	b, _ := json.Marshal(resp)
	return b
}

func mockVersionResponse(namespace, name, version, timestamp, license string, preRelease bool) []byte {
	resp := map[string]interface{}{
		"namespace":  namespace,
		"name":       name,
		"version":    version,
		"timestamp":  timestamp,
		"license":    license,
		"preRelease": preRelease,
	}
	b, _ := json.Marshal(resp)
	return b
}

// ─── Health / Ready / Metrics ─────────────────────────────────────────────────

func TestVsxHealthEndpoint(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: want 200, got %d", resp.StatusCode)
	}
}

func TestVsxReadyzOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathReadyz)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz: want 200, got %d", resp.StatusCode)
	}
}

func TestVsxReadyzUpstreamDown(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathReadyz)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz down: want 503, got %d", resp.StatusCode)
	}
}

func TestVsxMetrics(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Metrics:  config.MetricsConfig{Enabled: true},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics: want 200, got %d", resp.StatusCode)
	}
}

// ─── Extension metadata (handleExtension) ─────────────────────────────────────

func TestExtensionAllowed(t *testing.T) {
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: testTimeOld2021,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("extension: want 200, got %d", resp.StatusCode)
	}
}

func TestExtensionPreReleaseFiltered(t *testing.T) {
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: testTimeOld2021,
		testVersionPre: testTimeOld2021,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()

	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected X-Curation-Policy-Notice header")
	}
}

func TestExtensionCacheHit(t *testing.T) {
	calls := 0
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: testTimeOld2021,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.url + testPathExtPython)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if calls > 1 {
		t.Errorf("upstream called %d times; want 1 (cache should prevent second call)", calls)
	}
}

func TestExtensionUpstreamError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream 500: want 502, got %d", resp.StatusCode)
	}
}

func TestExtension404FromUpstream(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found"}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("upstream 404: want 404, got %d", resp.StatusCode)
	}
}

func TestExtensionBlockedByDenyRule(t *testing.T) {
	body := mockExtensionResponse("evil", "malware-loader", map[string]string{
		"1.0.0": testTimeOld2021,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/evil/malware-loader")
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("blocked extension: want 403, got %d", resp.StatusCode)
	}
}

func TestExtensionAllVersionsBlocked(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: recent,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("all versions blocked: want 403, got %d", resp.StatusCode)
	}
}

func TestExtensionFailModeClosed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, "not valid json {{{")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{FailMode: config.FailModeClosed}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("fail_mode:closed: want 502, got %d", resp.StatusCode)
	}
}

func TestExtensionFailModeOpen(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, "not valid json {{{")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{FailMode: config.FailModeOpen}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf(testErrGetExt, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fail_mode:open: want 200 (passthrough), got %d", resp.StatusCode)
	}
}

// ─── Version endpoint (handleExtensionVersion) ───────────────────────────────

func TestExtensionVersionAllowed(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("version: want 200, got %d", resp.StatusCode)
	}
}

func TestExtensionVersionBlockedByPackageRule(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtPython}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("package denied version: want 403, got %d", resp.StatusCode)
	}
}

func TestExtensionVersionBlockedByAge(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, recent, "MIT", false)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version age blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("age-blocked version: want 403, got %d", resp.StatusCode)
	}
}

func TestExtensionVersionBlockedByPreRelease(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionPre, testTimeOld2021, "MIT", true)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/ms-python/python/" + testVersionPre)
	if err != nil {
		t.Fatalf("GET pre-release version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("pre-release version: want 403, got %d", resp.StatusCode)
	}
}

func TestExtensionVersionUpstream500(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/2024.1.1") {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version 500: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream 500: want 502, got %d", resp.StatusCode)
	}
}

func TestExtensionVersion404(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found"}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version 404: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("upstream 404: want 404, got %d", resp.StatusCode)
	}
}

func TestExtensionVersionAgeFilterMissingTimestamp(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, "", "MIT", false)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVer)
	if err != nil {
		t.Fatalf("GET version no timestamp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing timestamp with age filter: want 403, got %d", resp.StatusCode)
	}
}

// ─── VSIX download (handleVsixDownload) ──────────────────────────────────────

func TestVsixDownloadAllowed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			w.Header().Set(hdrContentType, "application/octet-stream")
			fmt.Fprint(w, testFakeVsix)
			return
		}
		// Version metadata request.
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPythonVsix)
	if err != nil {
		t.Fatalf("GET vsix: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("vsix download: want 200, got %d", resp.StatusCode)
	}
}

func TestVsixDownloadBlockedByPackageRule(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtPython}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVsix)
	if err != nil {
		t.Fatalf("GET vsix blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vsix package denied: want 403, got %d", resp.StatusCode)
	}
}

func TestVsixDownloadBlockedByVersionAge(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			w.Header().Set(hdrContentType, "application/octet-stream")
			fmt.Fprint(w, testFakeVsix)
			return
		}
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, recent, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVsix)
	if err != nil {
		t.Fatalf("GET vsix age blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vsix age blocked: want 403, got %d", resp.StatusCode)
	}
}

func TestVsixDownloadAgeFilterMissingTimestamp(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			fmt.Fprint(w, testFakeVsix)
			return
		}
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, "", "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPythonVsix)
	if err != nil {
		t.Fatalf("GET vsix no timestamp: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vsix missing timestamp with age filter: want 403, got %d", resp.StatusCode)
	}
}

func TestVsixDownloadUpstreamError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathExtPythonVsix)
	if err != nil {
		t.Fatalf("GET vsix upstream error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("vsix upstream error: want 502, got %d", resp.StatusCode)
	}
}

// ─── Query endpoint (handleQuery) ────────────────────────────────────────────

func TestQueryAllowed(t *testing.T) {
	queryResp := vsxQueryResponse{
		Offset:    0,
		TotalSize: 1,
		Extensions: []vsxQueryExtension{
			{Namespace: testNsPython, Name: testNamePython, Version: testVersionOne, Timestamp: testTimeOld2021},
		},
	}
	body, _ := json.Marshal(queryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/api/-/query?extensionName=python")
	if err != nil {
		t.Fatalf("GET query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("query: want 200, got %d", resp.StatusCode)
	}
}

func TestQueryFiltersDeniedExtension(t *testing.T) {
	queryResp := vsxQueryResponse{
		Offset:    0,
		TotalSize: 2,
		Extensions: []vsxQueryExtension{
			{Namespace: testNsPython, Name: testNamePython, Version: testVersionOne, Timestamp: testTimeOld2021},
			{Namespace: "evil", Name: "malware-loader", Version: "1.0.0", Timestamp: testTimeOld2021},
		},
	}
	body, _ := json.Marshal(queryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/-/query?extensionName=python")
	if err != nil {
		t.Fatalf("GET query filtered: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("query filtered: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected policy notice header for filtered query")
	}

	var result vsxQueryResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if len(result.Extensions) != 1 {
		t.Errorf("filtered query: want 1 extension, got %d", len(result.Extensions))
	}
	if result.TotalSize != 1 {
		t.Errorf("filtered query totalSize: want 1, got %d", result.TotalSize)
	}
}

// ─── Passthrough (handlePassthrough) ─────────────────────────────────────────

func TestSearchPassthrough(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, `{"extensions":[]}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/api/-/search/python")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("search: want 200, got %d", resp.StatusCode)
	}
}

// ─── Helper function tests ──────────────────────────────────────────────────

func TestExtensionID(t *testing.T) {
	cases := []struct {
		ns, name, want string
	}{
		{"ms-python", "python", "ms-python.python"},
		{"MS-Python", "Python", "ms-python.python"},
		{"RedHat", "Java", "redhat.java"},
	}
	for _, tc := range cases {
		got := extensionID(tc.ns, tc.name)
		if got != tc.want {
			t.Errorf("extensionID(%q, %q) = %q, want %q", tc.ns, tc.name, got, tc.want)
		}
	}
}

func TestFilterExtensionResponseInvalidJSON(t *testing.T) {
	_, _, _, err := filterExtensionResponse([]byte("not json"), "test.ext", nil, nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestExtractVersionMeta(t *testing.T) {
	body := mockVersionResponse("ns", "ext", "1.0.0", "2021-06-15T10:00:00Z", "Apache-2.0", false)
	meta := extractVersionMeta(body, "1.0.0")

	if meta.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", meta.Version, "1.0.0")
	}
	if meta.License != "Apache-2.0" {
		t.Errorf("License = %q, want %q", meta.License, "Apache-2.0")
	}
	if meta.PublishedAt.IsZero() {
		t.Error("PublishedAt should not be zero")
	}
}

func TestExtractVersionMetaInvalidJSON(t *testing.T) {
	meta := extractVersionMeta([]byte("not json"), "1.0.0")
	if meta.Version != "1.0.0" {
		t.Errorf("fallback version: want %q, got %q", "1.0.0", meta.Version)
	}
	if !meta.PublishedAt.IsZero() {
		t.Error("PublishedAt should be zero for invalid JSON")
	}
}

func TestExtractVersionMetaEmptyTimestamp(t *testing.T) {
	body := mockVersionResponse("ns", "ext", "1.0.0", "", "MIT", false)
	meta := extractVersionMeta(body, "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("PublishedAt should be zero for empty timestamp")
	}
}

func TestBuildLatestVersionMeta(t *testing.T) {
	vm := buildLatestVersionMeta("1.0.0", "2021-01-01T00:00:00Z", "MIT", false)
	if vm.Version != "1.0.0" {
		t.Errorf("Version = %q, want %q", vm.Version, "1.0.0")
	}
	if vm.License != "MIT" {
		t.Errorf("License = %q, want %q", vm.License, "MIT")
	}
	if vm.PublishedAt.IsZero() {
		t.Error("PublishedAt should not be zero")
	}
}

func TestBuildLatestVersionMetaEmptyTimestamp(t *testing.T) {
	vm := buildLatestVersionMeta("1.0.0", "", "MIT", false)
	if !vm.PublishedAt.IsZero() {
		t.Error("PublishedAt should be zero for empty timestamp")
	}
}

func TestFilterQueryResponseInvalidJSON(t *testing.T) {
	result, removed := filterQueryResponse([]byte("not json"), nil, testErrorLogger())
	if removed != 0 {
		t.Errorf("removed = %d, want 0 for invalid JSON", removed)
	}
	if string(result) != "not json" {
		t.Errorf("expected passthrough for invalid JSON")
	}
}

// ─── Trusted packages ───────────────────────────────────────────────────────

func TestExtensionTrustedPackage(t *testing.T) {
	recent := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: recent,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		TrustedPackages: []string{"ms-python.*"},
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf("GET trusted ext: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("trusted extension: want 200, got %d", resp.StatusCode)
	}
}

// ─── DryRun ─────────────────────────────────────────────────────────────────

func TestExtensionDryRun(t *testing.T) {
	body := mockExtensionResponse("evil", "malware-loader", map[string]string{
		"1.0.0": testTimeOld2021,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/evil/malware-loader")
	if err != nil {
		t.Fatalf("GET dry-run: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run: want 200 (allowed), got %d", resp.StatusCode)
	}
}

func TestExtensionVersionDryRun(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionPre, testTimeOld2021, "MIT", true)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleBlockPre, BlockPreRelease: true},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/ms-python/python/" + testVersionPre)
	if err != nil {
		t.Fatalf("GET dry-run version: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run version: want 200 (allowed), got %d", resp.StatusCode)
	}
}

// ─── Auth ───────────────────────────────────────────────────────────────────

func buildVsxServerWithAuth(t *testing.T, upstreamURL, token, username, password string) (*Server, *httptest.Server) {
	t.Helper()
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{
			URL:            upstreamURL,
			TimeoutSeconds: 5,
			Token:          token,
			Username:       username,
			Password:       password,
		},
		Cache:   config.CacheConfig{TTLSeconds: 60},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger("text", "error", "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestAddUpstreamAuthToken(t *testing.T) {
	var gotAuth string
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{testVersionOne: testTimeOld2021})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	_, ts := buildVsxServerWithAuth(t, mock.URL, testTokenValue, "", "")
	http.Get(ts.URL + testPathExtPython) //nolint:errcheck
	if want := "Bearer " + testTokenValue; gotAuth != want {
		t.Errorf("Authorization: want %q, got %q", want, gotAuth)
	}
}

func TestAddUpstreamAuthBasic(t *testing.T) {
	var gotUser, gotPass string
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{testVersionOne: testTimeOld2021})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	_, ts := buildVsxServerWithAuth(t, mock.URL, "", "vsx-user", "vsx-password")
	http.Get(ts.URL + testPathExtPython) //nolint:errcheck
	if gotUser != "vsx-user" || gotPass != "vsx-password" {
		t.Errorf("BasicAuth: user=%s pass=%s", gotUser, gotPass)
	}
}

// ─── Gallery query filtering (handleGalleryQuery) ───────────────────────────

func mockGalleryQueryResponse(extensions ...struct{ Publisher, Name string }) []byte {
	exts := make([]map[string]interface{}, 0, len(extensions))
	for _, ext := range extensions {
		exts = append(exts, map[string]interface{}{
			"publisher":     map[string]interface{}{"publisherName": ext.Publisher},
			"extensionName": ext.Name,
			"versions":      []interface{}{},
		})
	}
	resp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": exts,
				"resultMetadata": []interface{}{
					map[string]interface{}{
						"metadataType": "ResultCount",
						"metadataItems": []interface{}{
							map[string]interface{}{"name": "TotalCount", "count": len(extensions)},
						},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func TestGalleryQueryFiltersBlockedExtensions(t *testing.T) {
	body := mockGalleryQueryResponse(
		struct{ Publisher, Name string }{"ms-python", "python"},
		struct{ Publisher, Name string }{"evil", "malware-loader"},
	)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected policy notice header for filtered gallery query")
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	results := result["results"].([]interface{})
	firstResult := results[0].(map[string]interface{})
	exts := firstResult["extensions"].([]interface{})
	if len(exts) != 1 {
		t.Errorf("gallery query: want 1 extension after filter, got %d", len(exts))
	}
}

func TestGalleryQueryAllowsAllCleanExtensions(t *testing.T) {
	body := mockGalleryQueryResponse(
		struct{ Publisher, Name string }{"ms-python", "python"},
		struct{ Publisher, Name string }{"redhat", "vscode-yaml"},
	)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) != "" {
		t.Error("no policy notice expected when nothing is filtered")
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	results := result["results"].([]interface{})
	firstResult := results[0].(map[string]interface{})
	exts := firstResult["extensions"].([]interface{})
	if len(exts) != 2 {
		t.Errorf("gallery query: want 2 extensions, got %d", len(exts))
	}
}

func TestGalleryQueryUpstreamError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("gallery query upstream error: want 502, got %d", resp.StatusCode)
	}
}

func TestGalleryQueryUpstreamNonOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("gallery query upstream 500: want 500, got %d", resp.StatusCode)
	}
}

func TestGalleryQueryBadJSON(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, "not-json")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query bad json: %v", err)
	}
	defer resp.Body.Close()

	// Bad JSON from upstream returns the body as-is (graceful degradation).
	if resp.StatusCode != http.StatusOK {
		t.Errorf("gallery query bad json: want 200, got %d", resp.StatusCode)
	}
}

// ─── Gallery VSIX download (handleGalleryVspackage) ─────────────────────────

func TestGalleryVspackageAllowed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "vspackage") {
			w.Header().Set(hdrContentType, "application/octet-stream")
			fmt.Fprint(w, testFakeVsix)
			return
		}
		// Version metadata request.
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("vspackage allowed: want 200, got %d", resp.StatusCode)
	}
}

func TestGalleryVspackageBlockedByDeny(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/evil/vsextensions/malware-loader/1.0.0/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vspackage denied: want 403, got %d", resp.StatusCode)
	}
}

func TestGalleryVspackageBlockedByAge(t *testing.T) {
	recentTime := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return a recent timestamp for version metadata.
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockVersionResponse("somedev", "new-ext", "1.0.0", recentTime, "MIT", false)) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, Action: "deny", MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/somedev/vsextensions/new-ext/1.0.0/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vspackage age blocked: want 403, got %d", resp.StatusCode)
	}
}

func TestGalleryVspackageAgeFailClosed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Return version metadata without timestamp.
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, `{"version":"1.0.0"}`)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		FailMode: "closed",
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, Action: "deny", MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/somedev/vsextensions/new-ext/1.0.0/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("vspackage age fail-closed: want 403, got %d", resp.StatusCode)
	}
}

func TestGalleryVspackageDryRun(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "vspackage") {
			w.Header().Set(hdrContentType, "application/octet-stream")
			fmt.Fprint(w, testFakeVsix)
			return
		}
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtPython}},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	// Dry run: request is allowed despite the deny rule.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("vspackage dry-run: want 200, got %d", resp.StatusCode)
	}
}

// TestGalleryQueryHandlesGzipResponse verifies that handleGalleryQuery correctly
// processes a gzip-compressed upstream response. VS Code sends Accept-Encoding: gzip
// and Open VSX compresses its replies; the proxy must decompress before filtering
// so that VS Code receives valid JSON instead of garbled compressed bytes.
func TestGalleryQueryHandlesGzipResponse(t *testing.T) {
	body := mockGalleryQueryResponse(
		struct{ Publisher, Name string }{testNsPython, testNamePython},
	)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		gz.Write(body) //nolint:errcheck
		gz.Close()     //nolint:errcheck
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(buf.Bytes()) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	req, err := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set(hdrContentType, mimeJSON)
	req.Header.Set("Accept-Encoding", "gzip") // simulates VS Code

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query gzip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query gzip: want 200, got %d", resp.StatusCode)
	}

	// Response must be parseable JSON — compressed bytes would cause decode to fail.
	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Errorf("gallery query gzip: expected valid JSON response, got decode error: %v", err)
	}
	if _, ok := result["results"]; !ok {
		t.Error("gallery query gzip: response missing 'results' field")
	}
}

// TestGalleryQueryAgeFilterBlocksNewExtension verifies that a brand-new extension
// (published < 7 days ago) is removed from gallery extensionquery results when
// all its versions fail the age policy.
// Before the fix, filterGalleryExtension only called EvaluatePackage and
// ignored version-scoped rules such as min_package_age_days.
func TestGalleryQueryAgeFilterBlocksNewExtension(t *testing.T) {
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)

	galleryResp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"publisher":     map[string]string{"publisherName": "cipher-ai"},
						"extensionName": "cipher-security",
						"versions": []interface{}{
							map[string]string{
								"version":     "1.0.0",
								"lastUpdated": recent,
							},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(galleryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected X-Curation-Policy-Notice: new extension must be blocked by age rule in gallery query")
	}
}

// TestGalleryQueryAgeFilterAllowsOldExtension verifies that a well-aged extension
// (published > 7 days ago) is NOT removed from gallery query results.
func TestGalleryQueryAgeFilterAllowsOldExtension(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)

	galleryResp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"publisher":     map[string]string{"publisherName": "trusted-co"},
						"extensionName": "established-tool",
						"versions": []interface{}{
							map[string]string{
								"version":     "2.0.0",
								"lastUpdated": old,
							},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(galleryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) != "" {
		t.Error("old extension must NOT be filtered by age rule in gallery query")
	}
}

// TestGalleryQueryAgeFilterNoTimestampDenied verifies that an extension with no
// version timestamp is blocked (fail-closed) when age filtering is required.
func TestGalleryQueryAgeFilterNoTimestampDenied(t *testing.T) {
	galleryResp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"publisher":     map[string]string{"publisherName": "unknown-co"},
						"extensionName": "mystery-ext",
						"versions": []interface{}{
							map[string]string{
								"version":     "0.1.0",
								"lastUpdated": "",
							},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(galleryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("extension with no timestamp must be blocked (fail-closed) when age filtering is required")
	}
}

// TestGalleryQueryAgeFilterKeepsOldVersions verifies that an extension with a
// mix of old and new versions keeps only the policy-passing (old) versions in
// the gallery response instead of being entirely removed.
func TestGalleryQueryAgeFilterKeepsOldVersions(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)

	galleryResp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"publisher":     map[string]string{"publisherName": "mixed-co"},
						"extensionName": "mixed-ext",
						"versions": []interface{}{
							map[string]interface{}{"version": "2.0.0", "lastUpdated": recent},
							map[string]interface{}{"version": "1.0.0", "lastUpdated": old},
						},
					},
				},
				"resultMetadata": []interface{}{
					map[string]interface{}{
						"metadataType": "ResultCount",
						"metadataItems": []interface{}{
							map[string]interface{}{"name": "TotalCount", "count": 1},
						},
					},
				},
			},
		},
	}
	body, _ := json.Marshal(galleryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery query: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("gallery query: want 200, got %d", resp.StatusCode)
	}

	// Extension should still be present (not fully removed).
	if resp.Header.Get(hdrPolicyNotice) != "" {
		t.Error("extension with a passing version should NOT be entirely filtered")
	}

	// Verify only the old version remains.
	var result map[string]json.RawMessage
	json.Unmarshal(respBody, &result) //nolint:errcheck
	var results []map[string]json.RawMessage
	json.Unmarshal(result["results"], &results) //nolint:errcheck
	var exts []map[string]json.RawMessage
	json.Unmarshal(results[0]["extensions"], &exts) //nolint:errcheck
	if len(exts) != 1 {
		t.Fatalf("want 1 extension, got %d", len(exts))
	}
	var versions []map[string]json.RawMessage
	json.Unmarshal(exts[0]["versions"], &versions) //nolint:errcheck
	if len(versions) != 1 {
		t.Fatalf("want 1 version after filtering, got %d", len(versions))
	}
	var ver string
	json.Unmarshal(versions[0]["version"], &ver) //nolint:errcheck
	if ver != "1.0.0" {
		t.Errorf("want surviving version 1.0.0, got %s", ver)
	}
}

// TestFilterVersionsFailClosedForNonLatest verifies that non-latest versions
// in an Open VSX extension response are denied when age filtering is required
// but their timestamps are absent (Open VSX only provides timestamps for the
// current version via root-level fields).
func TestFilterVersionsFailClosedForNonLatest(t *testing.T) {
	recent := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	// Both the latest and an older version entry are present; the older entry
	// has no timestamp in filterVersions (only latestVersion gets one).
	body := mockExtensionResponse(testNsPython, testNamePython, map[string]string{
		testVersionOne: recent, // latest — blocked by age
		"1.9.0":        recent, // non-latest — no timestamp in filterVersions, must fail-closed
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf("GET extension: %v", err)
	}
	defer resp.Body.Close()
	// All versions blocked → 403.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("all versions blocked (fail-closed for non-latest): want 403, got %d", resp.StatusCode)
	}
}

// ─── handleGalleryExtensionLatest (extensionUrlTemplate endpoint) ─────────────

// TestGalleryExtensionLatestBlockedByAge verifies that a new extension is blocked
// when accessed through the extensionUrlTemplate path that VS Code uses to resolve
// the latest version metadata.
func TestGalleryExtensionLatestBlockedByAge(t *testing.T) {
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	body := mockVersionResponse("cipher-ai", "cipher-security", "1.0.0", recent, "MIT", false)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/cipher-ai/cipher-security/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("new extension via extensionUrlTemplate: want 403, got %d", resp.StatusCode)
	}
}

// TestGalleryExtensionLatestAllowed verifies that a well-aged extension passes
// through the extensionUrlTemplate path.
func TestGalleryExtensionLatestAllowed(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockVersionResponse("trusted-co", "established-tool", testVersion200, old, "MIT", false)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/trusted-co/established-tool/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("old extension via extensionUrlTemplate: want 200, got %d", resp.StatusCode)
	}
}

// TestGalleryExtensionLatestRewritesAssetURLs verifies that the latest-version
// response rewrites asset URLs back through the proxy.
func TestGalleryExtensionLatestRewritesAssetURLs(t *testing.T) {
	body := []byte(`{
		"publisher":{"publisherName":"ms-python"},
		"extensionName":"python",
		"versions":[{
			"version":"2026.5.2026031201",
			"assetUri":"https://ms-python.gallerycdn.vsassets.io/extensions/ms-python/python/2026.5.2026031201/assetbyname",
			"fallbackAssetUri":"https://ms-python.gallerycdn.vsassets.io/extensions/ms-python/python/2026.5.2026031201/assetbyname",
			"files":[
				{"assetType":"Microsoft.VisualStudio.Services.VSIXPackage","source":"https://ms-python.gallerycdn.vsassets.io/extensions/ms-python/python/2026.5.2026031201/Microsoft.VisualStudio.Services.VSIXPackage"}
			]
		}]
	}`)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}

	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/_apis/public/gallery/vscode/ms-python/python/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("latest response: want 200, got %d", resp.StatusCode)
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(respBody)
	if !strings.Contains(bodyStr, ts.URL+"/_apis/public/gallery/publisher/ms-python/extension/python/2026.5.2026031201/assetbyname") {
		t.Fatalf("latest response did not rewrite asset URIs through proxy: %s", bodyStr)
	}
	if strings.Contains(bodyStr, "gallerycdn.vsassets.io") {
		t.Fatalf("latest response still contains upstream CDN URLs: %s", bodyStr)
	}
}

// TestGalleryExtensionLatestBlockedByPackageRule verifies that a denied extension
// is rejected at the package level before contacting the upstream.
func TestGalleryExtensionLatestBlockedByPackageRule(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("upstream should not be contacted for a denied package")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtEvil}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/evil/malware-loader/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("denied extension via extensionUrlTemplate: want 403, got %d", resp.StatusCode)
	}
}

// TestGalleryExtensionLatestFailClosedNoTimestamp verifies that an extension is
// blocked when metadata lacks a parseable timestamp and age filtering is required.
func TestGalleryExtensionLatestFailClosedNoTimestamp(t *testing.T) {
	body := []byte(`{"version":"1.0.0","license":"MIT"}`)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/unknown-co/mystery-ext/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing timestamp fail-closed: want 403, got %d", resp.StatusCode)
	}
}

// TestGalleryExtensionLatestUpstreamError verifies 502 is returned when the
// upstream is unreachable.
func TestGalleryExtensionLatestUpstreamError(t *testing.T) {
	ts := buildTestServer(t, "http://127.0.0.1:1", config.PolicyConfig{})

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/some/ext/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream error: want 502, got %d", resp.StatusCode)
	}
}

// TestGalleryExtensionLatestUpstream404 verifies that a 404 from the upstream is
// forwarded to the client without being transformed.
func TestGalleryExtensionLatestUpstream404(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, err := http.Get(ts.url + "/vscode/gallery/vscode/nonexistent/ext/latest")
	if err != nil {
		t.Fatalf("GET gallery extension latest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("upstream 404: want 404, got %d", resp.StatusCode)
	}
}

// ─── Open VSX query age filtering ─────────────────────────────────────────────

// TestQueryAgeFilterBlocksNew verifies that new extensions are filtered from Open
// VSX query results based on the min_package_age_days rule.
func TestQueryAgeFilterBlocksNew(t *testing.T) {
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	queryResp := vsxQueryResponse{
		Offset:    0,
		TotalSize: 1,
		Extensions: []vsxQueryExtension{
			{Namespace: "cipher-ai", Name: "cipher-security", Version: "1.0.0", Timestamp: recent},
		},
	}
	body, _ := json.Marshal(queryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/-/query?extensionName=cipher-security")
	if err != nil {
		t.Fatalf("GET query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected policy notice: new extension should be filtered by age")
	}
	var result vsxQueryResponse
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	if len(result.Extensions) != 0 {
		t.Errorf("want 0 extensions after age filter, got %d", len(result.Extensions))
	}
}

// TestQueryAgeFilterAllowsOld verifies that well-aged extensions pass through
// Open VSX query filtering.
func TestQueryAgeFilterAllowsOld(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	queryResp := vsxQueryResponse{
		Offset:    0,
		TotalSize: 1,
		Extensions: []vsxQueryExtension{
			{Namespace: testNsPython, Name: testNamePython, Version: testVersionOne, Timestamp: old},
		},
	}
	body, _ := json.Marshal(queryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/-/query?extensionName=python")
	if err != nil {
		t.Fatalf("GET query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) != "" {
		t.Error("old extension must NOT be filtered by age")
	}
}

// TestQueryAgeFilterFailClosed verifies that extensions with no timestamp are
// filtered (fail-closed) when age filtering is required in Open VSX queries.
func TestQueryAgeFilterFailClosed(t *testing.T) {
	queryResp := vsxQueryResponse{
		Offset:    0,
		TotalSize: 1,
		Extensions: []vsxQueryExtension{
			{Namespace: "unknown-co", Name: "mystery-ext", Version: "0.1.0", Timestamp: ""},
		},
	}
	body, _ := json.Marshal(queryResp)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/api/-/query?extensionName=mystery-ext")
	if err != nil {
		t.Fatalf("GET query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query: want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("extension with no timestamp must be filtered (fail-closed) when age rule active")
	}
}
