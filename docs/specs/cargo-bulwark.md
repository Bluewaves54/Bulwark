# Cargo (Rust) Bulwark — Implementation Specification

## Overview

**Ecosystem:** Cargo / crates.io (Rust)  
**Default upstream:** `https://crates.io`  
**Default port:** 18005  
**Binary name:** `cargo-bulwark`  
**Client tools:** `cargo install`, `cargo build`, `cargo add`

Cargo resolves dependencies via the crates.io **registry index** (a Git repo or
sparse HTTP index) and downloads crate tarballs. The API provides rich structured
JSON metadata including timestamps, licence, and publisher information.

---

## 1. crates.io API Endpoints

### 1.1 Crate Metadata

| Pattern                           | Upstream URL                                       | Description                             |
| --------------------------------- | -------------------------------------------------- | --------------------------------------- |
| `/api/v1/crates/{name}`           | `https://crates.io/api/v1/crates/{name}`           | Full crate metadata with latest version |
| `/api/v1/crates/{name}/versions`  | `https://crates.io/api/v1/crates/{name}/versions`  | All versions with timestamps            |
| `/api/v1/crates/{name}/{version}` | `https://crates.io/api/v1/crates/{name}/{version}` | Single version metadata                 |

Example response (`/api/v1/crates/serde`):

```json
{
  "crate": {
    "id": "serde",
    "name": "serde",
    "description": "A generic serialization/deserialization framework",
    "license": "MIT OR Apache-2.0",
    "repository": "https://github.com/serde-rs/serde",
    "created_at": "2015-01-28T22:55:43.062952+00:00",
    "updated_at": "2025-05-12T...",
    "max_version": "1.0.219",
    "max_stable_version": "1.0.219"
  },
  "versions": [
    {
      "id": 123456,
      "crate": "serde",
      "num": "1.0.219",
      "created_at": "2025-05-12T...",
      "yanked": false,
      "license": "MIT OR Apache-2.0",
      "crate_size": 80123,
      "published_by": { "id": 1, "login": "dtolnay", "name": "David Tolnay" }
    }
  ]
}
```

### 1.2 Crate Download

| Pattern                                    | Upstream URL                                                | Description                                                                         |
| ------------------------------------------ | ----------------------------------------------------------- | ----------------------------------------------------------------------------------- |
| `/api/v1/crates/{name}/{version}/download` | `https://crates.io/api/v1/crates/{name}/{version}/download` | **302 redirect** to `https://static.crates.io/crates/{name}/{name}-{version}.crate` |

The download endpoint returns a **302 redirect** to the static CDN. The proxy
must either:

- Follow the redirect and proxy the response, OR
- Intercept the redirect and validate before allowing the client to follow it

### 1.3 Sparse Registry Index

Modern Cargo (1.68+) uses a **sparse HTTP index** instead of cloning the full
Git index. The index base URL is `https://index.crates.io`.

| Pattern            | Description                          |
| ------------------ | ------------------------------------ |
| `/config.json`     | Index configuration                  |
| `/{prefix}/{name}` | Package metadata in the index format |

Prefix rules (based on crate name length):

- 1 char: `/1/{name}`
- 2 chars: `/2/{name}`
- 3 chars: `/3/{first-char}/{name}`
- 4+ chars: `/{first-two}/{next-two}/{name}`

Example: `serde` → `/se/rd/serde`

Each line in the index file is a JSON object:

```json
{"name":"serde","vers":"1.0.219","deps":[...],"cksum":"...","features":{},"yanked":false}
```

**Note:** The sparse index does NOT include timestamps, licence, or publisher
info. It only has version, dependencies, features, checksum, and yanked status.
Full metadata requires the API.

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /api/v1/crates/{name}            → handleCrateMetadata
GET /api/v1/crates/{name}/versions   → handleCrateVersions
GET /api/v1/crates/{name}/{version}  → handleCrateVersion
GET /api/v1/crates/{name}/{version}/download → handleCrateDownload
GET /1/{name}                        → handleSparseIndex (1-char crate)
GET /2/{name}                        → handleSparseIndex (2-char crate)
GET /3/{c}/{name}                    → handleSparseIndex (3-char crate)
GET /{ab}/{cd}/{name}                → handleSparseIndex (4+ char crate)
GET /config.json                     → handleSparseConfig
GET /health                          → healthHandler
GET /readyz                          → readyzHandler
GET /metrics                         → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Crate names are case-sensitive but conventionally lowercase.
- Names may contain letters, digits, `-`, and `_`. Hyphens and underscores are
  treated as equivalent by Cargo (e.g., `serde-json` == `serde_json`).
- Extract from URL path segments.

### 2.3 Metadata Retrieval Strategy

**For `handleCrateMetadata` / `handleCrateVersions`:**

1. Fetch upstream API response.
2. Parse JSON.
3. For each version, build `VersionMeta` from `created_at`, `license`, `yanked`.
4. Evaluate package-level and version-level rules.
5. Remove denied versions from the response JSON.
6. Return filtered response.

**For `handleCrateDownload` (artifact guard):**

1. Extract crate name and version from URL.
2. Fetch crate metadata for validation.
3. Evaluate rules.
4. If allowed, proxy the download (follow the 302 redirect internally, stream
   the `.crate` to the client).
5. If denied, return 403.

**For `handleSparseIndex` (index filtering):**

1. Fetch upstream sparse index file.
2. Parse each line (JSON per line).
3. For each version, evaluate rules (limited: no timestamps in sparse index).
4. Remove denied lines.
5. Return filtered index.
6. **Note:** For age-based and licence-based rules, fall back to the API
   endpoint since the sparse index lacks this data.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                                            |
| ------------------ | --------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                               |
| **Logic**          | Match crate name against `trusted_packages` globs. Normalize `-`/`_` equivalence. |
| **Example**        | `"serde*"`, `"tokio*"`, `"rand"`                                                  |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                                                  |
| ------------------ | --------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                     |
| **Logic**          | Standard glob matching on crate name.                                                   |
| **Note**           | Must handle `-`/`_` equivalence: `serde-json` and `serde_json` refer to the same crate. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                     |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                        |
| **Logic**          | Normalize name (lowercase, replace `-` and `_` with empty), compute Levenshtein distance.                                                  |
| **Note**           | crates.io already prevents exactly one-character-off names, but a proxy-level check adds defence in depth and protects private registries. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                              |
| ------------------ | --------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                             |
| **Reason**         | Rust/Cargo has no formal namespace system. Crate names are flat (no scopes, no group IDs).          |
| **Workaround**     | Convention-based prefix matching (e.g., `tokio-*`, `aws-sdk-*`). Match against `internal_patterns`. |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                                             |
| -------------------------- | -------------------------------------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                                                |
| **Logic**                  | Cargo uses SemVer 2.0. Pre-release versions have a `-` suffix: `1.0.0-alpha.1`, `0.1.0-rc.2`.      |
| **Custom `IsPreRelease`:** | Standard SemVer pre-release detection: check for `-` after the version core (`major.minor.patch`). |

### 3.6 Snapshot Blocking

| Aspect             | Detail                                                       |
| ------------------ | ------------------------------------------------------------ |
| **Implementable?** | NOT APPLICABLE                                               |
| **Reason**         | Cargo/crates.io does not have a SNAPSHOT versioning concept. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect                | Detail                                                                                                                                                                                             |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**    | YES                                                                                                                                                                                                |
| **Logic**             | The `created_at` field in the versions response provides an ISO 8601 timestamp per version. Compare against `min_package_age_days`.                                                                |
| **Data source**       | `/api/v1/crates/{name}/versions` — timestamps are directly in the primary metadata.                                                                                                                |
| **Sparse index note** | The sparse index does not include timestamps. If the proxy is primarily serving sparse index requests, it must call the API endpoint as a side-channel to get timestamps for age-based evaluation. |

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

| Aspect             | Detail                                                                                                              |
| ------------------ | ------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                 |
| **Logic**          | Fetch all version timestamps from the versions endpoint. Count versions within `window_hours` over `lookback_days`. |
| **Note**           | crates.io allows rapid publishing (unlike CRAN), making velocity detection particularly valuable.                   |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                                                                                                                                                                                            |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (via build scripts)                                                                                                                                                                                                                           |
| **Logic**          | Rust crates can include `build.rs` — a build script that runs arbitrary code at compile time. The presence of `build.rs` is signalled by `"links"` key in the sparse index or by inspecting the `.crate` tarball.                                 |
| **Implementation** | Option 1: Check for `links` field in crate metadata (indicates a build script that links to a native library). Option 2: Download and inspect `.crate` tarball for `build.rs`. Option 3: Check sparse index line for `links` field.               |
| **Scope**          | `build.rs` is very common in Rust (used for code generation, native compilation). This check is most useful as a warning rather than a blanket block. `allowed_with_scripts` should include common crates like `openssl-sys`, `ring`, `libz-sys`. |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                                            |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                               |
| **Logic**          | The `license` field in both API and sparse index contains SPDX expressions (e.g., `"MIT OR Apache-2.0"`, `"MIT"`, `"Apache-2.0"`). For compound expressions, split on `OR`/`AND`. |
| **Note**           | crates.io enforces valid SPDX expressions, so parsing is reliable.                                                                                                                |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                     |
| ------------------ | ------------------------------------------ |
| **Implementable?** | YES                                        |
| **Logic**          | Standard regex matching on version string. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                                                                                  |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                                                     |
| **Logic**          | From API metadata: check `repository` (missing_repository), `license` (missing_license), `description` (empty_description). Also consider checking `published_by` — anonymous publishes are a red flag. |

### 3.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail                                                 |
| ------------------ | ------------------------------------------------------ |
| **Implementable?** | YES                                                    |
| **Logic**          | Standard. Important for sparse index parsing failures. |

---

## 4. Sparse Index Filtering Algorithm

```
1. Determine crate name from URL path (apply prefix rules).
2. Fetch upstream sparse index file.
3. Split into lines (each line is a JSON object).
4. For each line:
   a. Parse JSON to extract name, vers, yanked, links.
   b. If yanked == true, optionally filter (allow or deny based on policy).
   c. Call EvaluatePackage() with package-level checks only.
   d. For age/licence checks, call the API as side-channel: GET /api/v1/crates/{name}/versions.
   e. Call EvaluateVersion() with enriched VersionMeta.
   f. If denied, remove the line.
5. Return filtered multiline response.
```

---

## 5. Download Redirect Handling

crates.io returns `302 Found` for download requests, redirecting to `static.crates.io`.

**Proxy strategy:**

1. Intercept the `/api/v1/crates/{name}/{version}/download` request.
2. Evaluate rules BEFORE following the redirect.
3. If allowed, follow the redirect internally using `http.Client` (not `CheckRedirect`).
4. Stream the `.crate` file back to the client.
5. Do NOT expose the redirect to the client — this ensures all traffic flows through the proxy.

Alternatively, add `static.crates.io` to `allowed_external_hosts` and handle it similarly to `handleExternal` in pypi-bulwark.

---

## 6. Configuration Example

```yaml
server:
  port: 18005

upstream:
  url: https://crates.io
  timeout_seconds: 30
  allowed_external_hosts:
    - "static.crates.io"
    - "index.crates.io"

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "serde*"
    - "tokio*"
    - "rand"
    - "clap"
    - "log"
    - "tracing*"
  defaults:
    min_package_age_days: 3
    block_pre_releases: false
  install_scripts:
    enabled: true
    action: warn
    allowed_with_scripts:
      - "openssl-sys"
      - "ring"
      - "libz-sys"
      - "cc"
    reason: "crate has a build.rs build script"
  rules:
    - name: deny-known-malicious
      package_patterns: ["malware-*"]
      action: deny
      reason: "known malicious crate"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 7
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses:
        ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause", "ISC"]
  version_patterns:
    - name: block-yanked-suffix
      match: "\\+yanked"
      action: deny
      reason: "yanked version metadata suffix"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 7. Ecosystem-Specific Considerations

1. **Hyphen/underscore equivalence:** Cargo treats `-` and `_` as identical in crate names. `serde-json` and `serde_json` are the same crate. All name comparisons must normalize this.

2. **Yanked versions:** crates.io marks bad versions as "yanked" rather than deleting them. Yanked crates are still downloadable but hidden from resolution. Consider an optional rule to block yanked versions.

3. **302 redirect downloads:** Unlike npm/PyPI/Maven which serve artifacts directly, crates.io redirects downloads to a CDN. The proxy must follow redirects internally.

4. **Sparse index vs API:** Cargo uses the sparse index for dependency resolution (fast, minimal) and the API for human-readable metadata (rich, slower). The proxy must support both paths with consistent policy enforcement.

5. **`build.rs` prevalence:** Build scripts are very common in Rust. A blanket block would be impractical. Recommend `action: warn` as the default with an explicit allowlist.

6. **Rate limiting:** crates.io requires a `User-Agent` header and rate-limits API calls. The proxy should set a descriptive User-Agent and respect rate limits. Cache API responses aggressively.

---

## 8. Rules NOT Applicable to Cargo

| Rule                            | Reason                                                                                  |
| ------------------------------- | --------------------------------------------------------------------------------------- |
| **block_snapshots**             | Cargo/crates.io has no SNAPSHOT versioning concept.                                     |
| **Namespace protection** (full) | Cargo has no formal namespace/scope system. Only heuristic prefix matching is possible. |
