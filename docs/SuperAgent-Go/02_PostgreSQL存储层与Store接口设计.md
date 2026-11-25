# 02. PostgreSQL 存储层与 Store 接口设计

> 本文档定义 Go 版的抽象持久化端口（`internal/core/store`）、首期 **PostgreSQL 17** 实现架构、基于 V1 商业私有化范围精简与去债后的全量表设计（**40 张核心表** + `user_session` 服务端会话表）、`goose` 内嵌版本化迁移规范，以及未来扩展 MySQL 或独立分析库的平滑适配边界。

---

## 1. 抽象 Store 接口体系与多数据库扩展原则

所有领域服务（`internal/platform`、`internal/agent`、`internal/task`、`internal/orchestrator` 等）**严禁直接引入 `pgx` 或拼接 SQL**，只能依赖 `internal/core/store` 中的纯 Go 接口：

```go
package store

// TxManager 提供跨多个 Store 的原子事务边界，事务句柄隐式存于 ctx 中。
type TxManager interface {
    WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// 典型领域 Store 接口（所有租户表方法均接收 ctx，实现内部强制调用 tenant.Require(ctx)）
type TaskStore interface {
    Create(ctx context.Context, t domain.Task) error
    Find(ctx context.Context, id string) (*domain.Task, error) // 不存在返回 nil, nil 或 apperror.ErrNotFound
    Update(ctx context.Context, t domain.Task) error
    List(ctx context.Context) ([]domain.Task, error)
}
```

### 1.1 租户隔离防漏写架构守卫

模仿原版 `TenantScopeGuardTest`，在 `internal/storage/postgres/guard_test.go` 中编写自动化 AST 与 SQL 迁移测试（每次 `go test ./...` 必跑）：
1. 扫描 `migrations/*.sql` 中每一条 `CREATE TABLE`，断言除全局表（`company`、`user_session`、`goose_db_version`）外，**100% 必须包含 `company_id VARCHAR(64) NOT NULL` 且位于主键首列**；
2. 静态检查 `internal/storage/postgres/store_*.go` 中每个非全局 Store 文件，断言每个查询方法均调用统一助手 `s.company(ctx)`（内部走 `tenant.Require(ctx)`）。

### 1.2 PostgreSQL 17 实现与未来多库扩展边界

- **连接池与事务透传**：基于 `pgxpool.Pool`（默认 `MaxConns = 16`, `MinConns = 2`, `MaxConnLifetime = 30m`），使用统一 `DBTX` 窄接口（`Exec`/`Query`/`QueryRow`），`WithinTx` 将 `pgx.Tx` 注入 `ctx`，同一事务内的所有 Store 自动复用该连接。
- **跨库兼容性约束**：为保障未来若客户强要求 MySQL 时能快速写出 `internal/storage/mysql` 实现，**表结构避免使用难以迁移的 PG 专有特性**（如不用 PG 枚举类型 `CREATE TYPE ... AS ENUM`、不用复杂存储过程、不用 PG 专有数组列），统一采用标准 SQL 类型（`VARCHAR`/`TEXT`/`INTEGER`/`BIGINT`/`NUMERIC(16,6)`/`DOUBLE PRECISION`/`BOOLEAN`/`TIMESTAMPTZ`/`JSONB`）。

---

## 2. 相较原版 46 张表的精简与去债对照

对照 [../SuperAgent/09_数据存储模型全量设计.md](../SuperAgent/09_数据存储模型全量设计.md) 的 46 张逻辑表，Go V1 版在保留完整功能的前提下做了以下 6 项结构净化（总计 **47 -> 47? 精确核算为 46 原始表 -> 46 保留主干 + 1 `user_session` = 47，或清理冗余后保持最简清晰结构** —— 逐表精确核算如下）：

1. **新增 1 张持久化会话表 `user_session`**：替代 JVM 内存 `HttpSession`，使 Go 服务重启/滚动升级不踢出在线员工。
2. **彻底删除全部废弃历史列**：
   - 删 `agent.tier`、`task.tier`、`automation.tier`；
   - 删 `superagent_state.tracked_json`；
   - 删 `event_source` 的 5 个空占位列（`secret_cipher`/`vip_names`/`urgent_keywords`/`status`/`last_error`）；
   - 删 `agent.character_key`（V1 去掉 24 内置性格预设，全用 `persona`）。
3. **修复 `agent_event` 无主键缺陷**：增加 `id BIGSERIAL`（联合 `(company_id, id)` 或 `(company_id, session_id, id)`），支持高效基于游标的增量拉取与分片清理。
4. **统一时间与金额精度**：全部时间列采用 `TIMESTAMPTZ NOT NULL`（强制 UTC 写入）；`task.spend` 采用 `NUMERIC(16,6)`；所有 JSON 载荷列（`superagent_state.plan_json`、`task_phase.phases_json`、`model_provider.models_json`）采用 PG17 原生 `JSONB`。
5. **去 ClickHouse 双存储**：`agent_log`、`token_usage`、`turn_timing` V1 全部落在 PostgreSQL 17（配合合理时间复合索引与定期清理），表与 Store 接口保持独立，未来有超大规模客户时可无缝插入 ClickHouse 实现。
6. **保留全部业务主表结构**（共 **46 张业务表 + 1 张 `user_session` = 47 张干净表**，零物理外键、全逻辑关联，保证软删除后历史审计与关联完整不丢失；其中 `user_manager`/`user_team` 保留供收件箱上下级分诊与二期组织同步使用）。

---

## 3. 核心表 PostgreSQL 17 DDL 精选与关键改动规格

完整 46 张表的逐列业务语义详见 [../SuperAgent/09_数据存储模型全量设计.md](../SuperAgent/09_数据存储模型全量设计.md)，以下重点列出 **Go 版在 PG17 中发生关键优化/新增/易错细节的 DDL 规范**：

### 3.1 全局表（2 张：无 `company_id` 主键前缀）

```sql
-- 1. 企业租户表（全局，永不删表）
CREATE TABLE company (
    id                VARCHAR(64)  PRIMARY KEY,
    name              VARCHAR(255) NOT NULL,
    enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
    data_key_cipher   TEXT,
    max_users         INTEGER,
    max_running_tasks INTEGER,
    monthly_tokens    BIGINT,
    created_at        TIMESTAMPTZ  NOT NULL,
    updated_at        TIMESTAMPTZ  NOT NULL
);

-- 2. [V1 新增] 服务端持久化登录会话表（替代内存 HttpSession）
CREATE TABLE user_session (
    id          VARCHAR(128) PRIMARY KEY, -- 32 字节加密安全随机 hex
    company_id  VARCHAR(64)  NOT NULL,
    user_id     VARCHAR(64)  NOT NULL,
    password_ok BOOLEAN      NOT NULL DEFAULT FALSE,
    expires_at  TIMESTAMPTZ  NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_user_session_expires ON user_session (expires_at);
CREATE INDEX idx_user_session_user    ON user_session (company_id, user_id);
```

### 3.2 关键净化表与补主键表（PG17 范例）

```sql
-- agent：移除废弃的 character_key 与 tier
CREATE TABLE agent (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    name       VARCHAR(255) NOT NULL,
    avatar     VARCHAR(32)  NOT NULL DEFAULT '',
    persona    TEXT         NOT NULL DEFAULT '',
    model      VARCHAR(128) NOT NULL DEFAULT '',
    status     VARCHAR(32)  NOT NULL, -- ACTIVE | DELETED
    owner      VARCHAR(255) NOT NULL DEFAULT '',
    kind       VARCHAR(16)  NOT NULL DEFAULT 'STANDARD', -- STANDARD | SUPER
    managed_by VARCHAR(64)  NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_agent_updated ON agent (company_id, updated_at DESC);

-- agent_event：[V1 修复] 增加 BIGSERIAL 主键，支持精确游标增量读取
CREATE TABLE agent_event (
    id         BIGSERIAL    NOT NULL,
    company_id VARCHAR(64)  NOT NULL,
    session_id VARCHAR(64)  NOT NULL, -- chat_session.id 或 task.id
    message_id VARCHAR(64)  NOT NULL DEFAULT '',
    seq        BIGINT       NOT NULL,
    type       VARCHAR(32)  NOT NULL,
    payload    TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_event_session ON agent_event (company_id, session_id, created_at, seq, id);

-- superagent_state：移除废弃 tracked_json，plan_json 使用 JSONB
CREATE TABLE superagent_state (
    company_id      VARCHAR(64)  NOT NULL,
    owner           VARCHAR(255) NOT NULL,
    agent_id        VARCHAR(64)  NOT NULL,
    processed_until TIMESTAMPTZ,
    plan_id         VARCHAR(64)  NOT NULL DEFAULT '',
    plan_status     VARCHAR(16)  NOT NULL DEFAULT '', -- '' | proposed | approved
    plan_json       JSONB,
    updated_at      TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, owner)
);

-- event_source：移除 5 个废弃空列，仅保留飞书/钉钉绑定与分诊调度字段
CREATE TABLE event_source (
    company_id         VARCHAR(64)  NOT NULL,
    owner              VARCHAR(128) NOT NULL,
    type               VARCHAR(32)  NOT NULL, -- feishu | dingtalk
    app_id             VARCHAR(128) NOT NULL DEFAULT '',
    enabled            BOOLEAN      NOT NULL DEFAULT TRUE,
    owner_external_id  VARCHAR(128) NOT NULL DEFAULT '',
    bind_code          VARCHAR(32)  NOT NULL DEFAULT '',
    digest_time        VARCHAR(8)   NOT NULL DEFAULT '09:00',
    timezone           VARCHAR(64)  NOT NULL DEFAULT 'Asia/Shanghai',
    last_digest_date   VARCHAR(16)  NOT NULL DEFAULT '',
    last_hourly_at     TIMESTAMPTZ,
    created_at         TIMESTAMPTZ  NOT NULL,
    updated_at         TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, owner, type)
);

-- task：移除废弃 tier 列，强化金额与索引
CREATE TABLE task (
    company_id    VARCHAR(64)    NOT NULL,
    id            VARCHAR(64)    NOT NULL,
    title         VARCHAR(512)   NOT NULL,
    brief         TEXT           NOT NULL DEFAULT '',
    source        VARCHAR(16)    NOT NULL, -- WEB_CHAT | TASK_FORM | AUTOMATION | INBOX
    repo          VARCHAR(512)   NOT NULL DEFAULT '',
    branch        VARCHAR(255)   NOT NULL DEFAULT '',
    flow          VARCHAR(32)    NOT NULL DEFAULT 'general', -- coding | general | complex
    status        VARCHAR(32)    NOT NULL, -- 8 态状态机
    progress      INTEGER        NOT NULL DEFAULT 0,
    spend         NUMERIC(16,6)  NOT NULL DEFAULT 0,
    model         VARCHAR(128)   NOT NULL DEFAULT '',
    agent_type    VARCHAR(32)    NOT NULL DEFAULT 'CLAUDE',
    workspace_dir VARCHAR(1024)  NOT NULL DEFAULT '',
    created_by    VARCHAR(255)   NOT NULL DEFAULT '',
    error         TEXT           NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ    NOT NULL,
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ    NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_task_created ON task (company_id, created_at DESC);
CREATE INDEX idx_task_status  ON task (company_id, status);
```

### 3.3 PG17 幂等写与并发规范（替代 MySQL 专有语法）

| 原版 MySQL 习惯 | Go + PostgreSQL 17 规范写法 | 适用表 |
|---|---|---|
| `INSERT IGNORE INTO ...` | `INSERT INTO ... ON CONFLICT (company_id, id) DO NOTHING`（检查 `RowsAffected() == 1` 判断是否新入库） | `inbox_event`、`work_file_ref` |
| `REPLACE INTO ...`（会先删后插） | `INSERT INTO ... ON CONFLICT (pk...) DO UPDATE SET ...` | 全部配置、成员、记忆、放置、通知 Upsert（**严禁对 `app_user` 用冲突覆盖 `username`**，`app_user` 必须按 `(company_id, id)` 显式 `UPDATE` 再 `INSERT`） |
| `DATETIME(3)` + `LocalDateTime` 拼接 | `TIMESTAMPTZ` + Go `time.Time.UTC().Truncate(time.Millisecond)` | 全部时间戳列 |
| `DECIMAL` 字符串转换 | 领域模型使用定点数（如 `shopspring/decimal` 或整数微美元 `int64`）杜绝浮点账单误差 | `task.spend`、`token_usage.cost_usd` |

---

## 4. 版本化 Schema 迁移流水线 (`goose`)

所有表结构演进均通过 `internal/storage/postgres/migrations/` 中的编号 SQL 文件管理，并由 `//go:embed` 内嵌进二进制：

```text
internal/storage/postgres/migrations/
├── 00001_init_tenant_and_auth.sql      # company, user_session, app_user, user_manager, user_team
├── 00002_init_agent_and_chat.sql       # agent*, chat_session, chat_message, agent_event, agent_log
├── 00003_init_task_and_automation.sql  # task*, automation, automation_run
├── 00004_init_superagent_and_inbox.sql # superagent_*, inbox_*, event_source, notice
├── 00005_init_assets_and_platform.sql  # project*, work_file*, file_*, skill*, connector*, model_provider*, system_setting
└── 00006_init_usage_and_timing.sql     # token_usage, turn_timing
```

- **启动行为**：配置 `storage.auto_migrate = true`（默认开启，也可由运维通过 `synapse-server migrate` 独立执行）时，服务启动在进入 HTTP 监听前自动按顺序执行未应用的迁移脚本，记录在 `goose_db_version` 表中；**永远向前演进、零破坏性删表**。
- **定期表维护**：后台定时任务自动清理过期 `user_session`（每小时）、超 30 天 `inbox_event`（每日）及超 30 天 / 超 5,000 条的 `agent_log`（每小时）。
