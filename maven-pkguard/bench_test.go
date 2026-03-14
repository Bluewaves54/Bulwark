// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
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
