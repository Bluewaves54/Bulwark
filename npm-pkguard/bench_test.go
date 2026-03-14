// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
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
		_, _, err := filterNpmPackument(body, "lodash", benchNpmEngine, "http://localhost:18001", benchNpmLogger)
		if err != nil {
			b.Fatalf(testErrFilterPkg, err)
		}
	}
}
