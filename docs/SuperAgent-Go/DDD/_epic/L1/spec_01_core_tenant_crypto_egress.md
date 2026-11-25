# spec 01 | L0/L1 Core — 核心契约、多租户上下文、信封加密与抗重绑定出口守卫

- **状态**：`冻结待实施`
- **篇号顺序**：01（全项目首篇，零业务前置依赖）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-01`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§5
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §1
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §1, §3
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
- **前置依赖**：无（本篇为模块首篇，仅依赖 `synapse-server/go.mod`：`module github.com/Karlsk/synapse-os`, `go 1.26.1`）
- **下游消费者**：全部后续篇 `SA-02` ~ `SA-14`（所有上层包仅允许依赖本篇冻结的 `internal/core/*` 契约）

---

## 1. 背景与范围

本篇构建 `synapse-server` 的 **L0 核心契约层 (`internal/core/*`)** 与 **工程包依赖守卫 (`internal/arch_test.go`)**，对应 Java 原版 `superagent-core` 与 `superagent-api` 的基础契约部分。解决的核心问题是：在 14 篇并行/分期重构之前，一次性冻结全系统的领域实体、枚举、38 个抽象 `Store` 接口、12 个跨模块与二期预留 SPI、Fail-Closed 多租户 `context.Context` 底座、AES-256-GCM 租户信封加密、抗 DNS Rebinding 的 `EgressGuard`、统一错误模型、进程内领域事件总线及可观测性原语，从而消除各业务域之间的循环依赖（尤其彻底隔离 `internal/agent` 与 `internal/task`）并锁定 V1 商业私有化的裁剪与加固基线。

**本篇边界与 V1 锁定裁剪决策**：
- **保留与强化**：保留 `company_id` 强隔离与 `TenantPaths` 防目录穿越；保留每租户独立 DEK 的 AES-256-GCM 信封加密（兼容原版 `v1:` / `v2:<companyID>:` 密文格式）；将原版 `EgressGuard` 升级为 **`DialContext` 级直连已校验 IP 字面量 + 6 跳重定向逐跳重检**，彻底消除 DNS Rebinding 窗口；正式定义 6 个 Prometheus 核心业务指标与 `slog` 上下文透传。
- **V1 裁剪（本篇即彻底不引入）**：
  - 删除 `AgentCharacters`（24 个内置性格预设）与 `Agent.Character` 字段；
  - 删除全部废弃历史字段：`Agent.Tier`、`Task.Tier`、`Automation.Tier`、`SuperAgentState.TrackedJSON`、`EventSource` 的 5 个空占位列（`secret_cipher`/`vip_names`/`urgent_keywords`/`status`/`last_error`）与 `secretary_agent_id`；
  - 删除死代码 `MaterialPrompt`（原版 `superagent-agent/src/main/java/com/superagent/agent/chat/MaterialPrompt.java`）；
  - 砍掉公网自助注册路由常量与相关异常（`POST /api/v1/orgs` 不再开放，`GET /api/v1/orgs/registration` 固定返回 `{open:false}`，租户创建交由 `SA-04`/`SA-14` 的 `bootstrap-org` CLI 实现）。
- **一期预留、二期交付的 5 大 SPI**：`ProcessLauncher`、`ModelGateway`、`Authenticator`、`IMConnector`、`OwnerTransferHook` 在本篇 `internal/core/spi` 冻结接口签名，由 `SA-03`/`SA-04`/`SA-11` 提供 V1 实现，二期实现零侵入替换。
- **留给后续篇**：PostgreSQL 17 建表迁移与 38 个 Store 实现见 `SA-02`；进程启动器与 Claude/Codex 适配见 `SA-03`；完整业务 REST DTO 与 Gin 路由实现见 `SA-04`~`SA-13`；应用装配与 CLI 启动见 `SA-14`。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 领域实体、枚举与纯领域工具（`TeamPaths`/`LinkUrls`/`TurnTimer`/`AutomationSchedule`/`WorkFileKind`） | `internal/core/domain/*.go`（按子域拆 11 文件） | 新增 |
| 2 | 38 个持久化 `Store` 接口与 `TxManager` | `internal/core/store/store.go`（及按域分文件 `store_*.go`） | 新增 |
| 3 | 12 个跨模块与二期预留 SPI（含 5 大二期预留接口） | `internal/core/spi/*.go` | 新增 |
| 4 | Fail-Closed 多租户 `context.Context` 原语、`Detach`、组织 slug 校验与 `TenantPaths` | `internal/core/tenant/context.go`、`paths.go`、`slug.go` | 新增 |
| 5 | AES-256-GCM 信封加密 `SecretCipher`（KEK 文件 `0600` + 每公司 DEK 缓存与 AAD 绑定） | `internal/core/crypto/cipher.go` | 新增 |
| 6 | 抗 DNS Rebinding 出口守卫 `EgressGuard`（`DialContext` + `CheckRedirect` + `ToURI` + `IsPublic`） | `internal/core/netguard/egress.go` | 新增 |
| 7 | 统一领域错误体系 `apperror`（10 类标准错误码与 HTTP 状态码映射） | `internal/core/apperror/errors.go` | 新增 |
| 8 | 进程内领域事件总线 `eventbus`（同步/异步发布、租户上下文透传、panic 隔离） | `internal/core/eventbus/bus.go`、`events.go` | 新增 |
| 9 | HTTP 基础信封与路径常量（`ApiResponse`/`ApiError`/`MetaResponse`/`PageQuery`/`PageResult`/`ApiPaths`） | `internal/core/api/envelope.go`、`paths.go` | 新增 |
| 10 | 可观测性底座 `obs`（`slog` 上下文 Handler、`TraceID` 校验、6 项核心 Prometheus 指标） | `internal/core/obs/logger.go`、`trace.go`、`metrics.go` | 新增 |
| 11 | AST 静态分层与包依赖守卫测试（禁止反向依赖、`agent<->task` 互引、L2+ 直引 `pgx`） | `internal/arch_test.go` | 新增 |
| 12 | 核心单元测试套件（覆盖 tenant/crypto/netguard/apperror/eventbus/domain/obs） | `internal/core/*/*_test.go` | 新增 |

---

## 3. 数据库设计

本篇属于 L0 契约与基础原语层，**不直接持有任何 SQL 迁移脚本或数据库驱动依赖**（`internal/core/*` 严禁 import `jackc/pgx` 或 `database/sql`）。

本篇在 `internal/core/domain` 与 `internal/core/store` 中为 `SA-02` 冻结以下数据模型约束（完整 47 张 PostgreSQL 17 DDL 与 6 个 `goose` 迁移文件详见 [spec_02_storage_postgres_migrations.md](./spec_02_storage_postgres_migrations.md) §3）：
1. **租户列契约**：除 `Company`（全局 `company` 表）与 `UserSession`（全局 `user_session` 表，按随机 `id` 主键查询但携带 `company_id` 字段）外，其余 45 张业务表对应的全部 `Store` 方法均**不接收 `companyID` 参数**，必须从 `ctx` 经 `tenant.Require(ctx)` 强制提取。
2. **新增实体字段**：
   - `domain.UserSession`（V1 新增持久化登录会话，对应 `user_session` 表）；
   - `domain.AgentEvent.ID int64`（V1 新增 `BIGSERIAL` 物理主键，对应 `agent_event.id`）。
3. **移除废弃字段**（绝不出现在 Go 领域结构体中）：`Agent.Character`、`Agent.Tier`、`Task.Tier`、`Automation.Tier`、`SuperAgentState.TrackedJSON`、`EventSource.{SecretCipher,VipNames,UrgentKeywords,Status,LastError,SecretaryAgentID}`。
4. **金额与时间类型契约**：
   - 所有时间戳统一使用 `time.Time`（写入存储前强制 `.UTC().Truncate(time.Millisecond)`，对齐 PG `TIMESTAMPTZ`）；
   - 计费金额 `Task.Spend`（`decimal.Decimal`）、`UsageRecord.CostUSD` / `CLICostUSD`（`*decimal.Decimal`）统一使用 `github.com/shopspring/decimal`，对齐 PG `NUMERIC(16,6)`，避免二进制浮点累加误差。

---

## 4. 冻结契约

### 4.1 导出符号

#### A. `internal/core/tenant`
```go
package tenant

const DefaultSentinelID = "superagent" // 仅用于登录前 Logo 与 /mcp 自检，非真实企业

var (
    ErrMissingTenant  = errors.New("missing tenant context")
    ErrInvalidCompany = errors.New("invalid company id")
)

func WithCompany(ctx context.Context, companyID string) (context.Context, error)
func Require(ctx context.Context) (string, error)
func Optional(ctx context.Context) (string, bool)
func Detach(ctx context.Context) context.Context // 剥离 HTTP 取消信号，保留 companyID / traceID / userID
func ValidateCompanyID(id string) error          // ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ 且不含 ".."
func ValidateOrgSlug(raw string) (string, error) // ^[a-z][a-z0-9-]{1,30}[a-z0-9]$ 且不在 12 个保留字中
func Path(ctx context.Context, root string) (string, error)
func PathFor(root, companyID string) (string, error)
```

#### B. `internal/core/crypto`
```go
package crypto

type CompanyKeyStore interface {
    FindCompany(ctx context.Context, id string) (*domain.Company, error)
    SaveCompany(ctx context.Context, c domain.Company) (*domain.Company, error)
}

type SecretCipher struct { /* masterAEAD, store, mu, dekCache */ }

func NewSecretCipher(configuredKeyB64, keyFile string, companies CompanyKeyStore) (*SecretCipher, error)
func (c *SecretCipher) Encrypt(ctx context.Context, plain string) (string, error)
func (c *SecretCipher) Decrypt(ctx context.Context, stored string) (string, error)
```

#### C. `internal/core/netguard`
```go
package netguard

type Resolver interface {
    LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type Options struct {
    Allowed    []string       // 精确主机 wiki.corp.example / 后缀 .corp.example / IPv4或IPv6 CIDR
    Resolver   Resolver       // nil 时默认 net.DefaultResolver
    ProxyURL   string         // 企业出口代理（可选）
    CABundle   *x509.CertPool // 企业自签根证书（可选）
    MaxHops    int            // 默认 6
    DialTimeout time.Duration // 默认 10s
}

type EgressGuard struct { /* ... */ }

var PublicOnly = MustNew(Options{})

func New(opts Options) (*EgressGuard, error)
func MustNew(opts Options) *EgressGuard
func ToURI(raw string) (*url.URL, error)
func IsPublic(addr netip.Addr) bool
func (g *EgressGuard) CheckURL(ctx context.Context, rawURL string) (*url.URL, error)
func (g *EgressGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (g *EgressGuard) NewHTTPClient(timeout time.Duration) *http.Client
```

#### D. `internal/core/apperror`
```go
package apperror

type Code string

const (
    CodeOK                     Code = "ok"
    CodeInvalidArgument        Code = "invalid_argument"
    CodeUnauthorized           Code = "unauthorized"
    CodePasswordChangeRequired Code = "password_change_required"
    CodeForbidden              Code = "forbidden"
    CodeNotFound               Code = "not_found"
    CodeMethodNotAllowed       Code = "method_not_allowed"
    CodeConflict               Code = "conflict"
    CodeBusy                   Code = "busy"
    CodeTooLarge               Code = "too_large"
    CodeInternal               Code = "internal_error"
)

type AppError struct {
    Code       Code
    HTTPStatus int
    Message    string
    Err        error
}

func (e *AppError) Error() string
func (e *AppError) Unwrap() error

func InvalidArg(msg string) *AppError
func Unauthorized(msg string) *AppError
func PasswordChangeRequired(msg string) *AppError
func Forbidden(msg string) *AppError
func NotFound(msg string) *AppError
func MethodNotAllowed(msg string) *AppError
func Conflict(msg string) *AppError
func Busy(msg string) *AppError
func TooLarge(msg string) *AppError
func Internal(msg string, cause error) *AppError
func EgressDenied(msg string) *AppError // 包装 Forbidden(msg) + ErrEgressDenied sentinel

type UnreadableLinkError struct {
    URL    string
    Reason string
    Hint   string
}
func (e *UnreadableLinkError) Error() string

type UnreadableMaterialsError struct {
    Items []*UnreadableLinkError
}
func (e *UnreadableMaterialsError) Error() string
func (e *UnreadableMaterialsError) AsAppError() *AppError // 409 conflict
```

#### E. `internal/core/eventbus`
```go
package eventbus

type Handler func(ctx context.Context, ev any)

type Bus interface {
    Publish(ctx context.Context, ev any)      // 同步按注册顺序分发，单订阅者 panic 记日志不中断其他订阅者
    PublishAsync(ctx context.Context, ev any) // 使用 tenant.Detach(ctx) 异步派发
    Subscribe(eventType reflect.Type, h Handler)
}

func SubscribeTyped[T any](b Bus, fn func(ctx context.Context, ev T))
func NewInMemoryBus(logger *slog.Logger) *InMemoryBus
```

#### F. `internal/core/spi`（核心 SPI + 5 大二期预留接口）
- **二期预留 5 大 SPI**：
  1. `ProcessLauncher` (`Mode() string`, `Start(ctx context.Context, spec ProcessSpec) (ProcessHandle, error)`)
  2. `ModelGateway` (`Resolve(ctx context.Context, providerKey, excludeCredID string) (*ResolvedEndpoint, error)`, `ReportEndpointFailure(ctx context.Context, providerKey string, ep *ResolvedEndpoint, reason string) (*ResolvedEndpoint, error)`)
  3. `Authenticator` (`Kind() string`, `Authenticate(ctx context.Context, cred Credentials) (*AuthPrincipal, error)`)
  4. `IMConnector` (`Type() string`, `Start(ctx context.Context) error`, `ReconnectCompany(ctx context.Context, companyID string) error`, `NotifyOwner(ctx context.Context, src domain.EventSource, text string) error`, `Close() error`)
  5. `OwnerTransferHook` (`TransferOwnership(ctx context.Context, fromUserID, toUserID string) (*OwnerTransferSummary, error)`)
- **跨模块解耦 7 大 SPI**（1:1 映射 Java `superagent-core`）：
  6. `AgentRunner` + `StreamParser` + `EventSink`
  7. `ProjectAccess`
  8. `TaskDoers`
  9. `ModelRouter`
  10. `TaskModelSelector`
  11. `InboxProcessor`
  12. `LinkReader` + `UsageRecorder` + `FileStore` + `FileStoreProvider`

#### G. `internal/core/store`（`TxManager` + 38 个持久化接口）
完整 38 个接口签名冻结在 §5.3（及 [spec_02_storage_postgres_migrations.md](./spec_02_storage_postgres_migrations.md) §4.1），接口零依赖任何驱动类型。

#### H. `internal/core/api` & `internal/core/obs`
- `api.Response[T]`（`Code`, `Message`, `Data` JSON 字段严格 camelCase，对齐 `superagent-api/.../ApiResponse.java`）；
- `api.ErrorBody`（`Code`, `Message`）、`api.MetaResponse`、`api.PageQuery`（`DefaultPage=1`, `DefaultSize=20`, `MaxSize=200`）、`api.PageResult[T]`、`api.Paths`；
- `obs.WithTraceID` / `obs.TraceIDFrom` / `obs.WithUserID` / `obs.UserIDFrom` / `obs.SanitizeTraceID` / `obs.NewContextHandler` / `obs.Metrics`（6 项 Prometheus 指标）。

### 4.2 路由契约

本篇不注册 Gin 路由，仅冻结 `internal/core/api/paths.go` 中的路径前缀常量（对照 `superagent-api/src/main/java/com/superagent/api/ApiPaths.java:L10-L86`），供 `SA-13`（`internal/web`）统一引用：
- `V1 = "/api/v1"`
- `Meta = V1 + "/meta"`, `Auth = V1 + "/auth"`, `Account = V1 + "/account"`, `Avatars = V1 + "/avatars"`, `Users = V1 + "/users"`, `DirectoryUsers = V1 + "/directory/users"`
- `OrgsRegistration = V1 + "/orgs/registration"`（V1 固定返回 `{"open": false}`；**不再定义或暴露 `POST /api/v1/orgs`**）
- `Agents = V1 + "/agents"`, `SuperAgent = V1 + "/superagent"`, `Sessions = V1 + "/sessions"`
- `Tasks = V1 + "/tasks"`, `Automations = V1 + "/automations"`
- `Files = V1 + "/files"`, `FileFolders = Files + "/folders"`, `Projects = V1 + "/projects"`, `Skills = V1 + "/skills"`
- `Connectors = V1 + "/connectors"`, `OAuth = V1 + "/oauth"`, `Providers = V1 + "/providers"`, `Settings = V1 + "/settings"`, `BrandingLogo = V1 + "/branding/logo"`
- `Inbox = V1 + "/inbox"`, `Notices = V1 + "/notices"`, `Usage = V1 + "/usage"`
- 管理端前缀：`AdminAnalytics = V1 + "/admin/analytics"`, `AdminUsage = V1 + "/admin/usage"`, `AdminOrg = V1 + "/admin/org"`, `AdminFileStorage = V1 + "/admin/file-storage"`, `AdminModelPrices = V1 + "/admin/model-prices"`, `AdminIntegrations = V1 + "/admin/integrations"`
- 非 V1 前缀：`MCP = "/mcp"`, `ActuatorHealth = "/actuator/health"`, `ActuatorPrometheus = "/actuator/prometheus"`

### 4.3 错误语义

所有 HTTP JSON 错误响应统一遵循 `{"code":"<Code>","message":"<Message>","data":null}`（`POST /api/v1/auth/login` 失败例外返回裸 `{"code":"unauthorized","message":"..."}`，详见 `SA-13`）。精确映射对照 Java `superagent-web/src/main/java/com/superagent/web/GlobalExceptionHandler.java:L33-L106`：

| `apperror` 构造 / 场景 | HTTP 状态码 | `code` | Java 原版参照 |
|---|---:|---|---|
| `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `IllegalArgumentException` / `MethodArgumentNotValidException` / `MethodArgumentTypeMismatchException` (`L33-L39,L66-L69,L83-L87`) |
| `apperror.Unauthorized(...)` | `401` | `unauthorized` | `SecurityConfig` EntryPoint / FailureHandler |
| `apperror.PasswordChangeRequired(...)` | `403` | `password_change_required` | `PasswordChangeFilter` |
| `apperror.Forbidden(...)` / `apperror.EgressDenied(...)` | `403` | `forbidden` | `ForbiddenException` (`L51-L54`), `EgressDeniedException` |
| `apperror.NotFound(...)` | `404` | `not_found` | `NotFoundException` (`L46-L49`), `NoResourceFoundException` (`L41-L44`) |
| `apperror.MethodNotAllowed(...)` | `405` | `method_not_allowed` | `HttpRequestMethodNotSupportedException` (`L77-L81`) |
| `apperror.Conflict(...)` / `UnreadableMaterialsError` | `409` | `conflict` | `ConflictException` (`L56-L59`) |
| `apperror.Busy(...)` | `409` | `busy` | `BusyException` (`L61-L64`) |
| `apperror.TooLarge(...)` | `413` | `too_large` | `MaxUploadSizeExceededException` (`L71-L75`，固定文案 `"单个文件不能超过 50 MB"`) |
| `apperror.Internal(...)` / 未分类 `error` | `500` | `internal_error` | `Exception` (`L100-L106`) |

**特别约束**：
- `tenant.Require(ctx)` 缺失租户返回 `tenant.ErrMissingTenant`（系统内部编程错误，映射为 `500 internal_error` 并在日志以 `ERROR` 级别告警，绝不静默降级为默认租户）。
- `SecretCipher.Decrypt` 遇到跨租户密文返回 `crypto.ErrCrossTenantSecret`（禁止泄露明文或退回主密钥重试）。
- `EgressGuard` 所有拦截错误均通过 `apperror.EgressDenied(msg)` 返回 HTTP `403 forbidden`，文案精确沿用原版中文提示（见 §5.6）。

### 4.4 SQL 契约

本篇不编写 SQL，但通过 `internal/core/store` 接口与 `internal/arch_test.go` 冻结两条面向存储层的铁律：
1. **依赖方向铁律**：`internal/core/*` 零引入任何 SQL/驱动包；`internal/platform/*`、`internal/agent/*`、`internal/task/*`、`internal/cost/*`、`internal/docs/*`、`internal/files/*`、`internal/project/*`、`internal/skills/*`、`internal/event/*`、`internal/orchestrator/*`、`internal/web/*` 严禁直接 import `github.com/jackc/pgx/v5` 或 `internal/storage/*`，所有数据读写必须经 `internal/core/store` 接口。
2. **事务透传铁律**：`store.TxManager.WithinTx(ctx, fn)` 通过 `ctx` 隐式透传事务句柄，业务服务无需感知底层数据库事务类型。

---

## 5. 行为语义（含 Java 源码对照、Go 数据定义与详细伪代码）

### 5.1 领域实体与枚举全集 (`internal/core/domain`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/{tenant,user,agent,chat,task,automation,project,file,skill,connector,provider,inbox,notice,usage,timing}/*.java`（全量扫描核对，应用 V1 裁剪点）。

```go
package domain

import (
	"time"
	"github.com/shopspring/decimal"
)

// ==================== 1. Tenant, User & Session ====================
// Java: superagent-core/.../tenant/Company.java:L10-L35
type Company struct {
	ID              string
	Name            string
	Enabled         bool
	DataKeyCipher   string // v1:<sealedDEK>，未生成前为空串
	MaxUsers        *int   // nil = 使用全局配置默认，0 = 不限
	MaxRunningTasks *int
	MonthlyTokens   *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// V1 新增：服务端持久化会话（替代 JVM 内存 HttpSession）
type UserSession struct {
	ID         string // 32 字节加密安全随机 hex（64 字符）
	CompanyID  string
	UserID     string
	PasswordOK bool
	ExpiresAt  time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// Java: superagent-core/.../user/UserSource.java:L4-L9
type UserSource string

const (
	UserSourceLocal  UserSource = "LOCAL"
	UserSourcePhone  UserSource = "PHONE"
	UserSourceSSO    UserSource = "SSO"
	UserSourceWeChat UserSource = "WECHAT"
)

// Java: superagent-core/.../user/User.java:L19-L52
type User struct {
	ID                 string // 不可变 UUID，全系统外键引用标识
	Username           string // 公司内唯一登录名
	DisplayName        string
	Roles              []string // 如 ["ADMIN"]（USER 隐式具备）
	Enabled            bool
	PasswordHash       string
	Phone              string
	ExternalID         string
	Source             UserSource
	CreatedAt          time.Time
	UpdatedAt          time.Time
	MustChangePassword bool
}

func (u User) IsAdmin() bool {
	for _, r := range u.Roles {
		if r == "ADMIN" {
			return true
		}
	}
	return false
}

// ==================== 2. Agent, Memory, Collaboration & Log ====================
// Java: superagent-core/.../agent/AgentStatus.java:L4-L7, AgentKind.java:L4-L9
type AgentStatus string

const (
	AgentStatusActive  AgentStatus = "ACTIVE"
	AgentStatusDeleted AgentStatus = "DELETED"
)

type AgentKind string

const (
	AgentKindStandard AgentKind = "STANDARD"
	AgentKindSuper    AgentKind = "SUPER"
)

// Java: superagent-core/.../agent/Agent.java:L23-L92
// V1 裁剪：彻底移除 Character（原 character_key）与 Tier 字段
type Agent struct {
	ID        string
	Name      string
	Avatar    string
	Persona   string
	Model     string
	Status    AgentStatus
	Owner     string
	Kind      AgentKind
	ManagedBy string // 聘用它的 SuperAgent agent.id，用户自建为空串
	CreatedAt time.Time
	UpdatedAt time.Time
}

func (a Agent) IsSuper() bool { return a.Kind == AgentKindSuper }

// Java: superagent-core/.../agent/AgentMember.java:L10-L11
type AgentMember struct {
	AgentID   string
	Username  string // 实际存储 app_user.id
	AddedBy   string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../agent/AgentInvite.java:L11-L22
type AgentInviteStatus string

const (
	AgentInvitePending   AgentInviteStatus = "PENDING"
	AgentInviteConfirmed AgentInviteStatus = "CONFIRMED"
	AgentInviteCancelled AgentInviteStatus = "CANCELLED"
)

type AgentInvite struct {
	ID        string
	AgentID   string
	Owner     string
	Usernames []string
	SessionID string
	MessageID string
	Status    AgentInviteStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../agent/AgentMemoryState.java:L13-L20
type AgentMemoryState struct {
	AgentID         string
	Owner           string
	SummarizedUntil *time.Time
	LastModel       string
	SessionFailures int
	Memory          string
	UpdatedAt       time.Time
}

// Java: superagent-core/.../agent/AgentLogEntry.java:L11-L19
type AgentLogEntry struct {
	ID       string
	AgentID  string
	Owner    string
	At       time.Time
	Level    string // info | warn | error
	Category string // session | turn | memory | superagent | settings
	Summary  string
	Detail   string
}

// ==================== 3. Chat, Messages & AgentEvent ====================
// Java: superagent-core/.../chat/AgentType.java:L8-L43
type AgentType string

const (
	AgentTypeClaude    AgentType = "CLAUDE"    // V1 GA
	AgentTypeCodex     AgentType = "CODEX"     // V1 GA
	AgentTypeKimi      AgentType = "KIMI"      // 预留扩展
	AgentTypeDeepSeek  AgentType = "DEEPSEEK"  // 预留扩展
	AgentTypeQwen      AgentType = "QWEN"      // 预留扩展
	AgentTypeGLM       AgentType = "GLM"       // 预留扩展
	AgentTypeGrok      AgentType = "GROK"      // 预留扩展
	AgentTypeCodeBuddy AgentType = "CODEBUDDY" // 预留扩展
	AgentTypeGemini    AgentType = "GEMINI"    // 预留扩展
	AgentTypeMiniMax   AgentType = "MINIMAX"   // 预留扩展
)

func (t AgentType) CLIName() string // strings.ToLower(string(t))
func ParseAgentType(s string) (AgentType, error) // 空串默认 CLAUDE

// Java: superagent-core/.../chat/{SessionStatus,MessageRole,AgentEventType}.java
type SessionStatus string

const (
	SessionStatusActive  SessionStatus = "ACTIVE"
	SessionStatusClosed  SessionStatus = "CLOSED"
	SessionStatusDeleted SessionStatus = "DELETED"
)

const (
	SessionReasonFirst        = "first"
	SessionReasonModelChanged = "model"
	SessionReasonModelDown    = "model_down"
	SessionReasonFailures     = "failures"
)

type MessageRole string

const (
	MessageRoleUser      MessageRole = "USER"
	MessageRoleAssistant MessageRole = "ASSISTANT"
	MessageRoleSystem    MessageRole = "SYSTEM"
)

type AgentEventType string

const (
	EventSystem     AgentEventType = "SYSTEM"
	EventThinking   AgentEventType = "THINKING"
	EventText       AgentEventType = "TEXT"
	EventToolUse    AgentEventType = "TOOL_USE"
	EventToolResult AgentEventType = "TOOL_RESULT"
	EventResult     AgentEventType = "RESULT"
	EventUsage      AgentEventType = "USAGE"
	EventTokens     AgentEventType = "TOKENS" // 由 UsageRecorder 截获，不落 agent_event
	EventError      AgentEventType = "ERROR"
)

// Java: superagent-core/.../chat/ChatSession.java:L15-L47
type ChatSession struct {
	ID             string
	Title          string
	AgentType      AgentType
	AgentSessionID string
	WorkspaceDir   string
	AgentID        string
	Status         SessionStatus
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Model          string
	Reason         string
	CLIRestarts    int
}

// Java: superagent-core/.../chat/ChatMessage.java:L9-L21
type ChatMessage struct {
	ID        string
	SessionID string
	Role      MessageRole
	Content   string
	CreatedAt time.Time
	Author    string // USER 消息发言者 app_user.id
}

// Java: superagent-core/.../chat/AgentEvent.java:L10-L16
// V1 修复：增加 ID int64（BIGSERIAL 主键，支持游标增量拉取）
type AgentEvent struct {
	ID        int64
	SessionID string
	MessageID string
	Seq       int64
	Type      AgentEventType
	Payload   string
	CreatedAt time.Time
}

// Java: superagent-core/.../chat/ImageAttachment.java:L11, RunCredential.java:L9-L15
type ImageAttachment struct {
	Path      string
	MediaType string
}

type RunCredential struct {
	ID      string
	BaseURL string
	APIKey  string
	Bearer  bool
}

func (r RunCredential) String() string // 严禁打印 APIKey，仅输出 ID + BaseURL

// Java: superagent-core/.../chat/AgentRunRequest.java:L18-L90
type AgentRunRequest struct {
	SessionID       string
	AgentSessionID  string
	WorkspaceDir    string
	Prompt          string
	Model           string
	AllowedTools    []string
	SkipPermissions bool
	MCPConfig       string
	SystemPrompt    string
	Images          []ImageAttachment
	Memory          string
	Credential      *RunCredential
	OwnerUserID     string // 供 ProcessLauncher 标识归属员工（二期每员工沙箱）
}

func (r AgentRunRequest) IsFirstTurn() bool { return strings.TrimSpace(r.AgentSessionID) == "" }
func (r AgentRunRequest) SystemPromptWithMemory() string // 严格保持 Java L81-L89 中文提示词块

// ==================== 4. Task & Automation ====================
// Java: superagent-core/.../task/{TaskSource,TaskStatus,TaskKind,TaskPhaseStatus}.java
type TaskSource string

const (
	TaskSourceWebChat    TaskSource = "WEB_CHAT"
	TaskSourceTaskForm   TaskSource = "TASK_FORM"
	TaskSourceAutomation TaskSource = "AUTOMATION"
	TaskSourceInbox      TaskSource = "INBOX"
)

type TaskStatus string

const (
	TaskStatusPending           TaskStatus = "PENDING"
	TaskStatusRunning           TaskStatus = "RUNNING"
	TaskStatusNeedsConfirmation TaskStatus = "NEEDS_CONFIRMATION"
	TaskStatusWaiting           TaskStatus = "WAITING"
	TaskStatusDone              TaskStatus = "DONE"
	TaskStatusError             TaskStatus = "ERROR"
	TaskStatusStopped           TaskStatus = "STOPPED"
	TaskStatusDeleted           TaskStatus = "DELETED"
)

type TaskKind string

const (
	TaskKindCoding  TaskKind = "coding"
	TaskKindGeneral TaskKind = "general"
	TaskKindComplex TaskKind = "complex"
)

func ParseTaskKind(s string) TaskKind // 空/未知均回落 TaskKindGeneral（对齐 TaskKind.java:L23-L32）

type TaskPhaseStatus string

const (
	TaskPhasePending TaskPhaseStatus = "PENDING"
	TaskPhaseRunning TaskPhaseStatus = "RUNNING"
	TaskPhaseDone    TaskPhaseStatus = "DONE"
)

// Java: superagent-core/.../task/Task.java:L18-L117
// V1 裁剪：移除 Tier 字段；Spend 使用 decimal.Decimal 对齐 PG NUMERIC(16,6)
type Task struct {
	ID           string
	Title        string
	Brief        string
	Source       TaskSource
	Repo         string
	Branch       string
	Flow         TaskKind
	Status       TaskStatus
	Progress     int // 0..100
	Spend        decimal.Decimal
	Model        string
	AgentType    AgentType
	WorkspaceDir string
	CreatedBy    string
	Error        string
	CreatedAt    time.Time
	StartedAt    *time.Time
	FinishedAt   *time.Time
	UpdatedAt    time.Time
}

// Java: superagent-core/.../task/{TaskPhase,TaskRunState,TaskOrigin,TaskLink,TaskModelChoice}.java
type TaskPhase struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Status   TaskPhaseStatus `json:"status"`
	Content  string          `json:"content,omitempty"`
	Evidence string          `json:"evidence,omitempty"`
}

type TaskRunState struct {
	TaskID         string
	AgentSessionID string
	FailureCount   int
	AttemptCount   int
	UpdatedAt      time.Time
}

type TaskOrigin struct {
	TaskID    string
	AgentID   string
	SessionID string
	MessageID string
}

type TaskLink struct {
	TaskID        string
	ParentID      string
	ContinuesFrom string
	CreatedAt     time.Time
}

const (
	ChoiceByJudge      = "judge"
	ChoiceByUser       = "user"
	ChoiceByFallback   = "fallback"
	ChoiceByEscalation = "escalation"
	ChoiceByDefault    = "default"
	ChoiceByFixed      = "fixed"
)

type TaskModelChoice struct {
	TaskID     string
	Seq        int
	By         string
	Difficulty string
	Tier       string // 仅作选型审计历史记录（来自成本路由档位）
	Model      string
	CLI        string
	Judge      string
	Candidates string
	Reasoning  string
	CreatedAt  time.Time
}

// Java: superagent-core/.../automation/{Automation,AutomationRun,AutomationType,OverlapPolicy}.java
// V1 裁剪：移除 Automation.Tier 字段
type AutomationType string

const (
	AutomationRecurring AutomationType = "RECURRING"
	AutomationOnce      AutomationType = "ONCE"
	AutomationAPI       AutomationType = "API"
)

type OverlapPolicy string

const (
	OverlapSkip     OverlapPolicy = "SKIP"
	OverlapParallel OverlapPolicy = "PARALLEL"
)

type Automation struct {
	ID           string
	Name         string
	Prompt       string
	Type         AutomationType
	Cron         string
	RunAt        *time.Time
	Timezone     string
	Model        string
	Overlap      OverlapPolicy
	Enabled      bool
	Source       string // manual | chat
	Owner        string
	APITokenHash string
	NextRunAt    *time.Time
	LastRunAt    *time.Time
	LastTaskID   string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type AutomationRun struct {
	ID            string
	AutomationID  string
	Trigger       string // schedule | api | manual
	TaskID        string
	SkippedReason string
	FiredAt       time.Time
}

// ==================== 5. SuperAgent, Inbox & Notice ====================
// Java: superagent-core/.../agent/{SuperAgentState,SuperAgentWork,SuperAgentWorkEvent}.java
// V1 裁剪：移除 SuperAgentState.TrackedJSON
const (
	PlanStatusProposed = "proposed"
	PlanStatusApproved = "approved"
)

type SuperAgentState struct {
	Owner          string
	AgentID        string
	ProcessedUntil *time.Time
	PlanID         string
	PlanStatus     string
	PlanJSON       []byte // PG17 JSONB（空时为 nil）
	UpdatedAt      time.Time
}

const (
	WorkStatusRunning           = "running"
	WorkStatusNeedsConfirmation = "needs_confirmation"
	WorkStatusDone              = "done"
	WorkStatusError             = "error"
	WorkStatusStopped           = "stopped"
	WorkStatusDeleted           = "deleted"
)

type SuperAgentWork struct {
	TaskID     string
	Owner      string
	PlanID     string
	Title      string
	MemberID   string
	MemberName string
	Status     string
	Summary    string
	AssignedAt time.Time
	FinishedAt *time.Time
	UpdatedAt  time.Time
	MessageID  string
}

type SuperAgentWorkEvent struct {
	ID     string
	TaskID string
	Owner  string
	Kind   string // assigned | needs_confirmation | replied | done | error | stopped | deleted
	Detail string
	At     time.Time
}

// Java: superagent-core/.../inbox/{EventPriority,EventSource,InboxEvent,InboxTodo}.java
// V1 裁剪：EventSource 移除 5 个废弃空列及 secretary_agent_id
type EventPriority string

const (
	PriorityP0     EventPriority = "P0"
	PriorityP1     EventPriority = "P1"
	PriorityP2     EventPriority = "P2"
	PriorityIgnore EventPriority = "IGNORE"
)

const (
	EventSourceFeishu   = "feishu"
	EventSourceDingTalk = "dingtalk"
)

type EventSource struct {
	Owner           string
	Type            string // feishu | dingtalk
	AppID           string
	Enabled         bool
	OwnerExternalID string
	BindCode        string
	DigestTime      string // HH:mm
	Timezone        string // IANA TZ
	LastDigestDate  string // YYYY-MM-DD（空串表示未发送过）
	LastHourlyAt    *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (s EventSource) LinkedTo(currentAppID string) bool {
	return s.OwnerExternalID != "" && currentAppID != "" && s.AppID == currentAppID
}

type InboxEvent struct {
	ID           string
	Owner        string
	Source       string
	ExternalID   string
	ChatID       string
	ChatName     string
	ChatType     string // p2p | group
	ThreadID     string
	SenderID     string
	SenderName   string
	Text         string
	MentionsMe   bool
	Raw          string
	Priority     EventPriority
	TriageReason string
	Processed    bool
	Handling     string // delivered | notified | todo | task | read | ignored
	OccurredAt   *time.Time
	ReceivedAt   time.Time
	UpdatedAt    time.Time
}

type InboxTodoStatus string

const (
	TodoOpen      InboxTodoStatus = "OPEN"
	TodoTasked    InboxTodoStatus = "TASKED"
	TodoDone      InboxTodoStatus = "DONE"
	TodoDismissed InboxTodoStatus = "DISMISSED"
)

type InboxTodo struct {
	ID        string
	Owner     string
	Title     string
	Detail    string
	Requester string
	Due       string
	EventIDs  string
	Source    string
	Status    InboxTodoStatus
	TaskID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../notice/Notice.java:L11-L40
type NoticeKind string

const (
	NoticeKindConfirm NoticeKind = "CONFIRM"
	NoticeKindAction  NoticeKind = "ACTION"
	NoticeKindDone    NoticeKind = "DONE"
	NoticeKindNotice  NoticeKind = "NOTICE"
	NoticeKindTrack   NoticeKind = "TRACK"
)

type NoticeState string

const (
	NoticeStateOpen    NoticeState = "OPEN"
	NoticeStateDone    NoticeState = "DONE"
	NoticeStateIgnored NoticeState = "IGNORED"
)

type Notice struct {
	ID        string
	Owner     string
	Key       string
	Kind      NoticeKind
	State     NoticeState
	Title     string
	Body      string
	Actor     string
	LinkType  string // task | agent | superagent | todo
	LinkID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ==================== 6. Project, WorkFile, Skill ====================
// Java: superagent-core/.../project/{Project,ProjectStatus,ProjectMember,ProjectItemKind}.java
type ProjectStatus string

const (
	ProjectStatusActive   ProjectStatus = "ACTIVE"
	ProjectStatusArchived ProjectStatus = "ARCHIVED"
)

type ProjectItemKind string

const (
	ProjectItemAgent ProjectItemKind = "AGENT"
	ProjectItemTask  ProjectItemKind = "TASK"
	ProjectItemFile  ProjectItemKind = "FILE"
)

type Project struct {
	ID          string
	Name        string
	Description string
	Owner       string
	Status      ProjectStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  *time.Time
}

type ProjectMember struct {
	ProjectID string
	Username  string
	AddedBy   string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../file/{WorkFile,WorkFileKind,FileFolder}.java
type WorkFileKind string

const (
	WorkFileDocument WorkFileKind = "document"
	WorkFileImage    WorkFileKind = "image"
	WorkFileVideo    WorkFileKind = "video"
	WorkFileLink     WorkFileKind = "link"
	WorkFileOther    WorkFileKind = "other"
)

const (
	WorkFileStorageInternal = "internal"
	WorkFileStorageLink     = "link"
	WorkFileOriginChat      = "chat"
	WorkFileOriginTask      = "task"
	WorkFileOriginDelivery  = "delivery"
	WorkFileOriginUpload    = "upload"
	TagStatePending         = ""
	TagStateAgent           = "agent"
	TagStateAuto            = "auto"
	TagStateUser            = "user"
	MaxWorkFileTags         = 5
	MaxWorkFileTagLen       = 20
)

type WorkFile struct {
	ID        string
	Name      string
	Kind      WorkFileKind
	MimeType  string
	Size      int64
	Storage   string
	Location  string
	Owner     string
	AgentID   string
	TaskID    string
	Origin    string
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Tags      []string
	TagState  string
}

type FileFolder struct {
	ID        string
	Owner     string
	Name      string
	ParentID  string
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Java: superagent-core/.../skill/{Skill,SkillDeletion}.java
type Skill struct {
	ID          string
	Name        string
	Description string
	Category    string
	Content     string
	Space       string // personal:<userId> | team:<path> | company
	Enabled     bool
	Deleted     bool
	CreatedBy   string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SkillDeletionStatus string

const (
	SkillDeletionPending   SkillDeletionStatus = "PENDING"
	SkillDeletionConfirmed SkillDeletionStatus = "CONFIRMED"
	SkillDeletionCancelled SkillDeletionStatus = "CANCELLED"
)

type SkillDeletion struct {
	ID        string
	SkillID   string
	SkillName string
	AgentID   string
	Requester string
	SessionID string
	MessageID string
	Status    SkillDeletionStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ==================== 7. Connector & ModelProvider ====================
// Java: superagent-core/.../connector/{Connector,ConnectorKind,ConnectorAuthMethod,ConnectorTransport,ConnectorCredential,McpServerSpec}.java
type ConnectorKind string

const ConnectorKindMCP ConnectorKind = "MCP"

type ConnectorAuthMethod string

const (
	ConnectorAuthNone          ConnectorAuthMethod = "NONE"
	ConnectorAuthSharedToken   ConnectorAuthMethod = "SHARED_TOKEN"
	ConnectorAuthPersonalToken ConnectorAuthMethod = "PERSONAL_TOKEN"
	ConnectorAuthOAuth         ConnectorAuthMethod = "OAUTH"
)

type ConnectorTransport string

const (
	ConnectorTransportStdio ConnectorTransport = "STDIO"
	ConnectorTransportHTTP  ConnectorTransport = "HTTP"
	ConnectorTransportSSE   ConnectorTransport = "SSE"
)

type Connector struct {
	ID          string
	Name        string
	Description string
	Kind        ConnectorKind
	AuthMethod  ConnectorAuthMethod
	Enabled     bool
	Transport   ConnectorTransport
	Endpoint    string
	TokenEnv    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const ConnectorCredentialCompanyOwner = ""

type ConnectorCredential struct {
	ConnectorID   string
	Owner         string // "" 表示公司共享凭证
	SecretCipher  string
	AccountLabel  string
	UpdatedAt     time.Time
	RefreshCipher string
	ExpiresAt     *time.Time
}

type MCPServerSpec struct {
	Name      string
	Transport ConnectorTransport
	URL       string
	Headers   map[string]string
	Command   string
	Args      []string
	Env       map[string]string
}

// Java: superagent-core/.../provider/{ModelProvider,ProviderCredential}.java
type ModelProvider struct {
	ID              string
	ProviderKey     string
	Installed       bool
	CanInfer        bool
	CurrentVersion  string
	LatestVersion   string
	Status          string // unsupported | not_installed | no_auth | ok | needs_upgrade
	LastCheckedAt   *time.Time
	ModelsJSON      []byte // PG17 JSONB
	ModelsError     string
	ModelsCheckedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

const (
	CredSourceConsole = "console"
	CredSourceMachine = "machine"
	CredStatusOK      = "ok"
	CredStatusError   = "error"
	CredStatusUnknown = "unknown"
)

type ProviderCredential struct {
	ID          string
	ProviderKey string
	Label       string
	BaseURL     string
	KeyCipher   string
	Bearer      bool
	Priority    int
	Enabled     bool
	Status      string
	LastError   string
	CheckedAt   *time.Time
	Deleted     bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string
	KeyHash     string
}

// ==================== 8. Usage & TurnTiming ====================
// Java: superagent-core/.../usage/{TokenUsage,UsageContext,UsageRecord}.java
const (
	UsageSourceTask        = "task"
	UsageSourceChat        = "chat"
	UsageSourceSuperAgent  = "superagent"
	UsageSourceBackground  = "superagent_background"
	UsageSourceModelChoice = "model_choice"
	UsageSourceMemory      = "memory"
	UsageSourceFileTags    = "file_tags"
	UsageSourceTaskTags    = "task_tags"
)

type TokenUsage struct {
	Model        string
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64
	Output       int64
	Reasoning    int64
	CLICostUSD   *decimal.Decimal
}

type UsageContext struct {
	User      string
	Source    string
	AgentID   string
	TaskID    string
	SessionID string
	CLI       string
}

type UsageRecord struct {
	ID           string
	At           time.Time
	User         string
	Source       string
	AgentID      string
	TaskID       string
	SessionID    string
	CLI          string
	Model        string
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64
	Output       int64
	Reasoning    int64
	CostUSD      *decimal.Decimal
	CLICostUSD   *decimal.Decimal
}

func (r UsageRecord) Total() int64 { return r.Input + r.CacheRead + r.CacheWrite + r.Output }

// Java: superagent-core/.../timing/TurnTiming.java:L16-L27
type ToolTime struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Ms    int64  `json:"ms"`
}

type TurnTiming struct {
	SessionID  string
	MessageID  string
	Label      string
	StartedAt  time.Time
	FinishedAt time.Time
	PrepMs     int64
	StartupMs  int64
	ModelMs    int64
	ToolsMs    int64
	OutputMs   int64
	RetryMs    int64
	SaveMs     int64
	Attempts   int
	Tools      []ToolTime
}

func (t TurnTiming) TotalMs() int64 { return t.FinishedAt.Sub(t.StartedAt).Milliseconds() }
```

### 5.2 纯领域工具算法对照（`TeamPaths` / `LinkUrls` / `WorkFile.CleanTags` / `TurnTimer` / `AutomationSchedule`）

1. **`TeamPaths`**（Java `superagent-core/.../org/TeamPaths.java:L7-L59`）：
   - `NormalizeTeamPath(raw string) (string, error)`：按 `/` 拆分，逐段 `strings.TrimSpace`，跳过空段（容忍 `a//b` 与首尾 `/`）；每段禁止长度 `> 64`、以 `.` 开头、含 `›`、含控制字符（`< 0x20` / `0x7f`）或 `\`；段数必须在 `1..8` 之间；用 `/` 拼接返回。
   - `TeamAncestors(team string) []string`：`a/b/c` -> `["a/b/c", "a/b", "a"]`。
   - `TeamWithin(team, root string) bool`：`team == root || strings.HasPrefix(team, root+"/")`。
2. **`LinkUrls`**（Java `superagent-core/.../link/LinkUrls.java:L11-L44`）：
   - 正则提取 `https?://[^\s<>"'\x60，。；、（）【】《》「」]+`（按出现顺序去重）；
   - 尾部剥离 `.,;:!?)]}*_~>`，遇到 `)` 时若链接中 `(` 数量 `>=` `)` 数量（Wikipedia 风格平衡括号）则停止剥离；结果长度须 `> len("https://")`。
3. **`WorkFile.CleanTags`**（Java `superagent-core/.../file/WorkFile.java:L51-L66`）：
   - 去除 `#`、将连续空白/中英文逗号分号压缩为单空格并 trim、截断至 **20 个 rune**（注意按 `utf8.RuneCountInString` 截断以安全支持中文标签）、按小写去重、最多保留 **5 个**。
4. **`TurnTimer`**（Java `superagent-core/.../timing/TurnTimer.java:L19-L134`）：
   - 内部持有 `sync.Mutex`、`clock func() int64`（默认 `time.Now().UnixMilli`）与 FIFO 队列 `open []openTool`；
   - `AttemptStarted()`：首次调用记录 `prepEnd = now`，后续调用累加 `retry += now - attemptStart` 并重置单次 `firstEvent/firstOutput/runEnd/toolsTotal/open/tools`，`attempts++`；
   - `AttemptEnded()`：记录 `runEnd = now`，并把未闭合的 `open` 工具全部结算至 `now`；
   - `TapTools(next spi.EventSink) spi.EventSink`：收到 `TOOL_USE` 时记录工具名与起始时间（空白名记为 `"tool"`），收到 `TOOL_RESULT` 时 FIFO 弹出并累加 `toolsTotal` 及按名聚合 `{count, ms}`；
   - `Finish(sessionID, messageID, label string) domain.TurnTiming`：
     - `attempts == 0`：全部归入 `prepMs = end - start`，其余为 `0`；
     - 否则精确计算 `startup = (firstEvent<0 ? ran : firstEvent) - attemptStart`、`model = firstEvent<0 ? 0 : (firstOutput<0 ? ran : firstOutput) - firstEvent`、`output = firstOutput<0 ? 0 : max(0, ran - firstOutput - toolsTotal)`、`toolsMs = min(toolsTotal, max(0, ran - firstOutputOrRan))`、`saveMs = max(0, end - ran)`，满足 `prep + retry + startup + model + tools + output + save == totalMs` 恒等式；`Tools` 按 `Ms DESC` 排序。
5. **`AutomationSchedule`**（Java `superagent-core/.../automation/AutomationSchedule.java:L20-L88`）：
   - 使用 `robfig/cron/v3` 标准 5 字段解析器（`cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow`）；
   - `LoadZone(tz string) (*time.Location, error)`：空串默认 `Asia/Shanghai`，非法 IANA 时区返 `apperror.InvalidArg`；
   - `NormalizeCron(raw string) (string, error)`：校验恰为 5 字段且合法，规范化为单空格分隔；
   - `NextCron(expr, tz string, after time.Time) (*time.Time, error)`：在目标 `Location` 下计算严格大于 `after` 的下一次触发 UTC 时间（DST 安全）；
   - `Upcoming(expr, tz string, after time.Time, count int) ([]time.Time, error)` 与 `ParseLocal(raw, tz string) (time.Time, error)`（支持 `2006-01-02T15:04` 与 `2006-01-02T15:04:05`）。

### 5.3 全量 38 个 Store 接口与 `TxManager` (`internal/core/store`)

> **Java 源码对照**：`superagent-core/src/main/java/**/*Store.java`（全部 37 个数据库 Store 接口，排除 `FileStore` 对象存储 SPI，外加 V1 新增 `SessionStore` = 38 个数据库 Store）。

```go
package store

import (
	"context"
	"time"
	"github.com/Karlsk/synapse-os/internal/core/domain"
)

// TxManager 跨 Store 原子事务边界（pgx.Tx 隐式存于 ctx）
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// --- 全局表 Store（2 个，不调用 tenant.Require）---
type CompanyStore interface {
	List(ctx context.Context) ([]domain.Company, error)
	Find(ctx context.Context, id string) (*domain.Company, error)
	Save(ctx context.Context, c domain.Company) (*domain.Company, error)
}

type SessionStore interface { // V1 新增（user_session）
	Create(ctx context.Context, s domain.UserSession) error
	Find(ctx context.Context, id string) (*domain.UserSession, error)
	Touch(ctx context.Context, id string, passwordOK bool, expiresAt time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteByUser(ctx context.Context, companyID, userID string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// --- 租户隔离 Store（36 个，实现内必须强制 tenant.Require(ctx)）---
type UserStore interface {
	ListUsers(ctx context.Context) ([]domain.User, error)
	FindByID(ctx context.Context, id string) (*domain.User, error)
	FindByUsername(ctx context.Context, username string) (*domain.User, error)
	FindByPhone(ctx context.Context, phone string) (*domain.User, error)
	FindByExternalID(ctx context.Context, externalID string) (*domain.User, error)
	Save(ctx context.Context, u domain.User) (*domain.User, error)
	Delete(ctx context.Context, id string) error
}

type OrgStore interface { // user_manager + user_team
	Manager(ctx context.Context, userID string) (string, bool, error)
	Managers(ctx context.Context) (map[string]string, error)
	SetManager(ctx context.Context, userID, managerID string) error // managerID == "" 清除
	Teams(ctx context.Context, userID string) ([]string, error)
	Memberships(ctx context.Context) (map[string][]string, error)
	SetTeams(ctx context.Context, userID string, teams []string) error
}

type AgentStore interface {
	Create(ctx context.Context, a domain.Agent) (*domain.Agent, error)
	Find(ctx context.Context, id string) (*domain.Agent, error)
	List(ctx context.Context) ([]domain.Agent, error)
	Update(ctx context.Context, a domain.Agent) error
}

type AgentMemberStore interface {
	Save(ctx context.Context, m domain.AgentMember) error
	Members(ctx context.Context, agentID string) ([]domain.AgentMember, error)
	AgentsOf(ctx context.Context, userID string) ([]string, error)
}

type AgentInviteStore interface {
	Save(ctx context.Context, inv domain.AgentInvite) error
	Find(ctx context.Context, id string) (*domain.AgentInvite, error)
	ForAgent(ctx context.Context, agentID string) ([]domain.AgentInvite, error)
}

type AgentParentStore interface {
	Set(ctx context.Context, agentID, parentID string) error // parentID == "" 清除
	ParentOf(ctx context.Context, agentID string) (string, bool, error)
	All(ctx context.Context) (map[string]string, error)
}

type AgentMemoryStateStore interface {
	Find(ctx context.Context, agentID string) (*domain.AgentMemoryState, error)
	Save(ctx context.Context, state domain.AgentMemoryState) (*domain.AgentMemoryState, error)
	Delete(ctx context.Context, agentID string) error
}

type AgentLogStore interface {
	Append(ctx context.Context, entry domain.AgentLogEntry) error
	List(ctx context.Context, agentID, category string, limit int) ([]domain.AgentLogEntry, error)
	Prune(ctx context.Context, before time.Time, keepPerAgent int) error
	DeleteAgent(ctx context.Context, agentID string) error
}

type ChatStore interface { // chat_session + chat_message + agent_event
	CreateSession(ctx context.Context, s domain.ChatSession) (*domain.ChatSession, error)
	FindSession(ctx context.Context, sessionID string) (*domain.ChatSession, error)
	ListSessions(ctx context.Context) ([]domain.ChatSession, error)
	ListSessionsByAgent(ctx context.Context, agentID string) ([]domain.ChatSession, error)
	UpdateSession(ctx context.Context, s domain.ChatSession) error
	AppendMessage(ctx context.Context, m domain.ChatMessage) (*domain.ChatMessage, error)
	ListMessages(ctx context.Context, sessionID string) ([]domain.ChatMessage, error)
	UpdateMessage(ctx context.Context, sessionID, messageID, content string) error
	AppendEvent(ctx context.Context, e domain.AgentEvent) (int64, error) // 返回 BIGSERIAL id
	ListEvents(ctx context.Context, sessionID string) ([]domain.AgentEvent, error)
	ListEventsAfter(ctx context.Context, sessionID string, afterID int64, limit int) ([]domain.AgentEvent, error)
}

type TaskStore interface {
	ListTasks(ctx context.Context) ([]domain.Task, error)
	FindTask(ctx context.Context, id string) (*domain.Task, error)
	Save(ctx context.Context, t domain.Task) (*domain.Task, error)
	Delete(ctx context.Context, id string) error
}

type TaskPhaseStore interface {
	FindPhasesJSON(ctx context.Context, taskID string) ([]byte, bool, error)
	Save(ctx context.Context, taskID string, phasesJSON []byte) error
	Delete(ctx context.Context, taskID string) error
}

type TaskRunStateStore interface {
	Find(ctx context.Context, taskID string) (*domain.TaskRunState, error)
	Save(ctx context.Context, s domain.TaskRunState) (*domain.TaskRunState, error)
	Delete(ctx context.Context, taskID string) error
}

type TaskAgentStore interface {
	Link(ctx context.Context, taskID, agentID, sessionID, messageID string) error
	TasksForAgent(ctx context.Context, agentID string) ([]string, error)
	AgentOf(ctx context.Context, taskID string) (string, bool, error)
	Origin(ctx context.Context, taskID string) (*domain.TaskOrigin, error)
	OriginsForAgent(ctx context.Context, agentID string) ([]domain.TaskOrigin, error)
}

type TaskLinkStore interface {
	Save(ctx context.Context, l domain.TaskLink) error
	Find(ctx context.Context, taskID string) (*domain.TaskLink, error)
	All(ctx context.Context) ([]domain.TaskLink, error)
	Children(ctx context.Context, parentID string) ([]string, error)
	FollowUps(ctx context.Context, taskID string) ([]string, error)
}

type TaskTagStore interface {
	Set(ctx context.Context, taskID string, tags []string) error
	Tags(ctx context.Context, taskID string) ([]string, error)
	All(ctx context.Context) (map[string][]string, error)
}

type TaskModelChoiceStore interface {
	Save(ctx context.Context, c domain.TaskModelChoice) error
	List(ctx context.Context, taskID string) ([]domain.TaskModelChoice, error)
}

type AutomationStore interface { // automation + automation_run
	List(ctx context.Context) ([]domain.Automation, error)
	Find(ctx context.Context, id string) (*domain.Automation, error)
	Save(ctx context.Context, a domain.Automation) (*domain.Automation, error)
	Delete(ctx context.Context, id string) error
	AppendRun(ctx context.Context, r domain.AutomationRun) error
	ListRuns(ctx context.Context, automationID string, limit int) ([]domain.AutomationRun, error)
	FindRunByTask(ctx context.Context, taskID string) (*domain.AutomationRun, error)
}

type SuperAgentStateStore interface {
	List(ctx context.Context) ([]domain.SuperAgentState, error)
	Find(ctx context.Context, owner string) (*domain.SuperAgentState, error)
	Save(ctx context.Context, s domain.SuperAgentState) (*domain.SuperAgentState, error)
}

type SuperAgentWorkStore interface { // superagent_work + superagent_work_event
	List(ctx context.Context, owner string) ([]domain.SuperAgentWork, error)
	Find(ctx context.Context, taskID string) (*domain.SuperAgentWork, error)
	Save(ctx context.Context, w domain.SuperAgentWork) (*domain.SuperAgentWork, error)
	AppendEvent(ctx context.Context, e domain.SuperAgentWorkEvent) error
	Events(ctx context.Context, taskID string) ([]domain.SuperAgentWorkEvent, error)
}

type InboxEventStore interface {
	InsertIfAbsent(ctx context.Context, e domain.InboxEvent) (bool, error)
	Update(ctx context.Context, e domain.InboxEvent) error
	Pending(ctx context.Context, owner string, priority domain.EventPriority, limit int) ([]domain.InboxEvent, error)
	Recent(ctx context.Context, owner string, limit int) ([]domain.InboxEvent, error)
	FindEvent(ctx context.Context, id string) (*domain.InboxEvent, error)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

type InboxTodoStore interface {
	Save(ctx context.Context, t domain.InboxTodo) error
	Find(ctx context.Context, id string) (*domain.InboxTodo, error)
	List(ctx context.Context, owner string) ([]domain.InboxTodo, error)
}

type EventSourceStore interface {
	Find(ctx context.Context, owner, typ string) (*domain.EventSource, error)
	ListEnabled(ctx context.Context, typ string) ([]domain.EventSource, error)
	List(ctx context.Context) ([]domain.EventSource, error)
	Save(ctx context.Context, s domain.EventSource) error
}

type NoticeStore interface {
	Save(ctx context.Context, n domain.Notice) error
	Find(ctx context.Context, id string) (*domain.Notice, error)
	FindByKey(ctx context.Context, owner, key string) (*domain.Notice, error)
	List(ctx context.Context, owner string, limit int) ([]domain.Notice, error)
}

type ProjectStore interface { // project + project_member
	Save(ctx context.Context, p domain.Project) error
	Find(ctx context.Context, id string) (*domain.Project, error)
	List(ctx context.Context) ([]domain.Project, error)
	SaveMember(ctx context.Context, m domain.ProjectMember) error
	Members(ctx context.Context, projectID string) ([]domain.ProjectMember, error)
	ProjectsOf(ctx context.Context, userID string) ([]string, error)
}

type ProjectItemStore interface {
	Assign(ctx context.Context, kind domain.ProjectItemKind, itemID, projectID string) error
	Unassign(ctx context.Context, kind domain.ProjectItemKind, itemID string) error
	ProjectOf(ctx context.Context, kind domain.ProjectItemKind, itemID string) (string, bool, error)
	Items(ctx context.Context, kind domain.ProjectItemKind, projectID string) ([]string, error)
}

type WorkFileStore interface { // work_file + work_file_ref
	Save(ctx context.Context, f domain.WorkFile) error
	Find(ctx context.Context, id string) (*domain.WorkFile, error)
	ListFor(ctx context.Context, owner string, agentIDs []string) ([]domain.WorkFile, error)
	Untagged(ctx context.Context, limit int) ([]domain.WorkFile, error)
	Link(ctx context.Context, fileID, sessionID, messageID string) error
	Messages(ctx context.Context, fileIDs []string) (map[string][]string, error)
	CountByStorage(ctx context.Context) (map[string]int64, error)
}

type FileFolderStore interface { // file_folder + file_placement
	Save(ctx context.Context, f domain.FileFolder) error
	Find(ctx context.Context, id string) (*domain.FileFolder, error)
	List(ctx context.Context, owner string) ([]domain.FileFolder, error)
	Owners(ctx context.Context) ([]string, error)
	Placements(ctx context.Context, owner string) (map[string]string, error)
	Place(ctx context.Context, owner, fileID, folderID, by string) error
}

type SkillStore interface { // skill + skill_binding
	List(ctx context.Context) ([]domain.Skill, error)
	Find(ctx context.Context, id string) (*domain.Skill, error)
	Save(ctx context.Context, s domain.Skill) error
	Binding(ctx context.Context, ownerType, ownerID string) ([]string, bool, error) // bool=false 表示无绑定记录，与空切片区分
	Bind(ctx context.Context, ownerType, ownerID string, skillIDs []string) error
}

type SkillDeletionStore interface {
	Save(ctx context.Context, d domain.SkillDeletion) error
	Find(ctx context.Context, id string) (*domain.SkillDeletion, error)
	ForAgent(ctx context.Context, agentID string) ([]domain.SkillDeletion, error)
}

type ConnectorStore interface {
	ListConnectors(ctx context.Context) ([]domain.Connector, error)
	FindConnector(ctx context.Context, id string) (*domain.Connector, error)
	Save(ctx context.Context, c domain.Connector) (*domain.Connector, error)
	Delete(ctx context.Context, id string) error
}

type ConnectorCredentialStore interface {
	Find(ctx context.Context, connectorID, owner string) (*domain.ConnectorCredential, error)
	List(ctx context.Context, connectorID string) ([]domain.ConnectorCredential, error)
	Save(ctx context.Context, c domain.ConnectorCredential) (*domain.ConnectorCredential, error)
	Delete(ctx context.Context, connectorID, owner string) error
	DeleteAll(ctx context.Context, connectorID string) error
}

type ModelProviderStore interface {
	List(ctx context.Context) ([]domain.ModelProvider, error)
	FindByID(ctx context.Context, id string) (*domain.ModelProvider, error)
	FindByKey(ctx context.Context, providerKey string) (*domain.ModelProvider, error)
	Save(ctx context.Context, p domain.ModelProvider) (*domain.ModelProvider, error)
	Delete(ctx context.Context, id string) error
}

type ProviderCredentialStore interface {
	List(ctx context.Context, providerKey string) ([]domain.ProviderCredential, error)
	Find(ctx context.Context, id string) (*domain.ProviderCredential, error)
	KnownKeyHashes(ctx context.Context, providerKey string) (map[string]struct{}, error)
	Save(ctx context.Context, c domain.ProviderCredential) error
	Delete(ctx context.Context, id string) error // 软删除，保留 key_hash
}

type SystemSettingStore interface {
	All(ctx context.Context) (map[string]string, error)
	Put(ctx context.Context, key, value string) error
}

type UsageStore interface {
	Save(ctx context.Context, r domain.UsageRecord) error
	ForTask(ctx context.Context, taskID string) ([]domain.UsageRecord, error)
	Between(ctx context.Context, userID string, from, to time.Time) ([]domain.UsageRecord, error)
}

type TurnTimingStore interface {
	Save(ctx context.Context, t domain.TurnTiming) error
	ForSessions(ctx context.Context, sessionIDs []string) ([]domain.TurnTiming, error)
}
```

### 5.4 多租户上下文与 `TenantPaths` (`internal/core/tenant`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/tenant/TenantContext.java:L16-L96`、`TenantPaths.java:L10-L29`。

```go
package tenant

var (
	safeCompanyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	orgSlugRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	reservedSlugs = map[string]struct{}{
		"admin": {}, "api": {}, "www": {}, "root": {}, "system": {}, "login": {},
		"mcp": {}, "static": {}, "support": {}, "help": {}, "default": {}, "platform": {},
	}
)

type companyKey struct{}

// WithCompany 返回绑定了合法租户 ID 的派生 Context；非法 ID 立即拒绝。
func WithCompany(ctx context.Context, companyID string) (context.Context, error) {
	if err := ValidateCompanyID(companyID); err != nil {
		return nil, err
	}
	return context.WithValue(ctx, companyKey{}, companyID), nil
}

// Require Fail-Closed：缺失租户立即报错，杜绝跨租户裸查。
func Require(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingTenant
	}
	id, ok := ctx.Value(companyKey{}).(string)
	if !ok || id == "" {
		return "", ErrMissingTenant
	}
	return id, nil
}

// Optional 仅用于登录前 Logo、无租户场景判断（如 SecretCipher v1 主密钥分支）。
func Optional(ctx context.Context) (string, bool) {
	if ctx == nil {
		return "", false
	}
	id, ok := ctx.Value(companyKey{}).(string)
	return id, ok && id != ""
}

// Detach 构造独立生命周期上下文（剥离 HTTP 请求取消/超时信号，避免 SSE 断连取消后端回合），
// 同时完整保留 companyID、traceID 与 userID（等价升级原版 TenantContext.wrap）。
func Detach(ctx context.Context) context.Context {
	out := context.Background()
	if id, ok := Optional(ctx); ok {
		out = context.WithValue(out, companyKey{}, id)
	}
	if tid := obs.TraceIDFrom(ctx); tid != "" {
		out = obs.WithTraceID(out, tid)
	}
	if uid := obs.UserIDFrom(ctx); uid != "" {
		out = obs.WithUserID(out, uid)
	}
	return out
}

// ValidateCompanyID 校验单路径段安全字符（对齐 TenantPaths.SAFE:L13,L24）。
func ValidateCompanyID(id string) error {
	if id == "" || !safeCompanyRe.MatchString(id) || strings.Contains(id, "..") {
		return fmt.Errorf("%w: %q", ErrInvalidCompany, id)
	}
	return nil
}

// ValidateOrgSlug 校验商业化开户组织短名（bootstrap-org CLI 复用）。
func ValidateOrgSlug(raw string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(raw))
	if !orgSlugRe.MatchString(s) {
		return "", apperror.InvalidArg("组织英文短名须为 3–32 位小写字母、数字或连字符，且以字母开头")
	}
	if _, bad := reservedSlugs[s]; bad {
		return "", apperror.InvalidArg("该组织短名为系统保留字，不可使用: " + s)
	}
	return s, nil
}

// Path 安全拼接 <root>/<companyID> 并再次确认相对路径不逃逸。
func Path(ctx context.Context, root string) (string, error) {
	id, err := Require(ctx)
	if err != nil {
		return "", err
	}
	return PathFor(root, id)
}

func PathFor(root, companyID string) (string, error) {
	if err := ValidateCompanyID(companyID); err != nil {
		return "", err
	}
	cleanRoot := filepath.Clean(root)
	joined := filepath.Join(cleanRoot, companyID)
	rel, err := filepath.Rel(cleanRoot, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: path escape for %q", ErrInvalidCompany, companyID)
	}
	return joined, nil
}
```

### 5.5 AES-256-GCM 租户信封加密 (`internal/core/crypto`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/security/SecretCipher.java:L36-L162`、`SecretCipherTest.java:L24-L45`。

**密文协议完全兼容原版**：
- 主密钥（KEK）：32 字节随机数；优先从 `configuredKeyB64`（环境变量 `SUPERAGENT_SECRET_KEY`）Base64 解码，否则读 `keyFile`（默认 `./data/secret.key`）；文件不存在时自动生成 32 字节写入 Base64 并设置 `0600` 权限（原子写入临时文件后 `os.Rename` 防止多进程竞态损坏）。
- 公司 DEK：首次加解密某 `companyID` 时，通过按公司并发锁（double-check `dekCache` + `mu.Lock()`）从 `CompanyKeyStore.FindCompany` 读取 `data_key_cipher`：
  - 若已有 `v1:<sealed>`：用 `masterAEAD` 以 AAD `"company:" + companyID` 解封得 Base64 字符串再解码 32 字节 DEK；
  - 若为空：用 `crypto/rand.Read` 生成 32 字节随机 DEK，将其 **Base64 编码字符串**以 `masterAEAD` + AAD `"company:" + companyID` 加密为 `v1:<sealed>` 写回 `company.data_key_cipher`，并缓存 `cipher.AEAD`。
- 租户密文格式：`v2:<companyID>:<base64(nonce12 || ct || tag16)>`，以租户 DEK 的 `cipher.AEAD` 加密且 **AAD = `[]byte(companyID)`**；
- 平台级密文格式（无租户上下文或 `companies == nil`）：`v1:<base64(nonce12 || ct || tag16)>`，以 `masterAEAD` 加密（AAD 为 `nil`）。

```go
package crypto

const (
	prefixMaster  = "v1:"
	prefixCompany = "v2:"
	nonceSize     = 12 // 96-bit GCM nonce
	keySize       = 32 // AES-256
)

var (
	ErrCrossTenantSecret = errors.New("secret belongs to another company")
	ErrInvalidCiphertext = errors.New("secret is not in a recognised format")
	ErrDecryptFailed     = errors.New("cannot decrypt secret (key mismatch or tampered)")
)

func (c *SecretCipher) Encrypt(ctx context.Context, plain string) (string, error) {
	companyID, hasCompany := tenant.Optional(ctx)
	if c.companies == nil || !hasCompany {
		sealed, err := sealAEAD(c.masterAEAD, plain, nil)
		if err != nil {
			return "", err
		}
		return prefixMaster + sealed, nil
	}
	dek, err := c.dataKey(ctx, companyID)
	if err != nil {
		return "", err
	}
	sealed, err := sealAEAD(dek, plain, []byte(companyID))
	if err != nil {
		return "", err
	}
	return prefixCompany + companyID + ":" + sealed, nil
}

func (c *SecretCipher) Decrypt(ctx context.Context, stored string) (string, error) {
	if strings.HasPrefix(stored, prefixCompany) {
		rest := stored[len(prefixCompany):]
		sep := strings.IndexByte(rest, ':')
		if sep <= 0 {
			return "", ErrCrossTenantSecret
		}
		cipherCompany := rest[:sep]
		curCompany, hasCompany := tenant.Optional(ctx)
		if c.companies == nil || !hasCompany || cipherCompany != curCompany {
			return "", ErrCrossTenantSecret
		}
		dek, err := c.dataKey(ctx, cipherCompany)
		if err != nil {
			return "", err
		}
		return openAEAD(dek, rest[sep+1:], []byte(cipherCompany))
	}
	if !strings.HasPrefix(stored, prefixMaster) {
		return "", ErrInvalidCiphertext
	}
	return openAEAD(c.masterAEAD, stored[len(prefixMaster):], nil)
}

func (c *SecretCipher) dataKey(ctx context.Context, companyID string) (cipher.AEAD, error) {
	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}
	comp, err := c.companies.FindCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if comp == nil {
		return nil, fmt.Errorf("unknown company: %s", companyID)
	}
	var rawDEK []byte
	aad := []byte("company:" + companyID)
	if comp.DataKeyCipher != "" {
		if !strings.HasPrefix(comp.DataKeyCipher, prefixMaster) {
			return nil, ErrInvalidCiphertext
		}
		b64, err := openAEAD(c.masterAEAD, comp.DataKeyCipher[len(prefixMaster):], aad)
		if err != nil {
			return nil, err
		}
		rawDEK, err = base64.StdEncoding.DecodeString(b64)
		if err != nil || len(rawDEK) != keySize {
			return nil, ErrDecryptFailed
		}
	} else {
		rawDEK = make([]byte, keySize)
		if _, err := rand.Read(rawDEK); err != nil {
			return nil, err
		}
		sealed, err := sealAEAD(c.masterAEAD, base64.StdEncoding.EncodeToString(rawDEK), aad)
		if err != nil {
			return nil, err
		}
		comp.DataKeyCipher = prefixMaster + sealed
		if _, err := c.companies.SaveCompany(ctx, *comp); err != nil {
			return nil, err
		}
	}
	aead, err := newGCM(rawDEK)
	if err != nil {
		return nil, err
	}
	c.dekCache.Store(companyID, aead)
	return aead, nil
}

func sealAEAD(aead cipher.AEAD, plain string, aad []byte) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	// out = nonce(12) || ciphertext || tag(16)
	out := aead.Seal(nonce, nonce, []byte(plain), aad)
	return base64.StdEncoding.EncodeToString(out), nil
}

func openAEAD(aead cipher.AEAD, sealedB64 string, aad []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealedB64)
	if err != nil || len(raw) < nonceSize+aead.Overhead() {
		return "", ErrDecryptFailed
	}
	plain, err := aead.Open(nil, raw[:nonceSize], raw[nonceSize:], aad)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plain), nil
}
```

### 5.6 抗 DNS Rebinding 出口守卫 (`internal/core/netguard`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/net/EgressGuard.java:L24-L146`、`EgressDeniedException.java:L6-L11`、`EgressGuardTest.java:L10-L36`。
> **V1 安全升级**：Java 原版仅在发起请求前做一次 `InetAddress.getAllByName` 预检，在注释（`L21-L22`）中明确承认存在短 TTL DNS Rebinding 竞态窗口。Go 版通过自定义 `DialContext` **解析全部 IP -> 校验全部 IP -> 直接对已校验通过的字面量 IP 发起 TCP `connect()`**（同时 Go `http.Transport` 自动将 TLS `ServerName` 设为原始 `Host`），并挂载 6 跳 `CheckRedirect` 逐跳重检，从根源消除 DNS Rebinding！

```go
package netguard

var allowedPorts = map[int]struct{}{0: {}, 80: {}, 443: {}, 8080: {}, 8443: {}}

// 1. ToURI：对空白、非 ASCII（如中文路径）及不安全字符逐字节 UTF-8 百分号编码（对齐 EgressGuard.java:L96-L113）
func ToURI(raw string) (*url.URL, error) {
	s := strings.TrimSpace(raw)
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		v := s[i]
		if v > 0x7e || v <= 0x20 || strings.IndexByte(`"<>\\^`+"`"+`{|} `, v) >= 0 {
			fmt.Fprintf(&b, "%%%02X", v)
		} else {
			b.WriteByte(v)
		}
	}
	u, err := url.Parse(b.String())
	if err != nil {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	return u, nil
}

// 2. IsPublic：统一 Unmap IPv4-mapped IPv6（如 ::ffff:127.0.0.1），拦截一切内网/回环/元数据/CGNAT/ULA 地址
// 对齐 EgressGuard.java:L76-L93 并补强 IPv4-mapped IPv6、RFC6598、RFC2544、IPv6 文档/保留段
func IsPublic(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap() // 关键修复：防止 ::ffff:169.254.169.254 绕过 IPv4 检查
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsPrivate() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	if addr.Is6() {
		b := addr.As16()
		if (b[0] & 0xfe) == 0xfc { // fc00::/7 ULA
			return false
		}
		return true
	}
	b := addr.As4()
	x, y := b[0], b[1]
	if x == 0 || x == 10 || x == 127 || x >= 224 ||
		(x == 100 && y >= 64 && y <= 127) || // 100.64.0.0/10 CGNAT
		(x == 169 && y == 254) ||            // 169.254.0.0/16 Link-Local / 云元数据
		(x == 172 && y >= 16 && y <= 31) ||  // 172.16.0.0/12
		(x == 192 && y == 168) ||            // 192.168.0.0/16
		(x == 198 && (y == 18 || y == 19)) { // 198.18.0.0/15 Benchmarking
		return false
	}
	return true
}

// 3. CheckURL：校验 scheme、host、端口白名单、管理员 host 白名单、本地域名后缀黑名单及全部 DNS 解析记录
func (g *EgressGuard) CheckURL(ctx context.Context, rawURL string) (*url.URL, error) {
	u, err := ToURI(rawURL)
	if err != nil {
		return nil, err
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, apperror.EgressDenied("只能读 http / https 链接")
	}
	host := u.Hostname()
	if host == "" {
		return nil, apperror.EgressDenied("链接里没有域名")
	}
	portNum := 0
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, apperror.EgressDenied("不读这个端口上的地址")
		}
		portNum = n
	}
	if _, ok := allowedPorts[portNum]; !ok {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}
	h := strings.ToLower(host)
	if g.allowedHost(h) {
		return u, nil
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
	}
	if _, err := g.resolveAndValidate(ctx, h); err != nil {
		return nil, err
	}
	return u, nil
}

// 4. DialContext：抗 DNS Rebinding 终极防线 —— 解析后校验全部 IP，并直接对合法 IP 字面量发起拨号！
func (g *EgressGuard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}
	if _, ok := allowedPorts[portNum]; !ok {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}
	h := strings.ToLower(host)
	dialer := &net.Dialer{Timeout: g.dialTimeout, KeepAlive: 30 * time.Second}
	if g.allowedHost(h) {
		return dialer.DialContext(ctx, network, addr)
	}
	if h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		strings.HasSuffix(h, ".local") || strings.HasSuffix(h, ".internal") {
		return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
	}
	ips, err := g.resolveAndValidate(ctx, h)
	if err != nil {
		return nil, err
	}
	// 逐个尝试已通过校验的字面量 IP，杜绝二次 DNS 查询导致的 Rebinding
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// 5. resolveAndValidate：若 host 本身是 IP 字面量直接校验；否则解析全部记录，任一非法即整组拒绝！
func (g *EgressGuard) resolveAndValidate(ctx context.Context, h string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(h); err == nil {
		if !IsPublic(ip) && !g.allowedPrefix(ip) {
			return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
		}
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := g.resolver.LookupNetIP(ctx, "ip", h)
	if err != nil || len(ips) == 0 {
		return nil, apperror.EgressDenied("找不到这个域名")
	}
	out := make([]netip.Addr, 0, len(ips))
	for _, ip := range ips {
		if !IsPublic(ip) && !g.allowedPrefix(ip) {
			return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
		}
		out = append(out, ip.Unmap())
	}
	return out, nil
}

// 6. NewHTTPClient：绑定防 Rebinding Transport + 最多 6 跳 CheckRedirect（每跳重检 URL）+ 可选企业代理与自定义 CA
func (g *EgressGuard) NewHTTPClient(timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DialContext:           g.DialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: timeout,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: g.caBundle},
	}
	if g.proxyFunc != nil {
		tr.Proxy = g.proxyFunc
	}
	return &http.Client{
		Transport: tr,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= g.maxHops { // 默认 6 跳
				return apperror.EgressDenied("重定向次数过多")
			}
			_, err := g.CheckURL(req.Context(), req.URL.String())
			return err
		},
	}
}
```

### 5.7 5 大二期预留 SPI 与跨模块 SPI 契约 (`internal/core/spi`)

> **Java 源码对照**：`AgentRunner.java`、`ProjectAccess.java`、`TaskDoers.java`、`ModelRouter.java`、`TaskModelSelector.java`、`InboxProcessor.java`、`LinkReader.java`、`UsageRecorder.java`、`FileStore.java`、`FileStoreProvider.java`，加上 V1 新增预留的 5 大二期接口。

```go
package spi

// === 1. [二期预留] 进程启动与沙箱隔离接口（V1: LocalLauncher; V2: Docker/gVisor/K8s/Worker）===
type TempFileSpec struct {
	Prefix  string
	Suffix  string
	Content []byte
	Mode    os.FileMode // 强制 0600
}

type ProcessSpec struct {
	CompanyID    string
	OwnerUserID  string
	Command      string
	Args         []string
	WorkDir      string
	Env          map[string]string
	StripEnvKeys []string
	MountFiles   []TempFileSpec
}

type ProcessHandle interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser
	Wait() (exitCode int, err error)
	Terminate(grace time.Duration) error
	Kill() error
	Close() error
}

type ProcessLauncher interface {
	Mode() string // "local" | "docker" | "gvisor" | "kata" | "worker"
	Start(ctx context.Context, spec ProcessSpec) (ProcessHandle, error)
}

// === 2. [二期预留] 集中式模型网关接口（V1: DirectCredentialGateway; V2: ProxyModelGateway）===
type ResolvedEndpoint struct {
	CredentialID string
	BaseURL      string
	APIKey       string
	Bearer       bool
	ProxyMode    bool
}

type ModelGateway interface {
	Resolve(ctx context.Context, providerKey, excludeCredID string) (*ResolvedEndpoint, error)
	ReportEndpointFailure(ctx context.Context, providerKey string, ep *ResolvedEndpoint, reason string) (*ResolvedEndpoint, error)
}

// === 3. [二期预留] 可插拔认证接口（V1: LocalAuthenticator; V2: OIDC/LDAP）===
type AuthPrincipal struct {
	UserID             string
	CompanyID          string
	Username           string
	DisplayName        string
	Roles              []string
	MustChangePassword bool
}

type Credentials struct {
	Kind     string // "local" | "oidc" | "ldap"
	Org      string
	Username string
	Password string
	Token    string
}

type Authenticator interface {
	Kind() string
	Authenticate(ctx context.Context, cred Credentials) (*AuthPrincipal, error)
}

// === 4. [二期预留] 企业 IM 连接器接口（V1: Feishu/DingTalk; V2: WeCom）===
type IMConnector interface {
	Type() string // "feishu" | "dingtalk" | "wecom"
	Start(ctx context.Context) error
	ReconnectCompany(ctx context.Context, companyID string) error
	NotifyOwner(ctx context.Context, src domain.EventSource, text string) error
	Close() error
}

// === 5. [二期预留] 员工离职资产交接钩子 ===
type OwnerTransferSummary struct {
	AgentsTransferred      int
	TasksTransferred       int
	AutomationsTransferred int
}

type OwnerTransferHook interface {
	TransferOwnership(ctx context.Context, fromUserID, toUserID string) (*OwnerTransferSummary, error)
}

// === 6-12. 跨模块解耦核心 SPI ===
type EventSink func(ev domain.AgentEvent)

type RunnerCapabilities struct {
	Type            domain.AgentType
	SupportImages   bool
	SupportStdioMCP bool
	SupportWarmPool bool
	Maturity        string // "ga" | "beta" | "experimental"
}

type AgentRunner interface {
	Type() domain.AgentType
	Capabilities() RunnerCapabilities
	Run(ctx context.Context, req domain.AgentRunRequest, sink EventSink) error
	RunChat(ctx context.Context, req domain.AgentRunRequest, sink EventSink) error
}

type StreamParser interface {
	ParseLine(line []byte) ([]domain.AgentEvent, error)
	Finish() []domain.AgentEvent
	Usage(fallbackModel string) (tokens []domain.TokenUsage, cliCostUSD *decimal.Decimal)
}

type ProjectAccess interface {
	AgentsThroughProjects(ctx context.Context, userID string) ([]string, error)
	SharesProject(ctx context.Context, agentID, userID string) (bool, error)
	ProjectOfAgent(ctx context.Context, agentID string) (string, bool, error)
	Archived(ctx context.Context, agentID string) (bool, error)
	VisibleThroughProject(ctx context.Context, kind domain.ProjectItemKind, itemID, userID string) (bool, error)
	ItemsInProject(ctx context.Context, projectID string, kind domain.ProjectItemKind, userID string) ([]string, error)
	RecordTask(ctx context.Context, taskID, agentID string) error
	RecordFile(ctx context.Context, fileID, agentID, taskID string) error
}

type TaskDoer struct {
	Name      string
	Character string
	Memory    string
}

type TaskDoers interface {
	Doer(ctx context.Context, agentID string) (*TaskDoer, error)
	Name(ctx context.Context, agentID string) (string, bool, error)
}

type ModelChoice struct {
	Model string
	CLI   string
}

type ModelRouter interface {
	Cheapest(ctx context.Context) (*ModelChoice, error)
	ChatModels(ctx context.Context) ([]string, error)
	ReportFailure(ctx context.Context, model, errMsg string)
}

type TaskModelSelector interface {
	Enabled(ctx context.Context) bool
	Choose(ctx context.Context, task domain.Task) (*domain.TaskModelChoice, error)
	Escalate(ctx context.Context, task domain.Task, failure string) (*domain.TaskModelChoice, error)
	Strongest(ctx context.Context, task domain.Task) (*domain.TaskModelChoice, error)
}

type InboxProcessResult struct{ NotifyText string }

type InboxProcessor interface {
	Process(ctx context.Context, src domain.EventSource, window domain.EventPriority, events []domain.InboxEvent) (*InboxProcessResult, error)
}

type LinkContent struct {
	URL      string
	Title    string
	Text     string
	Via      string // "public" | "authorized"
	Complete bool
}

type LinkReader interface {
	Read(ctx context.Context, rawURL, userID string) (*LinkContent, error)
}

type UsageRecorder interface {
	Record(ctx context.Context, uctx domain.UsageContext, tokensJSON string) (*decimal.Decimal, error)
}

// TapUsage 拦截 TOKENS 事件计费并透传其余事件（对齐 UsageRecorder.java:L18-L28）
func TapUsage(ctx context.Context, rec UsageRecorder, uctx *domain.UsageContext, next EventSink) EventSink {
	return func(ev domain.AgentEvent) {
		if ev.Type == domain.EventTokens {
			if rec != nil && uctx != nil {
				_, _ = rec.Record(ctx, *uctx, ev.Payload)
			}
			return
		}
		next(ev)
	}
}

type FileStore interface {
	Put(ctx context.Context, id, sourcePath, contentType string) error
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	Delete(ctx context.Context, id string) error
	LocalPath(ctx context.Context, id string) (string, bool)
}

type FileStoreField struct {
	Key         string
	Label       string
	Secret      bool
	Required    bool
	Placeholder string
	Locates     bool
}

type FileStoreProvider interface {
	Type() string
	Label() string
	Fields() []FileStoreField
	Open(config map[string]string) (FileStore, error)
}
```

### 5.8 领域事件总线 (`internal/core/eventbus`) 与可观测性 (`internal/core/obs`)

> **Java 源码对照**：`CompanyStarted.java`、`IntegrationChanged.java`、`NoticeEvents.java:L7-L48`、`InboxNotifyRequested.java`、`TraceIdFilter.java:L29-L47`、`LogContext.java:L13-L37`、`MetricNames.java:L11-L21`。

- **9 类冻结领域事件**：`CompanyStarted{CompanyID}`、`IntegrationChanged{Key}`、`TaskStatusChanged{TaskID, Owner, Title, Source, Status, Error, AgentID, Result}`、`PlanChanged{Owner, PlanID, Summary, Open}`、`InboxTodoChanged{TodoID, Owner, Title, Detail, Requester, Due, Source, Status, TaskID}`、`AgentShared{AgentID, AgentName, Username, ByName}`、`DefaultModelChanged{DefaultModel, InUse, Down, Reason}`、`NoticeIgnored{Owner, LinkType, LinkID}`、`InboxNotifyRequested{Owner, Text}`。
- **事件总线分发规则**：
  - `Publish(ctx, ev)`：读锁拷贝订阅列表后**锁外**依次执行，每个订阅者包一层 `defer recover()`，单个订阅者 panic 仅记 `slog.ErrorContext` 而不阻断后续订阅者；
  - `PublishAsync(ctx, ev)`：先调用 `detached := tenant.Detach(ctx)`，再为每个订阅者启 goroutine（或在受控 worker 池中）执行并保留 `company_id`/`trace_id`/`user_id`。
- **可观测性 (`internal/core/obs`)**：
  - `SanitizeTraceID(raw string) string`：校验 `^[A-Za-z0-9._-]{1,64}$`，合法则原样采纳，否则生成 32 位小写 hex UUID（防日志注入）；
  - `ContextHandler` 包装 `slog.Handler`，自动从 `ctx` 注入 `trace_id`、`company_id`（调 `tenant.Optional`）、`user_id`；
  - 6 项 Prometheus 指标通过 `prometheus.Registerer` 显式注册（禁止污染全局默认 Registry，便于单元测试）：
    1. `superagent_chat_turns_total` (`CounterVec`: `company_id`, `agent_type`, `status`)
    2. `superagent_task_runs_total` (`CounterVec`: `company_id`, `flow`, `status`)
    3. `superagent_task_queue_depth` (`GaugeVec`: `company_id`)
    4. `superagent_tokens_total` (`CounterVec`: `company_id`, `source`, `model`, `kind`)
    5. `superagent_cli_warm_pool_size` (`Gauge`)
    6. `superagent_im_connected` (`GaugeVec`: `company_id`, `type`)

### 5.9 AST 包依赖守卫 (`internal/arch_test.go`)

在 `internal/arch_test.go` 使用标准库 `go/parser` + `go/token` 遍历 `synapse-server/internal/...` 全部非测试 `.go` 文件的 `import` 路径，强制以下 5 条编译级架构红线：
1. **单向层级**：映射包层级 `core=0`, `storage=1`, `runtime=1`, `platform/files/project/skills=2`, `agent/task/cost/docs=3`, `event/orchestrator=4`, `web/ui=5`，禁止任何包 import **层级严格大于自身**的内部包；
2. **L0 纯净**：`internal/core/...` 严禁 import 任何其他 `internal/*` 包、严禁 import `github.com/gin-gonic/gin`、`github.com/jackc/pgx`、`database/sql`、`os/exec`；
3. **同层零互引**：明确禁止 `internal/agent` ↔ `internal/task`、`internal/event` ↔ `internal/orchestrator`、`internal/storage` ↔ `internal/runtime` 互相 import；
4. **存储隔离**：除 `internal/storage/postgres` 与 `cmd/synapse-server` 外，任何包严禁 import `github.com/jackc/pgx/v5` 或 `github.com/pressly/goose/v3`；
5. **进程隔离**：除 `internal/runtime/launcher` 与 `internal/platform/provider`（版本探测/私有 Node 托管）外，任何业务包严禁 import `os/exec`（必须走 `spi.ProcessLauncher`）。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**: 初始化 `synapse-server/go.mod` 核心基础依赖（`github.com/shopspring/decimal`、`github.com/robfig/cron/v3`、`github.com/prometheus/client_golang`），建立 `internal/core/*` 目录骨架。
- [ ] **T2**: 编写 `internal/core/apperror/errors_test.go`（红灯）→ 实现 `internal/core/apperror/errors.go`（全部错误码、`errors.As`、HTTP 状态码与 `UnreadableMaterialsError` 格式化）。
- [ ] **T3**: 编写 `internal/core/tenant/tenant_test.go`（红灯）→ 实现 `context.go`、`paths.go`、`slug.go`（Fail-Closed `Require`、`Detach`、`ValidateOrgSlug` 12 保留字、`Path` 防穿越）。
- [ ] **T4**: 编写 `internal/core/obs/obs_test.go`（红灯）→ 实现 `trace.go`、`logger.go`、`metrics.go`（`SanitizeTraceID` 防注入、`slog` 上下文注入、6 项 Prometheus 指标）。
- [ ] **T5**: 编写 `internal/core/domain/*_test.go`（红灯）→ 实现全量领域实体、枚举与 5 个纯领域工具（`TeamPaths`、`LinkUrls`、`WorkFile.CleanTags`、`TurnTimer`、`AutomationSchedule`）。
- [ ] **T6**: 编写 `internal/core/crypto/cipher_test.go`（红灯）→ 实现 `SecretCipher`（`v1:`/`v2:<companyID>:` 加解密、自动生成 `0600` 密钥文件、DEK 懒创建与并发缓存、跨租户与篡改前缀拒止）。
- [ ] **T7**: 编写 `internal/core/netguard/egress_test.go`（红灯，含 Mock `Resolver` 模拟 DNS Rebinding 与本地 `httptest.Server` 重定向跳转测试）→ 实现 `EgressGuard`（`ToURI`、`IsPublic` 含 `::ffff:` Unmap、`CheckURL`、`DialContext`、`NewHTTPClient`）。
- [ ] **T8**: 编写 `internal/core/eventbus/bus_test.go`（红灯）→ 实现 `InMemoryBus` 与 9 类事件定义（同步顺序分发、panic 隔离、异步 `tenant.Detach` 透传）。
- [ ] **T9**: 落地 `internal/core/store/*.go`（`TxManager` + 38 个接口）、`internal/core/spi/*.go`（12 个 SPI 含 5 大二期预留接口）及 `internal/core/api/{envelope,paths}.go`（含 `PageQuery.Normalized` 边界单测）。
- [ ] **T10**: 落地 `internal/arch_test.go` 并验证 `go build ./... && go test -race ./internal/...` 全绿。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 实施看板：将 `SA-01` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md`：若实施中新增或调整导出符号签名，反向同步校验保持一致；
- [ ] `synapse-server/README.md`（若无则在 `SA-01` 首次创建）：补充 `internal/core/*` 包职责地图与 `arch_test.go` 守卫说明。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | Fail-Closed 租户上下文与 `Detach` 继承 | `internal/core/tenant/tenant_test.go` | 空 `ctx` 调 `Require` 必返 `ErrMissingTenant`；`WithCompany` 拒 `../evil` 与空串；父 `ctx` cancel 后 `Detach(ctx)` 不取消且仍保留 `companyID`/`traceID`/`userID` |
| 2 | `TenantPaths` 路径穿越与 `ValidateOrgSlug` | `internal/core/tenant/tenant_test.go` | 12 保留字、大写、2 字符、33 字符、`a..b` 全部拒绝；合法 slug 通过且小写化；`PathFor("/data", "acme")` 返回 `/data/acme` |
| 3 | `SecretCipher` 租户信封隔离与重放篡改拒止 | `internal/core/crypto/cipher_test.go` | `acme` 密文以 `v2:acme:` 开头且 `company.data_key_cipher` 以 `v1:` 开头；`beta` 上下文解密报错 `ErrCrossTenantSecret`；将前缀改写为 `v2:beta:` 后仍因 AAD/DEK 不匹配解密失败；重启重载 `secret.key`（权限 `0600`）可正常解密；无租户上下文走 `v1:` |
| 4 | `SecretCipher` 并发首次生成 DEK 幂等性 | `internal/core/crypto/cipher_test.go` | 32 个 goroutine 并发首次对同一公司 `Encrypt`，仅生成并持久化 1 次 DEK，所有密文均可互相解密，`-race` 零告警 |
| 5 | `EgressGuard` 内网/元数据/IPv6/端口/协议拦截 | `internal/core/netguard/egress_test.go` | 拦截 `127.0.0.1`、`10.1.2.3`、`172.20.0.1`、`192.168.1.1`、`169.254.169.254`、`[::1]`、`[fd00::1]`、`[::ffff:127.0.0.1]`、`100.64.0.1`、`0.0.0.0`、`localhost`、`printer.local`、`svc.internal`、`file://`、`:22`；允许 `10.8.0.0/16` 与 `.corp.local` 管理员白名单 |
| 6 | `EgressGuard` 抗 DNS Rebinding 与 6 跳重定向 | `internal/core/netguard/egress_test.go` | Mock `Resolver` 首次返回公网 IP、第二次返回 `169.254.169.254`，`DialContext` 仅使用校验通过的 IP 或拦截重绑定；7 跳重定向拒止；重定向指向内网 IP 立即拦截；中文与空格 URL 经 `ToURI` 正确百分号编码 |
| 7 | `TurnTimer` 7 阶段耗时恒等式与工具聚合 | `internal/core/domain/turn_timer_test.go` | 对齐 `TurnTimerTest.java`：两次尝试（首轮失败 700ms 计入 `retry`）、重复工具 `Bash` 合并计数与耗时，`prep+retry+startup+model+tools+output+save == totalMs`；0 尝试全部归入 `prepMs` |
| 8 | `TeamPaths` / `LinkUrls` / `CleanTags` / `AutomationSchedule` | `internal/core/domain/helpers_test.go` | 团队路径容忍 `a//b`、拒绝 `>8` 层和 `.hidden`；`LinkUrls` 保留 Wikipedia 平衡括号 `)` 且去重；`CleanTags` 中文截断按 rune 不乱码、上限 5 个；`AutomationSchedule` 5 字段 Cron 跨 DST（如 `America/New_York`）与 `Asia/Shanghai` 推算精确 |
| 9 | `eventbus` 同步 panic 隔离与异步租户透传 | `internal/core/eventbus/bus_test.go` | 订阅者 1 panic 时订阅者 2 正常执行；`PublishAsync` 在原 HTTP `ctx` 取消后仍携带 `companyID` 和 `traceID` 完成执行 |
| 10 | `apperror` 错误映射与 `obs` 指标/日志注入 | `internal/core/apperror/errors_test.go`、`internal/core/obs/obs_test.go` | 全部 10 种错误码 HTTP 状态一致；含换行符的 `X-Request-Id` 被替换；`slog` 输出自动包含 `trace_id`/`company_id`/`user_id`；6 项 Prometheus 指标无重复注册冲突 |
| 11 | AST 静态包依赖守卫 | `internal/arch_test.go` | 校验真实工程全量通过；针对临时构造的违规 import 样例测试规则能够正确检出并报错 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/core/... ./internal` 全绿
- [ ] §2 交付物表 12 项逐一存在且路径与包名精确匹配
- [ ] §8 测试清单 11 项全部落地，核心包单元测试行覆盖率 ≥ 85%（`go test ./internal/core/... -cover`）
- [ ] `internal/arch_test.go` 通过：`internal/core/*` 零引入 `pgx`、`gin`、`database/sql`、`os/exec` 或任何上层包
- [ ] 全量 38 个 `Store` 接口与 5 大二期预留 SPI（`ProcessLauncher`/`ModelGateway`/`Authenticator`/`IMConnector`/`OwnerTransferHook`）签名与 §4.1/§5.3/§5.7 逐字一致
- [ ] 核对 `internal/core/domain` 中零出现废弃字段（`Character`/`Tier` on Agent·Task·Automation/`TrackedJSON`/`SecretCipher` on EventSource/`MaterialPrompt`）
- [ ] `SecretCipher` 跨租户解密拒止 + `EgressGuard` 抗 DNS Rebinding + IPv4-mapped IPv6 阻断专项断言全绿

---

## 10. 可提交节点

- 建议 commit 1（领域、错误、多租户、可观测性）：`feat(core): add L0 domain models, fail-closed tenant context, apperror and obs primitives (spec 01, SA-01)`
- 建议 commit 2（安全底座与总线）：`feat(core): add AES-256-GCM envelope SecretCipher, DNS-rebinding-safe EgressGuard and eventbus (spec 01, SA-01)`
- 建议 commit 3（Store/SPI 冻结契约与架构守卫）：`feat(core): freeze 38 Store interfaces, Phase-2 SPIs, API envelopes and AST arch_test guard (spec 01, SA-01)`
