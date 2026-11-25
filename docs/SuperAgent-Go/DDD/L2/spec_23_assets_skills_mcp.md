# spec 23 | L2 企业资产 — 可复用技能库、渐进披露安装、删除确认卡与 6 个 Skill MCP 工具

- **状态**：`draft`
- **篇号顺序**：23（L2 企业资产域收口篇；落实 V1 裁剪红线——彻底移除 3 个消费级内置种子技能，交付企业双层技能库、抗符号链接逃逸渐进披露安装与 6 个 Skill MCP 工具）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-23`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §2
  - 归档母本：[_epic/L2/spec_06_assets_files_projects_skills.md](../_epic/L2/spec_06_assets_files_projects_skills.md) §3.3, §4.1.D, §4.2.C, §5.6
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-skills/src/main/java/com/superagent/skills/SkillService.java` (`L52-L468`)
    - `superagent-skills/src/main/java/com/superagent/skills/SkillDeletionService.java` (`L20-L71`)
    - `superagent-skills/src/main/java/com/superagent/skills/SkillMcpTools.java` (`L31-L134`)
    - `superagent-web/src/main/java/com/superagent/web/skill/SkillController.java` (`L26-L92`)
- **前置依赖**：
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant.Path`）
  - [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md)（`SkillStore`/`SkillDeletionStore`）
  - [L1/spec_11 (SA-11)](../L1/spec_11_runtime_launcher_runner_framework.md)（`ImageAttachmentStore` 魔数嗅探）
  - [L1/spec_14 (SA-14)](../L1/spec_14_runtime_model_gateway_mcp_client.md)（`McpGrants`/`WorkspaceFile`/`McpToolProvider`）
  - [L2/spec_16 (SA-16)](./spec_16_platform_user_org_quota.md)（`UserStore` 管理员身份判定）
- **下游消费者**：`SA-27`（Agent 技能绑定）、`SA-28`（对话运行期 `Install` 渐进披露）、`SA-29`（`AgentSkillMcpTools`: `skill_attach`/`skill_detach`）、`SA-30`（任务技能绑定快照 `BindTaskToAgent`/`ForTask`）、`SA-38`（MCP 注册）、`SA-39`（技能 11 个 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业内部需要将运维排障 SOP、代码评审规范、合规报批模板沉淀为可复用的 Markdown 技能（含规范附图），按个人层（`personal:<userId>`）与公司层（`company`）分级共享，并在 Agent/任务运行前以渐进披露方式安装至工作区 `.superagent/skills/<id>/SKILL.md`。

本篇（`SA-23`）完整交付 `internal/skills` 模块并严格执行商业私有化 V1 边界与安全强化：
1. **彻底移除消费级种子技能**：不移植原版 `SkillSeeds.java`（删掉「写作规范」「画图规范（设计师）」「运营文案」3 个消费级预设），不监听 `CompanyStarted`，新租户技能库初始纯净为空；
2. **`os.Lstat` + `filepath.EvalSymlinks` 双重抗符号链接逃逸**：运行期安装技能至工作区前双重校验 `.superagent` 与 `.superagent/skills` 绝非符号链接且解析路径严格落在真实工作区内，杜绝恶意工作区诱导覆写宿主机文件；
3. **对话内人工确认删卡机制**：Agent 调用 `skill_delete` 只能生成 `PENDING` 删除提案卡片，必须由本人点击确认并再次通过权限校验后才真正删除。

### 1.2 本篇边界

- **本篇负责**：`internal/skills/{service,deletion,mcp_tools}.go` 及全套单测。
- **本篇不负责**：`AgentSkillMcpTools`（`skill_attach`/`skill_detach` 需判定 Agent 所有权与 `IsSuper()`，留给 `L3/spec_29` 杜绝 L2→L3 反向依赖）；Gin HTTP 路由注册（交由 `SA-39`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `SkillService`（双层空间权限、管理员升降级、5 MB 光栅附图、任务绑定快照、`EvalSymlinks` 渐进披露安装；**无种子技能**） | `internal/skills/service.go` | 新增 |
| 2 | `SkillDeletionService`（`PENDING → CONFIRMED / CANCELLED` 二次鉴权删卡状态机） | `internal/skills/deletion.go` | 新增 |
| 3 | `SkillMcpTools`（全部 **6 个 Skill MCP 工具**，`isError: true` 领域错误封装） | `internal/skills/mcp_tools.go` | 新增 |
| 4 | 技能权限、附图魔数、符号链接逃逸、任务快照与 MCP 单元测试套件 | `internal/skills/{service,deletion,mcp_tools}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md) 已落盘的 3 张租户隔离表：
- `skill`（`space`: `personal:<userId> | company`、`enabled`、`deleted`）；
- `skill_binding`（`(company_id, owner_type, owner_id)`，**严格区分“行不存在”与“行存在但 `skill_ids=''`”**）；
- `skill_deletion`（`PENDING | CONFIRMED | CANCELLED`）。

磁盘目录（均经 `tenant.Path` 校验）：
- 技能附图：`<skillDir>/<companyID>/<skillID>/images/<name>`
- 工作区安装点：`<workspaceDir>/.superagent/skills/<skillID>/{SKILL.md,images/*}`

---

## 4. 冻结契约

### 4.1 导出符号

```go
package skills

const (
	MaxContentChars = 200_000
	SpacePersonal   = "personal"
	SpaceCompany    = "company"
)

type SkillRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Content     string `json:"content"`
	Space       string `json:"space"` // "personal" | "company"
	Enabled     *bool  `json:"enabled"`
}

type SkillService struct { /* store store.SkillStore, users store.UserStore, rootDir string */ }
func NewSkillService(st store.SkillStore, users store.UserStore, rootDir string) *SkillService
func (s *SkillService) List(ctx context.Context, user string) ([]SkillDto, error)
func (s *SkillService) Get(ctx context.Context, id, user string) (*SkillDto, error)
func (s *SkillService) Create(ctx context.Context, req SkillRequest, user string) (*SkillDto, error)
func (s *SkillService) Update(ctx context.Context, id string, req SkillRequest, user string) (*SkillDto, error)
func (s *SkillService) Delete(ctx context.Context, id, user string) error
func (s *SkillService) RequireDeletable(ctx context.Context, id, user string) (string, error)
func (s *SkillService) AddImageBase64(ctx context.Context, id, name, base64Data, user string) (*SkillDto, error)
func (s *SkillService) AddImageBytes(ctx context.Context, id, requestedName string, data []byte, user string) (string, error)
func (s *SkillService) ReadImage(ctx context.Context, id, name, user string) (*ImagePayload, error)
func (s *SkillService) DeleteImage(ctx context.Context, id, name, user string) (*SkillDto, error)
func (s *SkillService) Images(ctx context.Context, id string) ([]string, error)
func (s *SkillService) AgentSkillIDs(ctx context.Context, agentID string) ([]string, error)
func (s *SkillService) SetAgentSkills(ctx context.Context, agentID string, skillIDs []string, user string) error
func (s *SkillService) BindTaskToAgent(ctx context.Context, taskID, agentID string) error
func (s *SkillService) ForAgent(ctx context.Context, agentID, owner string) ([]model.Skill, error)
func (s *SkillService) ForTask(ctx context.Context, taskID, owner string) ([]model.Skill, error)
func (s *SkillService) Install(ctx context.Context, workspaceDir string, skills []model.Skill, canManage bool) string

type SkillDeletionService struct { /* skills *SkillService, deletions store.SkillDeletionStore */ }
func (s *SkillDeletionService) Propose(ctx context.Context, skillID, user, agentID, sessionID, messageID string) (*model.SkillDeletion, error)
func (s *SkillDeletionService) List(ctx context.Context, agentID, user string) ([]SkillDeletionDto, error)
func (s *SkillDeletionService) Confirm(ctx context.Context, id, user string) (*SkillDeletionDto, error)
func (s *SkillDeletionService) Cancel(ctx context.Context, id, user string) (*SkillDeletionDto, error)
```

### 4.2 路由与 6 个 MCP 工具契约

- **HTTP 路由**（11 个端点由 `L5/spec_39` 挂载）：`GET/POST /api/v1/skills`、`GET/PUT/DELETE /api/v1/skills/:id`、`POST /api/v1/skills/:id/images`、`GET/DELETE /api/v1/skills/:id/images/:name`（图片响应带 `X-Content-Type-Options: nosniff`、`Cache-Control: private, max-age=2592000`）、`GET /api/v1/skills/deletions?agentId=`、`POST /api/v1/skills/deletions/:deletionId/(confirm|cancel)`。
- **6 个 Skill MCP 工具**（作用域 `CHAT`, `TASK`, `SUPER` 且 `SUPER` 后台轮 `sessionId==""` 拒止）：
  1. `skill_list`：列出可见未删技能摘要；
  2. `skill_get`：返回元信息、图片名列表与 Markdown 正文；
  3. `skill_create`：新建技能（默认 `personal`，`company` 仅管理员；**不自动绑定到任何 Agent**）；
  4. `skill_update`：部分更新，需写权限；
  5. `skill_add_image`：经 `WorkspaceFile` 读工作区图片、魔数嗅探存入 `images/` 并返回 `![说明](images/<stored>)`；
  6. `skill_delete`：**严禁在 `TASK` 或缺少 `agentId`/`messageId` 时调用**；仅生成 `PENDING` 确认卡片，绝不直接删除。

### 4.3 错误语义

- 他人私有技能、不存在图片、他人删卡：`404 not_found`；
- 普通员工写/删公司技能、重复确认已处理删卡：`403 forbidden`；
- 正文超 200,000 字符、附图非光栅（拒绝 SVG）或超 5 MB、非法 UUID 技能 ID：`400 invalid_argument`；
- MCP 工具捕获领域错误一律返回 `spi.McpCallResult{IsError: true, Text: err.Error()}`。

### 4.4 SQL 契约

- `SkillStore.Binding(ctx, ownerType, ownerID)` 返回 `(ids []string, exists bool, err error)`：
  - `exists == false`（未绑定记录，如无 Agent 独立任务）→ `ForTask` 返回 `owner` 全部可见启用技能；
  - `exists == true && len(ids) == 0`（显式空绑定）→ `ForTask` 返回空切片 `[]`，绝不回退全量技能。

---

## 5. 行为语义

### 5.1 双层空间权限与升降级（对齐 `SkillService.java:76-205`）

- `space` 落库：`personal:<userID>` 或 `company`（历史空串视同 `company`）；
- 读权限：个人层仅本人可见（他人 `404`），公司层全租户可见；
- 写/删权限：个人层仅本人；公司层仅 `User.IsAdmin()`（普通员工 `403`）；
- 管理员调用 `Update` 改变 `space` 可实现 `personal:<userID>` ↔ `company` 升级发布或降级收回。

### 5.2 `Install` 渐进披露与 `EvalSymlinks` 纵深防御（对齐 `SkillService.java:235-279` 强化版）

```go
package skills

func (s *SkillService) Install(ctx context.Context, workspaceDir string, list []model.Skill, canManage bool) string {
	if strings.TrimSpace(workspaceDir) == "" { return "" }
	realWorkspace, err := filepath.EvalSymlinks(workspaceDir)
	if err != nil { return "" }
	superagentDir := filepath.Join(realWorkspace, ".superagent")
	skillsDir := filepath.Join(superagentDir, "skills")
	// 1. 先 Lstat 拦截恶意预埋符号链接
	if isSymlink(superagentDir) || isSymlink(skillsDir) {
		slog.WarnContext(ctx, "workspace .superagent is a symlink; aborting skill install")
		return ""
	}
	if err := os.RemoveAll(skillsDir); err != nil { return "" }
	if len(list) == 0 {
		if canManage { return manageHint() }
		return ""
	}
	if err := os.MkdirAll(skillsDir, 0o750); err != nil { return "" }
	// 2. 二次 EvalSymlinks 断言目标目录严格留在 realWorkspace 子树内
	realSkillsDir, err := filepath.EvalSymlinks(skillsDir)
	if err != nil || !isSubPath(realWorkspace, realSkillsDir) { return "" }
	// 3. 逐个写入 <realSkillsDir>/<skillID>/{SKILL.md, images/*}（均拒绝符号链接），拼接紧凑单行 # Skills 索引
	return s.writeSkillsIndex(ctx, realSkillsDir, list, canManage)
}
```

### 5.3 `SkillDeletionService` 二次鉴权删卡（对齐 `SkillDeletionService.java:30-70`）

- `Propose`：先调 `skills.RequireDeletable(ctx, skillID, user)` 校验权限后写入 `PENDING` 卡片；
- `Confirm`：校验 `d.Requester == user`（否则 `404`）且 `d.Status == PENDING`（否则 `403`），**再次执行 `skills.Delete(ctx, d.SkillID, user)`**（防止提案后用户被管理员降权导致越权删除）再置 `CONFIRMED`；
- `Cancel`：校验本人且 `PENDING` 后置 `CANCELLED`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/skills/service_test.go`（红灯：双层权限、无种子技能验证、升降级、5 MB 光栅附图、绑定空切片 vs 无绑定语义、`EvalSymlinks` 逃逸拦截）。
- [ ] **T2**：实现 `internal/skills/service.go`。
- [ ] **T3**：编写 `internal/skills/deletion_test.go` 与 `mcp_tools_test.go`（红灯）→ 实现 `SkillDeletionService` 与 6 个 Skill MCP 工具。
- [ ] **T4**：运行 `go test -race -cover ./internal/skills/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-23` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `skill.dir` 配置项说明。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 无消费级种子与双层空间权限 | `internal/skills/service_test.go` | 新租户技能列表为空；他人私有技能返 `404`；普通员工改公司技能返 `403`；管理员可升降级 |
| 2 | 附图魔数校验与符号链接拒止 | `internal/skills/service_test.go` | SVG/伪造头/超 5 MB 拒止；工作区预埋 `.superagent/skills` 符号链接时 `Install` 拒写且不触碰宿主机目标 |
| 3 | 任务绑定快照空绑定 vs 无绑定 | `internal/skills/service_test.go` | 有绑定行（空切片）时 `ForTask` 返回 `[]`；无绑定行时返回全部可见启用技能 |
| 4 | 删卡二次鉴权与 6 个 MCP 工具 | `internal/skills/mcp_tools_test.go` | `TASK` 调 `skill_delete` 返 `isError: true`；`CHAT` 仅建 `PENDING` 卡；提案后被降权者点 `Confirm` 返 `403`；`SUPER` 后台轮拒止 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/skills/...` 全绿
- [ ] 代码中零残余 `SkillSeeds` /「写作规范」「画图规范」「运营文案」消费级种子
- [ ] §2 交付物表 4 项齐备，6 个 MCP 工具完整注册，行覆盖率 ≥ 80%，零 Redis 引入

---

## 10. 可提交节点

- 建议 commit：`feat(skills): implement SkillService (no consumer seeds), EvalSymlinks installer, SkillDeletionService and 6 Skill MCP tools (spec 23, SA-23)`
