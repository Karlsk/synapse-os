# spec 27 | L3 Agent 协作域 — Agent 生命周期、协作者邀请、任务执行者桥接与运行日志

- **状态**：`draft`
- **篇号顺序**：27（L3 Agent 域首篇；彻底移除 24 套消费级内置性格预设，补齐原版缺失的 `MergeUpdate` 审计日志，实现 `spi.TaskDoers` 解耦 `internal/task`）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-27`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §1
  - 归档母本：[_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md](../_epic/L3/spec_09_agent_chat_liveturn_memory_collab.md) §1.1, §4, §5.1, §5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-agent/src/main/java/com/superagent/agent/AgentService.java` (`L1-L516`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentInviteService.java` (`L1-L135`)
    - `superagent-agent/src/main/java/com/superagent/agent/AgentTaskDoers.java` (`L1-L37`)
    - `superagent-agent/src/main/java/com/superagent/agent/log/AgentLogService.java` (`L1-L112`)
    - `superagent-web/src/main/java/com/superagent/web/agent/AgentController.java` (`L1-L113`)
    - `superagent-web/src/main/java/com/superagent/web/agent/AgentInviteController.java` (`L1-L39`)
    - `superagent-web/src/main/java/com/superagent/web/agentlog/AgentLogController.java` (`L1-L43`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup` 供 `AgentLogService` 定时修剪）
  - [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md)（`AgentStore`/`AgentMemberStore`/`AgentInviteStore`/`AgentParentStore`/`AgentLogStore`）
  - [L2/spec_16 (SA-16)](../L2/spec_16_platform_user_org_quota.md)（`PeopleDirectory`）
  - [L2/spec_22 (SA-22)](../L2/spec_22_assets_projects_access.md)（`spi.ProjectAccess`）
  - [L2/spec_23 (SA-23)](../L2/spec_23_assets_skills_mcp.md)（`SkillService`）
- **下游消费者**：`SA-28`（`ChatService` 消费 `AgentService`/`AgentLogService`）、`SA-29`（记忆与 10 个 Agent MCP 工具）、`SA-30`（`TaskService` 通过 `spi.TaskDoers` 消费 `AgentTaskDoers`，零包级互引）、`SA-36`（`SuperAgentService` 雇佣成员与设上下级）、`SA-40`（Agent/邀请/日志 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业内部员工需要按岗位职责自定义专属 Agent、将 Agent 共享给直属协作者或协作项目、维护 Agent 汇报层级并完整审计配置与运行变更。本篇（`SA-27`）在 1.5 人天粒度内交付 Agent 注册表、邀请确认卡、运行日志与 `spi.TaskDoers` 桥接，同时落实 V1 商业化裁剪与原版修复：
1. **彻底移除 24 套内置性格预设（`AgentCharacters`）与 24 头像白名单**：纯自定义 `persona`（`BuildSystemPrompt` 仅拼姓名、人设与语言规范），`avatar` 放开为 `<= 32` rune 任意短文本/Emoji（`GET /api/v1/agents/characters` 在 L5 返回 `[]` 保前端兼容）；
2. **修复原版 `AgentService.MergeUpdate` 遗漏审计日志的 Bug**：正式经构造函数注入 `AgentLogService`，每次 `MergeUpdate` 真实落库 `category="settings"` 审计日志；
3. **单向依赖解耦任务层**：通过 `AgentTaskDoers` 实现 L1 `spi.TaskDoers`，`internal/agent` 绝不 import `internal/task`。

### 1.2 本篇边界

- **本篇负责**：`internal/agent/{types.go,agent_service.go,invite_service.go,task_doers.go,log_service.go}` 及对应单测。
- **本篇不负责**：17 步 `ChatService` 与 `LiveTurn`（交由 `SA-28`）；长期记忆循环与 10 个 Agent MCP 工具（交由 `SA-29`）；Gin 路由挂载（交由 `SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 包常量、默认推荐头像列表与 `AgentTurnCustomizer` 接口 | `internal/agent/types.go` | 新增 |
| 2 | `AgentService`（CRUD、纯自定义 `BuildSystemPrompt`、环检测、`CreateChild` 50 上限、修复 `MergeUpdate` 写 `runLog`、成员管理） | `internal/agent/agent_service.go` | 新增 |
| 3 | `AgentInviteService`（协作邀请提案、确认发布 `AgentShared` 与取消） | `internal/agent/invite_service.go` | 新增 |
| 4 | `AgentTaskDoers`（实现 `spi.TaskDoers`） | `internal/agent/task_doers.go` | 新增 |
| 5 | `AgentLogService`（5 分类运行日志、旁路容错与每小时 30 天/5000 条自动修剪） | `internal/agent/log_service.go` | 新增 |
| 6 | Agent/邀请/日志/TaskDoers 单测套件 | `internal/agent/{agent_service,invite_service,log_service}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_09 (SA-09)](../L1/spec_09_pg_storage_agent_task.md) 已落盘的 `agent`（已清理 `character_key`/`tier`）、`agent_parent`、`agent_member`、`agent_invite`、`agent_log` 5 张租户隔离表。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package agent

const (
	SuperAgentName        = "SuperAgent"
	SuperAgentAvatar      = "🦸"
	MaxAgentsPerUser      = 50
	MaxAvatarRunes        = 32
	LogCategorySession    = "session"
	LogCategoryTurn       = "turn"
	LogCategoryMemory     = "memory"
	LogCategorySuperAgent = "superagent"
	LogCategorySettings   = "settings"
)

var DefaultAvatars = []string{
	"🧑‍💻", "👩‍💻", "👨‍💻", "🧑‍🔬", "👩‍🔬", "👨‍🔬", "🧑‍🎨", "👩‍🎨",
	"🧑‍🚀", "👩‍🚀", "🧑‍🏫", "👩‍🏫", "🧑‍💼", "👩‍💼", "👨‍💼", "🧑‍🔧",
	"👩‍🔧", "🧑‍⚕️", "🧑‍🍳", "🧑‍🎓", "🕵️", "🧙", "🦸", "🥷",
}

type AgentTurnCustomizer interface {
	Handles(agent model.Agent) bool
	SystemPrompt(ctx context.Context, agent model.Agent) string
	RunsCommands(agent model.Agent) bool
	Tools(agent model.Agent) []string
	OpenGrant(ctx context.Context, agent model.Agent, userID string, session model.ChatSession, messageID string) (*mcp.Grant, error)
	Prompt(ctx context.Context, agent model.Agent, session model.ChatSession, userPrompt string) (string, error)
}

func BuildSystemPrompt(a model.Agent) string

type AgentService struct { /* store, parents, members, projects spi.ProjectAccess, skills, users, runLog *AgentLogService, bus */ }
func (s *AgentService) Create(ctx context.Context, req CreateAgentRequest, owner string) (*AgentDto, error)
func (s *AgentService) CreateTeamMember(ctx context.Context, owner, superAgentID, name, avatar, persona, modelID string, skillIDs []string) (*model.Agent, error)
func (s *AgentService) CreateChild(ctx context.Context, owner, fromAgentID, name, persona, modelID string, skillIDs []string) (*model.Agent, error)
func (s *AgentService) List(ctx context.Context, user string) ([]AgentDto, error)
func (s *AgentService) Owned(ctx context.Context, owner string) ([]model.Agent, error)
func (s *AgentService) EnsureSuperAgent(ctx context.Context, owner string) (*model.Agent, error)
func (s *AgentService) Get(ctx context.Context, id, user string) (*AgentDto, error)
func (s *AgentService) Require(ctx context.Context, id, owner string) (*model.Agent, error)
func (s *AgentService) RequireViewer(ctx context.Context, id, user string) (*model.Agent, error)
func (s *AgentService) Update(ctx context.Context, id string, req UpdateAgentRequest, owner string) (*AgentDto, error)
func (s *AgentService) MergeUpdate(ctx context.Context, owner, id string, name, persona, modelID *string, skillIDs []string, skillsSent bool) (string, error)
func (s *AgentService) Delete(ctx context.Context, id, owner string) error
func (s *AgentService) SetParent(ctx context.Context, agentID, owner, parentID string) error
func (s *AgentService) ParentOf(ctx context.Context, agentID string) string
func (s *AgentService) Invite(ctx context.Context, id, owner string, usernames []string) (*AgentDto, error)
func (s *AgentService) RemoveMember(ctx context.Context, id, caller, who string) error

type AgentInviteService struct { /* invites, agents, members, directory, bus */ }
func (s *AgentInviteService) FindPeople(ctx context.Context, caller, query string) ([]DirectoryHit, error)
func (s *AgentInviteService) Propose(ctx context.Context, agentID, owner string, usernames []string, sessionID, messageID string) (*model.AgentInvite, error)
func (s *AgentInviteService) List(ctx context.Context, agentID, viewer string) ([]AgentInviteDto, error)
func (s *AgentInviteService) Confirm(ctx context.Context, agentID, inviteID, owner string) (*AgentInviteDto, error)
func (s *AgentInviteService) Cancel(ctx context.Context, agentID, inviteID, owner string) (*AgentInviteDto, error)

type AgentLogService struct { /* store, companies, retentionDays, maxPerAgent */ }
func (s *AgentLogService) Start(bg *lifecycle.BackgroundGroup)
func (s *AgentLogService) Info(ctx context.Context, a *model.Agent, category, summary, detail string)
func (s *AgentLogService) Warn(ctx context.Context, a *model.Agent, category, summary, detail string)
func (s *AgentLogService) Error(ctx context.Context, a *model.Agent, category, summary, detail string)
func (s *AgentLogService) List(ctx context.Context, agentID, category string, limit int) ([]model.AgentLogEntry, error)
```

### 4.2 路由契约（由 `L5/spec_40` 挂载）

- `GET/POST /api/v1/agents`、`GET/PUT/DELETE /api/v1/agents/:id`、`GET /api/v1/agents/avatars`、`GET /api/v1/agents/characters`（固定返 `[]`）；
- `POST /api/v1/agents/:id/members`、`DELETE /api/v1/agents/:id/members/:username`；
- `GET /api/v1/agents/:id/invites`、`POST /api/v1/agents/:id/invites/:inviteId/{confirm,cancel}`；
- `GET /api/v1/agents/:id/logs?category=&limit=300`。

### 4.3 错误语义

- Agent 不存在或无查看权：`404 not_found`；
- 协作者越权修改/删除/邀请他人：`403 forbidden`；
- 超过每人 50 个 Agent 上限、不区分大小写重名、汇报成环、修改/删除 `SUPER` Agent 名称或将其设为上下级、头像超 32 rune：`400 invalid_argument`。

### 4.4 SQL 契约

- `AgentLogStore.Prune(ctx, before, keepPerAgent)` 清理早于 30 天及超出每 Agent 5000 条的历史日志；
- `AgentParentStore.Set(ctx, agentID, "")` 传空串即删除父子关系行。

---

## 5. 行为语义

### 5.1 纯自定义人设、自由头像与上下级环检测（对齐 `AgentService.java:95-515`）

- **`BuildSystemPrompt(a)`**：无 `persona` 返回空串（沿用 CLI 默认）；有 `persona` 返回 `"Your name is <name>.\n\n<persona>\n\nAlways reply in the language the user writes in."`；彻底移除 24 套内置性格模板；
- **头像校验**：留空时新建随机取 `DefaultAvatars`（`SUPER` 固定 `"🦸"`）；非空仅校验 `utf8.RuneCountInString(v) <= 32`，不限制 24 Emoji 白名单；
- **`SetParent`**：禁止 `SUPER` 设上级或被设为上级；沿 `ParentOf` 祖先链向上追溯，触达 `agentID` 立即报环错；
- **`CreateChild`**：校验每人 `<= 50` 个且大小写不敏感不重名；若父 Agent 归当前用户所有且非 `SUPER`，自动设置上级并调用 `projects.PlaceAgent` 继承同项目；
- **`MergeUpdate`（V1 Bug 修复）**：允许部分更新 `name`/`persona`/`model`/`skill_ids`，拦截修改 `SUPER` 名称，更新完成后通过已注入的 `s.runLog.Info(ctx, agent, LogCategorySettings, ...)` 真实落库审计日志。

### 5.2 `AgentInviteService` 与 `AgentLogService`

- `Propose`：仅 Owner 且非 `SUPER`，解析有效启用同事落库 `PENDING` 卡片，不直接加成员；
- `Confirm`：校验 Owner 且 `PENDING` → 将所有受邀人写入 `AgentMemberStore(active=true)` → 为每位新成员发布 `eventbus.AgentShared` 通知；
- `AgentLogService`：写入异常仅打 `slog.WarnContext` 旁路容错，绝不中断主流程；`Start(bg)` 延迟 1 分钟后每 60 分钟经 `CompanyService.ForEachEnabled` 执行 `Prune(now-30d, 5000)`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `agent_service_test.go`（红灯：纯自定义 `BuildSystemPrompt`、自由头像 32 rune、环检测、`CreateChild` 50 上限与重名、`MergeUpdate` 写 `runLog` 审计、SUPER 保护）→ 实现 `types.go`、`agent_service.go`、`task_doers.go`。
- [ ] **T2**：编写 `invite_service_test.go` 与 `log_service_test.go`（红灯）→ 实现 `invite_service.go` 与 `log_service.go`。
- [ ] **T3**：运行 `go test -race -cover ./internal/agent/...` 与 `internal/arch_test.go`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-27` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `agent_log.retention_days=30`、`agent_log.max_per_agent=5000`。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 纯自定义人设、自由头像与 50 上限 | `internal/agent/agent_service_test.go` | 自定义任意短 Emoji/文字通过，超 32 rune 返 `400`；超 50 个或大小写重名返 `400`；无 24 性格预设残留 |
| 2 | 上下级环检测与 `MergeUpdate` 审计修复 | `internal/agent/agent_service_test.go` | `A→B→A` 报环错；`MergeUpdate` 局部改 `persona` 保留原 `name`/`model` 并真实写入 `category="settings"` 日志；改 SUPER 名称不落库 |
| 3 | 邀请提案确认权限矩阵 | `internal/agent/invite_service_test.go` | 非 Owner 返 `403`；`Confirm` 写入 `AgentMember` 并发布 `AgentShared`；重复确认返 `400` |
| 4 | `AgentLogService` 旁路容错与清理 | `internal/agent/log_service_test.go` | Store 报错不外抛；`limit` 钳位 `[1,1000]`；定时清理传 `now-30d` 与 `5000` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/agent/...` 全绿
- [ ] 代码中零残余 `AgentCharacters` 24 预设与 `character_key`
- [ ] `internal/arch_test.go` 通过（`internal/agent` 零引入 `internal/task`，零 Redis），行覆盖率 ≥ 80%

---

## 10. 可提交节点

- 建议 commit：`feat(agent): implement AgentService (custom persona, runLog fix), InviteService, TaskDoers and AgentLogService (spec 27, SA-27)`
