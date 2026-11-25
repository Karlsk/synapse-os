# spec 08 | L1 PG 存储（二）— 资产域（项目、文件、目录、技能）与成本路由用量

- **状态**：`冻结待实施`
- **篇号顺序**：08（L1 PostgreSQL 17 存储层第二篇，承接 `SA-07` 公共基类与租户守卫，交付资产域与成本/用量/耗时持久化）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-08`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [_epic/L1/spec_02_storage_postgres_migrations.md](../_epic/L1/spec_02_storage_postgres_migrations.md)（47 表 + 38 Store 归档母本）
  - [_epic/L2/spec_06_assets_files_projects_skills.md](../_epic/L2/spec_06_assets_files_projects_skills.md) §3
  - [_epic/L3/spec_07_cost_catalog_router_usage.md](../_epic/L3/spec_07_cost_catalog_router_usage.md) §3
- **前置依赖**：
  - `L0/spec_02`（`SA-02`：`internal/infra/db` 提供的 `db.QuerierFromCtx`、`db.TxManager`、`db.MapPGError`、`db.NewTestSchema(t)`）
  - `L1/spec_05`（`SA-05`：`internal/core/{domain,store,tenant,apperror}`）
  - `L1/spec_07`（`SA-07`：`internal/storage/postgres/{migrations.go,helpers.go,guard_test.go}` 及 `00001`–`00002`）
- **下游消费者**：`SA-10`（全量 47 表端到端迁移与 38 Store 装配）、`SA-21`–`SA-25`（L2 工作文件 Local/S3、虚拟目录、协作项目、技能库、L3 模型价格目录与智能路由、Token 五维用量与耗时分析）

---

## 1. 背景与范围

本篇交付 `synapse-server` **L1 PostgreSQL 17 存储实现层 (`internal/storage/postgres`)** 的**知识资产域与成本分析域**持久化，涵盖 1 个版本化迁移脚本（**13 张全租户隔离表**）与 **9 个 PG17 Store 实现**：

1. **知识资产域（10 张表 + 6 个 Store）**：
   - **项目空间**：`project`、`project_member`（`ProjectStore`）与 `project_item`（`ProjectItemStore`）；
   - **工作文件与溯源**：`work_file`、`work_file_ref`（`WorkFileStore`），采用 PG17 `ON CONFLICT (company_id, file_id, message_id) DO NOTHING` 彻底消除 Java 原版 `AbstractJdbcWorkFileStore.java:L104-L113` 先 `SELECT COUNT(*)` 后 `INSERT` 的 TOCTOU 竞态；
   - **个人虚拟目录**：`file_folder`、`file_placement`（`FileFolderStore`），保留非破坏性软删标志；
   - **技能库与确认删卡**：`skill`、`skill_binding`（`SkillStore`，精确区分「无绑定记录 `(nil, false)`」与「显式清空绑定 `([]string{}, true)`」）及 `skill_deletion`（`SkillDeletionStore`）。
2. **成本路由、五维用量与耗时域（3 张表 + 3 个 Store）**：
   - **任务模型路由决策审计**：`task_model_choice`（`TaskModelChoiceStore`，供 `internal/cost` `TaskModelRouter` 写入不可变选型审计行）；
   - **Token 五维用量与账单**：`token_usage`（`UsageStore`，V1 彻底移除原版 ClickHouse 双 DDL，金额列 `cost_usd`/`cli_cost_usd` 统一升为 `NUMERIC(16,6)` 对齐 `shopspring/decimal` 杜绝浮点误差）；
   - **回合 7 阶段耗时与工具耗时**：`turn_timing`（`TurnTimingStore`，将原版制表符拼接文本 `tools` 列升级为 PG17 原生 `JSONB` 数组，消除特殊字符转义隐患）。
   - *说明*：模型目录价格覆盖与路由模式配置复用 `SA-07` 的 `system_setting` 表及 `model_provider.models_json`（JSONB），本篇不重复建表。

**本篇边界**：
- 本篇所有 Store 均通过 `SA-07` 建立的 `baseStore` 严格复用 `L0/spec_02` `internal/infra/db`（`db.QuerierFromCtx`、`db.MapPGError`、`db.NewTestSchema(t)`），并自动受 `SA-07` `guard_test.go` 的 SQL 静态校验与 Go AST 租户守卫约束；
- 不含文件对象读写（Local/S3 留给 `SA-21`）、目录 3 层校验（`SA-22`）、技能磁盘安装（`SA-24`）及价格目录刷新/计费公式（`SA-25`）；不引入任何外部内存缓存组件。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考路径（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） |
|---|---|---|---|---|
| 1 | 资产与成本/用量/耗时 13 表 PG17 迁移脚本 | `internal/storage/postgres/migrations/00003_assets_cost.sql` | 新增 | `MysqlProjectStore.java:L12-L35`, `MysqlProjectItemStore.java:L10-L18`, `MysqlWorkFileStore.java:L10-L41`, `MysqlFileFolderStore.java:L14-L35`, `MysqlSkillStore.java:L13-L38`, `MysqlSkillDeletionStore.java:L11-L25`, `JdbcTaskModelChoiceStore.java:L15-L29`, `JdbcUsageStore.java:L18-L41`, `MysqlTurnTimingStore.java:L11-L29` |
| 2 | 资产域 6 个 PG Store 实现（`ProjectStore`、`ProjectItemStore`、`WorkFileStore`、`FileFolderStore`、`SkillStore`、`SkillDeletionStore`） | `internal/storage/postgres/store_assets.go` | 新增 | `AbstractJdbcProjectStore.java`, `MysqlProjectStore.java`, `AbstractJdbcProjectItemStore.java`, `MysqlProjectItemStore.java`, `AbstractJdbcWorkFileStore.java`, `MysqlWorkFileStore.java`, `AbstractJdbcFileFolderStore.java`, `MysqlFileFolderStore.java`, `AbstractJdbcSkillStore.java`, `MysqlSkillStore.java`, `AbstractJdbcSkillDeletionStore.java`, `MysqlSkillDeletionStore.java` |
| 3 | 成本路由与用量/耗时 3 个 PG Store 实现（`TaskModelChoiceStore`、`UsageStore`、`TurnTimingStore`） | `internal/storage/postgres/store_cost_analytics.go` | 新增 | `JdbcTaskModelChoiceStore.java`, `JdbcUsageStore.java`, `AbstractJdbcTurnTimingStore.java`, `MysqlTurnTimingStore.java` |
| 4 | 资产域 6 Store 集成单测（含并发 `WorkFileStore.Link` 幂等、`SkillStore.Binding` 三态、跨租户隔离） | `internal/storage/postgres/store_assets_test.go` | 新增 | `ProjectStoreTest.java`, `WorkFileStoreTest.java`, `FileFolderStoreTest.java`, `SkillStoreTest.java` |
| 5 | 成本与用量/耗时 3 Store 集成单测（含 `NUMERIC(16,6)` 精度、`[from, to)` 区间、`JSONB` 工具往返） | `internal/storage/postgres/store_cost_analytics_test.go` | 新增 | `TaskModelChoiceStoreTest.java`, `UsageStoreTest.java`, `TurnTimingStoreTest.java` |

---

## 3. 数据库设计（PG17 DDL · `00003_assets_cost.sql` · 13 张表）

### 3.1 `migrations/00003_assets_cost.sql`（13 张表：全租户隔离）

```sql
-- +goose Up

-- 11. [TENANT] 协作项目表（Java: MysqlProjectStore.java:L12-L23）
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

-- 12. [TENANT] 项目成员表（Java: MysqlProjectStore.java:L25-L35）
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

-- 13. [TENANT] 项目资源归属表（Java: MysqlProjectItemStore.java:L10-L18）
CREATE TABLE project_item (
    company_id VARCHAR(64) NOT NULL,
    kind       VARCHAR(16) NOT NULL, -- AGENT | TASK | FILE
    item_id    VARCHAR(64) NOT NULL,
    project_id VARCHAR(64) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, kind, item_id)
);
CREATE INDEX idx_project_item_project ON project_item (company_id, project_id, kind);

-- 14. [TENANT] 工作文件索引表（Java: MysqlWorkFileStore.java:L10-L32, L48）
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

-- 15. [TENANT] 文件与对话回复溯源表（Java: MysqlWorkFileStore.java:L34-L41）
CREATE TABLE work_file_ref (
    company_id VARCHAR(64) NOT NULL,
    file_id    VARCHAR(64) NOT NULL,
    session_id VARCHAR(64) NOT NULL DEFAULT '',
    message_id VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, file_id, message_id)
);

-- 16. [TENANT] 个人文件虚拟目录表（Java: MysqlFileFolderStore.java:L14-L25）
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

-- 17. [TENANT] 个人文件目录放置表（Java: MysqlFileFolderStore.java:L27-L35）
CREATE TABLE file_placement (
    company_id VARCHAR(64)  NOT NULL,
    owner      VARCHAR(190) NOT NULL,
    file_id    VARCHAR(64)  NOT NULL,
    folder_id  VARCHAR(64)  NOT NULL DEFAULT '',
    placed_by  VARCHAR(16)  NOT NULL, -- user | agent | auto
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, owner, file_id)
);

-- 18. [TENANT] 可复用技能库表（Java: MysqlSkillStore.java:L13-L28, L46）
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

-- 19. [TENANT] Agent/任务技能绑定表（Java: MysqlSkillStore.java:L31-L38）
CREATE TABLE skill_binding (
    company_id VARCHAR(64) NOT NULL,
    owner_type VARCHAR(16) NOT NULL, -- agent | task
    owner_id   VARCHAR(64) NOT NULL,
    skill_ids  TEXT        NOT NULL DEFAULT '', -- 逗号分隔有序 UUID
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (company_id, owner_type, owner_id)
);

-- 20. [TENANT] 技能删除确认卡表（Java: MysqlSkillDeletionStore.java:L11-L25）
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

-- 21. [TENANT] 任务模型路由决策审计表（Java: JdbcTaskModelChoiceStore.java:L15-L29；成本路由模块直写）
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

-- 22. [TENANT] Token 五维用量与费用账单表（Java: JdbcUsageStore.java:L18-L41；V1 金额统一 NUMERIC(16,6)，去 ClickHouse）
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

-- 23. [TENANT] 回合 7 阶段耗时与工具耗时表（Java: MysqlTurnTimingStore.java:L11-L29；tools 升 PG17 JSONB）
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
DROP TABLE IF EXISTS task_model_choice;
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

> **本篇表数量核对**：`00003_assets_cost.sql` 共 **13 张表**（全租户隔离；累计 `SA-07`(10) + `SA-08`(13) = 23 张表）。

---

## 4. 冻结契约

### 4.1 导出符号（`internal/storage/postgres`）

```go
package postgres

// 本篇（SA-08）导出的 9 个租户 Store 构造函数（均复用 L0 internal/infra/db 与 SA-07 baseStore）：
func NewProjectStore(pool *pgxpool.Pool) store.ProjectStore
func NewProjectItemStore(pool *pgxpool.Pool) store.ProjectItemStore
func NewWorkFileStore(pool *pgxpool.Pool) store.WorkFileStore
func NewFileFolderStore(pool *pgxpool.Pool) store.FileFolderStore
func NewSkillStore(pool *pgxpool.Pool) store.SkillStore
func NewSkillDeletionStore(pool *pgxpool.Pool) store.SkillDeletionStore
func NewTaskModelChoiceStore(pool *pgxpool.Pool) store.TaskModelChoiceStore
func NewUsageStore(pool *pgxpool.Pool) store.UsageStore
func NewTurnTimingStore(pool *pgxpool.Pool) store.TurnTimingStore
```

编译期接口断言：
```go
var (
	_ store.ProjectStore         = (*projectStore)(nil)
	_ store.ProjectItemStore     = (*projectItemStore)(nil)
	_ store.WorkFileStore        = (*workFileStore)(nil)
	_ store.FileFolderStore      = (*fileFolderStore)(nil)
	_ store.SkillStore           = (*skillStore)(nil)
	_ store.SkillDeletionStore   = (*skillDeletionStore)(nil)
	_ store.TaskModelChoiceStore = (*taskModelChoiceStore)(nil)
	_ store.UsageStore           = (*usageStore)(nil)
	_ store.TurnTimingStore      = (*turnTimingStore)(nil)
)
```

### 4.2 路由契约

本篇为 L1 存储实现层，**不注册 HTTP 路由**。全部文件/项目/技能与成本/用量 API 路由归属 L5 Web 网关，仅通过 `internal/core/store` 接口间接消费本篇实现。

### 4.3 错误语义

沿用 `SA-07` 与 `L0/spec_02` 统一错误契约：
- 全部 9 个 Store 均为租户隔离 Store，缺失 `companyID` 时立即返回 `tenant.ErrMissingTenant`；
- 单行查询（`ProjectStore.Find`、`ProjectItemStore.ProjectOf`、`WorkFileStore.Find`、`FileFolderStore.Find`、`SkillStore.Find`/`Binding`、`SkillDeletionStore.Find`）遇 `pgx.ErrNoRows` 统一返回 `(nil, nil)` 或 `("", false, nil)` / `(nil, false, nil)`；
- `TaskModelChoiceStore.Save` 同一 `(company_id, task_id, seq)` 重复写入触发 PG `23505` → 经 `db.MapPGError` 映射为 `apperror.Conflict`；
- `TurnTimingStore.Save` 序列化 `Tools` 失败或读取非法 JSONB 时返回包装错误（绝不静默丢弃工具耗时统计）。

### 4.4 SQL 契约（本篇 9 Store 写策略、冲突语义与排序对照）

| # | Store / 表 | Java 原版写策略与源码行号 | Go PG17 冻结 SQL 写契约 | 读排序与边界过滤契约 |
|---|---|---|---|---|
| 1 | `ProjectStore` (`project`, `project_member`) | `REPLACE INTO` (`MysqlProjectStore.java:L46,L51`, `AbstractJdbcProjectStore.java:L62-L68`) | `Save`: `INSERT INTO project (...) ON CONFLICT (company_id, id) DO UPDATE SET name=..., description=..., owner=..., status=..., updated_at=..., archived_at=...`；`SaveMember`: `INSERT INTO project_member (...) ON CONFLICT (company_id, project_id, username) DO UPDATE SET added_by=..., active=..., updated_at=...` | `List`: `ORDER BY updated_at DESC`；`Members`: `WHERE company_id=$1 AND project_id=$2 AND active=TRUE ORDER BY created_at ASC`；`ProjectsOf`: `WHERE company_id=$1 AND username=$2 AND active=TRUE` |
| 2 | `ProjectItemStore` (`project_item`) | `REPLACE INTO` (`AbstractJdbcProjectItemStore.java:L29-L56`, `MysqlProjectItemStore.java`) | `Assign`: `INSERT INTO project_item (company_id, kind, item_id, project_id, updated_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (company_id, kind, item_id) DO UPDATE SET project_id=EXCLUDED.project_id, updated_at=EXCLUDED.updated_at`；`Unassign`: `DELETE FROM project_item WHERE company_id=$1 AND kind=$2 AND item_id=$3` | `Items`: `WHERE company_id=$1 AND project_id=$2 AND kind=$3 ORDER BY updated_at DESC` |
| 3 | `WorkFileStore` (`work_file`, `work_file_ref`) | `REPLACE INTO` + 查后插 (`AbstractJdbcWorkFileStore.java:L56-L130`, `MysqlWorkFileStore.java:L60`) | `Save`: `INSERT INTO work_file (...) ON CONFLICT (company_id, id) DO UPDATE SET name=..., kind=..., mime_type=..., size=..., storage=..., location=..., owner=..., agent_id=..., task_id=..., origin=..., deleted=..., updated_at=..., tags=..., tag_state=...`；`Link`: **`INSERT INTO work_file_ref (company_id, file_id, session_id, message_id, created_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (company_id, file_id, message_id) DO NOTHING`**（原子幂等，保留首次位置） | `ListFor`: `deleted=FALSE AND (owner=$2 OR agent_id = ANY($3::text[])) ORDER BY updated_at DESC`；`Untagged`: `deleted=FALSE AND tag_state='' ORDER BY created_at ASC LIMIT $2`（`limit < 1` 钳为 `1`）；`Messages`: 空 `fileIDs` 直接返空 map，否则 `file_id = ANY($2::text[]) ORDER BY created_at ASC`；`CountByStorage`: `deleted=FALSE GROUP BY storage` |
| 4 | `FileFolderStore` (`file_folder`, `file_placement`) | `REPLACE INTO` (`MysqlFileFolderStore.java:L46,L51`, `AbstractJdbcFileFolderStore.java:L50-L73`) | `Save`: `INSERT INTO file_folder (...) ON CONFLICT (company_id, id) DO UPDATE SET owner=..., name=..., parent_id=..., deleted=..., updated_at=...`；`Place`: `INSERT INTO file_placement (...) ON CONFLICT (company_id, owner, file_id) DO UPDATE SET folder_id=..., placed_by=..., updated_at=...` | `Find`/`List`/`Owners`: 均过滤 **`deleted = FALSE`**；`List`: `ORDER BY name ASC`；`Owners`: `SELECT DISTINCT owner`；`Placements`: `WHERE company_id=$1 AND owner=$2` |
| 5 | `SkillStore` (`skill`, `skill_binding`) | `ON DUPLICATE KEY` (`MysqlSkillStore.java:L57-L66`, `AbstractJdbcSkillStore.java:L54-L70`) | `Save`: `INSERT INTO skill (...) ON CONFLICT (company_id, id) DO UPDATE SET name=..., description=..., category=..., content=..., space=..., enabled=..., deleted=..., updated_by=..., updated_at=...`；`Bind`: `INSERT INTO skill_binding (company_id, owner_type, owner_id, skill_ids, updated_at) VALUES ($1,$2,$3,$4,$5) ON CONFLICT (company_id, owner_type, owner_id) DO UPDATE SET skill_ids=EXCLUDED.skill_ids, updated_at=EXCLUDED.updated_at` | `List`: `ORDER BY name ASC`（含软删，由上层按权限/可见性过滤）；`Binding`: 无行返 `(nil, false, nil)`，空串行返 `([]string{}, true, nil)` |
| 6 | `SkillDeletionStore` (`skill_deletion`) | `REPLACE INTO` (`MysqlSkillDeletionStore.java:L36`, `AbstractJdbcSkillDeletionStore.java:L50`) | `Save`: `INSERT INTO skill_deletion (...) ON CONFLICT (company_id, id) DO UPDATE SET skill_id=..., skill_name=..., agent_id=..., requester=..., session_id=..., message_id=..., status=..., updated_at=...` | `ForAgent`: `WHERE company_id=$1 AND agent_id=$2 ORDER BY created_at DESC` |
| 7 | `TaskModelChoiceStore` (`task_model_choice`) | `INSERT` (`JdbcTaskModelChoiceStore.java:L42-L54`) | `Save`: `INSERT INTO task_model_choice (company_id, task_id, seq, decided_by, difficulty, tier, model, cli, judge, candidates, reasoning, created_at) VALUES ($1..$12)`（不可变追加审计行） | `List`: `WHERE company_id=$1 AND task_id=$2 ORDER BY seq ASC` |
| 8 | `UsageStore` (`token_usage`) | `INSERT` (`JdbcUsageStore.java:L71-L93`) | `Save`: `INSERT INTO token_usage (company_id, id, at, user_name, source, agent_id, task_id, session_id, cli, model, input_tokens, cache_read, cache_write, cache_write_1h, output_tokens, reasoning, cost_usd, cli_cost_usd) VALUES ($1..$18)` | `ForTask`: `WHERE company_id=$1 AND task_id=$2 ORDER BY at ASC`；`Between`: `at >= $2 AND at < $3`（`userID != ""` 追加 `AND user_name = $4`）`ORDER BY at ASC` |
| 9 | `TurnTimingStore` (`turn_timing`) | `REPLACE INTO` (`MysqlTurnTimingStore.java:L40`, `AbstractJdbcTurnTimingStore.java:L62-L72`) | `Save`: `INSERT INTO turn_timing (company_id, message_id, session_id, label, started_at, finished_at, prep_ms, startup_ms, model_ms, tools_ms, output_ms, retry_ms, save_ms, attempts, tools) VALUES ($1..$15) ON CONFLICT (company_id, message_id) DO UPDATE SET ...` | `ForSessions`: 空切片立即返 `[]domain.TurnTiming{}, nil`（仍先检验租户），否则 `session_id = ANY($2::text[]) ORDER BY started_at ASC` |

---

## 5. 行为语义（含 Java 源码对照与 Go 实现伪代码）

### 5.1 `WorkFileStore` — 消除 TOCTOU 的原子 `Link` 与 `ANY($3::text[])` 统一可见查询

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcWorkFileStore.java:L56-L130`。

```go
// Link 用 PG17 ON CONFLICT DO NOTHING 替代 Java L104-L113 的 SELECT COUNT + INSERT，
// 保证同一回复重复修改同一文件时，保留首次插入时间戳且并发零竞态。
func (s *workFileStore) Link(ctx context.Context, fileID, sessionID, messageID string) error {
	cid, err := s.company(ctx)
	if err != nil {
		return err
	}
	_, err = s.q(ctx).Exec(ctx, `
		INSERT INTO work_file_ref (company_id, file_id, session_id, message_id, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (company_id, file_id, message_id) DO NOTHING`,
		cid, fileID, sessionID, messageID, utc(time.Now()),
	)
	return db.MapPGError("work_file_ref.link", err)
}

// ListFor 利用 PG17 数组参数 ANY($3::text[])：即使 agentIDs 为空切片，ANY('{}'::text[]) 也自然求值 false，无需动态拼接占位符。
func (s *workFileStore) ListFor(ctx context.Context, owner string, agentIDs []string) ([]domain.WorkFile, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	if agentIDs == nil {
		agentIDs = []string{}
	}
	rows, err := s.q(ctx).Query(ctx, `
		SELECT id, name, kind, mime_type, size, storage, location, owner, agent_id,
		       task_id, origin, deleted, created_at, updated_at, tags, tag_state
		  FROM work_file
		 WHERE company_id = $1
		   AND deleted = FALSE
		   AND (owner = $2 OR agent_id = ANY($3::text[]))
		 ORDER BY updated_at DESC`,
		cid, owner, agentIDs,
	)
	if err != nil {
		return nil, db.MapPGError("work_file.list_for", err)
	}
	defer rows.Close()
	return scanWorkFiles(rows) // tags 使用 splitLines 解析
}

func (s *workFileStore) Messages(ctx context.Context, fileIDs []string) (map[string][]string, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]string)
	if len(fileIDs) == 0 {
		return out, nil
	}
	rows, err := s.q(ctx).Query(ctx, `
		SELECT file_id, message_id
		  FROM work_file_ref
		 WHERE company_id = $1 AND file_id = ANY($2::text[])
		 ORDER BY created_at ASC`, cid, fileIDs)
	if err != nil {
		return nil, db.MapPGError("work_file_ref.messages", err)
	}
	defer rows.Close()
	for rows.Next() {
		var fid, mid string
		if err := rows.Scan(&fid, &mid); err != nil {
			return nil, db.MapPGError("work_file_ref.messages.scan", err)
		}
		out[fid] = append(out[fid], mid)
	}
	return out, db.MapPGError("work_file_ref.messages.rows", rows.Err())
}
```

### 5.2 `SkillStore.Binding` — 三态精确区分

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcSkillStore.java:L65-L70`、`MysqlSkillStore.java:L65-L66`。

```go
// Binding 严格区分三态：
// 1. 无行 -> (nil, false, nil)：调用方视为「从未显式绑定，遵循默认继承规则」；
// 2. 有行且 skill_ids == "" -> ([]string{}, true, nil)：调用方视为「显式解绑全部技能」；
// 3. 有行且非空 -> (strings.Split(csv, ","), true, nil)：按存储顺序返回有序 UUID 列表。
func (s *skillStore) Binding(ctx context.Context, ownerType, ownerID string) ([]string, bool, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, false, err
	}
	var csv string
	err = s.q(ctx).QueryRow(ctx, `
		SELECT skill_ids
		  FROM skill_binding
		 WHERE company_id = $1 AND owner_type = $2 AND owner_id = $3`,
		cid, ownerType, ownerID,
	).Scan(&csv)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, db.MapPGError("skill_binding.get", err)
	}
	return splitCSV(csv), true, nil
}
```

### 5.3 `UsageStore` — 定点数 `NUMERIC(16,6)` 精准读写与左闭右开 `[from, to)` 窗口

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/JdbcUsageStore.java:L70-L93`。
> **V1 升级**：用 `*decimal.Decimal`（经文本格式或 `pgtype.Numeric` 编解码）读写 `NUMERIC(16,6)`，彻底消除 Java `DOUBLE` 二进制浮点误差；空 `userID` 查全租户明细，非空精确过滤 `user_name = $4`。

```go
func decimalToNullableString(d *decimal.Decimal) *string {
	if d == nil {
		return nil
	}
	s := d.StringFixed(6)
	return &s
}

func nullableStringToDecimal(s *string) (*decimal.Decimal, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	v, err := decimal.NewFromString(*s)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (s *usageStore) Between(ctx context.Context, userID string, from, to time.Time) ([]domain.UsageRecord, error) {
	cid, err := s.company(ctx)
	if err != nil {
		return nil, err
	}
	var (
		rows pgx.Rows
	)
	if userID == "" {
		rows, err = s.q(ctx).Query(ctx, `
			SELECT id, at, user_name, source, agent_id, task_id, session_id, cli, model,
			       input_tokens, cache_read, cache_write, cache_write_1h, output_tokens, reasoning,
			       cost_usd::text, cli_cost_usd::text
			  FROM token_usage
			 WHERE company_id = $1 AND at >= $2 AND at < $3
			 ORDER BY at ASC`, cid, utc(from), utc(to))
	} else {
		rows, err = s.q(ctx).Query(ctx, `
			SELECT id, at, user_name, source, agent_id, task_id, session_id, cli, model,
			       input_tokens, cache_read, cache_write, cache_write_1h, output_tokens, reasoning,
			       cost_usd::text, cli_cost_usd::text
			  FROM token_usage
			 WHERE company_id = $1 AND at >= $2 AND at < $3 AND user_name = $4
			 ORDER BY at ASC`, cid, utc(from), utc(to), userID)
	}
	if err != nil {
		return nil, db.MapPGError("token_usage.between", err)
	}
	defer rows.Close()
	return scanUsageRecords(rows)
}
```

### 5.4 `TurnTimingStore` — PG17 `JSONB` 替代制表符拼接

> **Java 源码对照**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/AbstractJdbcTurnTimingStore.java:L31-L72`、`MysqlTurnTimingStore.java:L40`。
- 写入前若 `t.Tools == nil` 归一化为空 JSON 数组 `[]`（保证 `tools` 列恒为合法 JSON 数组而非 `null`）；
- 读取时 `json.Unmarshal` 还原 `[]domain.ToolTime`，支持任意中文、制表符、换行工具名无损往返；
- `ForSessions(ctx, []string{})` 在完成 `s.company(ctx)` 租户校验后直接返回空切片，不发起无意义 DB 查询。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 新增迁移脚本 `internal/storage/postgres/migrations/00003_assets_cost.sql`（13 张全租户表与全部索引），运行 `guard_test.go` 校验全部表/索引符合 `company_id` 守卫规则。
- [ ] **T2**: 编写 `internal/storage/postgres/store_assets_test.go`（先红灯：覆盖 6 个资产 Store 的 CRUD、并发 `Link`、`Binding` 三态、`FileFolder` 软删与跨租户隔离，使用 `db.NewTestSchema(t)`）。
- [ ] **T3**: 实现 `internal/storage/postgres/store_assets.go`（`ProjectStore`、`ProjectItemStore`、`WorkFileStore`、`FileFolderStore`、`SkillStore`、`SkillDeletionStore`），跑通 `store_assets_test.go` 与 `guard_test.go`。
- [ ] **T4**: 编写 `internal/storage/postgres/store_cost_analytics_test.go`（先红灯：覆盖 `TaskModelChoiceStore` 追加与顺序读、`UsageStore` `NUMERIC(16,6)` 精度与 `[from, to)` 边界、`TurnTimingStore` JSONB 工具列表往返）。
- [ ] **T5**: 实现 `internal/storage/postgres/store_cost_analytics.go`（`TaskModelChoiceStore`、`UsageStore`、`TurnTimingStore`），跑通 `store_cost_analytics_test.go`。
- [ ] **T6**: 全包回归 `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 验证 `SA-07` + `SA-08`（23 表 / 18 Store）全绿且覆盖率 ≥ 80%。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-08` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §4：同步迁移脚本 `00003_assets_cost.sql` 的 13 表归属清单。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `00003_assets_cost.sql` 静态租户守卫 + AST 守卫 | `internal/storage/postgres/guard_test.go` | 13 张表均含 `company_id VARCHAR(64) NOT NULL` 且为 PK/索引首列；9 个新增 Store 全部导出方法均调 `s.company(ctx)` 且 SQL 含 `company_id` |
| 2 | `ProjectStore` & `ProjectItemStore` 成员活跃过滤与资源重分配 | `internal/storage/postgres/store_assets_test.go` | `SaveMember(active=false)` 后 `Members`/`ProjectsOf` 立即排除；`Assign` 将同一 `(kind, item_id)` 切至新 `project_id` 幂等覆盖，`Unassign` 精准删除 |
| 3 | `WorkFileStore.ListFor` / `Untagged` / `CountByStorage` | `internal/storage/postgres/store_assets_test.go` | 空 `agentIDs` 与多 `agentIDs` 均返回正确可见集并排除 `deleted=TRUE`；`Untagged(limit)` 仅返 `tag_state=''` 按 `created_at ASC`；`CountByStorage` 排除软删行 |
| 4 | `WorkFileStore.Link` 并发幂等与 `Messages` 顺序 | `internal/storage/postgres/store_assets_test.go` | 16 协程并发调用 `Link(fid, sid, mid)` 零冲突零重复，`Messages([]string{fid})` 仅返回 1 条且按 `created_at ASC` 排序；空切片 `Messages(nil)` 不报错返空 map |
| 5 | `FileFolderStore` 软删过滤与 `Placements` 覆盖 | `internal/storage/postgres/store_assets_test.go` | `Save(deleted=true)` 后 `Find`/`List`/`Owners` 均排除；`Place` 反复变更 `folder_id`（含空串回根目录）幂等覆盖 |
| 6 | `SkillStore` 绑定三态与 `SkillDeletionStore` 排序 | `internal/storage/postgres/store_assets_test.go` | 未绑定 `Binding` 返 `(nil, false, nil)`；`Bind(..., []string{})` 后返 `([]string{}, true, nil)`；`Bind(..., ["s2","s1"])` 严格保序 `["s2","s1"]`；`SkillDeletionStore.ForAgent` 按 `created_at DESC` |
| 7 | `TaskModelChoiceStore` 追加顺序与冲突检测 | `internal/storage/postgres/store_cost_analytics_test.go` | `seq=1,2,3` 写入后 `List` 严格 `ORDER BY seq ASC`；重复 `seq` 返回 `apperror.CodeConflict` |
| 8 | `UsageStore` `NUMERIC(16,6)` 精确往返与 `[from, to)` 区间 | `internal/storage/postgres/store_cost_analytics_test.go` | `0.000123` 与 `nil`（未定价模型）精确往返 6 位小数；`at == from` 包含、`at == to` 排除；`userID==""` 返全租户，`userID!=""` 仅返该员工 |
| 9 | `TurnTimingStore` JSONB 特殊字符无损往返与空会话短路 | `internal/storage/postgres/store_cost_analytics_test.go` | 工具名含中文、`\t`、`\n`、双引号在 PG17 JSONB 中无损往返；`Tools == nil` 存为 `[]`；`ForSessions(nil)` 返空切片且校验租户 |
| 10 | 全部 9 个 Store 跨租户隔离与无租户 Fail-Closed | `internal/storage/postgres/store_assets_test.go`, `store_cost_analytics_test.go` | 无租户 `context.Background()` 调用全部 9 Store 必返 `tenant.ErrMissingTenant`；`acme` 与 `beta` 同 ID 写入互不冲突且零可见 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race ./internal/storage/postgres -run Test.*Guard` 零外部依赖秒级全绿
- [ ] `TEST_POSTGRES_DSN=... go test -race -cover ./internal/storage/postgres/...` 全绿，覆盖率 ≥ 80%
- [ ] §2 交付物 5 项、§3 `00003_assets_cost.sql` 全部 13 张表/索引及 9 个 Store 编译期断言逐项核对无遗漏
- [ ] `SA-07` 前置回归测试全绿；`00003` 可在 `db.NewTestSchema(t)` 中干净完成 `Up → Down → Up`

---

## 10. 可提交节点

- 建议 commit 1：`feat(storage): add assets and cost PG17 migration (00003_assets_cost.sql, 13 tables) (spec 08, SA-08)`
- 建议 commit 2：`feat(storage): implement 9 PG17 stores for projects/files/folders/skills/cost/usage/timing (spec 08, SA-08)`
