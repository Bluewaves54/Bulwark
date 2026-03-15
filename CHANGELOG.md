# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.1.0] — 2026-03-14

### Added

- **PyPI proxy** (`pypi-bulwark`): PEP 503/691 simple index (HTML + JSON), `/pypi/<pkg>/json` metadata endpoint, external tarball proxy with host allowlist.
- **npm proxy** (`npm-bulwark`): packument filtering, tarball proxy, scoped package support (`@scope/pkg`), install script detection.
- **Maven proxy** (`maven-bulwark`): `maven-metadata.xml` filtering, checksum invalidation, artifact policy enforcement, SNAPSHOT blocking.
- **Shared rule engine** (`common/rules`): trusted package allowlists, pre-release blocking, age quarantine (`min_package_age_days`), version pinning, deny lists, regex version patterns, namespace protection, typosquatting detection (Levenshtein distance), velocity anomaly detection, dry-run mode, license filtering.
- **One-click installer** (`common/installer`): `install.sh` (macOS/Linux) and `install.ps1` (Windows). Downloads correct binary, writes best-practices config, configures package manager, creates autostart entry (LaunchAgent / systemd / Windows Startup).
- **CLI flags**: `-setup`, `-uninstall`, `-background`, `-config`, `-auth-token`, `-auth-username`, `-auth-password`.
- **`-background` flag**: runs the proxy as a detached background process via `installer.Daemonize`. Output logged to `~/.bulwark/<binary>/daemon.log`.
- **First-run auto-setup**: launching a binary without an existing config automatically installs best-practices rules, configures the package manager, and starts the proxy.
- **Best-practices configs**: curated security rules for each ecosystem — known malware deny lists, install script blocking, typosquatting protection, 7-day age quarantine, pre-release blocking, trusted package allowlists.
- **Dynamic log level**: `GET/PUT /admin/log-level` admin endpoint to change log level at runtime without restart.
- **Structured logging**: `log/slog` with configurable level, format (text/json), and optional disk file output via `io.MultiWriter`.
- **In-memory TTL cache** (`common/rules/cache`): configurable TTL, `X-Cache: HIT/MISS` response headers.
- **Metrics**: JSON metrics at `/metrics` with `requests_total`, `requests_allowed`, `requests_denied`, `requests_dry_run`.
- **Health probes**: `/healthz` (liveness) and `/readyz` (upstream readiness) endpoints.
- **`fail_mode: closed`**: blocks requests when metadata parsing or policy evaluation fails (for regulated environments).
- **Enriched 403 responses**: all blocking responses include `[Bulwark] package: reason` with the specific rule name and rationale, including tarball/artifact/external download blocks.
- **Docker images**: multi-stage Dockerfiles for all three proxies, published to `ghcr.io`.
- **Kubernetes manifests**: Deployment, Service, ConfigMap for each ecosystem in `k8s/`.
- **GitHub Actions CI/CD**: `release.yml` triggered on `v*` tags — cross-compiles 6 platforms, builds Docker images, creates GitHub Release with binaries and checksums.
- **Docker E2E tests**: 90 tests across npm (33), PyPI (27), Maven (30) with real package manager clients.
- **Live E2E tests**: Go-based tests against real public registries with `//go:build e2e`.
- **Benchmarks**: documented performance baselines for rule evaluation and JSON/XML filtering pipelines.
- Documentation: README, ARCHITECTURE (C4 diagrams, sequence diagrams, deployment topologies), BENCHMARKS, FUTURE_ENHANCEMENTS, CONTRIBUTING, SECURITY, CODE_OF_CONDUCT.