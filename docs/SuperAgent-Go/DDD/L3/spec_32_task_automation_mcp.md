# spec 32 | L3 任务引擎 — 定时/API 自动化调度与全部 17 个 Task/Automation MCP 工具

- **状态**：`draft`
- **篇号顺序**：32（L3 任务与自动化域收口篇；交付 DST 安全 5 段 Cron + PG AdvisoryLock 防并发重复 + 外部 Webhook 常时鉴权 + 全部 17 个 MCP 工具且修复原版异常泄漏）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-32`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §2
  - 归档母本：[_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md](../_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md) §4.2.2, §5.7–§5.8
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-task/src/main/java/com/superagent/task/automation/AutomationService.java` (`L1-L429`)
    - `superagent-task/src/main/java/com/superagent/task/automation/AutomationScheduler.java` (`L1-L71`)
    - `superagent-task/src/main/java/com/superagent/task/automation/AutomationMcpTools.java` (`L1-L193`)
    - `superagent-task/src/main/java/com/superagent/task/TaskMcpTools.java` (`L1-L114`)
    - `superagent-task/src/main/java/com/superagent/task/ComplexTaskMcpTools.java` (`L1-L81`)
    - `superagent-web/src/main/java/com/superagent/web/automation/AutomationController.java` (`L1-L106`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L0/spec_02 (SA-02)](../L0/spec_02_db_pgx_tx_migration.md)（PG17 原生 `db.AdvisoryLock` 零 Redis 防调度重复）
  - [L1/spec_06 (SA-06)](../L1/spec_06_core_domain_runtime_agent_task_contracts.md)（`model.NextCron`/`UpcomingCron`/`ParseLocalTime`）
  - [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md)（`AutomationStore`）
  - [L3/spec_30 (SA-30)](./spec_30_task_state_codingflow_complex.md) & [L3/spec_31 (SA-31)](./spec_31_task_session_pool_watchdog.md)
- **下游消费者**：`SA-37`（`SuperAgentAutomationTools` 复用 `AutomationMcpTools`）、`SA-38`（向 `/mcp` 注册全部 17 个工具）、`SA-40`（Automation HTTP 与公开 Webhook 路由）、`SA-41`（装配 `AutomationScheduler`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业内部巡检、日报汇总、告警 Webhook 自动排障依赖稳定可靠的定时与事件触发自动化，而 Agent 与后台任务之间的交互则全部通过 MCP 工具完成。

本篇（`SA-32`）收口 L3 任务域（1.5 人天 PR），实现：
1. **DST 安全 5 段 Cron + 崩溃安全“至多一次” + PG 原生 `AdvisoryLock`**：先推进持久化 `next_run_at` 再触发任务，停机错过的多次调度自动折叠为单次补跑，利用 L0 `db.AdvisoryLock` 在**零 Redis** 条件下天然防止并发扫表重触发；
2. **公开 API Webhook 强安全验签**：从 `X-Automation-Token`（`<company>.<64-hex>`）解析租户并常时比对 SHA-256 哈希，ID 不存在与 Token 错误统一返回 `403` 防探测；
3. **修复 #3（17 个 MCP 工具领域错误归一化）**：将原版 `TaskMcpTools`/`ComplexTaskMcpTools` 漏捕的 `NotFound`/`Busy`/`InvalidArg`/`Forbidden` 统一转换为 `isError: true`（前缀 `"错误："`），杜绝 JSON-RPC `-32603` 泄漏打断 CLI。

### 1.2 本篇边界

- **本篇负责**：`internal/task/automation/{service.go,scheduler.go,tasks.go,mcp_tools.go}` 与 `internal/task/{mcp_tools.go,complex_mcp_tools.go}` 及全套单测。
- **本篇不负责**：5 段 Cron 基础解析函数（已在 `L1/spec_06` `internal/core/model/task.go` 交付）；Gin HTTP 路由注册（交由 `SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `AutomationService`（CRUD、重叠策略 `SKIP`/`PARALLEL`、先推后跑 `FireDue`、Webhook 常时验签） | `internal/task/automation/service.go`、`tasks.go` | 新增 |
| 2 | `AutomationScheduler`（立即首轮补跑 + 30s 周期 + PG `AdvisoryLock` + 挂入 `BackgroundGroup`） | `internal/task/automation/scheduler.go` | 新增 |
| 3 | `AutomationMcpTools`（**8 个自动化 MCP 工具**） | `internal/task/automation/mcp_tools.go` | 新增 |
| 4 | `TaskMcpTools`（**6 个任务工具**）与 `ComplexTaskMcpTools`（**3 个子任务工具**），统一 `isError: true` 包装 | `internal/task/mcp_tools.go`、`complex_mcp_tools.go` | 新增 |
| 5 | 自动化调度、Webhook 鉴权与全部 17 个 MCP 工具单测套件 | `internal/task/automation/{service,mcp_tools}_test.go`、`internal/task/mcp_tools_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md) 已落盘的 `automation`（已删 `tier`）与 `automation_run`；并发调度防重直接复用 L0 `db.AdvisoryLock`（PostgreSQL 原生事务/会话级咨询锁，**零 Redis**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package automation

type Service struct { /* store, tasks TaskBridge, providers, settings, locker *db.AdvisoryLocker, mu sync.Mutex */ }
func (s *Service) List(ctx context.Context, user string, isAdmin bool) ([]AutomationDto, error)
func (s *Service) Detail(ctx context.Context, id, user string, isAdmin bool) (*AutomationDetailDto, error)
func (s *Service) ForTask(ctx context.Context, taskID, user string, isAdmin bool) (*AutomationDto, error)
func (s *Service) Create(ctx context.Context, req SaveAutomationRequest, owner, source string) (*AutomationDto, error)
func (s *Service) Update(ctx context.Context, id string, req SaveAutomationRequest, user string, isAdmin bool) (*AutomationDto, error)
func (s *Service) SetEnabled(ctx context.Context, id string, enabled bool, user string, isAdmin bool) (*AutomationDto, error)
func (s *Service) Delete(ctx context.Context, id, user string, isAdmin bool) error
func (s *Service) RunNow(ctx context.Context, id, user string, isAdmin bool) (*AutomationRunDto, error)
func (s *Service) Trigger(ctx context.Context, id, token, input string) (*AutomationRunDto, error)
func (s *Service) RegenerateToken(ctx context.Context, id, user string, isAdmin bool) (*AutomationDto, error)
func (s *Service) Preview(ctx context.Context, req SaveAutomationRequest) ([]time.Time, error)
func (s *Service) FireDue(ctx context.Context, now time.Time) (int, error)

type Scheduler struct { /* companies, svc *Service, interval time.Duration */ }
func (s *Scheduler) Start(bg *lifecycle.BackgroundGroup)
```

### 4.2 路由与全部 17 个 MCP 工具契约

- **HTTP 路由**（11 个端点由 `L5/spec_40` 挂载）：`GET/POST /api/v1/automations`、`POST /api/v1/automations/preview`、`GET /api/v1/automations/by-task/:taskId`、`GET/PUT/DELETE /api/v1/automations/:id`、`POST /api/v1/automations/:id/{enabled,run,token}`、`POST /api/v1/automations/:id/trigger`（**公开路由**，凭 `X-Automation-Token` 解析租户并常时校验）。
- **全部 17 个 MCP 工具**：
  - `TaskMcpTools` (`CHAT`, 6)：`create_task`、`list_tasks`、`get_task`、`send_task_message`、`stop_task`、`delete_task`；
  - `ComplexTaskMcpTools` (`TASK` 且仅 `COMPLEX` 白名单, 3)：`create_subtask`、`list_subtasks`、`subtask_output`；
  - `AutomationMcpTools` (`CHAT`, 8, 始终 `isAdmin=false`)：`create_automation`（`type="api"` **严禁回显明文令牌**）、`list_automations`、`get_automation`、`update_automation`、`set_automation_enabled`、`run_automation_now`、`delete_automation`、`get_current_time`。

### 4.3 错误语义（含修复 #3）

- 自动化不存在或他人读取：`404 not_found`；他人修改/启停/删除/错误或缺失 Webhook Token：`403 forbidden`（`"Invalid automation id or token"` 统一文案防探测）；暂停的 API 自动化被触发：`400 invalid_argument`；
- **17 个 MCP 工具统一 `wrapMcpErr(err)`**：
  - `UnreadableMaterialsError` → `isError: true` + 附“切勿宣称任务已创建”提示；
  - 全部 `apperror.{NotFound,Busy,Conflict,InvalidArg,Forbidden}` → `isError: true` + `"错误：" + err.Error()`，绝不泄 `-32603`。

### 4.4 SQL 契约

- `AutomationStore.Delete` 仅删除 `automation` 定义行，完整保留 `automation_run` 与已产生历史 `task`；
- `FireDue` 每租户扫表获取 PG `AdvisoryLock("automation_fire:" + companyID)`（非阻塞 `TryLock`，未抢到直接跳过本轮），确保安全防重。

---

## 5. 行为语义

### 5.1 `FireDue` 先推后跑与 `SKIP` 重叠语义（对齐 `AutomationService.java:213-272`）

1. **先持久化推进再触发**：对 `Enabled && NextRunAt != nil && !NextRunAt.After(now)` 的项，先计算基于 `now` 的下次 `NextCron`（`ONCE` 则置 `Enabled=false, NextRunAt=nil`）并 `store.Save`，再调用 `fireLocked`——保证崩溃至多一次且停机错过多次自动折叠为单次；
2. **`SKIP` 重叠判定**：仅当上次任务状态为 `PENDING` 或 `RUNNING` 时跳过并记 `automation_run(skipped_reason)`；**刻意忽略 `NEEDS_CONFIRMATION` 与 `WAITING`**，防止暂停任务永久堵死后续定时触发；
3. **无人值守 Prompt 注入**：任务 `Brief` 尾部追加无人值守指令（“不要停下来等待确认，遇到不确定请合理假设并在总结中说明”），创建失败捕获记录 `automation_run` 不中断同批次其余到期项；
4. **`AutomationScheduler`**：挂入 L0 `BackgroundGroup`，启动立即跑第 1 轮完成停机补跑，随后每 `30s`（最小 `5s`）轮转租户。

### 5.2 Webhook `Trigger` 常时鉴权（对齐 `AutomationService.java:163-185, 397-411`）

从 `X-Automation-Token` 按首个 `.` 拆分 `<companyID>.<64-hex>` → 注入 `tenant.WithCompany` → 查 `automation` → 校验 `Type == API` 且 `subtle.ConstantTimeCompare(a.APITokenHash, sha256Hex(token)) == 1` → 校验 `a.Enabled`（停用返 `400`）→ 触发 `fireLocked(..., "api", input)`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/task/automation/service_test.go`（红灯：先推后跑折叠补跑、`ONCE` 自禁、`SKIP` 忽略 `WAITING`/`NEEDS_CONFIRMATION`、Webhook 常时校验与防探测、PG `AdvisoryLock` 防重）→ 实现 `service.go`、`tasks.go`、`scheduler.go`。
- [ ] **T2**：编写 `internal/task/mcp_tools_test.go` 与 `internal/task/automation/mcp_tools_test.go`（红灯：全部 17 个工具与修复 #3 `isError: true` 全路径回归、`create_automation(api)` 禁回显明文 token）→ 实现 `mcp_tools.go`、`complex_mcp_tools.go`、`automation/mcp_tools.go`。
- [ ] **T3**：运行 `go test -race -cover ./internal/task/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-32` 状态更新；
- [ ] `configs/config.example.yaml`：确认 `automation.{scheduler_enabled,scheduler_interval}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `FireDue` 先推后跑、停机折叠与 `ONCE` 自禁 | `internal/task/automation/service_test.go` | 停机错过 5 次只补跑 1 次且先写新 `next_run_at`；`ONCE` 触发后 `enabled=false` |
| 2 | `SKIP` 重叠与 Webhook 常时防探测 | `internal/task/automation/service_test.go` | 上次 `RUNNING` 跳过、`WAITING`/`NEEDS_CONFIRMATION` 正常跑；错 Token 与无 ID 均返 `403`；暂停返 `400` |
| 3 | 全部 17 个 MCP 工具与修复 #3 错误归一化 | `internal/task/mcp_tools_test.go`、`internal/task/automation/mcp_tools_test.go` | `NotFound`/`Busy`/`InvalidArg`/`UnreadableMaterials` 全部返 `isError: true`；`ComplexTaskMcpTools` 拒非 `TASK` grant；`create_automation(api)` 响应不含明文 token |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/task/...` 全绿
- [ ] 全部 17 个 MCP 工具（6 + 3 + 8）注册完整且修复 #3 回归绿
- [ ] 零 Redis 依赖（使用 PG `AdvisoryLock`），行覆盖率 ≥ 80%

---

## 10. 可提交节点

- 建议 commit 1：`feat(automation): implement DST-safe AutomationService, PG-locked Scheduler and Webhook trigger (spec 32, SA-32)`
- 建议 commit 2：`feat(task-mcp): implement all 17 task, complex-task and automation MCP tools with isError normalization (spec 32, SA-32)`
