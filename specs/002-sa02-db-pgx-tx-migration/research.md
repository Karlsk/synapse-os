# Phase 0 Research & Design Decisions: SA-02 L0 Database Foundation

## 1. `pgxpool` Setup, Session Defaults & Pool Telemetry (`pool.go`)

- **Decision**: Parse `cfg.DSN` with `pgxpool.ParseConfig`, apply all defaults (`MaxConns=16`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`, `HealthCheckPeriod=30s`, `ConnectTimeout=5s`, `StatementTimeout=30s`, `SlowQueryThreshold=200ms`, `ApplicationName="synapse-server"`), inject `RuntimeParams["TimeZone"] = "UTC"`, `RuntimeParams["application_name"]`, `RuntimeParams["statement_timeout"]` (ms), and optional `RuntimeParams["search_path"] = schema + ",public"`. Attach `slowQueryTracer` to `pcfg.ConnConfig.Tracer`, run initial `d.Ping(ctx)` (closing pool on failure), and register 4 `prometheus.GaugeFunc` metrics (`synapse_db_pool_acquired_conns`, `synapse_db_pool_idle_conns`, `synapse_db_pool_total_conns`, `synapse_db_pool_max_conns`) when `WithRegisterer` is provided.
- **Rationale**: Matches `spec_02 §4.4/§5.1/§8 #5` verbatim. Using `GaugeFunc` over `pool.Stat()` incurs zero background goroutines and reports exact real-time pool counts at scrape time.
- **Alternatives considered**: Background polling loop to update gauges — rejected because it adds an unnecessary goroutine and stale readings.

## 2. Context-Propagating `TxManager` & Nested `SAVEPOINT` (`tx.go`)

- **Decision**: Store active `pgx.Tx` in `context.Context` under unexported `txCtxKey{}`. When `WithinTx` or `WithinReadOnlyTx` detects a parent `pgx.Tx` in `ctx`, call `parentTx.Begin(ctx)` which automatically issues a PostgreSQL `SAVEPOINT` in `pgx/v5`. Use a deferred rollback guard (`if !committed`) that switches to a 3-second `context.Background()` timeout when `ctx.Err() != nil` so rollbacks always reach PG even after client cancellation or panic.
- **Rationale**: Allows stores to stay transaction-agnostic via `d.Querier(ctx)` while guaranteeing nested error isolation and zero leaked/dirty connections on panic.
- **Alternatives considered**: Passing `pgx.Tx` explicitly to every store method — rejected per Constitution Principle IV & `02_PostgreSQL存储层与Store接口设计.md` §1.

## 3. Non-Global `goose/v3` Provider & Static `DROP TABLE` Guard (`migrate.go`)

- **Decision**: Use `goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)` over `stdlib.OpenDBFromPool(pool)` (avoiding `goose` global mutable state). Before executing `MigrateUp`, run `ValidateMigrations(migrationsFS, dir)` which extracts only the `-- +goose Up` to `-- +goose Down` slice of every `.sql` file, strips `-- ...` line comments and `/* ... */` block comments, and rejects any `(?i)\bDROP\s+TABLE\b` match. Serialize `MigrateUp` across concurrent callers/instances using an unexported `locker.withLockBlocking(ctx, "synapse", "goose_migrate", ...)` helper with short jittered retry backoff until lock acquired or `ctx.Done()`.
- **Rationale**: `goose.NewProvider` is safe for concurrent `t.Parallel()` tests across isolated schemas, while keeping `withLockBlocking` unexported ensures 100% verbatim adherence to `spec_02 §4.1` exported symbols.
- **Alternatives considered**: Calling `goose.SetBaseFS` / `goose.Up` package globals — rejected because global state breaks concurrent parallel schema tests.

## 4. PG Error Translation & Leak-Free Slow Query Tracing (`errors.go`, `tracer.go`)

- **Decision**:
  - `MapError(op, err)`: return `nil` on `nil`; pass through `context.Canceled`, `context.DeadlineExceeded`, and PG `57014` (`query_canceled`) verbatim; map `pgx.ErrNoRows` -> `apperror.NotFound(op + ": not found")`; map `23505`/`23503` -> `apperror.Wrap(CodeConflict, 409, ...)`; map `22P02`/`22001`/`23502`/`23514` -> `apperror.Wrap(CodeInvalidArgument, 400, ...)`; map `40001`/`40P01` -> `apperror.Wrap(CodeBusy, 409, ...)`; map all others -> `apperror.Internal(op + ": database error", err)`.
  - `slowQueryTracer`: in `TraceQueryStart`, record only `time.Now()` and `compactSQL(data.SQL, 512)` (collapsing whitespace, truncating at 512 chars) — NEVER touch `data.Args`. In `TraceQueryEnd`, observe duration histogram (`synapse_db_query_duration_seconds`), log non-`ErrNoRows`/non-`Canceled` errors at `ErrorContext`, and log queries with `elapsed >= threshold` at `WarnContext` while incrementing `synapse_db_slow_queries_total`.
- **Rationale**: Prevents SQL/credential exposure in both external API responses and internal server logs while preserving full diagnostic metadata (`request_id`, `company_id`, `user_id` via `obs` `ContextHandler`).

## 5. Session & Transaction Advisory Locks (`advisory_lock.go`)

- **Decision**: Compute deterministic `int64` keys via FNV-1a 64-bit over `namespace + ":" + key`. For session-level locks (`Acquire` / `TryWithLock`), ALWAYS acquire a dedicated `*pgxpool.Conn` via `pool.Acquire(ctx)`, run `SELECT pg_try_advisory_lock($1)` on that connection, immediately release the connection on contention (`!ok`) or error, and in `LockLease.Release` use `atomic.Bool.CompareAndSwap(false, true)` to execute `SELECT pg_advisory_unlock($1)` on that exact same connection before calling `conn.Release()`. For transaction-level locks (`TryLockTx`), execute `SELECT pg_try_advisory_xact_lock($1)` on the caller's `Querier`.
- **Rationale**: Eliminates the classic connection-pool advisory-lock leak bug where unlock lands on a different pooled connection, while keeping zero Redis dependencies.
