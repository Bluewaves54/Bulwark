# RubyGems Bulwark — Implementation Specification

## Overview

**Ecosystem:** RubyGems (Ruby)  
**Default upstream:** `https://rubygems.org`  
**Default port:** 18008  
**Binary name:** `rubygems-bulwark`  
**Client tools:** `gem install`, `bundle install`, `bundler`

RubyGems has a well-documented JSON API and a compact binary index protocol.
The API provides rich metadata including timestamps, licences, and version lists.
RubyGems.org has had multiple supply chain attacks (typosquatting, account
takeover, malicious gem replacements).

---

## 1. RubyGems API Endpoints

### 1.1 Gem Info (Latest Version)

| Pattern                    | Upstream URL                                   | Description             |
| -------------------------- | ---------------------------------------------- | ----------------------- |
| `/api/v1/gems/{name}.json` | `https://rubygems.org/api/v1/gems/{name}.json` | Latest version metadata |

Response:

```json
{
  "name": "rails",
  "version": "8.0.2",
  "version_created_at": "2025-03-12T21:23:05.000Z",
  "licenses": ["MIT"],
  "info": "Ruby on Rails is a full-stack web framework...",
  "homepage_uri": "https://rubyonrails.org",
  "source_code_uri": "https://github.com/rails/rails/tree/v8.0.2",
  "bug_tracker_uri": "https://github.com/rails/rails/issues",
  "gem_uri": "https://rubygems.org/gems/rails-8.0.2.gem"
}
```

### 1.2 All Versions

| Pattern                        | Description                               |
| ------------------------------ | ----------------------------------------- |
| `/api/v1/versions/{name}.json` | All versions with timestamps and metadata |

Response (array):

```json
[
  {
    "number": "8.0.2",
    "created_at": "2025-03-12T21:23:05.000Z",
    "platform": "ruby",
    "prerelease": false,
    "licenses": ["MIT"],
    "sha": "abc123..."
  },
  {
    "number": "8.0.2.rc1",
    "created_at": "2025-02-15T...",
    "prerelease": true,
    "licenses": ["MIT"]
  }
]
```

### 1.3 Gem Download

| Pattern                      | Description                                                                 |
| ---------------------------- | --------------------------------------------------------------------------- |
| `/gems/{name}-{version}.gem` | Download the `.gem` file (tar archive containing data.tar.gz + metadata.gz) |

### 1.4 Compact Index (Bundler Protocol)

Modern Bundler (1.12+) uses the **Compact Index** protocol:

| Pattern        | Description                                      |
| -------------- | ------------------------------------------------ |
| `/versions`    | Gzipped file listing all gems and their versions |
| `/info/{name}` | Per-gem version list with dependency info        |

`/versions` format (incremental):

```
created_at: 2025-06-10T00:00:00Z
---
rails 8.0.2,8.0.1,8.0.0,7.2.2,... abc123def456
rack 3.1.12,3.1.11,... 789012345678
```

`/info/{name}` format:

```
---
8.0.2 activesupport:= 8.0.2,actionpack:= 8.0.2|checksum:abc123
8.0.1 activesupport:= 8.0.1,actionpack:= 8.0.1|checksum:def456
8.0.2.rc1 activesupport:= 8.0.2.rc1|checksum:789012
```

### 1.5 Dependency API (Legacy)

| Pattern                                     | Description                                          |
| ------------------------------------------- | ---------------------------------------------------- |
| `/api/v1/dependencies?gems={name1},{name2}` | Marshalled Ruby dependency data (legacy, deprecated) |

### 1.6 Quick Index (Legacy)

| Pattern                                          | Description                 |
| ------------------------------------------------ | --------------------------- |
| `/quick/Marshal.4.8/{name}-{version}.gemspec.rz` | Deflated marshalled gemspec |

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /api/v1/gems/{name}.json         → handleGemInfo
GET /api/v1/versions/{name}.json     → handleGemVersions
GET /gems/{name}-{version}.gem       → handleGemDownload
GET /versions                        → handleCompactVersions
GET /info/{name}                     → handleCompactInfo
GET /quick/Marshal.4.8/*.gemspec.rz  → handleQuickIndex
GET /api/v1/dependencies             → handleDependencies
GET /health                          → healthHandler
GET /readyz                          → readyzHandler
GET /metrics                         → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Gem names are case-sensitive but conventionally lowercase.
- Names may contain letters, digits, `-`, `_`, and `.`.
- From download URL: split `{name}-{version}.gem` — the version starts after the
  last `-` that is followed by a digit. Special care for names like `mini_mime`
  and version-like segments in names.
- Platform gems include platform suffix: `nokogiri-1.16.0-x86_64-linux.gem`. The
  platform is separate from the version.

### 2.3 Metadata Retrieval Strategy

**For `handleGemInfo` / `handleGemVersions` (JSON API):**

1. Fetch upstream JSON.
2. Parse response.
3. Apply package-level and version-level rules.
4. Remove denied versions from the array.
5. Return filtered JSON.

**For `handleGemDownload` (artifact guard):**

1. Extract gem name and version from URL.
2. Fetch version metadata (from `/api/v1/versions/{name}.json` or cache).
3. Evaluate rules.
4. If denied, return 403. If allowed, proxy the `.gem` download.

**For `handleCompactInfo` (Bundler compact index):**

1. Fetch upstream `/info/{name}`.
2. Parse line-by-line (version + dependencies per line).
3. For each version line, evaluate rules.
4. Remove denied version lines.
5. Recalculate ETag/checksum if needed.
6. Return filtered response.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                     |
| ------------------ | ---------------------------------------------------------- |
| **Implementable?** | YES                                                        |
| **Logic**          | Match gem name against `trusted_packages` globs.           |
| **Example**        | `"rails"`, `"rack"`, `"bundler"`, `"nokogiri"`, `"rspec*"` |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                              |
| ------------------ | ----------------------------------- |
| **Implementable?** | YES                                 |
| **Logic**          | Standard glob matching on gem name. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                |
| ------------------ | --------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                   |
| **Logic**          | Normalize name (lowercase, strip `-` and `_`), compute Levenshtein distance.                                          |
| **Note**           | RubyGems.org has had documented typosquatting attacks (e.g., `atlas-client` typosquat). This rule is highly valuable. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                 |
| ------------------ | ------------------------------------------------------------------------------------------------------ |
| **Implementable?** | PARTIAL                                                                                                |
| **Reason**         | RubyGems has no formal namespace system (no scopes, no group IDs).                                     |
| **Workaround**     | Convention-based prefix matching: `rack-*`, `rails-*`, `aws-sdk-*`. Match against `internal_patterns`. |

### 3.5 Pre-release Blocking

| Aspect             | Detail                                                                                                                                                                              |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                 |
| **Logic**          | The versions endpoint explicitly includes a `prerelease` boolean field. Use it directly.                                                                                            |
| **Alternative**    | Ruby pre-release versions contain a letter suffix: `1.0.0.pre`, `2.0.0.rc1`, `3.0.0.beta2`, `1.0.0.alpha`. Detect with regex: version contains a letter (not just digits and dots). |

### 3.6 Snapshot Blocking

| Aspect             | Detail                            |
| ------------------ | --------------------------------- |
| **Implementable?** | NOT APPLICABLE                    |
| **Reason**         | RubyGems has no SNAPSHOT concept. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                                            |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                               |
| **Logic**          | The `created_at` field in the versions response provides ISO 8601 timestamps per version. Compare against `min_package_age_days`. |
| **Data source**    | `/api/v1/versions/{name}.json` — timestamps directly in primary metadata.                                                         |

### 3.8 Bypass Age Filter

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.9 Pinned Versions

| Aspect             | Detail                        |
| ------------------ | ----------------------------- |
| **Implementable?** | YES                           |
| **Logic**          | Standard exact version match. |

### 3.10 Velocity Check

| Aspect             | Detail                                                                             |
| ------------------ | ---------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                |
| **Logic**          | Fetch all version timestamps from versions endpoint. Count versions within window. |
| **Note**           | RubyGems.org allows rapid publishing, making velocity detection valuable.          |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                                                                                                                                                                                                                                         |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                                                                                                                            |
| **Logic**          | Ruby gems can include `extconf.rb`, `Rakefile`, or native extensions that execute code during installation. The `extensions` field in the gemspec lists native extension build files.                                                                                                          |
| **Implementation** | Option 1: Check the gem metadata for `extensions` array (non-empty = has native extension build scripts). Option 2: The `platform` field — if not `"ruby"`, it's a pre-built binary (no install-time compilation). Option 3: Download `.gem` and inspect for `ext/` directory or `extconf.rb`. |
| **Mapping**        | `extensions` field → has install scripts. Map to `HasInstallScripts` in `VersionMeta`.                                                                                                                                                                                                         |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                                  |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                     |
| **Logic**          | The `licenses` field is an array of SPDX-like strings (e.g., `["MIT"]`, `["Apache-2.0"]`, `["Ruby", "BSD-2-Clause"]`). Check each element against allowed/denied lists. |
| **Note**           | Some gems use non-standard license strings (e.g., `"Ruby"` for the Ruby license). Consider a normalization map.                                                         |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                     |
| ------------------ | ------------------------------------------ |
| **Implementable?** | YES                                        |
| **Logic**          | Standard regex matching on version string. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                                     |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                        |
| **Logic**          | From gem info: `source_code_uri` or `homepage_uri` → missing_repository, `licenses` → missing_license (check for empty array), `info` → empty_description. |

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

## 4. Compact Index Filtering Algorithm

The Compact Index is Bundler's primary resolution protocol. Filtering it correctly
is crucial for transparent proxy operation.

### `/info/{name}` filtering:

```
1. Fetch upstream /info/{name}.
2. Parse header (---).
3. For each line after header:
   a. Parse: "{version} {deps}|checksum:{sha}"
   b. Determine if this is a pre-release (version contains letters).
   c. Build VersionMeta (for age checks, need API side-channel).
   d. Call EvaluateVersion().
   e. If denied, remove this line.
4. Reconstruct response with remaining lines.
5. Update ETag header.
```

### `/versions` filtering:

```
1. Fetch upstream /versions.
2. Parse header (created_at + ---).
3. For each line:
   a. Parse: "{gem} {ver1},{ver2},... {checksum}"
   b. Call EvaluatePackage() for the gem name.
   c. If denied, drop entire line.
   d. For each version, evaluate version-level rules.
   e. Remove denied versions from the comma-separated list.
   f. If no versions remain, drop the line.
4. Reconstruct response.
5. Handle Range requests (Bundler uses If-None-Match / ETag for incremental updates).
```

---

## 5. Configuration Example

```yaml
server:
  port: 18008

upstream:
  url: https://rubygems.org
  timeout_seconds: 30

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "rails"
    - "rack"
    - "bundler"
    - "nokogiri"
    - "rspec*"
    - "devise"
    - "sidekiq"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  install_scripts:
    enabled: true
    action: warn
    allowed_with_scripts:
      - "nokogiri"
      - "mysql2"
      - "pg"
      - "sqlite3"
      - "grpc"
    reason: "gem has native extensions (extconf.rb)"
  rules:
    - name: deny-malicious
      package_patterns: ["malware-*"]
      action: deny
      reason: "known malicious gem"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses:
        ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "Ruby"]
  version_patterns:
    - name: block-pre
      match: "\\.(alpha|beta|pre|rc)"
      action: deny
      reason: "pre-release versions not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 6. Ecosystem-Specific Considerations

1. **Bundler compact index:** This is the primary resolution protocol for modern Bundler. Filtering it correctly is essential. The `/versions` endpoint can be very large (all gems on rubygems.org). Incremental updates via ETag/Range are critical for performance.

2. **Platform gems:** Gems like `nokogiri` ship platform-specific pre-built binaries (`nokogiri-1.16.0-x86_64-linux.gem`). The platform suffix is NOT part of the version. Parsing must account for this.

3. **Yanked gems:** Rubygems.org allows owners to "yank" gems. Yanked gems disappear from the index but may still be downloadable via direct URL. The proxy should respect yanked status.

4. **MFA enforcement:** RubyGems.org enforces MFA for popular gems. This reduces account takeover risk but doesn't eliminate typosquatting or dependency confusion.

5. **Legacy protocols:** The quick index (`/quick/Marshal.4.8/`) and dependency API (`/api/v1/dependencies`) are legacy. Supporting them is optional but ensures compatibility with older Bundler versions.

6. **Gem name parsing:** The `{name}-{version}.gem` URL format is tricky when gem names contain hyphens. Example: `aws-sdk-s3-1.170.0.gem` — the gem name is `aws-sdk-s3` and version is `1.170.0`. Split on the last `-` where the segment after it starts with a digit.

---

## 7. Rules NOT Applicable to RubyGems

| Rule                            | Reason                                                                          |
| ------------------------------- | ------------------------------------------------------------------------------- |
| **block_snapshots**             | RubyGems has no SNAPSHOT concept.                                               |
| **Namespace protection** (full) | RubyGems has no formal namespace system. Only convention-based prefix matching. |
