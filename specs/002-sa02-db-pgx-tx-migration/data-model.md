# Data Model: SA-02 L0 Database Foundation

> SA-02 introduces **zero business tables**. Only runtime Go structures, PostgreSQL error mappings, advisory lock slots, and ephemeral test schemas are defined.

## 1. Pool & Configuration Types (`pool.go`)

| Type | Fields | Defaults / Invariants |
|---|---|---|
| `Config` | `DSN string`, `MaxConns int32`, `MinConns int32`, `MaxConnLifetime time.Duration`, `MaxConnIdleTime time.Duration`, `HealthCheckPeriod time.Duration`, `ConnectTimeout time.Duration`, `StatementTimeout time.Duration`, `SlowQueryThreshold time.Duration`, `ApplicationName string` | `MaxConns=16`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`, `HealthCheckPeriod=30s`, `ConnectTimeout=5s`, `StatementTimeout=30s`, `SlowQueryThreshold=200ms`, `ApplicationName="synapse-server"` |
| `PoolStats` | `AcquireCount int64`, `AcquireDuration time.Duration`, `AcquiredConns int32`, `CanceledAcquireCount int64`, `ConstructingConns int32`, `EmptyAcquireCount int64`, `IdleConns int32`, `MaxConns int32`, `TotalConns int32` | Direct snapshot from `pgxpool.Stat` |
| `DB` | Unexported `pool *pgxpool.Pool`, `logger *slog.Logger`, `locker *AdvisoryLocker` | Thread-safe handle; `Ping(ctx)` defaults to 2s timeout when `ctx` has no deadline |

## 2. Transaction & Migration Types (`tx.go`, `migrate.go`)

| Type | Fields / Methods | Invariants |
|---|---|---|
| `Querier` | `Exec`, `Query`, `QueryRow`, `SendBatch` | Satisfied by both `*pgxpool.Pool` and `pgx.Tx` |
| `TxManager` | `WithinTx(ctx, fn)`, `WithinReadOnlyTx(ctx, fn)` | Satisfied by `*DB`; nested calls issue PostgreSQL `SAVEPOINT` |
| `MigrationResult` | `Version int64`, `Source string`, `Duration time.Duration` | Returned by `MigrateUp` (slice) and `MigrateDownOne` (pointer) |

## 3. PostgreSQL Error Mapping Table (`errors.go`)

| Source / SQLSTATE | Mapped Result | HTTP Status | Preserves Cause? |
|---|---|---:|---|
| `nil` | `nil` | — | — |
| `pgx.ErrNoRows` | `apperror.NotFound(op + ": not found")` | `404` | — |
| `23505` (`unique_violation`) | `apperror.Wrap(CodeConflict, 409, op+": already exists", err)` | `409` | Yes (`*pgconn.PgError`) |
| `23503` (`foreign_key_violation`) | `apperror.Wrap(CodeConflict, 409, op+": reference conflict", err)` | `409` | Yes (`*pgconn.PgError`) |
| `22P02`, `22001`, `23502`, `23514` | `apperror.Wrap(CodeInvalidArgument, 400, op+": invalid input", err)` | `400` | Yes (`*pgconn.PgError`) |
| `40001`, `40P01` | `apperror.Wrap(CodeBusy, 409, op+": concurrent conflict, retry", err)` | `409` | Yes (`*pgconn.PgError`) |
| `57014`, `context.Canceled`, `context.DeadlineExceeded` | Raw `err` passthrough | Caller | Yes |
| Any other error | `apperror.Internal(op+": database error", err)` | `500` | Yes (masked message) |

## 4. Advisory Lock & Test Harness Types (`advisory_lock.go`, `harness.go`)

| Type | Structure | Invariants |
|---|---|---|
| `AdvisoryLocker` | Unexported `pool *pgxpool.Pool` | Never runs session lock on pool directly; always acquires dedicated `*pgxpool.Conn` |
| `LockLease` | Unexported `conn *pgxpool.Conn`, `keyID int64`, `released atomic.Bool` | `Release(ctx)` idempotent via CAS; unlocks on same `conn` before `conn.Release()` |
| Ephemeral Test Schema | `test_<16hex>` | Strict `^test_[a-f0-9]{16}$`, quoted via `pgx.Identifier{schema}.Sanitize()`, dropped via `DROP SCHEMA IF EXISTS ... CASCADE` in `t.Cleanup` |
