# Composer / Packagist Bulwark — Implementation Specification

## Overview

**Ecosystem:** Composer / Packagist (PHP)  
**Default upstream:** `https://repo.packagist.org`  
**Supplementary API:** `https://packagist.org`  
**Default port:** 18009  
**Binary name:** `composer-bulwark`  
**Client tools:** `composer install`, `composer update`, `composer require`

Composer resolves PHP packages from Packagist using a JSON-based metadata API.
The primary resolution protocol uses the **p2 metadata endpoint** (Composer 2+)
which provides all version metadata for a package in a single JSON file.

---

## 1. Packagist API Endpoints

### 1.1 P2 Metadata (Composer 2 — Primary)

| Pattern                           | Upstream URL                                            | Description           |
| --------------------------------- | ------------------------------------------------------- | --------------------- |
| `/p2/{vendor}/{package}.json`     | `https://repo.packagist.org/p2/{vendor}/{package}.json` | All versions metadata |
| `/p2/{vendor}/{package}~dev.json` | same                                                    | Dev versions only     |

Example response (`/p2/laravel/framework.json`):

```json
{
  "packages": {
    "laravel/framework": [
      {
        "name": "laravel/framework",
        "version": "v12.12.0",
        "version_normalized": "12.12.0.0",
        "license": ["MIT"],
        "time": "2025-06-03T14:22:41+00:00",
        "dist": {
          "type": "zip",
          "url": "https://api.github.com/repos/laravel/framework/zipball/abc123",
          "shasum": ""
        },
        "source": {
          "type": "git",
          "url": "https://github.com/laravel/framework.git",
          "reference": "abc123"
        },
        "description": "The Laravel Framework.",
        "homepage": "https://laravel.com",
        "require": { "php": "^8.2", ... },
        "type": "library"
      }
    ]
  }
}
```

### 1.2 P1 Metadata (Legacy — Composer 1)

| Pattern                             | Description                                   |
| ----------------------------------- | --------------------------------------------- |
| `/p/{vendor}/{package}${hash}.json` | Legacy provider with SHA-256 hash in filename |

**Note:** Composer 1 is deprecated. Support is optional.

### 1.3 Package List / Search

| Pattern                                      | Description               |
| -------------------------------------------- | ------------------------- |
| `https://packagist.org/packages/list.json`   | Full list of all packages |
| `https://packagist.org/search.json?q={term}` | Search packages           |

### 1.4 Package API (Rich Metadata)

| Pattern                                                  | Description                                                 |
| -------------------------------------------------------- | ----------------------------------------------------------- |
| `https://packagist.org/packages/{vendor}/{package}.json` | Full package info with all versions, downloads, maintainers |

### 1.5 Dist Download

Dist URLs point to VCS hosting (GitHub, GitLab) ZIP archives:

```
https://api.github.com/repos/{owner}/{repo}/zipball/{ref}
```

Composer downloads source code from VCS providers, not from Packagist itself.
The proxy controls metadata visibility; artifact downloads go directly to GitHub/GitLab.

### 1.6 packages.json Root

| Pattern          | Description                                                |
| ---------------- | ---------------------------------------------------------- |
| `/packages.json` | Root discovery document — lists all metadata provider URLs |

Response:

```json
{
  "packages": [],
  "metadata-url": "/p2/%package%.json",
  "provider-includes": { ... },
  "available-packages": [ ... ]
}
```

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /packages.json                    → handleRoot (rewrite metadata-url)
GET /p2/{vendor}/{package}.json       → handleP2Metadata
GET /p2/{vendor}/{package}~dev.json   → handleP2DevMetadata
GET /p/{vendor}/{package}${hash}.json → handleP1Metadata (legacy)
GET /health                           → healthHandler
GET /readyz                           → readyzHandler
GET /metrics                          → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Composer packages use `vendor/package` naming: `laravel/framework`, `symfony/console`.
- The vendor acts as a namespace (e.g., `laravel/*`, `symfony/*`).
- Extract from URL path: `/p2/{vendor}/{package}.json` → name is `{vendor}/{package}`.

### 2.3 Metadata Retrieval Strategy

**For `handleP2Metadata` (p2 filtering — primary):**

1. Fetch upstream `/p2/{vendor}/{package}.json`.
2. Parse JSON. The `packages.{vendor}/{package}` array contains all versions.
3. For each version entry:
   a. Extract `version`, `time`, `license`, `description`, `source.url`.
   b. Build `PackageMeta` and `VersionMeta`.
   c. Call `EvaluatePackage()` + `EvaluateVersion()`.
   d. Remove denied versions from the array.
4. Return filtered JSON.

**For `handleRoot` (packages.json rewriting):**

1. Fetch upstream `/packages.json`.
2. Rewrite `metadata-url` to point to the proxy.
3. Return modified JSON.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                        |
| ------------------ | ------------------------------------------------------------- |
| **Implementable?** | YES                                                           |
| **Logic**          | Match `vendor/package` name against `trusted_packages` globs. |
| **Example**        | `"laravel/*"`, `"symfony/*"`, `"phpunit/*"`, `"guzzlehttp/*"` |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                                                |
| ------------------ | ------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                   |
| **Logic**          | Glob matching on `vendor/package`. The `vendor/` prefix provides natural namespacing. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                                                                                          |
| **Logic**          | Normalize full name or package portion (lowercase, strip `-` and `_`), compute Levenshtein distance.                                                                                                         |
| **Note**           | Packagist has ~400,000 packages. Typosquatting the `vendor/package` pair provides stronger signal than package name alone. Consider comparing both the full `vendor/package` and just the `package` portion. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                                   |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES — STRONG FIT                                                                                                                                         |
| **Logic**          | Composer's `vendor/package` naming is an enforced namespace. Packagist verifies vendor ownership. Match `internal_patterns` against `vendor/*` prefixes. |
| **Note**           | This is the second-strongest namespace protection after Go modules, as the vendor prefix is mandatory and verified.                                      |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                                                                                                                                                                    |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                                                                                                                                                                       |
| **Logic**                  | Composer follows SemVer with some extensions. Pre-release identifiers: `-alpha`, `-beta`, `-RC`, `-dev`, `-patch`. The `version` field includes these suffixes. Also, dev versions are served separately via `~dev.json`. |
| **Custom `IsPreRelease`:** | Check for SemVer pre-release suffix (`-`), or `version_normalized` ending in non-zero stability flag. Alternatively, deny all versions from the `~dev.json` endpoint.                                                     |
| **Stability flags**        | Composer uses: `dev`, `alpha`, `beta`, `RC`, `stable`. Versions in `~dev.json` are `dev-*` branches.                                                                                                                      |

### 3.6 Snapshot Blocking

| Aspect             | Detail                                                                                                                                                                                                |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (as dev-branch blocking)                                                                                                                                                                          |
| **Logic**          | Composer `dev-*` versions (e.g., `dev-main`, `dev-master`) are analogous to Maven SNAPSHOTs — they reference the latest commit on a branch and change over time. Block versions starting with `dev-`. |
| **Implementation** | Check: `strings.HasPrefix(version, "dev-")`                                                                                                                                                           |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                        |
| ------------------ | ------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                           |
| **Logic**          | The `time` field in p2 metadata is an ISO 8601 timestamp per version. Compare against `min_package_age_days`. |
| **Data source**    | Directly in the p2 metadata — no supplementary API needed.                                                    |

### 3.8 Bypass Age Filter

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.9 Pinned Versions

| Aspect             | Detail                                                                              |
| ------------------ | ----------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                 |
| **Logic**          | Standard. Note Composer versions may have `v` prefix: `v12.12.0` or just `12.12.0`. |

### 3.10 Velocity Check

| Aspect             | Detail                                                                       |
| ------------------ | ---------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                          |
| **Logic**          | All version timestamps are in the p2 metadata. Count versions within window. |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                                                                                                                                                                              |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                                                                 |
| **Logic**          | Composer `composer.json` supports lifecycle scripts: `pre-install-cmd`, `post-install-cmd`, `pre-update-cmd`, `post-update-cmd`, `pre-autoload-dump`, `post-autoload-dump`, `post-root-package-install`, `post-create-project-cmd`. |
| **Challenge**      | The scripts are in the package's `composer.json`, not in the p2 metadata. The proxy would need to download and inspect the package to check.                                                                                        |
| **Alternative**    | The `type` field in p2 metadata can indicate packages that are likely to have scripts (e.g., `composer-plugin` type packages). Composer plugins execute code during `composer install`. Block/warn on `type: "composer-plugin"`.    |
| **Recommendation** | Block `type: "composer-plugin"` packages by default (these run arbitrary PHP code). For other script checks, require package download inspection.                                                                                   |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                            |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                               |
| **Logic**          | The `license` field is an array of SPDX identifiers (e.g., `["MIT"]`, `["GPL-2.0-or-later"]`, `["Apache-2.0"]`). Check each element against allowed/denied lists. |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                                             |
| ------------------ | ------------------------------------------------------------------ |
| **Implementable?** | YES                                                                |
| **Logic**          | Standard regex matching on version string.                         |
| **Example**        | `match: "^dev-"` with `action: deny` to block dev-branch versions. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                             |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                |
| **Logic**          | From p2 metadata: `source.url` or `homepage` → missing_repository, `license` → missing_license, `description` → empty_description. |

### 3.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

---

## 4. P2 Metadata Filtering Algorithm

```
1. Fetch upstream /p2/{vendor}/{package}.json.
2. Parse JSON into struct:
   { "packages": { "{vendor}/{package}": [ {version1}, {version2}, ... ] } }
3. For the package name {vendor}/{package}:
   a. Build PackageMeta{Name: vendor/package, Versions: allVersionMetas}.
   b. Call EvaluatePackage(). If denied, return empty packages array.
4. For each version in the array:
   a. Build VersionMeta{Version, PublishedAt: time, License: license[0]}.
   b. Call EvaluateVersion().
   c. If denied, remove from array.
5. Return filtered JSON with adjusted content-length.
```

---

## 5. Root Document Rewriting

The proxy must intercept `/packages.json` and rewrite the `metadata-url` to
point to itself:

```json
{
  "metadata-url": "http://localhost:18009/p2/%package%.json"
}
```

This ensures Composer routes all metadata requests through the proxy.

---

## 6. Configuration Example

```yaml
server:
  port: 18009

upstream:
  url: https://repo.packagist.org
  timeout_seconds: 30
  allowed_external_hosts:
    - "packagist.org"
    - "api.github.com"
    - "github.com"
    - "gitlab.com"

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "laravel/*"
    - "symfony/*"
    - "phpunit/*"
    - "guzzlehttp/*"
    - "monolog/*"
    - "doctrine/*"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  install_scripts:
    enabled: true
    action: deny
    allowed_with_scripts: []
    reason: "composer-plugin packages execute arbitrary PHP code"
  rules:
    - name: block-composer-plugins
      package_patterns: ["*"]
      action: deny
      reason: "composer plugins are restricted"
    - name: deny-malicious
      package_patterns: ["evil/*"]
      action: deny
      reason: "known malicious vendor"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses: ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause"]
  version_patterns:
    - name: block-dev-branches
      match: "^dev-"
      action: deny
      reason: "dev-branch versions are not allowed"
    - name: block-alpha
      match: "-alpha"
      action: deny
      reason: "alpha versions not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 7. Ecosystem-Specific Considerations

1. **Vendor/package naming:** Composer's mandatory `vendor/package` format provides strong natural namespacing. This makes namespace protection highly effective.

2. **Composer plugins are dangerous:** Packages with `type: "composer-plugin"` execute arbitrary PHP code during `composer install`. These should be treated with the same severity as npm `postinstall` scripts. Consider a dedicated rule or special-case in install scripts detection.

3. **Dist downloads from VCS:** Composer downloads actual package code from GitHub/GitLab/Bitbucket, not from Packagist. The proxy controls version visibility (which versions appear in metadata) but does not proxy the source download. Add VCS hosts to `allowed_external_hosts`.

4. **Dev versions endpoint:** The `~dev.json` endpoint serves development branch versions separately. Consider blocking this endpoint entirely as a blanket dev-version policy.

5. **Private Packagist:** Organizations often use Private Packagist (packagist.com) or Satis for private packages. The proxy should be configurable to proxy these by changing the upstream URL.

6. **`packages.json` rewriting:** Essential for Composer 2 to route metadata requests through the proxy. Without this, Composer discovers the real metadata URL and bypasses the proxy.

---

## 8. Rules NOT Applicable to Composer/Packagist

| Rule                                         | Reason                                                                                                                                                                                         |
| -------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **block_snapshots** (literal Maven SNAPSHOT) | Composer uses `dev-*` versions instead. Implement as version pattern or snapshot blocking mapped to `dev-*` prefix.                                                                            |
| **Install scripts** (full detection)         | Script definitions are in `composer.json` inside the package, not in p2 metadata. Only `type: "composer-plugin"` is detectable from metadata. Full script detection requires package download. |
