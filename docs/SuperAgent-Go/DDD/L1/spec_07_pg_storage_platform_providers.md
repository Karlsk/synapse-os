# spec 07 | L1 PG 存储（一）— 平台核心、供应商连接器与多租户守卫

- **状态**：`冻结待实施`
- **篇号顺序**：07（L1 PostgreSQL 17 存储层首篇，奠定 `internal/storage/postgres` 迁移嵌入、公共读写基类与静态/运行时多租户守卫）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-07`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§4
  - [_epic/L1/spec_02_storage_postgres_migrations.md](../_epic/L1/spec_02_storage_postgres_migrations.md)（47 表 + 38 Store 归档母本）
  - [SuperAgent/09_数据存储模型全量设计.md](../../../SuperAgent/09_数据存储模型全量设计.md)
- **前置依赖**：
  - `L0/spec_02`（`SA-02`：`internal/infra/db` 提供的 `pgxpool` 连接池、`db.Querier`、`db.QuerierFromCtx(ctx, pool)`、`db.TxManager`、`db.MapPGError`、`db.Migrate`/`db.MigrateDown`/`db.MigrateDownOne`、`db.NewTestSchema(t)`）
  - `L1/spec_05`（`SA-05`：`internal/core/{domain,store,tenant,apperror}` 中的租户/认证/组织/设置/连接器/供应商领域模型与 Store 接口）
- **下游消费者**：`SA-08`（资产与成本存储）、`SA-09`（Agent/任务存储）、`SA-10`（事件/编排存储与全量 38 Store 装配）、`SA-15`–`SA-20`（L2 平台治理、认证会话、用户组织、系统设置、供应商密钥池与连接器业务域）

---

## 1. 背景与范围

本篇交付 `synapse-server`（`module github.com/Karlsk/synapse-os`，`go 1.26.1`）**L1 PostgreSQL 17 存储实现层 (`internal/storage/postgres`)** 的首篇基础包，严格复用 `L0/spec_02 (SA-02)` 提供的 `internal/infra/db` 连接池、事务与 `goose/v3` 迁移基础设施（绝不重复造轮子），完成以下四项核心交付：

1. **内嵌迁移入口与公共存储基类 (`migrations.go`, `helpers.go`)**：声明 `//go:embed migrations/*.sql` 文件系统，将迁移升降级委托给 `internal/infra/db`；封装 `baseStore` 统一经由 `db.QuerierFromCtx(ctx, pool)` 获取连接/事务句柄、经由 `tenant.Require(ctx)` 强制校验租户上下文、经由 `db.MapPGError(op, err)` 统一转换 PG 错误码，并提供 UTC 毫秒截断与 CSV/换行序列化助手。
2. **前 2 个 PG17 版本化迁移文件（共 10 张表：2 全局 + 8 租户）**：
   - `00001_platform_core.sql`（6 张表）：`company`、`user_session`（V1 新增持久化登录会话表，彻底替代 JVM 内存 `HttpSession`，零外部缓存依赖）、`app_user`、`user_manager`、`user_team`、`system_setting`；
   - `00002_providers_connectors.sql`（4 张表）：`connector`、`connector_credential`、`model_provider`（`models_json` 升级 PG17 原生 `JSONB`）、`provider_credential`；
   - 彻底废弃原版 `TenantSchema.java:L30-L51` 的「缺 `company_id` 即 `DROP TABLE`」与各 Store 构造器内零散 `information_schema` `ALTER TABLE` 写法，严格坚持向前演进与零物理外键。
3. **9 个 PostgreSQL 17 Store 实现**：覆盖平台核心 5 个 Store（`CompanyStore`、`SessionStore`、`UserStore`、`OrgStore`、`SystemSettingStore`）与供应商/连接器 4 个 Store（`ConnectorStore`、`ConnectorCredentialStore`、`ModelProviderStore`、`ProviderCredentialStore`）。
4. **静态 + 运行时多租户隔离守卫 (`guard_test.go`)**：升级复刻 Java `TenantScopeGuardTest.java:L20-L68`，用 Go 标准库 `go/parser` + `go/ast` 同步审查迁移 SQL 与 Store 方法体 AST，确保所有含 `company_id` 的表结构与查询均强制带租户谓词且永不在 `-- +goose Up` 中删表；随 `SA-08`–`SA-10` 逐批合入自动覆盖全量表与 Store，并在 `SA-10` 锁定终态 **47 表 / 38 Store** 精确守恒。

**本篇边界与 47 表 / 38 Store 切分口径**：
- **不重复造基础设施**：`pgxpool.Pool` 初始化、`TimeZone=UTC` 会话参数、`TxManager`（含 PG `SAVEPOINT` 嵌套事务）、`MapPGError`、`goose/v3` 执行引擎及 `NewTestSchema(t)` 均由 `L0/spec_02 (internal/infra/db)` 交付，本篇仅消费不重实现。
- **跨篇归属说明**：
  - 资产（项目/文件/目录/技能）与成本（`task_model_choice`/`token_usage`/`turn_timing`）共 13 表 / 9 Store 归属 `SA-08`（`00003_assets_cost.sql`）；
  - Agent/对话/记忆与任务/自动化共 17 表 / 14 Store 归属 `SA-09`（`00004_agent_chat_memory.sql` + `00005_task_automation.sql`）；
  - 事件/收件箱/SuperAgent 编排及 `notice`（承接母本 `_epic/L1/spec_02` `store_superagent_inbox.go` 6 Store 聚合分组）共 7 表 / 6 Store 及全量 `NewStores` 装配工厂归属 `SA-10`（`00006_event_orchestrator.sql`）。
  - 四篇精确守恒：`10 + 13 + 17 + 7 = 47 张表`（2 全局 + 45 租户），`9 + 9 + 14 + 6 = 38 个 Store`（2 全局 + 36 租户）。
- **无业务编排与无外部缓存**：本篇不含密码哈希、配额统计、密钥加解密（均留给 `SA-15`–`SA-20`）；系统纯 PostgreSQL 17 持久化，不引入任何额外内存键值服务。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考路径（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） |
|---|---|---|---|---|
| 1 | `goose` 内嵌迁移文件系统与升降级入口（委托 `internal/infra/db`） | `internal/storage/postgres/migrations.go` | 新增 | `superagent-storage/src/main/java/com/superagent/storage/jdbc/TenantSchema.java`（V1 升级替代） |
| 2 | 公共读写基类 `baseStore`（复用 `db.QuerierFromCtx`、`tenant.Require`、`db.MapPGError`）与 UTC/文本转换助手 | `internal/storage/postgres/helpers.go` | 新增 | `superagent-storage/src/main/java/com/superagent/storage/jdbc/TenantSchema.java:L24-L27` |
| 3 | 平台核心 6 表 PG17 迁移脚本（含 V1 新增 `user_session`） | `internal/storage/postgres/migrations/00001_platform_core.sql` | 新增 | `JdbcCompanyStore.java:L21-L32`, `MysqlUserStore.java:L13-L31`, `MysqlOrgStore.java:L14-L29`, `MysqlSystemSettingStore.java:L12-L18` |
| 4 | 供应商与连接器 4 表 PG17 迁移脚本 | `internal/storage/postgres/migrations/00002_providers_connectors.sql` | 新增 | `MysqlConnectorStore.java:L13-L27`, `MysqlConnectorCredentialStore.java:L13-L23`, `MysqlModelProviderStore.java:L13-L30`, `MysqlProviderCredentialStore.java:L10-L30` |
| 5 | 租户、会话、用户、组织 4 个 PG Store 实现 | `internal/storage/postgres/store_tenant_auth.go` | 新增 | `JdbcCompanyStore.java`, `AbstractJdbcUserStore.java`, `MysqlUserStore.java`, `AbstractJdbcOrgStore.java`, `MysqlOrgStore.java` |
| 6 | 系统设置、连接器/凭证、模型供应商/密钥池 5 个 PG Store 实现 | `internal/storage/postgres/store_platform.go` | 新增 | `AbstractJdbcSystemSettingStore.java`, `MysqlSystemSettingStore.java`, `AbstractJdbcConnectorStore.java`, `MysqlConnectorStore.java`, `AbstractJdbcConnectorCredentialStore.java`, `MysqlConnectorCredentialStore.java`, `AbstractJdbcModelProviderStore.java`, `MysqlModelProviderStore.java`, `AbstractJdbcProviderCredentialStore.java`, `MysqlProviderCredentialStore.java` |
| 7 | 静态 SQL + Go AST 多租户隔离与禁删表守卫 + 运行时租户隔离验证辅助 | `internal/storage/postgres/guard_test.go` | 新增 | `superagent-storage/src/test/java/com/superagent/storage/jdbc/TenantScopeGuardTest.java:L20-L68` |
| 8 | 平台核心 4 Store 集成单测（复用 `db.NewTestSchema(t)`） | `internal/storage/postgres/store_tenant_auth_test.go` | 新增 | `CompanyStoreTest.java`, `UserStoreTest.java`, `OrgStoreTest.java` |
| 9 | 设置/连接器/供应商 5 Store 集成单测（复用 `db.NewTestSchema(t)`） | `internal/storage/postgres/store_platform_test.go` | 新增 | `SystemSettingStoreTest.java`, `ConnectorStoreTest.java`, `ModelProviderStoreTest.java`, `ProviderCredentialStoreTest.java` |

---

## 3. 数据库设计（PG17 DDL · 2 个 `goose` 迁移文件 · 10 张表）

> **统一设计红线（贯穿 `SA-07`–`SA-10` 全部 47 表）**：
> - 零物理外键（保持软删除与历史审计记录完整）；
> - 除 2 张全局表（`company`、`user_session`）外，全部租户表 `company_id VARCHAR(64) NOT NULL` 必为主键首列、唯一约束首列与二级索引首列；
> - 全部索引名带表名前缀保证 PG17 全局唯一（`idx_<table>_<purpose>` / `uq_<table>_<purpose>`）；
> - 时间列统一 `TIMESTAMPTZ`（强制 UTC 毫秒精度写入），JSON 载荷列采用原生 `JSONB`，不使用 PG 枚举类型或存储过程；
> - `-- +goose Up` 段绝不允许出现 `DROP TABLE`。

### 3.1 `migrations/00001_platform_core.sql`（6 张表：2 全局 + 4 租户）

```sql
-- +goose Up

-- 1. [GLOBAL] 企业租户表（Java: JdbcCompanyStore.java:L21-L32, L60-L62）
CREATE TABLE company (
    id                VARCHAR(64)  PRIMARY KEY,
    name              VARCHAR(255) NOT NULL,
    enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
    data_key_cipher   TEXT         NOT NULL DEFAULT '',
    max_users         INTEGER,
    max_running_tasks INTEGER,
    monthly_tokens    BIGINT,
    created_at        TIMESTAMPTZ  NOT NULL,
    updated_at        TIMESTAMPTZ  NOT NULL
);

-- 2. [GLOBAL][V1 新增] 服务端持久化登录会话表（替代 JVM 内存 HttpSession，服务重启不踢人）
CREATE TABLE user_session (
    id          VARCHAR(128) PRIMARY KEY, -- 32 字节加密安全随机 hex（64 字符）
    company_id  VARCHAR(64)  NOT NULL,
    user_id     VARCHAR(64)  NOT NULL,
    password_ok BOOLEAN      NOT NULL DEFAULT FALSE,
    expires_at  TIMESTAMPTZ  NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_user_session_expires ON user_session (expires_at);
CREATE INDEX idx_user_session_user    ON user_session (company_id, user_id);

-- 3. [TENANT] 预置用户账号表（Java: MysqlUserStore.java:L13-L31）
CREATE TABLE app_user (
    company_id           VARCHAR(64)  NOT NULL,
    id                   VARCHAR(64)  NOT NULL,
    username             VARCHAR(190) NOT NULL,
    display_name         VARCHAR(255) NOT NULL DEFAULT '',
    roles                VARCHAR(512) NOT NULL DEFAULT '',
    enabled              BOOLEAN      NOT NULL DEFAULT TRUE,
    password_hash        VARCHAR(255) NOT NULL DEFAULT '',
    phone                VARCHAR(32)  NOT NULL DEFAULT '',
    external_id          VARCHAR(255) NOT NULL DEFAULT '',
    source               VARCHAR(16)  NOT NULL, -- LOCAL | PHONE | SSO | WECHAT
    created_at           TIMESTAMPTZ  NOT NULL,
    updated_at           TIMESTAMPTZ  NOT NULL,
    must_change_password BOOLEAN      NOT NULL DEFAULT FALSE,
    PRIMARY KEY (company_id, id),
    CONSTRAINT uq_app_user_username UNIQUE (company_id, username)
);
CREATE INDEX idx_app_user_phone    ON app_user (company_id, phone) WHERE phone <> '';
CREATE INDEX idx_app_user_external ON app_user (company_id, external_id) WHERE external_id <> '';

-- 4. [TENANT] 汇报关系表（Java: MysqlOrgStore.java:L14-L20；username 列存储 app_user.id）
CREATE TABLE user_manager (
    company_id VARCHAR(64)  NOT NULL,
    username   VARCHAR(190) NOT NULL,
    manager    VARCHAR(190) NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, username)
);

-- 5. [TENANT] 层级团队归属表（Java: MysqlOrgStore.java:L23-L29）
CREATE TABLE user_team (
    company_id VARCHAR(64)  NOT NULL,
    username   VARCHAR(190) NOT NULL,
    team_path  VARCHAR(514) NOT NULL,
    PRIMARY KEY (company_id, username, team_path)
);
CREATE INDEX idx_user_team_path ON user_team (company_id, team_path);

-- 6. [TENANT] 公司级扁平 KV 系统配置表（Java: MysqlSystemSettingStore.java:L12-L18）
CREATE TABLE system_setting (
    company_id    VARCHAR(64)   NOT NULL,
    setting_key   VARCHAR(128)  NOT NULL,
    setting_value VARCHAR(4096) NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, setting_key)
);

-- +goose Down
DROP TABLE IF EXISTS system_setting;
DROP TABLE IF EXISTS user_team;
DROP TABLE IF EXISTS user_manager;
DROP TABLE IF EXISTS app_user;
DROP TABLE IF EXISTS user_session;
DROP TABLE IF EXISTS company;
```

### 3.2 `migrations/00002_providers_connectors.sql`（4 张表：全租户隔离）

```sql
-- +goose Up

-- 7. [TENANT] MCP 连接器定义表（Java: MysqlConnectorStore.java:L13-L27）
CREATE TABLE connector (
    company_id  VARCHAR(64)   NOT NULL,
    id          VARCHAR(64)   NOT NULL,
    name        VARCHAR(255)  NOT NULL,
    description VARCHAR(1024) NOT NULL DEFAULT '',
    kind        VARCHAR(16)   NOT NULL, -- MCP
    auth_method VARCHAR(32)   NOT NULL, -- NONE | SHARED_TOKEN | PERSONAL_TOKEN | OAUTH
    enabled     BOOLEAN       NOT NULL DEFAULT TRUE,
    transport   VARCHAR(16)   NOT NULL, -- STDIO | HTTP | SSE
    endpoint    VARCHAR(2048) NOT NULL DEFAULT '',
    token_env   VARCHAR(128)  NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ   NOT NULL,
    updated_at  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id)
);

-- 8. [TENANT] 连接器加密凭证表（Java: MysqlConnectorCredentialStore.java:L13-L23, L33）
CREATE TABLE connector_credential (
    company_id     VARCHAR(64)   NOT NULL,
    connector_id   VARCHAR(64)   NOT NULL,
    owner          VARCHAR(128)  NOT NULL, -- '' 表示公司共享凭证
    secret_cipher  VARCHAR(4096) NOT NULL,
    account_label  VARCHAR(255)  NOT NULL DEFAULT '',
    updated_at     TIMESTAMPTZ   NOT NULL,
    refresh_cipher VARCHAR(4096) NOT NULL DEFAULT '',
    expires_at     TIMESTAMPTZ,
    PRIMARY KEY (company_id, connector_id, owner)
);

-- 9. [TENANT] 模型供应商 CLI 状态与模型目录表（Java: MysqlModelProviderStore.java:L13-L30, L48；models_json 升 JSONB）
CREATE TABLE model_provider (
    company_id        VARCHAR(64)   NOT NULL,
    id                VARCHAR(64)   NOT NULL,
    provider_key      VARCHAR(64)   NOT NULL,
    installed         BOOLEAN       NOT NULL DEFAULT FALSE,
    can_infer         BOOLEAN       NOT NULL DEFAULT FALSE,
    current_version   VARCHAR(64)   NOT NULL DEFAULT '',
    latest_version    VARCHAR(64)   NOT NULL DEFAULT '',
    status            VARCHAR(32)   NOT NULL, -- unsupported | not_installed | no_auth | ok | needs_upgrade
    last_checked_at   TIMESTAMPTZ,
    models_json       JSONB,
    models_error      VARCHAR(1024) NOT NULL DEFAULT '',
    models_checked_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ   NOT NULL,
    updated_at        TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id),
    CONSTRAINT uq_model_provider_key UNIQUE (company_id, provider_key)
);

-- 10. [TENANT] 供应商多密钥容灾池表（Java: MysqlProviderCredentialStore.java:L10-L30, L37）
CREATE TABLE provider_credential (
    company_id   VARCHAR(64)   NOT NULL,
    id           VARCHAR(64)   NOT NULL,
    provider_key VARCHAR(32)   NOT NULL,
    label        VARCHAR(100)  NOT NULL,
    base_url     VARCHAR(512)  NOT NULL DEFAULT '',
    key_cipher   TEXT          NOT NULL,
    bearer       BOOLEAN       NOT NULL DEFAULT FALSE,
    priority     INTEGER       NOT NULL DEFAULT 0,
    enabled      BOOLEAN       NOT NULL DEFAULT TRUE,
    status       VARCHAR(16)   NOT NULL DEFAULT 'unknown', -- ok | error | unknown
    last_error   VARCHAR(1000) NOT NULL DEFAULT '',
    checked_at   TIMESTAMPTZ,
    deleted      BOOLEAN       NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ   NOT NULL,
    updated_at   TIMESTAMPTZ   NOT NULL,
    source       VARCHAR(16)   NOT NULL DEFAULT 'console', -- console | machine
    key_hash     VARCHAR(64)   NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_provider_credential_lookup ON provider_credential (company_id, provider_key, deleted, priority);

-- +goose Down
DROP TABLE IF EXISTS provider_credential;
DROP TABLE IF EXISTS model_provider;
DROP TABLE IF EXISTS connector_credential;
DROP TABLE IF EXISTS connector;
```

> **本篇表数量核对**：`00001_platform_core.sql` (6) + `00002_providers_connectors.sql` (4) = **10 张表**（2 全局 + 8 租户）。

---

## 4. 冻结契约

### 4.1 导出符号（`internal/storage/postgres`）

```go
package postgres

import (
	"context"
	"embed"

	"github.com/Karlsk/synapse-os/internal/core/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// MigrationsFS 内嵌 internal/storage/postgres/migrations/*.sql 全部版本化迁移脚本。
// 随 SA-07 ~ SA-10 依次合入 00001 ~ 00006，最终包含全量 47 张业务/全局表。
//go:embed migrations/*.sql
var MigrationsFS embed.FS

// Migrate / MigrateDown / MigrateDownOne 均直接委托给 L0 internal/infra/db 的 goose/v3 Runner。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error
func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error
func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool) error

// 本篇（SA-07）导出的 9 个 Store 构造函数（SA-10 将在 stores.go 中由 NewStores 统一装配全部 38 个 Store）：
func NewCompanyStore(pool *pgxpool.Pool) store.CompanyStore
func NewSessionStore(pool *pgxpool.Pool) store.SessionStore
func NewUserStore(pool *pgxpool.Pool) store.UserStore
func NewOrgStore(pool *pgxpool.Pool, tx store.TxManager) store.OrgStore
func NewSystemSettingStore(pool *pgxpool.Pool) store.SystemSettingStore
func NewConnectorStore(pool *pgxpool.Pool) store.ConnectorStore
func NewConnectorCredentialStore(pool *pgxpool.Pool) store.ConnectorCredentialStore
func NewModelProviderStore(pool *pgxpool.Pool) store.ModelProviderStore
func NewProviderCredentialStore(pool *pgxpool.Pool) store.ProviderCredentialStore
```

编译期接口断言（与每个实现同文件声明）：
```go
var (
	_ store.CompanyStore             = (*companyStore)(nil)
	_ store.SessionStore             = (*sessionStore)(nil)
	_ store.UserStore                = (*userStore)(nil)
	_ store.OrgStore                 = (*orgStore)(nil)
	_ store.SystemSettingStore       = (*systemSettingStore)(nil)
	_ store.ConnectorStore           = (*connectorStore)(nil)
	_ store.ConnectorCredentialStore = (*connectorCredentialStore)(nil)
	_ store.ModelProviderStore       = (*modelProviderStore)(nil)
	_ store.ProviderCredentialStore  = (*providerCredentialStore)(nil)
)
```

### 4.2 路由契约

本篇为 L1 存储实现层，**不注册任何 HTTP 路由**。
- 健康检查 `/actuator/health` 与 `/actuator/ready` 直接复用 `L0/spec_02` `internal/infra/db` 提供的 `Ping(ctx)`；
- `synapse-server migrate` 子命令（`SA-41`）调用本篇导出的 `postgres.Migrate(ctx, pool)`。

### 4.3 错误语义

本篇严格复用 `L0/spec_02` 的 `db.MapPGError(op, err)` 与 `L1/spec_05` 的 `tenant.Require(ctx)`：
- **`pgx.ErrNoRows` 归一化**：所有单行查询方法（`CompanyStore.Find`、`SessionStore.Find`、`UserStore.FindBy*`、`OrgStore.Manager`、`ConnectorStore.FindConnector`、`ConnectorCredentialStore.Find`、`ModelProviderStore.FindByID`/`FindByKey`、`ProviderCredentialStore.Find`）遇 `pgx.ErrNoRows` 统一返回 `(nil, nil)` 或 `("", false, nil)`，严禁将 `pgx.ErrNoRows` 泄漏至 L2 业务层。
- **PG 错误码映射（经 `db.MapPGError`）**：
  - `23505`（`unique_violation`）→ `apperror.Conflict(...)`（例如 `uq_app_user_username`、`uq_model_provider_key`）；
  - `22001`（超长截断）/ `22P02`（非法文本格式）→ `apperror.InvalidArg(...)`；
  - `57014`（查询取消）/ `context.Canceled` / `context.DeadlineExceeded` → 原样透传上下文错误；
  - 其他数据库错误 → 包装为内部错误（上层映为 `500 internal_error`）。
- **缺失租户上下文（Fail-Closed）**：除全局 Store（`companyStore`、`sessionStore`）外，其余 7 个租户 Store 的每个导出方法首行必须调用 `s.company(ctx)`（内部调用 `tenant.Require(ctx)`），缺失 `companyID` 时立即返回 `tenant.ErrMissingTenant`，绝不发起数据库查询。

### 4.4 SQL 契约（本篇 9 Store 写策略、冲突语义与排序对照）

| # | Store / 表 | Java 原版写策略与源码行号 | Go PG17 冻结 SQL 写契约 | 读排序与边界过滤契约 |
|---|---|---|---|---|
| 1 | `CompanyStore` (`company`) [GLOBAL] | `REPLACE INTO` (`JdbcCompanyStore.java:L81`) | `INSERT INTO company (...) VALUES (...) ON CONFLICT (id) DO UPDATE SET name=EXCLUDED.name, enabled=EXCLUDED.enabled, data_key_cipher=EXCLUDED.data_key_cipher, max_users=EXCLUDED.max_users, max_running_tasks=EXCLUDED.max_running_tasks, monthly_tokens=EXCLUDED.monthly_tokens, updated_at=EXCLUDED.updated_at` | `List`: `ORDER BY created_at ASC` |
| 2 | `SessionStore` (`user_session`) [GLOBAL] | *(V1 新增，替代 `HttpSession`)* | `Create`: `INSERT INTO user_session (...)`；`Touch`: `UPDATE user_session SET password_ok=$2, expires_at=$3, updated_at=$4 WHERE id=$1`；`Delete`: `DELETE FROM user_session WHERE id=$1`；`DeleteByUser`: `DELETE FROM user_session WHERE company_id=$1 AND user_id=$2`；`DeleteExpired`: `DELETE FROM user_session WHERE expires_at < $1`（返回 `RowsAffected()`） | `Find`: `WHERE id = $1 AND expires_at > NOW()`（过期会话视同不存在返 `nil, nil`） |
| 3 | `UserStore` (`app_user`) | **先 `UPDATE` 后 `INSERT`** (`MysqlUserStore.java:L47-L54`) | **严禁 `ON CONFLICT (company_id, username)` 覆盖他人！** 先 `UPDATE app_user SET ... WHERE company_id=$1 AND id=$2`，`RowsAffected()==0` 时再 `INSERT`；同租户重名由 `uq_app_user_username` 触发 `23505` → `apperror.Conflict`；`Delete`: `DELETE FROM app_user WHERE company_id=$1 AND id=$2` | `ListUsers`: `ORDER BY created_at ASC`；`FindByPhone`/`FindByExternalID` 空串直接返 `(nil, nil)` 不查库 |
| 4 | `OrgStore` (`user_manager`, `user_team`) | `DELETE` + `INSERT` (`AbstractJdbcOrgStore.java:L56-L87`, `MysqlOrgStore.java:L43`) | `SetManager`: `strings.TrimSpace(managerID)==""` 时 `DELETE FROM user_manager WHERE company_id=$1 AND username=$2`，否则 `INSERT ... ON CONFLICT (company_id, username) DO UPDATE SET manager=EXCLUDED.manager, updated_at=EXCLUDED.updated_at`；`SetTeams`: 在 `s.tx.WithinTx` 内先 `DELETE FROM user_team WHERE company_id=$1 AND username=$2` 再批量 `INSERT ... ON CONFLICT (company_id, username, team_path) DO NOTHING` | `Manager`/`Managers`: 忽略空串 `manager`；`Teams`: `ORDER BY team_path ASC`；`Memberships`: `ORDER BY username ASC, team_path ASC` |
| 5 | `SystemSettingStore` (`system_setting`) | `ON DUPLICATE KEY` (`MysqlSystemSettingStore.java:L32-L33`) | `Put`: `INSERT INTO system_setting (company_id, setting_key, setting_value, updated_at) VALUES ($1,$2,$3,$4) ON CONFLICT (company_id, setting_key) DO UPDATE SET setting_value=EXCLUDED.setting_value, updated_at=EXCLUDED.updated_at` | `All`: `SELECT setting_key, setting_value FROM system_setting WHERE company_id=$1 ORDER BY setting_key ASC` |
| 6 | `ConnectorStore` (`connector`) | `REPLACE INTO` (`MysqlConnectorStore.java:L41,L47`) | `Save`: `INSERT INTO connector (...) VALUES (...) ON CONFLICT (company_id, id) DO UPDATE SET name=..., description=..., kind=..., auth_method=..., enabled=..., transport=..., endpoint=..., token_env=..., updated_at=...`；`Delete`: `DELETE FROM connector WHERE company_id=$1 AND id=$2` | `ListConnectors`: `ORDER BY created_at ASC` |
| 7 | `ConnectorCredentialStore` (`connector_credential`) | `REPLACE INTO` (`MysqlConnectorCredentialStore.java:L45,L51,L57`) | `Save`: `INSERT INTO connector_credential (...) VALUES (...) ON CONFLICT (company_id, connector_id, owner) DO UPDATE SET secret_cipher=..., account_label=..., updated_at=..., refresh_cipher=..., expires_at=...`；`Delete`: `WHERE company_id=$1 AND connector_id=$2 AND owner=$3`；`DeleteAll`: `WHERE company_id=$1 AND connector_id=$2` | `List`: `WHERE company_id=$1 AND connector_id=$2 ORDER BY owner ASC` |
| 8 | `ModelProviderStore` (`model_provider`) | `ON DUPLICATE KEY` (`MysqlModelProviderStore.java:L59-L60,L72`) | `Save`: `INSERT INTO model_provider (...) VALUES (...) ON CONFLICT (company_id, id) DO UPDATE SET provider_key=..., installed=..., can_infer=..., current_version=..., latest_version=..., status=..., last_checked_at=..., models_json=..., models_error=..., models_checked_at=..., updated_at=...`（若换 `provider_key` 碰撞 `uq_model_provider_key` 返 `Conflict`）；`Delete`: `WHERE company_id=$1 AND id=$2` | `List`: `ORDER BY created_at ASC`；`models_json` 存取原样 `[]byte`（空切片按 `NULL` 落盘） |
| 9 | `ProviderCredentialStore` (`provider_credential`) | `REPLACE INTO` + 软删 (`AbstractJdbcProviderCredentialStore.java:L49-L81`, `MysqlProviderCredentialStore.java:L49`) | `Save`: `INSERT INTO provider_credential (...) VALUES (...) ON CONFLICT (company_id, id) DO UPDATE SET provider_key=..., label=..., base_url=..., key_cipher=..., bearer=..., priority=..., enabled=..., status=..., last_error=..., checked_at=..., deleted=FALSE, updated_at=..., source=..., key_hash=...`；`Delete`: **`UPDATE provider_credential SET deleted=TRUE, updated_at=$3 WHERE company_id=$1 AND id=$2 AND deleted=FALSE`** | `List`: `WHERE company_id=$1 AND provider_key=$2 AND deleted=FALSE ORDER BY priority ASC, created_at ASC`；`Find`: 必带 `AND deleted=FALSE`；`KnownKeyHashes`: **`WHERE company_id=$1 AND provider_key=$2 AND key_hash <> ''`（故意包含已软删行！）** |

---

## 5. 行为语义（含 Java 源码对照与 Go 实现伪代码）

### 5.1 复用 L0 `internal/infra/db` 的迁移与基类助手 (`migrations.go`, `helpers.go`)

```go
// internal/storage/postgres/migrations.go
package postgres

import (
	"context"
	"embed"

	"github.com/Karlsk/synapse-os/internal/infra/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var MigrationsFS embed.FS

func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	return db.Migrate(ctx, pool, MigrationsFS, "migrations")
}

func MigrateDown(ctx context.Context, pool *pgxpool.Pool) error {
	return db.MigrateDown(ctx, pool, MigrationsFS, "migrations")
}

func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool) error {
	return db.MigrateDownOne(ctx, pool, MigrationsFS, "migrations")
}
```

```go
// internal/storage/postgres/helpers.go
package postgres

import (
	"context"
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
	"github.com/Karlsk/synapse-os/internal/infra/db"
	"github.com/jackc/pgx/v5/pgxpool"
)

// baseStore 统一封装连接池访问与租户提取，严格复用 L0 internal/infra/db，零自建事务逻辑。
type baseStore struct {
	pool *pgxpool.Pool
}

// q 复用 L0 db.QuerierFromCtx：ctx 处于 db.TxManager.WithinTx 事务内时返回 pgx.Tx，否则返回 pgxpool.Pool。
func (b *baseStore) q(ctx context.Context) db.Querier {
	return db.QuerierFromCtx(ctx, b.pool)
}

// company 所有租户隔离 Store 方法的强制入口（guard_test.go AST 静态守卫检查点）。
func (b *baseStore) company(ctx context.Context) (string, error) {
	return tenant.Require(ctx)
}

// utc 统一归一化为 UTC 毫秒精度，对齐原版 DATETIME(3) 与 PG17 TIMESTAMPTZ。
func utc(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC().Truncate(time.Millisecond)
	}
	return t.UTC().Truncate(time.Millisecond)
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil || t.IsZero() {
		return nil
	}
	v := t.UTC().Truncate(time.Millisecond)
	return &v
}

// nullableJSONB 空切片返回 nil（写 SQL NULL），非空原样写入 PG17 JSONB。
func nullableJSONB(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return b
}

func joinCSV(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, ",")
}

func splitCSV(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func joinLines(items []string) string {
	if len(items) == 0 {
		return ""
	}
	return strings.Join(items, "\n")
}

func splitLines(raw string) []string {
	if raw == "" {
		return []string{}
	}
	parts := strings.Split(raw, "\n")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
```

### 5.2 关键 Store 语义与并发边界 (`store_tenant_auth.go`, `store_platform.go`)

#### A. `UserStore.Save` — 严格 `UPDATE`-then-`INSERT` 杜绝同名账号静默覆写
> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlUserStore.java:L43-L56`、`AbstractJdbcUserStore.java:L56-L103`。

```go
func (s *userStore) Save(ctx context.Context, u domain.User) (*domain.User, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	rolesCSV := joinCSV(u.Roles)
	tag, err := s.q(ctx).Exec(ctx, `
		UPDATE app_user
		   SET username = $3, display_name = $4, roles = $5, enabled = $6,
		       password_hash = $7, phone = $8, external_id = $9, source = $10,
		       updated_at = $11, must_change_password = $12
		 WHERE company_id = $1 AND id = $2`,
		cid, u.ID, u.Username, u.DisplayName, rolesCSV, u.Enabled,
		u.PasswordHash, u.Phone, u.ExternalID, string(u.Source),
		utc(u.UpdatedAt), u.MustChangePassword,
	)
	if err != nil {
		return nil, db.MapPGError("user.update", err) // 改名撞 uq_app_user_username -> 409 Conflict
	}
	if tag.RowsAffected() == 0 {
		_, err = s.q(ctx).Exec(ctx, `
			INSERT INTO app_user (
				company_id, id, username, display_name, roles, enabled,
				password_hash, phone, external_id, source, created_at, updated_at, must_change_password
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			cid, u.ID, u.Username, u.DisplayName, rolesCSV, u.Enabled,
			u.PasswordHash, u.Phone, u.ExternalID, string(u.Source),
			utc(u.CreatedAt), utc(u.UpdatedAt), u.MustChangePassword,
		)
		if err != nil {
			return nil, db.MapPGError("user.insert", err) // 23505 -> apperror.Conflict
		}
	}
	return &u, nil
}
```
- **空串短路**：`FindByPhone(ctx, "")` 与 `FindByExternalID(ctx, "")` 仍须先校验 `s.company(ctx)`（满足租户 Fail-Closed），校验通过后若参数为空串则直接返回 `(nil, nil)`，避免命中空字符串记录。

#### B. `SessionStore` — 持久化会话滑动续期、按用户批量吊销与过期清理
> **V1 新增**：彻底替代原版 JVM 内存 `HttpSession`，`Touch` 在认证中间件滑动续期与改密清除强制标记时更新。

```go
func (s *sessionStore) Find(ctx context.Context, id string) (*domain.UserSession, error) {
	if id == "" {
		return nil, nil
	}
	var out domain.UserSession
	err := s.q(ctx).QueryRow(ctx, `
		SELECT id, company_id, user_id, password_ok, expires_at, created_at, updated_at
		  FROM user_session
		 WHERE id = $1 AND expires_at > NOW()`, id,
	).Scan(&out.ID, &out.CompanyID, &out.UserID, &out.PasswordOK, &out.ExpiresAt, &out.CreatedAt, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, db.MapPGError("session.find", err)
	}
	return &out, nil
}

func (s *sessionStore) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := s.q(ctx).Exec(ctx, `DELETE FROM user_session WHERE expires_at < $1`, utc(now))
	if err != nil {
		return 0, db.MapPGError("session.delete_expired", err)
	}
	return tag.RowsAffected(), nil
}
```

#### C. `OrgStore.SetTeams` — 借助 L0 `db.TxManager` 保证成员团队全量替换原子性
> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcOrgStore.java:L81-L87`。

```go
func (s *orgStore) SetTeams(ctx context.Context, userID string, teams []string) error {
	cid, err := s.company(ctx)
	if err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		q := s.q(txCtx) // 自动取得 WithinTx 注入的 pgx.Tx / SAVEPOINT
		if _, err := q.Exec(txCtx,
			`DELETE FROM user_team WHERE company_id = $1 AND username = $2`, cid, userID); err != nil {
			return db.MapPGError("org.clear_teams", err)
		}
		for _, tp := range teams {
			if tp == "" {
				continue
			}
			if _, err := q.Exec(txCtx, `
				INSERT INTO user_team (company_id, username, team_path)
				VALUES ($1, $2, $3)
				ON CONFLICT (company_id, username, team_path) DO NOTHING`, cid, userID, tp); err != nil {
				return db.MapPGError("org.insert_team", err)
			}
		}
		return nil
	})
}
```

#### D. `ProviderCredentialStore.Delete` & `KnownKeyHashes` — 软删除保留指纹语义
> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcProviderCredentialStore.java:L62-L81`。
- `Delete(ctx, id)` 执行原子 `UPDATE provider_credential SET deleted = TRUE, updated_at = $3 WHERE company_id = $1 AND id = $2 AND deleted = FALSE`（相比 Java 原版「先 `find` 再 `REPLACE INTO`」消除 TOCTOU 窗口）；
- `Find` / `List` 必须过滤 `deleted = FALSE`；
- `KnownKeyHashes(ctx, providerKey)` **刻意不过滤 `deleted`**，仅过滤 `key_hash <> ''`，以防止已删除的宿主机导入密钥被再次自动导入。

### 5.3 静态 + 运行时多租户隔离守卫 (`guard_test.go`)

> **Java 源码对照**：`superagent-storage/src/test/java/com/superagent/storage/jdbc/TenantScopeGuardTest.java:L20-L68`。
> **V1 升级**：同时覆盖 **SQL 迁移静态校验** + **Go Store 方法级 AST 校验** + **SQL 字符串字面量 `company_id` 谓词扫描** + **无租户上下文运行时 Fail-Closed 校验**。

```go
// internal/storage/postgres/guard_test.go
package postgres

// 全局豁免表白名单（仅允许 2 张全局表）
var globalTables = map[string]bool{
	"company":      true,
	"user_session": true,
}

// 全局豁免 Store 接收者白名单（仅允许 2 个全局 Store）
var globalStoreReceivers = map[string]bool{
	"companyStore": true,
	"sessionStore": true,
}

// 1. TestMigrationTenantScopeGuard（零外部依赖，纯静态解析 migrations/*.sql）：
//    - 扫描全部 migrations/*.sql 的 `-- +goose Up` 段（截至 `-- +goose Down` 之前）；
//    - 断言 `-- +goose Up` 段绝对禁止出现不区分大小写的 `DROP TABLE`；
//    - 提取每条 `CREATE TABLE <name> ( ... );`：
//      * 校验表名无重复；
//      * 非 globalTables 的表必须满足：
//        (a) 含列定义 `company_id VARCHAR(64) NOT NULL`（归一化空白匹配）；
//        (b) 主键定义 `PRIMARY KEY (company_id, ...)` 以 company_id 为首列；
//        (c) 任何表级 `UNIQUE (...)` 约束必以 company_id 为首列且命名为 `uq_<table>_*`；
//    - 提取每条 `CREATE [UNIQUE] INDEX <idx> ON <table> (<cols>)`：
//      * 非 globalTables 的索引首列必须是 `company_id`；
//      * 索引名必须以 `idx_<table>_` 或 `uq_<table>_` 为前缀。
//    - 阶段性表数量基线断言（本篇 SA-07 断言 >= 10；SA-10 锁定精确 == 47 且租户表 == 45）。
//
// 2. TestStoreMethodTenantGuard（零外部依赖，go/parser + go/ast 解析 store_*.go）：
//    - 遍历包内所有 `store_*.go`（排除 `*_test.go`）中的所有方法声明 `func (s *<recv>) <Name>(...)`；
//    - 若 `<recv>` 为 `*xxxStore` 且不在 `globalStoreReceivers` 中，且 `<Name>` 为导出方法：
//      (a) 遍历函数体 AST，断言必存在对 `s.company(ctx)` 或 `tenant.Require(ctx)` 的调用；
//      (b) 扫描方法体内所有含 `SELECT`/`INSERT`/`UPDATE`/`DELETE` 的 SQL 字符串字面量，
//          断言每条 SQL 语句均包含 `company_id`（杜绝拿到 cid 却忘记在 WHERE/VALUES 中使用的静默漏洞！）。
//
// 3. assertTenantFailClosed(t, fn)（运行时守卫辅助，供所有 store_*_test.go 调用）：
//    - 传入不带租户的 `context.Background()` 调用租户 Store 方法，断言必返回 `errors.Is(err, tenant.ErrMissingTenant)`；
//    - 同时提供双租户并发写入相同主键 `id` 互不冲突与交叉读不可见断言模板。
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 创建 `internal/storage/postgres/migrations/00001_platform_core.sql`（6 表）与 `00002_providers_connectors.sql`（4 表），实现 `migrations.go` 与 `helpers.go`（严格复用 `L0/spec_02` `internal/infra/db`）。
- [ ] **T2**: **守卫先行（TDD）**：编写 `internal/storage/postgres/guard_test.go`（SQL 迁移守卫 + Go AST 方法级 `s.company(ctx)` & SQL `company_id` 谓词双守卫 + 运行时 `assertTenantFailClosed` 辅助），跑 `go test ./internal/storage/postgres -run Test.*Guard` 验证 10 表 DDL 合规。
- [ ] **T3**: 编写 `store_tenant_auth_test.go`（先红灯：覆盖 `CompanyStore`、`SessionStore`、`UserStore` 同名不覆写冲突、`OrgStore` 事务回滚与跨租户隔离），使用 `db.NewTestSchema(t)` 隔离测试 schema。
- [ ] **T4**: 实现 `store_tenant_auth.go`（`CompanyStore`、`SessionStore`、`UserStore`、`OrgStore` 4 个 Store），验证 `store_tenant_auth_test.go` 与 `guard_test.go` 全绿。
- [ ] **T5**: 编写 `store_platform_test.go`（先红灯：覆盖 `SystemSettingStore`、`ConnectorStore`、`ConnectorCredentialStore`、`ModelProviderStore` JSONB 往返与 `uq_model_provider_key` 冲突、`ProviderCredentialStore` 软删保留 `key_hash` 与跨租户隔离）。
- [ ] **T6**: 实现 `store_platform.go`（`SystemSettingStore`、`ConnectorStore`、`ConnectorCredentialStore`、`ModelProviderStore`、`ProviderCredentialStore` 5 个 Store），跑全包 `go test -race -cover ./internal/storage/postgres/...` 验证全绿且覆盖率 ≥ 80%。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-07` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §4：同步迁移文件命名 `00001_platform_core.sql`、`00002_providers_connectors.sql` 及 L0 `internal/infra/db` 复用关系；
- [ ] `synapse-server/README.md`：记录本地 `TEST_POSTGRES_DSN` 配置方式及「禁止修改已合入迁移脚本，只追加新编号」规范。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | SQL 迁移租户与禁删表守卫 | `internal/storage/postgres/guard_test.go` | `00001`+`00002` 解析出 10 张表；`-- +goose Up` 零 `DROP TABLE`；8 张租户表全部 `company_id VARCHAR(64) NOT NULL` 且为 PK/唯一约束/索引首列 |
| 2 | Go AST 方法级与 SQL 谓词租户守卫 | `internal/storage/postgres/guard_test.go` | 除 `companyStore`/`sessionStore` 外，7 个租户 Store 的所有导出方法 AST 均调用 `s.company(ctx)` 且其内全部 SQL 字面量均含 `company_id` |
| 3 | `00001`–`00002` `goose Up / Down / Up` 幂等 | `internal/storage/postgres/store_tenant_auth_test.go` | 在 `db.NewTestSchema(t)` 独立 schema 执行 `Migrate` → 重复 `Migrate` 幂等 → `MigrateDownOne`×2 清空 10 表 → 再次 `Migrate` 成功 |
| 4 | `CompanyStore` CRUD 与可空配额字段 | `internal/storage/postgres/store_tenant_auth_test.go` | `MaxUsers`/`MaxRunningTasks`/`MonthlyTokens` 的 `nil` vs `0` vs 正数精确往返；`List` 按 `created_at ASC` 排序 |
| 5 | `SessionStore` 过期过滤、滑动续期与按用户吊销 | `internal/storage/postgres/store_tenant_auth_test.go` | 过期会话 `Find` 返回 `(nil, nil)`；`Touch` 原子更新 `password_ok`/`expires_at`；`DeleteByUser` 仅删指定租户指定用户；`DeleteExpired` 返回精确删除行数 |
| 6 | `UserStore.Save` 严防重名覆写与部分索引查找 | `internal/storage/postgres/store_tenant_auth_test.go` | 用户 A 占用 `alice` 后，以新 UUID 保存 `alice` 返回 `apperror.CodeConflict` 且用户 A 记录完好；同租户多用户空 `phone`/`external_id` 不冲突；`FindByPhone("")` 返 `(nil, nil)` |
| 7 | `OrgStore.SetTeams` 事务原子替换与清除 | `internal/storage/postgres/store_tenant_auth_test.go` | `SetManager(u, "")` 删除汇报行；`SetTeams` 在 `db.TxManager.WithinTx` 内全量替换且去重；外层事务回滚时变更完全撤销 |
| 8 | `SystemSettingStore` & `Connector*Store` 跨租户隔离 | `internal/storage/postgres/store_platform_test.go` | 两租户使用相同 `connector.id` 与 `setting_key` 互不干扰；`ConnectorCredentialStore.DeleteAll` 仅清理本租户指定连接器全部凭证（含公司共享 `owner=""`） |
| 9 | `ModelProviderStore` JSONB 往返与唯一键冲突 | `internal/storage/postgres/store_platform_test.go` | `models_json` 非空 JSON 数组与 `nil` 精确往返；同租户不同 `id` 重复 `provider_key` 触发 `uq_model_provider_key` → `apperror.CodeConflict` |
| 10 | `ProviderCredentialStore` 软删除与 `KnownKeyHashes` 保留 | `internal/storage/postgres/store_platform_test.go` | `Delete` 后 `Find`/`List` 均不可见，但 `KnownKeyHashes` 仍返回其 `key_hash`；`List` 按 `(priority ASC, created_at ASC)` 排序；无租户 `ctx` 调用全部 7 个租户 Store 均返 `tenant.ErrMissingTenant` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race ./internal/storage/postgres -run Test.*Guard` 零外部依赖秒级全绿
- [ ] 面向真实 PostgreSQL 17 运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 全绿，覆盖率 ≥ 80%
- [ ] 逐项核对 `internal/storage/postgres` 绝不重复创建 `pgxpool.NewWithConfig`、`txManager` 或自定义 `mapPgErr`，全部复用 `L0/spec_02` `internal/infra/db`
- [ ] §2 交付物 9 项与 §3 全量 10 张表、索引、约束及 9 个 Store 编译期断言逐项核对无遗漏
- [ ] `L0/spec_02`、`L1/spec_05` 及 `internal/arch_test.go` 回归全绿，无业务包直接 import `pgx/v5` 或 `internal/storage/postgres`

---

## 10. 可提交节点

- 建议 commit 1：`feat(storage): add platform/provider PG17 migrations (00001-00002), base helpers and AST/SQL tenant guard (spec 07, SA-07)`
- 建议 commit 2：`feat(storage): implement 9 PG17 stores for tenant/auth/org/settings/connectors/providers with integration tests (spec 07, SA-07)`
