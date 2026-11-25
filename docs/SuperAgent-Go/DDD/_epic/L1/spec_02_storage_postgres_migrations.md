# spec 02 | L1 Storage — PostgreSQL 17 全量 47 表迁移、38 个 Store 实现与租户隔离守卫

- **状态**：`冻结待实施`
- **篇号顺序**：02（紧随 `SA-01` 的核心底座实现层）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-02`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§2
  - [SuperAgent/09_数据存储模型全量设计.md](../../../SuperAgent/09_数据存储模型全量设计.md)
- **前置依赖**：[spec_01_core_tenant_crypto_egress.md](./spec_01_core_tenant_crypto_egress.md)（`SA-01`，依赖 `internal/core/{domain,store,tenant,apperror}`）
- **下游消费者**：`SA-04`（平台治理）、`SA-05`（供应商/连接器）、`SA-06`（文件/项目/技能）、`SA-07`（成本路由与用量）、`SA-08`（办公文档）、`SA-09`（Agent/对话/记忆）、`SA-10`（任务/自动化）、`SA-11`（IM 收件箱）、`SA-12`（SuperAgent 编排）、`SA-14`（Server 装配与 `migrate` CLI）

---

## 1. 背景与范围

本篇交付 `synapse-server` 的 **L1 PostgreSQL 17 存储实现层 (`internal/storage/postgres`)**，对应 Java 原版 `superagent-storage` 模块（37 个 `AbstractJdbc*`/`Mysql*`/`Jdbc*` Store + `TenantSchema` + `TenantScopeGuardTest`），并在此基础上完成商业私有化 V1 的数据库架构现代化改造：
1. **单库 PostgreSQL 17 + `pgx/v5` (`pgxpool`)**：彻底移除原版 ClickHouse 双存储分支（`ClickHouse*Store`）与 `FINAL` 方言拼接；所有 47 张表（含 `agent_log`/`token_usage`/`turn_timing`）在 V1 统一落 PG17，同时通过 `internal/core/store` 接口隔离保留二期平滑拆分分析库或扩展 `internal/storage/mysql` 的能力。
2. **版本化 `goose/v3` 内嵌迁移（永不删表）**：彻底摒弃原版 `TenantSchema.java:L30-L51` 的「缺 `company_id` 即 `DROP TABLE`」与各 Store 构造器内零散 `information_schema` `ALTER TABLE` 写法，改为 6 个通过 `//go:embed migrations/*.sql` 编译进二进制的版本化迁移文件，支持服务启动自动迁移及独立 `synapse-server migrate` 命令。
3. **47 张干净表结构净化与缺陷修复**：
   - 新增持久化登录会话表 **`user_session`**（解决 JVM 内存 `HttpSession` 重启踢人问题）；
   - 为 **`agent_event`** 补齐物理主键 `id BIGSERIAL`（联合 `PRIMARY KEY (company_id, id)`），支持精确游标增量拉取；
   - 彻底移除废弃列：`agent.character_key`、`agent.tier`、`task.tier`、`automation.tier`、`superagent_state.tracked_json`、`event_source` 的 5 个废弃空列（`secret_cipher`/`vip_names`/`urgent_keywords`/`status`/`last_error`）与历史迁移列 `secretary_agent_id`；
   - 全表时间列统一 `TIMESTAMPTZ`（强制 UTC 写入）、计费金额统一 `NUMERIC(16,6)`、JSON 载荷列（`superagent_state.plan_json`、`task_phase.phases_json`、`model_provider.models_json`、`turn_timing.tools`）采用 PG17 原生 `JSONB`。
4. **自动化 AST + SQL 租户隔离守卫 (`guard_test.go`)**：升级复刻 Java `TenantScopeGuardTest.java`，在每次 `go test ./...` 中双维度校验迁移 SQL 与 Go AST，从工程根源杜绝跨租户泄漏。

**本篇边界**：本篇只负责 `pgxpool` 连接管理、`TxManager`、`goose` 迁移器、38 个 Store 实现与存储层测试；不包含业务编排逻辑（如配额校验、密码哈希、权限过滤，留给 `SA-04`~`SA-12`）及定时清理后台循环注册（清理方法在本篇实现，定时调度器在 `SA-14` 接线）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `pgxpool` 连接池配置、UTC 会话参数、`goose` 内嵌迁移与健康检查 | `internal/storage/postgres/db.go` | 新增 |
| 2 | `DBTX` 窄接口、`TxManager`（`WithinTx` 嵌套 savepoint 支持）、错误映射与公共读写助手 | `internal/storage/postgres/tx.go`、`errors.go`、`helpers.go` | 新增 |
| 3 | 6 个内嵌 `goose` PG17 迁移脚本（全量 **47 张表** DDL + 索引） | `internal/storage/postgres/migrations/00001_*.sql` ~ `00006_*.sql` | 新增 |
| 4 | 租户、会话、用户、组织 4 个 Store 实现 | `internal/storage/postgres/store_tenant_auth.go` | 新增 |
| 5 | Agent、成员、邀请、父子、记忆、日志、对话/消息/事件 7 个 Store 实现 | `internal/storage/postgres/store_agent_chat.go` | 新增 |
| 6 | 任务、阶段、运行态、归属、图谱、标签、模型选型、自动化 8 个 Store 实现 | `internal/storage/postgres/store_task_automation.go` | 新增 |
| 7 | SuperAgent 状态/工作台账、收件箱事件/待办/事件源、通知 6 个 Store 实现 | `internal/storage/postgres/store_superagent_inbox.go` | 新增 |
| 8 | 项目/项目项、工作文件/目录、技能/删除确认 6 个 Store 实现 | `internal/storage/postgres/store_assets.go` | 新增 |
| 9 | 连接器/凭证、模型供应商/密钥池、系统设置 5 个 Store 实现 | `internal/storage/postgres/store_platform.go` | 新增 |
| 10 | Token 用量与 7 阶段耗时 2 个 Store 实现 + `Stores` 聚合工厂 | `internal/storage/postgres/store_analytics.go`、`stores.go` | 新增 |
| 11 | AST + SQL 自动化多租户隔离与禁删表守卫测试 | `internal/storage/postgres/guard_test.go` | 新增 |
| 12 | PostgreSQL 17 集成与并发单测套件（覆盖全 38 Store + 事务回滚 + 跨租户隔离） | `internal/storage/postgres/store_*_test.go` | 新增 |

---

## 3. 数据库设计（全量 47 表 PostgreSQL 17 DDL · 6 个 `goose` 迁移文件）

> **统一规范**：
> - 零物理外键（保持软删除历史审计完整）；
> - 全部租户表（45 张）`company_id VARCHAR(64) NOT NULL` 必为主键首列、二级索引首列；
> - 全部索引名带表名前缀保证 PG 全局唯一（`idx_<table>_<purpose>` / `uq_<table>_<purpose>`）；
> - 仅使用标准 SQL + `TIMESTAMPTZ` + `JSONB` + `NUMERIC(16,6)` + `BOOLEAN`，不使用 PG 枚举类型或存储过程，保证未来跨库移植性；
> - **严格向前演进**：所有 `-- +goose Up` 脚本绝不含 `DROP TABLE`。

### 3.1 `migrations/00001_init_tenant_and_auth.sql`（5 张表：2 全局 + 3 租户）

```sql
-- +goose Up

-- 1. [GLOBAL] 企业租户表（Java: JdbcCompanyStore.java:L20-L32）
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

-- 2. [GLOBAL][V1 新增] 服务端持久化登录会话表（替代 JVM 内存 HttpSession）
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

-- +goose Down
DROP TABLE IF EXISTS user_team;
DROP TABLE IF EXISTS user_manager;
DROP TABLE IF EXISTS app_user;
DROP TABLE IF EXISTS user_session;
DROP TABLE IF EXISTS company;
```

### 3.2 `migrations/00002_init_agent_and_chat.sql`（9 张表：全租户隔离）

```sql
-- +goose Up

-- 6. [TENANT] 智能体表（Java: MysqlAgentStore.java:L16-L32；V1 移除 character_key 与 tier）
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

-- 7. [TENANT] Agent 协作者表（Java: MysqlAgentMemberStore.java:L11-L21）
CREATE TABLE agent_member (
    company_id VARCHAR(64)  NOT NULL,
    agent_id   VARCHAR(64)  NOT NULL,
    username   VARCHAR(128) NOT NULL,
    added_by   VARCHAR(128) NOT NULL DEFAULT '',
    active     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, agent_id, username)
);
CREATE INDEX idx_agent_member_user ON agent_member (company_id, username) WHERE active = TRUE;

-- 8. [TENANT] Agent 协作邀请确认卡表（Java: MysqlAgentInviteStore.java:L11-L24）
CREATE TABLE agent_invite (
    company_id VARCHAR(64)   NOT NULL,
    id         VARCHAR(64)   NOT NULL,
    agent_id   VARCHAR(64)   NOT NULL,
    owner      VARCHAR(190)  NOT NULL,
    usernames  VARCHAR(2048) NOT NULL, -- 逗号分隔
    session_id VARCHAR(64)   NOT NULL DEFAULT '',
    message_id VARCHAR(64)   NOT NULL DEFAULT '',
    status     VARCHAR(16)   NOT NULL, -- PENDING | CONFIRMED | CANCELLED
    created_at TIMESTAMPTZ   NOT NULL,
    updated_at TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_agent_invite_agent ON agent_invite (company_id, agent_id, created_at DESC);

-- 9. [TENANT] Agent 上下级汇报树（Java: JdbcAgentParentStore.java:L16-L22）
CREATE TABLE agent_parent (
    company_id VARCHAR(64) NOT NULL,
    agent_id   VARCHAR(64) NOT NULL,
    parent_id  VARCHAR(64) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, agent_id)
);

-- 10. [TENANT] Agent 长期记忆与会话健康状态表（Java: MysqlAgentMemoryStateStore.java:L13-L23）
CREATE TABLE agent_memory (
    company_id       VARCHAR(64)  NOT NULL,
    agent_id         VARCHAR(64)  NOT NULL,
    owner            VARCHAR(255) NOT NULL DEFAULT '',
    summarized_until TIMESTAMPTZ,
    last_model       VARCHAR(128) NOT NULL DEFAULT '',
    session_failures INTEGER      NOT NULL DEFAULT 0,
    memory           TEXT         NOT NULL DEFAULT '',
    updated_at       TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, agent_id)
);

-- 11. [TENANT] Agent 运行日志表（Java: MysqlAgentLogStore.java:L12-L24；V1 统一落 PG17）
CREATE TABLE agent_log (
    company_id VARCHAR(64)   NOT NULL,
    id         VARCHAR(64)   NOT NULL,
    agent_id   VARCHAR(64)   NOT NULL,
    owner      VARCHAR(255)  NOT NULL DEFAULT '',
    at_time    TIMESTAMPTZ   NOT NULL,
    level      VARCHAR(8)    NOT NULL, -- info | warn | error
    category   VARCHAR(16)   NOT NULL, -- session | turn | memory | superagent | settings
    summary    VARCHAR(1024) NOT NULL,
    detail     TEXT          NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_agent_log_agent_at ON agent_log (company_id, agent_id, at_time DESC);
CREATE INDEX idx_agent_log_at       ON agent_log (company_id, at_time);

-- 12. [TENANT] 对话会话表（Java: MysqlChatStore.java:L18-L34）
CREATE TABLE chat_session (
    company_id       VARCHAR(64)   NOT NULL,
    id               VARCHAR(64)   NOT NULL,
    title            VARCHAR(255)  NOT NULL DEFAULT '',
    agent_type       VARCHAR(32)   NOT NULL,
    agent_session_id VARCHAR(64)   NOT NULL DEFAULT '',
    workspace_dir    VARCHAR(1024) NOT NULL DEFAULT '',
    agent_id         VARCHAR(64)   NOT NULL DEFAULT '',
    status           VARCHAR(32)   NOT NULL, -- ACTIVE | CLOSED | DELETED
    created_at       TIMESTAMPTZ   NOT NULL,
    updated_at       TIMESTAMPTZ   NOT NULL,
    model            VARCHAR(128)  NOT NULL DEFAULT '',
    reason           VARCHAR(32)   NOT NULL DEFAULT '',
    cli_restarts     INTEGER       NOT NULL DEFAULT 0,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_chat_session_updated ON chat_session (company_id, updated_at DESC);
CREATE INDEX idx_chat_session_agent   ON chat_session (company_id, agent_id, updated_at DESC) WHERE agent_id <> '';

-- 13. [TENANT] 对话与任务消息表（Java: MysqlChatStore.java:L37-L46 + ALTER author）
CREATE TABLE chat_message (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    session_id VARCHAR(64)  NOT NULL, -- chat_session.id 或 task.id
    role       VARCHAR(16)  NOT NULL, -- USER | ASSISTANT | SYSTEM
    content    TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    author     VARCHAR(128) NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_chat_message_session ON chat_message (company_id, session_id, created_at);

-- 14. [TENANT][V1 修复] 执行流事件表（Java: MysqlChatStore.java:L49-L58；补齐 BIGSERIAL 主键）
CREATE TABLE agent_event (
    id         BIGSERIAL   NOT NULL,
    company_id VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL, -- chat_session.id 或 task.id
    message_id VARCHAR(64) NOT NULL DEFAULT '',
    seq        BIGINT      NOT NULL,
    type       VARCHAR(32) NOT NULL,
    payload    TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_agent_event_session ON agent_event (company_id, session_id, created_at, seq, id);

-- +goose Down
DROP TABLE IF EXISTS agent_event;
DROP TABLE IF EXISTS chat_message;
DROP TABLE IF EXISTS chat_session;
DROP TABLE IF EXISTS agent_log;
DROP TABLE IF EXISTS agent_memory;
DROP TABLE IF EXISTS agent_parent;
DROP TABLE IF EXISTS agent_invite;
DROP TABLE IF EXISTS agent_member;
DROP TABLE IF EXISTS agent;
```

### 3.3 `migrations/00003_init_task_and_automation.sql`（9 张表：全租户隔离）

```sql
-- +goose Up

-- 15. [TENANT] 异步任务主表（Java: MysqlTaskStore.java:L13-L36；V1 移除 tier，保留 NUMERIC(16,6)）
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

-- 16. [TENANT] 任务结构化阶段表（Java: MysqlTaskPhaseStore.java:L12-L18；PG17 原生 JSONB）
CREATE TABLE task_phase (
    company_id  VARCHAR(64) NOT NULL,
    task_id     VARCHAR(64) NOT NULL,
    phases_json JSONB       NOT NULL DEFAULT '[]'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 17. [TENANT] 任务崩溃恢复运行态表（Java: MysqlTaskRunStateStore.java:L13-L21）
CREATE TABLE task_run_state (
    company_id       VARCHAR(64) NOT NULL,
    task_id          VARCHAR(64) NOT NULL,
    agent_session_id VARCHAR(64) NOT NULL DEFAULT '',
    failure_count    INTEGER     NOT NULL DEFAULT 0,
    attempt_count    INTEGER     NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 18. [TENANT] 任务来源 Agent 关联表（Java: MysqlTaskAgentStore.java:L12-L21）
CREATE TABLE task_agent (
    company_id VARCHAR(64) NOT NULL,
    task_id    VARCHAR(64) NOT NULL,
    agent_id   VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL DEFAULT '',
    message_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);
CREATE INDEX idx_task_agent_agent ON task_agent (company_id, agent_id, created_at DESC);

-- 19. [TENANT] 任务子任务与追问接续图谱表（Java: JdbcTaskLinkStore.java:L16-L25）
CREATE TABLE task_link (
    company_id     VARCHAR(64) NOT NULL,
    task_id        VARCHAR(64) NOT NULL,
    parent_id      VARCHAR(64) NOT NULL DEFAULT '',
    continues_from VARCHAR(64) NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);
CREATE INDEX idx_task_link_parent ON task_link (company_id, parent_id) WHERE parent_id <> '';
CREATE INDEX idx_task_link_from   ON task_link (company_id, continues_from) WHERE continues_from <> '';

-- 20. [TENANT] 任务主题标签表（Java: JdbcTaskTagStore.java:L17-L23；换行分隔）
CREATE TABLE task_tag (
    company_id VARCHAR(64) NOT NULL,
    task_id    VARCHAR(64) NOT NULL,
    tags       TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 21. [TENANT] 任务模型路由决策审计表（Java: JdbcTaskModelChoiceStore.java:L15-L29）
CREATE TABLE task_model_choice (
    company_id VARCHAR(64)  NOT NULL,
    task_id    VARCHAR(64)  NOT NULL,
    seq        INTEGER      NOT NULL,
    decided_by VARCHAR(16)  NOT NULL, -- judge | user | fallback | escalation | default | fixed
    difficulty VARCHAR(16)  NOT NULL DEFAULT '',
    tier       VARCHAR(16)  NOT NULL DEFAULT '',
    model      VARCHAR(128) NOT NULL DEFAULT '',
    cli        VARCHAR(32)  NOT NULL DEFAULT '',
    judge      VARCHAR(128) NOT NULL DEFAULT '',
    candidates TEXT         NOT NULL DEFAULT '',
    reasoning  TEXT         NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, task_id, seq)
);

-- 22. [TENANT] 定时/Webhook 自动化定义表（Java: MysqlAutomationStore.java:L13-L35；V1 移除 tier）
CREATE TABLE automation (
    company_id     VARCHAR(64)  NOT NULL,
    id             VARCHAR(64)  NOT NULL,
    name           VARCHAR(255) NOT NULL,
    prompt         TEXT         NOT NULL,
    type           VARCHAR(16)  NOT NULL, -- RECURRING | ONCE | API
    cron           VARCHAR(128) NOT NULL DEFAULT '',
    run_at         TIMESTAMPTZ,
    timezone       VARCHAR(64)  NOT NULL DEFAULT 'Asia/Shanghai',
    model          VARCHAR(128) NOT NULL DEFAULT '',
    overlap_policy VARCHAR(16)  NOT NULL DEFAULT 'SKIP', -- SKIP | PARALLEL
    enabled        BOOLEAN      NOT NULL DEFAULT TRUE,
    source         VARCHAR(16)  NOT NULL DEFAULT 'manual', -- manual | chat
    owner          VARCHAR(255) NOT NULL DEFAULT '',
    api_token_hash VARCHAR(128) NOT NULL DEFAULT '',
    next_run_at    TIMESTAMPTZ,
    last_run_at    TIMESTAMPTZ,
    last_task_id   VARCHAR(64)  NOT NULL DEFAULT '',
    created_at     TIMESTAMPTZ  NOT NULL,
    updated_at     TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_automation_next ON automation (company_id, next_run_at) WHERE enabled = TRUE AND next_run_at IS NOT NULL;

-- 23. [TENANT] 自动化触发历史表（Java: MysqlAutomationStore.java:L38-L48）
CREATE TABLE automation_run (
    company_id     VARCHAR(64)  NOT NULL,
    id             VARCHAR(64)  NOT NULL,
    automation_id  VARCHAR(64)  NOT NULL,
    trigger_kind   VARCHAR(16)  NOT NULL, -- schedule | api | manual
    task_id        VARCHAR(64)  NOT NULL DEFAULT '',
    skipped_reason VARCHAR(512) NOT NULL DEFAULT '',
    fired_at       TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_automation_run_auto ON automation_run (company_id, automation_id, fired_at DESC);
CREATE INDEX idx_automation_run_task ON automation_run (company_id, task_id) WHERE task_id <> '';

-- +goose Down
DROP TABLE IF EXISTS automation_run;
DROP TABLE IF EXISTS automation;
DROP TABLE IF EXISTS task_model_choice;
DROP TABLE IF EXISTS task_tag;
DROP TABLE IF EXISTS task_link;
DROP TABLE IF EXISTS task_agent;
DROP TABLE IF EXISTS task_run_state;
DROP TABLE IF EXISTS task_phase;
DROP TABLE IF EXISTS task;
```

### 3.4 `migrations/00004_init_superagent_and_inbox.sql`（7 张表：全租户隔离）

```sql
-- +goose Up

-- 24. [TENANT] 个人 SuperAgent 状态与计划表（Java: MysqlSuperAgentStateStore.java:L13-L24；V1 移除 tracked_json，plan_json 升 JSONB）
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

-- 25. [TENANT] SuperAgent 委派工作台账表（Java: MysqlSuperAgentWorkStore.java:L13-L29）
CREATE TABLE superagent_work (
    company_id  VARCHAR(64)  NOT NULL,
    task_id     VARCHAR(64)  NOT NULL,
    owner       VARCHAR(255) NOT NULL,
    plan_id     VARCHAR(64)  NOT NULL DEFAULT '',
    title       VARCHAR(512) NOT NULL,
    member_id   VARCHAR(64)  NOT NULL DEFAULT '',
    member_name VARCHAR(255) NOT NULL DEFAULT '',
    status      VARCHAR(32)  NOT NULL, -- running | needs_confirmation | done | error | stopped | deleted
    summary     TEXT         NOT NULL DEFAULT '',
    assigned_at TIMESTAMPTZ  NOT NULL,
    finished_at TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ  NOT NULL,
    message_id  VARCHAR(64)  NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, task_id)
);
CREATE INDEX idx_superagent_work_owner ON superagent_work (company_id, owner, assigned_at DESC);

-- 26. [TENANT] SuperAgent 工作状态时间线表（Java: MysqlSuperAgentWorkStore.java:L32-L42）
CREATE TABLE superagent_work_event (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    task_id    VARCHAR(64)  NOT NULL,
    owner      VARCHAR(255) NOT NULL,
    kind       VARCHAR(32)  NOT NULL,
    detail     TEXT         NOT NULL DEFAULT '',
    at_time    TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_superagent_work_event_task ON superagent_work_event (company_id, task_id, at_time);

-- 27. [TENANT] IM 收件箱入站消息表（Java: MysqlInboxStore.java:L16-L41）
CREATE TABLE inbox_event (
    company_id    VARCHAR(64)  NOT NULL,
    id            VARCHAR(64)  NOT NULL, -- source + external_id 确定性去重 ID
    owner         VARCHAR(128) NOT NULL,
    source        VARCHAR(32)  NOT NULL, -- feishu | dingtalk
    external_id   VARCHAR(255) NOT NULL,
    chat_id       VARCHAR(128) NOT NULL DEFAULT '',
    chat_name     VARCHAR(255) NOT NULL DEFAULT '',
    chat_type     VARCHAR(16)  NOT NULL DEFAULT '', -- p2p | group
    thread_id     VARCHAR(128) NOT NULL DEFAULT '',
    sender_id     VARCHAR(128) NOT NULL DEFAULT '',
    sender_name   VARCHAR(255) NOT NULL DEFAULT '',
    text          TEXT         NOT NULL DEFAULT '',
    mentions_me   BOOLEAN      NOT NULL DEFAULT FALSE,
    raw           TEXT         NOT NULL DEFAULT '',
    priority      VARCHAR(16)  NOT NULL, -- P0 | P1 | P2 | IGNORE
    triage_reason VARCHAR(255) NOT NULL DEFAULT '',
    processed     BOOLEAN      NOT NULL DEFAULT FALSE,
    handling      VARCHAR(16)  NOT NULL DEFAULT '',
    occurred_at   TIMESTAMPTZ,
    received_at   TIMESTAMPTZ  NOT NULL,
    updated_at    TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_inbox_event_pending  ON inbox_event (company_id, owner, priority, processed, received_at);
CREATE INDEX idx_inbox_event_received ON inbox_event (company_id, received_at);

-- 28. [TENANT] 收件箱提炼待办表（Java: MysqlInboxStore.java:L44-L60）
CREATE TABLE inbox_todo (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    owner      VARCHAR(128) NOT NULL,
    title      VARCHAR(500) NOT NULL,
    detail     TEXT         NOT NULL DEFAULT '',
    requester  VARCHAR(255) NOT NULL DEFAULT '',
    due        VARCHAR(128) NOT NULL DEFAULT '',
    event_ids  TEXT         NOT NULL DEFAULT '',
    source     VARCHAR(255) NOT NULL DEFAULT '',
    status     VARCHAR(16)  NOT NULL, -- OPEN | TASKED | DONE | DISMISSED
    task_id    VARCHAR(64)  NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_inbox_todo_owner ON inbox_todo (company_id, owner, created_at DESC);

-- 29. [TENANT] 员工 IM 身份绑定与简报排期表
-- Java: MysqlInboxStore.java:L63-L84；V1 移除 5 个废弃空列 + 历史 secretary_agent_id（见 SecretaryCleanup.java）
CREATE TABLE event_source (
    company_id        VARCHAR(64)  NOT NULL,
    owner             VARCHAR(128) NOT NULL,
    type              VARCHAR(32)  NOT NULL, -- feishu | dingtalk
    app_id            VARCHAR(128) NOT NULL DEFAULT '',
    enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
    owner_external_id VARCHAR(128) NOT NULL DEFAULT '',
    bind_code         VARCHAR(32)  NOT NULL DEFAULT '',
    digest_time       VARCHAR(8)   NOT NULL DEFAULT '09:00',
    timezone          VARCHAR(64)  NOT NULL DEFAULT 'Asia/Shanghai',
    last_digest_date  VARCHAR(16)  NOT NULL DEFAULT '',
    last_hourly_at    TIMESTAMPTZ,
    created_at        TIMESTAMPTZ  NOT NULL,
    updated_at        TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, owner, type)
);

-- 30. [TENANT] 用户待办首页通知表（Java: MysqlNoticeStore.java:L11-L28）
CREATE TABLE notice (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    owner      VARCHAR(128) NOT NULL,
    notice_key VARCHAR(200) NOT NULL,
    kind       VARCHAR(16)  NOT NULL, -- CONFIRM | ACTION | DONE | NOTICE | TRACK
    state      VARCHAR(16)  NOT NULL, -- OPEN | DONE | IGNORED
    title      VARCHAR(500) NOT NULL,
    body       TEXT         NOT NULL DEFAULT '',
    actor      VARCHAR(255) NOT NULL DEFAULT '',
    link_type  VARCHAR(32)  NOT NULL DEFAULT '',
    link_id    VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id),
    CONSTRAINT uq_notice_owner_key UNIQUE (company_id, owner, notice_key)
);
CREATE INDEX idx_notice_owner ON notice (company_id, owner, updated_at DESC);

-- +goose Down
DROP TABLE IF EXISTS notice;
DROP TABLE IF EXISTS event_source;
DROP TABLE IF EXISTS inbox_todo;
DROP TABLE IF EXISTS inbox_event;
DROP TABLE IF EXISTS superagent_work_event;
DROP TABLE IF EXISTS superagent_work;
DROP TABLE IF EXISTS superagent_state;
```

### 3.5 `migrations/00005_init_assets_and_platform.sql`（15 张表：全租户隔离）

```sql
-- +goose Up

-- 31. [TENANT] 协作项目表（Java: MysqlProjectStore.java:L12-L23）
CREATE TABLE project (
    company_id  VARCHAR(64)  NOT NULL,
    id          VARCHAR(64)  NOT NULL,
    name        VARCHAR(255) NOT NULL,
    description TEXT         NOT NULL DEFAULT '',
    owner       VARCHAR(128) NOT NULL,
    status      VARCHAR(16)  NOT NULL, -- ACTIVE | ARCHIVED
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL,
    archived_at TIMESTAMPTZ,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_project_updated ON project (company_id, updated_at DESC);

-- 32. [TENANT] 项目成员表（Java: MysqlProjectStore.java:L25-L35）
CREATE TABLE project_member (
    company_id VARCHAR(64)  NOT NULL,
    project_id VARCHAR(64)  NOT NULL,
    username   VARCHAR(128) NOT NULL,
    added_by   VARCHAR(128) NOT NULL DEFAULT '',
    active     BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, project_id, username)
);
CREATE INDEX idx_project_member_user ON project_member (company_id, username) WHERE active = TRUE;

-- 33. [TENANT] 项目资源归属表（Java: MysqlProjectItemStore.java:L10-L18）
CREATE TABLE project_item (
    company_id VARCHAR(64) NOT NULL,
    kind       VARCHAR(16) NOT NULL, -- AGENT | TASK | FILE
    item_id    VARCHAR(64) NOT NULL,
    project_id VARCHAR(64) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, kind, item_id)
);
CREATE INDEX idx_project_item_project ON project_item (company_id, project_id, kind);

-- 34. [TENANT] 工作文件索引表（Java: MysqlWorkFileStore.java:L10-L32）
CREATE TABLE work_file (
    company_id VARCHAR(64)   NOT NULL,
    id         VARCHAR(64)   NOT NULL,
    name       VARCHAR(255)  NOT NULL,
    kind       VARCHAR(16)   NOT NULL, -- document | image | video | link | other
    mime_type  VARCHAR(128)  NOT NULL DEFAULT '',
    size       BIGINT        NOT NULL DEFAULT 0,
    storage    VARCHAR(32)   NOT NULL, -- internal | link | <channel>
    location   VARCHAR(1024) NOT NULL DEFAULT '',
    owner      VARCHAR(128)  NOT NULL,
    agent_id   VARCHAR(64)   NOT NULL DEFAULT '',
    task_id    VARCHAR(64)   NOT NULL DEFAULT '',
    origin     VARCHAR(16)   NOT NULL, -- chat | task | delivery | upload
    deleted    BOOLEAN       NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ   NOT NULL,
    updated_at TIMESTAMPTZ   NOT NULL,
    tags       VARCHAR(512)  NOT NULL DEFAULT '', -- 换行分隔
    tag_state  VARCHAR(8)    NOT NULL DEFAULT '', -- '' | agent | auto | user
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_work_file_owner ON work_file (company_id, owner, deleted, updated_at DESC);
CREATE INDEX idx_work_file_agent ON work_file (company_id, agent_id, deleted, updated_at DESC) WHERE agent_id <> '';
CREATE INDEX idx_work_file_task  ON work_file (company_id, task_id) WHERE task_id <> '';

-- 35. [TENANT] 文件与对话回复溯源表（Java: MysqlWorkFileStore.java:L34-L41）
CREATE TABLE work_file_ref (
    company_id VARCHAR(64) NOT NULL,
    file_id    VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL DEFAULT '',
    message_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, file_id, message_id)
);

-- 36. [TENANT] 个人文件虚拟目录表（Java: MysqlFileFolderStore.java:L14-L25）
CREATE TABLE file_folder (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    owner      VARCHAR(190) NOT NULL,
    name       VARCHAR(255) NOT NULL,
    parent_id  VARCHAR(64)  NOT NULL DEFAULT '',
    deleted    BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_file_folder_owner ON file_folder (company_id, owner, deleted);

-- 37. [TENANT] 个人文件目录放置表（Java: MysqlFileFolderStore.java:L27-L35）
CREATE TABLE file_placement (
    company_id VARCHAR(64)  NOT NULL,
    owner      VARCHAR(190) NOT NULL,
    file_id    VARCHAR(64)  NOT NULL,
    folder_id  VARCHAR(64)  NOT NULL DEFAULT '',
    placed_by  VARCHAR(16)  NOT NULL, -- user | agent | auto
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, owner, file_id)
);

-- 38. [TENANT] 可复用技能库表（Java: MysqlSkillStore.java:L13-L28）
CREATE TABLE skill (
    company_id  VARCHAR(64)   NOT NULL,
    id          VARCHAR(64)   NOT NULL,
    name        VARCHAR(255)  NOT NULL,
    description VARCHAR(1024) NOT NULL DEFAULT '',
    category    VARCHAR(64)   NOT NULL DEFAULT '',
    content     TEXT          NOT NULL DEFAULT '',
    space       VARCHAR(512)  NOT NULL DEFAULT '', -- personal:<uid> | team:<path> | company
    enabled     BOOLEAN       NOT NULL DEFAULT TRUE,
    deleted     BOOLEAN       NOT NULL DEFAULT FALSE,
    created_by  VARCHAR(128)  NOT NULL DEFAULT '',
    updated_by  VARCHAR(128)  NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ   NOT NULL,
    updated_at  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id)
);

-- 39. [TENANT] Agent/任务技能绑定表（Java: MysqlSkillStore.java:L31-L38）
CREATE TABLE skill_binding (
    company_id VARCHAR(64) NOT NULL,
    owner_type VARCHAR(16) NOT NULL, -- agent | task
    owner_id   VARCHAR(64) NOT NULL,
    skill_ids  TEXT        NOT NULL DEFAULT '', -- 逗号分隔有序 UUID
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, owner_type, owner_id)
);

-- 40. [TENANT] 技能删除确认卡表（Java: MysqlSkillDeletionStore.java:L11-L25）
CREATE TABLE skill_deletion (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    skill_id   VARCHAR(64)  NOT NULL,
    skill_name VARCHAR(255) NOT NULL DEFAULT '',
    agent_id   VARCHAR(64)  NOT NULL DEFAULT '',
    requester  VARCHAR(190) NOT NULL,
    session_id VARCHAR(64)  NOT NULL DEFAULT '',
    message_id VARCHAR(64)  NOT NULL DEFAULT '',
    status     VARCHAR(16)  NOT NULL, -- PENDING | CONFIRMED | CANCELLED
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_skill_deletion_agent ON skill_deletion (company_id, agent_id, created_at DESC);

-- 41. [TENANT] MCP 连接器定义表（Java: MysqlConnectorStore.java:L13-L27）
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

-- 42. [TENANT] 连接器加密凭证表（Java: MysqlConnectorCredentialStore.java:L13-L23）
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

-- 43. [TENANT] 模型供应商 CLI 状态与模型目录表（Java: MysqlModelProviderStore.java:L13-L30；models_json 升 JSONB）
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

-- 44. [TENANT] 供应商多密钥容灾池表（Java: MysqlProviderCredentialStore.java:L10-L30）
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

-- 45. [TENANT] 公司级扁平 KV 系统配置表（Java: MysqlSystemSettingStore.java:L12-L18）
CREATE TABLE system_setting (
    company_id    VARCHAR(64)   NOT NULL,
    setting_key   VARCHAR(128)  NOT NULL,
    setting_value VARCHAR(4096) NOT NULL DEFAULT '',
    updated_at    TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, setting_key)
);

-- +goose Down
DROP TABLE IF EXISTS system_setting;
DROP TABLE IF EXISTS provider_credential;
DROP TABLE IF EXISTS model_provider;
DROP TABLE IF EXISTS connector_credential;
DROP TABLE IF EXISTS connector;
DROP TABLE IF EXISTS skill_deletion;
DROP TABLE IF EXISTS skill_binding;
DROP TABLE IF EXISTS skill;
DROP TABLE IF EXISTS file_placement;
DROP TABLE IF EXISTS file_folder;
DROP TABLE IF EXISTS work_file_ref;
DROP TABLE IF EXISTS work_file;
DROP TABLE IF EXISTS project_item;
DROP TABLE IF EXISTS project_member;
DROP TABLE IF EXISTS project;
```

### 3.6 `migrations/00006_init_usage_and_timing.sql`（2 张表：全租户隔离）

```sql
-- +goose Up

-- 46. [TENANT] Token 五维用量与费用账单表（Java: JdbcUsageStore.java:L18-L41；V1 金额统一 NUMERIC(16,6)）
CREATE TABLE token_usage (
    company_id     VARCHAR(64)   NOT NULL,
    id             VARCHAR(64)   NOT NULL,
    at             TIMESTAMPTZ   NOT NULL,
    user_name      VARCHAR(128)  NOT NULL DEFAULT '',
    source         VARCHAR(32)   NOT NULL,
    agent_id       VARCHAR(64)   NOT NULL DEFAULT '',
    task_id        VARCHAR(64)   NOT NULL DEFAULT '',
    session_id     VARCHAR(64)   NOT NULL DEFAULT '',
    cli            VARCHAR(32)   NOT NULL DEFAULT '',
    model          VARCHAR(128)  NOT NULL DEFAULT '',
    input_tokens   BIGINT        NOT NULL DEFAULT 0,
    cache_read     BIGINT        NOT NULL DEFAULT 0,
    cache_write    BIGINT        NOT NULL DEFAULT 0,
    cache_write_1h BIGINT        NOT NULL DEFAULT 0,
    output_tokens  BIGINT        NOT NULL DEFAULT 0,
    reasoning      BIGINT        NOT NULL DEFAULT 0,
    cost_usd       NUMERIC(16,6),
    cli_cost_usd   NUMERIC(16,6),
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_token_usage_task    ON token_usage (company_id, task_id) WHERE task_id <> '';
CREATE INDEX idx_token_usage_user_at ON token_usage (company_id, user_name, at);
CREATE INDEX idx_token_usage_at      ON token_usage (company_id, at);

-- 47. [TENANT] 回合 7 阶段耗时与工具耗时表（Java: MysqlTurnTimingStore.java:L11-L29；tools 升 JSONB）
CREATE TABLE turn_timing (
    company_id  VARCHAR(64) NOT NULL,
    message_id  VARCHAR(64) NOT NULL,
    session_id  VARCHAR(64) NOT NULL,
    label       VARCHAR(32) NOT NULL DEFAULT '',
    started_at  TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    prep_ms     BIGINT      NOT NULL,
    startup_ms  BIGINT      NOT NULL,
    model_ms    BIGINT      NOT NULL,
    tools_ms    BIGINT      NOT NULL,
    output_ms   BIGINT      NOT NULL,
    retry_ms    BIGINT      NOT NULL,
    save_ms     BIGINT      NOT NULL,
    attempts    INTEGER     NOT NULL,
    tools       JSONB       NOT NULL DEFAULT '[]'::jsonb,
    PRIMARY KEY (company_id, message_id)
);
CREATE INDEX idx_turn_timing_session ON turn_timing (company_id, session_id, started_at);

-- +goose Down
DROP TABLE IF EXISTS turn_timing;
DROP TABLE IF EXISTS token_usage;
```

> **47 表总数校验**：`00001`(5) + `00002`(9) + `00003`(9) + `00004`(7) + `00005`(15) + `00006`(2) = **47 张表**（2 张全局表 `company`/`user_session` + 45 张 `company_id` 强隔离表）。

---

## 4. 冻结契约

### 4.1 导出符号（`internal/storage/postgres`）

```go
package postgres

type Config struct {
	DSN             string        // postgres://user:pass@host:5432/synapse?sslmode=disable
	MaxConns        int32         // 默认 16
	MinConns        int32         // 默认 2
	MaxConnLifetime time.Duration // 默认 30m
	MaxConnIdleTime time.Duration // 默认 5m
	AutoMigrate     bool          // 默认 true
}

// DB 封装连接池与事务管理器
type DB struct {
	Pool *pgxpool.Pool
	Tx   store.TxManager
}

func Open(ctx context.Context, cfg Config) (*DB, error)
func Migrate(ctx context.Context, pool *pgxpool.Pool) error
func MigrateDownOne(ctx context.Context, pool *pgxpool.Pool) error // 仅用于测试与运维回滚验证
func (d *DB) Ping(ctx context.Context) error
func (d *DB) Close()

// Stores 一次性装配全部 38 个 Store 实现与 TxManager，编译期强制实现全部 store.* 接口
type Stores struct {
	Tx                  store.TxManager
	Company             store.CompanyStore
	Session             store.SessionStore
	User                store.UserStore
	Org                 store.OrgStore
	Agent               store.AgentStore
	AgentMember         store.AgentMemberStore
	AgentInvite         store.AgentInviteStore
	AgentParent         store.AgentParentStore
	AgentMemory         store.AgentMemoryStateStore
	AgentLog            store.AgentLogStore
	Chat                store.ChatStore
	Task                store.TaskStore
	TaskPhase           store.TaskPhaseStore
	TaskRunState        store.TaskRunStateStore
	TaskAgent           store.TaskAgentStore
	TaskLink            store.TaskLinkStore
	TaskTag             store.TaskTagStore
	TaskModelChoice     store.TaskModelChoiceStore
	Automation          store.AutomationStore
	SuperAgentState     store.SuperAgentStateStore
	SuperAgentWork      store.SuperAgentWorkStore
	InboxEvent          store.InboxEventStore
	InboxTodo           store.InboxTodoStore
	EventSource         store.EventSourceStore
	Notice              store.NoticeStore
	Project             store.ProjectStore
	ProjectItem         store.ProjectItemStore
	WorkFile            store.WorkFileStore
	FileFolder          store.FileFolderStore
	Skill               store.SkillStore
	SkillDeletion       store.SkillDeletionStore
	Connector           store.ConnectorStore
	ConnectorCredential store.ConnectorCredentialStore
	ModelProvider       store.ModelProviderStore
	ProviderCredential  store.ProviderCredentialStore
	SystemSetting       store.SystemSettingStore
	Usage               store.UsageStore
	TurnTiming          store.TurnTimingStore
}

func NewStores(db *DB) *Stores
```

### 4.2 路由契约

本篇为 L1 存储实现层，不直接注册 HTTP 路由。健康探针 `/actuator/health` 由 `SA-13` 调用 `db.Ping(ctx)`（超时 2s）检查 PG17 连接池可用性；迁移子命令 `synapse-server migrate` 由 `SA-14` 调用 `postgres.Migrate(ctx, db.Pool)`。

### 4.3 错误语义

- **`pgx.ErrNoRows` 归一化约定**：全部单条查找方法 `Find*` / `ParentOf` / `Manager` / `ProjectOf` / `AgentOf` / `Origin` / `Binding` / `FindPhasesJSON` 遇到 `pgx.ErrNoRows` 时，**统一返回 `(nil, nil)` 或 `("", false, nil)`**（不返回原始 `pgx.ErrNoRows`，交由 L2–L4 领域服务决定是否转为 `apperror.NotFound` 或创建默认值）。
- **`pgconn.PgError` 错误码转换 (`mapPgErr`)**：
  - `23505` (`unique_violation`) → `apperror.Conflict(...)`（例如 `app_user` 的 `uq_app_user_username`、`model_provider` 的 `uq_model_provider_key`、`task_model_choice` 重复 `seq`）；
  - `22001` (`string_data_right_truncation`) / `22P02` (`invalid_text_representation`) → `apperror.InvalidArg(...)`；
  - `57014` (`query_canceled`) / `context.Canceled` / `context.DeadlineExceeded` → 原样透传上下文错误；
  - 其余数据库错误 → `fmt.Errorf("postgres %s: %w", op, err)`（由上层日志记录并对外映为 `500 internal_error`）。
- **缺失租户上下文**：任何租户 Store 调用 `s.company(ctx)` 时若 `ctx` 无 `companyID`，立即返回 `tenant.ErrMissingTenant`（绝不向 PostgreSQL 发送查询）。

### 4.4 SQL 契约（全 38 Store 幂等写、冲突策略与排序对照）

| Store / 表 | Java 原版写策略 | Go PG17 冻结 SQL 写契约 | 读排序与关键过滤契约 |
|---|---|---|---|
| `CompanyStore` (`company`) | `REPLACE INTO` (`JdbcCompanyStore:L81`) | `INSERT ... ON CONFLICT (id) DO UPDATE SET name=..., enabled=..., data_key_cipher=..., max_users=..., max_running_tasks=..., monthly_tokens=..., updated_at=...` | `ORDER BY created_at ASC` |
| `SessionStore` (`user_session`) | *(V1 新增)* | `INSERT INTO user_session ...`；`Touch`: `UPDATE ... SET password_ok=$2, expires_at=$3, updated_at=$4 WHERE id=$1`；`DeleteExpired`: `DELETE FROM user_session WHERE expires_at < $1` | `Find` 仅返回未过期会话（`expires_at > NOW()`） |
| `UserStore` (`app_user`) | **先 `UPDATE` 后 `INSERT`** (`MysqlUserStore:L47-L54`) | **严禁 `ON CONFLICT` 覆盖 `username`！** 严格先 `UPDATE app_user SET ... WHERE company_id=$1 AND id=$2`，若 `RowsAffected()==0` 再 `INSERT`，重复 `username` 触发 `23505` → `apperror.Conflict` | `ListUsers`: `ORDER BY created_at ASC`；`FindByPhone`/`FindByExternalID` 空串直接返 `nil, nil` |
| `OrgStore` (`user_manager`, `user_team`) | `DELETE` + `INSERT` (`AbstractJdbcOrgStore:L56-L87`) | `SetManager`：`manager==""` 时 `DELETE`，否则 `ON CONFLICT (company_id, username) DO UPDATE`；`SetTeams`：在 `WithinTx` 内先 `DELETE` 再批量 `INSERT ... ON CONFLICT DO NOTHING` | `Teams`: `ORDER BY team_path ASC`；`Memberships`: `ORDER BY username, team_path` |
| `AgentStore` (`agent`) | `INSERT` / `ON DUPLICATE KEY` (`MysqlAgentStore:L61`) | `Create`: `INSERT`；`Update`: `INSERT ... ON CONFLICT (company_id, id) DO UPDATE SET name=..., avatar=..., persona=..., model=..., status=..., owner=..., kind=..., managed_by=..., updated_at=...` | `List`: `ORDER BY updated_at DESC` |
| `AgentMemberStore` (`agent_member`) | `REPLACE INTO` (`MysqlAgentMemberStore:L32`) | `INSERT ... ON CONFLICT (company_id, agent_id, username) DO UPDATE SET added_by=..., active=..., updated_at=...` | `Members`: `active = TRUE ORDER BY created_at ASC`；`AgentsOf`: `active = TRUE` |
| `AgentInviteStore` (`agent_invite`) | `REPLACE INTO` (`MysqlAgentInviteStore:L35`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE SET usernames=..., session_id=..., message_id=..., status=..., updated_at=...` | `ForAgent`: `ORDER BY created_at DESC` |
| `AgentParentStore` (`agent_parent`) | `DELETE` / `REPLACE INTO` (`JdbcAgentParentStore:L38-L41`) | `parentID==""` 执行 `DELETE`；否则 `ON CONFLICT (company_id, agent_id) DO UPDATE SET parent_id=..., updated_at=...` | `All`: 返回 `map[agentID]parentID` |
| `AgentMemoryStateStore` (`agent_memory`) | `ON DUPLICATE KEY` (`MysqlAgentMemoryStateStore:L48`) | `INSERT ... ON CONFLICT (company_id, agent_id) DO UPDATE SET owner=..., summarized_until=..., last_model=..., session_failures=..., memory=..., updated_at=...` | 按 `(company_id, agent_id)` 精确查找 |
| `AgentLogStore` (`agent_log`) | `INSERT` + 2 步 `Prune` (`AbstractJdbcAgentLogStore:L53-L89`) | `Append`: `INSERT`；`Prune`: ① `DELETE WHERE company_id=$1 AND at_time < $2`；② 单条窗口函数 SQL `DELETE` 保留每个 `agent_id` 最新 `keepPerAgent` 条（零 N+1 查询！） | `List`: 可选 `category`，`ORDER BY at_time DESC LIMIT $n` |
| `ChatStore` (`chat_session`, `chat_message`, `agent_event`) | `INSERT` / `UPDATE` (`AbstractJdbcChatStore:L107-L173`) | `UpdateSession`: `UPDATE chat_session SET ... WHERE company_id=$1 AND id=$2`；`AppendEvent`: `INSERT INTO agent_event (...) VALUES (...) RETURNING id` | `ListSessions`/`ByAgent`: `ORDER BY updated_at DESC`；`ListMessages`: `ORDER BY created_at ASC`；`ListEvents`: `ORDER BY created_at, seq, id`；`ListEventsAfter`: `WHERE id > $3 ORDER BY id ASC LIMIT $4` |
| `TaskStore` (`task`) | `ON DUPLICATE KEY` (`MysqlTaskStore:L69`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE SET title=..., brief=..., source=..., repo=..., branch=..., flow=..., status=..., progress=..., spend=..., model=..., agent_type=..., workspace_dir=..., created_by=..., error=..., started_at=..., finished_at=..., updated_at=...` | `ListTasks`: `ORDER BY created_at DESC` |
| `TaskPhaseStore` (`task_phase`) | `ON DUPLICATE KEY` (`MysqlTaskPhaseStore:L32`) | `INSERT ... ON CONFLICT (company_id, task_id) DO UPDATE SET phases_json=EXCLUDED.phases_json, updated_at=EXCLUDED.updated_at` | `FindPhasesJSON` 返原始 `JSONB` 字节 |
| `TaskRunStateStore` (`task_run_state`) | `ON DUPLICATE KEY` (`MysqlTaskRunStateStore:L35`) | `INSERT ... ON CONFLICT (company_id, task_id) DO UPDATE SET agent_session_id=..., failure_count=..., attempt_count=..., updated_at=...` | 精确匹配 `(company_id, task_id)` |
| `TaskAgentStore` (`task_agent`) | `REPLACE INTO` (`MysqlTaskAgentStore:L38`) | 空 `taskID`/`agentID` 直接 no-op；`ON CONFLICT (company_id, task_id) DO UPDATE SET agent_id=..., session_id=..., message_id=...` | `TasksForAgent`/`OriginsForAgent`: `ORDER BY created_at DESC` |
| `TaskLinkStore` (`task_link`) | `INSERT` (`JdbcTaskLinkStore:L40`) | `INSERT ... ON CONFLICT (company_id, task_id) DO NOTHING` | `Children`/`FollowUps`: `ORDER BY created_at ASC` |
| `TaskTagStore` (`task_tag`) | `REPLACE INTO` (`JdbcTaskTagStore:L38`) | `INSERT ... ON CONFLICT (company_id, task_id) DO UPDATE SET tags=..., updated_at=...`（`\n` 分隔） | `All`: 仅返回非空标签条目 |
| `TaskModelChoiceStore` (`task_model_choice`) | `INSERT` (`JdbcTaskModelChoiceStore:L42`) | `INSERT INTO task_model_choice ...`（追加不可变审计行） | `List`: `ORDER BY seq ASC` |
| `AutomationStore` (`automation`, `automation_run`) | `ON DUPLICATE KEY` + `INSERT` (`MysqlAutomationStore:L71`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`Delete`: 仅删 `automation`，保留 `automation_run` | `List`: `ORDER BY created_at DESC`；`ListRuns`: `ORDER BY fired_at DESC LIMIT $n`；`FindRunByTask`: `ORDER BY fired_at DESC LIMIT 1` |
| `SuperAgentStateStore` (`superagent_state`) | `ON DUPLICATE KEY` (`MysqlSuperAgentStateStore:L38`) | `INSERT ... ON CONFLICT (company_id, owner) DO UPDATE SET agent_id=..., processed_until=..., plan_id=..., plan_status=..., plan_json=..., updated_at=...` | `List` / `Find` 按租户 |
| `SuperAgentWorkStore` (`superagent_work`, `superagent_work_event`) | `ON DUPLICATE KEY` + `INSERT` (`MysqlSuperAgentWorkStore:L62`) | `Save`: `ON CONFLICT (company_id, task_id) DO UPDATE`；`AppendEvent`: `INSERT` | `List`: `ORDER BY assigned_at DESC`；`Events`: `ORDER BY at_time ASC` |
| `InboxEventStore` (`inbox_event`) | **`INSERT IGNORE`** + `REPLACE` (`MysqlInboxStore:L103-L108`) | `InsertIfAbsent`: **`INSERT ... ON CONFLICT (company_id, id) DO NOTHING`**（`RowsAffected()==1` 返 `true`）；`Update`: `ON CONFLICT (company_id, id) DO UPDATE` | `Pending`: `processed=FALSE ORDER BY received_at ASC LIMIT $n`；`Recent`: `ORDER BY received_at DESC LIMIT $n` |
| `InboxTodoStore` (`inbox_todo`) | `REPLACE INTO` (`MysqlInboxStore:L118`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE` | `List`: `ORDER BY created_at DESC` |
| `EventSourceStore` (`event_source`) | `REPLACE INTO` (`MysqlInboxStore:L123`) | `INSERT ... ON CONFLICT (company_id, owner, type) DO UPDATE` | `ListEnabled`: `type=$2 AND enabled=TRUE` |
| `NoticeStore` (`notice`) | `REPLACE INTO` (`MysqlNoticeStore:L39`) | **`ON CONFLICT (company_id, owner, notice_key) DO UPDATE`**（同时防并发同 key 竞态与按原 key 更新状态！） | `List`: **`AND kind <> 'TRACK' ORDER BY updated_at DESC LIMIT $n`**（严格对齐 `AbstractJdbcNoticeStore:L57`） |
| `ProjectStore` (`project`, `project_member`) | `REPLACE INTO` (`MysqlProjectStore:L46,L51`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`SaveMember`: `ON CONFLICT (company_id, project_id, username) DO UPDATE` | `List`: `ORDER BY updated_at DESC`；`Members`: `active=TRUE ORDER BY created_at ASC` |
| `ProjectItemStore` (`project_item`) | `REPLACE INTO` (`AbstractJdbcProjectItemStore:L33`) | `Assign`: `ON CONFLICT (company_id, kind, item_id) DO UPDATE SET project_id=..., updated_at=...` | `Items`: `ORDER BY updated_at DESC` |
| `WorkFileStore` (`work_file`, `work_file_ref`) | `REPLACE INTO` + 查后插 (`AbstractJdbcWorkFileStore:L57-L112`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`Link`: **`INSERT INTO work_file_ref ... ON CONFLICT (company_id, file_id, message_id) DO NOTHING`**（消除原版 TOCTOU！） | `ListFor`: `deleted=FALSE AND (owner=$2 OR agent_id = ANY($3)) ORDER BY updated_at DESC`；`Untagged`: `deleted=FALSE AND tag_state='' ORDER BY created_at ASC LIMIT $n`；`Messages`: `ORDER BY created_at ASC` |
| `FileFolderStore` (`file_folder`, `file_placement`) | `REPLACE INTO` (`MysqlFileFolderStore:L46,L51`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`Place`: `ON CONFLICT (company_id, owner, file_id) DO UPDATE SET folder_id=..., placed_by=..., updated_at=...` | `Find`/`List`/`Owners`: `deleted=FALSE`；`List`: `ORDER BY name ASC` |
| `SkillStore` (`skill`, `skill_binding`) | `ON DUPLICATE KEY` (`MysqlSkillStore:L57,L65`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`Bind`: `ON CONFLICT (company_id, owner_type, owner_id) DO UPDATE SET skill_ids=..., updated_at=...` | `List`: `ORDER BY name ASC`（含软删，调用方过滤）；`Binding`: 无行返 `nil, false, nil`，空串行返 `[]string{}, true, nil` |
| `SkillDeletionStore` (`skill_deletion`) | `REPLACE INTO` (`MysqlSkillDeletionStore:L36`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE` | `ForAgent`: `ORDER BY created_at DESC` |
| `ConnectorStore` (`connector`) | `REPLACE INTO` (`MysqlConnectorStore:L41`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE` | `ListConnectors`: `ORDER BY created_at ASC` |
| `ConnectorCredentialStore` (`connector_credential`) | `REPLACE INTO` (`MysqlConnectorCredentialStore:L45`) | `INSERT ... ON CONFLICT (company_id, connector_id, owner) DO UPDATE` | 精确查找与按 `connector_id` 列表 |
| `ModelProviderStore` (`model_provider`) | `ON DUPLICATE KEY` (`MysqlModelProviderStore:L59`) | `INSERT ... ON CONFLICT (company_id, id) DO UPDATE`（重复 `provider_key` 触发 `uq_model_provider_key` → `Conflict`） | `List`: `ORDER BY created_at ASC` |
| `ProviderCredentialStore` (`provider_credential`) | `REPLACE INTO` + 软删 (`AbstractJdbcProviderCredentialStore:L73-L81`) | `Save`: `ON CONFLICT (company_id, id) DO UPDATE`；`Delete`: **`UPDATE provider_credential SET deleted=TRUE, updated_at=$3 WHERE company_id=$1 AND id=$2 AND deleted=FALSE`** | `List`: `deleted=FALSE ORDER BY priority ASC, created_at ASC`；`KnownKeyHashes`: **含已软删行**（`key_hash <> ''`） |
| `SystemSettingStore` (`system_setting`) | `ON DUPLICATE KEY` (`MysqlSystemSettingStore:L32`) | `INSERT ... ON CONFLICT (company_id, setting_key) DO UPDATE SET setting_value=..., updated_at=...` | `All`: `ORDER BY setting_key ASC` |
| `UsageStore` (`token_usage`) | `INSERT` (`JdbcUsageStore:L71`) | `INSERT INTO token_usage ...` | `ForTask`: `ORDER BY at ASC`；`Between`: `at >= $2 AND at < $3`（可选 `AND user_name=$4`）`ORDER BY at ASC` |
| `TurnTimingStore` (`turn_timing`) | `REPLACE INTO` (`MysqlTurnTimingStore:L40`) | `INSERT ... ON CONFLICT (company_id, message_id) DO UPDATE`（`tools` 列以 JSONB 数组存取） | `ForSessions`: `session_id = ANY($2::text[]) ORDER BY started_at ASC`（空切片直接返空） |

---

## 5. 行为语义（含 Java 源码对照、Go 数据定义与详细伪代码）

### 5.1 连接池、`goose` 迁移与 `TxManager` (`db.go`, `tx.go`)

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/JdbcStorageConfiguration.java:L34-L56`、`TenantSchema.java:L24-L51`。
> **V1 升级**：用 `pgxpool` 强制 `TimeZone=UTC` 运行时参数替代 JDBC URL `connectionTimeZone=UTC`；用 `goose/v3` 内嵌版本迁移彻底替代 `TenantSchema.create()` 删表重建。

```go
package postgres

import (
	"context"
	"embed"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DBTX 统一池与事务的执行窄接口，所有 Store 均通过 q(ctx) 获取它
type DBTX interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type txKey struct{}

type txManager struct{ pool *pgxpool.Pool }

// WithinTx 在单事务内执行 fn；若 ctx 已处于外层事务中，则使用 PG SAVEPOINT 实现嵌套原子回滚。
func (m *txManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if parentTx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		sp, err := parentTx.Begin(ctx) // pgx/v5 自动使用 SAVEPOINT
		if err != nil {
			return fmt.Errorf("begin savepoint: %w", err)
		}
		if err := fn(context.WithValue(ctx, txKey{}, sp)); err != nil {
			_ = sp.Rollback(ctx)
			return err
		}
		return sp.Commit(ctx)
	}
	tx, err := m.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	txCtx := context.WithValue(ctx, txKey{}, tx)
	if err := fn(txCtx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

func Open(ctx context.Context, cfg Config) (*DB, error) {
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse postgres dsn: %w", err)
	}
	if cfg.MaxConns <= 0 { cfg.MaxConns = 16 }
	if cfg.MinConns <= 0 { cfg.MinConns = 2 }
	if cfg.MaxConnLifetime <= 0 { cfg.MaxConnLifetime = 30 * time.Minute }
	if cfg.MaxConnIdleTime <= 0 { cfg.MaxConnIdleTime = 5 * time.Minute }
	pcfg.MaxConns = cfg.MaxConns
	pcfg.MinConns = cfg.MinConns
	pcfg.MaxConnLifetime = cfg.MaxConnLifetime
	pcfg.MaxConnIdleTime = cfg.MaxConnIdleTime
	// 强制所有物理连接会话时区为 UTC，与原版 forceConnectionTimeZoneToSession=true 对齐
	pcfg.ConnConfig.RuntimeParams["TimeZone"] = "UTC"

	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("open pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	if cfg.AutoMigrate {
		if err := Migrate(ctx, pool); err != nil {
			pool.Close()
			return nil, fmt.Errorf("run goose migrations: %w", err)
		}
	}
	return &DB{Pool: pool, Tx: &txManager{pool: pool}}, nil
}

// Migrate 使用 goose/v3 Provider API（无全局状态污染，并发测试安全）执行内嵌迁移。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	subFS, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, subFS)
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
```

### 5.2 公共基类助手与租户提取 (`helpers.go`, `errors.go`)

```go
package postgres

type baseStore struct {
	pool *pgxpool.Pool
}

// q 若 ctx 含活动事务则返回 pgx.Tx，否则返回 pgxpool.Pool。
func (b *baseStore) q(ctx context.Context) DBTX {
	if tx, ok := ctx.Value(txKey{}).(pgx.Tx); ok {
		return tx
	}
	return b.pool
}

// company 所有非全局 Store 方法的统一入口，guard_test.go 静态 AST 校验必经此方法。
func (b *baseStore) company(ctx context.Context) (string, error) {
	return tenant.Require(ctx)
}

// utc 统一归一化为 UTC 毫秒精度，对齐原版 DATETIME(3) 与 PG TIMESTAMPTZ。
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

func mapPgErr(op string, err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return apperror.Conflict(fmt.Sprintf("%s: duplicate key (%s)", op, pgErr.ConstraintName))
		case "22001", "22P02":
			return apperror.InvalidArg(fmt.Sprintf("%s: invalid data (%s)", op, pgErr.Message))
		}
	}
	return fmt.Errorf("postgres %s: %w", op, err)
}
```

### 5.3 核心关键 Store 详细伪代码（覆盖全部特殊并发与语义边界）

#### A. `UserStore.Save` — 严格 `UPDATE`-then-`INSERT` 防止 `username` 冲突覆盖他人
> **Java 源码对照**：`superagent-storage/.../MysqlUserStore.java:L43-L56`。
```go
func (s *userStore) Save(ctx context.Context, u domain.User) (*domain.User, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	rolesCSV := strings.Join(u.Roles, ",")
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
		return nil, mapPgErr("user.update", err)
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
			return nil, mapPgErr("user.insert", err) // uq_app_user_username 碰撞映射为 409 Conflict
		}
	}
	return &u, nil
}
```

#### B. `ChatStore.AppendEvent` & `ListEventsAfter` — `BIGSERIAL` 主键与游标增量读取
> **Java 源码对照**：`superagent-storage/.../AbstractJdbcChatStore.java:L163-L173`（V1 修复无主键问题）。
```go
func (s *chatStore) AppendEvent(ctx context.Context, e domain.AgentEvent) (int64, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return 0, err
	}
	var id int64
	err = s.q(ctx).QueryRow(ctx, `
		INSERT INTO agent_event (company_id, session_id, message_id, seq, type, payload, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		cid, e.SessionID, e.MessageID, e.Seq, string(e.Type), e.Payload, utc(e.CreatedAt),
	).Scan(&id)
	return id, mapPgErr("chat.append_event", err)
}

func (s *chatStore) ListEventsAfter(ctx context.Context, sessionID string, afterID int64, limit int) ([]domain.AgentEvent, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.q(ctx).Query(ctx, `
		SELECT id, session_id, message_id, seq, type, payload, created_at
		  FROM agent_event
		 WHERE company_id = $1 AND session_id = $2 AND id > $3
		 ORDER BY id ASC
		 LIMIT $4`, cid, sessionID, afterID, limit)
	// ... scan into []domain.AgentEvent
}
```

#### C. `AgentLogStore.Prune` — 单 SQL 窗口函数替代 Java N+1 裁剪
> **Java 源码对照**：`superagent-storage/.../AbstractJdbcAgentLogStore.java:L70-L84`。
```go
func (s *agentLogStore) Prune(ctx context.Context, before time.Time, keepPerAgent int) error {
	cid, err := s.company(ctx)
	if err != nil {
		return err
	}
	if _, err := s.q(ctx).Exec(ctx,
		`DELETE FROM agent_log WHERE company_id = $1 AND at_time < $2`, cid, utc(before)); err != nil {
		return mapPgErr("agent_log.prune_time", err)
	}
	if keepPerAgent <= 0 {
		return nil
	}
	// 利用 PG17 窗口函数一次原子删掉每个 Agent 超过 keepPerAgent 的旧日志，消除原版循环查询
	_, err = s.q(ctx).Exec(ctx, `
		DELETE FROM agent_log a
		USING (
			SELECT id FROM (
				SELECT id, ROW_NUMBER() OVER (PARTITION BY agent_id ORDER BY at_time DESC, id DESC) AS rn
				  FROM agent_log
				 WHERE company_id = $1
			) ranked
			WHERE rn > $2
		) victim
		WHERE a.company_id = $1 AND a.id = victim.id`, cid, keepPerAgent)
	return mapPgErr("agent_log.prune_cap", err)
}
```

#### D. `InboxEventStore.InsertIfAbsent`、`WorkFileStore.Link`、`NoticeStore.Save`、`ProviderCredentialStore.Delete`、`SkillStore.Binding`
> **Java 源码对照**：`MysqlInboxStore.java:L102-L109`、`AbstractJdbcWorkFileStore.java:L103-L113`、`AbstractJdbcNoticeStore.java:L55-L59`、`AbstractJdbcProviderCredentialStore.java:L62-L81`、`AbstractJdbcSkillStore.java:L65-L70`。
```go
// 1. InboxEventStore.InsertIfAbsent：原子去重，返回是否首次入库
func (s *inboxStore) InsertIfAbsent(ctx context.Context, e domain.InboxEvent) (bool, error) {
	cid, err := s.company(ctx)
	if err != nil { return false, err }
	tag, err := s.q(ctx).Exec(ctx, `
		INSERT INTO inbox_event (
			company_id, id, owner, source, external_id, chat_id, chat_name, chat_type,
			thread_id, sender_id, sender_name, text, mentions_me, raw, priority,
			triage_reason, processed, handling, occurred_at, received_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21)
		ON CONFLICT (company_id, id) DO NOTHING`,
		cid, e.ID, e.Owner, e.Source, e.ExternalID, e.ChatID, e.ChatName, e.ChatType,
		e.ThreadID, e.SenderID, e.SenderName, e.Text, e.MentionsMe, e.Raw, string(e.Priority),
		e.TriageReason, e.Processed, e.Handling, utcPtr(e.OccurredAt), utc(e.ReceivedAt), utc(e.UpdatedAt),
	)
	if err != nil { return false, mapPgErr("inbox_event.insert_if_absent", err) }
	return tag.RowsAffected() == 1, nil
}

// 2. WorkFileStore.Link：用 ON CONFLICT DO NOTHING 消除原版 SELECT COUNT + INSERT 竞态
func (s *workFileStore) Link(ctx context.Context, fileID, sessionID, messageID string) error {
	cid, err := s.company(ctx)
	if err != nil { return err }
	_, err = s.q(ctx).Exec(ctx, `
		INSERT INTO work_file_ref (company_id, file_id, session_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (company_id, file_id, message_id) DO NOTHING`,
		cid, fileID, sessionID, messageID, utc(time.Now()),
	)
	return mapPgErr("work_file_ref.link", err)
}

// 3. NoticeStore.Save：按逻辑唯一键 (company_id, owner, notice_key) Upsert，List 过滤 kind <> 'TRACK'
func (s *noticeStore) Save(ctx context.Context, n domain.Notice) error {
	cid, err := s.company(ctx)
	if err != nil { return err }
	_, err = s.q(ctx).Exec(ctx, `
		INSERT INTO notice (
			company_id, id, owner, notice_key, kind, state, title, body, actor,
			link_type, link_id, created_at, updated_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (company_id, owner, notice_key) DO UPDATE SET
			kind = EXCLUDED.kind, state = EXCLUDED.state, title = EXCLUDED.title,
			body = EXCLUDED.body, actor = EXCLUDED.actor, link_type = EXCLUDED.link_type,
			link_id = EXCLUDED.link_id, updated_at = EXCLUDED.updated_at`,
		cid, n.ID, n.Owner, n.Key, string(n.Kind), string(n.State), n.Title, n.Body, n.Actor,
		n.LinkType, n.LinkID, utc(n.CreatedAt), utc(n.UpdatedAt),
	)
	return mapPgErr("notice.save", err)
}

// 4. ProviderCredentialStore.Delete：软删除（保留 key_hash 供 KnownKeyHashes 防止重复导入宿主机密钥）
func (s *providerCredentialStore) Delete(ctx context.Context, id string) error {
	cid, err := s.company(ctx)
	if err != nil { return err }
	_, err = s.q(ctx).Exec(ctx, `
		UPDATE provider_credential
		   SET deleted = TRUE, updated_at = $3
		 WHERE company_id = $1 AND id = $2 AND deleted = FALSE`,
		cid, id, utc(time.Now()),
	)
	return mapPgErr("provider_credential.delete", err)
}

// 5. SkillStore.Binding：严格区分「无绑定记录 (nil, false, nil)」与「显式空绑定 ([]string{}, true, nil)」
func (s *skillStore) Binding(ctx context.Context, ownerType, ownerID string) ([]string, bool, error) {
	cid, err := s.company(ctx)
	if err != nil { return nil, false, err }
	var csv string
	err = s.q(ctx).QueryRow(ctx, `
		SELECT skill_ids FROM skill_binding
		 WHERE company_id = $1 AND owner_type = $2 AND owner_id = $3`,
		cid, ownerType, ownerID,
	).Scan(&csv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, mapPgErr("skill_binding.get", err)
	}
	if csv == "" {
		return []string{}, true, nil
	}
	return strings.Split(csv, ","), true, nil
}
```

### 5.4 自动化租户隔离与禁删表守卫 (`guard_test.go`)

> **Java 源码对照**：`superagent-storage/src/test/java/com/superagent/storage/jdbc/TenantScopeGuardTest.java:L20-L68`。
> **V1 升级**：Go 版借助标准库 `go/parser` + `go/ast` 同时做 **SQL 迁移静态校验** + **Go Store 方法级 AST 校验**，精度远超原版文件级字符串匹配！

```go
package postgres

// 1. TestMigrationTenantScopeGuard：扫描 migrations/*.sql 全部 -- +goose Up 段：
//    - 统计表数量必须等于 47（防止漏写或私自增表未登记）；
//    - 断言 -- +goose Up 段绝对不允许出现 "DROP TABLE"；
//    - 除全局白名单 {"company", "user_session"} 外，其余 45 张表必须同时满足：
//      (a) 包含列定义 `company_id VARCHAR(64) NOT NULL`（忽略多余空白）；
//      (b) 包含 `PRIMARY KEY (company_id, ...)`（company_id 必为主键首列）；
//      (c) 该表的所有 `CREATE [UNIQUE] INDEX` 必须以 `company_id` 作为首列且索引名以 `idx_<table_>`/`uq_<table_>` 开头。
//
// 2. TestStoreMethodTenantGuard：用 go/parser 解析全部 store_*.go 文件的 AST：
//    - 白名单仅豁免全局 Store 接收者类型：`*companyStore`、`*sessionStore`；
//    - 对其余 36 个租户 Store 结构体的每一个导出方法 `func (s *xxxStore) Method(ctx context.Context, ...)`:
//      遍历其函数体 `ast.BlockStmt`，断言必须存在至少一次 `s.company(ctx)`（或 `tenant.Require(ctx)`）调用！
//      （允许特殊无 SQL 辅助或转调自身带 company 校验的方法如 `WithinTx` 内层回调）。
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 在 `synapse-server/go.mod` 引入 `github.com/jackc/pgx/v5` 与 `github.com/pressly/goose/v3`；编写 6 个迁移文件 `migrations/00001_*.sql` ~ `00006_*.sql`（全 47 表）。
- [ ] **T2**: **守卫先行（TDD）**：编写 `internal/storage/postgres/guard_test.go`（校验 47 表 `company_id`、主键首列、索引前缀、禁 `DROP TABLE` 及 Store 方法 AST 校验），确认无实现时正确校验迁移 SQL。
- [ ] **T3**: 实现 `db.go`、`tx.go`、`errors.go`、`helpers.go`（`Open`、`Migrate`、`MigrateDownOne`、`TxManager` 嵌套 savepoint、`mapPgErr`）+ 编写迁移升降级与事务回滚集成测试 `db_test.go`。
- [ ] **T4**: 实现 `store_tenant_auth.go`（`CompanyStore`、`SessionStore`、`UserStore`、`OrgStore`）+ 编写 `store_tenant_auth_test.go`（重点测 `app_user` `username` 冲突不覆盖他人、`user_session` 过期清理与 `DeleteByUser`）。
- [ ] **T5**: 实现 `store_agent_chat.go`（7 个 Store）+ 编写 `store_agent_chat_test.go`（重点测 `agent_event` `BIGSERIAL` 游标增量、`agent_log.Prune` 窗口函数按时间+上限裁剪、跨租户零可见）。
- [ ] **T6**: 实现 `store_task_automation.go`（8 个 Store）+ 编写 `store_task_automation_test.go`（重点测 `task.spend` `NUMERIC(16,6)` 精确往返、`task_phase` JSONB、`automation` 硬删保留 `automation_run`）。
- [ ] **T7**: 实现 `store_superagent_inbox.go`（6 个 Store）+ 编写 `store_superagent_inbox_test.go`（重点测 `inbox_event.InsertIfAbsent` 并发去重、`notice` 按 `(owner, notice_key)` 幂等覆盖及 `kind <> 'TRACK'` 过滤、`superagent_state.plan_json` JSONB）。
- [ ] **T8**: 实现 `store_assets.go`（6 个 Store）+ 编写 `store_assets_test.go`（重点测 `work_file_ref.Link` 并发幂等、`skill_binding` 无绑定 vs 空绑定区分、`file_folder`/`file_placement`）。
- [ ] **T9**: 实现 `store_platform.go`（5 个 Store）与 `store_analytics.go`（2 个 Store）+ `stores.go` 聚合工厂 + 编写对应 `*_test.go`（重点测 `provider_credential` 软删保留 `key_hash`、`token_usage` 时间区间 `[from, to)`、`turn_timing` JSONB 工具列表）。
- [ ] **T10**: 全量运行 `go test -race ./...`（含 `arch_test.go` + `guard_test.go` + 真实 PG17 测试实例）。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 实施看板：将 `SA-02` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md`：核对文中描述的 47 张表（修正 §1 摘要中历史手误「40 张」表述为精确 **47 张**）与 6 个迁移文件清单 100% 同步；
- [ ] `synapse-server/README.md`：补充本地测试 PG17 启动命令（`TEST_POSTGRES_DSN=...`）及 `goose` 迁移开发规范（禁止修改已合入的迁移文件，只能追加新编号文件）。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | SQL 迁移 47 表租户与禁删表守卫 | `internal/storage/postgres/guard_test.go` | 精确解析出 47 张表；`Up` 段零 `DROP TABLE`；除 `company`/`user_session` 外 45 张表全部 `company_id VARCHAR(64) NOT NULL` 且为 PK/索引首列 |
| 2 | Store 方法级 AST 租户守卫 | `internal/storage/postgres/guard_test.go` | 除 `companyStore`/`sessionStore` 外，其余 36 个 Store 的所有导出方法 AST 中必含 `s.company(ctx)` 调用 |
| 3 | `goose` 全量 `Up` → `Down` → `Up` 幂等迁移 | `internal/storage/postgres/db_test.go` | 全 6 个迁移文件在全新 PG17 库执行 `Up` 无报错，重复 `Up` 幂等，逐个 `Down` 干净无残表，再次 `Up` 成功 |
| 4 | `TxManager.WithinTx` 提交、回滚与嵌套 Savepoint | `internal/storage/postgres/db_test.go` | 外层返回 error 全部回滚；嵌套内层返回 error 仅回滚内层 savepoint、外层继续提交成功 |
| 5 | 全局跨租户物理隔离回归（核心代表表） | `internal/storage/postgres/store_tenant_auth_test.go` 等 | 同主键 `id` 同时写入 `acme` 与 `beta` 互不冲突；在 `acme` 查询绝不返回 `beta` 数据；无租户 `context.Background()` 调用任何租户 Store 必返 `tenant.ErrMissingTenant` |
| 6 | `UserStore.Save` 用户名唯一约束防覆盖 | `internal/storage/postgres/store_tenant_auth_test.go` | 用户 A 占 `alice` 后，以新 UUID 插入 `alice` 返回 `apperror.CodeConflict`，且用户 A 记录完好无损；`SessionStore.DeleteExpired` 精准清理过期行 |
| 7 | `ChatStore` `agent_event` 主键与游标增量拉取 | `internal/storage/postgres/store_agent_chat_test.go` | 连续 `AppendEvent` 返回严格递增 `id`；`ListEventsAfter(sid, lastID, 50)` 无漏无重；并发多协程写入无竞态 |
| 8 | `AgentLogStore.Prune` 时间与单 Agent 上限双维裁剪 | `internal/storage/postgres/store_agent_chat_test.go` | 写入跨 2 个 Agent 与新旧时间戳日志，`Prune(cutoff, 3)` 后旧于 `cutoff` 全删且每个 Agent 仅留最新 3 条，不影响另一租户日志 |
| 9 | `TaskStore` 金额精度与 `AutomationStore` 保留历史 | `internal/storage/postgres/store_task_automation_test.go` | `spend = 0.000123` 精确往返 6 位小数；`task_phase` JSONB 完整往返；`AutomationStore.Delete` 删除定义后 `ListRuns`/`FindRunByTask` 仍可读历史 |
| 10 | `InboxEventStore` 并发去重与 `NoticeStore` 唯一键 Upsert | `internal/storage/postgres/store_superagent_inbox_test.go` | 16 协程并发 `InsertIfAbsent` 同 ID 仅 1 个返回 `true`；`NoticeStore.Save` 同 `(owner, notice_key)` 覆盖更新不报 23505，且 `List` 自动排除 `TRACK` |
| 11 | `WorkFileStore` / `SkillStore` / `ProviderCredentialStore` 边界 | `internal/storage/postgres/store_assets_test.go`、`store_platform_test.go` | `WorkFileStore.Link` 并发重复调用幂等；`SkillStore.Binding` 精确区分 `(nil, false)` 与 `([], true)`；`ProviderCredentialStore.Delete` 后 `Find`/`List` 不可见但 `KnownKeyHashes` 仍含其 `key_hash` |
| 12 | `UsageStore` 区间查询与 `TurnTimingStore` JSONB 工具列表 | `internal/storage/postgres/store_analytics_test.go` | `Between(user, from, to)` 严格左闭右开 `[from, to)`，空 `user` 查全租户；`TurnTiming.Tools` 含中文/制表符名称在 JSONB 中无损往返 |

> **测试环境约定**：`guard_test.go` 零外部依赖（纯静态 SQL/AST 解析，CI 与本地秒跑）；集成单测通过环境变量 `TEST_POSTGRES_DSN`（或 `testcontainers-go` 拉起 `postgres:17-alpine`）自动为每个测试包创建隔离 schema / database 运行，不污染外部环境。

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race ./internal/storage/postgres -run Test.*Guard` 与 `go test -race ./internal`（`arch_test.go`）无外部依赖秒级全绿
- [ ] 面向真实 **PostgreSQL 17** 运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 全绿，包行覆盖率 ≥ 80%
- [ ] §2 交付物表 12 项与 §3 全量 **47 张表 / 6 个 `goose` 迁移文件**逐项核对无遗漏
- [ ] §4.1 `Stores` 聚合结构体通过编译期接口断言 `var _ store.XxxStore = (*xxxStore)(nil)`（全部 38 个接口 100% 匹配 `SA-01`）
- [ ] `SA-01` 前置包回归全绿；`arch_test.go` 确认无业务层包直接 import `pgx/v5` 或 `internal/storage/postgres`
- [ ] 人工核验：使用独立 `postgres:17-alpine` 容器执行迁移后 `\dt` 精确输出 **48 张表**（47 张业务/全局表 + 1 张 `goose_db_version`）

---

## 10. 可提交节点

- 建议 commit 1（迁移与守卫）：`feat(storage): add 6 goose PostgreSQL 17 migrations for all 47 tables and AST/SQL tenant guard (spec 02, SA-02)`
- 建议 commit 2（连接池、事务与基础域 Store）：`feat(storage): implement pgxpool, TxManager and tenant/auth/agent/chat/task/automation stores (spec 02, SA-02)`
- 建议 commit 3（编排、资产、平台、分析 Store 与集成测试）：`feat(storage): implement superagent/inbox/assets/platform/analytics stores and PG17 test suite (spec 02, SA-02)`
