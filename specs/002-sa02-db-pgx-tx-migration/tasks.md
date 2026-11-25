# Tasks: SA-02 L0 Database Foundation — pgxpool, Context TxManager, Embedded goose Migrations & AdvisoryLock

**Input**: Design documents from `/specs/002-sa02-db-pgx-tx-migration/` and frozen PR Spec `docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md`

**Prerequisites**: `plan.md` (required), `spec.md` (required), `research.md`, `data-model.md`, `contracts/db-contracts.md`, `quickstart.md`

**Tests**: TDD is MANDATORY per PR Spec `§6` & `§8` — offline unit tests and integration tests drive implementation and verification under `-race` and `-cover` (≥ 80% on live PG17).

**Organization**: Tasks are grouped by user story (`US1`–`US4`) aligning 1:1 with PR Spec `§2` deliverables and `§6` TDD tasks.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (`[US1]`..`[US4]`)
- Exact file paths included in every task description

## Path Conventions

- Backend module root: `synapse-server/` (`module github.com/Karlsk/synapse-os`, Go `1.26.1`)
- Target L0 package: `synapse-server/internal/infra/db/`
- Test migration fixtures: `synapse-server/internal/infra/db/testdata/migrations/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify pinned DB dependencies and create test migration fixtures (maps to PR Spec `§6 T1`)

- [X] T001 Verify `synapse-server/go.mod` pins `github.com/jackc/pgx/v5`, `github.com/pressly/goose/v3`, and `github.com/prometheus/client_golang` with zero Redis/ClickHouse/GORM entries, and create test SQL migration fixtures `synapse-server/internal/infra/db/testdata/migrations/00001_create_items.sql` and `synapse-server/internal/infra/db/testdata/migrations/00002_add_items_note.sql`

---

## Phase 2: User Story 1 - Deterministic Database Error Mapping & Leak-Free Slow Query Tracing (Priority: P1) 🎯 MVP

**Goal**: Deliver offline-verified PostgreSQL error translation (`errors.go`) and zero-argument-leak slow-query `pgx.QueryTracer` (`tracer.go`) (maps to PR Spec `§2 #4,#5`, `§6 T2`, `§8 #1,#2`).

**Independent Test**: `cd synapse-server && go test -race -run 'Test(MapError|Is|SlowQueryTracer)' ./internal/infra/db/...` passes completely offline without PostgreSQL.

### Tests for User Story 1 (TDD RED First) ⚠️

- [X] T002 [P] [US1] Write TDD RED offline unit tests covering `MapError` (`nil`, `pgx.ErrNoRows` -> 404, `23505`/`23503` -> 409 `conflict` preserving `*pgconn.PgError`, `22P02`/`22001`/`23502`/`23514` -> 400 `invalid_argument`, `40001`/`40P01` -> 409 `busy`, `57014`/`context.Canceled`/`context.DeadlineExceeded` passthrough, unknown error -> masked 500 `internal_error`), `IsNoRows`, `IsUniqueViolation`, and `IsForeignKeyViolation` in `synapse-server/internal/infra/db/errors_test.go`
- [X] T003 [P] [US1] Write TDD RED offline unit tests covering `slowQueryTracer` (slow query >= threshold logs `WARN` with `request_id`/`company_id`/`duration_ms`/compacted SQL, zero bound parameter secret leakage, increments `synapse_db_slow_queries_total` and observes `synapse_db_query_duration_seconds`; query error logs `ERROR` while skipping `pgx.ErrNoRows` and `context.Canceled`) in `synapse-server/internal/infra/db/tracer_test.go`

### Implementation for User Story 1 (GREEN → REFACTOR)

- [X] T004 [P] [US1] Implement `MapError`, `IsNoRows`, `IsUniqueViolation`, and `IsForeignKeyViolation` matching PR Spec `§4.1` & `§4.3` verbatim in `synapse-server/internal/infra/db/errors.go`
- [X] T005 [P] [US1] Implement `slowQueryTracer` (`TraceQueryStart`/`TraceQueryEnd`), `compactSQL`, and Prometheus metrics (`synapse_db_slow_queries_total`, `synapse_db_query_duration_seconds`) matching PR Spec `§5.4` in `synapse-server/internal/infra/db/tracer.go`

---

## Phase 3: User Story 2 - Forward-Only Embedded Schema Migrations with Static Safety Guard & Multi-Instance Lock (Priority: P1)

**Goal**: Deliver `goose/v3` `NewProvider` migration runner with static `-- +goose Up` `DROP TABLE` guard and advisory-lock serialization (maps to PR Spec `§2 #3`, `§6 T3`, `§8 #3`).

**Independent Test**: Offline `ValidateMigrations` unit tests pass without PG; full `MigrateUp` (8-goroutine concurrency) -> `MigrateDownOne` -> `MigrateUp` -> `MigrateVersion` verified against PG17 in `T013`.

### Tests for User Story 2 (TDD RED First) ⚠️

- [X] T006 [P] [US2] Write TDD RED offline unit tests covering `ValidateMigrations` (rejecting `DROP TABLE` inside `-- +goose Up`, allowing `DROP TABLE` in `-- +goose Down`, allowing `DROP TABLE` inside `--` line comments and `/* ... */` block comments, and passing valid `testdata/migrations`) in `synapse-server/internal/infra/db/migrate_unit_test.go`

### Implementation for User Story 2 (GREEN → REFACTOR)

- [X] T007 [US2] Implement `MigrationResult`, `ValidateMigrations`, `extractGooseUpSQL`, `MigrateUp` (static guard + blocking PG advisory lock + `goose.NewProvider`), `MigrateDownOne`, and `MigrateVersion` matching PR Spec `§4.1` & `§5.3` in `synapse-server/internal/infra/db/migrate.go`

---

## Phase 4: User Story 3 - Tuned Connection Pool, Context-Propagating Transactions & Nested Savepoints (Priority: P1)

**Goal**: Deliver `pgxpool` setup with UTC timezone/statement timeout/pool Prometheus metrics (`pool.go`) and context-based `TxManager` with nested `SAVEPOINT` & panic-safe rollback (`tx.go`) (maps to PR Spec `§2 #1,#2`, `§6 T4`).

**Independent Test**: Verified against PostgreSQL 17 in `T013` (`SHOW TIME ZONE == UTC`, `Ping`, `synapse_db_pool_max_conns`, atomic commit/rollback, nested `SAVEPOINT`, panic rollback, `InTx`/`WithoutTx`).

### Implementation for User Story 3

- [X] T008 [P] [US3] Implement `Config`, `PoolStats`, `Option`, `WithLogger`, `WithRegisterer`, `WithSearchPath`, `DB`, `Open`, `Pool`, `Ping` (2s default timeout), `Stats`, `Locker`, `Close`, and pool Prometheus `GaugeFunc` metrics (`synapse_db_pool_{acquired,idle,total,max}_conns`) in `synapse-server/internal/infra/db/pool.go`
- [X] T009 [P] [US3] Implement `Querier`, `TxManager`, `DB.Querier`, `InTx`, `WithoutTx`, `WithinTx`, and `WithinReadOnlyTx` (nested `SAVEPOINT` via `parentTx.Begin` and cancel/panic-safe rollback) in `synapse-server/internal/infra/db/tx.go`

---

## Phase 5: User Story 4 - Zero-Redis Native Advisory Locks & Parallel Integration Test Harness (Priority: P2)

**Goal**: Deliver PG native `AdvisoryLocker` (dedicated physical connection session locks + transaction locks) and parallel-safe `NewTestSchemaDB` / `RunInRollbackTx` test harnesses with full PG17 integration test coverage (maps to PR Spec `§2 #6,#7,#8`, `§6 T4,T5`, `§8 #4-#9`).

**Independent Test**: Offline `LockKeyID` unit tests pass without PG; live PG17 integration suite passes all 5 integration test scenarios (`§8 #5-#9`) under `-race` and auto-skips cleanly when `TEST_POSTGRES_DSN` is unset.

### Tests for User Story 4 (TDD RED First) ⚠️

- [X] T010 [P] [US4] Write TDD RED offline unit tests covering `LockKeyID` determinism across repeated calls and distinct `int64` outputs across namespaces (`("cron","a")` vs `("migrate","a")`) in `synapse-server/internal/infra/db/advisory_lock_test.go`

### Implementation for User Story 4

- [X] T011 [P] [US4] Implement `AdvisoryLocker`, `LockLease`, `LockKeyID` (FNV-1a 64-bit), `NewAdvisoryLocker`, `TryWithLock`, `Acquire` (dedicated `*pgxpool.Conn`), idempotent `LockLease.Release` (same connection unlock + release), `TryLockTx`, and internal `withLockBlocking` in `synapse-server/internal/infra/db/advisory_lock.go`
- [X] T012 [P] [US4] Implement `NewTestSchemaDB` (`test_<16hex>` sanitized schema, `t.Skip` when `TEST_POSTGRES_DSN` unset, optional `MigrateUp`, `t.Cleanup` `DROP SCHEMA ... CASCADE`) and `RunInRollbackTx` in `synapse-server/internal/infra/db/harness.go`
- [X] T013 [US4] Write PostgreSQL 17 integration tests covering `§8 #5` (`Open`, `SHOW TIME ZONE == UTC`, `Ping`, `Stats`, `synapse_db_pool_max_conns`), `§8 #6` (`WithinTx`/`WithinReadOnlyTx`, nested `SAVEPOINT`, panic rollback, `InTx`/`WithoutTx`), `§8 #7` (8-goroutine concurrent `MigrateUp`, `MigrateVersion`, `MigrateDownOne`, re-`MigrateUp`), `§8 #8` (`AdvisoryLock` mutual exclusion, 10-goroutine concurrency <= 1 active, idempotent release, `TryLockTx`), and `§8 #9` (parallel `NewTestSchemaDB` isolation & schema drop verification, `RunInRollbackTx` isolation) in `synapse-server/internal/infra/db/db_integration_test.go`

---

## Phase 6: Polish, Documentation Sync & Mandatory Quality Gates

**Purpose**: Complete PR Spec `§7` documentation sync and `§6 T6` / `§9` acceptance gates

- [X] T014 [P] Update `synapse-server/README.md` with `internal/infra/db` L0 responsibilities, local PostgreSQL 17 `TEST_POSTGRES_DSN` test commands, and `AdvisoryLock` usage discipline (`spec_02 §7`)
- [X] T015 [P] Update `docs/SuperAgent-Go/DDD/README.md` implementation board row for `SA-02` (`spec_02 §7`)
- [X] T016 Run all hard quality gates: `gofmt -l .` (zero output), `go vet ./...`, `go build ./...`, offline `go test -race ./...` (verifying `SA-01` regression green + `internal/infra/db` offline tests green and integration tests auto-skip cleanly), verify `internal/infra/db` imports contain zero Redis or business-domain packages, and run live PostgreSQL 17 integration tests (`TEST_POSTGRES_DSN=... go test -race -cover ./internal/infra/db/...`) confirming ≥ 80% statement coverage

---

## Dependencies & Execution Order

- **Phase 1 (`T001`)**: Must complete first to prepare migration test fixtures.
- **Phases 2–5 (`US1`–`US4`)**:
  - Offline TDD tests (`T002`, `T003`, `T006`, `T010`) can be written in parallel first (RED).
  - Production files (`T004`, `T005`, `T007`, `T008`, `T009`, `T011`, `T012`) compile together in `package db` (GREEN).
  - Integration tests (`T013`) exercise all components end-to-end against real PostgreSQL 17 (`docker-compose.dev.yml`).
- **Phase 6 (`T014`–`T016`)**: Documentation sync and final offline + live PG17 verification gates.
