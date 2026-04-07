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
	benchVsxEngine = rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{BlockPreReleases: true},
	})
	benchVsxLogger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
)

// buildVsxExtensionBody creates a minimal extension metadata JSON with n versions.
func buildVsxExtensionBody(namespace, name string, n int) []byte {
	allVersions := make(map[string]string, n)
	old := time.Now().Add(-30 * 24 * time.Hour).Format(time.RFC3339)
	var latestVer string
	for i := 0; i < n; i++ {
		ver := fmt.Sprintf("1.0.%d", i)
		allVersions[ver] = fmt.Sprintf("https://open-vsx.org/api/%s/%s/%s", namespace, name, ver)
		latestVer = ver
	}

	resp := map[string]interface{}{
		"namespace":   namespace,
		"name":        name,
		"allVersions": allVersions,
		"version":     latestVer,
		"timestamp":   old,
		"license":     "MIT",
		"preRelease":  false,
	}
	b, _ := json.Marshal(resp)
	return b
}

func BenchmarkFilterVsxExtension10Versions(b *testing.B) {
	benchmarkVsxExtension(b, 10)
}

func BenchmarkFilterVsxExtension50Versions(b *testing.B) {
	benchmarkVsxExtension(b, 50)
}

func BenchmarkFilterVsxExtension200Versions(b *testing.B) {
	benchmarkVsxExtension(b, 200)
}

func benchmarkVsxExtension(b *testing.B, n int) {
	b.Helper()
	body := buildVsxExtensionBody("ms-python", "python", n)
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := filterExtensionResponse(body, testExtPython, benchVsxEngine, benchVsxLogger)
		if err != nil {
			b.Fatalf(testErrFilterExt, err)
		}
	}
}

// ─── End-to-end HTTP latency benchmarks ──────────────────────────────────────

func benchVsxE2ESetup(b *testing.B, n int) (proxyURL string, cleanup func()) {
	b.Helper()
	body := buildVsxExtensionBody("ms-python", "python", n)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(hdrContentType, mimeJSON)
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

func BenchmarkVsxE2EUncached50Versions(b *testing.B) {
	proxyURL, cleanup := benchVsxE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + fmt.Sprintf("/api/ms-python/python%d", i))
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}

func BenchmarkVsxE2ECached50Versions(b *testing.B) {
	proxyURL, cleanup := benchVsxE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	// Prime the cache.
	resp, err := client.Get(proxyURL + testPathExtPython)
	if err != nil {
		b.Fatalf("prime cache: %v", err)
	}
	resp.Body.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + testPathExtPython)
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}
