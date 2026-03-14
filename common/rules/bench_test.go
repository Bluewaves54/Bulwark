// SPDX-License-Identifier: Apache-2.0

package rules_test

import (
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

// benchPolicy is a representative policy used across all benchmarks.
var benchPolicy = config.PolicyConfig{
	Defaults: config.RulesDefaults{
		MinPackageAgeDays: 7,
		BlockPreReleases:  true,
	},
	Rules: []config.PackageRule{
		{
			Name:            "internal-namespace",
			PackagePatterns: []string{"^corp-"},
			Action:          "deny",
			Reason:          "internal namespace reserved",
		},
		{
			Name:            "trusted-pkgs",
			PackagePatterns: []string{"^requests$", "^lodash$", "^numpy$"},
			Action:          "allow",
			BypassAgeFilter: true,
		},
	},
}

// buildBenchVersions creates n VersionMeta entries alternating stable, pre-release, and recent.
func buildBenchVersions(n int) []rules.VersionMeta {
	now := time.Now()
	vers := make([]rules.VersionMeta, n)
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0: // stable, old enough
			vers[i] = rules.VersionMeta{Version: "1.0." + string(rune('0'+i%10)), PublishedAt: now.Add(-30 * 24 * time.Hour)}
		case 1: // pre-release
			vers[i] = rules.VersionMeta{Version: "1.0." + string(rune('0'+i%10)) + "a1", PublishedAt: now.Add(-30 * 24 * time.Hour)}
		case 2: // too new
			vers[i] = rules.VersionMeta{Version: "2.0." + string(rune('0'+i%10)), PublishedAt: now.Add(-1 * time.Hour)}
		}
	}
	return vers
}

func BenchmarkEvaluatePackageAllowed(b *testing.B) {
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "requests"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.EvaluatePackage(pkg)
	}
}

func BenchmarkEvaluatePackageDenied(b *testing.B) {
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "corp-internal-lib"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.EvaluatePackage(pkg)
	}
}

func BenchmarkEvaluateVersionStable(b *testing.B) {
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "somelib"}
	ver := rules.VersionMeta{Version: "1.2.3", PublishedAt: time.Now().Add(-30 * 24 * time.Hour)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.EvaluateVersion(pkg, ver)
	}
}

func BenchmarkEvaluateVersionPreRelease(b *testing.B) {
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "somelib"}
	ver := rules.VersionMeta{Version: "1.2.3b2", PublishedAt: time.Now().Add(-30 * 24 * time.Hour)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.EvaluateVersion(pkg, ver)
	}
}

func BenchmarkEvaluateVersionTooNew(b *testing.B) {
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "somelib"}
	ver := rules.VersionMeta{Version: "1.2.3", PublishedAt: time.Now().Add(-1 * time.Hour)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		engine.EvaluateVersion(pkg, ver)
	}
}

// BenchmarkEvaluateVersionBulk simulates filtering N versions of a single package —
// the real work the proxy does per metadata request.
func BenchmarkEvaluateVersionBulk10(b *testing.B) {
	benchmarkBulkVersions(b, 10)
}

func BenchmarkEvaluateVersionBulk50(b *testing.B) {
	benchmarkBulkVersions(b, 50)
}

func BenchmarkEvaluateVersionBulk200(b *testing.B) {
	benchmarkBulkVersions(b, 200)
}

func benchmarkBulkVersions(b *testing.B, n int) {
	b.Helper()
	engine := rules.New(benchPolicy)
	pkg := rules.PackageMeta{Name: "somelib", Versions: buildBenchVersions(n)}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, v := range pkg.Versions {
			engine.EvaluateVersion(pkg, v)
		}
	}
}
