# spec 36 | L4 个人编排层 — SuperAgent 双半隔离核心、三大硬安全门禁与后台推进循环

- **状态**：`draft`
- **篇号顺序**：36（L4 编排器核心篇；交付个人 SuperAgent 实时对话半与独立后台半、UI 计划审批门禁、外部输入防提示词注入隔离与毒丸自愈）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-36`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §3.1
  - 归档母本：[_epic/L4/spec_12_orchestrator_superagent_dual_half.md](../_epic/L4/spec_12_orchestrator_superagent_dual_half.md) §1, §4.1–§5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentService.java` (`L1-L972`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentChat.java` (`L1-L71`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentLoop.java` (`L1-L70`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentInbox.java` (`L1-L63`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentInboxProcessor.java` (`L1-L66`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentPrompts.java` (`L1-L91`)
    - `superagent-web/src/main/java/com/superagent/web/superagent/SuperAgentController.java` (`L1-L70`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md)（`SuperAgentStateStore`/`SuperAgentWorkStore`）
  - [L3/spec_28 (SA-28)](../L3/spec_28_agent_chat_liveturn_sse.md)（`AgentTurnCustomizer`/`ChatService`）
  - [L3/spec_30 (SA-30)](../L3/spec_30_task_state_codingflow_complex.md)（`TaskService`）
  - [L4/spec_35 (SA-35)](./spec_35_event_identity_triage_inbox.md)（`spi.InboxProcessor`）
- **下游消费者**：`SA-37`（25 个 Orchestrator MCP 工具）、`SA-40`（`/api/v1/superagent*` 6 个 HTTP 路由）、`SA-41`（装配 `SuperAgentChat`/`SuperAgentInboxProcessor`/`SuperAgentLoop`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

每位企业员工拥有一位专属个人团队负责人（`SuperAgent`）：一方面在实时聊天中帮助员工组建最多 6 人的 AI 团队、直接派发任务；另一方面每 3 秒在后台静默跟进所有已分派任务的状态变化并处理企业 IM 推送的外部消息，在关键节点通过 `tell_user` 向员工汇报。

本篇（`SA-36`）在 2 人天粒度内交付双半隔离架构与 **三大不可绕过的服务端硬安全门禁**：
1. **实时对话半 vs 后台脱离会话彻底隔离**：实时对话强制 `RunsCommands=false`（剥离 `Bash` 与 `skipPermissions`）并在每轮前注入 `superagent:background` 新发言、计划/团队状态与 `TURN_CHECK`；后台半运行于独立脱离会话（UUIDv3 `"superagent-background:" + sa.ID`），受 10s 静默防抖 / 60s 防饥饿 / P0 立即唤醒调度；
2. **门禁 (a) UI 计划审批门禁**：`hire_member`/`update_member`/`assign_task` 必须校验 `plan_status == "approved"`，聊天口头确认绝不放行，唯独点击 UI 卡片调用 `ApprovePlan` 解锁；
3. **门禁 (b) `【外部消息】` 前缀切分 + 双重防提示词注入隔离**：内部通知与外部第三方消息绝不混跑同一轮；外部消息轮次同时实施 CLI 白名单裁剪（`ExternalRoundTools`）+ 服务端 `requireOwnInput` 硬拒止全部 5 个行动工具；
4. **门禁 (c) 毒丸批次自愈**：后台轮次失败时强制清空后台 `AgentSessionID`（下轮重开全新 CLI 对话）且**无论成败一律推进 `ProcessedUntil = cutoff`**，杜绝坏批次卡死循环。

### 1.2 本篇边界

- **本篇负责**：`internal/orchestrator/{inbox_rules.go,prompts.go,service.go,service_round.go,service_team.go,chat_customizer.go,inbox_processor.go,loop.go}` 及全套安全门禁单测。
- **本篇不负责**：全部 25 个 Orchestrator MCP 工具提供者（独立落位 `SA-37`）；Gin 路由注册（交由 `SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 纯规则引擎 `SuperAgentInbox`（`PendingMessages`/`Due` 10s·60s·immediate/`Watch` 4 态机）与 `SuperAgentPrompts` | `internal/orchestrator/inbox_rules.go`、`prompts.go` | 新增 |
| 2 | `Service`（状态/计划审批/团队/派活台账/外部前缀切分/毒丸自愈后台轮次/`BackfillWork`） | `internal/orchestrator/service.go`、`service_round.go`、`service_team.go` | 新增 |
| 3 | `ChatCustomizer`（实现 `AgentTurnCustomizer`）与 `InboxProcessor`（实现 `spi.InboxProcessor` + P0 原文直推兜底） | `internal/orchestrator/chat_customizer.go`、`inbox_processor.go` | 新增 |
| 4 | `Loop`（3s 周期多租户后台调度，挂入 L0 `BackgroundGroup`，单用户 panic 隔离） | `internal/orchestrator/loop.go` | 新增 |
| 5 | 规则引擎与三大硬安全门禁单元测试套件 | `internal/orchestrator/{inbox_rules,service_safety,chat_customizer,inbox_processor}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md) 已落盘的 3 张租户表：`superagent_state`（已清理废弃 `tracked_json`，`plan_json` 为 PG17 `JSONB`）、`superagent_work`、`superagent_work_event`。内存中的 `running`、`immediate`、`backgroundGrants` 均为纯进程内 `sync.Map`（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package orchestrator

const (
	BackgroundAuthor  = "superagent:background"
	ExternalMsgPrefix = "【外部消息】"
)

type WatchAction int
const (
	WatchKeep WatchAction = iota
	WatchReportAndKeep
	WatchReportAndDrop
	WatchDrop
)

func PendingMessages(all []model.ChatMessage, after *time.Time) []model.ChatMessage
func Due(pending []model.ChatMessage, now time.Time, quiet, maxWait time.Duration, immediate bool) bool
func Watch(status model.TaskStatus, reported string) WatchAction

type Config struct {
	Enabled       bool
	TickInterval  time.Duration // 默认 3s，最小 1s
	QuietDuration time.Duration // 默认 10s
	MaxWait       time.Duration // 默认 60s
	TeamLimit     int           // 默认 6
	Timezone      string        // 默认 "Asia/Shanghai"
}

type Service struct { /* cfg, states, works, agents, chats, tasks, projects, skills, memory, router, runners, gateway, grants, runLog, bus, stateMu, running, immediate, backgroundGrants */ }
func NewService(cfg Config, deps Deps) *Service
func (s *Service) State(ctx context.Context, owner string) (*SuperAgentStateDto, error)
func (s *Service) DeliverExternal(ctx context.Context, owner, text string, urgent bool) error
func (s *Service) ApprovePlan(ctx context.Context, owner, planID string) (*SuperAgentStateDto, error)
func (s *Service) Invites(ctx context.Context, owner string) ([]AgentInviteDto, error)
func (s *Service) Background(ctx context.Context, owner string, limit int) ([]MessageDto, error)
func (s *Service) Work(ctx context.Context, owner, status, memberID string, limit int) ([]SuperAgentWorkDto, error)
func (s *Service) WorkEvents(ctx context.Context, owner, taskID string) ([]SuperAgentWorkEventDto, error)
func (s *Service) BackfillWork(ctx context.Context) error
func (s *Service) Tick(ctx context.Context, now time.Time)
func (s *Service) RequireApproved(ctx context.Context, owner string) error
func (s *Service) RequireOwnInput(grantToken string) error

type ChatCustomizer struct { /* svc *Service, grants, teamLimit int */ }
type InboxProcessor struct { /* svc *Service */ }
type Loop struct { /* companies, svc *Service, cfg Config */ }
func (l *Loop) Start(bg *lifecycle.BackgroundGroup)
```

### 4.2 路由契约（6 个端点由 `L5/spec_40` 挂载，强绑定登录用户 `owner`）

- `GET /api/v1/superagent`、`GET /api/v1/superagent/invites`、`GET /api/v1/superagent/background?limit=200`；
- `GET /api/v1/superagent/work?status=&memberId=&limit=100`、`GET /api/v1/superagent/work/:taskId/events`；
- `POST /api/v1/superagent/plans/:planId/approve`（唯一合法计划审批解锁入口）。

### 4.3 错误语义

- 计划未审批调受控行动工具：`400 invalid_argument` / MCP `isError: true`（`"计划尚未确认：请先用 propose_plan 提交计划，并请用户在界面上点击「确认执行」..."`）；
- 外部消息后台轮调行动工具：`400 invalid_argument` / MCP `isError: true`（`"处理外部消息时不能招人、调整成员、派任务或回复任务；需要团队去做的事请用 propose_plan 提计划。"`）；
- 审批已过期或不匹配的 `planId`：`400 invalid_argument` `"This plan is no longer waiting for approval"`；
- 查看非本人工作事件：`404 not_found`。

### 4.4 SQL 契约

- `superagent_state.plan_json` 使用 `NULLIF($7, '')::jsonb`；
- `superagent_work` Upsert 仅更新 `status`/`summary`/`finished_at`/`updated_at`，不覆写 `assigned_at` 与 `message_id`。

---

## 5. 行为语义

### 5.1 实时对话半 `ChatCustomizer`（对齐 `SuperAgentChat.java:30-70`）

- `Handles(agent)` 仅接管 `agent.IsSuper()`；`RunsCommands` 恒返 `false`（禁 `Bash` 与 `skipPermissions`）；
- `Prompt` 每轮按顺序注入：① 上次正常回复之后的全部 `superagent:background` 后台帖子；② 当前计划状态与团队容量；③ `TurnCheckPrompt`；④ 用户原始输入。

### 5.2 后台推进半与三大安全门禁（对齐 `SuperAgentService.java:267-498`）

1. **`maybeStartRound` 前缀切分**：取 `PendingMessages(msgs, st.ProcessedUntil)`，经 `Due(10s/60s/immediate)` 放行后，按 `all[0]` 是否带 `"【外部消息】"` 切分**最长连续同类前缀**（若还有剩余异类消息立即写 `immediate[runKey]` 让下个 3s tick 立刻跑）；
2. **双重防注入**：`external == true` 时不仅把 CLI 白名单缩为 `ExternalRoundTools`，同时登记 `backgroundGrants[grant.Token] = true`，服务端 `RequireOwnInput` 在任意行动工具入口硬拦截；
3. **毒丸自愈**：`attempt.Failed()` 时强制清空后台 `session.AgentSessionID = ""`；无论成败无条件推进 `st.ProcessedTo(cutoff, now)`；
4. **`SuperAgentInboxProcessor`**：将收件箱批次拼成带防注入声明的 `<messages>` XML 块写入 `"【外部消息】"` 后台消息；若 `window == P0` 额外构造 `"【SuperAgent 紧急提醒】<sender>（<chat>）：<text>"`（200 字截断）返回 `NotifyText` 供秒级私聊兜底；
5. **`reportTrackedTasks` & `BackfillWork`**：每 3s 用 `Watch` 4 态机检查未终结工作台账并写系统通知与 `superagent_work_event`；`CompanyStarted` 幂等补录既有分派任务。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `inbox_rules_test.go`（红灯：10s/60s/immediate 边界与 `Watch` 4 态机全组合）→ 实现 `inbox_rules.go` 与 `prompts.go`。
- [ ] **T2**：编写 `service_safety_test.go`（红灯：UI 计划审批门禁、`【外部消息】` 前缀切分与 `RequireOwnInput` 阻断、失败清会话+无条件推进游标防毒丸、`reportTrackedTasks`）→ 实现 `service.go`、`service_round.go`、`service_team.go`。
- [ ] **T3**：编写 `chat_customizer_test.go` 与 `inbox_processor_test.go`（红灯）→ 实现 `chat_customizer.go`、`inbox_processor.go`、`loop.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/orchestrator/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-36` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `superagent.{enabled,tick_interval,quiet_duration,max_wait,team_limit,timezone}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `SuperAgentInbox` 防抖/防饥饿/Watch 4 态机 | `internal/orchestrator/inbox_rules_test.go` | 未满 10s 且未满 60s 不触发；连发满 60s 强触发；P0 `immediate` 秒触发；`Watch` 全状态转换精确 |
| 2 | 门禁 (a) UI 计划审批 | `internal/orchestrator/service_safety_test.go` | `proposed` 态调 `RequireApproved` 返错；调 `ApprovePlan` 后通过且发 `PlanChanged(open=false)`；错误 `planId` 返 `400` |
| 3 | 门禁 (b) `【外部消息】` 切分与 `RequireOwnInput` | `internal/orchestrator/service_safety_test.go` | `[外部, 内部]` 混合队列首轮仅取外部并置 `immediate`；外部轮 grant 调 `RequireOwnInput` 必拒 |
| 4 | 门禁 (c) 毒丸自愈与 P0 原文兜底 | `internal/orchestrator/service_safety_test.go`、`inbox_processor_test.go` | 后台轮失败清空 `AgentSessionID` 且 `ProcessedUntil` 仍推进至 `cutoff`；P0 返回 `NotifyText` 含 `【SuperAgent 紧急提醒】` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/orchestrator/...` 全绿
- [ ] 三大硬安全门禁均有独立回归用例 100% 通过，行覆盖率 ≥ 80%
- [ ] `SuperAgentLoop` 挂入 L0 `BackgroundGroup`，零 `tracked_json` 残留，零 Redis 依赖

---

## 10. 可提交节点

- 建议 commit：`feat(orchestrator): implement dual-half SuperAgent core, 3 hard safety gates and background loop (spec 36, SA-36)`
