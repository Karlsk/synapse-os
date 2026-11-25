# spec 06 | L1 核心领域契约（二）— 运行时、Agent 对话、任务自动化、IM 收件箱与编排模型/Store/SPI

- **状态**：`draft`
- **篇号顺序**：06（L1 核心领域契约收口篇；与 `L1/spec_05` 共同完成全部领域模型、38 个 Store 接口、12+ 个跨模块/二期预留 SPI 与 9 类领域事件的无损冻结）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-06`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§4
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§3
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §1–§3
  - 归档母本：[_epic/L1/spec_01_core_tenant_crypto_egress.md](../_epic/L1/spec_01_core_tenant_crypto_egress.md) §1–§5
  - [SuperAgent/04_Agent管理_多轮对话_长期记忆与团队协作.md](../../../SuperAgent/04_Agent管理_多轮对话_长期记忆与团队协作.md)
  - [SuperAgent/05_任务执行引擎_编码工作流_并发池与定时自动化.md](../../../SuperAgent/05_异步任务引擎_研发三步流与自动化调度.md)
  - [SuperAgent/06_IM事件接入_智能分诊_收件箱与个人SuperAgent编排.md](../../../SuperAgent/06_个人SuperAgent编排器与IM收件箱分诊.md)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（依赖 `internal/core/apperror`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（依赖 `internal/core/eventbus` 与 `internal/arch_test.go`）
  - [L1/spec_05 (SA-05)](./spec_05_core_domain_platform_assets_contracts.md)（复用 `model.RunCredential`、`model.TokenUsage`、`model.UsageContext`、`model.TurnTiming`、`model.TaskModelChoice`、`spi.UsageRecorder`）
- **下游消费者**：`SA-09`（PG Agent/Task 17 表与 14 Store）、`SA-10`（PG Event/Orchestrator 6 表与 5 Store + 38 Store 全量装配）、`SA-11`–`SA-14`（L1 Runtime 执行引擎/Claude/Codex/MCP）、`SA-25`–`SA-37`（L3 路由/文档/Agent/Task 与 L4 IM/SuperAgent 编排）、`SA-38`（`/mcp` 网关）、`SA-40`（Agent/Task/IM/SuperAgent API 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

在 `L1/spec_05 (SA-05)` 冻结平台治理、供应商连接器、知识资产与成本用量契约之后，本篇（`SA-06`）承接整个系统最复杂的核心执行与编排域——**运行时执行器、Agent 多轮对话与长期记忆、8 态任务与自动化调度、企业 IM 事件与收件箱、个人 SuperAgent 双半编排**——并将其领域数据结构、剩余 19 个持久化 Store 接口、运行/MCP/IM 侧 SPI 以及 5 类业务事件全部冻结在无重依赖的 `internal/core/*` 层。

本篇的核心价值：
1. **彻底切断同层循环依赖**：通过冻结 `spi.TaskDoers`、`spi.TaskModelSelector`、`spi.InboxProcessor`、`spi.LinkReader` 与 `spi.McpToolProvider`，确保 L3 的 `internal/agent/*` 与 `internal/task/*` 永不互相 import，L4 的 `internal/event/*` 与 `internal/orchestrator/*` 仅通过窄 SPI 与 `eventbus` 协同；
2. **锁定 V1 商业私有化修复与二期演进位**：原生固化 `AgentEvent.ID int64`（PG `BIGSERIAL` 主键游标）、清理原版 4 处废弃列（`Agent.Character`/`*.Tier`/`SuperAgentState.TrackedJSON`/`EventSource` 5 废弃列），并冻结二期沙箱隔离 `spi.ProcessLauncher` 与二期企微接入 `spi.IMConnector` 接口；
3. **38 Store + 9 领域事件精确守恒闭环**：与 `SA-05` 的前 19 Store / 4 事件拼接后，精确形成全量 **38 个数据库 Store 接口（对应 47 张 PG17 表）** 与 **9 类领域事件**，零遗漏、零重复。

### 1.2 本篇边界与 V1 裁剪红线

- **本篇负责**：
  - `internal/core/model`：
    - `agent.go`：`Agent`、`AgentStatus`、`AgentKind`、`AgentMember`、`AgentInvite`、`AgentInviteStatus`、`AgentMemoryState`、`AgentLogEntry`；
    - `chat.go`：`AgentType`（`CLAUDE`/`CODEX` 为 V1 GA，其余枚举值保留作插件扩展标识）、`SessionStatus`、SessionReason 常量、`MessageRole`、`AgentEventType`、`ChatSession`、`ChatMessage`、`AgentEvent`（含 `ID int64`）、`ImageAttachment`、`AgentRunRequest`（含 `SystemPromptWithMemory()`）；
    - `task.go`：`TaskSource`、`TaskStatus`（8 态）、`TaskKind`、`TaskPhaseStatus`、`Task`、`TaskPhase`、`TaskRunState`、`TaskOrigin`、`TaskLink`、`AutomationType`、`OverlapPolicy`、`Automation`、`AutomationRun` 与 `AutomationSchedule`（`robfig/cron/v3` 5 字段 DST 安全推算）；
    - `orchestrator.go`：`SuperAgentState`、`SuperAgentWork`、`SuperAgentWorkEvent`、`EventPriority`、`EventSource`（含 `LinkedTo`）、`InboxEvent`、`InboxTodoStatus`、`InboxTodo`；
    - `helpers_runtime.go`：`LinkUrls`（平衡括号 URL 提取）与 `TurnTimer`（7 阶段耗时恒等式 + 工具耗时 FIFO 聚合）。
  - `internal/core/store`：后 **19 个租户隔离持久化 Store 接口**（覆盖 `SA-09` 的 14 个 Agent/Task Store 与 `SA-10` 的 5 个 Event/Orchestrator Store）。
  - `internal/core/spi`：运行与编排侧 SPI（`ProcessLauncher`/`ProcessHandle`/`ProcessSpec`/`TempFileSpec`、`EventSink`/`RunnerCapabilities`/`AgentRunner`/`StreamParser`、`IMConnector`、`TaskDoer`/`TaskDoers`、`TaskModelSelector`、`InboxProcessor`/`InboxProcessResult`、`LinkReader`/`LinkContent`、`TapUsage`、`McpToolDef`/`McpCallResult`/`McpToolProvider`/`McpToolRegistry`）。
  - `internal/core/eventbus/events_runtime.go`：剩余 **5 类业务领域事件**（`TaskStatusChanged`、`PlanChanged`、`InboxTodoChanged`、`AgentShared`、`InboxNotifyRequested`）。
- **本篇不负责（留给其他篇）**：
  - 平台/供应商/资产/成本领域模型与前 19 个 Store → 已由 [L1/spec_05 (SA-05)](./spec_05_core_domain_platform_assets_contracts.md) 交付；
  - PG17 迁移与 19 个 Store 的 SQL 实现 → 见 [L1/spec_09 (SA-09)](./spec_09_pg_storage_agent_task.md) 与 [L1/spec_10 (SA-10)](./spec_10_pg_storage_event_orchestrator.md)；
  - `LocalLauncher`、`ClaudeRunner`/`CodexRunner`、MCP Dispatcher 与具体业务服务实现 → 见 `SA-11`–`SA-14` 及 `L3`/`L4`。
- **硬性红线**：
  - **严禁 Redis**：本篇零 Redis 依赖或抽象；
  - **严禁恢复废弃字段**：`Agent` 绝不包含 `Character`/`CharacterKey`/`Tier`；`Task` 与 `Automation` 绝不包含 `Tier`；`SuperAgentState` 绝不包含 `TrackedJSON`；`EventSource` 绝不包含 `AppSecretCipher`/`EncryptKeyCipher`/`VerificationToken`/`BotID`/`SecretaryAgentID` 废弃列；绝不引入 `MaterialPrompt`。

### 1.3 Java 原版源码精确映射表

所有路径均经核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`：

| 目标 Go 文件 | Java 参照源码（相对 `orgin/SuperAgent/`） | 核验行号范围 | 重构与 V1 演进要点 |
|---|---|---|---|
| `internal/core/model/agent.go` | `superagent-core/src/main/java/com/superagent/core/agent/{Agent,AgentStatus,AgentKind,AgentMember,AgentInvite,AgentMemoryState,AgentLogEntry}.java` | `Agent:23-92`, `AgentMember:10-11`, `AgentInvite:11-22`, `AgentMemoryState:13-20`, `AgentLogEntry:11-19` | 移除 `character_key`（24 套性格预设）与 `tier` 废弃字段；保留 `Persona` 与 `ManagedBy` |
| `internal/core/model/chat.go` | `superagent-core/src/main/java/com/superagent/core/chat/{AgentType,SessionStatus,MessageRole,AgentEventType,ChatSession,ChatMessage,AgentEvent,ImageAttachment,AgentRunRequest}.java` | `AgentType:8-43`, `ChatSession:15-47`, `ChatMessage:9-21`, `AgentEvent:10-16`, `AgentRunRequest:18-90` | `AgentEvent` 增 `ID int64`（PG `BIGSERIAL` 主键）；`AgentRunRequest` 增 `OwnerUserID` 供二期每员工沙箱使用；1:1 保持 `SystemPromptWithMemory()` 中文提示词格式 |
| `internal/core/model/task.go` | `superagent-core/src/main/java/com/superagent/core/task/{Task,TaskSource,TaskStatus,TaskKind,TaskPhase,TaskPhaseStatus,TaskRunState,TaskOrigin,TaskLink}.java`、`automation/{Automation,AutomationRun,AutomationType,OverlapPolicy,AutomationSchedule}.java` | `Task:18-117`, `TaskKind:23-32`, `AutomationSchedule:20-88` | 移除 `Task.Tier`/`Automation.Tier`；`Spend` 采用 `decimal.Decimal`；`AutomationSchedule` 使用 `robfig/cron/v3` 5 字段解析 |
| `internal/core/model/orchestrator.go` | `superagent-core/src/main/java/com/superagent/core/agent/{SuperAgentState,SuperAgentWork,SuperAgentWorkEvent}.java`、`inbox/{EventPriority,EventSource,InboxEvent,InboxTodo}.java` | `SuperAgentState:1-42`, `EventSource:1-54`, `InboxEvent:1-60`, `InboxTodo:1-38` | `SuperAgentState` 移除 `TrackedJSON`、`PlanJSON` 用 `[]byte` 对齐 PG `JSONB`；`EventSource` 仅留 `feishu`/`dingtalk` 且移除 5 废弃空列与 `secretary_agent_id` |
| `internal/core/model/helpers_runtime.go` | `superagent-core/src/main/java/com/superagent/core/link/LinkUrls.java`、`timing/TurnTimer.java` | `LinkUrls:11-44`, `TurnTimer:19-134` | `TurnTimer` 用 `sync.Mutex` 保证并发安全与 7 阶段耗时恒等式；`LinkUrls` 完整保真平衡括号剥离 |
| `internal/core/store/{agent,task,event_orchestrator}_store.go` | `superagent-core/src/main/java/com/superagent/core/{agent/AgentStore,agent/AgentMemberStore,agent/AgentInviteStore,agent/AgentParentStore,agent/AgentMemoryStateStore,agent/AgentLogStore,chat/ChatStore,task/TaskStore,task/TaskPhaseStore,task/TaskRunStateStore,task/TaskAgentStore,task/TaskLinkStore,task/TaskTagStore,automation/AutomationStore,agent/SuperAgentStateStore,agent/SuperAgentWorkStore,inbox/InboxEventStore,inbox/InboxTodoStore,inbox/EventSourceStore}.java` | 全部 19 个 Java Store 接口 | 首参统一 `ctx context.Context`；`ChatStore.AppendEvent` 返回 `BIGSERIAL id` 并新增 `ListEventsAfter` 游标增量拉取 |
| `internal/core/spi/{runtime,agent_task,event_mcp}.go` | `superagent-core/src/main/java/com/superagent/core/{chat/AgentRunner,task/TaskDoers,task/TaskModelSelector,inbox/InboxProcessor,link/LinkReader,usage/UsageRecorder}.java` | `AgentRunner:1-32`, `TaskDoers:1-16`, `TaskModelSelector:1-28`, `InboxProcessor:1-18`, `LinkReader:1-22`, `UsageRecorder:18-28` | 冻结二期预留 SPI（`ProcessLauncher`、`IMConnector`）、`StreamParser`、`McpToolProvider`/`McpToolRegistry` 与 `TapUsage` 拦截器 |
| `internal/core/eventbus/events_runtime.go` | `superagent-core/src/main/java/com/superagent/core/{notice/NoticeEvents,inbox/InboxNotifyRequested}.java` | `NoticeEvents:7-41`, `InboxNotifyRequested:1-12` | 冻结剩余 5 类领域事件结构体，配合 `SA-04` 泛型总线使用 |

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | Agent、成员、邀请、长期记忆与运行审计日志领域模型 | `internal/core/model/agent.go` | 新增 |
| 2 | 执行引擎枚举、对话会话/消息/事件（含 `BIGSERIAL ID`）与 `AgentRunRequest` 模型 | `internal/core/model/chat.go` | 新增 |
| 3 | 任务 8 态、阶段、运行态、血缘、自动化与 `AutomationSchedule` 5 字段 Cron 推算工具 | `internal/core/model/task.go` | 新增 |
| 4 | SuperAgent 状态/工作项/事件、IM 事件源、收件箱事件与待办领域模型 | `internal/core/model/orchestrator.go` | 新增 |
| 5 | `LinkUrls` 平衡括号 URL 提取与 `TurnTimer` 7 阶段耗时聚合器 | `internal/core/model/helpers_runtime.go` | 新增 |
| 6 | 后 19 个持久化 Store 接口（Agent 7 个 + Task/Automation 7 个 + Event/Orchestrator 5 个） | `internal/core/store/agent_store.go`、`task_store.go`、`event_orchestrator_store.go` | 新增 |
| 7 | 运行/MCP/IM/编排侧 SPI（`ProcessLauncher`/`AgentRunner`/`StreamParser`/`IMConnector`/`TaskDoers`/`TaskModelSelector`/`InboxProcessor`/`LinkReader`/`TapUsage`/`McpToolProvider`/`McpToolRegistry`） | `internal/core/spi/runtime.go`、`agent_task.go`、`event_mcp.go` | 新增 |
| 8 | 剩余 5 类领域事件结构体（`TaskStatusChanged`/`PlanChanged`/`InboxTodoChanged`/`AgentShared`/`InboxNotifyRequested`） | `internal/core/eventbus/events_runtime.go` | 新增 |
| 9 | 领域模型、Cron 跨 DST、`LinkUrls`、`TurnTimer` 恒等式、`TapUsage` 与事件收发单测套件 | `internal/core/model/{agent,chat,task,orchestrator,helpers_runtime}_test.go`、`internal/core/spi/runtime_event_test.go` | 新增 |

---

## 3. 数据库设计

本篇属于 L1 核心领域契约层，**不编写 SQL DDL 或迁移脚本**。本篇为下游 [L1/spec_09 (SA-09)](./spec_09_pg_storage_agent_task.md)（17 表 / 14 Store）与 [L1/spec_10 (SA-10)](./spec_10_pg_storage_event_orchestrator.md)（6 表 / 5 Store，加 `SA-05` 侧 `notice` 表共 7 表 / 6 Store）冻结以下存储映射约束：

1. **表与 Store 精确对应**：
   - `SA-09`（17 表 / 14 Store）：`AgentStore`(`agent`), `AgentMemberStore`(`agent_member`), `AgentInviteStore`(`agent_invite`), `AgentParentStore`(`agent_parent`), `AgentMemoryStateStore`(`agent_memory`), `AgentLogStore`(`agent_log`), `ChatStore`(`chat_session`, `chat_message`, `agent_event`), `TaskStore`(`task`), `TaskPhaseStore`(`task_phase`), `TaskRunStateStore`(`task_run_state`), `TaskAgentStore`(`task_agent`), `TaskLinkStore`(`task_link`), `TaskTagStore`(`task_tag`), `AutomationStore`(`automation`, `automation_run`)。
   - `SA-10`（本篇占 6 表 / 5 Store）：`SuperAgentStateStore`(`superagent_state`), `SuperAgentWorkStore`(`superagent_work`, `superagent_work_event`), `InboxEventStore`(`inbox_event`), `InboxTodoStore`(`inbox_todo`), `EventSourceStore`(`event_source`)。
2. **字段类型强制对齐**：
   - `AgentEvent.ID` 映射 PG17 `BIGSERIAL` 主键 `(company_id, id)`，支持 `ListEventsAfter(ctx, sessionID, afterID, limit)` 高效游标分页；
   - `Task.Spend` 映射 PG17 `NUMERIC(16,6)`（`shopspring/decimal`），杜绝浮点精度漂移；
   - `SuperAgentState.PlanJSON` 与 `TaskPhaseStore` 的 `phasesJSON` 均使用 `[]byte` 对应 PG17 `JSONB`，空值传 `nil`（写入 `NULL` 或空数组由各 Store 契约明示）；
   - 所有时间字段统一使用 UTC `time.Time`（PG `TIMESTAMPTZ`），可选时间用 `*time.Time`；
   - 全部 19 个 Store 均为**租户隔离 Store**，PG 实现层必须调用 `tenant.Require(ctx)` 强制注入 `company_id`。

---

## 4. 冻结契约

### 4.1 导出符号

本篇冻结以下导出包与核心符号（详细完整定义见 §5）：
- `internal/core/model`：
  - Agent 域：`AgentStatus`, `AgentKind`, `Agent` (`IsSuper()`), `AgentMember`, `AgentInviteStatus`, `AgentInvite`, `AgentMemoryState`, `AgentLogEntry`
  - Chat/Runtime 域：`AgentType` (`CLIName()`, `ParseAgentType`), `SessionStatus`, `SessionReason*`, `MessageRole`, `AgentEventType`, `ChatSession`, `ChatMessage`, `AgentEvent`, `ImageAttachment`, `AgentRunRequest` (`IsFirstTurn()`, `SystemPromptWithMemory()`)
  - Task/Automation 域：`TaskSource`, `TaskStatus`, `TaskKind` (`ParseTaskKind`), `TaskPhaseStatus`, `Task`, `TaskPhase`, `TaskRunState`, `TaskOrigin`, `TaskLink`, `AutomationType`, `OverlapPolicy`, `Automation`, `AutomationRun`, `LoadZone`, `NormalizeCron`, `NextCron`, `UpcomingCron`, `ParseLocalTime`
  - Orchestrator/Inbox 域：`PlanStatus*`, `SuperAgentState`, `WorkStatus*`, `SuperAgentWork`, `SuperAgentWorkEvent`, `EventPriority`, `EventSourceFeishu`, `EventSourceDingTalk`, `EventSource` (`LinkedTo`), `InboxEvent`, `InboxTodoStatus`, `InboxTodo`
  - 纯工具：`ExtractLinkURLs`, `TurnTimer` (`NewTurnTimer`, `AttemptStarted`, `AttemptEnded`, `RecordFirstEvent`, `RecordFirstOutput`, `TapTools`, `Finish`)
- `internal/core/store`：上述后 19 个 Store 接口（签名逐字冻结见 §5.3）。
- `internal/core/spi`：
  - 二期预留 SPI：`TempFileSpec`, `ProcessSpec`, `ProcessHandle`, `ProcessLauncher`, `IMConnector`
  - 运行与跨模块 SPI：`EventSink`, `RunnerCapabilities`, `AgentRunner`, `StreamParser`, `TaskDoer`, `TaskDoers`, `TaskModelSelector`, `InboxProcessResult`, `InboxProcessor`, `LinkContent`, `LinkReader`, `TapUsage`
  - MCP 插件 SPI：`McpToolDef`, `McpToolContext`, `McpCallResult`, `McpToolProvider`, `McpToolRegistry`
- `internal/core/eventbus`：
  - `TaskStatusChanged`, `PlanChanged`, `InboxTodoChanged`, `AgentShared`, `InboxNotifyRequested`

### 4.2 路由契约

本篇为 L1 纯领域契约层，**不注册任何 HTTP/SSE/MCP 路由**。所有对应 HTTP 端点与 `/mcp` 端点分别由 [L5/spec_38 (SA-38)](../L5/spec_38_web_gateway_mcp_ui_embed.md) 与 [L5/spec_40 (SA-40)](../L5/spec_40_web_api_agent_task_event_orchestrator.md) 暴露。

### 4.3 错误语义

- `ParseAgentType(s)`：空串默认返回 `AgentTypeClaude`；未知枚举值返回 `apperror.InvalidArg("不支持的执行引擎类型: " + s)`（HTTP 400）；
- `LoadZone(tz)`：空串默认 `Asia/Shanghai`；非法 IANA 时区名返回 `apperror.InvalidArg`（HTTP 400）；
- `NormalizeCron(raw)` / `NextCron` / `ParseLocalTime`：非 5 字段 Cron 表达式、语法错误或时间格式非法均返回 `apperror.InvalidArg`（HTTP 400）；
- `McpCallResult`：领域执行错误（例如参数无效、权限不足、资源不存在）由 `McpToolProvider.Call` 封装为 `McpCallResult{IsError: true, Text: errMsg}` 返回，`error` 返回值仅用于协议级/不可恢复系统异常（对应 V1 修复原版 `TaskMcpTools` 异常裸抛打断 CLI 的缺陷）。

### 4.4 SQL 契约

本篇不直接编写 SQL，但对下游 `SA-09`/`SA-10` 冻结以下关键 Store 语义契约：
1. `ChatStore.AppendEvent` 必须使用 `INSERT ... RETURNING id` 返回单调递增的 `BIGSERIAL id`；`ListEventsAfter(ctx, sessionID, afterID, limit)` 必须按 `id > $afterID ORDER BY id ASC LIMIT $limit` 执行；
2. `InboxEventStore.InsertIfAbsent` 必须依赖唯一约束 `ON CONFLICT (company_id, owner, source, external_id) DO NOTHING`，返回 `(inserted bool, err error)` 以支持幂等事件去重；
3. `AgentParentStore.Set(ctx, agentID, "")` 当 `parentID == ""` 时执行 `DELETE` 清除父子关系；
4. `TaskTagStore.Set(ctx, taskID, tags)` 必须在事务内原子替换该任务的全部标签（先清空旧标签再批量写入去重后的新标签）；
5. `AgentLogStore.Prune(ctx, before, keepPerAgent)` 清理早于 `before` 的日志但每个 Agent 至少保留最新 `keepPerAgent` 条。

---

## 5. 行为语义

### 5.1 领域模型与纯工具详细定义 (`internal/core/model`)

#### A. `internal/core/model/agent.go`

```go
package model

import "time"

// Java: superagent-core/.../agent/AgentStatus.java:L4-L7, AgentKind.java:L4-L9
type AgentStatus string

const (
	AgentStatusActive  AgentStatus = "ACTIVE"
	AgentStatusDeleted AgentStatus = "DELETED"
)

type AgentKind string

const (
	AgentKindStandard AgentKind = "STANDARD"
	AgentKindSuper    AgentKind = "SUPER"
)

// Java: superagent-core/.../agent/Agent.java:L23-L92
// V1 裁剪：彻底移除 Character（原 character_key）与 Tier 字段
type Agent struct {
	ID        string
	Name      string
	Avatar    string
	Persona   string
	Model     string
	Status    AgentStatus
	Owner     string
	Kind      AgentKind
	ManagedBy string // 聘用它的 SuperAgent agent.id，用户自建为空串
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (a Agent) IsSuper() bool { return a.Kind == AgentKindSuper }

// Java: superagent-core/.../agent/AgentMember.java:L10-L11
type AgentMember struct {
	AgentID   string
	Username  string // 实际存储 app_user.id
	AddedBy   string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../agent/AgentInvite.java:L11-L22
type AgentInviteStatus string

const (
	AgentInvitePending   AgentInviteStatus = "PENDING"
	AgentInviteConfirmed AgentInviteStatus = "CONFIRMED"
	AgentInviteCancelled AgentInviteStatus = "CANCELLED"
)

type AgentInvite struct {
	ID        string
	AgentID   string
	Owner     string
	Usernames []string
	SessionID string
	MessageID string
	Status    AgentInviteStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../agent/AgentMemoryState.java:L13-L20
type AgentMemoryState struct {
	AgentID         string
	Owner           string
	SummarizedUntil *time.Time
	LastModel       string
	SessionFailures int
	Memory          string
	UpdatedAt       time.Time
}

// Java: superagent-core/.../agent/AgentLogEntry.java:L11-L19
type AgentLogEntry struct {
	ID       string
	AgentID  string
	Owner    string
	At       time.Time
	Level    string // info | warn | error
	Category string // session | turn | memory | superagent | settings
	Summary  string
	Detail   string
}
```

#### B. `internal/core/model/chat.go`

```go
package model

import (
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// Java: superagent-core/.../chat/AgentType.java:L8-L43
type AgentType string

const (
	AgentTypeClaude    AgentType = "CLAUDE"    // V1 GA
	AgentTypeCodex     AgentType = "CODEX"     // V1 GA
	AgentTypeKimi      AgentType = "KIMI"      // 预留插件扩展
	AgentTypeDeepSeek  AgentType = "DEEPSEEK"  // 预留插件扩展
	AgentTypeQwen      AgentType = "QWEN"      // 预留插件扩展
	AgentTypeGLM       AgentType = "GLM"       // 预留插件扩展
	AgentTypeGrok      AgentType = "GROK"      // 预留插件扩展
	AgentTypeCodeBuddy AgentType = "CODEBUDDY" // 预留插件扩展
	AgentTypeGemini    AgentType = "GEMINI"    // 预留插件扩展
	AgentTypeMiniMax   AgentType = "MINIMAX"   // 预留插件扩展
)

func (t AgentType) CLIName() string { return strings.ToLower(string(t)) }

func ParseAgentType(raw string) (AgentType, error) {
	s := strings.ToUpper(strings.TrimSpace(raw))
	if s == "" {
		return AgentTypeClaude, nil
	}
	switch AgentType(s) {
	case AgentTypeClaude, AgentTypeCodex, AgentTypeKimi, AgentTypeDeepSeek,
		AgentTypeQwen, AgentTypeGLM, AgentTypeGrok, AgentTypeCodeBuddy,
		AgentTypeGemini, AgentTypeMiniMax:
		return AgentType(s), nil
	default:
		return "", apperror.InvalidArg("不支持的执行引擎类型: " + raw)
	}
}

type SessionStatus string

const (
	SessionStatusActive  SessionStatus = "ACTIVE"
	SessionStatusClosed  SessionStatus = "CLOSED"
	SessionStatusDeleted SessionStatus = "DELETED"
)

const (
	SessionReasonFirst        = "first"
	SessionReasonModelChanged = "model"
	SessionReasonModelDown    = "model_down"
	SessionReasonFailures     = "failures"
)

type MessageRole string

const (
	MessageRoleUser      MessageRole = "USER"
	MessageRoleAssistant MessageRole = "ASSISTANT"
	MessageRoleSystem    MessageRole = "SYSTEM"
)

type AgentEventType string

const (
	EventSystem     AgentEventType = "SYSTEM"
	EventThinking   AgentEventType = "THINKING"
	EventText       AgentEventType = "TEXT"
	EventToolUse    AgentEventType = "TOOL_USE"
	EventToolResult AgentEventType = "TOOL_RESULT"
	EventResult     AgentEventType = "RESULT"
	EventUsage      AgentEventType = "USAGE"
	EventTokens     AgentEventType = "TOKENS" // 由 spi.TapUsage 截获计费，不落 agent_event
	EventError      AgentEventType = "ERROR"
)

// Java: superagent-core/.../chat/ChatSession.java:L15-L47
type ChatSession struct {
	ID             string
	Title          string
	AgentType      AgentType
	AgentSessionID string
	WorkspaceDir   string
	AgentID        string
	Status         SessionStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Model          string
	Reason         string
	CLIRestarts    int
}

// Java: superagent-core/.../chat/ChatMessage.java:L9-L21
type ChatMessage struct {
	ID        string
	SessionID string
	Role      MessageRole
	Content   string
	CreatedAt time.Time
	Author    string // USER 消息发言者 app_user.id
}

// Java: superagent-core/.../chat/AgentEvent.java:L10-L16
// V1 修复：增加 ID int64（PG17 BIGSERIAL 主键，支持游标增量拉取）
type AgentEvent struct {
	ID        int64
	SessionID string
	MessageID string
	Seq       int64
	Type      AgentEventType
	Payload   string
	CreatedAt time.Time
}

type ImageAttachment struct {
	Path      string
	MediaType string
}

// Java: superagent-core/.../chat/AgentRunRequest.java:L18-L90
type AgentRunRequest struct {
	SessionID       string
	AgentSessionID  string
	WorkspaceDir    string
	Prompt          string
	Model           string
	AllowedTools    []string
	SkipPermissions bool
	MCPConfig       string
	SystemPrompt    string
	Images          []ImageAttachment
	Memory          string
	Credential      *RunCredential // 定义于 SA-05 internal/core/model/provider.go
	OwnerUserID     string         // 供 ProcessLauncher 标识归属员工（二期每员工沙箱）
}

func (r AgentRunRequest) IsFirstTurn() bool { return strings.TrimSpace(r.AgentSessionID) == "" }

// SystemPromptWithMemory 严格保真 Java AgentRunRequest.java:L81-L89 的记忆注入格式。
func (r AgentRunRequest) SystemPromptWithMemory() string {
	base := strings.TrimSpace(r.SystemPrompt)
	mem := strings.TrimSpace(r.Memory)
	if mem == "" {
		return base
	}
	block := "## 长期记忆（过往对话沉淀）\n" + mem
	if base == "" {
		return block
	}
	return base + "\n\n" + block
}
```

#### C. `internal/core/model/task.go`

```go
package model

import (
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/robfig/cron/v3"
	"github.com/shopspring/decimal"
)

// Java: superagent-core/.../task/{TaskSource,TaskStatus,TaskKind,TaskPhaseStatus}.java
type TaskSource string

const (
	TaskSourceWebChat    TaskSource = "WEB_CHAT"
	TaskSourceTaskForm   TaskSource = "TASK_FORM"
	TaskSourceAutomation TaskSource = "AUTOMATION"
	TaskSourceInbox      TaskSource = "INBOX"
)

type TaskStatus string

const (
	TaskStatusPending           TaskStatus = "PENDING"
	TaskStatusRunning           TaskStatus = "RUNNING"
	TaskStatusNeedsConfirmation TaskStatus = "NEEDS_CONFIRMATION"
	TaskStatusWaiting           TaskStatus = "WAITING"
	TaskStatusDone              TaskStatus = "DONE"
	TaskStatusError             TaskStatus = "ERROR"
	TaskStatusStopped           TaskStatus = "STOPPED"
	TaskStatusDeleted           TaskStatus = "DELETED"
)

type TaskKind string

const (
	TaskKindCoding  TaskKind = "coding"
	TaskKindGeneral TaskKind = "general"
	TaskKindComplex TaskKind = "complex"
)

func ParseTaskKind(s string) TaskKind {
	switch TaskKind(strings.ToLower(strings.TrimSpace(s))) {
	case TaskKindCoding:
		return TaskKindCoding
	case TaskKindComplex:
		return TaskKindComplex
	default:
		return TaskKindGeneral
	}
}

type TaskPhaseStatus string

const (
	TaskPhasePending TaskPhaseStatus = "PENDING"
	TaskPhaseRunning TaskPhaseStatus = "RUNNING"
	TaskPhaseDone    TaskPhaseStatus = "DONE"
)

// Java: superagent-core/.../task/Task.java:L18-L117
// V1 裁剪：移除 Tier 字段；Spend 采用 decimal.Decimal 对齐 PG NUMERIC(16,6)
type Task struct {
	ID           string
	Title        string
	Brief        string
	Source       TaskSource
	Repo         string
	Branch       string
	Flow         TaskKind
	Status       TaskStatus
	Progress     int // 0..100
	Spend        decimal.Decimal
	Model        string
	AgentType    AgentType
	WorkspaceDir string
	CreatedBy    string
	Error        string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	UpdatedAt    time.Time
}

type TaskPhase struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Status   TaskPhaseStatus `json:"status"`
	Content  string          `json:"content,omitempty"`
	Evidence string          `json:"evidence,omitempty"`
}

type TaskRunState struct {
	TaskID         string
	AgentSessionID string
	FailureCount   int
	AttemptCount   int
	UpdatedAt      time.Time
}

type TaskOrigin struct {
	TaskID    string
	AgentID   string
	SessionID string
	MessageID string
}

type TaskLink struct {
	TaskID        string
	ParentID      string
	ContinuesFrom string
	CreatedAt     time.Time
}

// Java: superagent-core/.../automation/{Automation,AutomationRun,AutomationType,OverlapPolicy}.java
// V1 裁剪：移除 Automation.Tier 字段
type AutomationType string

const (
	AutomationRecurring AutomationType = "RECURRING"
	AutomationOnce      AutomationType = "ONCE"
	AutomationAPI       AutomationType = "API"
)

type OverlapPolicy string

const (
	OverlapSkip     OverlapPolicy = "SKIP"
	OverlapParallel OverlapPolicy = "PARALLEL"
)

type Automation struct {
	ID           string
	Name         string
	Prompt       string
	Type         AutomationType
	Cron         string
	RunAt        *time.Time
	Timezone     string
	Model        string
	Overlap      OverlapPolicy
	Enabled      bool
	Source       string // manual | chat
	Owner        string
	APITokenHash string
	NextRunAt    *time.Time
	LastRunAt    *time.Time
	LastTaskID   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AutomationRun struct {
	ID            string
	AutomationID  string
	Trigger       string // schedule | api | manual
	TaskID        string
	SkippedReason string
	FiredAt       time.Time
}

// --- AutomationSchedule 5 字段标准 Cron 工具（对齐 AutomationSchedule.java:L20-L88）---
var cronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func LoadZone(tz string) (*time.Location, error) {
	s := strings.TrimSpace(tz)
	if s == "" {
		s = "Asia/Shanghai"
	}
	loc, err := time.LoadLocation(s)
	if err != nil {
		return nil, apperror.InvalidArg("无效的时区: " + s)
	}
	return loc, nil
}

func NormalizeCron(raw string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(raw))
	if len(fields) != 5 {
		return "", apperror.InvalidArg("Cron 表达式必须为 5 段（分 时 日 月 周）")
	}
	norm := strings.Join(fields, " ")
	if _, err := cronParser.Parse(norm); err != nil {
		return "", apperror.InvalidArg("无效的 Cron 表达式: " + err.Error())
	}
	return norm, nil
}

func NextCron(expr, tz string, after time.Time) (*time.Time, error) {
	norm, err := NormalizeCron(expr)
	if err != nil {
		return nil, err
	}
	loc, err := LoadZone(tz)
	if err != nil {
		return nil, err
	}
	sched, err := cronParser.Parse(norm)
	if err != nil {
		return nil, apperror.InvalidArg(err.Error())
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return nil, nil
	}
	u := next.UTC()
	return &u, nil
}

func UpcomingCron(expr, tz string, after time.Time, count int) ([]time.Time, error) {
	if count <= 0 {
		return []time.Time{}, nil
	}
	norm, err := NormalizeCron(expr)
	if err != nil {
		return nil, err
	}
	loc, err := LoadZone(tz)
	if err != nil {
		return nil, err
	}
	sched, err := cronParser.Parse(norm)
	if err != nil {
		return nil, apperror.InvalidArg(err.Error())
	}
	out := make([]time.Time, 0, count)
	cur := after.In(loc)
	for range count {
		cur = sched.Next(cur)
		if cur.IsZero() {
			break
		}
		out = append(out, cur.UTC())
	}
	return out, nil
}

func ParseLocalTime(raw, tz string) (time.Time, error) {
	loc, err := LoadZone(tz)
	if err != nil {
		return time.Time{}, err
	}
	s := strings.TrimSpace(raw)
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, apperror.InvalidArg("无法解析执行时间: " + raw)
}
```

#### D. `internal/core/model/orchestrator.go`

```go
package model

import "time"

// Java: superagent-core/.../agent/{SuperAgentState,SuperAgentWork,SuperAgentWorkEvent}.java
// V1 裁剪：移除 SuperAgentState.TrackedJSON
const (
	PlanStatusProposed = "proposed"
	PlanStatusApproved = "approved"
)

type SuperAgentState struct {
	Owner          string
	AgentID        string
	ProcessedUntil *time.Time
	PlanID         string
	PlanStatus     string
	PlanJSON       []byte // PG17 JSONB（空时为 nil）
	UpdatedAt      time.Time
}

const (
	WorkStatusRunning           = "running"
	WorkStatusNeedsConfirmation = "needs_confirmation"
	WorkStatusDone              = "done"
	WorkStatusError             = "error"
	WorkStatusStopped           = "stopped"
	WorkStatusDeleted           = "deleted"
)

type SuperAgentWork struct {
	TaskID     string
	Owner      string
	PlanID     string
	Title      string
	MemberID   string
	MemberName string
	Status     string
	Summary    string
	AssignedAt time.Time
	FinishedAt *time.Time
	UpdatedAt  time.Time
	MessageID  string
}

type SuperAgentWorkEvent struct {
	ID     string
	TaskID string
	Owner  string
	Kind   string // assigned | needs_confirmation | replied | done | error | stopped | deleted
	Detail string
	At     time.Time
}

// Java: superagent-core/.../inbox/{EventPriority,EventSource,InboxEvent,InboxTodo}.java
// V1 裁剪：EventSource 移除 5 个废弃空列及 secretary_agent_id
type EventPriority string

const (
	PriorityP0     EventPriority = "P0"
	PriorityP1     EventPriority = "P1"
	PriorityP2     EventPriority = "P2"
	PriorityIgnore EventPriority = "IGNORE"
)

const (
	EventSourceFeishu   = "feishu"
	EventSourceDingTalk = "dingtalk"
)

type EventSource struct {
	Owner           string
	Type            string // feishu | dingtalk
	AppID           string
	Enabled         bool
	OwnerExternalID string
	BindCode        string
	DigestTime      string // HH:mm
	Timezone        string // IANA TZ
	LastDigestDate  string // YYYY-MM-DD（空串表示未发送过）
	LastHourlyAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s EventSource) LinkedTo(currentAppID string) bool {
	return s.OwnerExternalID != "" && currentAppID != "" && s.AppID == currentAppID
}

type InboxEvent struct {
	ID           string
	Owner        string
	Source       string
	ExternalID   string
	ChatID       string
	ChatName     string
	ChatType     string // p2p | group
	ThreadID     string
	SenderID     string
	SenderName   string
	Text         string
	MentionsMe   bool
	Raw          string
	Priority     EventPriority
	TriageReason string
	Processed    bool
	Handling     string // delivered | notified | todo | task | read | ignored
	OccurredAt   *time.Time
	ReceivedAt   time.Time
	UpdatedAt    time.Time
}

type InboxTodoStatus string

const (
	TodoOpen      InboxTodoStatus = "OPEN"
	TodoTasked    InboxTodoStatus = "TASKED"
	TodoDone      InboxTodoStatus = "DONE"
	TodoDismissed InboxTodoStatus = "DISMISSED"
)

type InboxTodo struct {
	ID        string
	Owner     string
	Title     string
	Detail    string
	Requester string
	Due       string
	EventIDs  string
	Source    string
	Status    InboxTodoStatus
	TaskID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}
```

#### E. `internal/core/model/helpers_runtime.go`（`LinkUrls` & `TurnTimer`）

1. **`ExtractLinkURLs(text string) []string`**（对齐 `LinkUrls.java:L11-L44`）：
   - 正则匹配 `https?://[^\s<>"'\x60，。；、（）【】《》「」]+`，按首次出现顺序去重；
   - 尾部循环剥离 `.,;:!?)]}*_~>`；遇到 `)` 时统计当前子串中 `(` 与 `)` 的数量，若 `open >= close`（如 Wikipedia 词条链接中的平衡括号）则停止剥离；
   - 最终长度必须 `> len("https://")`。
2. **`TurnTimer`**（对齐 `TurnTimer.java:L19-L134`）：
   - 内部持有 `mu sync.Mutex`、`clock func() int64`（默认 `time.Now().UnixMilli`）与 FIFO 队列 `open []openTool`；
   - `AttemptStarted()`：首次记 `prepEnd = now`，后续累加 `retry += now - attemptStart` 并重置单次 `firstEvent`/`firstOutput`/`runEnd`/`toolsTotal`/`open`/`tools`，`attempts++`；
   - `AttemptEnded()`：记 `runEnd = now`，并将所有未闭合 `open` 工具结算至 `now`；
   - `OnEvent(ev AgentEvent)`：记录 `firstEvent`（首个任意非系统事件）与 `firstOutput`（首个 `TEXT`/`THINKING`/`TOOL_USE`）；收到 `TOOL_USE` 压栈（空工具名归一为 `"tool"`），收到 `TOOL_RESULT` FIFO 出栈累加 `toolsTotal` 与按名聚合 `{count, ms}`；
   - `Finish(sessionID, messageID, label string) TurnTiming`：
     - `attempts == 0`：全部归入 `PrepMs = end - start`，其余耗时为 `0`；
     - 否则保证 **`PrepMs + RetryMs + StartupMs + ModelMs + ToolsMs + OutputMs + SaveMs == TotalMs()` 精确恒等式**，`Tools` 按 `Ms DESC` 排序输出。

---

### 5.2 后 19 个持久化 Store 接口 (`internal/core/store`)

> **38 Store 守恒校验**：`SA-05` 含前 19 个 Store（`CompanyStore`、`SessionStore`、`UserStore`、`OrgStore`、`SystemSettingStore`、`NoticeStore`、`ConnectorStore`、`ConnectorCredentialStore`、`ModelProviderStore`、`ProviderCredentialStore`、`ProjectStore`、`ProjectItemStore`、`WorkFileStore`、`FileFolderStore`、`SkillStore`、`SkillDeletionStore`、`TaskModelChoiceStore`、`UsageStore`、`TurnTimingStore`），本篇冻结剩余 19 个 Store，合计精确 **38 个数据库 Store 接口**。

```go
package store

import (
	"context"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/model"
)

// ==================== internal/core/store/agent_store.go (7 Stores) ====================

type AgentStore interface {
	Create(ctx context.Context, a model.Agent) (*model.Agent, error)
	Find(ctx context.Context, id string) (*model.Agent, error)
	List(ctx context.Context) ([]model.Agent, error)
	Update(ctx context.Context, a model.Agent) error
}

type AgentMemberStore interface {
	Save(ctx context.Context, m model.AgentMember) error
	Members(ctx context.Context, agentID string) ([]model.AgentMember, error)
	AgentsOf(ctx context.Context, userID string) ([]string, error)
}

type AgentInviteStore interface {
	Save(ctx context.Context, inv model.AgentInvite) error
	Find(ctx context.Context, id string) (*model.AgentInvite, error)
	ForAgent(ctx context.Context, agentID string) ([]model.AgentInvite, error)
}

type AgentParentStore interface {
	Set(ctx context.Context, agentID, parentID string) error // parentID == "" 清除
	ParentOf(ctx context.Context, agentID string) (string, bool, error)
	All(ctx context.Context) (map[string]string, error)
}

type AgentMemoryStateStore interface {
	Find(ctx context.Context, agentID string) (*model.AgentMemoryState, error)
	Save(ctx context.Context, state model.AgentMemoryState) (*model.AgentMemoryState, error)
	Delete(ctx context.Context, agentID string) error
}

type AgentLogStore interface {
	Append(ctx context.Context, entry model.AgentLogEntry) error
	List(ctx context.Context, agentID, category string, limit int) ([]model.AgentLogEntry, error)
	Prune(ctx context.Context, before time.Time, keepPerAgent int) error
	DeleteAgent(ctx context.Context, agentID string) error
}

type ChatStore interface { // chat_session + chat_message + agent_event
	CreateSession(ctx context.Context, s model.ChatSession) (*model.ChatSession, error)
	FindSession(ctx context.Context, sessionID string) (*model.ChatSession, error)
	ListSessions(ctx context.Context) ([]model.ChatSession, error)
	ListSessionsByAgent(ctx context.Context, agentID string) ([]model.ChatSession, error)
	UpdateSession(ctx context.Context, s model.ChatSession) error
	AppendMessage(ctx context.Context, m model.ChatMessage) (*model.ChatMessage, error)
	ListMessages(ctx context.Context, sessionID string) ([]model.ChatMessage, error)
	UpdateMessage(ctx context.Context, sessionID, messageID, content string) error
	AppendEvent(ctx context.Context, e model.AgentEvent) (int64, error) // 返回 PG BIGSERIAL id
	ListEvents(ctx context.Context, sessionID string) ([]model.AgentEvent, error)
	ListEventsAfter(ctx context.Context, sessionID string, afterID int64, limit int) ([]model.AgentEvent, error)
}

// ==================== internal/core/store/task_store.go (7 Stores) ====================

type TaskStore interface {
	ListTasks(ctx context.Context) ([]model.Task, error)
	FindTask(ctx context.Context, id string) (*model.Task, error)
	Save(ctx context.Context, t model.Task) (*model.Task, error)
	Delete(ctx context.Context, id string) error
}

type TaskPhaseStore interface {
	FindPhasesJSON(ctx context.Context, taskID string) ([]byte, bool, error)
	Save(ctx context.Context, taskID string, phasesJSON []byte) error
	Delete(ctx context.Context, taskID string) error
}

type TaskRunStateStore interface {
	Find(ctx context.Context, taskID string) (*model.TaskRunState, error)
	Save(ctx context.Context, s model.TaskRunState) (*model.TaskRunState, error)
	Delete(ctx context.Context, taskID string) error
}

type TaskAgentStore interface {
	Link(ctx context.Context, taskID, agentID, sessionID, messageID string) error
	TasksForAgent(ctx context.Context, agentID string) ([]string, error)
	AgentOf(ctx context.Context, taskID string) (string, bool, error)
	Origin(ctx context.Context, taskID string) (*model.TaskOrigin, error)
	OriginsForAgent(ctx context.Context, agentID string) ([]model.TaskOrigin, error)
}

type TaskLinkStore interface {
	Save(ctx context.Context, l model.TaskLink) error
	Find(ctx context.Context, taskID string) (*model.TaskLink, error)
	All(ctx context.Context) ([]model.TaskLink, error)
	Children(ctx context.Context, parentID string) ([]string, error)
	FollowUps(ctx context.Context, taskID string) ([]string, error)
}

type TaskTagStore interface {
	Set(ctx context.Context, taskID string, tags []string) error
	Tags(ctx context.Context, taskID string) ([]string, error)
	All(ctx context.Context) (map[string][]string, error)
}

type AutomationStore interface { // automation + automation_run
	List(ctx context.Context) ([]model.Automation, error)
	Find(ctx context.Context, id string) (*model.Automation, error)
	Save(ctx context.Context, a model.Automation) (*model.Automation, error)
	Delete(ctx context.Context, id string) error
	AppendRun(ctx context.Context, r model.AutomationRun) error
	ListRuns(ctx context.Context, automationID string, limit int) ([]model.AutomationRun, error)
	FindRunByTask(ctx context.Context, taskID string) (*model.AutomationRun, error)
}

// ==================== internal/core/store/event_orchestrator_store.go (5 Stores) ====================

type SuperAgentStateStore interface {
	List(ctx context.Context) ([]model.SuperAgentState, error)
	Find(ctx context.Context, owner string) (*model.SuperAgentState, error)
	Save(ctx context.Context, s model.SuperAgentState) (*model.SuperAgentState, error)
}

type SuperAgentWorkStore interface { // superagent_work + superagent_work_event
	List(ctx context.Context, owner string) ([]model.SuperAgentWork, error)
	Find(ctx context.Context, taskID string) (*model.SuperAgentWork, error)
	Save(ctx context.Context, w model.SuperAgentWork) (*model.SuperAgentWork, error)
	AppendEvent(ctx context.Context, e model.SuperAgentWorkEvent) error
	Events(ctx context.Context, taskID string) ([]model.SuperAgentWorkEvent, error)
}

type InboxEventStore interface {
	InsertIfAbsent(ctx context.Context, e model.InboxEvent) (bool, error)
	Update(ctx context.Context, e model.InboxEvent) error
	Pending(ctx context.Context, owner string, priority model.EventPriority, limit int) ([]model.InboxEvent, error)
	Recent(ctx context.Context, owner string, limit int) ([]model.InboxEvent, error)
	FindEvent(ctx context.Context, id string) (*model.InboxEvent, error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type InboxTodoStore interface {
	Save(ctx context.Context, t model.InboxTodo) error
	Find(ctx context.Context, id string) (*model.InboxTodo, error)
	List(ctx context.Context, owner string) ([]model.InboxTodo, error)
}

type EventSourceStore interface {
	Find(ctx context.Context, owner, typ string) (*model.EventSource, error)
	ListEnabled(ctx context.Context, typ string) ([]model.EventSource, error)
	List(ctx context.Context) ([]model.EventSource, error)
	Save(ctx context.Context, s model.EventSource) error
}
```

---

### 5.3 运行时、跨模块解耦与 MCP 插件 SPI (`internal/core/spi`)

```go
package spi

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/model"
	"github.com/shopspring/decimal"
)

// ==================== internal/core/spi/runtime.go ====================

// [二期预留] 进程启动与沙箱隔离接口（V1: LocalLauncher; V2: Docker/gVisor/Kata/Worker）
type TempFileSpec struct {
	Key     string      // 在 Args 中通过 ${mount:<Key>} 引用挂载后的路径
	Prefix  string
	Suffix  string
	Content []byte
	Mode    os.FileMode // 强制 0600
}

type ProcessSpec struct {
	CompanyID    string
	OwnerUserID  string
	Command      string
	Args         []string
	WorkDir      string
	Env          map[string]string
	StripEnvKeys []string
	MountFiles   []TempFileSpec
}

type ProcessHandle interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() (exitCode int, err error)
	Terminate(grace time.Duration) error
	Kill() error
	Close() error
}

type ProcessLauncher interface {
	Mode() string // "local" | "docker" | "gvisor" | "kata" | "worker"
	Start(ctx context.Context, spec ProcessSpec) (ProcessHandle, error)
}

type EventSink func(ev model.AgentEvent)

type RunnerCapabilities struct {
	Type            model.AgentType
	SupportImages   bool
	SupportStdioMCP bool
	SupportWarmPool bool
	Maturity        string // "ga" | "beta" | "experimental"
}

type AgentRunner interface {
	Type() model.AgentType
	Capabilities() RunnerCapabilities
	Run(ctx context.Context, req model.AgentRunRequest, sink EventSink) error
	RunChat(ctx context.Context, req model.AgentRunRequest, sink EventSink) error
}

type StreamParser interface {
	ParseLine(line []byte) ([]model.AgentEvent, error)
	Finish() []model.AgentEvent
	Usage(fallbackModel string) (tokens []model.TokenUsage, cliCostUSD *decimal.Decimal)
}

// TapUsage 拦截 TOKENS 事件计费并透传其余事件（对齐 UsageRecorder.java:L18-L28）
func TapUsage(ctx context.Context, rec UsageRecorder, uctx *model.UsageContext, next EventSink) EventSink {
	return func(ev model.AgentEvent) {
		if ev.Type == model.EventTokens {
			if rec != nil && uctx != nil {
				_, _ = rec.Record(ctx, *uctx, ev.Payload)
			}
			return
		}
		if next != nil {
			next(ev)
		}
	}
}

// ==================== internal/core/spi/agent_task.go ====================

type TaskDoer struct {
	Name      string
	Character string // V1 映射自 Agent.Persona 摘要
	Memory    string
}

// TaskDoers 解耦 L3 internal/task 与 internal/agent，禁止二者互引
type TaskDoers interface {
	Doer(ctx context.Context, agentID string) (*TaskDoer, error)
	Name(ctx context.Context, agentID string) (string, bool, error)
}

type TaskModelSelector interface {
	Enabled(ctx context.Context) bool
	Choose(ctx context.Context, task model.Task) (*model.TaskModelChoice, error)
	Escalate(ctx context.Context, task model.Task, failure string) (*model.TaskModelChoice, error)
	Strongest(ctx context.Context, task model.Task) (*model.TaskModelChoice, error)
}

type LinkContent struct {
	URL      string
	Title    string
	Text     string
	Via      string // "public" | "authorized"
	Complete bool
}

type LinkReader interface {
	Read(ctx context.Context, rawURL, userID string) (*LinkContent, error)
}

// ==================== internal/core/spi/event_mcp.go ====================

// [二期预留] 企业 IM 连接器接口（V1: Feishu/DingTalk; V2: WeCom）
type IMConnector interface {
	Type() string // "feishu" | "dingtalk" | "wecom"
	Start(ctx context.Context) error
	ReconnectCompany(ctx context.Context, companyID string) error
	NotifyOwner(ctx context.Context, src model.EventSource, text string) error
	Close() error
}

type InboxProcessResult struct{ NotifyText string }

type InboxProcessor interface {
	Process(ctx context.Context, src model.EventSource, window model.EventPriority, events []model.InboxEvent) (*InboxProcessResult, error)
}

// 内置 MCP 工具网关统一 SPI（供 L2/L3/L4 各域注册工具、L1 McpDispatcher 与 L5 /mcp 调度）
type McpToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	Scope       string          `json:"-"` // "chat" | "task" | "superagent" | "common"
}

type McpToolContext struct {
	CompanyID    string
	UserID       string
	AgentID      string
	SessionID    string
	MessageID    string
	TaskID       string
	WorkspaceDir string
	Role         string // "chat" | "task" | "superagent" | "external"
}

type McpCallResult struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError,omitempty"`
}

type McpToolProvider interface {
	Group() string
	Tools(ctx context.Context, tctx McpToolContext) []McpToolDef
	Call(ctx context.Context, tctx McpToolContext, name string, args json.RawMessage) (McpCallResult, error)
}

type McpToolRegistry interface {
	Register(provider McpToolProvider)
	ListTools(ctx context.Context, tctx McpToolContext) []McpToolDef
	Dispatch(ctx context.Context, tctx McpToolContext, name string, args json.RawMessage) (McpCallResult, error)
}
```

---

### 5.4 剩余 5 类领域事件结构体 (`internal/core/eventbus/events_runtime.go`)

```go
package eventbus

import "github.com/Karlsk/synapse-os/internal/core/model"

// 与 SA-05 events_platform.go 中的 4 类事件合计构成全量 9 类领域事件。
type TaskStatusChanged struct {
	TaskID  string
	Owner   string
	Title   string
	Source  model.TaskSource
	Status  model.TaskStatus
	Error   string
	AgentID string
	Result  string
}

type PlanChanged struct {
	Owner   string
	PlanID  string
	Summary string
	Open    bool
}

type InboxTodoChanged struct {
	TodoID    string
	Owner     string
	Title     string
	Detail    string
	Requester string
	Due       string
	Source    string
	Status    model.InboxTodoStatus
	TaskID    string
}

type AgentShared struct {
	AgentID   string
	AgentName string
	Username  string
	ByName    string
}

type InboxNotifyRequested struct {
	Owner string
	Text  string
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/core/model/agent_test.go` 与 `chat_test.go`（红灯）→ 落地 `agent.go`、`chat.go`（校验 `ParseAgentType` 默认 `CLAUDE` 与非法拒绝、`Agent.IsSuper()`、`AgentRunRequest.SystemPromptWithMemory()` 中文格式、无废弃字段）。
- [ ] **T2**：编写 `internal/core/model/task_test.go`（红灯）→ 落地 `task.go`（校验 `ParseTaskKind`、`AutomationSchedule` 5 字段校验、`America/New_York` 跨 DST 与 `Asia/Shanghai` `NextCron`/`UpcomingCron`/`ParseLocalTime`）。
- [ ] **T3**：编写 `internal/core/model/orchestrator_test.go` 与 `helpers_runtime_test.go`（红灯）→ 落地 `orchestrator.go` 与 `helpers_runtime.go`（校验 `EventSource.LinkedTo`、`ExtractLinkURLs` Wikipedia 平衡括号与中文标点截断、`TurnTimer` 7 阶段耗时恒等式与重复工具聚合）。
- [ ] **T4**：落地 `internal/core/store/{agent,task,event_orchestrator}_store.go`（冻结剩余 19 个持久化 Store 接口）。
- [ ] **T5**：编写 `internal/core/spi/runtime_event_test.go`（红灯）→ 落地 `internal/core/spi/{runtime,agent_task,event_mcp}.go` 与 `internal/core/eventbus/events_runtime.go`（校验 `TapUsage` 拦截 `EventTokens` 不向下游泄露、其余事件原样透传、5 类事件在 `SA-04` 总线往返无误）。
- [ ] **T6**：运行 `go build ./... && go test -race ./internal/core/... ./internal` 确认全部单测与 `arch_test.go` 守卫全绿。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：将 `SA-06` 状态更新为 `已实施（commit <hash>）`；
- [ ] 如因下游编码调整了任何 Store 或 SPI 方法签名，须同步更新本篇 §5.2/§5.3 及对应下游 Spec。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `ParseAgentType` 与 `AgentRunRequest.SystemPromptWithMemory` | `internal/core/model/chat_test.go` | 空串返 `CLAUDE`；小写 `codex` 归一为 `CODEX`；未知值返 `apperror.InvalidArg`；有/无 `Memory` 拼接中文块严格对齐 Java |
| 2 | `AutomationSchedule` 5 字段 Cron 与跨 DST 推算 | `internal/core/model/task_test.go` | 6 字段/非法 Cron 拒绝；空时区默认 `Asia/Shanghai`；`America/New_York` 夏令时切换点推算精确；`UpcomingCron` 严格递增 |
| 3 | `ExtractLinkURLs` 平衡括号与中文标点截断 | `internal/core/model/helpers_runtime_test.go` | `https://en.wikipedia.org/wiki/Go_(programming_language)` 保留尾 `)`；句子末尾 `(https://a.com/x.)` 剥离 `.)`；按出现顺序去重 |
| 4 | `TurnTimer` 7 阶段恒等式与多尝试/工具并发安全 | `internal/core/model/helpers_runtime_test.go` | 对齐 `TurnTimerTest.java`：两次尝试重试 700ms 计入 `RetryMs`、多次 `Bash` 合并计数且按 `Ms DESC` 排序、`Prep+Retry+Startup+Model+Tools+Output+Save == TotalMs()`；0 次尝试全计 `PrepMs` |
| 5 | `EventSource.LinkedTo` 与枚举边界 | `internal/core/model/orchestrator_test.go` | `AppID` 匹配且 `OwnerExternalID != ""` 返 `true`；空串或不匹配返 `false` |
| 6 | `spi.TapUsage` 拦截与 5 类事件总线往返 | `internal/core/spi/runtime_event_test.go` | `EventTokens` 仅触发 `UsageRecorder.Record` 且不调用 `next`；`EventText`/`EventToolUse` 原样透传；5 类事件通过 `eventbus.Bus` 正确投递 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/core/... ./internal` 全绿
- [ ] §2 交付物表 9 项逐一存在且路径精确匹配
- [ ] 与 `SA-05` 合计精确覆盖全部 **38 个数据库 Store 接口**、**5 大二期预留 SPI**（`ProcessLauncher`/`ModelGateway`/`Authenticator`/`IMConnector`/`OwnerTransferHook`）与 **9 类领域事件**，零重复、零遗漏
- [ ] `internal/arch_test.go` 通过：`internal/core/*` 零引入 `pgx`、`gin`、`os/exec`、上层业务包或任何 Redis 驱动
- [ ] 核对 `internal/core/model` 中零出现废弃字段（`Agent.Character`/`*.Tier`/`SuperAgentState.TrackedJSON`/`EventSource` 5 废弃空列/`MaterialPrompt`）
- [ ] §8 测试清单 6 项全部落地，包覆盖率 ≥ 85%

---

## 10. 可提交节点

- 建议 commit 1（Agent/Chat/Task/Orchestrator 领域模型与工具）：`feat(core): add agent, chat, task, orchestrator models and Cron/TurnTimer/LinkUrls helpers (spec 06, SA-06)`
- 建议 commit 2（后 19 Store、运行/MCP/IM SPI 与 5 类事件）：`feat(core): freeze remaining 19 Store interfaces, runtime/MCP/IM SPIs and runtime domain events (spec 06, SA-06)`
