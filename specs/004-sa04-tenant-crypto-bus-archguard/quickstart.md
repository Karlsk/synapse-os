# Quickstart Validation Guide: SA-04 L0 Core Security, Event Bus, MemCache & Arch Guard

## Validation Commands

```bash
cd synapse-server

# 1. Formatting & static analysis
gofmt -l .
go vet ./...

# 2. Build all packages
go build ./...

# 3. Race-checked tests & coverage across all SA-04 packages + internal/arch_test.go (target >= 85%)
go test -race -v -count=1 ./internal
go test -race -count=1 -cover ./internal/core/tenant/... ./internal/core/crypto/... ./internal/core/eventbus/... ./internal/infra/memcache/...

# 4. Full L0 regression suite (SA-01 + SA-02 + SA-03 + SA-04 + arch_test)
go test -race ./...
```

## Expected Outcomes

- `gofmt -l .` produces zero output; `go vet ./...` and `go build ./...` exit 0.
- `internal/arch_test.go` passes both positive full-repo scanning and negative violation detection for all 6 rules.
- All 4 new packages (`tenant`, `crypto`, `eventbus`, `memcache`) pass `-race` with **≥ 85%** statement coverage.
- Full L0 suite (11 packages + `internal/arch_test.go`) passes 100% with zero Redis dependencies.
