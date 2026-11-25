# Implementation Plan: SA-02 L0 Database Foundation — pgxpool, Context TxManager, Embedded goose Migrations & AdvisoryLock

**Branch**: `002-sa02-db-pgx-tx-migration` | **Date**: 2026-11-25 | **Spec**: [spec.md](./spec.md) | **PR Spec**: [docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md](../../docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md)

**Input**: Feature specification from `specs/002-sa02-db-pgx-tx-migration/spec.md` and frozen PR contract `docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md`.

## Summary

Implement the L0 PostgreSQL 17 database infrastructure package `synapse-server/internal/infra/db` consisting of 7 production modules plus colocated offline unit tests and PostgreSQL 17 integration tests:
1. `pool.go`: Tuned `pgxpool` setup (`Open`, defaults, `TimeZone=UTC`, `application_name`, `statement_timeout`, optional `search_path`), 2s-default `Ping`, `Stats() PoolStats`, `Pool()`, `Locker()`, `Close()`, and Prometheus pool GaugeFuncs (`synapse_db_pool_*`).
2. `tx.go`: Narrow `Querier` & `TxManager` interfaces, context-propagating `Querier(ctx)`, `InTx(ctx)`, `WithoutTx(ctx)`, `WithinTx`, `WithinReadOnlyTx` with automatic nested `SAVEPOINT` via `pgx.Tx.Begin` and panic/cancel-safe rollback.
3. `migrate.go`: Non-global `goose/v3` `NewProvider` runner (`ValidateMigrations`, `MigrateUp`, `MigrateDownOne`, `MigrateVersion`) with static `-- +goose Up` `DROP TABLE` rejection (ignoring comments and `-- +goose Down`) and blocking PG `AdvisoryLock` serialization.
4. `errors.go`: Deterministic PostgreSQL SQLSTATE / `pgx.ErrNoRows` -> `SA-01` `apperror` translator (`MapError`, `IsNoRows`, `IsUniqueViolation`, `IsForeignKeyViolation`) with zero SQL/secret leakage.
5. `tracer.go`: `pgx.QueryTracer` slow-query and error logger (`compactSQL`, zero bound parameter logging, `slog` context metadata propagation, Prometheus `synapse_db_slow_queries_total` & `synapse_db_query_duration_seconds`).
6. `advisory_lock.go`: Zero-Redis PG native advisory locking (`LockKeyID` via FNV-1a 64-bit, `NewAdvisoryLocker`, `TryWithLock`, `Acquire`, idempotent `LockLease.Release` on the exact same physical connection, and `TryLockTx`).
7. `harness.go`: Parallel-safe `NewTestSchemaDB` (`test_<16hex>` sanitized schema + auto-cleanup + `t.Skip` when `TEST_POSTGRES_DSN` unset) and `RunInRollbackTx`.

## Technical Context

**Language/Version**: Go `1.26.1` (`synapse-server/`, `module github.com/Karlsk/synapse-os`, Go Modules, `CGO_ENABLED=0` static binary compatible)

**Primary Dependencies**:
- `github.com/jackc/pgx/v5` (`pgx`, `pgconn`, `pgxpool`, `stdlib`) — already pinned (`v5.13.0`)
- `github.com/pressly/goose/v3` (`goose.NewProvider`) — already pinned (`v3.30.1`)
- `github.com/prometheus/client_golang/prometheus` — already pinned (`v1.25.0`)
- `SA-01` L0 packages: `github.com/Karlsk/synapse-os/internal/core/apperror` (and `internal/infra/obs` in tests for context metadata/isolated Prometheus verification)
- Go standard library (`context`, `crypto/rand`, `encoding/hex`, `errors`, `fmt`, `hash/fnv`, `io/fs`, `log/slog`, `os`, `regexp`, `strconv`, `strings`, `sync/atomic`, `testing`, `time`)

**Storage**: PostgreSQL 17 (`pgxpool` + `goose_db_version` ledger + in-memory PG advisory locks; zero business tables or DDL in SA-02)

**Testing**:
- Offline unit tests (`errors_test.go`, `tracer_test.go`, `migrate_unit_test.go`, `advisory_lock_test.go`) running in < 1s with zero DB dependency
- PostgreSQL 17 integration tests (`db_integration_test.go` + `testdata/migrations/*.sql`) using `NewTestSchemaDB` / `RunInRollbackTx` (`t.Parallel()` safe, auto-skipping when `TEST_POSTGRES_DSN` unset, verified live against `postgres:17-alpine`)

**Target Platform**: Linux (`linux/amd64`, `linux/arm64`) & macOS (`darwin/arm64`)

**Project Type**: Backend Go L0 database infrastructure package (`synapse-server/internal/infra/db`)

**Performance Goals**: `pgxpool` defaults (`MaxConns=16`, `MinConns=2`, `ConnectTimeout=5s`, `StatementTimeout=30s`, `SlowQueryThreshold=200ms`, `Ping=2s`); non-blocking `TryWithLock` releases connection immediately on contention; parallel temporary-schema test isolation

**Constraints**: Zero Redis / ClickHouse / GORM; `internal/infra/db` MUST NOT import any business-domain package or `internal/core/tenant`; never log bound SQL parameter values; never allow `DROP TABLE` in `-- +goose Up`; session advisory unlock MUST run on the exact same acquired physical connection; `go test -race -cover` ≥ 80% against PG17

**Scale/Scope**: 7 Go source files + 5 Go test files + 2 test migration fixtures (~900–1,200 LOC total)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Verify compliance with `.specify/memory/constitution.md` (v1.0.1) and `AGENTS.md`:

- [x] **V1 Scope & Zero Redundant Infra**: Single PostgreSQL 17 engine + PG native `AdvisoryLock`; zero Redis, zero ClickHouse, zero ORM/DI containers.
- [x] **L0–L6 Layering & Explicit Wiring**: `internal/infra/db` resides strictly in L0 (`layer = 0`), importing only stdlib, whitelisted L0 third-party drivers (`pgx/v5`, `goose/v3`, `prometheus`), and `internal/core/apperror`. Zero upward imports or business-package dependencies.
- [x] **Multi-Tenant Fail-Closed & Crypto**: Stateless L0 DB layer (no tenant bypass); `slowQueryTracer` NEVER logs bound SQL parameters (protecting secrets/tokens/ciphertexts) and propagates `obs` context metadata via `slog.*Context`.
- [x] **Infrastructure Isolation**: `internal/infra/db` is explicitly one of the 3 packages authorized by Constitution Principle IV to import `pgx` and `goose`; exposes `Querier` and `TxManager` abstractions so upper layers never import `pgx`. Zero `os/exec` or raw `http.Client`.
- [x] **PG17 Schema & Migrations**: Forward-only `goose/v3` via non-global `NewProvider`; static `ValidateMigrations` rejects `DROP TABLE` in `-- +goose Up` prior to execution; every connection enforces `TimeZone=UTC`; zero business DDL in SA-02.
- [x] **Resilience & Concurrency**: Explicit timeouts (`ConnectTimeout=5s`, `StatementTimeout=30s`, `Ping=2s`, rollback fallback `3s`, lock release fallback `3s`); atomic CAS on `LockLease.Release`; dedicated connection acquired/released for session-level advisory locks; panic-safe deferred transaction rollback.
- [x] **API, MCP & Go Quality Gates**: `MapError` maps PG errors to frozen `SA-01` `apperror` codes; `NewTestSchemaDB` auto-skips when `TEST_POSTGRES_DSN` unset; verified via `gofmt`, `go vet`, `go build`, `go test -race -cover` (≥ 80% on PG17) + `SA-01` regression green.

*Post-Phase-1 Design Re-check*: PASS — All 7 gates satisfied with zero exceptions.

## Project Structure

### Documentation (this feature)

```text
specs/002-sa02-db-pgx-tx-migration/
├── plan.md              # This file
├── research.md          # Phase 0 design decisions
├── data-model.md        # Phase 1 structs, error table, lock & schema models
├── quickstart.md        # Phase 1 offline & PG17 validation guide
├── contracts/
│   └── db-contracts.md  # Phase 1 frozen Go signatures mirror
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 TDD task breakdown
```

### Source Code (repository root)

```text
synapse-server/
├── README.md                                        # Updated with PG17 TEST_POSTGRES_DSN & AdvisoryLock guide (Spec §7)
└── internal/infra/db/                               # [L0 infra] PG17 database foundation
    ├── pool.go                                      # Config, Option, PoolStats, DB, Open, Pool, Ping, Stats, Locker, Close, pool metrics
    ├── tx.go                                        # Querier, TxManager, DB.Querier, WithinTx, WithinReadOnlyTx, InTx, WithoutTx
    ├── migrate.go                                   # MigrationResult, ValidateMigrations, MigrateUp, MigrateDownOne, MigrateVersion
    ├── errors.go                                    # MapError, IsNoRows, IsUniqueViolation, IsForeignKeyViolation
    ├── tracer.go                                    # slowQueryTracer (pgx.QueryTracer), compactSQL, Prometheus query metrics
    ├── advisory_lock.go                             # AdvisoryLocker, LockLease, LockKeyID, TryWithLock, Acquire, Release, TryLockTx, withLockBlocking
    ├── harness.go                                   # NewTestSchemaDB, RunInRollbackTx
    ├── errors_test.go                               # §8 Test #1 (offline)
    ├── tracer_test.go                               # §8 Test #2 (offline)
    ├── migrate_unit_test.go                         # §8 Test #3 (offline)
    ├── advisory_lock_test.go                        # §8 Test #4 (offline)
    ├── db_integration_test.go                       # §8 Tests #5-#9 (PG17, auto-skip if TEST_POSTGRES_DSN unset)
    └── testdata/
        └── migrations/
            ├── 00001_create_items.sql               # Test fixture migration 1
            └── 00002_add_items_note.sql             # Test fixture migration 2
```

**Structure Decision**: Direct 1:1 mapping to `docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md` §2 deliverables table under `synapse-server/internal/infra/db/`.

## Complexity Tracking

> No Constitution violations; table intentionally empty.
