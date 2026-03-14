// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

var (
	benchNpmEngine = rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{BlockPreReleases: true},
	})
	benchNpmLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
)

// buildNpmPackumentBody creates a minimal packument JSON with n versions.
// Odd-indexed versions are pre-releases.
func buildNpmPackumentBody(pkg string, n int) []byte {
	type versionEntry struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Dist    struct {
			Tarball string `json:"tarball"`
		} `json:"dist"`
	}
	type packument struct {
		Name     string                  `json:"name"`
		Versions map[string]versionEntry `json:"versions"`
		DistTags map[string]string       `json:"dist-tags"`
		Time     map[string]string       `json:"time"`
	}

	p := packument{
		Name:     pkg,
		Versions: make(map[string]versionEntry, n),
		DistTags: map[string]string{"latest": "1.0.0"},
		Time:     make(map[string]string, n),
	}
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	for i := 0; i < n; i++ {
		ver := fmt.Sprintf("1.0.%d", i)
		if i%2 == 1 {
			ver += "-beta.1"
		}
		p.Versions[ver] = versionEntry{
			Name:    pkg,
			Version: ver,
			Dist: struct {
				Tarball string `json:"tarball"`
			}{Tarball: fmt.Sprintf("https://registry.npmjs.org/%s/-/%s-%s.tgz", pkg, pkg, ver)},
		}
		p.Time[ver] = old
	}

	b, _ := json.Marshal(p)
	return b
}

func BenchmarkFilterNpmPackument10Versions(b *testing.B) {
	benchmarkNpmPackument(b, 10)
}

func BenchmarkFilterNpmPackument50Versions(b *testing.B) {
	benchmarkNpmPackument(b, 50)
}

func BenchmarkFilterNpmPackument200Versions(b *testing.B) {
	benchmarkNpmPackument(b, 200)
}

func benchmarkNpmPackument(b *testing.B, n int) {
	b.Helper()
	body := buildNpmPackumentBody("lodash", n)
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := filterNpmPackument(body, "lodash", benchNpmEngine, "http://localhost:18001", benchNpmLogger)
		if err != nil {
			b.Fatalf(testErrFilterPkg, err)
		}
	}
}

// ─── End-to-end HTTP latency benchmarks ──────────────────────────────────────
// These measure the full proxy overhead: HTTP handling + upstream fetch + filter +
// cache. The mock upstream has ~zero latency, so the numbers represent
// pure proxy-added overhead.

func benchNpmE2ESetup(b *testing.B, n int) (proxyURL string, cleanup func()) {
	b.Helper()
	body := buildNpmPackumentBody("lodash", n)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body) //nolint:errcheck
	}))
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 300},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy: config.PolicyConfig{
			Defaults: config.RulesDefaults{BlockPreReleases: true},
		},
	}
	cfg.Defaults()
	logger, logLevel, _ := createLogger("text", "error", "")
	srv, err := buildServer(cfg, logger, logLevel)
	if err != nil {
		b.Fatalf("buildServer: %v", err)
	}
	ts := httptest.NewServer(srv.mux)
	return ts.URL, func() { ts.Close(); mock.Close() }
}

func BenchmarkNpmE2EUncached50Versions(b *testing.B) {
	proxyURL, cleanup := benchNpmE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Use unique package names to avoid cache hits.
		resp, err := client.Get(proxyURL + fmt.Sprintf("/lodash%d", i))
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}

func BenchmarkNpmE2ECached50Versions(b *testing.B) {
	proxyURL, cleanup := benchNpmE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	// Prime the cache.
	resp, err := client.Get(proxyURL + "/lodash")
	if err != nil {
		b.Fatalf("prime cache: %v", err)
	}
	resp.Body.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + "/lodash")
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}
