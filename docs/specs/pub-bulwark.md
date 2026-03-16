# Pub (Dart/Flutter) Bulwark — Implementation Specification

## Overview

**Ecosystem:** Pub (Dart / Flutter)  
**Default upstream:** `https://pub.dev`  
**Default port:** 18010  
**Binary name:** `pub-bulwark`  
**Client tools:** `dart pub get`, `flutter pub get`, `dart pub add`

Pub.dev is the official package repository for Dart and Flutter. It provides a
well-structured JSON API with rich metadata including timestamps, licences,
scores, and archive URLs. The API is clean and well-documented.

---

## 1. Pub.dev API Endpoints

### 1.1 Package Metadata

| Pattern                | Upstream URL                          | Description                             |
| ---------------------- | ------------------------------------- | --------------------------------------- |
| `/api/packages/{name}` | `https://pub.dev/api/packages/{name}` | Full package metadata with all versions |

Response:

```json
{
  "name": "http",
  "latest": {
    "version": "1.3.0",
    "pubspec": {
      "name": "http",
      "version": "1.3.0",
      "description": "A composable, multi-platform, Future-based API for HTTP requests.",
      "repository": "https://github.com/dart-lang/http",
      "environment": { "sdk": "^3.4.0" },
      "dependencies": { ... },
      "license": null
    },
    "archive_url": "https://pub.dev/api/archives/http-1.3.0.tar.gz",
    "archive_sha256": "abc123...",
    "published": "2025-01-23T19:49:33.728795Z"
  },
  "versions": [
    {
      "version": "1.3.0",
      "pubspec": { ... },
      "archive_url": "https://pub.dev/api/archives/http-1.3.0.tar.gz",
      "archive_sha256": "abc123...",
      "published": "2025-01-23T19:49:33.728795Z"
    },
    {
      "version": "1.2.2",
      "published": "2024-06-04T17:08:30.614481Z",
      ...
    }
  ]
}
```

### 1.2 Specific Version

| Pattern                                   | Description             |
| ----------------------------------------- | ----------------------- |
| `/api/packages/{name}/versions/{version}` | Single version metadata |

### 1.3 Package Archive Download

| Pattern                                 | Description                               |
| --------------------------------------- | ----------------------------------------- |
| `/api/archives/{name}-{version}.tar.gz` | Gzipped tar archive of the package source |

### 1.4 Package Score / Metrics (Optional)

| Pattern                      | Description                                             |
| ---------------------------- | ------------------------------------------------------- |
| `/api/packages/{name}/score` | Pub.dev package score (popularity, health, maintenance) |

### 1.5 Search

| Pattern                | Description     |
| ---------------------- | --------------- |
| `/api/search?q={term}` | Search packages |

### 1.6 Hosted URL Configuration

Dart/Flutter clients respect the `PUB_HOSTED_URL` environment variable:

```bash
export PUB_HOSTED_URL=http://localhost:18010
```

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /api/packages/{name}                    → handlePackageMetadata
GET /api/packages/{name}/versions/{version} → handlePackageVersion
GET /api/archives/{name}-{version}.tar.gz   → handleArchiveDownload
GET /api/search                             → handleSearch (pass-through or filter)
GET /api/packages/{name}/score              → handleScore (pass-through)
GET /health                                 → healthHandler
GET /readyz                                 → readyzHandler
GET /metrics                                → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Package names are lowercase, may contain `_` only (no hyphens).
- Extract from URL path: `/api/packages/{name}` and `/api/archives/{name}-{version}.tar.gz`.
- For archive URLs, split on the last `-` before `.tar.gz` to separate name from version.

### 2.3 Metadata Retrieval Strategy

**For `handlePackageMetadata` (metadata filtering):**

1. Fetch upstream `/api/packages/{name}`.
2. Parse JSON.
3. For each version in the `versions` array:
   a. Extract `version`, `published`, `pubspec.license`, `pubspec.description`, `pubspec.repository`.
   b. Evaluate rules.
   c. Remove denied versions.
4. Update `latest` if the latest version was denied.
5. Return filtered JSON.

**For `handleArchiveDownload` (artifact guard):**

1. Extract package name and version from URL.
2. Fetch package metadata (or use cache).
3. Evaluate rules.
4. If denied, return 403. If allowed, proxy the archive download.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                       |
| ------------------ | ------------------------------------------------------------ |
| **Implementable?** | YES                                                          |
| **Logic**          | Match package name against `trusted_packages` globs.         |
| **Example**        | `"http"`, `"flutter_*"`, `"dart_*"`, `"provider"`, `"bloc*"` |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                  |
| ------------------ | --------------------------------------- |
| **Implementable?** | YES                                     |
| **Logic**          | Standard glob matching on package name. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                    |
| ------------------ | ----------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                       |
| **Logic**          | Normalize name (lowercase, strip `_`), compute Levenshtein distance.                      |
| **Note**           | Pub.dev has ~50,000 packages. The growing Flutter ecosystem increases typosquatting risk. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                                        |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                                       |
| **Reason**         | Pub.dev has no formal namespace/scope system. Package names are flat.                                                                                         |
| **Workaround**     | Convention-based prefix matching: `flutter_*`, `dart_*`, `firebase_*`, `google_*`. Pub.dev supports **verified publishers** which provides some trust signal. |
| **Note**           | Pub.dev verified publishers (e.g., `dart.dev`, `google.dev`, `flutter.dev`) own packages but this isn't reflected in the package name.                        |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                                            |
| -------------------------- | ------------------------------------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                                               |
| **Logic**                  | Dart/Pub uses SemVer. Pre-release versions: `1.0.0-alpha.1`, `2.0.0-dev.1`, `1.0.0-nullsafety.0`. |
| **Custom `IsPreRelease`:** | Standard SemVer: check for `-` after version core.                                                |

### 3.6 Snapshot Blocking

| Aspect             | Detail                           |
| ------------------ | -------------------------------- |
| **Implementable?** | NOT APPLICABLE                   |
| **Reason**         | Pub.dev has no SNAPSHOT concept. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                              |
| ------------------ | --------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                 |
| **Logic**          | The `published` field is an RFC 3339 timestamp per version. Compare against `min_package_age_days`. |
| **Data source**    | Directly in the package metadata — no supplementary API needed.                                     |

### 3.8 Bypass Age Filter

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.9 Pinned Versions

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.10 Velocity Check

| Aspect             | Detail                                                               |
| ------------------ | -------------------------------------------------------------------- |
| **Implementable?** | YES                                                                  |
| **Logic**          | All version timestamps in the package metadata. Count within window. |

### 3.11 Install Scripts Detection

| Aspect                  | Detail                                                                                                                                                                                                                           |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**      | NOT DIRECTLY APPLICABLE                                                                                                                                                                                                          |
| **Reason**              | Dart/Pub has no lifecycle install scripts. There are no `preinstall` or `postinstall` hooks.                                                                                                                                     |
| **Partial alternative** | Dart packages can include native assets (FFI bindings) that compile native code during build. The `pubspec.yaml` may declare `native` dependencies. This is a newer feature and detectable from the pubspec in the API response. |
| **Recommendation**      | Skip install scripts detection for Pub. Optionally add native-asset detection as a Dart/Flutter-specific extension.                                                                                                              |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                                                                                    |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                                                                                                   |
| **Logic**          | The `pubspec` in the API response does NOT reliably include a `license` field. Pub.dev reads the `LICENSE` file from the package source. The API's score/metrics may indicate licence status.                             |
| **Workaround**     | Option 1: Download the archive and extract the `LICENSE` file. Option 2: Use the pub.dev web page or score API — the score includes a licence check. Option 3: Make licence checking opt-in with a documented limitation. |
| **Note**           | Newer Dart SDK versions support a `license` field in `pubspec.yaml`, but adoption is not yet universal.                                                                                                                   |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                     |
| ------------------ | ------------------------------------------ |
| **Implementable?** | YES                                        |
| **Logic**          | Standard regex matching on version string. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                         |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                            |
| **Logic**          | From pubspec in API: `repository` or `homepage` → missing_repository, `description` → empty_description. Licence check is limited (see §3.12). |

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

## 4. Package Metadata Filtering Algorithm

```
1. Fetch upstream /api/packages/{name}.
2. Parse JSON into package struct.
3. Build PackageMeta{Name: name, Versions: allVersionMetas}.
4. Call EvaluatePackage(). If denied, return 404.
5. For each version in versions array:
   a. Build VersionMeta{Version, PublishedAt: published, License: pubspec.license}.
   b. Call EvaluateVersion().
   c. If denied, remove from versions array.
6. If latest version was removed, update "latest" to the newest remaining version.
7. Return filtered JSON.
```

---

## 5. Configuration Example

```yaml
server:
  port: 18010

upstream:
  url: https://pub.dev
  timeout_seconds: 30

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "http"
    - "provider"
    - "bloc"
    - "flutter_bloc"
    - "dio"
    - "json_serializable"
    - "freezed*"
    - "riverpod*"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  rules:
    - name: deny-malicious
      package_patterns: ["malware_*"]
      action: deny
      reason: "known malicious package"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
    - name: typosquat-guard
      package_patterns: ["*"]
      typosquat_check:
        enabled: true
        max_levenshtein_dist: 2
        protected_packages:
          - "http"
          - "provider"
          - "flutter_bloc"
          - "dio"
          - "riverpod"
  version_patterns:
    - name: block-nullsafety-migration
      match: "-nullsafety"
      action: deny
      reason: "null-safety migration versions are outdated"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 6. Ecosystem-Specific Considerations

1. **`PUB_HOSTED_URL`**: Clients configure the proxy via this environment variable. No Dart/Flutter-internal proxy chaining exists — if the proxy is down, resolution fails entirely.

2. **Archive verification:** Each version includes `archive_sha256`. The proxy should verify this hash after downloading to detect tampering or corruption during transit.

3. **Flutter vs Dart:** Flutter and Dart share the same pub.dev registry. No separate handling is needed.

4. **Verified publishers:** Pub.dev has a verified publisher system (e.g., `dart.dev`, `google.dev`). This information is available on the web but not reliably in the API. Consider adding a future "verified_publishers_only" rule.

5. **Retracted versions:** Pub.dev supports version retraction (advisory, non-breaking). Retracted versions still resolve but display a warning. The proxy could optionally block retracted versions.

6. **License detection gap:** The primary API response does not include standardised licence fields. This is the biggest limitation for this ecosystem. Track the `license` field adoption in `pubspec.yaml` spec.

---

## 7. Rules NOT Applicable to Pub

| Rule                             | Reason                                                                                     |
| -------------------------------- | ------------------------------------------------------------------------------------------ |
| **block_snapshots**              | Pub has no SNAPSHOT concept.                                                               |
| **install_scripts**              | Dart/Pub has no lifecycle install scripts. No `preinstall`/`postinstall` hooks.            |
| **Namespace protection** (full)  | Pub has no formal namespace system. Only convention-based prefix matching.                 |
| **License filtering** (reliable) | Licence is not reliably in the API metadata. Requires archive download for full detection. |
