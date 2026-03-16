# Hex (Erlang/Elixir) Bulwark — Implementation Specification

## Overview

**Ecosystem:** Hex (Erlang / Elixir)  
**Default upstream:** `https://hex.pm`  
**Repository mirror:** `https://repo.hex.pm`  
**Default port:** 18012  
**Binary name:** `hex-bulwark`  
**Client tools:** `mix deps.get` (Elixir), `rebar3` (Erlang)

Hex.pm is the package manager for the Erlang/Elixir ecosystem (BEAM VM). It has
a clean, well-documented HTTP API and a separate repository endpoint for package
downloads. Hex is used by critical infrastructure (telecom, messaging, financial
systems) where supply chain security is particularly important.

---

## 1. Hex.pm API Endpoints

### 1.1 Package Metadata

| Pattern                | Upstream URL                         | Description                             |
| ---------------------- | ------------------------------------ | --------------------------------------- |
| `/api/packages/{name}` | `https://hex.pm/api/packages/{name}` | Full package metadata with all releases |

Response:

```json
{
  "name": "phoenix",
  "url": "https://hex.pm/api/packages/phoenix",
  "html_url": "https://hex.pm/packages/phoenix",
  "docs_html_url": "https://hexdocs.pm/phoenix",
  "meta": {
    "description": "Peace of mind from prototype to production.",
    "licenses": ["MIT"],
    "links": {
      "GitHub": "https://github.com/phoenixframework/phoenix"
    },
    "maintainers": []
  },
  "releases": [
    {
      "version": "1.7.21",
      "url": "https://hex.pm/api/packages/phoenix/releases/1.7.21",
      "inserted_at": "2025-04-24T18:11:49.671399Z",
      "has_docs": true
    },
    {
      "version": "1.7.20",
      "inserted_at": "2025-03-18T17:14:41.342127Z",
      ...
    }
  ]
}
```

### 1.2 Release (Version) Details

| Pattern                                   | Description               |
| ----------------------------------------- | ------------------------- |
| `/api/packages/{name}/releases/{version}` | Detailed release metadata |

Response:

```json
{
  "version": "1.7.21",
  "inserted_at": "2025-04-24T18:11:49.671399Z",
  "requirements": {
    "phoenix_pubsub": {
      "requirement": "~> 2.1",
      "optional": false,
      "app": "phoenix_pubsub"
    },
    "plug": { "requirement": "~> 1.14", "optional": false, "app": "plug" }
  },
  "meta": {
    "app": "phoenix",
    "build_tools": ["mix"],
    "elixir": "~> 1.14"
  },
  "has_docs": true,
  "retirement": null
}
```

### 1.3 Package Tarball Download

| Pattern                          | Upstream URL                                        | Description     |
| -------------------------------- | --------------------------------------------------- | --------------- |
| `/tarballs/{name}-{version}.tar` | `https://repo.hex.pm/tarballs/{name}-{version}.tar` | Package tarball |

The tarball is a custom format containing:

- `metadata.config` — Erlang term file with package metadata
- `contents.tar.gz` — Gzipped source code
- `CHECKSUM` — SHA-256 checksum
- `VERSION` — Tarball format version

### 1.4 Registry Resources

| Pattern                         | Description                                         |
| ------------------------------- | --------------------------------------------------- |
| `/repos/{repo}/names`           | ETS (Erlang Term Storage) encoded package name list |
| `/repos/{repo}/versions`        | ETS encoded version list for all packages           |
| `/repos/{repo}/packages/{name}` | ETS encoded package metadata                        |

**Note:** Mix/rebar3 primarily use these protobuf/ETS-encoded resources for
resolution, not the JSON API. However, the JSON API provides richer data for
rule evaluation.

### 1.5 Search

| Pattern                        | Description        |
| ------------------------------ | ------------------ |
| `/api/packages?search={term}`  | Search packages    |
| `/api/packages?sort=downloads` | List by popularity |

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /api/packages/{name}                    → handlePackageMetadata
GET /api/packages/{name}/releases/{version} → handleRelease
GET /tarballs/{name}-{version}.tar          → handleTarballDownload
GET /repos/{repo}/names                     → handleRepoNames (pass-through or filter)
GET /repos/{repo}/versions                  → handleRepoVersions (pass-through or filter)
GET /repos/{repo}/packages/{name}           → handleRepoPackage
GET /health                                 → healthHandler
GET /readyz                                 → readyzHandler
GET /metrics                                → metricsHandler
```

### 2.2 Package Name & Version Extraction

- Package names are lowercase, may contain `_` only (no hyphens).
- Extract from URL path: `/api/packages/{name}` and `/tarballs/{name}-{version}.tar`.
- For tarball URLs, split on the last `-` before `.tar`.

### 2.3 Metadata Retrieval Strategy

**For `handlePackageMetadata` (JSON API filtering):**

1. Fetch upstream `/api/packages/{name}`.
2. Parse JSON.
3. For each release:
   a. Extract `version`, `inserted_at` (timestamp), and metadata fields.
   b. Evaluate rules.
   c. Remove denied releases.
4. Return filtered JSON.

**For `handleTarballDownload` (artifact guard):**

1. Extract package name and version from URL.
2. Fetch package metadata (from API or cache).
3. Evaluate rules.
4. If denied, return 403. If allowed, proxy the tarball from `repo.hex.pm`.

**For `handleRepoPackage` (ETS/protobuf):**
The Mix/rebar3 clients use ETS-encoded repository resources. Filtering these
requires parsing the Hex protobuf/ETS format, which is non-trivial.
Options:

1. Parse and filter the protobuf-encoded data (complex).
2. Pass through repository resources but enforce rules at the tarball download
   layer (simpler, but allows metadata to leak).
3. Serve the JSON API and configure clients to use the API endpoint.

**Recommendation:** Enforce at both the metadata (JSON API) and tarball download
layers. For ETS/protobuf resources, pass through initially and add filtering
as an enhancement.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                       |
| ------------------ | ------------------------------------------------------------ |
| **Implementable?** | YES                                                          |
| **Logic**          | Match package name against `trusted_packages` globs.         |
| **Example**        | `"phoenix*"`, `"ecto*"`, `"plug*"`, `"jason"`, `"telemetry"` |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                  |
| ------------------ | --------------------------------------- |
| **Implementable?** | YES                                     |
| **Logic**          | Standard glob matching on package name. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                    |
| ------------------ | ------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                       |
| **Logic**          | Normalize name (lowercase, strip `_`), compute Levenshtein distance.      |
| **Note**           | Hex has ~14,000 packages — a manageable size for typosquatting detection. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                          |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                         |
| **Reason**         | Hex packages are flat-named (no scopes/vendors). However, Hex supports **organizations** (`/repos/{org}/packages/{name}`) for private packages. |
| **Workaround**     | Prefix matching: `phoenix_*`, `ecto_*`, `plug_*`. Organization repos provide stronger namespace isolation.                                      |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                |
| -------------------------- | --------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                   |
| **Logic**                  | Hex uses SemVer. Pre-release versions: `1.0.0-alpha.1`, `2.0.0-rc.1`. |
| **Custom `IsPreRelease`:** | Standard SemVer pre-release detection.                                |

### 3.6 Snapshot Blocking

| Aspect             | Detail                       |
| ------------------ | ---------------------------- |
| **Implementable?** | NOT APPLICABLE               |
| **Reason**         | Hex has no SNAPSHOT concept. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                      |
| ------------------ | ----------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                         |
| **Logic**          | The `inserted_at` field provides an ISO 8601 timestamp per release. Compare against `min_package_age_days`. |
| **Data source**    | Directly in the API metadata — no supplementary API needed.                                                 |

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
| **Logic**          | All release timestamps in the package metadata. Count within window. |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                            |
| **Logic**          | Erlang/Elixir does not have install scripts in the npm sense. However:                                                                             |
|                    | - Hex packages can include **native extensions** (NIFs — Native Implemented Functions) that compile C/Rust code.                                   |
|                    | - The `meta.build_tools` field indicates the build system (`mix`, `rebar3`, `make`). Packages using `make` are more likely to compile native code. |
|                    | - The tarball's `contents.tar.gz` can be inspected for `Makefile`, `c_src/`, or `native/` directories.                                             |
| **Recommendation** | Check `meta.build_tools` for `make` as a signal. For deeper inspection, examine tarball contents for NIF indicators.                               |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                |
| **Logic**          | The `meta.licenses` array contains SPDX-like licence strings (e.g., `["MIT"]`, `["Apache-2.0"]`). Check each element against allowed/denied lists. |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                     |
| ------------------ | ------------------------------------------ |
| **Implementable?** | YES                                        |
| **Logic**          | Standard regex matching on version string. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                                                        |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                           |
| **Logic**          | From API metadata: `meta.links.GitHub` or equivalent → missing_repository, `meta.licenses` → missing_license (check for empty array), `meta.description` → empty_description. |

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
3. Build PackageMeta{Name: name, Versions: allVersionMetas from releases}.
4. Call EvaluatePackage(). If denied, return 404.
5. For each release:
   a. Extract version, inserted_at, meta.licenses, has_docs.
   b. Build VersionMeta{Version, PublishedAt: inserted_at, License: licenses[0]}.
   c. Call EvaluateVersion().
   d. If denied, remove from releases array.
6. Return filtered JSON.
```

---

## 5. Retirement Status Handling

Hex supports **version retirement** — a mechanism to mark versions as deprecated,
insecure, or invalid without removing them.

Retirement response:

```json
{
  "retirement": {
    "reason": "security",
    "message": "CVE-2024-XXXX: XSS vulnerability"
  }
}
```

Retirement reasons: `other`, `invalid`, `security`, `deprecated`, `renamed`.

**Recommendation:** Optionally deny retired versions, especially those with
`reason: "security"`. This could be a Hex-specific rule or handled as a version
pattern/metadata anomaly.

---

## 6. Configuration Example

```yaml
server:
  port: 18012

upstream:
  url: https://hex.pm
  timeout_seconds: 30
  allowed_external_hosts:
    - "repo.hex.pm"

cache:
  ttl_seconds: 300
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "phoenix*"
    - "ecto*"
    - "plug*"
    - "jason"
    - "telemetry"
    - "bandit"
    - "oban*"
    - "absinthe*"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  install_scripts:
    enabled: true
    action: warn
    allowed_with_scripts:
      - "comeonin"
      - "bcrypt_elixir"
      - "argon2_elixir"
    reason: "package includes native extensions (NIFs)"
  rules:
    - name: deny-malicious
      package_patterns: ["malware_*"]
      action: deny
      reason: "known malicious package"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses: ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause"]
    - name: typosquat-guard
      package_patterns: ["*"]
      typosquat_check:
        enabled: true
        max_levenshtein_dist: 2
        protected_packages:
          - "phoenix"
          - "ecto"
          - "plug"
          - "jason"
          - "oban"
          - "absinthe"
  version_patterns:
    - name: block-rc
      match: "-(rc|alpha|beta)"
      action: deny
      reason: "pre-release versions not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 7. Ecosystem-Specific Considerations

1. **ETS/protobuf parsing:** Mix and rebar3 primarily use the binary ETS/protobuf-encoded repository resources for resolution, not the JSON API. Full proxy coverage requires parsing this format. The Hex specification defines the protobuf schema.

2. **Dual upstream hosts:** The API is on `hex.pm` and tarballs are on `repo.hex.pm`. The proxy must handle both or route tarball requests separately.

3. **Organization repositories:** Hex supports private organization repos at `/repos/{org}/packages/{name}`. The proxy should support organization routing for enterprise deployments.

4. **Version retirement:** A unique Hex feature. Consider adding a dedicated rule to block retired versions, especially those retired for security reasons.

5. **OTP applications:** Hex packages are OTP applications. The `meta.app` field identifies the OTP application name. This can differ from the package name — important for name matching.

6. **Mix.lock integrity:** Mix generates `mix.lock` with SHA-256 checksums. The proxy should preserve tarball integrity — any modification to the tarball (even re-compression) would break checksums.

7. **Telecom/infrastructure use:** Erlang/Elixir is used in critical infrastructure (WhatsApp, Discord, telecom switches). Supply chain attacks here could have outsized impact. Consider defaulting to `fail_mode: closed` for Hex deployments.

---

## 8. Rules NOT Applicable to Hex

| Rule                            | Reason                                                                                   |
| ------------------------------- | ---------------------------------------------------------------------------------------- |
| **block_snapshots**             | Hex has no SNAPSHOT concept.                                                             |
| **install_scripts** (full)      | Erlang/Elixir has no lifecycle scripts. NIF detection is partial.                        |
| **Namespace protection** (full) | Hex public packages are flat-named. Only organization repos provide namespace isolation. |
