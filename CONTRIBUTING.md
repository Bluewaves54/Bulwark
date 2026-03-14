# Contributing

Thank you for your interest in contributing to the PKGuard project.

## Prerequisites

- Go 1.26 or later
- `golangci-lint` (install via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`)

## Development Workflow

1. Fork the repository and create a feature branch.
2. Make your changes, following the coding conventions below and the quality gates in this document.
3. Run the full quality gate before opening a pull request:

```bash
go vet ./...
golangci-lint run ./...
go test -count=1 -race -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep "^total:"
```

4. Ensure total statement coverage remains at or above 90%.
5. Update relevant documentation (README, ARCHITECTURE, CHANGELOG) if your change affects user-facing behaviour, configuration, or system topology.

## Coding Conventions

- Use `log/slog` for all logging; no `fmt.Println` or bare `log` package calls.
- Use `http.ServeMux` only; no third-party routers.
- Use `sync/atomic` counters for metrics; no Prometheus client.
- Use `gopkg.in/yaml.v3` for config; no Viper or Koanf.
- All SPDX licence headers (`// SPDX-License-Identifier: Apache-2.0`) must be present in every new Go source file.
- Test functions must use PascalCase with no underscores: `TestLoadConfigValid`, not `TestLoadConfig_Valid`.

## Licence

By contributing, you agree that your contributions will be licensed under the Apache 2.0 licence.
