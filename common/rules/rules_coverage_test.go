// SPDX-License-Identifier: Apache-2.0

package rules_test

import (
	"fmt"
	"testing"
	"time"

	"Bulwark/common/config"
	"Bulwark/common/rules"
)

// ─── helpers ────────────────────────────────────────────────────────────────

func customPreRelease(v string) bool { return v == "custom-pre" }

// ─── NewRuleEngine ───────────────────────────────────────────────────────────

func TestNewRuleEngineInvalidRegex(t *testing.T) {
	policy := config.PolicyConfig{
		VersionPatterns: []config.VersionPatternRule{
			{Name: "bad", Match: "[invalid(", Action: "deny"},
		},
	}
	_, err := rules.NewRuleEngine(policy, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid regex in version_pattern rule")
	}
}

func TestNewRuleEngineNilFnAndLogger(t *testing.T) {
	policy := config.PolicyConfig{}
	e, err := rules.NewRuleEngine(policy, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if e == nil {
		t.Fatal("expected non-nil engine")
	}
}

func TestNewRuleEngineCustomPreRelease(t *testing.T) {
	policy := config.PolicyConfig{
		Rules: []config.PackageRule{
			{Name: "block-pre", BlockPreRelease: true},
		},
	}
	e, err := rules.NewRuleEngine(policy, customPreRelease, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "custom-pre" should be recognised as pre-release by the injected func.
	dec := e.EvaluateVersion(rules.PackageMeta{Name: "pkg"}, rules.VersionMeta{Version: "custom-pre"})
	if dec.Allow {
		t.Error("custom pre-release function should have denied the version")
	}
	// Regular semver should not be denied by the custom func.
	dec2 := e.EvaluateVersion(rules.PackageMeta{Name: "pkg"}, rules.VersionMeta{Version: "1.0.0"})
	if !dec2.Allow {
		t.Error("non-pre-release should be allowed by custom function")
	}
}

// ─── PinnedVersions ─────────────────────────────────────────────────────────

func TestEvaluateVersionPinnedAllowsPreRelease(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{
			Name:            "block-pre-with-pin",
			BlockPreRelease: true,
			PinnedVersions:  []string{"1.0.0-beta.1"},
		},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "mypkg"}

	// Pinned pre-release must be allowed despite the block_pre_release flag.
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0-beta.1"})
	if !dec.Allow {
		t.Error("pinned version should be allowed even when block_pre_release is true")
	}
	if dec.Reason != "pinned_version" {
		t.Errorf("reason: want pinned_version, got %q", dec.Reason)
	}

	// Non-pinned pre-release should still be denied.
	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0-beta.2"})
	if dec2.Allow {
		t.Error("non-pinned pre-release should be denied")
	}
}

func TestEvaluateVersionPinnedAllowsAgeDenied(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{
			Name:              "age-rule",
			MinPackageAgeDays: 30,
			PinnedVersions:    []string{"2.0.0"},
		},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "mypkg"}

	young := rules.VersionMeta{Version: "2.0.0", PublishedAt: time.Now().Add(-1 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, young)
	if !dec.Allow {
		t.Error("pinned version should be allowed regardless of age")
	}
}

// ─── BypassAgeFilter ─────────────────────────────────────────────────────────

func TestEvaluateVersionBypassAgeFilter(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{
			Name:              "age-bypass",
			MinPackageAgeDays: 30,
			BypassAgeFilter:   true,
		},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "critical-tool"}

	young := rules.VersionMeta{Version: "1.0.0", PublishedAt: time.Now().Add(-1 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, young)
	if !dec.Allow {
		t.Error("BypassAgeFilter=true should allow young versions")
	}
}

func TestEvaluateVersionNoBypassAgeFilter(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{
			Name:              "age-rule",
			MinPackageAgeDays: 30,
			BypassAgeFilter:   false,
		},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "mypkg"}

	young := rules.VersionMeta{Version: "1.0.0", PublishedAt: time.Now().Add(-1 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, young)
	if dec.Allow {
		t.Error("BypassAgeFilter=false should still enforce age check")
	}
}

// ─── VersionPatternRule ──────────────────────────────────────────────────────

func TestVersionPatternDenyCanary(t *testing.T) {
	policy := config.PolicyConfig{
		VersionPatterns: []config.VersionPatternRule{
			{Name: "no-canary", Match: `.*-canary.*`, Action: "deny", Reason: "no canary builds"},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "react"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "18.0.0-canary.1"})
	if dec.Allow {
		t.Error("canary version should be denied by version pattern rule")
	}
	if dec.Reason != "version_pattern" {
		t.Errorf("reason: want version_pattern, got %q", dec.Reason)
	}

	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "18.0.0"})
	if !dec2.Allow {
		t.Error("stable version should not be denied by canary pattern")
	}
}

func TestVersionPatternAllowOverride(t *testing.T) {
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{BlockPreReleases: true},
		VersionPatterns: []config.VersionPatternRule{
			{Name: "allow-rc", Match: `.*-rc\.\d+$`, Action: "allow"},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "react"}

	// RC version: version pattern "allow" short-circuits the global deny.
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "18.0.0-rc.1"})
	if !dec.Allow {
		t.Error("version pattern allow should override global defaults")
	}
}

func TestVersionPatternDisabled(t *testing.T) {
	disabled := boolPtr(false)
	policy := config.PolicyConfig{
		VersionPatterns: []config.VersionPatternRule{
			{Name: "disabled-pattern", Match: `.*`, Action: "deny", Enabled: disabled},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluateVersion(rules.PackageMeta{Name: "pkg"}, rules.VersionMeta{Version: "1.0.0"})
	if !dec.Allow {
		t.Error("disabled version pattern should not apply")
	}
}

func TestVersionPatternDryRun(t *testing.T) {
	policy := config.PolicyConfig{
		DryRun: true,
		VersionPatterns: []config.VersionPatternRule{
			{Name: "no-canary", Match: `.*-canary.*`, Action: "deny"},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluateVersion(rules.PackageMeta{Name: "react"}, rules.VersionMeta{Version: "18.0.0-canary.1"})
	if !dec.Allow {
		t.Error("dry-run: version pattern deny should allow through")
	}
	if !dec.DryRun {
		t.Error("dry-run: DryRun flag should be set")
	}
}

// ─── Global Defaults ─────────────────────────────────────────────────────────

func TestGlobalDefaultsBlockPreRelease(t *testing.T) {
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{BlockPreReleases: true},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "some-pkg"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0-beta.1"})
	if dec.Allow {
		t.Error("global default block_pre_releases should deny pre-release")
	}
	if dec.RuleName != "defaults" {
		t.Errorf("rule name: want defaults, got %q", dec.RuleName)
	}

	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0"})
	if !dec2.Allow {
		t.Error("global default should allow stable versions")
	}
}

func TestGlobalDefaultsMinAge(t *testing.T) {
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{MinPackageAgeDays: 7},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "new-pkg"}

	young := rules.VersionMeta{Version: "1.0.0", PublishedAt: time.Now().Add(-2 * time.Hour)}
	dec := engine.EvaluateVersion(pkg, young)
	if dec.Allow {
		t.Error("global default min age should deny young versions")
	}

	old := rules.VersionMeta{Version: "0.9.0", PublishedAt: time.Now().AddDate(0, 0, -30)}
	dec2 := engine.EvaluateVersion(pkg, old)
	if !dec2.Allow {
		t.Error("global default should allow old versions")
	}
}

func TestGlobalDefaultsNotAppliedWhenRuleMatches(t *testing.T) {
	// Specific package rule disables blocking; global default should NOT kick in.
	policy := config.PolicyConfig{
		Defaults: config.RulesDefaults{BlockPreReleases: true},
		Rules: []config.PackageRule{
			{Name: "allow-all", PackagePatterns: []string{"myco-*"}, Action: "allow"},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "myco-internal"},
		rules.VersionMeta{Version: "1.0.0-beta"},
	)
	if !dec.Allow {
		t.Error("explicit allow rule should suppress global defaults")
	}
}

func TestGlobalDefaultsDryRun(t *testing.T) {
	policy := config.PolicyConfig{
		DryRun:   true,
		Defaults: config.RulesDefaults{BlockPreReleases: true},
	}
	engine := rules.New(policy)
	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "pkg"},
		rules.VersionMeta{Version: "1.0.0-alpha"},
	)
	if !dec.Allow {
		t.Error("dry-run: global defaults deny should allow through")
	}
	if !dec.DryRun {
		t.Error("dry-run: DryRun flag should be set on decision")
	}
}

// ─── Explicit Allow Rule ─────────────────────────────────────────────────────

func TestEvaluatePackageExplicitAllow(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "allow-internal", PackagePatterns: []string{"myco-*"}, Action: "allow"},
		{Name: "deny-all", Action: "deny"},
	}}
	engine := rules.New(policy)

	// The "allow" rule matches and terminates evaluation before the "deny-all" rule.
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "myco-service"})
	if !dec.Allow {
		t.Error("explicit allow rule should win")
	}

	// Non-matching package hits deny-all.
	dec2 := engine.EvaluatePackage(rules.PackageMeta{Name: "external-pkg"})
	if dec2.Allow {
		t.Error("deny-all rule should apply to non-matching packages")
	}
}

func TestEvaluateVersionExplicitAllow(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{
			Name:            "allow-internal",
			PackagePatterns: []string{"myco-*"},
			Action:          "allow",
			BlockPreRelease: true, // should be ignored when action=allow
		},
	}}
	engine := rules.New(policy)

	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "myco-service"},
		rules.VersionMeta{Version: "1.0.0-beta.1"},
	)
	if !dec.Allow {
		t.Error("explicit allow should override BlockPreRelease check")
	}
}

// ─── License Checking ───────────────────────────────────────────────────────

func TestLicenseAllowedList(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "oss-only", AllowedLicenses: []string{"MIT", "Apache-2.0", "BSD-3-Clause"}},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "some-pkg"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: "GPL-3.0"})
	if dec.Allow {
		t.Error("GPL-3.0 should be denied when not in allowed_licenses")
	}
	if dec.Reason != "license" {
		t.Errorf("reason: want license, got %q", dec.Reason)
	}

	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: "MIT"})
	if !dec2.Allow {
		t.Error("MIT should be allowed")
	}
}

func TestLicenseDeniedList(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "no-gpl", DeniedLicenses: []string{"GPL-2.0", "GPL-3.0", "AGPL-3.0"}},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "some-pkg"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: "GPL-3.0"})
	if dec.Allow {
		t.Error("GPL-3.0 in denied_licenses should be denied")
	}

	dec2 := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: "MIT"})
	if !dec2.Allow {
		t.Error("MIT not in denied_licenses should be allowed")
	}
}

func TestLicenseEmptySkipsCheck(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "oss-only", AllowedLicenses: []string{"MIT"}},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "some-pkg"}

	// Empty license → skip check (fail open).
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: ""})
	if !dec.Allow {
		t.Error("empty license should skip license check (fail open)")
	}
}

// ─── Install Scripts ─────────────────────────────────────────────────────────

func TestInstallScriptsDenyByHasInstallScripts(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
			Reason:  "no install hooks",
		},
		Rules: []config.PackageRule{
			{Name: "global"},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "sketchy-pkg"}

	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true})
	if dec.Allow {
		t.Error("package with install scripts should be denied")
	}
	if dec.Reason != "no install hooks" {
		t.Errorf("reason: want %q, got %q", "no install hooks", dec.Reason)
	}
}

func TestInstallScriptsAllowlistBypasses(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled:            true,
			Action:             "deny",
			AllowedWithScripts: []string{"@scoped/legit-tool"},
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)

	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "@scoped/legit-tool"},
		rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true},
	)
	if !dec.Allow {
		t.Error("allowlisted package should bypass install script check")
	}
}

func TestInstallScriptsWarnAction(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "warn",
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)

	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "tool"},
		rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true},
	)
	if !dec.Allow {
		t.Error("warn action should allow the package through")
	}
}

func TestInstallScriptsDisabledNoEffect(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled: false,
			Action:  "deny",
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)

	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "tool"},
		rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true},
	)
	if !dec.Allow {
		t.Error("disabled install_scripts check should have no effect")
	}
}

func TestInstallScriptsDryRun(t *testing.T) {
	policy := config.PolicyConfig{
		DryRun: true,
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)

	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "tool"},
		rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true},
	)
	if !dec.Allow {
		t.Error("dry-run: install scripts deny should allow through")
	}
	if !dec.DryRun {
		t.Error("dry-run flag should be set")
	}
}

// ─── CheckInstallScripts (method) ────────────────────────────────────────────

func TestCheckInstallScriptsMethod(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled:            true,
			Action:             "deny",
			AllowedWithScripts: []string{"safe-pkg"},
		},
	}
	engine := rules.New(policy)

	scripts := map[string]string{"postinstall": "node setup.js"}

	// Unallowlisted package with postinstall → deny.
	dec := engine.CheckInstallScripts("evil-pkg", scripts)
	if dec == nil || dec.Allow {
		t.Error("evil-pkg with postinstall should be denied")
	}

	// Allowlisted package → nil (pass).
	dec2 := engine.CheckInstallScripts("safe-pkg", scripts)
	if dec2 != nil {
		t.Error("allowlisted package should return nil")
	}

	// No dangerous scripts → nil.
	dec3 := engine.CheckInstallScripts("other-pkg", map[string]string{"test": "jest"})
	if dec3 != nil {
		t.Error("package without lifecycle scripts should return nil")
	}

	// Install scripts check disabled → nil.
	engineDisabled := rules.New(config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{Enabled: false},
	})
	dec4 := engineDisabled.CheckInstallScripts("any-pkg", scripts)
	if dec4 != nil {
		t.Error("disabled check should return nil")
	}
}

func TestCheckInstallScriptsWarnMethod(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{Enabled: true, Action: "warn"},
	}
	engine := rules.New(policy)
	dec := engine.CheckInstallScripts("pkg", map[string]string{"postinstall": "x"})
	if dec != nil {
		t.Error("warn action should return nil (allow)")
	}
}

func TestCheckInstallScriptsDryRunMethod(t *testing.T) {
	policy := config.PolicyConfig{
		DryRun:         true,
		InstallScripts: config.InstallScriptsConfig{Enabled: true, Action: "deny"},
	}
	engine := rules.New(policy)
	dec := engine.CheckInstallScripts("evil", map[string]string{"postinstall": "x"})
	if dec == nil {
		t.Fatal("expected non-nil decision")
	}
	if !dec.Allow {
		t.Error("dry-run CheckInstallScripts should return Allow=true")
	}
	if !dec.DryRun {
		t.Error("dry-run flag should be set")
	}
}

// ─── CheckMetadataAnomalies ──────────────────────────────────────────────────

func TestCheckMetadataAnomaliesAllPresent(t *testing.T) {
	meta := map[string]interface{}{
		"repository":  "https://github.com/example/pkg",
		"license":     "MIT",
		"description": "A useful library",
	}
	anomalies := rules.CheckMetadataAnomalies(meta)
	if len(anomalies) != 0 {
		t.Errorf("expected no anomalies, got %d: %v", len(anomalies), anomalies)
	}
}

func TestCheckMetadataAnomaliesMissingAll(t *testing.T) {
	anomalies := rules.CheckMetadataAnomalies(map[string]interface{}{})
	if len(anomalies) != 3 {
		t.Errorf("expected 3 anomalies, got %d", len(anomalies))
	}
	checksFound := make(map[string]bool)
	for _, a := range anomalies {
		checksFound[a.Check] = true
	}
	for _, expected := range []string{"missing_repository", "missing_license", "empty_description"} {
		if !checksFound[expected] {
			t.Errorf("expected anomaly %q not found", expected)
		}
	}
}

func TestCheckMetadataAnomaliesEmptyStrings(t *testing.T) {
	meta := map[string]interface{}{
		"repository":  "  ",
		"license":     "",
		"description": nil,
	}
	anomalies := rules.CheckMetadataAnomalies(meta)
	if len(anomalies) != 3 {
		t.Errorf("expected 3 anomalies for blank/nil fields, got %d", len(anomalies))
	}
}

// ─── ApplyDryRun ─────────────────────────────────────────────────────────────

func TestApplyDryRunConvertsDeny(t *testing.T) {
	engine := rules.New(config.PolicyConfig{DryRun: true})
	deny := rules.FilterDecision{Allow: false, Reason: "age", RuleName: "r"}
	got := engine.ApplyDryRun(deny)
	if !got.Allow {
		t.Error("ApplyDryRun should set Allow=true in dry-run mode")
	}
	if !got.DryRun {
		t.Error("ApplyDryRun should set DryRun=true")
	}
}

func TestApplyDryRunNoEffectInEnforceMode(t *testing.T) {
	engine := rules.New(config.PolicyConfig{DryRun: false})
	deny := rules.FilterDecision{Allow: false, Reason: "age"}
	got := engine.ApplyDryRun(deny)
	if got.Allow {
		t.Error("ApplyDryRun should not change Allow in enforce mode")
	}
}

func TestApplyDryRunDoesNotChangeAllow(t *testing.T) {
	engine := rules.New(config.PolicyConfig{DryRun: true})
	allow := rules.FilterDecision{Allow: true, Reason: "allowed"}
	got := engine.ApplyDryRun(allow)
	if !got.Allow {
		t.Error("ApplyDryRun should not change an already-allowed decision")
	}
	if got.DryRun {
		t.Error("ApplyDryRun should not set DryRun on an already-allowed decision")
	}
}

// ─── NormalizeName & LevenshteinDistance ────────────────────────────────────

func TestNormalizeName(t *testing.T) {
	cases := []struct{ input, want string }{
		{"my-package", "mypackage"},
		{"my_package", "mypackage"},
		{"my.package", "mypackage"},
		{"My-Package", "mypackage"},
		{"requests", "requests"},
	}
	for _, tc := range cases {
		got := rules.NormalizeName(tc.input)
		if got != tc.want {
			t.Errorf("NormalizeName(%q): want %q, got %q", tc.input, tc.want, got)
		}
	}
}

func TestLevenshteinDistance(t *testing.T) {
	cases := []struct {
		a, b    string
		maxDist int
		want    int
	}{
		{"kitten", "sitting", 10, 3},
		{"requests", "requests", 2, 0},
		{"reqvests", "requests", 2, 1},
		{"abc", "xyz", 1, 2}, // early exit: returns maxDist+1 = 2
	}
	for _, tc := range cases {
		got := rules.LevenshteinDistance(tc.a, tc.b, tc.maxDist)
		if got != tc.want {
			t.Errorf("LevenshteinDistance(%q,%q,max=%d): want %d, got %d",
				tc.a, tc.b, tc.maxDist, tc.want, got)
		}
	}
}

// ─── Age cutoff uses midnight truncation ────────────────────────────────────

func TestAgeFilterCutoffDayPrecise(t *testing.T) {
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "age-rule", MinPackageAgeDays: 7},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "pkg"}

	// Published exactly 8 days ago: always allowed.
	old := rules.VersionMeta{
		Version:     "1.0.0",
		PublishedAt: time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -8),
	}
	dec := engine.EvaluateVersion(pkg, old)
	if !dec.Allow {
		t.Error("version published 8 days ago should be allowed with 7-day rule")
	}

	// Published exactly 6 days ago: should be denied.
	recent := rules.VersionMeta{
		Version:     "1.0.1",
		PublishedAt: time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -6),
	}
	dec2 := engine.EvaluateVersion(pkg, recent)
	if dec2.Allow {
		t.Error("version published 6 days ago should be denied with 7-day rule")
	}
}

// ─── Config VersionPatternRule validation ───────────────────────────────────

func TestConfigValidatesVersionPatternAction(t *testing.T) {
	policy := config.PolicyConfig{
		VersionPatterns: []config.VersionPatternRule{
			{Name: "bad", Match: ".*", Action: "invalid"},
		},
	}
	cfg := &config.Config{
		Upstream: config.UpstreamConfig{URL: "https://example.com"},
		Server:   config.ServerConfig{Port: 8080},
		Policy:   policy,
	}
	cfg.Defaults()
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid version_pattern action")
	}
}

// ─── containsString (via license checking) ──────────────────────────────────

func TestContainsStringViaLicense(t *testing.T) {
	// Tests the containsString helper indirectly through license evaluation.
	policy := config.PolicyConfig{Rules: []config.PackageRule{
		{Name: "r", AllowedLicenses: []string{"MIT", "Apache-2.0"}},
	}}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "p"}

	for _, lic := range []string{"MIT", "Apache-2.0"} {
		dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: lic})
		if !dec.Allow {
			t.Errorf("license %q should be allowed", lic)
		}
	}
	for _, lic := range []string{"GPL-3.0", "LGPL-2.1"} {
		dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "1.0.0", License: lic})
		if dec.Allow {
			t.Errorf("license %q should be denied", lic)
		}
	}
}

// ─── Compile-time: all exported identifiers reachable ────────────────────────

// TestExportedConstants ensures the detection constants compile.
func TestExportedConstants(t *testing.T) {
	constants := []string{
		rules.RuleNamespaceProtection,
		rules.RuleTyposquatting,
		rules.RuleVelocityDetection,
		rules.RuleInstallScripts,
	}
	for _, c := range constants {
		if c == "" {
			t.Errorf("expected non-empty constant, got empty string")
		}
	}
}

// TestInstallScriptDefaultReason verifies that an empty Reason falls back to the
// internal constant (exercises the default-reason branch).
func TestInstallScriptDefaultReason(t *testing.T) {
	policy := config.PolicyConfig{
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
			Reason:  "", // intentionally empty — should use built-in default
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)
	dec := engine.EvaluateVersion(
		rules.PackageMeta{Name: "evil"},
		rules.VersionMeta{Version: "1.0.0", HasInstallScripts: true},
	)
	if dec.Allow {
		t.Error("should be denied")
	}
	if dec.Reason == "" {
		t.Error("reason should not be empty")
	}
	_ = fmt.Sprintf("reason: %s", dec.Reason) // suppress unused import warning
}

// ─── Trusted Packages ────────────────────────────────────────────────────────

func TestTrustedPackageAllowsPackageLevel(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*"},
		Rules: []config.PackageRule{
			{Name: "deny-all", Action: "deny", PackagePatterns: []string{"*"}},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "@types/node"})
	if !dec.Allow {
		t.Error("trusted package should be allowed at package level")
	}
	if dec.Reason != "trusted_package" {
		t.Errorf("reason: want trusted_package, got %q", dec.Reason)
	}
	if dec.RuleName != "trusted_packages" {
		t.Errorf("rule name: want trusted_packages, got %q", dec.RuleName)
	}
}

func TestTrustedPackageAllowsVersionLevel(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"@angular/*"},
		Rules: []config.PackageRule{
			{Name: "block-pre", BlockPreRelease: true},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "@angular/core"}
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{Version: "17.0.0-rc.1"})
	if !dec.Allow {
		t.Error("trusted package version should be allowed even for pre-release")
	}
	if dec.Reason != "trusted_package" {
		t.Errorf("reason: want trusted_package, got %q", dec.Reason)
	}
}

func TestTrustedPackageBypassesInstallScripts(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"esbuild"},
		InstallScripts: config.InstallScriptsConfig{
			Enabled: true,
			Action:  "deny",
		},
		Rules: []config.PackageRule{{Name: "global"}},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "esbuild"}
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{
		Version:           "0.19.12",
		HasInstallScripts: true,
	})
	if !dec.Allow {
		t.Error("trusted package should bypass install scripts check")
	}
}

func TestTrustedPackageBypassesAge(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*"},
		Rules: []config.PackageRule{
			{Name: "age-block", MinPackageAgeDays: 10000},
		},
	}
	engine := rules.New(policy)
	pkg := rules.PackageMeta{Name: "@types/react"}
	dec := engine.EvaluateVersion(pkg, rules.VersionMeta{
		Version:     "18.0.0",
		PublishedAt: time.Now().Add(-24 * time.Hour),
	})
	if !dec.Allow {
		t.Error("trusted package should bypass age check")
	}
}

func TestTrustedPackageExactMatch(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"lodash"},
		Rules: []config.PackageRule{
			{Name: "deny-all", Action: "deny", PackagePatterns: []string{"*"}},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "lodash"})
	if !dec.Allow {
		t.Error("exact-match trusted package should be allowed")
	}
}

func TestTrustedPackageNoMatchDenied(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*"},
		Rules: []config.PackageRule{
			{Name: "deny-all", Action: "deny", PackagePatterns: []string{"*"}},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "evil-pkg"})
	if dec.Allow {
		t.Error("non-trusted package should still be denied")
	}
}

func TestTrustedPackageEmptyListNoEffect(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{},
		Rules: []config.PackageRule{
			{Name: "deny-all", Action: "deny", PackagePatterns: []string{"*"}},
		},
	}
	engine := rules.New(policy)
	dec := engine.EvaluatePackage(rules.PackageMeta{Name: "any-pkg"})
	if dec.Allow {
		t.Error("empty trusted list should not allow packages")
	}
}

func TestIsTrustedPackageMethod(t *testing.T) {
	policy := config.PolicyConfig{
		TrustedPackages: []string{"@types/*", "lodash", "junit:*"},
	}
	engine := rules.New(policy)
	tests := []struct {
		name string
		want bool
	}{
		{"@types/node", true},
		{"@types/react", true},
		{"lodash", true},
		{"junit:junit", true},
		{"evil-pkg", false},
		{"@types", false},
	}
	for _, tc := range tests {
		if got := engine.IsTrustedPackage(tc.name); got != tc.want {
			t.Errorf("IsTrustedPackage(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
