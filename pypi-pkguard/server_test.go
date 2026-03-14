// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"PKGuard/common/config"
)

const (
	testPkg           = "requests"
	testVersion       = "2.28.0"
	testVersionOld    = "2.25.1"
	testVersionPreRel = "2.29.0a1"

	testPathSimple    = "/simple/"
	testPathPyPIJSON  = "/pypi/requests/json"
	testErrGETSimple  = "GET /simple: %v"
	testErrGET        = "GET: %v"
	testFmtWant200    = "want 200, got %d"
	testMyPackageNorm = "my-package"
	testRuleAge7d     = "age-7d"
	testExtTarGz      = ".tar.gz"
)

// buildTestServer creates a Server with the given upstream URL (from mock) and policy.
func buildTestServer(t *testing.T, upstreamURL string, policy config.PolicyConfig) *testServerResult {
	t.Helper()
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstreamURL, TimeoutSeconds: 5, AllowedExternalHosts: []string{"example.com", "files.pythonhosted.org"}},
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

// mockPyPIJSONResponse returns a minimal PyPI JSON API response.
func mockPyPIJSONResponse(versions map[string]string) []byte {
	type distFile struct {
		UploadTime string `json:"upload_time"`
		Filename   string `json:"filename"`
		URL        string `json:"url"`
		Digests    struct {
			SHA256 string `json:"sha256"`
		} `json:"digests"`
	}
	type resp struct {
		Releases map[string][]distFile `json:"releases"`
	}
	r := resp{Releases: make(map[string][]distFile)}
	for ver, uploadTime := range versions {
		r.Releases[ver] = []distFile{{
			UploadTime: uploadTime,
			Filename:   testPkg + "-" + ver + testExtTarGz,
			URL:        "https://files.pythonhosted.org/packages/" + testPkg + "-" + ver + testExtTarGz,
			Digests: struct {
				SHA256 string `json:"sha256"`
			}{SHA256: "abc123"},
		}}
	}
	b, _ := json.Marshal(r)
	return b
}

func TestHealthEndpoint(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
		t.Errorf("healthz: "+testFmtWant200, resp.StatusCode)
	}
}

func TestReadyzUpstreamOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz: "+testFmtWant200, resp.StatusCode)
	}
}

func TestReadyzUpstreamDown(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz with down upstream: want 503, got %d", resp.StatusCode)
	}
}

func TestSimpleIndexAllowed(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := mockPyPIJSONResponse(map[string]string{testVersionOld: oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "age", MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
	if err != nil {
		t.Fatalf(testErrGETSimple, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("simple index: "+testFmtWant200, resp.StatusCode)
	}
	if resp.Header.Get(hdrXCache) != "MISS" {
		t.Errorf("first request should be cache MISS")
	}
}

func TestSimpleIndexSecondRequestCacheHit(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := mockPyPIJSONResponse(map[string]string{testVersionOld: oldTime})
	callCount := 0

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	if callCount > 1 {
		t.Errorf("upstream called %d times; want 1 (second request should use cache)", callCount)
	}
}

func TestSimpleIndexPreReleaseFiltered(t *testing.T) {
	now := time.Now()
	oldTime := now.AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := mockPyPIJSONResponse(map[string]string{
		testVersionOld:    oldTime,
		testVersionPreRel: oldTime,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "block-pre", BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
	if err != nil {
		t.Fatalf(testErrGETSimple, err)
	}
	defer resp.Body.Close()

	if resp.Header.Get("X-Curation-Policy-Notice") == "" {
		t.Error("expected X-Curation-Policy-Notice header when versions are filtered")
	}
}

func TestSimpleIndexHTMLFormat(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := mockPyPIJSONResponse(map[string]string{testVersionOld: oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodGet, ts.url+testPathSimple+testPkg+"/", nil)
	req.Header.Set("Accept", "text/html")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /simple with Accept text/html: %v", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get(hdrContentType)
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("content-type: want text/html, got %q", ct)
	}
}

func TestSimpleIndexJSONFormat(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := mockPyPIJSONResponse(map[string]string{testVersionOld: oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodGet, ts.url+testPathSimple+testPkg+"/", nil)
	req.Header.Set("Accept", "application/vnd.pypi.simple.v1+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /simple with PEP 691 Accept: %v", err)
	}
	defer resp.Body.Close()
	ct := resp.Header.Get(hdrContentType)
	if ct != "application/vnd.pypi.simple.v1+json" {
		t.Errorf("content-type: want PEP 691 JSON, got %q", ct)
	}
}

func TestSimpleRedirect(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Get(ts.url + testPathSimple + testPkg)
	if err != nil {
		t.Fatalf("GET /simple without trailing slash: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMovedPermanently {
		t.Errorf("redirect: want 301, got %d", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
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
		t.Errorf("metrics: "+testFmtWant200, resp.StatusCode)
	}
}

func TestExternalMissingURL(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/external")
	if err != nil {
		t.Fatalf("GET /external: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("external no url: want 400, got %d", resp.StatusCode)
	}
}

func TestExternalPrivateHost(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/external?url=http://localhost/evil")
	if err != nil {
		t.Fatalf("GET /external localhost: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("external localhost: want 403, got %d", resp.StatusCode)
	}
}

func TestExternalAllowedHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* upstream not used */
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstream.URL, TimeoutSeconds: 5, AllowedExternalHosts: []string{"example.com"}},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy:   config.PolicyConfig{},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	srv.upstream = &http.Client{Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte("external-content")) //nolint:errcheck
		return rr.Result(), nil
	}}}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// Request external URL with allowed host.
	resp, err := http.Get(ts.URL + "/external?url=https://example.com/file.tar.gz")
	if err != nil {
		t.Fatalf("GET /external allowed host: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("external allowed host: want 200, got %d", resp.StatusCode)
	}
}

func TestExternalDeniedHost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* upstream not used */
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstream.URL, TimeoutSeconds: 5, AllowedExternalHosts: []string{"example.com"}},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy:   config.PolicyConfig{},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// Request external URL with host not in allowlist.
	resp, err := http.Get(ts.URL + "/external?url=https://evil.com/malware.tar.gz")
	if err != nil {
		t.Fatalf("GET /external denied host: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("external denied host: want 403, got %d", resp.StatusCode)
	}
}

func TestExternalWildcardAllowed(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* upstream not used */
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstream.URL, TimeoutSeconds: 5, AllowedExternalHosts: []string{"*.pythonhosted.org"}},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy:   config.PolicyConfig{},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	srv.upstream = &http.Client{Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte("external-content")) //nolint:errcheck
		return rr.Result(), nil
	}}}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// Request external URL with host matching wildcard.
	resp, err := http.Get(ts.URL + "/external?url=https://files.pythonhosted.org/packages/file.tar.gz")
	if err != nil {
		t.Fatalf("GET /external wildcard: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("external wildcard: want 403, got %d", resp.StatusCode)
	}
}

func TestExternalEmptyAllowlist(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* upstream not used */
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstream.URL, TimeoutSeconds: 5, AllowedExternalHosts: []string{}},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy:   config.PolicyConfig{},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// Request external URL with empty allowlist (fail-closed).
	resp, err := http.Get(ts.URL + "/external?url=https://example.com/file.tar.gz")
	if err != nil {
		t.Fatalf("GET /external empty allowlist: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("external empty allowlist: want 403, got %d", resp.StatusCode)
	}
}

func TestUpstreamErrorReturns502(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
	if err != nil {
		t.Fatalf(testErrGETSimple, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream 500: want 502, got %d", resp.StatusCode)
	}
}

func TestNormalizePyPIName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Requests", "requests"},
		{"my_package", testMyPackageNorm},
		{"My.Package", testMyPackageNorm},
		{"foo-bar", "foo-bar"},
	}
	for _, tc := range cases {
		got := normalizePyPIName(tc.in)
		if got != tc.want {
			t.Errorf("normalizePyPIName(%q): want %q, got %q", tc.in, tc.want, got)
		}
	}
}

func TestHandlePackageJSONFiltersDeniedVersions(t *testing.T) {
	mockBody := []byte(`{
		"info": {"name": "requests", "license": "MIT"},
		"releases": {
			"2.28.0": [{"upload_time": "2020-01-01T00:00:00", "filename": "requests-2.28.0.tar.gz", "url": "https://example.com/f.tar.gz", "digests": {"sha256": "abc"}}],
			"2.29.0a1": [{"upload_time": "2020-01-01T00:00:00", "filename": "requests-2.29.0a1.tar.gz", "url": "https://example.com/g.tar.gz", "digests": {"sha256": "def"}}]
		}
	}`)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "block-pre", BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathPyPIJSON)
	if err != nil {
		t.Fatalf(testErrGET, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200, resp.StatusCode)
	}

	var result map[string]json.RawMessage
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var releases map[string]json.RawMessage
	if err = json.Unmarshal(result["releases"], &releases); err != nil {
		t.Fatalf("unmarshal releases: %v", err)
	}
	if _, ok := releases[testVersionPreRel]; ok {
		t.Error("pre-release " + testVersionPreRel + " should have been filtered from /pypi/pkg/json")
	}
	if _, ok := releases[testVersion]; !ok {
		t.Error("stable " + testVersion + " should still be present")
	}
}

func TestHandlePackageJSONPackageDenied(t *testing.T) {
	mockBody := []byte(`{
		"info": {"name": "requests"},
		"releases": {
			"1.0.0": [{"upload_time": "2020-01-01T00:00:00"}]
		}
	}`)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "deny-pkg", PackagePatterns: []string{testPkg}, Action: "deny"},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathPyPIJSON)
	if err != nil {
		t.Fatalf(testErrGET, err)
	}
	defer resp.Body.Close()
	// Package-level deny should return 403.
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("package denied: want 403, got %d", resp.StatusCode)
	}
}

func TestExtractPkgVersionFromFilename(t *testing.T) {
	cases := []struct {
		filename, wantPkg, wantVer string
	}{
		{"requests-" + testVersion + testExtTarGz, "requests", testVersion},
		{"requests-" + testVersion + "-py3-none-any.whl", "requests", testVersion},
		{testMyPackageNorm + "-1.0.0" + testExtTarGz, testMyPackageNorm, "1.0.0"},
		{"Flask-2.0.0.zip", "Flask", "2.0.0"},
		{"unknown", "", ""},
		{"nodash" + testExtTarGz, "", ""},
	}
	for _, tc := range cases {
		pkg, ver := extractPkgVersionFromFilename(tc.filename)
		if pkg != tc.wantPkg || ver != tc.wantVer {
			t.Errorf("extractPkgVersionFromFilename(%q): got (%q, %q), want (%q, %q)",
				tc.filename, pkg, ver, tc.wantPkg, tc.wantVer)
		}
	}
}

// TestSimpleIndexAgeBlockFiltersRecentVersions verifies that recent versions
// (published after cutoff) are removed from the simple index by the age rule.
func TestSimpleIndexAgeBlockFiltersRecentVersions(t *testing.T) {
	recentTime := time.Now().AddDate(0, 0, -1).Format(pypiTimeFmt) // 1 day old
	mockBody := mockPyPIJSONResponse(map[string]string{
		testVersion:    recentTime,
		testVersionOld: recentTime,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
	if err != nil {
		t.Fatalf(testErrGETSimple, err)
	}
	defer resp.Body.Close()
	// Both versions are 1 day old; with 7-day rule, both should be blocked.
	// The proxy should return 403 since all versions are blocked.
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("age block all versions: want 403, got %d", resp.StatusCode)
	}
}

// TestSimpleIndexAgeBlockKeepsOldVersionsRemovesNew verifies that old versions
// pass the age filter while recent ones are removed from the simple index.
func TestSimpleIndexAgeBlockKeepsOldVersionsRemovesNew(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt) // 30 days old
	newTime := time.Now().AddDate(0, 0, -1).Format(pypiTimeFmt)  // 1 day old
	mockBody := mockPyPIJSONResponse(map[string]string{
		testVersionOld: oldTime,
		testVersion:    newTime,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathSimple + testPkg + "/")
	if err != nil {
		t.Fatalf(testErrGETSimple, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	bodyStr := string(body)

	// testVersionOld (30 days old) should survive; testVersion (1 day old) should be removed.
	if !strings.Contains(bodyStr, testVersionOld) {
		t.Error("age block: old version " + testVersionOld + " should remain in simple index")
	}
	if strings.Contains(bodyStr, testVersion) {
		t.Error("age block: recent version " + testVersion + " should be removed from simple index")
	}
}

// TestPackageJSONAgeBlockFiltersRecentVersions verifies that /pypi/<pkg>/json
// endpoint also applies the age filter and removes recent releases.
func TestPackageJSONAgeBlockFiltersRecentVersions(t *testing.T) {
	recentTime := time.Now().AddDate(0, 0, -1).Format(pypiTimeFmt)
	oldTime := time.Now().AddDate(0, 0, -30).Format(pypiTimeFmt)
	mockBody := []byte(fmt.Sprintf(`{
		"info": {"name": "requests", "license": "MIT"},
		"releases": {
			"2.25.1": [{"upload_time": "%s", "filename": "requests-2.25.1.tar.gz", "url": "https://example.com/f.tar.gz", "digests": {"sha256": "abc"}}],
			"2.28.0": [{"upload_time": "%s", "filename": "requests-2.28.0.tar.gz", "url": "https://example.com/g.tar.gz", "digests": {"sha256": "def"}}]
		}
	}`, oldTime, recentTime))

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + testPathPyPIJSON)
	if err != nil {
		t.Fatalf(testErrGET, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200, resp.StatusCode)
	}

	var result map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	var releases map[string]json.RawMessage
	json.Unmarshal(result["releases"], &releases) //nolint:errcheck

	if _, ok := releases[testVersionOld]; !ok {
		t.Error("age block: old version " + testVersionOld + " should remain in JSON releases")
	}
	if _, ok := releases[testVersion]; ok {
		t.Error("age block: recent version " + testVersion + " should be removed from JSON releases")
	}
}
