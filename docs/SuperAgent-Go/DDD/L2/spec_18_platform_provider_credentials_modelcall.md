# spec 18 | L2 模型供应商治理 — ProviderCatalog、租户密钥池熔断切换、探活与 ModelCall 轻量直调

- **状态**：`冻结待实施`
- **篇号顺序**：18（L2 平台供应商治理核心篇，承接 L0 `httpclient`/`crypto`/`memcache` 与 L1 `postgres`/`gateway`）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-18`
- **预估工作量**：`2 人天`
- **上位契约**：
  - `docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §1、§4.1、§5.1
  - `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §1–§4
  - `docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §1.3
  - 归档母本：`docs/SuperAgent-Go/DDD/_epic/L2/spec_05_platform_provider_node_credentials_connectors.md` §1–§5.1、§5.4–§5.5
- **前置依赖**：
  - `L0/spec_03 (SA-03)`（`internal/infra/httpclient` `TrustedClient` 与结构化 `*ClientError`/`ErrorKind`；并依托同层 `SA-01` `apperror`/`config`/`obs`/`lifecycle.BackgroundGroup`、`SA-04` `tenant`/`crypto.SecretCipher`/`memcache.ShardedCache`）
  - `L1/spec_07 (SA-07)`（`internal/storage/postgres` `00002_providers_connectors.sql` 的 `model_provider` 与 `provider_credential` 两张 PG17 表及 `ModelProviderStore` / `ProviderCredentialStore` 实现）
  - `L1/spec_14 (SA-14)`（`internal/runtime/gateway` 的 `CredentialResolver` 反转端口、`EndpointFailures` 规则与 `EnvAssembler`）
- **下游消费者**：`SA-19`（`NodeRuntimeService` 与 `CLIInstaller` 消费 `CatalogRegistry`、`ProviderProbe` 并实现本篇冻结的 `CLIInstaller` 窄接口）、`SA-24`（`ModelPriceService` 消费 `ProviderService.StoredModels` 与 `ModelCall`）、`SA-25`（`TaskModelRouter` 消费 `ProviderService.CanInfer`/`CLIFor` 与 `ModelCall`）、`SA-28`（`ChatService` 消费 `CredentialService` via `ModelGateway`、`ProviderService` 与 `ModelCall`）、`SA-39`（L5 Web `ProviderController` 路由绑定）
- **重构参照源码**（绝对路径均基于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
  - `superagent-core/src/main/java/com/superagent/core/provider/{ProviderCatalog.java,ModelProvider.java,ProviderCredential.java,ModelProviderStore.java,ProviderCredentialStore.java}`
  - `superagent-platform/src/main/java/com/superagent/platform/provider/{CredentialService.java,EndpointFailures.java,ProviderProbe.java,ApiAccess.java,ProviderModelFetcher.java,ProviderInferenceProbe.java,ProbeUnreachableException.java,ProviderService.java,ProviderStatusChecker.java,ModelCall.java,ProviderAlreadyAddedException.java,ProviderNotFoundException.java}`
  - `superagent-api/src/main/java/com/superagent/api/provider/{ProviderDto.java,ProviderModelDto.java,CatalogEntryDto.java,CredentialDto.java,CredentialRequests.java,AddProviderRequest.java}`
  - `superagent-web/src/main/java/com/superagent/web/provider/ProviderController.java`

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业级多租户私有化部署中，模型供应商治理需要同时解决四大痛点：
1. **严格多租户凭据隔离与安全扩展**：每个企业租户独立维护自己的 API Key 与中转 Endpoint，必须以每租户独立 DEK 密文落盘，空池直接返回 `403 NoCompanyKeyError`，**绝不允许静默回退宿主机环境变量或 CLI 登录态**；同时静态目录仅在代码侧声明安全安装命令，V1 默认仅上线经过严格验证的 **Claude Code** 与 **OpenAI Codex** 双 GA 供应商，但开放 `CatalogRegistry` 注册表支持后续零侵入扩展。
2. **多密钥容灾与 429/5xx 冷却熔断切换**：单一上游 Key 遭遇限流（`429`）、上游过载（`500/502/503/504/529`）、额度耗尽（`402`）或鉴权失效（`401/403`）时，系统需要在零输出失败瞬间标记故障、对限流/过载实施进程内退避冷却熔断，并自动切换至次优先级健康 Key 重试；同时后台每 10 分钟复检故障 Key，使恢复后的高优 Key 自动接管流量。
3. **可信模型目录拉取与不误杀的真实推理探活**：模型列表仅证明“目录存在”而非“当前账号可推理”；系统需定期自动刷新目录并对模型执行最小化真实推理探测（`Claude /v1/messages`、`Codex /responses`），且精准区分“模型/鉴权错误”与“瞬时网络不可达（`*ProbeUnreachableError`）”，网络抖动时保护既有可用状态并在 5 分钟后自动单租户重试。
4. **复用 L0 `TrustedClient` 的轻量 `ModelCall` 直调**：为成本路由、记忆提炼、标题/标签生成等系统内部短决策提供低延迟直连 LLM 通道（直接复用 L0 `httpclient.TrustedClient` 享受企业代理/私有 CA/熔断观测），且**严格遵守 L0 `SA-03` 架构红线——HTTP 层不内置业务降级，由 `ModelCall` 作为调用方主动执行 `CredentialService.Failover` 密钥切换重试**。

### 1.2 本篇边界（2 人天，单人可逐行 Review）

- **本篇负责（`internal/platform/provider`）**：
  - `CatalogRegistry`（V1 GA 默认注册 `claude` & `codex`，开放 `Register` 扩展；私有化模式禁用 `MachineCredentials` 自动宿主机密钥导入与 `CliConfigWriter`）；
  - `CredentialService` + `EndpointFailures`（每租户 DEK 密文存储、优先级池、429/5xx 进程内冷却熔断表（基于 L0 `memcache.ShardedCache`，**严禁 Redis**）、Layer-1 `Failover`、`10m/1h` 巡检复检、实现 `SA-14` `gateway.CredentialResolver` 端口）；
  - `ProbeService` 族（`ProviderProbe` 本地版本与 npm 最新版探测、`ApiAccess`、`ProviderModelFetcher` 复用 L0 `TrustedClient` 与可扩展注册点、`ProviderInferenceProbe` + `*ProbeUnreachableError` + `UsageRecorder` 记账）；
  - `ProviderService`（写锁串行化、模型列表刷新、6 并发推理探测 `Sweep`、`CLIFor`、`CanInfer`、`StoredModels`，以及供 `SA-19` 挂载的 `CLIInstaller` 窄接口）与 `ProviderStatusChecker`（4 条调度节奏 + 5 分钟单租户去重重试，统一挂入 L0 `lifecycle.BackgroundGroup`）；
  - `ModelCall`（优先复用 L0 `httpclient.TrustedClient` 进行轻量协议直调，调用方内建多 Key 降级切换；对不支持直调或显式指定 CLI 的场景保留 `AskViaCLI` 经 `RunnerRegistry` 兜底）。
- **本篇不负责（明确切割至后续篇）**：
  - 私有 Node.js 下载、SHA-256 校验、60s 停滞 watchdog、页面在线 `npm install -g` 与 `Setpgid` 进程组强杀归属 **`L2/spec_19 (SA-19)`**（`internal/platform/provider/installer`），通过本篇定义的 `CLIInstaller` 与 `NodeVersionChecker` 窄接口解耦；
  - Gin HTTP Controller 路由注册与请求反序列化归属 **`L5/spec_39 (SA-39)`**；
  - **架构硬红线**：严禁引入 Redis；所有出站 LLM/模型目录请求强制经 L0 `httpclient.TrustedClient`（严禁自建裸 `http.Client`）；严禁导入 `internal/storage/postgres`、`internal/agent`、`internal/task`、`internal/cost`、`internal/web`。

### 1.3 Java 原版源码精确映射表

| 目标 Go 组件 | Java 参照源码（绝对路径均基于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`） | 核心行号范围 | V1 裁剪 / 强化要点 |
|---|---|---|---|
| `provider.CatalogRegistry` | `superagent-core/src/main/java/com/superagent/core/provider/ProviderCatalog.java` | `L19-L106`（`claude:L64-66`, `codex:L67-68`） | 升级为可扩展注册表；V1 默认仅注册 `claude` & `codex` 双 GA 条目 |
| `provider.EndpointFailures` | `superagent-platform/src/main/java/com/superagent/platform/provider/EndpointFailures.java` | `L12-L60`（`STATUS:L15-16`, `PHRASES:L18-28`, `MODEL_GONE:L34-36`） | 1:1 完整保留正则、39 条端点短语、12 条模型下架短语与 `404` 判定，新增 `IsTransientRateOr5xx` 区分冷却与永久错 |
| `provider.CredentialService` | `superagent-platform/src/main/java/com/superagent/platform/provider/CredentialService.java`、`superagent-core/src/main/java/com/superagent/core/provider/ProviderCredential.java` | `CredentialService.java:L43-L440`（`failover:L149-163`, `choose:L170-186`, `checkAll/dueForCheck/check:L212-261`, `add/update/reorder/mask/baseUrl:L304-439`） | 实现 `SA-14` `gateway.CredentialResolver`；新增基于 L0 `memcache` 的 429/5xx 冷却熔断表；默认禁用宿主机导入确保租户强隔离 |
| `provider.ProviderProbe` | `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderProbe.java` | `L27-L185`（`packageVersion:L56-71`, `version:L74-92`, `latestNpmVersion:L115-141`, `isNewer:L144-159`） | 子进程统一改由 `spi.ProcessLauncher` 执行，零裸 `os/exec` |
| `provider.ApiAccess` | `superagent-platform/src/main/java/com/superagent/platform/provider/ApiAccess.java` | `L13-L68` | 仅消费租户 `RunCredential`，移除 `System.getenv("ANTHROPIC_API_KEY")` 宿主机回退 |
| `provider.ProviderModelFetcher` | `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderModelFetcher.java` | `L42-L388`（`NON_CHAT:L45-46`, `fetchCodex:L220-250`, `fetchClaude:L254-350`） | 替换裸 `HttpClient` 为 L0 `TrustedClient`；V1 内建 `claude`/`codex` + `RegisterFetcher` 扩展点 |
| `provider.ProviderInferenceProbe`, `ProbeUnreachableError` | `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderInferenceProbe.java`、`ProbeUnreachableException.java` | `ProviderInferenceProbe.java:L42-L615`（`checkKey:L87-113`, `checkAnthropic:L369-391`, `sendRaw:L402-414`, `checkCodex:L441-473`, `cliFailure/unreachable:L517-563`, `cause:L576-585`） | 替换裸 `HttpClient` 为 L0 `TrustedClient`；将 `ClientError(KindNetwork/KindTimeout/KindCircuitOpen)` 统一映射为 `*ProbeUnreachableError` |
| `provider.ProviderService`, `ProviderStatusChecker` | `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderService.java`、`ProviderStatusChecker.java` | `ProviderService.java:L50-L248,L435-L705`; `ProviderStatusChecker.java:L29-L148` | 安装动作抽象至 `CLIInstaller` 端口交由 `SA-19`；4 条定时循环统一挂入 L0 `lifecycle.BackgroundGroup` |
| `provider.ModelCall` | `superagent-platform/src/main/java/com/superagent/platform/provider/ModelCall.java` | `L29-L103` | 新增复用 L0 `TrustedClient` 的轻量协议直调 + 调用方主动 Key 降级切换，保留 `AskViaCLI` 兜底 |

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 供应商与凭据 API DTO / 领域错误常量核对与补全 | `internal/core/api/provider.go`、`internal/core/domain/provider.go` | 核对/补全 |
| 2 | 可扩展 `CatalogRegistry`（V1 GA 仅预注册 `claude` & `codex`） | `internal/platform/provider/catalog.go` | 新增 |
| 3 | `EndpointFailures`（HTTP 状态码正则、39 条端点短语、12 条模型下架短语与瞬态限流/5xx 分类） | `internal/platform/provider/endpoint_failures.go` | 新增 |
| 4 | `CredentialService`（租户 DEK 密文存储、多 Key 池轮询、429/5xx 进程内冷却熔断、`Failover`、`CheckAll`、`gateway.CredentialResolver` 实现） | `internal/platform/provider/credential_service.go` | 新增 |
| 5 | `ProviderProbe` + `ApiAccess` + `ProviderModelFetcher` + `ProviderInferenceProbe` + `ProbeUnreachableError` | `internal/platform/provider/probe.go`、`api_access.go`、`model_fetcher.go`、`inference_probe.go` | 新增 |
| 6 | `ProviderService`（含 `CLIInstaller` 窄接口、写锁串行化、6 并发推理探测 `Sweep`）与 `ProviderStatusChecker`（挂入 `BackgroundGroup`） | `internal/platform/provider/provider_service.go`、`status_checker.go` | 新增 |
| 7 | `ModelCall`（复用 L0 `TrustedClient` 轻量直调 + 调用方 Key 降级切换 + `AskViaCLI` 兜底） | `internal/platform/provider/model_call.go` | 新增 |
| 8 | 完整同包单元测试套件（Fake Store + `httptest` 模拟 Anthropic/OpenAI 上游 200/401/429/503/断连） | `internal/platform/provider/*_test.go` | 新增 |

> **分层守卫约束（`internal/arch_test.go`）**：
> `internal/platform/provider` 仅允许导入 `internal/core/{apperror,api,crypto,domain,spi,store,tenant}`、`internal/infra/{config,httpclient,lifecycle,memcache,obs}`、`internal/runtime/{gateway,runner}` 及 Go 标准库；**严禁导入** `internal/storage/postgres`、`internal/platform/provider/installer`、`internal/agent`、`internal/task`、`internal/cost`、`internal/web` 或任何 Redis 驱动。

---

## 3. 数据库设计

本篇直接复用 `L1/spec_07 (SA-07)` 在 `internal/storage/postgres/migrations/00002_providers_connectors.sql` 中落地并由 `ModelProviderStore` / `ProviderCredentialStore` 实现的两张租户隔离表，**本篇不新增表、不修改表结构**。以下冻结本篇强依赖的列与索引语义：

### 3.1 `model_provider`（消费 `SA-07`）

- **Java 参考**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlModelProviderStore.java:L13-L30`
- **关键列语义**：
  - 主键 `(company_id, id)`，唯一约束 `uq_model_provider_key (company_id, provider_key)`；
  - `status VARCHAR(32)` 取值枚举：`unsupported | not_installed | no_auth | ok | needs_upgrade | installed`（运行态 `installing` 由内存 `CLIInstaller.IsBusy(id)` 动态覆盖，不落盘）；
  - `models_json JSONB` 存储 `[]api.ProviderModelDto`（空值归一化为前端 `[]`，严禁返回 `null`）；
  - 时间戳列 `last_checked_at` / `models_checked_at` / `created_at` / `updated_at` 统一 `TIMESTAMPTZ` UTC。

### 3.2 `provider_credential`（消费 `SA-07`）

- **Java 参考**：`superagent-storage/src/main/java/com/superagent/storage/jdbc/MysqlProviderCredentialStore.java:L10-L30`、`AbstractJdbcProviderCredentialStore.java:L22-L82`
- **关键列语义**：
  - 主键 `(company_id, id)`，池查询索引 `idx_provider_credential_lookup (company_id, provider_key, deleted, priority)`；
  - `key_cipher TEXT NOT NULL`：仅存放 `SA-04` `crypto.SecretCipher.Encrypt(ctx, plainKey)` 生成的 `v2:<companyID>:<base64>` 租户信封密文（AAD 绑定 `company_id`）；
  - `key_hash VARCHAR(64)`：`hex(sha256(strings.TrimSpace(apiKey)))`，用于池内判重与历史去重；
  - `deleted BOOLEAN NOT NULL DEFAULT FALSE`：删除执行软删除，`List`/`Find` 过滤 `deleted = FALSE`，`KnownKeyHashes` 刻意包含已软删行；
  - `status VARCHAR(16)`：`ok | error | unknown`；`last_error VARCHAR(1000)` 截断保留前 990 runes + `"…"`。

### 3.3 进程内缓存与并发态（纯内存，零 Redis）

| 内存结构 | 载体 | 键格式 / 淘汰策略 | 语义说明 |
|---|---|---|---|
| 429/5xx 密钥冷却熔断表 | `memcache.ShardedCache[string, cooldownState]` | key = `<companyID>\|<providerKey>\|<credID>`，`MaxEntries: 4096` | 遇 429/5xx 时按 `Retry-After` 或指数退避（`30s→60s→120s→300s`，上限 `10m`）写入冷却期；健康复检通过立即 `Delete` 解除冷却 |
| 推理探测防重入表 | `sync.Mutex` + `map[string]time.Time` | key = `<companyID>\|<providerID>` | 同一供应商同时最多运行 1 轮 6 并发探测，供 `ProviderDto.CheckingSince` 展示 |
| 模型列表来源标签 | `sync.RWMutex` + `map[string]string` | key = `<companyID>\|<providerID>` | 仅用于展示（如 `"GET /v1/models"`、`"codex debug models"`），重启由下轮刷新回填 |
| 5 分钟不可达重试去重集 | `sync.Mutex` + `map[string]struct{}` | key = `<companyID>` | 某租户 `Sweep` 返回 `reached=false` 时单租户去重排期 1 次 5m 重试 |

---

## 4. 冻结契约

### 4.1 导出符号

```go
package provider

import (
	"context"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/api"
	"github.com/Karlsk/synapse-os/internal/core/crypto"
	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	"github.com/Karlsk/synapse-os/internal/core/store"
	"github.com/Karlsk/synapse-os/internal/infra/httpclient"
	"github.com/Karlsk/synapse-os/internal/infra/lifecycle"
	"github.com/Karlsk/synapse-os/internal/runtime/gateway"
	"github.com/Karlsk/synapse-os/internal/runtime/runner"
)

// --- 1. ProviderCatalog（V1 默认仅 Claude & Codex GA，开放注册表扩展） ---

const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
	ProtocolOther     = "other"
)

type AccessSpec struct {
	Protocol       string // "anthropic" | "openai" | "other"
	DefaultBaseURL string // 空表示无固定官方地址
	KeyEnv         string
	BaseURLEnv     string
}

type CatalogEntry struct {
	Key            string
	Name           string
	CLI            string
	Supported      bool
	InstallCommand string
	UpgradeCommand string
	VersionArgs    []string
	NPMPackage     string
	AuthFiles      []string
	AuthEnvVars    []string
	MinNode        string
	Preview        bool
	Access         AccessSpec
}

func (e CatalogEntry) Installable() bool

// NewNPMEntry 构造标准 npm 全局安装目录项（命令固定由代码静态生成，严禁从外部请求传入）。
func NewNPMEntry(key, name, cli string, supported bool, npmPkg, minNode string, preview bool, authFiles []string, access AccessSpec) CatalogEntry

type CatalogRegistry struct { /* mu sync.RWMutex; order []string; entries map[string]CatalogEntry */ }

func NewDefaultCatalog() *CatalogRegistry // V1 GA 仅预注册 claude & codex
func (c *CatalogRegistry) Register(entry CatalogEntry) error
func (c *CatalogRegistry) All() []CatalogEntry
func (c *CatalogRegistry) Find(key string) (CatalogEntry, bool)
func (c *CatalogRegistry) PooledKeys() []string

// --- 2. 与 SA-19 及其他域解耦的窄接口 ---

// CLIInstaller 由 SA-19 (internal/platform/provider/installer) 实现并在 wire.go 注入，彻底解耦 SA-18 与 SA-19。
type CLIInstaller interface {
	Install(ctx context.Context, providerID string, entry CatalogEntry) error
	Upgrade(ctx context.Context, providerID string, entry CatalogEntry) error
	IsBusy(providerID string) bool
	LastError(providerID string) string
	ClearState(providerID string)
	CurrentStep(providerID string) (step string, hasLog bool)
	InstallLog(providerID string) *api.InstallLogDto
}

// CompanyIterator 由 L2 CompanyService 实现，驱动跨租户公平巡检。
type CompanyIterator interface {
	ForEachEnabled(ctx context.Context, fn func(tenantCtx context.Context) error) error
}

// UsageRecorder 窄接口（L3 cost 域实现，允许 nil 用于单测）。
type UsageRecorder interface {
	Record(ctx context.Context, uctx domain.UsageContext, usage []domain.TokenUsage) error
	Tap(ctx context.Context, uctx *domain.UsageContext, sink spi.EventSink) spi.EventSink
}

// UsageReader 窄接口（查询 24h 探活 Token 消耗，允许 nil）。
type UsageReader interface {
	SystemCheckTokens24h(ctx context.Context, providerKey string) (int64, error)
}

// --- 3. EndpointFailures 故障分类 ---

func IsEndpointFailure(errMsg string) bool
func IsTransientRateOr5xx(errMsg string) bool // 命中 429/500/502/503/504/529/rate_limit/overloaded 等可冷却恢复故障
func IsModelFailure(errMsg string) bool

// --- 4. CredentialService（严格租户隔离 + 429/5xx 冷却熔断 + 实现 SA-14 gateway.CredentialResolver） ---

type NoCompanyKeyError struct {
	ProviderKey string
}

func (e *NoCompanyKeyError) Error() string
func (e *NoCompanyKeyError) Unwrap() error // 包装 apperror.Forbidden (HTTP 403)

type CooldownConfig struct {
	InitialBackoff time.Duration // 默认 30s
	MaxBackoff     time.Duration // 默认 10m
	MaxEntries     int           // 默认 4096
}

type CredentialService struct { /* store, providers, catalog, cipher, probe, cooldowns, bg, onSwitch */ }

var _ gateway.CredentialResolver = (*CredentialService)(nil)

func NewCredentialService(
	credStore store.ProviderCredentialStore,
	providerStore store.ModelProviderStore,
	catalog *CatalogRegistry,
	cipher *crypto.SecretCipher,
	probe *ProviderInferenceProbe,
	bg *lifecycle.BackgroundGroup,
	cdCfg CooldownConfig,
) *CredentialService

func (s *CredentialService) OnSwitch(listenerKey string, fn func(ctx context.Context, providerKey string))
func (s *CredentialService) HasOwnKey(ctx context.Context, providerKey string) (bool, error)
func (s *CredentialService) Current(ctx context.Context, providerKey string) (*domain.RunCredential, error)
func (s *CredentialService) ResolveCurrent(ctx context.Context, providerKey, excludeCredID string) (*domain.RunCredential, error)
func (s *CredentialService) Failover(ctx context.Context, providerKey string, used *domain.RunCredential, errMsg string) (*domain.RunCredential, bool, error)
func (s *CredentialService) FailoverWithRetryAfter(ctx context.Context, providerKey string, used *domain.RunCredential, errMsg string, retryAfter time.Duration) (*domain.RunCredential, bool, error)
func (s *CredentialService) CheckAll(ctx context.Context) error
func (s *CredentialService) List(ctx context.Context, providerKey string) ([]api.CredentialDto, error)
func (s *CredentialService) Add(ctx context.Context, providerKey string, req api.SaveCredentialRequest) (*api.CredentialDto, error)
func (s *CredentialService) Update(ctx context.Context, providerKey, id string, req api.SaveCredentialRequest) (*api.CredentialDto, error)
func (s *CredentialService) Delete(ctx context.Context, providerKey, id string) error
func (s *CredentialService) CheckNow(ctx context.Context, providerKey, id string) (*api.CredentialDto, error)
func (s *CredentialService) Reorder(ctx context.Context, providerKey string, ids []string) ([]api.CredentialDto, error)
func MaskKey(key string) string
func NormalizeBaseURL(raw string) (string, error)
func HashAPIKey(key string) string

// --- 5. ProbeService 族（本地探针、目录拉取、真实推理探活） ---

type ProbeUnreachableError struct {
	Message string
	Cause   error
}

func (e *ProbeUnreachableError) Error() string
func (e *ProbeUnreachableError) Unwrap() error

type ProviderProbe struct { /* paths *runner.CliPaths; launcher spi.ProcessLauncher */ }

func NewProviderProbe(paths *runner.CliPaths, launcher spi.ProcessLauncher) *ProviderProbe
func (p *ProviderProbe) FindBinary(cli string) (string, bool)
func (p *ProviderProbe) Version(ctx context.Context, binary string, entry CatalogEntry) (string, bool)
func (p *ProviderProbe) VersionWithArgs(ctx context.Context, binary string, args []string) (string, bool)
func (p *ProviderProbe) LatestNPMVersion(ctx context.Context, npmPackage string) (string, bool)
func (p *ProviderProbe) SearchPath() []string
func IsNewerSemver(latest, current string) bool

type ModelFetchResult struct {
	Models []api.ProviderModelDto
	Source string
}

type ModelFetcherFunc func(ctx context.Context, cred *domain.RunCredential) (*ModelFetchResult, error)

type ProviderModelFetcher struct { /* probe, launcher, catalog, trustedClient, customFetchers */ }

func NewProviderModelFetcher(probe *ProviderProbe, launcher spi.ProcessLauncher, catalog *CatalogRegistry, trustedClient *httpclient.Client) *ProviderModelFetcher
func (f *ProviderModelFetcher) RegisterFetcher(providerKey string, fn ModelFetcherFunc)
func (f *ProviderModelFetcher) Fetch(ctx context.Context, providerKey string, cred *domain.RunCredential) (*ModelFetchResult, error)

type ProviderInferenceProbe struct { /* probe, launcher, catalog, trustedClient, usage */ }

func NewProviderInferenceProbe(probe *ProviderProbe, launcher spi.ProcessLauncher, catalog *CatalogRegistry, trustedClient *httpclient.Client, usage UsageRecorder) *ProviderInferenceProbe
func (p *ProviderInferenceProbe) CheckKey(ctx context.Context, providerKey, modelID string, cred *domain.RunCredential) error
func (p *ProviderInferenceProbe) Check(ctx context.Context, providerKey, modelID string, cred *domain.RunCredential) error

// --- 6. ProviderService & ProviderStatusChecker ---

type ProviderService struct { /* store, catalog, probe, fetcher, inferProbe, creds, installer, usageReader, bg, writeMu, checking, modelsSource */ }

func NewProviderService(
	providerStore store.ModelProviderStore,
	catalog *CatalogRegistry,
	probe *ProviderProbe,
	fetcher *ProviderModelFetcher,
	inferProbe *ProviderInferenceProbe,
	creds *CredentialService,
	bg *lifecycle.BackgroundGroup,
	usageReader UsageReader,
) *ProviderService

// SetInstaller 在 wire.go 中注入 SA-19 CLIInstaller（并绑定安装完毕刷新回调）。
func (s *ProviderService) SetInstaller(inst CLIInstaller)
func (s *ProviderService) OnInstallFinished(ctx context.Context, providerID string)

func (s *ProviderService) List(ctx context.Context) ([]api.ProviderDto, error)
func (s *ProviderService) Detail(ctx context.Context, id string) (*api.ProviderDto, error)
func (s *ProviderService) Catalog(ctx context.Context) ([]api.CatalogEntryDto, error)
func (s *ProviderService) Add(ctx context.Context, key string) (*api.ProviderDto, error)
func (s *ProviderService) Remove(ctx context.Context, id string) error
func (s *ProviderService) Install(ctx context.Context, id string) (*api.ProviderDto, error)
func (s *ProviderService) Upgrade(ctx context.Context, id string) (*api.ProviderDto, error)
func (s *ProviderService) InstallLog(ctx context.Context, id string) (*api.InstallLogDto, error)
func (s *ProviderService) RefreshModelsNow(ctx context.Context, id string) (*api.ProviderDto, error)
func (s *ProviderService) CheckLatest(ctx context.Context, id string) (*api.ProviderDto, error)
func (s *ProviderService) KeyOf(ctx context.Context, id string) (string, error)
func (s *ProviderService) StoredModels(ctx context.Context, key string) ([]api.ProviderModelDto, error)
func (s *ProviderService) CanInfer(ctx context.Context, key, modelID string) bool
func (s *ProviderService) CLIFor(ctx context.Context, modelID string) (string, bool)
func (s *ProviderService) RefreshLocal(ctx context.Context, p domain.ModelProvider) (domain.ModelProvider, error)
func (s *ProviderService) RefreshLatest(ctx context.Context, p domain.ModelProvider) (domain.ModelProvider, error)
func (s *ProviderService) RefreshModels(ctx context.Context, p domain.ModelProvider) (domain.ModelProvider, error)
func (s *ProviderService) RefreshInference(ctx context.Context, p domain.ModelProvider, freshEnough time.Duration) (reached bool, err error)

type CheckerConfig struct {
	Enabled            bool
	LocalInterval      time.Duration // 默认 3s，最小 1s
	LatestInterval     time.Duration // 默认 3600s，最小 60s
	InferInterval      time.Duration // 默认 3600s，最小 60s
	CredentialInterval time.Duration // 默认 600s，最小 60s
	RetryUnreachable   time.Duration // 默认 300s（5 分钟）
}

type ProviderStatusChecker struct { /* companies, store, service, creds, cfg, retryPending */ }

func NewProviderStatusChecker(companies CompanyIterator, providerStore store.ModelProviderStore, service *ProviderService, creds *CredentialService, cfg CheckerConfig) *ProviderStatusChecker
// RegisterLoops 将 4 条后台循环与启动预热协程注册进 L0 lifecycle.BackgroundGroup。
func (c *ProviderStatusChecker) RegisterLoops(bg *lifecycle.BackgroundGroup)

// --- 7. ModelCall（轻量 HTTP 直调复用 L0 TrustedClient + 调用方主动 Key 降级切换 + CLI 兜底） ---

type ModelCall struct { /* catalog, providers, creds, trustedClient, runners, usage */ }

func NewModelCall(
	catalog *CatalogRegistry,
	providers *ProviderService,
	creds *CredentialService,
	trustedClient *httpclient.Client,
	runners *runner.Registry,
	usage UsageRecorder,
) *ModelCall

// Ask 优先经 L0 TrustedClient 轻量直调模型 HTTP API（Anthropic /v1/messages 或 OpenAI /responses），
// 遭遇端点故障/429/5xx 时由本调用方主动触发 creds.Failover 降级切换下一可用 Key 重试；
// 当供应商协议为 ProtocolOther 或无直调适配时自动回落至 AskViaCLI。
func (m *ModelCall) Ask(ctx context.Context, model, prompt string, timeout time.Duration, paying *domain.UsageContext) (string, error)

// AskViaCLI 通过 RunnerRegistry 在临时 sa-ask-* 目录执行无工具单轮调用（1:1 承接 Java ModelCall.java）。
func (m *ModelCall) AskViaCLI(ctx context.Context, model, prompt string, timeout time.Duration, paying *domain.UsageContext) (string, error)
```

### 4.2 路由契约（供 L5 `SA-39` `ProviderController` 绑定，与原版前端 100% 兼容）

全部响应遵循统一信封 `{"code":"ok","message":null,"data":<payload>}`，DTO JSON 字段名与 Java `ProviderDto`/`ProviderModelDto`/`CatalogEntryDto`/`CredentialDto` 100% camelCase 对齐：

| 路由路径 | HTTP 方法 | 角色 | 本篇绑定服务方法 |
|---|---|---|---|
| `/api/v1/providers` | `GET` / `POST` | `ADMIN` | `ProviderService.List` / `ProviderService.Add` |
| `/api/v1/providers/catalog` | `GET` | `ADMIN` | `ProviderService.Catalog`（V1 默认返回 `claude` & `codex`） |
| `/api/v1/providers/:id` | `GET` / `DELETE` | `ADMIN` | `ProviderService.Detail` / `ProviderService.Remove` |
| `/api/v1/providers/:id/models/refresh` | `POST` | `ADMIN` | `ProviderService.RefreshModelsNow` |
| `/api/v1/providers/:id/check-latest` | `POST` | `ADMIN` | `ProviderService.CheckLatest` |
| `/api/v1/providers/:id/credentials` | `GET` / `POST` | `ADMIN` | `CredentialService.List` / `CredentialService.Add` |
| `/api/v1/providers/:id/credentials/order` | `PUT` | `ADMIN` | `CredentialService.Reorder`（静态 `/order` 先于 `/:cid`） |
| `/api/v1/providers/:id/credentials/:cid` | `PUT` / `DELETE` | `ADMIN` | `CredentialService.Update` / `CredentialService.Delete` |
| `/api/v1/providers/:id/credentials/:cid/check` | `POST` | `ADMIN` | `CredentialService.CheckNow` |

> 注：`/api/v1/providers/node*` 与 `/api/v1/providers/:id/{install,upgrade,install-log}` 由 `SA-19` 定义实现并经 `ProviderService` 委托暴露。

### 4.3 错误语义

| 场景 | 返回错误类型 / HTTP 状态码 | 精确中文 / 英文文案（对齐 Java 原版） |
|---|---|---|
| 租户无任何已启用 API Key | `*NoCompanyKeyError`（wrap `apperror.Forbidden`, HTTP `403`） | `"本公司还没有配置 <providerKey> 的 API Key：请管理员在「模型」里为它添加 Key"` |
| 重复添加同一供应商 | `apperror.Conflict`（HTTP `409`） | `"Provider already added: <key>"`（对齐 `ProviderAlreadyAddedException.java:L8`） |
| 未知供应商 ID | `apperror.NotFound`（HTTP `404`） | `"No such provider: <id>"`（对齐 `ProviderNotFoundException.java:L8`） |
| 未知目录 Key | `apperror.InvalidArg`（HTTP `400`） | `"Unknown provider: <key>"` |
| 空 Key / 重复 Key / 未知 Key ID / 不可配置池 CLI | `apperror.InvalidArg`（HTTP `400`） | `"API Key 不能为空"` / `"这个 API Key 已经在列表里了"` / `"没有这个 API Key：<id>"` / `"这个 CLI 不能配置 API Key：<providerKey>"` |
| 非法 BaseURL | `apperror.InvalidArg`（HTTP `400`） | `"访问地址格式不对：<url>"` / `"访问地址要以 http:// 或 https:// 开头：<url>"` |
| 推理探测网络不可达 / DNS / 超时 / 熔断开路 | `*ProbeUnreachableError`（内部错误，不标模型失败） | `"无法连接 <base>（<cause>）"` / `"请求 <base> 失败：<cause>"`，保留模型上次 `inferStatus` 并触发 5 分钟单租户去重重试 |
| 密文无法解密（KEK 丢失或跨租户） | `CredentialDto.KeyHint` 脱敏 / `CheckNow` 标 `error` | `keyHint = "（无法解密）"`；`lastError = "密钥无法解密，请重新填写 API Key"` |
| `ModelCall` 无匹配 CLI / 超时 / 空答 | `apperror.ServiceUnavailable` / `error` | `"No CLI can run <model>"` / `"<model> didn't answer within <N>s"` / `"<model> gave no answer"` |

### 4.4 SQL 契约

本篇通过 `internal/core/store` 窄接口操作 PG17，零直接 SQL 拼接，强依赖 `SA-07` 冻结的三条存储语义：
1. **租户强隔离与幂等 Upsert**：`ModelProviderStore.Save`（`ON CONFLICT (company_id, id) DO UPDATE`，冲突 `uq_model_provider_key` 返回 `apperror.Conflict`）与 `ProviderCredentialStore.Save`（`ON CONFLICT (company_id, id) DO UPDATE SET ..., deleted = FALSE`），每次调用必携带 `tenant.WithCompany(ctx, cid)`；
2. **`provider_credential` 软删除保留哈希**：`Delete` 执行 `UPDATE provider_credential SET deleted = TRUE, updated_at = $3 WHERE company_id = $1 AND id = $2 AND deleted = FALSE`；`List` 按 `priority ASC, created_at ASC` 返回 `deleted = FALSE` 行；`KnownKeyHashes` 返回全部历史（含已软删）`key_hash`；
3. **`model_provider` 行写串行化**：`ProviderService.update` 持有 `writeMu sync.Mutex` 执行 `FindByID -> transform -> Save`，确保 3s 本地探测、1h 最新版本/模型列表与 6 并发推理探测绝不相互覆盖彼此列。

---

## 5. 行为语义（含流程图与核心 Go 伪代码）

### 5.1 `ProviderCatalog`（V1 仅 Claude & Codex GA + 可扩展注册表）

> **Java 源码对照**：`ProviderCatalog.java:L19-L106`（V1 保留 `L64-68` 双 GA 条目，移除硬编码静态 `List.of` 改为并发安全可扩展 `CatalogRegistry`）。

| `Key` | `Name` | `CLI` | `NPMPackage` | `MinNode` | `Protocol` | `DefaultBaseURL` | `KeyEnv` | `BaseURLEnv` |
|---|---|---|---|---|---|---|---|---|
| `claude` | `Claude Code` | `claude` | `@anthropic-ai/claude-code` | `22.0.0` | `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` |
| `codex` | `OpenAI Codex` | `codex` | `@openai/codex` | `16.0.0` | `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` | `""` |

- `NewNPMEntry` 固定生成 `InstallCommand = UpgradeCommand = "npm install -g " + npmPkg + "@latest --no-fund --no-audit"`、`VersionArgs = []string{"--version"}`。
- `Register(entry)` 拒绝空 `Key` 或重复 `Key`（返回 `apperror.InvalidArg`/`apperror.Conflict`），按注册顺序保序输出供前端展示。
- 未来接入 DeepSeek / GLM / Kimi / Qwen / Grok / CodeBuddy / Gemini / MiniMax 时，仅需注册 `CatalogEntry` + `ModelFetcherFunc` + `AgentRunner` 插件，本篇核心代码零修改。

### 5.2 `CredentialService` 多密钥池轮询与 429/5xx 冷却熔断切换

> **Java 源码对照**：`CredentialService.java:L140-L284,L297-L440`、`EndpointFailures.java:L12-L60`。

```mermaid
flowchart TD
    Req["运行期 / ModelCall 请求 ResolveCurrent(ctx, providerKey, excludeID)"] --> List["PG 读池: deleted=false, enabled=true<br/>ORDER BY priority ASC, created_at ASC"]
    List --> Empty{pool 为空?}
    Empty -- "是" --> NoKey["返回 403 NoCompanyKeyError<br/>严禁回退宿主机环境变量"]
    Empty -- "否" --> Pick["遍历 pool: 选首个 Usable() 且 未命中进程内冷却熔断表"]
    Pick --> Found{找到健康未冷却 Key?}
    Found -- "是" --> Decrypt["SecretCipher.Decrypt(v2:companyID:...)<br/>空 BaseURL 补齐 Catalog.DefaultBaseURL"]
    Found -- "否" --> Fallback["兜底: 选冷却最早到期 / priority 最高 pool[0]"]
    Fallback --> Decrypt

    Fail["调用遇零输出错误 -> Failover(used, errMsg, retryAfter)"] --> Match{IsEndpointFailure(errMsg)?}
    Match -- "否" --> NoSwitch["返回 (nil, false, nil)<br/>暴露原始业务/模型错误"]
    Match -- "是" --> Trans{IsTransientRateOr5xx?}
    Trans -- "是 (429/5xx)" --> Cool["写入进程内 memcache 冷却窗<br/>max(retryAfter, 30s * 2^strikes, cap 10m)<br/>+ 持久化 status=error, last_error"]
    Trans -- "否 (401/402/403 等)" --> MarkErr["持久化 status=error, last_error<br/>等待 10m 巡检或人工修复"]
    Cool --> Next["ResolveCurrent(ctx, providerKey, excludeID=used.ID)"]
    MarkErr --> Next
    Next --> Switched{新 Key 存在且 != used.ID?}
    Switched -- "是" --> Notify["触发 onSwitch 回调 -> 返回 (next, true, nil)"]
    Switched -- "否" --> NoSwitch
```

```go
package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

var (
	endpointStatusRe = regexp.MustCompile(`(?i)(api error|status|http|code)[^\n]{0,16}?\b(401|402|403|429|500|502|503|504|529)\b`)
	transientStatusRe = regexp.MustCompile(`(?i)(api error|status|http|code)[^\n]{0,16}?\b(429|500|502|503|504|529)\b`)
	word404Re        = regexp.MustCompile(`\b404\b`)

	// 1:1 精确对齐 EndpointFailures.java:L18-L28（39 条短语）
	endpointPhrases = []string{
		"invalid api key", "invalid x-api-key", "invalid_api_key", "incorrect api key", "authentication_error",
		"authentication failed", "unauthorized", "forbidden", "api key not valid",
		"failed to authenticate", "oauth session expired", "oauth token has expired",
		"rate limit", "rate_limit", "too many requests", "quota", "insufficient_quota", "credit balance",
		"billing", "额度", "余额不足", "欠费",
		"overloaded", "service unavailable", "bad gateway", "gateway timeout", "internal server error",
		"econnrefused", "econnreset", "enotfound", "etimedout", "connection refused", "connection error",
		"fetch failed", "socket hang up", "could not resolve host", "error sending request", "network error",
		"无法连接",
	}

	transientPhrases = []string{
		"rate limit", "rate_limit", "too many requests", "overloaded",
		"service unavailable", "bad gateway", "gateway timeout", "internal server error",
		"econnrefused", "econnreset", "etimedout", "connection refused", "connection error",
		"fetch failed", "socket hang up", "error sending request", "network error", "无法连接",
	}

	// 1:1 精确对齐 EndpointFailures.java:L34-L36（12 条短语）
	modelGonePhrases = []string{
		"model not found", "model_not_found", "not_found_error",
		"does not exist", "no such model", "unknown model", "invalid model", "model is not supported",
		"not supported model", "deprecated", "模型不存在", "模型不可用",
	}
)

type cooldownState struct {
	Until   time.Time
	Strikes int
}

func (s *CredentialService) ResolveCurrent(ctx context.Context, providerKey, excludeCredID string) (*domain.RunCredential, error) {
	if _, ok := s.catalog.Find(providerKey); !ok {
		return nil, nil
	}
	cid, err := tenant.Require(ctx)
	if err != nil {
		return nil, err
	}
	all, err := s.store.List(ctx, providerKey)
	if err != nil {
		return nil, err
	}
	var pool []domain.ProviderCredential
	for _, c := range all {
		if c.Enabled && c.ID != excludeCredID {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		return nil, &NoCompanyKeyError{ProviderKey: providerKey}
	}
	now := s.nowFn()
	// 1. 优先返回首个状态健康且未处于进程内冷却期的最高优先级 Key
	for _, c := range pool {
		if c.Usable() && !s.inCooldown(cid, providerKey, c.ID, now) {
			return s.toRunCredential(ctx, c)
		}
	}
	// 2. 次优：状态非 error 但处于短时冷却的 Key 中最早解除冷却者；否则兜底最高优先级 pool[0]
	bestIdx := 0
	var earliest time.Time
	for i, c := range pool {
		if cd, ok := s.cooldowns.Get(cooldownKey(cid, providerKey, c.ID)); ok {
			if earliest.IsZero() || cd.Until.Before(earliest) {
				earliest = cd.Until
				bestIdx = i
			}
		}
	}
	return s.toRunCredential(ctx, pool[bestIdx])
}

func (s *CredentialService) FailoverWithRetryAfter(
	ctx context.Context,
	providerKey string,
	used *domain.RunCredential,
	errMsg string,
	retryAfter time.Duration,
) (*domain.RunCredential, bool, error) {
	if _, ok := s.catalog.Find(providerKey); !ok || !IsEndpointFailure(errMsg) {
		return nil, false, nil
	}
	cid, err := tenant.Require(ctx)
	if err != nil {
		return nil, false, err
	}
	if used != nil && used.ID != "" {
		if IsTransientRateOr5xx(errMsg) {
			s.recordCooldown(cid, providerKey, used.ID, retryAfter)
		}
		if cur, err := s.store.Find(ctx, used.ID); err == nil && cur != nil {
			now := s.nowFn().UTC()
			cur.Status = domain.CredStatusError
			cur.LastError = truncateRunes(strings.TrimSpace(errMsg), 990)
			cur.CheckedAt = &now
			cur.UpdatedAt = now
			_ = s.store.Save(ctx, *cur)
		}
	}
	excludeID := ""
	if used != nil {
		excludeID = used.ID
	}
	next, err := s.ResolveCurrent(ctx, providerKey, excludeID)
	if err != nil {
		var noKey *NoCompanyKeyError
		if errors.As(err, &noKey) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if next == nil || (used != nil && next.ID == used.ID) {
		return nil, false, nil
	}
	s.notifySwitch(ctx, providerKey)
	return next, true, nil
}

func (s *CredentialService) recordCooldown(companyID, providerKey, credID string, retryAfter time.Duration) {
	key := cooldownKey(companyID, providerKey, credID)
	strikes := 1
	if prev, ok := s.cooldowns.Get(key); ok {
		strikes = prev.Strikes + 1
	}
	backoff := s.cdCfg.InitialBackoff << min(strikes-1, 4) // 30s -> 60s -> 120s -> 240s -> 480s
	if backoff > s.cdCfg.MaxBackoff {
		backoff = s.cdCfg.MaxBackoff
	}
	if retryAfter > backoff {
		backoff = min(retryAfter, s.cdCfg.MaxBackoff)
	}
	s.cooldowns.Set(key, cooldownState{Until: s.nowFn().Add(backoff), Strikes: strikes}, backoff)
}

func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return hex.EncodeToString(sum[:])
}
```

- **巡检与恢复语义 (`CheckAll`)**：
  - `dueForCheck(c)`：`c.Status != "ok" || c.CheckedAt == nil || c.CheckedAt.Add(1*time.Hour).Before(now)`（故障/未知每 10 分钟复检，健康每 1 小时复检）；
  - 探活成功时清除 `cooldowns` 对应冷却条目并持久化 `status="ok", last_error=""`；
  - 检测前后比较当前生效 Key ID，若高优 Key 恢复并夺回主用位置，触发 `notifySwitch(ctx, providerKey)` 驱动模型目录与全量推理重探。
- **管理 CRUD 语义**：
  - `Add`：校验非空 Key、同供应商池内活跃 `key_hash` 不重复、`priority = max + 1`、`claude` 默认 `bearer = true`（其余默认 `false`）、落库后 `notifySwitch` + 经 `BackgroundGroup` 异步触发 `checkSoon`（先 `tenant.Detach(ctx)` 继承租户上下文）；
  - `Update`：未传 `apiKey` 保留原 `key_cipher` 与 `key_hash`；任意变更重置 `status = "unknown"` 并清除旧冷却后异步复检；
  - `Reorder`：按请求 `ids` 重排 `1..N`，请求遗漏的既有 Key 保持原相对顺序排在末尾；
  - `MaskKey(k)`：`< 12` 字符返回 `"••••"`，否则 `k[:4] + "…" + k[len-4:]`（按 rune 安全截断）。

### 5.3 `ProbeService`：本地探测、模型拉取与不误杀的真实推理探活

> **Java 源码对照**：`ProviderProbe.java:L46-L185`、`ProviderModelFetcher.java:L220-L350`、`ProviderInferenceProbe.java:L87-L147,L369-L473,L517-L585`。

1. **`ProviderProbe`**：
   - `Version`：若 `entry.NPMPackage != ""`，先对 `filepath.EvalSymlinks(binary)` 向上逐级查找 `package.json` 且 `name == entry.NPMPackage` 直接读取 semver（免起进程，3s 周期零开销）；失败则经 `spi.ProcessLauncher` 执行 `<binary> <versionArgs>`（8s 超时）。
   - `LatestNPMVersion`：经 `spi.ProcessLauncher` 执行 `npm view <npmPackage> version`（30s 超时，`CliPaths` 自动注入 `PATH`/`NPM_CONFIG_REGISTRY`/代理/`NODE_EXTRA_CA_CERTS`）。
   - `IsNewerSemver`：正则 `(\d+)\.(\d+)\.(\d+)` 严格三段数值比较。
2. **`ProviderModelFetcher`（复用 L0 `TrustedClient`）**：
   - `claude`：通过 `trustedClient`（`Service: "llm_models"`, `TotalTimeout: 20s`）分页请求 `GET <base>/v1/models?limit=1000`（上限 20 页，Header `anthropic-version: 2023-06-01`，`Authorization: Bearer` 或 `x-api-key`），用 `NON_CHAT` 过滤非对话模型，标注 `nativeModel = strings.HasPrefix(id, "claude-")`，优先解析 `capabilities.effort`（`effortSource="model"`，按 `minimal/low/medium/high/xhigh/max/ultra` 排序），原生模型缺失能力字段时回退解析 `claude --help`（`effortSource="cli"`），最终按 `nativeModel DESC, id ASC` 排序；
   - `codex`：经 `spi.ProcessLauncher` 执行 `codex debug models -c model_provider="openai"`（60s 超时），忽略 `visibility == "hide"`，提取 `slug`/`display_name`/`supported_reasoning_levels`/`default_reasoning_level`/`context_window`；
   - 其他厂商：先查 `customFetchers[providerKey]` 扩展注册表，未注册返回明确错误。
3. **`ProviderInferenceProbe` & `*ProbeUnreachableError`（复用 L0 `TrustedClient`）**：
   - **探活专用 `TrustedClient`**：启用企业代理与私有 CA，**显式配置 `Retry.MaxAttempts = 1`**（避免探活单次失败被 HTTP 层静默重试掩盖真实延迟或误耗 Token）；
   - **`CheckKey`**：
     - `codex`（及 OpenAI 协议扩展）：先发零 Token `GET <base>/models`（30s，`Bearer`）——`200` 直接通过，`401/403` 直接失败，其余状态码降级走最小推理；
     - `claude`：若 `modelID == ""` 返回 `"还没有可用来测试的模型（等模型列表刷新后再检查）"`，否则走 `Check(..., "key_check")`；
   - **`Check`**：
     - `claude`：`POST <base>/v1/messages`（`model`, `max_tokens: 8`, `messages: [{role:"user", content:"你好"}]`），验 `content` 为数组，提取 4 维 `TokenUsage`；
     - `codex`：`POST <base>/responses`（`model`, `input: "你好"`, `max_output_tokens: 16`），提取 `usage` 并按 `uncached = max(0, input_tokens - cached_tokens)` 拆分；
   - **不可达分类**：捕获 L0 `httpclient.KindOf(err)` 为 `KindNetwork` / `KindTimeout` / `KindCircuitOpen` 时，统一返回 `&ProbeUnreachableError{...}`；HTTP 非 200 时用 `cause(status)`（401/402/403/404/429/5xx 中文说明）+ 响应 `error.message` 构造普通失败；
   - **记账**：每次推理探活成功结束尽力调用 `usage.Record(ctx, UsageContext{User: "system", Source: source, CLI: providerKey}, []TokenUsage{used})`。

### 5.4 `ProviderService` 并发探测与 `ProviderStatusChecker` 四节奏调度

> **Java 源码对照**：`ProviderService.java:L99-L248,L435-L694`、`ProviderStatusChecker.java:L51-L148`。

- **`onSwitch` 联动**：订阅 `credentials.OnSwitch("models", ...)`，按 `companyID+"/"+providerKey` 记录上次生效 Key ID，仅当首次添加 Key（且原模型列表为空）或实际发生 Key 切换时，经 `BackgroundGroup` 异步执行 `RefreshModels -> RefreshInference(freshEnough=0)`。
- **6 并发 `Sweep` 与不可达保护**：
  - `checking[companyID+"|"+providerID]` 防重入；
  - 新鲜窗口：定时巡检 `50m`、手动 Refresh `1m`、新增/切 Key `0`（全检）；
  - 使用容量 6 的信号量 channel + `sync.WaitGroup`，每个模型 goroutine 携带 `tenant.Detach(ctx)` 执行 `inferenceProbe.Check`；
  - 命中 `*ProbeUnreachableError` 计入 `unreachableCount` 且**不覆盖该模型原 `inferStatus`**；命中普通错误写入 `inferStatus="error"`；
  - 在 `writeMu` 保护下将新结果按 `id` 归并回数据库最新模型列表，返回 `reached = (unreachableCount == 0)`。
- **`ProviderStatusChecker.RegisterLoops(bg)`**：
  - 4 个 `lifecycle.Loop` 全部挂载至 L0 `lifecycle.BackgroundGroup`（享受统一 panic recover 与优雅停机取消）：
    1. `provider-local-check`：每 `3s`（最小 `1s`）跨所有启用租户执行 `RefreshLocal`；
    2. `provider-startup-warmup`：一次性协程，启动即对所有启用租户顺序跑一遍 `RefreshLatest + RefreshLocal + RefreshModels + RefreshInference`；
    3. `provider-latest-models`：每 `3600s`（最小 `60s`）执行 `RefreshLatest + RefreshLocal + RefreshModels`；
    4. `provider-inference-sweep`：每 `3600s`（最小 `60s`）执行 `RefreshInference`；某租户返回 `reached == false` 时，经 `retryPending` 按租户去重并在 `300s` 后启动单租户定时重试（监听 `bg.Context().Done()`）；
    5. `provider-credential-check`：初始延迟 `60s`、每 `600s`（最小 `60s`）执行 `credentials.CheckAll`。

### 5.5 `ModelCall` 轻量直调与调用方 Key 降级切换

> **Java 源码对照**：`ModelCall.java:L29-L103`；架构升级满足 L0 `SA-03` 职责边界（HTTP 客户端只分类不降级，由 `ModelCall` 作为调用方完成 Key 池降级切换）。

```go
package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/domain"
	"github.com/Karlsk/synapse-os/internal/infra/httpclient"
)

const maxModelCallFailovers = 4

func (m *ModelCall) Ask(ctx context.Context, model, prompt string, timeout time.Duration, paying *domain.UsageContext) (string, error) {
	cli, ok := m.providers.CLIFor(ctx, model)
	if !ok {
		return "", fmt.Errorf("No CLI can run %s", model)
	}
	entry, ok := m.catalog.Find(cli)
	if !ok || (entry.Access.Protocol != ProtocolAnthropic && entry.Access.Protocol != ProtocolOpenAI) {
		return m.AskViaCLI(ctx, model, prompt, timeout, paying)
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cred, err := m.creds.Current(callCtx, cli)
	if err != nil {
		return "", err
	}

	// 由调用方显式控制多 Key 降级切换，L0 TrustedClient 仅负责传输与错误分类
	for attempt := 0; attempt <= maxModelCallFailovers; attempt++ {
		out, usage, retryAfter, callErr := m.doDirectHTTP(callCtx, entry, model, prompt, cred)
		if callErr == nil {
			m.recordUsage(callCtx, cli, paying, usage)
			if strings.TrimSpace(out) == "" {
				return "", fmt.Errorf("%s gave no answer", model)
			}
			return out, nil
		}
		if callCtx.Err() != nil {
			return "", fmt.Errorf("%s didn't answer within %ds", model, int(timeout.Seconds()))
		}
		// 仅在端点故障（401/402/403/429/5xx/断连/熔断）时尝试切换下一个可用 Key
		next, switched, fErr := m.creds.FailoverWithRetryAfter(callCtx, cli, cred, callErr.Error(), retryAfter)
		if fErr != nil || !switched || next == nil {
			return "", callErr
		}
		cred = next
	}
	return "", fmt.Errorf("%s failed after exhausting available API keys", model)
}

func (m *ModelCall) doDirectHTTP(
	ctx context.Context,
	entry CatalogEntry,
	model, prompt string,
	cred *domain.RunCredential,
) (answer string, used *domain.TokenUsage, retryAfter time.Duration, err error) {
	req, err := buildDirectRequest(ctx, entry, model, prompt, cred)
	if err != nil {
		return "", nil, 0, err
	}
	resp, err := m.trustedClient.Do(req)
	if err != nil {
		var ce *httpclient.ClientError
		if errors.As(err, &ce) {
			return "", nil, ce.RetryAfter, fmt.Errorf("HTTP %d %s: %w", ce.StatusCode, ce.Kind, ce)
		}
		return "", nil, 0, err
	}
	defer resp.Body.Close()

	body, rerr := httpclient.ReadLimited(resp.Body, 4<<20) // 4 MiB 上限防异常大响应
	if rerr != nil {
		return "", nil, 0, rerr
	}
	if resp.StatusCode != http.StatusOK {
		ra := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
		return "", nil, ra, fmt.Errorf("HTTP %d: %s", resp.StatusCode, extractAPIErrorMessage(body))
	}
	return parseDirectResponse(entry.Access.Protocol, model, body)
}
```

- `AskViaCLI` 完整承接 Java `ModelCall.java:L57-L102`：通过 `RunnerRegistry.Get(AgentType(cli))` 创建 `os.MkdirTemp("", "sa-ask-*")` 临时工作目录（`defer os.RemoveAll`），使用 `context.WithTimeout` 控制超时，`AllowedTools: nil`, `SkipPermissions: false`, 经 `UsageRecorder.Tap` 收集 Token，并在零输出端点错误时同样调用 `creds.Failover` 切换重试一次。

---

## 6. 任务清单（TDD 粒度）

- [ ] T1: 核对 `internal/core/{domain,api}/provider.go` 结构体与 JSON camelCase tag，确保与 `ProviderDto`/`ProviderModelDto`/`CatalogEntryDto`/`CredentialDto` 100% 一致
- [ ] T2: 编写 `catalog_test.go` 与 `endpoint_failures_test.go` 并实现 `catalog.go`（V1 仅 `claude` & `codex` + `Register` 扩展）与 `endpoint_failures.go`（完整正则 + 39 短语 + 12 模型短语 + 瞬态分类）
- [ ] T3: 编写 `credential_service_test.go` 并实现 `credential_service.go`（DEK 密文存储、严格多租户 `NoCompanyKeyError`、429/5xx 进程内 `memcache` 冷却熔断表、`FailoverWithRetryAfter`、`CheckAll` 10m/1h 复检、实现 `gateway.CredentialResolver`）
- [ ] T4: 编写 `probe_test.go`、`model_fetcher_test.go`、`inference_probe_test.go` 并实现 `probe.go`、`api_access.go`、`model_fetcher.go`、`inference_probe.go`（复用 L0 `TrustedClient`、`*ProbeUnreachableError`、Anthropic `/v1/messages` 与 Codex `/responses` 探活及 `UsageRecorder` 记账）
- [ ] T5: 编写 `provider_service_test.go` 与 `status_checker_test.go` 并实现 `provider_service.go`（`CLIInstaller` 窄接口、`writeMu` 串行化、6 并发 `Sweep`、`CLIFor` `nativeModel` 优先）与 `status_checker.go`（4 节奏 + 5 分钟单租户去重重试 + 挂入 L0 `BackgroundGroup`）
- [ ] T6: 编写 `model_call_test.go` 并实现 `model_call.go`（复用 L0 `TrustedClient` 轻量直调 + 调用方 429/5xx/401 主动 Key 降级切换 + `AskViaCLI` 兜底）
- [ ] T7: 运行 `go test -race -cover ./internal/platform/provider/...` 与 `internal/arch_test.go` 全绿验证

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 看板：合入后将 `SA-18` 状态更新为 `已实施（commit <sha>）`
- [ ] `docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §1.3：同步确认 `CredentialService` 429/5xx 冷却熔断与 `gateway.CredentialResolver` 接线细节
- [ ] `configs/config.example.yaml`：核对 `provider.checker.{enabled,local_interval_seconds,latest_interval_seconds,infer_interval_seconds,credential_interval_seconds}` 与 `provider.cooldown.{initial_backoff,max_backoff}` 配置注释

---

## 8. 测试清单

| 测试点 | 落位 | 关键断言 |
|---|---|---|
| `CatalogRegistry` V1 GA 锁定与扩展点 | `internal/platform/provider/catalog_test.go` | `NewDefaultCatalog().All()` 仅含 `claude` & `codex`；`Register` 可扩展新供应商并拒绝重复 Key；命令仅静态生成 |
| `EndpointFailures` 全规则与瞬态分类 | `internal/platform/provider/endpoint_failures_test.go` | 覆盖 `API Error: 429`/`HTTP 529`/`401`/`quota`/`额度`/`ECONNREFUSED` 等 39 条短语；`IsTransientRateOr5xx` 精准区分 `429/503` 与 `401/403`；普通工具错误返 `false`；`IsModelFailure` 识别 `404` 与 12 条模型下架短语 |
| `CredentialService` 租户隔离、密文与 429/5xx 冷却熔断切换 | `internal/platform/provider/credential_service_test.go` | 空池必返 `403 NoCompanyKeyError`（即使宿主机设 `ANTHROPIC_API_KEY`）；跨租户密文解密报错脱敏为 `"（无法解密）"`；429/503 写入进程内 `memcache` 冷却窗（支持 `Retry-After`）并切下一高优 Key + 触发 `onSwitch`；软删后 `KnownKeyHashes` 仍保留哈希；`CheckAll` 按 10m/1h 复检并在恢复后清除冷却与切换回高优 Key；`MaskKey`/`NormalizeBaseURL` 边界全覆盖 |
| `ProviderProbe` 与 `ProviderModelFetcher` | `internal/platform/provider/model_fetcher_test.go` | 优先从 `EvalSymlinks` 向上读 `package.json` 版本；`IsNewerSemver` 严格 3 段数值比较；Claude 分页 2 页拉取、过滤 `NON_CHAT`、`capabilities.effort` 与 `--help` 回退、`nativeModel` 排序；Codex 解析 `debug models` 过滤 `visibility="hide"`；自定义 `RegisterFetcher` 生效 |
| `ProviderInferenceProbe` 与 `*ProbeUnreachableError` | `internal/platform/provider/inference_probe_test.go` | `codex` `CheckKey` 优先零 Token `GET /models`（200 过、401 败、404 降级 `/responses`）；`claude` 走 `/v1/messages` 校验 `content` 数组；`httptest` 断连/超时/熔断返回 `*ProbeUnreachableError`；`UsageRecorder` 记账到 `user="system"` |
| `ProviderService` 写锁串行化与 6 并发 `Sweep` 保活 | `internal/platform/provider/provider_service_test.go` | `Sweep` 遇 `*ProbeUnreachableError` 保留模型原 `ok` 状态且返回 `reached=false`；普通错误写 `inferStatus="error"`；并发刷新列表与探测写回不互覆；`CLIFor` 优先 `nativeModel == true` |
| `ProviderStatusChecker` 4 节奏与 5 分钟重试 | `internal/platform/provider/status_checker_test.go` | 某租户 `reached=false` 仅排期 1 次单租户重试且不阻塞其他租户；`BackgroundGroup.Stop` 干净退出无泄漏 |
| `ModelCall` 轻量直调与 Key 降级切换 | `internal/platform/provider/model_call_test.go` | 模拟 Key1 返 `429`（带 `Retry-After: 45`）或 `503`，`ModelCall` 主动调用 `Failover` 切至 Key2 成功返回答案并记账；全部 Key 耗尽返回清晰错误；`ProtocolOther` 回落 `AskViaCLI` 并删除 `sa-ask-*` 临时目录 |

---

## 9. 验收门

- [ ] `go build ./... && go test -race -cover ./internal/platform/provider/...` 全绿（零 data race，包语句覆盖率 ≥ 80%）
- [ ] §2 交付物表 8 项逐一存在且与 `internal/platform/provider/` 路径一致
- [ ] `internal/arch_test.go` 通过：零 Redis 依赖、零直引 `internal/storage/postgres`、零自建裸 `http.Client`（全部出站请求走 L0 `httpclient.TrustedClient`）、零裸 `os/exec`（全部子进程走 `spi.ProcessLauncher`）
- [ ] V1 门禁：`NewDefaultCatalog()` 仅含 `claude` & `codex`；多租户无 Key 必返 `403 NoCompanyKeyError` 绝不回退宿主机 env
- [ ] 前置篇 `SA-03`、`SA-07`、`SA-14` 回归测试全绿

---

## 10. 可提交节点

- **建议主 commit**：`feat(provider): 实现 ProviderCatalog(claude/codex)、租户密钥池冷却熔断切换、探活与 ModelCall 轻量直调（spec 18，PR SA-18）`
- **推荐分层拆分提交（可选）**：
  1. `feat(provider): 实现 CatalogRegistry、EndpointFailures 与 CredentialService 多密钥冷却熔断池`
  2. `feat(provider): 实现 ProviderProbe、ProviderModelFetcher、ProviderInferenceProbe 与 ProviderService/StatusChecker`
  3. `feat(provider): 实现复用 L0 TrustedClient 且支持调用方 Key 降级切换的 ModelCall`
