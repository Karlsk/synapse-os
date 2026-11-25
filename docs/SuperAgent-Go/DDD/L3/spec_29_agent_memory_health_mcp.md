# spec 29 | L3 Agent 协作域 — 长期记忆蒸馏循环、会话健康度与 10 个 Agent MCP 工具

- **状态**：`draft`
- **篇号顺序**：29（L3 Agent 域收口篇；交付 60k 原文结转、5 节 Markdown 长期记忆蒸馏循环、28 模式会话健康度分类器及 5 组共 10 个 Agent MCP 工具）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-29`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §1
  - 归档母本：[_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md](../_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md) §4.2.B, §5.4
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-agent/src/main/java/com/superagent/agent/memory/AgentMemoryService.java` (`L1-L489`)
    - `superagent-agent/src/main/java/com/superagent/agent/memory/AgentMemoryRules.java` (`L1-L163`)
    - `superagent-agent/src/main/java/com/superagent/agent/memory/AgentMemoryLoop.java` (`L1-L69`)
    - `superagent-agent/src/main/java/com/superagent/agent/memory/SessionHealth.java` (`L1-L29`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentCollabMcpTools.java` (`L1-L104`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentCreateMcpTools.java` (`L1-L90`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentUpdateMcpTools.java` (`L1-L102`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentSkillMcpTools.java` (`L1-L104`)
    - `superagent-agent/src/main/java/com/superagent/agent/chat/ChatHistoryMcpTools.java` (`L1-L143`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md)（`AgentMemoryStateStore`/`ChatStore`/`TaskAgentStore`）
  - [L2/spec_23 (SA-23)](../L2/spec_23_assets_skills_mcp.md)（`SkillService` 供 `AgentSkillMcpTools` 绑定解绑）
  - [L3/spec_24 (SA-24)](./spec_24_cost_catalog_pricing_usage.md)（`ModelRouter.Cheapest` + `UsageRecorder`）
  - [L3/spec_27 (SA-27)](./spec_27_agent_registry_collab.md) & [L3/spec_28 (SA-28)](./spec_28_agent_chat_liveturn_sse.md)
- **下游消费者**：`SA-36`（SuperAgent 记忆继承）、`SA-38`（向 `/mcp` 注册全部 5 组 10 个 Agent MCP 工具）、`SA-40`（记忆查看/编辑/手动蒸馏 HTTP 路由）、`SA-41`（Server 启动挂接 `AgentMemoryLoop`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

当 Agent 因切换模型、连续会话失败或长期多轮使用而开启新 CLI 会话时，需要无缝延续历史上下文与关键约定，同时避免上下文无限膨胀。本篇（`SA-29`）交付：
1. **首轮 60,000 字原文结转 + Top-20 文件摘要**：超长单条消息采用首 2/3 + 尾 1/3 中间截断并内嵌 `chat_history_read` 续读指针；
2. **后台 5 节 Markdown 长期记忆蒸馏循环**：定时 + `ConsolidateSoon` 异步蒸馏，严格校验 5 节结构与 `NO_CHANGE`/`INVALID`（非法输出绝不覆盖旧记忆），支持员工手动编辑与触发蒸馏；
3. **28 模式瞬态故障分类器与全部 10 个 Agent MCP 工具**：精确区分网络/上游瞬态错误与会话自身损坏；暴露协作、创建/更新（保留前端快捷按钮短语且移除废弃 `character` 参数）、技能绑定与历史全文检索工具。

### 1.2 本篇边界

- **本篇负责**：`internal/agent/{memory_rules.go,memory_service.go,mcp_tools.go}` 及全套单测。
- **本篇不负责**：`ChatService` 17 步主流程（已由 `SA-28` 交付）；`/mcp` 与 `/api/v1/agents/:id/memory*` 路由挂载（交由 `SA-38`/`SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `AgentMemoryRules`（60k 结转中间截断、5 节蒸馏提示词与结果解析）与 `SessionHealth`（28 模式瞬态分类器） | `internal/agent/memory_rules.go` | 新增 |
| 2 | `AgentMemoryService`（上下文组装、失败计数、手动编辑/蒸馏、`ConsolidateSoon`）与 `AgentMemoryLoop` | `internal/agent/memory_service.go` | 新增 |
| 3 | 5 组共 **10 个 Agent MCP 工具**（`AgentCollab` 4 + `AgentCreate` 1 + `AgentUpdate` 1 + `AgentSkill` 2 + `ChatHistory` 2） | `internal/agent/mcp_tools.go` | 新增 |
| 4 | 记忆结转、28 瞬态模式与 10 个 MCP 工具单测套件 | `internal/agent/{memory,mcp_tools}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md) 的 `agent_memory`、`chat_session`、`chat_message` 与 `task_agent`。`inFlight` 互斥集合、`backoff` 退避表与 `ConsolidateSoon` 队列均为纯进程内并发结构（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package agent

const (
	DefaultCarryChars     = 60_000
	CarriedFilesLimit     = 20
	ExcerptedFilesLimit   = 3
	ExcerptMaxRunes       = 6_000
	SessionFailureLimit   = 10
	HistoryReadChunkRunes = 20_000
)

type DistillOutcomeKind string
const (
	OutcomeUpdated  DistillOutcomeKind = "UPDATED"
	OutcomeNoChange DistillOutcomeKind = "NO_CHANGE"
	OutcomeInvalid  DistillOutcomeKind = "INVALID"
)

func IsTransientSessionError(errMsg string) bool
func CarryOverMessages(all []model.ChatMessage, loc *time.Location, budget int) string
func ParseDistillOutcome(raw string) (kind DistillOutcomeKind, markdown string)

type AgentMemoryService struct { /* store, chatStore, agents, files, router, asker, settings, companies, runLog, inFlight sync.Map, backoff sync.Map, soonCh chan soonReq */ }
func (s *AgentMemoryService) Start(bg *lifecycle.BackgroundGroup)
func (s *AgentMemoryService) Read(ctx context.Context, a model.Agent) string
func (s *AgentMemoryService) Context(ctx context.Context, a model.Agent, excludeMsgIDs map[string]bool, viewer string) string
func (s *AgentMemoryService) RecordSuccess(ctx context.Context, a model.Agent, modelID string)
func (s *AgentMemoryService) RecordFailure(ctx context.Context, a model.Agent, errMsg string) (shouldRotate bool)
func (s *AgentMemoryService) ConsolidateSoon(ctx context.Context, agentID string)
func (s *AgentMemoryService) ConsolidateDue(ctx context.Context, now time.Time) int
func (s *AgentMemoryService) Get(ctx context.Context, agentID, viewer string) (*AgentMemoryDto, error)
func (s *AgentMemoryService) Edit(ctx context.Context, agentID, owner, text string) (*AgentMemoryDto, error)
func (s *AgentMemoryService) DistillNow(ctx context.Context, agentID, owner string) (*AgentMemoryDto, error)
```

### 4.2 路由与 10 个 MCP 工具契约

- **HTTP 路由**（由 `L5/spec_40` 挂载）：`GET/PUT /api/v1/agents/:id/memory`、`POST /api/v1/agents/:id/memory/distill`。
- **10 个 Agent MCP 工具**（全部领域异常返回 `isError: true`）：
  1. `AgentCollabMcpTools` (`CHAT`, 4)：`collab_find_people`、`collab_list_members`、`collab_invite`（仅生成 `PENDING` 卡片）、`collab_remove`；
  2. `AgentCreateMcpTools` (`CHAT`, 1)：`agent_create`（描述保留精确短语 **`确认创建吗？`**，已移除 `character` 参数）；
  3. `AgentUpdateMcpTools` (`CHAT`, 1)：`agent_update`（描述保留精确短语 **`确认修改吗？`**，已移除 `character` 参数，触发 `MergeUpdate` 审计日志）；
  4. `AgentSkillMcpTools` (`CHAT, SUPER`, 2)：`skill_attach`、`skill_detach`（`SUPER` 后台轮 `SessionID==""` 拒止，禁挂 SUPER 本体）；
  5. `ChatHistoryMcpTools` (`CHAT, TASK`, 2)：`chat_history_search`（跨会话倒序搜索，160 rune 摘要片段）、`chat_history_read`（按 20,000 rune 分段读取）。

### 4.3 错误语义

- 查看记忆无权限返 `404`；编辑或手动蒸馏非 Owner 返 `403`；
- 蒸馏输出 `INVALID`（不以 `# ` 开头或不足 2 行）：**绝不覆盖既有记忆**，写入 `backoff[agentID] = now + idle`（默认 30m）并记 `AgentLog(memory, warn)`；
- MCP 工具捕获全部预期领域错误后返回 `spi.McpCallResult{IsError: true, Text: err.Error()}`。

### 4.4 SQL 契约

- `AgentMemoryStateStore.Save` 采用 `ON CONFLICT (company_id, agent_id) DO UPDATE`；仅在 `UPDATED` 或 `NO_CHANGE` 时推进 `summarized_until` 水位。

---

## 5. 行为语义

### 5.1 60k 原文结转与 5 节长期记忆蒸馏（对齐 `AgentMemoryRules.java:19-163`, `AgentMemoryService.java:144-489`）

1. **`Context` 三段拼接**：长期记忆正文 + `CarryOverMessages`（从最新消息倒推填充 60,000 rune，单条装不下时按首 2/3 + 尾 1/3 中间截断并插指针 `…（中间省略 N 字；完整原文用 chat_history_read 读取消息 <msgId>）…`）+ 最近 20 个工作文件摘要（前 3 个可读文本各摘录前 6,000 rune）；
2. **后台循环 `AgentMemoryLoop`**：挂入 L0 `BackgroundGroup`，每 5 分钟轮转租户，满足 `len(fresh) > 0 && last+2m <= now && (len(fresh) >= 10 || last+30m <= now)` 触发蒸馏，每轮每租户最多 3 个 Agent；`inFlight sync.Map` 防止定时与 `ConsolidateSoon`/手动蒸馏并发重叠；
3. **蒸馏模型与计费**：优先 `modelRouter.Cheapest()` 回退默认模型，使用临时空目录无工具调用，计费至 `agent.Owner` / `source="memory"`；解析结果剥围栏后区分 `NO_CHANGE` / `INVALID` / `UPDATED`。

### 5.2 28 模式瞬态故障分类器（对齐 `SessionHealth.java:11-29`）

空串判非瞬态（计故障）；非空按 `" " + strings.ToLower(err)` 匹配精确 28 子串（`"network"`, `"econnreset"`, `"econnrefused"`, `"etimedout"`, `"enotfound"`, `"eai_again"`, `"socket hang up"`, `"connection reset"`, `"connection refused"`, `"connection closed"`, `"fetch failed"`, `"timed out"`, `"timeout"`, `"overloaded"`, `"rate limit"`, `"rate_limit"`, `"too many requests"`, `"service unavailable"`, `"bad gateway"`, `"gateway timeout"`, `"internal server error"`, `" 429"`, `" 500"`, `" 502"`, `" 503"`, `" 504"`, `" 529"`）。瞬态错误不增加 `session_failures`；非瞬态累计达到 `10` 次归零并返回 `shouldRotate = true`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `memory_test.go`（红灯：60k 结转中间截断、5 节解析/`NO_CHANGE`/`INVALID` 保旧记忆、28 瞬态模式、10 次非瞬态触发轮换、`inFlight` 互斥与退避）→ 实现 `memory_rules.go` 与 `memory_service.go`。
- [ ] **T2**：编写 `mcp_tools_test.go`（红灯：覆盖全部 10 个 Agent MCP 工具、快捷按钮短语、无 `character` 字段、`SUPER` 后台拒止、`chat_history_read` 20k 分段）→ 实现 `mcp_tools.go`。
- [ ] **T3**：运行 `go test -race -cover ./internal/agent/...` 与 `internal/arch_test.go`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-29` 状态更新；
- [ ] `configs/config.example.yaml`：核对 `memory.{enabled,interval_minutes=5,min_messages=10,settle_minutes=2,idle_minutes=30,agents_per_tick=3,carry_chars=60000}` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 60k 结转中间截断与 `chat_history_read` 指针 | `internal/agent/memory_test.go` | 超预算首条消息按 2/3+1/3 截断并包含 `chat_history_read` 及消息 ID |
| 2 | 5 节蒸馏解析、`INVALID` 保护与 `inFlight` 互斥 | `internal/agent/memory_test.go` | 非 `# ` 开头输出判 `INVALID` 不覆盖旧记忆且写退避；并发触发仅运行 1 次 |
| 3 | 28 模式瞬态分类与 10 次失败轮换 | `internal/agent/memory_test.go` | 全部 28 模式不增计数；非瞬态第 10 次归零并返 `true` |
| 4 | 10 个 Agent MCP 工具契约与权限隔离 | `internal/agent/mcp_tools_test.go` | `agent_create`/`update` 含 `"确认创建吗？"`/`"确认修改吗？"` 且无 `character`；`skill_attach` 拦截 `SUPER` 后台轮；`chat_history_read` 按 20k 分页 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/agent/...` 全绿
- [ ] 全部 10 个 MCP 工具注册完整且域错误均返 `isError: true`，行覆盖率 ≥ 80%
- [ ] `internal/arch_test.go` 通过（零 import `internal/task`，零 Redis），`AgentMemoryLoop` 挂入 `BackgroundGroup`

---

## 10. 可提交节点

- 建议 commit 1：`feat(agent-memory): add 60k carryover, 5-section memory distillation loop and 28-pattern SessionHealth (spec 29, SA-29)`
- 建议 commit 2：`feat(agent-mcp): implement all 10 Agent collaboration, lifecycle, skill and history MCP tools (spec 29, SA-29)`
