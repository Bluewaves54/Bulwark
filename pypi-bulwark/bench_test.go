// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

// buildPyPIJSONBody constructs a minimal PyPI JSON API response with n versions.
// Half the versions are pre-releases to exercise the filter path.
func buildPyPIJSONBody(pkg string, n int) []byte {
	type distFile struct {
		UploadTime string `json:"upload_time"`
		Filename   string `json:"filename"`
		URL        string `json:"url"`
	}
	type response struct {
		Info     map[string]string     `json:"info"`
		Releases map[string][]distFile `json:"releases"`
	}
	r := response{
		Info:     map[string]string{"license": "MIT"},
		Releases: make(map[string][]distFile, n),
	}
	old := time.Now().Add(-30 * 24 * time.Hour).Format("2006-01-02T15:04:05")
	for i := 0; i < n; i++ {
		ver := fmt.Sprintf("1.0.%d", i)
		if i%2 == 1 {
			ver += "a1"
		}
		r.Releases[ver] = []distFile{{
			UploadTime: old,
			Filename:   pkg + "-" + ver + ".tar.gz",
			URL:        "https://files.pythonhosted.org/packages/" + pkg + "-" + ver + ".tar.gz",
		}}
	}
	b, _ := json.Marshal(r)
	return b
}

var benchEngine = rules.New(config.PolicyConfig{
	Defaults: config.RulesDefaults{BlockPreReleases: true},
})

func BenchmarkFilterPyPIJSONResponse10Versions(b *testing.B) {
	benchmarkPyPIJSON(b, 10)
}

func BenchmarkFilterPyPIJSONResponse50Versions(b *testing.B) {
	benchmarkPyPIJSON(b, 50)
}

func BenchmarkFilterPyPIJSONResponse200Versions(b *testing.B) {
	benchmarkPyPIJSON(b, 200)
}

func benchmarkPyPIJSON(b *testing.B, n int) {
	b.Helper()
	body := buildPyPIJSONBody("requests", n)
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, _, err := filterPyPIJSONResponse(body, "requests", benchEngine)
		if err != nil {
			b.Fatalf("filterPyPIJSONResponse: %v", err)
		}
	}
}

// ─── End-to-end HTTP latency benchmarks ──────────────────────────────────────

func benchPyPIE2ESetup(b *testing.B, n int) (proxyURL string, cleanup func()) {
	b.Helper()
	body := buildPyPIJSONBody("requests", n)
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

func BenchmarkPyPIE2EUncached50Versions(b *testing.B) {
	proxyURL, cleanup := benchPyPIE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + fmt.Sprintf("/pypi/requests%d/json", i))
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}

func BenchmarkPyPIE2ECached50Versions(b *testing.B) {
	proxyURL, cleanup := benchPyPIE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	resp, err := client.Get(proxyURL + "/pypi/requests/json")
	if err != nil {
		b.Fatalf("prime cache: %v", err)
	}
	resp.Body.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + "/pypi/requests/json")
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}
