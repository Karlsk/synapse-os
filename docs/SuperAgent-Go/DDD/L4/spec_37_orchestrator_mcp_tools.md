# spec 37 | L4 个人编排层 — 4 组共 25 个 Orchestrator MCP 工具集与三场景白名单门禁

- **状态**：`draft`
- **篇号顺序**：37（L4 编排器收口篇；交付 `SuperAgentTools`(13)、`SuperAgentTaskTools`(1)、`SuperAgentAutomationTools`(7) 与 `SuperAgentCollabTools`(4) 共 25 个 MCP 工具及严格白名单安全矩阵）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-37`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §3.1
  - 归档母本：[_epic/L4/spec_12_orchestrator_superagent_dual_half.md](../_epic/L4/spec_12_orchestrator_superagent_dual_half.md) §4.2.2–§4.2.3
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentTools.java` (`L1-L355`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentTaskTools.java` (`L1-L71`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentAutomationTools.java` (`L1-L52`)
    - `superagent-orchestrator/src/main/java/com/superagent/orchestrator/SuperAgentCollabTools.java` (`L1-L121`)
- **前置依赖**：
  - [L3/spec_26 (SA-26)](../L3/spec_26_docs_feishu_dingtalk_link_reader.md)（只读办公文档工具白名单对齐）
  - [L3/spec_29 (SA-29)](../L3/spec_29_agent_memory_health_mcp.md)（Agent 技能与协作约定）
  - [L3/spec_32 (SA-32)](../L3/spec_32_task_automation_mcp.md)（委托包装 `AutomationMcpTools`）
  - [L4/spec_36 (SA-36)](./spec_36_orchestrator_superagent_dual_half.md)（`Service` 及其 `RequireApproved`/`RequireOwnInput` 门禁）
- **下游消费者**：`SA-38`（向 `/mcp` 注册全部 4 组 25 个 `McpToolProvider`）、`SA-41`（`wire.go` 装配）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

个人 SuperAgent 的全部业务编排能力——查团队、提计划、招人/调岗、派活、追问任务、直接起任务、管理自动化与邀请同事协作——均以标准 MCP 工具暴露给底层 CLI 执行器。本篇（`SA-37`）独立交付全部 **25 个 Orchestrator MCP 工具**与三场景（实时对话 / 内部后台 / 外部消息后台）白名单矩阵（1.5 人天 PR），确保每一项权限边界、防提示词注入门禁与工具名无冲突均可逐项独立审阅与测试。

### 1.2 本篇边界

- **本篇负责**：`internal/orchestrator/{tool_sets.go,mcp_tools.go,mcp_task_tools.go,mcp_automation_tools.go,mcp_collab_tools.go}` 及全部 25 个工具的门禁与功能单测。
- **本篇不负责**：`SuperAgentService` 双半引擎与后台循环（已由 `SA-36` 交付）；`/mcp` HTTP 网关接入（交由 `SA-38`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 三场景白名单常量表（`ChatTools`/`BackgroundTools`/`ExternalRoundTools`/`ActingTools`） | `internal/orchestrator/tool_sets.go` | 新增 |
| 2 | `SuperAgentTools`（**13 个核心编排工具**） | `internal/orchestrator/mcp_tools.go` | 新增 |
| 3 | `SuperAgentTaskTools`（**1 个 `start_task` 工具**，仅实时对话） | `internal/orchestrator/mcp_task_tools.go` | 新增 |
| 4 | `SuperAgentAutomationTools`（**7 个自动化工具**，委托 `SA-32` 并剔除 `get_current_time` 防重名） | `internal/orchestrator/mcp_automation_tools.go` | 新增 |
| 5 | `SuperAgentCollabTools`（**4 个协作工具**，仅实时对话且禁共享他人 Agent 或 SuperAgent） | `internal/orchestrator/mcp_collab_tools.go` | 新增 |
| 6 | 全部 25 个 MCP 工具、无重名校验与安全门禁矩阵单测 | `internal/orchestrator/mcp_tools_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，全部持久化通过 `SA-36` `Service` 及 L2/L3 既有服务完成，零 Redis 依赖。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package orchestrator

// 4 个 McpToolProvider 构造函数（供 SA-38 / SA-41 注册至 McpToolRegistry）
func NewSuperAgentTools(svc *Service) spi.McpToolProvider
func NewSuperAgentTaskTools(svc *Service) spi.McpToolProvider
func NewSuperAgentAutomationTools(svc *Service, autoTools spi.McpToolProvider) spi.McpToolProvider
func NewSuperAgentCollabTools(svc *Service, agents AgentCollabPort, invites InvitePort) spi.McpToolProvider

// 三场景 CLI 工具白名单（精确冻结，禁止随意扩大外部轮次权限）
var (
	ActingTools        = []string{"hire_member", "update_member", "place_member", "assign_task", "reply_task"}
	ExternalRoundTools = []string{ /* 7 只读/提计划/时钟 + tell_user + 6 InboxMcpTools */ }
	BackgroundTools    = []string{ /* ExternalRoundTools + 5 ActingTools */ }
	ChatTools          = []string{ /* 12 SuperAgentTools(去tell_user) + start_task + 7 Auto + 4 Collab + 6 Inbox + FileManage/Folder + Skill + DocRead */ }
)
```

### 4.2 全部 25 个 MCP 工具契约（Scope 均为 `SUPER`）

1. **`SuperAgentTools`（13 个）**：
   - 只读/提计划/时钟（7）：`list_team`、`list_projects`、`list_skills`、`propose_plan`（校验 `currentTeam + newHires <= teamLimit`、写 `proposed`、发 `PlanChanged(open=true)`）、`list_assigned_tasks`、`get_task`、`get_current_time`；
   - 仅后台（1）：`tell_user`（非后台轮次拒止；写主会话 `author="superagent:background"`）；
   - 行动工具（5，**全部强制 `RequireOwnInput` 阻断外部消息轮**）：`hire_member`（+`RequireApproved`，建前先校验 `checkPlace` 防孤儿成员）、`update_member`（+`RequireApproved`，已移除废弃 `character`）、`place_member`（免计划审批，支持 `"none"` 清除）、`assign_task`（+`RequireApproved`）、`reply_task`（校验在本人 `SuperAgentWork` 台账中）。
2. **`SuperAgentTaskTools`（1 个）**：
   - `start_task`（**仅实时对话**，免计划卡，支持可选 `member_id`，不填则为 `"独立任务"`，绑定 `grant.MessageID` 渲染内联卡片）。
3. **`SuperAgentAutomationTools`（7 个）**：
   - `create_automation`、`list_automations`、`get_automation`、`update_automation`、`set_automation_enabled`、`run_automation_now`、`delete_automation`；
   - **过滤剔除 `get_current_time`**（防与 `SuperAgentTools.get_current_time` 重名）；后台轮次（`SessionID==""`）一律拒止；始终 `isAdmin=false`。
4. **`SuperAgentCollabTools`（4 个）**：
   - `collab_find_people`、`collab_list_members`、`collab_invite`（仅生成 `PENDING` 卡片）、`collab_remove`；
   - 双重门禁：禁止后台轮次 + 目标 Agent 必须归属 `owner` 且 `!agent.IsSuper()`。

### 4.3 错误语义

- 全部 25 个工具捕获 `apperror.*` 后统一封装为 `spi.McpCallResult{IsError: true, Text: "错误：" + err.Error()}`，绝不抛 JSON-RPC `-32603`；
- `hire_member` 在创建 Agent 之前先校验目标 `project_id`/`parent_id`（防止先创建出 Agent 后因项目归档或环检测失败留下孤儿成员）。

### 4.4 SQL 契约

本篇无直接 SQL，全部经 `SA-36` 与下层 Service 落库。

---

## 5. 行为语义

### 5.1 工具注册唯一性与白名单隔离校验

- 4 组共 25 个工具名全局无重名（特别是 `SuperAgentAutomationTools` 显式过滤 `get_current_time`）；
- `ExternalRoundTools` 与 `ActingTools` 交集必须严格为空集 `∅`（编译后由单测强制断言），与服务端 `RequireOwnInput` 构成双重纵深防御；
- `hire_member` / `update_member` 配合 V1 移除 24 性格预设：`persona` 纯自定义，绝不出现 `character` 入参字段。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/orchestrator/mcp_tools_test.go`（红灯：覆盖 25 个工具全量注册无重名、`ExternalRoundTools ∩ ActingTools == ∅`、计划审批门禁、外部轮拒止、后台轮拒止 `start_task`/自动化/协作、实时轮拒止 `tell_user`、SuperAgent 禁共享、`hire_member` 预检防孤儿）。
- [ ] **T2**：实现 `tool_sets.go`、`mcp_tools.go`、`mcp_task_tools.go`、`mcp_automation_tools.go`、`mcp_collab_tools.go`。
- [ ] **T3**：运行 `go test -race -cover ./internal/orchestrator/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-37` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 25 工具精确计数与全局无重名 | `internal/orchestrator/mcp_tools_test.go` | 4 个 Provider 返回 `13 + 1 + 7 + 4 = 25` 个不重复工具名，`get_current_time` 仅出现 1 次 |
| 2 | 白名单集合与双重防注入矩阵 | `internal/orchestrator/mcp_tools_test.go` | `ExternalRoundTools` 不含任何 `ActingTools`；外部轮调用 5 个 `ActingTools` 全部返 `isError: true` |
| 3 | 前后台作用域互锁 | `internal/orchestrator/mcp_tools_test.go` | 实时轮调 `tell_user` 拒止；后台轮调 `start_task`/7 自动化/4 协作全部拒止 |
| 4 | 计划审批、团队上限、防孤儿与禁共享 SUPER | `internal/orchestrator/mcp_tools_test.go` | 未审批调 `hire_member`/`update_member`/`assign_task` 拒止；超 6 人拒 `propose_plan`；归档项目 `hire_member` 不留孤儿 Agent；`collab_invite` 拒 `SUPER` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/orchestrator/...` 全绿
- [ ] 25 个工具名称、Schema、门禁 100% 对齐，行覆盖率 ≥ 80%，零 Redis 依赖

---

## 10. 可提交节点

- 建议 commit：`feat(orchestrator-mcp): implement all 25 Orchestrator MCP tools and 3-scope safety matrix (spec 37, SA-37)`
