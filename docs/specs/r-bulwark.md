# R (CRAN) Bulwark — Implementation Specification

## Overview

**Ecosystem:** R / CRAN (Comprehensive R Archive Network)  
**Default upstream:** `https://cloud.r-project.org` (CDN-fronted CRAN mirror)  
**Default port:** 18003  
**Binary name:** `r-bulwark`  
**Client tool:** `install.packages()`, `pak`, `renv`, `remotes`

R clients download packages from CRAN via a simple HTTP file layout. There is no
structured JSON API on CRAN itself — the primary metadata index is a flat-text
`PACKAGES` file (RFC-822 style). A supplementary API at `crandb.r-pkg.org`
provides structured JSON with timestamps.

---

## 1. CRAN URL Layout & Proxy Endpoints

### 1.1 Package Index (Metadata)

| Client request path                           | Upstream URL                          | Content type                              |
| --------------------------------------------- | ------------------------------------- | ----------------------------------------- |
| `/src/contrib/PACKAGES`                       | `{upstream}/src/contrib/PACKAGES`     | `text/plain` (RFC-822 records)            |
| `/src/contrib/PACKAGES.gz`                    | `{upstream}/src/contrib/PACKAGES.gz`  | `application/gzip`                        |
| `/src/contrib/PACKAGES.rds`                   | `{upstream}/src/contrib/PACKAGES.rds` | `application/octet-stream` (R serialised) |
| `/bin/windows/contrib/{Rver}/PACKAGES*`       | same                                  | Windows binary index                      |
| `/bin/macosx/{arch}/contrib/{Rver}/PACKAGES*` | same                                  | macOS binary index                        |

**PACKAGES format** (one record per package, separated by blank lines):

```
Package: ggplot2
Version: 3.5.2
Depends: R (>= 3.5)
Imports: cli, glue, grDevices, grid, ...
License: MIT + file LICENSE
NeedsCompilation: no
```

**Key limitation:** The PACKAGES file does **not** contain `Date/Publication`
timestamps. Only package name, version, dependencies, licence, and compilation
flag are present.

### 1.2 Source Tarballs (Artifacts)

| Pattern                                   | Example                             |
| ----------------------------------------- | ----------------------------------- |
| `/src/contrib/{Package}_{Version}.tar.gz` | `/src/contrib/ggplot2_3.5.2.tar.gz` |

### 1.3 Archive (Old Versions)

| Pattern                                                     | Returns                                  |
| ----------------------------------------------------------- | ---------------------------------------- |
| `/src/contrib/Archive/{Package}/`                           | Directory listing (HTML) of old tarballs |
| `/src/contrib/Archive/{Package}/{Package}_{Version}.tar.gz` | Archived tarball                         |

### 1.4 Binary Packages

| Platform       | Pattern                                                             |
| -------------- | ------------------------------------------------------------------- |
| Windows        | `/bin/windows/contrib/{Rver}/{Package}_{Version}.zip`               |
| macOS (arm64)  | `/bin/macosx/big-sur-arm64/contrib/{Rver}/{Package}_{Version}.tgz`  |
| macOS (x86_64) | `/bin/macosx/big-sur-x86_64/contrib/{Rver}/{Package}_{Version}.tgz` |

### 1.5 Supplementary JSON API (crandb)

**Base URL:** `https://crandb.r-pkg.org`

| Endpoint               | Returns                                                                                  |
| ---------------------- | ---------------------------------------------------------------------------------------- |
| `/{package}`           | JSON: latest version with `Date/Publication`, `License`, `URL`, `BugReports`, all fields |
| `/{package}/{version}` | JSON: specific version                                                                   |
| `/{package}/all`       | JSON: all versions with timestamps                                                       |

Example response for `GET /ggplot2`:

```json
{
  "Package": "ggplot2",
  "Version": "3.5.2",
  "Date/Publication": "2025-04-09 12:10:06 UTC",
  "License": "MIT + file LICENSE",
  "URL": "https://ggplot2.tidyverse.org, https://github.com/tidyverse/ggplot2",
  "BugReports": "https://github.com/tidyverse/ggplot2/issues",
  ...
}
```

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /src/contrib/PACKAGES         → handlePackageIndex
GET /src/contrib/PACKAGES.gz      → handlePackageIndex (compressed)
GET /src/contrib/PACKAGES.rds     → handlePackageIndex (RDS)
GET /src/contrib/{pkg}_{ver}.tar.gz → handleTarball
GET /src/contrib/Archive/{pkg}/{pkg}_{ver}.tar.gz → handleTarball
GET /bin/windows/contrib/{Rver}/{pkg}_{ver}.zip → handleBinary
GET /bin/macosx/{arch}/contrib/{Rver}/{pkg}_{ver}.tgz → handleBinary
GET /health                       → healthHandler
GET /readyz                       → readyzHandler
GET /metrics                      → metricsHandler
```

### 2.2 Package Name & Version Extraction

From URL path segments:

- Source tarball: parse `{Package}_{Version}.tar.gz` — split on last `_` before `.tar.gz`
- Windows binary: parse `{Package}_{Version}.zip`
- macOS binary: parse `{Package}_{Version}.tgz`

R package names may contain `.` (periods) — e.g., `data.table`, `Rcpp`. They
cannot contain `_` in the name, so splitting on `_` safely separates name from version.

### 2.3 Metadata Retrieval Strategy

**For `handlePackageIndex` (PACKAGES filtering):**

1. Fetch upstream `PACKAGES` (or `.gz`/`.rds` variant).
2. Parse RFC-822 records into per-package structs.
3. For each package+version, evaluate `EvaluatePackage()` and `EvaluateVersion()`.
4. To get publish timestamps for age filtering, call `crandb.r-pkg.org/{package}/all`
   (cache aggressively — this is a third-party service).
5. Rebuild the PACKAGES file excluding denied entries.

**For `handleTarball` / `handleBinary` (artifact download):**

1. Extract package name and version from URL.
2. Fetch metadata from crandb (or cache).
3. Call `EvaluatePackage()` + `EvaluateVersion()`.
4. If denied, return 403. If allowed, proxy the artifact.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                                                                         |
| ------------------ | -------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                            |
| **Logic**          | Match package name against `trusted_packages` glob patterns. If matched, bypass all checks and proxy directly. |
| **Data source**    | Package name extracted from URL path or PACKAGES record.                                                       |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                                                                                               |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                  |
| **Logic**          | Match package name against `package_patterns` globs. If `action: deny`, block with 403. If `action: allow`, short-circuit and serve. |
| **Data source**    | Package name.                                                                                                                        |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                                                   |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                                                      |
| **Logic**          | Normalize name (lowercase, strip `.` and `-`), compute Levenshtein distance against `protected_packages`. If distance ≤ `max_levenshtein_dist`, deny.                    |
| **Note**           | R packages use `.` as word separator (e.g., `data.table`). Normalization should strip both `.` and `-`. CRAN has ~21,000 packages so false-positive tuning is important. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                                                                                                                                            |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                                                                                                                                           |
| **Logic**          | R does not have formal namespaces/scopes like npm (`@scope/`) or Maven (`groupId:`). However, some organizations use naming conventions (e.g., `RcppXxx` for Rcpp-related packages, `tidyverse`-prefixed packages). `internal_patterns` can match these prefixes. |
| **Limitation**     | No registry-enforced namespace. Protection is heuristic based on naming conventions only.                                                                                                                                                                         |

### 3.5 Pre-release Blocking

| Aspect                              | Detail                                                                                                                                                                                                                                                                                      |
| ----------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**                  | YES                                                                                                                                                                                                                                                                                         |
| **Logic**                           | R versions typically use `X.Y.Z` or `X.Y-Z`. Pre-release/development versions use 4-component versions like `1.2.3.9000` (convention: `.9000` suffix indicates dev). The `IsPreRelease` function should check for 4th component ≥ 9000 or version strings containing `alpha`, `beta`, `rc`. |
| **Custom `IsPreRelease` function:** | `strings.Contains(ver, ".9000")` or `strings.HasSuffix(ver, ".9000")` or parse 4-component version where 4th >= 9000. Note: CRAN itself rarely hosts `.9000` versions (they are typically only on GitHub), but dev versions may appear on other R repos (e.g., R-universe).                 |

### 3.6 Snapshot Blocking

| Aspect             | Detail                                                                                                                     |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | NOT APPLICABLE                                                                                                             |
| **Reason**         | R/CRAN does not have a "SNAPSHOT" versioning concept like Maven. This rule should be documented as N/A for this ecosystem. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                                                                                                                         |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (with supplementary API)                                                                                                                                                                                   |
| **Logic**          | Fetch `Date/Publication` from `crandb.r-pkg.org/{package}/{version}` or `/{package}/all`. Parse the RFC-3339-like timestamp. Compare against `min_package_age_days` cutoff.                                    |
| **Fallback**       | If crandb is unavailable, use `Last-Modified` HTTP header from the tarball as an approximation. In `fail_mode: closed`, deny if no timestamp is available. In `fail_mode: open`, allow through with a warning. |
| **Cache**          | Cache crandb responses with the configured TTL to avoid hammering the third-party API.                                                                                                                         |

### 3.8 Bypass Age Filter

| Aspect             | Detail                                                                                  |
| ------------------ | --------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                     |
| **Logic**          | Standard: skip age check for packages matched by a rule with `bypass_age_filter: true`. |

### 3.9 Pinned Versions

| Aspect             | Detail                                                                          |
| ------------------ | ------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                             |
| **Logic**          | Standard: if version matches a `pinned_versions` entry, short-circuit to allow. |

### 3.10 Velocity Check

| Aspect             | Detail                                                                                                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                                |
| **Logic**          | Fetch all version timestamps from `crandb.r-pkg.org/{package}/all`. Count versions published within `window_hours` over the last `lookback_days`. If count exceeds `max_versions_in_window`, deny. |
| **Note**           | CRAN has strict manual review, so rapid version publishing is very rare. This rule still provides defence for R-universe and other repos that allow unrestricted publishing.                       |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                                                                                                                                                                  |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL (different mechanism)                                                                                                                                                                                           |
| **Logic**          | R packages use `configure`, `configure.win`, `cleanup`, and `cleanup.win` scripts in the tarball root. These run during `R CMD INSTALL`. The `NeedsCompilation` field in PACKAGES indicates if compilation hooks exist. |
| **Implementation** | Parse PACKAGES for `NeedsCompilation: yes`. For deeper inspection, the tarball would need to be downloaded and inspected for `configure`/`cleanup` scripts — expensive.                                                 |
| **Recommendation** | Use `NeedsCompilation: yes` as an initial signal. Allow more fine-grained inspection as an opt-in feature. Map `scripts["configure"]` / `scripts["cleanup"]` to the install-scripts check.                              |

### 3.12 License Filtering

| Aspect                           | Detail                                                                                                                                                                                                                                                                 |
| -------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**               | YES                                                                                                                                                                                                                                                                    |
| **Logic**                        | The `License` field is present in both PACKAGES and crandb. Note that R licences use non-standard SPDX-like strings: `GPL-2`, `GPL (>= 3)`, `MIT + file LICENSE`, `LGPL-2.1`, `Apache License 2.0`. Normalization is needed to compare with standard SPDX identifiers. |
| **Normalization map** (examples) | `"GPL-2"` → `"GPL-2.0-only"`, `"GPL (>= 3)"` → `"GPL-3.0-or-later"`, `"MIT + file LICENSE"` → `"MIT"`, `"Apache License 2.0"` → `"Apache-2.0"`                                                                                                                         |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                                                             |
| ------------------ | ---------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                |
| **Logic**          | Apply configured regex patterns to the version string. Standard implementation.    |
| **Example**        | `match: "^0\\.0\\."` with `action: deny` to block very early development versions. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                                                       |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (via crandb)                                                                                                                                                             |
| **Logic**          | Fetch crandb JSON. Check: `URL` field (repository URL), `License` field, `Description` field. Map to `missing_repository`, `missing_license`, `empty_description` anomalies. |
| **Note**           | CRAN PACKAGES file does include `License` but not `URL` or `Description` at all. Full metadata checks require crandb or DESCRIPTION file extraction from the tarball.        |

### 3.15 Dry-Run Mode

| Aspect             | Detail                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                           |
| **Logic**          | Standard: when `dry_run: true`, convert all deny decisions to allow, log with `DryRun: true`. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail                                                                                                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                           |
| **Logic**          | Standard: when `fail_mode: closed`, return 502 if metadata cannot be parsed or fetched. When `fail_mode: open`, proxy through with a warning. Especially important for crandb unavailability. |

---

## 4. PACKAGES File Filtering Algorithm

```
1. Fetch upstream PACKAGES (gzipped preferred for bandwidth).
2. Decompress if needed.
3. Parse into []PackageRecord (split on blank lines, parse key: value pairs).
4. For each record:
   a. Extract Package and Version fields.
   b. Call EvaluatePackage(PackageMeta{Name: pkg, Versions: allVersions}).
   c. If denied at package level, drop this record entirely.
   d. Build VersionMeta from crandb lookup (PublishedAt, License).
   e. Call EvaluateVersion(PackageMeta, VersionMeta).
   f. If denied, drop this record.
5. Reserialize remaining records into PACKAGES format.
6. Recompress if the client requested .gz.
7. Return with corrected Content-Length.
```

---

## 5. Configuration Example

```yaml
server:
  port: 18003

upstream:
  url: https://cloud.r-project.org
  timeout_seconds: 30

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "ggplot2"
    - "dplyr"
    - "tidyr"
    - "Rcpp"
  defaults:
    min_package_age_days: 7
    block_pre_releases: true
  install_scripts:
    enabled: true
    action: deny
    allowed_with_scripts:
      - "rJava"
      - "sf"
    reason: "R packages with compilation/configure scripts are blocked"
  rules:
    - name: deny-known-malicious
      package_patterns: ["malware-pkg*"]
      action: deny
      reason: "known malicious package"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
      bypass_age_filter: false
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses:
        [
          "MIT",
          "GPL-2.0-only",
          "GPL-3.0-or-later",
          "Apache-2.0",
          "LGPL-2.1-or-later",
        ]
  version_patterns:
    - name: block-dev-versions
      match: "\\.9000$"
      action: deny
      reason: "development versions (.9000) are not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 6. Ecosystem-Specific Considerations

1. **Third-party timestamp dependency:** Unlike npm/PyPI/Maven that embed timestamps in their metadata, CRAN requires a supplementary API (crandb) for publish dates. This introduces a runtime dependency on an external, community-maintained service. Consider caching aggressively and providing a fallback to `Last-Modified` headers.

2. **R-universe** (`https://r-universe.dev`): An alternative R package repository that does provide structured JSON APIs with timestamps. If users configure r-bulwark to proxy r-universe instead of CRAN, the timestamp limitation does not apply and the API is richer.

3. **Bioconductor** (`https://bioconductor.org`): Another major R package repository for bioinformatics. Uses a similar PACKAGES file layout but has its own release cycle. r-bulwark should be configurable to proxy Bioconductor by changing the upstream URL.

4. **PACKAGES.rds parsing:** The `.rds` format is R's native serialisation. Parsing it in Go requires a custom RDS reader (the format is documented but non-trivial). Prefer `.gz` for filtering; pass `.rds` through unmodified (with `fail_mode` semantics).

5. **Package names with periods:** R packages commonly use `.` in names (e.g., `data.table`, `R.utils`). Glob matching must handle `.` as a literal character, not a regex wildcard.

---

## 7. Rules NOT Applicable to R/CRAN

| Rule                            | Reason                                                                              |
| ------------------------------- | ----------------------------------------------------------------------------------- |
| **block_snapshots**             | R/CRAN has no SNAPSHOT versioning concept. Not applicable.                          |
| **Namespace protection** (full) | R has no formal namespace/scope system. Only heuristic prefix matching is possible. |
