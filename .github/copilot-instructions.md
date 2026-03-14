# GitHub Copilot Instructions — Corp-Registry Curation Proxy

This document defines the mandatory quality gates, conventions, and task-completion checklist that every AI-generated code contribution to this repository must satisfy. Read it in full before writing any code.

---

## 0. Git Safety Rule (Non-Negotiable)

**NEVER run `git commit` or `git push` unless the user explicitly tells you to.** You may stage files (`git add`), create branches, switch branches, and reset — but committing and pushing require explicit user approval every time.

---

## 1. Quality Gates (Non-Negotiable)

### 1.1 Unit Test Coverage

- Every Go module (`common/`, `pypi-curation/`, `npm-curation/`, and any future ecosystem module) must maintain **≥ 90% statement coverage** at all times.
- Run coverage check before marking any task complete:
  ```bash
  go test -count=1 -race -coverprofile=coverage.out ./...
  go tool cover -func=coverage.out | grep "^total:"
  ```
- PRs that reduce total coverage below 90% are **blocked** in CI.
- New code paths that are genuinely untestable (e.g., `os.Exit` paths, `init()`) must have a documented exemption in the test file via a comment.

### 1.2 Linting

- `golangci-lint run ./...` must produce **zero errors and zero warnings** before any task is complete.
- Enabled linters (configured in `.golangci.yml`):
  - `govet`, `errcheck`, `staticcheck`, `gosimple`, `ineffassign`, `unused`
  - `gocognit` (max cognitive complexity: 15)
  - `goconst` (min string occurrences before requiring constant: 3)
  - `misspell`, `godot`, `gofmt`, `goimports`
- Never suppress linter warnings with `//nolint` without a detailed inline justification.

### 1.3 SonarQube

- Zero **new** bugs, vulnerabilities, security hotspots, or code smells introduced per PR.
- Cognitive complexity per function ≤ 15. Extract helper functions and use early-return patterns to stay within budget.
- No string literal used 3+ times without a package-level constant.
- Test function names must match `^[a-zA-Z0-9]+$` — **no underscores** (use `TestLoadConfigValid`, not `TestLoadConfig_Valid`).
- Security rules: no hardcoded credentials, no `InsecureSkipVerify: true` propagated to production paths, no unvalidated URL redirects.

---

## 2. Documentation Requirements

**All relevant documentation must be updated before a coding task is considered complete.** Documentation is not optional and not deferred to a follow-up PR.

| Change type                   | Documents to update                                                          |
| ----------------------------- | ---------------------------------------------------------------------------- |
| New or changed API endpoint   | `docs/ARCHITECTURE.md` sequence diagrams, `README.md` endpoints table        |
| New ecosystem proxy module    | `docs/ARCHITECTURE.md` component diagram, `README.md` features & quick start |
| New configuration field       | Relevant `config.yaml` examples, `README.md` configuration section           |
| New detection rule type       | `docs/ARCHITECTURE.md`, `README.md` features list                            |
| New CLI flag or env var       | `README.md` usage / environment variables section                            |
| Architecture topology change  | `docs/ARCHITECTURE.md` all affected diagrams                                 |
| User-facing behavioral change | `README.md`                                                                  |
| New Kubernetes manifest       | `docs/ARCHITECTURE.md` deployment section, `README.md`                       |

---

## 3. Go Code Conventions

These conventions are aligned with the SonarQube and linting gates above. Deviating requires explicit justification in the PR description.

### 3.1 Logging

- Use `log/slog` structured logger exclusively.
- Never use bare `log`, `fmt.Println`, `fmt.Printf`, or `os.Stderr.Write` for runtime output.
- Use `slog.With(...)` to attach request-scoped fields (package, version, rule) to log entries.

### 3.2 HTTP Routing

- Use `http.ServeMux` (stdlib) only. No third-party routers (gorilla/mux, chi, echo, gin, etc.).

### 3.3 Metrics

- Use `sync/atomic` counters (`atomic.Int64`) only. No Prometheus client library, no expvar.
- The `/metrics` endpoint serialises these counters to JSON manually.

### 3.4 Configuration

- Use `gopkg.in/yaml.v3` only. No Viper, Koanf, or other config libraries.
- All struct fields must have explicit `yaml:"snake_case_name"` tags.

### 3.5 Error Handling

- Wrap errors with context: `fmt.Errorf("loading config: %w", err)`.
- Never silently discard errors. Use `_` only when the error is provably irrelevant.

### 3.6 Receiver Names

- Consistent per type: `s` for `*Server`, `e` for `*RuleEngine`, `c` for `*Cache`.
- Never change receiver names mid-file.

### 3.7 Architecture Patterns

- Standalone package-level funcs when no receiver state is needed.
- Struct-based walkers for HTML/JSON tree traversal.
- Builder functions (`buildServer`, `applyPortEnvOverride`, `createLogger`) live in `main.go` to keep `main()` thin.
- `PackageRule.Enabled *bool` — `nil` means enabled; explicit `false` disables without deletion.

---

## 4. Testing Conventions

### 4.1 File Organisation

| File                      | Purpose                                                    |
| ------------------------- | ---------------------------------------------------------- |
| `server_test.go`          | Integration tests with `httptest.NewServer` mock upstreams |
| `server_coverage_test.go` | Unit tests targeting specific code paths for coverage gaps |
| `rules_test.go` (common)  | Rule engine unit tests                                     |
| `config_test.go`          | Config loading, defaults, validation                       |

### 4.2 Test Patterns

- Use `t.TempDir()` for any files created during tests — never leave disk artifacts.
- Use `httptest.NewRequest` + `httptest.NewRecorder` for handler-level unit tests.
- Use `httptest.NewServer` for integration tests with a mock upstream.
- For TLS tests: generate self-signed certs with `crypto/ecdsa` + `crypto/x509`.
- To verify cache hit/miss: make two sequential requests and assert on the `X-Cache` response header.
- To simulate connection pool exhaustion: use `panic(http.ErrAbortHandler)` in mock handlers.
- Define all test string constants in a `const` block at the top of the test file — no repeated raw literals.
- Test both scoped (`@scope/pkg`) and unscoped npm packages in npm tests.
- Table-driven tests (`[]struct{ name, input, want }`) are preferred for functions with multiple input cases.

### 4.3 Test Naming

- Test functions: `TestFunctionNameScenario` (PascalCase, no underscores).
- Sub-tests within `t.Run(...)`: short lowercase description strings are acceptable inside `Run`.

### 4.4 Docker E2E Tests

- Docker-based end-to-end tests live in `e2e/docker/` and exercise real package-manager clients (npm, pip, Maven/Gradle) against proxy instances.
- The test runner is `e2e/docker/run.sh`. Run per-ecosystem with `--node-only`, `--python-only`, or `--java-only`.
- **All Docker E2E tests must pass before any commit that touches proxy logic, rule evaluation, configuration handling, or test infrastructure.**
- Real-life configs (`e2e/docker/configs/*-real-life.yaml`) combine multiple rules simultaneously (trusted packages, install scripts, age, pre-release, deny lists, version patterns). These configs must stay in sync with any rule engine changes.
- When adding a new rule type or changing rule evaluation order, add corresponding test cases to the real-life test sections in `e2e/docker/clients/*/test.sh`.

---

## 5. Security Requirements

- No hardcoded credentials, tokens, or secrets anywhere in source or config templates.
- Default config files must ship with empty auth fields: `token: ""`, `username: ""`, `password: ""`.
- All upstream URLs must be validated before use: scheme must be `https` in production defaults; `http` only permitted with explicit `insecure_skip_verify: true` and logged as a warning.
- External URLs proxied via `handleExternal` / equivalent must be validated against an explicit allowlist (`allowed_external_hosts`).
- Input from HTTP request paths, query parameters, and headers must be sanitised before logging (no raw user input in log fields without truncation/escaping).
- Docker images must run as non-root (UID 1001) and have no `SETUID`/`SETGID` binaries in the runtime image.
- Kubernetes manifests must set `automountServiceAccountToken: false` and define resource limits on every container.

---

## 6. Task Completion Checklist

Before marking **any** coding task complete, verify every item:

```
[ ] go vet ./...                                     — zero errors
[ ] golangci-lint run ./...                          — zero errors/warnings
[ ] go test -count=1 -race ./...                     — all tests pass, no race conditions
[ ] go test -coverprofile=coverage.out ./... &&
    go tool cover -func=coverage.out | grep total    — ≥ 90% statement coverage
[ ] SonarQube analysis                               — zero new issues
[ ] Docker E2E tests pass                            — run.sh per affected ecosystem
[ ] Relevant documentation updated                   — per §2 table above
[ ] docs/ARCHITECTURE.md updated                     — if topology/component changed
[ ] README.md updated                                — if user-facing behaviour changed
[ ] YAML config examples validated                   — against updated Go structs
[ ] Docker builds pass                               — docker build -f */Dockerfile .
[ ] k8s manifests valid                              — kubectl apply --dry-run=client
```

---

## 7. Open Source Readiness

This project is open sourced under the Apache 2.0 licence. Every contribution must:

- Not introduce dependencies with GPL, AGPL, LGPL, or proprietary licences.
- Include a SPDX licence header in every new Go source file:
  ```go
  // SPDX-License-Identifier: Apache-2.0
  ```
- Be free of third-party trademarks, proprietary registry names, or company-internal hostnames in default configs or documentation.
- Treat corp-registry as a generic placeholder name, not a specific vendor product.
