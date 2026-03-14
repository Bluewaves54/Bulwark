// SPDX-License-Identifier: Apache-2.0

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"PKGuard/common/config"
)

const (
	validURL      = "https://example.com"
	invalidAction = "invalid"
	ruleName      = "testrule"
)

func TestLoadConfigValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
server:
  port: 18000
upstream:
  url: "https://pypi.org"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 18000 {
		t.Errorf("port: want 18000, got %d", cfg.Server.Port)
	}
	if cfg.Upstream.URL != "https://pypi.org" {
		t.Errorf("url: want https://pypi.org, got %q", cfg.Upstream.URL)
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("default port: want 8080, got %d", cfg.Server.Port)
	}
	if cfg.Server.ReadTimeoutSeconds != 30 {
		t.Errorf("default read timeout: want 30, got %d", cfg.Server.ReadTimeoutSeconds)
	}
	if cfg.Cache.TTLSeconds != 300 {
		t.Errorf("default ttl: want 300, got %d", cfg.Cache.TTLSeconds)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("default log level: want info, got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("default log format: want text, got %q", cfg.Logging.Format)
	}
}

func TestLoadConfigMissingFile(t *testing.T) {
	_, err := config.Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadConfigMissingURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("server:\n  port: 8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for missing upstream.url")
	}
}

func TestLoadConfigInvalidPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "server:\n  port: 99999\nupstream:\n  url: \"https://pypi.org\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid port")
	}
}

func TestLoadConfigInvalidAction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "upstream:\n  url: \"https://pypi.org\"\npolicy:\n  rules:\n    - name: \"" + ruleName + "\"\n      action: \"" + invalidAction + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid action")
	}
}

func TestLoadConfigInvalidURLScheme(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "upstream:\n  url: \"ftp://example.com\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected validation error for ftp:// URL")
	}
}

func TestLoadConfigInvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(": invalid\n::::\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected parse error for invalid yaml")
	}
}

func TestEnvOverridePort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "19000")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Server.Port != 19000 {
		t.Errorf("env PORT override: want 19000, got %d", cfg.Server.Port)
	}
}

func TestEnvOverrideAuthToken(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGUARD_AUTH_TOKEN", "secret-token")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Upstream.Token != "secret-token" {
		t.Errorf("env token override: want secret-token, got %q", cfg.Upstream.Token)
	}
}

func TestEnvOverrideAuthCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PKGUARD_AUTH_USERNAME", "user1")
	t.Setenv("PKGUARD_AUTH_PASSWORD", "pass1")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Upstream.Username != "user1" {
		t.Errorf("env username override: want user1, got %q", cfg.Upstream.Username)
	}
	if cfg.Upstream.Password != "pass1" {
		t.Errorf("env password override: want pass1, got %q", cfg.Upstream.Password)
	}
}

func TestEnvOverridePortInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PORT", "notanumber")
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid PORT env var is ignored; default applies.
	if cfg.Server.Port != 8080 {
		t.Errorf("invalid PORT env: want default 8080, got %d", cfg.Server.Port)
	}
}

func TestConfigRuleParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
upstream:
  url: "https://pypi.org"
policy:
  rules:
    - name: "block-pre-release"
      action: "deny"
      block_pre_release: true
      reason: "no prereleases"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Policy.Rules) != 1 {
		t.Fatalf("want 1 rule, got %d", len(cfg.Policy.Rules))
	}
	r := cfg.Policy.Rules[0]
	if r.Name != "block-pre-release" {
		t.Errorf("rule name: want block-pre-release, got %q", r.Name)
	}
	if r.Action != "deny" {
		t.Errorf("rule action: want deny, got %q", r.Action)
	}
	if !r.BlockPreRelease {
		t.Error("block_pre_release should be true")
	}
}

func TestFailModeDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("upstream:\n  url: \"https://pypi.org\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Policy.FailMode != config.FailModeOpen {
		t.Errorf("default fail_mode: want %q, got %q", config.FailModeOpen, cfg.Policy.FailMode)
	}
}

func TestFailModeClosedValid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "upstream:\n  url: \"https://pypi.org\"\npolicy:\n  fail_mode: \"closed\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Policy.FailMode != config.FailModeClosed {
		t.Errorf("fail_mode: want %q, got %q", config.FailModeClosed, cfg.Policy.FailMode)
	}
}

func TestFailModeOpenExplicit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "upstream:\n  url: \"https://pypi.org\"\npolicy:\n  fail_mode: \"open\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Policy.FailMode != config.FailModeOpen {
		t.Errorf("fail_mode: want %q, got %q", config.FailModeOpen, cfg.Policy.FailMode)
	}
}

func TestFailModeInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "upstream:\n  url: \"https://pypi.org\"\npolicy:\n  fail_mode: \"strict\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected error for invalid fail_mode")
	}
}
