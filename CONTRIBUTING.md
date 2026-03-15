# Contributing

Thank you for your interest in contributing to the Bulwark project.

## Prerequisites

- Go 1.26 or later
- `golangci-lint` v1.64+ (install via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.64.8`)
- Docker (for E2E tests)

## Project Structure

Bulwark is a multi-module Go project. Each ecosystem proxy is a standalone module with its own `go.mod`:

```
common/          # Shared library: config, rules engine, cache, installer
npm-bulwark/     # npm registry proxy (port 18001)
pypi-bulwark/    # PyPI registry proxy (port 18000)
maven-bulwark/   # Maven repository proxy (port 18002)
e2e/             # Live E2E tests (Go-based, hit real registries)
e2e/docker/      # Docker-based E2E tests (real package manager clients)
```

## Development Workflow

1. Fork the repository and create a feature branch.
2. Make your changes, following the coding conventions below and the quality gates in this document.
3. Run the full quality gate **for each affected module** before opening a pull request:

```bash
# Run for each module you changed (common, npm-bulwark, pypi-bulwark, maven-bulwark):
cd <module>
go vet ./...
golangci-lint run ./...
go test -count=1 -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep "^total:"
```

Or run all modules at once:

```bash
for mod in common npm-bulwark pypi-bulwark maven-bulwark; do
  echo "=== $mod ===" && cd $mod && \
  go vet ./... && \
  golangci-lint run ./... && \
  go test -count=1 -race -coverprofile=coverage.out ./... && \
  go tool cover -func=coverage.out | grep "^total:" && \
  cd ..
done
```

4. Ensure total statement coverage remains at or above **90%** per module.
5. If your change touches proxy logic, rule evaluation, or configuration, also run the Docker E2E tests:

```bash
cd e2e/docker && ./run.sh          # All ecosystems
./run.sh --node-only               # npm only
./run.sh --python-only             # PyPI only
./run.sh --java-only               # Maven only
```

6. Update relevant documentation (README, ARCHITECTURE, CHANGELOG) if your change affects user-facing behaviour, configuration, or system topology.

## Coding Conventions

- Use `log/slog` for all logging; no `fmt.Println` or bare `log` package calls.
- Use `http.ServeMux` only; no third-party routers.
- Use `sync/atomic` counters for metrics; no Prometheus client.
- Use `gopkg.in/yaml.v3` for config; no Viper or Koanf.
- All SPDX licence headers (`// SPDX-License-Identifier: Apache-2.0`) must be present in every new Go source file.
- Test functions must use PascalCase with no underscores: `TestLoadConfigValid`, not `TestLoadConfig_Valid`.
- Wrap errors with context: `fmt.Errorf("loading config: %w", err)`.
- Blocking responses must include a `[Bulwark]` prefix with the package name and reason.

## Licence

By contributing, you agree that your contributions will be licensed under the Apache 2.0 licence.
