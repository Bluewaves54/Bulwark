// SPDX-License-Identifier: Apache-2.0

package rules_test

import (
	"testing"
	"time"

	"PKGuard/common/config"
	"PKGuard/common/rules"
)

const (
	testPkgRequests  = "requests"
	testPkgRequestss = "requestss"
	testPkgReqvests  = "reqvests"
	ruleDefault      = "default"
)

func boolPtr(b bool) *bool { return &b }

func TestEvaluatePackageAllowedDefault(t *testing.T) {
	engine := rules.New(config.PolicyConfig{})
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: testPkgRequests})
	if !dec.Allow {
		t.Errorf("empty rule set should allow all, got deny reason=%q", dec.Reason)
	}
}

func TestEvaluatePackageExplicitDeny(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, Action: "deny", PackagePatterns: []string{"banned"}},
	}}
	engine := rules.New(policy)
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "banned"})
	if dec.Allow {
		t.Error("explicit deny rule should deny package")
	}
	if dec.Reason != "explicit_deny" {
		t.Errorf("reason: want explicit_deny, got %q", dec.Reason)
	}
}

func TestEvaluatePackageNamespaceViolation(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, NamespaceProtection: config.NamespaceCfg{
			Enabled:          true,
			InternalPatterns: []string{"myco-*"},
		}},
	}}
	engine := rules.New(policy)

	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "myco-utils"})
	if dec.Allow {
		t.Error("namespace violation should be denied")
	}
	if dec.Reason != "namespace_violation" {
		t.Errorf("reason: want namespace_violation, got %q", dec.Reason)
	}

	dec2 := engine.EvaluatePackage(rules.PackageMeta{Name: "requests"})
	if !dec2.Allow {
		t.Error("non-matching package should be allowed")
	}
}

func TestEvaluatePackageTyposquat(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, TyposquatCheck: config.TyposquatCfg{
			Enabled:            true,
			MaxLevenshteinDist: 2,
			ProtectedPackages:  []string{testPkgRequests},
		}},
	}}
	engine := rules.New(policy)

	dec := engine.EvaluatePackage(rules.PackageMeta{Name: testPkgReqvests})
	if dec.Allow {
		t.Errorf("reqvests is 2 edits from requests, should be denied as typosquat")
	}

	decExact := engine.EvaluatePackage(rules.PackageMeta{Name: testPkgRequests})
	if !decExact.Allow {
		t.Error("exact match should not be flagged as typosquat")
	}

	decFar := engine.EvaluatePackage(rules.PackageMeta{Name: "completely-different"})
	if !decFar.Allow {
		t.Error("far-away name should not be flagged as typosquat")
	}
}

func TestEvaluateVersionPreRelease(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, BlockPreRelease: true},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: testPkgRequests}

	cases := []struct {
		version string
		deny    bool
	}{
		{"1.0.0", false},
		{"1.0.0-beta.1", true},
		{"2.0.0-rc.1", true},
		{"3.0.0-alpha", true},
		{"2.28.0a1", true},
		{"1.1.0b2", true},
	}
	for _, tc := range cases {
		dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: tc.version})
		if dec.Allow == tc.deny {
			t.Errorf("version %q: allow=%v, want deny=%v", tc.version, dec.Allow, tc.deny)
		}
	}
}

func TestEvaluateVersionSnapshot(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, BlockSnapshots: true},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "mylib"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0-SNAPSHOT"})
	if dec.Allow {
		t.Error("SNAPSHOT should be denied")
	}
	if dec.Reason != "snapshot" {
		t.Errorf("reason: want snapshot, got %q", dec.Reason)
	}

	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0"})
	if !dec2.Allow {
		t.Error("non-snapshot should be allowed")
	}
}

func TestEvaluateVersionDefaultsApplyWithMatchingNonDecisiveRule(t *testing.T) {
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		Rules: []config.PackageRule{
			{Name: "block-snapshots", PackagePatterns: []string{"*"}, BlockSnapshots: true},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "software.amazon.awssdk:s3"}

	youngVer := rules.VersionMeta{Version: "2.42.11", PublishedAt: time.Now().Add(-24 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, youngVer)
	if dec.Allow {
		t.Error("young version should be denied by defaults age rule")
	}
	if dec.Reason != "age" {
		t.Errorf("reason: want age, got %q", dec.Reason)
	}
}

func TestEvaluateVersionBypassAgeRuleSkipsDefaultAge(t *testing.T) {
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 10000},
		Rules: []config.PackageRule{
			{Name: "bypass-age-for-ms", PackagePatterns: []string{"ms"}, BypassAgeFilter: true},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "ms"}

	ver := rules.VersionMeta{Version: "2.1.3", PublishedAt: time.Now().AddDate(0, 0, -1)}
	dec := engine.EvaluateVersion(pkg, ver)
	if !dec.Allow {
		t.Errorf("bypass_age_filter should skip default age deny, got deny reason=%q", dec.Reason)
	}
}

func TestEvaluateVersionAge(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, MinPackageAgeDays: 7},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: testPkgRequests}

	youngVer := rules.VersionMeta{Version: "99.0.0", PublishedAt: time.Now().Add(-12 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, youngVer)
	if dec.Allow {
		t.Error("version too young should be denied")
	}
	if dec.Reason != "age" {
		t.Errorf("reason: want age, got %q", dec.Reason)
	}

	oldVer := rules.VersionMeta{Version: "1.0.0", PublishedAt: time.Now().AddDate(0, 0, -30)}
	dec2 := engine.EvaluateVersion(pkg, oldVer)
	if !dec2.Allow {
		t.Error("old version should be allowed")
	}

	// Zero time should skip age filter (fail-open).
	zeroVer := rules.VersionMeta{Version: "1.0.0"}
	dec3 := engine.EvaluateVersion(pkg, zeroVer)
	if !dec3.Allow {
		t.Error("zero PublishedAt should skip age filter and allow")
	}
}

func TestEvaluateVersionVelocity(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, VelocityCheck: config.VelocityCfg{
			Enabled:             true,
			MaxVersionsInWindow: 3,
			WindowHours:         1,
			LookbackDays:        7,
		}},
	}}
	engine := rules.New(policy)

	now := time.Now()
	pkg := rules.PackageMeta{
		Name: "fast-pkg",
		Versions: []rules.VersionMeta{
			{Version: "1.0.0", PublishedAt: now.Add(-30 * time.Minute)},
			{Version: "1.0.1", PublishedAt: now.Add(-25 * time.Minute)},
			{Version: "1.0.2", PublishedAt: now.Add(-20 * time.Minute)},
			{Version: "1.0.3", PublishedAt: now.Add(-15 * time.Minute)},
		},
	}
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.3"})
	if dec.Allow {
		t.Error("velocity exceeded should deny")
	}
	if dec.Reason != "velocity" {
		t.Errorf("reason: want velocity, got %q", dec.Reason)
	}

	// Slow package — should be allowed.
	slowPkg := rules.PackageMeta{
		Name: "slow-pkg",
		Versions: []rules.VersionMeta{
			{Version: "1.0.0", PublishedAt: now.AddDate(0, 0, -30)},
			{Version: "1.1.0", PublishedAt: now.AddDate(0, 0, -20)},
		},
	}
	dec2 := engine.EvaluateVersion(slowPkg, rules.VersionMeta{Version: "1.1.0"})
	if !dec2.Allow {
		t.Error("slow package should be allowed")
	}
}

func TestDryRunMode(t *testing.T) {
	policy := config.PolicyConfig{
		DryRun: true,
		Rules: []config.PackageRule{
			{Name: ruleDefault, BlockPreRelease: true},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: testPkgRequests}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0-beta"})
	if !dec.Allow {
		t.Error("dry-run mode should allow items that would otherwise be denied")
	}
	if !dec.DryRun {
		t.Error("dry-run mode should set DryRun=true on the decision")
	}
}

func TestRuleDisabled(t *testing.T) {
	disabled := boolPtr(false)
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, Enabled: disabled, Action: "deny", PackagePatterns: []string{"banned"}},
	}}
	engine := rules.New(policy)

	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "banned"})
	if !dec.Allow {
		t.Error("disabled rule should not apply")
	}
}

func TestPatternWildcard(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: ruleDefault, Action: "deny", PackagePatterns: []string{"myco-*"}},
	}}
	engine := rules.New(policy)

	cases := []struct {
		name  string
		allow bool
	}{
		{"myco-utils", false},
		{"myco-core", false},
		{"requests", true},
		{"myco", true}, // prefix match requires at least one char after
	}
	for _, tc := range cases {
		dec := engine.EvaluatePackage(rules.PackageMeta{Name: tc.name})
		if dec.Allow != tc.allow {
			t.Errorf("package %q: allow=%v, want %v", tc.name, dec.Allow, tc.allow)
		}
	}
}

func TestIsPreRelease(t *testing.T) {
	cases := []struct {
		version    string
		prerelease bool
	}{
		{"1.0.0", false},
		{"1.0.0-beta.1", true},
		{"1.0.0-rc.3", true},
		{"2.28.0a1", true},
		{"3.0b2", true},
		{"1.0.0-SNAPSHOT", false}, // handled by IsSnapshot
		{"1.0.0-alpha", true},
		{"1.0.0-milestone.1", true},
		{"1.0-M1", true},
		{"1.0-M10", true},
		{"4.0.0", false},
	}
	for _, tc := range cases {
		got := rules.IsPreRelease(tc.version)
		if got != tc.prerelease {
			t.Errorf("IsPreRelease(%q): want %v, got %v", tc.version, tc.prerelease, got)
		}
	}
}

func TestIsSnapshot(t *testing.T) {
	if !rules.IsSnapshot("1.0.0-SNAPSHOT") {
		t.Error("1.0.0-SNAPSHOT should be snapshot")
	}
	if rules.IsSnapshot("1.0.0") {
		t.Error("1.0.0 should not be snapshot")
	}
	if rules.IsSnapshot("1.0.0-beta") {
		t.Error("1.0.0-beta should not be snapshot")
	}
}

// ─── RequiresAgeFiltering ────────────────────────────────────────────────────

func TestRequiresAgeFilteringGlobalDefaults(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
	})
	if !engine.RequiresAgeFiltering("lodash", "4.17.21") {
		t.Error("expected true when global defaults have MinPackageAgeDays>0")
	}
}

func TestRequiresAgeFilteringNoAge(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 0},
	})
	if engine.RequiresAgeFiltering("lodash", "4.17.21") {
		t.Error("expected false when no age rules configured")
	}
}

func TestRequiresAgeFilteringTrustedPackage(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		TrustedPackages: []string{"@types/*", "setuptools"},
		Defaults:        config.RulesDefaults{MinPackageAgeDays: 7},
	})
	if engine.RequiresAgeFiltering("setuptools", "69.0.0") {
		t.Error("trusted packages should not require age filtering")
	}
	if engine.RequiresAgeFiltering("@types/node", "20.0.0") {
		t.Error("trusted wildcard pattern should not require age filtering")
	}
}

func TestRequiresAgeFilteringPinnedVersion(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		Rules: []config.PackageRule{
			{Name: "pin-lodash", PackagePatterns: []string{"lodash"}, PinnedVersions: []string{"4.17.21"}, MinPackageAgeDays: 7},
		},
	})
	if engine.RequiresAgeFiltering("lodash", "4.17.21") {
		t.Error("pinned version should not require age filtering")
	}
	if !engine.RequiresAgeFiltering("lodash", "4.18.0") {
		t.Error("non-pinned version should require age filtering")
	}
}

func TestRequiresAgeFilteringBypassAgeFilter(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		Rules: []config.PackageRule{
			{Name: "bypass-ms", PackagePatterns: []string{"ms"}, BypassAgeFilter: true},
		},
	})
	if engine.RequiresAgeFiltering("ms", "2.1.3") {
		t.Error("bypass_age_filter should skip age filtering")
	}
	if !engine.RequiresAgeFiltering("lodash", "4.17.21") {
		t.Error("non-bypassed package should still require age filtering")
	}
}

func TestRequiresAgeFilteringExplicitAllow(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
		Rules: []config.PackageRule{
			{Name: "allow-internal", PackagePatterns: []string{"internal-*"}, Action: "allow"},
		},
	})
	if engine.RequiresAgeFiltering("internal-utils", "1.0.0") {
		t.Error("explicit allow rule should not require age filtering")
	}
}

func TestRequiresAgeFilteringPackageRuleAge(t *testing.T) {
	engine := rules.New(config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 0},
		Rules: []config.PackageRule{
			{Name: "age-check", PackagePatterns: []string{"lodash"}, MinPackageAgeDays: 14},
		},
	})
	if !engine.RequiresAgeFiltering("lodash", "4.17.21") {
		t.Error("package-specific age rule should require age filtering")
	}
	if engine.RequiresAgeFiltering("express", "4.0.0") {
		t.Error("unmatched package with no global defaults should not require age filtering")
	}
}
