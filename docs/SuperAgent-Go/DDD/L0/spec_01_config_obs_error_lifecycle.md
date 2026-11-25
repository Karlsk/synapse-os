# spec 01 | L0 基础底座 — 配置管理、可观测性、领域错误体系与生命周期治理

- **状态**：`冻结待实施`
- **篇号顺序**：01（项目首篇，全仓 L0–L6 零业务前置依赖）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-01`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1, §5
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
  - 归档母本：[spec_01_core_tenant_crypto_egress.md](../_epic/L1/spec_01_core_tenant_crypto_egress.md)（错误码与可观测性部分）、[spec_14_server_bootstrap_lifecycle_docker_delivery.md](../_epic/L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md)（配置、代理/CA 与优雅停机部分）
- **前置依赖**：无（项目首篇，仅依赖 `synapse-server/go.mod`：`module github.com/Karlsk/synapse-os`, `go 1.26.1`）
- **下游消费者**：`SA-02`–`SA-42`（全仓所有存储、HTTP 客户端、核心原语、业务服务、Web 网关与 Server 入口均直接复用本篇冻结的 `apperror`、`config`、`obs`、`lifecycle`）

---

## 1. 背景与范围

本篇构建 `synapse-server` 的 **L0 最底层运行时基础设施（4 个原子包）**，为后续 `SA-02`–`SA-42` 提供统一的错误语义、企业级配置加载与启动前 fail-fast 校验、上下文感知且自动脱敏的可观测性底座，以及带 panic 隔离与 30 秒分阶段逆序优雅停机的后台协程治理原语。

对照 Java 原版（`superagent-core`、`superagent-web`、`superagent-server`），本篇解决以下核心工程问题：
1. **统一领域错误语义 (`internal/core/apperror`)**：等价重构 Java `GlobalExceptionHandler.java` 与 `com.superagent.core.error.*` 异常层次，使任何层抛出的领域错误都能通过 `errors.As` 无损映射为兼容 `/api/v1` 前端的 HTTP 状态码与 `{"code","message","data":null}` JSON 信封；并扩展 `rate_limited`（429）与 `service_unavailable`（503）供 `SA-03`/`SA-04` 使用。
2. **企业联网私有化配置体系 (`internal/infra/config`)**：替代 Spring Boot `application.yml`，提供 `gopkg.in/yaml.v3` 强类型 YAML 解析（启用 `KnownFields(true)` 启动即暴露拼写错误）+ `SYNAPSE_*` / 原版 `SERVER_PORT` / `SUPERAGENT_*` / `SECRET_KEY` 环境变量覆盖 + 企业出站代理（`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` 且强制保底 `127.0.0.1,localhost` 防止 `/mcp` 自检走代理）与自定义私有 CA（同时注入 Go `*tls.Config` 与 CLI 子进程 `NODE_EXTRA_CA_CERTS`）+ 启动前 `Validate()` 全量 fail-fast 校验。
3. **上下文透传与自动脱敏可观测性 (`internal/infra/obs`)**：补齐 Java 原版仅有 `MetricNames.java` 空壳而缺少业务 Prometheus 指标的缺口；基于标准库 `log/slog` 构造 `ContextHandler`（自动附带 `request_id`/`company_id`/`user_id`）+ `RedactingHandler`（自动脱敏密钥/Token/DSN 密码段，杜绝日志泄密）+ 独立 Prometheus Registry（带公共标签 `application="synapse-server"`，零污染全局默认 Registry）。
4. **后台协程与 30 秒优雅停机治理 (`internal/infra/lifecycle`)**：替代 Java 散落的 `SmartLifecycle` / `@PreDestroy` / 裸 `ExecutorService`，提供 `BackgroundGroup`（统一协程生命周期、自动 `panic recover` 记堆栈防进程崩溃）与 `ShutdownManager`（分阶段 30s 总预算严格逆序关闭编排）。

**本篇边界（严格控制 1.5 人天，零越界）**：
- 本篇**严禁引入任何 Redis 配置项或缓存中间件抽象**（原版无 Redis，系统单库 PostgreSQL 17 + 进程内内存缓存）。
- 本篇不实现 PostgreSQL 连接池与迁移（见 `SA-02`）、不实现出站 HTTP 客户端与 `EgressGuard`（见 `SA-03`）、不实现多租户 Fail-Closed `TenantContext` 与信封加密（见 `SA-04`）、不实现具体 CLI 入口 `cmd/synapse-server/main.go` 与 Actuator HTTP 路由（见 `SA-41`/`SA-42`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | 统一领域错误码、`AppError`、`EgressDenied`、`UnreadableMaterialsError` 与 `FromError` | `internal/core/apperror/errors.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/error/{BusyException,ConflictException,ForbiddenException,NotFoundException}.java`、`superagent-core/src/main/java/com/superagent/core/link/{UnreadableLinkException,UnreadableMaterialsException}.java`、`superagent-web/src/main/java/com/superagent/web/GlobalExceptionHandler.java:L33-L106` |
| 2 | 强类型配置树、默认值、YAML+Env 加载器与 fail-fast `Validate()` | `internal/infra/config/config.go`、`load.go`、`validate.go` | 新增 | `superagent-server/src/main/resources/application.yml:L1-L65` |
| 3 | 企业代理解析（保底本地 `NO_PROXY`）、私有 CA 证书池与子进程环境变量构建 | `internal/infra/config/network.go` | 新增 | 商业私有化 V1 新增增强 |
| 4 | 完整注释版商业配置模板 | `configs/config.example.yaml` | 新增 | `superagent-server/src/main/resources/application.yml:L1-L65` |
| 5 | `slog` 上下文元数据透传（`request_id`/`company_id`/`user_id`）、`SanitizeRequestID` 防注入与敏感字段自动脱敏器 | `internal/infra/obs/context.go`、`logger.go`、`redact.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/logging/{LogContext,LogFormat}.java`、`superagent-web/src/main/java/com/superagent/web/TraceIdFilter.java:L29-L47` |
| 6 | 独立 Prometheus 指标注册表与核心业务/HTTP 指标集 | `internal/infra/obs/metrics.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/metrics/{MetricNames,MetricsConfiguration,CommonTagsMeterRegistryPostProcessor}.java` |
| 7 | `BackgroundGroup`（协程托管 + `panic recover`）与 `ShutdownManager`（30s 分阶段逆序优雅停机 + 信号监听） | `internal/infra/lifecycle/group.go`、`shutdown.go` | 新增 | `superagent-server/src/main/resources/application.yml:L14` 及各模块 `SmartLifecycle`/`@PreDestroy` 收口 |
| 8 | 4 个包的完整同包单元测试套件 | `internal/core/apperror/errors_test.go`、`internal/infra/config/*_test.go`、`internal/infra/obs/*_test.go`、`internal/infra/lifecycle/*_test.go` | 新增 | 新建 Go TDD 单元测试 |

---

## 3. 数据库设计

本篇属于 L0 最底层无状态基础设施，**不持有任何 SQL 迁移脚本、数据库表定义或 `pgx` 驱动依赖**。

本篇仅在 `internal/infra/config` 中定义 `PostgresConfig` 配置结构（DSN、连接池上下限、慢查询阈值、自动迁移开关），供 `SA-02`（`internal/infra/db`）消费并初始化连接池：
- 强制约定默认 DSN 包含 `TimeZone=UTC`，确保全库 `TIMESTAMPTZ` 会话时区一致；
- `Validate()` 启动前强校验 `storage.postgres.dsn` 非空且以 `postgres://` 或 `postgresql://` 开头、`max_conns >= min_conns >= 1`。

---

## 4. 冻结契约

### 4.1 导出符号

#### A. `internal/core/apperror`（零内部依赖，全仓最底层叶子包）

```go
package apperror

import "errors"

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
	CodeRateLimited            Code = "rate_limited"
	CodeServiceUnavailable     Code = "service_unavailable"
	CodeInternal               Code = "internal_error"
)

// ErrEgressDenied 供下游通过 errors.Is 精确识别 SSRF/出口守卫拒止错误。
var ErrEgressDenied = errors.New("egress denied")

type AppError struct {
	Code       Code
	HTTPStatus int
	Message    string
	Err        error
}

func (e *AppError) Error() string
func (e *AppError) Unwrap() error
func (e *AppError) Is(target error) bool

// 标准领域错误构造器（HTTP 状态码与 Java GlobalExceptionHandler 1:1 对齐）
func InvalidArg(msg string) *AppError
func Unauthorized(msg string) *AppError
func PasswordChangeRequired(msg string) *AppError
func Forbidden(msg string) *AppError
func EgressDenied(msg string) *AppError // HTTP 403 + CodeForbidden + Wrap ErrEgressDenied
func NotFound(msg string) *AppError
func MethodNotAllowed(msg string) *AppError
func Conflict(msg string) *AppError
func Busy(msg string) *AppError
func TooLarge(msg string) *AppError
func RateLimited(msg string) *AppError
func ServiceUnavailable(msg string, cause error) *AppError
func Internal(msg string, cause error) *AppError

// Wrap 保留内部根因同时附带对外错误分类
func Wrap(code Code, httpStatus int, msg string, cause error) *AppError

// FromError 将任意 error 归一化为 *AppError；未知错误映为 500 internal_error（脱敏防泄漏细节）
func FromError(err error) *AppError
func HTTPStatusOf(err error) int
func CodeOf(err error) Code

// 链接资料不可读领域复合错误（对齐 UnreadableLinkException.java:L4-L22 / UnreadableMaterialsException.java:L8-L32）
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
func (e *UnreadableMaterialsError) AsAppError() *AppError // HTTP 409 conflict
```

#### B. `internal/infra/config`

```go
package config

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/url"
	"time"
)

type Config struct {
	Server        ServerConfig        `yaml:"server"`
	App           AppConfig           `yaml:"app"`
	Storage       StorageConfig       `yaml:"storage"`
	Secret        SecretConfig        `yaml:"secret"`
	Network       NetworkConfig       `yaml:"network"`
	Runtime       RuntimeConfig       `yaml:"runtime"`
	Paths         PathsConfig         `yaml:"paths"`
	Quota         QuotaConfig         `yaml:"quota"`
	Agent         AgentConfig         `yaml:"agent"`
	Chat          ChatConfig          `yaml:"chat"`
	MCP           MCPConfig           `yaml:"mcp"`
	Files         FilesConfig         `yaml:"files"`
	Task          TaskConfig          `yaml:"task"`
	Automation    AutomationConfig    `yaml:"automation"`
	SuperAgent    SuperAgentConfig    `yaml:"superagent"`
	Memory        MemoryConfig        `yaml:"memory"`
	Provider      ProviderConfig      `yaml:"provider"`
	Cost          CostConfig          `yaml:"cost"`
	Inbox         InboxConfig         `yaml:"inbox"`
	AgentLog      AgentLogConfig      `yaml:"agent_log"`
	Observability ObservabilityConfig `yaml:"observability"`
	Platform      PlatformConfig      `yaml:"platform"`
}

// NetworkRuntime 由 BuildNetworkRuntime 构建，供 httpclient（SA-03）与 LocalLauncher（SA-11）共享
type NetworkRuntime struct {
	CertPool  *x509.CertPool
	TLSConfig *tls.Config
	ProxyFunc func(*http.Request) (*url.URL, error)
	ChildEnv  map[string]string // HTTP_PROXY/HTTPS_PROXY/NO_PROXY/NODE_EXTRA_CA_CERTS/SSL_CERT_FILE/NPM_CONFIG_REGISTRY
	NoProxy   string
}

type Overrides struct {
	Listen string // CLI --listen 覆盖
	UIDir  string // CLI --ui-dir 覆盖
}

func Default() *Config
func Load(path string, ov Overrides) (*Config, error) // Default -> YAML -> Env -> CLI Overrides -> Validate()
func LoadFromBytes(yamlData []byte, lookupEnv func(string) (string, bool), ov Overrides) (*Config, error)
func (c *Config) Validate() error
func BuildNetworkRuntime(netCfg NetworkConfig, rtCfg RuntimeConfig) (*NetworkRuntime, error)
```

#### C. `internal/infra/obs`

```go
package obs

import (
	"context"
	"io"
	"log/slog"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	RequestIDHeader = "X-Request-Id"
	AttrRequestID   = "request_id"
	AttrCompanyID   = "company_id"
	AttrUserID      = "user_id"
	RedactedValue   = "[REDACTED]"
)

// 上下文元数据读写（仅用于日志与链路追踪；鉴权与租户隔离严禁直接信任此处 company_id）
func WithRequestID(ctx context.Context, id string) context.Context
func RequestIDFrom(ctx context.Context) string
func WithCompanyID(ctx context.Context, id string) context.Context
func CompanyIDFrom(ctx context.Context) string
func WithUserID(ctx context.Context, id string) context.Context
func UserIDFrom(ctx context.Context) string
func CopyMeta(src, dst context.Context) context.Context // 供 tenant.Detach 复制可观测三元组

// SanitizeRequestID 校验 ^[A-Za-z0-9._-]{1,64}$，不合法则生成 32 位小写 hex 随机 ID
func SanitizeRequestID(raw string) string

type LoggerConfig struct {
	Level  string    // debug | info | warn | error
	Format string    // text | json
	File   string    // 非空时双写到文件（0640 权限）
	Output io.Writer // nil 时默认 os.Stdout（便于单元测试捕获输出）
}

func NewLogger(cfg LoggerConfig) (logger *slog.Logger, closeFn func() error, err error)
func NewContextHandler(next slog.Handler) slog.Handler
func NewRedactingHandler(next slog.Handler) slog.Handler
func RedactDSN(raw string) string

// Metrics 持有独立 Prometheus Registry，所有指标固定携带 application="<appName>" 标签
type Metrics struct {
	ChatTurnsTotal   *prometheus.CounterVec
	TaskRunsTotal    *prometheus.CounterVec
	TaskQueueDepth   *prometheus.GaugeVec
	TokensTotal      *prometheus.CounterVec
	CLIWarmPoolSize  prometheus.Gauge // 保持与 01 架构契约一致：superagent_cli_warm_pool_size
	IMConnected      *prometheus.GaugeVec
	HTTPRequestsSecs *prometheus.HistogramVec
	// ... 内部持有 *prometheus.Registry 与 wrapped prometheus.Registerer
}

func NewMetrics(appName string) (*Metrics, error)
func (m *Metrics) Registerer() prometheus.Registerer // 供 SA-02/SA-03/SA-04 注册各自基础设施指标（自动带 application 标签）
func (m *Metrics) Gatherer() prometheus.Gatherer     // 供 /actuator/prometheus 暴露
```

#### D. `internal/infra/lifecycle`

```go
package lifecycle

import (
	"context"
	"log/slog"
	"time"
)

type Loop interface {
	Name() string
	Run(ctx context.Context) error
}

type BackgroundGroup struct { /* rootCtx, cancel, wg, logger, onPanic, mu, errs */ }

type GroupOption func(*BackgroundGroup)

func WithLogger(l *slog.Logger) GroupOption
func WithOnPanic(fn func(name string, recovered any, stack []byte)) GroupOption

func NewBackgroundGroup(parent context.Context, opts ...GroupOption) *BackgroundGroup
func (g *BackgroundGroup) Context() context.Context
func (g *BackgroundGroup) Go(name string, fn func(ctx context.Context) error)
func (g *BackgroundGroup) GoLoop(loop Loop)
func (g *BackgroundGroup) Active() int
func (g *BackgroundGroup) Stop(ctx context.Context) error // 触发 cancel 并等待所有协程退出，超时返回 ctx.Err() + 未退协程名清单

// ShutdownPhase 定义严格逆序停机阶段优先级（数值越小越先执行）
type ShutdownPhase int

const (
	PhaseHTTP        ShutdownPhase = 10 // 停接新连接 + 排空普通 HTTP 请求（≤15s）
	PhaseBackground  ShutdownPhase = 20 // 停止后台调度循环与异步任务（≤8s）
	PhaseConnections ShutdownPhase = 30 // 关闭 IM 长连接与 CLI 热进程池（≤5s）
	PhaseStorage     ShutdownPhase = 40 // 关闭 PostgreSQL 连接池（≤2s）
	PhaseFlush       ShutdownPhase = 50 // 日志刷盘与最终资源释放（≤1s）
)

type ShutdownManager struct { /* totalBudget, logger, mu, hooks */ }

func NewShutdownManager(totalBudget time.Duration, logger *slog.Logger) *ShutdownManager
func (m *ShutdownManager) Register(phase ShutdownPhase, name string, budget time.Duration, fn func(ctx context.Context) error)
func (m *ShutdownManager) Shutdown(ctx context.Context) error
func NotifySignalContext(parent context.Context) (context.Context, context.CancelFunc) // 监听 SIGINT / SIGTERM
```

### 4.2 路由契约

本篇为 L0 底座包，不直接注册 HTTP 路由。
- `obs.RequestIDHeader = "X-Request-Id"` 与 `obs.SanitizeRequestID` 供 L5 Gin 中间件（`SA-36`）统一读写请求头并回写响应头；
- `obs.Metrics.Gatherer()` 供 L6 `/actuator/prometheus`（`SA-41`）直接挂载 `promhttp.HandlerFor(m.Gatherer(), promhttp.HandlerOpts{})`。

### 4.3 错误语义

精确对齐 Java `superagent-web/src/main/java/com/superagent/web/GlobalExceptionHandler.java:L33-L106` 并补齐商业私有化所需限流与上游不可用错误码：

| `apperror` 构造 | HTTP 状态码 | `code` | Java 原版参照 / 用途 |
|---|---:|---|---|
| `InvalidArg(msg)` | `400` | `invalid_argument` | `IllegalArgumentException` / `MethodArgumentNotValidException` / `MethodArgumentTypeMismatchException` (`L33-L39,L66-L69,L83-L87`) |
| `Unauthorized(msg)` | `401` | `unauthorized` | 未登录/凭据无效 (`SecurityConfig`) |
| `PasswordChangeRequired(msg)` | `403` | `password_change_required` | 首次强制改密门禁 (`PasswordChangeFilter`) |
| `Forbidden(msg)` / `EgressDenied(msg)` | `403` | `forbidden` | `ForbiddenException` (`L51-L54`), `EgressDeniedException` |
| `NotFound(msg)` | `404` | `not_found` | `NotFoundException` (`L46-L49`), `NoResourceFoundException` (`L41-L44`) |
| `MethodNotAllowed(msg)` | `405` | `method_not_allowed` | `HttpRequestMethodNotSupportedException` (`L77-L81`) |
| `Conflict(msg)` / `UnreadableMaterialsError` | `409` | `conflict` | `ConflictException` (`L56-L59`), `UnreadableMaterialsException` |
| `Busy(msg)` | `409` | `busy` | `BusyException` (`L61-L64`) |
| `TooLarge(msg)` | `413` | `too_large` | `MaxUploadSizeExceededException` (`L71-L75`，文件上限 `"单个文件不能超过 50 MB"`) |
| `RateLimited(msg)` | `429` | `rate_limited` | V1 增强：登录防爆破限流（`SA-04` `memcache`）与上游限流分类（`SA-03`） |
| `ServiceUnavailable(msg, cause)` | `503` | `service_unavailable` | V1 增强：熔断开启、依赖暂不可用 |
| `Internal(msg, cause)` / 未知 `error` | `500` | `internal_error` | `Exception` (`L100-L106`)；`FromError` 对未知错误对外仅返回通用文案 `"internal server error"`，根因仅留服务端日志 |

### 4.4 SQL 契约

本篇无 SQL 语句。仅通过 `config.PostgresConfig` 冻结以下连接配置契约供 `SA-02` 使用：
- 默认 `DSN = "postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC"`；
- 默认 `MaxConns = 16`、`MinConns = 2`、`MaxConnLifetime = 30m`、`MaxConnIdleTime = 5m`、`SlowQueryThreshold = 200ms`、`AutoMigrate = true`。

---

## 5. 行为语义（含 Java 源码对照、Go 契约定义与详细伪代码）

### 5.1 统一领域错误 (`internal/core/apperror/errors.go`)

> **Java 源码对照**：`superagent-web/src/main/java/com/superagent/web/GlobalExceptionHandler.java:L33-L106`、`superagent-core/src/main/java/com/superagent/core/link/UnreadableMaterialsException.java:L21-L31`。

```go
package apperror

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return string(e.Code)
}

func (e *AppError) Unwrap() error { return e.Err }

// Is 允许通过 errors.Is(err, &AppError{Code: CodeNotFound}) 或 sentinel 匹配
func (e *AppError) Is(target error) bool {
	if t, ok := target.(*AppError); ok {
		return t.Code != "" && t.Code == e.Code
	}
	return false
}

func EgressDenied(msg string) *AppError {
	return &AppError{
		Code:       CodeForbidden,
		HTTPStatus: http.StatusForbidden,
		Message:    msg,
		Err:        ErrEgressDenied,
	}
}

func FromError(err error) *AppError {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr
	}
	var materialsErr *UnreadableMaterialsError
	if errors.As(err, &materialsErr) {
		return materialsErr.AsAppError()
	}
	// 严禁将未知内部错误（如原始 SQL、文件路径、堆栈）直接透传给前端
	return Internal("internal server error", err)
}

// 对齐 UnreadableMaterialsException.java:L21-L31 中文错误聚合格式
func (e *UnreadableMaterialsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "任务没有创建：有 %d 份资料读不到，先解决再建任务。\n", len(e.Items))
	for _, item := range e.Items {
		fmt.Fprintf(&b, "- %s：%s", item.URL, item.Reason)
		if h := strings.TrimSpace(item.Hint); h != "" {
			fmt.Fprintf(&b, "（%s）", h)
		}
		b.WriteByte('\n')
	}
	return strings.TrimSpace(b.String())
}

func (e *UnreadableMaterialsError) AsAppError() *AppError {
	return &AppError{
		Code:       CodeConflict,
		HTTPStatus: http.StatusConflict,
		Message:    e.Error(),
		Err:        e,
	}
}
```

### 5.2 配置多源加载、企业代理/CA 与 fail-fast 校验 (`internal/infra/config`)

> **Java 源码对照**：`superagent-server/src/main/resources/application.yml:L1-L65`。

**配置加载优先级（高 → 低）**：`CLI Overrides (--listen/--ui-dir)` > `环境变量` > `YAML 文件` > `Default()` 内置默认值。

关键环境变量映射表（兼容原版 + `SYNAPSE_*` 规范命名，前者优先或按显式顺位覆盖）：

| 配置字段 | 环境变量（按优先级从高到低） | 默认值 |
|---|---|---|
| `Server.Listen` | `SYNAPSE_LISTEN`, `SERVER_PORT`（纯端口自动补 `:`） | `:18080` |
| `Server.ShutdownTimeout` | `SYNAPSE_SHUTDOWN_TIMEOUT` | `30s` |
| `Server.UIDir` | `SYNAPSE_UI_DIR` | `""` |
| `Server.SessionCookieSecure` | `SYNAPSE_COOKIE_SECURE` | `false` |
| `Storage.Postgres.DSN` | `SYNAPSE_PG_DSN`, `DATABASE_URL` | `postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC` |
| `Storage.Postgres.MaxConns` | `SYNAPSE_PG_MAX_CONNS` | `16` |
| `Storage.AutoMigrate` | `SYNAPSE_AUTO_MIGRATE` | `true` |
| `Secret.Key` | `SECRET_KEY`, `SUPERAGENT_SECRET_KEY` | `""` |
| `Secret.KeyFile` | `SYNAPSE_SECRET_KEY_FILE` | `./data/secret.key` |
| `Network.HTTPProxy` | `HTTP_PROXY`, `http_proxy` | `""` |
| `Network.HTTPSProxy` | `HTTPS_PROXY`, `https_proxy` | `""` |
| `Network.NoProxy` | `NO_PROXY`, `no_proxy` | `localhost,127.0.0.1,postgres` |
| `Network.CABundleFile` | `SYNAPSE_CA_BUNDLE_FILE`, `NODE_EXTRA_CA_CERTS` | `""` |
| `Runtime.NPMRegistry` | `NPM_CONFIG_REGISTRY` | `""` |
| `Runtime.NodeDistMirror` | `SYNAPSE_NODE_DIST_MIRROR` | `https://nodejs.org/dist` |
| `Paths.*` | `SYNAPSE_DATA_DIR`（统一重定位 7 个子目录根路径） | `./data/{workspaces,tasks,files,attachments,avatars,branding,skills}` |
| `Observability.LogLevel/Format/File` | `SYNAPSE_LOG_LEVEL`, `SYNAPSE_LOG_FORMAT`, `SYNAPSE_LOG_FILE` | `info` / `text` / `""` |

```go
package config

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/net/http/httpproxy"
	"gopkg.in/yaml.v3"
)

func LoadFromBytes(yamlData []byte, lookupEnv func(string) (string, bool), ov Overrides) (*Config, error) {
	cfg := Default()
	if len(bytes.TrimSpace(yamlData)) > 0 {
		dec := yaml.NewDecoder(bytes.NewReader(yamlData))
		dec.KnownFields(true) // Fail-Fast：拼写错误的未知 YAML 字段立即拒绝启动
		if err := dec.Decode(cfg); err != nil {
			return nil, fmt.Errorf("parse config yaml: %w", err)
		}
	}
	if err := applyEnvOverrides(cfg, lookupEnv); err != nil {
		return nil, err
	}
	if ov.Listen != "" {
		cfg.Server.Listen = ov.Listen
	}
	if ov.UIDir != "" {
		cfg.Server.UIDir = ov.UIDir
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// Validate 启动前一次性聚合所有配置错误，避免运维人员修一个错重启一次。
func (c *Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Server.Listen) == "" {
		errs = append(errs, errors.New("server.listen must not be empty"))
	}
	if c.Server.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("server.shutdown_timeout must be > 0"))
	}
	dsn := strings.TrimSpace(c.Storage.Postgres.DSN)
	if dsn == "" || (!strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://")) {
		errs = append(errs, errors.New("storage.postgres.dsn must start with postgres:// or postgresql://"))
	}
	if c.Storage.Postgres.MinConns < 1 || c.Storage.Postgres.MaxConns < c.Storage.Postgres.MinConns {
		errs = append(errs, fmt.Errorf("invalid postgres pool bounds: min=%d max=%d",
			c.Storage.Postgres.MinConns, c.Storage.Postgres.MaxConns))
	}
	if k := strings.TrimSpace(c.Secret.Key); k != "" {
		raw, err := base64.StdEncoding.DecodeString(k)
		if err != nil || len(raw) != 32 {
			errs = append(errs, errors.New("secret.key must be 32-byte standard base64"))
		}
	}
	for name, rawURL := range map[string]string{
		"network.http_proxy":  c.Network.HTTPProxy,
		"network.https_proxy": c.Network.HTTPSProxy,
	} {
		if rawURL != "" {
			u, err := url.Parse(rawURL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				errs = append(errs, fmt.Errorf("%s is not a valid http(s) proxy url: %q", name, rawURL))
			}
		}
	}
	if c.Network.CABundleFile != "" {
		pemBytes, err := os.ReadFile(c.Network.CABundleFile)
		if err != nil {
			errs = append(errs, fmt.Errorf("read network.ca_bundle_file: %w", err))
		} else if !x509.NewCertPool().AppendCertsFromPEM(pemBytes) {
			errs = append(errs, fmt.Errorf("network.ca_bundle_file %q contains no valid PEM certificate", c.Network.CABundleFile))
		}
	}
	switch strings.ToLower(c.Observability.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		errs = append(errs, fmt.Errorf("invalid observability.log_level: %q", c.Observability.LogLevel))
	}
	switch strings.ToLower(c.Observability.LogFormat) {
	case "text", "json":
	default:
		errs = append(errs, fmt.Errorf("invalid observability.log_format: %q", c.Observability.LogFormat))
	}
	if c.Files.UploadMaxMB <= 0 || c.Files.MaxMB < c.Files.UploadMaxMB {
		errs = append(errs, errors.New("files.upload_max_mb must be > 0 and <= files.max_mb"))
	}
	if c.Task.MaxParallel <= 0 {
		errs = append(errs, errors.New("task.max_parallel must be > 0"))
	}
	if _, err := time.LoadLocation(c.SuperAgent.Timezone); err != nil {
		errs = append(errs, fmt.Errorf("invalid superagent.timezone %q: %w", c.SuperAgent.Timezone, err))
	}
	return errors.Join(errs...)
}

// BuildNetworkRuntime 构建统一的企业代理函数、TLS CertPool 与 CLI 子进程环境。
func BuildNetworkRuntime(netCfg NetworkConfig, rtCfg RuntimeConfig) (*NetworkRuntime, error) {
	noProxy := ensureLocalNoProxy(netCfg.NoProxy) // 强制包含 127.0.0.1,localhost,::1
	proxyCfg := &httpproxy.Config{
		HTTPProxy:  netCfg.HTTPProxy,
		HTTPSProxy: netCfg.HTTPSProxy,
		NoProxy:    noProxy,
		CGI:        false,
	}
	proxyURLFn := proxyCfg.ProxyFunc()
	proxyFunc := func(req *http.Request) (*url.URL, error) {
		return proxyURLFn(req.URL)
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	childEnv := map[string]string{}
	if netCfg.HTTPProxy != "" {
		childEnv["HTTP_PROXY"] = netCfg.HTTPProxy
		childEnv["http_proxy"] = netCfg.HTTPProxy
	}
	if netCfg.HTTPSProxy != "" {
		childEnv["HTTPS_PROXY"] = netCfg.HTTPSProxy
		childEnv["https_proxy"] = netCfg.HTTPSProxy
	}
	childEnv["NO_PROXY"] = noProxy
	childEnv["no_proxy"] = noProxy
	if rtCfg.NPMRegistry != "" {
		childEnv["NPM_CONFIG_REGISTRY"] = rtCfg.NPMRegistry
	}
	if netCfg.CABundleFile != "" {
		pemBytes, err := os.ReadFile(netCfg.CABundleFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_bundle_file %q: %w", netCfg.CABundleFile, err)
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, fmt.Errorf("no valid PEM cert in %q", netCfg.CABundleFile)
		}
		childEnv["NODE_EXTRA_CA_CERTS"] = netCfg.CABundleFile
		childEnv["SSL_CERT_FILE"] = netCfg.CABundleFile
	}

	return &NetworkRuntime{
		CertPool:  pool,
		TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool},
		ProxyFunc: proxyFunc,
		ChildEnv:  childEnv,
		NoProxy:   noProxy,
	}, nil
}
```

### 5.3 上下文透传日志、敏感字段脱敏器与 Prometheus 注册表 (`internal/infra/obs`)

> **Java 源码对照**：`superagent-web/src/main/java/com/superagent/web/TraceIdFilter.java:L29-L47`、`superagent-core/src/main/java/com/superagent/core/logging/LogContext.java:L13-L37`、`superagent-core/src/main/java/com/superagent/core/metrics/MetricNames.java:L11-L21`、`CommonTagsMeterRegistryPostProcessor.java:L15-L30`。

**日志处理器链（外 → 内）**：`ContextHandler`（注入 `request_id`/`company_id`/`user_id`）→ `RedactingHandler`（按 Key 与值模式递归脱敏）→ `slog.TextHandler` 或 `slog.JSONHandler`。

```go
package obs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/url"
	"regexp"
	"strings"
)

var (
	safeRequestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	bearerRe        = regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	dsnCredRe       = regexp.MustCompile(`(postgres(?:ql)?://[^:@/\s]+):([^@\s]+)@`)
	sensitiveKeys   = []string{
		"password", "passwd", "secret", "api_key", "apikey", "token",
		"authorization", "cookie", "set-cookie", "private_key",
		"key_cipher", "secret_cipher", "data_key_cipher", "credential",
	}
)

func SanitizeRequestID(raw string) string {
	if safeRequestIDRe.MatchString(raw) {
		return raw
	}
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// isSensitiveKey 大小写不敏感匹配常见敏感字段名（包括嵌套属性名与连字符/下划线变体）
func isSensitiveKey(key string) bool {
	k := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
	for _, pat := range sensitiveKeys {
		if strings.Contains(k, pat) {
			return true
		}
	}
	return false
}

func redactAttr(a slog.Attr) slog.Attr {
	if a.Value.Kind() == slog.KindGroup {
		attrs := a.Value.Group()
		out := make([]slog.Attr, len(attrs))
		for i, sub := range attrs {
			out[i] = redactAttr(sub)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(out...)}
	}
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, RedactedValue)
	}
	if a.Value.Kind() == slog.KindString {
		s := a.Value.String()
		s = dsnCredRe.ReplaceAllString(s, "${1}:***@")
		s = bearerRe.ReplaceAllString(s, "${1} "+RedactedValue)
		return slog.String(a.Key, s)
	}
	return a
}

// RedactDSN 对 postgres://user:pass@host/db 做密码掩码，供启动日志安全输出
func RedactDSN(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return dsnCredRe.ReplaceAllString(raw, "${1}:***@")
	}
	if _, hasPwd := u.User.Password(); hasPwd {
		u.User = url.UserPassword(u.User.Username(), "***")
	}
	return u.String()
}
```

**Prometheus 指标注册契约 (`metrics.go`)**：
- `NewMetrics(appName string)` 创建全新的 `reg := prometheus.NewRegistry()`，绝不调用 `prometheus.MustRegister(...)` 污染全局状态；
- 使用 `wrapped := prometheus.WrapRegistererWith(prometheus.Labels{"application": appName}, reg)` 对所有注册指标自动附加 `application="synapse-server"` 标签；
- 注册 `collectors.NewGoCollector()`、`collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})` 与以下核心指标（严格控基数，禁用 `user_id`/`session_id`/`request_id` 作为标签）：
  1. `superagent_chat_turns_total` (`company_id`, `cli`, `outcome`)
  2. `superagent_task_runs_total` (`company_id`, `flow`, `status`)
  3. `superagent_task_queue_depth` (`company_id`, `status`)
  4. `superagent_tokens_total` (`company_id`, `cli`, `model`, `kind`)
  5. `superagent_cli_warm_pool_size`（无额外标签）
  6. `superagent_im_connected` (`company_id`, `channel`)
  7. `http_server_requests_seconds` (`method`, `route`, `status`)

### 5.4 后台协程托管与 30 秒分阶段逆序优雅停机 (`internal/infra/lifecycle`)

```go
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"sync"
	"sync/atomic"
	"time"
)

func (g *BackgroundGroup) Go(name string, fn func(ctx context.Context) error) {
	g.mu.Lock()
	if g.stopped {
		g.mu.Unlock()
		g.logger.Warn("background task rejected after group stopped", "task", name)
		return
	}
	g.running[name] = struct{}{}
	g.mu.Unlock()

	g.active.Add(1)
	g.wg.Add(1)
	go func() {
		defer g.wg.Done()
		defer g.active.Add(-1)
		defer func() {
			g.mu.Lock()
			delete(g.running, name)
			g.mu.Unlock()
		}()
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				g.logger.ErrorContext(g.ctx, "background goroutine panicked",
					"task", name, "panic", r, "stack", string(stack))
				if g.onPanic != nil {
					g.onPanic(name, r, stack)
				}
			}
		}()

		g.logger.InfoContext(g.ctx, "background task started", "task", name)
		if err := fn(g.ctx); err != nil && !errors.Is(err, context.Canceled) {
			g.logger.ErrorContext(g.ctx, "background task exited with error", "task", name, "err", err)
			g.recordErr(fmt.Errorf("task %s: %w", name, err))
		} else {
			g.logger.InfoContext(g.ctx, "background task stopped", "task", name)
		}
	}()
}

// Shutdown 按 Phase 升序（PhaseHTTP -> PhaseBackground -> PhaseConnections -> PhaseStorage -> PhaseFlush）
// 同 Phase 内按注册逆序执行，每个 hook 的超时严格裁剪在剩余总预算（默认 30s）之内。
func (m *ShutdownManager) Shutdown(ctx context.Context) error {
	deadline := time.Now().Add(m.totalBudget)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}

	m.mu.Lock()
	hooks := slices.Clone(m.hooks)
	m.mu.Unlock()

	// 稳定排序：Phase 小的先执行；同 Phase 内后注册的先执行（LIFO）
	slices.SortStableFunc(hooks, func(a, b shutdownHook) int {
		if a.phase != b.phase {
			return int(a.phase) - int(b.phase)
		}
		return b.seq - a.seq
	})

	var errs []error
	for _, h := range hooks {
		rem := time.Until(deadline)
		if rem <= 0 {
			errs = append(errs, fmt.Errorf("shutdown budget exhausted before hook %q", h.name))
			break
		}
		budget := h.budget
		if budget <= 0 || budget > rem {
			budget = rem
		}
		phaseCtx, cancel := context.WithTimeout(context.Background(), budget)
		start := time.Now()
		err := h.fn(phaseCtx)
		cancel()
		if err != nil {
			m.logger.Warn("shutdown hook returned error",
				"phase", h.phase, "hook", h.name, "elapsed_ms", time.Since(start).Milliseconds(), "err", err)
			errs = append(errs, fmt.Errorf("%s: %w", h.name, err))
		} else {
			m.logger.Info("shutdown hook completed",
				"phase", h.phase, "hook", h.name, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}
	return errors.Join(errs...)
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：初始化 `synapse-server/go.mod` 基础依赖（`gopkg.in/yaml.v3`、`golang.org/x/net`、`github.com/prometheus/client_golang`），建立 `internal/core/apperror` 与 `internal/infra/{config,obs,lifecycle}` 目录骨架。
- [ ] **T2**：编写 `internal/core/apperror/errors_test.go`（红灯）→ 实现 `errors.go`（全部 13 个错误码、`errors.As`/`errors.Is`、`EgressDenied`、`UnreadableMaterialsError` 中文格式与 `FromError` 未知错误脱敏）。
- [ ] **T3**：编写 `internal/infra/config/config_test.go` 与 `network_test.go`（红灯）→ 实现 `config.go`、`load.go`、`validate.go`、`network.go` 与 `configs/config.example.yaml`（严格未知字段报错、多源优先级、代理 `NO_PROXY` 保底、自签 PEM 加载与全量 fail-fast 校验）。
- [ ] **T4**：编写 `internal/infra/obs/obs_test.go`（红灯）→ 实现 `context.go`、`redact.go`、`logger.go`、`metrics.go`（`SanitizeRequestID` 防换行注入、`ContextHandler` 透传三元组、`RedactingHandler` 嵌套脱敏与 DSN 密码掩码、独立 Prometheus Registry）。
- [ ] **T5**：编写 `internal/infra/lifecycle/lifecycle_test.go`（红灯）→ 实现 `group.go` 与 `shutdown.go`（`BackgroundGroup` panic recover 防崩、超时列出卡住协程、`ShutdownManager` 分阶段 LIFO 顺序与 30s 总预算截断）。
- [ ] **T6**：全量运行 `cd synapse-server && go build ./... && go test -race -cover ./internal/core/apperror/... ./internal/infra/...`，确认零竞态与覆盖率达标。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-01` 状态更新为 `已实施（commit <hash>）`；
- [ ] `synapse-server/configs/config.example.yaml`：保持字段名与 `internal/infra/config/config.go` YAML tag 100% 一致（通过单测加载 `config.example.yaml` 自动守卫防漂移）；
- [ ] `synapse-server/README.md`：记录 L0 底座包职责与环境变量覆盖优先级表。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | 领域错误码与 HTTP 状态映射全集 | `internal/core/apperror/errors_test.go` | 12 类错误构造器状态码与 `Code` 精确一致；`EgressDenied` 匹配 `errors.Is(err, ErrEgressDenied)` 且状态 403；未知 `errors.New("pg password ...")` 经 `FromError` 映射为 500 且 `Message` 不泄漏原始敏感文本；`UnreadableMaterialsError` 格式化与 Java 一致且转 409 |
| 2 | 配置多源覆盖优先级与未知字段 fail-fast | `internal/infra/config/config_test.go` | `CLI > Env > YAML > Default` 生效；`SERVER_PORT=19090` 正确转为 `:19090`；YAML 含拼写错误字段立即返错；解析仓库真实 `configs/config.example.yaml` 必须零报错通过 `Validate()` |
| 3 | 配置启动前 fail-fast 聚合校验 | `internal/infra/config/config_test.go` | 同时给非法 DSN、`secret.key` 非 32B、`max_conns < min_conns`、无效 `log_level`、损坏 `ca_bundle_file`，`Validate()` 一次性聚合全部错误返回 |
| 4 | 企业代理 `NO_PROXY` 保底与自签 CA 加载 | `internal/infra/config/network_test.go` | 即使配置 `no_proxy: "corp.local"`，`BuildNetworkRuntime` 后对 `http://127.0.0.1:18080/mcp` 和 `http://localhost:18080/mcp` 均返回 `nil` 代理；合法自签 PEM 成功加入 `CertPool` 并生成 `NODE_EXTRA_CA_CERTS`、`SSL_CERT_FILE` |
| 5 | `SanitizeRequestID` 防日志注入与上下文透传 | `internal/infra/obs/obs_test.go` | 含 `\r\n`、空格、超 64 字符的入站 `X-Request-Id` 均被替换为 32 位合法 hex；`slog.InfoContext(ctx, ...)` JSON 输出同时含 `request_id`、`company_id`、`user_id`；`CopyMeta` 完整迁移三元组 |
| 6 | 日志敏感字段递归脱敏与 DSN 脱敏 | `internal/infra/obs/obs_test.go` | `password`、`api_key`、`Authorization`、`secret_cipher`、嵌套 `slog.Group("auth", "token", "abc")` 输出均为 `[REDACTED]`；普通字段值含 `Bearer eyJ...` 或 `postgres://u:p@h/db` 均被掩码，明文绝不出现在日志缓冲区 |
| 7 | Prometheus 独立注册表与零冲突并发创建 | `internal/infra/obs/obs_test.go` | 连续两次 `NewMetrics("synapse-server")` 不 panic（不污染默认 Registry）；`Gather()` 包含全部 6 项 `superagent_*` 指标且均带 `application="synapse-server"` 标签 |
| 8 | `BackgroundGroup` panic 隔离与优雅退出 | `internal/infra/lifecycle/lifecycle_test.go` | 协程 A `panic("boom")` 触发 `OnPanic` 与日志记录，协程 B 正常持续运行直至 `Stop(ctx)` 返回 `nil`；刻意阻塞忽略 `ctx` 的协程在 `Stop` 超时时报错并列出未退出任务名 |
| 9 | `ShutdownManager` 分阶段顺序与总预算裁剪 | `internal/infra/lifecycle/lifecycle_test.go` | 校验严格按 `PhaseHTTP → PhaseBackground → PhaseConnections → PhaseStorage → PhaseFlush` 执行，同 Phase 按 LIFO 执行；总预算 200ms 时慢 hook 被强制截断且后续按剩余预算收敛 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race -cover ./internal/core/apperror/... ./internal/infra/config/... ./internal/infra/obs/... ./internal/infra/lifecycle/...` 全绿
- [ ] §2 交付物表 8 项逐一落地且导出签名与 §4.1 逐字一致
- [ ] §8 测试清单 9 项全部落地，核心包行覆盖率 ≥ 85%
- [ ] `configs/config.example.yaml` 经单测 `Load` + `Validate()` 校验通过，且零包含任何 Redis 配置项
- [ ] `internal/core/apperror` 确认零引入任何内部包与第三方库（仅 Go 标准库）

---

## 10. 可提交节点

- 建议 commit 1（错误模型与配置体系）：`feat(l0): add apperror domain error taxonomy and fail-fast YAML/env config with enterprise proxy/CA (spec 01, PR SA-01)`
- 建议 commit 2（可观测性与生命周期治理）：`feat(l0): add context-aware redacting slog, isolated Prometheus registry, BackgroundGroup and 30s ShutdownManager (spec 01, PR SA-01)`
