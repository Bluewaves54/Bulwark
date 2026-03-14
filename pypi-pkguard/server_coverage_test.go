// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"PKGuard/common/config"
)

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}

// ─── Utility helpers ─────────────────────────────────────────────────────────

const (
	testPyPIToken    = "test-token-abc"
	testPyPIUser     = "pypi-user"
	testPyPIPassword = "pypi-pass"

	testConfigPattern   = "config-*.yaml"
	testErrTempDir      = "TempDir: %v"
	testErrWriteString  = "WriteString: %v"
	testPathPyPIJSONCov = "/pypi/requests/json"
	testErrGETCov       = "GET: %v"
	testFmtWant200Cov   = "want 200, got %d"
	testRuleBlockPre    = "block-pre"
	testPathReadyz      = "/readyz"
	testTimeOld2020     = "2020-01-01T00:00:00"
	testPathSimpleCov   = "/simple/"
	testErrInitServer   = "initServer: %v"
	testTokenOverride   = "override-token"
	testUpstreamInvalid = "://invalid"
)

func TestAddrFromPortPyPI(t *testing.T) {
	if got := addrFromPort(9090); got != ":9090" {
		t.Errorf("addrFromPort(9090) = %q, want \":9090\"", got)
	}
}

func TestCreateLoggerTextInfo(t *testing.T) {
	if l := createLogger("text", "info"); l == nil {
		t.Error("expected non-nil logger for text/info")
	}
}

func TestCreateLoggerJSONDebug(t *testing.T) {
	if l := createLogger("json", "debug"); l == nil {
		t.Error("expected non-nil logger for json/debug")
	}
}

func TestCreateLoggerWarnLevel(t *testing.T) {
	if l := createLogger("text", "warn"); l == nil {
		t.Error("expected non-nil logger for text/warn")
	}
}

func TestCreateLoggerErrorLevel(t *testing.T) {
	if l := createLogger("text", "error"); l == nil {
		t.Error("expected non-nil logger for text/error")
	}
}

func TestCreateLoggerUnknownLevel(t *testing.T) {
	if l := createLogger("text", "verbose"); l == nil {
		t.Error("expected non-nil logger for unknown level")
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := loadConfig("/nonexistent/path/to/config.yaml")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestLoadConfigValidFile(t *testing.T) {
	yaml := `
upstream:
  url: "https://pypi.org"
server:
  port: 9000
`
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(yaml); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	cfg, err := loadConfig(f.Name())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Upstream.URL != "https://pypi.org" {
		t.Errorf("Upstream.URL = %q, want https://pypi.org", cfg.Upstream.URL)
	}
}

func TestApplyFlagOverridesToken(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, testPyPIToken, "", "")
	if cfg.Upstream.Token != testPyPIToken {
		t.Errorf("Token = %q, want %q", cfg.Upstream.Token, testPyPIToken)
	}
}

func TestApplyFlagOverridesBasicAuth(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, "", testPyPIUser, testPyPIPassword)
	if cfg.Upstream.Username != testPyPIUser {
		t.Errorf("Username = %q, want %q", cfg.Upstream.Username, testPyPIUser)
	}
	if cfg.Upstream.Password != testPyPIPassword {
		t.Errorf("Password = %q, want %q", cfg.Upstream.Password, testPyPIPassword)
	}
}

func TestApplyFlagOverridesEmpty(t *testing.T) {
	cfg := &config.Config{Upstream: config.UpstreamConfig{Token: "original"}}
	applyFlagOverrides(cfg, "", "", "")
	if cfg.Upstream.Token != "original" {
		t.Error("empty overrides should not change existing token")
	}
}

// ─── handlePackageJSON ────────────────────────────────────────────────────────

func TestHandlePackageJSONSuccess(t *testing.T) {
	const body = `{"info":{"name":"requests"},"releases":{}}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	// Cache MISS on first request.
	resp, err := http.Get(ts.url + testPathPyPIJSONCov)
	if err != nil {
		t.Fatalf(testErrGETCov, err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("first request: "+testFmtWant200Cov, resp.StatusCode)
	}
	if got := resp.Header.Get(hdrXCache); got != "MISS" {
		t.Errorf("first request X-Cache: want MISS, got %q", got)
	}

	// Cache HIT on second request.
	resp2, err := http.Get(ts.url + testPathPyPIJSONCov)
	if err != nil {
		t.Fatalf("GET 2: %v", err)
	}
	resp2.Body.Close()
	if got := resp2.Header.Get(hdrXCache); got != "HIT" {
		t.Errorf("second request X-Cache: want HIT, got %q", got)
	}
}

func TestHandlePackageJSONNonOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/pypi/nonexistent-pkg/json")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestHandlePackageJSONUpstreamError(t *testing.T) {
	// Start then immediately close the upstream so the connection fails.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + testPathPyPIJSONCov)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── handleExternal ──────────────────────────────────────────────────────────

func TestHandleExternalMissingURL(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for missing url, got %d", resp.StatusCode)
	}
}

func TestHandleExternalPrivateHost(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external?url=http://localhost/secret")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for private host, got %d", resp.StatusCode)
	}
}

func TestHandleExternal10xPrivateHost(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external?url=http://10.0.0.1/secret")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for 10.x RFC-1918 address, got %d", resp.StatusCode)
	}
}

func TestHandleExternal192168PrivateHost(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external?url=http://192.168.1.100/secret")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for 192.168.x address, got %d", resp.StatusCode)
	}
}

func TestHandleExternal172PrivateHost(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external?url=http://172.16.0.1/secret")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for 172.x address, got %d", resp.StatusCode)
	}
}

func TestHandleExternalInvalidScheme(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	resp, _ := http.Get(ts.url + "/external?url=ftp://example.com/file.tar.gz")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for ftp scheme, got %d", resp.StatusCode)
	}
}

func TestHandleExternalSuccess(t *testing.T) {
	const content = "fake-tarball-bytes"
	// Use a custom transport so the upstream call can be intercepted for any URL,
	// including non-private hostnames used in the test URL query param.
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.Header().Set(hdrContentType, "application/octet-stream")
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte(content)) //nolint:errcheck
		return rr.Result(), nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, err := http.Get(ts.url + "/external?url=https://example.com/pkg-1.0.tar.gz")
	if err != nil {
		t.Fatalf(testErrGETCov, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200Cov, resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), content) {
		t.Errorf("body does not contain expected content")
	}
}

func TestHandleExternalUpstreamError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		return nil, io.ErrUnexpectedEOF
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, _ := http.Get(ts.url + "/external?url=https://example.com/pkg.tar.gz")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// ─── handleExternal rule enforcement ─────────────────────────────────────────

func TestHandleExternalBlockedPreRelease(t *testing.T) {
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte("bytes")) //nolint:errcheck
		return rr.Result(), nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{
		Rules: []config.PackageRule{{Name: testRuleBlockPre, BlockPreRelease: true}},
	})
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, _ := http.Get(ts.url + "/external?url=https://example.com/packages/requests-2.29.0a1.tar.gz")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for pre-release via external, got %d", resp.StatusCode)
	}
}

func TestHandleExternalPackageDenied(t *testing.T) {
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte("bytes")) //nolint:errcheck
		return rr.Result(), nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{{
			Name:            "deny-evil",
			PackagePatterns: []string{"evil-pkg"},
			Action:          "deny",
			Reason:          "blocked by test",
		}},
	}
	ts := buildTestServer(t, mock.URL, policy)
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, _ := http.Get(ts.url + "/external?url=https://example.com/packages/evil-pkg-1.0.0.tar.gz")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied package via external, got %d", resp.StatusCode)
	}
}

func TestHandleExternalFailOpenUnparseable(t *testing.T) {
	const content = "file-content"
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.Header().Set(hdrContentType, "application/octet-stream")
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte(content)) //nolint:errcheck
		return rr.Result(), nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{
		Rules: []config.PackageRule{{Name: testRuleBlockPre, BlockPreRelease: true}},
	})
	ts.srv.upstream = &http.Client{Transport: transport}

	// Filename without a version — extraction fails, should pass through (fail-open).
	resp, err := http.Get(ts.url + "/external?url=https://example.com/packages/noversion.tar.gz")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for unparseable filename (fail-open), got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), content) {
		t.Errorf("body does not contain expected content")
	}
}

// ─── evaluateExternalURL unit tests ──────────────────────────────────────────

func TestEvaluateExternalURLNoSlash(t *testing.T) {
	ts := buildTestServer(t, "http://unused", config.PolicyConfig{})
	if dec := ts.srv.evaluateExternalURL("noslash"); dec != nil {
		t.Errorf("expected nil decision for URL without slash, got %+v", dec)
	}
}

func TestEvaluateExternalURLQueryStripped(t *testing.T) {
	ts := buildTestServer(t, "http://unused", config.PolicyConfig{
		Rules: []config.PackageRule{{Name: testRuleBlockPre, BlockPreRelease: true}},
	})
	// Pre-release in filename with query params — should still be caught.
	dec := ts.srv.evaluateExternalURL("https://example.com/requests-2.0.0a1.tar.gz?sig=abc")
	if dec == nil {
		t.Error("expected deny decision for pre-release URL with query params")
	}
}

// ─── addUpstreamAuth (via PackageJSON) ───────────────────────────────────────

func TestAddUpstreamAuthTokenPyPI(t *testing.T) {
	var gotAuth string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write([]byte(`{"releases":{}}`)) //nolint:errcheck
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5, Token: testPyPIToken},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger := createLogger(cfg.Logging.Format, cfg.Logging.Level)
	srv, _ := buildServer(cfg, logger)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	http.Get(ts.URL + testPathPyPIJSONCov) //nolint:errcheck
	if want := "Bearer " + testPyPIToken; gotAuth != want {
		t.Errorf("Authorization: want %q, got %q", want, gotAuth)
	}
}

func TestAddUpstreamAuthBasicPyPI(t *testing.T) {
	var gotUser, gotPass string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write([]byte(`{"releases":{}}`)) //nolint:errcheck
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{
			URL:            mock.URL,
			TimeoutSeconds: 5,
			Username:       testPyPIUser,
			Password:       testPyPIPassword,
		},
		Cache:   config.CacheConfig{TTLSeconds: 60},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger := createLogger(cfg.Logging.Format, cfg.Logging.Level)
	srv, _ := buildServer(cfg, logger)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	http.Get(ts.URL + testPathPyPIJSONCov) //nolint:errcheck
	if gotUser != testPyPIUser || gotPass != testPyPIPassword {
		t.Errorf("BasicAuth: want %s/%s, got %s/%s", testPyPIUser, testPyPIPassword, gotUser, gotPass)
	}
}

// ─── buildServer with TLS insecure flag ──────────────────────────────────────

func TestBuildServerInsecureTLS(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5, TLS: config.TLSConfig{InsecureSkipVerify: true}},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	_, err := buildServer(cfg, createLogger("text", "error"))
	if err != nil {
		t.Fatalf("buildServer with InsecureSkipVerify=true: %v", err)
	}
}

// ─── handleReady 503 path ────────────────────────────────────────────────────

func TestHandleReadyUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/readyz")
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestHandleReadyUpstream5xx(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 when upstream returns 5xx, got %d", resp.StatusCode)
	}
}

// ─── applyPortEnvOverride ─────────────────────────────────────────────────────

func TestApplyPortEnvOverride(t *testing.T) {
	// This is a no-op; cover it for completeness.
	cfg := &config.Config{Server: config.ServerConfig{Port: 8080}}
	applyPortEnvOverride(cfg) // must not panic
}

// ─── filterVersions: package-level deny ──────────────────────────────────────

func TestHandleSimplePackageLevelDeny(t *testing.T) {
	mockBody := mockPyPIJSONResponse(map[string]string{
		"1.0.0": testTimeOld2020,
	})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "denial", PackagePatterns: []string{testPkg}, Action: "deny"},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200Cov, resp.StatusCode)
	}
	resp.Body.Close()
	// Stats counter for denied should have incremented.
	if ts.srv.reqDenied.Load() == 0 {
		t.Error("expected reqDenied counter to increment for package-level deny")
	}
}

// ─── filterVersions: dry-run ─────────────────────────────────────────────────

func TestHandleSimpleDryRun(t *testing.T) {
	mockBody := mockPyPIJSONResponse(map[string]string{
		testVersionPreRel: "2024-06-01T00:00:00",
		testVersion:       testTimeOld2020,
	})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleBlockPre, BlockPreRelease: true},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200Cov+" for dry-run", resp.StatusCode)
	}
	resp.Body.Close()
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment in dry-run mode")
	}
}

func TestHandleSimpleLicenseDenied(t *testing.T) {
	mockBody := []byte(`{
		"info": {"license": "GPL-3.0"},
		"releases": {
			"1.0.0": [{
				"upload_time": "2020-01-01T00:00:00",
				"filename": "requests-1.0.0.tar.gz",
				"url": "https://files.pythonhosted.org/packages/requests-1.0.0.tar.gz",
				"digests": {"sha256": "abc123"}
			}]
		}
	}`)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{{
		Name:           "deny-gpl",
		DeniedLicenses: []string{"GPL-3.0"},
	}}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200Cov, resp.StatusCode)
	}
	resp.Body.Close()
	if ts.srv.reqDenied.Load() == 0 {
		t.Error("expected reqDenied counter to increment for denied license")
	}
}

// ─── fetchPyPIMeta: non-200 non-404 upstream response ─────────────────────────

func TestHandleSimpleUpstreamNon200(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable) // 503 → not OK and not 404
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream 503, got %d", resp.StatusCode)
	}
}

// ─── handleSimple: JSON format ────────────────────────────────────────────────

func TestHandleSimpleJSONFormat(t *testing.T) {
	mockBody := mockPyPIJSONResponse(map[string]string{
		testVersion: testTimeOld2020,
	})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockBody) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	req, _ := http.NewRequest(http.MethodGet, ts.url+testPathSimpleCov+testPkg+"/", nil)
	req.Header.Set("Accept", "application/vnd.pypi.simple.v1+json")
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(testFmtWant200Cov, resp.StatusCode)
	}
	if ct := resp.Header.Get(hdrContentType); ct != "application/vnd.pypi.simple.v1+json" {
		t.Errorf("Content-Type: want application/vnd.pypi.simple.v1+json, got %q", ct)
	}
	resp.Body.Close()
}

// ─── initServer ───────────────────────────────────────────────────────────────

const testPyPIConfigYAML = `upstream:
  url: "https://pypi.org"
server:
  port: 9000
`

func TestInitServerSuccess(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testPyPIConfigYAML); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, logger, err := initServer(f.Name(), "", "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	if srv == nil {
		t.Error("expected non-nil server")
	}
	if logger == nil {
		t.Error("expected non-nil logger")
	}
}

func TestInitServerMissingConfig(t *testing.T) {
	_, _, err := initServer("/nonexistent/path/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestInitServerWithTokenOverride(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testPyPIConfigYAML); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, _, err := initServer(f.Name(), testTokenOverride, "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	if srv.cfg.Upstream.Token != testTokenOverride {
		t.Errorf("Token override: want %q, got %q", testTokenOverride, srv.cfg.Upstream.Token)
	}
}

// ─── handleReady: invalid upstream URL (NewRequest error path) ────────────────

func TestHandleReadyInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// ─── handlePackageJSON: error paths ──────────────────────────────────────────

func TestHandlePackageJSONInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathPyPIJSONCov)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// errorBody is an io.ReadCloser whose Read always returns an error,
// used to simulate an upstream body that fails mid-read.
type errorBody struct{}

func (errorBody) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errorBody) Close() error               { return nil }

func TestHandlePackageJSONBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{hdrContentType: {mimeJSON}},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	resp, _ := http.Get(ts.url + testPathPyPIJSONCov)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for body read error, got %d", resp.StatusCode)
	}
}

func TestHandlePackageJSONFilterInvalidJSON(t *testing.T) {
	// Upstream returns 200 with invalid JSON — filterPyPIJSONResponse fails,
	// handler should fall back to serving the raw body.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not-json")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathPyPIJSONCov)
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200Cov+" (fallback to raw body)", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "not-json" {
		t.Errorf("body should be raw upstream content, got %q", string(body))
	}
}

func TestHandlePackageJSONNoReleases(t *testing.T) {
	// Upstream returns JSON with no releases key — nothing to filter.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"info":{"name":"foo"}}`)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/pypi/foo/json")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── fetchPyPIMeta: error paths (via /simple/) ────────────────────────────────

func TestFetchPyPIMetaInvalidURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

func TestFetchPyPIMetaUpstreamConnectionFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestFetchPyPIMetaPackageNotFound(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream 404, got %d", resp.StatusCode)
	}
}

func TestFetchPyPIMetaBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{hdrContentType: {mimeJSON}},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for body read error, got %d", resp.StatusCode)
	}
}

func TestFetchPyPIMetaInvalidJSON(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not-valid-json")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathSimpleCov + testPkg + "/")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for invalid JSON, got %d", resp.StatusCode)
	}
}

// ─── runServer ────────────────────────────────────────────────────────────────

// ─── fail_mode: "closed" ─────────────────────────────────────────────────────

func TestFailModeClosedPackageJSONFilterError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		// Return malformed JSON to trigger filterPyPIJSONResponse error.
		w.Write([]byte("{not valid json}")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeClosed})
	resp, err := http.Get(ts.url + "/pypi/" + testPkg + "/json")
	if err != nil {
		t.Fatalf("GET /pypi/%s/json: %v", testPkg, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("fail_mode:closed + filter error: want 502, got %d", resp.StatusCode)
	}
}

func TestFailModeOpenPackageJSONFilterError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{not valid json}")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeOpen})
	resp, err := http.Get(ts.url + "/pypi/" + testPkg + "/json")
	if err != nil {
		t.Fatalf("GET /pypi/%s/json: %v", testPkg, err)
	}
	defer resp.Body.Close()
	// fail_mode:open must pass the raw body through — still 200.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fail_mode:open + filter error: want 200, got %d", resp.StatusCode)
	}
}

func TestRunServerGracefulShutdown(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testPyPIConfigYAML); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, logger, err := initServer(f.Name(), "", "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	// Override port to 0 so OS picks a free port.
	srv.cfg.Server.Port = 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, srv, logger, "pypi-pkguard-test")
	}()

	// Give the goroutine a moment to start listening, then cancel.
	<-time.After(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("runServer returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("runServer did not return after context cancellation")
	}
}

// ─── RequiresAgeFiltering integration (engine method) ────────────────────────
// Authoritative unit tests for RequiresAgeFiltering live in common/rules/rules_test.go.
// These tests verify the pypi proxy correctly calls the engine method in external URL paths.

func TestExternalURLTrustedPackageBypassesAgeCheck(t *testing.T) {
	cfg := &config.Config{
		Server: config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{
			URL:                  "https://pypi.org",
			TimeoutSeconds:       5,
			AllowedExternalHosts: []string{"example.com"},
		},
		Cache: config.CacheConfig{TTLSeconds: 60},
		Policy: config.PolicyConfig{
			TrustedPackages: []string{"requests"},
			Defaults:        config.RulesDefaults{MinPackageAgeDays: 30},
		},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	srv, err := buildServer(cfg, createLogger("text", "error"))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	srv.upstream = &http.Client{Transport: &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		rr.WriteHeader(http.StatusOK)
		rr.Write([]byte("file-content")) //nolint:errcheck
		return rr.Result(), nil
	}}}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// External URL with allowed host. Trusted packages should bypass age filtering.
	resp, err := http.Get(ts.URL + "/external?url=https://example.com/packages/requests-2.31.0.tar.gz")
	if err != nil {
		t.Fatalf("GET external: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("trusted package should not be blocked on external download, got %d", resp.StatusCode)
	}
}

// ─── isAllowedExternalHost: allowlist validation ──────────────────────────────

func TestIsAllowedExternalHostExactMatch(t *testing.T) {
	allowlist := []string{"example.com", "files.pythonhosted.org"}
	logger := createLogger("text", "error")
	if !isAllowedExternalHost("example.com", allowlist, logger) {
		t.Error("exact match should be allowed")
	}
	if !isAllowedExternalHost("files.pythonhosted.org", allowlist, logger) {
		t.Error("exact match should be allowed")
	}
}

func TestIsAllowedExternalHostNotInList(t *testing.T) {
	allowlist := []string{"example.com"}
	logger := createLogger("text", "error")
	if isAllowedExternalHost("evil.com", allowlist, logger) {
		t.Error("host not in allowlist should be denied")
	}
}

func TestIsAllowedExternalHostWildcard(t *testing.T) {
	allowlist := []string{"*.pythonhosted.org"}
	logger := createLogger("text", "error")
	if isAllowedExternalHost("files.pythonhosted.org", allowlist, logger) {
		t.Error("wildcard patterns should not be allowed")
	}
	if isAllowedExternalHost("cdn.pythonhosted.org", allowlist, logger) {
		t.Error("wildcard patterns should not be allowed")
	}
	if isAllowedExternalHost("pythonhosted.org", allowlist, logger) {
		t.Error("wildcard patterns should not be treated as exact hosts")
	}
}

func TestIsAllowedExternalHostEmptyAllowlist(t *testing.T) {
	allowlist := []string{}
	logger := createLogger("text", "error")
	if isAllowedExternalHost("any-host.com", allowlist, logger) {
		t.Error("empty allowlist should deny all hosts")
	}
}

func TestIsAllowedExternalHostCaseInsensitive(t *testing.T) {
	allowlist := []string{"Example.COM"}
	logger := createLogger("text", "error")
	if !isAllowedExternalHost("example.com", allowlist, logger) {
		t.Error("matching should be case-insensitive")
	}
	if !isAllowedExternalHost("EXAMPLE.COM", allowlist, logger) {
		t.Error("matching should be case-insensitive")
	}
}

func TestIsAllowedExternalHostWithPort(t *testing.T) {
	allowlist := []string{"example.com"}
	logger := createLogger("text", "error")
	if !isAllowedExternalHost("example.com:8080", allowlist, logger) {
		t.Error("port should be stripped before matching")
	}
}
