// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Bulwark/common/config"
)

// Maven coordinate test constants.
const (
	testGroupID      = "com/example"
	testArtifactID   = "mylib"
	testVersion100   = "1.0.0"
	testVersionRC    = "1.0.0-rc1"
	testVersionSnap  = "1.0.0-SNAPSHOT"
	testVersion2Snap = "2.0.0-SNAPSHOT"

	metadataPath = "/com/example/mylib/maven-metadata.xml"
	artifactPath = "/com/example/mylib/1.0.0/mylib-1.0.0.jar"
	snapshotPath = "/com/example/mylib/1.0.0-SNAPSHOT/mylib-1.0.0-SNAPSHOT.jar"
	rcPath       = "/com/example/mylib/1.0.0-rc1/mylib-1.0.0-rc1.jar"

	xmlHeader      = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
	testContentXML = mimeXML

	testRuleBlockSnapshots = "block-snapshots"
	testRuleAge7d          = "age-7d"
	testErrGETMetadata     = "GET metadata: %v"
	testHdrPolicyNotice    = "X-Curation-Policy-Notice"
	testErrGETArtifact     = "GET artifact: %v"
)

// mavenMetadataXML returns a sample maven-metadata.xml with specified versions.
func mavenMetadataXML(versions []string) string {
	versionElems := ""
	for _, v := range versions {
		versionElems += fmt.Sprintf("    <version>%s</version>\n", v)
	}
	latest := ""
	release := ""
	if len(versions) > 0 {
		latest = versions[len(versions)-1]
		// release is the last non-SNAPSHOT, non-pre-release version
		for i := len(versions) - 1; i >= 0; i-- {
			if !strings.Contains(versions[i], "SNAPSHOT") && !strings.Contains(versions[i], "rc") {
				release = versions[i]
				break
			}
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>mylib</artifactId>
  <versioning>
    <latest>%s</latest>
    <release>%s</release>
    <versions>
%s    </versions>
  </versioning>
</metadata>
`, latest, release, versionElems)
}

func buildMavenTestServer(t *testing.T, upstreamURL string, policy config.PolicyConfig) *mavenTestResult {
	t.Helper()
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: upstreamURL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 60},
		Metrics:  config.MetricsConfig{Enabled: true},
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
	return &mavenTestResult{srv: srv, url: ts.URL}
}

type mavenTestResult struct {
	srv *Server
	url string
}

func TestMavenHealthEndpoint(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: want 200, got %d", resp.StatusCode)
	}
}

func TestMavenReadyzOK(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("readyz: want 200, got %d", resp.StatusCode)
	}
}

func TestMavenReadyzDown(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("readyz down: want 503, got %d", resp.StatusCode)
	}
}

func TestMavenMetricsEndpoint(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metrics: want 200, got %d", resp.StatusCode)
	}
}

func TestMavenMetadataNoFilter(t *testing.T) {
	// Upstream returns metadata with only stable versions; none should be filtered.
	xmlBody := mavenMetadataXML([]string{"1.0.0", "1.1.0", "1.2.0"})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "maven-metadata.xml") {
			w.Header().Set(hdrContentType, testContentXML)
			fmt.Fprint(w, xmlBody)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockSnapshots, Action: "deny", BlockSnapshots: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metadata no filter: want 200, got %d", resp.StatusCode)
	}
	// No filtering happened — header must be absent.
	if resp.Header.Get(testHdrPolicyNotice) != "" {
		t.Errorf("unexpected X-Curation-Policy-Notice: %q", resp.Header.Get(testHdrPolicyNotice))
	}
}

func TestMavenMetadataSnapshotFiltered(t *testing.T) {
	xmlBody := mavenMetadataXML([]string{"1.0.0", "1.1.0-SNAPSHOT"})
	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockSnapshots, Action: "deny", BlockSnapshots: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("metadata snapshot filter: want 200, got %d", resp.StatusCode)
	}
	notice := resp.Header.Get(testHdrPolicyNotice)
	if notice == "" {
		t.Error("expected X-Curation-Policy-Notice to be set")
	}
	if !strings.Contains(notice, "1") {
		t.Errorf("expected notice to mention 1 filtered version, got %q", notice)
	}
}

func TestMavenMetadataCacheHit(t *testing.T) {
	xmlBody := mavenMetadataXML([]string{"1.0.0"})
	calls := 0
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})

	// First request — upstream called.
	resp1, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf("first GET: %v", err)
	}
	resp1.Body.Close()

	// Second request — must be served from cache.
	resp2, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	resp2.Body.Close()

	if calls != 1 {
		t.Errorf("cache hit: upstream called %d times, want 1", calls)
	}
	if resp2.Header.Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT on second request, got %q", resp2.Header.Get("X-Cache"))
	}
}

func TestMavenChecksumReturn404ForFilteredMeta(t *testing.T) {
	// Metadata with a SNAPSHOT version — will be filtered, cached.
	xmlBody := mavenMetadataXML([]string{"1.0.0", "1.1.0-SNAPSHOT"})
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".xml") {
			w.Header().Set(hdrContentType, testContentXML)
			fmt.Fprint(w, xmlBody)
			return
		}
		// Should not be reached if checksum returns 404.
		fmt.Fprint(w, "abc123")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockSnapshots, Action: "deny", BlockSnapshots: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	// Warm the metadata cache.
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf("metadata GET: %v", err)
	}
	resp.Body.Close()

	// Now request the checksum — must return 404.
	csResp, err := http.Get(ts.url + metadataPath + ".sha1")
	if err != nil {
		t.Fatalf("checksum GET: %v", err)
	}
	csResp.Body.Close()
	if csResp.StatusCode != http.StatusNotFound {
		t.Errorf("checksum after filter: want 404, got %d", csResp.StatusCode)
	}
}

func TestMavenChecksumProxiedForUnfilteredMeta(t *testing.T) {
	// No rules — nothing filtered; checksum should be proxied normally.
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".sha1") {
			fmt.Fprint(w, "deadbeef")
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})

	// Request the checksum without warming the metadata cache — should proxy.
	resp, err := http.Get(ts.url + metadataPath + ".sha1")
	if err != nil {
		t.Fatalf("GET checksum: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("checksum proxy: want 200, got %d", resp.StatusCode)
	}
}

func TestMavenArtifactAllowed(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, "application/java-archive")
		fmt.Fprint(w, "PK") // fake JAR header
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + artifactPath)
	if err != nil {
		t.Fatalf(testErrGETArtifact, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("artifact allowed: want 200, got %d", resp.StatusCode)
	}
}

func TestMavenArtifactBlockedPreRelease(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "PK")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "block-pre-release", Action: "deny", BlockPreRelease: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + rcPath)
	if err != nil {
		t.Fatalf("GET rc artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("pre-release artifact: want 403, got %d", resp.StatusCode)
	}
}

func TestMavenArtifactBlockedByDefaultsAgeWithWildcardRule(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC1123)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Last-Modified", recent)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set(hdrContentType, "application/java-archive")
		fmt.Fprint(w, "PK")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		Rules: []config.PackageRule{
			{Name: testRuleBlockSnapshots, PackagePatterns: []string{"*"}, Action: "deny", BlockSnapshots: true},
		},
	}

	ts := buildMavenTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + artifactPath)
	if err != nil {
		t.Fatalf(testErrGETArtifact, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("young artifact: want 403, got %d", resp.StatusCode)
	}
}

func TestMavenArtifactBlockedSnapshot(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "PK")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleBlockSnapshots, Action: "deny", BlockSnapshots: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + snapshotPath)
	if err != nil {
		t.Fatalf("GET snapshot artifact: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("snapshot artifact: want 403, got %d", resp.StatusCode)
	}
}

func TestMavenArtifactUpstream502(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + artifactPath)
	if err != nil {
		t.Fatalf(testErrGETArtifact, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream 502 passthrough: want 502, got %d", resp.StatusCode)
	}
}

func TestMavenMetadataUpstream502(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer mock.Close()

	ts := buildMavenTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("metadata upstream 502: want 502, got %d", resp.StatusCode)
	}
}

func TestParseMavenPath(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		wantGroup    string
		wantArtifact string
		wantVersion  string
		wantFilename string
		wantErr      bool
	}{
		{
			name:         "artifact JAR",
			input:        artifactPath,
			wantGroup:    testGroupID,
			wantArtifact: "mylib",
			wantVersion:  "1.0.0",
			wantFilename: "mylib-1.0.0.jar",
		},
		{
			name:         "metadata xml",
			input:        metadataPath,
			wantGroup:    testGroupID,
			wantArtifact: "mylib",
			wantVersion:  "",
			wantFilename: "maven-metadata.xml",
		},
		{
			name:         "metadata sha1 checksum",
			input:        "/com/example/mylib/maven-metadata.xml.sha1",
			wantGroup:    testGroupID,
			wantArtifact: "mylib",
			wantVersion:  "",
			wantFilename: "maven-metadata.xml.sha1",
		},
		{
			name:         "snapshot artifact",
			input:        "/org/example/lib/1.0-SNAPSHOT/lib-1.0-SNAPSHOT.jar",
			wantGroup:    "org/example",
			wantArtifact: "lib",
			wantVersion:  "1.0-SNAPSHOT",
			wantFilename: "lib-1.0-SNAPSHOT.jar",
		},
		{
			name:    "too short path",
			input:   "/a",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			group, artifact, version, filename, err := ParseMavenPath(tc.input)
			assertParseMavenPath(t, tc.wantErr, tc.wantGroup, tc.wantArtifact, tc.wantVersion, tc.wantFilename, group, artifact, version, filename, err)
		})
	}
}

//nolint:revive // test helper with parallel expected/actual value comparison
func assertParseMavenPath(t *testing.T, wantErr bool, wantGroup, wantArtifact, wantVersion, wantFilename, group, artifact, version, filename string, err error) { //NOSONAR
	t.Helper()
	if wantErr {
		if err == nil {
			t.Error("expected error, got nil")
		}
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if group != wantGroup {
		t.Errorf("group: want %q, got %q", wantGroup, group)
	}
	if artifact != wantArtifact {
		t.Errorf("artifact: want %q, got %q", wantArtifact, artifact)
	}
	if version != wantVersion {
		t.Errorf("version: want %q, got %q", wantVersion, version)
	}
	if filename != wantFilename {
		t.Errorf("filename: want %q, got %q", wantFilename, filename)
	}
}

func TestIsMetadataRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/com/example/mylib/maven-metadata.xml", true},
		{"/com/example/mylib/maven-metadata.xml.sha1", true},
		{"/com/example/mylib/maven-metadata.xml.md5", true},
		{"/com/example/mylib/1.0.0/mylib-1.0.0.jar", false},
		{"/com/example/mylib/1.0.0/mylib-1.0.0.pom", false},
		{"/com/example/mylib/1.0.0/mylib-1.0.0.jar.sha1", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := IsMetadataRequest(tc.path)
			if got != tc.want {
				t.Errorf("IsMetadataRequest(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsChecksumRequest(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/a/b/c.jar.sha1", true},
		{"/a/b/c.jar.md5", true},
		{"/a/b/c.jar.sha256", true},
		{"/a/b/c.jar", false},
		{"/a/b/maven-metadata.xml", false},
	}

	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := IsChecksumRequest(tc.path)
			if got != tc.want {
				t.Errorf("IsChecksumRequest(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseMetadataXML(t *testing.T) {
	raw := mavenMetadataXML([]string{"1.0.0", "1.1.0", testVersion2Snap})
	versions, err := ParseMetadataXML([]byte(raw))
	if err != nil {
		t.Fatalf("ParseMetadataXML: %v", err)
	}
	if len(versions) != 3 {
		t.Errorf("want 3 versions, got %d: %v", len(versions), versions)
	}
}

func TestParseMetadataXMLInvalid(t *testing.T) {
	_, err := ParseMetadataXML([]byte("not-xml"))
	if err == nil {
		t.Error("expected error for invalid XML")
	}
}

func TestParseMetadataVersionMetaUsesLastUpdated(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>mylib</artifactId>
  <versioning>
    <versions>
      <version>1.0.0</version>
      <version>1.1.0-SNAPSHOT</version>
    </versions>
    <lastUpdated>20260310112233</lastUpdated>
    <snapshotVersions>
      <snapshotVersion>
        <value>1.1.0-SNAPSHOT</value>
        <updated>20260311121212</updated>
      </snapshotVersion>
    </snapshotVersions>
  </versioning>
</metadata>`

	versions, err := ParseMetadataVersionMeta([]byte(raw))
	if err != nil {
		t.Fatalf("ParseMetadataVersionMeta: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("want 2 versions, got %d", len(versions))
	}
	if versions[0].PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt from lastUpdated fallback")
	}
	if versions[1].PublishedAt.IsZero() {
		t.Error("expected non-zero PublishedAt from snapshotVersion.updated")
	}
}

func TestFilterMetadataXML(t *testing.T) {
	raw := mavenMetadataXML([]string{"1.0.0", "1.1.0", testVersion2Snap})
	allowed := []string{"1.0.0", "1.1.0"}
	filtered, err := FilterMetadataXML([]byte(raw), allowed)
	if err != nil {
		t.Fatalf("FilterMetadataXML: %v", err)
	}
	result := string(filtered)
	if strings.Contains(result, testVersion2Snap) {
		t.Error("filtered XML still contains SNAPSHOT version")
	}
	if !strings.Contains(result, "1.0.0") {
		t.Error("filtered XML missing 1.0.0")
	}
	if !strings.Contains(result, "1.1.0") {
		t.Error("filtered XML missing 1.1.0")
	}
}

func TestFilterMetadataXMLAllFiltered(t *testing.T) {
	raw := mavenMetadataXML([]string{testVersionSnap})
	filtered, err := FilterMetadataXML([]byte(raw), []string{})
	if err != nil {
		t.Fatalf("FilterMetadataXML: %v", err)
	}
	result := string(filtered)
	if strings.Contains(result, testVersionSnap) {
		t.Error("should not contain any versions")
	}
}

// mavenMetadataXMLWithTimestamp generates metadata XML including a <lastUpdated> field.
func mavenMetadataXMLWithTimestamp(versions []string, lastUpdated string) string {
	versionElems := ""
	for _, v := range versions {
		versionElems += fmt.Sprintf("    <version>%s</version>\n", v)
	}
	latest := ""
	release := ""
	if len(versions) > 0 {
		latest = versions[len(versions)-1]
		for i := len(versions) - 1; i >= 0; i-- {
			if !strings.Contains(versions[i], "SNAPSHOT") && !strings.Contains(versions[i], "rc") {
				release = versions[i]
				break
			}
		}
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<metadata>
  <groupId>com.example</groupId>
  <artifactId>mylib</artifactId>
  <versioning>
    <latest>%s</latest>
    <release>%s</release>
    <versions>
%s    </versions>
    <lastUpdated>%s</lastUpdated>
  </versioning>
</metadata>
`, latest, release, versionElems, lastUpdated)
}

// TestMavenMetadataAgeBlockFiltersRecentVersions verifies that a metadata response
// with a recent lastUpdated timestamp has all its versions filtered when
// min_package_age_days is set, since all versions inherit the artifact-level timestamp.
func TestMavenMetadataAgeBlockFiltersRecentVersions(t *testing.T) {
	// Use "just now" as the lastUpdated — every version inherits this publication time.
	now := time.Now().UTC().Format("20060102150405")
	xmlBody := mavenMetadataXMLWithTimestamp([]string{"1.0.0", "1.1.0", "2.0.0"}, now)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()
	// All versions are recent (0 days old < 7) → all blocked → 403.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("age block all versions: want 403, got %d", resp.StatusCode)
	}
}

// TestMavenMetadataAgeBlockKeepsOldVersions verifies that old versions survive
// the age filter while recent ones are removed, and <latest>/<release> are rewritten.
func TestMavenMetadataAgeBlockKeepsOldVersions(t *testing.T) {
	// Use a timestamp 30 days ago — old enough to pass a 7-day age rule.
	oldTime := time.Now().UTC().AddDate(0, 0, -30).Format("20060102150405")
	xmlBody := mavenMetadataXMLWithTimestamp([]string{"1.0.0", "1.1.0"}, oldTime)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := string(body)

	// Both versions should survive because their timestamp is 30 days old.
	if !strings.Contains(result, "1.0.0") || !strings.Contains(result, "1.1.0") {
		t.Errorf("age block: old versions should pass; got:\n%s", result)
	}
}

// TestMavenMetadataLatestRewrittenAfterAgeFilter verifies that when the
// <latest> version is filtered by age, it is repointed to the newest remaining version.
func TestMavenMetadataLatestRewrittenAfterAgeFilter(t *testing.T) {
	// All versions share lastUpdated (now) — so the age rule blocks everything.
	now := time.Now().UTC().Format("20060102150405")
	xmlBody := mavenMetadataXMLWithTimestamp([]string{"1.0.0", "2.0.0", "3.0.0"}, now)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()

	// All versions age-blocked → 403.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("age block all versions: want 403, got %d", resp.StatusCode)
	}
}

// TestMavenMetadataLatestRewrittenToOldestAllowed verifies that when only the
// latest version is blocked (by pre-release rule), <latest> is repointed.
func TestMavenMetadataLatestRewrittenToOldestAllowed(t *testing.T) {
	oldTime := time.Now().UTC().AddDate(0, 0, -30).Format("20060102150405")
	// Latest is 2.0.0-rc1 (pre-release); only 1.0.0 survives the filter.
	xmlBody := mavenMetadataXMLWithTimestamp([]string{"1.0.0", "2.0.0-rc1"}, oldTime)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, testContentXML)
		fmt.Fprint(w, xmlBody)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "block-pre", BlockPreRelease: true},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + metadataPath)
	if err != nil {
		t.Fatalf(testErrGETMetadata, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	result := string(body)

	if strings.Contains(result, "2.0.0-rc1") {
		t.Error("pre-release 2.0.0-rc1 should be filtered from metadata")
	}
	if !strings.Contains(result, "1.0.0") {
		t.Error("stable 1.0.0 should remain")
	}

	// <latest> originally pointed to 2.0.0-rc1; after filter it should be 1.0.0.
	if strings.Contains(result, "<latest>2.0.0-rc1</latest>") {
		t.Error("<latest> should have been rewritten from 2.0.0-rc1 to 1.0.0")
	}
}

// TestMavenArtifactAgeBlockDeniesNoTimestamp verifies that a direct artifact download
// is denied when age filtering applies and no Last-Modified header is available
// (fail-closed to prevent age-check bypass via direct artifact requests).
func TestMavenArtifactAgeBlockDeniesNoTimestamp(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// No Last-Modified header → publishedAt is zero.
		fmt.Fprint(w, "PK")
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildMavenTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + artifactPath)
	if err != nil {
		t.Fatalf(testErrGETArtifact, err)
	}
	defer resp.Body.Close()

	// Should be 403: age filtering required but no timestamp available (fail-closed).
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("artifact with age rule + no timestamp: want 403, got %d", resp.StatusCode)
	}
}
