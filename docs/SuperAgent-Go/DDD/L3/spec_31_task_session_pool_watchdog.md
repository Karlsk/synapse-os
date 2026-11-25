# spec 31 | L3 任务引擎 — 修复版会话复用池、异步调度器与看门狗重启恢复

- **状态**：`draft`
- **篇号顺序**：31（L3 任务域稳定性篇；彻底修复原版 Java `TaskSessionPool` 跨模型污染、`RestoreSessions` 与 `Recover` 断链两项核心缺陷）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-31`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §2
  - 归档母本：[_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md](../_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md) §1, §5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-task/src/main/java/com/superagent/task/TaskSessionPool.java` (`L1-L254`)
    - `superagent-task/src/main/java/com/superagent/task/TaskScheduler.java` (`L1-L75`)
    - `superagent-task/src/main/java/com/superagent/task/TaskWatchdog.java` (`L1-L141`)
    - `superagent-task/src/main/java/com/superagent/task/TaskRecovery.java` (`L1-L23`)
    - `superagent-task/src/main/java/com/superagent/task/TaskService.java` (`L306-L322, L1450-L1515`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`lifecycle.BackgroundGroup`、`ShutdownManager`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant.Path`、`tenant.Detach`）
  - [L2/spec_15 (SA-15)](../L2/spec_15_platform_tenant_bootstrap_auth.md)（`CompanyService.ForEachEnabled`）
  - [L3/spec_30 (SA-30)](./spec_30_task_state_codingflow_complex.md)（`TaskService` 核心执行方法）
- **下游消费者**：`SA-32`（自动化调度与 Task MCP）、`SA-41`（Server 启动注册 `TaskScheduler` 与 `TaskWatchdog` 到 `BackgroundGroup`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

后台任务执行周期长、并发数受限、且必须能承受进程重启或偶发 CLI 僵死。本篇（`SA-31`）独立收口任务并发调度与自愈恢复内核（1.5 人天 PR），重点修复原版 Java 两处严重线上隐患：
1. **修复 #1（`TaskSessionPool.Acquire` 跨模型污染）**：原版遗漏 `s.model == model` 比对，不同模型复用同一 CLI 会话导致 thinking block 签名校验报错。Go V1 严格比对 `s.Model == model`，模型变更时在**同一 `workspaceDir` 内轮换全新 `sessionID`** 并置 `Initialized = false`（保留磁盘文件、避免会话污染）；
2. **修复 #2（`RestoreSessions` 与 `Recover` 恢复断链）**：补全原版启动恢复 `IDLE`/`RESERVED` 槽位（带正确 `model`）与重启前追问任务重入队，并在 `Recover` 中正确调用 `recoverRun` 按任务类型（Coding/Complex/General）与是否轮换新会话生成精确续跑提示词。

### 1.2 本篇边界

- **本篇负责**：`internal/task/{session_pool.go,scheduler.go,watchdog.go,service_recover.go}` 及专项回归单测。
- **本篇不负责**：8 态任务业务流（已由 `SA-30` 交付）；Cron 自动化与 17 个 MCP 工具（交由 `SA-32`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 修复版 `SessionPool`（`IDLE`/`BUSY`/`RESERVED` 三态、跨模型同目录轮换、租户路径隔离） | `internal/task/session_pool.go` | 新增 |
| 2 | `TaskScheduler`（非阻塞信号通道合并唤醒、FIFO 准入、复杂父任务并发限流、租户配额） | `internal/task/scheduler.go` | 新增 |
| 3 | `TaskWatchdog`（启动即恢复中断 `RUNNING` + 每 30s 扫描 120s 停滞与 `ERROR` 任务） | `internal/task/watchdog.go` | 新增 |
| 4 | `Service.RestoreSessions`、`Service.Recover` 与 `recoverRun`（按类型构造续跑提示词） | `internal/task/service_recover.go` | 新增 |
| 5 | 会话池、调度器、看门狗与两项修复专项回归单测 | `internal/task/{session_pool,service_recover,scheduler_watchdog}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 `task`、`task_run_state` 与 `task_phase`（`SA-09`）。会话池与活跃运行表均为纯进程内内存结构（**零 Redis 依赖**），服务启动时通过 `RestoreSessions` 从 PG17 确定性重建。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package task

type PoolState string
const (
	StateIdle     PoolState = "IDLE"
	StateBusy     PoolState = "BUSY"
	StateReserved PoolState = "RESERVED"
)

type Session struct {
	ID, Owner, WorkspaceDir, Model, TaskID, ReservedFor string
	AgentType                                           model.AgentType
	State                                               PoolState
	Initialized                                         bool
}

type Lease struct {
	Session            *Session
	FirstTurn          bool
	ReusedConversation bool
}

type SessionPool struct { /* workspaceRoot string, maxParallelPerOwner int, byOwner map[string][]*Session */ }
func NewSessionPool(workspaceRoot string, maxParallelPerOwner int) *SessionPool
func (p *SessionPool) Acquire(ctx context.Context, owner string, agentType model.AgentType, taskID, modelID string) (*Lease, error)
func (p *SessionPool) BorrowReserved(taskID string) (*Lease, bool)
func (p *SessionPool) Pause(s *Session, reservedFor string)
func (p *SessionPool) Release(s *Session)
func (p *SessionPool) DropReservation(taskID string)
func (p *SessionPool) Restore(owner string, agentType model.AgentType, sessionID, workspaceDir, modelID, reservedFor string)
func (p *SessionPool) Adopt(owner string, agentType model.AgentType, taskID, sessionID, workspaceDir string, initialized bool) *Session
func (p *SessionPool) SessionForTask(taskID string) *Session

type Scheduler struct { /* companies, svc *Service, sweep time.Duration, wakeCh chan struct{} */ }
func (s *Scheduler) Start(bg *lifecycle.BackgroundGroup)
func (s *Scheduler) Wake()

type Watchdog struct { /* companies, tasks store.TaskStore, recovery TaskRecovery, interval, stall time.Duration */ }
func (w *Watchdog) Start(bg *lifecycle.BackgroundGroup)

// 落位 service_recover.go
func (s *Service) RestoreSessions(ctx context.Context) error
func (s *Service) ActiveTaskIDs() map[string]struct{}
func (s *Service) Recover(ctx context.Context, id string) error
```

### 4.2 路由契约

本篇为后台调度与恢复引擎，不新增 HTTP 路由。

### 4.3 错误语义

- `SessionPool.Acquire` 达到单用户并行上限（默认 50）：返回 `(nil, nil)`（非错误），调用方保持 `PENDING` 等待下次唤醒；
- `Recover` 发现 `AttemptCount >= 10`（预算耗尽）：将任务收敛为 `ERROR`（`"retry budget exhausted after 10 attempts"`）并发布 `TaskStatusChanged`，不再无限重启；
- `RestoreSessions` 中重启前处于 `PENDING` 且有工作区的追问任务若查无最新 `USER` 消息：收敛为 `STOPPED`。

### 4.4 SQL 契约

- `TaskRunStateStore.Save` 必须在每次 `Recover` 派生 goroutine 前同步落库更新 `attempt_count` 与 `agent_session_id`，确保崩溃后计数单调不回退。

---

## 5. 行为语义

### 5.1 修复 #1：`SessionPool.Acquire` 跨模型同工作区轮换

1. 遍历该 `owner` 下处于 `IDLE`、同 `AgentType`、且 `WorkspaceDir` 位于当前租户根目录下的槽位：
   - 若 `s.Model == modelID`：沿用原 `s.ID`（`FirstTurn = !s.Initialized`, `ReusedConversation = s.Initialized`）；
   - **若 `s.Model != modelID`**：保留 `s.WorkspaceDir` 磁盘成果，将 `s.ID` 替换为新 UUID、`s.Model = modelID`、`s.Initialized = false`，返回 `FirstTurn = true, ReusedConversation = false`；
2. 若无空闲槽位且 `busy < maxParallelPerOwner`：在 `<workspaceRoot>/<companyID>/<uuid>` 新建工作区并分配新槽位；
3. `Pause(s, reservedFor)` 将 `NEEDS_CONFIRMATION`/`WAITING` 任务槽位置为 `RESERVED`（**不计入 `busy` 并行配额**），待用户回复或子任务汇总后通过 `BorrowReserved` 原会话续跑；若被他人借用，归还时还原为原 `reservedFor` 的 `RESERVED`。

### 5.2 修复 #2：`RestoreSessions` 与 `Recover` / `recoverRun`

- **`RestoreSessions`**：
  1. 扫描所有非删且 `WorkspaceDir != ""` 任务，标记 `RUNNING`/`NEEDS_CONFIRMATION`/`WAITING` 占用目录；
  2. 对其余目录每个仅取 `UpdatedAt` 最新的任务恢复为 `IDLE`（带正确 `Model`）；
  3. 对 `NEEDS_CONFIRMATION`/`WAITING` 恢复为 `RESERVED`（带 `Model` 与 `reservedFor = t.ID`）；
  4. 对重启前排队的追问任务（`PENDING && WorkspaceDir != ""`）重建排队（无 `USER` 消息置 `STOPPED`）。
- **`Watchdog` + `Recover`**：
  1. 启动后立即对全部启用租户扫一遍遗留 `RUNNING` 任务触发 `Recover`；此后每 `30s`（最小 `5s`）扫一遍停滞超过 `120s` 且不在 `activeRuns` 的 `RUNNING` 与所有 `ERROR` 任务；
  2. `ERROR` 任务在 `FailureCount+1 >= 1`（`rotateAfterFailures = 1`）时立即在同 `WorkspaceDir` 轮换新 `sessionID`（`freshSession = true`）；
  3. 调用 `recoverRun`：
     - `CODING`：调用 `codingFlow.ResumePrompt(phases, brief, freshSession)`；
     - 非轮换续跑：传入 `ResumeNote`；
     - `COMPLEX` 轮换：传入 `FreshNote + brief` 且 `wrapBrief = false`（防止重新拆分）；
     - `GENERAL` 轮换：传入 `FreshNote + brief` 且 `wrapBrief = true`。

### 5.3 `TaskScheduler` 单通道合并唤醒

- 挂入 L0 `BackgroundGroup`；使用 `wakeCh := make(chan struct{}, 1)` 非阻塞合并多次唤醒信号 + `30s` 兜底 ticker；
- 每轮按 `CompanyService.ForEachEnabled` 公平轮转租户，依次执行 `sumUpFinished()` → `awaitingAdmission()`（FIFO、受复杂父任务 `maxParallel` 与租户配额约束）→ `admit()`（需 LLM 选型时以 `tenant.Detach` 异步执行不阻塞调度循环）。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `session_pool_test.go`（红灯：跨模型同目录轮换、`RESERVED` 不占并发额度与归还、租户目录越界隔离）→ 实现 `session_pool.go`。
- [ ] **T2**：编写 `service_recover_test.go`（红灯：`RestoreSessions` 4 条规则、`Recover` `ERROR` 轮换与 `Coding/Complex/General` `recoverRun` 提示词、10 次预算耗尽）→ 实现 `service_recover.go`。
- [ ] **T3**：编写 `scheduler_watchdog_test.go`（红灯：合并唤醒、FIFO 限流、120s 停滞检测与优雅停机）→ 实现 `scheduler.go` 与 `watchdog.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/task/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-31` 状态更新；
- [ ] `configs/config.example.yaml`：确认 `task.{scheduler_sweep,watchdog_enabled,watchdog_interval,watchdog_stall}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `SessionPool` 跨模型复用轮换（**修复 #1**） | `internal/task/session_pool_test.go` | 同 CLI 异 `model` 复用 IDLE 槽位：`workspaceDir` 不变、`sessionID` 换新 UUID、`FirstTurn==true`、`s.Model==newModel`；同 `model` 保持原 `sessionID` |
| 2 | `RESERVED` 让渡归还与并发上限 | `internal/task/session_pool_test.go` | `RESERVED` 不占 `maxParallel`；借用归还后仍指向原 `reservedFor` |
| 3 | `RestoreSessions` 4 场景恢复（**修复 #2a**） | `internal/task/service_recover_test.go` | `RUNNING` 不变 `IDLE`；`WAITING`/`NEEDS_CONFIRMATION` 变 `RESERVED`；同目录取最新 `model`；无 `USER` 消息追问置 `STOPPED` |
| 4 | `Watchdog` + `Recover` 调用 `recoverRun`（**修复 #2b**） | `internal/task/service_recover_test.go` | 120s 停滞续跑原会话；`ERROR` 立即同目录轮换新会话；`COMPLEX` 轮换 `wrapBrief==false`；第 10 次耗尽置 `ERROR` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/task/...` 全绿
- [ ] 两项原版缺陷修复（`SessionPool` 跨模型轮换、`RestoreSessions`/`Recover`）均有独立回归用例保真
- [ ] `Scheduler` 与 `Watchdog` 均接入 L0 `BackgroundGroup`，`ctx` 取消即干净退出，零 Redis 依赖

---

## 10. 可提交节点

- 建议 commit：`feat(task): implement model-safe SessionPool, Scheduler, Watchdog and fixed RestoreSessions/Recover (spec 31, SA-31)`
