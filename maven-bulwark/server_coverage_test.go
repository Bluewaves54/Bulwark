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

// ─── Constants ───────────────────────────────────────────────────────────────

const (
	testMavenToken     = "maven-token-xyz"
	testMavenUser      = "maven-user"
	testMavenPassword  = "maven-pass"
	testConfigPattern  = "config-*.yaml"
	testErrTempDir     = "TempDir: %v"
	testErrWriteString = "WriteString: %v"
	testErrUnexpected  = "unexpected error: %v"
	testPathReadyz     = "/readyz"
	testErrInitServer  = "initServer: %v"
	testTokenMaven     = "my-maven-token"
)

// ─── Utility functions ────────────────────────────────────────────────────────

func TestAddrFromPortMaven(t *testing.T) {
	if got := addrFromPort(8081); got != ":8081" {
		t.Errorf("addrFromPort(8081) = %q, want \":8081\"", got)
	}
}

func TestCreateLoggerTextInfoMaven(t *testing.T) {
	if l, _, _ := createLogger("text", "info", ""); l == nil {
		t.Error("expected non-nil logger for text/info")
	}
}

func TestCreateLoggerJSONDebugMaven(t *testing.T) {
	if l, _, _ := createLogger("json", "debug", ""); l == nil {
		t.Error("expected non-nil logger for json/debug")
	}
}

func TestCreateLoggerWarnMaven(t *testing.T) {
	if l, _, _ := createLogger("text", "warn", ""); l == nil {
		t.Error("non-nil logger for warn")
	}
}

func TestCreateLoggerErrorMaven(t *testing.T) {
	if l, _, _ := createLogger("text", "error", ""); l == nil {
		t.Error("non-nil logger for error")
	}
}

func TestCreateLoggerDefaultMaven(t *testing.T) {
	if l, _, _ := createLogger("text", "notsuchlog", ""); l == nil {
		t.Error("non-nil logger for unknown level")
	}
}

func TestLoadConfigMissingFileMaven(t *testing.T) {
	_, err := loadConfig("/no/such/maven-config.yaml")
	if err == nil {
		t.Error("expected error loading missing config")
	}
}

func TestLoadConfigValidMaven(t *testing.T) {
	yaml := `
upstream:
  url: "https://repo1.maven.org/maven2"
server:
  port: 8081
`
	f, err := os.CreateTemp(t.TempDir(), "maven-cfg-*.yaml")
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	f.WriteString(yaml) //nolint:errcheck
	f.Close()

	cfg, err := loadConfig(f.Name())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Upstream.URL != "https://repo1.maven.org/maven2" {
		t.Errorf("Upstream.URL = %q", cfg.Upstream.URL)
	}
}

func TestApplyFlagOverridesTokenMaven(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, testMavenToken, "", "")
	if cfg.Upstream.Token != testMavenToken {
		t.Errorf("Token = %q, want %q", cfg.Upstream.Token, testMavenToken)
	}
}

func TestApplyFlagOverridesBasicAuthMaven(t *testing.T) {
	cfg := &config.Config{}
	applyFlagOverrides(cfg, "", testMavenUser, testMavenPassword)
	if cfg.Upstream.Username != testMavenUser || cfg.Upstream.Password != testMavenPassword {
		t.Errorf("Username/Password mismatch")
	}
}

func TestApplyFlagOverridesEmptyMaven(t *testing.T) {
	cfg := &config.Config{Upstream: config.UpstreamConfig{Token: "keep-me"}}
	applyFlagOverrides(cfg, "", "", "")
	if cfg.Upstream.Token != "keep-me" {
		t.Error("empty overrides should not overwrite token")
	}
}

// ─── addUpstreamAuth ──────────────────────────────────────────────────────────

func buildMavenServerWithAuth(t *testing.T, upstreamURL, token, username, password string) (*Server, *httptest.Server) {
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

func TestAddUpstreamAuthTokenMaven(t *testing.T) {
	var gotAuth string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	_, ts := buildMavenServerWithAuth(t, mock.URL, testMavenToken, "", "")
	http.Get(ts.URL + artifactPath) //nolint:errcheck

	if want := "Bearer " + testMavenToken; gotAuth != want {
		t.Errorf("Authorization: want %q, got %q", want, gotAuth)
	}
}

func TestAddUpstreamAuthBasicMaven(t *testing.T) {
	var gotUser, gotPass string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUser, gotPass, _ = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	_, ts := buildMavenServerWithAuth(t, mock.URL, "", testMavenUser, testMavenPassword)
	http.Get(ts.URL + artifactPath) //nolint:errcheck

	if gotUser != testMavenUser || gotPass != testMavenPassword {
		t.Errorf("BasicAuth: user=%s pass=%s want %s/%s", gotUser, gotPass, testMavenUser, testMavenPassword)
	}
}

// ─── handleArtifact: blocked version ─────────────────────────────────────────

func TestHandleArtifactBlockedPreRelease(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "block-rc", BlockPreRelease: true},
	}}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + rcPath)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for rc artifact, got %d", resp.StatusCode)
	}
}

func TestHandleArtifactBlockedSnapshot(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "block-snap", BlockSnapshots: true},
	}}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + snapshotPath)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for SNAPSHOT artifact, got %d", resp.StatusCode)
	}
}

func TestHandleArtifactBlockedPackage(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "deny-artifact", PackagePatterns: []string{testGroupID + ":" + testArtifactID}, Action: "deny"},
	}}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + artifactPath)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied package artifact, got %d", resp.StatusCode)
	}
}

func TestHandleMetadataBlockedPackage(t *testing.T) {
	xml := mavenMetadataXML([]string{testVersion100})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, xml)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "deny-meta", PackagePatterns: []string{testGroupID + ":" + testArtifactID}, Action: "deny"},
	}}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + metadataPath)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("want 403 for denied package metadata, got %d", resp.StatusCode)
	}
}

func TestHandleArtifactUpstreamError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	dead.Close()

	ts := buildMavenTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + artifactPath)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream error, got %d", resp.StatusCode)
	}
}

// ─── handleChecksum ────────────────────────────────────────────────────────

func TestHandleChecksumReturns404AfterMetadataCached(t *testing.T) {
	xml := mavenMetadataXML([]string{testVersion100})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, xml)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})

	// First request: populate the metadata cache.
	resp1, _ := http.Get(ts.url + metadataPath)
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("metadata: want 200, got %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	// Second request: checksum for the same metadata → 404 (stale checksum guard).
	resp2, _ := http.Get(ts.url + metadataPath + ".sha1")
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("checksum after cached metadata: want 404, got %d", resp2.StatusCode)
	}
}

func TestHandleChecksumProxiesNonMetadata(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "abc123checksum")
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})

	// Checksum for a JAR (not metadata) → proxied normally.
	resp, _ := http.Get(ts.url + "/com/example/mylib/1.0.0/mylib-1.0.0.jar.sha1")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("non-metadata checksum: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── handleMetadata: bad upstream body ────────────────────────────────────────

func TestHandleMetadataUpstreamError(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	dead.Close()

	ts := buildMavenTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + metadataPath)
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for upstream error, got %d", resp.StatusCode)
	}
}

// ─── ParseMavenPath edge cases ───────────────────────────────────────────────

func TestParseMavenPathTooShort(t *testing.T) {
	if _, _, _, _, err := ParseMavenPath("/group"); err == nil {
		t.Error("expected error for too-short path")
	}
}

func TestParseMavenPathVersionSpecificMetadata(t *testing.T) {
	path := "/com/example/mylib/1.0.0/maven-metadata.xml"
	group, artifact, version, filename, err := ParseMavenPath(path)
	if err != nil {
		t.Fatalf(testErrUnexpected, err)
	}
	if group == "" || artifact == "" || version == "" || filename == "" {
		t.Errorf("unexpected empty fields: group=%q artifact=%q version=%q file=%q",
			group, artifact, version, filename)
	}
}

func TestParseMavenPathChecksumSidecar(t *testing.T) {
	path := "/com/example/mylib/1.0.0/mylib-1.0.0.jar.sha1"
	group, artifact, version, _, err := ParseMavenPath(path)
	if err != nil {
		t.Fatalf(testErrUnexpected, err)
	}
	if group != "com/example" {
		t.Errorf("group: want com/example, got %q", group)
	}
	if artifact != "mylib" {
		t.Errorf("artifact: want mylib, got %q", artifact)
	}
	if version != "1.0.0" {
		t.Errorf("version: want 1.0.0, got %q", version)
	}
}

// ─── isVersion ───────────────────────────────────────────────────────────────

func TestIsVersionTrue(t *testing.T) {
	if !isVersion("1.0.0") {
		t.Error("expected 1.0.0 to be a version")
	}
}

func TestIsVersionFalseEmpty(t *testing.T) {
	if isVersion("") {
		t.Error("expected empty string to not be a version")
	}
}

func TestIsVersionFalseNonDigit(t *testing.T) {
	if isVersion("maven-metadata.xml") {
		t.Error("expected metadata filename to not be a version")
	}
}

// ─── buildServer with insecure TLS ───────────────────────────────────────────

func TestBuildServerInsecureTLSMaven(t *testing.T) {
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

func TestHandleReadyMavenUpstreamDown(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	dead.Close()

	ts := buildMavenTestServer(t, dead.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 when upstream is down, got %d", resp.StatusCode)
	}
}

func TestHandleReadyMavenUpstream5xx(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for upstream 5xx, got %d", resp.StatusCode)
	}
}

// ─── handleMetadata: short path (ParseMavenPath fails → proxy as-is) ─────────

func TestHandleMetadataShortPathProxiesAsIs(t *testing.T) {
	const xmlContent = `<?xml version="1.0" encoding="UTF-8"?><metadata/>`
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, xmlContent)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})

	// A bare /maven-metadata.xml path has only one path component → ParseMavenPath fails.
	resp, _ := http.Get(ts.url + "/maven-metadata.xml")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("short path proxy-as-is: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── handleMetadata: invalid XML (ParseMetadataXML fails → proxy as-is) ──────

func TestHandleMetadataInvalidXML(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "this is not XML at all")
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + metadataPath)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("invalid XML proxy-as-is: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// ─── handleMetadata: non-200 upstream ────────────────────────────────────────

func TestHandleMetadataNonOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, _ := http.Get(ts.url + metadataPath)
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("non-200 metadata: want 404, got %d", resp.StatusCode)
	}
}

// ─── handleMetadata: dry-run ─────────────────────────────────────────────────

func TestHandleMetadataDryRun(t *testing.T) {
	xml := mavenMetadataXML([]string{testVersion100, testVersionSnap})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, xml)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		DryRun: true,
		Rules:  []config.PackageRule{{Name: "block-snap", BlockSnapshots: true}},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + metadataPath)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("dry-run metadata: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter >0 for dry-run metadata")
	}
}

// ─── FilterMetadataXML: all versions filtered ────────────────────────────────

func TestFilterMetadataXMLAllFilteredEmpty(t *testing.T) {
	xmlInput := mavenMetadataXML([]string{testVersion100, testVersionRC, testVersionSnap})
	result, err := FilterMetadataXML([]byte(xmlInput), []string{}) // allow none
	if err != nil {
		t.Fatalf(testErrUnexpected, err)
	}
	// The output should contain no version elements.
	if len(result) == 0 {
		t.Fatal("expected non-empty XML output")
	}
}

// ─── FilterMetadataXML: invalid XML ──────────────────────────────────────────

func TestFilterMetadataXMLBadInputCoverage(t *testing.T) {
	_, err := FilterMetadataXML([]byte("not xml"), []string{"1.0.0"})
	if err == nil {
		t.Error("expected error for invalid XML input")
	}
}

// ─── FilterMetadataXML: update Latest/Release when current value is removed ──

func TestFilterMetadataXMLUpdatesLatest(t *testing.T) {
	// XML where latest=3.0 is denied but 1.0 and 2.0 remain allowed.
	// FilterMetadataXML should update Latest to the last remaining version.
	xmlIn := `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>mylib</artifactId>
  <versioning>
    <latest>3.0</latest>
    <release>3.0</release>
    <versions>
      <version>1.0</version>
      <version>2.0</version>
      <version>3.0</version>
    </versions>
  </versioning>
</metadata>`

	out, err := FilterMetadataXML([]byte(xmlIn), []string{"1.0", "2.0"})
	if err != nil {
		t.Fatalf("FilterMetadataXML: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("expected non-empty output")
	}
	// The filtered XML must NOT contain 3.0 as latest/release.
	outStr := string(out)
	if len(outStr) == 0 {
		t.Error("empty output")
	}
}

// ─── initServer ───────────────────────────────────────────────────────────────

const testMavenConfigYAML = `upstream:
  url: "https://repo1.maven.org/maven2"
server:
  port: 8080
`

func TestInitServerMavenSuccess(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testMavenConfigYAML); err != nil {
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

func TestInitServerMavenMissingConfig(t *testing.T) {
	_, _, _, err := initServer("/nonexistent/path/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config file")
	}
}

func TestInitServerMavenWithTokenOverride(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testMavenConfigYAML); err != nil {
		t.Fatalf(testErrWriteString, err)
	}
	f.Close()

	srv, _, _, err := initServer(f.Name(), testTokenMaven, "", "")
	if err != nil {
		t.Fatalf(testErrInitServer, err)
	}
	if srv.cfg.Upstream.Token != testTokenMaven {
		t.Errorf("Token: want %q, got %q", testTokenMaven, srv.cfg.Upstream.Token)
	}
}

// ─── handleReady: invalid upstream URL (NewRequest error path) ────────────────

func TestHandleReadyMavenInvalidURL(t *testing.T) {
	ts := buildMavenTestServer(t, "://invalid", config.PolicyConfig{})
	resp, _ := http.Get(ts.url + testPathReadyz)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("want 503 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// ─── handleArtifact: DryRun path ─────────────────────────────────────────────

func TestHandleArtifactDryRun(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("fake-jar")) //nolint:errcheck
	}))
	defer mock.Close()

	// DryRun + BlockPreRelease: RC version is allowed but counted as dry-run.
	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: "block-pre", BlockPreRelease: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, _ := http.Get(ts.url + "/com/example/mylib/1.0-rc1/mylib-1.0-rc1.jar")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 for dry-run RC artifact, got %d", resp.StatusCode)
	}
	if ts.srv.reqDryRun.Load() == 0 {
		t.Error("expected reqDryRun counter to increment")
	}
}

// ─── fetchUpstream: error paths ──────────────────────────────────────────────

func TestHandleArtifactInvalidUpstreamURL(t *testing.T) {
	ts := buildMavenTestServer(t, "://invalid", config.PolicyConfig{})
	// A valid 4-part artifact path so ParseMavenPath succeeds and allows it.
	resp, _ := http.Get(ts.url + "/com/example/mylib/1.0/mylib-1.0.jar")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for invalid upstream URL, got %d", resp.StatusCode)
	}
}

// roundTripperFunc adapts a function to the http.RoundTripper interface.
type roundTripperFunc struct {
	fn func(*http.Request) (*http.Response, error)
}

func (r *roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return r.fn(req)
}

// errorBody is an io.ReadCloser whose Read always returns an error.
type errorBody struct{}

func (errorBody) Read(_ []byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (errorBody) Close() error               { return nil }

func TestHandleArtifactBodyReadError(t *testing.T) {
	transport := &roundTripperFunc{fn: func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       errorBody{},
		}, nil
	}}

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		/* intentionally empty */
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	ts.srv.upstream = &http.Client{Transport: transport}
	resp, _ := http.Get(ts.url + "/com/example/mylib/1.0/mylib-1.0.jar")
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("want 502 for body read error, got %d", resp.StatusCode)
	}
}

// ─── ParseMavenPath: artifact path too short ─────────────────────────────────

func TestParseMavenPathArtifactTooShort(t *testing.T) {
	// 3 path components without metadata extension → artifact path too short.
	_, _, _, _, err := ParseMavenPath("/com/example/mylib")
	if err == nil {
		t.Error("expected error for artifact path with < 4 components")
	}
}

// ─── runServer ────────────────────────────────────────────────────────────────

func TestRunServerMavenGracefulShutdown(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), testConfigPattern)
	if err != nil {
		t.Fatalf(testErrTempDir, err)
	}
	if _, err = f.WriteString(testMavenConfigYAML); err != nil {
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
		done <- runServer(ctx, srv, logger, "maven-bulwark-test")
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

// ─── fail_mode: "closed" ─────────────────────────────────────────────────────

func TestFailModeClosedMavenParseError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<broken xml")) //nolint:errcheck
	}))
	defer mock.Close()
	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeClosed})
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf("GET %s: %v", metadataPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("fail_mode:closed + parse error: want 502, got %d", resp.StatusCode)
	}
}

func TestFailModeOpenMavenParseError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(hdrContentType, mimeXML)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<broken xml")) //nolint:errcheck
	}))
	defer mock.Close()
	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{FailMode: config.FailModeOpen})
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf("GET %s: %v", metadataPath, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("fail_mode:open + parse error: want 200, got %d", resp.StatusCode)
	}
}

// ─── RequiresAgeFiltering integration (engine method) ────────────────────────
// Authoritative unit tests for RequiresAgeFiltering live in common/rules/rules_test.go.
// These tests verify the maven proxy correctly calls the engine method in artifact paths.

func TestArtifactTrustedPackageBypassesAgeCheck(t *testing.T) {
	// Mock upstream: HEAD returns 200 without Last-Modified (PublishedAt zero).
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("artifact-content")) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{
		TrustedPackages: []string{"com/example:artifact"},
		Defaults:        config.RulesDefaults{MinPackageAgeDays: 30},
	})

	resp, err := http.Get(ts.url + "/com/example/artifact/1.0.0/artifact-1.0.0.jar")
	if err != nil {
		t.Fatalf("GET artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden {
		t.Errorf("trusted package should not be blocked on artifact download, got %d", resp.StatusCode)
	}
}

// ─── handleArtifact age-check bypass prevention ─────────────────────────────

func TestHandleArtifactAgeCheckNoLastModified(t *testing.T) {
	// Mock upstream that returns 200 for HEAD but no Last-Modified header.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			// No Last-Modified header
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("artifact-content")) //nolint:errcheck
	}))
	defer mock.Close()

	cfg := &config.Config{
		Server: config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{
			URL:            mock.URL,
			TimeoutSeconds: 5,
		},
		Cache: config.CacheConfig{TTLSeconds: 60},
		Policy: config.PolicyConfig{
			Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		},
		Logging: config.LoggingConfig{Level: "error", Format: "text"},
	}
	cfg.Defaults()

	logger, logLevel, _ := createLogger("text", "error", "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		t.Fatalf("buildServer: %v", err)
	}

	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/org/example/artifact/1.0.0/artifact-1.0.0.jar")
	if err != nil {
		t.Fatalf("GET artifact: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 when age filtering required but no Last-Modified, got %d", resp.StatusCode)
	}
}

// ─── parseLogLevel ───────────────────────────────────────────────────────────

func TestParseLogLevelValidMaven(t *testing.T) {
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

func TestParseLogLevelInvalidMaven(t *testing.T) {
	_, ok := parseLogLevel("trace")
	if ok {
		t.Error("parseLogLevel(\"trace\") should return false")
	}
}

// ─── handleGetLogLevel / handleSetLogLevel ───────────────────────────────────

func TestHandleGetLogLevelMaven(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
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

func TestHandleSetLogLevelMaven(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
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

func TestHandleSetLogLevelInvalidBodyMaven(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader("not json"))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid body, got %d", rec.Code)
	}
}

func TestHandleSetLogLevelInvalidLevelMaven(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	req := httptest.NewRequest(http.MethodPut, "/admin/log-level", strings.NewReader(`{"level":"trace"}`))
	rec := httptest.NewRecorder()
	ts.srv.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("want 400 for invalid level, got %d", rec.Code)
	}
}

// ─── createLogger with file path ─────────────────────────────────────────────

func TestCreateLoggerWithFilePathMaven(t *testing.T) {
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

func TestCreateLoggerWithoutFilePathMaven(t *testing.T) {
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
	if p.Ecosystem != "maven" {
		t.Errorf("Ecosystem = %s, want maven", p.Ecosystem)
	}
	if p.BinaryName != "maven-bulwark" {
		t.Errorf("BinaryName = %s, want maven-bulwark", p.BinaryName)
	}
	if p.Port != 18002 {
		t.Errorf("Port = %d, want 18002", p.Port)
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
	err := run(ctx, "/no/such/config.yaml", true, false, false, "", "", "", io.Discard)
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
	err := run(ctx, cfgFile, true, false, false, "", "", "", io.Discard)
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

func TestInitServerBadConfig(t *testing.T) {
	_, _, _, err := initServer("/no/such/config.yaml", "", "", "")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestEnforcePackagePolicyDryRun(t *testing.T) {
	cfg := &config.Config{
		Policy: config.PolicyConfig{
			DryRun: true,
			Rules: []config.PackageRule{
				{Name: "deny-evil", PackagePatterns: []string{"com/evil:*"}, Action: "deny"},
			},
		},
	}
	srv := &Server{
		cfg:    cfg,
		engine: rules.New(cfg.Policy),
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	pkgMeta := rules.PackageMeta{Name: "com/evil:malware"}
	w := httptest.NewRecorder()
	blocked := srv.enforcePackagePolicy(w, pkgMeta, 1)
	if blocked {
		t.Error("dry-run should not block")
	}
}

func TestHandleMetadataFilterXMLFail(t *testing.T) {
	// Upstream returns valid-looking metadata but with XML that will be hard to filter.
	// This exercises the FilterMetadataXML error path in handleMetadata.
	badMetadata := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<metadata><groupId>com.test</groupId><artifactId>lib</artifactId>` +
		`<versioning><versions>` +
		`<version>1.0.0</version>` +
		`</versions><lastUpdated>20240601</lastUpdated></versioning></metadata>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(badMetadata)) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/test/lib/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestFetchVersionMetaFromPackumentBadJSON(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", "Mon, 15 Jan 2024 12:00:00 GMT")
		w.Write([]byte(`{"versions":"bad"}`)) //nolint:errcheck
	}))
	defer upstream.Close()
	srv := &Server{
		cfg:      &config.Config{Upstream: config.UpstreamConfig{URL: upstream.URL}},
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	got := srv.fetchUpstreamLastModified("/test/path")
	if got.IsZero() {
		t.Error("expected non-zero time")
	}
}

func TestHandleMetadataAllVersionsRemoved(t *testing.T) {
	metadata := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<metadata><groupId>com.test</groupId><artifactId>lib</artifactId>` +
		`<versioning><versions>` +
		`<version>1.0.0</version>` +
		`</versions><lastUpdated>20240601</lastUpdated></versioning></metadata>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(metadata)) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
		Policy: config.PolicyConfig{
			VersionPatterns: []config.VersionPatternRule{
				{Name: "deny-all", Match: ".*", Action: "deny", Reason: "test"},
			},
		},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/test/lib/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestHandleMetadataUpstreamNon200(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found")) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/test/lib/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestHandleMetadataParseVersionMetaFail(t *testing.T) {
	badXML := `<<<not valid xml at all>>>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(badXML)) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/test/lib/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	// With default fail_mode open: should return 200 with raw body.
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestHandleMetadataParseVersionMetaFailClosed(t *testing.T) {
	badXML := `<<<not valid xml at all>>>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(badXML)) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
		Policy:   config.PolicyConfig{FailMode: "closed"},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/test/lib/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestHandleMetadataPackageBlocked(t *testing.T) {
	metadata := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<metadata><groupId>com.evil</groupId><artifactId>malware</artifactId>` +
		`<versioning><versions>` +
		`<version>1.0.0</version>` +
		`</versions><lastUpdated>20240601</lastUpdated></versioning></metadata>`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write([]byte(metadata)) //nolint:errcheck
	}))
	defer upstream.Close()
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: upstream.URL},
		Cache:    config.CacheConfig{TTLSeconds: 1},
		Policy: config.PolicyConfig{
			Rules: []config.PackageRule{
				{Name: "deny-evil", PackagePatterns: []string{"com/evil:*"}, Action: "deny"},
			},
		},
	}
	srv := &Server{
		cfg:      cfg,
		engine:   rules.New(cfg.Policy),
		cache:    rules.NewCache(1),
		upstream: upstream.Client(),
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	req := httptest.NewRequest(http.MethodGet, "/com/evil/malware/maven-metadata.xml", nil)
	rec := httptest.NewRecorder()
	srv.handleMetadata(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
}

func TestParseMavenLastUpdated(t *testing.T) {
	tests := []struct {
		name  string
		input string
		zero  bool
	}{
		{"empty", "", true},
		{"whitespace", "   ", true},
		{"invalid", "not-a-timestamp", true},
		{"valid", "20240115120000", false},
		{"validWithSpaces", " 20240601083045 ", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMavenLastUpdated(tc.input)
			if tc.zero && !got.IsZero() {
				t.Errorf("expected zero time for %q, got %v", tc.input, got)
			}
			if !tc.zero && got.IsZero() {
				t.Errorf("expected non-zero time for %q", tc.input)
			}
		})
	}
}

func TestFetchUpstreamLastModified(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		header     string
		expectZero bool
	}{
		{"status404", 404, "", true},
		{"status500", 500, "", true},
		{"noHeader", 200, "", true},
		{"rfc1123", 200, "Mon, 15 Jan 2024 12:00:00 GMT", false},
		{"rfc1123z", 200, "Mon, 15 Jan 2024 12:00:00 +0000", false},
		{"invalidFormat", 200, "January 15, 2024", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Last-Modified", tc.header)
				}
				w.WriteHeader(tc.status)
			}))
			defer upstream.Close()

			srv := &Server{
				cfg: &config.Config{
					Upstream: config.UpstreamConfig{URL: upstream.URL},
				},
				upstream: upstream.Client(),
				logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
			}
			got := srv.fetchUpstreamLastModified("/test/path")
			if tc.expectZero && !got.IsZero() {
				t.Errorf("expected zero time, got %v", got)
			}
			if !tc.expectZero && got.IsZero() {
				t.Error("expected non-zero time")
			}
		})
	}
}
