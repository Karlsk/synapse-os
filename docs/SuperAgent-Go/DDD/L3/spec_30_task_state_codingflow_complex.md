# spec 30 | L3 任务引擎 — 8 态任务状态机、资料预读、CodingFlow 三步流、COMPLEX 分治与自动打标

- **状态**：`draft`
- **篇号顺序**：30（L3 任务域核心篇；与 `internal/agent` 严格同层零互引，交付 8 态任务状态机、编码三步流、复杂任务分治与完结自动打标）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-30`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §2
  - 归档母本：[_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md](../_epic/L3/spec_10_task_codingflow_pool_watchdog_automation.md) §5.1–§5.4, §5.6
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-task/src/main/java/com/superagent/task/TaskService.java` (`L645-L1407, L1519-L2005`)
    - `superagent-task/src/main/java/com/superagent/task/CodingFlow.java` (`L1-L162`) + `superagent-task/src/main/resources/flows/coding.md` (`L1-L19`)
    - `superagent-task/src/main/java/com/superagent/task/ComplexTaskPrompts.java` (`L1-L135`)
    - `superagent-task/src/main/java/com/superagent/task/TaskKinds.java` (`L1-L40`)
    - `superagent-task/src/main/java/com/superagent/task/TaskTagger.java` (`L1-L158`)
- **前置依赖**：
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant.Detach`、`eventbus`）
  - [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md)（7 个 Task PG Store）
  - [L1/spec_12 (SA-12)](../L1/spec_12_runtime_claude_adapter_warmpool.md) & [L1/spec_13 (SA-13)](../L1/spec_13_runtime_codex_adapter.md)
  - [L2/spec_21 (SA-21)](../L2/spec_21_assets_files_storage_mcp.md)（`FileService`）& [L2/spec_22 (SA-22)](../L2/spec_22_assets_projects_access.md)（`spi.ProjectAccess`）& [L2/spec_23 (SA-23)](../L2/spec_23_assets_skills_mcp.md)（`SkillService`）
  - [L3/spec_25 (SA-25)](./spec_25_cost_task_model_router.md)（`spi.TaskModelSelector`）& [L3/spec_26 (SA-26)](./spec_26_docs_feishu_dingtalk_link_reader.md)（`spi.LinkReader`）
- **下游消费者**：`SA-31`（会话池、调度器与看门狗恢复）、`SA-32`（自动化调度与 17 个 Task/Automation MCP 工具）、`SA-35`（`UserInboxService.ToTask`）、`SA-36`（`SuperAgentService.Dispatch`）、`SA-40`（Task HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

长耗时后台任务脱离实时 SSE 连接独立运行，支撑研发编码（PRD→开发→验收）、复杂任务分治并行研究以及可中断追问返工。本篇（`SA-30`）在 2 人天粒度内交付任务核心业务状态机：
1. **8 态任务状态机与三路事件扇出**：`PENDING`/`RUNNING`/`NEEDS_CONFIRMATION`/`WAITING`/`DONE`/`ERROR`/`STOPPED`/`DELETED`，仅顶层任务发布 `eventbus.TaskStatusChanged` 驱动 `NoticeService`、`ChatTaskResults` 与 `TaskTagger`；
2. **建任务前 8 链接 90s 并发预读与 5 代 `continuesFrom` 前情链**：任一链接不可读立即返 `409` 且零 DB 写入，避免任务跑半途发现读不到资料；
3. **`CodingFlow` 三步流 + `COMPLEX` 分治扇出/扇入 + 失败自动升档**：严格保留 PRD 三标题自动重试门禁、完结后返工 `runFollowUp` 不覆写 `output.md`、复杂任务失败补派额度与 3 轮封顶。

### 1.2 本篇边界

- **本篇负责**：`internal/task/{flows/coding.md,coding_flow.go,kinds.go,complex_prompts.go,service.go,service_query.go,service_exec.go,tagger.go}` 及单测。
- **本篇不负责**：`TaskSessionPool`、`TaskScheduler`、`TaskWatchdog`、`RestoreSessions`/`Recover`（独立落位 `SA-31`）；自动化与 17 个 MCP 工具（独立落位 `SA-32`）；Gin 路由挂载（交由 `SA-40`）。**严禁 import `internal/agent`**（仅通过 `spi.TaskDoers` 读人设与记忆）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 编码三步流内嵌模板 `coding.md` 与 `CodingFlow` 状态机 | `internal/task/flows/coding.md`、`internal/task/coding_flow.go` | 新增 |
| 2 | 任务类型校验 `TaskKinds`、标签清洗与复杂任务提示词 `ComplexTaskPrompts` | `internal/task/kinds.go`、`internal/task/complex_prompts.go` | 新增 |
| 3 | 核心任务引擎（创建、8 链接预读、查询/分页/统计/关系、执行、`CodingFlow`、复杂任务扇出/扇入、失败自动升档、追问/停止/删除） | `internal/task/service.go`、`service_query.go`、`service_exec.go` | 新增 |
| 4 | 完结自动打标器 `TaskTagger`（订阅 `TaskStatusChanged(done)`） | `internal/task/tagger.go` | 新增 |
| 5 | 8 态流转、预读、CodingFlow、COMPLEX 分治与打标单元测试 | `internal/task/{coding_flow,service,tagger}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md) 已落盘的 7 张任务表：`task`（已清理 `tier`，`spend NUMERIC(16,6)`）、`task_phase`（`JSONB`）、`task_run_state`、`task_agent`、`task_link`、`task_tag` 与 `SA-08` 的 `task_model_choice`；执行流消息/事件复用 `chat_message`/`agent_event`（以 `session_id = task.id` 索引）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package task

type CreateOptions struct {
	Owner, AgentID, SessionID, MessageID string
	Materials                            []string // nil 扫描 brief 中全部 URL；非 nil 仅预读指定列表
}

type PageFilter struct {
	Status, Tag, AgentID, Source, ProjectID string
	Page, Size                              int // clamp 1..100
}

type Service struct { /* cfg, stores, runners, gateway, grants, files, projects, skills, doers spi.TaskDoers, selector spi.TaskModelSelector, linkReader spi.LinkReader, usage spi.UsageRecorder, bus, pool, schedMu, activeRuns, lastReply */ }
func NewService(cfg Config, deps Deps) *Service
func (s *Service) Create(ctx context.Context, req CreateTaskRequest, opts CreateOptions) (*TaskDto, error)
func (s *Service) CreateSubtask(ctx context.Context, parentID, owner, title, brief string) (*TaskDto, error)
func (s *Service) List(ctx context.Context, owner string) ([]TaskDto, error)
func (s *Service) ListForAgent(ctx context.Context, agentID string) ([]TaskDto, error)
func (s *Service) Page(ctx context.Context, owner string, f PageFilter) (*TaskPageDto, error)
func (s *Service) Facets(ctx context.Context, owner string) (*TaskFacetsDto, error)
func (s *Service) Stats(ctx context.Context, owner string) (*TaskStatsDto, error)
func (s *Service) Detail(ctx context.Context, id, owner string) (*TaskDetailDto, error)
func (s *Service) EventsFrom(ctx context.Context, id, owner string, offset int) ([]TaskEventDto, error)
func (s *Service) Status(ctx context.Context, id, owner string) (*TaskDto, error)
func (s *Service) Relations(ctx context.Context, id, owner string) (*TaskRelationsDto, error)
func (s *Service) ModelChoices(ctx context.Context, id, owner string) ([]TaskModelChoiceDto, error)
func (s *Service) Timing(ctx context.Context, id, owner string) (*TaskTimingDto, error)
func (s *Service) SubtaskOutput(ctx context.Context, parentID, childID, owner string) (string, error)
func (s *Service) SetTags(ctx context.Context, id, owner string, tags []string) (*TaskDto, error)
func (s *Service) SendMessage(ctx context.Context, id, owner, text string, uploads []ImageUploadDto, fileIDs []string) error
func (s *Service) Stop(ctx context.Context, id, owner string) error
func (s *Service) Delete(ctx context.Context, id, owner string) error

func RequireKind(args map[string]any) (string, error)
func CleanTags(given []string) []string // 去重、<=20 rune、上限 8 个
```

### 4.2 路由契约（16 个任务端点由 `L5/spec_40` 挂载）

`POST /api/v1/tasks`、`GET /api/v1/tasks/agents`、`GET /api/v1/tasks`、`GET /api/v1/tasks/{facets,stats}`、`GET /api/v1/tasks/:id`、`GET /api/v1/tasks/:id/{events,status,relations,model-choices,timing}`、`PUT /api/v1/tasks/:id/tags`、`POST /api/v1/tasks/:id/messages`、`GET /api/v1/tasks/:id/messages/:messageId/images/:index`、`POST /api/v1/tasks/:id/stop`、`DELETE /api/v1/tasks/:id`。

### 4.3 错误语义

- 任务不存在、已软删或无查看权：`404 not_found`；
- 追问时任务处于 `RUNNING`/未分配工作区/已在排队：`409 busy`；
- 资料预读任一链接失败或 90s 超时：`409 conflict`（`*apperror.UnreadableMaterialsError`，**零 DB 写入**）；
- 非法 `kind`、超复杂子任务额度/轮次上限：`400 invalid_argument`；
- 租户月度 Token 配额超限：`403 forbidden`。

### 4.4 SQL 契约

- 任务 `spend` 使用 `decimal.Decimal` 累加落 `NUMERIC(16,6)`；
- 失败自动升档（`escalate`）在回退 `PENDING` 时保留已累计 `spend`，删除 `task_run_state` 并在 `CODING` 模式下重置 `task_phase` 为 3 个 `PENDING`。

---

## 5. 行为语义

### 5.1 创建前 8 链接 90s 并发预读与 5 代 `continuesFrom`（对齐 `TaskService.java:656-721, 1989-2005`）

- 最多并发预读前 8 条去重 URL（共享 `90s` `context.WithTimeout`）；单条失败**不取消**其余预读，以便一次性向用户列出全部不可读链接与修复建议；只要有 1 条失败立即返 `409` 且不写库；全部成功时将内容存为 `WorkFile` 并在 `brief` 尾部追加文件 ID 清单；
- `continuesFrom` 沿 `TaskLinkStore` 向前追溯至多 5 代（环保护），最新前序保留 10,000 字+工作目录，更早前序截断 1,200 字；
- Kind 判定：子任务强制 `general`；明确传非 `"auto"` 走 `ParseTaskKind`；否则**仅当 `repo != ""` 判 `coding`**。

### 5.2 `CodingFlow` 三步流与 `runFollowUp`（对齐 `CodingFlow.java:23-162`, `TaskService.java:1744-1808`）

- 3 阶段：`prd` (`10%→33%`) → `dev` (`33%→66%`) → `accept` (`66%→100%`)，完结写入 `<workspaceDir>/output.md`；
- **PRD 门禁**：抽取 `#` 开头标题行，必须同时包含 `"背景"`、`"需求"`、`"技术思路"`；首轮未过同会话自动重试 1 次（`PRDRetryPrompt`），仍未过置 `NEEDS_CONFIRMATION(10%)`；
- **验收门禁**：`accept` 空回复置 `NEEDS_CONFIRMATION(66%)`；
- **完结后追问**：3 阶段均 `DONE` 后用户发消息走单回合 `runFollowUp`，不修改 `task_phase` 且**不覆写** `output.md`。

### 5.3 `COMPLEX` 分治扇出/扇入、失败升档与 `TaskTagger`

- 父任务走 `selector.Strongest`（子任务走 `selector.Choose`）；单父任务默认上限 `10` 子任务 / `5` 并行 / `3` 轮汇总；**失败补派额度** `room = max(0, maxSubtasks + count(ERROR|STOPPED) - total)`；
- `ERROR` 子任务仅在 `AttemptCount >= 10` 后才算真正完结，避免过早汇总；全子任务完结后按 `60,000` 字总预算均分截断拼装 `【子任务汇总】` 唤醒父任务；
- 进入 `ERROR` 前尝试 `escalate`（用户手选 `BY_USER` 或固定模式 `Refused()` 跳过）；
- `TaskTagger` 订阅 `TaskStatusChanged(done)`（单 worker + `tenant.Detach`），跳过已有标签任务，用 `ModelRouter.Cheapest` 结合 Top-40 既有标签输出最多 4 个标签。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `coding_flow_test.go`（红灯：PRD 三标题检测、自动重试 1 次、双门禁暂停、`runFollowUp` 保护）→ 落地 `flows/coding.md`、`coding_flow.go`、`kinds.go`、`complex_prompts.go`。
- [ ] **T2**：编写 `service_test.go`（红灯：8 态状态机、8 链接 90s 预读失败零写库、5 代 `continuesFrom` 环保护、COMPLEX 扇出/扇入/补派/3 轮封顶、失败自动升档与 `BY_USER` 保护）→ 落地 `service.go`、`service_query.go`、`service_exec.go`。
- [ ] **T3**：编写 `tagger_test.go`（红灯：仅顶层 `done` 触发、Top-40 词表、已人工打标跳过）→ 落地 `tagger.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/task/...` 与 `internal/arch_test.go`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-30` 状态更新；
- [ ] `configs/config.example.yaml`：确认 `task.{workspace_dir,allowed_tools,skip_permissions,max_parallel_per_owner}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 8 链接 90s 预读与 `continuesFrom` 5 代链 | `internal/task/service_test.go` | 9 链接只预读前 8 条；任一失败返 `409` 且 0 条写库；6 代链截前 5 代且自环不死循环 |
| 2 | `CodingFlow` PRD 重试、Accept 门禁与 `runFollowUp` | `internal/task/coding_flow_test.go` | 首轮无三标题自动重试 1 次；重试仍无暂停 `10%`；完结后追问不改阶段不覆写 `output.md` |
| 3 | `COMPLEX` 分治、失败补派与 3 轮封顶 | `internal/task/service_test.go` | 失败子任务补派额度生效；重试期内 `ERROR` 子任务不触发汇总；第 3 轮拒 `create_subtask` |
| 4 | 失败升档与 `TaskTagger` 3 路事件 | `internal/task/tagger_test.go` | 升档重置阶段保留 `spend`；`BY_USER` 不升档；子任务不发事件；顶层 `done` 打 ≤4 标签 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/task/...` 全绿
- [ ] `internal/arch_test.go` 通过（`internal/task` 零引入 `internal/agent`，零 Redis）
- [ ] §2 交付物表 5 项齐备，行覆盖率 ≥ 80%

---

## 10. 可提交节点

- 建议 commit：`feat(task): implement 8-state TaskService, material pre-reading, CodingFlow, COMPLEX fan-out and TaskTagger (spec 30, SA-30)`
