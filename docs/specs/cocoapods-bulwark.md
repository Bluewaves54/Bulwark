# CocoaPods Bulwark — Implementation Specification

## Overview

**Ecosystem:** CocoaPods (iOS / macOS / tvOS / watchOS)  
**Default upstream (CDN):** `https://cdn.cocoapods.org`  
**Supplementary API (Trunk):** `https://trunk.cocoapods.org`  
**Default port:** 18006  
**Binary name:** `cocoapods-bulwark`  
**Client tools:** `pod install`, `pod update`, `pod repo update`

CocoaPods resolves dependencies via a **CDN-based index** (since CocoaPods 1.8+).
The CDN serves sharded podspec files organized by an MD5 hash of the pod name.
A supplementary **Trunk API** provides version timestamps and ownership data.

**Supply chain context:** In 2024, researchers discovered that ~1,800 CocoaPods
pods had unclaimed ownership — an attacker could have taken control of them.
This makes proxy-level supply chain protection especially valuable.

---

## 1. CocoaPods CDN & Trunk API Endpoints

### 1.1 CDN — Shard Index (Version Discovery)

| Pattern                           | Description                                |
| --------------------------------- | ------------------------------------------ |
| `/all_pods_versions_{prefix}.txt` | Shard file listing pods and their versions |

**Shard prefix:** First 3 characters of the MD5 hex digest of the pod name
(joined, not split). There are 4096 shard files (000–fff).

Example: `MD5("Alamofire")` = `da2...` → shard file is `all_pods_versions_da2.txt`

Format (one pod per line):

```
Alamofire/1.0.0/1.0.1/1.1.0/.../5.10.2
AnotherPod/0.1.0/1.0.0
```

### 1.2 CDN — Podspec (Per-Version Metadata)

| Pattern                                                                | Description  |
| ---------------------------------------------------------------------- | ------------ |
| `/Specs/{md5[0]}/{md5[1]}/{md5[2]}/{Pod}/{version}/{Pod}.podspec.json` | Podspec JSON |

**Shard path:** First 3 characters of MD5 hex digest, split into individual
directory components.

Example: `MD5("Alamofire")` starts with `d`, `a`, `2` →

```
/Specs/d/a/2/Alamofire/5.10.2/Alamofire.podspec.json
```

Example podspec response:

```json
{
  "name": "Alamofire",
  "version": "5.10.2",
  "license": "MIT",
  "summary": "Elegant HTTP Networking in Swift",
  "homepage": "https://github.com/Alamofire/Alamofire",
  "source": {
    "git": "https://github.com/Alamofire/Alamofire.git",
    "tag": "5.10.2"
  },
  "platforms": { "ios": "10.0", "osx": "10.12" },
  "swift_versions": ["5"],
  "dependencies": { ... }
}
```

### 1.3 Trunk API — Pod Metadata (Timestamps)

| Pattern                                          | Description                          |
| ------------------------------------------------ | ------------------------------------ |
| `https://trunk.cocoapods.org/api/v1/pods/{name}` | Pod metadata with version timestamps |

Example response:

```json
{
  "name": "Alamofire",
  "versions": [
    { "name": "5.10.2", "created_at": "2024-11-26 19:58:43 UTC" },
    { "name": "5.10.1", "created_at": "2024-10-21 16:33:22 UTC" },
    ...
  ]
}
```

### 1.4 CDN — CocoaPods Repo Metadata

| Pattern                    | Description                 |
| -------------------------- | --------------------------- |
| `/CocoaPods-version.yml`   | Repository version metadata |
| `/deprecated_podspecs.txt` | List of deprecated pods     |

---

## 2. Proxy Architecture

### 2.1 Handler Registration

```
GET /all_pods_versions_{prefix}.txt          → handleShardIndex
GET /Specs/{a}/{b}/{c}/{pod}/{ver}/{pod}.podspec.json → handlePodspec
GET /CocoaPods-version.yml                   → handlePassThrough
GET /deprecated_podspecs.txt                 → handlePassThrough
GET /health                                  → healthHandler
GET /readyz                                  → readyzHandler
GET /metrics                                 → metricsHandler
```

### 2.2 Package Name & Version Extraction

- **Shard index:** Pod name and version list parsed from the line format `PodName/ver1/ver2/ver3`.
- **Podspec:** Pod name and version extracted from URL: `/Specs/{a}/{b}/{c}/{Pod}/{version}/{Pod}.podspec.json`.
- Pod names are case-sensitive and may contain hyphens, underscores, and dots.

### 2.3 Metadata Retrieval Strategy

**For `handleShardIndex` (version listing filtering):**

1. Fetch upstream shard file.
2. Parse lines into pod name → version list.
3. For each pod+version, evaluate rules.
4. For age/licence checks, fetch data from Trunk API or podspec.
5. Rebuild shard file with denied versions removed.
6. Return filtered response.

**For `handlePodspec` (per-version metadata guard):**

1. Extract pod name and version from URL.
2. Fetch the podspec JSON from CDN.
3. Fetch timestamp from Trunk API (cache aggressively).
4. Evaluate rules.
5. If denied, return 403/404. If allowed, return the podspec.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                      |
| ------------------ | ----------------------------------------------------------- |
| **Implementable?** | YES                                                         |
| **Logic**          | Match pod name against `trusted_packages` globs.            |
| **Example**        | `"Alamofire"`, `"AFNetworking"`, `"Firebase*"`, `"Google*"` |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                              |
| ------------------ | ----------------------------------- |
| **Implementable?** | YES                                 |
| **Logic**          | Standard glob matching on pod name. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                 |
| ------------------ | ------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                    |
| **Logic**          | Normalize name (lowercase, strip `-` and `_`), compute Levenshtein distance.                           |
| **Note**           | CocoaPods has ~100,000 pods. Typosquatting is a real risk given the 2024 unclaimed-ownership incident. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                               |
| ------------------ | ---------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                              |
| **Reason**         | CocoaPods has no formal namespace/scope system. Pods are flat names.                                 |
| **Workaround**     | Convention-based prefix matching: `Firebase*`, `Google*`, `AWS*`. Match against `internal_patterns`. |

### 3.5 Pre-release Blocking

| Aspect                     | Detail                                                                                     |
| -------------------------- | ------------------------------------------------------------------------------------------ |
| **Implementable?**         | YES                                                                                        |
| **Logic**                  | CocoaPods supports SemVer. Pre-release versions include `-alpha`, `-beta`, `-rc` suffixes. |
| **Custom `IsPreRelease`:** | Standard SemVer pre-release detection.                                                     |

### 3.6 Snapshot Blocking

| Aspect             | Detail                             |
| ------------------ | ---------------------------------- |
| **Implementable?** | NOT APPLICABLE                     |
| **Reason**         | CocoaPods has no SNAPSHOT concept. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                                                   |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES (via Trunk API)                                                                                                                      |
| **Logic**          | Fetch `created_at` from `trunk.cocoapods.org/api/v1/pods/{name}`. Match version's `created_at` timestamp against `min_package_age_days`. |
| **Fallback**       | If Trunk is unavailable, use `fail_mode` semantics. The CDN podspec does NOT include timestamps.                                         |
| **Cache**          | Cache Trunk responses with configured TTL.                                                                                               |

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
| **Logic**          | Fetch all version timestamps from Trunk API. Count versions within `window_hours`. |

### 3.11 Install Scripts Detection

| Aspect                      | Detail                                                                                                                                                                        |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?**          | YES (via podspec)                                                                                                                                                             |
| **Logic**                   | Podspec JSON includes `"script_phases"` for custom build phases, and `"prepare_command"` for pre-install scripts. Both execute arbitrary shell commands during `pod install`. |
| **Implementation**          | Check podspec for: `script_phases` (array of shell commands), `prepare_command` (shell string). If present, treat as "has install scripts".                                   |
| **Example podspec fields:** | `"prepare_command": "make build"`, `"script_phases": [{"name": "Build", "script": "..."}]`                                                                                    |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                                                                     |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                                                                                                                                        |
| **Logic**          | The `license` field in podspec is either a string (`"MIT"`) or an object (`{"type": "MIT", "file": "LICENSE"}`). Extract the licence type string and compare against allowed/denied lists. |
| **Normalization**  | Handle both string and object forms: `if typeof license == string, use directly; else use license.type`.                                                                                   |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                   |
| ------------------ | ------------------------ |
| **Implementable?** | YES                      |
| **Logic**          | Standard regex matching. |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                                         |
| ------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                            |
| **Logic**          | From podspec: `homepage` → repository check (missing_repository if absent or empty), `license` → missing_license, `summary`/`description` → empty_description. |
| **Note**           | Also check `source.git` — if missing, the pod may be pulling from an unverified source.                                                                        |

### 3.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail                                                                   |
| ------------------ | ------------------------------------------------------------------------ |
| **Implementable?** | YES                                                                      |
| **Logic**          | Standard. Important when Trunk API is unavailable for timestamp lookups. |

---

## 4. Shard Index Filtering Algorithm

```
1. Fetch upstream shard file: GET /all_pods_versions_{prefix}.txt
2. Parse lines: each line is "PodName/ver1/ver2/.../verN"
3. For each pod line:
   a. Split on "/" — first element is pod name, rest are versions.
   b. Call EvaluatePackage(). If denied at package level, drop entire line.
   c. For each version:
      i.  Fetch podspec from CDN cache (for licence, metadata checks).
      ii. Fetch timestamp from Trunk API cache (for age checks).
      iii. Build VersionMeta and call EvaluateVersion().
      iv. If denied, remove this version from the list.
   d. If no versions remain, drop the entire line.
   e. Rebuild line: "PodName/allowed_ver1/allowed_ver2/..."
4. Return filtered shard file.
```

**Performance note:** Shard files can list hundreds of pods. Fetching podspec
and Trunk data per-pod-per-version is expensive. Use aggressive caching and
consider lazy evaluation (only fetch side-channel data when needed by active rules).

---

## 5. MD5 Shard Path Computation

```go
func cdnShardPath(podName string) string {
    hash := md5.Sum([]byte(podName))
    hex := fmt.Sprintf("%x", hash)
    return fmt.Sprintf("/Specs/%c/%c/%c/%s", hex[0], hex[1], hex[2], podName)
}

func cdnShardPrefix(podName string) string {
    hash := md5.Sum([]byte(podName))
    return fmt.Sprintf("%x", hash[:2])[:3] // first 3 hex chars
}
```

---

## 6. Configuration Example

```yaml
server:
  port: 18006

upstream:
  url: https://cdn.cocoapods.org
  timeout_seconds: 30
  allowed_external_hosts:
    - "trunk.cocoapods.org"

cache:
  ttl_seconds: 600
  max_size_mb: 256

policy:
  dry_run: false
  fail_mode: open
  trusted_packages:
    - "Alamofire"
    - "AFNetworking"
    - "Firebase*"
    - "Google*"
    - "SDWebImage"
    - "SnapKit"
    - "Kingfisher"
  defaults:
    min_package_age_days: 7
    block_pre_releases: true
  install_scripts:
    enabled: true
    action: warn
    allowed_with_scripts:
      - "gRPC-Core"
      - "Protobuf"
    reason: "pod uses prepare_command or script_phases"
  rules:
    - name: deny-deprecated
      package_patterns: ["DeprecatedPod*"]
      action: deny
      reason: "deprecated pod"
    - name: age-quarantine
      package_patterns: ["*"]
      min_package_age_days: 14
    - name: license-filter
      package_patterns: ["*"]
      allowed_licenses: ["MIT", "Apache-2.0", "BSD-2-Clause", "BSD-3-Clause"]
  version_patterns:
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

1. **Dual data sources:** CDN provides podspec (metadata, licence) but NOT timestamps. Trunk API provides timestamps but limited metadata. Both must be queried for full rule evaluation.

2. **MD5 shard paths:** The CDN uses MD5-based directory sharding. The proxy must compute MD5 hashes to construct correct paths. Use `crypto/md5` in Go.

3. **Source-based distribution:** CocoaPods primarily distributes source code (podspecs point to Git repos via `source.git`). The actual code is cloned from GitHub/GitLab by the CocoaPods client. The proxy controls metadata access (which versions are visible) but does not proxy the Git clone itself.

4. **XCFrameworks / binary pods:** Newer pods may distribute pre-built binaries via `vendored_frameworks`. The binary URL is in the podspec. Consider validating these URLs against `allowed_external_hosts`.

5. **Trunk ownership:** The Trunk API can reveal ownership information. Consider adding an optional check for pods with no verified owner (the 2024 vulnerability).

6. **Deprecated pods:** The CDN serves `deprecated_podspecs.txt`. Consider auto-denying or warning on deprecated pods.

---

## 8. Rules NOT Applicable to CocoaPods

| Rule                            | Reason                                                                    |
| ------------------------------- | ------------------------------------------------------------------------- |
| **block_snapshots**             | CocoaPods has no SNAPSHOT versioning concept.                             |
| **Namespace protection** (full) | CocoaPods has no formal namespace system. Only heuristic prefix matching. |
