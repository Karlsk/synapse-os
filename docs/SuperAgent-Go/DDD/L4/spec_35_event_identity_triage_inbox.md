# spec 35 | L4 IM 接入层 — 7 步四级分诊引擎、收件箱批处理、4 态待办镜像与 6 个 Inbox MCP 工具

- **状态**：`draft`
- **篇号顺序**：35（L4 事件接入层收口篇；交付确定性 7 步四级分诊、UUIDv3 去重摄入、P0 秒级直推兜底、待办与首页 Notice 双向联动及 6 个 Inbox MCP 工具）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-35`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §3.2
  - 归档母本：[_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md](../_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md) §4.2, §5.1–§5.2
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-event/src/main/java/com/superagent/event/TriageRules.java` (`L1-L65`)
    - `superagent-event/src/main/java/com/superagent/event/InboxService.java` (`L1-L279`)
    - `superagent-event/src/main/java/com/superagent/event/UserInboxService.java` (`L1-L232`)
    - `superagent-event/src/main/java/com/superagent/event/InboxMcpTools.java` (`L1-L122`)
    - `superagent-web/src/main/java/com/superagent/web/inbox/InboxController.java` (`L1-L56`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md)（`EventSourceStore`/`InboxEventStore`/`InboxTodoStore`）
  - [L2/spec_16 (SA-16)](../L2/spec_16_platform_user_org_quota.md)（`OrgStore`/`UserStore`）& [L2/spec_17 (SA-17)](../L2/spec_17_platform_settings_notice_analytics.md)（`NoticeIgnored`/`InboxTodoChanged` 事件联动）
  - [L3/spec_30 (SA-30)](../L3/spec_30_task_state_codingflow_complex.md)（`TaskService.Create` 供待办一键转任务）
  - [L4/spec_33 (SA-33)](./spec_33_event_im_connector_feishu.md) & [L4/spec_34 (SA-34)](./spec_34_event_dingtalk_connector.md)
- **下游消费者**：`SA-36`（`SuperAgentInboxProcessor` 实现 `spi.InboxProcessor` 承接本篇投递的分诊批次）、`SA-38`（注册 6 个 Inbox MCP 工具）、`SA-40`（Inbox HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

IM 群消息体量巨大且噪音极多，若直接把全部消息丢给大模型既烧钱又拖慢关键提醒。本篇（`SA-35`）交付零成本、确定性、可解释的收件箱分诊与待办闭环（1.5 人天 PR）：
1. **7 步四级确定性分诊（`P0`/`P1`/`P2`/`IGNORE`）**：结合企业真实汇报树（`OrgStore.Managers` 动态识别“上级/下属”），用群噪防误报守卫避免普通群聊出现“紧急”二字就误触 P0；
2. **UUIDv3 原子去重 + P0 秒级 2-Worker 即时池 + 10m 失败退避 + 30 天保留清理**：IM 重投静默幂等；P0 秒级送达 `spi.InboxProcessor` 并把返回的原文告警经对应 `IMConnector` 秒级私聊直推员工兜底；
3. **4 态 `InboxTodo` 待办 ↔ 首页 `Notice` 双向镜像 + 6 个 `SUPER` 域 MCP 工具**：支持员工界面勾选完成/忽略/一键转后台任务，并在首页忽略待办时反向同步 `DISMISSED`。

### 1.2 本篇边界

- **本篇负责**：`internal/event/{triage.go,inbox_service.go,user_inbox_service.go,mcp_tools.go}` 及全套单测。
- **本篇不负责**：飞书/钉钉连接器（已由 `SA-33`/`SA-34` 交付）；`spi.InboxProcessor` 具体实现（由 `SA-36` 实现，严格依赖倒置禁止 `event` import `orchestrator`）；Gin 路由挂载（交由 `SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 确定性 7 步四级分诊引擎 `TriageRules`（含 8 紧急词、17 确认词、纯标点 Emoji 判定） | `internal/event/triage.go` | 新增 |
| 2 | `InboxService`（UUIDv3 去重摄入、2-Worker P0 即时池、60s P0/P1/P2 巡检、10m 退避、30 天清理与 `InboxNotifyRequested` 扇出） | `internal/event/inbox_service.go` | 新增 |
| 3 | `UserInboxService`（摘要/事件/待办、4 态流转、幂等转任务 `ToTask`、`OnNoticeIgnored` 双向镜像） | `internal/event/user_inbox_service.go` | 新增 |
| 4 | `InboxMcpTools`（全部 **6 个收件箱 MCP 工具**，作用域 `SUPER`） | `internal/event/mcp_tools.go` | 新增 |
| 5 | 分诊 7 步边界、批处理退避、待办镜像与 6 MCP 工具单测套件 | `internal/event/{triage,inbox_service,user_inbox_service,mcp_tools}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md) 已落盘的 `event_source`、`inbox_event`（主键 `(company_id, id)` 其中 `id = UUIDv3(source:external_id:owner)` 支撑 `ON CONFLICT DO NOTHING` 原子去重）与 `inbox_todo`。`failedUntil` 退避表与按用户串行互斥锁为纯进程内结构（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package event

type TriageDecision struct {
	Priority model.EventPriority
	Reason   string
}

func Triage(e model.InboxEvent, fromOwner, fromBot bool, people map[string]string) TriageDecision
func DeterministicEventID(source, externalID, owner string) string // RFC 4122 MD5 UUIDv3

type InboxService struct { /* cfg, sources, events, org, users, companies, processor spi.InboxProcessor, registry *im.Registry, urgentCh, ownerLocks, failedUntil */ }
func (s *InboxService) Start(bg *lifecycle.BackgroundGroup)
func (s *InboxService) Ingest(ctx context.Context, src model.EventSource, e model.InboxEvent, fromOwner, fromBot bool) (*model.InboxEvent, error)
func (s *InboxService) OnNotify(ctx context.Context, req eventbus.InboxNotifyRequested)

type UserInboxService struct { /* sources, events, todos, tasks TaskCreator, bus */ }
func (s *UserInboxService) RegisterHandlers(bus *eventbus.Bus) // 监听 NoticeIgnored
func (s *UserInboxService) Summary(ctx context.Context, owner string) (*InboxSummaryDto, error)
func (s *UserInboxService) Events(ctx context.Context, owner string, limit int) ([]InboxEventDto, error)
func (s *UserInboxService) Todos(ctx context.Context, owner string) ([]InboxTodoDto, error)
func (s *UserInboxService) SetTodoStatus(ctx context.Context, owner, id, status string) (*InboxTodoDto, error)
func (s *UserInboxService) ToTask(ctx context.Context, owner, id, extra string) (*InboxTodoDto, error)
func (s *UserInboxService) SuperAgentSearch(ctx context.Context, owner, query string, limit int) ([]model.InboxEvent, error)
func (s *UserInboxService) SuperAgentAddTodo(ctx context.Context, owner, title, detail, requester, due string, refs []string) (*InboxTodoDto, error)
func (s *UserInboxService) SuperAgentNotify(ctx context.Context, owner, text string, refs []string) error
func (s *UserInboxService) SuperAgentMark(ctx context.Context, owner string, refs []string, handling string) (int, error)
```

### 4.2 路由与 6 个 MCP 工具契约

- **HTTP 路由**（5 个端点由 `L5/spec_40` 挂载）：`GET /api/v1/inbox/summary`、`GET /api/v1/inbox/events?limit=200`、`GET /api/v1/inbox/todos`、`PUT /api/v1/inbox/todos/:id`、`POST /api/v1/inbox/todos/:id/task`。
- **6 个 `InboxMcpTools`**（作用域 `SUPER`，全轮次开放）：
  1. `inbox_search`（最近 2,000 条中检索，末尾固定加第三方数据防提示词注入声明）；
  2. `inbox_add_todo`（解析 8 位 `ref` 建 `OPEN` 待办、发 `InboxTodoChanged`、标源消息 `handling="todo"`）；
  3. `inbox_notify`（截 500 字加 `"【SuperAgent】"` 前缀发 `InboxNotifyRequested` 扇出所有启用 IM）；
  4. `inbox_mark`（批量改 `handling` 为 `read|ignored|task`）；
  5. `todo_list`；
  6. `todo_update`（`done|dismissed|open`，禁止直接设为 `tasked`）。

### 4.3 错误语义

- `SetTodoStatus` 非法状态或直接传 `"tasked"`：`400 invalid_argument`；
- 非本人或不存在待办：`404 not_found`（防探测）；
- 6 个 MCP 工具捕获领域异常全部转 `isError: true`。

### 4.4 SQL 契约

- `InboxEventStore.InsertIfAbsent` 必须使用 `ON CONFLICT (company_id, id) DO NOTHING` 并依据 `RowsAffected() == 1` 返回是否首次入库；
- `InboxEventStore.DeleteBefore` 每日每租户执行一次清理早于 `retentionDays`（默认 30 天）的历史事件。

---

## 5. 行为语义

### 5.1 7 步四级分诊规则（对齐 `TriageRules.java:21-65`）

严格自顶向下：
1. `fromBot` → `IGNORE`（`"机器人消息"`）；
2. `fromOwner && chatType != "p2p"` → `IGNORE`（`"我自己在群里的发言"`）；
3. 命中 17 寒暄确认词（`收到`/`好的`/`ok`/`+1`/`👍`等）或纯标点/空白/Emoji → `IGNORE`；
4. 命中 8 紧急关键词（`线上故障`/`紧急`/`加急`/`今天必须`/`马上`/`宕机`/`urgent`/`asap`）且 **`(addressed || relation != "")`** → `P0`（非直达且非上下级的群内紧急词降级防误报）；
5. `relation != "" && addressed`（上级/下属私聊或 `@我`）→ `P0`；
6. `addressed`（其他人私聊或 `@我`）→ `P1`；
7. 其余群消息 → `P2`。

### 5.2 `InboxService` 批处理节奏与 `UserInboxService` 双向镜像

- `Ingest` 写入去重后，若为 `P0` 立即投递至 2-Worker 异步池（队列满不阻塞 IM ACK，由 60s 巡检兜底）；
- `runWindow` 按 `companyID/owner` 加串行锁，单批最多 `300` 条送 `spi.InboxProcessor.Process`：失败写入 `failedUntil` 退避 `10m` 保留 `processed=false`；成功标记 `processed=true, handling="delivered"`，若返回 `NotifyText != ""` 立即调对应 `IMConnector.NotifyOwner` 原文直推；
- 60s 巡检（挂入 L0 `BackgroundGroup`）：依次跑 `P0` 兜底、`P1` 每小时一次、`P2` 按 `source.Timezone` 到达 `digestTime`（默认 `09:00`）每日一次；
- `UserInboxService`：`ToTask` 若已 `TASKED` 且有 `TaskID` 直接幂等返回；`OnNoticeIgnored` 收到 `linkType == "todo"` 时将对应 `OPEN` 待办静默置为 `DISMISSED`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `triage_test.go`（红灯：覆盖全部 7 步、群噪防误报、上级优先于下属）→ 实现 `triage.go`。
- [ ] **T2**：编写 `inbox_service_test.go`（红灯：UUIDv3 去重、P0 秒级池与原文直推、10m 退避、P1 小时与 P2 跨时区日报触发）→ 实现 `inbox_service.go`。
- [ ] **T3**：编写 `user_inbox_service_test.go` 与 `mcp_tools_test.go`（红灯：4 态流转、幂等 `ToTask`、`OnNoticeIgnored` 联动与 6 个 MCP 工具防注入尾注）→ 实现 `user_inbox_service.go` 与 `mcp_tools.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/event/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-35` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `inbox.retention_days=30` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 7 步分诊全路径与群噪防误报 | `internal/event/triage_test.go` | 普通群员未 `@我` 说“紧急”不升 P0 落 P2；上级 `@我` 必 P0；机器人/确认词必 IGNORE |
| 2 | UUIDv3 原子去重、P0 直推与 10m 退避 | `internal/event/inbox_service_test.go` | 重复投递返 `(nil, nil)`；处理器失败记 10m 退避不标 processed；P0 返回 `NotifyText` 立即调 `NotifyOwner` |
| 3 | 待办 4 态、幂等 `ToTask` 与 `NoticeIgnored` 镜像 | `internal/event/user_inbox_service_test.go` | 直接改 `tasked` 返 `400`；重复 `ToTask` 仅建 1 任务；`NoticeIgnored(todo)` 自动置 `DISMISSED` |
| 4 | 6 个 Inbox MCP 工具与防注入声明 | `internal/event/mcp_tools_test.go` | `inbox_search` 末尾含防注入声明；`inbox_notify` 截 500 字加 `【SuperAgent】` 前缀；`inbox_add_todo` 解析 8 位 ref 并标源消息 `todo` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/event/...` 全绿
- [ ] 全部 6 个 Inbox MCP 工具注册完整，行覆盖率 ≥ 80%
- [ ] `internal/arch_test.go` 通过（零引入 `internal/orchestrator`/`internal/agent`，零 Redis）

---

## 10. 可提交节点

- 建议 commit：`feat(event-inbox): implement 7-step TriageRules, InboxService, UserInboxService and 6 Inbox MCP tools (spec 35, SA-35)`
