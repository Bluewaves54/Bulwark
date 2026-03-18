// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/installer"
	"Bulwark/common/rules"
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
	testNpmToken      = "npm-token-xyz"
	testNpmUser       = "npm-user"
	testNpmPassword   = "npm-password"
	testConfigPattern = "config-*.yaml"
)

// ─── Utility functions ────────────────────────────────────────────────────────

func TestAddrFromPortNpm(t *testing.T) {
	if got := addrFromPort(4873); got != ":4873" {
		t.Errorf("addrFromPort(4873) = %q, want \":4873\"", got)
	}
}

func TestCreateLoggerTextInfoNpm(t *testing.T) {
	if l, _, _ := createLogger("text", "info", ""); l == nil {
		t.Error("expected non-nil logger")
	}
}

func TestCreateLoggerJSONDebugNpm(t *testing.T) {
	if l, _, _ := createLogger("json", "debug", ""); l == nil {
		t.Error("expected non-nil logger for json/debug")
	}
}

func TestCreateLoggerWarnNpm(t *testing.T) {
	if l, _, _ := createLogger("text", "warn", ""); l == nil {
		t.Error("expected non-nil logger for warn")
	}
}

func TestCreateLoggerErrorNpm(t *testing.T) {
	if l, _, _ := createLogger("text", "error", ""); l == nil {
		t.Error("expected non-nil logger for error")
	}
}

func TestCreateLoggerDefaultNpm(t *testing.T) {
	if l, _, _ := createLogger("text", "unknown", ""); l == nil {
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
	logger, logLevel, _ := createLogger("text", "error", "")
	srv, err := buildServer(cfg, logger, logLevel)
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
	logger, logLevel, _ := createLogger("text", "error", "")
	_, err := buildServer(cfg, logger, logLevel)
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

	srv, logger, _, err := initServer(f.Name(), "", "", "")
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
	_, _, _, err := initServer("/nonexistent/path/config.yaml", "", "", "")
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

	srv, _, _, err := initServer(f.Name(), testTokenValue, "", "")
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

	srv, logger, _, err := initServer(f.Name(), "", "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	srv.cfg.Server.Port = 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runServer(ctx, srv, logger, "npm-bulwark-test")
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
			w.Header().Set("Content-Type", mimeJSON)
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

// ─── parseLogLevel ───────────────────────────────────────────────────────────

func TestParseLogLevelValidNpm(t *testing.T) {
	cases := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"DEBUG", slog.LevelDebug},
		{"INFO", slog.LevelInfo},
	}
	for _, tc := range cases {
		lvl, ok := parseLogLevel(tc.input)
		if !ok {
			t.Errorf("parseLogLevel(%q) returned not-ok", tc.input)
		}
		if lvl != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.input, lvl, tc.want)
		}
	}
}

func TestParseLogLevelInvalidNpm(t *testing.T) {
	_, ok := parseLogLevel("trace")
	if ok {
		t.Error("parseLogLevel(\"trace\") should return false")
	}
}

// ─── handleGetLogLevel / handleSetLogLevel ───────────────────────────────────

func TestHandleGetLogLevelNpm(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodGet, "/admin/log-level", nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/log-level: want 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["level"] == "" {
		t.Error("expected non-empty level in response")
	}
}

func TestHandleSetLogLevelNpm(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader(`{"level":"debug"}`))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /admin/log-level: want 200, got %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["level"] != "debug" {
		t.Errorf("level: want \"debug\", got %q", body["level"])
	}
}

func TestHandleSetLogLevelInvalidBodyNpm(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid body, got %d", rec.Code)
	}
}

func TestHandleSetLogLevelInvalidLevelNpm(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader(`{"level":"trace"}`))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid level, got %d", rec.Code)
	}
}

// ─── createLogger with file path ─────────────────────────────────────────────

func TestCreateLoggerWithFilePathNpm(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "test.log")
	logger, lvl, f := createLogger("text", "info", logPath)
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if lvl == nil {
		t.Fatal("expected non-nil LevelVar")
	}
	if f == nil {
		t.Fatal("expected non-nil file when filePath is set")
	}
	defer f.Close()
	logger.Info("test message")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "test message") {
		t.Errorf("log file should contain 'test message', got %q", string(data))
	}
}

func TestCreateLoggerWithoutFilePathNpm(t *testing.T) {
	logger, lvl, f := createLogger("text", "info", "")
	if logger == nil {
		t.Fatal("expected non-nil logger")
	}
	if lvl == nil {
		t.Fatal("expected non-nil LevelVar")
	}
	if f != nil {
		f.Close()
		t.Error("expected nil file when filePath is empty")
	}
}

// main() is exempt from unit test coverage because it contains flag.Parse(),
// os.Exit(), and os.Stdout references. The extracted newProxyInfo and
// handleInstallMode functions below ARE tested.

func TestDefaultConfigEmbed(t *testing.T) {
	if len(defaultConfig) == 0 {
		t.Fatal("defaultConfig embed is empty")
	}
	if !strings.Contains(string(defaultConfig), "server:") {
		t.Error("defaultConfig should contain server: section")
	}
}

func TestNewProxyInfo(t *testing.T) {
	p := newProxyInfo()
	if p.Ecosystem != "npm" {
		t.Errorf("Ecosystem = %s, want npm", p.Ecosystem)
	}
	if p.BinaryName != "npm-bulwark" {
		t.Errorf("BinaryName = %s, want npm-bulwark", p.BinaryName)
	}
	if p.Port != 18001 {
		t.Errorf("Port = %d, want 18001", p.Port)
	}
	if len(p.ConfigData) == 0 {
		t.Error("ConfigData should not be empty")
	}
}

func TestHandleInstallModeNeither(t *testing.T) {
	p := newProxyInfo()
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	handled, err := handleInstallMode(false, false, p, io.Discard, stub, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handled {
		t.Error("expected handled=false")
	}
}

func TestHandleInstallModeSetup(t *testing.T) {
	p := newProxyInfo()
	var called bool
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { called = true; return nil }
	noop := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	handled, err := handleInstallMode(true, false, p, io.Discard, stub, noop)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Error("expected handled=true")
	}
	if !called {
		t.Error("setup function not called")
	}
}

func TestHandleInstallModeUninstall(t *testing.T) {
	p := newProxyInfo()
	var called bool
	noop := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { called = true; return nil }
	handled, err := handleInstallMode(false, true, p, io.Discard, noop, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !handled {
		t.Error("expected handled=true")
	}
	if !called {
		t.Error("uninstall function not called")
	}
}

func TestHandleInstallModeError(t *testing.T) {
	p := newProxyInfo()
	fail := func(_ installer.ProxyInfo, _ io.Writer) error { return fmt.Errorf("test error") }
	noop := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	_, err := handleInstallMode(true, false, p, io.Discard, fail, noop)
	if err == nil {
		t.Error("expected error")
	}
}

// --- run() tests ---

func TestRunBadConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := run(ctx, "/no/such/config.yaml", true, false, false, false, false, false, "", "", "", io.Discard)
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestRunNormalStartup(t *testing.T) {
	tmpDir := t.TempDir()
	cfgFile := filepath.Join(tmpDir, "config.yaml")
	cfgData := "server:\n  port: 0\nupstream:\n  url: http://localhost:19999\n  timeout_seconds: 1\ncache:\n  ttl_seconds: 1\n"
	if err := os.WriteFile(cfgFile, []byte(cfgData), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := run(ctx, cfgFile, true, false, false, false, false, false, "", "", "", io.Discard)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- resolveConfig tests ---

func TestResolveConfigExplicit(t *testing.T) {
	p := newProxyInfo()
	home := t.TempDir()
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	got, err := resolveConfig("custom.yaml", true, p, home, "linux", io.Discard, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "custom.yaml" {
		t.Errorf("got %q, want custom.yaml", got)
	}
}

func TestResolveConfigDefaultExists(t *testing.T) {
	p := newProxyInfo()
	home := t.TempDir()
	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := func(_ installer.ProxyInfo, _ io.Writer) error {
		t.Error("setup should not be called")
		return nil
	}
	got, err := resolveConfig(cfgFile, false, p, home, "linux", io.Discard, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfgFile {
		t.Errorf("got %q, want %q", got, cfgFile)
	}
}

func TestResolveConfigAlreadyInstalled(t *testing.T) {
	p := newProxyInfo()
	home := t.TempDir()
	dir := filepath.Join(home, ".bulwark", p.BinaryName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	installedCfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(installedCfg, []byte("test"), 0o644); err != nil {
		t.Fatal(err)
	}
	stub := func(_ installer.ProxyInfo, _ io.Writer) error {
		t.Error("setup should not be called")
		return nil
	}
	got, err := resolveConfig("nonexistent.yaml", false, p, home, "linux", io.Discard, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != installedCfg {
		t.Errorf("got %q, want %q", got, installedCfg)
	}
}

func TestResolveConfigAutoSetup(t *testing.T) {
	p := newProxyInfo()
	home := t.TempDir()
	setupCalled := false
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { setupCalled = true; return nil }
	got, err := resolveConfig("nonexistent.yaml", false, p, home, "linux", io.Discard, stub)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !setupCalled {
		t.Error("setup function not called")
	}
	want := installer.InstalledConfigPath(p, home, "linux")
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveConfigAutoSetupError(t *testing.T) {
	p := newProxyInfo()
	home := t.TempDir()
	fail := func(_ installer.ProxyInfo, _ io.Writer) error { return fmt.Errorf("test error") }
	_, err := resolveConfig("nonexistent.yaml", false, p, home, "linux", io.Discard, fail)
	if err == nil {
		t.Error("expected error")
	}
}

// --- extractVersionPolicyFields direct tests ---

func TestExtractVersionPolicyFieldsBadJSON(t *testing.T) {
	hasScripts, license := extractVersionPolicyFields(json.RawMessage(`{invalid`))
	if hasScripts {
		t.Error("expected no scripts for invalid JSON")
	}
	if license != "" {
		t.Errorf("expected empty license, got %q", license)
	}
}

func TestExtractVersionPolicyFieldsStringLicense(t *testing.T) {
	hasScripts, license := extractVersionPolicyFields(json.RawMessage(`{"license":"MIT"}`))
	if hasScripts {
		t.Error("expected no scripts")
	}
	if license != "MIT" {
		t.Errorf("license = %q, want MIT", license)
	}
}

func TestExtractVersionPolicyFieldsObjectLicense(t *testing.T) {
	hasScripts, license := extractVersionPolicyFields(json.RawMessage(`{"license":{"type":"BSD"}}`))
	if hasScripts {
		t.Error("expected no scripts")
	}
	if license != "BSD" {
		t.Errorf("license = %q, want BSD", license)
	}
}

func TestExtractVersionPolicyFieldsArrayLicense(t *testing.T) {
	hasScripts, license := extractVersionPolicyFields(json.RawMessage(`{"license":["MIT"]}`))
	if hasScripts {
		t.Error("expected no scripts")
	}
	if license != "" {
		t.Errorf("expected empty license for array, got %q", license)
	}
}

func TestExtractVersionPolicyFieldsBoolLicense(t *testing.T) {
	hasScripts, license := extractVersionPolicyFields(json.RawMessage(`{"license":true}`))
	if hasScripts {
		t.Error("expected no scripts")
	}
	if license != "" {
		t.Errorf("expected empty license for bool, got %q", license)
	}
}

func TestExtractVersionPolicyFieldsInstallScript(t *testing.T) {
	raw := json.RawMessage(`{"scripts":{"postinstall":"node setup.js"},"license":"ISC"}`)
	hasScripts, license := extractVersionPolicyFields(raw)
	if !hasScripts {
		t.Error("expected install scripts")
	}
	if license != "ISC" {
		t.Errorf("license = %q, want ISC", license)
	}
}

func TestExtractVersionPolicyFieldsBuildScriptOnly(t *testing.T) {
	hasScripts, _ := extractVersionPolicyFields(json.RawMessage(`{"scripts":{"build":"make"}}`))
	if hasScripts {
		t.Error("build script should not count as install script")
	}
}

func TestInitServerBadConfig(t *testing.T) {
	_, _, _, err := initServer("/no/such/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestFetchVersionMetaBadJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("not json")) //nolint:errcheck
	}))
	defer upstream.Close()
	srv := &Server{
		cfg:      &config.Config{Upstream: config.UpstreamConfig{URL: upstream.URL}},
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := srv.fetchVersionMetaFromPackument("test-pkg", "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero time for bad JSON")
	}
}

func TestFetchVersionMetaNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer upstream.Close()
	srv := &Server{
		cfg:      &config.Config{Upstream: config.UpstreamConfig{URL: upstream.URL}},
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := srv.fetchVersionMetaFromPackument("test-pkg", "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero time for 404")
	}
}

func TestFetchVersionMetaEmptyPkg(t *testing.T) {
	srv := &Server{
		cfg:    &config.Config{Upstream: config.UpstreamConfig{URL: "http://localhost"}},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := srv.fetchVersionMetaFromPackument("", "1.0.0")
	if meta.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", meta.Version)
	}
}

func TestFetchVersionMetaNoMatchingVersion(t *testing.T) {
	body := `{"name":"test","versions":{"2.0.0":{}},"dist-tags":{"latest":"2.0.0"},"time":{"2.0.0":"2024-01-01T00:00:00Z"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer upstream.Close()
	srv := &Server{
		cfg:      &config.Config{Upstream: config.UpstreamConfig{URL: upstream.URL}},
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := srv.fetchVersionMetaFromPackument("test", "9.9.9")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero time for missing version")
	}
}

func TestFetchVersionMetaWithTime(t *testing.T) {
	body := `{"name":"test","versions":{"1.0.0":{"license":"MIT"}},"dist-tags":{"latest":"1.0.0"},"time":{"1.0.0":"2024-06-01T00:00:00Z"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body)) //nolint:errcheck
	}))
	defer upstream.Close()
	srv := &Server{
		cfg:      &config.Config{Upstream: config.UpstreamConfig{URL: upstream.URL}},
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	meta := srv.fetchVersionMetaFromPackument("test", "1.0.0")
	if meta.PublishedAt.IsZero() {
		t.Error("expected non-zero time")
	}
	if meta.License != "MIT" {
		t.Errorf("license = %q, want MIT", meta.License)
	}
}

func TestFilterNpmPackumentBadJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cfg := &config.Config{}
	engine := rules.New(cfg.Policy)
	_, _, _, err := filterNpmPackument([]byte("bad json"), "pkg", engine, "http://proxy", logger)
	if err == nil {
		t.Error("expected error for bad JSON")
	}
}

func TestFilterNpmPackumentAllVersionsRemoved(t *testing.T) {
	body := `{"name":"test","versions":{"0.0.1-alpha":{}},"dist-tags":{"latest":"0.0.1-alpha"},"time":{"0.0.1-alpha":"2024-01-01T00:00:00Z"}}`
	cfg := config.PolicyConfig{
		VersionPatterns: []config.VersionPatternRule{
			{Name: "deny-all", Match: ".*", Action: "deny", Reason: "test"},
		},
	}
	engine := rules.New(cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, removed, reason, err := filterNpmPackument([]byte(body), "test", engine, "http://proxy", logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if reason == "" {
		t.Error("expected non-empty block reason")
	}
}

func TestFilterNpmPackumentPackageBlocked(t *testing.T) {
	body := `{"name":"evil","versions":{"1.0.0":{}},"dist-tags":{"latest":"1.0.0"}}`
	cfg := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "deny-evil", PackagePatterns: []string{"evil"}, Action: "deny"},
		},
	}
	engine := rules.New(cfg)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	filtered, removed, reason, err := filterNpmPackument([]byte(body), "evil", engine, "http://proxy", logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if removed != 1 {
		t.Errorf("removed = %d, want 1", removed)
	}
	if reason == "" {
		t.Error("expected non-empty block reason")
	}
	if !strings.Contains(string(filtered), `"versions":{}`) {
		t.Error("expected empty versions in response")
	}
}

// ─── Explicit logging level behavior tests (npm) ─────────────────────────────

func TestLoggingLevelDebugBehaviorNpm(t *testing.T) {
	logger, lvl, _ := createLogger("text", "debug", "")
	if lvl.Level() != slog.LevelDebug {
		t.Errorf("want LevelDebug, got %v", lvl.Level())
	}
	if !logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug messages should be enabled at debug level")
	}
}

func TestLoggingLevelInfoBehaviorNpm(t *testing.T) {
	logger, lvl, _ := createLogger("text", "info", "")
	if lvl.Level() != slog.LevelInfo {
		t.Errorf("want LevelInfo, got %v", lvl.Level())
	}
	if logger.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("debug messages should NOT be enabled at info level")
	}
	if !logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info messages should be enabled at info level")
	}
}

func TestLoggingLevelWarnBehaviorNpm(t *testing.T) {
	logger, lvl, _ := createLogger("text", "warn", "")
	if lvl.Level() != slog.LevelWarn {
		t.Errorf("want LevelWarn, got %v", lvl.Level())
	}
	if logger.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("info messages should NOT be enabled at warn level")
	}
	if !logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn messages should be enabled at warn level")
	}
}

func TestLoggingLevelErrorBehaviorNpm(t *testing.T) {
	logger, lvl, _ := createLogger("text", "error", "")
	if lvl.Level() != slog.LevelError {
		t.Errorf("want LevelError, got %v", lvl.Level())
	}
	if logger.Enabled(context.Background(), slog.LevelWarn) {
		t.Error("warn messages should NOT be enabled at error level")
	}
	if !logger.Enabled(context.Background(), slog.LevelError) {
		t.Error("error messages should be enabled at error level")
	}
}

func TestLoggingJSONFormatBehaviorNpm(t *testing.T) {
	logger, _, _ := createLogger("json", "info", "")
	if logger == nil {
		t.Error("expected non-nil logger for json format")
	}
}

func TestLoggingFileOutputNpm(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "test.log")
	logger, _, logFile := createLogger("text", "info", tmpFile)
	if logFile == nil {
		t.Fatal("expected non-nil log file")
	}
	defer logFile.Close()
	logger.Info("test message")
	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "test message") {
		t.Error("log file should contain test message")
	}
}

func TestDynamicLogLevelAPINpm(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	// GET initial level.
	resp, err := http.Get(ts.url + "/admin/log-level")
	if err != nil {
		t.Fatalf("GET log-level: %v", err)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body) //nolint:errcheck
	resp.Body.Close()
	if body["level"] == "" {
		t.Error("expected non-empty level")
	}

	// PUT change to debug.
	req, _ := http.NewRequest(http.MethodPut, ts.url+"/admin/log-level",
		strings.NewReader(`{"level":"debug"}`))
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT log-level: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("PUT log-level: want 200, got %d", resp2.StatusCode)
	}

	// Verify level changed.
	resp3, _ := http.Get(ts.url + "/admin/log-level")
	var body3 map[string]string
	json.NewDecoder(resp3.Body).Decode(&body3) //nolint:errcheck
	resp3.Body.Close()
	if body3["level"] != "debug" {
		t.Errorf("level after PUT: want debug, got %q", body3["level"])
	}

	// PUT invalid level.
	reqBad, _ := http.NewRequest(http.MethodPut, ts.url+"/admin/log-level",
		strings.NewReader(`{"level":"verbose"}`))
	reqBad.Header.Set("Content-Type", "application/json")
	resp4, _ := http.DefaultClient.Do(reqBad)
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusBadRequest {
		t.Errorf("PUT invalid level: want 400, got %d", resp4.StatusCode)
	}
}

func TestParseLogLevelNpm(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
		ok    bool
	}{
		{"debug", slog.LevelDebug, true},
		{"info", slog.LevelInfo, true},
		{"warn", slog.LevelWarn, true},
		{"error", slog.LevelError, true},
		{"DEBUG", slog.LevelDebug, true},
		{"verbose", slog.LevelInfo, false},
	}
	for _, tc := range tests {
		got, ok := parseLogLevel(tc.input)
		if ok != tc.ok {
			t.Errorf("parseLogLevel(%q): ok = %v, want %v", tc.input, ok, tc.ok)
		}
		if got != tc.want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestLoggingEnvOverrideNpm(t *testing.T) {
	yaml := `
upstream:
  url: "https://registry.npmjs.org"
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

	t.Setenv("BULWARK_LOG_LEVEL", "debug")
	srv, _, _, err := initServer(f.Name(), "", "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	if srv.logLevel.Level() != slog.LevelDebug {
		t.Errorf("env override: want LevelDebug, got %v", srv.logLevel.Level())
	}
}

func TestIsAddrInUseNpm(t *testing.T) {
	if isAddrInUse(nil) {
		t.Error("nil error should not be address-in-use")
	}
	if isAddrInUse(fmt.Errorf("connection refused")) {
		t.Error("unrelated error should not match")
	}
	if !isAddrInUse(fmt.Errorf("listen tcp :8080: bind: address already in use")) {
		t.Error("address-in-use error should match")
	}
}

func TestKillProcessOnPortNoProcessNpm(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	killProcessOnPort(59999, logger)
}

func TestKillProcessOnPortUnsupportedOSNpm(t *testing.T) {
	old := hostOS
	hostOS = "plan9"
	defer func() { hostOS = old }()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	killProcessOnPort(12345, logger)
}

func TestKillProcessOnPortOwnProcessNpm(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	killProcessOnPort(port, logger)
}

func TestKillProcessOnPortForeignProcessNpm(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	script := fmt.Sprintf(
		"import socket,time;s=socket.socket();s.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1);"+
			"s.bind(('127.0.0.1',%d));s.listen(1);time.sleep(60)", port)
	cmd := exec.Command("python3", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Skip("python3 not available")
	}
	defer cmd.Process.Kill() //nolint:errcheck // best-effort cleanup
	time.Sleep(300 * time.Millisecond)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	killProcessOnPort(port, logger)

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
		// Process was killed — success.
	case <-time.After(3 * time.Second):
		t.Error("expected subprocess to be killed within timeout")
	}
}

func TestRunServerPortConflictNpm(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	port := ln.Addr().(*net.TCPAddr).Port
	srv := &Server{
		cfg: &config.Config{
			Server: config.ServerConfig{
				Port:                port,
				ReadTimeoutSeconds:  5,
				WriteTimeoutSeconds: 5,
				IdleTimeoutSeconds:  30,
			},
		},
		mux:      http.NewServeMux(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		logLevel: &slog.LevelVar{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if runErr := runServer(ctx, srv, srv.logger, "npm-bulwark-test"); runErr == nil {
		t.Error("expected error when port is occupied, got nil")
	}
}

func TestListenWithRetrySucceedsImmediatelyNpm(t *testing.T) {
	ln, err := listenWithRetry(":0", 3, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	ln.Close()
}

func TestListenWithRetryFailsAfterMaxAttemptsNpm(t *testing.T) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	addr := ln.Addr().String()

	_, err = listenWithRetry(addr, 2, 10*time.Millisecond)
	if err == nil {
		t.Error("expected error when port is occupied")
	}
}
