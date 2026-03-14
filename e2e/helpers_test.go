// SPDX-License-Identifier: Apache-2.0

//go:build e2e

// Package e2e contains live end-to-end tests for the Corp Registry Curation proxies.
// These tests start real proxy binaries (built via "go build") and direct them at
// the public upstream registries (pypi.org, registry.npmjs.org, repo1.maven.org).
//
// Tests are gated by the "e2e" build tag so they never run during normal CI unit testing.
//
// Usage:
//
//	PKGUARD_E2E_LIVE=true go test -v -tags=e2e -timeout=5m ./e2e/...
package e2e

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// Port assignments for e2e proxy instances (offset from defaults to avoid conflicts).
const (
	pypiE2EPort  = 18100
	npmE2EPort   = 18101
	mavenE2EPort = 18102
)

// Module directory names.
const (
	pypiDir  = "pypi-pkguard"
	npmDir   = "npm-pkguard"
	mavenDir = "maven-pkguard"
)

// ProxyProcess holds a running proxy child process and its base URL.
type ProxyProcess struct {
	cmd     *exec.Cmd
	BaseURL string
}

// Stop terminates the child process.
func (p *ProxyProcess) Stop() {
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill() // best-effort; process may already have exited
	}
}

// binaryExt returns ".exe" on Windows, "" otherwise.
func binaryExt() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// testdataConfig returns the absolute path to a testdata config file.
func testdataConfig(t *testing.T, name string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Join(wd, "testdata", "rules", name)
}

// startProxy launches a proxy binary with the given config on the given port.
// The process is registered with t.Cleanup for automatic teardown.
func startProxy(t *testing.T, binary, cfgPath string, port int) *ProxyProcess {
	t.Helper()
	portEnv := fmt.Sprintf("PORT=%s", strconv.Itoa(port))
	cmd := exec.Command(binary, "-config", cfgPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), portEnv)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start proxy %s: %v", binary, err)
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	pp := &ProxyProcess{cmd: cmd, BaseURL: baseURL}
	t.Cleanup(pp.Stop)
	pollHealthz(t, baseURL+"/healthz")
	return pp
}

// pollHealthz polls the /healthz endpoint until it returns 200 or the 15-second timeout elapses.
func pollHealthz(t *testing.T, healthURL string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			statusOK := resp.StatusCode == http.StatusOK
			resp.Body.Close()
			if statusOK {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("proxy did not become healthy at %s within 15s", healthURL)
}

// mustGet performs an HTTP GET and fails the test on transport error.
func mustGet(t *testing.T, rawURL string) *http.Response {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

// mustGetWithHeader performs an HTTP GET with an additional request header.
func mustGetWithHeader(t *testing.T, rawURL, headerName, headerValue string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		t.Fatalf("new request %s: %v", rawURL, err)
	}
	req.Header.Set(headerName, headerValue)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	return resp
}

// assertStatus fails the test if the response status code does not match want.
func assertStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Errorf("status: want %d, got %d", want, resp.StatusCode)
	}
}

// skipIfNotLive skips the test when PKGUARD_E2E_LIVE is not set to "true".
func skipIfNotLive(t *testing.T) {
	t.Helper()
	if os.Getenv("PKGUARD_E2E_LIVE") != "true" {
		t.Skip("skipping live e2e test: set PKGUARD_E2E_LIVE=true to run")
	}
}
