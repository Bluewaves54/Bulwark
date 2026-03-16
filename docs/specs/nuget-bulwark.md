# NuGet Bulwark — Implementation Specification

## Overview

**Ecosystem:** NuGet (.NET / C# / F# / VB.NET)  
**Default upstream:** `https://api.nuget.org`  
**Default port:** 18004  
**Binary name:** `nuget-bulwark`  
**Client tools:** `dotnet restore`, `nuget.exe install`, Visual Studio Package Manager, Rider

NuGet uses a versioned HTTP API (currently V3) with a service index that
advertises resource endpoints. The client discovers endpoints dynamically via the
service index JSON, then queries registration, flat container, search, and
content resources.

---

## 1. NuGet V3 API Endpoints

### 1.1 Service Index (Discovery)

| Endpoint         | Upstream URL                          |
| ---------------- | ------------------------------------- |
| `/v3/index.json` | `https://api.nuget.org/v3/index.json` |

Returns a JSON document listing all resource types and their base URLs:

```json
{
  "version": "3.0.0",
  "resources": [
    { "@id": "https://api.nuget.org/v3-flatcontainer/", "@type": "PackageBaseAddress/3.0.0" },
    { "@id": "https://api.nuget.org/v3/registration5-gz-semver2/", "@type": "RegistrationsBaseUrl/3.6.0" },
    { "@id": "https://azuresearch-usnc.nuget.org/query", "@type": "SearchQueryService" },
    ...
  ]
}
```

**Proxy strategy:** Intercept the service index and rewrite resource URLs to
point back to the proxy, so all subsequent calls are also intercepted.

### 1.2 Package Registration (Metadata)

| Pattern                                                             | Upstream URL                                                              |
| ------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| `/v3/registration5-gz-semver2/{id-lower}/index.json`                | `https://api.nuget.org/v3/registration5-gz-semver2/{id-lower}/index.json` |
| `/v3/registration5-gz-semver2/{id-lower}/{version}.json`            | same pattern                                                              |
| `/v3/registration5-gz-semver2/{id-lower}/page/{lower}/{upper}.json` | paginated                                                                 |

**Response encoding:** The `registration5-gz-semver2` variant returns **gzip-compressed** JSON. Must decompress before parsing.

Example registration entry (per version):

```json
{
  "catalogEntry": {
    "id": "Newtonsoft.Json",
    "version": "13.0.3",
    "published": "2023-03-08T18:10:35.737+00:00",
    "licenseExpression": "MIT",
    "listed": true,
    "description": "Json.NET is a popular high-performance JSON framework for .NET",
    "projectUrl": "https://www.newtonsoft.com/json",
    "packageContent": "https://api.nuget.org/v3-flatcontainer/newtonsoft.json/13.0.3/newtonsoft.json.13.0.3.nupkg"
  }
}
```

Key fields: `published` (ISO 8601 timestamp), `licenseExpression` (SPDX), `listed` (boolean), `description`, `projectUrl`.

### 1.3 Flat Container (Package Content)

| Pattern                                                             | Description          |
| ------------------------------------------------------------------- | -------------------- |
| `/v3-flatcontainer/{id-lower}/index.json`                           | List of all versions |
| `/v3-flatcontainer/{id-lower}/{version}/{id-lower}.{version}.nupkg` | Package download     |
| `/v3-flatcontainer/{id-lower}/{version}/{id-lower}.nuspec`          | Package spec XML     |

Version listing:

```json
{
  "versions": ["12.0.1", "12.0.2", "12.0.3", "13.0.1", "13.0.2", "13.0.3"]
}
```

### 1.4 Search

| Pattern                          | Description                                                       |
| -------------------------------- | ----------------------------------------------------------------- |
| `/query?q={term}&skip=0&take=20` | Search packages (proxied from `azuresearch-usnc.nuget.org/query`) |

### 1.5 Package Content Download

| Pattern                                                             | Description                |
| ------------------------------------------------------------------- | -------------------------- |
| `/v3-flatcontainer/{id-lower}/{version}/{id-lower}.{version}.nupkg` | `.nupkg` file (ZIP format) |

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /v3/index.json                      → handleServiceIndex (rewrite resource URLs)
GET /v3/registration5-gz-semver2/{id}/index.json → handleRegistration
GET /v3/registration5-gz-semver2/{id}/{version}.json → handleRegistrationVersion
GET /v3-flatcontainer/{id}/index.json   → handleVersionList
GET /v3-flatcontainer/{id}/{ver}/{id}.{ver}.nupkg → handlePackageDownload
GET /v3-flatcontainer/{id}/{ver}/{id}.nuspec → handleNuspec
GET /query                              → handleSearch (pass-through or filter)
GET /health                             → healthHandler
GET /readyz                             → readyzHandler
GET /metrics                            → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Package IDs are **case-insensitive**. NuGet API uses lowercase IDs in URLs.
- Extract from URL path: `/v3-flatcontainer/{id-lower}/{version}/...`
- Registration URLs also embed the lowercased package ID.

### 2.3 Metadata Retrieval Strategy

**For `handleRegistration` (metadata filtering):**

1. Fetch upstream registration index (gzip-compressed).
2. Decompress and parse JSON.
3. For each version in `items[].items[]` (or paged):
   a. Extract `catalogEntry.version`, `catalogEntry.published`, `catalogEntry.licenseExpression`, etc.
   b. Call `EvaluatePackage()` + `EvaluateVersion()`.
   c. Remove denied versions from the response.
4. Recompress (gzip) and return.

**For `handlePackageDownload` (artifact guard):**

1. Extract package ID and version from URL.
2. Fetch registration metadata for the specific version (or use cached).
3. Evaluate rules.
4. If denied, return 403. If allowed, proxy the `.nupkg` download.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                                        |
| ------------------ | ----------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                           |
| **Logic**          | Match package ID (case-insensitive) against `trusted_packages` glob patterns. |
| **Example**        | `"Microsoft.*"`, `"System.*"`, `"Newtonsoft.Json"`                            |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                |
| **Logic**          | Standard glob matching on package ID (case-insensitive).                                                           |
| **Note**           | NuGet package IDs use dots as separators (e.g., `Microsoft.Extensions.Logging`). Globs should treat `.` literally. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                                                                                      |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                                         |
| **Logic**          | Normalize name (lowercase, strip `.` and `-`), compute Levenshtein distance against `protected_packages`.                                                                                                   |
| **Note**           | NuGet has ~400,000 packages. The `.` separator means `Microsoft.Extensions.Logging` normalizes to `microsoftextensionslogging`. Typosquatting detection is most useful for well-known short-named packages. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                                          |
| **Logic**          | NuGet uses dot-separated naming conventions that act as de facto namespaces (e.g., `Microsoft.*`, `System.*`, `Azure.*`). Match against `internal_patterns`. |
| **Note**           | NuGet.org has a **package ID prefix reservation** system that provides some official namespace protection. This rule adds proxy-level enforcement.           |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                                                                                                      |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                                                                                                         |
| **Logic**                  | NuGet follows SemVer 2.0. Pre-release versions have a hyphen suffix: `1.0.0-beta.1`, `2.0.0-rc.1`, `1.0.0-preview.3`. Check for `-` after the version core. |
| **Custom `IsPreRelease`:** | `strings.Contains(ver, "-")` after the `major.minor.patch` core, which is standard SemVer pre-release detection.                                            |

### 3.6 Snapshot Blocking

| Aspect             | Detail                                                                                    |
| ------------------ | ----------------------------------------------------------------------------------------- |
| **Implementable?** | NOT APPLICABLE                                                                            |
| **Reason**         | NuGet does not have a SNAPSHOT concept. Pre-release versions (§3.5) serve a similar role. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect                | Detail                                                                                                                                                                    |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**    | YES                                                                                                                                                                       |
| **Logic**             | The `published` field in `catalogEntry` contains an ISO 8601 timestamp. Parse and compare against `min_package_age_days`.                                                 |
| **Data source**       | Registration endpoint or catalog API. **No supplementary API needed** — timestamps are directly in the primary metadata.                                                  |
| **Unlisted handling** | When `listed: false`, the `published` timestamp is set to `1900-01-01T00:00:00+00:00`. Detect and skip age filtering for unlisted packages (or deny in fail-closed mode). |

### 3.8 Bypass Age Filter

| Aspect             | Detail                                         |
| ------------------ | ---------------------------------------------- |
| **Implementable?** | YES                                            |
| **Logic**          | Standard: skip age check for matched packages. |

### 3.9 Pinned Versions

| Aspect             | Detail                                                          |
| ------------------ | --------------------------------------------------------------- |
| **Implementable?** | YES                                                             |
| **Logic**          | Standard: exact version string match against `pinned_versions`. |

### 3.10 Velocity Check

| Aspect             | Detail                                                                                                                                                      |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                         |
| **Logic**          | From the registration index, extract `published` timestamps for all versions. Count versions published within `window_hours` over the last `lookback_days`. |
| **Data source**    | Registration endpoint has all version timestamps.                                                                                                           |

### 3.11 Install Scripts Detection

| Aspect                      | Detail                                                                                                                                                                                                                                                                                                           |
| --------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**          | YES (from nuspec or nupkg)                                                                                                                                                                                                                                                                                       |
| **Logic**                   | NuGet packages can include PowerShell scripts: `install.ps1`, `uninstall.ps1`, and `init.ps1` in the `tools/` directory. The `.nuspec` file may declare `<files>` entries.                                                                                                                                       |
| **Implementation approach** | Option 1: Download the `.nuspec` and check for `tools/install.ps1` references. Option 2: Download the `.nupkg` (ZIP) and inspect contents for `tools/*.ps1`. Option 3: NuGet deprecated install.ps1 in PackageReference format (modern .NET), so this is mainly relevant for legacy `packages.config` consumers. |
| **Note**                    | Modern NuGet (PackageReference) ignores `install.ps1`. Consider making this an opt-in check with a documentation note about the reduced risk in modern .NET.                                                                                                                                                     |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                                                     |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                                                                        |
| **Logic**          | `catalogEntry.licenseExpression` contains SPDX expressions (e.g., `"MIT"`, `"Apache-2.0"`, `"MIT OR Apache-2.0"`). For compound expressions, split on `OR`/`AND` and check each component. |
| **Fallback**       | Some older packages use `licenseUrl` instead of `licenseExpression`. When `licenseExpression` is empty, the licence is unknown — handle per `fail_mode`.                                   |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                |
| **Logic**          | Standard regex matching on the version string.                                                     |
| **Example**        | `match: "-preview"` with `action: deny` to block preview builds while allowing other pre-releases. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                              |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                 |
| **Logic**          | From `catalogEntry`: check `projectUrl` (repository), `licenseExpression` (license), `description`. Map to standard anomaly checks. |
| **Fields**         | `projectUrl` → `missing_repository`, `licenseExpression` → `missing_license`, `description` → `empty_description`.                  |

### 3.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail                                                                                   |
| ------------------ | ---------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                      |
| **Logic**          | Standard. Particularly relevant when gzip decompression of registration responses fails. |

---

## 4. Registration Response Filtering Algorithm

```
1. Fetch upstream registration index (gzip-compressed).
2. Decompress with gzip reader.
3. Parse JSON into registration index struct:
   { "items": [ { "items": [ { "catalogEntry": {...} }, ... ] } ] }
4. If paginated (items contain @id references instead of inline items),
   fetch each page and merge.
5. For each catalogEntry:
   a. Build PackageMeta{Name: id, Versions: allVersionMetas}.
   b. Call EvaluatePackage(). If denied, remove all versions.
   c. For each version, build VersionMeta from catalogEntry fields.
   d. Call EvaluateVersion(). If denied, remove this version from items.
6. Rebuild JSON with remaining versions.
7. Gzip-compress the response.
8. Return with correct Content-Length and Content-Encoding: gzip.
```

---

## 5. Service Index Rewriting

The proxy must intercept `/v3/index.json` and rewrite all resource `@id` URLs
to point to the proxy's own address. This ensures the NuGet client routes all
subsequent requests through the proxy.

Example rewrite:

```
"@id": "https://api.nuget.org/v3-flatcontainer/"
→ "@id": "http://localhost:18004/v3-flatcontainer/"
```

All resource types to rewrite:

- `PackageBaseAddress/3.0.0` → flat container
- `RegistrationsBaseUrl/3.6.0` → registration
- `SearchQueryService` → search
- `SearchAutocompleteService` → autocomplete
- `PackagePublish/2.0.0` → publish (block or pass-through)

---

## 6. Configuration Example

```yaml
server:
  port: 18004

upstream:
  url: https://api.nuget.org
  timeout_seconds: 30

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "Microsoft.*"
    - "System.*"
    - "Newtonsoft.Json"
    - "NUnit"
    - "xunit"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  install_scripts:
    enabled: true
    action: deny
    allowed_with_scripts:
      - "EntityFramework"
    reason: "packages with install.ps1 scripts are blocked"
  rules:
    - name: deny-known-malicious
      package_patterns: ["malicious.*"]
      action: deny
      reason: "known malicious package"
    - name: block-preview-builds
      package_patterns: ["*"]
      block_pre_release: true
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses: ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause"]
  version_patterns:
    - name: block-preview
      match: "-preview"
      action: deny
      reason: "preview versions are not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 7. Ecosystem-Specific Considerations

1. **Service index rewriting** is mandatory. NuGet clients discover all API endpoints from the service index. Without rewriting, subsequent calls bypass the proxy entirely.

2. **Gzip handling:** The `registration5-gz-semver2` resource returns gzip-compressed responses by default. Must decompress to filter, then recompress. Alternatively, use the non-gz `registration5-semver2` variant but it may not be available on all NuGet servers.

3. **Case insensitivity:** NuGet package IDs are case-insensitive. `Newtonsoft.Json`, `newtonsoft.json`, `NEWTONSOFT.JSON` all refer to the same package. All comparisons must be case-insensitive.

4. **Package ID prefix reservation:** NuGet.org reserves ID prefixes for verified owners (e.g., `Microsoft.*` can only be published by Microsoft). The proxy's namespace protection adds an additional layer.

5. **Unlisted packages:** Packages with `listed: false` have `published: "1900-01-01T00:00:00+00:00"`. The proxy should either skip these in age filtering or treat the sentinel date appropriately.

6. **SemVer 2.0 metadata:** NuGet supports SemVer 2.0 build metadata (e.g., `1.0.0+build.123`). Version comparison should normalize build metadata.

---

## 8. Rules NOT Applicable to NuGet

| Rule                | Reason                                                                               |
| ------------------- | ------------------------------------------------------------------------------------ |
| **block_snapshots** | NuGet does not have SNAPSHOT versions. Pre-release versions serve a similar purpose. |
