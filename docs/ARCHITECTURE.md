# Architecture — PKGuard

This document provides the complete architectural reference for the PKGuard, including system context, component relationships, deployment topologies, and request-processing sequence diagrams. All diagrams use [Mermaid](https://mermaid.js.org) syntax.

---

## 1. System Context (C4 Level 1)

The PKGuard sits between developer tools and public package registries, acting as a policy enforcement gateway. It has no UI and no persistent database — all state is held in-process (config + cache).

```mermaid
C4Context
  title System Context — PKGuard

  Person(dev, "Developer / CI Pipeline", "Uses language-specific package managers: pip, npm, mvn, cargo, dotnet, bundler, go get")

    System(pkguard, "PKGuard — PKGuard", "HTTP proxy gateway that enforces package security policy. Filters versions by age, pre-release status, namespace, typosquatting, velocity, and custom rules.")

  System_Ext(pypi, "PyPI (pypi.org)", "Python package index")
  System_Ext(npm, "npm Registry (registry.npmjs.org)", "JavaScript package registry")
  System_Ext(maven, "Maven Central (repo1.maven.org)", "Java package repository")
  System_Ext(nuget, "NuGet Gallery (api.nuget.org)", ".NET package registry")
  System_Ext(cargo, "crates.io", "Rust package registry")
  System_Ext(rubygems, "RubyGems.org", "Ruby gem registry")
  System_Ext(gomod, "proxy.golang.org", "Go module proxy")
  System_Ext(enterprise, "Enterprise Registry\n(corporate artifact repository)", "Corporate package mirror and internal artifact store")
  System_Ext(osv, "OSV.dev / NVD", "Vulnerability advisory feeds (future)")

    Rel(dev, pkguard, "Package install requests", "HTTP/HTTPS")
    Rel(pkguard, pypi, "Filtered requests (Topology A)", "HTTPS")
    Rel(pkguard, npm, "Filtered requests (Topology A)", "HTTPS")
    Rel(pkguard, maven, "Filtered requests (Topology A)", "HTTPS")
    Rel(pkguard, nuget, "Filtered requests (Topology A)", "HTTPS")
    Rel(pkguard, cargo, "Filtered requests (Topology A)", "HTTPS")
    Rel(pkguard, enterprise, "Filtered requests (Topology B)", "HTTPS")
  Rel(enterprise, pypi, "Upstream fetch (Topology B)", "HTTPS")
  Rel(enterprise, npm, "Upstream fetch (Topology B)", "HTTPS")
    Rel(pkguard, osv, "Advisory feed pull (future)", "HTTPS")
```

---

## 2. Container Diagram (C4 Level 2)

Each ecosystem is a self-contained Go binary. All binaries share the `common/` module for config types, rule engine, and cache.

```mermaid
C4Container
  title Container Diagram — PKGuard

  Person(dev, "Developer / CI Pipeline")
  System_Ext(upstream, "Upstream Registries\n(public or enterprise)")

    Container_Boundary(pkguard, "PKGuard") {
    Container(pypi_proxy, "pypi-pkguard", "Go binary", "Implements PyPI Simple Index and PEP 691 protocols. Port 18000.")
    Container(npm_proxy, "npm-pkguard", "Go binary", "Implements npm registry / packument protocol. Port 18001.")
    Container(maven_proxy, "maven-pkguard", "Go binary [FUTURE]", "Implements Maven 2 repository protocol. Port 18002.")
    Container(nuget_proxy, "nuget-pkguard", "Go binary [FUTURE]", "Implements NuGet V3 API protocol. Port 18003.")
    Container(cargo_proxy, "cargo-pkguard", "Go binary [FUTURE]", "Implements Cargo sparse index protocol. Port 18004.")
    Container(rubygems_proxy, "rubygems-pkguard", "Go binary [FUTURE]", "Implements RubyGems API protocol. Port 18005.")
    Container(gomod_proxy, "gomod-pkguard", "Go binary [FUTURE]", "Implements Go module proxy protocol. Port 18006.")

    ContainerDb(cache, "In-Process Cache", "sync.RWMutex + map + TTL", "Per-binary TTL cache. Keyed by request URL. TTL configurable; max_size_mb is reserved but not yet enforced.")
    Container(rule_engine, "Rule Engine (common/)", "Go module", "Shared: EvaluatePackage, EvaluateVersion, trusted packages, typosquatting, namespace protection, install scripts, velocity detection.")
    Container(config, "Config (common/)", "Go module", "Shared structs, validation, defaults. Loaded from YAML at startup.")
    Container(installer, "Installer (common/installer)", "Go module", "Shared one-click setup and uninstall. Embedded best-practices config, package manager configuration, OS autostart entries.")
  }

  Rel(dev, pypi_proxy, "pip / pip3 / uv / poetry / pdm", "HTTP :18000")
  Rel(dev, npm_proxy, "npm / yarn / pnpm", "HTTP :18001")
  Rel(dev, maven_proxy, "mvn / gradle", "HTTP :18002")
  Rel(dev, nuget_proxy, "dotnet / nuget CLI", "HTTP :18003")
  Rel(dev, cargo_proxy, "cargo", "HTTP :18004")
  Rel(dev, rubygems_proxy, "gem / bundler", "HTTP :18005")
  Rel(dev, gomod_proxy, "go get / GOPROXY", "HTTP :18006")

  Rel(pypi_proxy, rule_engine, "uses")
  Rel(npm_proxy, rule_engine, "uses")
  Rel(pypi_proxy, cache, "read/write")
  Rel(npm_proxy, cache, "read/write")
  Rel(rule_engine, config, "reads")

  Rel(pypi_proxy, upstream, "HTTPS (validated)")
  Rel(npm_proxy, upstream, "HTTPS (validated)")
```

---

## 3. Component Diagram — Single Proxy Binary (C4 Level 3)

The internal structure of each proxy binary follows the same pattern. This diagram uses the PyPI proxy as the reference implementation.

```mermaid
C4Component
  title Component Diagram — pypi-pkguard binary

  Container_Boundary(pypi, "pypi-pkguard") {
    Component(main, "main.go", "Go package main", "Entry point. Parses flags (-setup, -uninstall, -config), loads config, creates logger, builds server, starts HTTP server, handles graceful shutdown on SIGINT/SIGTERM.")
    Component(server, "server.go — Server", "Go struct", "Registers HTTP routes on ServeMux. Holds references to config, HTTP client, cache, metrics, rule engine, logger.")
    Component(handlers, "server.go — Handlers", "Go methods on Server", "handleSimple, handleExternal, handleProxy, handleHealth, handleReadyz, handleMetrics, handleGetLogLevel, handleSetLogLevel. Each handler follows the filter pipeline.")
    Component(pipeline, "Filtering Pipeline", "Logic within handlers", "1. Parse request. 2. Check package rules (deny → 403 with reason). 3. Cache lookup. 4. Fetch upstream. 5. Evaluate each version. 6. If all versions blocked → 403 with reason. 7. Rewrite and return filtered response.")
    Component(pypi_helpers, "pypi.go", "Go package", "IsPreRelease (PEP 440), ExtractVersion (filename → version), PEP 691 JSON parsing, HTML simple index parsing.")
    Component(config_loader, "config.go", "Go package", "LoadConfig, applyDefaults, validate. PyPI-specific: PEP691Enabled, BaseURL, AllowedExternalHosts.")
    Component(metrics, "Metrics", "atomic.Int64 counters", "RequestsTotal, CacheHits, CacheMisses, VersionsFiltered, PackagesDenied, VelocityAnomalies, etc.")
  }

  Container_Ext(common_rules, "common/rules — RuleEngine", "Go module", "EvaluatePackage, EvaluateVersion, trusted packages, threat detections")
  Container_Ext(common_cache, "common/rules — Cache", "Go module", "In-memory TTL cache (no LRU; max_size_mb not yet enforced)")
  Container_Ext(upstream_reg, "Upstream Registry", "External HTTP", "PyPI, files.pythonhosted.org, or enterprise registry")

  Rel(main, config_loader, "loads config")
  Rel(main, server, "builds and starts")
  Rel(server, handlers, "dispatches requests to")
  Rel(handlers, pipeline, "executes")
  Rel(pipeline, pypi_helpers, "version parsing")
  Rel(pipeline, common_rules, "EvaluatePackage / EvaluateVersion")
  Rel(pipeline, common_cache, "Get / Set")
  Rel(pipeline, upstream_reg, "HTTP GET (metadata + files)")
  Rel(handlers, metrics, "increments counters")
```

---

## 4. Deployment Topology A — Direct Proxy

Developer tools are pointed directly at the PKGuard. No enterprise registry is involved.

```mermaid
flowchart TD
    subgraph Developer Workstation / CI
        pip["pip / uv / poetry\n~/.pip/pip.conf\nindex-url = http://pkguard:18000/simple/"]
        npm_cli["npm / yarn / pnpm\n.npmrc\nregistry=http://pkguard:18001/"]
        mvn["mvn / gradle\nsettings.xml mirror\nurl: http://pkguard:18002/"]
        cargo_cli["cargo\n.cargo/config.toml\n[source.crates-io] replace-with = 'pkguard'\n[source.pkguard] registry = 'sparse+http://pkguard:18004/'"]
    end

    subgraph Kubernetes / Docker — PKGuard Proxy
        direction TB
        PyPI["pypi-pkguard :18000\nTopology A config\nupstream: https://pypi.org"]
        NPM["npm-pkguard :18001\nTopology A config\nupstream: https://registry.npmjs.org"]
        MVN["maven-pkguard :18002\nTopology A config\nupstream: https://repo1.maven.org"]
        CRG["cargo-pkguard :18004\nTopology A config\nupstream: https://sparse.crates.io"]
    end

    subgraph Public Internet
        PyPIReg["pypi.org\nfiles.pythonhosted.org"]
        NpmReg["registry.npmjs.org"]
        MavenCentral["repo1.maven.org"]
        CratesIO["crates.io / sparse.crates.io"]
    end

    pip -->|HTTP :18000| PyPI
    npm_cli -->|HTTP :18001| NPM
    mvn -->|HTTP :18002| MVN
    cargo_cli -->|HTTP :18004| CRG

    PyPI -->|HTTPS filtered| PyPIReg
    NPM -->|HTTPS filtered| NpmReg
    MVN -->|HTTPS filtered| MavenCentral
    CRG -->|HTTPS filtered| CratesIO

    style PyPI fill:#2563eb,color:#fff
    style NPM fill:#2563eb,color:#fff
    style MVN fill:#2563eb,color:#fff
    style CRG fill:#2563eb,color:#fff
```

---

## 5. Deployment Topology B — Enterprise Registry Middleware

Developer tools are pointed at the existing enterprise registry. The enterprise registry's remote/proxy repositories are reconfigured to fetch through the PKGuard. No developer client reconfiguration is needed.

```mermaid
flowchart TD
    subgraph Developer Workstation / CI
        pip2["pip / uv / poetry\nindex-url = https://registry.corp.example/pypi/simple/\n(unchanged from before)"]
        npm2["npm / yarn / pnpm\nregistry=https://registry.corp.example/npm/\n(unchanged)"]
    end

    subgraph Enterprise Registry — Corporate Artifact Repository
        direction TB
        ArtPyPI["PyPI Remote Repo\nRemote URL → http://pkguard:18000/simple/"]
        ArtNPM["npm Remote Repo\nRemote URL → http://pkguard:18001/"]
        ArtInternal["Internal / local repos\n(private packages — bypasses pkguard)"]
    end

    subgraph Kubernetes / Docker — PKGuard Proxy
        direction TB
        PyPIB["pypi-pkguard :18000\nTopology B config\nupstream: https://pypi.org\n(points at public — pkguard is the middle hop)"]
        NPMB["npm-pkguard :18001\nTopology B config\nupstream: https://registry.npmjs.org"]
    end

    subgraph Public Internet
        PyPIReg2["pypi.org"]
        NpmReg2["registry.npmjs.org"]
    end

    pip2 -->|HTTPS| ArtPyPI
    npm2 -->|HTTPS| ArtNPM

    ArtPyPI -->|HTTP (internal)| PyPIB
    ArtNPM -->|HTTP (internal)| NPMB

    PyPIB -->|HTTPS filtered| PyPIReg2
    NPMB -->|HTTPS filtered| NpmReg2

    style PyPIB fill:#2563eb,color:#fff
    style NPMB fill:#2563eb,color:#fff
    style ArtPyPI fill:#7c3aed,color:#fff
    style ArtNPM fill:#7c3aed,color:#fff
```

> **Note — Topology B variant:** Alternatively, the enterprise registry can be configured as the pkguard proxy's **upstream** (i.e. pkguard fetches from the enterprise registry, enterprise registry fetches from public). This variant is activated by setting `upstream.base_url` to the enterprise registry URL. In this case developers point at pkguard, and the enterprise registry sits **downstream** of public registries. Consult the `config-enterprise.yaml` files for both topologies.

---

## 6. Request Filtering Pipeline — State Machine

Every proxy request follows the same decision pipeline. The diagram below shows the PyPI simple-index path as the canonical example.

```mermaid
stateDiagram-v2
    [*] --> ParseRequest
    ParseRequest --> ExtractPackageName
    ExtractPackageName --> CheckTrustedPkg

    CheckTrustedPkg --> CheckCache : trusted package → allow all
    CheckTrustedPkg --> EvaluatePackage : not trusted

    EvaluatePackage --> ReturnDeny : package rule = deny\n(namespace / typosquat / explicit)
    EvaluatePackage --> CheckCache : package allowed

    CheckCache --> ReturnCacheHit : cache hit (X-Cache: HIT)
    CheckCache --> FetchUpstreamMetadata : cache miss

    FetchUpstreamMetadata --> ReturnUpstreamError : upstream error (502)
    FetchUpstreamMetadata --> FetchPublishTimes : metadata received

    FetchPublishTimes --> FilterVersions : publish times fetched (or skipped on 404)

    FilterVersions --> EvaluateVersion : for each version
    EvaluateVersion --> VersionAllowed : trusted / pinned / age ok / pattern allow
    EvaluateVersion --> VersionDenied : too new / pre-release / pattern deny / install scripts / velocity / CVE
    VersionAllowed --> FilterVersions : next version
    VersionDenied --> FilterVersions : next version

    FilterVersions --> BuildFilteredResponse : all versions evaluated
    BuildFilteredResponse --> SetPolicyNoticeHeader : some versions removed
    BuildFilteredResponse --> ReturnBlockedResponse : all versions removed (403)
    BuildFilteredResponse --> StoreInCache
    SetPolicyNoticeHeader --> StoreInCache
    StoreInCache --> ReturnFilteredResponse

    ReturnDeny --> [*]
    ReturnCacheHit --> [*]
    ReturnUpstreamError --> [*]
    ReturnFilteredResponse --> [*]
    ReturnBlockedResponse --> [*]
```

---

### Block Response Behaviour

When a package is entirely blocked — either by a package-level deny rule or because every individual version was removed by version-level rules — the proxy returns **HTTP 403 Forbidden** with a clear policy reason in the response body instead of an empty version list.

| Ecosystem | Response Format | Example Body |
|---|---|---|
| **npm** | JSON `{"error":"..."}` | `{"error":"[PKGuard] event-stream: package matches deny list"}` |
| **PyPI** | Plain text | `[PKGuard] requests: all available versions blocked by policy` |
| **Maven** | Plain text (via `http.Error`) | `[PKGuard] com.example:mylib: all available versions blocked by policy` |

This ensures package managers display a meaningful error message (e.g., npm shows the `error` field) instead of confusing messages like `ENOVERSIONS` (npm) or "No matching distribution found" (pip).

When only *some* versions are blocked, the proxy still returns a **200 OK** with the filtered response and an `X-Curation-Policy-Notice` header indicating how many versions were removed.

**Cached 403 responses** are stored in the in-memory cache with the same TTL as normal responses, so repeated requests for blocked packages are served from cache.

---

## 7. Sequence Diagram — PyPI Package Install (Topology A)

`pip install requests` with age filter of 7 days. Three versions exist: two old enough, one too new.

```mermaid
sequenceDiagram
    participant pip as pip client
    participant proxy as pypi-pkguard proxy
    participant cache as In-Process Cache
    participant engine as Rule Engine
    participant pypi as pypi.org

    pip->>proxy: GET /simple/requests/ (Accept: text/html)
    proxy->>engine: EvaluatePackage("requests")
    engine-->>proxy: {Allowed: true}

    proxy->>cache: Get("/simple/requests/")
    cache-->>proxy: miss

    proxy->>pypi: GET /simple/requests/ (PEP 691 attempt)
    pypi-->>proxy: 200 application/vnd.pypi.simple.v1+json (versions: 2.28.0, 2.29.0, 2.31.0)

    proxy->>pypi: GET /pypi/requests/json (upload timestamps)
    pypi-->>proxy: 200 JSON (2.28.0→2023-01-01, 2.29.0→2023-06-01, 2.31.0→today)

    proxy->>engine: EvaluateVersion("requests", "2.28.0", 2023-01-01)
    engine-->>proxy: {Allowed: true}
    proxy->>engine: EvaluateVersion("requests", "2.29.0", 2023-06-01)
    engine-->>proxy: {Allowed: true}
    proxy->>engine: EvaluateVersion("requests", "2.31.0", today)
    engine-->>proxy: {Allowed: false, Reason: "version too new"}

    proxy->>cache: Set("/simple/requests/", filteredHTML)
    proxy-->>pip: 200 text/html (2.28.0, 2.29.0 only)\nX-Cache: MISS\nX-Curation-Policy-Notice: 1 version(s) removed

    Note over pip: pip selects 2.29.0 as latest allowed

    pip->>proxy: GET /external?url=https://files.pythonhosted.org/packages/.../requests-2.29.0.tar.gz
    proxy->>engine: EvaluateVersion("requests", "2.29.0", 2023-06-01)
    engine-->>proxy: {Allowed: true}
    proxy->>pypi: GET https://files.pythonhosted.org/packages/.../requests-2.29.0.tar.gz
    pypi-->>proxy: 200 (tarball bytes)
    proxy-->>pip: 200 (tarball bytes, streamed unmodified)
```

---

## 8. Sequence Diagram — PyPI Package Install (Topology B via Enterprise Registry)

Same `pip install requests` but the enterprise registry is in the middle.

```mermaid
sequenceDiagram
    participant pip as pip client
    participant art as Enterprise Registry
    participant proxy as pypi-pkguard proxy\n(Topology A config)
    participant pypi as pypi.org

    pip->>art: GET /pypi/simple/requests/ (enterprise registry virtual repo)
    art->>art: Cache miss in enterprise registry

    art->>proxy: GET /simple/requests/ (remote repo fetch via pkguard)
    proxy->>proxy: EvaluatePackage, cache miss
    proxy->>pypi: GET /simple/requests/
    pypi-->>proxy: 200 (all versions)
    proxy->>pypi: GET /pypi/requests/json
    pypi-->>proxy: upload timestamps
    proxy->>proxy: FilterVersions (2 of 3 allowed)
    proxy-->>art: 200 (filtered simple index)\nX-Curation-Policy-Notice: 1 version(s) removed

    art->>art: Cache filtered index (enterprise registry internal cache)
    art-->>pip: 200 (filtered simple index)

    pip->>art: GET tarball for requests-2.29.0
    art->>art: Cache miss
    art->>proxy: GET /external?url=.../requests-2.29.0.tar.gz
    proxy->>proxy: EvaluateVersion (allowed)
    proxy->>pypi: GET tarball
    pypi-->>proxy: 200 tarball
    proxy-->>art: 200 tarball
    art->>art: Cache tarball
    art-->>pip: 200 tarball
```

---

## 9. Sequence Diagram — npm Package Install (Topology A)

`npm install lodash`. Age filter 30 days. One version too new.

```mermaid
sequenceDiagram
    participant npm_cli as npm client
    participant proxy as npm-pkguard proxy
    participant cache as In-Process Cache
    participant engine as Rule Engine
    participant npmjs as registry.npmjs.org

    npm_cli->>proxy: GET /lodash
    proxy->>engine: EvaluatePackage("lodash")
    engine-->>proxy: {Allowed: true}

    proxy->>cache: Get("/lodash")
    cache-->>proxy: miss

    proxy->>npmjs: GET /lodash (full packument)
    npmjs-->>proxy: 200 JSON (versions: 4.17.19, 4.17.20, 4.17.21[published today])

    proxy->>engine: EvaluateVersion("lodash", "4.17.19", time)
    engine-->>proxy: {Allowed: true}
    proxy->>engine: EvaluateVersion("lodash", "4.17.20", time)
    engine-->>proxy: {Allowed: true}
    proxy->>engine: EvaluateVersion("lodash", "4.17.21", today)
    engine-->>proxy: {Allowed: false, Reason: "version too new"}

    proxy->>proxy: Remove 4.17.21 from packument\nUpdate dist-tags.latest → "4.17.20"
    proxy->>cache: Set("/lodash", filteredPackument)
    proxy-->>npm_cli: 200 JSON (filtered packument)\nX-Cache: MISS\nX-Curation-Policy-Notice: 1 version(s) removed

    npm_cli->>proxy: GET /lodash/-/lodash-4.17.20.tgz
    proxy->>engine: EvaluateVersion("lodash", "4.17.20", time)
    engine-->>proxy: {Allowed: true}
    proxy->>npmjs: GET /lodash/-/lodash-4.17.20.tgz
    npmjs-->>proxy: 200 tarball
    proxy-->>npm_cli: 200 tarball (streamed, unmodified)
```

---

## 10. Sequence Diagram — Typosquatting Detection

An attacker publishes `reqvests` (edit distance 1 from `requests`). A developer mistypes the package name.

```mermaid
sequenceDiagram
    participant pip as pip client
    participant proxy as pypi-pkguard proxy
    participant engine as Rule Engine

    pip->>proxy: GET /simple/reqvests/

    proxy->>engine: EvaluatePackage("reqvests")
    engine->>engine: CheckNamespaceProtection: no match
    engine->>engine: CheckTyposquatting("reqvests")\nvs protected: ["requests", "flask", "django", ...]\nLevenshtein("reqvests", "requests") = 2\nLevenshtein("reqvests", "reqests") = 1 (hypothetical)
    Note over engine: distance ≤ MaxEditDistance (2)
    engine-->>proxy: {Allowed: false, Rule: "typosquatting",\nReason: "name too similar to protected package 'requests'"}

    proxy-->>pip: 403 Forbidden\n{"error":"package denied by policy","package":"reqvests","reason":"name too similar to protected package 'requests'"}
```

---

## 11. Sequence Diagram — Rule Reload via Admin API

Operator updates the rules YAML file on disk and triggers a live reload.

```mermaid
sequenceDiagram
    participant ops as Operator
    participant admin as Admin API :18099
    participant proxy as Proxy (Server)
    participant config as Config / RuleEngine

    ops->>ops: Edit rules in config.yaml on disk\n(or via ConfigMap update in k8s)

    ops->>admin: POST /admin/rules/reload\n(Basic Auth header)
    admin->>admin: Verify Basic Auth credentials
    admin->>config: ReadConfig(configPath)
    config->>config: Validate updated config\nCompile new RuleEngine
    config-->>admin: new *RuleEngine

    admin->>proxy: atomic.Value.Store(newEngine)
    Note over proxy: In-flight requests continue using old engine\nNew requests pick up new engine atomically

    admin-->>ops: 200 {"reloaded": true, "rules_count": 12}

    ops->>admin: POST /admin/cache/invalidate\n(flush stale cached responses)
    admin->>proxy: cache.Invalidate("")
    admin-->>ops: 200 {"invalidated": true}
```

---

## 12. Sequence Diagram — Cache Behaviour (HIT path)

Second request for the same package within TTL.

```mermaid
sequenceDiagram
    participant client as Package Manager Client
    participant proxy as PKGuard
    participant cache as In-Process Cache
    participant upstream as Upstream Registry

    Note over client,upstream: First request (MISS path)
    client->>proxy: GET /simple/django/
    proxy->>cache: Get("/simple/django/")
    cache-->>proxy: miss
    proxy->>upstream: GET /simple/django/ + GET /pypi/django/json
    upstream-->>proxy: metadata + timestamps
    proxy->>proxy: filter versions
    proxy->>cache: Set("/simple/django/", filteredResponse, TTL=300s)
    proxy-->>client: 200 filtered response (X-Cache: MISS)

    Note over client,upstream: Second request within TTL (HIT path)
    client->>proxy: GET /simple/django/
    proxy->>cache: Get("/simple/django/")
    cache-->>proxy: hit (data, created 45s ago, TTL 300s)
    proxy-->>client: 200 filtered response (X-Cache: HIT)
    Note over upstream: Upstream not contacted
```

---

## 13. Sequence Diagram — Upstream Error Handling

The upstream registry returns a 503. The proxy fails gracefully.

```mermaid
sequenceDiagram
    participant client as Package Manager Client
    participant proxy as PKGuard
    participant upstream as Upstream Registry

    client->>proxy: GET /simple/flask/
    proxy->>proxy: EvaluatePackage: allowed
    proxy->>proxy: Cache: miss
    proxy->>upstream: GET /simple/flask/
    upstream-->>proxy: 503 Service Unavailable

    proxy->>proxy: log error (level=error)\n{msg: "upstream error", status: 503, url: "/simple/flask/"}
    proxy->>proxy: increment upstream_errors counter

    proxy-->>client: 502 Bad Gateway\n{"error": "upstream unavailable", "status": 503}
```

---

## 14. Deployment — Kubernetes (Single Ecosystem)

Standard Kubernetes deployment for `pypi-pkguard` in a dedicated namespace.

```mermaid
flowchart TB
    subgraph Kubernetes Cluster
        subgraph curation-ns — namespace
            direction TB
            SA["ServiceAccount\npkguard-pypi-sa\n(no RBAC)"]
            CM["ConfigMap\npkguard-pypi-config\nconfig.yaml data key"]
            SVC["Service\nClusterIP :18000\n→ pods :18000"]

            subgraph Deployment — 2 replicas
                POD1["Pod 1\npypi-pkguard container\nUID 1001 (non-root)\nresources: 100m/64Mi req\n500m/256Mi lim\nvolumeMount: /app/config.yaml"]
                POD2["Pod 2\n(same spec)"]
            end

            CM --> POD1
            CM --> POD2
            SA --> POD1
            SA --> POD2
        end

        subgraph dev-tools — namespace
            PIP["Developer Pod\n(pip, npm, cargo, etc.)"]
        end

        subgraph monitoring — namespace
            PROM["Prometheus\n(scrapes /metrics via json-exporter sidecar)"]
        end

        PIP -->|ClusterIP| SVC
        SVC --> POD1
        SVC --> POD2
        PROM -->|scrape :18000/metrics| SVC
    end

    subgraph External
        PYPI2["pypi.org"]
    end

    POD1 -->|HTTPS| PYPI2
    POD2 -->|HTTPS| PYPI2
```

---

## 15. Data Flow — Rule Evaluation Priority Order

The rule engine evaluates rules in strict priority order. The first matching rule wins.

```mermaid
flowchart TD
    A[Incoming package + version request] --> B{Namespace\nProtection\nenabled?}
    B -->|Yes| C{Matches\ninternal pattern\nor package?}
    B -->|No| D{Explicit\nPackage Rule\nmatches?}
    C -->|Yes, action=deny| DENY1[DENY: namespace protection]
    C -->|Yes, action=warn| WARN1[WARN + continue]
    C -->|No| D

    D -->|deny rule match| DENY2[DENY: explicit rule]
    D -->|allow rule match| AGE[Skip to age check\nbypass_age_filter?]
    D -->|no match| E{Typosquatting\ncheck\nenabled?}

    E -->|distance ≤ maxEditDistance| DENY3[DENY: typosquatting]
    E -->|no match| F[Version-level evaluation]

    F --> G{Pinned version\nmatch?}
    G -->|Yes| ALLOW1[ALLOW: pinned version]
    G -->|No| H{Version pattern\nrule match?}

    H -->|deny pattern| DENY4[DENY: version pattern]
    H -->|allow pattern| ALLOW2[ALLOW: version pattern]
    H -->|no match| I{Pre-release\nand block_pre_releases?}

    I -->|pre-release + blocked| DENY5[DENY: pre-release]
    I -->|stable or not blocked| J{Age filter:\nuploadTime + minAge\n< now?}

    J -->|too new| DENY6[DENY: age filter]
    J -->|old enough or zero time| K{CVE advisory\ncheck - future}

    K -->|vulnerable version| DENY7[DENY: CVE advisory]
    K -->|clean or disabled| ALLOW3[ALLOW]

    AGE -->|bypass_age_filter=true| ALLOW4[ALLOW: bypassed age]
    AGE -->|bypass_age_filter=false| I

    DENY1 --> DRY{DryRun\nmode?}
    DENY2 --> DRY
    DENY3 --> DRY
    DENY4 --> DRY
    DENY5 --> DRY
    DENY6 --> DRY
    DENY7 --> DRY
    DRY -->|Yes| DRYALLOW[ALLOW + log warn\ndry_run=true\nincrement dry_run_blocked]
    DRY -->|No| FINAL_DENY[Final DENY]

    style DENY1 fill:#dc2626,color:#fff
    style DENY2 fill:#dc2626,color:#fff
    style DENY3 fill:#dc2626,color:#fff
    style DENY4 fill:#dc2626,color:#fff
    style DENY5 fill:#dc2626,color:#fff
    style DENY6 fill:#dc2626,color:#fff
    style DENY7 fill:#dc2626,color:#fff
    style FINAL_DENY fill:#dc2626,color:#fff
    style ALLOW1 fill:#16a34a,color:#fff
    style ALLOW2 fill:#16a34a,color:#fff
    style ALLOW3 fill:#16a34a,color:#fff
    style ALLOW4 fill:#16a34a,color:#fff
    style DRYALLOW fill:#ca8a04,color:#fff
```

---

## 16. Component Interaction — Live E2E Test Stack (Topology A)

The E2E test suite compiles the proxy binaries, starts them as child processes, and sends real HTTP
requests to public package registries. **No mocks are used.** Tests are gated by `//go:build e2e`
and the `PKGUARD_E2E_LIVE=true` environment variable.

Topology B compatibility cannot be tested in open-source CI. See
the Topology B section in `README.md` for integration guidance.

```mermaid
flowchart TB
    subgraph Live E2E Test Process
        TM["TestMain\ngo test -tags=e2e ./e2e/...\n\n1. go build each proxy binary\n2. start proxy processes\n3. wait for /healthz\n4. run test functions\n5. kill processes"]
    end

    subgraph Proxy Processes (test-managed child processes)
        PA["pypi-pkguard :18100\nupstream=https://pypi.org\nrules: allow all (age=0)"]
        NA["npm-pkguard :18101\nupstream=https://registry.npmjs.org\nrules: allow all (age=0)"]
        MA["maven-pkguard :18102\nupstream=https://repo1.maven.org/maven2\nrules: allow all (age=0)"]
    end

    subgraph Public Registries (real internet)
        PR["pypi.org\nPyPI Simple Index + Metadata API"]
        NR["registry.npmjs.org\nnpm packument API"]
        MR["repo1.maven.org/maven2\nMaven Central"]
    end

    TM -->|HTTP :18100| PA
    TM -->|HTTP :18101| NA
    TM -->|HTTP :18102| MA
    PA -->|HTTPS| PR
    NA -->|HTTPS| NR
    MA -->|HTTPS| MR

    style TM fill:#2563eb,color:#fff
    style PA fill:#2563eb,color:#fff
    style NA fill:#2563eb,color:#fff
    style MA fill:#2563eb,color:#fff
    style PR fill:#16a34a,color:#fff
    style NR fill:#16a34a,color:#fff
    style MR fill:#16a34a,color:#fff
```

**Resilience rules:**

- Each test calls `t.Skip` if the upstream is unreachable (DNS failure, timeout) — never fails CI on transient outage.
- Stable, ancient packages are used (published ≥ 3 years ago) so age-filter and block rules can be exercised predictably.
- Tests do **not** assert on exact version lists (registries add versions over time); they assert on presence of specific known-old versions.

**Stable test packages:**

| Ecosystem | Package                 | Version     | Published  |
| --------- | ----------------------- | ----------- | ---------- |
| PyPI      | `pip`                   | `22.3.1`    | 2022-11-07 |
| PyPI      | `certifi`               | `2022.12.7` | 2022-12-07 |
| PyPI      | `urllib3`               | `1.26.14`   | 2023-01-11 |
| npm       | `lodash`                | `4.17.21`   | 2021-02-20 |
| npm       | `ms`                    | `2.1.3`     | 2020-03-17 |
| npm       | `is-odd`                | `3.0.1`     | 2018-10-15 |
| Maven     | `junit:junit`           | `4.13.2`    | 2021-02-13 |
| Maven     | `commons-io:commons-io` | `2.11.0`    | 2021-07-13 |
| Maven     | `org.slf4j:slf4j-api`   | `1.7.36`    | 2022-03-16 |

---

## 16.1 Docker-Based E2E Tests (Real Clients, Multi-Rule Configs)

In addition to the Go-based live E2E tests, the `e2e/docker/` directory contains Docker Compose-based integration tests that run real package manager clients through the curation proxies in containers. The test runner (`run.sh`) executes phases one at a time: each phase starts a single proxy container with a specific config, runs the corresponding test client container, then tears down before moving to the next phase.

Each ecosystem includes a **real-life** configuration where **all rules are active simultaneously**, simulating production enterprise policy:

- **npm** (`npm-real-life.yaml`): trusted scopes (`@types/*`, `@babel/*`), install scripts deny (esbuild exempted), 7-day age, pre-release block, explicit deny (event-stream), canary/nightly version patterns.
- **PyPI** (`pypi-real-life.yaml`): trusted packages (setuptools, pip, wheel), 7-day age, pre-release block, explicit deny (python3-dateutil), dev/alpha/beta version patterns.
- **Maven** (`maven-real-life.yaml`): trusted group (`org/apache/commons:*`, `commons-io:*`), 7-day age, pre-release block, SNAPSHOT block, explicit deny (junit:junit), milestone/RC version patterns.

**Total Docker E2E test count:** npm 33, PyPI 27, Maven 30 (90 tests across all phases).

See `e2e/docker/README.md` for the full test matrix.

---

## 17. Security Threat Model

```mermaid
flowchart LR
    subgraph External Threats
        T1["Typosquatting\n(reqvests → requests)"]
        T2["Malicious new package\n(published today)"]
        T3["Namespace hijack\n(myco-utils published\nby attacker)"]
        T4["Velocity attack\n(50 versions in 1 hour)"]
        T5["Install script attack\n(postinstall: curl ...)"]
        T6["CVE in dependency\n(known vulnerable version)"]
    end

    subgraph PKGuard Controls
        C1["Typosquatting detection\n(Levenshtein distance)"]
        C2["Age filter\n(min_package_age_days)"]
        C3["Namespace protection\n(internal pattern match)"]
        C4["Velocity detection\n(sliding window check)"]
        C5["Install scripts check\n(pre/post/install keys)"]
        C6["CVE advisory check\n(OSV.dev feed — future)"]
    end

    T1 --> C1
    T2 --> C2
    T3 --> C3
    T4 --> C4
    T5 --> C5
    T6 --> C6

    subgraph Outcome
        BLOCK["Package/version\nblocked (403/404)\nAudit log entry"]
        WARN["Warn + pass through\n(dry-run or warn action)\nAudit log entry"]
    end

    C1 --> BLOCK
    C2 --> BLOCK
    C3 --> BLOCK
    C4 --> WARN
    C5 --> BLOCK
    C6 --> BLOCK

    style BLOCK fill:#dc2626,color:#fff
    style WARN fill:#ca8a04,color:#fff
```

---

## 18. Configuration Schema — Topology Selection

Both topologies are selected by changing a single configuration key. The same binary, same rule engine, same everything.

```mermaid
flowchart LR
    subgraph "Topology A — config.yaml"
        A1["upstream:\n  base_url: https://pypi.org\n  # (or registry.npmjs.org, etc.)"]
    end

    subgraph "Topology B — config-enterprise.yaml"
        B1["upstream:\n  base_url: https://registry.corp.example/pypi\n  auth:\n    token: env:PKGUARD_AUTH_TOKEN\n  tls:\n    ca_cert_file: /certs/internal-ca.pem"]
    end

    A1 --> SAME["Same binary\nSame rule engine\nSame filtering pipeline"]
    B1 --> SAME

    SAME --> OUT1["Upstream requests\ngo to PyPI directly\n(Topology A)"]
    SAME --> OUT2["Upstream requests\ngo to enterprise registry\n(Topology B)"]
```

---

## 19. One-Click Installer Architecture

Each proxy binary embeds its `config-best-practices.yaml` via Go's `//go:embed` directive. The shared `common/installer` package provides platform-aware setup and uninstall logic.

### Installer Flow

```mermaid
flowchart TD
    A["User runs: binary -setup"] --> B["installer.Setup()"]
    B --> C["os.UserHomeDir()"]
    B --> D["os.Executable()"]
    C --> E["SetupFiles()"]
    D --> E
    E --> F["Create ~/.pkguard/<ecosystem>/"]
    E --> G["Write config.yaml\n(embedded best-practices)"]
    E --> H["Copy binary to\n~/.pkguard/bin/"]
    E --> I["writePkgMgrConfig()"]
    E --> J["writeAutostartFile()"]
    I --> K{"Ecosystem?"}
    K -->|npm| L["Deferred to ActivateServices"]
    K -->|pypi| M["Write pip.conf / pip.ini"]
    K -->|maven| N["Write settings.xml\n(backup existing)"]
    J --> O{"OS?"}
    O -->|macOS| P["Write LaunchAgent plist"]
    O -->|Linux| Q["Write systemd user service"]
    O -->|Windows| R["Write Startup .bat"]
    E --> S["ActivateServices()"]
    S --> T["npm config set registry\n(if npm ecosystem)"]
    S --> U{"OS?"}
    U -->|macOS| V["launchctl load"]
    U -->|Linux| W["systemctl --user enable"]
    U -->|Windows| X["Print manual start instructions"]
```

### Installed File Layout

```
~/.pkguard/
├── bin/
│   ├── npm-pkguard          # (or .exe on Windows)
│   ├── pypi-pkguard
│   └── maven-pkguard
├── npm-pkguard/
│   └── config.yaml           # Editable rules config
├── pypi-pkguard/
│   └── config.yaml
└── maven-pkguard/
    └── config.yaml
```

### Platform-Specific Autostart

| OS | Mechanism | File Location |
|----|-----------|---------------|
| macOS | LaunchAgent | `~/Library/LaunchAgents/com.pkguard.<eco>.plist` |
| Linux | systemd user service | `~/.config/systemd/user/pkguard-<eco>.service` |
| Windows | Startup batch file | `%APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\pkguard-<eco>.bat` |

### Design Decisions

- **`//go:embed`** for config: the binary is self-contained; no need to download config separately.
- **`goos` parameter** on all file-system functions: enables cross-platform unit testing without mocking `runtime.GOOS`.
- **Separation of `SetupFiles` vs `ActivateServices`**: file-only operations are fully unit-testable with `t.TempDir()`; external commands (`launchctl`, `systemctl`, `npm`) are isolated with documented coverage exemptions.
- **Maven backup/restore**: existing `settings.xml` is backed up to `settings.xml.pkguard-backup` on setup and restored on uninstall.
