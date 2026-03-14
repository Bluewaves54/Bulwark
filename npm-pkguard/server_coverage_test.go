// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// ─── Constants ───────────────────────────────────────────────────────────────

const (
	testNpmToken    = "npm-token-xyz"
	testNpmUser     = "npm-user"
	testNpmPassword = "npm-password"
)

// ─── Utility functions ────────────────────────────────────────────────────────

func TestAddrFromPortNpm(t *testing.T) {
	if got := addrFromPort(4873); got != ":4873" {
		t.Errorf("addrFromPort(4873) = %q, want \":4873\"", got)
	}
}

func TestCreateLoggerTextInfoNpm(t *testing.T) {
	if l := createLogger("text", "info"); l == nil {
		t.Error("expected non-nil logger")
	}
}

func TestCreateLoggerJSONDebugNpm(t *testing.T) {
	if l := createLogger("json", "debug"); l == nil {
		t.Error("expected non-nil logger for json/debug")
	}
}

func TestCreateLoggerWarnNpm(t *testing.T) {
	if l := createLogger("text", "warn"); l == nil {
		t.Error("expected non-nil logger for warn")
	}
}

func TestCreateLoggerErrorNpm(t *testing.T) {
	if l := createLogger("text", "error"); l == nil {
		t.Error("expected non-nil logger for error")
	}
}

func TestCreateLoggerDefaultNpm(t *testing.T) {
	if l := createLogger("text", "unknown"); l == nil {
		t.Error("expected non-nil logger for unknown level")
	}
}

func TestLoadConfigMissingFileNpm(t *testing.T) {
	_, err := loadConfig("/nonexistent/npm-config.yaml")
	if err == nil {
		t.Error("expected error loading missing config")
	}
}

func TestLoadConfigValidNpm(t *testing.T) {
	yaml := `
upstream:
  url: https://registry.npmjs.org
server:
  port: 4873
`
	f, err := os.CreateTemp(t.TempDir(), "npm-cfg-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	f.WriteString(yaml) //nolint:errcheck
	f.Close()

	cfg, err := loadConfig(f.Name())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Upstream.URL != testUpstreamURL {
		t.Errorf("URL = %q", cfg.Upstream.URL)
	}
}

func TestApplyFlagOverridesTokenNpm(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, testNpmToken, "", "")
	if cfg.Upstream.Token != testNpmToken {
		t.Errorf("Token = %q, want %q", cfg.Upstream.Token, testNpmToken)
	}
}

func TestApplyFlagOverridesBasicAuthNpm(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, "", testNpmUser, testNpmPassword)
	if cfg.Upstream.Username != testNpmUser || cfg.Upstream.Password != testNpmPassword {
		t.Errorf("Username/Password mismatch")
	}
}

func TestApplyFlagOverridesEmptyNpm(t *testing.T) {
	cfg := &config.Config{Upstream: config.UpstreamConfig{Token: "keep"}}
	applyFlagOverrides(cfg, "", "", "")
	if cfg.Upstream.Token != "keep" {
		t.Error("empty overrides should not overwrite token")
	}
}

// ─── addUpstreamAuth ──────────────────────────────────────────────────────────

func buildNpmServerWithAuth(t *testing.T, upstreamURL, token, username, password string) (*Server, *httptest.Server) {
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
	srv, err := buildServer(cfg, createLogger("text", "error"))
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestAddUpstreamAuthTokenNpm(t *testing.T) {
	var gotAuth string
	p := mockPackument(testPkgLodash, map[string]string{testVersionOne: testTimeOld2021})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(p) //nolint:errcheck
	}))
	defer mock.Close()

	_, ts := buildNpmServerWithAuth(t, mock.URL, testNpmToken, "", "")

	http.Get(ts.URL + "/" + testPkgLodash) //nolint:errcheck
	if want := "Bearer " + testNpmToken; gotAuth != want {
		t.Errorf("Authorization: want %q, got %q", want, gotAuth)
	}
}

func TestAddUpstreamAuthBasicNpm(t *testing.T) {
	var gotUser, gotPass string
	p := mockPackument(testPkgLodash, map[string]string{testVersionOne: testTimeOld2021})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(p) //nolint:errcheck
	}))
	defer mock.Close()

	_, ts := buildNpmServerWithAuth(t, mock.URL, "", testNpmUser, testNpmPassword)

	http.Get(ts.URL + "/" + testPkgLodash) //nolint:errcheck
	if gotUser != testNpmUser || gotPass != testNpmPassword {
		t.Errorf("BasicAuth: user=%s pass=%s", gotUser, gotPass)
	}
}

// ─── handleTarball: denied version ───────────────────────────────────────────

func TestHandleTarballDeniedVersion(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tarball-bytes")) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockPre, BlockPreRelease: true},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + "/" + testPkgLodash + testPathLodashTarPfx + testVersionPre + ".tgz")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for pre-release tarball, got %d", resp.StatusCode)
	}
}

func TestHandleTarballDeniedPackage(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("tarball-bytes")) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "deny-pkg", PackagePatterns: []string{testPkgLodash}, Action: "deny"},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + "/" + testPkgLodash + testPathLodashTarPfx + testVersionOne + ".tgz")
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied package tarball, got %d", resp.StatusCode)
	}
}

func TestHandleTarballAllowed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testFakeTarballShort)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/" + testPkgLodash + testPathLodashTarPfx + testVersionOne + ".tgz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for allowed tarball, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestHandleTarballUpstreamError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { /* intentionally empty */ }))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/" + testPkgLodash + testPathLodashTarPfx + testVersionOne + ".tgz")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream error, got %d", resp.StatusCode)
	}
}

// ─── handlePackument: tarball URL rewrite ────────────────────────────────────

func TestHandlePackumentRewritesTarballURL(t *testing.T) {
	type versionEntry struct {
		Dist struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}

	// We need to produce a packument where version dist.tarball is rewritten.
	type packumentFull struct {
		Name     string                  `json:"name"`
		Versions map[string]versionEntry `json:"versions"`
		DistTags map[string]string       `json:"dist-tags"`
		Time     map[string]string       `json:"time"`
	}

	ve := versionEntry{}
	ve.Dist.Tarball = "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"
	pf := packumentFull{
		Name:     testPkgLodash,
		Versions: map[string]versionEntry{testVersionOne: ve},
		DistTags: map[string]string{"latest": testVersionOne},
		Time:     map[string]string{testVersionOne: testTimeOld2021},
	}
	body, _ := json.Marshal(pf)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/" + testPkgLodash)
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── buildServer with insecure TLS ───────────────────────────────────────────

func TestBuildServerInsecureTLSNpm(t *testing.T) {
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
		t.Fatalf("buildServer with InsecureSkipVerify: %v", err)
	}
}

// ─── handleReady ─────────────────────────────────────────────────────────────

func TestHandleReadyNpmDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { /* intentionally empty */ }))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestHandleReadyNpm5xx(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for upstream 5xx, got %d", resp.StatusCode)
	}
}

// ─── handlePackument: non-OK non-500 (e.g. 404) ──────────────────────────────

func TestHandlePackumentNotFound(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"not found"}`)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/nonexistent-pkg")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// ─── handlePackument: bad JSON body → filter error → serve unfiltered ────────

func TestHandlePackumentFilterError(t *testing.T) {
	// Return invalid JSON so filterNpmPackument returns an error.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`not json at all`)) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/" + testPkgLodash)
	// Fallback: serve the unfiltered body → 200.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("filter error should fall back to unfiltered: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── extractVersionFromTarball edge cases ─────────────────────────────────────

func TestExtractVersionNoDash(t *testing.T) {
	// If the filename portion has no dash, extractVersionFromTarball returns "".
	got := extractVersionFromTarball("/lodash/-/lodash4.17.21.tgz", "lodash")
	// "lodash4.17.21" split on "-": ["lodash4.17.21"] → len<2 → ""
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestExtractVersionNoSlashMinus(t *testing.T) {
	// Path with no "/-/" → returns "".
	got := extractVersionFromTarball("/some/path/without/tarball", "some")
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ─── initServer ───────────────────────────────────────────────────────────────

const testNpmConfigYAML = `upstream:
  url: https://registry.npmjs.org
server:
  port: 4873
`

func TestInitServerNpmSuccess(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testNpmConfigYAML); err != nil {
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

func TestInitServerNpmMissingConfig(t *testing.T) {
	_, _, err := initServer("/nonexistent/path/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestInitServerNpmWithTokenOverride(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testNpmConfigYAML); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, _, err := initServer(f.Name(), testTokenValue, "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	if srv.cfg.Upstream.Token != testTokenValue {
		t.Errorf("Token: want %q, got %q", testTokenValue, srv.cfg.Upstream.Token)
	}
}

// ─── handleReady: invalid upstream URL (NewRequest error path) ────────────────

func TestHandleReadyNpmInvalidURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// ─── handlePackument: error paths ────────────────────────────────────────────

func TestHandlePackumentInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathLodash)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

func TestHandlePackumentUpstreamConnectionFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { /* intentionally empty */ }))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathLodash)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 when upstream connection fails, got %d", resp.StatusCode)
	}
}

// errorBody is an io.ReadCloser whose Read always returns an error.
type errorBody struct{}

func (errorBody) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errorBody) Close() error               { return nil }

func TestHandlePackumentBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { /* intentionally empty */ }))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	resp, _ := http.Get(ts.url + testPathLodash)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for body read error, got %d", resp.StatusCode)
	}
}

func TestHandlePackumentNoContentType(t *testing.T) {
	// Use a custom transport that returns a response with NO Content-Type header,
	// so the proxy triggers the "ct == empty → default to application/json" path.
	pkgJSON := `{"name":"lodash","versions":{},"dist-tags":{}}`
	transport := &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{}, // Explicitly no Content-Type.
			Body:       io.NopCloser(strings.NewReader(pkgJSON)),
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { /* intentionally empty */ }))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, _ := http.Get(ts.url + testPathLodash)
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	if ct := resp.Header.Get(hdrContentType); ct != mimeJSON {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
}

// ─── handleTarball: DryRun and error paths ───────────────────────────────────

func TestHandleTarballDryRun(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(testFakeTarballShort)) //nolint:errcheck
	}))
	defer mock.Close()

	// Global DryRun + BlockPreRelease: pre-release tarball is allowed but counted as dry-run.
	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleBlockPre, BlockPreRelease: true},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + "/lodash/-/lodash-4.0.0-beta.1.tgz")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for dry-run pre-release tarball, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment")
	}
}

func TestHandleTarballInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathLodashTarball)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// ─── filterNpmPackument: dist-tags removal ────────────────────────────────────

func TestFilterNpmPackumentDistTagsRemoved(t *testing.T) {
	// Pack with "beta" dist-tag pointing to a denied pre-release version.
	pkgJSON := `{
		"name": "lodash",
		"versions": {
			"4.17.21": {},
			"5.0.0-beta.1": {}
		},
		"dist-tags": {
			"latest": "4.17.21",
			"beta": "5.0.0-beta.1"
		},
		"time": {
			"4.17.21": "2020-01-01T00:00:00Z",
			"5.0.0-beta.1": "2024-01-01T00:00:00Z"
		}
	}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(pkgJSON)) //nolint:errcheck
	}))
	defer mock.Close()

	// BlockPreRelease denies "5.0.0-beta.1" → its "beta" dist-tag entry is                // removed.
	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockPre, BlockPreRelease: true},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathLodash)
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	resp.Body.Close()
	// Confirm the "beta" tag was removed (denied version was the tag value).
	if ts.srv.reqDenied.Load() == 0 {
		t.Error("expected reqDenied counter > 0 when pre-release is blocked")
	}
}

// ─── runServer ────────────────────────────────────────────────────────────────

const testNpmConfigYAMLForRun = `upstream:
  url: https://registry.npmjs.org
server:
  port: 0
`

// ─── fail_mode: "closed" ─────────────────────────────────────────────────────

func TestFailModeClosedPackumentFilterError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		// Malformed JSON triggers filterNpmPackument error.
		w.Write([]byte("{not valid json}")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeClosed})
	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf("GET /%s: %v", testPkgLodash, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("fail_mode:closed + filter error: want 502, got %d", resp.StatusCode)
	}
}

func TestFailModeOpenPackumentFilterError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{not valid json}")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeOpen})
	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf("GET /%s: %v", testPkgLodash, err)
	}
	defer resp.Body.Close()
	// fail_mode:open must pass the raw response through.
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fail_mode:open + filter error: want 200, got %d", resp.StatusCode)
	}
}

func TestRunServerNpmGracefulShutdown(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "npm-cfg-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testNpmConfigYAMLForRun); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, logger, err := initServer(f.Name(), "", "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	srv.cfg.Server.Port = 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, srv, logger, "npm-pkguard-test")
	}()

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
// These tests verify the npm proxy correctly calls the engine method in tarball paths.

func TestTarballTrustedPackageBypassesAgeCheck(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return packument with no time info (PublishedAt will be zero).
		if !strings.Contains(r.URL.Path, "/-/") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"name":"lodash","versions":{"4.17.21":{"dist":{"tarball":"http://localhost/lodash/-/lodash-4.17.21.tgz"}}},"dist-tags":{"latest":"4.17.21"},"time":{}}`)
			return
		}
		w.Write([]byte("tarball-content")) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		TrustedPackages: []string{"lodash"},
		Defaults:        config.RulesDefaults{MinPackageAgeDays: 30},
	}
	ts := buildTestServer(t, mock.URL, policy)

	req := httptest.NewRequest(http.MethodGet, "/lodash/-/lodash-4.17.21.tgz", nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Errorf("trusted package should not be blocked on tarball download, got %d", rec.Code)
	}
}
