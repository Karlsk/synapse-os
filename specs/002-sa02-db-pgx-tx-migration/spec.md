# Feature Specification: SA-02 L0 Database Foundation — Connection Pool, Context Transactions, Embedded Migrations & Advisory Locks

**Feature Branch**: `002-sa02-db-pgx-tx-migration`

**Created**: 2026-11-25

**Status**: Draft

**Input**: PR Spec `docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md` (PR `SA-02`) — L0 PostgreSQL 17 数据库底座（连接池、Context 事务透传、嵌入 SQL 迁移、错误映射、慢查询追踪、原生 AdvisoryLock 与单测隔离夹具）。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Deterministic Database Error Mapping & Leak-Free Slow Query Tracing (Priority: P1)

Downstream storage implementations and operators need every database error to map deterministically to the unified domain error taxonomy without leaking raw SQL statements or secrets, and need slow or failed queries automatically traced with request/tenant correlation while guaranteeing bound parameter values are never logged.

**Why this priority**: All 38 downstream PostgreSQL stores (`SA-07`–`SA-10`) rely on consistent error translation and safe observability before any persistence logic can be written safely.

**Independent Test**: Can be fully tested offline without a database server by feeding representative SQLSTATE errors, no-rows errors, context cancellations, and unknown errors into the mapper, and simulating slow/failed queries through the query tracer against an in-memory log buffer and isolated Prometheus registry.

**Acceptance Scenarios**:

1. **Given** a database error representing no rows, unique violation (`23505`), foreign key violation (`23503`), invalid input/constraint (`22P02`, `22001`, `23502`, `23514`), serialization/deadlock (`40001`, `40P01`), cancellation/timeout (`57014`, `context.Canceled`, `context.DeadlineExceeded`), or an unknown internal failure, **When** mapped through the error mapper, **Then** each maps deterministically to the expected domain error code and HTTP status (`404`, `409 conflict`, `400`, `409 busy`, passthrough, or masked `500 internal_error`), retaining underlying error details only via unwrap for server diagnostics.
2. **Given** a query executing slower than the configured slow-query threshold with request and company context metadata attached and sensitive bound parameters passed as query arguments, **When** query tracing completes, **Then** a warning log is emitted containing `request_id`, `company_id`, `duration_ms`, and compacted SQL, zero bound parameter values appear in the log output, and the slow-query Prometheus counter increments.
3. **Given** a query that fails with a real error (excluding no-rows and context cancellation), **When** query tracing completes, **Then** an error log is emitted with duration and compacted SQL while never logging bound parameters.

---

### User Story 2 - Forward-Only Embedded Schema Migrations with Static Safety Guard & Multi-Instance Lock (Priority: P1)

Enterprise operators deploying single-binary upgrades and multi-instance startups need embedded versioned schema migrations that statically reject destructive `DROP TABLE` statements in forward migrations prior to execution and serialize concurrent startup migrations safely without introducing external coordination middleware.

**Why this priority**: Preventing accidental data loss from destructive migrations and eliminating race conditions during concurrent cold starts are non-negotiable production safety requirements.

**Independent Test**: Static safety validation (`DROP TABLE` rejection in Up sections, allowance in Down sections and comments) can be tested offline; full `Up` → `DownOne` → `Up` idempotency and multi-goroutine concurrent `MigrateUp` serialization can be verified against PostgreSQL 17.

**Acceptance Scenarios**:

1. **Given** an embedded migration filesystem where an `-- +goose Up` section contains `DROP TABLE`, **When** migration validation or execution runs, **Then** it fails fast with a clear error before touching the database.
2. **Given** a migration file where `DROP TABLE` appears only inside `-- +goose Down` or inside SQL comments, **When** migration validation runs, **Then** validation succeeds.
3. **Given** multiple concurrent instances calling `MigrateUp` simultaneously against a fresh PostgreSQL 17 schema, **When** migrations execute, **Then** an advisory lock serializes execution so all callers succeed without conflict and each migration version applies exactly once, with `MigrateVersion` and `MigrateDownOne` operating accurately.

---

### User Story 3 - Tuned Connection Pool, Context-Propagating Transactions & Nested Savepoints (Priority: P1)

Storage and domain services need a tuned PostgreSQL 17 connection pool enforcing UTC session timezone, statement timeouts, health-check pings, and pool telemetry, combined with transparent context-propagating transactions where nested transaction blocks use database savepoints and recover cleanly from errors or panics.

**Why this priority**: Business services must compose atomic multi-store operations without coupling to database driver transaction handles or risking leaked connections on panics or canceled contexts.

**Independent Test**: Verified against PostgreSQL 17 by asserting `SHOW TIME ZONE` equals `UTC`, `Ping` latency/health succeeds, pool Prometheus metrics are registered, multi-operation commits and rollbacks are atomic, nested transactions roll back independently via savepoints, and panics inside callbacks roll back without leaking pool connections.

**Acceptance Scenarios**:

1. **Given** a valid PostgreSQL 17 connection configuration, **When** the database handle is opened, **Then** every connection enforces `TimeZone=UTC`, `application_name`, and `statement_timeout`, initial health ping succeeds, and pool metrics (`acquired`, `idle`, `total`, `max_conns`) are exported when a Prometheus registerer is supplied.
2. **Given** a caller executing multiple repository queries inside a transaction block via the context-aware querier, **When** the outer callback succeeds, **Then** all changes commit atomically; when the outer callback returns an error or panics, all changes roll back cleanly even if the request context was canceled.
3. **Given** an active outer transaction containing a nested transaction block, **When** the inner block returns an error and the outer block handles it and returns nil, **Then** only the inner block's changes are rolled back (via savepoint) while the outer transaction commits intact.
4. **Given** an active transaction context, **When** a caller checks transaction state or explicitly strips the transaction handle, **Then** transaction detection accurately reflects active state and stripped contexts execute directly against the pool.

---

### User Story 4 - Zero-Redis Native Advisory Locks & Parallel Integration Test Harness (Priority: P2)

Scheduled background jobs (`AutomationScheduler`, `TaskScheduler`, `InboxTicker`, `SessionPruner`) need mutual exclusion across instances using PostgreSQL native advisory locks without introducing Redis, and downstream store test suites (`SA-07`–`SA-10`) need isolated temporary-schema and rollback-transaction test harnesses that run safely under `t.Parallel()` and auto-skip when offline.

**Why this priority**: Eliminates Redis from the V1 commercial footprint while giving all 38 upcoming PostgreSQL stores fast, deterministic, race-free integration test isolation.

**Independent Test**: Lock key hashing determinism and namespace isolation are tested offline; session-level and transaction-level mutual exclusion, release across concurrent workers, temporary-schema creation/cleanup, and rollback-tx isolation are verified against PostgreSQL 17 (auto-skipping cleanly when `TEST_POSTGRES_DSN` is unset).

**Acceptance Scenarios**:

1. **Given** a namespace and key pair, **When** the 64-bit advisory lock ID is computed repeatedly or across distinct namespaces, **Then** identical pairs always produce the exact same `int64` ID while different namespaces with the same key produce distinct IDs.
2. **Given** concurrent workers competing for the same session-level advisory lock, **When** worker A holds the lock on its dedicated connection, **Then** worker B's non-blocking `TryWithLock` immediately returns `(false, nil)` without leaking connections, and once worker A releases/exits, worker B succeeds with `(true, nil)`; across 10 concurrent workers, at most 1 executes the critical section at any instant.
3. **Given** two parallel tests calling the temporary-schema test harness against PostgreSQL 17, **When** both create and write identically named tables, **Then** each operates in its own isolated `test_<16hex>` schema that is automatically dropped on cleanup; when `TEST_POSTGRES_DSN` is unset, tests skip cleanly without failing offline runs.
4. **Given** a test executing writes inside the rollback-transaction harness, **When** the callback completes, **Then** all writes are rolled back automatically and invisible outside the test.

---

### Edge Cases

- What happens when `MapError` receives `nil` or an error wrapping `pgx.ErrNoRows` / `*pgconn.PgError` / `context.Canceled`? `MapError(op, nil)` returns `nil`; wrapped errors are unwrapped via `errors.Is`/`errors.As`.
- What happens when `Ping(ctx)` is called with a context that has no deadline vs one with an existing shorter deadline? Without a deadline, a 2-second timeout is applied automatically; an existing deadline is respected.
- What happens when `LockLease.Release(ctx)` is called multiple times or on a nil lease? Atomic CAS ensures idempotent single unlock and single physical connection release without double-release panics.
- What happens when `ValidateMigrations` encounters an empty directory or non-`.sql` files or multiline `-- +goose Up` / `-- +goose Down` markers with comments? Non-`.sql` files and directories are skipped; SQL line comments (`-- ...`) and block comments (`/* ... */`) are stripped before checking for `DROP TABLE`.
- What happens when `WithinTx` callback panics? The deferred rollback executes cleanly before the panic propagates so the transaction connection is never left dirty in the pool.
- What happens when `Open` fails the initial `Ping`? The newly created connection pool is closed before returning the error to avoid resource leaks.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a tuned PostgreSQL 17 pool constructor (`Open`) applying safe defaults (`MaxConns=16`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`, `HealthCheckPeriod=30s`, `ConnectTimeout=5s`, `StatementTimeout=30s`, `SlowQueryThreshold=200ms`, `ApplicationName="synapse-server"`), enforcing `TimeZone=UTC`, `application_name`, `statement_timeout`, and optional `search_path`, supporting `WithLogger`/`WithRegisterer`/`WithSearchPath`, performing an initial `Ping`, exposing `Pool()`, `Ping(ctx)` (2s default timeout), `Stats() PoolStats`, `Locker()`, `Close()`, and exporting pool metrics (`synapse_db_pool_*`) when a registerer is provided.
- **FR-002**: System MUST provide narrow `Querier` and `TxManager` interfaces, context-based `Querier(ctx)`, `InTx(ctx)`, `WithoutTx(ctx)`, `WithinTx(ctx, fn)`, and `WithinReadOnlyTx(ctx, fn)` supporting automatic nested `SAVEPOINT` via `pgx.Tx.Begin`, panic-safe rollback, and detached short-timeout rollback when the caller context is already canceled.
- **FR-003**: System MUST provide `ValidateMigrations`, `MigrateUp`, `MigrateDownOne`, and `MigrateVersion` built on non-global `goose/v3` `NewProvider`, rejecting `DROP TABLE` in `-- +goose Up` sections (while allowing comments and `-- +goose Down`), and serializing concurrent `MigrateUp` calls via a PostgreSQL advisory lock.
- **FR-004**: System MUST provide deterministic `MapError(op, err)`, `IsNoRows(err)`, `IsUniqueViolation(err)`, and `IsForeignKeyViolation(err)` mapping PostgreSQL errors to `SA-01` `apperror` codes (`404`, `409 conflict`, `400`, `409 busy`, cancellation passthrough, masked `500`) without exposing raw SQL or secrets in external error messages.
- **FR-005**: System MUST provide a `pgx.QueryTracer` recording slow queries (>= threshold) and query errors (excluding `ErrNoRows` and `context.Canceled`) with compacted SQL, context metadata (`request_id`/`company_id`/`user_id`), and Prometheus metrics, while strictly forbidding logging bound query parameter values.
- **FR-006**: System MUST provide zero-Redis PostgreSQL advisory locking (`LockKeyID` via deterministic FNV-1a 64-bit hash, `NewAdvisoryLocker`, `TryWithLock`, `Acquire`, idempotent `LockLease.Release`, and transaction-scoped `TryLockTx`), always acquiring a dedicated physical pool connection for session-level locks and unlocking on the exact same connection before releasing it.
- **FR-007**: System MUST provide isolated testing harnesses `NewTestSchemaDB(t, migrationsFS, dir)` (creating `test_<16hex>` sanitized schemas, running optional embedded migrations, dropping schemas on `t.Cleanup`, and auto-skipping when `TEST_POSTGRES_DSN` is unset) and `RunInRollbackTx(t, d, fn)`.

### Explicit Boundaries (Out of Scope)

- No business table DDL or `internal/storage/postgres` Store implementations (deferred to `SA-07`–`SA-10`).
- No Redis clients or external distributed lock dependencies.
- No dependency on `internal/core/tenant` (deferred to `SA-04`) or HTTP routing/Actuator endpoints (deferred to `SA-41`).

### Key Entities

- **`Config` & `PoolStats`**: Connection pool tuning configuration and runtime pool statistics snapshot.
- **`DB`, `Querier`, `TxManager`**: Unified database handle, query execution abstraction over pool/tx, and context-propagating transaction coordinator.
- **`MigrationResult`**: Record of an applied or reverted `goose/v3` migration (version, source path, duration).
- **`AdvisoryLocker` & `LockLease`**: Session-level and transaction-level PostgreSQL advisory lock manager and connection-bound lease.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of offline unit tests (error mapping, slow-query tracer redaction & metrics, migration `DROP TABLE` guard, advisory lock key hashing) pass cleanly under `-race` with zero external dependencies.
- **SC-002**: 100% of PostgreSQL 17 integration tests (UTC timezone, pool ping & metrics, tx commit/rollback/nested savepoint/panic safety, 8-goroutine concurrent `MigrateUp` + `MigrateDownOne` + `MigrateUp`, 10-goroutine advisory lock mutual exclusion, parallel temporary-schema & rollback-tx harnesses) pass cleanly under `-race` when `TEST_POSTGRES_DSN` is configured, and skip cleanly when unset.
- **SC-003**: `internal/infra/db` achieves ≥ 80% statement coverage against PostgreSQL 17 (per PR Spec `§9`), passes `gofmt`, `go vet`, `go build`, and `go test -race ./...` (including zero regressions in `SA-01`), and imports zero Redis or business-domain packages.

## Assumptions

- `SA-01` (`internal/core/apperror`, `internal/infra/obs`, `internal/infra/config`) is already merged on `main` and available.
- `github.com/jackc/pgx/v5` (`v5.13.0`), `github.com/pressly/goose/v3` (`v3.30.1`), and `github.com/prometheus/client_golang` (`v1.25.0`) are already pinned in `synapse-server/go.mod`.
- PostgreSQL 17 integration tests require `TEST_POSTGRES_DSN` pointing to a reachable PostgreSQL 17 instance (e.g., via `docker-compose.dev.yml` or local PG17); when unset, `NewTestSchemaDB` calls `t.Skip` per Constitution Principle VII.
