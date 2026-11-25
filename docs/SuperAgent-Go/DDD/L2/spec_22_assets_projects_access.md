# spec 22 | L2 企业资产 — 协作项目空间与 `spi.ProjectAccess` 权限实现

- **状态**：`draft`
- **篇号顺序**：22（L2 企业资产域第二篇；交付封闭成员制协作项目空间及跨模块共享的 `spi.ProjectAccess` 权限引擎）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-22`
- **预估工作量**：`1 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §2
  - 归档母本：[_epic/L2/spec_06_assets_files_projects_skills.md](../_epic/L2/spec_06_assets_files_projects_skills.md) §3.2, §4.1.C, §5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-project/src/main/java/com/superagent/project/ProjectService.java` (`L36-L270`)
    - `superagent-project/src/main/java/com/superagent/project/ProjectForbiddenException.java` (`L5-L10`)
    - `superagent-project/src/main/java/com/superagent/project/ProjectNotFoundException.java` (`L5-L10`)
    - `superagent-web/src/main/java/com/superagent/web/project/ProjectController.java` (`L21-L76`)
- **前置依赖**：
  - [L1/spec_05 (SA-05)](../L1/spec_05_core_domain_platform_assets_contracts.md)（`Project`/`ProjectMember`/`ProjectItemKind`/`ProjectStore`/`ProjectItemStore`/`spi.ProjectAccess`）
  - [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md)（`project`/`project_member`/`project_item` PG 存储实现）
  - [L2/spec_16 (SA-16)](./spec_16_platform_user_org_quota.md)（`UserStore` 解析不可变 `userID`）
- **下游消费者**：`SA-21`（文件项目可见性与 `RecordFile`）、`SA-27`（Agent 共享发言人前缀修复覆盖项目成员）、`SA-28`（对话项目归档只读检查）、`SA-30`（`RecordTask` 永久归属）、`SA-36`（SuperAgent 项目上下文）、`SA-39`（项目 9 个 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业团队通常围绕具体项目协同使用多个 Agent、产生任务与交付文件。本篇（`SA-22`）交付简洁、高内聚的 `internal/project` 模块（1 人天独立 PR），实现：
1. **成员封闭防探测**：非项目成员访问任何项目详情、资源或试图将 Agent 放入该项目均统一返回 `404`（不暴露项目存在性）；
2. **可逆归档与永久资产继承**：项目仅支持 `ACTIVE` ↔ `ARCHIVED` 可逆状态切换（无硬删）；任务与文件在创建时通过 `RecordTask` / `RecordFile` 永久归属当下项目，即便后续 Agent 移出项目，历史产出依然留在原项目内可审计；
3. **跨层解耦枢纽**：完整实现 L1 `spi.ProjectAccess` 的 8 个方法，使 `SA-21`（文件）、`SA-27`/`SA-28`（Agent 与对话发言人前缀修复）、`SA-30`（任务）无需反向耦合 `internal/project` 具体实现。

### 1.2 本篇边界

- **本篇负责**：`internal/project/service.go` 及完整单元测试。
- **本篇不负责**：Gin HTTP 路由注册（交由 `SA-39`）；Agent 与文件本身的 CRUD（分别在 `SA-27`、`SA-21`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `ProjectService`（项目 CRUD、成员管理、可逆归档、Agent 归属、完整实现 `spi.ProjectAccess` 8 方法） | `internal/project/service.go` | 新增 |
| 2 | 项目成员权限、归档只读、永久继承与 `spi.ProjectAccess` 单测套件 | `internal/project/service_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，全部复用 [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md) 已落盘的 3 张租户隔离表：
- `project`（`status`: `ACTIVE | ARCHIVED`，含 `archived_at`）；
- `project_member`（`username` 实际存储不可变 `app_user.id`，移除时翻 `active=FALSE` 保留审计）；
- `project_item`（联合主键 `(company_id, kind, item_id)` 天然保证单个 Agent/Task/File 至多归属 1 个项目）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package project

const NameMaxLen = 80

// 编译期断言严格实现 L1 spi.ProjectAccess
var _ spi.ProjectAccess = (*ProjectService)(nil)

type ProjectDto struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Owner       string             `json:"owner"`
	Status      model.ProjectStatus `json:"status"`
	Members     []ProjectMemberDto `json:"members"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	ArchivedAt  *time.Time         `json:"archivedAt,omitempty"`
}

type ProjectService struct { /* store store.ProjectStore, items store.ProjectItemStore, agents store.AgentStore, users store.UserStore */ }

func NewProjectService(ps store.ProjectStore, items store.ProjectItemStore, agents store.AgentStore, users store.UserStore) *ProjectService
func (s *ProjectService) Create(ctx context.Context, owner, name, description string) (*ProjectDto, error)
func (s *ProjectService) List(ctx context.Context, user string) ([]ProjectDto, error)
func (s *ProjectService) Get(ctx context.Context, id, user string) (*ProjectDto, error)
func (s *ProjectService) Update(ctx context.Context, id, user, name, description string) (*ProjectDto, error)
func (s *ProjectService) Archive(ctx context.Context, id, user string) (*ProjectDto, error)
func (s *ProjectService) Reopen(ctx context.Context, id, user string) (*ProjectDto, error)
func (s *ProjectService) AddMembers(ctx context.Context, id, owner string, identifiers []string) (*ProjectDto, error)
func (s *ProjectService) RemoveMember(ctx context.Context, id, owner, targetUserID string) (*ProjectDto, error)
func (s *ProjectService) SetAgentProject(ctx context.Context, agentID, user, projectID string) error

// spi.ProjectAccess 全部 8 个方法
func (s *ProjectService) AgentsThroughProjects(ctx context.Context, user string) ([]string, error)
func (s *ProjectService) SharesProject(ctx context.Context, agentID, user string) (bool, error)
func (s *ProjectService) ProjectOfAgent(ctx context.Context, agentID string) (string, bool, error)
func (s *ProjectService) Archived(ctx context.Context, agentID string) (bool, error)
func (s *ProjectService) VisibleThroughProject(ctx context.Context, kind model.ProjectItemKind, itemID, user string) (bool, error)
func (s *ProjectService) ItemsInProject(ctx context.Context, projectID string, kind model.ProjectItemKind, user string) ([]string, error)
func (s *ProjectService) RecordTask(ctx context.Context, taskID, agentID string) error
func (s *ProjectService) RecordFile(ctx context.Context, fileID, agentID, taskID string) error
```

### 4.2 路由契约（9 个端点由 `L5/spec_39` 挂载）

- `GET /api/v1/projects`、`POST /api/v1/projects`
- `GET /api/v1/projects/:id`、`PUT /api/v1/projects/:id`
- `POST /api/v1/projects/:id/archive`、`POST /api/v1/projects/:id/reopen`
- `POST /api/v1/projects/:id/members`、`DELETE /api/v1/projects/:id/members/:username`
- `PUT /api/v1/agents/:agentId/project`

### 4.3 错误语义

- 非项目成员访问或放入 Agent：`404 not_found`（防探测）；
- 非项目 Owner 修改/归档/重开/增删成员，或非 Agent Owner 调整 Agent 归属：`403 forbidden`；
- 项目名空或超 80 字符、尝试移出项目 Owner 自身、试图将 `SuperAgent` 放入项目、向已归档项目添加 Agent：`400 invalid_argument`。

### 4.4 SQL 契约

- `ProjectStore.SaveMember` 走 `ON CONFLICT (company_id, project_id, username) DO UPDATE SET active = EXCLUDED.active, updated_at = EXCLUDED.updated_at`（重新拉回旧成员时保留原 `created_at` 与 `added_by`）；
- `ProjectItemStore.Assign` 走 `ON CONFLICT (company_id, kind, item_id) DO UPDATE SET project_id = EXCLUDED.project_id, updated_at = EXCLUDED.updated_at`。

---

## 5. 行为语义

### 5.1 封闭访问、成员解析与归属流转（对齐 `ProjectService.java:55-269`）

1. **`Create`**：校验名称 `1..80` rune，保存 `ACTIVE` 项目后立即将 `owner` 写入 `project_member(active=true)`；
2. **`AddMembers`**：逐项先按 `UserStore.FindByID` 再按 `UserStore.FindByUsername` 解析，统一落库为不可变 `u.ID`（跳过未知或停用用户并记录警告）；
3. **`RemoveMember`**：若 `targetUserID == p.Owner` 返回 `400`；否则将成员行翻为 `active = false`；
4. **`SetAgentProject`**：
   - 校验 Agent 存在且未删；仅允许 `a.Owner == user`（否则 `403`）；禁止 `a.IsSuper()`（否则 `400`）；
   - `projectID == ""` 时 `items.Unassign(AGENT, agentID)`；
   - 非空时 `requireMember(ctx, projectID, user)` 且校验 `p.Status != ARCHIVED`，写入 `items.Assign(AGENT, agentID, projectID)`（历史任务与文件留在原项目不受影响）；
5. **`RecordFile(ctx, fileID, agentID, taskID)`**：优先读取 `taskID` 所在项目，若无则回退 `agentID` 所在项目，非空则永久写入 `project_item(FILE, fileID, pid)`；
6. **`SharesProject(ctx, agentID, user)`**：查询 `agentID` 所在项目，再检查 `user` 是否为该项目的 `active` 成员（直接支撑 `SA-27` 修复共享 Agent 发言人前缀覆盖项目成员）。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/project/service_test.go`（红灯：覆盖创建、非成员 404、非 Owner 403、移除 Owner 400、归档/重开、SuperAgent 400、永久资产继承与 `spi.ProjectAccess` 全部 8 方法）。
- [ ] **T2**：实现 `internal/project/service.go` 使全部单测转绿。
- [ ] **T3**：运行 `go test -race -cover ./internal/project/...` 与 `internal/arch_test.go`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-22` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 成员封闭 `404` 与 Owner `403` | `internal/project/service_test.go` | 非成员 `Get`/`SetAgentProject` 返 `404`；普通成员修改/归档/增删成员返 `403` |
| 2 | 成员增删与重新激活 | `internal/project/service_test.go` | 按 `username` 添加解析为不可变 `u.ID`；移除 Owner 返 `400`；移除再添加恢复 `active=true` |
| 3 | 可逆归档与 Agent 归属边界 | `internal/project/service_test.go` | `Archive` 后 `Archived(agentID)==true` 且禁放新 Agent；`Reopen` 后恢复可写；放 `SuperAgent` 返 `400` |
| 4 | 永久资产继承与 `spi.ProjectAccess` 8 方法 | `internal/project/service_test.go` | Agent 换项目后原任务/文件仍通过 `ItemsInProject`/`VisibleThroughProject` 属于旧项目；`SharesProject` 精准判定项目成员 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/project/...` 全绿
- [ ] 编译期断言 `var _ spi.ProjectAccess = (*ProjectService)(nil)` 通过
- [ ] 行覆盖率 ≥ 85%，`internal/arch_test.go` 通过（零引用 L3，零 Redis）

---

## 10. 可提交节点

- 建议 commit：`feat(project): implement ProjectService and spi.ProjectAccess with member isolation and archival (spec 22, SA-22)`
