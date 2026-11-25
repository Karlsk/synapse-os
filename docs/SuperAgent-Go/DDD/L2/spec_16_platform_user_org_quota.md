# spec 16 | L2 平台管理 — 用户管理、账号改密头像、通讯录、组织树与租户配额

- **状态**：`draft`
- **篇号顺序**：16（L2 平台核心域第二篇；承接 `SA-15` 租户与认证，交付企业内部用户/组织架构/配额与员工离职交接扩展点）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-16`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §2–§3
  - 归档母本：[_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md](../_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md) §4–§5.4
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-platform/src/main/java/com/superagent/platform/user/UserService.java` (`L48-L155`)
    - `superagent-platform/src/main/java/com/superagent/platform/user/AccountService.java` (`L48-L152`)
    - `superagent-platform/src/main/java/com/superagent/platform/user/PeopleDirectory.java`
    - `superagent-platform/src/main/java/com/superagent/platform/org/OrgService.java`
    - `superagent-platform/src/main/java/com/superagent/platform/tenant/QuotaService.java` (`L49-L93`)
    - `superagent-core/src/main/java/com/superagent/core/org/TeamPaths.java` (`L16-L59`)
- **前置依赖**：
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant.TenantPaths`、`memcache`）
  - [L1/spec_05 (SA-05)](../L1/spec_05_core_domain_platform_assets_contracts.md)（`User`/`TeamPaths`/`UserStore`/`OrgStore`/`UsageStore`/`OwnerTransferHook`/`LicenseQuotaHook`）
  - [L1/spec_07 (SA-07)](../L1/spec_07_pg_storage_platform_providers.md)（`app_user`/`user_manager`/`user_team`/`company` 存储实现）
  - [L2/spec_15 (SA-15)](./spec_15_platform_tenant_bootstrap_auth.md)（`CompanyService`、`SessionManager`）
- **下游消费者**：`SA-21`（文件配额/归属）、`SA-22`（项目成员）、`SA-23`（技能空间 `team:<path>`）、`SA-27`（Agent 共享成员/发言人展示名）、`SA-35`（IM 通讯录/手机号绑定）、`SA-39`（用户/账号/通讯录 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业完成租户初始化与登录认证（`SA-15`）后，管理员需要管理本公司内部成员、维护组织汇报线与部门树、控制租户用户上限与月度 Token 额度，而普通员工需要安全修改密码、上传光栅头像、通过企业通讯录检索同事以实现 Agent/项目协作。

本篇（`SA-16`）将 `UserService`、`AccountService`、`PeopleDirectory`、`OrgService` 与 `QuotaService` 收口在一个 1.5 人天的独立 PR 内，核心价值在于：
1. **企业级防误操作守卫**：实现“唯一管理员保护”——禁止删除、停用或降权公司内最后一个启用态 `ADMIN`；
2. **停用/删号实时断会话 + 离职交接预留**：员工被停用、删除或重置密码时立即通过 `SessionManager.InvalidateUser` 吊销 PG17 `user_session`；同时在停用/删除节点触发二期 `spi.OwnerTransferHook`，为后续员工离职 Agent/任务资产交接留好零侵入挂载点；
3. **PG17 月度 Token 聚合性能优化**：将原版 Java `QuotaService.java:88` 全表拉取改为 SQL 聚合 `COALESCE(SUM(...), 0)` 配合进程内 1 分钟 UTC 月份缓存（纯内存，零 Redis）。

### 1.2 本篇边界

- **本篇负责**：
  - `internal/platform/user`：`UserService`（CRUD、唯一管理员守卫、配额联动、会话吊销、`OwnerTransferHook`）、`AccountService`（修改密码与 1 MB 光栅头像原子落盘）、`PeopleDirectory`（启用成员通讯录检索与展示名解析）、`OrgService`（上下级汇报关系与部门路径树 CRUD）；
  - `internal/platform/tenant/quota.go`：`QuotaService`（用户上限 `CheckCanAddUser`、月度 Token 上限 `CheckCanUseTokens`、1m UTC 进程内缓存）。
- **本篇不负责**：租户开户与登录鉴权（已由 `SA-15` 交付）；系统配置/Logo/通知/分析（留给 `SA-17`）；Gin HTTP 路由挂载（留给 `SA-39`）；二期 `OwnerTransferHook` 具体资产迁移实现（V1 注入默认 `NoopOwnerTransferHook`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 租户配额服务 `QuotaService`（用户数上限 + 月度 Token 聚合与 1m UTC 缓存） | `internal/platform/tenant/quota.go` | 新增 |
| 2 | 用户治理服务 `UserService`（CRUD、唯一管理员保护、会话吊销、离职钩子） | `internal/platform/user/user.go` | 新增 |
| 3 | 个人账号服务 `AccountService`（改密校验、1 MB 光栅头像防 XSS 原子落盘与读取） | `internal/platform/user/account.go` | 新增 |
| 4 | 企业通讯录 `PeopleDirectory` 与组织部门树服务 `OrgService` | `internal/platform/user/directory.go`、`internal/platform/user/org.go` | 新增 |
| 5 | 配额、用户、账号头像、通讯录与组织树单测套件 | `internal/platform/tenant/quota_test.go`、`internal/platform/user/{user,account,directory,org}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表结构，全部复用 [L1/spec_07 (SA-07)](../L1/spec_07_pg_storage_platform_providers.md) 与 [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md) 建成的既有表：
- `company`（读取 `max_users`、`max_monthly_tokens` 租户配额覆写列）；
- `app_user`（`id` 不可变 UUID、`username` 租户内唯一、`password_hash`、`must_change_password`、`enabled`、`roles`）；
- `user_session`（用户停用/删号/改密时批量失效）；
- `user_manager` / `user_team`（汇报线与多部门归属）；
- `token_usage`（按 `(company_id, at)` 索引聚合月度总 Token）。

头像文件采用租户磁盘隔离落盘：`<avatarsDir>/<companyID>/<sha256_hex(userID)>`（通过 `tenant.Path(ctx, avatarsDir)` 杜绝路径穿越），写入先 `os.CreateTemp` → `Sync()` → `os.Rename` 保证原子性。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package tenant

type QuotaLimits struct {
	MaxUsers         int   // <=0 表示不限
	MaxMonthlyTokens int64 // <=0 表示不限
}

type QuotaService struct { /* companies, usage, defaults, cache *memcache.Cache[string, usedCacheEntry] */ }
func NewQuotaService(companies store.CompanyStore, usage store.UsageStore, defaults QuotaLimits) *QuotaService
func (q *QuotaService) Limits(ctx context.Context) (QuotaLimits, error)
func (q *QuotaService) TokensThisMonth(ctx context.Context) (int64, error)
func (q *QuotaService) CheckCanAddUser(ctx context.Context, currentCount int) error
func (q *QuotaService) CheckCanUseTokens(ctx context.Context) error
```

```go
package user

type SaveUserRequest struct {
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Roles       []string `json:"roles"`
	Enabled     *bool    `json:"enabled"`
	Password    string   `json:"password,omitempty"`
	Phone       string   `json:"phone,omitempty"`
	ExternalID  string   `json:"externalId,omitempty"`
	Source      string   `json:"source,omitempty"`
}

type UserService struct { /* users, quotas, license, sessions, transfer */ }
func NewUserService(users store.UserStore, quotas *tenant.QuotaService, license spi.LicenseQuotaHook, sessions SessionInvalidator, transfer spi.OwnerTransferHook) *UserService
func (s *UserService) List(ctx context.Context) ([]model.User, error)
func (s *UserService) Create(ctx context.Context, req SaveUserRequest) (*model.User, error)
func (s *UserService) Update(ctx context.Context, id string, req SaveUserRequest) (*model.User, error)
func (s *UserService) Delete(ctx context.Context, id string) error

type AvatarPayload struct {
	Bytes     []byte
	MediaType string
	ModTimeMs int64
}

type AccountService struct { /* users, sessions, avatarRoot string */ }
func (s *AccountService) MustChangePassword(ctx context.Context, userID string) (bool, error)
func (s *AccountService) ChangePassword(ctx context.Context, userID, currentSessionID, currentPwd, nextPwd string) error
func (s *AccountService) SetAvatar(ctx context.Context, userID, base64Data string) (int64, error)
func (s *AccountService) DeleteAvatar(ctx context.Context, userID string) error
func (s *AccountService) AvatarVersions(ctx context.Context) (map[string]int64, error)
func (s *AccountService) ReadAvatar(ctx context.Context, userID string) (*AvatarPayload, error)

type DirectoryUser struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName"`
	Teams       []string `json:"teams"`
	ManagerID   string   `json:"managerId,omitempty"`
}

type PeopleDirectory struct { /* users, org */ }
func (d *PeopleDirectory) ListEnabled(ctx context.Context) ([]DirectoryUser, error)
func (d *PeopleDirectory) DisplayName(ctx context.Context, userID string) string

type OrgService struct { /* org store.OrgStore, users store.UserStore */ }
func (s *OrgService) SetManager(ctx context.Context, userID, managerID string) error
func (s *OrgService) SetTeams(ctx context.Context, userID string, rawTeams []string) ([]string, error)
func (s *OrgService) Memberships(ctx context.Context) (map[string][]string, error)
```

### 4.2 路由契约（由 `L5/spec_39` 挂载，本篇冻结语义）

| 方法 | 路径 | 权限 | 请求 / 响应要点 |
|---|---|---|---|
| `GET` | `/api/v1/users` | `ADMIN` | 返回 `[]UserDto`（严禁返回 `passwordHash`/`mustChangePassword`） |
| `POST` | `/api/v1/users` | `ADMIN` | `SaveUserRequest` → `UserDto` |
| `PUT` | `/api/v1/users/:id` | `ADMIN` | `SaveUserRequest` → `UserDto` |
| `DELETE` | `/api/v1/users/:id` | `ADMIN` | 返回 `null` |
| `PUT` | `/api/v1/account/password` | 已登录（免强制改密拦截） | `{"current","next"}` → `null` |
| `PUT` / `DELETE` | `/api/v1/account/avatar` | 已登录 | 上传返回 `int64`（mtime ms）；删除返回 `null` |
| `GET` | `/api/v1/avatars` | 已登录 | 返回 `map[userId]int64` |
| `GET` | `/api/v1/avatars/:userId` | 已登录 | 原始光栅字节 + `X-Content-Type-Options: nosniff` + `Cache-Control: private, max-age=2592000` |
| `GET` | `/api/v1/directory/users` | 已登录 | 仅启用用户，按 `displayName` 大小写不敏感升序 |

### 4.3 错误语义

| 场景 | 错误类型 | HTTP | 冻结文案 |
|---|---|---|---|
| 账号数超租户上限 | `apperror.Forbidden` | `403` | `"公司的账号数已到上限（%d 个）"` |
| 月度 Token 超上限 | `apperror.Forbidden` | `403` | `"公司本月的模型用量已到上限（%d tokens），下月恢复或请管理员调整额度"` |
| 用户名冲突 | `apperror.Conflict` | `409` | `"Username already exists: %s"` |
| 唯一管理员防误操作 | `apperror.InvalidArg` | `400` | `"这是公司唯一的管理员，不能删除、停用或取消管理员角色"` |
| 改密校验失败 | `apperror.InvalidArg` | `400` | `"这个账号不用密码登录"` / `"当前密码不正确"` / `"新密码至少 8 位"` / `"新密码不能与当前密码相同"` |
| 头像格式/大小非法 | `apperror.InvalidArg` | `400` | `"头像图片不能超过 1 MB"` / `"头像只支持 PNG、JPEG、GIF、WebP 图片"` |
| 用户不存在 | `apperror.NotFound` | `404` | `"No such user: %s"` |
| 上级成环（自设或循环汇报） | `apperror.InvalidArg` | `400` | `"汇报关系不能形成循环"` |

### 4.4 SQL 契约

1. 月度 Token 聚合（`UsageStore.SumTokensBetween`）：
   ```sql
   SELECT COALESCE(SUM(input_tokens + cache_read + cache_write + output_tokens), 0)
   FROM token_usage
   WHERE company_id = $1 AND at >= $2 AND at < $3;
   ```
2. `UserStore.Save` 按 `(company_id, id)` 更新，0 行时 `INSERT`（触发唯一约束 `23505` 转 `apperror.Conflict`，严禁用 `ON CONFLICT (company_id, username)` 误覆盖他人账号）。

---

## 5. 行为语义

### 5.1 `QuotaService` 覆盖优先级与 1m UTC 缓存（对齐 `QuotaService.java:49-93`）

```go
package tenant

const tokensFresh = time.Minute

type usedCacheEntry struct {
	yearMonth string
	tokens    int64
	at        time.Time
}

func (q *QuotaService) Limits(ctx context.Context) (QuotaLimits, error) {
	cid, err := coretenant.Require(ctx)
	if err != nil { return QuotaLimits{}, err }
	c, err := q.companies.Find(ctx, cid)
	if err != nil || c == nil { return q.defaults, err }
	out := q.defaults
	if c.MaxUsers != nil { out.MaxUsers = *c.MaxUsers }
	if c.MaxMonthlyTokens != nil { out.MaxMonthlyTokens = *c.MaxMonthlyTokens }
	return out, nil
}

func (q *QuotaService) TokensThisMonth(ctx context.Context) (int64, error) {
	cid, err := coretenant.Require(ctx)
	if err != nil { return 0, err }
	now := time.Now().UTC()
	ym := fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
	if entry, ok := q.cache.Get(cid); ok && entry.yearMonth == ym && now.Sub(entry.at) <= tokensFresh {
		return entry.tokens, nil
	}
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	total, err := q.usage.SumTokensBetween(ctx, from, now)
	if err != nil { return 0, err }
	q.cache.Set(cid, usedCacheEntry{yearMonth: ym, tokens: total, at: now}, 2*tokensFresh)
	return total, nil
}
```

### 5.2 `UserService` 唯一管理员保护、会话吊销与离职交接钩子（对齐 `UserService.java:48-155`）

```go
package user

func requireAnAdminLeft(all []model.User, before model.User, after *model.User) error {
	wasAdmin := before.Enabled && before.IsAdmin()
	stillAdmin := after != nil && after.Enabled && after.IsAdmin()
	if !wasAdmin || stillAdmin {
		return nil
	}
	for _, u := range all {
		if u.ID != before.ID && u.Enabled && u.IsAdmin() {
			return nil
		}
	}
	return apperror.InvalidArg("这是公司唯一的管理员，不能删除、停用或取消管理员角色")
}

func (s *UserService) Update(ctx context.Context, id string, req SaveUserRequest) (*model.User, error) {
	all, err := s.users.ListUsers(ctx)
	if err != nil { return nil, err }
	existing := findByID(all, id)
	if existing == nil { return nil, apperror.NotFound("No such user: " + id) }
	// 1. 校验改名唯一性、roles 规范化（始终大写去重，空默认 ["USER"]）
	updated := applyUserPatch(*existing, req)
	if req.Password != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
		if err != nil { return nil, err }
		updated.PasswordHash = string(hash)
		updated.MustChangePassword = true // 管理员重设密码后强制员工首次登录改密
	}
	if err := requireAnAdminLeft(all, *existing, &updated); err != nil {
		return nil, err
	}
	saved, err := s.users.Save(ctx, updated)
	if err != nil { return nil, err }
	cid, _ := coretenant.Require(ctx)
	disabledNow := existing.Enabled && !updated.Enabled
	if disabledNow || req.Password != "" {
		_ = s.sessions.InvalidateUser(ctx, cid, id)
	}
	if disabledNow && s.transfer != nil {
		// 二期员工离职资产交接预留挂载点（V1 Noop）
		_, _ = s.transfer.TransferOwnership(ctx, id, "")
	}
	return saved, nil
}
```

### 5.3 `AccountService` 改密与 1 MB 光栅头像防 XSS（对齐 `AccountService.java:48-152`）

- **`ChangePassword`**：
  1. 查用户，校验 `PasswordHash != ""`（否则返 `"这个账号不用密码登录"`）；
  2. `bcrypt.CompareHashAndPassword` 核验 `currentPwd`（失败返 `"当前密码不正确"`）；
  3. `utf8.RuneCountInString(nextPwd) >= 8` 且 `nextPwd != currentPwd`；
  4. 保存新哈希且 `MustChangePassword = false`；
  5. 调用 `sessions.MarkPasswordOK(ctx, currentSessionID)` 并吊销该用户的其他旧会话（保留当前会话）。
- **`SetAvatar`**：
  1. 支持去除可选 `data:image/...;base64,` 前缀后 `base64.StdEncoding.DecodeString`；
  2. 强校验 `0 < len(raw) <= 1<<20`（1 MB）；
  3. 使用魔数嗅探仅放行 **PNG / JPEG / GIF / WebP**，严禁放行 SVG/HTML（防存储型 XSS）；
  4. 文件名取 `sha256.Sum256([]byte(userID))` 十六进制，落位 `<avatarRoot>/<companyID>/<hash>`，同目录临时文件 `Sync` + `os.Rename` 原子替换；返回文件 `ModTime().UnixMilli()`。

### 5.4 `OrgService` 部门路径与无环汇报校验

- `SetTeams`：逐条调用 `model.NormalizeTeamPath`（复用 `SA-05` 校验规则：1–8 层、每段 ≤ 64 字符、无 `.` 开头/`›`/`\`/控制字符），去重排序后落库 `OrgStore.SetTeams`。
- `SetManager`：若 `managerID == ""` 清除汇报人；若 `managerID == userID` 拒绝；沿现有 `OrgStore.Managers(ctx)` 向上追踪祖先链，若触达 `userID` 立即返回 `apperror.InvalidArg("汇报关系不能形成循环")`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/platform/tenant/quota_test.go`（红灯）→ 实现 `QuotaService`（覆盖优先级、`<=0` 不限、1m UTC 缓存与跨月刷新、超限 403）。
- [ ] **T2**：编写 `internal/platform/user/user_test.go`（红灯）→ 实现 `UserService`（CRUD、唯一管理员保护、`max_users` 与 `LicenseQuotaHook`、停用/重设密码吊销会话与 `OwnerTransferHook`）。
- [ ] **T3**：编写 `internal/platform/user/account_test.go`（红灯）→ 实现 `AccountService`（改密 4 条校验与会话保活+踢旧、1 MB 光栅魔数嗅探防 SVG XSS、原子落盘）。
- [ ] **T4**：编写 `internal/platform/user/{directory,org}_test.go`（红灯）→ 实现 `PeopleDirectory`（启用过滤 + 展示名不区分大小写排序）与 `OrgService`（部门路径规范化 + 汇报关系成环检测）。
- [ ] **T5**：运行 `go test -race -cover ./internal/platform/{tenant,user}/...` 验证覆盖率与无竞态。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-16` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `tenant.quota.default_max_users`、`tenant.quota.default_max_monthly_tokens`、`avatars.dir` 配置项说明。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `QuotaService` 优先级、1m 缓存与跨月失效 | `internal/platform/tenant/quota_test.go` | 公司列覆写全局默认；`0` 表示不限；1m 内重复查仅调用 `UsageStore` 1 次；跨 UTC 月份立即重查 |
| 2 | `UserService` 唯一管理员守卫与配额/交接钩子 | `internal/platform/user/user_test.go` | 删除/停用/降权唯一管理员均返 `400`；超 `max_users` 返 `403`；停用或重设密码调用 `InvalidateUser` 且停用触发 `OwnerTransferHook` |
| 3 | `AccountService` 改密边界 | `internal/platform/user/account_test.go` | 无本地哈希、旧密码错、`<8` 字符、与旧密码相同均返 `400`；成功后清 `MustChangePassword` 并标 `password_ok=true` |
| 4 | `AccountService` 头像光栅嗅探与原子替换 | `internal/platform/user/account_test.go` | SVG、伪造 PNG 头、超 1 MB 均返 `400`；合法 PNG/WebP 落盘于租户目录且可读取，无残留 `.tmp` 文件 |
| 5 | `OrgService` 汇报成环拦截与 `PeopleDirectory` 排序 | `internal/platform/user/org_test.go` | `A→B→C→A` 成环返 `400`；非法团队路径返 `400`；通讯录排除停用用户并按展示名不区分大小写升序 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/platform/{tenant,user}/...` 全绿
- [ ] §2 交付物表 5 项逐一存在，核心包行覆盖率 ≥ 80%
- [ ] `internal/arch_test.go` 通过（零跨层倒挂、零 Redis 引入）
- [ ] 头像上传拒绝 SVG 与 >1 MB 文件，且文件路径严格受 `tenant.Path` 约束

---

## 10. 可提交节点

- 建议 commit 1：`feat(platform-tenant): add QuotaService with PG monthly token sum and 1m UTC memory cache (spec 16, SA-16)`
- 建议 commit 2：`feat(platform-user): add UserService, AccountService, PeopleDirectory and OrgService (spec 16, SA-16)`
