// SPDX-License-Identifier: Apache-2.0

// Package config defines the shared configuration structures and loading logic
// used by all pkguard proxy modules (pypi-pkguard, npm-pkguard, maven-pkguard).
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure shared by all proxy modules.
// Each proxy embeds this via their own top-level config and may add
// ecosystem-specific sections.
type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Upstream UpstreamConfig `yaml:"upstream"`
	Cache    CacheConfig    `yaml:"cache"`
	Policy   PolicyConfig   `yaml:"policy"`
	Metrics  MetricsConfig  `yaml:"metrics"`
	Logging  LoggingConfig  `yaml:"logging"`
}

// ServerConfig holds listener settings.
type ServerConfig struct {
	Port                int `yaml:"port"`
	ReadTimeoutSeconds  int `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int `yaml:"write_timeout_seconds"`
	IdleTimeoutSeconds  int `yaml:"idle_timeout_seconds"`
}

// UpstreamConfig holds settings for the upstream (public) registry.
type UpstreamConfig struct {
	URL                  string    `yaml:"url"`
	TimeoutSeconds       int       `yaml:"timeout_seconds"`
	Username             string    `yaml:"username"`
	Password             string    `yaml:"password"`
	Token                string    `yaml:"token"`
	TLS                  TLSConfig `yaml:"tls"`
	AllowedExternalHosts []string  `yaml:"allowed_external_hosts"`
}

// TLSConfig holds TLS settings for upstream connections.
type TLSConfig struct {
	InsecureSkipVerify bool `yaml:"insecure_skip_verify"`
}

// CacheConfig holds in-memory response cache settings.
type CacheConfig struct {
	TTLSeconds int `yaml:"ttl_seconds"`
	MaxSizeMB  int `yaml:"max_size_mb"`
}

// RulesDefaults are the global fallback rule settings applied whenever
// package-level and version-pattern checks do not produce a definitive decision.
type RulesDefaults struct {
	// MinPackageAgeDays denies versions published within fewer than this many days.
	// Zero disables the global age filter.
	MinPackageAgeDays int `yaml:"min_package_age_days"`
	// BlockPreReleases denies pre-release versions globally when true.
	BlockPreReleases bool `yaml:"block_pre_releases"`
}

// VersionPatternRule allows or denies versions whose string matches a regex.
// It is applied after package-level rules and before global defaults.
type VersionPatternRule struct {
	// Name is a human-readable identifier for the rule.
	Name string `yaml:"name"`
	// Match is a Go regex applied to the version string.
	Match string `yaml:"match"`
	// Action must be "deny" or "allow".
	Action string `yaml:"action"`
	// Reason is the human-readable policy reason returned to the caller.
	Reason  string `yaml:"reason"`
	Enabled *bool  `yaml:"enabled"`
}

// InstallScriptsConfig is a global guard that denies packages with lifecycle
// install scripts (npm postinstall, etc.) unless they are explicitly allowlisted.
type InstallScriptsConfig struct {
	// Enabled activates install-script detection.
	Enabled bool `yaml:"enabled"`
	// Action is "deny" (default) or "warn" (log only, allow through).
	Action string `yaml:"action"`
	// AllowedWithScripts lists package names permitted to have install scripts.
	AllowedWithScripts []string `yaml:"allowed_with_scripts"`
	// Reason is the human-readable message returned to the caller.
	Reason string `yaml:"reason"`
}

// FailModeOpen passes the request through when the policy engine cannot
// inspect metadata (e.g. parse failure), logging a warning. This is the
// default and preserves zero-friction adoption.
const FailModeOpen = "open"

// FailModeClosed blocks the request with 502 Bad Gateway when the policy
// engine cannot inspect metadata. Use this in regulated environments
// (e.g. FedRAMP) where an uninspected artefact must never cross the boundary.
const FailModeClosed = "closed"

// PolicyConfig holds the rule engine policy settings.
type PolicyConfig struct {
	// DryRun converts all deny decisions to allow, logging what would have been denied.
	DryRun bool `yaml:"dry_run"`
	// FailMode controls proxy behaviour when metadata cannot be parsed or
	// filtered. "open" (default) passes the raw response through with a
	// warning; "closed" returns 502 Bad Gateway to prevent uninspected
	// artefacts from reaching clients.
	FailMode string `yaml:"fail_mode"`
	// TrustedPackages is a list of package name glob patterns that bypass ALL
	// rule evaluation (age, pre-release, install scripts, licence, etc.).
	// Use this for well-known scoped packages maintained by trusted organisations.
	// Patterns support trailing wildcard: "@types/*", "@angular/*", "junit:*".
	TrustedPackages []string `yaml:"trusted_packages"`
	// Defaults are applied when rule-specific checks do not produce a
	// definitive allow/deny decision.
	Defaults RulesDefaults `yaml:"defaults"`
	// InstallScripts is a global check applied to every version that declares lifecycle scripts.
	InstallScripts InstallScriptsConfig `yaml:"install_scripts"`
	// Rules is the ordered list of package-level rules.
	Rules []PackageRule `yaml:"rules"`
	// VersionPatterns are regex-based rules applied to version strings after package rules.
	VersionPatterns []VersionPatternRule `yaml:"version_patterns"`
}

// PackageRule is a single package-level allow/deny rule.
// Enabled == nil means enabled; Enabled == &false means disabled (soft delete).
type PackageRule struct {
	Name            string   `yaml:"name"`
	Enabled         *bool    `yaml:"enabled"`
	PackagePatterns []string `yaml:"package_patterns"`
	// Action must be "deny" or "allow". An explicit "allow" terminates rule
	// evaluation early and skips all subsequent rules for this package.
	Action string `yaml:"action"`
	Reason string `yaml:"reason"`
	// MinPackageAgeDays denies versions newer than this many days. Zero disables.
	MinPackageAgeDays int `yaml:"min_package_age_days"`
	// BypassAgeFilter skips the age check for packages matched by this rule.
	BypassAgeFilter bool `yaml:"bypass_age_filter"`
	// PinnedVersions are always allowed regardless of other rule conditions.
	PinnedVersions []string `yaml:"pinned_versions"`
	// BlockPreRelease denies pre-release versions for matched packages.
	BlockPreRelease bool `yaml:"block_pre_release"`
	// BlockSnapshots denies Maven SNAPSHOT versions for matched packages.
	BlockSnapshots bool `yaml:"block_snapshots"`
	// AllowedLicenses, when non-empty, denies versions whose licence is not in the list.
	AllowedLicenses []string `yaml:"allowed_licenses"`
	// DeniedLicenses explicitly blocks versions carrying any of the listed licences.
	DeniedLicenses      []string     `yaml:"denied_licenses"`
	NamespaceProtection NamespaceCfg `yaml:"namespace_protection"`
	VelocityCheck       VelocityCfg  `yaml:"velocity_check"`
	TyposquatCheck      TyposquatCfg `yaml:"typosquat_check"`
}

// NamespaceCfg configures internal-namespace protection.
type NamespaceCfg struct {
	Enabled          bool     `yaml:"enabled"`
	InternalPatterns []string `yaml:"internal_patterns"`
}

// VelocityCfg configures velocity (rapid publishing) detection.
type VelocityCfg struct {
	Enabled             bool `yaml:"enabled"`
	MaxVersionsInWindow int  `yaml:"max_versions_in_window"`
	WindowHours         int  `yaml:"window_hours"`
	LookbackDays        int  `yaml:"lookback_days"`
}

// TyposquatCfg configures typosquatting detection.
type TyposquatCfg struct {
	Enabled            bool     `yaml:"enabled"`
	MaxLevenshteinDist int      `yaml:"max_levenshtein_dist"`
	ProtectedPackages  []string `yaml:"protected_packages"`
}

// MetricsConfig controls the /metrics endpoint.
type MetricsConfig struct {
	Enabled bool `yaml:"enabled"`
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level    string `yaml:"level"`
	Format   string `yaml:"format"`    // "text" | "json"
	FilePath string `yaml:"file_path"` // optional path to a log file on disk
}

// Defaults applies sensible defaults to any zero-value fields.
func (c *Config) Defaults() {
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeoutSeconds == 0 {
		c.Server.ReadTimeoutSeconds = 30
	}
	if c.Server.WriteTimeoutSeconds == 0 {
		c.Server.WriteTimeoutSeconds = 30
	}
	if c.Server.IdleTimeoutSeconds == 0 {
		c.Server.IdleTimeoutSeconds = 60
	}
	if c.Upstream.TimeoutSeconds == 0 {
		c.Upstream.TimeoutSeconds = 30
	}
	if c.Cache.TTLSeconds == 0 {
		c.Cache.TTLSeconds = 300
	}
	if c.Cache.MaxSizeMB == 0 {
		c.Cache.MaxSizeMB = 256
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "text"
	}
	if c.Policy.FailMode == "" {
		c.Policy.FailMode = FailModeOpen
	}
}

// Validate returns an error if the configuration is semantically invalid.
func (c *Config) Validate() error {
	if c.Upstream.URL == "" {
		return errors.New("upstream.url is required")
	}
	if !strings.HasPrefix(c.Upstream.URL, "https://") && !strings.HasPrefix(c.Upstream.URL, "http://") {
		return fmt.Errorf("upstream.url must start with http:// or https://, got %q", c.Upstream.URL)
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be 1-65535, got %d", c.Server.Port)
	}
	for _, r := range c.Policy.Rules {
		if r.Action != "deny" && r.Action != "allow" && r.Action != "" {
			return fmt.Errorf("rule %q: action must be \"deny\" or \"allow\", got %q", r.Name, r.Action)
		}
	}
	for _, vpr := range c.Policy.VersionPatterns {
		if vpr.Action != "deny" && vpr.Action != "allow" {
			return fmt.Errorf("version_pattern rule %q: action must be \"deny\" or \"allow\", got %q", vpr.Name, vpr.Action)
		}
	}
	if c.Policy.FailMode != FailModeOpen && c.Policy.FailMode != FailModeClosed {
		return fmt.Errorf("policy.fail_mode must be %q or %q, got %q", FailModeOpen, FailModeClosed, c.Policy.FailMode)
	}
	return nil
}

// Load reads a YAML config file, applies defaults, applies environment variable
// overrides, and validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	var cfg Config
	if err = yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config yaml: %w", err)
	}
	cfg.Defaults()
	applyEnvOverrides(&cfg)
	if err = cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}
	return &cfg, nil
}

// applyEnvOverrides replaces config fields with environment variable values
// when the corresponding env var is set and non-empty.
func applyEnvOverrides(cfg *Config) {
	if v := os.Getenv("PORT"); v != "" {
		var port int
		if _, err := fmt.Sscanf(v, "%d", &port); err == nil && port >= 1 && port <= 65535 {
			cfg.Server.Port = port
		}
	}
	if v := os.Getenv("PKGUARD_AUTH_TOKEN"); v != "" {
		cfg.Upstream.Token = v
	}
	if v := os.Getenv("PKGUARD_AUTH_USERNAME"); v != "" {
		cfg.Upstream.Username = v
	}
	if v := os.Getenv("PKGUARD_AUTH_PASSWORD"); v != "" {
		cfg.Upstream.Password = v
	}
}
