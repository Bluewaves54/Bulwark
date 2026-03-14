// SPDX-License-Identifier: Apache-2.0

// Package rules implements the PKGuard policy rule engine. It evaluates package
// and version requests against a set of configured PackageRules and returns a
// FilterDecision indicating whether the item should be allowed or denied.
package rules

import (
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode"

	"PKGuard/common/config"
)

// FilterDecision is the result of evaluating a package or version against the rule engine.
type FilterDecision struct {
	// Allow is true when the item should be served to the client.
	Allow bool
	// Reason is a machine-readable string describing why the item was allowed or denied.
	Reason string
	// RuleName is the name of the PackageRule that produced this decision.
	RuleName string
	// DryRun is true when the decision was deny but the policy is in dry-run mode.
	DryRun bool
}

// IsPreReleaseFunc is a function that reports whether a version string is a pre-release.
// Ecosystems can register their own implementation via NewRuleEngine.
type IsPreReleaseFunc func(version string) bool

// VersionMeta carries the metadata the rule engine needs per version.
type VersionMeta struct {
	// Version is the version string (e.g. "1.2.3", "1.0.0-beta.1").
	Version string
	// PublishedAt is the time the version was published. If zero, age filtering is skipped.
	PublishedAt time.Time
	// HasInstallScripts is true when the package declares lifecycle scripts (npm postinstall etc).
	HasInstallScripts bool
	// License is the SPDX license identifier declared for this version. Empty skips licence checks.
	License string
}

// PackageMeta carries package-level metadata.
type PackageMeta struct {
	// Name is the package name as it appears in the registry.
	Name string
	// Versions is the list of all known published versions of the package.
	// Used for velocity detection.
	Versions []VersionMeta
}

// compiledVersionPattern is a VersionPatternRule with its regex pre-compiled.
type compiledVersionPattern struct {
	rule    config.VersionPatternRule
	pattern *regexp.Regexp
}

// actionAllow is the rule action string that explicitly permits a package or version.
const actionAllow = "allow"

// RuleEngine evaluates PackageRules against package and version metadata.
type RuleEngine struct {
	rules           []config.PackageRule
	dryRun          bool
	defaults        config.RulesDefaults
	versionPatterns []compiledVersionPattern
	installScripts  config.InstallScriptsConfig
	trustedPackages []string
	isPreRelease    IsPreReleaseFunc
	logger          *slog.Logger
}

// New creates a RuleEngine from the given policy configuration using the default
// IsPreRelease function. Panics if any VersionPatternRule regex is invalid.
func New(policy config.PolicyConfig) *RuleEngine {
	e, err := NewRuleEngine(policy, IsPreRelease, slog.Default())
	if err != nil {
		panic(fmt.Sprintf("rules.New: invalid configuration: %v", err))
	}
	return e
}

// NewRuleEngine creates a RuleEngine with a custom IsPreRelease function and logger.
// Returns an error if any VersionPatternRule contains an invalid Go regex.
func NewRuleEngine(policy config.PolicyConfig, fn IsPreReleaseFunc, logger *slog.Logger) (*RuleEngine, error) {
	if fn == nil {
		fn = IsPreRelease
	}
	if logger == nil {
		logger = slog.Default()
	}
	compiled := make([]compiledVersionPattern, 0, len(policy.VersionPatterns))
	for _, vpr := range policy.VersionPatterns {
		if vpr.Enabled != nil && !*vpr.Enabled {
			continue
		}
		re, err := regexp.Compile(vpr.Match)
		if err != nil {
			return nil, fmt.Errorf("invalid regex in version_pattern rule %q: %w", vpr.Name, err)
		}
		compiled = append(compiled, compiledVersionPattern{rule: vpr, pattern: re})
	}
	return &RuleEngine{
		rules:           policy.Rules,
		dryRun:          policy.DryRun,
		defaults:        policy.Defaults,
		versionPatterns: compiled,
		installScripts:  policy.InstallScripts,
		trustedPackages: policy.TrustedPackages,
		isPreRelease:    fn,
		logger:          logger,
	}, nil
}

// IsTrustedPackage reports whether the package name matches a trusted_packages pattern.
func (e *RuleEngine) IsTrustedPackage(name string) bool {
	return len(e.trustedPackages) > 0 && matchesPackagePattern(e.trustedPackages, name)
}

// RequiresAgeFiltering reports whether the given package and version need a
// non-zero PublishedAt timestamp for age-based policy evaluation.
// Returns false for trusted packages, pinned versions, packages matched by a
// rule with bypass_age_filter or action:"allow", and when no age rules apply
// (neither rule-specific nor global defaults).
func (e *RuleEngine) RequiresAgeFiltering(pkgName, version string) bool {
	if e.IsTrustedPackage(pkgName) {
		return false
	}
	bypass, ruleAge, exempt := e.scanAgeRules(pkgName, version)
	if exempt {
		return false
	}
	if ruleAge {
		return true
	}
	return !bypass && e.defaults.MinPackageAgeDays > 0
}

// scanAgeRules walks matching rules and returns:
//   - bypassAge: a matching rule sets bypass_age_filter
//   - ruleRequiresAge: a matching rule has MinPackageAgeDays > 0 without bypass
//   - exempt: the package/version is exempt from age checks (allow or pinned)
func (e *RuleEngine) scanAgeRules(pkgName, version string) (bypassAge, ruleRequiresAge, exempt bool) {
	for i := range e.rules {
		r := &e.rules[i]
		if !isRuleEnabled(r) || !matchesPackagePattern(r.PackagePatterns, pkgName) {
			continue
		}
		if r.Action == actionAllow || isPinnedVersion(r.PinnedVersions, version) {
			return false, false, true
		}
		if r.BypassAgeFilter {
			bypassAge = true
		}
		if r.MinPackageAgeDays > 0 && !r.BypassAgeFilter {
			ruleRequiresAge = true
		}
	}
	return bypassAge, ruleRequiresAge, false
}

// isPinnedVersion returns true when version matches one of the pinned versions.
func isPinnedVersion(pinned []string, version string) bool {
	for _, p := range pinned {
		if p == version {
			return true
		}
	}
	return false
}

// EvaluatePackage evaluates a package (all versions) against the rule engine.
// It returns a FilterDecision for the package as a whole (namespace protection,
// typosquatting, explicit deny). If all rules pass, it returns Allow=true.
func (e *RuleEngine) EvaluatePackage(pkg PackageMeta) FilterDecision {
	if e.IsTrustedPackage(pkg.Name) {
		return FilterDecision{Allow: true, Reason: reasonTrusted, RuleName: "trusted_packages"}
	}
	for i := range e.rules {
		r := &e.rules[i]
		if !isRuleEnabled(r) || !matchesPackagePattern(r.PackagePatterns, pkg.Name) {
			continue
		}
		if dec, found := e.evalPackageRule(r, pkg); found {
			return dec
		}
	}
	return FilterDecision{Allow: true, Reason: "allowed"}
}

// evalPackageRule applies a single rule to a package-level check.
// Returns the decision and found=true when the rule produces a definitive result.
func (e *RuleEngine) evalPackageRule(r *config.PackageRule, pkg PackageMeta) (FilterDecision, bool) {
	if r.NamespaceProtection.Enabled {
		if isNamespaceViolation(pkg.Name, r.NamespaceProtection.InternalPatterns) {
			return e.deny(r, reasonNamespace), true
		}
	}
	if r.TyposquatCheck.Enabled && len(r.TyposquatCheck.ProtectedPackages) > 0 {
		if typosquatting(pkg.Name, r.TyposquatCheck.ProtectedPackages, r.TyposquatCheck.MaxLevenshteinDist) {
			return e.deny(r, reasonTyposquat), true
		}
	}
	if r.Action == "deny" && !hasVersionScopedChecks(r) {
		return e.deny(r, reasonExplicitDeny), true
	}
	if r.Action == actionAllow {
		return FilterDecision{Allow: true, Reason: "allowed", RuleName: r.Name}, true
	}
	return FilterDecision{}, false
}

// hasVersionScopedChecks reports whether the rule is intended to evaluate
// individual versions rather than block the whole package upfront.
// This includes package-level checks like typosquatting and namespace
// protection that only deny specific packages rather than all matches.
func hasVersionScopedChecks(r *config.PackageRule) bool {
	return r.BlockPreRelease ||
		r.BlockSnapshots ||
		r.MinPackageAgeDays > 0 ||
		r.BypassAgeFilter ||
		len(r.PinnedVersions) > 0 ||
		r.VelocityCheck.Enabled ||
		len(r.AllowedLicenses) > 0 ||
		len(r.DeniedLicenses) > 0 ||
		r.TyposquatCheck.Enabled ||
		r.NamespaceProtection.Enabled
}

// EvaluateVersion evaluates a single version against the rule engine.
// Evaluation order:
//  1. Pinned versions (any rule with a matching pinned version → always allow)
//  2. Package rules: namespace, typosquat already handled at package level; here
//     we check pre-release, snapshot, age, velocity, install scripts, licence.
//  3. Version pattern rules (regex matching on the version string).
//  4. Global defaults (applied when no earlier step produced a definitive decision).
func (e *RuleEngine) EvaluateVersion(pkg PackageMeta, ver VersionMeta) FilterDecision {
	if e.IsTrustedPackage(pkg.Name) {
		return FilterDecision{Allow: true, Reason: reasonTrusted, RuleName: "trusted_packages"}
	}
	bypassDefaultAge := e.hasBypassAgeRuleMatch(pkg.Name)
	if dec, found := e.evalPinnedVersions(pkg, ver); found {
		return dec
	}
	if dec, hardDecision := e.evalPackageRules(pkg, ver); hardDecision {
		return dec
	}
	if ver.HasInstallScripts && e.installScripts.Enabled {
		if dec := e.checkInstallScriptsEnabled(pkg.Name); dec != nil {
			return *dec
		}
	}
	if dec, found := e.evalVersionPatterns(ver); found {
		return dec
	}
	return e.evalGlobalDefaults(ver, bypassDefaultAge)
}

// hasBypassAgeRuleMatch reports whether any enabled matching PackageRule sets
// bypass_age_filter for the package.
func (e *RuleEngine) hasBypassAgeRuleMatch(pkgName string) bool {
	for i := range e.rules {
		r := &e.rules[i]
		if isRuleEnabled(r) && matchesPackagePattern(r.PackagePatterns, pkgName) && r.BypassAgeFilter {
			return true
		}
	}
	return false
}

// evalPinnedVersions checks if ver is pinned (explicitly allowed) by any rule.
func (e *RuleEngine) evalPinnedVersions(pkg PackageMeta, ver VersionMeta) (FilterDecision, bool) {
	for i := range e.rules {
		r := &e.rules[i]
		if !isRuleEnabled(r) || !matchesPackagePattern(r.PackagePatterns, pkg.Name) {
			continue
		}
		for _, pinned := range r.PinnedVersions {
			if pinned == ver.Version {
				return FilterDecision{Allow: true, Reason: reasonPinned, RuleName: r.Name}, true
			}
		}
	}
	return FilterDecision{}, false
}

// evalPackageRules walks PackageRules for version-level checks.
// Returns the decision and hardDecision=true only when the answer is definitive
// (explicit deny, or explicit allow action). When no hard decision is made, the
// caller should continue to version-pattern rules and global defaults.
func (e *RuleEngine) evalPackageRules(pkg PackageMeta, ver VersionMeta) (FilterDecision, bool) {
	for i := range e.rules {
		r := &e.rules[i]
		if !isRuleEnabled(r) || !matchesPackagePattern(r.PackagePatterns, pkg.Name) {
			continue
		}
		if r.Action == actionAllow {
			return FilterDecision{Allow: true, Reason: "allowed", RuleName: r.Name}, true
		}
		if dec, found := e.evalVersionChecks(r, pkg, ver); found {
			return dec, true
		}
	}
	return FilterDecision{}, false
}

// evalVersionChecks evaluates the version-level policy checks within a single rule.
func (e *RuleEngine) evalVersionChecks(r *config.PackageRule, pkg PackageMeta, ver VersionMeta) (FilterDecision, bool) {
	if r.BlockPreRelease && e.isPreRelease(ver.Version) {
		return e.deny(r, reasonPreRelease), true
	}
	if r.BlockSnapshots && IsSnapshot(ver.Version) {
		return e.deny(r, reasonSnapshot), true
	}
	if r.MinPackageAgeDays > 0 && !r.BypassAgeFilter && !ver.PublishedAt.IsZero() {
		cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -r.MinPackageAgeDays)
		if ver.PublishedAt.UTC().After(cutoff) {
			return e.deny(r, reasonAge), true
		}
	}
	if r.VelocityCheck.Enabled && velocityExceeded(pkg.Versions, r.VelocityCheck) {
		return e.deny(r, reasonVelocity), true
	}
	if dec, found := e.evalLicenseChecks(r, ver); found {
		return dec, true
	}
	return FilterDecision{}, false
}

// evalLicenseChecks applies allowed/denied licence list checks.
func (e *RuleEngine) evalLicenseChecks(r *config.PackageRule, ver VersionMeta) (FilterDecision, bool) {
	if len(r.AllowedLicenses) > 0 && ver.License != "" {
		if !containsString(r.AllowedLicenses, ver.License) {
			return e.deny(r, reasonLicense), true
		}
	}
	if len(r.DeniedLicenses) > 0 && ver.License != "" {
		if containsString(r.DeniedLicenses, ver.License) {
			return e.deny(r, reasonLicense), true
		}
	}
	return FilterDecision{}, false
}

// evalVersionPatterns checks regex-based version pattern rules.
func (e *RuleEngine) evalVersionPatterns(ver VersionMeta) (FilterDecision, bool) {
	for _, cvp := range e.versionPatterns {
		if !cvp.pattern.MatchString(ver.Version) {
			continue
		}
		if cvp.rule.Action == "deny" {
			if e.dryRun {
				e.logger.Warn("dry_run: would deny by version pattern",
					slog.String("rule", cvp.rule.Name),
					slog.String("version", ver.Version),
				)
				return FilterDecision{Allow: true, Reason: reasonVersionPattern, RuleName: cvp.rule.Name, DryRun: true}, true
			}
			return FilterDecision{Allow: false, Reason: reasonVersionPattern, RuleName: cvp.rule.Name}, true
		}
		// Explicit actionAllow pattern — short-circuit.
		if cvp.rule.Action == actionAllow {
			return FilterDecision{Allow: true, Reason: "allowed", RuleName: cvp.rule.Name}, true
		}
	}
	return FilterDecision{}, false
}

// evalGlobalDefaults applies global fallback checks that remain after
// rule-specific evaluation.
func (e *RuleEngine) evalGlobalDefaults(ver VersionMeta, bypassAge bool) FilterDecision {
	if e.defaults.BlockPreReleases && e.isPreRelease(ver.Version) {
		return e.denyDefault(reasonPreRelease)
	}
	if !bypassAge && e.defaults.MinPackageAgeDays > 0 && !ver.PublishedAt.IsZero() {
		cutoff := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -e.defaults.MinPackageAgeDays)
		if ver.PublishedAt.UTC().After(cutoff) {
			return e.denyDefault(reasonAge)
		}
	}
	return FilterDecision{Allow: true, Reason: "allowed"}
}

// checkInstallScriptsEnabled returns a deny decision when the install-scripts
// global check is enabled and pkg is not in the allowlist.
func (e *RuleEngine) checkInstallScriptsEnabled(pkg string) *FilterDecision {
	cfg := e.installScripts
	for _, allowed := range cfg.AllowedWithScripts {
		if strings.EqualFold(allowed, pkg) {
			return nil
		}
	}
	reason := cfg.Reason
	if reason == "" {
		reason = reasonInstallScripts
	}
	if cfg.Action == "warn" {
		e.logger.Warn("install scripts detected",
			slog.String("package", pkg),
			slog.String("action", "warn"),
		)
		return nil
	}
	if e.dryRun {
		e.logger.Warn("dry_run: would deny install scripts",
			slog.String("package", pkg),
		)
		d := FilterDecision{Allow: true, Reason: reason, RuleName: RuleInstallScripts, DryRun: true}
		return &d
	}
	d := FilterDecision{Allow: false, Reason: reason, RuleName: RuleInstallScripts}
	return &d
}

// deny builds a deny (or dry-run allow) decision for a named rule.
func (e *RuleEngine) deny(r *config.PackageRule, reason string) FilterDecision {
	if e.dryRun {
		e.logger.Warn("dry_run: would deny",
			slog.String("rule", r.Name),
			slog.String("reason", reason),
		)
		return FilterDecision{Allow: true, Reason: reason, RuleName: r.Name, DryRun: true}
	}
	return FilterDecision{Allow: false, Reason: reason, RuleName: r.Name}
}

// denyDefault builds a deny decision coming from global defaults (no named rule).
func (e *RuleEngine) denyDefault(reason string) FilterDecision {
	if e.dryRun {
		e.logger.Warn("dry_run: would deny (defaults)",
			slog.String("reason", reason),
		)
		return FilterDecision{Allow: true, Reason: reason, RuleName: "defaults", DryRun: true}
	}
	return FilterDecision{Allow: false, Reason: reason, RuleName: "defaults"}
}

// ApplyDryRun converts a deny decision into a dry-run allow when the engine is
// in dry-run mode. Callers that build their own FilterDecision outside the engine
// (e.g. in detection helpers) can use this to respect the global dry-run flag.
func (e *RuleEngine) ApplyDryRun(d FilterDecision) FilterDecision {
	if e.dryRun && !d.Allow {
		d.Allow = true
		d.DryRun = true
	}
	return d
}

// isRuleEnabled returns true when a rule should be evaluated.
// A nil Enabled pointer means enabled. An explicit false disables the rule.
func isRuleEnabled(r *config.PackageRule) bool {
	return r.Enabled == nil || *r.Enabled
}

// matchesPackagePattern returns true when the package name matches at least one
// of the patterns, or when the pattern list is empty (matches all packages).
// Patterns use simple glob-style prefix matching with a trailing wildcard (*).
func matchesPackagePattern(patterns []string, name string) bool {
	if len(patterns) == 0 {
		return true
	}
	lower := strings.ToLower(name)
	for _, p := range patterns {
		lp := strings.ToLower(p)
		if strings.HasSuffix(lp, "*") {
			if strings.HasPrefix(lower, lp[:len(lp)-1]) {
				return true
			}
		} else if lower == lp {
			return true
		}
	}
	return false
}

// isNamespaceViolation returns true when the package name matches one of the
// internal namespace patterns.
func isNamespaceViolation(name string, patterns []string) bool {
	lower := strings.ToLower(name)
	for _, p := range patterns {
		lp := strings.ToLower(p)
		if strings.HasSuffix(lp, "*") {
			if strings.HasPrefix(lower, lp[:len(lp)-1]) {
				return true
			}
		} else if lower == lp {
			return true
		}
	}
	return false
}

// IsPreRelease returns true when the version string indicates a pre-release build.
// Handles: SemVer pre-release labels (hyphen suffix), Python PEP 440 pre-release
// identifiers (a, b, rc, dev, alpha, beta, preview, c), and Maven qualifiers
// (-alpha, -beta, -rc, -milestone, -M[N]).
func IsPreRelease(version string) bool {
	v := strings.ToLower(version)

	// Maven pre-release qualifiers (snapshot handled separately by IsSnapshot).
	for _, qualifier := range []string{"-alpha", "-beta", "-rc", "-milestone", "-cr"} {
		if strings.Contains(v, qualifier) {
			return true
		}
	}
	// Maven -M[digits] milestone pattern.
	if idx := strings.Index(v, "-m"); idx >= 0 {
		rest := v[idx+2:]
		if len(rest) > 0 && unicode.IsDigit(rune(rest[0])) {
			return true
		}
	}

	// SemVer: any hyphen label is a pre-release (except -SNAPSHOT which IsSnapshot handles).
	if idx := strings.Index(v, "-"); idx >= 0 {
		label := v[idx+1:]
		if label == "snapshot" {
			return false
		}
		if len(label) > 0 {
			return true
		}
	}

	// PEP 440 pre-release identifiers attached to version (no hyphen).
	for _, suffix := range []string{"a", "b", "rc", "alpha", "beta", "dev", "preview", ".post"} {
		if containsPEP440Suffix(v, suffix) {
			return true
		}
	}

	return false
}

// containsPEP440Suffix returns true when the version contains a PEP 440 pre-release
// suffix that follows a digit. E.g. "1.0a1", "2.0.0b3", "3.0rc1".
func containsPEP440Suffix(version, suffix string) bool {
	idx := strings.Index(version, suffix)
	if idx < 0 || idx == 0 {
		return false
	}
	return unicode.IsDigit(rune(version[idx-1]))
}

// IsSnapshot returns true when the version is a Maven SNAPSHOT.
func IsSnapshot(version string) bool {
	return strings.HasSuffix(strings.ToUpper(version), "-SNAPSHOT")
}

// velocityExceeded returns true when the velocity rule fires: more than MaxVersionsInWindow
// versions were published within any WindowHours-hour sliding window, looking back LookbackDays days.
func velocityExceeded(versions []VersionMeta, cfg config.VelocityCfg) bool {
	if cfg.MaxVersionsInWindow <= 0 || cfg.WindowHours <= 0 {
		return false
	}
	cutoff := time.Now().AddDate(0, 0, -cfg.LookbackDays)
	window := time.Duration(cfg.WindowHours) * time.Hour

	recent := make([]time.Time, 0, len(versions))
	for _, v := range versions {
		if !v.PublishedAt.IsZero() && v.PublishedAt.After(cutoff) {
			recent = append(recent, v.PublishedAt)
		}
	}
	if len(recent) == 0 {
		return false
	}

	for i, t := range recent {
		count := 0
		for _, u := range recent[i:] {
			if u.Sub(t) <= window {
				count++
			}
		}
		if count > cfg.MaxVersionsInWindow {
			return true
		}
	}
	return false
}

// containsString reports whether slice contains s (case-sensitive).
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
