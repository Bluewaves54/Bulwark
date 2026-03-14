// SPDX-License-Identifier: Apache-2.0

package rules

import (
	"log/slog"
	"strings"
)

// Rule name constants returned in FilterDecision.RuleName for system-level checks.
const (
	// RuleNamespaceProtection is the rule name for internal-namespace protection denials.
	RuleNamespaceProtection = "namespace_protection"
	// RuleTyposquatting is the rule name for typo-squatting denials.
	RuleTyposquatting = "typosquatting"
	// RuleVelocityDetection is the rule name for velocity-anomaly denials.
	RuleVelocityDetection = "velocity_detection"
	// RuleInstallScripts is the rule name for install-script denials.
	RuleInstallScripts = "install_scripts"
)

// Reason constants used in FilterDecision.Reason.
const (
	reasonPreRelease     = "pre_release"
	reasonSnapshot       = "snapshot"
	reasonAge            = "age"
	reasonVelocity       = "velocity"
	reasonExplicitDeny   = "explicit_deny"
	reasonNamespace      = "namespace_violation"
	reasonTyposquat      = "typosquat"
	reasonPinned         = "pinned_version"
	reasonVersionPattern = "version_pattern"
	reasonInstallScripts = "install_scripts"
	reasonLicense        = "license"
	reasonTrusted        = "trusted_package"
)

// MetadataAnomaly describes a single suspicious field found in package metadata.
type MetadataAnomaly struct {
	// Check is the name of the check that triggered (e.g. "missing_repository").
	Check string
	// Message is a human-readable description.
	Message string
}

// CheckInstallScripts returns a FilterDecision denying the package when it declares
// lifecycle scripts (preinstall, install, postinstall) and is not in the allowlist.
// scripts is typically the "scripts" object from an npm package.json or packument.
// Returns nil when no action should be taken.
func (e *RuleEngine) CheckInstallScripts(pkgName string, scripts map[string]string) *FilterDecision {
	if !e.installScripts.Enabled {
		return nil
	}
	dangerousKeys := []string{"preinstall", "install", "postinstall"}
	hasDangerous := false
	for _, key := range dangerousKeys {
		if _, ok := scripts[key]; ok {
			hasDangerous = true
			break
		}
	}
	if !hasDangerous {
		return nil
	}

	for _, allowed := range e.installScripts.AllowedWithScripts {
		if strings.EqualFold(allowed, pkgName) {
			return nil
		}
	}

	reason := e.installScripts.Reason
	if reason == "" {
		reason = reasonInstallScripts
	}

	if e.installScripts.Action == "warn" {
		e.logger.Warn("install scripts detected (warn only)",
			slog.String("package", pkgName),
			slog.String("scripts", joinKeys(scripts)),
		)
		return nil
	}

	if e.dryRun {
		e.logger.Warn("dry_run: would deny install scripts",
			slog.String("package", pkgName),
		)
		d := FilterDecision{Allow: true, Reason: reason, RuleName: RuleInstallScripts, DryRun: true}
		return &d
	}
	d := FilterDecision{Allow: false, Reason: reason, RuleName: RuleInstallScripts}
	return &d
}

// CheckMetadataAnomalies scans a package metadata map for suspicious absences.
// meta is a free-form map representing fields from a registry metadata response.
// The following fields are examined when cfg.MetadataCheck is enabled (checked by caller):
//   - "repository": missing or empty → MissingRepository anomaly
//   - "license": missing or empty → MissingLicense anomaly
//   - "description": missing or empty → EmptyDescription anomaly
func CheckMetadataAnomalies(meta map[string]interface{}) []MetadataAnomaly {
	var anomalies []MetadataAnomaly

	if isEmpty(meta["repository"]) {
		anomalies = append(anomalies, MetadataAnomaly{
			Check:   "missing_repository",
			Message: "package has no repository URL declared",
		})
	}
	if isEmpty(meta["license"]) {
		anomalies = append(anomalies, MetadataAnomaly{
			Check:   "missing_license",
			Message: "package has no SPDX license declared",
		})
	}
	if isEmpty(meta["description"]) {
		anomalies = append(anomalies, MetadataAnomaly{
			Check:   "empty_description",
			Message: "package has an empty description",
		})
	}
	return anomalies
}

// isEmpty returns true when v is nil, an empty string, or a non-null interface
// wrapping an empty string.
func isEmpty(v interface{}) bool {
	if v == nil {
		return true
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s) == ""
	}
	return false
}

// joinKeys returns the keys of a map joined by commas — used for log messages.
func joinKeys(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return strings.Join(keys, ",")
}
