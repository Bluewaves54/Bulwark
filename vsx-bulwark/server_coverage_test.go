// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/installer"
	"Bulwark/common/rules"
)

const bulwarkDir = ".bulwark"

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}

// ─── Constants ───────────────────────────────────────────────────────────────

const (
	testVsxToken    = "vsx-token-xyz"
	testVsxUser     = "vsx-user"
	testVsxPassword = "vsx-password"
)

// ─── Utility functions ────────────────────────────────────────────────────────

func TestAddrFromPortVsx(t *testing.T) {
	if got := addrFromPort(18003); got != ":18003" {
		t.Errorf("addrFromPort(18003) = %q, want \":18003\"", got)
	}
}

func TestCreateLoggerTextInfoVsx(t *testing.T) {
	if l, _, _ := createLogger("text", "info", ""); l == nil {
		t.Error("expected non-nil logger")
	}
}

func TestCreateLoggerJSONDebugVsx(t *testing.T) {
	if l, _, _ := createLogger("json", "debug", ""); l == nil {
		t.Error("expected non-nil logger for json/debug")
	}
}

func TestCreateLoggerWarnVsx(t *testing.T) {
	if l, _, _ := createLogger("text", "warn", ""); l == nil {
		t.Error("expected non-nil logger for warn")
	}
}

func TestCreateLoggerErrorVsx(t *testing.T) {
	if l, _, _ := createLogger("text", "error", ""); l == nil {
		t.Error("expected non-nil logger for error")
	}
}

func TestCreateLoggerDefaultVsx(t *testing.T) {
	if l, _, _ := createLogger("text", "unknown", ""); l == nil {
		t.Error("expected non-nil logger for unknown level")
	}
}

func TestLoadConfigMissingFileVsx(t *testing.T) {
	_, err := loadConfig("/nonexistent/vsx-config.yaml")
	if err == nil {
		t.Error("expected error loading missing config")
	}
}

func TestLoadConfigValidVsx(t *testing.T) {
	yaml := `
upstream:
  url: https://open-vsx.org
server:
  port: 18003
`
	f, err := os.CreateTemp(t.TempDir(), "vsx-cfg-*.yaml")
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

func TestApplyFlagOverridesTokenVsx(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, testVsxToken, "", "")
	if cfg.Upstream.Token != testVsxToken {
		t.Errorf("Token = %q, want %q", cfg.Upstream.Token, testVsxToken)
	}
}

func TestApplyFlagOverridesBasicAuthVsx(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, "", testVsxUser, testVsxPassword)
	if cfg.Upstream.Username != testVsxUser || cfg.Upstream.Password != testVsxPassword {
		t.Errorf("Username/Password mismatch")
	}
}

func TestApplyFlagOverridesEmptyVsx(t *testing.T) {
	cfg := &config.Config{Upstream: config.UpstreamConfig{Token: "keep"}}
	applyFlagOverrides(cfg, "", "", "")
	if cfg.Upstream.Token != "keep" {
		t.Error("empty overrides should not overwrite token")
	}
}

// ─── buildServer with insecure TLS ───────────────────────────────────────────

func TestBuildServerInsecureTLSVsx(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5, TLS: config.TLSConfig{InsecureSkipVerify: true}},
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

// ─── handleReady error paths ─────────────────────────────────────────────────

func TestHandleReadyVsxDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestHandleReadyVsxInvalidURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// ─── handleExtension error paths ─────────────────────────────────────────────

func TestHandleExtensionInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPython)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

func TestHandleExtensionUpstreamConnectionFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPython)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 when upstream connection fails, got %d", resp.StatusCode)
	}
}

func TestHandleExtensionNoContentType(t *testing.T) {
	extJSON := `{"namespace":"ms-python","name":"python","allVersions":{}}`
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(extJSON)),
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}

	resp, _ := http.Get(ts.url + testPathExtPython)
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	if ct := resp.Header.Get(hdrContentType); ct != mimeJSON {
		t.Errorf("Content-Type: want application/json, got %q", ct)
	}
}

// ─── handleExtensionVersion error paths ──────────────────────────────────────

func TestHandleExtensionVersionUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPythonVer)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestHandleExtensionVersionDryRunPackage(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtPython}},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathExtPythonVer)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run package deny version: want 200, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment for dry-run package deny")
	}
}

func TestHandleExtensionVersionDryRunVersion(t *testing.T) {
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

	resp, _ := http.Get(ts.url + "/api/ms-python/python/" + testVersionPre)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run version deny: want 200, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment")
	}
}

// ─── handleVsixDownload error paths ──────────────────────────────────────────

func TestHandleVsixDownloadDryRunPackage(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			fmt.Fprint(w, testFakeVsix)
			return
		}
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{testExtPython}},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run vsix: want 200, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment")
	}
}

func TestHandleVsixDownloadDryRunVersion(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			fmt.Fprint(w, testFakeVsix)
			return
		}
		recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, recent, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("dry-run vsix version: want 200, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment")
	}
}

func TestHandleVsixDownloadUpstreamConnectionFailed(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for vsix upstream down, got %d", resp.StatusCode)
	}
}

func TestHandleVsixDownloadInvalidUpstreamURL(t *testing.T) {
	// Use a transport that returns version metadata but then a closed URL for the vsix download.
	callCount := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "MIT", false)
			w.Header().Set(hdrContentType, mimeJSON)
			w.Write(body) //nolint:errcheck
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for vsix download failure, got %d", resp.StatusCode)
	}
}

// ─── handleQuery error paths ─────────────────────────────────────────────────

func TestHandleQueryUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/query?extensionName=python")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for query upstream down, got %d", resp.StatusCode)
	}
}

func TestHandleQueryUpstream404(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found"}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/query?extensionName=python")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404 for query upstream 404, got %d", resp.StatusCode)
	}
}

func TestHandleQueryPOST(t *testing.T) {
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
	resp, err := http.Post(ts.url+"/api/-/query", mimeJSON, strings.NewReader(`{"namespaceName":"ms-python"}`))
	if err != nil {
		t.Fatalf("POST query: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST query: want 200, got %d", resp.StatusCode)
	}
}

// errorBody is an io.ReadCloser whose Read always returns an error.
type errorBody struct{}

func (errorBody) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errorBody) Close() error               { return nil }

func TestHandleQueryBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	resp, _ := http.Get(ts.url + "/api/-/query?extensionName=python")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for query body read error, got %d", resp.StatusCode)
	}
}

// ─── handlePassthrough error paths ───────────────────────────────────────────

func TestHandlePassthroughUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/search/python")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for passthrough upstream down, got %d", resp.StatusCode)
	}
}

func TestHandlePassthroughWithQueryString(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, `{"extensions":[]}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/search/python?sortBy=relevance")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for passthrough with query, got %d", resp.StatusCode)
	}
}

// ─── fetchVersionMetaFromAPI edge cases ──────────────────────────────────────

func TestFetchVersionMetaFromAPIEmpty(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	meta := ts.srv.fetchVersionMetaFromAPI("", "", "")
	if meta.Version != "" {
		t.Errorf("expected empty version for empty args, got %q", meta.Version)
	}
}

func TestFetchVersionMetaFromAPIUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	meta := ts.srv.fetchVersionMetaFromAPI(testNsPython, testNamePython, testVersionOne)
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt when upstream is down")
	}
}

func TestFetchVersionMetaFromAPIUpstream404(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	meta := ts.srv.fetchVersionMetaFromAPI(testNsPython, testNamePython, testVersionOne)
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for 404")
	}
}

func TestFetchVersionMetaFromAPIBadJSON(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, "not json")
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	meta := ts.srv.fetchVersionMetaFromAPI(testNsPython, testNamePython, testVersionOne)
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for bad JSON")
	}
}

func TestFetchVersionMetaFromAPISuccess(t *testing.T) {
	body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, testTimeOld2021, "Apache-2.0", false)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	meta := ts.srv.fetchVersionMetaFromAPI(testNsPython, testNamePython, testVersionOne)
	if meta.PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt for valid response")
	}
	if meta.License != "Apache-2.0" {
		t.Errorf("License = %q, want Apache-2.0", meta.License)
	}
}

// ─── parseLogLevel ───────────────────────────────────────────────────────────

func TestParseLogLevelValidVsx(t *testing.T) {
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

func TestParseLogLevelInvalidVsx(t *testing.T) {
	_, ok := parseLogLevel("trace")
	if ok {
		t.Error("parseLogLevel(\"trace\") should return false")
	}
}

// ─── handleGetLogLevel / handleSetLogLevel ───────────────────────────────────

func TestHandleGetLogLevelVsx(t *testing.T) {
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

func TestHandleSetLogLevelVsx(t *testing.T) {
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

func TestHandleSetLogLevelInvalidBodyVsx(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid body, got %d", rec.Code)
	}
}

func TestHandleSetLogLevelInvalidLevelVsx(t *testing.T) {
	ts := buildTestServer(t, testUpstreamURL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader(`{"level":"trace"}`))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid level, got %d", rec.Code)
	}
}

// ─── createLogger with file path ─────────────────────────────────────────────

func TestCreateLoggerWithFilePathVsx(t *testing.T) {
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

func TestCreateLoggerWithoutFilePathVsx(t *testing.T) {
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

// ─── main.go: embedded config, proxyInfo, install mode ───────────────────────

func TestDefaultConfigEmbedVsx(t *testing.T) {
	if len(defaultConfig) == 0 {
		t.Fatal("defaultConfig embed is empty")
	}
	if !strings.Contains(string(defaultConfig), "server:") {
		t.Error("defaultConfig should contain server: section")
	}
}

func TestNewProxyInfoVsx(t *testing.T) {
	p := newProxyInfo()
	if p.Ecosystem != "vsx" {
		t.Errorf("Ecosystem = %s, want vsx", p.Ecosystem)
	}
	if p.BinaryName != "vsx-bulwark" {
		t.Errorf("BinaryName = %s, want vsx-bulwark", p.BinaryName)
	}
	if p.Port != 18003 {
		t.Errorf("Port = %d, want 18003", p.Port)
	}
	if len(p.ConfigData) == 0 {
		t.Error("ConfigData should not be empty")
	}
}

func TestHandleInstallModeNeitherVsx(t *testing.T) {
	p := newProxyInfo()
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	handled, err := handleInstallMode(false, false, p, io.Discard, stub, stub)
	if handled {
		t.Error("expected handled=false when neither flag is set")
	}
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestHandleInstallModeSetupVsx(t *testing.T) {
	p := newProxyInfo()
	called := false
	setupFn := func(_ installer.ProxyInfo, _ io.Writer) error {
		called = true
		return nil
	}
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	handled, err := handleInstallMode(true, false, p, io.Discard, setupFn, stub)
	if !handled || err != nil {
		t.Errorf("setup: handled=%v, err=%v", handled, err)
	}
	if !called {
		t.Error("setup function was not called")
	}
}

func TestHandleInstallModeUninstallVsx(t *testing.T) {
	p := newProxyInfo()
	called := false
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	uninstallFn := func(_ installer.ProxyInfo, _ io.Writer) error {
		called = true
		return nil
	}
	handled, err := handleInstallMode(false, true, p, io.Discard, stub, uninstallFn)
	if !handled || err != nil {
		t.Errorf("uninstall: handled=%v, err=%v", handled, err)
	}
	if !called {
		t.Error("uninstall function was not called")
	}
}

func TestHandleInstallModeSetupErrorVsx(t *testing.T) {
	p := newProxyInfo()
	setupFn := func(_ installer.ProxyInfo, _ io.Writer) error { return fmt.Errorf("setup failed") }
	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	_, err := handleInstallMode(true, false, p, io.Discard, setupFn, stub)
	if err == nil {
		t.Error("expected error from setup")
	}
}

// ─── resolveConfig ───────────────────────────────────────────────────────────

func TestResolveConfigExplicitVsx(t *testing.T) {
	path, err := resolveConfig("/explicit/path.yaml", true, newProxyInfo(), "", "", io.Discard, nil)
	if err != nil {
		t.Fatalf("resolveConfig explicit: %v", err)
	}
	if path != "/explicit/path.yaml" {
		t.Errorf("path = %q, want /explicit/path.yaml", path)
	}
}

func TestResolveConfigLocalFileExistsVsx(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	f.WriteString("server:\n  port: 18003\n") //nolint:errcheck
	f.Close()

	path, err := resolveConfig(f.Name(), false, newProxyInfo(), "", "", io.Discard, nil)
	if err != nil {
		t.Fatalf("resolveConfig local: %v", err)
	}
	if path != f.Name() {
		t.Errorf("path = %q, want %q", path, f.Name())
	}
}

// ─── initServer ───────────────────────────────────────────────────────────────

const testVsxConfigYAML = `upstream:
  url: https://open-vsx.org
server:
  port: 18003
`

func TestInitServerVsxSuccess(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testVsxConfigYAML); err != nil {
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

func TestInitServerVsxMissingConfig(t *testing.T) {
	_, _, _, err := initServer("/nonexistent/path/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestInitServerVsxWithTokenOverride(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "config-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testVsxConfigYAML); err != nil {
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

// ─── runServer graceful shutdown ─────────────────────────────────────────────

func TestRunServerVsxGracefulShutdown(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "vsx-cfg-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testVsxConfigYAML); err != nil {
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
		done <- runServer(ctx, srv, logger, "vsx-bulwark-test")
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

// ─── RequiresAgeFiltering integration ────────────────────────────────────────

func TestVsixDownloadTrustedBypassesAgeCheck(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			fmt.Fprint(w, testFakeVsix)
			return
		}
		// Return version with no timestamp.
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, "", "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		TrustedPackages: []string{"ms-python.*"},
		Defaults:        config.RulesDefaults{MinPackageAgeDays: 30},
	}
	ts := buildTestServer(t, mock.URL, policy)

	req := httptest.NewRequest(http.MethodGet, testPathExtPythonVsix, nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Errorf("trusted extension should not be blocked, got %d", rec.Code)
	}
}

// ─── fetchUpstream edge cases ────────────────────────────────────────────────

func TestFetchUpstreamBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}

	_, _, _, err := ts.srv.fetchUpstream(mock.URL+"/api/test/ext", nil)
	if err == nil {
		t.Error("expected error for body read error in fetchUpstream")
	}
}

// ─── handleQuery with query string ──────────────────────────────────────────

func TestHandleQueryWithQueryString(t *testing.T) {
	queryResp := vsxQueryResponse{
		Offset:     0,
		TotalSize:  0,
		Extensions: []vsxQueryExtension{},
	}
	body, _ := json.Marshal(queryResp)

	var gotQuery string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/query?namespaceName=ms-python&extensionName=python")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if gotQuery == "" {
		t.Error("expected query string to be forwarded to upstream")
	}
}

// ─── resolveConfig additional paths ──────────────────────────────────────────

func TestResolveConfigAlreadyInstalledVsx(t *testing.T) {
	home := t.TempDir()
	p := newProxyInfo()
	// Create the installed config file so IsInstalledAt returns true.
	cfgDir := filepath.Join(home, bulwarkDir, p.BinaryName)
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	cfgPath := filepath.Join(cfgDir, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("server:\n  port: 18003\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	stub := func(_ installer.ProxyInfo, _ io.Writer) error { return nil }
	// Use a non-existent default path so it falls through to installer check.
	got, err := resolveConfig("/nonexistent/config.yaml", false, p, home, "linux", io.Discard, stub)
	if err != nil {
		t.Fatalf("resolveConfig: %v", err)
	}
	if got != cfgPath {
		t.Errorf("resolveConfig returned %q, want %q", got, cfgPath)
	}
}

func TestResolveConfigAutoSetupSuccessVsx(t *testing.T) {
	home := t.TempDir()
	p := newProxyInfo()
	cfgPath := filepath.Join(home, bulwarkDir, p.BinaryName, "config.yaml")

	setupFn := func(pi installer.ProxyInfo, _ io.Writer) error {
		// Simulate setup by creating the config file.
		dir := filepath.Join(home, bulwarkDir, pi.BinaryName)
		os.MkdirAll(dir, 0o755) //nolint:errcheck
		return os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("server:\n  port: 18003\n"), 0o644)
	}

	got, err := resolveConfig("/nonexistent/config.yaml", false, p, home, "linux", io.Discard, setupFn)
	if err != nil {
		t.Fatalf("resolveConfig auto-setup: %v", err)
	}
	if got != cfgPath {
		t.Errorf("resolveConfig returned %q, want %q", got, cfgPath)
	}
}

func TestResolveConfigAutoSetupFailureVsx(t *testing.T) {
	home := t.TempDir()
	p := newProxyInfo()
	setupFn := func(_ installer.ProxyInfo, _ io.Writer) error {
		return fmt.Errorf("setup boom")
	}

	_, err := resolveConfig("/nonexistent/config.yaml", false, p, home, "linux", io.Discard, setupFn)
	if err == nil {
		t.Error("expected error from auto-setup failure")
	}
	if !strings.Contains(err.Error(), "auto-setup") {
		t.Errorf("error should mention auto-setup: %v", err)
	}
}

// ─── handlePassthrough invalid upstream URL ──────────────────────────────────

func TestHandlePassthroughInvalidUpstreamURL(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/search/python")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500 for invalid passthrough upstream URL, got %d", resp.StatusCode)
	}
}

// ─── handleVsixDownload internal request error ──────────────────────────────

func TestHandleVsixDownloadInternalReqError(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	// With invalid upstream URL, the version metadata fetch fails, then we should
	// get a denial for age filtering (if configured) or the vsix download request fails.
	if resp.StatusCode == http.StatusOK {
		t.Error("should not get 200 for invalid upstream URL")
	}
}

// ─── handleQuery internal request error ──────────────────────────────────────

func TestHandleQueryInternalReqError(t *testing.T) {
	ts := buildTestServer(t, testUpstreamInvalid, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/query?extensionName=python")
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("want 500 for invalid query upstream URL, got %d", resp.StatusCode)
	}
}

// ─── fetchVersionMetaFromAPI body read error ────────────────────────────────

func TestFetchVersionMetaFromAPIBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	meta := ts.srv.fetchVersionMetaFromAPI(testNsPython, testNamePython, testVersionOne)
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for body read error")
	}
}

// ─── handleExtension 500 from upstream ──────────────────────────────────────

func TestHandleExtensionUpstream500(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPython)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream 500, got %d", resp.StatusCode)
	}
}

// ─── handleExtension non-200 non-500 (e.g. 403) ─────────────────────────────

func TestHandleExtensionUpstream403(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"forbidden"}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPython)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 proxied, got %d", resp.StatusCode)
	}
}

// ─── handleExtensionVersion 500 from upstream ────────────────────────────────

func TestHandleExtensionVersionUpstream500(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathExtPythonVer)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream 500, got %d", resp.StatusCode)
	}
}

// ─── namespace "-" passthrough delegation ────────────────────────────────────

func TestExtensionNamespaceDashPassthrough(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, `{"delegated":true}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/namespace-info")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for dash namespace passthrough, got %d", resp.StatusCode)
	}
}

func TestVsixDownloadNamespaceDashPassthrough(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, `{"delegated":true}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + "/api/-/some/v1/file/foo.vsix")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for dash namespace vsix passthrough, got %d", resp.StatusCode)
	}
}

// ─── handleVsixDownload: version blocked after fetching metadata ────────────

func TestHandleVsixDownloadVersionBlockedByAge(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".vsix") {
			fmt.Fprint(w, testFakeVsix)
			return
		}
		body := mockVersionResponse(testNsPython, testNamePython, testVersionOne, recent, "MIT", false)
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)
	resp, _ := http.Get(ts.url + testPathExtPythonVsix)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for age-blocked vsix download, got %d", resp.StatusCode)
	}
}

// TestFilterTransitiveDepsStripsBlockedPack verifies that filterTransitiveDeps
// removes denied extension IDs from an extensionPack array and leaves allowed ones.
func TestFilterTransitiveDepsStripsBlockedPack(t *testing.T) {
	ts := buildTestServer(t, "http://localhost", config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "deny-malicious", Action: "deny", PackagePatterns: []string{"oigotm.*"}},
		},
	})

	// Build a metadata response containing an extensionPack with one blocked and one clean entry.
	const maliciousDep = "oigotm.my-command-palette-extension"
	const cleanDep = "esbenp.prettier-vscode"
	pack, _ := json.Marshal([]string{maliciousDep, cleanDep})
	raw := map[string]json.RawMessage{
		"namespace":     json.RawMessage(`"otoboss"`),
		"name":          json.RawMessage(`"autoimport-extension"`),
		"version":       json.RawMessage(`"1.5.7"`),
		"allVersions":   json.RawMessage(`{"1.5.7":"https://open-vsx.org/api/otoboss/autoimport-extension/1.5.7"}`),
		"extensionPack": pack,
	}
	body, _ := json.Marshal(raw)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()
	ts.srv.cfg.Upstream.URL = mock.URL

	resp, err := http.Get(ts.url + "/api/otoboss/autoimport-extension")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if resp.Header.Get("X-Curation-Policy-Notice") == "" {
		t.Error("expected X-Curation-Policy-Notice header for transitive dep strip")
	}

	var result map[string]json.RawMessage
	b, _ := io.ReadAll(resp.Body)
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	assertPackNotContains(t, result, maliciousDep)
	assertPackContains(t, result, cleanDep)
}

// assertPackContains fails the test if id is absent from the extensionPack field.
func assertPackContains(t *testing.T, result map[string]json.RawMessage, id string) {
	t.Helper()
	for _, got := range extensionPackIDs(t, result) {
		if strings.EqualFold(got, id) {
			return
		}
	}
	t.Errorf("clean dep %q was incorrectly stripped from extensionPack", id)
}

// assertPackNotContains fails the test if id is present in the extensionPack field.
func assertPackNotContains(t *testing.T, result map[string]json.RawMessage, id string) {
	t.Helper()
	for _, got := range extensionPackIDs(t, result) {
		if strings.EqualFold(got, id) {
			t.Errorf("malicious dep %q was NOT stripped from extensionPack", id)
			return
		}
	}
}

// extensionPackIDs extracts the extensionPack array from a JSON result map.
func extensionPackIDs(t *testing.T, result map[string]json.RawMessage) []string {
	t.Helper()
	packRaw, ok := result["extensionPack"]
	if !ok {
		t.Fatal("extensionPack field missing from response")
	}
	var ids []string
	json.Unmarshal(packRaw, &ids) //nolint:errcheck
	return ids
}

// TestFilterTransitiveDepsNoBlockedDeps verifies that extensions with only
// allowed transitive dependencies are returned unchanged (no notice header).
func TestFilterTransitiveDepsNoBlockedDeps(t *testing.T) {
	ts := buildTestServer(t, "http://localhost", config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "deny-malicious", Action: "deny", PackagePatterns: []string{"oigotm.*"}},
		},
	})

	pack, _ := json.Marshal([]string{"esbenp.prettier-vscode", "dbaeumer.vscode-eslint"})
	raw := map[string]json.RawMessage{
		"namespace":     json.RawMessage(`"redhat"`),
		"name":          json.RawMessage(`"java"`),
		"version":       json.RawMessage(`"1.0.0"`),
		"allVersions":   json.RawMessage(`{"1.0.0":"https://open-vsx.org/api/redhat/java/1.0.0"}`),
		"extensionPack": pack,
	}
	body, _ := json.Marshal(raw)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()
	ts.srv.cfg.Upstream.URL = mock.URL

	resp, err := http.Get(ts.url + "/api/redhat/java")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if notice := resp.Header.Get("X-Curation-Policy-Notice"); notice != "" {
		t.Errorf("unexpected policy notice for all-clean deps: %s", notice)
	}
}

// ─── Glassworm / early-exit block + JSON error visibility ────────────────────

// TestFilterTransitiveDepsBlocksParentOnRequiredDep verifies that when a
// required extension dependency (extensionDependencies) is blocked by policy
// the parent extension is itself denied with a descriptive reason.
func TestFilterTransitiveDepsBlocksParentOnRequiredDep(t *testing.T) {
	const blockedDep = "oigotm.my-command-palette-extension"
	const cleanDep = "esbenp.prettier-vscode"

	ts := buildTestServer(t, "http://localhost", config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "deny-malicious", Action: "deny", PackagePatterns: []string{"oigotm.*"}},
		},
	})

	deps, _ := json.Marshal([]string{blockedDep, cleanDep})
	raw := map[string]json.RawMessage{
		"namespace":             json.RawMessage(`"goodpub"`),
		"name":                  json.RawMessage(`"goodext"`),
		"version":               json.RawMessage(`"1.0.0"`),
		"allVersions":           json.RawMessage(`{"1.0.0":"https://open-vsx.org/api/goodpub/goodext/1.0.0"}`),
		"extensionDependencies": deps,
	}
	body, _ := json.Marshal(raw)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()
	ts.srv.cfg.Upstream.URL = mock.URL

	resp, err := http.Get(ts.url + "/api/goodpub/goodext")
	if err != nil {
		t.Fatalf("GET failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("want 403 when required dep is blocked, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), blockedDep) {
		t.Errorf("response body should name the blocked dep; got: %s", string(b))
	}
}

// TestHandleExtensionBlockedBeforeUpstreamFetch verifies that a package-level
// deny blocks the request before the proxy contacts the upstream. This is
// critical for Glassworm IOC extensions that were removed from Open VSX: without
// the early-exit, a request returns 404 (passthrough) instead of 403 (policy).
// The 403 response body must be JSON with a [Bulwark] reason for VS Code visibility.
func TestHandleExtensionBlockedBeforeUpstreamFetch(t *testing.T) {
	upstreamHit := false
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{"oigotm.*"}},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	req := httptest.NewRequest(http.MethodGet, "/api/oigotm/command-palette-extension", nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rec.Code)
	}
	if upstreamHit {
		t.Error("upstream must NOT be contacted when extension is blocked before fetch")
	}
	if ct := rec.Header().Get(hdrContentType); ct != mimeJSON {
		t.Errorf("Content-Type: want %q, got %q", mimeJSON, ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("403 response body must be JSON: %v", err)
	}
	if !strings.Contains(body["error"], "[Bulwark]") {
		t.Errorf("JSON error should contain [Bulwark], got: %q", body["error"])
	}
}

// TestHandleExtensionVersionBlockedJSONError verifies that handleExtensionVersion
// returns a JSON-formatted 403 response so VS Code can surface the block reason.
func TestHandleExtensionVersionBlockedJSONError(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{"oigotm.*"}},
	}}
	ts := buildTestServer(t, testUpstreamInvalid, policy)

	req := httptest.NewRequest(http.MethodGet, "/api/oigotm/malware/1.0.0", nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rec.Code)
	}
	if ct := rec.Header().Get(hdrContentType); ct != mimeJSON {
		t.Errorf("Content-Type: want %q, got %q", mimeJSON, ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("version block response must be JSON: %v", err)
	}
	if !strings.Contains(body["error"], "[Bulwark]") {
		t.Errorf("JSON error should contain [Bulwark], got: %q", body["error"])
	}
}

// TestHandleVsixDownloadBlockedJSONError verifies that handleVsixDownload
// returns a JSON-formatted 403 response so VS Code can surface the block reason.
func TestHandleVsixDownloadBlockedJSONError(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleDeny, Action: "deny", PackagePatterns: []string{"oigotm.*"}},
	}}
	ts := buildTestServer(t, testUpstreamInvalid, policy)

	req := httptest.NewRequest(http.MethodGet, "/api/oigotm/malware/1.0.0/file/oigotm.malware-1.0.0.vsix", nil)
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("want 403, got %d", rec.Code)
	}
	if ct := rec.Header().Get(hdrContentType); ct != mimeJSON {
		t.Errorf("Content-Type: want %q, got %q", mimeJSON, ct)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("vsix block response must be JSON: %v", err)
	}
	if !strings.Contains(body["error"], "[Bulwark]") {
		t.Errorf("JSON error should contain [Bulwark], got: %q", body["error"])
	}
}

// ─── handleGalleryPassthrough ─────────────────────────────────────────────────

// TestHandleGalleryPassthroughSuccess verifies that gallery requests are
// forwarded to the upstream and the response is proxied back unchanged.
func TestHandleGalleryPassthroughSuccess(t *testing.T) {
	const galleryBody = `{"results":[{"extensions":[]}]}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", mimeJSON)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, galleryBody)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})

	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST gallery: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "extensions") {
		t.Errorf("body not proxied: %s", string(b))
	}
}

// TestHandleGalleryPassthroughWithQueryString verifies that the query string
// from the original request is appended to the upstream URL.
func TestHandleGalleryPassthroughWithQueryString(t *testing.T) {
	receivedQuery := ""
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/vscode/gallery/search?query=go")
	if err != nil {
		t.Fatalf("GET gallery: %v", err)
	}
	resp.Body.Close()

	if receivedQuery != "query=go" {
		t.Errorf("query string not forwarded: got %q", receivedQuery)
	}
}

// TestHandleGalleryPassthroughUpstreamError verifies that when the upstream is
// unreachable the proxy returns 502.
func TestHandleGalleryPassthroughUpstreamError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/vscode/gallery/extensionquery")
	if err != nil {
		t.Fatalf("GET gallery: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestHandleGalleryPassthroughItemRoute verifies the /vscode/item/{path} route
// is also handled by handleGalleryPassthrough.
func TestHandleGalleryPassthroughItemRoute(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/vscode/item/ms-python.python")
	if err != nil {
		t.Fatalf("GET item: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

// TestHandleGalleryPassthroughStripsHSTS verifies that HSTS headers from the
// upstream are stripped before the response reaches the client. This prevents
// Chromium from caching an HSTS policy for localhost and breaking all gallery
// connectivity with "Failed to fetch" errors.
func TestHandleGalleryPassthroughStripsHSTS(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", mimeJSON)
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("X-Custom-Header", "keep-me")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	// Use a generic gallery sub-path that is NOT the extensionUrlTemplate pattern
	// (/vscode/gallery/vscode/{pub}/{name}/latest) which now has its own handler.
	resp, err := http.Get(ts.url + "/vscode/gallery/some/arbitrary/path")
	if err != nil {
		t.Fatalf("GET gallery passthrough: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	if hsts := resp.Header.Get("Strict-Transport-Security"); hsts != "" {
		t.Errorf("Strict-Transport-Security must be stripped, got %q", hsts)
	}
	if resp.Header.Get("X-Custom-Header") != "keep-me" {
		t.Error("X-Custom-Header should be preserved")
	}
}

// TestFilterExtensionResponseBadUpstreamJSON verifies that a malformed upstream
// extension-metadata response results in a 502 from the proxy when fail_mode is
// closed. In open mode the proxy falls through; closed is the tighter gate.
func TestFilterExtensionResponseBadUpstreamJSON(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", mimeJSON)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "not-json{{{")
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeClosed})
	resp, err := http.Get(ts.url + testPathExtPython)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for invalid upstream JSON with fail_mode=closed, got %d", resp.StatusCode)
	}
}

// ─── handleGalleryQuery coverage ────────────────────────────────────────────

// TestGalleryQueryReadError covers the io.ReadAll failure path when the
// upstream connection drops mid-response.
func TestGalleryQueryReadError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "99999")
		w.WriteHeader(http.StatusOK)
		// Write partial data, then drop the connection.
		fmt.Fprint(w, `{"partial":`)
		panic(http.ErrAbortHandler)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestGalleryQueryWithQueryString verifies the query string is forwarded.
func TestGalleryQueryWithQueryString(t *testing.T) {
	receivedQuery := ""
	body := `{"results":[{"extensions":[]}]}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.RawQuery
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, body)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery?api-version=3.0-preview.1", strings.NewReader(`{}`))
	req.Header.Set(hdrContentType, mimeJSON)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	resp.Body.Close()

	if receivedQuery != "api-version=3.0-preview.1" {
		t.Errorf("query string not forwarded: got %q", receivedQuery)
	}
}

// TestGalleryQueryNoContentType verifies that a response is still returned
// when upstream does not set an explicit Content-Type header.
func TestGalleryQueryNoContentType(t *testing.T) {
	body := `{"results":[{"extensions":[]}]}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, body)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	// Response body should still be valid.
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "results") {
		t.Errorf("body should contain results: %s", string(b))
	}
}

// ─── handleGalleryVspackage coverage ────────────────────────────────────────

// TestGalleryVspackageUpstreamDown verifies 502 when the upstream is unreachable.
func TestGalleryVspackageUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	dead.Close()

	ts := buildTestServer(t, dead.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/ms-python/vsextensions/python/1.0.0/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestGalleryVspackagePreReleaseBlocked verifies pre-release versions are blocked.
func TestGalleryVspackagePreReleaseBlocked(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(mockVersionResponse("somedev", "ext", testVersionPre, testTimeOld2020, "MIT", true)) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, Action: "deny", BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/vscode/gallery/publishers/somedev/vsextensions/ext/" + testVersionPre + "/vspackage")
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for pre-release, got %d", resp.StatusCode)
	}
}

// TestFilterGalleryQueryResponseMalformedJSON verifies graceful fallback for bad JSON.
func TestFilterGalleryQueryResponseMalformedJSON(t *testing.T) {
	input := []byte("not-json{{{")
	engine := rules.New(config.PolicyConfig{})
	result, removed := filterGalleryQueryResponse(input, engine, testErrorLogger(), "")
	if removed != 0 {
		t.Errorf("want 0 removed for bad json, got %d", removed)
	}
	if string(result) != string(input) {
		t.Errorf("expected original body returned for bad json")
	}
}

// TestFilterGalleryQueryResponseEmptyResults verifies handling of empty results.
func TestFilterGalleryQueryResponseEmptyResults(t *testing.T) {
	input := []byte(`{"results":[]}`)
	engine := rules.New(config.PolicyConfig{})
	result, removed := filterGalleryQueryResponse(input, engine, testErrorLogger(), "")
	if removed != 0 {
		t.Errorf("want 0 removed for empty results, got %d", removed)
	}
	if !strings.Contains(string(result), "results") {
		t.Errorf("expected results key in output")
	}
}

// TestSetResultMetadataCountSetsExactCount verifies that TotalCount is set to
// the exact number of kept extensions so VS Code's virtual list matches the array.
func TestSetResultMetadataCountSetsExactCount(t *testing.T) {
	resultMap := map[string]json.RawMessage{
		"resultMetadata": json.RawMessage(`[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":10}]}]`),
	}
	setResultMetadataCount(resultMap, 7)
	var meta []map[string]json.RawMessage
	if err := json.Unmarshal(resultMap["resultMetadata"], &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	var items []map[string]json.RawMessage
	json.Unmarshal(meta[0]["metadataItems"], &items) //nolint:errcheck
	var count int
	json.Unmarshal(items[0]["count"], &count) //nolint:errcheck
	if count != 7 {
		t.Errorf("TotalCount: want 7, got %d", count)
	}
}

// TestSetResultMetadataCountSetsZero verifies the count can be set to zero.
func TestSetResultMetadataCountSetsZero(t *testing.T) {
	resultMap := map[string]json.RawMessage{
		"resultMetadata": json.RawMessage(`[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":2}]}]`),
	}
	setResultMetadataCount(resultMap, 0)
	var meta []map[string]json.RawMessage
	json.Unmarshal(resultMap["resultMetadata"], &meta) //nolint:errcheck
	var items []map[string]json.RawMessage
	json.Unmarshal(meta[0]["metadataItems"], &items) //nolint:errcheck
	var count int
	json.Unmarshal(items[0]["count"], &count) //nolint:errcheck
	if count != 0 {
		t.Errorf("TotalCount: want 0, got %d", count)
	}
}

// TestSetResultMetadataCountOverwritesAnyValue verifies that the count is unconditionally set.
func TestSetResultMetadataCountOverwritesAnyValue(t *testing.T) {
	resultMap := map[string]json.RawMessage{
		"resultMetadata": json.RawMessage(`[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":5}]}]`),
	}
	setResultMetadataCount(resultMap, 5)
	var meta []map[string]json.RawMessage
	json.Unmarshal(resultMap["resultMetadata"], &meta) //nolint:errcheck
	var items []map[string]json.RawMessage
	json.Unmarshal(meta[0]["metadataItems"], &items) //nolint:errcheck
	var count int
	json.Unmarshal(items[0]["count"], &count) //nolint:errcheck
	if count != 5 {
		t.Errorf("TotalCount: want 5, got %d", count)
	}
}

// TestSetResultMetadataCountNoMetadata verifies graceful no-op when field is absent.
func TestSetResultMetadataCountNoMetadata(t *testing.T) {
	resultMap := map[string]json.RawMessage{}
	// Should not panic.
	setResultMetadataCount(resultMap, 3)
}

// TestSetResultMetadataCountMalformedJSON verifies graceful handling of bad JSON.
func TestSetResultMetadataCountMalformedJSON(t *testing.T) {
	resultMap := map[string]json.RawMessage{
		"resultMetadata": json.RawMessage(`not-json`),
	}
	// Should not panic.
	setResultMetadataCount(resultMap, 3)
}

// TestFilterGalleryExtensionStripsBlockedVersions verifies that version-level
// filtering removes only blocked versions, keeping the extension with passing ones.
func TestFilterGalleryExtensionStripsBlockedVersions(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	raw := json.RawMessage(fmt.Sprintf(`{
		"publisher":{"publisherName":"test-co"},
		"extensionName":"multi-ver",
		"versions":[
			{"version":"2.0.0","lastUpdated":%q},
			{"version":"1.0.0","lastUpdated":%q}
		]
	}`, recent, old))

	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}})
	result, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), "")
	if blocked {
		t.Fatal("extension with a passing version should not be fully blocked")
	}
	var m map[string]json.RawMessage
	json.Unmarshal(result, &m) //nolint:errcheck
	var versions []map[string]json.RawMessage
	json.Unmarshal(m["versions"], &versions) //nolint:errcheck
	if len(versions) != 1 {
		t.Fatalf("want 1 surviving version, got %d", len(versions))
	}
	var ver string
	json.Unmarshal(versions[0]["version"], &ver) //nolint:errcheck
	if ver != "1.0.0" {
		t.Errorf("want surviving version 1.0.0, got %s", ver)
	}
}

// TestFilterGalleryExtensionBlocksWhenAllVersionsFail verifies full removal
// when every version fails policy.
func TestFilterGalleryExtensionBlocksWhenAllVersionsFail(t *testing.T) {
	recent := time.Now().UTC().Add(-10 * time.Minute).Format(time.RFC3339)
	raw := json.RawMessage(fmt.Sprintf(`{
		"publisher":{"publisherName":"new-co"},
		"extensionName":"new-ext",
		"versions":[
			{"version":"1.0.0","lastUpdated":%q}
		]
	}`, recent))

	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleAge7d, MinPackageAgeDays: 7},
	}})
	_, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), "")
	if !blocked {
		t.Error("extension with all versions failing should be fully blocked")
	}
}

// TestFilterGalleryExtensionPackageDeny verifies that a package-level deny
// blocks the extension regardless of version ages.
func TestFilterGalleryExtensionPackageDeny(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	raw := json.RawMessage(fmt.Sprintf(`{
		"publisher":{"publisherName":"evil"},
		"extensionName":"malware",
		"versions":[
			{"version":"1.0.0","lastUpdated":%q}
		]
	}`, old))

	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "deny-evil", Action: "deny", PackagePatterns: []string{"evil.malware"}},
	}})
	_, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), "")
	if !blocked {
		t.Error("package-level deny should block the extension entirely")
	}
}

// TestFilterExtensionsInResultUpdatesTotalCount verifies that filtering extensions
// decrements resultMetadata TotalCount, preventing VS Code's renderer crash.
func TestFilterExtensionsInResultUpdatesTotalCount(t *testing.T) {
	input := json.RawMessage(`{
		"extensions":[
			{"publisher":{"publisherName":"ms-python"},"extensionName":"python","versions":[]},
			{"publisher":{"publisherName":"evil"},"extensionName":"malware","versions":[]}
		],
		"resultMetadata":[{"metadataType":"ResultCount","metadataItems":[{"name":"TotalCount","count":2}]}]
	}`)
	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "deny-evil", Action: "deny", PackagePatterns: []string{"evil.malware"}},
	}})

	result, removed := filterExtensionsInResult(input, engine, testErrorLogger(), "")
	if removed != 1 {
		t.Fatalf("want 1 removed, got %d", removed)
	}

	var resultMap map[string]json.RawMessage
	json.Unmarshal(result, &resultMap) //nolint:errcheck
	var meta []map[string]json.RawMessage
	json.Unmarshal(resultMap["resultMetadata"], &meta) //nolint:errcheck
	var items []map[string]json.RawMessage
	json.Unmarshal(meta[0]["metadataItems"], &items) //nolint:errcheck
	var count int
	json.Unmarshal(items[0]["count"], &count) //nolint:errcheck
	if count != 1 {
		t.Errorf("TotalCount after filter: want 1, got %d", count)
	}
}

// ─── Marketplace / URL translation tests ─────────────────────────────────────

func TestIsMarketplaceTrue(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://marketplace.visualstudio.com", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if !srv.isMarketplace() {
		t.Error("expected isMarketplace() == true for marketplace registry type")
	}
}

func TestIsMarketplaceFalseForOpenVSX(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}
	if srv.isMarketplace() {
		t.Error("expected isMarketplace() == false for openvsx registry type")
	}
}

func TestUpstreamGalleryURLOpenVSX(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamGalleryURL("/vscode/gallery/extensionquery")
	want := "https://open-vsx.org/vscode/gallery/extensionquery"
	if got != want {
		t.Errorf("upstreamGalleryURL: got %q, want %q", got, want)
	}
}

func TestUpstreamGalleryURLMarketplace(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://marketplace.visualstudio.com", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamGalleryURL("/vscode/gallery/extensionquery")
	want := "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery"
	if got != want {
		t.Errorf("upstreamGalleryURL: got %q, want %q", got, want)
	}
}

func TestUpstreamItemURLOpenVSX(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamItemURL("/vscode/item?itemName=ms-python.python")
	want := "https://open-vsx.org/vscode/item?itemName=ms-python.python"
	if got != want {
		t.Errorf("upstreamItemURL: got %q, want %q", got, want)
	}
}

func TestUpstreamItemURLMarketplace(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://marketplace.visualstudio.com", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamItemURL("/vscode/item?itemName=ms-python.python")
	want := "https://marketplace.visualstudio.com/items?itemName=ms-python.python"
	if got != want {
		t.Errorf("upstreamItemURL: got %q, want %q", got, want)
	}
}

func TestUpstreamAPIURLPassthrough(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamAPIURL("/api/ms-python/python")
	want := "https://open-vsx.org/api/ms-python/python"
	if got != want {
		t.Errorf("upstreamAPIURL: got %q, want %q", got, want)
	}
}

func TestUpstreamVsixURLOpenVSX(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	proxyPath := "/api/ms-python/python/2024.1.1/file/ms-python.python-2024.1.1.vsix"
	got := srv.upstreamVsixURL(proxyPath, "ms-python", "python", "2024.1.1")
	want := "https://open-vsx.org" + proxyPath
	if got != want {
		t.Errorf("upstreamVsixURL(openvsx): got %q, want %q", got, want)
	}
}

func TestUpstreamVsixURLMarketplace(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://marketplace.visualstudio.com", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	got := srv.upstreamVsixURL("/api/ms-python/python/2024.1.1/file/ms-python.python-2024.1.1.vsix", "ms-python", "python", "2024.1.1")
	want := "https://marketplace.visualstudio.com/_apis/public/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage"
	if got != want {
		t.Errorf("upstreamVsixURL(marketplace): got %q, want %q", got, want)
	}
}

func TestExtractMarketplaceVersionMetaValid(t *testing.T) {
	body := []byte(`{"results":[{"extensions":[{"versions":[{"version":"2024.1.1","lastUpdated":"2024-06-15T10:00:00Z"}]}]}]}`)
	meta := extractMarketplaceVersionMeta(body, "2024.1.1")
	if meta.Version != "2024.1.1" {
		t.Errorf("version: want %q, got %q", "2024.1.1", meta.Version)
	}
	if meta.PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt")
	}
	wantYear := 2024
	if meta.PublishedAt.Year() != wantYear {
		t.Errorf("year: want %d, got %d", wantYear, meta.PublishedAt.Year())
	}
}

func TestExtractMarketplaceVersionMetaMicrosoftTimestamp(t *testing.T) {
	body := []byte(`{"results":[{"extensions":[{"versions":[{"version":"1.2.3","lastUpdated":"2024-03-20T14:30:00.1234567Z"}]}]}]}`)
	meta := extractMarketplaceVersionMeta(body, "1.2.3")
	if meta.PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt for Microsoft timestamp format")
	}
}

func TestExtractMarketplaceVersionMetaVersionNotFound(t *testing.T) {
	body := []byte(`{"results":[{"extensions":[{"versions":[{"version":"1.0.0","lastUpdated":"2024-01-01T00:00:00Z"}]}]}]}`)
	meta := extractMarketplaceVersionMeta(body, "9.9.9")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt when version not found")
	}
}

func TestExtractMarketplaceVersionMetaMalformedJSON(t *testing.T) {
	meta := extractMarketplaceVersionMeta([]byte("not-json{"), "1.0.0")
	if meta.Version != "1.0.0" {
		t.Errorf("version: want %q, got %q", "1.0.0", meta.Version)
	}
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for malformed JSON")
	}
}

func TestExtractMarketplaceVersionMetaEmptyResults(t *testing.T) {
	meta := extractMarketplaceVersionMeta([]byte(`{"results":[]}`), "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for empty results")
	}
}

// TestHandleExtensionViaGalleryIntegration verifies the marketplace gallery
// handler returns filtered extension data from a mock marketplace upstream.
func TestHandleExtensionViaGalleryIntegration(t *testing.T) {
	galleryResp := `{"results":[{"extensions":[{"extensionId":"abc","publisher":{"publisherName":"ms-python"},"extensionName":"python","versions":[{"version":"2024.1.1","lastUpdated":"2024-06-15T10:00:00Z","files":[]}],"statistics":[]}],"pagingToken":null,"resultMetadata":[]}]}`

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "extensionquery") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, galleryResp)
			return
		}
		http.NotFound(w, r)
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

	resp, err := http.Get(ts.URL + "/api/ms-python/python")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

// TestGalleryQueryViaMarketplace verifies gallery query requests are correctly
// forwarded with the translated path when the upstream is the marketplace.
func TestGalleryQueryViaMarketplace(t *testing.T) {
	var receivedPath string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"results":[{"extensions":[],"pagingToken":null,"resultMetadata":[]}]}`)
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

	body := strings.NewReader(`{"filters":[{"criteria":[{"filterType":8,"value":"Microsoft.VisualStudio.Code"}]}],"flags":914}`)
	resp, err := http.Post(ts.URL+"/vscode/gallery/extensionquery", "application/json", body)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	want := "/_apis/public/gallery/extensionquery"
	if receivedPath != want {
		t.Errorf("upstream received path %q, want %q", receivedPath, want)
	}
}

// TestHandleExtensionViaGalleryUpstreamError covers the error branch when the
// upstream marketplace is unreachable.
func TestHandleExtensionViaGalleryUpstreamError(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "http://127.0.0.1:1", TimeoutSeconds: 1, RegistryType: config.RegistryMarketplace},
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

	resp, err := http.Get(ts.URL + "/api/ms-python/python")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502, got %d", resp.StatusCode)
	}
}

// TestHandleExtensionViaGalleryUpstreamNonOK covers the branch when the
// marketplace returns a non-200 status.
func TestHandleExtensionViaGalleryUpstreamNonOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"error":"not found"}`)
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
	srv, _ := buildServer(cfg, logger, logLevel)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ms-python/python")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

// TestHandleExtensionViaGalleryPolicyFiltering verifies that gallery query
// filtering is applied and the policy notice header is set when extensions are blocked.
func TestHandleExtensionViaGalleryPolicyFiltering(t *testing.T) {
	galleryResp := `{"results":[{"extensions":[{"extensionId":"abc","publisher":{"publisherName":"evil"},"extensionName":"malware-loader","versions":[{"version":"1.0.0","lastUpdated":"2024-06-15T10:00:00Z","files":[]}],"statistics":[]}],"pagingToken":null,"resultMetadata":[]}]}`

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, galleryResp)
	}))
	defer mock.Close()

	denyTrue := true
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy: config.PolicyConfig{
			Rules: []config.PackageRule{
				{Name: "evil.malware-loader", Action: "deny", Enabled: &denyTrue},
			},
		},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/evil/malware-loader")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	// The package-level deny should block before gallery query.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied extension, got %d", resp.StatusCode)
	}
}

// TestHandleExtensionViaGalleryContentTypeFallback verifies the handler sets
// Content-Type to application/json when the upstream response Content-Type header
// is absent. Uses a custom RoundTripper to strip the header.
func TestHandleExtensionViaGalleryContentTypeFallback(t *testing.T) {
	galleryResp := `{"results":[{"extensions":[{"extensionId":"xyz","publisher":{"publisherName":"test"},"extensionName":"ext","versions":[{"version":"1.0.0","lastUpdated":"2024-01-01T00:00:00Z","files":[]}],"statistics":[]}],"pagingToken":null,"resultMetadata":[]}]}`

	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "http://fake-marketplace.local", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)
	// Override the transport to return a response with no Content-Type header.
	srv.upstream.Transport = &roundTripperFunc{fn: func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(strings.NewReader(galleryResp)),
		}, nil
	}}
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/test/ext")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "json") {
		t.Errorf("expected json content-type fallback, got %q", ct)
	}
}

// TestFetchVersionMetaViaMarketplace exercises the fetchVersionMetaFromAPI →
// fetchVersionMetaFromMarketplace path with a mock marketplace server.
func TestFetchVersionMetaViaMarketplace(t *testing.T) {
	galleryResp := `{"results":[{"extensions":[{"versions":[{"version":"2024.1.1","lastUpdated":"2024-06-15T10:00:00Z"}]}]}]}`

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, galleryResp)
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
	srv, _ := buildServer(cfg, logger, logLevel)

	meta := srv.fetchVersionMetaFromAPI("ms-python", "python", "2024.1.1")
	if meta.Version != "2024.1.1" {
		t.Errorf("version: want %q, got %q", "2024.1.1", meta.Version)
	}
	if meta.PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt from marketplace fetch")
	}
}

// TestFetchVersionMetaMarketplaceUpstreamError covers the error path.
func TestFetchVersionMetaMarketplaceUpstreamError(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "http://127.0.0.1:1", TimeoutSeconds: 1, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	meta := srv.fetchVersionMetaFromAPI("ms-python", "python", "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt when upstream is unreachable")
	}
}

// TestFetchVersionMetaMarketplaceNonOK covers the non-200 status branch.
func TestFetchVersionMetaMarketplaceNonOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
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
	srv, _ := buildServer(cfg, logger, logLevel)

	meta := srv.fetchVersionMetaFromAPI("ms-python", "python", "1.0.0")
	if !meta.PublishedAt.IsZero() {
		t.Error("expected zero PublishedAt for non-200 upstream")
	}
}

// TestVsixDownloadViaMarketplace verifies VSIX download uses the marketplace
// vspackage URL translation path.
func TestVsixDownloadViaMarketplace(t *testing.T) {
	var receivedPath string
	galleryResp := `{"results":[{"extensions":[{"versions":[{"version":"2024.1.1","lastUpdated":"2024-06-15T10:00:00Z"}]}]}]}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		if strings.Contains(r.URL.Path, "vspackage") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("fake-vsix")) //nolint:errcheck
			return
		}
		if strings.Contains(r.URL.Path, "extensionquery") {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, galleryResp)
			return
		}
		http.NotFound(w, r)
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
	srv, _ := buildServer(cfg, logger, logLevel)
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ms-python/python/2024.1.1/file/ms-python.python-2024.1.1.vsix")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
	wantPath := "/_apis/public/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage"
	if receivedPath != wantPath {
		t.Errorf("upstream path: got %q, want %q", receivedPath, wantPath)
	}
}

// ─── CORS middleware tests ──────────────────────────────────────────────────

func TestCORSHeadersPresentOnGET(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Metrics:  config.MetricsConfig{Enabled: true},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	srv.handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Error("Access-Control-Allow-Methods header missing")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("Access-Control-Allow-Headers header missing")
	}
	if got := rec.Header().Get("Access-Control-Expose-Headers"); got == "" {
		t.Error("Access-Control-Expose-Headers header missing")
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "86400" {
		t.Errorf("Access-Control-Max-Age = %q, want %q", got, "86400")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestCORSOptionsPreflightReturns204(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "debug", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/vscode/gallery/extensionquery", nil)
	srv.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("OPTIONS Access-Control-Allow-Origin = %q, want %q", got, "*")
	}
}

func TestCORSOptionsOnMarketplacePrefix(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/_apis/public/gallery/extensionquery", nil)
	srv.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

func TestStatusRecorderCapturesCode(t *testing.T) {
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec, status: http.StatusOK}

	sr.WriteHeader(http.StatusForbidden)
	if sr.status != http.StatusForbidden {
		t.Errorf("status = %d, want %d", sr.status, http.StatusForbidden)
	}

	// Second WriteHeader must be a no-op for the captured status.
	sr.WriteHeader(http.StatusOK)
	if sr.status != http.StatusForbidden {
		t.Errorf("status changed to %d after second WriteHeader, want %d", sr.status, http.StatusForbidden)
	}
}

func TestRequestLoggingAtDebugLevel(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "debug", Format: "text"},
		Metrics:  config.MetricsConfig{Enabled: true},
	}
	cfg.Defaults()

	var buf strings.Builder
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelDebug)
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: lvl}))
	srv, _ := buildServer(cfg, logger, lvl)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	srv.handler.ServeHTTP(rec, req)

	logOutput := buf.String()
	if !strings.Contains(logOutput, "request") {
		t.Error("debug log should contain 'request' entry")
	}
	if !strings.Contains(logOutput, "/metrics") {
		t.Error("debug log should contain the request path")
	}
}

// ─── /_apis/public/gallery route alias tests ────────────────────────────────

func TestMarketplacePrefixGalleryQuery(t *testing.T) {
	galleryResp := `{"results":[{"extensions":[{"publisher":{"publisherName":"ms-python"},"extensionName":"python"}],"resultMetadata":[]}]}`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", mimeJSON)
		fmt.Fprint(w, galleryResp)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Post(ts.url+"/_apis/public/gallery/extensionquery", mimeJSON, strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	if ts.srv.reqTotal.Load() == 0 {
		t.Error("reqTotal should be >0 for /_apis/public/gallery/extensionquery")
	}
}

func TestMarketplacePrefixGalleryPassthrough(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", mimeJSON)
		fmt.Fprint(w, `{"status":"ok"}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/_apis/public/gallery/flags")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
	if ts.srv.reqTotal.Load() == 0 {
		t.Error("reqTotal should be >0 for /_apis/public/gallery passthrough")
	}
}

func TestMarketplacePrefixGalleryVspackage(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "extensionquery") {
			w.Header().Set("Content-Type", mimeJSON)
			fmt.Fprint(w, `{"results":[{"extensions":[{"versions":[{"version":"2024.1.1","lastUpdated":"2021-01-01T00:00:00Z"}]}]}]}`)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		fmt.Fprint(w, testFakeVsix)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/_apis/public/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf(testFmtWant200, resp.StatusCode)
	}
}

// ─── upstreamGalleryURL path normalization ──────────────────────────────────

func TestUpstreamGalleryURLNormalizesMarketplacePrefixOpenVSX(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://open-vsx.org", TimeoutSeconds: 5, RegistryType: config.RegistryOpenVSX},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	got := srv.upstreamGalleryURL("/_apis/public/gallery/extensionquery")
	want := "https://open-vsx.org/vscode/gallery/extensionquery"
	if got != want {
		t.Errorf("upstreamGalleryURL: got %q, want %q", got, want)
	}
}

func TestUpstreamGalleryURLNormalizesMarketplacePrefixMarketplace(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: "https://marketplace.visualstudio.com", TimeoutSeconds: 5, RegistryType: config.RegistryMarketplace},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	got := srv.upstreamGalleryURL("/_apis/public/gallery/extensionquery")
	want := "https://marketplace.visualstudio.com/_apis/public/gallery/extensionquery"
	if got != want {
		t.Errorf("upstreamGalleryURL: got %q, want %q", got, want)
	}
}

// TestCORSWithOriginHeaderMirrorsOrigin verifies that when a request carries an
// Origin header the middleware echoes it back (instead of "*") and adds
// Access-Control-Allow-Credentials: true so credentialed fetch calls from the
// VS Code Extensions webview work in VS Code 1.112+.
func TestCORSWithOriginHeaderMirrorsOrigin(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Metrics:  config.MetricsConfig{Enabled: true},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	const vsCodeOrigin = "vscode-file://vscode-app"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set("Origin", vsCodeOrigin)
	srv.handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != vsCodeOrigin {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, vsCodeOrigin)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want \"true\"", got)
	}
	if got := rec.Header().Get("Vary"); !strings.Contains(got, "Origin") {
		t.Errorf("Vary = %q, want it to contain \"Origin\"", got)
	}
}

// TestCORSPreflightEchoesRequestHeaders verifies that OPTIONS preflight
// responses echo back Access-Control-Request-Headers so that new VS Code
// versions can add arbitrary gallery request headers without a proxy update.
func TestCORSPreflightEchoesRequestHeaders(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	const requested = "x-market-client-id, x-market-telemetry-id, x-new-future-header"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodOptions, "/vscode/gallery/extensionquery", nil)
	req.Header.Set("Access-Control-Request-Headers", requested)
	srv.handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != requested {
		t.Errorf("Access-Control-Allow-Headers = %q, want %q", got, requested)
	}
}

// TestCORSExcludedFromAdminEndpoints verifies that admin endpoints do not
// receive CORS headers, preventing cross-origin access to administrative
// functions like log-level changes.
func TestCORSExcludedFromAdminEndpoints(t *testing.T) {
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 18003},
		Upstream: config.UpstreamConfig{URL: testUpstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger(cfg.Logging.Format, cfg.Logging.Level, "")
	srv, _ := buildServer(cfg, logger, logLevel)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/admin/log-level", nil)
	req.Header.Set("Origin", "https://evil.com")
	srv.handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("admin endpoint should not have Access-Control-Allow-Origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("admin endpoint should not have Access-Control-Allow-Credentials, got %q", got)
	}
}

// TestGalleryPassthroughForwardsMarketClientID confirms that
// handleGalleryPassthrough forwards X-Market-Client-Id (and related headers)
// to the upstream. Without this header the Microsoft Marketplace returns 400.
func TestGalleryPassthroughForwardsMarketClientID(t *testing.T) {
	const wantClientID = "VSCode 1.112.0"
	var gotClientID string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientID = r.Header.Get("X-Market-Client-Id")
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodGet, ts.url+"/vscode/gallery/vscode/esbenp/prettier-vscode/latest", nil)
	req.Header.Set("X-Market-Client-Id", wantClientID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if gotClientID != wantClientID {
		t.Errorf("upstream X-Market-Client-Id = %q, want %q", gotClientID, wantClientID)
	}
}

// TestGalleryQueryForwardsMarketClientID confirms that handleGalleryQuery
// forwards X-Market-Client-Id to the upstream for extension search.
func TestGalleryQueryForwardsMarketClientID(t *testing.T) {
	const wantClientID = "VSCode 1.112.0"
	var gotClientID string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientID = r.Header.Get("X-Market-Client-Id")
		w.Header().Set("Content-Type", mimeJSON)
		fmt.Fprint(w, `{"results":[]}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	req, _ := http.NewRequest(http.MethodPost, ts.url+"/vscode/gallery/extensionquery", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", mimeJSON)
	req.Header.Set("X-Market-Client-Id", wantClientID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()

	if gotClientID != wantClientID {
		t.Errorf("upstream X-Market-Client-Id = %q, want %q", gotClientID, wantClientID)
	}
}

// ─── forwardRequestHeaders ────────────────────────────────────────────────────

// TestForwardRequestHeadersForwardsAll verifies that standard headers are copied
// from src to dst.
func TestForwardRequestHeadersForwardsAll(t *testing.T) {
	src := httptest.NewRequest(http.MethodGet, "/", nil)
	src.Header.Set("Content-Type", mimeJSON)
	src.Header.Set("Accept", mimeJSON)
	src.Header.Set("X-Market-Client-Id", "VSCode 1.112.0")
	src.Header.Set("X-Custom-Future-Header", "value")

	dst, _ := http.NewRequest(http.MethodGet, "http://upstream/", nil)
	forwardRequestHeaders(src, dst)

	for _, hdr := range []string{"Content-Type", "Accept", "X-Market-Client-Id", "X-Custom-Future-Header"} {
		if dst.Header.Get(hdr) == "" {
			t.Errorf("header %q not forwarded to upstream", hdr)
		}
	}
}

// TestForwardRequestHeadersExcludesHopByHop verifies that RFC 7230 hop-by-hop
// headers are never forwarded regardless of what the client sends.
func TestForwardRequestHeadersExcludesHopByHop(t *testing.T) {
	src := httptest.NewRequest(http.MethodGet, "/", nil)
	for h := range hopByHopHeaders {
		src.Header.Set(h, "should-not-appear")
	}
	src.Header.Set("X-Safe-Header", "keep-me")

	dst, _ := http.NewRequest(http.MethodGet, "http://upstream/", nil)
	forwardRequestHeaders(src, dst)

	for h := range hopByHopHeaders {
		if dst.Header.Get(h) != "" {
			t.Errorf("hop-by-hop header %q must not be forwarded", h)
		}
	}
	if dst.Header.Get("X-Safe-Header") == "" {
		t.Error("X-Safe-Header should have been forwarded")
	}
}

// TestForwardRequestHeadersWithExclusion verifies that caller-specified headers
// are excluded in addition to hop-by-hop headers.
func TestForwardRequestHeadersWithExclusion(t *testing.T) {
	src := httptest.NewRequest(http.MethodGet, "/", nil)
	src.Header.Set("Accept-Encoding", "gzip")
	src.Header.Set("Accept", mimeJSON)

	dst, _ := http.NewRequest(http.MethodGet, "http://upstream/", nil)
	forwardRequestHeaders(src, dst, "Accept-Encoding")

	if dst.Header.Get("Accept-Encoding") != "" {
		t.Error("Accept-Encoding must not be forwarded when explicitly excluded")
	}
	if dst.Header.Get("Accept") == "" {
		t.Error("Accept should still be forwarded")
	}
}

// TestForwardResponseHeadersCopiesSafe verifies that normal upstream response
// headers are forwarded to the client.
func TestForwardResponseHeadersCopiesSafe(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", mimeJSON)
	src.Set("X-Custom", "value")
	src.Set("Cache-Control", "no-cache")

	dst := http.Header{}
	forwardResponseHeaders(src, dst)

	if dst.Get("Content-Type") != mimeJSON {
		t.Error("Content-Type must be forwarded")
	}
	if dst.Get("X-Custom") != "value" {
		t.Error("X-Custom must be forwarded")
	}
	if dst.Get("Cache-Control") != "no-cache" {
		t.Error("Cache-Control must be forwarded")
	}
}

// TestForwardResponseHeadersStripsHSTS verifies that Strict-Transport-Security
// from the upstream is NOT forwarded. Forwarding HSTS to a localhost HTTP proxy
// causes Chromium to cache an HSTS pin for localhost and silently upgrade all
// subsequent requests to HTTPS, breaking VS Code gallery connectivity.
func TestForwardResponseHeadersStripsHSTS(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", mimeJSON)
	src.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
	src.Set("Public-Key-Pins", "pin-sha256=abc; max-age=600")
	src.Set("Public-Key-Pins-Report-Only", "pin-sha256=xyz; max-age=600")

	dst := http.Header{}
	forwardResponseHeaders(src, dst)

	if dst.Get("Content-Type") != mimeJSON {
		t.Error("Content-Type must be forwarded")
	}
	if dst.Get("Strict-Transport-Security") != "" {
		t.Error("Strict-Transport-Security must NOT be forwarded to localhost proxy clients")
	}
	if dst.Get("Public-Key-Pins") != "" {
		t.Error("Public-Key-Pins must NOT be forwarded")
	}
	if dst.Get("Public-Key-Pins-Report-Only") != "" {
		t.Error("Public-Key-Pins-Report-Only must NOT be forwarded")
	}
}

// TestForwardResponseHeadersStripsHopByHop verifies that hop-by-hop headers
// from the upstream response are not forwarded.
func TestForwardResponseHeadersStripsHopByHop(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", mimeJSON)
	src.Set("Connection", "keep-alive")
	src.Set("Transfer-Encoding", "chunked")

	dst := http.Header{}
	forwardResponseHeaders(src, dst)

	if dst.Get("Content-Type") != mimeJSON {
		t.Error("Content-Type must be forwarded")
	}
	if dst.Get("Connection") != "" {
		t.Error("Connection must NOT be forwarded")
	}
	if dst.Get("Transfer-Encoding") != "" {
		t.Error("Transfer-Encoding must NOT be forwarded")
	}
}

// TestForwardResponseHeadersStripsCORSHeaders verifies that upstream CORS headers
// are NOT forwarded to the client. The corsAndLogMiddleware already sets the correct
// Access-Control-Allow-Origin; forwarding an additional upstream value produces a
// multi-value header that browsers block (error: "multiple values not allowed").
func TestForwardResponseHeadersStripsCORSHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Content-Type", mimeJSON)
	src.Set("Access-Control-Allow-Origin", "*")
	src.Set("Access-Control-Allow-Methods", "GET, POST")
	src.Set("Access-Control-Allow-Headers", "Content-Type")
	src.Set("Access-Control-Allow-Credentials", "true")
	src.Set("Access-Control-Expose-Headers", "X-Custom")
	src.Set("Access-Control-Max-Age", "86400")

	dst := http.Header{}
	forwardResponseHeaders(src, dst)

	if dst.Get("Content-Type") != mimeJSON {
		t.Error("Content-Type must be forwarded")
	}
	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
		"Access-Control-Expose-Headers",
		"Access-Control-Max-Age",
	} {
		if dst.Get(h) != "" {
			t.Errorf("%s must NOT be forwarded from upstream (would duplicate middleware header)", h)
		}
	}
}

// ─── vsCodeVersionFromMarketClientID ─────────────────────────────────────────

// TestVsCodeVersionFromMarketClientID verifies version extraction across formats.
func TestVsCodeVersionFromMarketClientID(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		{"VSCode 1.112.0", "1.112.0"},
		{"VSCode 2.0.0-beta.1", "2.0.0-beta.1"},
		{"vsextension 1.0.0", ""},
		{"", ""},
		{"VSCode", ""},
		{"VSCode ", ""},
	}
	for _, c := range cases {
		got := vsCodeVersionFromMarketClientID(c.input)
		if got != c.want {
			t.Errorf("vsCodeVersionFromMarketClientID(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

// ─── handleGalleryVspackage header forwarding ─────────────────────────────────

// TestGalleryVspackageForwardsRequestHeaders confirms that handleGalleryVspackage
// forwards client headers (e.g. X-Market-Client-Id) to the upstream.
func TestGalleryVspackageForwardsRequestHeaders(t *testing.T) {
	const wantClientID = "VSCode 1.112.0"
	var gotClientID string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotClientID = r.Header.Get("X-Market-Client-Id")
		w.Header().Set("Content-Type", "application/vsix")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, testFakeVsix)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	path := ts.url + "/vscode/gallery/publishers/ms-python/vsextensions/python/2024.1.1/vspackage"
	req, _ := http.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("X-Market-Client-Id", wantClientID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET vspackage: %v", err)
	}
	defer resp.Body.Close()

	if gotClientID != wantClientID {
		t.Errorf("upstream X-Market-Client-Id = %q, want %q", gotClientID, wantClientID)
	}
}

// ─── handlePassthrough header forwarding ─────────────────────────────────────

// TestPassthroughForwardsRequestHeaders confirms that the generic passthrough
// handler forwards client headers to the upstream /api path.
// Uses /api/-/{name} which routes through handleExtension → handlePassthrough
// (namespace "-" triggers the pass-through short-circuit in handleExtension).
func TestPassthroughForwardsRequestHeaders(t *testing.T) {
	const wantUserAgent = "vscode/1.112.0"
	var gotUserAgent string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserAgent = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", mimeJSON)
		fmt.Fprint(w, `{}`)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	// /api/-/search has namespace="-" which short-circuits handleExtension
	// directly into handlePassthrough, exercising header forwarding.
	req, _ := http.NewRequest(http.MethodGet, ts.url+"/api/-/search", nil)
	req.Header.Set("User-Agent", wantUserAgent)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	if gotUserAgent != wantUserAgent {
		t.Errorf("upstream User-Agent = %q, want %q", gotUserAgent, wantUserAgent)
	}
}

// ─── middleware VS Code version logging ───────────────────────────────────────

// TestMiddlewareLogsVsCodeVersion verifies that the request log includes the
// vscode_version attribute when X-Market-Client-Id is present.
func TestMiddlewareLogsVsCodeVersion(t *testing.T) {
	var logOutput strings.Builder
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelDebug)
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: lvl}))

	mux := http.NewServeMux()
	mux.HandleFunc("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsAndLogMiddleware(mux, logger)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	req.Header.Set("X-Market-Client-Id", "VSCode 1.112.0")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if !strings.Contains(logOutput.String(), "1.112.0") {
		t.Errorf("log output should contain vscode_version; got: %s", logOutput.String())
	}
}

// TestMiddlewareOmitsVsCodeVersionWhenAbsent verifies that vscode_version is
// not logged when the X-Market-Client-Id header is not present.
func TestMiddlewareOmitsVsCodeVersionWhenAbsent(t *testing.T) {
	var logOutput strings.Builder
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelDebug)
	logger := slog.New(slog.NewTextHandler(&logOutput, &slog.HandlerOptions{Level: lvl}))

	mux := http.NewServeMux()
	mux.HandleFunc("/probe", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := corsAndLogMiddleware(mux, logger)

	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if strings.Contains(logOutput.String(), "vscode_version") {
		t.Errorf("log output must not contain vscode_version when header absent; got: %s", logOutput.String())
	}
}

// ─── TLS listener ────────────────────────────────────────────────────────────

func TestRunServerTLSListensHTTPS(t *testing.T) {
	// Generate a self-signed cert in a temp dir.
	dir := t.TempDir()
	certPath := filepath.Join(dir, "tls.crt")
	keyPath := filepath.Join(dir, "tls.key")
	if err := installer.GenerateSelfSignedCert(certPath, keyPath); err != nil {
		t.Fatalf("generating cert: %v", err)
	}

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{
			Port:                0, // will use net.Listen to get a free port
			ReadTimeoutSeconds:  5,
			WriteTimeoutSeconds: 5,
			IdleTimeoutSeconds:  5,
			TLSCertFile:         certPath,
			TLSKeyFile:          keyPath,
		},
		Upstream: config.UpstreamConfig{
			URL:            upstream.URL,
			TimeoutSeconds: 5,
			RegistryType:   config.RegistryOpenVSX,
		},
		Cache: config.CacheConfig{TTLSeconds: 60, MaxSizeMB: 1},
		Policy: config.PolicyConfig{
			FailMode: config.FailModeOpen,
		},
	}
	lvl := &slog.LevelVar{}
	lvl.Set(slog.LevelDebug)
	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: lvl}))

	srv, err := buildServer(cfg, logger, lvl)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start server in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- runServer(ctx, srv, logger, "test-tls")
	}()

	// Give the server a moment to start.
	time.Sleep(200 * time.Millisecond)
	cancel()

	if err := <-errCh; err != nil {
		t.Errorf("runServer returned error: %v", err)
	}
}

// TestIsMarketplacePreRelease verifies the helper that inspects the properties
// array for the Microsoft.VisualStudio.Code.PreRelease key.
func TestIsMarketplacePreRelease(t *testing.T) {
	type prop = struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	cases := []struct {
		name  string
		props []prop
		want  bool
	}{
		{"flagTrue", []prop{{Key: "Microsoft.VisualStudio.Code.PreRelease", Value: "true"}}, true},
		{"flagFalse", []prop{{Key: "Microsoft.VisualStudio.Code.PreRelease", Value: "false"}}, false},
		{"noFlag", []prop{{Key: "some.other.key", Value: "true"}}, false},
		{"empty", nil, false},
		{"caseInsensitiveValue", []prop{{Key: "Microsoft.VisualStudio.Code.PreRelease", Value: "True"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isMarketplacePreRelease(tc.props)
			if got != tc.want {
				t.Errorf("isMarketplacePreRelease(%v) = %v, want %v", tc.props, got, tc.want)
			}
		})
	}
}

// TestFilterGalleryVersionsBlocksMarketplacePreRelease verifies that gallery
// versions with the Microsoft.VisualStudio.Code.PreRelease property are
// stripped when the block-pre-release rule is active.
func TestFilterGalleryVersionsBlocksMarketplacePreRelease(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	raw := json.RawMessage(fmt.Sprintf(`{
		"publisher":{"publisherName":"gitkraken"},
		"extensionName":"gitlens",
		"versions":[
			{"version":"2026.3.1805","lastUpdated":%q,"properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]},
			{"version":"16.5.0","lastUpdated":%q,"properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"false"}]}
		]
	}`, old, old))

	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, BlockPreRelease: true},
	}})
	result, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), "")
	if blocked {
		t.Fatal("extension with a stable version should not be fully blocked")
	}
	var m map[string]json.RawMessage
	json.Unmarshal(result, &m) //nolint:errcheck
	var versions []map[string]json.RawMessage
	json.Unmarshal(m["versions"], &versions) //nolint:errcheck
	if len(versions) != 1 {
		t.Fatalf("want 1 surviving version (stable only), got %d", len(versions))
	}
	var ver string
	json.Unmarshal(versions[0]["version"], &ver) //nolint:errcheck
	if ver != "16.5.0" {
		t.Errorf("surviving version should be stable 16.5.0, got %s", ver)
	}
}

// TestFilterGalleryVersionsBlocksAllPreRelease verifies that an extension is
// fully blocked when all its versions carry the pre-release property.
func TestFilterGalleryVersionsBlocksAllPreRelease(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	raw := json.RawMessage(fmt.Sprintf(`{
		"publisher":{"publisherName":"gitkraken"},
		"extensionName":"gitlens",
		"versions":[
			{"version":"2026.3.1805","lastUpdated":%q,"properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]},
			{"version":"2026.3.1705","lastUpdated":%q,"properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]}
		]
	}`, old, old))

	engine := rules.New(config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, BlockPreRelease: true},
	}})
	_, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), "")
	if !blocked {
		t.Error("extension where all versions are pre-release must be fully blocked")
	}
}

// TestExtractMarketplaceVersionMetaPreRelease verifies that
// extractMarketplaceVersionMeta correctly flags a version as pre-release
// when the Microsoft.VisualStudio.Code.PreRelease property is present.
func TestExtractMarketplaceVersionMetaPreRelease(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"versions": []interface{}{
							map[string]interface{}{
								"version":     "2026.3.1805",
								"lastUpdated": testTimeOld2021,
								"properties": []interface{}{
									map[string]string{
										"key":   "Microsoft.VisualStudio.Code.PreRelease",
										"value": "true",
									},
								},
							},
						},
					},
				},
			},
		},
	})

	meta := extractMarketplaceVersionMeta(body, "2026.3.1805")
	if !meta.PreRelease {
		t.Error("extractMarketplaceVersionMeta must set PreRelease=true from properties")
	}
	if meta.PublishedAt.IsZero() {
		t.Error("extractMarketplaceVersionMeta must parse lastUpdated timestamp")
	}
}

// TestExtractMarketplaceVersionMetaLatestFallback verifies that passing
// empty version to extractMarketplaceVersionMeta returns the first entry.
func TestExtractMarketplaceVersionMetaLatestFallback(t *testing.T) {
	body, _ := json.Marshal(map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"versions": []interface{}{
							map[string]interface{}{
								"version":     "2026.3.1805",
								"lastUpdated": testTimeOld2021,
								"properties": []interface{}{
									map[string]string{
										"key":   "Microsoft.VisualStudio.Code.PreRelease",
										"value": "true",
									},
								},
							},
						},
					},
				},
			},
		},
	})

	meta := extractMarketplaceVersionMeta(body, "")
	if meta.Version != "2026.3.1805" {
		t.Errorf("want version 2026.3.1805, got %s", meta.Version)
	}
	if !meta.PreRelease {
		t.Error("must set PreRelease=true from properties even with empty version lookup")
	}
}

// TestGalleryQueryBlocksMarketplacePreRelease is an integration test verifying
// that the gallery query endpoint strips versions with the marketplace
// pre-release property when the block-pre-release rule is active.
func TestGalleryQueryBlocksMarketplacePreRelease(t *testing.T) {
	old := time.Now().UTC().AddDate(0, 0, -30).Format(time.RFC3339)
	galleryResp := map[string]interface{}{
		"results": []interface{}{
			map[string]interface{}{
				"extensions": []interface{}{
					map[string]interface{}{
						"publisher":     map[string]string{"publisherName": "gitkraken"},
						"extensionName": "gitlens",
						"versions": []interface{}{
							map[string]interface{}{
								"version":     "2026.3.1805",
								"lastUpdated": old,
								"properties": []interface{}{
									map[string]string{
										"key":   "Microsoft.VisualStudio.Code.PreRelease",
										"value": "true",
									},
								},
							},
							map[string]interface{}{
								"version":     "16.5.0",
								"lastUpdated": old,
								"properties": []interface{}{
									map[string]string{
										"key":   "Microsoft.VisualStudio.Code.PreRelease",
										"value": "false",
									},
								},
							},
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
		{Name: testRuleBlockPre, BlockPreRelease: true},
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
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var result map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&result) //nolint:errcheck
	results := result["results"].([]interface{})
	firstResult := results[0].(map[string]interface{})
	exts := firstResult["extensions"].([]interface{})
	if len(exts) != 1 {
		t.Fatalf("want 1 extension surviving, got %d", len(exts))
	}
	ext := exts[0].(map[string]interface{})
	versions := ext["versions"].([]interface{})
	if len(versions) != 1 {
		t.Fatalf("want 1 version (stable only), got %d", len(versions))
	}
	ver := versions[0].(map[string]interface{})
	if ver["version"] != "16.5.0" {
		t.Errorf("surviving version should be 16.5.0, got %v", ver["version"])
	}
}

func TestStripMarketplaceStatsFlag(t *testing.T) {
	t.Run("flagsWithStats", func(t *testing.T) {
		result := stripMarketplaceStatsFlag([]byte(`{"filters":[],"flags":914}`))
		assertJSONFlags(t, result, 402)
	})
	t.Run("flagsWithoutStats", func(t *testing.T) {
		input := `{"filters":[],"flags":402}`
		result := stripMarketplaceStatsFlag([]byte(input))
		if string(result) != input {
			t.Errorf("expected unchanged body, got %s", result)
		}
	})
	t.Run("noFlagsField", func(t *testing.T) {
		input := `{"filters":[]}`
		result := stripMarketplaceStatsFlag([]byte(input))
		if string(result) != input {
			t.Errorf("expected unchanged body, got %s", result)
		}
	})
	t.Run("invalidJSON", func(t *testing.T) {
		input := `not json`
		result := stripMarketplaceStatsFlag([]byte(input))
		if string(result) != input {
			t.Errorf("expected unchanged body, got %s", result)
		}
	})
	t.Run("onlyStatsFlag", func(t *testing.T) {
		result := stripMarketplaceStatsFlag([]byte(`{"flags":512}`))
		assertJSONFlags(t, result, 0)
	})
}

// assertJSONFlags parses result as {"flags": N, ...} and compares N to want.
func assertJSONFlags(t *testing.T, result []byte, want int) {
	t.Helper()
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	var flags int
	if err := json.Unmarshal(parsed["flags"], &flags); err != nil {
		t.Fatalf("cannot parse flags: %v", err)
	}
	if flags != want {
		t.Errorf("want flags %d, got %d", want, flags)
	}
}

// unmarshalOrFail is a test helper that unmarshals JSON or fails the test.
func unmarshalOrFail[T any](t *testing.T, data json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	return v
}

// TestRewriteVersionAssetURIs verifies that gallery version entries have their
// assetUri, fallbackAssetUri and files[].source rewritten to route through the proxy.
func TestRewriteVersionAssetURIs(t *testing.T) {
	proxyBase := "https://localhost:18003"

	t.Run("RewritesAllURIs", func(t *testing.T) {
		input := json.RawMessage(`{
			"version": "17.11.1",
			"assetUri": "https://eamodio.gallerycdn.vsassets.io/extensions/eamodio/gitlens/17.11.1/123",
			"fallbackAssetUri": "https://eamodio.gallery.vsassets.io/_apis/public/gallery/publisher/eamodio/extension/gitlens/17.11.1/assetbyname",
			"files": [
				{"assetType": "Microsoft.VisualStudio.Services.VSIXPackage", "source": "https://cdn.example.com/vsix"},
				{"assetType": "Microsoft.VisualStudio.Services.Icons.Default", "source": "https://cdn.example.com/icon"}
			]
		}`)
		result := rewriteVersionAssetURIs(input, "eamodio", "gitlens", proxyBase)
		m := unmarshalOrFail[map[string]json.RawMessage](t, result)
		wantBase := "https://localhost:18003/_apis/public/gallery/publisher/eamodio/extension/gitlens/17.11.1/assetbyname"
		assertRewrittenURI(t, m, "assetUri", wantBase)
		assertRewrittenURI(t, m, "fallbackAssetUri", wantBase)
		files := unmarshalOrFail[[]map[string]json.RawMessage](t, m["files"])
		if len(files) != 2 {
			t.Fatalf("want 2 files, got %d", len(files))
		}
		assertRewrittenURI(t, files[0], "source", wantBase+"/Microsoft.VisualStudio.Services.VSIXPackage")
	})

	t.Run("NoVersionNoop", func(t *testing.T) {
		input := json.RawMessage(`{"assetUri": "https://cdn.example.com/x"}`)
		result := rewriteVersionAssetURIs(input, "pub", "ext", proxyBase)
		if string(result) != string(input) {
			t.Errorf("expected no change for entry without version field")
		}
	})

	t.Run("InvalidJSONNoop", func(t *testing.T) {
		input := json.RawMessage(`not json`)
		result := rewriteVersionAssetURIs(input, "pub", "ext", proxyBase)
		if string(result) != string(input) {
			t.Errorf("expected no change for invalid JSON")
		}
	})

	t.Run("EmptyProxyBaseSkipped", func(t *testing.T) {
		input := json.RawMessage(`{
			"version": "1.0.0",
			"assetUri": "https://cdn.example.com/x"
		}`)
		result := rewriteVersionAssetURIs(input, "pub", "ext", "")
		m := unmarshalOrFail[map[string]json.RawMessage](t, result)
		assertRewrittenURI(t, m, "assetUri", "/_apis/public/gallery/publisher/pub/extension/ext/1.0.0/assetbyname")
	})
}

// assertRewrittenURI checks that a JSON string field in m matches want.
func assertRewrittenURI(t *testing.T, m map[string]json.RawMessage, key, want string) {
	t.Helper()
	got := unmarshalOrFail[string](t, m[key])
	if got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}

// TestFilterGalleryExtensionRewritesAssetURIs verifies that the filter pipeline
// rewrites asset URIs when a proxyBase is provided.
func TestFilterGalleryExtensionRewritesAssetURIs(t *testing.T) {
	cfg := config.Config{
		Policy: config.PolicyConfig{
			Rules: []config.PackageRule{
				{Name: "block-pre-release", Action: "deny", BlockPreRelease: true},
			},
		},
	}
	engine := rules.New(cfg.Policy)
	proxyBase := "https://proxy.local:9000"

	raw := json.RawMessage(`{
		"publisher": {"publisherName": "testpub"},
		"extensionName": "testext",
		"versions": [
			{
				"version": "2.0.0",
				"lastUpdated": "2020-01-01T00:00:00.000Z",
				"assetUri": "https://cdn.vsassets.io/extensions/testpub/testext/2.0.0/123",
				"fallbackAssetUri": "https://gallery.vsassets.io/old",
				"properties": [],
				"files": [{"assetType": "Microsoft.VisualStudio.Services.VSIXPackage", "source": "https://cdn/vsix"}]
			}
		]
	}`)

	result, blocked := filterGalleryExtension(raw, engine, testErrorLogger(), proxyBase)
	if blocked {
		t.Fatal("extension should not be fully blocked")
	}

	m := unmarshalOrFail[map[string]json.RawMessage](t, result)
	versions := unmarshalOrFail[[]map[string]json.RawMessage](t, m["versions"])
	if len(versions) != 1 {
		t.Fatalf("want 1 version, got %d", len(versions))
	}

	want := "https://proxy.local:9000/_apis/public/gallery/publisher/testpub/extension/testext/2.0.0/assetbyname"
	assertRewrittenURI(t, versions[0], "assetUri", want)

	files := unmarshalOrFail[[]map[string]json.RawMessage](t, versions[0]["files"])
	if len(files) != 1 {
		t.Fatalf("want 1 file, got %d", len(files))
	}
	assertRewrittenURI(t, files[0], "source", want+"/Microsoft.VisualStudio.Services.VSIXPackage")
}

// TestHandleGalleryAssetByNameBlocksPreRelease tests the asset-by-name handler
// blocks VSIX downloads for blocked versions and allows non-VSIX assets.
func TestHandleGalleryAssetByNameBlocksPreRelease(t *testing.T) {
	logger := testErrorLogger()

	t.Run("BlockedPackageVSIX", func(t *testing.T) {
		cfg := config.Config{
			Policy: config.PolicyConfig{
				Rules: []config.PackageRule{
					{Name: "block-known-malicious", Action: "deny",
						PackagePatterns: []string{"malicious.*"}},
				},
			},
		}
		srv := &Server{cfg: &cfg, engine: rules.New(cfg.Policy), logger: logger}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/malicious/extension/stealer/1.0.0/assetbyname/Microsoft.VisualStudio.Services.VSIXPackage", nil)
		req.SetPathValue("pub", "malicious")
		req.SetPathValue("ext", "stealer")
		req.SetPathValue("ver", "1.0.0")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.VSIXPackage")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("want 403, got %d", rec.Code)
		}
	})

	t.Run("NonVSIXAssetPassesThrough", func(t *testing.T) {
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer mock.Close()
		cfg := config.Config{
			Policy: config.PolicyConfig{
				Rules: []config.PackageRule{
					{Name: "block-known-malicious", Action: "deny",
						PackagePatterns: []string{"malicious.*"}},
				},
			},
			Upstream: config.UpstreamConfig{URL: mock.URL},
		}
		srv := &Server{
			cfg:      &cfg,
			engine:   rules.New(cfg.Policy),
			logger:   logger,
			upstream: mock.Client(),
		}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/malicious/extension/stealer/1.0.0/assetbyname/Microsoft.VisualStudio.Services.Icons.Default", nil)
		req.SetPathValue("pub", "malicious")
		req.SetPathValue("ext", "stealer")
		req.SetPathValue("ver", "1.0.0")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.Icons.Default")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Error("non-VSIX asset should not be blocked by policy")
		}
	})

	t.Run("AllowedVSIXPassesThrough", func(t *testing.T) {
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(hdrContentType, "application/vsix")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("fake-vsix")) //nolint:errcheck
		}))
		defer mock.Close()
		cfg := config.Config{
			Upstream: config.UpstreamConfig{URL: mock.URL, RegistryType: "marketplace"},
		}
		srv := &Server{
			cfg:      &cfg,
			engine:   rules.New(cfg.Policy),
			logger:   logger,
			upstream: mock.Client(),
		}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/safe/extension/goodext/1.0.0/assetbyname/Microsoft.VisualStudio.Services.VSIXPackage", nil)
		req.SetPathValue("pub", "safe")
		req.SetPathValue("ext", "goodext")
		req.SetPathValue("ver", "1.0.0")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.VSIXPackage")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("want 200 for allowed VSIX, got %d", rec.Code)
		}
	})

	t.Run("AgeFilterBlocksWhenMetadataUnavailable", func(t *testing.T) {
		cfg := config.Config{
			Policy: config.PolicyConfig{
				Rules: []config.PackageRule{
					{Name: "age-check", Action: "deny", MinPackageAgeDays: 7},
				},
			},
		}
		srv := &Server{
			cfg:      &cfg,
			engine:   rules.New(cfg.Policy),
			logger:   logger,
			upstream: &http.Client{},
		}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/newpub/extension/newext/0.0.1/assetbyname/Microsoft.VisualStudio.Services.VSIXPackage", nil)
		req.SetPathValue("pub", "newpub")
		req.SetPathValue("ext", "newext")
		req.SetPathValue("ver", "0.0.1")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.VSIXPackage")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("want 403 for unavailable age metadata, got %d", rec.Code)
		}
	})

	t.Run("VersionRuleBlocksPreRelease", func(t *testing.T) {
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set(hdrContentType, mimeJSON)
			resp := `{"results":[{"extensions":[{"versions":[{
				"version":"2.0.0-beta",
				"lastUpdated":"2020-01-01T00:00:00.000Z",
				"properties":[{"key":"Microsoft.VisualStudio.Code.PreRelease","value":"true"}]
			}]}]}]}`
			w.Write([]byte(resp)) //nolint:errcheck
		}))
		defer mock.Close()
		cfg := config.Config{
			Policy: config.PolicyConfig{
				Rules: []config.PackageRule{
					{Name: "block-pre", Action: "deny", BlockPreRelease: true},
				},
			},
			Upstream: config.UpstreamConfig{URL: mock.URL, RegistryType: "marketplace"},
		}
		srv := &Server{
			cfg:      &cfg,
			engine:   rules.New(cfg.Policy),
			logger:   logger,
			upstream: mock.Client(),
		}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/testpub/extension/testext/2.0.0-beta/assetbyname/Microsoft.VisualStudio.Services.VSIXPackage", nil)
		req.SetPathValue("pub", "testpub")
		req.SetPathValue("ext", "testext")
		req.SetPathValue("ver", "2.0.0-beta")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.VSIXPackage")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("want 403 for pre-release VSIX, got %d", rec.Code)
		}
	})

	t.Run("UpstreamError", func(t *testing.T) {
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic(http.ErrAbortHandler)
		}))
		defer mock.Close()
		cfg := config.Config{
			Upstream: config.UpstreamConfig{URL: mock.URL},
		}
		srv := &Server{
			cfg:      &cfg,
			engine:   rules.New(cfg.Policy),
			logger:   logger,
			upstream: mock.Client(),
		}
		req := httptest.NewRequest(http.MethodGet,
			"/_apis/public/gallery/publisher/pub/extension/ext/1.0.0/assetbyname/Microsoft.VisualStudio.Services.Icons.Default", nil)
		req.SetPathValue("pub", "pub")
		req.SetPathValue("ext", "ext")
		req.SetPathValue("ver", "1.0.0")
		req.SetPathValue("assetType", "Microsoft.VisualStudio.Services.Icons.Default")
		rec := httptest.NewRecorder()
		srv.handleGalleryAssetByName(rec, req)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("want 502, got %d", rec.Code)
		}
	})
}
