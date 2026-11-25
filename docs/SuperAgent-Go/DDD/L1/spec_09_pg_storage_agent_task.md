# spec 09 | L1 PG 存储（三）— Agent、对话流事件、长期记忆与任务自动化

- **状态**：`冻结待实施`
- **篇号顺序**：09（L1 PostgreSQL 17 存储层第三篇，交付 Agent/对话/记忆/日志与异步任务/自动化两大核心执行域存储）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-09`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [_epic/L1/spec_02_storage_postgres_migrations.md](../_epic/L1/spec_02_storage_postgres_migrations.md)（47 表 + 38 Store 归档母本）
  - [_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md](../_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md) §3
  - [_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md](../_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md) §3
- **前置依赖**：
  - `L0/spec_02`（`SA-02`：`internal/infra/db` 提供的 `db.QuerierFromCtx`、`db.TxManager`、`db.MapPGError`、`db.NewTestSchema(t)`）
  - `L1/spec_06`（`SA-06`：`internal/core/{domain,store}` 中 Agent/Chat/Task/Automation 领域模型与 14 个 Store 接口）
  - `L1/spec_07`（`SA-07`：`internal/storage/postgres/{migrations.go,helpers.go,guard_test.go}`）
- **下游消费者**：`SA-10`（全量 47 表端到端迁移与 38 Store 装配）、`SA-27`–`SA-32`（L3 Agent 协作、实时对话 SSE/LiveTurn、长期记忆、异步任务 8 态执行器、任务看门狗与定时/Webhook 自动化）

---

## 1. 背景与范围

本篇交付 `synapse-server` **L1 PostgreSQL 17 存储实现层 (`internal/storage/postgres`)** 中数据写入最密集、并发边界最关键的两大核心域——**Agent/对话/记忆域**与**任务/自动化域**，包含 2 个版本化迁移脚本（**17 张全租户隔离表**）与 **14 个 PG17 Store 实现**，并落地多项 V1 结构净化与缺陷修复：

1. **Agent、会话、消息、流事件、记忆与日志域（`00004_agent_chat_memory.sql` · 9 表 · 7 Store）**：
   - 表：`agent`、`agent_member`、`agent_invite`、`agent_parent`、`agent_memory`、`agent_log`、`chat_session`、`chat_message`、`agent_event`；
   - **V1 清理废弃列**：彻底移除 `agent.character_key`（V1 砍掉 24 个内置性格预设，纯用 `persona`）与 `agent.tier`；
   - **V1 修复 `agent_event` 无主键缺陷**：为 `agent_event` 补齐物理自增列 `id BIGSERIAL NOT NULL` 与联合主键 `PRIMARY KEY (company_id, id)`，支持 `AppendEvent` `RETURNING id` 与 `ListEventsAfter(sessionID, afterID, limit)` 精确游标增量断线续传；
   - **去 ClickHouse 与消除 N+1 裁剪**：`agent_log` 在 V1 统一落 PG17，且 `AgentLogStore.Prune` 用单条 PG17 窗口函数 `ROW_NUMBER() OVER (PARTITION BY agent_id ...)` 原子替换原版 `AbstractJdbcAgentLogStore.java:L70-L84` 逐 Agent 循环查询删除。
2. **异步任务与定时/Webhook 自动化域（`00005_task_automation.sql` · 8 表 · 7 Store）**：
   - 表：`task`、`task_phase`、`task_run_state`、`task_agent`、`task_link`、`task_tag`、`automation`、`automation_run`；
   - **V1 清理废弃列**：彻底移除 `task.tier` 与 `automation.tier`；
   - **PG17 类型强化**：`task.spend` 严格采用 `NUMERIC(16,6)` 对齐 `shopspring/decimal`，`task_phase.phases_json` 采用原生 `JSONB NOT NULL DEFAULT '[]'::jsonb`；
   - **审计留痕**：`AutomationStore.Delete` 仅删除 `automation` 定义行，保留 `automation_run` 历史触发记录供回溯。

**本篇边界与跨篇衔接说明**：
- 成本路由决策表 `task_model_choice`（`TaskModelChoiceStore`）因由 `internal/cost` 路由器直写，已归属 `SA-08`（`00003_assets_cost.sql`）；
- `superagent_state.tracked_json` 废弃列清理归属 `SA-10`（`00006_event_orchestrator.sql`）；
- 本篇全部 14 个 Store 均复用 `L0/spec_02` `internal/infra/db`（`db.QuerierFromCtx`、`db.MapPGError`、`db.NewTestSchema(t)`）与 `SA-07` `baseStore`/`guard_test.go`，不含 SSE 内存广播（`LiveTurn`）、任务调度池或 `robfig/cron` 触发器（留给 `SA-27`–`SA-32`），零外部缓存组件。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考路径（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） |
|---|---|---|---|---|
| 1 | Agent、协作、记忆、日志与对话流 9 表 PG17 迁移脚本 | `internal/storage/postgres/migrations/00004_agent_chat_memory.sql` | 新增 | `MysqlAgentStore.java:L16-L32`, `MysqlAgentMemberStore.java:L11-L21`, `MysqlAgentInviteStore.java:L11-L24`, `JdbcAgentParentStore.java:L16-L22`, `MysqlAgentMemoryStateStore.java:L13-L23`, `MysqlAgentLogStore.java:L12-L24`, `MysqlChatStore.java:L18-L58` |
| 2 | 异步任务与自动化 8 表 PG17 迁移脚本 | `internal/storage/postgres/migrations/00005_task_automation.sql` | 新增 | `MysqlTaskStore.java:L13-L36`, `MysqlTaskPhaseStore.java:L12-L18`, `MysqlTaskRunStateStore.java:L13-L21`, `MysqlTaskAgentStore.java:L12-L21`, `JdbcTaskLinkStore.java:L16-L25`, `JdbcTaskTagStore.java:L17-L23`, `MysqlAutomationStore.java:L13-L48` |
| 3 | Agent 与对话域 7 个 PG Store 实现 | `internal/storage/postgres/store_agent_chat.go` | 新增 | `AbstractJdbcAgentStore.java`, `MysqlAgentStore.java`, `AbstractJdbcAgentMemberStore.java`, `MysqlAgentMemberStore.java`, `AbstractJdbcAgentInviteStore.java`, `MysqlAgentInviteStore.java`, `JdbcAgentParentStore.java`, `AbstractJdbcAgentMemoryStateStore.java`, `MysqlAgentMemoryStateStore.java`, `AbstractJdbcAgentLogStore.java`, `MysqlAgentLogStore.java`, `AbstractJdbcChatStore.java`, `MysqlChatStore.java` |
| 4 | 任务与自动化域 7 个 PG Store 实现 | `internal/storage/postgres/store_task_automation.go` | 新增 | `AbstractJdbcTaskStore.java`, `MysqlTaskStore.java`, `AbstractJdbcTaskPhaseStore.java`, `MysqlTaskPhaseStore.java`, `AbstractJdbcTaskRunStateStore.java`, `MysqlTaskRunStateStore.java`, `AbstractJdbcTaskAgentStore.java`, `MysqlTaskAgentStore.java`, `JdbcTaskLinkStore.java`, `JdbcTaskTagStore.java`, `AbstractJdbcAutomationStore.java`, `MysqlAutomationStore.java` |
| 5 | Agent 与对话 7 Store 集成单测（含 `agent_event` 游标、窗口函数 `Prune`、并发写入） | `internal/storage/postgres/store_agent_chat_test.go` | 新增 | `AgentStoreTest.java`, `AgentMemberStoreTest.java`, `AgentInviteStoreTest.java`, `AgentParentStoreTest.java`, `AgentMemoryStateStoreTest.java`, `AgentLogStoreTest.java`, `ChatStoreTest.java` |
| 6 | 任务与自动化 7 Store 集成单测（含 `NUMERIC(16,6)`、`JSONB`、保留 `automation_run`） | `internal/storage/postgres/store_task_automation_test.go` | 新增 | `TaskStoreTest.java`, `MysqlTaskPhaseStoreTest.java`, `TaskAgentStoreTest.java`, `AutomationStoreTest.java` |

---

## 3. 数据库设计（PG17 DDL · 2 个 `goose` 迁移文件 · 17 张表）

### 3.1 `migrations/00004_agent_chat_memory.sql`（9 张表：全租户隔离）

```sql
-- +goose Up

-- 24. [TENANT] 智能体表（Java: MysqlAgentStore.java:L16-L32；V1 彻底移除 character_key 与 tier）
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

-- 25. [TENANT] Agent 协作者表（Java: MysqlAgentMemberStore.java:L11-L21）
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

-- 26. [TENANT] Agent 协作邀请确认卡表（Java: MysqlAgentInviteStore.java:L11-L24）
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

-- 27. [TENANT] Agent 上下级汇报树（Java: JdbcAgentParentStore.java:L16-L22）
CREATE TABLE agent_parent (
    company_id VARCHAR(64) NOT NULL,
    agent_id   VARCHAR(64) NOT NULL,
    parent_id  VARCHAR(64) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, agent_id)
);

-- 28. [TENANT] Agent 长期记忆与会话健康状态表（Java: MysqlAgentMemoryStateStore.java:L13-L23, L32, L37）
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

-- 29. [TENANT] Agent 运行日志表（Java: MysqlAgentLogStore.java:L12-L24；V1 统一落 PG17，去 ClickHouse）
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

-- 30. [TENANT] 对话会话表（Java: MysqlChatStore.java:L18-L34, L79）
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

-- 31. [TENANT] 对话与任务消息表（Java: MysqlChatStore.java:L37-L46 + ALTER author）
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

-- 32. [TENANT][V1 修复] 执行流事件表（Java: MysqlChatStore.java:L49-L58；补齐 BIGSERIAL 物理主键）
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

### 3.2 `migrations/00005_task_automation.sql`（8 张表：全租户隔离）

```sql
-- +goose Up

-- 33. [TENANT] 异步任务主表（Java: MysqlTaskStore.java:L13-L36, L50；V1 移除 tier，保留 NUMERIC(16,6)）
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

-- 34. [TENANT] 任务结构化阶段表（Java: MysqlTaskPhaseStore.java:L12-L18；PG17 原生 JSONB）
CREATE TABLE task_phase (
    company_id  VARCHAR(64) NOT NULL,
    task_id     VARCHAR(64) NOT NULL,
    phases_json JSONB       NOT NULL DEFAULT '[]'::jsonb,
    updated_at  TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 35. [TENANT] 任务崩溃恢复运行态表（Java: MysqlTaskRunStateStore.java:L13-L21）
CREATE TABLE task_run_state (
    company_id       VARCHAR(64) NOT NULL,
    task_id          VARCHAR(64) NOT NULL,
    agent_session_id VARCHAR(64) NOT NULL DEFAULT '',
    failure_count    INTEGER     NOT NULL DEFAULT 0,
    attempt_count    INTEGER     NOT NULL DEFAULT 0,
    updated_at       TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 36. [TENANT] 任务来源 Agent 关联表（Java: MysqlTaskAgentStore.java:L12-L21, L27）
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

-- 37. [TENANT] 任务子任务与追问接续图谱表（Java: JdbcTaskLinkStore.java:L16-L25）
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

-- 38. [TENANT] 任务主题标签表（Java: JdbcTaskTagStore.java:L17-L23；换行分隔）
CREATE TABLE task_tag (
    company_id VARCHAR(64) NOT NULL,
    task_id    VARCHAR(64) NOT NULL,
    tags       TEXT        NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, task_id)
);

-- 39. [TENANT] 定时/Webhook 自动化定义表（Java: MysqlAutomationStore.java:L13-L35；V1 移除 tier）
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

-- 40. [TENANT] 自动化触发历史表（Java: MysqlAutomationStore.java:L38-L48）
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
DROP TABLE IF EXISTS task_tag;
DROP TABLE IF EXISTS task_link;
DROP TABLE IF EXISTS task_agent;
DROP TABLE IF EXISTS task_run_state;
DROP TABLE IF EXISTS task_phase;
DROP TABLE IF EXISTS task;
```

> **本篇表数量核对**：`00004_agent_chat_memory.sql` (9) + `00005_task_automation.sql` (8) = **17 张表**（全租户隔离；累计 `SA-07`(10) + `SA-08`(13) + `SA-09`(17) = 40 张表）。

---

## 4. 冻结契约

### 4.1 导出符号（`internal/storage/postgres`）

```go
package postgres

// 本篇（SA-09）导出的 14 个租户 Store 构造函数（均复用 L0 internal/infra/db 与 SA-07 baseStore）：
func NewAgentStore(pool *pgxpool.Pool) store.AgentStore
func NewAgentMemberStore(pool *pgxpool.Pool) store.AgentMemberStore
func NewAgentInviteStore(pool *pgxpool.Pool) store.AgentInviteStore
func NewAgentParentStore(pool *pgxpool.Pool) store.AgentParentStore
func NewAgentMemoryStateStore(pool *pgxpool.Pool) store.AgentMemoryStateStore
func NewAgentLogStore(pool *pgxpool.Pool) store.AgentLogStore
func NewChatStore(pool *pgxpool.Pool) store.ChatStore

func NewTaskStore(pool *pgxpool.Pool) store.TaskStore
func NewTaskPhaseStore(pool *pgxpool.Pool) store.TaskPhaseStore
func NewTaskRunStateStore(pool *pgxpool.Pool) store.TaskRunStateStore
func NewTaskAgentStore(pool *pgxpool.Pool) store.TaskAgentStore
func NewTaskLinkStore(pool *pgxpool.Pool) store.TaskLinkStore
func NewTaskTagStore(pool *pgxpool.Pool) store.TaskTagStore
func NewAutomationStore(pool *pgxpool.Pool) store.AutomationStore
```

编译期接口断言：
```go
var (
	_ store.AgentStore            = (*agentStore)(nil)
	_ store.AgentMemberStore      = (*agentMemberStore)(nil)
	_ store.AgentInviteStore      = (*agentInviteStore)(nil)
	_ store.AgentParentStore      = (*agentParentStore)(nil)
	_ store.AgentMemoryStateStore = (*agentMemoryStateStore)(nil)
	_ store.AgentLogStore         = (*agentLogStore)(nil)
	_ store.ChatStore             = (*chatStore)(nil)
	_ store.TaskStore             = (*taskStore)(nil)
	_ store.TaskPhaseStore        = (*taskPhaseStore)(nil)
	_ store.TaskRunStateStore     = (*taskRunStateStore)(nil)
	_ store.TaskAgentStore        = (*taskAgentStore)(nil)
	_ store.TaskLinkStore         = (*taskLinkStore)(nil)
	_ store.TaskTagStore          = (*taskTagStore)(nil)
	_ store.AutomationStore       = (*automationStore)(nil)
)
```

### 4.2 路由契约

本篇为 L1 存储实现层，**不注册 HTTP 路由**。对话 SSE 断线重连（`Last-Event-ID` 游标回放）由 L3/L5 层消费本篇 `ChatStore.ListEventsAfter` 实现。

### 4.3 错误语义

严格复用 `L0/spec_02` `db.MapPGError` 与 `SA-07` 错误归一化约定：
- 全部 14 个 Store 均为租户隔离 Store，缺失 `companyID` 时立即返 `tenant.ErrMissingTenant`；
- 单条查找（`AgentStore.Find`、`AgentInviteStore.Find`、`AgentParentStore.ParentOf`、`AgentMemoryStateStore.Find`、`ChatStore.FindSession`、`TaskStore.FindTask`、`TaskPhaseStore.FindPhasesJSON`、`TaskRunStateStore.Find`、`TaskAgentStore.AgentOf`/`Origin`、`TaskLinkStore.Find`、`AutomationStore.Find`/`FindRunByTask`）遇 `pgx.ErrNoRows` 统一返回 `(nil, nil)` 或 `("", false, nil)` / `(nil, false, nil)`；
- `AgentStore.Create` 重复主键返回 `apperror.Conflict`（经 `db.MapPGError` 映射 `23505`）；
- `TaskPhaseStore.Save` 对空切片默认回退 `[]byte("[]")`，非法 JSON 载荷由 PG17 `22P02` 映射为 `apperror.InvalidArg`。

### 4.4 SQL 契约（本篇 14 Store 写策略、冲突语义与排序对照）

| # | Store / 表 | Java 原版写策略与源码行号 | Go PG17 冻结 SQL 写契约 | 读排序与边界过滤契约 |
|---|---|---|---|---|
| 1 | `AgentStore` (`agent`) | `INSERT` / `ON DUPLICATE KEY` (`AbstractJdbcAgentStore.java:L63,L85`, `MysqlAgentStore.java:L61-L62`) | `Create`: `INSERT INTO agent (...) VALUES (...)`；`Update`: `INSERT INTO agent (...) VALUES (...) ON CONFLICT (company_id, id) DO UPDATE SET name=..., avatar=..., persona=..., model=..., status=..., owner=..., kind=..., managed_by=..., updated_at=...` | `List`: `ORDER BY updated_at DESC` |
| 2 | `AgentMemberStore` (`agent_member`) | `REPLACE INTO` (`MysqlAgentMemberStore.java:L32`, `AbstractJdbcAgentMemberStore.java:L40`) | `Save`: `INSERT INTO agent_member (...) ON CONFLICT (company_id, agent_id, username) DO UPDATE SET added_by=..., active=..., updated_at=...` | `Members`: `WHERE company_id=$1 AND agent_id=$2 AND active=TRUE ORDER BY created_at ASC`；`AgentsOf`: `WHERE company_id=$1 AND username=$2 AND active=TRUE` |
| 3 | `AgentInviteStore` (`agent_invite`) | `REPLACE INTO` (`MysqlAgentInviteStore.java:L35`, `AbstractJdbcAgentInviteStore.java:L54`) | `Save`: `INSERT INTO agent_invite (...) ON CONFLICT (company_id, id) DO UPDATE SET usernames=..., session_id=..., message_id=..., status=..., updated_at=...` | `ForAgent`: `WHERE company_id=$1 AND agent_id=$2 ORDER BY created_at DESC` |
| 4 | `AgentParentStore` (`agent_parent`) | `DELETE` / `REPLACE INTO` (`JdbcAgentParentStore.java:L38-L41`) | `Set`: `parentID==""` 执行 `DELETE FROM agent_parent WHERE company_id=$1 AND agent_id=$2`；否则 `INSERT ... ON CONFLICT (company_id, agent_id) DO UPDATE SET parent_id=EXCLUDED.parent_id, updated_at=EXCLUDED.updated_at` | `All`: 返回 `map[agentID]parentID` |
| 5 | `AgentMemoryStateStore` (`agent_memory`) | `ON DUPLICATE KEY` (`MysqlAgentMemoryStateStore.java:L48-L58`) | `Save`: `INSERT INTO agent_memory (...) ON CONFLICT (company_id, agent_id) DO UPDATE SET owner=..., summarized_until=..., last_model=..., session_failures=..., memory=..., updated_at=...`；`Delete`: `WHERE company_id=$1 AND agent_id=$2` | 按 `(company_id, agent_id)` 精确查找 |
| 6 | `AgentLogStore` (`agent_log`) | `INSERT` + 2 步 `Prune` (`AbstractJdbcAgentLogStore.java:L53-L84`, `MysqlAgentLogStore.java:L33`) | `Append`: `INSERT INTO agent_log (...)`；`Prune`: ① `DELETE WHERE company_id=$1 AND at_time < $2`；② 单条窗口函数 `DELETE FROM agent_log a USING (...) victim WHERE a.company_id=$1 AND a.id=victim.id`（零 N+1）；`DeleteAgent`: `DELETE WHERE company_id=$1 AND agent_id=$2` | `List`: `category==""` 不过滤类别，否则 `AND category=$3`；`ORDER BY at_time DESC LIMIT $n`（`limit <= 0` 默认 100） |
| 7 | `ChatStore` (`chat_session`, `chat_message`, `agent_event`) | `INSERT` / `UPDATE` (`AbstractJdbcChatStore.java:L108-L173`, `MysqlChatStore.java:L90`) | `CreateSession`: `INSERT`；`UpdateSession`: `UPDATE chat_session SET ... WHERE company_id=$1 AND id=$2`；`AppendMessage`: `INSERT`；`UpdateMessage`: `UPDATE chat_message SET content=$4 WHERE company_id=$1 AND session_id=$2 AND id=$3`；`AppendEvent`: **`INSERT INTO agent_event (...) VALUES (...) RETURNING id`** | `ListSessions`/`ListSessionsByAgent`: `ORDER BY updated_at DESC`；`ListMessages`: `ORDER BY created_at ASC`；`ListEvents`: `ORDER BY created_at ASC, seq ASC, id ASC`；`ListEventsAfter`: `WHERE company_id=$1 AND session_id=$2 AND id > $3 ORDER BY id ASC LIMIT $4` |
| 8 | `TaskStore` (`task`) | `ON DUPLICATE KEY` (`MysqlTaskStore.java:L69-L82`, `AbstractJdbcTaskStore.java:L64`) | `Save`: `INSERT INTO task (...) ON CONFLICT (company_id, id) DO UPDATE SET title=..., brief=..., source=..., repo=..., branch=..., flow=..., status=..., progress=..., spend=..., model=..., agent_type=..., workspace_dir=..., created_by=..., error=..., started_at=..., finished_at=..., updated_at=...`；`Delete`: `DELETE FROM task WHERE company_id=$1 AND id=$2` | `ListTasks`: `ORDER BY created_at DESC`；`spend` 以 `NUMERIC(16,6)` 精确往返 `decimal.Decimal` |
| 9 | `TaskPhaseStore` (`task_phase`) | `ON DUPLICATE KEY` (`MysqlTaskPhaseStore.java:L32-L39`) | `Save`: `INSERT INTO task_phase (company_id, task_id, phases_json, updated_at) VALUES ($1,$2,$3,$4) ON CONFLICT (company_id, task_id) DO UPDATE SET phases_json=EXCLUDED.phases_json, updated_at=EXCLUDED.updated_at`；`Delete`: `WHERE company_id=$1 AND task_id=$2` | `FindPhasesJSON`: 返回原始 JSONB 字节与存在标志 `(bytes, true, nil)` |
| 10 | `TaskRunStateStore` (`task_run_state`) | `ON DUPLICATE KEY` (`MysqlTaskRunStateStore.java:L35-L46`) | `Save`: `INSERT INTO task_run_state (...) ON CONFLICT (company_id, task_id) DO UPDATE SET agent_session_id=..., failure_count=..., attempt_count=..., updated_at=...`；`Delete`: `WHERE company_id=$1 AND task_id=$2` | 精确匹配 `(company_id, task_id)` |
| 11 | `TaskAgentStore` (`task_agent`) | `REPLACE INTO` (`MysqlTaskAgentStore.java:L38`, `AbstractJdbcTaskAgentStore.java:L45,L70`) | `Link`: 校验租户后若 `taskID=="" || agentID==""` 直接 no-op；否则 `INSERT ... ON CONFLICT (company_id, task_id) DO UPDATE SET agent_id=EXCLUDED.agent_id, session_id=EXCLUDED.session_id, message_id=EXCLUDED.message_id` | `TasksForAgent`/`OriginsForAgent`: `WHERE company_id=$1 AND agent_id=$2 ORDER BY created_at DESC` |
| 12 | `TaskLinkStore` (`task_link`) | `INSERT` (`JdbcTaskLinkStore.java:L40-L62`) | `Save`: `INSERT INTO task_link (company_id, task_id, parent_id, continues_from, created_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (company_id, task_id) DO NOTHING` | `Children`: `WHERE company_id=$1 AND parent_id=$2 ORDER BY created_at ASC`；`FollowUps`: `WHERE company_id=$1 AND continues_from=$2 ORDER BY created_at ASC` |
| 13 | `TaskTagStore` (`task_tag`) | `REPLACE INTO` (`JdbcTaskTagStore.java:L38`) | `Set`: `INSERT INTO task_tag (company_id, task_id, tags, updated_at) VALUES ($1,$2,$3,$4) ON CONFLICT (company_id, task_id) DO UPDATE SET tags=EXCLUDED.tags, updated_at=EXCLUDED.updated_at`（`\n` 分隔） | `All`: 仅返回非空标签条目（`tags <> ''`） |
| 14 | `AutomationStore` (`automation`, `automation_run`) | `ON DUPLICATE KEY` + `INSERT` (`MysqlAutomationStore.java:L71-L84`, `AbstractJdbcAutomationStore.java:L69-L95`) | `Save`: `INSERT INTO automation (...) ON CONFLICT (company_id, id) DO UPDATE SET ...`；`Delete`: **`DELETE FROM automation WHERE company_id=$1 AND id=$2`（不删 `automation_run`！）**；`AppendRun`: `INSERT INTO automation_run (...)` | `List`: `ORDER BY created_at DESC`；`ListRuns`: `WHERE company_id=$1 AND automation_id=$2 ORDER BY fired_at DESC LIMIT $3`；`FindRunByTask`: `WHERE company_id=$1 AND task_id=$2 ORDER BY fired_at DESC LIMIT 1` |

---

## 5. 行为语义（含 Java 源码对照与 Go 实现伪代码）

### 5.1 `ChatStore.AppendEvent` & `ListEventsAfter` — `BIGSERIAL` 主键与精确增量游标

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcChatStore.java:L163-L173`、`MysqlChatStore.java:L49-L58`。

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
	return id, db.MapPGError("chat.append_event", err)
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
	if err != nil {
		return nil, db.MapPGError("chat.list_events_after", err)
	}
	defer rows.Close()
	return scanAgentEvents(rows)
}
```

### 5.2 `AgentLogStore.Prune` — 单 SQL PG17 窗口函数原子裁剪

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcAgentLogStore.java:L70-L84`（原版先查全部 `DISTINCT agent_id` 再逐个查第 `keepPerAgent` 条时间点做删除，存在 N+1 与并发抖动）。

```go
func (s *agentLogStore) Prune(ctx context.Context, before time.Time, keepPerAgent int) error {
	cid, err := s.company(ctx)
	if err != nil {
		return err
	}
	if _, err := s.q(ctx).Exec(ctx,
		`DELETE FROM agent_log WHERE company_id = $1 AND at_time < $2`,
		cid, utc(before)); err != nil {
		return db.MapPGError("agent_log.prune_time", err)
	}
	if keepPerAgent <= 0 {
		return nil
	}
	// 利用 PG17 ROW_NUMBER() 窗口函数单条 SQL 原子裁剪本租户每个 Agent 超出上限的旧日志
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
	return db.MapPGError("agent_log.prune_cap", err)
}
```

### 5.3 `TaskStore.Save` & `TaskPhaseStore` — 定点金额与 JSONB 阶段结构

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlTaskStore.java:L69-L82`、`MysqlTaskPhaseStore.java:L32-L39`。
- `TaskStore.Save` 将 `t.Spend.StringFixed(6)` 绑定至 PG17 `NUMERIC(16,6)`，查询时以 `spend::text` 扫描并调用 `decimal.NewFromString` 还原，彻底消除二进制浮点精度漂移；
- `TaskPhaseStore.Save` 若传入空 `phasesJSON` 则归一化写入 `[]byte("[]")`，配合表定义 `JSONB NOT NULL DEFAULT '[]'::jsonb`，保证读取端永不遇到 `NULL` 或非法空串；
- `TaskLinkStore.Save` 使用 `ON CONFLICT (company_id, task_id) DO NOTHING`，保证任务谱系（`parent_id` / `continues_from`）一旦建立不可被后续重放覆盖。

### 5.4 `AutomationStore` — 删除定义保留审计历史与部分索引调度查询

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlAutomationStore.java:L71-L84`、`AbstractJdbcAutomationStore.java:L69-L95`。
- `Delete(ctx, id)` 仅执行 `DELETE FROM automation WHERE company_id = $1 AND id = $2`，刻意保留 `automation_run` 表记录，确保历史任务仍可通过 `FindRunByTask(ctx, taskID)` 溯源其触发来源；
- `idx_automation_next` 为部分索引 `WHERE enabled = TRUE AND next_run_at IS NOT NULL`，大幅降低定时调度轮询开销。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 新增 `internal/storage/postgres/migrations/00004_agent_chat_memory.sql`（9 表）与 `00005_task_automation.sql`（8 表），确认已彻底移除 `agent.character_key`/`agent.tier`/`task.tier`/`automation.tier` 且补齐 `agent_event.id BIGSERIAL`；运行 `guard_test.go` 验证 17 表静态守卫全绿。
- [ ] **T2**: 编写 `internal/storage/postgres/store_agent_chat_test.go`（先红灯：覆盖 7 个 Agent/Chat Store，重点含 `agent_event` 并发写入与游标增量拉取、`agent_log.Prune` 窗口函数双维裁剪、跨租户隔离，使用 `db.NewTestSchema(t)`）。
- [ ] **T3**: 实现 `internal/storage/postgres/store_agent_chat.go`（7 个 Store），验证 `store_agent_chat_test.go` 与 `guard_test.go` 全绿。
- [ ] **T4**: 编写 `internal/storage/postgres/store_task_automation_test.go`（先红灯：覆盖 7 个 Task/Automation Store，重点含 `task.spend` 6 位小数往返、`task_phase` JSONB、`task_link` 幂等不覆盖、`automation` 删除保留 `automation_run`）。
- [ ] **T5**: 实现 `internal/storage/postgres/store_task_automation.go`（7 个 Store），验证 `store_task_automation_test.go` 全绿。
- [ ] **T6**: 运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 验证 `SA-07` + `SA-08` + `SA-09`（40 表 / 32 Store）全绿且覆盖率 ≥ 80%。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-09` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §3–§4：确认 `00004_agent_chat_memory.sql`（9 表）与 `00005_task_automation.sql`（8 表）的 DDL 净化条目一致。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `00004`+`00005` 17 表静态守卫 + 14 Store AST 守卫 | `internal/storage/postgres/guard_test.go` | 17 表均含 `company_id VARCHAR(64) NOT NULL` 且无 `character_key`/`tier` 废弃列；14 个 Store 导出方法均调 `s.company(ctx)` 且 SQL 含 `company_id` |
| 2 | `AgentStore` / `AgentMemberStore` / `AgentInviteStore` / `AgentParentStore` | `internal/storage/postgres/store_agent_chat_test.go` | `Create` 重复主键返 `Conflict`；`AgentMemberStore.Save(active=false)` 从 `Members`/`AgentsOf` 排除；`AgentParentStore.Set(id, "")` 删除父关系；`AgentInviteStore.ForAgent` 按 `created_at DESC` |
| 3 | `AgentMemoryStateStore` 可空 `summarized_until` 与 Upsert | `internal/storage/postgres/store_agent_chat_test.go` | `SummarizedUntil == nil` 与非空时间精确往返；`SessionFailures` 累加更新与 `Delete` 生效 |
| 4 | `AgentLogStore.Prune` 窗口函数按时间 + 单 Agent 上限双维裁剪 | `internal/storage/postgres/store_agent_chat_test.go` | 写入 2 租户 × 2 Agent × 新旧时间戳日志，`Prune(cutoff, 3)` 后旧于 `cutoff` 全删、每 Agent 仅留最新 3 条、另一租户日志丝毫无损 |
| 5 | `ChatStore` `agent_event` `BIGSERIAL` 并发写入与游标增量 | `internal/storage/postgres/store_agent_chat_test.go` | 16 协程并发 `AppendEvent` 返回严格递增唯一 `id`；`ListEventsAfter(sid, lastID, 50)` 无漏无重；`UpdateMessage` 仅更新匹配 `(company_id, session_id, id)` 的内容 |
| 6 | `TaskStore` `NUMERIC(16,6)` 精确往返与 8 态更新 | `internal/storage/postgres/store_task_automation_test.go` | `spend = 0.000123` / `12345.678901` 精确 6 位小数往返；`StartedAt`/`FinishedAt` 可空时间往返；`ListTasks` 按 `created_at DESC` |
| 7 | `TaskPhaseStore` JSONB 与 `TaskRunStateStore` 恢复态 | `internal/storage/postgres/store_task_automation_test.go` | JSON 数组含中文/引号在 `JSONB` 无损往返；空写入归一化为 `[]`；非法 JSON 返 `InvalidArg`；`TaskRunStateStore` 反复更新计数与 `Delete` 正确 |
| 8 | `TaskAgentStore` 空参 no-op、`TaskLinkStore` 幂等与 `TaskTagStore` 过滤 | `internal/storage/postgres/store_task_automation_test.go` | `TaskAgentStore.Link("", "a", ...)` 无副作用；`TaskLinkStore.Save` 重复 `task_id` 保持首次关系不被覆写，`Children`/`FollowUps` 按 `created_at ASC`；`TaskTagStore.All` 忽略空标签行 |
| 9 | `AutomationStore` 删除保留运行历史与 `FindRunByTask` | `internal/storage/postgres/store_task_automation_test.go` | `Delete(autoID)` 后 `Find(autoID)` 返 `(nil, nil)`，但 `ListRuns(autoID, 10)` 与 `FindRunByTask(taskID)` 仍完整返回历史触发记录 |
| 10 | 全部 14 个 Store 跨租户物理隔离与无租户 Fail-Closed | `internal/storage/postgres/store_agent_chat_test.go`, `store_task_automation_test.go` | 无租户 `context.Background()` 调用全部 14 Store 必返 `tenant.ErrMissingTenant`；双租户同 ID 写入零冲突、交叉读零可见 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race ./internal/storage/postgres -run Test.*Guard` 零外部依赖秒级全绿
- [ ] `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 全绿，覆盖率 ≥ 80%
- [ ] §2 交付物 6 项、§3 全量 17 张表/索引及 14 个 Store 编译期断言逐项核对无遗漏
- [ ] 核查 `00004`+`00005` 零 `character_key`、零 `*.tier` 废弃列，`agent_event` 已含 `id BIGSERIAL` 与 `PRIMARY KEY (company_id, id)`
- [ ] `SA-07`/`SA-08` 前置回归全绿；`00004`–`00005` 在 `db.NewTestSchema(t)` 中干净完成 `Up → Down → Up`

---

## 10. 可提交节点

- 建议 commit 1：`feat(storage): add agent/chat/memory and task/automation PG17 migrations (00004-00005, 17 tables) (spec 09, SA-09)`
- 建议 commit 2：`feat(storage): implement 14 PG17 stores for agent/chat/memory/task/automation with integration tests (spec 09, SA-09)`
