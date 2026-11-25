# spec 10 | L1 PG 存储（四）— 事件源、收件箱、SuperAgent 编排与全量 47 表 38 Store 装配收口

- **状态**：`冻结待实施`
- **篇号顺序**：10（L1 PostgreSQL 17 存储层收官篇，交付最后 1 个迁移文件 7 张表 + 6 个 Store + 全量 38 Store 聚合工厂 + 47 表端到端 `goose up/down` 验收测试）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-10`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [_epic/L1/spec_02_storage_postgres_migrations.md](../_epic/L1/spec_02_storage_postgres_migrations.md)（47 表 + 38 Store 归档母本）
  - [_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md](../_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md) §3
  - [_epic/L4/spec_12_orchestrator_superagent_dual_half.md](../_epic/L4/spec_12_orchestrator_superagent_dual_half.md) §3
- **前置依赖**：
  - `L0/spec_02`（`SA-02`：`internal/infra/db` 提供的 `db.QuerierFromCtx`、`db.TxManager`、`db.MapPGError`、`db.Migrate`/`MigrateDown`/`MigrateDownOne`、`db.NewTestSchema(t)`）
  - `L1/spec_06`（`SA-06`：`internal/core/{domain,store}` 中 SuperAgent/Inbox/EventSource/Notice 领域模型与 Store 接口）
  - `L1/spec_07`（`SA-07`）、`L1/spec_08`（`SA-08`）、`L1/spec_09`（`SA-09`）（共已交付 40 表 + 32 Store）
- **下游消费者**：`SA-33`–`SA-37`（L4 飞书/钉钉 IM 长连接收件箱分诊、个人 SuperAgent 双半编排与计划审批）、`SA-41`（L6 `cmd/synapse-server/wire.go` 装配全部 38 Store 与 `migrate` CLI 子命令）

---

## 1. 背景与范围

本篇是 `synapse-server` **L1 PostgreSQL 17 存储实现层 (`internal/storage/postgres`)** 的最终收口篇，承担两项核心职责：

1. **交付最后 1 个迁移文件（`00006_event_orchestrator.sql` · 7 张全租户隔离表）与最后 6 个 PG17 Store 实现**：
   - **SuperAgent 个人编排域（3 表 · 2 Store）**：`superagent_state`（`SuperAgentStateStore`，**V1 清理废弃列 `tracked_json`**，`plan_json` 升级 PG17 原生 `JSONB`）、`superagent_work` 与 `superagent_work_event`（`SuperAgentWorkStore`）；
   - **IM 收件箱与事件源域（3 表 · 3 Store）**：`inbox_event`（`InboxEventStore`，`InsertIfAbsent` 采用 PG17 `ON CONFLICT (company_id, id) DO NOTHING` 实现原子去重）、`inbox_todo`（`InboxTodoStore`）、`event_source`（`EventSourceStore`，**V1 彻底清理 5 个废弃空列 `secret_cipher`/`vip_names`/`urgent_keywords`/`status`/`last_error` 及历史迁移列 `secretary_agent_id`**，参照 `SecretaryCleanup.java`）；
   - **首页待办通知域（1 表 · 1 Store）**：`notice`（`NoticeStore`，承接母本 `_epic/L1/spec_02` `store_superagent_inbox.go` 6 Store 分组；写入强制按逻辑唯一键 `ON CONFLICT (company_id, owner, notice_key) DO UPDATE` 消除并发竞态，查询强制 `kind <> 'TRACK'`）。
2. **全量 47 表 + 38 Store 收口装配与端到端校验 (`stores.go`, `guard_test.go`, `e2e_migration_test.go`)**：
   - 新增 `stores.go` 冻结导出 `Stores` 聚合结构体与 `NewStores(pool *pgxpool.Pool, tx store.TxManager) *Stores`，对全部 **38 个 `store.*` 接口**进行编译期实现校验，供 `SA-41` `cmd/synapse-server/wire.go` 一行装配；
   - 升级锁定 `guard_test.go` 终态守恒断言：**精确 47 张表（2 全局 + 45 租户）**、**精确 38 个 Store 实现（2 全局 + 36 租户）**，零遗漏、零重复、零 `DROP TABLE`；
   - 编写 `e2e_migration_test.go`，在 `db.NewTestSchema(t)` 独立 PG17 schema 中对全部 6 个迁移脚本（`00001`–`00006`）执行全链路 `Up → 幂等 Up → pg_tables 48 表校验（47 业务/全局表 + 1 goose_db_version）→ 逐级 Down×6 零残表校验 → 再次 Up → NewStores 38 Store 全非 nil 装配与基础探活`。

**本篇边界**：本篇不含飞书/钉钉 WebSocket 长连接、四级分诊规则、SuperAgent 双半编排循环或待办通知事件监听（留给 `SA-33`–`SA-37`），不含 `cmd/synapse-server` CLI 与后台定时清理协程接线（留给 `SA-41`），严禁引入任何外部内存缓存组件。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考路径（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） |
|---|---|---|---|---|
| 1 | 事件源、收件箱、SuperAgent 编排与通知 7 表 PG17 迁移脚本 | `internal/storage/postgres/migrations/00006_event_orchestrator.sql` | 新增 | `MysqlSuperAgentStateStore.java:L13-L24`, `MysqlSuperAgentWorkStore.java:L13-L42`, `MysqlInboxStore.java:L16-L84`, `MysqlNoticeStore.java:L11-L28`, `superagent-event/.../SecretaryCleanup.java:L19-L45` |
| 2 | 编排、收件箱与通知 6 个 PG Store 实现 | `internal/storage/postgres/store_event_orchestrator.go` | 新增 | `AbstractJdbcSuperAgentStateStore.java`, `MysqlSuperAgentStateStore.java`, `AbstractJdbcSuperAgentWorkStore.java`, `MysqlSuperAgentWorkStore.java`, `AbstractJdbcInboxStore.java`, `MysqlInboxStore.java`, `AbstractJdbcNoticeStore.java`, `MysqlNoticeStore.java` |
| 3 | 全量 38 Store 聚合结构体 `Stores` 与 `NewStores` 装配工厂 | `internal/storage/postgres/stores.go` | 新增 | `superagent-storage/src/main/java/com/superagent/storage/jdbc/JdbcStorageConfiguration.java`（统一显式替代全部 31 个 JDBC StorageConfiguration 装配类） |
| 4 | 锁定终态 47 表 + 38 Store 守恒断言 | `internal/storage/postgres/guard_test.go` | 修改 | `superagent-storage/src/test/java/com/superagent/storage/jdbc/TenantScopeGuardTest.java:L20-L68` |
| 5 | 编排、收件箱与通知 6 Store 集成单测 | `internal/storage/postgres/store_event_orchestrator_test.go` | 新增 | `SuperAgentStateStoreTest.java`, `SuperAgentWorkStoreTest.java`, `InboxStoreTest.java`, `NoticeStoreTest.java` |
| 6 | 全量 47 表端到端 `goose Up/Down/Up` 与 38 Store 装配测试 | `internal/storage/postgres/e2e_migration_test.go` | 新增 | *(Go V1 新增：全量迁移升降级与装配验收测试)* |

---

## 3. 数据库设计（PG17 DDL · `00006_event_orchestrator.sql` · 7 张表 + 全量 47 表总账）

### 3.1 `migrations/00006_event_orchestrator.sql`（7 张表：全租户隔离）

```sql
-- +goose Up

-- 41. [TENANT] 个人 SuperAgent 状态与计划表（Java: MysqlSuperAgentStateStore.java:L13-L24；V1 移除 tracked_json，plan_json 升 JSONB）
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

-- 42. [TENANT] SuperAgent 委派工作台账表（Java: MysqlSuperAgentWorkStore.java:L13-L29, L51）
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

-- 43. [TENANT] SuperAgent 工作状态时间线表（Java: MysqlSuperAgentWorkStore.java:L32-L42）
CREATE TABLE superagent_work_event (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    task_id    VARCHAR(64)  NOT NULL,
    owner      VARCHAR(255) NOT NULL,
    kind       VARCHAR(32)  NOT NULL, -- assigned | needs_confirmation | replied | done | error | stopped | deleted
    detail     TEXT         NOT NULL DEFAULT '',
    at_time    TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_superagent_work_event_task ON superagent_work_event (company_id, task_id, at_time);

-- 44. [TENANT] IM 收件箱入站消息表（Java: MysqlInboxStore.java:L16-L41, L92）
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

-- 45. [TENANT] 收件箱提炼待办表（Java: MysqlInboxStore.java:L44-L60）
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

-- 46. [TENANT] 员工 IM 身份绑定与简报排期表
-- Java: MysqlInboxStore.java:L63-L84, AbstractJdbcInboxStore.java:L120-L125；
-- V1 彻底移除 5 个废弃空列（secret_cipher/vip_names/urgent_keywords/status/last_error）+ 历史列 secretary_agent_id（见 SecretaryCleanup.java）
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

-- 47. [TENANT] 用户待办首页通知表（Java: MysqlNoticeStore.java:L11-L28）
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

### 3.2 L1 PostgreSQL 17 全量 47 表 + 38 Store 精确守恒总账（`SA-07`–`SA-10`）

| 篇号 / PR | 迁移文件 | 包含表清单 | 表数 | 包含 Store 清单 | Store 数 |
|---|---|---|---:|---|---:|
| `spec_07` (`SA-07`) | `00001_platform_core.sql` + `00002_providers_connectors.sql` | `company`, `user_session`, `app_user`, `user_manager`, `user_team`, `system_setting`, `connector`, `connector_credential`, `model_provider`, `provider_credential` | **10** | `CompanyStore`, `SessionStore`, `UserStore`, `OrgStore`, `SystemSettingStore`, `ConnectorStore`, `ConnectorCredentialStore`, `ModelProviderStore`, `ProviderCredentialStore` | **9** |
| `spec_08` (`SA-08`) | `00003_assets_cost.sql` | `project`, `project_member`, `project_item`, `work_file`, `work_file_ref`, `file_folder`, `file_placement`, `skill`, `skill_binding`, `skill_deletion`, `task_model_choice`, `token_usage`, `turn_timing` | **13** | `ProjectStore`, `ProjectItemStore`, `WorkFileStore`, `FileFolderStore`, `SkillStore`, `SkillDeletionStore`, `TaskModelChoiceStore`, `UsageStore`, `TurnTimingStore` | **9** |
| `spec_09` (`SA-09`) | `00004_agent_chat_memory.sql` + `00005_task_automation.sql` | `agent`, `agent_member`, `agent_invite`, `agent_parent`, `agent_memory`, `agent_log`, `chat_session`, `chat_message`, `agent_event`, `task`, `task_phase`, `task_run_state`, `task_agent`, `task_link`, `task_tag`, `automation`, `automation_run` | **17** | `AgentStore`, `AgentMemberStore`, `AgentInviteStore`, `AgentParentStore`, `AgentMemoryStateStore`, `AgentLogStore`, `ChatStore`, `TaskStore`, `TaskPhaseStore`, `TaskRunStateStore`, `TaskAgentStore`, `TaskLinkStore`, `TaskTagStore`, `AutomationStore` | **14** |
| `spec_10` (`SA-10`) | `00006_event_orchestrator.sql` | `superagent_state`, `superagent_work`, `superagent_work_event`, `inbox_event`, `inbox_todo`, `event_source`, `notice` | **7** | `SuperAgentStateStore`, `SuperAgentWorkStore`, `InboxEventStore`, `InboxTodoStore`, `EventSourceStore`, `NoticeStore` | **6** |
| **合计** | **6 个迁移文件** | **2 全局表 + 45 租户隔离表（零遗漏、零重复）** | **47** | **2 全局 Store + 36 租户隔离 Store（零遗漏、零重复）** | **38** |

---

## 4. 冻结契约

### 4.1 导出符号（`internal/storage/postgres`）

```go
package postgres

import (
	"github.com/Karlsk/synapse-os/internal/core/store"
	"github.com/jackc/pgx/v5/pgxpool"
)

// 本篇（SA-10）导出的 6 个租户 Store 构造函数：
func NewSuperAgentStateStore(pool *pgxpool.Pool) store.SuperAgentStateStore
func NewSuperAgentWorkStore(pool *pgxpool.Pool) store.SuperAgentWorkStore
func NewInboxEventStore(pool *pgxpool.Pool) store.InboxEventStore
func NewInboxTodoStore(pool *pgxpool.Pool) store.InboxTodoStore
func NewEventSourceStore(pool *pgxpool.Pool) store.EventSourceStore
func NewNoticeStore(pool *pgxpool.Pool) store.NoticeStore

// Stores 一次性装配全系统全部 38 个持久化 Store 与 L0 TxManager（供 SA-41 wire.go 使用）。
// 连接池与 TxManager 由 L0/spec_02 internal/infra/db 创建并注入，本包不重复创建连接池。
type Stores struct {
	Tx                  store.TxManager
	Company             store.CompanyStore
	Session             store.SessionStore
	User                store.UserStore
	Org                 store.OrgStore
	SystemSetting       store.SystemSettingStore
	Connector           store.ConnectorStore
	ConnectorCredential store.ConnectorCredentialStore
	ModelProvider       store.ModelProviderStore
	ProviderCredential  store.ProviderCredentialStore
	Project             store.ProjectStore
	ProjectItem         store.ProjectItemStore
	WorkFile            store.WorkFileStore
	FileFolder          store.FileFolderStore
	Skill               store.SkillStore
	SkillDeletion       store.SkillDeletionStore
	TaskModelChoice     store.TaskModelChoiceStore
	Usage               store.UsageStore
	TurnTiming          store.TurnTimingStore
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
	Automation          store.AutomationStore
	SuperAgentState     store.SuperAgentStateStore
	SuperAgentWork      store.SuperAgentWorkStore
	InboxEvent          store.InboxEventStore
	InboxTodo           store.InboxTodoStore
	EventSource         store.EventSourceStore
	Notice              store.NoticeStore
}

// NewStores 接收 L0 internal/infra/db 打开的连接池与 TxManager，装配全部 38 个 Store 实现。
func NewStores(pool *pgxpool.Pool, tx store.TxManager) *Stores
```

### 4.2 路由契约

本篇为 L1 存储实现层，**不注册 HTTP 路由**。由 `SA-41` 在服务启动阶段通过 `internal/infra/db` 打开连接池后依次调用 `postgres.Migrate(ctx, pool)` 与 `postgres.NewStores(pool, txMgr)` 完成全量存储装配。

### 4.3 错误语义

严格复用 `L0/spec_02` `db.MapPGError` 与 `SA-07` 错误归一化约定：
- 全部 6 个新增 Store 均为租户隔离 Store，缺失 `companyID` 时立即返 `tenant.ErrMissingTenant`；
- 单条查找（`SuperAgentStateStore.Find`、`SuperAgentWorkStore.Find`、`InboxEventStore.FindEvent`、`InboxTodoStore.Find`、`EventSourceStore.Find`、`NoticeStore.Find`/`FindByKey`）遇 `pgx.ErrNoRows` 统一返回 `(nil, nil)`；
- `InboxEventStore.InsertIfAbsent` 遇主键重复绝不报错，正常返回 `(false, nil)`；
- `NoticeStore.Save` 按 `(company_id, owner, notice_key)` 幂等覆盖更新，不因并发相同 `notice_key` 抛出 `23505`；
- `SuperAgentStateStore.Save` 对 `PlanJSON` 传入非法 JSON 字节时，由 PG17 `22P02` 映射为 `apperror.InvalidArg`。

### 4.4 SQL 契约（本篇 6 Store 写策略、冲突语义与排序对照）

| # | Store / 表 | Java 原版写策略与源码行号 | Go PG17 冻结 SQL 写契约 | 读排序与边界过滤契约 |
|---|---|---|---|---|
| 1 | `SuperAgentStateStore` (`superagent_state`) | `ON DUPLICATE KEY` (`MysqlSuperAgentStateStore.java:L38-L39`) | `Save`: `INSERT INTO superagent_state (company_id, owner, agent_id, processed_until, plan_id, plan_status, plan_json, updated_at) VALUES ($1..$8) ON CONFLICT (company_id, owner) DO UPDATE SET agent_id=EXCLUDED.agent_id, processed_until=EXCLUDED.processed_until, plan_id=EXCLUDED.plan_id, plan_status=EXCLUDED.plan_status, plan_json=EXCLUDED.plan_json, updated_at=EXCLUDED.updated_at` | `List`: `WHERE company_id=$1 ORDER BY updated_at DESC`；`plan_json` 空切片按 SQL `NULL` 落盘 |
| 2 | `SuperAgentWorkStore` (`superagent_work`, `superagent_work_event`) | `ON DUPLICATE KEY` + `INSERT` (`MysqlSuperAgentWorkStore.java:L62-L63`) | `Save`: `INSERT INTO superagent_work (...) ON CONFLICT (company_id, task_id) DO UPDATE SET owner=..., plan_id=..., title=..., member_id=..., member_name=..., status=..., summary=..., assigned_at=..., finished_at=..., updated_at=..., message_id=...`；`AppendEvent`: `INSERT INTO superagent_work_event (...)` | `List`: `WHERE company_id=$1 AND owner=$2 ORDER BY assigned_at DESC`；`Events`: `WHERE company_id=$1 AND task_id=$2 ORDER BY at_time ASC` |
| 3 | `InboxEventStore` (`inbox_event`) | **`INSERT IGNORE`** + `REPLACE` (`MysqlInboxStore.java:L103-L113`, `AbstractJdbcInboxStore.java:L65-L81`) | `InsertIfAbsent`: **`INSERT INTO inbox_event (...) VALUES ($1..$21) ON CONFLICT (company_id, id) DO NOTHING`**（`RowsAffected()==1` 返 `true`）；`Update`: `INSERT INTO inbox_event (...) ON CONFLICT (company_id, id) DO UPDATE SET ...`；`DeleteBefore`: `DELETE FROM inbox_event WHERE company_id=$1 AND received_at < $2`（返回删除行数） | `Pending`: `WHERE company_id=$1 AND owner=$2 AND priority=$3 AND processed=FALSE ORDER BY received_at ASC LIMIT $4`；`Recent`: `WHERE company_id=$1 AND owner=$2 ORDER BY received_at DESC LIMIT $3` |
| 4 | `InboxTodoStore` (`inbox_todo`) | `REPLACE INTO` (`MysqlInboxStore.java:L118`, `AbstractJdbcInboxStore.java:L107-L116`) | `Save`: `INSERT INTO inbox_todo (...) ON CONFLICT (company_id, id) DO UPDATE SET owner=..., title=..., detail=..., requester=..., due=..., event_ids=..., source=..., status=..., task_id=..., updated_at=...` | `List`: `WHERE company_id=$1 AND owner=$2 ORDER BY created_at DESC` |
| 5 | `EventSourceStore` (`event_source`) | `REPLACE INTO` (`MysqlInboxStore.java:L123`, `AbstractJdbcInboxStore.java:L149-L164`) | `Save`: `INSERT INTO event_source (company_id, owner, type, app_id, enabled, owner_external_id, bind_code, digest_time, timezone, last_digest_date, last_hourly_at, created_at, updated_at) VALUES ($1..$13) ON CONFLICT (company_id, owner, type) DO UPDATE SET app_id=..., enabled=..., owner_external_id=..., bind_code=..., digest_time=..., timezone=..., last_digest_date=..., last_hourly_at=..., updated_at=...` | `ListEnabled`: `WHERE company_id=$1 AND type=$2 AND enabled=TRUE`；`List`: `WHERE company_id=$1 ORDER BY owner ASC, type ASC` |
| 6 | `NoticeStore` (`notice`) | `REPLACE INTO` (`MysqlNoticeStore.java:L39`, `AbstractJdbcNoticeStore.java:L43-L59`) | `Save`: **`INSERT INTO notice (...) VALUES ($1..$13) ON CONFLICT (company_id, owner, notice_key) DO UPDATE SET kind=EXCLUDED.kind, state=EXCLUDED.state, title=EXCLUDED.title, body=EXCLUDED.body, actor=EXCLUDED.actor, link_type=EXCLUDED.link_type, link_id=EXCLUDED.link_id, updated_at=EXCLUDED.updated_at`**（保留首次 `id` 与 `created_at`） | `List`: **`WHERE company_id=$1 AND owner=$2 AND kind <> 'TRACK' ORDER BY updated_at DESC LIMIT $3`**（严格对齐 `AbstractJdbcNoticeStore.java:L57-L58`） |

---

## 5. 行为语义（含 Java 源码对照与 Go 实现伪代码）

### 5.1 `InboxEventStore.InsertIfAbsent` 与 `NoticeStore.Save` / `List`

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlInboxStore.java:L102-L115`、`AbstractJdbcNoticeStore.java:L49-L59`。

```go
// InsertIfAbsent 利用 PG17 ON CONFLICT DO NOTHING 原子判重：多实例/断线重放同条飞书或钉钉消息，仅首次返回 true。
func (s *inboxEventStore) InsertIfAbsent(ctx context.Context, e domain.InboxEvent) (bool, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return false, err
	}
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
	if err != nil {
		return false, db.MapPGError("inbox_event.insert_if_absent", err)
	}
	return tag.RowsAffected() == 1, nil
}

// NoticeStore.Save 按逻辑唯一键 (company_id, owner, notice_key) 幂等更新，防止事件并发触发重复通知；
// List 严格排除 kind = 'TRACK'（仅作为内部追踪哨兵，不在首页通知列表展示）。
func (s *noticeStore) Save(ctx context.Context, n domain.Notice) error {
	cid, err := s.company(ctx)
	if err != nil {
		return err
	}
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
	return db.MapPGError("notice.save", err)
}
```

### 5.2 `EventSourceStore` — 净化列映射与 `last_hourly_at` 可空语义修复

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcInboxStore.java:L120-L146`。
- 原版在 MySQL/ClickHouse 为规避不可空时间列将 `last_hourly_at == null` 编码为 `Instant.EPOCH`（`1970-01-01`）；V1 PG17 表定义 `last_hourly_at TIMESTAMPTZ` 原生允许 `NULL`，读写直接使用 `utcPtr(s.LastHourlyAt)`，彻底消除 `EPOCH` 魔数哨兵；
- 5 个历史废弃空列与 `secretary_agent_id` 永不写入、永不存在于 DDL 中。

### 5.3 全量 38 Store 装配工厂 (`stores.go`) 与终态守卫锁定 (`guard_test.go`)

```go
// internal/storage/postgres/stores.go
package postgres

func NewStores(pool *pgxpool.Pool, tx store.TxManager) *Stores {
	if pool == nil {
		panic("postgres.NewStores: nil pgxpool.Pool")
	}
	if tx == nil {
		panic("postgres.NewStores: nil store.TxManager")
	}
	return &Stores{
		Tx:                  tx,
		Company:             NewCompanyStore(pool),
		Session:             NewSessionStore(pool),
		User:                NewUserStore(pool),
		Org:                 NewOrgStore(pool, tx),
		SystemSetting:       NewSystemSettingStore(pool),
		Connector:           NewConnectorStore(pool),
		ConnectorCredential: NewConnectorCredentialStore(pool),
		ModelProvider:       NewModelProviderStore(pool),
		ProviderCredential:  NewProviderCredentialStore(pool),
		Project:             NewProjectStore(pool),
		ProjectItem:         NewProjectItemStore(pool),
		WorkFile:            NewWorkFileStore(pool),
		FileFolder:          NewFileFolderStore(pool),
		Skill:               NewSkillStore(pool),
		SkillDeletion:       NewSkillDeletionStore(pool),
		TaskModelChoice:     NewTaskModelChoiceStore(pool),
		Usage:               NewUsageStore(pool),
		TurnTiming:          NewTurnTimingStore(pool),
		Agent:               NewAgentStore(pool),
		AgentMember:         NewAgentMemberStore(pool),
		AgentInvite:         NewAgentInviteStore(pool),
		AgentParent:         NewAgentParentStore(pool),
		AgentMemory:         NewAgentMemoryStateStore(pool),
		AgentLog:            NewAgentLogStore(pool),
		Chat:                NewChatStore(pool),
		Task:                NewTaskStore(pool),
		TaskPhase:           NewTaskPhaseStore(pool),
		TaskRunState:        NewTaskRunStateStore(pool),
		TaskAgent:           NewTaskAgentStore(pool),
		TaskLink:            NewTaskLinkStore(pool),
		TaskTag:             NewTaskTagStore(pool),
		Automation:          NewAutomationStore(pool),
		SuperAgentState:     NewSuperAgentStateStore(pool),
		SuperAgentWork:      NewSuperAgentWorkStore(pool),
		InboxEvent:          NewInboxEventStore(pool),
		InboxTodo:           NewInboxTodoStore(pool),
		EventSource:         NewEventSourceStore(pool),
		Notice:              NewNoticeStore(pool),
	}
}
```

- **`guard_test.go` 终态锁定**：在本篇收口时将 `guard_test.go` 的精确数量断言锁定为：
  - `len(allTables) == 47`（`len(globalTables) == 2` + `len(tenantTables) == 45`）；
  - `len(allStoreStructs) == 38`（`len(globalStoreReceivers) == 2` + `len(tenantStoreStructs) == 36`）；
  - 反射校验 `Stores` 结构体除 `Tx` 外的 38 个接口字段 100% 绑定对应实现，杜绝增删表/Store 未同步登记。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 新增 `internal/storage/postgres/migrations/00006_event_orchestrator.sql`（7 张全租户表），确认已移除 `superagent_state.tracked_json` 与 `event_source` 5 废弃列 + `secretary_agent_id`。
- [ ] **T2**: 编写 `internal/storage/postgres/store_event_orchestrator_test.go`（先红灯：覆盖 6 个 Store，重点测 `InboxEventStore.InsertIfAbsent` 16 协程并发去重、`NoticeStore` 按 `(owner, notice_key)` 幂等覆盖及排除 `TRACK`、`SuperAgentStateStore` JSONB 往返、跨租户隔离，使用 `db.NewTestSchema(t)`）。
- [ ] **T3**: 实现 `internal/storage/postgres/store_event_orchestrator.go`（6 个 Store），跑通 `store_event_orchestrator_test.go`。
- [ ] **T4**: 实现 `internal/storage/postgres/stores.go`（`Stores` 与 `NewStores` 装配全部 38 Store），升级 `guard_test.go` 锁定精确 **47 表 / 38 Store** 静态与 AST 守恒断言。
- [ ] **T5**: 编写并跑通 `internal/storage/postgres/e2e_migration_test.go`：在 `db.NewTestSchema(t)` 独立 PG17 schema 中验证全 6 迁移文件 `Up → 幂等 Up → 校验 48 表（47 + goose_db_version）→ Down×6 零残表 → 再次 Up → NewStores 38 Store 全非 nil 装配 + 双租户基础探活`。
- [ ] **T6**: 全量运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/... ./internal` 验证全绿且覆盖率 ≥ 80%。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-10` 状态更新为 `已实施（commit <hash>）`，标注 L1 PostgreSQL 17 存储层（47 表 + 38 Store）全部收口；
- [ ] `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §1/§4：将文中历史笔误「40 张核心表」校对为精确 **47 张表（2 全局 + 45 租户）**，同步 6 个迁移文件名 `00001_platform_core.sql` ~ `00006_event_orchestrator.sql`。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 终态 47 表 + 38 Store 静态 SQL & AST 守卫 | `internal/storage/postgres/guard_test.go` | 精确解析出 **47 张表**（2 全局 + 45 租户），`-- +goose Up` 零 `DROP TABLE`；精确解析出 **38 个 Store**（2 全局 + 36 租户），全部 36 个租户 Store 导出方法均含 `s.company(ctx)` 且每条 SQL 含 `company_id` |
| 2 | `SuperAgentStateStore` JSONB 计划与 `processed_until` 往返 | `internal/storage/postgres/store_event_orchestrator_test.go` | `PlanJSON == nil` 与复杂嵌套 JSON 精确往返；不含 `tracked_json` 列；同 `owner` 重复 `Save` 覆盖更新 |
| 3 | `SuperAgentWorkStore` 台账 Upsert 与时间线顺序 | `internal/storage/postgres/store_event_orchestrator_test.go` | `List(owner)` 按 `assigned_at DESC`；`Events(taskID)` 按 `at_time ASC`；支持追加 `message_id` 与状态演进 |
| 4 | `InboxEventStore` 并发去重、`Pending` 队列与 `DeleteBefore` | `internal/storage/postgres/store_event_orchestrator_test.go` | 16 协程并发 `InsertIfAbsent` 同 ID 仅精确 1 次返 `true`；`Pending` 仅返 `processed=FALSE` 且按 `received_at ASC`；`Recent` 按 `received_at DESC`；`DeleteBefore(cutoff)` 返回精确删除数且不影响另一租户 |
| 5 | `InboxTodoStore` & `EventSourceStore` 净化字段读写 | `internal/storage/postgres/store_event_orchestrator_test.go` | `InboxTodoStore.List` 按 `created_at DESC`；`EventSourceStore` `LastHourlyAt == nil` 与非空精确往返，`ListEnabled("feishu")` 仅返 `enabled=TRUE` |
| 6 | `NoticeStore` 唯一键并发 Upsert 与 `TRACK` 过滤 | `internal/storage/postgres/store_event_orchestrator_test.go` | 相同 `(owner, notice_key)` 换新 `id` 调用 `Save` 不报 `23505` 而原地覆盖更新；`List` 自动排除 `kind='TRACK'` 并按 `updated_at DESC LIMIT $n` |
| 7 | 全量 6 迁移文件端到端 `goose Up → Down → Up` | `internal/storage/postgres/e2e_migration_test.go` | 在 `db.NewTestSchema(t)` 独立 schema 执行全量 `Migrate` 后，查 `information_schema.tables` 精确等于 **48 张表**（47 业务/全局表 + `goose_db_version`）；重复 `Migrate` 幂等；`MigrateDownOne` 连续 6 次后零业务表残留；再次 `Migrate` 恢复 47 表 |
| 8 | `NewStores` 38 Store 完整注册装配与跨租户冒烟 | `internal/storage/postgres/e2e_migration_test.go` | 反射校验 `Stores` 全部 39 个字段（`Tx` + 38 Store）非 `nil`；通过 `Stores` 在 `acme` 与 `beta` 双租户下完成事务写入、回滚与跨租户不可见端到端验证 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race ./internal/storage/postgres -run Test.*Guard` 与 `go test -race ./internal`（`arch_test.go`）零外部依赖秒级全绿
- [ ] 面向真实 **PostgreSQL 17** 运行 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 全绿，包行覆盖率 ≥ 80%
- [ ] 端到端测试实证：全新 PG17 schema 执行 `postgres.Migrate` 后业务/全局表精确等于 **47 张**（含 `goose_db_version` 共 48 张），全量 `Down` 零残表
- [ ] `Stores` 聚合结构通过全部 **38 个 `store.*` 接口**编译期断言与运行时非空反射校验
- [ ] 核查 4 篇总账无重复、无遗漏、纯 PG17 单库零外部缓存依赖、零自定义重复连接池/事务代码（100% 复用 `L0/spec_02` `internal/infra/db`）

---

## 10. 可提交节点

- 建议 commit 1：`feat(storage): add event/inbox/superagent/notice PG17 migration (00006_event_orchestrator.sql, 7 tables) and 6 stores (spec 10, SA-10)`
- 建议 commit 2：`feat(storage): add NewStores 38-store wiring, lock 47-table guard and e2e goose up/down tests (spec 10, SA-10)`
