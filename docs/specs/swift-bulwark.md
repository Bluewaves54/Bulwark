# Swift Package Manager Bulwark — Implementation Specification

## Overview

**Ecosystem:** Swift Package Manager (SPM)  
**Default upstream:** None (Git-based, decentralised)  
**Discovery sites:** `https://swiftpackageindex.com` (community), Apple Package Discovery  
**Default port:** 18011  
**Binary name:** `swift-bulwark`  
**Client tools:** `swift package resolve`, `swift build`, Xcode

**IMPORTANT: Swift Package Manager is architecturally different from all other
ecosystems in this document.** SPM resolves dependencies directly from **Git
repositories** (primarily GitHub), not from a central package registry. There is
no centralised metadata API, no tarball download endpoint, and no package index
that can be intercepted by a caching proxy in the same way as npm/PyPI/Maven.

This document describes the limited proxy functionality that is feasible and
recommends an alternative architecture for SPM supply chain protection.

---

## 1. SPM Dependency Resolution Protocol

### 1.1 How SPM Resolves Packages

1. Client reads `Package.swift` dependencies:
   ```swift
   .package(url: "https://github.com/Alamofire/Alamofire.git", from: "5.0.0")
   ```
2. SPM performs Git operations against the URL:
   - `git ls-remote` to list refs (branches, tags)
   - `git clone` / `git fetch` to get the repository
   - Reads `Package.swift` from the tag/branch to resolve transitive dependencies
3. Downloads are cached in `.build/` locally.

### 1.2 Git Protocol Endpoints Used

| Operation  | Protocol | Endpoint                                                                      |
| ---------- | -------- | ----------------------------------------------------------------------------- |
| List refs  | HTTPS    | `GET https://github.com/{owner}/{repo}.git/info/refs?service=git-upload-pack` |
| Fetch pack | HTTPS    | `POST https://github.com/{owner}/{repo}.git/git-upload-pack`                  |
| Clone      | HTTPS    | `git clone https://github.com/{owner}/{repo}.git`                             |

### 1.3 Apple Swift Package Registry (Emerging)

Apple has defined the **Swift Package Registry** specification (SE-0292):

- Spec: https://github.com/apple/swift-package-manager/blob/main/Documentation/PackageRegistry/Registry.md
- The spec defines a REST API but **no widely-adopted public registry exists yet**.

Registry API (per spec):
| Pattern | Description |
|---|---|
| `GET /{scope}/{name}` | Package metadata (JSON) |
| `GET /{scope}/{name}/{version}` | Specific version metadata |
| `GET /{scope}/{name}/{version}.zip` | Source archive download |
| `GET /identifiers?url={git-url}` | Map Git URL to registry identifier |

---

## 2. Proxy Architecture Options

### Option A: Git HTTPS Proxy (Primary)

Intercept Git HTTPS requests to GitHub/GitLab by configuring SPM to use a
proxy or by acting as a Git HTTPS mitm proxy.

**How it works:**

1. Configure the development environment to route Git HTTPS through the proxy
   (via `https_proxy` environment variable or Git config).
2. The proxy intercepts `git ls-remote` and `git fetch` requests.
3. Before allowing the fetch, the proxy evaluates rules against the repository
   owner/name and tag (version).

**Limitations:**

- Requires TLS interception (MITM) for HTTPS Git operations.
- Must generate and trust a CA certificate.
- Complex implementation compared to HTTP REST API proxying.
- No structured metadata endpoint — must parse Git refs to extract versions.

### Option B: Swift Package Registry Proxy (Future)

Implement the Swift Package Registry API (SE-0292) as a proxy that translates
registry API calls into Git operations against upstream.

**How it works:**

1. swift-bulwark implements the SE-0292 registry API endpoints.
2. Client configures SPM to use the registry:
   ```json
   // .swiftpm/configuration/registries.json
   { "registries": { "[default]": { "url": "http://localhost:18011" } } }
   ```
3. For each API call, swift-bulwark:
   - Translates the scope/name to a Git URL (via a configurable mapping).
   - Clones/fetches the repo.
   - Reads `Package.swift` to extract metadata.
   - Evaluates rules.
   - Returns filtered results via the registry API.

**This is the recommended long-term approach** but requires significant effort.

### Option C: Git Ref Filtering Proxy (Practical Middle Ground)

A lightweight HTTP proxy that intercepts `git ls-remote` (info/refs) and filters
the returned tags before SPM sees them.

**How it works:**

1. Proxy listens on a port and rewrites Git HTTPS URLs.
2. SPM resolves packages through the proxy.
3. For `info/refs` requests, proxy fetches upstream refs, parses the response,
   and removes tags (versions) that violate policy.
4. SPM only sees allowed versions and resolves accordingly.

---

## 3. Rule Implementation Matrix

### 3.1 Trusted Packages

| Aspect             | Detail                                                  |
| ------------------ | ------------------------------------------------------- |
| **Implementable?** | YES                                                     |
| **Logic**          | Match repository `owner/name` against trusted patterns. |
| **Example**        | `"apple/*"`, `"Alamofire/*"`, `"vapor/*"`               |

### 3.2 Explicit Deny / Allow (package_patterns)

| Aspect             | Detail                                                          |
| ------------------ | --------------------------------------------------------------- |
| **Implementable?** | YES                                                             |
| **Logic**          | Match repository `owner/name` or full Git URL against patterns. |

### 3.3 Typosquatting Detection

| Aspect             | Detail                                                                                                                                                           |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                                                                                                              |
| **Logic**          | Extract repo name from URL, normalize, compute Levenshtein distance.                                                                                             |
| **Note**           | Since SPM dependencies reference full Git URLs, the proxy has both the owner and repository name — providing strong identity signal for typosquatting detection. |

### 3.4 Namespace Protection

| Aspect             | Detail                                                                                                                                            |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | YES — STRONG FIT                                                                                                                                  |
| **Logic**          | Git URLs contain the hosting platform + organization: `github.com/apple/swift-nio`. Match organization-level patterns: `github.com/my-company/*`. |
| **Note**           | Strongest possible namespace protection — the VCS URL is authoritative for ownership.                                                             |

### 3.5 Pre-release Blocking

| Aspect             | Detail                                                                            |
| ------------------ | --------------------------------------------------------------------------------- |
| **Implementable?** | YES                                                                               |
| **Logic**          | Parse Git tags as SemVer. Filter tags with pre-release suffixes from `info/refs`. |

### 3.6 Snapshot Blocking

| Aspect             | Detail                                                                                                                                    |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                   |
| **Logic**          | Block branch-based dependencies (e.g., `branch: "main"`). In `info/refs`, filter out branch refs if policy requires tagged versions only. |

### 3.7 Age Quarantine (min_package_age_days)

| Aspect             | Detail                                                                                                                                                       |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **Implementable?** | PARTIAL                                                                                                                                                      |
| **Logic**          | Git tags have creation timestamps (tagger date for annotated tags). However, obtaining the tag date requires fetching the tag object, not just listing refs. |
| **Alternative**    | Use GitHub/GitLab API to get release dates: `GET https://api.github.com/repos/{owner}/{repo}/releases`.                                                      |
| **Limitation**     | Requires a side-channel API call per tag/release. Not available for self-hosted Git repos without APIs.                                                      |

### 3.8 Bypass Age Filter

| Aspect             | Detail                                 |
| ------------------ | -------------------------------------- |
| **Implementable?** | YES (if age quarantine is implemented) |
| **Logic**          | Standard.                              |

### 3.9 Pinned Versions

| Aspect             | Detail                                    |
| ------------------ | ----------------------------------------- |
| **Implementable?** | YES                                       |
| **Logic**          | Standard. Version extracted from Git tag. |

### 3.10 Velocity Check

| Aspect             | Detail                                                                          |
| ------------------ | ------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                         |
| **Logic**          | Requires fetching tag timestamps from Git or VCS API. Count tags within window. |

### 3.11 Install Scripts Detection

| Aspect             | Detail                                                                |
| ------------------ | --------------------------------------------------------------------- |
| **Implementable?** | NOT APPLICABLE                                                        |
| **Reason**         | SPM has no install/lifecycle scripts. `Package.swift` is declarative. |

### 3.12 License Filtering

| Aspect             | Detail                                                                                                                                          |
| ------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                         |
| **Logic**          | Requires fetching the repository's `LICENSE` file or using GitHub API (`GET /repos/{owner}/{repo}/license`). Not available from Git refs alone. |

### 3.13 Version Patterns (regex)

| Aspect             | Detail                                                    |
| ------------------ | --------------------------------------------------------- |
| **Implementable?** | YES                                                       |
| **Logic**          | Apply regex to Git tag names (which are version strings). |

### 3.14 Metadata Anomaly Checks

| Aspect             | Detail                                                                                                                                        |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implementable?** | PARTIAL                                                                                                                                       |
| **Logic**          | Limited to VCS metadata. Can check for missing LICENSE file, empty description (from GitHub API), etc. Not available from Git protocol alone. |

### 3.15 Dry-Run Mode

| Aspect             | Detail    |
| ------------------ | --------- |
| **Implementable?** | YES       |
| **Logic**          | Standard. |

### 3.16 Fail-Closed Mode

| Aspect             | Detail                                      |
| ------------------ | ------------------------------------------- |
| **Implementable?** | YES                                         |
| **Logic**          | Standard. Important for Git fetch failures. |

---

## 4. Recommended Implementation: Git Ref Filter Proxy

```
1. Parse incoming request URL to extract {host}/{owner}/{repo}.
2. Forward to upstream Git hosting:
   - For info/refs: Fetch upstream refs, parse, filter by policy, return.
   - For git-upload-pack: If version was allowed by refs filtering, allow.
     Otherwise, the client won't request disallowed versions.
3. Rule evaluation:
   a. Extract version from tag name (strip "v" prefix if present).
   b. Call EvaluatePackage() with owner/repo as the package name.
   c. Call EvaluateVersion() for each tag.
   d. Build filtered refs response excluding denied versions.
4. Return filtered Git smart HTTP response.
```

---

## 5. Configuration Example

```yaml
server:
  port: 18011

upstream:
  url: https://github.com
  timeout_seconds: 60
  allowed_external_hosts:
    - "github.com"
    - "gitlab.com"
    - "api.github.com"

cache:
  ttl_seconds: 600
  max_size_mb: 512

policy:
  dry_run: false
  fail_mode: closed
  trusted_packages:
    - "apple/*"
    - "Alamofire/*"
    - "vapor/*"
    - "pointfreeco/*"
    - "swift-server/*"
  defaults:
    min_package_age_days: 7
    block_pre_releases: false
  rules:
    - name: namespace-protection
      package_patterns: ["my-company/*"]
      namespace_protection:
        enabled: true
        internal_patterns: ["my-company/*"]
    - name: deny-malicious
      package_patterns: ["evil-org/*"]
      action: deny
      reason: "blocked organization"
  version_patterns:
    - name: block-beta-tags
      match: "-(beta|alpha|rc)"
      action: deny
      reason: "pre-release tags blocked"

logging:
  level: info
  format: json

metrics:
  enabled: true
```

---

## 6. Ecosystem-Specific Considerations

1. **No central registry:** This is the fundamental difference from all other ecosystems. SPM depends on Git hosting (GitHub, GitLab, Bitbucket) rather than a package registry. The proxy must operate at the Git protocol level.

2. **TLS interception complexity:** Proxying Git HTTPS requires certificate management. Organizations using SPM supply chain protection should deploy the proxy alongside a corporate CA.

3. **SE-0292 Registry spec:** Apple's Swift Package Registry specification is the path to a cleaner proxy architecture. If/when public registries adopt it, swift-bulwark can transition to a standard REST proxy.

4. **GitHub API rate limits:** Using GitHub API for metadata (timestamps, licences) is rate-limited. Use authenticated requests and cache aggressively.

5. **Binary targets:** SPM supports binary targets (XCFrameworks) downloaded from arbitrary URLs. These bypass Git-based resolution entirely. Consider adding URL allowlisting for binary target sources.

6. **Xcode integration:** Xcode performs SPM resolution internally. Proxying requires network-level configuration (proxy settings in macOS System Preferences or Xcode configuration).

---

## 7. Rules NOT Applicable / Severely Limited for SPM

| Rule                   | Status           | Reason                                                    |
| ---------------------- | ---------------- | --------------------------------------------------------- |
| **install_scripts**    | NOT APPLICABLE   | SPM has no lifecycle scripts.                             |
| **License filtering**  | REQUIRES VCS API | Not available from Git protocol. Needs GitHub/GitLab API. |
| **Age quarantine**     | REQUIRES VCS API | Tag timestamps need API or full clone.                    |
| **Velocity check**     | REQUIRES VCS API | Tag timestamps not in `info/refs`.                        |
| **Metadata anomalies** | REQUIRES VCS API | No structured metadata in Git protocol.                   |
| **block_snapshots**    | PARTIAL          | Can block branch refs but not directly analogous.         |

---

## 8. Recommendation

SPM proxy implementation is **significantly more complex** than other ecosystems
due to the Git-based architecture. Recommended prioritisation:

1. **Phase 1:** Git ref filtering (info/refs interception) — provides package deny/allow, pre-release blocking, version patterns, typosquatting, namespace protection.
2. **Phase 2:** GitHub/GitLab API integration — adds age quarantine, velocity check, licence filtering, metadata anomalies.
3. **Phase 3:** SE-0292 Registry API — when public registries emerge, migrate to standard REST proxy architecture.

Consider whether the implementation complexity justifies the effort versus the
supply chain risk for SPM (which is lower than npm/PyPI due to the inherent
source-code-from-Git model and smaller attack surface).
