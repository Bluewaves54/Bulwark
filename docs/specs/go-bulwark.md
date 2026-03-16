# Go Modules Bulwark — Implementation Specification

## Overview

**Ecosystem:** Go modules (GOPROXY protocol)  
**Default upstream:** `https://proxy.golang.org`  
**Checksum database:** `https://sum.golang.org`  
**Default port:** 18007  
**Binary name:** `go-bulwark`  
**Client tools:** `go get`, `go mod download`, `go build`, `go install`

Go modules use a well-defined **GOPROXY protocol** (documented at
`go.dev/ref/mod#goproxy-protocol`). The proxy protocol is simple: 4 endpoints
per module version. The Go team's official proxy (`proxy.golang.org`) acts as a
caching proxy for all public Go modules.

---

## 1. GOPROXY Protocol Endpoints

### 1.1 Module Version Listing

| Pattern             | Description                              |
| ------------------- | ---------------------------------------- |
| `/{module}/@v/list` | Newline-delimited list of known versions |

Response (plain text):

```
v1.0.0
v1.1.0
v1.2.0
v1.2.1
```

### 1.2 Version Info

| Pattern                       | Description                     |
| ----------------------------- | ------------------------------- |
| `/{module}/@v/{version}.info` | JSON with version and timestamp |

Response:

```json
{
  "Version": "v1.2.1",
  "Time": "2025-03-15T10:30:00Z",
  "Origin": {
    "VCS": "git",
    "URL": "https://github.com/example/module",
    "Ref": "refs/tags/v1.2.1",
    "Hash": "abc123..."
  }
}
```

### 1.3 Module File (go.mod)

| Pattern                      | Description                        |
| ---------------------------- | ---------------------------------- |
| `/{module}/@v/{version}.mod` | The `go.mod` file for this version |

Response (plain text):

```
module github.com/example/module

go 1.21

require (
    github.com/some/dep v1.0.0
)
```

### 1.4 Module Source Archive

| Pattern                      | Description                      |
| ---------------------------- | -------------------------------- |
| `/{module}/@v/{version}.zip` | ZIP archive of the module source |

### 1.5 Latest Version Query

| Pattern             | Description                                        |
| ------------------- | -------------------------------------------------- |
| `/{module}/@latest` | JSON: latest version info (same format as `.info`) |

### 1.6 Checksum Database (sum.golang.org)

| Pattern                      | Description                               |
| ---------------------------- | ----------------------------------------- |
| `/lookup/{module}@{version}` | Hash and tree position for module+version |
| `/tile/{H}/{L}/{K}`          | Merkle tree tile                          |
| `/latest`                    | Latest signed tree head                   |

**Note:** The checksum database provides tamper-proof verification. The proxy
should pass these through unmodified — integrity verification is the Go toolchain's
responsibility.

---

## 2. Module Path Encoding

Go module paths contain `/` characters (e.g., `github.com/gorilla/mux`). In
GOPROXY URLs, uppercase letters in module paths are escaped as `!{lowercase}`:

- `github.com/Azure/azure-sdk-for-go` → `github.com/!azure/azure-sdk-for-go`

The proxy must decode this encoding when extracting module names for rule evaluation,
and preserve encoding when proxying to upstream.

---

## 3. Proxy Architecture

### 3.1 Handler Registration

```
GET /{module...}/@v/list             → handleVersionList
GET /{module...}/@v/{version}.info   → handleVersionInfo
GET /{module...}/@v/{version}.mod    → handleModFile
GET /{module...}/@v/{version}.zip    → handleModuleZip
GET /{module...}/@latest             → handleLatest
GET /sumdb/sum.golang.org/*          → handleSumDB (pass-through)
GET /health                          → healthHandler
GET /readyz                          → readyzHandler
GET /metrics                         → metricsHandler
```

### 3.2 Module Name & Version Extraction

- Module path: everything before `/@v/` in the URL path.
- Version: segment after `/@v/` and before the extension (`.info`, `.mod`, `.zip`).
- Decode `!x` → `X` for case-folding.
- Module paths are hierarchical: `github.com/org/repo/subpackage`.

### 3.3 Metadata Retrieval Strategy

**For `handleVersionList` (list filtering):**

1. Fetch upstream `/@v/list`.
2. For each version, fetch `.info` to get the `Time` (timestamp).
3. Evaluate rules for each version.
4. Return filtered version list.

**For `handleVersionInfo` / `handleModFile` / `handleModuleZip` (per-version guard):**

1. Extract module path and version from URL.
2. Fetch `.info` for the timestamp.
3. Evaluate `EvaluatePackage()` + `EvaluateVersion()`.
4. If denied, return 404 (GOPROXY protocol: 404/410 means "version not available").
5. If allowed, proxy the upstream response.

**Important:** GOPROXY protocol returns 404 for denied resources (not 403). The
Go toolchain interprets 404/410 as "try the next proxy or direct fetch". Return
410 Gone for hard denials to prevent fallback to direct.

---

## 4. Rule Implementation Matrix

### 4.1 Trusted Packages

| Aspect             | Detail                                                               |
| ------------------ | -------------------------------------------------------------------- |
| **Implementable?** | YES                                                                  |
| **Logic**          | Match module path against `trusted_packages` globs.                  |
| **Example**        | `"github.com/golang/*"`, `"golang.org/x/*"`, `"google.golang.org/*"` |

### 4.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                |
| **Logic**          | Glob matching on the full module path (e.g., `github.com/evil/malware`).                                                                           |
| **Note**           | Module paths are hierarchical. Consider supporting `github.com/org/*` to match all repos under an org, and `github.com/org/repo/*` for submodules. |

### 4.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                                                                                                |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (with caveats)                                                                                                                                                                                                    |
| **Logic**          | Extract the repository name portion (last path segment) and compare against protected packages using Levenshtein distance.                                                                                            |
| **Caveat**         | Go module paths include the hosting domain (e.g., `github.com/gorilla/mux`). Typosquatting typically targets the repository name, not the full path. Compare only the final path component or the `org/repo` portion. |
| **Example**        | Protect `gorilla/mux` → detect `gorilla/muxx`, `gorila/mux`, `gorilla/muc`.                                                                                                                                           |

### 4.4 Namespace Protection

| Aspect             | Detail                                                                                                                                          |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES — STRONG FIT                                                                                                                                |
| **Logic**          | Go module paths naturally encode namespaces: `github.com/{org}/{repo}`. Internal patterns can match org-level paths: `github.com/my-company/*`. |
| **Note**           | This is the strongest namespace protection of any ecosystem because the module path contains the VCS host and organization.                     |

### 4.5 Pre-release Blocking

| Aspect                     | Detail                                                                                                                                                                    |
| -------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**         | YES                                                                                                                                                                       |
| **Logic**                  | Go uses SemVer with `v` prefix. Pre-release: `v1.0.0-beta.1`, `v2.0.0-rc.1`. Also pseudo-versions: `v0.0.0-20230101000000-abcdef123456` (generated for untagged commits). |
| **Custom `IsPreRelease`:** | Check for `-` after the version core. Also consider pseudo-versions as a separate category — they indicate dependency on an untagged commit, which is a risk signal.      |

### 4.6 Snapshot Blocking

| Aspect             | Detail                                                                                                                                                                                             |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL — as pseudo-version blocking                                                                                                                                                               |
| **Logic**          | Go pseudo-versions (e.g., `v0.0.0-20230101000000-abcdef123456`) are analogous to snapshots — they reference a specific unreleased commit. Detect with regex: `v\d+\.\d+\.\d+-\d{14}-[a-f0-9]{12}`. |
| **Recommendation** | Implement as a version pattern rule or a dedicated `block_pseudo_versions` check.                                                                                                                  |

### 4.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                                                                                            |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                                               |
| **Logic**          | The `.info` endpoint returns a `Time` field with an RFC 3339 timestamp. Compare against `min_package_age_days`.                                                                   |
| **Data source**    | Directly from the `.info` endpoint — no supplementary API needed.                                                                                                                 |
| **Note**           | The `Time` field reflects when the version was first cached by the Go module proxy, not necessarily when it was tagged in Git. For most practical purposes, this is close enough. |

### 4.8 Bypass Age Filter

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 4.9 Pinned Versions

| Aspect             | Detail                                                                            |
| ------------------ | --------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                               |
| **Logic**          | Standard. Note Go versions include the `v` prefix: `pinned_versions: ["v1.2.3"]`. |

### 4.10 Velocity Check

| Aspect             | Detail                                                                                                             |
| ------------------ | ------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                |
| **Logic**          | Fetch `/@v/list` to get all versions, then fetch `.info` for each to get timestamps. Count versions within window. |
| **Optimization**   | Cache `.info` responses. The version list is small for most modules.                                               |

### 4.11 Install Scripts Detection

| Aspect                  | Detail                                                                                                                                                                                                                                             |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**      | NOT DIRECTLY APPLICABLE                                                                                                                                                                                                                            |
| **Reason**              | Go does not have install/lifecycle scripts like npm. There are no `preinstall`, `postinstall`, or equivalent hooks in Go modules.                                                                                                                  |
| **Partial alternative** | Some Go modules use `go:generate` directives that execute arbitrary commands, but these only run when explicitly invoked with `go generate`, not during `go build` or `go install`.                                                                |
| **`cgo`**               | Modules using cgo execute C compiler toolchain during build. Detecting cgo usage requires inspecting source files for `import "C"` or `#cgo` directives. This is a partial analogue but not a direct security risk in the same way as npm scripts. |
| **Recommendation**      | Skip install scripts detection for Go. Optionally add a cgo detection rule as a separate, Go-specific extension.                                                                                                                                   |

### 4.12 License Filtering

| Aspect                     | Detail                                                                                                                                                                                                                     |
| -------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**         | PARTIAL (requires source inspection)                                                                                                                                                                                       |
| **Logic**                  | The GOPROXY protocol does NOT include licence information in any endpoint (`.info`, `.mod`, `.zip`).                                                                                                                       |
| **Implementation options** | Option 1: Fetch the `.zip`, extract the `LICENSE` file, and classify. Option 2: Use `pkg.go.dev` API (`GET /api/packages/{module}@{version}`) if available. Option 3: Use `go-licenses` tool or a SPDX classifier library. |
| **Limitation**             | This is significantly more expensive than other ecosystems. Consider making licence checks opt-in with a clearly documented performance warning.                                                                           |

### 4.13 Version Patterns (regex)

| Aspect             | Detail                                                                                         |
| ------------------ | ---------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                            |
| **Logic**          | Standard regex matching. Useful for blocking pseudo-versions: `match: "^v0\\.0\\.0-\\d{14}-"`. |

### 4.14 Metadata Anomaly Checks

| Aspect               | Detail                                                                                                                                                                                          |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**   | PARTIAL                                                                                                                                                                                         |
| **Logic**            | The `.info` response includes `Origin.URL` (repository URL). Can check for missing VCS origin. The `.mod` file provides the module declaration and dependencies but not description or licence. |
| **Available checks** | `missing_repository`: check `Origin.URL` or `Origin.VCS` in `.info`. `missing_license` and `empty_description`: NOT available without downloading the `.zip`.                                   |

### 4.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 4.16 Fail-Closed Mode

| Aspect             | Detail                                                                               |
| ------------------ | ------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                  |
| **Logic**          | Standard. Return 410 Gone (not 404) in fail-closed to prevent Go toolchain fallback. |

---

## 5. GOPROXY Response Code Semantics

| Status        | Go toolchain behaviour                                                        |
| ------------- | ----------------------------------------------------------------------------- |
| 200           | Success — use the response.                                                   |
| 404           | Version not found — **try next GOPROXY** in the list, or fall back to direct. |
| 410           | Version gone — **do NOT try next proxy** or direct. Hard denial.              |
| Other 4xx/5xx | Error — try next proxy.                                                       |

**Recommendation:** Use 410 for policy denials to prevent circumvention via
GOPROXY fallback. Use 404 only for genuinely unknown modules.

---

## 6. GONOSUMCHECK & Checksum Considerations

The Go toolchain verifies module checksums against `sum.golang.org`. The proxy
should pass through sumdb requests unmodified. However, consider:

- If a denied version's checksum is requested, the sumdb endpoint still returns
  it. This is fine — the client won't have the `.zip` to verify against.
- If `GONOSUMCHECK` is set for internal modules, the proxy should still enforce
  all policy rules.

---

## 7. Configuration Example

```yaml
server:
  port: 18007

upstream:
  url: https://proxy.golang.org
  timeout_seconds: 30
  allowed_external_hosts:
    - "sum.golang.org"

cache:
  ttl_seconds: 600
  max_size_mb: 512

policy:
  dry_run: false
  fail_mode: closed
  trusted_packages:
    - "golang.org/x/*"
    - "google.golang.org/*"
    - "github.com/golang/*"
    - "github.com/google/*"
  defaults:
    min_package_age_days: 3
    block_pre_releases: false
  rules:
    - name: block-pseudo-versions
      package_patterns: ["*"]
      block_pre_release: false
    - name: deny-known-malicious
      package_patterns: ["github.com/evil/*"]
      action: deny
      reason: "known malicious module"
    - name: namespace-protection
      package_patterns: ["github.com/mycompany/*"]
      namespace_protection:
        enabled: true
        internal_patterns: ["github.com/mycompany/*"]
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 7
  version_patterns:
    - name: block-pseudo-versions
      match: "^v0\\.0\\.0-\\d{14}-[a-f0-9]{12}$"
      action: deny
      reason: "pseudo-versions (untagged commits) are not allowed"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 8. Ecosystem-Specific Considerations

1. **GOPROXY fallback chain:** Go supports comma-separated GOPROXY values (e.g., `GOPROXY=http://bulwark:18007,direct`). Users must use `|` separator instead of `,` if they want the proxy to be the only option: `GOPROXY=http://bulwark:18007|off`. Alternatively, the proxy returns 410 for denials.

2. **Module path encoding:** Uppercase letters are escaped as `!lowercase`. The proxy must handle encoding/decoding correctly for all PATH operations.

3. **Major version suffixes:** Go modules v2+ must include `/v2`, `/v3`, etc. in the module path (e.g., `github.com/go-redis/redis/v9`). This affects glob matching.

4. **No licence metadata:** The biggest limitation. Go proxy protocol provides no licence information. Full licence checking requires downloading and inspecting the ZIP archive.

5. **Pseudo-versions:** A unique Go concept — versions generated from commit hashes. These are a form of unpinned dependency and represent a significant supply chain risk. Block them with version patterns.

6. **Private modules:** If the proxy is configured for internal modules (`GOPRIVATE` patterns), it should handle authentication to private VCS. The `username`/`password`/`token` upstream config supports this.

---

## 9. Rules NOT Applicable to Go Modules

| Rule                                    | Reason                                                                                                               |
| --------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| **install_scripts**                     | Go has no install/lifecycle scripts. `go:generate` is explicit and manual.                                           |
| **License filtering** (standard)        | GOPROXY protocol includes no licence data. Requires ZIP download and source inspection — expensive and non-standard. |
| **block_snapshots** (literal)           | Go uses pseudo-versions instead. Block via version patterns.                                                         |
| **Metadata anomaly: empty_description** | GOPROXY provides no description field.                                                                               |
| **Metadata anomaly: missing_license**   | Not available without ZIP inspection.                                                                                |
