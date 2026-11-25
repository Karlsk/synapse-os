# Frozen Go Package Contracts: SA-02 (`internal/infra/db`)

Source of truth: [docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md](../../../docs/SuperAgent-Go/DDD/L0/spec_02_db_pgx_tx_migration.md) `§4.1`–`§4.4`. All exported symbol names, parameter lists, and return types MUST match verbatim.

## Exported Symbols (`package db`)

- **Pool & Options (`pool.go`)**:
  - `type Config struct { DSN string; MaxConns, MinConns int32; MaxConnLifetime, MaxConnIdleTime, HealthCheckPeriod, ConnectTimeout, StatementTimeout, SlowQueryThreshold time.Duration; ApplicationName string }`
  - `type PoolStats struct { ... }`
  - `type Option func(*openOptions)`
  - `func WithLogger(l *slog.Logger) Option`
  - `func WithRegisterer(r prometheus.Registerer) Option`
  - `func WithSearchPath(schema string) Option`
  - `type DB struct`
  - `func Open(ctx context.Context, cfg Config, opts ...Option) (*DB, error)`
  - `func (d *DB) Pool() *pgxpool.Pool`
  - `func (d *DB) Ping(ctx context.Context) (time.Duration, error)`
  - `func (d *DB) Stats() PoolStats`
  - `func (d *DB) Locker() *AdvisoryLocker`
  - `func (d *DB) Close()`
- **Transactions (`tx.go`)**:
  - `type Querier interface { Exec(...); Query(...); QueryRow(...); SendBatch(...) }`
  - `type TxManager interface { WithinTx(ctx, fn) error; WithinReadOnlyTx(ctx, fn) error }`
  - `func (d *DB) Querier(ctx context.Context) Querier`
  - `func (d *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error`
  - `func (d *DB) WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error`
  - `func InTx(ctx context.Context) bool`
  - `func WithoutTx(ctx context.Context) context.Context`
- **Migrations (`migrate.go`)**:
  - `type MigrationResult struct { Version int64; Source string; Duration time.Duration }`
  - `func ValidateMigrations(migrationsFS fs.FS, dir string) error`
  - `func MigrateUp(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) ([]MigrationResult, error)`
  - `func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (*MigrationResult, error)`
  - `func MigrateVersion(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (int64, error)`
- **Error Mapping (`errors.go`)**:
  - `func MapError(op string, err error) error`
  - `func IsNoRows(err error) bool`
  - `func IsUniqueViolation(err error) bool`
  - `func IsForeignKeyViolation(err error) bool`
- **Advisory Lock (`advisory_lock.go`)**:
  - `type AdvisoryLocker struct`
  - `type LockLease struct`
  - `func LockKeyID(namespace, key string) int64`
  - `func NewAdvisoryLocker(pool *pgxpool.Pool) *AdvisoryLocker`
  - `func (l *AdvisoryLocker) TryWithLock(ctx context.Context, namespace, key string, fn func(ctx context.Context) error) (acquired bool, err error)`
  - `func (l *AdvisoryLocker) Acquire(ctx context.Context, namespace, key string) (lease *LockLease, acquired bool, err error)`
  - `func (lease *LockLease) Release(ctx context.Context) error`
  - `func TryLockTx(ctx context.Context, q Querier, namespace, key string) (bool, error)`
- **Test Harness (`harness.go`)**:
  - `func NewTestSchemaDB(t testing.TB, migrationsFS fs.FS, dir string) *DB`
  - `func RunInRollbackTx(t testing.TB, d *DB, fn func(txCtx context.Context))`
