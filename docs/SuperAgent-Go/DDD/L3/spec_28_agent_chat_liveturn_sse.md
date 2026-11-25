# spec 28 | L3 Agent 协作域 — 17 步对话引擎、LiveTurn SSE 广播、三层容灾与文件自动打标

- **状态**：`draft`
- **篇号顺序**：28（L3 Agent 域核心篇；交付完整 17 步 `ChatService`、Go 原生多端同屏 `LiveTurn`、3 层容灾、任务回传通知与 `FileTagger`，修复原版共享发言人前缀与旋转中止漂移两大缺陷）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-28`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §1
  - 归档母本：[_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md](../_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md) §5.2–§5.3, §5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-agent/src/main/java/com/superagent/agent/chat/ChatService.java` (`L1-L1089`)
    - `superagent-agent/src/main/java/com/superagent/agent/chat/ChatTaskResults.java` (`L1-L119`)
    - `superagent-agent/src/main/java/com/superagent/agent/chat/TaskConfirmPrompt.java` (`L1-L74`)
    - `superagent-agent/src/main/java/com/superagent/agent/files/FileTagger.java` (`L1-L316`)
    - `superagent-web/src/main/java/com/superagent/web/chat/ChatController.java` (`L1-L118`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant.Detach`、`eventbus`）
  - [L1/spec_06 (SA-06)](../L1/spec_06_core_domain_runtime_agent_task_contracts.md)（`model.TurnTimer`、`spi.AgentRunner`/`UsageRecorder`/`ModelRouter`）
  - [L1/spec_12 (SA-12)](../L1/spec_12_runtime_claude_adapter_warmpool.md) & [L1/spec_13 (SA-13)](../L1/spec_13_runtime_codex_adapter.md)（Claude/Codex Runner）
  - [L2/spec_21 (SA-21)](../L2/spec_21_assets_files_storage_mcp.md)（`FileService`/`FileFolderService`）
  - [L3/spec_24 (SA-24)](./spec_24_cost_catalog_pricing_usage.md)（`ModelRouter`/`UsageRecorder`）
  - [L3/spec_27 (SA-27)](./spec_27_agent_registry_collab.md)（`AgentService`/`AgentLogService`）
- **下游消费者**：`SA-29`（记忆循环与 10 MCP）、`SA-36`（`SuperAgentChat` 实现 `AgentTurnCustomizer`）、`SA-40`（对话与 SSE 流 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

多轮实时对话是用户感知最直接的核心链路，必须同时满足：**单 Agent 写互斥、多端同屏实时 SSE 观战且断线不杀后端回合、跨 Key/跨模型/跨会话三层自愈容灾、7 阶段耗时精准埋点**。

本篇（`SA-28`）在 1:1 保真 Java 17 步 `ChatService` 的基础上落定 3 项关键修复与清理：
1. **修复共享 Agent 发言人前缀漏判项目成员**：不仅检查直接协作者 `AgentMemberStore`，同步检查 `ProjectAccess.HasOtherMembers`，确保通过项目共享发言也携带 `"[<displayName>]："` 前缀；
2. **修复会话旋转后 `StopGeneration` 取消键漂移**：`activeRuns` 与 `liveTurns` 统一以 `liveKey`（`agent.ID` 或无 Agent 时的 `session.ID`）索引 `context.CancelFunc`，Layer 2/3 旋转会话后停止按钮 100% 命中；
3. **清理死代码 `MaterialPrompt`**：彻底移除零引用的 `MaterialPrompt.java`，保留实际生效的链接收藏（`SaveLinks`）与 `>= 3,000` 字长粘贴自动转 `.md`（`KeepPasted`）。

### 1.2 本篇边界

- **本篇负责**：`internal/agent/{live_turn.go,chat_service.go,task_results.go,file_tagger.go}` 及全套单测。
- **本篇不负责**：`AgentMemoryService` 详细实现（本篇通过窄接口依赖记忆读写/记录失败，完整记忆蒸馏循环在 `SA-29`）；`SuperAgentChat` 定制器（在 `SA-36`）；Gin SSE Flush 包装（在 `SA-40`）。**严禁 import `internal/task`**。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | Go 原生多订阅者并发 SSE 广播器 `LiveTurn`（同锁原子快照+有界 channel，慢消费不阻塞上游） | `internal/agent/live_turn.go` | 新增 |
| 2 | 17 步对话引擎 `ChatService`（并发占锁、3 层容灾、双路发言人前缀、会话轮换与 `StopGeneration`） | `internal/agent/chat_service.go` | 新增 |
| 3 | `ChatTaskResults`（监听 `TaskStatusChanged` 写回对话）与 `TaskConfirmPrompt` | `internal/agent/task_results.go` | 新增 |
| 4 | `FileTagger`（90s/60s 后台文件自动归档 + 按人分组自动打标 + 10m 租户熔断） | `internal/agent/file_tagger.go` | 新增 |
| 5 | `LiveTurn`、17 步对话 3 层容灾、回传与 `FileTagger` 单测套件 | `internal/agent/{live_turn,chat_service,file_tagger}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md) 与 [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md) 的 `chat_session`、`chat_message`、`agent_event`（`BIGSERIAL` 主键，**`TOKENS` 永不落库**）、`turn_timing`。`liveTurns` 与 `activeRuns` 为纯进程内 `sync.Map`（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package agent

const (
	LongPasteRunes         = 3_000
	ModelHops              = 2
	ErrorMark              = "⚠️ 出错了："
	StoppedMark            = "⏸ 回复被中断"
	TaskReportAuthorPrefix = "task:"
)

type LiveTurn struct { /* mu, By, ByName, StartedAt, SessionID, text, activity, events, ended, subs */ }
func NewLiveTurn(by, byName string, startedAt time.Time, sessionID string) *LiveTurn
func (l *LiveTurn) Publish(ev StreamEventDto)
func (l *LiveTurn) Subscribe() (snapshot []StreamEventDto, ch <-chan StreamEventDto, unsub func(), ended bool)
func (l *LiveTurn) End()
func (l *LiveTurn) ToDTO(sessionID string) LiveTurnDto

type ChatService struct { /* store, agentStore, members, projects, runners, gateway, grants, files, skills, images, memory, router, usage, timings, quotas, runLog, customizers, liveTurns, activeRuns */ }
func (s *ChatService) CreateSession(ctx context.Context, req CreateSessionRequest, user string) (*SessionDto, error)
func (s *ChatService) RequireAccess(ctx context.Context, sessionID, user string) error
func (s *ChatService) SendMessage(ctx context.Context, sessionID, content string, uploads []ImageUploadDto, fileIDs []string, user string) (*LiveTurn, error)
func (s *ChatService) Live(ctx context.Context, sessionID string) (*LiveTurnDto, error)
func (s *ChatService) WatchLive(ctx context.Context, sessionID string) (*LiveTurn, bool, error)
func (s *ChatService) StopGeneration(ctx context.Context, sessionID string)
func (s *ChatService) EditMessage(ctx context.Context, sessionID, messageID, content string) error
func (s *ChatService) ResolveModel(ctx context.Context, a *model.Agent) string
func (s *ChatService) SessionForTurn(ctx context.Context, a model.Agent, modelID string) (*model.ChatSession, error)
func (s *ChatService) RotateSession(ctx context.Context, a model.Agent, reason string) (*model.ChatSession, error)

type ChatTaskResults struct { /* store, agents, taskAgents */ }
func (r *ChatTaskResults) RegisterHandlers(bus *eventbus.Bus)
func (r *ChatTaskResults) PendingConfirmSection(ctx context.Context, sessionID string) string

type FileTagger struct { /* companies, files, folders, router, asker, settings, pausedUntil sync.Map */ }
func (t *FileTagger) Start(bg *lifecycle.BackgroundGroup)
```

### 4.2 路由与 SSE 契约（由 `L5/spec_40` 挂载）

- `POST/GET /api/v1/sessions`、`GET/DELETE /api/v1/sessions/:id`、`PUT /api/v1/sessions/:id/title`；
- `POST /api/v1/sessions/:id/messages`（SSE）、`GET /api/v1/sessions/:id/live`、`GET /api/v1/sessions/:id/live/stream`（SSE）、`POST /api/v1/sessions/:id/stop`、`PUT /api/v1/sessions/:id/messages/:messageId`、`GET /api/v1/sessions/:id/messages/:messageId/images/:index`；
- SSE 帧格式：`event: <type>\ndata: {"type":"<type>","seq":<n>,"payload":<str|null>}\n\n`（枚举：`system`/`thinking`/`text`/`tool_use`/`tool_result`/`result`/`usage`/`error`/`stopped`/`done`/`idle`，**`TOKENS` 永不落库、永不推 SSE**）。

### 4.3 错误语义

- 租户月度 Token 配额超限：`403 forbidden`；
- 项目已归档禁对话、编辑非 `USER` 消息：`400 invalid_argument`；
- 单 Agent 回合并发占用：`409 busy`（`"<byName> 触发的回复正在进行中，请稍候"`）；
- 无权访问会话：`404 not_found`。

### 4.4 SQL 契约

- 落库顺序铁律：`ChatStore.AppendMessage(ASSISTANT)` → **`TurnTimingStore.Save`（必须早于 SSE `"done"` 事件推送，前端收到 `"done"` 立刻拉 `/timings`）** → `ChatStore.UpdateSession` → `AgentLogService.Info(turn)` → `live.Publish(done)` → `live.End()`。

---

## 5. 行为语义

### 5.1 `LiveTurn` 原子快照+实时订阅（对齐 `ChatService.java:974-1046`）

- `Publish` 持有 `mu` 合并连续 `text`/`thinking` 增量压缩回放体积，并向每个订阅者有界缓冲 channel（`cap = 256`）执行非阻塞 `select { case ch <- ev: default: }` 投递，确保慢客户端绝不阻塞 CLI stdout；
- `Subscribe` 在同一把锁内克隆历史 `events` 并注册新 `ch`，保证零丢帧、零重复；`End()` 幂等关闭全部订阅者 channel。

### 5.2 17 步 `ChatService` 与 3 层容灾（对齐 `ChatService.java:512-891`）

1. **Step 1–4（同步门禁）**：启动 `model.NewTurnTimer()` → 检租户配额 → 匹配 `AgentTurnCustomizer`（`SUPER` 必须命中定制器）并拒止已归档项目 → `liveTurns` 预检冲突返 `409`；
2. **Step 5–7（准备与双路发言人前缀修复）**：`ResolveModel` → `SessionForTurn`（模型变更则开新会话复用工作区）→ 装配系统提示词（已删除 `MaterialPrompt`）→ 保存图片/附件、写 `USER` 消息、`SaveLinks`、`>= 3,000` rune 调 `KeepPasted` 存 `.md` → 当 `len(members) > 0 || projects.HasOtherMembers(agentID, owner)` 时拼接 `"[<displayName>]："` 前缀；
3. **Step 8–9（记忆与原子占锁）**：新会话载入 `memory.Context`，续跑仅载 `memory.Read` → `liveTurns.LoadOrStore(liveKey, live)` 原子抢锁 → `activeRuns.Store(liveKey, cancel)`（统一按 `liveKey` 防旋转漂移）→ 以 `tenant.Detach(ctx)` 启动异步 goroutine（客户端断连不杀回合）；
4. **Step 10–16（三层容灾）**：
   - **Layer 1（切 Key）**：零输出且非取消出错 → `gateway.ReportEndpointFailure` 返回新端点则立即同会话重试；
   - **Layer 2（模型跳变，最多 `ModelHops = 2` 跳）**：命中 `IsModelFailure` → `modelRouter.ReportFailure` → 取下一备选模型开 `reason="model_down"` 新会话带 `memory.Context` 重试；
   - **Layer 3（会话健康度轮换）**：非瞬态连续失败达 `10` 次 → `RotateSession("failures")` + `ConsolidateSoon` 重试 1 次；
5. **Step 17（保序落库收口）**：严格按 §4.4 顺序落库、写耗时、发终态帧并 `live.End()`。

### 5.3 `ChatTaskResults` 与 `FileTagger`

- `ChatTaskResults` 订阅 `eventbus.TaskStatusChanged`（`done`/`error`/`needs_confirmation`），仅通过事件解耦 `internal/task`，向 Agent 最新未删会话追加 `author="task:<taskID>"` 的 `ASSISTANT` 消息，并在下轮对话前拼入 `TaskConfirmPrompt`；
- `FileTagger` 挂入 L0 `BackgroundGroup`（启动延迟 90s，周期 60s），按租户轮转执行两阶段（单批 20 个）：① 个人目录自动归档（永不覆盖 `user`/`agent` 放置）；② 按 `(owner, agentID)` 共享 Top-60 既有词表生成 1–3 个标签；单租户出错熔断 `10m` 互不影响。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `live_turn_test.go`（红灯：并发订阅原子快照、文本合并、慢消费不阻塞）→ 实现 `live_turn.go`。
- [ ] **T2**：编写 `chat_service_test.go`（红灯：17 步全链路、双路项目成员发言人前缀、`>=3000` 字长粘贴、Layer 1/2/3 三层容灾、`TurnTiming` 先于 `done`、旋转后 `StopGeneration` 命中、`ChatTaskResults`）→ 实现 `chat_service.go` 与 `task_results.go`。
- [ ] **T3**：编写 `file_tagger_test.go`（红灯：自动归档+按人词表打标+10m 租户熔断）→ 实现 `file_tagger.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/agent/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-28` 状态更新；
- [ ] `configs/config.example.yaml`：确认 `chat.{workspace_dir,allowed_tools,skip_permissions,sse_timeout_ms,files_auto_tag}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `LiveTurn` 多订阅者并发原子性 | `internal/agent/live_turn_test.go` | `-race` 下 10 订阅者中途接入拿到合并历史 + 完整后续帧；阻塞慢消费者不阻塞 `Publish` |
| 2 | 双路共享发言人前缀与长粘贴 | `internal/agent/chat_service_test.go` | 仅项目共享（无直接 `AgentMember`）发言也带 `"[名字]："` 前缀；`>=3000` 字存 `.md` 并附 id 提示 |
| 3 | Layer 1/2/3 容灾、保序与旋转取消 | `internal/agent/chat_service_test.go` | 零输出切 Key；`503` 跳备选模型最多 2 跳；连续 10 次非瞬态失败旋转重试；`TurnTimingStore.Save` 早于 `"done"`；旋转后 `StopGeneration` 仍命中中止 |
| 4 | `FileTagger` 归档/打标与 10m 租户熔断 | `internal/agent/file_tagger_test.go` | 按 `(owner, agentID)` 带 Top-60 词表截前 3 标签；租户 A 失败暂停 10m 不影响租户 B |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/agent/...` 全绿
- [ ] 零 `MaterialPrompt` 残留，零 import `internal/task`，零 Redis 引入
- [ ] §2 交付物表 5 项齐备，行覆盖率 ≥ 80%

---

## 10. 可提交节点

- 建议 commit 1：`feat(agent-chat): add concurrent LiveTurn SSE broadcaster and ChatTaskResults listener (spec 28, SA-28)`
- 建议 commit 2：`feat(agent-chat): implement 17-step ChatService with 3-layer failover, shared speaker fix and FileTagger (spec 28, SA-28)`
