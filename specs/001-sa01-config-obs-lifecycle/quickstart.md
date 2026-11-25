# Quickstart Validation Guide: SA-01 L0 Core Foundation

## Prerequisites

- Go `1.26.1` installed (`go version`)
- Working directory: `synapse-server/`

## Validation Commands

```bash
cd synapse-server

# 1. Formatting & static analysis
gofmt -l .
go vet ./...

# 2. Build all packages
go build ./...

# 3. Race-checked unit tests with coverage across all 4 SA-01 L0 packages
go test -race -v -cover ./internal/core/apperror/... ./internal/infra/config/... ./internal/infra/obs/... ./internal/infra/lifecycle/...

# 4. Verify apperror has strictly Go stdlib imports only
go list -f '{{join .Imports "\n"}}' ./internal/core/apperror

# 5. Full repository test suite
go test -race ./...
```

## Expected Outcomes

- `gofmt -l .` outputs nothing (exit code 0).
- `go vet ./...` and `go build ./...` exit 0 with zero warnings.
- All 9 test scenarios from `spec_01` §8 pass under `-race`, including loading and validating `configs/config.example.yaml`.
- Line coverage for each of the 4 packages (`apperror`, `config`, `obs`, `lifecycle`) is **≥ 85%**.
- `go list` for `internal/core/apperror` lists only Go standard library packages (`errors`, `fmt`, `net/http`, `strings`).
- `configs/config.example.yaml` contains zero `redis` references.
