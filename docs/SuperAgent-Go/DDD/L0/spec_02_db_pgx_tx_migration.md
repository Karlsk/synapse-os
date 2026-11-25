# spec 02 | L0 数据库底座 — pgxpool 连接池、Context 事务透传、goose 迁移与 AdvisoryLock

- **状态**：`冻结待实施`
- **篇号顺序**：02（L0 数据库技术底座，支撑全部 L1 持久化 Store 与后台调度单实例锁）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-02`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1, §4
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1
  - 归档母本：[spec_02_storage_postgres_migrations.md](../_epic/L1/spec_02_storage_postgres_migrations.md)（连接池、`TxManager`、`goose` 迁移与错误映射部分）
- **前置依赖**：[L0/spec_01 (SA-01)](./spec_01_config_obs_error_lifecycle.md)（依赖 `internal/core/apperror`、`internal/infra/obs`、`internal/infra/config`）
- **下游消费者**：`SA-07`–`SA-10`（L1 47 表迁移脚本与 38 个 PostgreSQL Store 实现）、`SA-32`（自动化调度器防重复触发）、`SA-41`（`migrate` CLI 与 `/actuator/health` 探针）

---

## 1. 背景与范围

本篇交付 `synapse-server` 的 **L0 PostgreSQL 17 数据库基础设施包 (`internal/infra/db`)**，将原版 Java `JdbcStorageConfiguration.java`（HikariCP + `JdbcTemplate`）与 `TenantSchema.java`（散落建表 + 危险 `DROP TABLE`）升级为面向 Go 1.26 与 PostgreSQL 17 的高性能、强可观测、零外部中间件依赖的存储引擎底座：
1. **`pgxpool` 连接池调优与健康检查**：强制会话 `TimeZone=UTC` 与 `statement_timeout`；内建连接池 Prometheus 指标采集与 2 秒超时健康检查 `Ping`。
2. **`Querier` 窄接口 + 基于 `context.Context` 的 `TxManager`**：Store 层只需通过 `db.Querier(ctx)` 获取执行器即可自动无感知复用外层事务；嵌套 `WithinTx` 自动通过 PG `SAVEPOINT` 实现子事务回滚，发生 panic 时保证回滚不泄漏事务连接。
3. **`goose/v3` 内嵌 SQL 迁移执行器（永不删表）**：基于非全局的 `goose.NewProvider` API（并发安全），内置迁移前安全扫描（严禁 `-- +goose Up` 含 `DROP TABLE`）与 PG 原生 `AdvisoryLock` 互斥保护，防止多实例并发冷启动迁移冲突。
4. **PG 错误码统一映射 (`MapError`)**：精准将 `23505`（唯一冲突）、`23503`（外键约束）、`22P02`/`22001`/`23502`/`23514`（非法数据）、`40001`/`40P01`（死锁/序列化冲突）及 `pgx.ErrNoRows` 映射为 `SA-01` 的标准 `apperror.*`，不向调用方泄漏原始 SQL 细节。
5. **慢查询 `QueryTracer`**：基于 `pgx.QueryTracer` 自动记录超过阈值（默认 200ms）或执行失败的 SQL（截断且**严禁打印绑定参数明文**），自动携带 `request_id`/`company_id`/`user_id` 并上报 Prometheus 指标。
6. **PostgreSQL 原生 `AdvisoryLock`（零 Redis 分布式锁替代）**：利用 PG 原生 `pg_try_advisory_lock` / `pg_try_advisory_xact_lock` 为定时调度（`AutomationScheduler`、`TaskScheduler`、`InboxTicker`、`SessionPruner`）与启动迁移提供防重复执行互斥能力，**彻底杜绝引入 Redis**。
7. **单测临时 Schema / 事务回滚 Harness**：为下游 `SA-07`–`SA-10` 的 38 个 Store 单测提供可并发（`t.Parallel()`）的独立临时 Schema 隔离器与毫秒级事务回滚测试夹具。

**本篇边界（1.5 人天）**：
- 本篇只交付 `internal/infra/db` 通用 PG17 驱动与测试夹具，**不含任何业务表 DDL 或业务 Store 实现**（47 张业务表迁移与 38 个 Store 归属 L1 `SA-07`–`SA-10`）；
- 本篇不依赖 `internal/core/tenant`（租户上下文隔离与 AST 租户守卫在 `SA-04` 与 `SA-07`–`SA-10` 组合落地）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | `pgxpool` 配置、连接池构建、UTC 会话参数、健康检查 `Ping` 与池指标采集 | `internal/infra/db/pool.go` | 新增 | `superagent-storage/src/main/java/com/superagent/storage/jdbc/JdbcStorageConfiguration.java:L34-L56` |
| 2 | `Querier` 窄接口、`TxManager`（`WithinTx` / `WithinReadOnlyTx` / 嵌套 `SAVEPOINT` / `WithoutTx`） | `internal/infra/db/tx.go` | 新增 | 替代 Spring `TransactionTemplate` / `JdbcTemplate` |
| 3 | `goose/v3` 嵌入 SQL 迁移执行器（`MigrateUp`/`MigrateDownOne`/`Status` + 禁 `DROP TABLE` 静态校验 + 迁移互斥锁） | `internal/infra/db/migrate.go` | 新增 | 升级替代 `superagent-storage/src/main/java/com/superagent/storage/jdbc/TenantSchema.java:L24-L51` |
| 4 | PostgreSQL 错误码（`23505`/`23503`/`22P02`/`pgx.ErrNoRows` 等）→ `apperror` 统一映射器 | `internal/infra/db/errors.go` | 新增 | 替代 Spring `DataAccessException` 转换 |
| 5 | 慢查询与错误 `QueryTracer`（不打印绑定参数 + `slog` 透传 + Prometheus 指标） | `internal/infra/db/tracer.go` | 新增 | V1 可观测性新增补强 |
| 6 | PG 原生 `AdvisoryLock`（事务级 `TryLockTx` + 独占物理连接会话级 `TryWithLock`/`Acquire`，零 Redis） | `internal/infra/db/advisory_lock.go` | 新增 | V1 零 Redis 多实例防重新增 |
| 7 | 单测隔离夹具：临时 Schema 创建/销毁器 & 事务自动回滚运行器 | `internal/infra/db/harness.go` | 新增 | 供 `SA-07`–`SA-10` 全量 Store 复用 |
| 8 | 纯单元测试（错误映射/Tracer/SQL 安全校验/哈希确定性）+ PG17 集成测试（迁移/嵌套回滚/AdvisoryLock 互斥/Harness） | `internal/infra/db/*_test.go`、`internal/infra/db/testdata/migrations/*.sql` | 新增 | 新建 Go TDD 测试套件 |

---

## 3. 数据库设计

本篇不创建任何持久化业务表；运行期仅涉及 PostgreSQL 17 内建与 `goose` 元数据能力：

| 对象 | 用途 | 说明 |
|---|---|---|
| `goose_db_version` | `goose/v3` 迁移版本账本表 | 由 `goose.NewProvider` 自动创建与维护（默认表名 `goose_db_version`），记录已应用迁移版本号与时间戳 |
| PG 咨询锁槽位 (`int64`) | 进程间互斥（迁移锁、Cron 防重锁） | 存于 PostgreSQL 内存锁表（零落盘、零业务表开销），通过确定性 `FNV-1a 64-bit` 哈希命名空间键生成 |
| 临时测试 Schema `test_<hex>` | 单测隔离环境 | 仅由 `NewTestSchemaDB(t, ...)` 在测试期创建并在 `t.Cleanup` 自动 `DROP SCHEMA ... CASCADE` 清理 |

---

## 4. 冻结契约

### 4.1 导出符号（`internal/infra/db`）

```go
package db

import (
	"context"
	"io/fs"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

type Config struct {
	DSN                string
	MaxConns           int32         // 默认 16
	MinConns           int32         // 默认 2
	MaxConnLifetime    time.Duration // 默认 30m
	MaxConnIdleTime    time.Duration // 默认 5m
	HealthCheckPeriod  time.Duration // 默认 30s
	ConnectTimeout     time.Duration // 默认 5s
	StatementTimeout   time.Duration // 默认 30s
	SlowQueryThreshold time.Duration // 默认 200ms
	ApplicationName    string        // 默认 "synapse-server"
}

type Option func(*openOptions)

func WithLogger(l *slog.Logger) Option
func WithRegisterer(r prometheus.Registerer) Option
func WithSearchPath(schema string) Option // 供单测临时 Schema 注入 search_path

// Querier 统一 *pgxpool.Pool 与 pgx.Tx 的最小查询接口，Store 层仅依赖此接口实现事务无感知复用
type Querier interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	SendBatch(ctx context.Context, b *pgx.Batch) pgx.BatchResults
}

// TxManager 基于 context.Context 隐式透传事务边界（同时满足未来 L1 store.TxManager 接口签名）
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
	WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type DB struct { /* pool, logger, locker */ }

func Open(ctx context.Context, cfg Config, opts ...Option) (*DB, error)
func (d *DB) Pool() *pgxpool.Pool
func (d *DB) Querier(ctx context.Context) Querier // ctx 含活动事务返 pgx.Tx，否则返 *pgxpool.Pool
func (d *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
func (d *DB) WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error
func (d *DB) Ping(ctx context.Context) (time.Duration, error)
func (d *DB) Stats() PoolStats
func (d *DB) Locker() *AdvisoryLocker
func (d *DB) Close()

// 上下文事务控制辅助
func InTx(ctx context.Context) bool
func WithoutTx(ctx context.Context) context.Context // 显式剥离外层事务（极少数需独立提交的审计写使用）

// goose/v3 迁移执行器
type MigrationResult struct {
	Version  int64
	Source   string
	Duration time.Duration
}

func ValidateMigrations(migrationsFS fs.FS, dir string) error
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) ([]MigrationResult, error)
func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (*MigrationResult, error)
func MigrateVersion(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) (int64, error)

// PG 错误码 -> apperror 统一映射
func MapError(op string, err error) error
func IsNoRows(err error) bool
func IsUniqueViolation(err error) bool
func IsForeignKeyViolation(err error) bool

// PG 原生 AdvisoryLock（零 Redis 分布式锁）
type AdvisoryLocker struct { /* pool */ }

type LockLease struct { /* conn, keyID, released */ }

func LockKeyID(namespace, key string) int64
func NewAdvisoryLocker(pool *pgxpool.Pool) *AdvisoryLocker
func (l *AdvisoryLocker) TryWithLock(ctx context.Context, namespace, key string, fn func(ctx context.Context) error) (acquired bool, err error)
func (l *AdvisoryLocker) Acquire(ctx context.Context, namespace, key string) (lease *LockLease, acquired bool, err error)
func (lease *LockLease) Release(ctx context.Context) error
func TryLockTx(ctx context.Context, q Querier, namespace, key string) (bool, error) // 必须处于事务内，随事务结束自动释放

// 单测隔离 Harness（未设 TEST_POSTGRES_DSN 时自动 t.Skip）
func NewTestSchemaDB(t testing.TB, migrationsFS fs.FS, dir string) *DB
func RunInRollbackTx(t testing.TB, d *DB, fn func(txCtx context.Context))
```

### 4.2 路由契约

本篇不注册 HTTP 路由。
- L6 `/actuator/health`（`SA-41`）调用 `db.Ping(ctx)`（默认 2 秒超时）检测 PG17 可用性并返回 `latencyMs`；
- L6 `synapse-server migrate` 子命令（`SA-41`）调用 `db.MigrateUp(ctx, d.Pool(), migrationsFS, "migrations")`。

### 4.3 错误语义

`db.MapError(op string, err error) error` 严格执行以下确定性映射规则（绝不把原始 SQL 语句或数据库密码透传至 `AppError.Message`）：

| PostgreSQL / `pgx` 错误来源 | `SQLSTATE` / Sentinel | 映射结果 (`apperror`) | HTTP 状态码 | 备注 |
|---|---|---|---:|---|
| `nil` | — | `nil` | — | 无错误 |
| 未查到记录 | `pgx.ErrNoRows` | `apperror.NotFound(op + ": not found")` | `404` | Store 若需语义 `(nil, nil)`，应先用 `db.IsNoRows(err)` 分流处理 |
| 唯一约束冲突 | `23505` (`unique_violation`) | `apperror.Wrap(CodeConflict, 409, ...)` | `409` | 保留底层 `*pgconn.PgError` 供 `errors.As` 读取 `ConstraintName` |
| 外键约束冲突 | `23503` (`foreign_key_violation`) | `apperror.Wrap(CodeConflict, 409, ...)` | `409` | 保留底层 `*pgconn.PgError` |
| 非法文本/UUID/JSONB/截断/非空/检查约束 | `22P02`, `22001`, `23502`, `23514` | `apperror.Wrap(CodeInvalidArgument, 400, ...)` | `400` | 如非法 UUID、超长字符串、必填列为空 |
| 序列化冲突 / 死锁 | `40001`, `40P01` | `apperror.Wrap(CodeBusy, 409, ...)` | `409` | 提示调用方可重试 |
| 上下文取消 / 超时 | `57014`, `context.Canceled`, `context.DeadlineExceeded` | 原样返回 `err` | 调用方决定 | 不伪装为 `500 internal_error` |
| 其他数据库错误（断连/语法/权限） | 其余 | `apperror.Internal(op+": database error", err)` | `500` | 对外掩码，原始错误仅留在 `Unwrap()` 与服务端日志 |

### 4.4 SQL 契约

1. **会话级强制参数**：每条新建立的物理连接均在启动握手期通过 `RuntimeParams` 注入 `TimeZone=UTC`、`application_name=<appName>` 与 `statement_timeout=<ms>`，确保跨宿主机时区部署时 `TIMESTAMPTZ` 行为 100% 确定；
2. **禁止在 Up 迁移中删表**：`ValidateMigrations` 在执行 `goose.Up` 前扫描所有 `.sql` 文件的 `-- +goose Up` 至 `-- +goose Down` 区间（剔除注释行后），若出现 `DROP TABLE` 立即 fail-fast 拒绝执行；
3. **咨询锁绑定规范**：会话级 `AdvisoryLock` 必须通过 `pool.Acquire(ctx)` 独占单条物理连接并在退出时在**同一物理连接**上执行 `SELECT pg_advisory_unlock($1)` 后归还连接池，严禁直接对 `*pgxpool.Pool` 调用会话级 `pg_try_advisory_lock`（否则解锁落在其他连接导致锁永久泄漏）；
4. **单测 Schema 命名约束**：`NewTestSchemaDB` 生成的 Schema 名严格匹配 `^test_[a-f0-9]{16}$`，避免 SQL 注入风险，同时使用 `pgx.Identifier{schema}.Sanitize()` 拼接 `CREATE SCHEMA` / `DROP SCHEMA`。

---

## 5. 行为语义（含 Java 源码对照、Go 契约定义与详细伪代码）

### 5.1 连接池构建、健康检查与指标 (`pool.go`)

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/JdbcStorageConfiguration.java:L34-L56`。

```go
package db

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
)

func Open(ctx context.Context, cfg Config, opts ...Option) (*DB, error) {
	o := openOptions{logger: slog.Default()}
	for _, fn := range opts {
		fn(&o)
	}
	applyDefaults(&cfg)

	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	pcfg.HealthCheckPeriod = cfg.HealthCheckPeriod
	pcfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout
	pcfg.ConnConfig.RuntimeParams["TimeZone"] = "UTC"
	pcfg.ConnConfig.RuntimeParams["application_name"] = cfg.ApplicationName
	if cfg.StatementTimeout > 0 {
		pcfg.ConnConfig.RuntimeParams["statement_timeout"] = strconv.FormatInt(cfg.StatementTimeout.Milliseconds(), 10)
	}
	if o.searchPath != "" {
		pcfg.ConnConfig.RuntimeParams["search_path"] = o.searchPath + ",public"
	}

	tracer := newSlowQueryTracer(cfg.SlowQueryThreshold, o.logger, o.reg)
	pcfg.ConnConfig.Tracer = tracer

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("create pgxpool: %w", err)
	}
	d := &DB{pool: pool, logger: o.logger, locker: NewAdvisoryLocker(pool)}
	if _, err := d.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("initial postgres ping failed: %w", err)
	}
	if o.reg != nil {
		registerPoolMetrics(o.reg, pool) // GaugeFunc: acquired/idle/total/max_conns
	}
	return d, nil
}

func (d *DB) Ping(ctx context.Context) (time.Duration, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
	}
	start := time.Now()
	err := d.pool.Ping(ctx)
	return time.Since(start), err
}
```

### 5.2 Context 事务透传与嵌套 `SAVEPOINT` (`tx.go`)

```go
package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type txCtxKey struct{}

func (d *DB) Querier(ctx context.Context) Querier {
	if tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx); ok && tx != nil {
		return tx
	}
	return d.pool
}

func InTx(ctx context.Context) bool {
	tx, ok := ctx.Value(txCtxKey{}).(pgx.Tx)
	return ok && tx != nil
}

func WithoutTx(ctx context.Context) context.Context {
	return context.WithValue(ctx, txCtxKey{}, (pgx.Tx)(nil))
}

func (d *DB) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.withinTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted}, fn)
}

func (d *DB) WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return d.withinTxOpts(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadOnly}, fn)
}

func (d *DB) withinTxOpts(ctx context.Context, txOpts pgx.TxOptions, fn func(ctx context.Context) error) (retErr error) {
	var tx pgx.Tx
	var err error
	if parentTx, ok := ctx.Value(txCtxKey{}).(pgx.Tx); ok && parentTx != nil {
		// pgx/v5 的 parentTx.Begin(ctx) 自动签发 PostgreSQL SAVEPOINT
		tx, err = parentTx.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin savepoint: %w", err)
		}
	} else {
		tx, err = d.pool.BeginTx(ctx, txOpts)
		if err != nil {
			return fmt.Errorf("begin tx: %w", err)
		}
	}

	committed := false
	defer func() {
		if !committed {
			// 若业务 ctx 已取消，仍需用短超时背景上下文确保 ROLLBACK 发送到 PG，防止连接脏状态
			rbCtx := ctx
			if rbCtx.Err() != nil {
				var cancel context.CancelFunc
				rbCtx, cancel = context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
			}
			_ = tx.Rollback(rbCtx)
		}
	}()

	txCtx := context.WithValue(ctx, txCtxKey{}, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		if errors.Is(err, pgx.ErrTxClosed) {
			return err
		}
		return fmt.Errorf("commit tx: %w", err)
	}
	committed = true
	return nil
}
```

### 5.3 `goose/v3` 嵌入迁移与禁删表校验 (`migrate.go`)

> **Java 源码对照**：彻底摒弃 `superagent-storage/src/main/java/com/superagent/storage/jdbc/TenantSchema.java:L30-L36` 的自动 `DROP TABLE`，改为版本化向前演进。

```go
package db

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

var dropTableRe = regexp.MustCompile(`(?i)\bDROP\s+TABLE\b`)

// ValidateMigrations 静态校验全部嵌入迁移文件的 -- +goose Up 段绝对不含 DROP TABLE。
func ValidateMigrations(migrationsFS fs.FS, dir string) error {
	subFS, err := fs.Sub(migrationsFS, dir)
	if err != nil {
		return fmt.Errorf("sub migrations fs: %w", err)
	}
	entries, err := fs.ReadDir(subFS, ".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		data, err := fs.ReadFile(subFS, e.Name())
		if err != nil {
			return err
		}
		upSection := extractGooseUpSQL(string(data)) // 仅取 -- +goose Up 到 -- +goose Down 之间并剥离单行注释
		if dropTableRe.MatchString(upSection) {
			return fmt.Errorf("forbidden DROP TABLE in migration %s (+goose Up)", e.Name())
		}
	}
	return nil
}

// MigrateUp 先静态检验安全性，再通过 PG AdvisoryLock 串行化多实例启动迁移，执行 goose.Provider.Up。
func MigrateUp(ctx context.Context, pool *pgxpool.Pool, migrationsFS fs.FS, dir string) ([]MigrationResult, error) {
	if err := ValidateMigrations(migrationsFS, dir); err != nil {
		return nil, err
	}
	locker := NewAdvisoryLocker(pool)
	var out []MigrationResult
	// 迁移启动时使用阻塞获取（轮询退避直到拿到锁或 ctx 超时），确保双实例同时冷启动时第二个实例安全等待第一个完成
	err := locker.WithLockBlocking(ctx, "synapse", "goose_migrate", func(lockCtx context.Context) error {
		subFS, err := fs.Sub(migrationsFS, dir)
		if err != nil {
			return err
		}
		sqlDB := stdlib.OpenDBFromPool(pool)
		defer sqlDB.Close()

		provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)
		if err != nil {
			return fmt.Errorf("init goose provider: %w", err)
		}
		res, err := provider.Up(lockCtx)
		if err != nil {
			return fmt.Errorf("goose up: %w", err)
		}
		for _, r := range res {
			out = append(out, MigrationResult{
				Version:  r.Source.Version,
				Source:   r.Source.Path,
				Duration: r.Duration,
			})
		}
		return nil
	})
	return out, err
}
```

### 5.4 慢查询 `QueryTracer` (`tracer.go`)

```go
package db

type traceStartKey struct{}

type traceStart struct {
	at  time.Time
	sql string
}

type slowQueryTracer struct {
	threshold time.Duration
	logger    *slog.Logger
	slowTotal prometheus.Counter
	durHist   prometheus.Histogram
}

func (t *slowQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	// 安全铁律：仅记录归一化裁剪后的 SQL 模板，绝不记录 data.Args（防止密码、Token、密文进日志）
	return context.WithValue(ctx, traceStartKey{}, traceStart{
		at:  time.Now(),
		sql: compactSQL(data.SQL, 512),
	})
}

func (t *slowQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	st, ok := ctx.Value(traceStartKey{}).(traceStart)
	if !ok {
		return
	}
	elapsed := time.Since(st.at)
	if t.durHist != nil {
		t.durHist.Observe(elapsed.Seconds())
	}
	if data.Err != nil && !errors.Is(data.Err, pgx.ErrNoRows) && !errors.Is(data.Err, context.Canceled) {
		t.logger.ErrorContext(ctx, "postgres query failed",
			"duration_ms", elapsed.Milliseconds(), "sql", st.sql, "err", data.Err)
		return
	}
	if t.threshold > 0 && elapsed >= t.threshold {
		if t.slowTotal != nil {
			t.slowTotal.Inc()
		}
		t.logger.WarnContext(ctx, "slow postgres query",
			"duration_ms", elapsed.Milliseconds(), "threshold_ms", t.threshold.Milliseconds(),
			"rows", data.CommandTag.RowsAffected(), "sql", st.sql)
	}
}
```

### 5.5 PostgreSQL 原生 `AdvisoryLock`（零 Redis 防 Cron 重复）(`advisory_lock.go`)

```go
package db

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func LockKeyID(namespace, key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(namespace))
	_, _ = h.Write([]byte{':'})
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64())
}

// TryWithLock 非阻塞尝试获取会话级咨询锁：
// - 未抢到锁时立刻归还物理连接并返回 (false, nil)，供 Cron 调度器静默跳过本轮；
// - 抢到锁后在同一物理连接上持有锁执行 fn，退出时必然在同连接执行 pg_advisory_unlock 并 Release。
func (l *AdvisoryLocker) TryWithLock(ctx context.Context, namespace, key string, fn func(ctx context.Context) error) (bool, error) {
	lease, acquired, err := l.Acquire(ctx, namespace, key)
	if err != nil || !acquired {
		return false, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = lease.Release(releaseCtx)
	}()
	return true, fn(ctx)
}

func (l *AdvisoryLocker) Acquire(ctx context.Context, namespace, key string) (*LockLease, bool, error) {
	conn, err := l.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire conn for advisory lock: %w", err)
	}
	keyID := LockKeyID(namespace, key)
	var ok bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, keyID).Scan(&ok); err != nil {
		conn.Release()
		return nil, false, fmt.Errorf("pg_try_advisory_lock(%s:%s): %w", namespace, key, err)
	}
	if !ok {
		conn.Release()
		return nil, false, nil
	}
	return &LockLease{conn: conn, keyID: keyID}, true, nil
}

func (lease *LockLease) Release(ctx context.Context) error {
	if lease == nil || !lease.released.CompareAndSwap(false, true) {
		return nil
	}
	defer lease.conn.Release()
	var unlocked bool
	if err := lease.conn.QueryRow(ctx, `SELECT pg_advisory_unlock($1)`, lease.keyID).Scan(&unlocked); err != nil {
		return fmt.Errorf("pg_advisory_unlock: %w", err)
	}
	return nil
}

// TryLockTx 事务级咨询锁：随外层事务 COMMIT/ROLLBACK 自动释放（不额外占用独立连接）。
func TryLockTx(ctx context.Context, q Querier, namespace, key string) (bool, error) {
	var ok bool
	err := q.QueryRow(ctx, `SELECT pg_try_advisory_xact_lock($1)`, LockKeyID(namespace, key)).Scan(&ok)
	return ok, err
}
```

### 5.6 单测临时 Schema & 事务回滚 Harness (`harness.go`)

```go
package db

// NewTestSchemaDB 为每个测试创建独立临时 Schema (test_<16hex>)，隔离并发 t.Parallel() 测试；
// 未配置 TEST_POSTGRES_DSN 时优雅 t.Skip，保障纯离线 `go test ./...` 秒级通过。
func NewTestSchemaDB(t testing.TB, migrationsFS fs.FS, dir string) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set; skipping postgres integration test")
	}
	schema := "test_" + randomHex(8)
	ctx := context.Background()

	adminDB, err := Open(ctx, Config{DSN: dsn, MaxConns: 2, MinConns: 1})
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	safeSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := adminDB.Pool().Exec(ctx, "CREATE SCHEMA "+safeSchema); err != nil {
		adminDB.Close()
		t.Fatalf("create test schema %s: %v", schema, err)
	}

	testDB, err := Open(ctx, Config{DSN: dsn, MaxConns: 8, MinConns: 1}, WithSearchPath(schema))
	if err != nil {
		_, _ = adminDB.Pool().Exec(ctx, "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
		adminDB.Close()
		t.Fatalf("open schema db: %v", err)
	}
	if migrationsFS != nil {
		if _, err := MigrateUp(ctx, testDB.Pool(), migrationsFS, dir); err != nil {
			testDB.Close()
			_, _ = adminDB.Pool().Exec(ctx, "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
			adminDB.Close()
			t.Fatalf("migrate test schema %s: %v", schema, err)
		}
	}
	t.Cleanup(func() {
		testDB.Close()
		_, _ = adminDB.Pool().Exec(context.Background(), "DROP SCHEMA IF EXISTS "+safeSchema+" CASCADE")
		adminDB.Close()
	})
	return testDB
}

// RunInRollbackTx 在单事务中运行测试并在结束时强制 ROLLBACK（内层调用 WithinTx 自动走 SAVEPOINT）。
func RunInRollbackTx(t testing.TB, d *DB, fn func(txCtx context.Context)) {
	t.Helper()
	ctx := context.Background()
	tx, err := d.Pool().BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatalf("begin rollback test tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(context.WithValue(ctx, txCtxKey{}, tx))
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：在 `synapse-server/go.mod` 引入 `github.com/jackc/pgx/v5` 与 `github.com/pressly/goose/v3`，创建 `internal/infra/db/` 目录与 `testdata/migrations/` 测试固件。
- [ ] **T2**：编写 `internal/infra/db/errors_test.go` 与 `tracer_test.go`（零 PG 离线单测红灯）→ 实现 `errors.go` 与 `tracer.go`（完整 PG SQLSTATE 映射、`ErrNoRows` 识别、慢查询告警与零参数泄漏）。
- [ ] **T3**：编写 `internal/infra/db/migrate_unit_test.go`（离线测试 `ValidateMigrations` 拦截 `DROP TABLE` 与放行合法 Up 脚本）→ 实现 `migrate.go` 核心逻辑。
- [ ] **T4**：实现 `pool.go`、`tx.go`、`advisory_lock.go`、`harness.go`。
- [ ] **T5**：编写 `internal/infra/db/db_integration_test.go`（基于 `TEST_POSTGRES_DSN` 验证 `Open`/`Ping`/`TimeZone=UTC`、`WithinTx` 提交/回滚/嵌套 `SAVEPOINT`/panic 回滚、`MigrateUp`→`DownOne`→`Up` 幂等、`AdvisoryLock` 并发互斥与释放、`NewTestSchemaDB` 与 `RunInRollbackTx` 隔离性）。
- [ ] **T6**：运行离线单测 + 面向真实 `postgres:17-alpine` 的集成单测（`-race -cover`），确认全绿。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-02` 状态更新为 `已实施（commit <hash>）`；
- [ ] `synapse-server/README.md`：补充本地测试 PG17 命令（`TEST_POSTGRES_DSN=postgres://...`）及 `AdvisoryLock` 使用规范（严禁直接对池调用会话咨询锁，统一经 `db.Locker()`）。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | PG 错误码 → `apperror` 全量映射 | `internal/infra/db/errors_test.go` | `23505`→409 `conflict`、`23503`→409 `conflict`、`22P02`/`22001`/`23502`→400 `invalid_argument`、`40001`/`40P01`→409 `busy`、`pgx.ErrNoRows`→404 `not_found`、`context.Canceled` 原样透传、未知错误→500 且不泄漏敏感 SQL |
| 2 | `SlowQueryTracer` 阈值告警与参数防泄漏 | `internal/infra/db/tracer_test.go` | 模拟 250ms 查询（阈值 100ms）输出 `WARN` 含 `request_id`/`company_id`/`duration_ms` 与裁剪 SQL，且日志缓冲区绝对不含传入的密码参数值；Prometheus 慢查询计数 +1 |
| 3 | `ValidateMigrations` 禁删表静态守卫 | `internal/infra/db/migrate_unit_test.go` | `+goose Up` 含 `DROP TABLE foo` 立即返错；仅在 `+goose Down` 含 `DROP TABLE` 或注释中出现 `DROP TABLE` 时合法通过 |
| 4 | `LockKeyID` 确定性与命名空间隔离 | `internal/infra/db/advisory_lock_test.go` | 同 `(ns, key)` 跨调用哈希一致；`("cron","a")` 与 `("migrate","a")` 产生不同 `int64` 锁 ID |
| 5 | `Open` 连接池、`UTC` 会话时区与 `Ping`（PG17） | `internal/infra/db/db_integration_test.go` | `SHOW TIME ZONE` 精确返回 `UTC`；`Ping` 延迟 >0 且 err==nil；Prometheus 注册表采集到 `synapse_db_pool_max_conns` |
| 6 | `TxManager` 透传、嵌套 `SAVEPOINT` 与 panic 安全（PG17） | `internal/infra/db/db_integration_test.go` | 同事务内多个 `Querier(ctx)` 读写原子可见；外层返回 err 全部回滚；内层 `WithinTx` 返回 err 仅回滚内层而外层提交成功；回调内 `panic` 后数据库记录已回滚且连接池无泄漏 |
| 7 | `goose` 迁移 `Up` → `DownOne` → `Up` 幂等与并发锁（PG17） | `internal/infra/db/db_integration_test.go` | 8 协程并发调用 `MigrateUp` 全部无错返回且版本表仅执行 1 次；`MigrateDownOne` 与再次 `MigrateUp` 正常 |
| 8 | `AdvisoryLock` 并发互斥与跨连接释放（PG17） | `internal/infra/db/db_integration_test.go` | 协程 A 持锁期间协程 B `TryWithLock` 立即返回 `(false, nil)`；A 退出后 B 立即成功抢锁 `(true, nil)`；10 协程并发争抢同一 Cron 锁同一时刻并发执行数恒 ≤ 1 |
| 9 | 单测 Harness 临时 Schema 与回滚夹具（PG17） | `internal/infra/db/db_integration_test.go` | 两个 `t.Parallel()` 测试各自写入同名表互不干扰且 `Cleanup` 后临时 Schema 已彻底删除；`RunInRollbackTx` 写入数据在闭包结束后主库查询不到 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/infra/db/...` 离线单测零依赖秒级全绿
- [ ] 面向真实 **PostgreSQL 17** 运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/infra/db/...` 全绿，行覆盖率 ≥ 80%
- [ ] §2 交付物表 8 项全部存在，§4.1 导出符号签名逐字一致
- [ ] `internal/infra/db` 零引入 Redis 或任何业务领域包（仅依赖标准库、`pgx/v5`、`goose/v3`、`prometheus` 与 `SA-01` 包）
- [ ] `SA-01` 回归测试全绿

---

## 10. 可提交节点

- 建议 commit 1（连接池、事务透传、错误映射与慢查询追踪）：`feat(db): add pgxpool, context TxManager with SAVEPOINT, PG error mapper and slow query tracer (spec 02, PR SA-02)`
- 建议 commit 2（goose 迁移、PG AdvisoryLock 与测试夹具）：`feat(db): add embedded goose migrator, PG advisory lock (zero Redis) and isolated test harness (spec 02, PR SA-02)`
