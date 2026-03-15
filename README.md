# Bulwark

**Bulwark** is a lightweight, zero-dependency security gateway that sits between your package managers and public registries (PyPI, npm, Maven Central). It inspects every package request against configurable policy rules and blocks anything risky, before it reaches your developers or CI pipeline.

No database, UI, or vendor lock-in; just a single Go binary per ecosystem, a YAML config file, and full control over your software supply chain.

---

## Why We Built This

Software supply chain attacks are the fastest-growing threat vector in the industry. From [event-stream](https://blog.npmjs.org/post/180565383195/details-about-the-event-stream-incident) and [ua-parser-js](https://github.com/nicercs/ua-parser-js-security) to [PyPI malware campaigns](https://blog.phylum.io/pypi-malware-replaces-crypto-addresses-in-developer-clipboards/), these attacks hit organizations of every size. Threats like the Shai-Hulud virus can reach any developer with a laptop connected to the internet.

The risk is getting worse. AI agents have lowered the barrier to development — great for innovation, but many new developers lack awareness of package management and supply chain security. They pull dependencies without thinking, trusting that "public packages must be safe."

Teams face three choices:
1. **Do nothing** — trust the open-source ecosystem. Fast, but completely unprotected.
2. **Buy a commercial platform** — enterprise artifact repositories and SCA scanners exist. You get controls, but at significant cost with opaque rule engines and vendor lock-in.
3. **Bulwark** — a transparent, self-hosted policy layer *you* own. Write rules in YAML. Version-control them. Deploy on Friday afternoon. Immediately protect your org.

We built Bulwark as an open-source answer to option 3.

---

## The 5-Minute Demo: See It Filter Packages in Real-Time

Watch Bulwark automatically apply safety rules to protect your package stream.

**Step 1: Start Bulwark**

```bash
docker-compose -f docker-compose.demo.yml up -d
```

Wait ~10 seconds for the container to boot. Check health:
```bash
curl http://localhost:18001/healthz
```

**Step 2: Configure npm**

```bash
npm config set registry http://localhost:18001/
```

**Step 3: Install a Well-Established Package**

```bash
npm install lodash
```

This succeeds. `lodash` is years old and passes Bulwark's 7-day minimum age check.

**Step 4: Try a Known-Malicious Package (Blocked by Policy)**

```bash
npm install event-stream
```

This fails. `event-stream` is on the deny list ([compromised in 2018](https://blog.npmjs.org/post/180565383195/details-about-the-event-stream-incident)), so Bulwark blocks it before any code reaches your machine.

**Step 5: Try a Package with Install Scripts (Blocked by Policy)**

```bash
npm install bcrypt
```

This fails. `bcrypt` has native `install` scripts in every published version, and it isn't in the trusted allowlist. Bulwark strips all those versions, leaving nothing installable. Your policies are enforced at the network level — no potentially malicious scripts execute.

**Step 6: Try a Typosquatted Package (Blocked by Policy)**

```bash
npm install loadsh
```

This fails. `loadsh` is 1 edit away from `lodash`, typical typosquat. Bulwark's Levenshtein distance check catches it automatically and blocks the install. Real supply chain attacks [use exactly this technique](https://blog.phylum.io/typosquatting-campaign-targets-popular-npm-packages/).

**Step 7: Try a Brand-New Package**

```bash
npm install any-package-published-today
```

This fails. Even if legitimate, Bulwark's 7-day quarantine window blocks it by default. This prevents zero-day exploits before the community has time to discover them.

To clean up:
```bash
docker-compose -f docker-compose.demo.yml down
npm config delete registry  # restore default npm registry

# Remove the Docker images built during the demo
docker rmi bulwark-npm:latest bulwark-pypi:latest bulwark-maven:latest 2>/dev/null
# Remove any dangling build cache
docker builder prune -f
```

---

## How It Works (2-Minute Explanation)

When a package request arrives:

1. **Package check:** Does the package name match your deny lists? Is it typosquatted? Does it look suspicious? Block immediately if any rule fires.

2. **Version filtering:** For allowed packages, Bulwark fetches the version list from the upstream registry and filters each version:
   - **Too new?** Block if published < N days ago (quarantine window for zero-days).
   - **Pre-release?** Block alpha/beta/RC if your policy says so.
   - **Install scripts?** Block npm `preinstall`/`postinstall` scripts unless allowlisted.
   - **Regex patterns?** Block versions matching custom patterns (e.g., anything with "rc" or "dev").
   - **Pinned approved?** Bypass age/other checks if you've explicitly approved the exact version.

3. **Response rewriting:** Remove blocked versions from the response. When *some* versions pass policy, the filtered response is returned normally. When a package is entirely blocked (package-level deny or all versions removed), Bulwark returns **HTTP 403** with a `[Bulwark] package: reason` message so your package manager displays a meaningful error instead of a confusing "no versions found" message. Direct download blocks (tarballs, artifacts) include the same structured message with the specific version and rule reason.

4. **Caching:** Cache filtered responses in memory (configurable TTL) so repeated requests don't hit the upstream registry repeatedly.

**Result:** A single Go binary, YAML config, and your package managers now have transparent, auditable supply chain protection.


**When to choose Bulwark:** You want immediate, transparent control without committing to a commercial platform. You're comfortable without a CVE feed today (layerable later).

**When commercial makes sense:** You need a constantly-updated CVE feed with SLA-backed updates.

---

## Deployment Topologies

### Option A: Direct Proxy (Tested)

Point your package managers directly at Bulwark. Simplest setup, most transparent.

```
Developer → npm/pip/mvn → Bulwark → PyPI/npm/Maven Central
```

### Option B: Behind Enterprise Registry (Not tested yet, but should work as is)
Keep your existing enterprise registry (any artifact repository that supports remote/proxy repositories). Reconfigure its remote/proxy to fetch through Bulwark. No developer client changes needed. **Feedback welcome — report your findings if you deploy this.**

```
Developer → npm/pip/mvn → Enterprise Registry → Bulwark → PyPI/npm/Maven Central
```

---

## Features

- **PyPI**: PEP 691 JSON + HTML simple index, `/pypi/<pkg>/json` passthrough, external tarball proxy with allowlist.
- **npm**: Packument filtering, tarball proxy, scoped packages (`@scope/pkg`), install script detection.
- **Maven**: `maven-metadata.xml` filtering, checksum invalidation, artifact policy, SNAPSHOT blocking.
- **Shared rule engine:** Trusted package allowlists, pre-release blocking, age quarantine, license filtering, version pinning, deny lists, regex patterns, namespace protection, typosquatting detection, velocity anomalies, dry-run mode.
- **Operational:** YAML config, structured logging (log/slog), dynamic log-level API, disk file logging, in-memory TTL cache, `/healthz` & `/readyz` probes, JSON metrics.

---

## Getting Started

### Prerequisites

- **No prerequisites for pre-built binaries** — download and run.
- Go 1.26+ (only if building from source)
- Docker (optional, for containerized deployment)

### Option 1: One-Click Installer (Recommended for Non-Developers)

The fastest way to get started. The installer downloads the correct binary for your platform, installs it, configures your package manager, creates an autostart entry, and applies the best-practices security rules — all in one command.

> **Note:** This requires at least one [GitHub Release](../../releases) to exist. Maintainers: see [Releasing](#releasing) below.

**macOS / Linux:**

```bash
curl -fsSL https://raw.githubusercontent.com/Bluewaves54/Bulwark/main/scripts/install.sh | bash
```

**Windows (PowerShell):**

```powershell
irm https://raw.githubusercontent.com/Bluewaves54/Bulwark/main/scripts/install.ps1 | iex
```

**Install specific ecosystems only:**

macOS / Linux — install only the npm and pypi proxies:
```bash
curl -fsSL https://raw.githubusercontent.com/Bluewaves54/Bulwark/main/scripts/install.sh | bash -s -- npm pypi
```
Windows — install only maven:
```bash
& { irm https://raw.githubusercontent.com/Bluewaves54/Bulwark/main/scripts/install.ps1 } maven
```

**What the installer does:**

1. Downloads the correct binary for your OS and architecture.
2. Copies it to `~/.bulwark/bin/<ecosystem>-bulwark`.
3. Writes the best-practices config to `~/.bulwark/<ecosystem>-bulwark/config.yaml`.
4. Configures your package manager (npm registry, pip index-url, Maven mirror).
5. Creates an autostart entry (macOS LaunchAgent, Linux systemd user service, Windows Startup batch).

**After installation — reconfiguring rules:**

Edit the config file for the ecosystem you want to change:

```bash
# npm rules:
nano ~/.bulwark/npm-bulwark/config.yaml

# pypi rules:
nano ~/.bulwark/pypi-bulwark/config.yaml

# maven rules:
nano ~/.bulwark/maven-bulwark/config.yaml
```

The service restarts automatically on reboot. To apply changes immediately, restart the proxy using the `-setup` flag or restart the service:

```bash
# macOS:
launchctl unload ~/Library/LaunchAgents/com.bulwark.npm.plist
launchctl load ~/Library/LaunchAgents/com.bulwark.npm.plist

# Linux:
systemctl --user restart bulwark-npm.service
```

**Uninstalling:**

```bash
~/.bulwark/bin/npm-bulwark -uninstall
~/.bulwark/bin/pypi-bulwark -uninstall
~/.bulwark/bin/maven-bulwark -uninstall
```

The uninstall command restores your original package manager configuration (npm registry, Maven settings.xml backup).

**Using the `-setup` flag directly (if you already have the binary):**

```bash
./npm-bulwark -setup       # Install with best-practices config, or your edited config file
```

### Option 2: Download Pre-Built Binary (Zero-Config)

Pre-built binaries are available for every release on the [GitHub Releases](../../releases) page.

| Platform | Architectures |
|----------|--------------|
| Linux | amd64, arm64 |
| macOS | amd64 (Intel), arm64 (Apple Silicon) |
| Windows | amd64, arm64 |

**Quick start — just download and run:**

```bash
# Download the binary from GitHub or from the command line
curl -LO https://github.com/Bluewaves54/Bulwark/releases/latest/download/npm-bulwark-linux-amd64
chmod +x npm-bulwark-linux-amd64

# Run it — first launch automatically sets up everything:
./npm-bulwark-linux-amd64
```

On first run the binary detects that no config exists, performs a full setup (writes best-practices config to `~/.bulwark/<ecosystem>-bulwark/config.yaml`, configures your package manager, and creates an autostart entry), then starts the proxy. No extra downloads or terminal commands needed.

To use a custom config instead of the auto-installed one (requires same format):

```bash
./npm-bulwark-linux-amd64 -config my-custom-config.yaml
```

On macOS use `darwin-arm64`, on Windows use `npm-bulwark-windows-amd64.exe`.

### Option 3: Best Practices Config (Build from Source)

Each ecosystem includes a `config-best-practices.yaml` — a production-ready policy file you can deploy immediately. These configs are curated with real-world supply chain attack data and sensible defaults:

| Config | What's included |
|--------|----------------|
| `npm-bulwark/config-best-practices.yaml` | 38 known-malicious/typosquatted packages blocked, install script deny with allowlist (esbuild, node-gyp, sharp), typosquat detection (Levenshtein) protecting lodash/express/react/axios/etc., 7-day age quarantine, pre-release blocking, trusted scopes (@types/\*, @babel/\*, @angular/\*) |
| `pypi-bulwark/config-best-practices.yaml` | 42 known-malicious/typosquatted packages blocked (colourama, python3-dateutil, noblesse, ctx, etc.), 7-day age quarantine, pre-release blocking, trusted packages (pip, setuptools, numpy\*, django\*, flask\*, requests) |
| `maven-bulwark/config-best-practices.yaml` | 15 malicious/impersonation artifacts blocked (Log4Shell impersonators, namespace squatting on Spring/Apache/AWS SDK), SNAPSHOT blocking, 7-day age quarantine, pre-release blocking, trusted groups (org.apache.\*, org.springframework.\*, com.google.\*) |

**Deploy on Friday and immediately protect your org:**

```bash
cd npm-bulwark
go build -o bin/npm-bulwark .
./bin/npm-bulwark -config config-best-practices.yaml
```

Then configure npm:
```bash
npm config set registry http://localhost:18001/
npm install lodash  # Works (trusted)
npm install event-stream  # Blocked (known malware)
```

### Option 4: Build and Run (Default Config)

**PyPI** (port 18000):
```bash
cd pypi-bulwark && go build -o bin/pypi-bulwark . && ./bin/pypi-bulwark -config config.yaml
```

```ini
# ~/.pip/pip.conf
[global]
index-url = http://localhost:18000/simple/
```

**npm** (port 18001):
```bash
cd npm-bulwark && go build -o bin/npm-bulwark . && ./bin/npm-bulwark -config config.yaml
```

```bash
npm config set registry http://localhost:18001/
```

**Maven** (port 18002):
```bash
cd maven-bulwark && go build -o bin/maven-bulwark . && ./bin/maven-bulwark -config config.yaml
```

```xml
<!-- ~/.m2/settings.xml -->
<settings>
  <mirrors>
    <mirror>
      <id>bulwark-maven</id>
      <mirrorOf>central</mirrorOf>
      <url>http://localhost:18002</url>
    </mirror>
  </mirrors>
</settings>
```

### Option 5: Docker

```bash
# Build
docker build -f npm-bulwark/Dockerfile -t bulwark-npm:latest .

# Run with best practices config
docker run -p 18001:18001 \
  -v $(pwd)/npm-bulwark/config-best-practices.yaml:/app/config.yaml \
  bulwark-npm:latest
```

Or use the demo Compose setup:
```bash
docker-compose -f docker-compose.demo.yml up -d
```

### Option 6: Kubernetes

```bash
kubectl apply -f k8s/npm-bulwark/
kubectl apply -f k8s/pypi-bulwark/
kubectl apply -f k8s/maven-bulwark/
```

---

## Configuration

Each proxy reads its own `config.yaml`. Here's a minimal example:

```yaml
server:
  port: 18001

upstream:
  url: "https://registry.npmjs.org"
  timeout_seconds: 30

cache:
  ttl_seconds: 300

policy:
  trusted_packages:
    - "react"
    - "lodash"
  install_scripts:
    enabled: true
    action: "deny"
    allowed_with_scripts: ["esbuild"]
  defaults:
    min_package_age_days: 3
    block_pre_releases: true
  rules:
    - name: "deny-known-bad"
      action: "deny"
      package_patterns:
        - "event-stream"
        - "flatmap-stream"
      reason: "Known malicious package"
```

**Use `config-best-practices.yaml` for production-ready defaults** — includes trusted package allowlists, install script blocking, and known malware blockers.

### Environment Variable Overrides

| Variable               | Description                      |
|------------------------|----------------------------------|
| `PORT`                 | Override `server.port`           |
| `BULWARK_AUTH_TOKEN`   | Bearer token for upstream auth   |
| `BULWARK_AUTH_USERNAME`| Basic-auth username              |
| `BULWARK_AUTH_PASSWORD`| Basic-auth password              |

### Logging

Bulwark uses Go's `log/slog` for structured logging with configurable levels, optional disk output, and runtime level changes.

```yaml
logging:
  level: "info"          # debug | info | warn | error
  format: "text"         # text | json
  file_path: "/var/log/bulwark/npm.log"  # optional; logs also written to this file
```

When `file_path` is set, log output is written to **both** stdout and the specified file using `io.MultiWriter`.

**Dynamic log-level changes** — every proxy exposes an admin API to adjust the log level at runtime without restarting:

```bash
# Get current level
curl http://localhost:18001/admin/log-level
# → {"level":"info"}

# Set level to debug
curl -X PUT http://localhost:18001/admin/log-level \
  -d '{"level":"debug"}'
# → {"level":"debug"}
```

Blocked packages are logged with structured fields including the package name, version, rule name, and reason for blocking.

---

## Testing

### Unit Tests

```bash
for mod in common npm-bulwark pypi-bulwark maven-bulwark; do
  (cd $mod && go test -count=1 -race -coverprofile=coverage.out ./... && \
   go tool cover -func=coverage.out | grep "^total:")
done
```

All modules maintain 90%+ statement coverage.

### Docker E2E Tests

```bash
cd e2e/docker && ./run.sh          # Linux/macOS
cd e2e\docker && .\run.ps1         # Windows PowerShell

# Individual ecosystems
./run.sh --python-only
./run.sh --node-only
./run.sh --java-only
```

See [e2e/docker/README.md](e2e/docker/README.md) for details.

---

## Releasing

Binaries are built and published automatically by the GitHub Actions [release workflow](.github/workflows/release.yml) when a version tag is pushed.

```bash
# Tag the current commit and push — this triggers the release workflow
git tag v0.1.0
git push origin v0.1.0
```

The workflow will:
1. Run all unit tests.
2. Cross-compile binaries for 6 platforms (linux/darwin/windows × amd64/arm64).
3. Build and push Docker images to `ghcr.io`.
4. Create a GitHub Release with all binaries and checksums attached.

Once the release is published, the [one-click installer](#option-1-one-click-installer-recommended-for-non-developers) and [pre-built binary downloads](#option-2-download-pre-built-binary) will work.

---

## CLI Flags

All three proxy binaries (`npm-bulwark`, `pypi-bulwark`, `maven-bulwark`) accept the same flags:

| Flag | Description |
|------|-------------|
| `-setup` | Install Bulwark with best-practices config and configure the package manager |
| `-uninstall` | Remove Bulwark and restore the original package manager configuration |
| `-background` | Start the proxy as a detached background process (no terminal needed). Prints the PID and exits. Output logged to `~/.bulwark/<binary>/daemon.log` |
| `-config <path>` | Path to configuration file (default: `config.yaml`) |
| `-auth-token <token>` | Upstream auth bearer token (overrides config) |
| `-auth-username <user>` | Upstream auth username (overrides config) |
| `-auth-password <pass>` | Upstream auth password (overrides config) |

**Stopping a background process:**

```bash
# Find the PID (printed when started, or use ps)
ps aux | grep bulwark

# Stop it
kill <PID>
```

---

## API Endpoints

**Common endpoints (all three proxies):**

| Path | Purpose |
|------|-------|
| `GET /healthz` | Liveness probe — always 200 when running |
| `GET /readyz` | Readiness probe — checks upstream |
| `GET /metrics` | JSON metrics counters (enabled via config) |
| `GET /admin/log-level` | Get current log level |
| `PUT /admin/log-level` | Change log level at runtime (JSON body: `{"level":"debug"}`) |

**PyPI-specific (port 18000):**

| Path | Purpose |
|------|-------|
| `GET /simple/{pkg}/` | PEP 503/691 simple index — returns filtered HTML or JSON |
| `GET /simple/{pkg}` | Redirects to trailing-slash form |
| `GET /pypi/{pkg}/json` | PyPI JSON metadata API — filtered releases |
| `GET /external?url=...` | Proxied tarball download (host allowlist enforced) |

**npm-specific (port 18001):**

| Path | Purpose |
|------|-------|
| `GET /{pkg}` | Filtered packument (metadata + versions) |
| `GET /{pkg}/-/{file}.tgz` | Proxied tarball download with version policy check |

**Maven-specific (port 18002):**

| Path | Purpose |
|------|-------|
| `GET /.../maven-metadata.xml` | Filtered metadata — blocked versions removed from XML |
| `GET /.../artifact-version.jar` | Proxied artifact download with version policy check |
| `GET /.../artifact.sha1` | Checksum sidecar — returns 404 if base file was filtered |

---

## Documentation

- [**Architecture & Diagrams**](docs/ARCHITECTURE.md) — C4 system context, components, deployment sequences.
- [**Benchmarks**](docs/BENCHMARKS.md) — Performance baselines for filtering and rule evaluation.
- [**Future Enhancements**](docs/FUTURE_ENHANCEMENTS.md) — Roadmap: CVE feeds, distributed caching, observability.
- [**Contributing**](CONTRIBUTING.md) — Development workflow, quality gates, conventions.
- [**Changelog**](CHANGELOG.md) — Release notes and changes.

---

## License

Apache 2.0 — see [LICENSE](LICENSE).
