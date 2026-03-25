// SPDX-License-Identifier: Apache-2.0

//go:build e2e

package e2e

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Shared proxy URLs — started once in TestMain with allow-all configs.
var (
	pypiProxyURL  string
	npmProxyURL   string
	mavenProxyURL string
	vsxProxyURL   string
)

// Shared binary paths — built once in TestMain, used by filter tests that start their own proxy.
var (
	pypiProxyBinPath  string
	npmProxyBinPath   string
	mavenProxyBinPath string
	vsxProxyBinPath   string
)

// TestMain builds all proxy binaries, starts allow-all proxy instances, runs the
// test suite, then terminates the proxies.
// The entire suite is skipped when BULWARK_E2E_LIVE is not "true".
func TestMain(m *testing.M) {
	if os.Getenv("BULWARK_E2E_LIVE") != "true" {
		fmt.Fprintln(os.Stderr, "e2e: skipping — set BULWARK_E2E_LIVE=true to run live tests")
		os.Exit(0)
	}

	wd, err := os.Getwd()
	if err != nil {
		log.Fatalf("e2e: getwd: %v", err)
	}
	// The e2e module lives one directory below the repo root.
	repoRoot := filepath.Dir(wd)
	cfgDir := filepath.Join(wd, "testdata", "rules")

	tmpDir, err := os.MkdirTemp("", "bulwark-e2e-*")
	if err != nil {
		log.Fatalf("e2e: mktempdir: %v", err)
	}

	pypiProxyBinPath = buildBinaryMain(repoRoot, pypiDir, tmpDir)
	npmProxyBinPath = buildBinaryMain(repoRoot, npmDir, tmpDir)
	mavenProxyBinPath = buildBinaryMain(repoRoot, mavenDir, tmpDir)
	vsxProxyBinPath = buildBinaryMain(repoRoot, vsxDir, tmpDir)

	pypiProxy := startProxyMain(pypiProxyBinPath, filepath.Join(cfgDir, "pypi-allow-all.yaml"), pypiE2EPort)
	npmProxy := startProxyMain(npmProxyBinPath, filepath.Join(cfgDir, "npm-allow-all.yaml"), npmE2EPort)
	mavenProxy := startProxyMain(mavenProxyBinPath, filepath.Join(cfgDir, "maven-allow-all.yaml"), mavenE2EPort)
	vsxProxy := startProxyMain(vsxProxyBinPath, filepath.Join(cfgDir, "vsx-allow-all.yaml"), vsxE2EPort)

	pypiProxyURL = pypiProxy.BaseURL
	npmProxyURL = npmProxy.BaseURL
	mavenProxyURL = mavenProxy.BaseURL
	vsxProxyURL = vsxProxy.BaseURL

	code := m.Run()

	pypiProxy.Stop()
	npmProxy.Stop()
	mavenProxy.Stop()
	vsxProxy.Stop()
	_ = os.RemoveAll(tmpDir)
	os.Exit(code)
}

// buildBinaryMain compiles a proxy module without a *testing.T (for use in TestMain).
func buildBinaryMain(root, dir, outDir string) string {
	ext := binaryExt()
	outPath := filepath.Join(outDir, dir+ext)
	cmd := exec.Command("go", "build", "-o", outPath, ".")
	cmd.Dir = filepath.Join(root, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		log.Fatalf("e2e: go build %s: %v", dir, err)
	}
	return outPath
}

// startProxyMain starts a proxy process without a *testing.T (for use in TestMain).
func startProxyMain(binary, cfgPath string, port int) *ProxyProcess {
	portEnv := fmt.Sprintf("PORT=%s", strconv.Itoa(port))
	cmd := exec.Command(binary, "-config", cfgPath)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), portEnv)
	if err := cmd.Start(); err != nil {
		log.Fatalf("e2e: start proxy %s: %v", binary, err)
	}
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	pollHealthzMain(baseURL + "/healthz")
	return &ProxyProcess{cmd: cmd, BaseURL: baseURL}
}

// pollHealthzMain polls a /healthz endpoint without a *testing.T (for use in TestMain).
func pollHealthzMain(healthURL string) {
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
	log.Fatalf("e2e: proxy did not become healthy at %s within 15s", healthURL)
}
