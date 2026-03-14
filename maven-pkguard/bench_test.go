// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

var benchMavenEngine = rules.New(config.PolicyConfig{
	Defaults: config.RulesDefaults{BlockPreReleases: true},
	Rules: []config.PackageRule{
		{
			Name:            "block-snapshots",
			PackagePatterns: []string{".*"},
			Action:          "deny",
			BlockSnapshots:  true,
			Reason:          "SNAPSHOT versions are not permitted",
		},
	},
})

// buildMavenMetadataXML creates a maven-metadata.xml body with n versions.
// Every third version is a SNAPSHOT.
func buildMavenMetadataXML(n int) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	sb.WriteString("<metadata>\n  <groupId>com.example</groupId>\n  <artifactId>mylib</artifactId>\n  <versioning>\n    <versions>\n")
	for i := 0; i < n; i++ {
		ver := fmt.Sprintf("1.0.%d", i)
		if i%3 == 2 {
			ver += "-SNAPSHOT"
		}
		sb.WriteString("      <version>" + ver + "</version>\n")
	}
	sb.WriteString("    </versions>\n  </versioning>\n</metadata>\n")
	return []byte(sb.String())
}

func BenchmarkParseAndFilterMavenMetadata10Versions(b *testing.B) {
	benchmarkMavenMetadata(b, 10)
}

func BenchmarkParseAndFilterMavenMetadata50Versions(b *testing.B) {
	benchmarkMavenMetadata(b, 50)
}

func BenchmarkParseAndFilterMavenMetadata200Versions(b *testing.B) {
	benchmarkMavenMetadata(b, 200)
}

func benchmarkMavenMetadata(b *testing.B, n int) {
	b.Helper()
	body := buildMavenMetadataXML(n)
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		versions, err := ParseMetadataVersionMeta(body)
		if err != nil {
			b.Fatalf("ParseMetadataVersionMeta: %v", err)
		}
		pkgMeta := rules.PackageMeta{Name: "com.example:mylib", Versions: versions}
		allowed := make([]string, 0, len(versions))
		for _, v := range versions {
			if dec := benchMavenEngine.EvaluateVersion(pkgMeta, v); dec.Allow {
				allowed = append(allowed, v.Version)
			}
		}
		_, err = FilterMetadataXML(body, allowed)
		if err != nil {
			b.Fatalf("FilterMetadataXML: %v", err)
		}
	}
}

// ─── End-to-end HTTP latency benchmarks ──────────────────────────────────────

func benchMavenE2ESetup(b *testing.B, n int) (proxyURL string, cleanup func()) {
	b.Helper()
	body := buildMavenMetadataXML(n)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(body) //nolint:errcheck
	}))
	cfg := &config.Config{
		Server:   config.ServerConfig{Port: 0},
		Upstream: config.UpstreamConfig{URL: mock.URL, TimeoutSeconds: 5},
		Cache:    config.CacheConfig{TTLSeconds: 300},
		Logging:  config.LoggingConfig{Level: "error", Format: "text"},
		Policy: config.PolicyConfig{
			Defaults: config.RulesDefaults{BlockPreReleases: true},
			Rules: []config.PackageRule{
				{
					Name:            "block-snapshots",
					PackagePatterns: []string{".*"},
					Action:          "deny",
					BlockSnapshots:  true,
					Reason:          "SNAPSHOT versions are not permitted",
				},
			},
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

func BenchmarkMavenE2EUncached50Versions(b *testing.B) {
	proxyURL, cleanup := benchMavenE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + fmt.Sprintf("/com/example/mylib%d/maven-metadata.xml", i))
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}

func BenchmarkMavenE2ECached50Versions(b *testing.B) {
	proxyURL, cleanup := benchMavenE2ESetup(b, 50)
	defer cleanup()
	tr := &http.Transport{MaxIdleConnsPerHost: 1, DisableKeepAlives: false}
	client := &http.Client{Transport: tr}
	defer tr.CloseIdleConnections()
	resp, err := client.Get(proxyURL + "/com/example/mylib/maven-metadata.xml")
	if err != nil {
		b.Fatalf("prime cache: %v", err)
	}
	resp.Body.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		resp, err := client.Get(proxyURL + "/com/example/mylib/maven-metadata.xml")
		if err != nil {
			b.Fatalf("GET: %v", err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
}
