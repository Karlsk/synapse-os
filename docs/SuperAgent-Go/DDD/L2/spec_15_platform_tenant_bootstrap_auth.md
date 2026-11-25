# spec 15 | L2 平台治理（一）— 多租户生命周期、商业开户引导与持久化会话认证

- **状态**：`冻结待实施`
- **篇号顺序**：15（L2 平台核心域首篇，奠定多租户运行框架、离线/超管开户与防爆破持久化会话认证）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-15`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§3、§5
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §1.1、§2.1、§3（M0/M3）
  - [_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md](../_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md)（L2 平台核心域归档母本）
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
- **前置依赖**：
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`internal/core/{tenant,crypto,eventbus}`、`internal/infra/memcache`；及 `L0/spec_01` `apperror`/`obs`/`lifecycle`、`L0/spec_02` `db.TxManager`）
  - [L1/spec_07 (SA-07)](../L1/spec_07_pg_storage_platform_providers.md)（`00001_platform_core.sql` 与 `CompanyStore`、`SessionStore`、`UserStore`、`OrgStore`、`SystemSettingStore`）
- **下游消费者**：
  - `SA-16`（`internal/platform/user`：消费 `SessionInvalidator`/`SessionPasswordMarker`、`spi.LicenseQuotaHook`、`spi.OwnerTransferHook`）
  - `SA-17`（`internal/platform/settings`/`notice`/`analytics`：消费 `CompanyStarted`、实现 `ProfileSettingsReader`）
  - `SA-38`、`SA-39`（L5 Web 网关：挂接登录/登出/身份、超管开户/启停/配额与 6 级安全中间件）
  - `SA-41`（L6 Server 装配：`bootstrap-org` CLI 子命令、`wire.go`、`lifecycle.BackgroundGroup` 注册 `SessionPruner` 与 `FairTicker`）

---

## 1. 背景与范围

本篇交付 `synapse-server`（`module github.com/Karlsk/synapse-os`，`go 1.26.1`）**L2 平台核心域的首篇基础服务**，覆盖 `internal/platform/tenant` 与 `internal/platform/auth` 两大子包以及配套的跨模块 SPI 契约 `internal/core/spi/platform.go`，实现商业私有化部署所需的租户治理与企业级身份认证：

1. **多租户生命周期与公平轮转 (`internal/platform/tenant/company.go`, `fair_ticker.go`)**：
   - 1:1 继承 Java `CompanyService.java:L26-L87` 的租户启停、查询、`StartAll` 与 `ForEachEnabled` 轮转公平调度（每次扫描轮换起始租户 + 单租户 `panic recover` 隔离，不因个别租户故障中断全局后台循环）；
   - 提供 `crypto.CompanyKeyStore` 适配器对接 L0 `internal/core/crypto`，严格落实「**永不删除 `company` 行**」铁律以保护租户信封加密 DEK（`data_key_cipher`）；
   - 将 `FairTicker` 接入 L0 `internal/infra/lifecycle.Loop`，使全部跨租户后台轮询（任务调度、自动化、收件箱简报）统一受 `lifecycle.BackgroundGroup` 托管并支持 30s 优雅停机。
2. **商业私有化开户引导 (`internal/platform/tenant/bootstrap.go`, `license_hook.go`)**：
   - **V1 硬性商业裁剪——严禁公网自助注册企业**：彻底关闭 Java 原版 `OrganizationService.java` 的公网自注册入口，`GET /api/v1/orgs/registration` 恒返回 `{"open": false}`，公网 `POST /api/v1/orgs` 不挂载（或统一拒止返回 `403 forbidden`）；
   - 改为 `BootstrapProvisioner`，仅支持运维离线 `synapse-server bootstrap-org` CLI 与携带 `X-Super-Admin-Token` 的超管管理 API 开户；保留 14 位 `crypto/rand` 无歧义强随机初始化密码、12 保留词校验、`must_change_password=true` 首次强制改密；
   - **首租户 / 系统默认租户保障**：明确区分非企业哨兵 `tenant.DefaultSentinelID`（`"superagent"`，仅用于登录前默认品牌与 `/mcp` 自检，严禁作为可登录租户）与真实业务租户，提供幂等 `EnsureBootstrapOrg`（支持私有化首次部署按配置/CLI 幂等初始化首个默认组织，零租户启动时输出清晰运维指引）；
   - **预留二期 `LicenseQuotaHook` SPI**：开户前在互斥与事务边界内校验商业授权租户上限（V1 默认 `NoopLicenseQuotaHook`，二期零侵入接入 License 文件校验）。
3. **可插拔认证、PG17 持久化会话与纯内存防爆破 (`internal/platform/auth`)**：
   - 抽象 `spi.Authenticator` SPI（V1 交付 `LocalAuthenticator` BCrypt 认证与常量时间防用户名/组织枚举），**预留二期 OIDC / LDAP 接入契约**（`Kind="oidc"|"ldap"`、`ExternalIdentityClaim`、`ExternalAuthConfig`），保证二期无缝接入企业 IdP 而不改会话与鉴权中台；
   - `SessionService` 将登录会话持久化至 PG17 `user_session` 表（复用 `L1/spec_07` `store.SessionStore`），彻底替代原版 JVM 内存 `HttpSession`，服务重启/滚动升级不踢人，并支持滑动续期、`password_ok` 门禁缓存、按用户批量吊销与挂入 `lifecycle.BackgroundGroup` 的过期会话定期清理循环 `SessionPruner`；
   - **登录失败防爆破滑动窗口**：100% 复用 L0 `internal/infra/memcache.SlidingWindowLimiter`（纯进程内分片双窗口，**严禁 Redis**），同时对「账号（`org+username`）」与「客户端 IP」双维度限流，超限立即返回 `429 rate_limited` + `Retry-After`，阻止 BCrypt CPU 耗尽攻击。

### 1.1 Java 原版参考源码精确映射

| Go 目标文件 | 对应 Java 原版源码（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） | 行号范围 | V1 重构 / 加固要点 |
|---|---|---|---|
| `internal/core/spi/platform.go` | `superagent-web/src/main/java/com/superagent/web/auth/AppUserDetailsService.java`<br>`superagent-web/src/main/java/com/superagent/web/auth/TenantUser.java` | `AppUserDetailsService.java:L21-L63`<br>`TenantUser.java:L12-L42` | 抽象 `Authenticator`、`LicenseQuotaHook`、`OwnerTransferHook`、`ModelChecker` 跨模块 SPI，消除同层循环依赖 |
| `internal/platform/tenant/company.go`<br>`internal/platform/tenant/fair_ticker.go` | `superagent-platform/src/main/java/com/superagent/platform/tenant/CompanyService.java`<br>`superagent-core/src/main/java/com/superagent/core/tenant/Company.java`<br>`superagent-core/src/main/java/com/superagent/core/tenant/CompanyStarted.java` | `CompanyService.java:L26-L87`<br>`Company.java:L10-L35`<br>`CompanyStarted.java:L1-L15` | 保留 `ForEachEnabled` 公平轮转与异常/panic 隔离；新增 `SetEnabled`/`UpdateQuotas`/`CompanyKeyStoreAdapter`；`FairTicker` 实现 `lifecycle.Loop` |
| `internal/platform/tenant/bootstrap.go`<br>`internal/platform/tenant/license_hook.go` | `superagent-platform/src/main/java/com/superagent/platform/tenant/OrganizationService.java`<br>`superagent-web/src/main/java/com/superagent/web/org/OrgController.java` | `OrganizationService.java:L27-L134`<br>`OrgController.java:L13-L32` | **严禁公网自助注册**；改为 `BootstrapProvisioner`（CLI + 超管 Token API），保留 14 位强随机密码、12 保留词与 `must_change_password=true`，新增 `EnsureBootstrapOrg` 与 `LicenseQuotaHook` |
| `internal/platform/auth/authenticator.go`<br>`internal/platform/auth/local.go` | `superagent-web/src/main/java/com/superagent/web/auth/AppUserDetailsService.java`<br>`superagent-web/src/main/java/com/superagent/web/auth/OrgLoginFilter.java` | `AppUserDetailsService.java:L35-L62`<br>`OrgLoginFilter.java:L16-L38` | 常量时间 `dummyBcryptHash` 防组织/账号枚举；预留二期 `oidc`/`ldap` 认证器契约 |
| `internal/platform/auth/session.go`<br>`internal/platform/auth/bruteforce.go`<br>`internal/platform/auth/service.go` | `superagent-web/src/main/java/com/superagent/web/auth/SecurityConfig.java`<br>`superagent-web/src/main/java/com/superagent/web/auth/TenantFilter.java`<br>`superagent-web/src/main/java/com/superagent/web/auth/PasswordChangeFilter.java`<br>`superagent-web/src/main/java/com/superagent/web/auth/AuthController.java`<br>`superagent-api/src/main/java/com/superagent/api/auth/MeDto.java` | `SecurityConfig.java:L41-L131`<br>`TenantFilter.java:L19-L38`<br>`PasswordChangeFilter.java:L21-L58`<br>`AuthController.java:L26-L56`<br>`MeDto.java:L14-L17` | 新增 PG17 `user_session` 持久化（零 Redis）+ `SessionPruner` (`lifecycle.Loop`) + `memcache.SlidingWindowLimiter` 双维度防爆破；`AuthService` 通过窄读接口拼装 `MeDto` 避免与 `SA-16`/`SA-17` 循环依赖 |

### 1.2 本篇边界

- **本篇负责**：`internal/core/spi/platform.go`、`internal/platform/tenant`（不含 `QuotaService`，该配额服务归属 `SA-16`）、`internal/platform/auth` 全部领域逻辑、SPI 契约与单元测试。
- **留给后续篇**：
  - `SA-16`：`UserService`、`AccountService`、`PeopleDirectory`、`OrgService`、`TeamPaths`、`QuotaService` 与 `OwnerTransferHook` 集成；
  - `SA-17`：`SettingsService`、`BrandingService`、`NoticeService`、`AnalyticsService`；
  - `SA-38` / `SA-39`：Gin HTTP Handler 与 6 级中间件装配；
  - `SA-41`：`cmd/synapse-server bootstrap-org` 命令行解析、`wire.go` 与 `BackgroundGroup` 启动接线。
- **严格红线**：**严禁引入 Redis**；所有短期状态与防爆破窗口均基于 L0 `internal/infra/memcache`，所有会话持久化均落 PG17 `user_session`。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 跨模块平台 SPI（`AuthPrincipal`、`Credentials`、`Authenticator`、V2 OIDC/LDAP 契约占位、`LicenseQuotaHook`、`OwnerTransferHook`、`ModelChecker`） | `internal/core/spi/platform.go` | 新增 |
| 2 | `CompanyService`（租户启停、查询、创建、`ForEachEnabled` 轮转公平与 panic 隔离、`CompanyKeyStoreAdapter`） | `internal/platform/tenant/company.go` | 新增 |
| 3 | `FairTicker`（实现 `lifecycle.Loop` 的多租户周期公平轮转辅助器） | `internal/platform/tenant/fair_ticker.go` | 新增 |
| 4 | `BootstrapProvisioner`（离线 CLI / 超管开户、14 位强随机密码、`EnsureBootstrapOrg`、公网注册恒关闭）与 `NoopLicenseQuotaHook` | `internal/platform/tenant/bootstrap.go`<br>`internal/platform/tenant/license_hook.go` | 新增 |
| 5 | `Authenticator` 路由注册表、二期 OIDC/LDAP 接入契约与 `LocalAuthenticator`（BCrypt + 常量时间防枚举） | `internal/platform/auth/authenticator.go`<br>`internal/platform/auth/local.go` | 新增 |
| 6 | `SessionService`（PG17 `user_session` 创建/滑动续期/`MarkPasswordOK`/批量吊销/改密踢旧会话）与 `SessionPruner` (`lifecycle.Loop`) | `internal/platform/auth/session.go` | 新增 |
| 7 | `LoginBruteForceGuard`（基于 L0 `memcache.SlidingWindowLimiter` 的账号 + IP 双维度纯内存防爆破守卫） | `internal/platform/auth/bruteforce.go` | 新增 |
| 8 | `AuthService`（`Login`/`Logout`/`ResolvePrincipal`/`CheckPasswordGate`/`Me`、`VerifySuperAdminToken`）与认证 Prometheus 指标 | `internal/platform/auth/service.go` | 新增 |
| 9 | 完整同包单元测试套件（覆盖轮转公平、开户边界、防枚举、PG 会话生命周期、双维度防爆破与 `-race` 并发验证） | `internal/platform/tenant/*_test.go`<br>`internal/platform/auth/*_test.go` | 新增 |

---

## 3. 数据库设计

本篇直接读写以下 **3 张核心表**（DDL 已由 `L1/spec_07 (SA-07)` 的 `migrations/00001_platform_core.sql` 冻结创建，本篇不新增迁移脚本，严格通过 `internal/core/store` 接口访问，零直引 `pgx`）：

### 3.1 `company`（全局表，对应 `store.CompanyStore`）

- 主键：`id VARCHAR(64) PRIMARY KEY`（即组织英文短名 slug）。
- 关键列：`name VARCHAR(255)`、`enabled BOOLEAN DEFAULT TRUE`、`data_key_cipher TEXT NOT NULL DEFAULT ''`（供 L0 `crypto.SecretCipher` 封存公司 DEK）、`max_users INTEGER`、`max_running_tasks INTEGER`、`monthly_tokens BIGINT`、`created_at`/`updated_at TIMESTAMPTZ`。
- **写保护铁律**：永不 `DELETE` 公司行；停用租户仅更新 `enabled = FALSE`，保留 `data_key_cipher` 与审计历史。`tenant.DefaultSentinelID`（`"superagent"`）绝不写入 `company` 作为可登录企业。

### 3.2 `user_session`（全局表，对应 `store.SessionStore`）

- 主键：`id VARCHAR(128) PRIMARY KEY`（32 字节 `crypto/rand` 生成的 64 位小写 hex）。
- 关键列：`company_id VARCHAR(64) NOT NULL`、`user_id VARCHAR(64) NOT NULL`（不可变 `app_user.id`）、`password_ok BOOLEAN NOT NULL DEFAULT FALSE`、`expires_at TIMESTAMPTZ NOT NULL`、`created_at`/`updated_at TIMESTAMPTZ NOT NULL`。
- 索引：`idx_user_session_expires (expires_at)`（供 `SessionPruner` 定时清理）、`idx_user_session_user (company_id, user_id)`（供停用/删号/重置密码同步批量吊销）。
- **零 Redis 约定**：PG17 `user_session` 是登录会话的唯一事实源；`SessionStore.Find` 自带 `AND expires_at > NOW()` 过滤。

### 3.3 `app_user`（租户隔离表，对应 `store.UserStore`）

- 主键：`PRIMARY KEY (company_id, id)`，唯一约束：`uq_app_user_username UNIQUE (company_id, username)`，部分索引：`idx_app_user_external (company_id, external_id) WHERE external_id <> ''`（供二期 OIDC/LDAP 身份映射）。
- 本篇写入点：仅 `BootstrapProvisioner.ProvisionOrg` 在 `TxManager.WithinTx` 内创建租户首个 `ADMIN` 用户（`source='LOCAL'`、`must_change_password=TRUE`）；`LocalAuthenticator` 与 `AuthService` 仅执行只读校验。

---

## 4. 冻结契约

### 4.1 导出符号

#### A. 跨模块平台 SPI（`internal/core/spi/platform.go`）

```go
package spi

import "context"

// AuthPrincipal 认证通过后的不可变身份主体。
// 铁律：UserID 为不可变 UUID，全系统所有权主键；Username 仅为租户内登录名，严禁混用。
type AuthPrincipal struct {
	UserID             string
	CompanyID          string
	Username           string
	DisplayName        string
	Roles              []string // 裸角色名，始终含 "USER"，管理员含 "ADMIN"
	MustChangePassword bool
}

// Credentials 多认证源统一凭据（V1 使用 Kind="local"，V2 扩展 "oidc"/"ldap"）。
type Credentials struct {
	Kind      string // "" 或 "local" | "oidc" | "ldap"
	Org       string // 组织英文短名（company_id）
	Username  string // local/ldap 登录名
	Password  string // local/ldap 明文密码（日志脱敏）
	Token     string // V2 OIDC ID Token / Access Token 扩展位
	StateCode string // V2 OIDC Authorization Code 回调扩展位
}

// Authenticator 可插拔认证器 SPI（对标 Java AppUserDetailsService，V2 零侵入接 OIDC/LDAP）。
type Authenticator interface {
	Kind() string
	Authenticate(ctx context.Context, cred Credentials) (*AuthPrincipal, error)
}

// ExternalSyncMode 二期企业目录/IdP 登录同步策略占位枚举。
type ExternalSyncMode string

const (
	ExternalSyncPreprovisionOnly ExternalSyncMode = "preprovision_only" // 仅允许已预置 external_id 账号登录
	ExternalSyncJITProvision     ExternalSyncMode = "jit_provision"     // V2 首次通过 OIDC/LDAP 验证时自动建档
)

// ExternalIdentityClaim 二期 OIDC/LDAP 认证器统一映射契约（V1 冻结结构，零实现逻辑）。
type ExternalIdentityClaim struct {
	Provider    string           // "oidc" | "ldap"
	CompanyID   string           // 目标租户
	ExternalID  string           // OIDC sub / LDAP entryUUID / sAMAccountName
	Username    string           // 期望登录名
	DisplayName string           // 展示姓名
	Email       string           // 邮箱（可选）
	Groups      []string         // 外部角色/部门组（供 V2 Org/Role 映射）
	SyncMode    ExternalSyncMode // 同步模式
}

// LicenseQuotaHook 商业私有化 License 席位/租户上限预留钩子（V1 默认 NoopLicenseQuotaHook）。
type LicenseQuotaHook interface {
	CheckCanProvisionOrg(ctx context.Context, currentOrgs int) error
	CheckCanAddUser(ctx context.Context, companyID string, companyUsers int, totalUsers int) error
}

// OwnerTransferHook 员工停用/删除时的资产交接预留钩子（由 SA-16 UserService 调用，V2 接离职交接）。
type OwnerTransferHook interface {
	OnUserDisabled(ctx context.Context, userID string) error
	OnUserDeleted(ctx context.Context, userID string) error
}

// ModelChecker SettingsService 校验模型是否通过推理探活的窄接口（由 SA-18 provider.ProviderService 满足）。
type ModelChecker interface {
	HasRunnableModel(ctx context.Context, modelID string) bool
}
```

#### B. 多租户与商业开户服务（`internal/platform/tenant`）

```go
package tenant

import (
	"context"
	"log/slog"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/crypto"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/eventbus"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	"github.com/Karlsk/synapse-os/internal/core/store"
)

type QuotaOverrides struct {
	MaxUsers        *int   `json:"maxUsers,omitempty"`
	MaxRunningTasks *int   `json:"maxRunningTasks,omitempty"`
	MonthlyTokens   *int64 `json:"monthlyTokens,omitempty"`
}

type CompanyService struct { /* companies store.CompanyStore, bus *eventbus.Bus, logger *slog.Logger, turn atomic.Uint64 */ }

func NewCompanyService(companies store.CompanyStore, bus *eventbus.Bus, logger *slog.Logger) *CompanyService
func (s *CompanyService) StartAll(ctx context.Context) error
func (s *CompanyService) EnabledCompany(ctx context.Context, org string) (string, bool, error)
func (s *CompanyService) Exists(ctx context.Context, id string) (bool, error)
func (s *CompanyService) Find(ctx context.Context, id string) (*domain.Company, error)
func (s *CompanyService) ListAll(ctx context.Context) ([]domain.Company, error)
func (s *CompanyService) Create(ctx context.Context, id, name string, quotas *QuotaOverrides) (*domain.Company, error)
func (s *CompanyService) CreateWithoutEvent(ctx context.Context, id, name string, quotas *QuotaOverrides, now time.Time) (*domain.Company, error)
func (s *CompanyService) PublishStarted(ctx context.Context, companyID string)
func (s *CompanyService) SetEnabled(ctx context.Context, id string, enabled bool) (*domain.Company, error)
func (s *CompanyService) UpdateQuotas(ctx context.Context, id string, q QuotaOverrides) (*domain.Company, error)
func (s *CompanyService) EnabledIDs(ctx context.Context) ([]string, error)
func (s *CompanyService) ForEachEnabled(ctx context.Context, work func(ctx context.Context) error)

// NewCompanyKeyStoreAdapter 将 store.CompanyStore 适配为 L0 crypto.CompanyKeyStore 窄接口，
// 供 L6 wire.go 注入 crypto.NewSecretCipher，保护 company.data_key_cipher。
func NewCompanyKeyStoreAdapter(companies store.CompanyStore) crypto.CompanyKeyStore

// FairTicker 实现 L0 lifecycle.Loop，将按租户公平轮转任务挂入 lifecycle.BackgroundGroup。
type FairTicker struct { /* name string, interval time.Duration, companies *CompanyService, work func(context.Context) error */ }

func NewFairTicker(name string, interval time.Duration, companies *CompanyService, work func(ctx context.Context) error) *FairTicker
func (t *FairTicker) Name() string
func (t *FairTicker) Run(ctx context.Context) error

// ProvisionOrgRequest 替代原公网自注册：仅由 bootstrap-org CLI 或超管 Token API 调用。
type ProvisionOrgRequest struct {
	ID            string          `json:"id"`                      // 组织英文短名（slug）
	Name          string          `json:"name"`                    // 组织中文/全名（1..64 runes）
	AdminUsername string          `json:"adminUsername,omitempty"` // 可选，留空默认等于 ID
	Quotas        *QuotaOverrides `json:"quotas,omitempty"`        // 可选单租户配额覆盖
	IfNotExists   bool            `json:"ifNotExists,omitempty"`   // true 时若已存在则幂等返回 AlreadyExisted=true（CLI 首次部署脚本友好）
}

type ProvisionedOrg struct {
	Org            string `json:"org"`
	Name           string `json:"name"`
	Username       string `json:"username"`
	Password       string `json:"password,omitempty"` // 14 位一次性明文密码，永不落库、永不打日志；AlreadyExisted=true 时为空
	AlreadyExisted bool   `json:"alreadyExisted,omitempty"`
}

type BootstrapProvisioner struct { /* companies *CompanyService, users store.UserStore, tx store.TxManager, license spi.LicenseQuotaHook, logger *slog.Logger, mu sync.Mutex */ }

func NewBootstrapProvisioner(companies *CompanyService, users store.UserStore, tx store.TxManager, license spi.LicenseQuotaHook, logger *slog.Logger) *BootstrapProvisioner
func (p *BootstrapProvisioner) RegistrationOpen() bool // V1 商业红线：恒返回 false
func (p *BootstrapProvisioner) ProvisionOrg(ctx context.Context, req ProvisionOrgRequest) (*ProvisionedOrg, error)
func (p *BootstrapProvisioner) EnsureFirstOrg(ctx context.Context, defaultReq *ProvisionOrgRequest) (*ProvisionedOrg, error)

// NoopLicenseQuotaHook V1 默认商业授权挂钩实现（放行全部开户与增员）。
type NoopLicenseQuotaHook struct{}

func (NoopLicenseQuotaHook) CheckCanProvisionOrg(context.Context, int) error             { return nil }
func (NoopLicenseQuotaHook) CheckCanAddUser(context.Context, string, int, int) error     { return nil }
```

#### C. 认证、防爆破与 PG17 持久化会话（`internal/platform/auth`）

```go
package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/api"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	"github.com/Karlsk/synapse-os/internal/core/store"
	"github.com/Karlsk/synapse-os/internal/infra/memcache"
	"github.com/Karlsk/synapse-os/internal/platform/tenant"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	KindLocal = "local"
	KindOIDC  = "oidc" // V2 预留
	KindLDAP  = "ldap" // V2 预留
)

type SessionConfig struct {
	CookieName     string        // 默认 "SYNAPSE_SID"
	TTL            time.Duration // 默认 7 * 24 * time.Hour
	SlidingRefresh time.Duration // 默认 12 * time.Hour（仅在剩余 TTL < TTL/2 且距上次更新 >= SlidingRefresh 时续期）
	PruneInterval  time.Duration // 默认 10 * time.Minute（SessionPruner 后台清理周期）
	CookieSecure   bool          // 生产 HTTPS 开启；HttpOnly 恒 true，SameSite 恒 Lax
}

type BruteForceConfig struct {
	AccountMaxFailures int           // 默认 5 次
	IPMaxFailures      int           // 默认 20 次
	Window             time.Duration // 默认 15 * time.Minute
	MaxKeys            int           // 默认 100_000（防随机 IP 洪泛 OOM）
}

// 窄读接口：解耦 auth(SA-15) 与 user(SA-16) / settings(SA-17)，杜绝 L2 循环依赖。
type ProfileSettingsReader interface {
	CompanyDisplayNames(ctx context.Context) (companyName, companyShortName string, err error)
}

type AccountStateReader interface {
	AvatarVersion(ctx context.Context, userID string) (*int64, error)
	CanChangePassword(ctx context.Context, userID string) (bool, error)
	MustChangePassword(ctx context.Context, userID string) (bool, error)
}

type SessionService struct { /* store store.SessionStore, cfg SessionConfig, logger *slog.Logger, nowFn func() time.Time */ }

func NewSessionService(s store.SessionStore, cfg SessionConfig, logger *slog.Logger) *SessionService
func (m *SessionService) Config() SessionConfig
func (m *SessionService) Create(ctx context.Context, companyID, userID string, passwordOK bool) (*domain.UserSession, error)
func (m *SessionService) Get(ctx context.Context, sessionID string) (*domain.UserSession, error)
func (m *SessionService) MaybeRefresh(ctx context.Context, sess *domain.UserSession) (*domain.UserSession, error)
func (m *SessionService) MarkPasswordOK(ctx context.Context, sessionID string) error
func (m *SessionService) Invalidate(ctx context.Context, sessionID string) error
func (m *SessionService) InvalidateUser(ctx context.Context, companyID, userID string) error
func (m *SessionService) PruneExpired(ctx context.Context) (int64, error)

// SessionPruner 实现 L0 lifecycle.Loop，挂载到 lifecycle.BackgroundGroup 定期清理过期会话与限流窗格。
type SessionPruner struct { /* sessions *SessionService, guard *LoginBruteForceGuard, logger *slog.Logger */ }

func NewSessionPruner(sessions *SessionService, guard *LoginBruteForceGuard, logger *slog.Logger) *SessionPruner
func (p *SessionPruner) Name() string // "auth.session_pruner"
func (p *SessionPruner) Run(ctx context.Context) error

// LoginBruteForceGuard 纯内存双维度登录防爆破守卫（复用 L0 memcache.SlidingWindowLimiter，零 Redis）。
type LoginBruteForceGuard struct { /* acctLimiter, ipLimiter *memcache.SlidingWindowLimiter */ }

func NewLoginBruteForceGuard(cfg BruteForceConfig) *LoginBruteForceGuard
func (g *LoginBruteForceGuard) Check(org, username, clientIP string) memcache.Decision
func (g *LoginBruteForceGuard) RecordFailure(org, username, clientIP string) memcache.Decision
func (g *LoginBruteForceGuard) RecordSuccess(org, username string)
func (g *LoginBruteForceGuard) PurgeExpired() int

type LocalAuthenticator struct { /* companies *tenant.CompanyService, users store.UserStore */ }

func NewLocalAuthenticator(companies *tenant.CompanyService, users store.UserStore) *LocalAuthenticator
func (a *LocalAuthenticator) Kind() string // "local"
func (a *LocalAuthenticator) Authenticate(ctx context.Context, cred spi.Credentials) (*spi.AuthPrincipal, error)

// V2 预留：OIDC 与 LDAP 配置契约（V1 仅冻结配置结构，不启用外部网络绑定）。
type OIDCConfig struct {
	Enabled      bool                 `yaml:"enabled"`
	IssuerURL    string               `yaml:"issuer_url"`
	ClientID     string               `yaml:"client_id"`
	ClientSecret string               `yaml:"client_secret"`
	RedirectURL  string               `yaml:"redirect_url"`
	Scopes       []string             `yaml:"scopes"`
	SyncMode     spi.ExternalSyncMode `yaml:"sync_mode"`
}

type LDAPConfig struct {
	Enabled      bool                 `yaml:"enabled"`
	URL          string               `yaml:"url"` // ldaps://...
	BindDN       string               `yaml:"bind_dn"`
	BindPassword string               `yaml:"bind_password"`
	BaseDN       string               `yaml:"base_dn"`
	UserFilter   string               `yaml:"user_filter"`
	SyncMode     spi.ExternalSyncMode `yaml:"sync_mode"`
}

type LoginResult struct {
	Session   *domain.UserSession
	Principal *spi.AuthPrincipal
}

type AuthService struct {
	/* authenticators map[string]spi.Authenticator, sessions *SessionService, guard *LoginBruteForceGuard,
	   users store.UserStore, org store.OrgStore, settings ProfileSettingsReader, account AccountStateReader,
	   superAdminToken string, logger *slog.Logger, loginTotal *prometheus.CounterVec */
}

type AuthOptions struct {
	Authenticators  []spi.Authenticator
	Sessions        *SessionService
	BruteForce      *LoginBruteForceGuard
	Users           store.UserStore
	Org             store.OrgStore
	Settings        ProfileSettingsReader // 可选：nil 时 Me() 返回空公司名
	Account         AccountStateReader    // 可选：nil 时自动降级直查 UserStore
	SuperAdminToken string
	Logger          *slog.Logger
	Registerer      prometheus.Registerer // 复用 L0 obs.Metrics.Registerer()
}

func NewAuthService(opts AuthOptions) (*AuthService, error)
func (s *AuthService) RegisterAuthenticator(a spi.Authenticator) // 允许 V2 启动期注入 oidc/ldap 认证器
func (s *AuthService) Login(ctx context.Context, cred spi.Credentials, clientIP string) (*LoginResult, error)
func (s *AuthService) Logout(ctx context.Context, sessionID string) error
func (s *AuthService) ResolvePrincipal(ctx context.Context, sessionID string) (*spi.AuthPrincipal, *domain.UserSession, error)
func (s *AuthService) CheckPasswordGate(ctx context.Context, sess *domain.UserSession) error
func (s *AuthService) Me(ctx context.Context, p *spi.AuthPrincipal) (*api.MeDto, error)
func (s *AuthService) VerifySuperAdminToken(token string) error // 常量时间比对；未配 Token 返 404
```

### 4.2 路由契约（由 `L5/spec_38` 挂载，本篇冻结语义与 DTO）

| 方法 | 路径 | 权限 / 中间件豁免 | 请求体 / 参数 | 响应格式与特殊头 |
|---|---|---|---|---|
| `GET` | `/api/v1/orgs/registration` | 公开 | — | `ApiResponse<{"open": false}>`（V1 商业红线：恒返 `false`） |
| `POST` | `/api/v1/orgs` | **V1 禁开公网自注册** | — | 路由不挂载或统一拒止 `403 forbidden` `"这台服务器没有开放注册组织，请联系管理员开通"` |
| `POST` | `/api/v1/platform/orgs` | **超管 Token**（`X-Super-Admin-Token`；未配返 404） | `ProvisionOrgRequest` | `ApiResponse<ProvisionedOrg>`（一次性返回 14 位密码） |
| `PUT` | `/api/v1/platform/orgs/:id/quotas` | **超管 Token** | `QuotaOverrides` | `ApiResponse<CompanyDto>` |
| `POST` | `/api/v1/platform/orgs/:id/enabled` | **超管 Token** | `{"enabled": bool}` | `ApiResponse<CompanyDto>` |
| `POST` | `/api/v1/auth/login` | 公开（走 `LoginBruteForceGuard`） | Form `org`、`username`、`password`（兼容 JSON） | **裸 JSON（无 `ApiResponse` 壳，1:1 对齐原版前端）** 200 `{"userId","username","roles"}` + `Set-Cookie: SYNAPSE_SID=...; Path=/; HttpOnly; SameSite=Lax[; Secure]`；401 `{"code":"unauthorized","message":"组织、用户名或密码错误，或账号未授权"}`；429 `{"code":"rate_limited","message":"登录失败次数过多，请 %d 秒后再试"}` + `Retry-After` 头 |
| `POST` | `/api/v1/auth/logout` | 已登录（免强制改密拦截） | — | HTTP 200 空体，清除 Cookie 并删 `user_session` |
| `GET` | `/api/v1/auth/me` | 已登录（免强制改密拦截） | — | `ApiResponse<MeDto>`（12 字段：`id`, `username`, `displayName`, `roles`, `admin`, `teams`, `companyName`, `companyShortName`, `avatarVersion`, `canChangePassword`, `org`, `mustChangePassword`） |

### 4.3 错误语义

全部错误通过 L0 `internal/core/apperror` 构造并自动映射 HTTP 状态码：

| 场景 | Go 领域错误 | HTTP 状态码 | JSON `code` | 冻结文案（对齐 Java 原版） |
|---|---|---:|---|---|
| 试图公网自助注册企业 | `apperror.Forbidden(...)` | `403` | `forbidden` | `"这台服务器没有开放注册组织，请联系管理员开通"` |
| 组织名称为空或超 64 字 | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"请填写组织名称（最多 64 个字）"` |
| 组织英文短名为空 / 格式非法 / 12 保留词 / 命中哨兵 `superagent` | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"请填写组织的英文名"` / `"英文名只能用小写字母、数字和连字符，以字母开头，3 到 32 位，例如 acme 或 acme-china"` / `"英文名「%s」是保留词，请换一个"` |
| 组织英文短名已存在 | `apperror.Conflict(...)` | `409` | `conflict` | `"英文名「%s」已被其他组织使用，请换一个"` |
| 超管 Token 未配置 / 不匹配 | `apperror.NotFound(...)` / `apperror.Unauthorized(...)` | `404` / `401` | `not_found` / `unauthorized` | 未配置时返 404 隐藏管理接口存在性；配置但不匹配返 401 |
| 触发账号或 IP 防爆破限流 | `apperror.RateLimited(...)` | `429` | `rate_limited` | `"登录失败次数过多，请 %d 秒后再试"`（附带 `Retry-After` 秒数） |
| 组织不存在/停用、账号不存在/停用、非 LOCAL 空哈希、密码错误 | `apperror.Unauthorized(...)` | `401` | `unauthorized` | `"组织、用户名或密码错误，或账号未授权"` |
| 未知认证类型 `cred.Kind` | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"不支持的登录方式：%s"` |
| 首次初始密码未修改访问受保护 API | `apperror.PasswordChangeRequired(...)` | `403` | `password_change_required` | `"请先修改初始密码"` |
| 查询/修改不存在的租户 | `apperror.NotFound(...)` | `404` | `not_found` | `"No such company: %s"` |

### 4.4 SQL 与持久化契约

1. **租户与初始管理员原子事务创建**：`BootstrapProvisioner.ProvisionOrg` 必须通过 `store.TxManager.WithinTx` 在单个 PG17 事务内先写 `company`、再注入 `tenant.WithCompany(txCtx, org)` 写 `app_user`；任一步失败整笔回滚，杜绝孤儿租户（无管理员）或孤儿用户。
2. **事务后发布 `CompanyStarted`**：仅在 `WithinTx` 返回 `nil` 提交成功后，通过 `eventbus.Publish` 发布 `domain.CompanyStarted{CompanyID: org}`，保证 `NoticeService`、连接器种子等监听器读到已提交数据。
3. **会话滑动续期节流**：`SessionService.MaybeRefresh` 仅当 `sess.ExpiresAt.Sub(now) < cfg.TTL/2` 且 `now.Sub(sess.UpdatedAt) >= cfg.SlidingRefresh` 时才执行 `SessionStore.Touch`，将普通读请求的会话表写放大降低 99%+。
4. **过期会话清理 SQL**：`SessionPruner` 调用 `SessionStore.DeleteExpired(ctx, now)`（对应 `SA-07` `DELETE FROM user_session WHERE expires_at < $1`），命中 `idx_user_session_expires` 索引，单次清理无全表扫描。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 多租户轮转公平、DEK 适配与 `FairTicker`

- **Java 源码对照**：`CompanyService.java:L41-L86`。
- **关键行为**：
  1. `EnabledCompany(ctx, org)` 清洗 `strings.ToLower(strings.TrimSpace(org))`，**显式拒绝 `tenant.DefaultSentinelID`（`"superagent"`）**，仅当 `c != nil && c.Enabled` 时返回 `(c.ID, true, nil)`；
  2. `ForEachEnabled` 每次执行动态拉取启用租户列表，使用 `turn.Add(1) - 1` 轮转起始位置，每个租户独立 `func()` 内 `defer recover()`，单租户错误或 panic 仅记录结构化 `slog.WarnContext`/`ErrorContext` 而不中断其他租户；
  3. `FairTicker` 实现 `lifecycle.Loop`，按固定周期调度 `companies.ForEachEnabled(ctx, work)`，响应 `ctx.Done()` 立即退出。

```go
// internal/platform/tenant/company.go — 对照 CompanyService.java:L41-L86
package tenant

import (
	"context"
	"log/slog"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/eventbus"
	coretenant "github.com/Karlsk/synapse-os/internal/core/tenant"
)

func (s *CompanyService) EnabledCompany(ctx context.Context, org string) (string, bool, error) {
	id := strings.ToLower(strings.TrimSpace(org))
	if id == "" || id == coretenant.DefaultSentinelID {
		return "", false, nil
	}
	c, err := s.companies.Find(ctx, id)
	if err != nil {
		return "", false, err
	}
	if c == nil || !c.Enabled {
		return "", false, nil
	}
	return c.ID, true, nil
}

func (s *CompanyService) StartAll(ctx context.Context) error {
	ids, err := s.EnabledIDs(ctx)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		s.logger.WarnContext(ctx, "no enabled organizations found; run `synapse-server bootstrap-org --id <slug> --name <name>` to provision the first tenant")
		return nil
	}
	for _, id := range ids {
		s.PublishStarted(ctx, id)
	}
	return nil
}

func (s *CompanyService) PublishStarted(ctx context.Context, companyID string) {
	tctx, err := coretenant.WithCompany(ctx, companyID)
	if err != nil {
		s.logger.WarnContext(ctx, "skip CompanyStarted for invalid company id", "company_id", companyID, "err", err)
		return
	}
	eventbus.Publish(tctx, s.bus, domain.CompanyStarted{CompanyID: companyID})
}

func (s *CompanyService) ForEachEnabled(ctx context.Context, work func(ctx context.Context) error) {
	ids, err := s.EnabledIDs(ctx)
	if err != nil {
		s.logger.WarnContext(ctx, "list enabled companies failed", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	start := int((s.turn.Add(1) - 1) % uint64(len(ids)))
	for i := range len(ids) {
		if ctx.Err() != nil {
			return
		}
		id := ids[(start+i)%len(ids)]
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.logger.ErrorContext(ctx, "background work panicked for company",
						"company_id", id, "panic", r, "stack", string(debug.Stack()))
				}
			}()
			tctx, err := coretenant.WithCompany(ctx, id)
			if err != nil {
				s.logger.WarnContext(ctx, "invalid company id skipped", "company_id", id, "err", err)
				return
			}
			if err := work(tctx); err != nil && ctx.Err() == nil {
				s.logger.WarnContext(tctx, "background work failed for company", "company_id", id, "err", err)
			}
		}()
	}
}

func (s *CompanyService) SetEnabled(ctx context.Context, id string, enabled bool) (*domain.Company, error) {
	c, err := s.companies.Find(ctx, strings.ToLower(strings.TrimSpace(id)))
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, apperror.NotFound("No such company: " + id)
	}
	wasEnabled := c.Enabled
	c.Enabled = enabled
	c.UpdatedAt = time.Now().UTC().Truncate(time.Millisecond)
	saved, err := s.companies.Save(ctx, *c)
	if err != nil {
		return nil, err
	}
	if !wasEnabled && enabled {
		s.PublishStarted(ctx, saved.ID)
	}
	return saved, nil
}
```

### 5.2 商业开户 `BootstrapProvisioner`、14 位强随机密码与首租户保障

- **Java 源码对照**：`OrganizationService.java:L32-L133`、`OrgController.java:L22-L31`。
- **关键行为**：
  1. `RegistrationOpen()` 恒为 `false`，彻底封死公网自注册；
  2. 复用 L0 `coretenant.ValidateOrgSlug(req.ID)`（校验 `^[a-z][a-z0-9-]{1,30}[a-z0-9]$` 及 12 保留词），并额外拒绝 `coretenant.DefaultSentinelID`（`"superagent"`）；
  3. 14 位密码使用 `crypto/rand` + `math/big.Int` 在 55 字符无歧义字母表（去除 `I/O/l/0/1`）上均匀采样，零模偏差；**严禁任何日志打印明文密码**；
  4. 互斥锁内先检 `license.CheckCanProvisionOrg(ctx, len(allCompanies))`，再判重（`req.IfNotExists == true` 时幂等返回 `AlreadyExisted: true` 且不暴露密码；否则返 `409 Conflict`）；
  5. `EnsureFirstOrg`：供私有化首次启动调用——若库中已有启用租户则直接返回 `(nil, nil)`；若零租户且配置提供了首租户参数，则以 `IfNotExists: true` 自动创建并一次性返回凭据，保障首租户开箱即用且不创建弱默认密码账号。

```go
// internal/platform/tenant/bootstrap.go — 对照 OrganizationService.java:L68-L119
package tenant

import (
	"context"
	"crypto/rand"
	"math/big"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	coretenant "github.com/Karlsk/synapse-os/internal/core/tenant"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
)

const (
	initialPasswordLen = 14
	passwordAlphabet   = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
)

func (p *BootstrapProvisioner) RegistrationOpen() bool { return false }

func (p *BootstrapProvisioner) ProvisionOrg(ctx context.Context, req ProvisionOrgRequest) (*ProvisionedOrg, error) {
	cleanName := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(cleanName); n == 0 || n > 64 {
		return nil, apperror.InvalidArg("请填写组织名称（最多 64 个字）")
	}
	org, err := coretenant.ValidateOrgSlug(req.ID)
	if err != nil {
		return nil, err
	}
	if org == coretenant.DefaultSentinelID {
		return nil, apperror.InvalidArg("英文名「" + org + "」是保留词，请换一个")
	}
	adminUser := strings.TrimSpace(req.AdminUsername)
	if adminUser == "" {
		adminUser = org
	}
	password, err := generateRandomPassword(initialPasswordLen)
	if err != nil {
		return nil, apperror.Internal("generate initial password", err)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, apperror.Internal("hash initial password", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	existing, err := p.companies.Find(ctx, org)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if req.IfNotExists {
			return &ProvisionedOrg{Org: existing.ID, Name: existing.Name, Username: adminUser, AlreadyExisted: true}, nil
		}
		return nil, apperror.Conflict("英文名「" + org + "」已被其他组织使用，请换一个")
	}

	allCompanies, err := p.companies.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.license.CheckCanProvisionOrg(ctx, len(allCompanies)); err != nil {
		return nil, err
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	err = p.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if _, err := p.companies.CreateWithoutEvent(txCtx, org, cleanName, req.Quotas, now); err != nil {
			return err
		}
		tctx, err := coretenant.WithCompany(txCtx, org)
		if err != nil {
			return err
		}
		_, err = p.users.Save(tctx, domain.User{
			ID:                 uuid.NewString(),
			Username:           adminUser,
			DisplayName:        cleanName + " 管理员",
			Roles:              []string{"ADMIN"},
			Enabled:            true,
			PasswordHash:       string(hash),
			Source:             domain.UserSourceLocal,
			MustChangePassword: true,
			CreatedAt:          now,
			UpdatedAt:          now,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	p.companies.PublishStarted(ctx, org)
	p.logger.InfoContext(ctx, "organization provisioned", "org", org, "admin_username", adminUser)
	return &ProvisionedOrg{Org: org, Name: cleanName, Username: adminUser, Password: password}, nil
}

func generateRandomPassword(n int) (string, error) {
	max := big.NewInt(int64(len(passwordAlphabet)))
	buf := make([]byte, n)
	for i := range n {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		buf[i] = passwordAlphabet[idx.Int64()]
	}
	return string(buf), nil
}
```

### 5.3 防枚举 `LocalAuthenticator`、双维度防爆破与 PG17 `SessionService`

- **Java 源码对照**：`AppUserDetailsService.java:L35-L62`、`PasswordChangeFilter.java:L32-L57`、`SecurityConfig.java:L84-L118`、`AuthController.java:L40-L55`。
- **关键行为**：
  1. **防爆破优先于 BCrypt**：`AuthService.Login` 先执行 `guard.Check(org, username, clientIP)`（内部调用 `memcache.SlidingWindowLimiter.Peek` 不增计数），若账号或 IP 任一维度已被锁定，立即返回 `429 rate_limited`，直接跳过 BCrypt 计算保护 CPU；
  2. **常量时间防枚举**：`LocalAuthenticator.Authenticate` 遇到组织不存在、组织停用、用户不存在、用户停用或非 `LOCAL` 空哈希时，均执行一次 `bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(cred.Password))` 保持相近耗时，统一返回 `"组织、用户名或密码错误，或账号未授权"`；
  3. **会话与实时停用校验**：`ResolvePrincipal` 查 `SessionService.Get(ctx, sessionID)` -> 在租户上下文查 `UserStore.FindByID(tctx, sess.UserID)` -> 若用户已删除或 `Enabled == false`，立即调用 `sessions.Invalidate(ctx, sessionID)` 并返 401 -> 调用 `MaybeRefresh` 滑动续期 -> 返回不可变 `AuthPrincipal`；
  4. **强制改密门禁**：`CheckPasswordGate` 若 `sess.PasswordOK == true` 零 DB 查询直接放行；否则检查账号 `must_change_password`，为 `true` 返 `403 password_change_required`，为 `false` 回写 `sessions.MarkPasswordOK(ctx, sess.ID)`；
  5. **后台过期会话与限流清理**：`SessionPruner.Run(ctx)` 挂入 `lifecycle.BackgroundGroup`，按 `PruneInterval`（默认 10 分钟）清理过期 `user_session` 行及过期内存限流窗格。

```go
// internal/platform/auth/bruteforce.go — 纯进程内双维度滑动窗口防爆破（零 Redis）
package auth

import (
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/infra/memcache"
)

func NewLoginBruteForceGuard(cfg BruteForceConfig) *LoginBruteForceGuard {
	if cfg.AccountMaxFailures <= 0 {
		cfg.AccountMaxFailures = 5
	}
	if cfg.IPMaxFailures <= 0 {
		cfg.IPMaxFailures = 20
	}
	if cfg.Window <= 0 {
		cfg.Window = 15 * time.Minute
	}
	if cfg.MaxKeys <= 0 {
		cfg.MaxKeys = 100_000
	}
	return &LoginBruteForceGuard{
		acctLimiter: memcache.NewSlidingWindowLimiter(memcache.LimiterOptions{
			Limit: cfg.AccountMaxFailures, Window: cfg.Window, Shards: 32, MaxKeys: cfg.MaxKeys,
		}),
		ipLimiter: memcache.NewSlidingWindowLimiter(memcache.LimiterOptions{
			Limit: cfg.IPMaxFailures, Window: cfg.Window, Shards: 32, MaxKeys: cfg.MaxKeys,
		}),
	}
}

func accountKey(org, username string) string {
	return "acct:" + strings.ToLower(strings.TrimSpace(org)) + ":" + strings.ToLower(strings.TrimSpace(username))
}

func ipKey(clientIP string) string {
	return "ip:" + strings.TrimSpace(clientIP)
}

func (g *LoginBruteForceGuard) Check(org, username, clientIP string) memcache.Decision {
	if d := g.acctLimiter.Peek(accountKey(org, username)); !d.Allowed {
		return d
	}
	if ip := strings.TrimSpace(clientIP); ip != "" {
		if d := g.ipLimiter.Peek(ipKey(ip)); !d.Allowed {
			return d
		}
	}
	return memcache.Decision{Allowed: true}
}

func (g *LoginBruteForceGuard) RecordFailure(org, username, clientIP string) memcache.Decision {
	da := g.acctLimiter.RecordFailure(accountKey(org, username))
	if ip := strings.TrimSpace(clientIP); ip != "" {
		di := g.ipLimiter.RecordFailure(ipKey(ip))
		if !di.Allowed && (da.Allowed || di.RetryAfter > da.RetryAfter) {
			return di
		}
	}
	return da
}

func (g *LoginBruteForceGuard) RecordSuccess(org, username string) {
	g.acctLimiter.Reset(accountKey(org, username))
}
```

```go
// internal/platform/auth/service.go — Login / ResolvePrincipal / CheckPasswordGate / VerifySuperAdminToken
package auth

import (
	"context"
	"crypto/subtle"
	"fmt"
	"math"
	"strings"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	coretenant "github.com/Karlsk/synapse-os/internal/core/tenant"
	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

func (s *AuthService) Login(ctx context.Context, cred spi.Credentials, clientIP string) (*LoginResult, error) {
	kind := strings.ToLower(strings.TrimSpace(cred.Kind))
	if kind == "" {
		kind = KindLocal
	}
	if s.guard != nil {
		if dec := s.guard.Check(cred.Org, cred.Username, clientIP); !dec.Allowed {
			s.incLoginMetric(kind, "rate_limited")
			secs := max(1, int(math.Ceil(dec.RetryAfter.Seconds())))
			return nil, apperror.RateLimited(fmt.Sprintf("登录失败次数过多，请 %d 秒后再试", secs))
		}
	}
	authImpl, ok := s.authenticators[kind]
	if !ok {
		return nil, apperror.InvalidArg("不支持的登录方式：" + kind)
	}
	principal, err := authImpl.Authenticate(ctx, cred)
	if err != nil {
		if s.guard != nil {
			s.guard.RecordFailure(cred.Org, cred.Username, clientIP)
		}
		s.incLoginMetric(kind, "failed")
		s.logger.WarnContext(ctx, "login failed", "kind", kind, "org", cred.Org, "username", cred.Username, "client_ip", clientIP)
		return nil, err
	}
	if s.guard != nil {
		s.guard.RecordSuccess(cred.Org, cred.Username)
	}
	sess, err := s.sessions.Create(ctx, principal.CompanyID, principal.UserID, !principal.MustChangePassword)
	if err != nil {
		return nil, err
	}
	s.incLoginMetric(kind, "ok")
	s.logger.InfoContext(obs.WithUserID(ctx, principal.UserID), "login succeeded",
		"kind", kind, "company_id", principal.CompanyID, "username", principal.Username)
	return &LoginResult{Session: sess, Principal: principal}, nil
}

func (s *AuthService) ResolvePrincipal(ctx context.Context, sessionID string) (*spi.AuthPrincipal, *domain.UserSession, error) {
	sess, err := s.sessions.Get(ctx, sessionID)
	if err != nil || sess == nil {
		return nil, nil, apperror.Unauthorized("请先登录")
	}
	tctx, err := coretenant.WithCompany(ctx, sess.CompanyID)
	if err != nil {
		return nil, nil, apperror.Unauthorized("请先登录")
	}
	u, err := s.users.FindByID(tctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}
	if u == nil || !u.Enabled {
		_ = s.sessions.Invalidate(ctx, sess.ID)
		return nil, nil, apperror.Unauthorized("请先登录")
	}
	sess, err = s.sessions.MaybeRefresh(ctx, sess)
	if err != nil {
		s.logger.WarnContext(tctx, "session sliding refresh failed", "err", err)
	}
	return &spi.AuthPrincipal{
		UserID:             u.ID,
		CompanyID:          sess.CompanyID,
		Username:           u.Username,
		DisplayName:        firstNonBlank(u.DisplayName, u.Username),
		Roles:              normalizePrincipalRoles(u.Roles),
		MustChangePassword: u.MustChangePassword,
	}, sess, nil
}

func (s *AuthService) VerifySuperAdminToken(token string) error {
	expected := strings.TrimSpace(s.superAdminToken)
	if expected == "" {
		return apperror.NotFound("not found")
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(token)), []byte(expected)) != 1 {
		return apperror.Unauthorized("invalid super admin token")
	}
	return nil
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 定义 `internal/core/spi/platform.go`（`AuthPrincipal`、`Credentials`、`Authenticator`、V2 OIDC/LDAP 契约类型、`LicenseQuotaHook`、`OwnerTransferHook`、`ModelChecker`），核对零循环依赖与 `internal/arch_test.go` 合规。
- [ ] **T2**: 编写 `internal/platform/tenant/company_test.go` 与 `bootstrap_test.go`（先红灯：公平轮转、panic 隔离、DEK 适配器、公网注册恒关闭、14 位密码复杂度、12 保留词 + `superagent` 哨兵拦截、事务回滚、`EnsureFirstOrg`、`LicenseQuotaHook`）。
- [ ] **T3**: 实现 `internal/platform/tenant/{company.go,fair_ticker.go,bootstrap.go,license_hook.go}`，跑通租户全量测试。
- [ ] **T4**: 编写 `internal/platform/auth/{local_test.go,session_test.go,bruteforce_test.go,service_test.go}`（先红灯：常量时间防枚举、PG 会话创建/滑动续期/`MarkPasswordOK`/批量吊销/`SessionPruner`、账号+IP 双维度滑动窗口防爆破与 `RetryAfter`、停用用户实时踢出、强制改密门禁、`MeDto` 12 字段、`VerifySuperAdminToken`）。
- [ ] **T5**: 实现 `internal/platform/auth/{authenticator.go,local.go,session.go,bruteforce.go,service.go}` 并接入 `obs` 脱敏日志与 Prometheus 登录指标。
- [ ] **T6**: 全量验证：`cd synapse-server && go build ./... && go test -race -cover ./internal/platform/tenant/... ./internal/platform/auth/... ./internal`。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：将 `SA-15` 状态更新为 `已实施（commit <hash>）`；
- [ ] `configs/config.example.yaml`：补充 `platform.super_admin_token`、`auth.session_cookie_name`、`auth.session_ttl`、`auth.cookie_secure`、`auth.bruteforce.{account_max_failures,ip_max_failures,window}` 配置项注释及二期 `auth.oidc`/`auth.ldap` 注释占位；
- [ ] `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md`：核对 `bootstrap-org` CLI 参数与超管 API 开关。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `CompanyService.ForEachEnabled` 公平轮转与 panic 隔离 | `internal/platform/tenant/company_test.go` | 连续 3 轮遍历 3 个启用租户时起始租户依次为 `A→B→C`；租户 A 触发 panic 或返回 error 不影响 B/C 执行；停用租户与 `superagent` 哨兵均不进入遍历 |
| 2 | `FairTicker` 周期调度与取消退出 | `internal/platform/tenant/company_test.go` | 挂入 `lifecycle.BackgroundGroup` 后周期回调执行，`Stop(ctx)` 时零泄漏立即退出 |
| 3 | `BootstrapProvisioner` 校验、14 位强随机密码与事务原子性 | `internal/platform/tenant/bootstrap_test.go` | `RegistrationOpen() == false`；非法 slug / 12 保留词 / `"superagent"` 返 `400`；重名返 `409`；`IfNotExists=true` 幂等返回 `AlreadyExisted=true` 且不泄漏密码；生成密码长度 14 且全落 55 无歧义字符集；管理员 `must_change_password=true`；写用户失败时事务回滚且不发布 `CompanyStarted` |
| 4 | `LicenseQuotaHook` 拦截与 `EnsureFirstOrg` | `internal/platform/tenant/bootstrap_test.go` | Mock `LicenseQuotaHook` 返回超限时拒绝开户并零写入；空库调 `EnsureFirstOrg` 建首租户，非空库幂等跳过 |
| 5 | `LocalAuthenticator` 角色归一化与常量时间防枚举 | `internal/platform/auth/local_test.go` | 正确密码返回 UUID `UserID` 与包含 `USER` 的角色集；空密码/停用用户/停用组织/不存在组织/非 `LOCAL` 空哈希均统一返 `401` 相同中文文案 |
| 6 | `SessionService` 全生命周期、滑动续期节流与 `SessionPruner` | `internal/platform/auth/session_test.go` | `Create` 生成 64 位小写 hex ID；剩余 TTL > 50% 时 `MaybeRefresh` 零写库，低于 50% 且超节流间隔时调用 `Touch` 续期；`InvalidateUser` 精确删除单租户用户全部会话；`SessionPruner` 按周期调用 `DeleteExpired` 且响应 `ctx` 取消 |
| 7 | `LoginBruteForceGuard` 账号 + IP 双维度纯内存防爆破 | `internal/platform/auth/bruteforce_test.go` | 同一 `(org, user)` 连续 5 次失败后第 6 次在 BCrypt 之前即被拦截返 `429 rate_limited`；登录成功 `Reset` 账号窗口；同 IP 撞库 20 个不同账号后触发 IP 维度锁定；可注入时钟推进 15m 后自动解封；`-race` 并发安全 |
| 8 | `AuthService` `ResolvePrincipal`/`CheckPasswordGate`/`Me`/`VerifySuperAdminToken` | `internal/platform/auth/service_test.go` | 用户被停用后原有效会话在 `ResolvePrincipal` 立即被删除并返 `401`；`password_ok=false` 且 `must_change_password=true` 返 `403 password_change_required`，`password_ok=true` 零查库放行；`Me` 输出完整 12 字段；未配超管 Token 返 `404`，错 Token 返 `401` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./...` 零错误零警告
- [ ] `go test -race -cover ./internal/platform/tenant/... ./internal/platform/auth/...` 100% 全绿，包覆盖率 ≥ 85%
- [ ] `go test -race ./internal`（`arch_test.go`）全绿：确认 `platform/auth` 零引用 `platform/user`/`platform/settings`（零循环依赖），全仓零 Redis 依赖、零直引 `pgx`
- [ ] 核对日志脱敏：登录失败/开户成功日志绝不输出明文 `password`、`sessionID`、`superAdminToken`
- [ ] §2 交付物 9 项与 §8 测试清单 8 类场景逐项核对无遗漏

---

## 10. 可提交节点

- 建议 commit 1：`feat(platform-tenant): add platform SPI, CompanyService, FairTicker and BootstrapProvisioner with LicenseQuotaHook (spec 15, SA-15)`
- 建议 commit 2：`feat(platform-auth): add Authenticator SPI, LocalAuthenticator, PG17 SessionService, SessionPruner and in-memory brute-force guard (spec 15, SA-15)`
