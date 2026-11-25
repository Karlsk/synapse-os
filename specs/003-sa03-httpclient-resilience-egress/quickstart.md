# Quickstart Validation Guide: SA-03 L0 EgressGuard & Resilient HTTP Client

## Prerequisites

- Go `1.26.1` installed
- Working directory: `synapse-server/`

## Validation Commands

```bash
cd synapse-server

# 1. Formatting & static analysis
gofmt -l .
go vet ./...

# 2. Build all packages
go build ./...

# 3. Race-checked unit tests & coverage across egress and httpclient (target >= 85%)
go test -race -v -count=1 -cover ./internal/core/egress/... ./internal/infra/httpclient/...

# 4. Full regression suite (SA-01 + SA-02 + SA-03)
go test -race ./...

# 5. Verify import boundaries
go list -f '{{join .Imports "\n"}}' ./internal/core/egress
go list -f '{{join .Imports "\n"}}' ./internal/infra/httpclient
```

## Expected Outcomes

- `gofmt -l .` produces zero output; `go vet ./...` and `go build ./...` exit 0.
- All 8 test points from `spec_03 §8` pass under `-race` completely offline.
- Statement coverage for both `internal/core/egress` and `internal/infra/httpclient` is **≥ 85%**.
- `internal/core/egress` imports only Go stdlib and `internal/core/apperror`.
- `internal/infra/httpclient` contains zero business fallback logic and zero Redis/DB/`os/exec` imports.
