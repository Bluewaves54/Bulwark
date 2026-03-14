// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
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
		_, _, err := filterPyPIJSONResponse(body, "requests", benchEngine)
		if err != nil {
			b.Fatalf("filterPyPIJSONResponse: %v", err)
		}
	}
}
