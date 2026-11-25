# spec 05 | Platform 供应商、私有 Node、密钥池与飞书/钉钉/自定义 MCP 连接器

- **状态**：`draft`
- **篇号顺序**：05（L2 平台治理层；依赖 `SA-01`/`SA-02`/`SA-03` 及 `SA-04` 的多租户轮转与设置服务）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-05`
- **上位契约**：`docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §1、§4.1、§5.1；`docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §1–§4；`docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §1.3、§4；`docs/SuperAgent-Go/DDD/README.md` §1–§3
- **前置依赖**：`L1/spec_01_core_tenant_crypto_egress.md`（`SA-01`：`SecretCipher`/`eventbus`/`tenant`/`apperror`）、`L1/spec_02_storage_postgres_migrations.md`（`SA-02`：PG17 建表迁移与 Store 实现）、`L1/spec_03_runtime_launcher_claude_codex_mcp.md`（`SA-03`：`ProcessLauncher`/`CliPaths`/`RunnerRegistry`/`McpGrants`/`McpClient`/`McpToolProvider`）、`L2/spec_04_platform_tenant_auth_user_settings_notice.md`（`SA-04`：`CompanyService.ForEachEnabled`/`SettingsService.PublicURL`）
- **下游消费者**：`SA-07`（`ModelPriceService`/`TaskModelRouter` 消费 `ProviderService`、`ModelCall`、`EndpointFailures`）、`SA-08`（`FeishuDocTools`/`DingTalkDocTools` 消费 `IntegrationService`、`ConnectorService.UserToken`、`FeishuDocsOAuthClient.Base`）、`SA-09`（`ChatService` 消费 `CredentialService`/`ModelGateway`、`ProviderService`、`ConnectorService`）、`SA-10`（`TaskService`）、`SA-11`（`FeishuIMConnector`/`DingTalkIMConnector` 监听 `IntegrationChanged` 并回报 `IntegrationService.SetStatus`）、`SA-13`（Web Controllers 与 OAuth 回调路由）、`SA-14`（`wire.go` 装配与 `ProviderStatusChecker` 优雅停机）
- **重构参照源码**：`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-platform/src/main/java/com/superagent/platform/{provider,integration,connector}/*`、`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-core/src/main/java/com/superagent/core/{provider,connector,integration}/*`、`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-web/src/main/java/com/superagent/web/{provider,integration,connector}/*`

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

本篇覆盖 `synapse-server` 平台层两大核心治理域：
1. **模型供应商、私有 Node 与多 Key 故障转移域 (`internal/platform/provider`)**：让私有化企业管理员在 Web 控制台一键安装/升级模型 CLI（无 `sudo`，缺 Node 或无写权限时自动安装私有 Node LTS 并支持企业代理、国内镜像源与自签根证书）、维护租户隔离的优先级加密 API Key 池（零输出端点故障自动切换下一 Key + 定时恢复探测）、自动拉取模型列表与后台真实推理就绪探测（网络瞬断不误杀模型状态），并为成本路由/记忆/标签提供统一的 `ModelCall` 工具；
2. **企业集成与 MCP 连接器域 (`internal/platform/integration`, `internal/platform/connector`)**：面向中国及跨国商业私有化客户，聚焦**飞书 / Lark 与钉钉双企业生态** + **管理员自由接入的通用自定义 MCP 连接器**（`STDIO` / `HTTP` / `SSE`，支持无认证、公司共享 Token 与员工个人 Token），提供员工级飞书文档 OAuth 授权与防重放单次 `state` / 提前 10 分钟并发安全刷新，并向每回合 Agent 运行动态装配 `McpServerSpec` 与 `ConnectorToolsService` 只读工具清单。

### 1.2 本篇边界与 V1 锁定裁剪清单

- **供应商目录 (`ProviderCatalog`)**：
  - V1 改为可扩展 `CatalogRegistry`，**默认仅发货 `claude` 与 `codex` 两套 GA 条目**（对应 `SA-03` 两大 GA Runner）；
  - 保留严格静态注册安全模型（安装/升级命令仅由代码注册，绝不接受来自 HTTP 请求的命令字符串），未来接入 DeepSeek / Qwen / Kimi / Gemini / GLM / CodeBuddy 等只需注册目录项与 Runner 即可生效；
  - `CliConfigWriter` 与 `MachineCredentials`：在多租户私有化默认形态下保持关闭/不自动导入宿主机密钥，确保**严格租户隔离**（空池返回 `403 NoCompanyKeyError`，绝不静默回退宿主机环境变量或 CLI 登录态）。
- **企业集成 (`IntegrationService`)**：
  - **V1 仅保留 `feishu` 与 `dingtalk`**；
  - **彻底砍掉** `github`、`gitee`、`notion`、`confluence` 4 套公网消费级/国外 SaaS 集成定义与 `siteUrl` 特判。
- **连接器与 OAuth (`ConnectorService`, `ConnectorOAuthService`, `ConnectorToolsService`)**：
  - **V1 预置系统连接器仅保留 2 个**：
    1. `feishu-docs`（`OAUTH`，进程内 `OWN_TOOLS`，由 `SA-08` `FeishuDocTools` 直接消费 `ConnectorService.UserToken`）；
    2. `dingtalk-tools`（`STDIO`：`npx -y dingtalk-mcp@latest`，`NONE`，默认**关闭**，启用且 `dingtalk` 集成就绪时自动注入 `DINGTALK_Client_ID`、`DINGTALK_Client_Secret`、`ACTIVE_PROFILES`）；
  - **保留列表顶部 3 个合成状态行**：`superagent`（内置 MCP 自检）、`feishu`（IM 状态 + 8 位绑定码）、`dingtalk`（IM 状态 + 8 位绑定码）；
  - **保留通用自定义 MCP 连接器**（管理员 CRUD：`STDIO` / `HTTP` / `SSE` × `NONE` / `SHARED_TOKEN` / `PERSONAL_TOKEN`）；
  - **彻底砍掉** `github`、`gitee`、`notion`、`confluence` 种子逻辑、`ConfluenceSite` 及 `GitHubOAuthClient`、`GiteeOAuthClient`、`NotionOAuthClient`（保留 `OAuthProvider` 接口，V1 注册 `FeishuDocsOAuthClient`）。

### 1.3 Java 原版源码精确映射表

| 目标 Go 组件 | Java 参照源码（绝对路径均基于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`） | 核心行号范围 | V1 裁剪 / 增强说明 |
|---|---|---|---|
| `provider.Catalog` | `superagent-core/src/main/java/com/superagent/core/provider/ProviderCatalog.java` | `L19-106`（`claude:64-66`, `codex:67-68`） | 可扩展注册表；V1 GA 默认仅注册 `claude` & `codex` |
| `provider.NodeRuntimeService` | `superagent-platform/src/main/java/com/superagent/platform/provider/NodeRuntimeService.java` | `L43-336`（`ensure:119-133`, `installLatestLts:167-195`, `download/STALL:243-295`, `verify:302-316`） | 新增 `node_dist_mirror`、`HTTPS_PROXY`、`ca_bundle_file` 自定义 TLS 与代理支持 |
| `provider.InstallJob`, `InstallFailures` | `superagent-platform/src/main/java/com/superagent/platform/provider/{InstallJob.java,InstallFailures.java}` | `InstallJob.java:11-57`, `InstallFailures.java:13-79` | 1:1 保真 400 行环缓冲与 7 类 npm 错误码中文诊断 |
| `provider.ProviderProbe` | `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderProbe.java` | `L27-185`（`packageVersion:56-71`, `version:74-92`, `latestNpmVersion:115-141`, `isNewer:144-159`） | 经 `ProcessLauncher` / `CliPaths` 执行，自动注入 `NPM_CONFIG_REGISTRY` 与代理/CA |
| `provider.ProviderModelFetcher`, `ApiAccess` | `superagent-platform/src/main/java/com/superagent/platform/provider/{ProviderModelFetcher.java,ApiAccess.java}` | `ProviderModelFetcher.java:42-388`（`codex:220-250`, `claude:254-350`）, `ApiAccess.java:13-68` | V1 主路径 `claude` + `codex` + 可插拔 `ModelFetcherFunc` 扩展点 |
| `provider.ProviderInferenceProbe`, `ProbeUnreachableError` | `superagent-platform/src/main/java/com/superagent/platform/provider/{ProviderInferenceProbe.java,ProbeUnreachableException.java}` | `ProviderInferenceProbe.java:42-615`（`checkKey:87-113`, `checkAnthropic:369-391`, `sendRaw:402-414`, `checkCodex:441-473`, `cliFailure/unreachable:517-563`） | 完整保真 `ProbeUnreachableError` 保护原状态 + `UsageRecorder` `system` 记账 |
| `provider.ProviderService`, `ProviderStatusChecker` | `superagent-platform/src/main/java/com/superagent/platform/provider/{ProviderService.java,ProviderStatusChecker.java}` | `ProviderService.java:50-705`（`runAction/execute/runCommand:249-420`, `refreshLocal/Latest/Models:435-499`, `refreshInference/sweep:511-602`, `cliFor:164-180`）; `ProviderStatusChecker.java:29-148` | `runCommand` 改由 `ProcessLauncher` 整组强杀（3m 静默 / 30m 总超时）+ EACCES 自动降级私有 Node；`FairRunner` 调度 |
| `provider.CredentialService`, `EndpointFailures` | `superagent-platform/src/main/java/com/superagent/platform/provider/{CredentialService.java,EndpointFailures.java}` | `CredentialService.java:43-440`（`failover:149-163`, `choose:170-186`, `checkAll/dueForCheck/check:212-261`, `add/update/reorder/mask/baseUrl:304-439`）; `EndpointFailures.java:12-60` | 实现 `SA-03` `gateway.CredentialResolver` 端口；严格多租户无 Key 返 `403 NoCompanyKeyError` |
| `provider.ModelCall` | `superagent-platform/src/main/java/com/superagent/platform/provider/ModelCall.java` | `L29-103` | `context.WithTimeout` 替代 `ScheduledExecutorService` + `Cancellation` |
| `integration.IntegrationService`, `AppUrl` | `superagent-platform/src/main/java/com/superagent/platform/integration/IntegrationService.java`、`superagent-web/.../integration/AppUrl.java` | `IntegrationService.java:30-247`, `AppUrl.java:15-36` | V1 仅保留 `feishu` & `dingtalk`；`IntegrationChanged` 走 `eventbus` |
| `connector.ConnectorService` | `superagent-platform/src/main/java/com/superagent/platform/connector/ConnectorService.java` | `L47-565`（`list/builtin/messagingRow:86-134`, `create/update/delete:138-169`, `authorize/revoke/newBindCode:182-223`, `mcpServersFor/specFor/spec:232-324`, `seedDefaults:389-475`） | V1 仅播种 `feishu-docs` & `dingtalk-tools` + 3 合成行 + 自定义 MCP；删 GitHub/Gitee/Notion/Confluence |
| `connector.ConnectorOAuthService`, `FeishuDocsOAuthClient`, `OAuthProvider` | `superagent-platform/src/main/java/com/superagent/platform/connector/{ConnectorOAuthService.java,FeishuDocsOAuthClient.java,OAuthProvider.java}` | `ConnectorOAuthService.java:37-212`, `FeishuDocsOAuthClient.java:25-122`, `OAuthProvider.java:10-50` | V1 注册 `FeishuDocsOAuthClient`；保留 `OAuthProvider` SPI；每 `<company>/<connector>/<owner>` 互斥锁双检刷新 |
| `connector.ConnectorToolsService` | `superagent-platform/src/main/java/com/superagent/platform/connector/ConnectorToolsService.java` | `L35-148` | 10 分钟缓存 + 内置 4 项与外部 `McpClient` 探测 |

---

## 2. 本篇交付物表

| # | 交付物 | 落位（相对 `synapse-server/`） | 类型 |
|---|---|---|---|
| 1 | 领域模型与 Store 接口补全（`ModelProvider`/`ProviderCredential`/`Connector`/`ConnectorCredential`/`IntegrationChanged`） | `internal/core/domain/{provider,connector}.go`、`internal/core/store/{provider,connector}.go`、`internal/core/eventbus/events.go`、`internal/core/api/{provider,integration,connector}.go` | 新增/核对 |
| 2 | `ProviderCatalog`（可扩展注册表，V1 默认 `claude` & `codex`） | `internal/platform/provider/catalog.go` | 新增 |
| 3 | `NodeRuntimeService`（私有 Node LTS、镜像/代理/CA、60s 停滞 watchdog、SHA-256、原子替换） | `internal/platform/provider/node_runtime.go` | 新增 |
| 4 | `InstallJob`（400 行环缓冲）+ `InstallFailures`（npm 错误码中文诊断） | `internal/platform/provider/install_job.go`、`install_failures.go` | 新增 |
| 5 | `ProviderProbe`、`ApiAccess`、`ProviderModelFetcher`、`ProviderInferenceProbe`（含 `ProbeUnreachableError`） | `internal/platform/provider/probe.go`、`api_access.go`、`model_fetcher.go`、`inference_probe.go` | 新增 |
| 6 | `EndpointFailures` + `CredentialService`（多 Key 池、Layer-1 故障切换、10m/1h 探测、实现 `gateway.CredentialResolver`） | `internal/platform/provider/endpoint_failures.go`、`credential_service.go` | 新增 |
| 7 | `ProviderService` + `ProviderStatusChecker` + `ModelCall` | `internal/platform/provider/provider_service.go`、`status_checker.go`、`model_call.go` | 新增 |
| 8 | `IntegrationService`（仅 `feishu` & `dingtalk`）+ `AppUrl` 回调基址解析器 | `internal/platform/integration/service.go`、`app_url.go` | 新增 |
| 9 | `ConnectorService`（`feishu-docs` + `dingtalk-tools` + 3 合成行 + 自定义 MCP） | `internal/platform/connector/service.go` | 新增 |
| 10 | `OAuthProvider` SPI + `FeishuDocsOAuthClient` + `ConnectorOAuthService` | `internal/platform/connector/oauth_provider.go`、`feishu_oauth.go`、`oauth_service.go` | 新增 |
| 11 | `ConnectorToolsService`（10 分钟缓存 + 内置/外部工具探测） | `internal/platform/connector/tools_service.go` | 新增 |
| 12 | 同包单元测试套件（内存 Fake Store + `httptest` 模拟 Anthropic/OpenAI/Node/Feishu OAuth/MCP） | `internal/platform/{provider,integration,connector}/*_test.go` | 新增 |

> **分层守卫检查**：`internal/platform/{provider,integration,connector}` 仅依赖 `internal/core/**`、`internal/runtime/{clipaths,launcher,runner,mcp,gateway}`（L1）及同层基础设施 `internal/platform/{tenant,settings}`（无环），**严禁依赖** `internal/storage`、`internal/agent`、`internal/task`、`internal/cost`、`internal/docs`、`internal/web`。

---

## 3. 数据库设计

本篇直接消费由 `SA-02`（`internal/storage/postgres/migrations/00005_init_assets_and_platform.sql`）落地并由 `internal/storage/postgres/store_*.go` 实现的 4 张核心表及 `system_setting` 表。以下逐表冻结 PG17 DDL、索引与本篇强依赖的列语义（所有表均严格满足 `company_id VARCHAR(64) NOT NULL` 位于主键首列，全时间戳使用 `TIMESTAMPTZ` UTC）：

### 3.1 `model_provider`

- **Java 参考**：`MysqlModelProviderStore.java:13-30`
- **选型说明**：`models_json` 在 PG17 中使用原生 `JSONB`（出口序列化为 JSON 数组 `[]`，禁止 `null`）；取消 MySQL `ensureColumn` 运行期动态 `ALTER`，统一由 `goose` 迁移管理。

```sql
CREATE TABLE model_provider (
    company_id        VARCHAR(64)   NOT NULL,
    id                VARCHAR(64)   NOT NULL,
    provider_key      VARCHAR(64)   NOT NULL, -- "claude" | "codex" | ...
    installed         BOOLEAN       NOT NULL DEFAULT FALSE,
    can_infer         BOOLEAN       NOT NULL DEFAULT FALSE,
    current_version   VARCHAR(64),
    latest_version    VARCHAR(64),
    status            VARCHAR(32)   NOT NULL, -- unsupported | not_installed | no_auth | ok | needs_upgrade | installed
    last_checked_at   TIMESTAMPTZ,
    models_json       JSONB         NOT NULL DEFAULT '[]'::jsonb,
    models_error      VARCHAR(1024),
    models_checked_at TIMESTAMPTZ,
    created_at        TIMESTAMPTZ   NOT NULL,
    updated_at        TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE UNIQUE INDEX uk_model_provider_key ON model_provider (company_id, provider_key);
```

### 3.2 `provider_credential`

- **Java 参考**：`MysqlProviderCredentialStore.java:10-30`、`AbstractJdbcProviderCredentialStore.java:22-82`
- **选型说明**：删除采用**软删除**（`deleted = TRUE`），从而 `KnownKeyHashes(ctx, providerKey)` 始终包含历史已删 Key 的 SHA-256 哈希，避免重复导入或冲突；`key_cipher` 存放 `SecretCipher` `v2:<companyId>:<base64>` 租户信封密文。

```sql
CREATE TABLE provider_credential (
    company_id   VARCHAR(64)   NOT NULL,
    id           VARCHAR(64)   NOT NULL,
    provider_key VARCHAR(32)   NOT NULL,
    label        VARCHAR(100)  NOT NULL,
    base_url     VARCHAR(512)  NOT NULL DEFAULT '',
    key_cipher   TEXT          NOT NULL,
    bearer       BOOLEAN       NOT NULL DEFAULT FALSE,
    priority     INTEGER       NOT NULL,
    enabled      BOOLEAN       NOT NULL DEFAULT TRUE,
    status       VARCHAR(16)   NOT NULL, -- ok | error | unknown
    last_error   VARCHAR(1000) NOT NULL DEFAULT '',
    checked_at   TIMESTAMPTZ,
    deleted      BOOLEAN       NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ   NOT NULL,
    updated_at   TIMESTAMPTZ   NOT NULL,
    source       VARCHAR(16)   NOT NULL DEFAULT 'console', -- console | machine
    key_hash     VARCHAR(64)   NOT NULL DEFAULT '',
    PRIMARY KEY (company_id, id)
);
CREATE INDEX idx_provider_credential_pool ON provider_credential (company_id, provider_key, deleted, priority, created_at);
```

### 3.3 `connector`

- **Java 参考**：`MysqlConnectorStore.java:13-27`

```sql
CREATE TABLE connector (
    company_id  VARCHAR(64)   NOT NULL,
    id          VARCHAR(64)   NOT NULL,
    name        VARCHAR(255)  NOT NULL,
    description VARCHAR(1024),
    kind        VARCHAR(16)   NOT NULL, -- MCP
    auth_method VARCHAR(32)   NOT NULL, -- NONE | SHARED_TOKEN | PERSONAL_TOKEN | OAUTH
    enabled     BOOLEAN       NOT NULL DEFAULT TRUE,
    transport   VARCHAR(16)   NOT NULL, -- STDIO | HTTP | SSE
    endpoint    VARCHAR(2048),          -- STDIO 命令行或 HTTP/SSE URL；OWN_TOOLS(feishu-docs) 为 NULL
    token_env   VARCHAR(128),           -- 仅 STDIO + 非 NONE 时有值
    created_at  TIMESTAMPTZ   NOT NULL,
    updated_at  TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, id)
);
```

### 3.4 `connector_credential`

- **Java 参考**：`MysqlConnectorCredentialStore.java:13-23`
- **主键语义**：`owner = ""`（`ConnectorCredentialCompany`）表示公司级 `SHARED_TOKEN`；`owner = <userId>` 表示员工级 `PERSONAL_TOKEN` / `OAUTH` 凭据。

```sql
CREATE TABLE connector_credential (
    company_id     VARCHAR(64)   NOT NULL,
    connector_id   VARCHAR(64)   NOT NULL,
    owner          VARCHAR(128)  NOT NULL, -- "" 表示公司共享，否则为 userId
    secret_cipher  VARCHAR(4096) NOT NULL, -- SecretCipher v2 密文（access_token / token）
    account_label  VARCHAR(255),           -- 员工展示名（如飞书用户名）
    updated_at     TIMESTAMPTZ   NOT NULL,
    refresh_cipher VARCHAR(4096),          -- SecretCipher v2 密文（refresh_token）
    expires_at     TIMESTAMPTZ,            -- access_token 过期时间（UTC）
    PRIMARY KEY (company_id, connector_id, owner)
);
```

### 3.5 `system_setting` 键命名空间与内存缓存表

| 命名空间 / 内存态 | 载体 | 键格式 / 策略 |
|---|---|---|
| 企业集成配置（仅 `feishu` / `dingtalk`） | `system_setting` 表 | `integration.feishu.{enabled,domain,appId,appSecret}`、`integration.dingtalk.{enabled,clientId,clientSecret}`（secret 字段经 `SecretCipher` 加密存密文） |
| 预置连接器单次播种标志 | `system_setting` 表 | `connector.seeded.feishu-docs = "true"`、`connector.seeded.dingtalk-tools = "true"` |
| 企业集成实时连接态 | 内存 `sync.RWMutex` Map | key = `<companyId>/<integrationKey>` -> `{State, Error}`（停用时删除） |
| 安装/升级日志 | 内存 `sync.Mutex` Map | `ProviderService.jobs[providerID]` / `NodeRuntimeService.lastJob`，每任务 400 行环形缓冲，重启清空 |
| OAuth 防 CSRF 临时状态 | 内存 `sync.Mutex` Map | `pendingStates[32-hex]`，TTL `10m`，消费即删（单次使用） |
| 外部 MCP 工具清单缓存 | 内存 `sync.RWMutex` Map | key = `<companyId>|<connectorId>|<viewer>`，TTL `10m`，出错自动驱逐 |

---

## 4. 冻结契约

### 4.1 导出符号（完整 Go Structs & Interfaces）

#### 4.1.1 领域模型与持久化 Store（`internal/core/domain` & `internal/core/store`）

```go
package domain

import "time"

const (
	ProviderStatusUnsupported  = "unsupported"
	ProviderStatusNotInstalled = "not_installed"
	ProviderStatusNoAuth       = "no_auth"
	ProviderStatusOK           = "ok"
	ProviderStatusNeedsUpgrade = "needs_upgrade"
	ProviderStatusInstalling   = "installing"
	ProviderStatusInstalled    = "installed"
)

type ModelProvider struct {
	ID              string
	ProviderKey     string
	Installed       bool
	CanInfer        bool
	CurrentVersion  string
	LatestVersion   string
	Status          string
	LastCheckedAt   *time.Time
	ModelsJSON      []byte // JSONB: []api.ProviderModelDto
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
	BaseURL     string // 空串表示使用目录默认 BaseURL
	KeyCipher   string
	Bearer      bool
	Priority    int
	Enabled     bool
	Status      string
	LastError   string
	CheckedAt   *time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string
	KeyHash     string // SHA-256 hex(strings.TrimSpace(apiKey))
}

func (c ProviderCredential) Usable() bool { return c.Enabled && c.Status != CredStatusError }

type ConnectorKind string
const ConnectorKindMCP ConnectorKind = "MCP"

type ConnectorAuthMethod string
const (
	AuthNone          ConnectorAuthMethod = "NONE"
	AuthSharedToken   ConnectorAuthMethod = "SHARED_TOKEN"
	AuthPersonalToken ConnectorAuthMethod = "PERSONAL_TOKEN"
	AuthOAuth         ConnectorAuthMethod = "OAUTH"
)

func (m ConnectorAuthMethod) Personal() bool { return m == AuthPersonalToken || m == AuthOAuth }

type Connector struct {
	ID          string
	Name        string
	Description string
	Kind        ConnectorKind
	AuthMethod  ConnectorAuthMethod
	Enabled     bool
	Transport   ConnectorTransport
	Endpoint    string // OWN_TOOLS 为空
	TokenEnv    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const ConnectorCredentialCompany = "" // 公司共享凭证的 owner 占位符

type ConnectorCredential struct {
	ConnectorID   string
	Owner         string
	SecretCipher  string
	AccountLabel  string
	UpdatedAt     time.Time
	RefreshCipher string
	ExpiresAt     *time.Time
}
```

```go
package store

type ModelProviderStore interface {
	List(ctx context.Context) ([]domain.ModelProvider, error)
	FindByID(ctx context.Context, id string) (*domain.ModelProvider, error)
	FindByKey(ctx context.Context, providerKey string) (*domain.ModelProvider, error)
	Save(ctx context.Context, p domain.ModelProvider) error
	Delete(ctx context.Context, id string) error
}

type ProviderCredentialStore interface {
	List(ctx context.Context, providerKey string) ([]domain.ProviderCredential, error) // deleted=false ORDER BY priority, created_at
	Find(ctx context.Context, id string) (*domain.ProviderCredential, error)           // deleted=false
	KnownKeyHashes(ctx context.Context, providerKey string) (map[string]struct{}, error) // 含已软删
	Save(ctx context.Context, c domain.ProviderCredential) error
	Delete(ctx context.Context, id string) error // 软删：SET deleted=TRUE, updated_at=NOW()
}

type ConnectorStore interface {
	ListConnectors(ctx context.Context) ([]domain.Connector, error)
	FindConnector(ctx context.Context, id string) (*domain.Connector, error)
	Save(ctx context.Context, c domain.Connector) error
	Delete(ctx context.Context, id string) error
}

type ConnectorCredentialStore interface {
	Find(ctx context.Context, connectorID, owner string) (*domain.ConnectorCredential, error)
	List(ctx context.Context, connectorID string) ([]domain.ConnectorCredential, error)
	Save(ctx context.Context, c domain.ConnectorCredential) error
	Delete(ctx context.Context, connectorID, owner string) error
	DeleteAll(ctx context.Context, connectorID string) error
}
```

#### 4.1.2 核心服务与可扩展接口（`internal/platform/{provider,integration,connector}`）

```go
// internal/platform/provider
const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
	ProtocolOther     = "other"
)
type AccessSpec struct {
	Protocol       string
	DefaultBaseURL string
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
func (e CatalogEntry) Installable() bool { return e.InstallCommand != "" }

type CatalogRegistry struct { /* mu sync.RWMutex; order []string; entries map[string]CatalogEntry */ }
func NewDefaultCatalog() *CatalogRegistry // V1 GA 预注册 claude & codex
func (c *CatalogRegistry) Register(entry CatalogEntry) error
func (c *CatalogRegistry) All() []CatalogEntry
func (c *CatalogRegistry) Find(key string) (CatalogEntry, bool)

type NodeRuntimeConfig struct {
	DistMirror   string // 默认 "https://nodejs.org/dist"
	CABundleFile string
	HTTPProxy    string
	HTTPSProxy   string
	NoProxy      string
}
type NodeRuntimeService struct { /* ... */ }
func (s *NodeRuntimeService) Status(ctx context.Context) api.NodeRuntimeDto
func (s *NodeRuntimeService) InstallAsync(ctx context.Context) api.NodeRuntimeDto
func (s *NodeRuntimeService) InstallLog() *api.InstallLogDto
func (s *NodeRuntimeService) Version(ctx context.Context) (string, bool)
func (s *NodeRuntimeService) HasPrivate() bool
func (s *NodeRuntimeService) PrivatePrefixFor(npmBinary string) (string, bool)
func (s *NodeRuntimeService) Ensure(ctx context.Context, minNode string, job *InstallJob) error
func (s *NodeRuntimeService) InstallPrivateNow(ctx context.Context, job *InstallJob) error

// CredentialService 实现 SA-03 的 gateway.CredentialResolver
type CredentialService struct { /* ... */ }
func (s *CredentialService) OnSwitch(listenerKey string, fn func(ctx context.Context, providerKey string))
func (s *CredentialService) HasOwnKey(ctx context.Context, providerKey string) (bool, error)
func (s *CredentialService) Current(ctx context.Context, providerKey string) (*domain.RunCredential, error)
func (s *CredentialService) ResolveCurrent(ctx context.Context, providerKey, excludeID string) (*domain.RunCredential, error)
func (s *CredentialService) Failover(ctx context.Context, providerKey string, used *domain.RunCredential, errMsg string) (*domain.RunCredential, bool, error)
func (s *CredentialService) CheckAll(ctx context.Context)
func (s *CredentialService) List(ctx context.Context, providerKey string) ([]api.CredentialDto, error)
func (s *CredentialService) Add(ctx context.Context, providerKey string, req api.SaveCredentialRequest) (*api.CredentialDto, error)
func (s *CredentialService) Update(ctx context.Context, providerKey, id string, req api.SaveCredentialRequest) (*api.CredentialDto, error)
func (s *CredentialService) Delete(ctx context.Context, providerKey, id string) error
func (s *CredentialService) CheckNow(ctx context.Context, providerKey, id string) (*api.CredentialDto, error)
func (s *CredentialService) Reorder(ctx context.Context, providerKey string, ids []string) ([]api.CredentialDto, error)

type ProviderService struct { /* ... */ }
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
func (s *ProviderService) Shutdown(ctx context.Context)

type ProviderStatusChecker struct { /* ... */ }
func (c *ProviderStatusChecker) Start(ctx context.Context)
func (c *ProviderStatusChecker) Stop()

type ModelCall struct { /* ... */ }
func (m *ModelCall) Ask(ctx context.Context, model, prompt string, timeout time.Duration, paying *domain.UsageContext) (string, error)

// internal/platform/integration
const (
	KeyFeishu          = "feishu"
	KeyDingTalk        = "dingtalk"
	FeishuDomainFeishu = "feishu"
	FeishuDomainLark   = "lark"
)
type IntegrationService struct { /* ... */ }
func (s *IntegrationService) List(ctx context.Context, baseURL string) ([]api.IntegrationDto, error)
func (s *IntegrationService) Update(ctx context.Context, key string, req api.SaveIntegrationRequest, baseURL string) (*api.IntegrationDto, error)
func (s *IntegrationService) Ready(ctx context.Context, key string) (bool, error)
func (s *IntegrationService) Value(ctx context.Context, key, field string) (string, bool)
func (s *IntegrationService) SetStatus(ctx context.Context, key, state, errMsg string)
func (s *IntegrationService) Status(ctx context.Context, key string) (state, lastError string)
func CallbackURL(key, baseURL string) string

// internal/platform/connector
const (
	BuiltinID       = "superagent"
	FeishuID        = "feishu"
	DingTalkID      = "dingtalk"
	FeishuDocsID    = "feishu-docs"
	DingTalkToolsID = "dingtalk-tools"
)
type OAuthTokens struct {
	Access    string
	Refresh   string
	ExpiresAt *time.Time
}
type TokenRejectedError struct { Message string }
type OAuthProvider interface {
	Key() string
	Integration() string
	ClientIDField() string
	ClientSecretField() string
	AuthorizeURL(ctx context.Context, clientID, redirectURI, state string) string
	Exchange(ctx context.Context, clientID, clientSecret, code, redirectURI string) (*OAuthTokens, error)
	Refresh(ctx context.Context, clientID, clientSecret, refreshToken string) (*OAuthTokens, error)
	FetchLogin(ctx context.Context, accessToken string) (string, error)
}
type FeishuDocsOAuthClient struct { /* ... */ }
func (c *FeishuDocsOAuthClient) Base(ctx context.Context) string

type ConnectorOAuthService struct { /* ... */ }
func (s *ConnectorOAuthService) IsConfigured(ctx context.Context, provider string) bool
func (s *ConnectorOAuthService) ProviderFor(connectorID string) (string, bool)
func (s *ConnectorOAuthService) BuildAuthorizeURL(ctx context.Context, provider, connectorID, owner, baseURL string) (string, error)
func (s *ConnectorOAuthService) HandleCallback(ctx context.Context, provider, code, state, caller string) (string, error)
func (s *ConnectorOAuthService) AccessToken(ctx context.Context, cred domain.ConnectorCredential) (string, bool)

type ConnectorService struct { /* ... */ }
func (s *ConnectorService) OnCompanyStarted(ctx context.Context, companyID string) // 订阅 eventbus.CompanyStarted
func (s *ConnectorService) List(ctx context.Context, viewer string, admin bool) ([]api.ConnectorDto, error)
func (s *ConnectorService) Create(ctx context.Context, req api.SaveConnectorRequest, viewer string) (*api.ConnectorDto, error)
func (s *ConnectorService) Update(ctx context.Context, id string, req api.SaveConnectorRequest, viewer string) (*api.ConnectorDto, error)
func (s *ConnectorService) SetEnabled(ctx context.Context, id string, enabled bool, viewer string) (*api.ConnectorDto, error)
func (s *ConnectorService) Delete(ctx context.Context, id string) error
func (s *ConnectorService) Authorize(ctx context.Context, id, viewer, token string, admin bool) (*api.ConnectorDto, error)
func (s *ConnectorService) Revoke(ctx context.Context, id, viewer string, admin bool) (*api.ConnectorDto, error)
func (s *ConnectorService) NewBindCode(ctx context.Context, id, viewer string, admin bool) (*api.ConnectorDto, error)
func (s *ConnectorService) MCPServersFor(ctx context.Context, owner string) ([]domain.McpServerSpec, error)
func (s *ConnectorService) SpecFor(ctx context.Context, id, owner string) (*domain.McpServerSpec, bool)
func (s *ConnectorService) AuthorizedBy(ctx context.Context, id, viewer string) bool
func (s *ConnectorService) UserToken(ctx context.Context, connectorID, owner string) (string, bool)
func ToolPatterns(servers []domain.McpServerSpec) []string

type ConnectorToolsService struct { /* ... */ }
func (s *ConnectorToolsService) Tools(ctx context.Context, id, viewer string, refresh bool) (*api.ConnectorToolsDto, error)
```

### 4.2 路由契约（与原版前端 100% 兼容，L5 `SA-13` 绑定）

全部 JSON API 统一响应信封 `{"code":"ok","message":null,"data":<payload>}`（错误走 `apperror` 统一映射），严格兼容 camelCase 字段名（与 `ProviderDto.java`、`ProviderModelDto.java`、`CredentialDto.java`、`NodeRuntimeDto.java`、`InstallLogDto.java`、`IntegrationDto.java`、`ConnectorDto.java`、`ConnectorToolsDto.java` 逐字段对齐）：

| 路由路径 | HTTP 方法 | 角色/权限 | 对应服务方法 |
|---|---|---|---|
| `/api/v1/providers` | `GET` / `POST` | `ADMIN` | `ProviderService.List` / `Add` |
| `/api/v1/providers/catalog` | `GET` | `ADMIN` | `ProviderService.Catalog`（V1 返回 `claude` & `codex`） |
| `/api/v1/providers/node` | `GET` | `ADMIN` | `NodeRuntimeService.Status` |
| `/api/v1/providers/node/install` | `POST` | `ADMIN` | `NodeRuntimeService.InstallAsync` |
| `/api/v1/providers/node/install-log` | `GET` | `ADMIN` | `NodeRuntimeService.InstallLog` |
| `/api/v1/providers/:id` | `GET` / `DELETE` | `ADMIN` | `ProviderService.Detail` / `Remove` |
| `/api/v1/providers/:id/install` | `POST` | `ADMIN` | `ProviderService.Install` |
| `/api/v1/providers/:id/upgrade` | `POST` | `ADMIN` | `ProviderService.Upgrade` |
| `/api/v1/providers/:id/install-log` | `GET` | `ADMIN` | `ProviderService.InstallLog` |
| `/api/v1/providers/:id/models/refresh` | `POST` | `ADMIN` | `ProviderService.RefreshModelsNow` |
| `/api/v1/providers/:id/check-latest` | `POST` | `ADMIN` | `ProviderService.CheckLatest` |
| `/api/v1/providers/:id/credentials` | `GET` / `POST` | `ADMIN` | `CredentialService.List` / `Add` |
| `/api/v1/providers/:id/credentials/order` | `PUT` | `ADMIN` | `CredentialService.Reorder`（注意 Gin 中静态 `/order` 优先于 `/:cid`） |
| `/api/v1/providers/:id/credentials/:cid` | `PUT` / `DELETE` | `ADMIN` | `CredentialService.Update` / `Delete` |
| `/api/v1/providers/:id/credentials/:cid/check` | `POST` | `ADMIN` | `CredentialService.CheckNow` |
| `/api/v1/admin/integrations` | `GET` | `ADMIN` | `IntegrationService.List`（V1 仅返回 `feishu` & `dingtalk`） |
| `/api/v1/admin/integrations/:key` | `PUT` | `ADMIN` | `IntegrationService.Update` |
| `/api/v1/connectors` | `GET`（全员） / `POST`（`ADMIN`） | 见左 | `ConnectorService.List` / `Create` |
| `/api/v1/connectors/:id` | `PUT` / `DELETE` | `ADMIN` | `ConnectorService.Update` / `Delete` |
| `/api/v1/connectors/:id/enabled` | `PUT` | `ADMIN` | `ConnectorService.SetEnabled`（系统连接器唯一可变项） |
| `/api/v1/connectors/:id/tools` | `GET` | 登录用户 | `ConnectorToolsService.Tools(id, viewer, refresh)` |
| `/api/v1/connectors/:id/token` | `PUT` / `DELETE` | 登录用户 | `ConnectorService.Authorize` / `Revoke` |
| `/api/v1/connectors/:id/bind-code` | `POST` | 登录用户 | `ConnectorService.NewBindCode`（`feishu`/`dingtalk`） |
| `/api/v1/oauth/:provider/authorize` | `GET` | 登录用户 | `302` 重定向至飞书授权页，或 `/?connect_error=<code>#connectors` |
| `/api/v1/oauth/:provider/callback` | `GET` | 登录用户 | `302` 重定向至 `/?connected=<id>#connectors` 或 `/?connect_error=<code>#connectors` |

> **Gin 前缀树路由防冲突检查**：`/api/v1/providers/node*`、`/api/v1/providers/catalog` 与 `/api/v1/providers/:id` 并存，`/api/v1/providers/:id/credentials/order` 与 `/api/v1/providers/:id/credentials/:cid` 并存——Gin 支持静态段与参数段同级无冲突（静态优先），但在 `SA-13` 路由注册测试中必须显式验证。

### 4.3 错误语义

| 场景 | 错误类型 / HTTP 状态码 | 精确文案 / 跳转码 |
|---|---|---|
| 租户无任何已启用 API Key | `NoCompanyKeyError`（wrap `apperror.Forbidden`，HTTP `403`） | `"本公司还没有配置 <providerKey> 的 API Key：请管理员在「模型」里为它添加 Key"` |
| 重复添加供应商 | `apperror.Conflict`（HTTP `409`） | `ProviderAlreadyAdded` |
| 未知供应商/连接器/集成 | `apperror.NotFound`（HTTP `404`） | `"No such integration: <key>"` 等 |
| 重复添加同一 Key / 空 Key / 非法 URL | `apperror.InvalidArg`（HTTP `400`） | `"API Key 不能为空"` / `"这个 API Key 已经在列表里了"` / `"访问地址要以 http:// 或 https:// 开头：<url>"` |
| 尝试编辑/删除系统连接器或合成行 | `apperror.Forbidden`（HTTP `403`） | `"系统自带的连接不能编辑或删除，只能启用或停用"` / `"SuperAgent's own connector is built in and can't be changed"` / `"Feishu is set up under System Settings → integrations"` / `"DingTalk is set up under System Settings → integrations"` |
| 推理探测网络不可达 | `*ProbeUnreachableError`（内部错误类型） | 保留模型上次 `inferStatus`，并触发 5 分钟单租户去重重试 |
| OAuth 异常 | `*OAuthError`（HTTP `302` 重定向） | `no_oauth_provider` / `not_configured` / `unknown_connector` / `invalid_state` / `missing_code` / `exchange_failed` -> `/?connect_error=<code>#connectors` |
| 飞书 OAuth 刷新被 4xx 拒绝 | `*TokenRejectedError`（内部错误类型） | 自动删除该员工凭证并返回 `("", false)`，前端立即显示需重新授权 |

### 4.4 SQL 契约

1. **PG17 幂等 Upsert**：`model_provider`、`provider_credential`、`connector`、`connector_credential` 统一使用 `INSERT INTO ... ON CONFLICT (<pk>) DO UPDATE SET ...`，彻底替换原版 MySQL `REPLACE INTO` 与 `ON DUPLICATE KEY UPDATE`；
2. **`provider_credential` 软删保留哈希**：`Delete` 执行 `UPDATE provider_credential SET deleted = TRUE, updated_at = $3 WHERE company_id = $1 AND id = $2 AND deleted = FALSE`，`List`/`Find` 恒定过滤 `deleted = FALSE`，`KnownKeyHashes` 刻意**不过滤** `deleted`；
3. **`model_provider` 行写串行化**：`ProviderService` 持有 `writeMu sync.Mutex` 串行化 `Read -> Transform -> Save`，确保 3s 本地探测、1h 最新版本/模型列表与 6 并发推理探测绝不相互覆盖彼此列。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 `ProviderCatalog`（V1 GA 双条目 + 可扩展注册表）

- **Java 参考**：`ProviderCatalog.java:55-102`
- **V1 锁定条目**：

| `Key` | `Name` | `CLI` | `NPMPackage` | `MinNode` | `Protocol` | `DefaultBaseURL` | `KeyEnv` | `BaseURLEnv` |
|---|---|---|---|---|---|---|---|---|
| `claude` | `Claude Code` | `claude` | `@anthropic-ai/claude-code` | `22.0.0` | `anthropic` | `https://api.anthropic.com` | `ANTHROPIC_API_KEY` | `ANTHROPIC_BASE_URL` |
| `codex` | `OpenAI Codex` | `codex` | `@openai/codex` | `16.0.0` | `openai` | `https://api.openai.com/v1` | `OPENAI_API_KEY` | `""` |

- 公共命令生成助手 `NewNPMEntry(...)` 固定拼装 `npm install -g <npmPackage>@latest --no-fund --no-audit` 与 `VersionArgs: []string{"--version"}`，未来增量添加 `deepseek`/`glm`/`kimi`/`qwen`/`grok`/`codebuddy`/`gemini`/`minimax` 只需调用 `catalog.Register(NewNPMEntry(...))`。

### 5.2 `NodeRuntimeService`：私有 Node LTS 安装、镜像/代理/CA、60s 停滞 Watchdog 与 SHA-256

- **Java 参考**：`NodeRuntimeService.java:43-336`
- **V1 商业私有化增强**：
  1. 下载基址不再写死 `https://nodejs.org/dist/`，而是取 `cfg.DistMirror`（默认 `https://nodejs.org/dist`，自动规范末尾 `/`），支持内网/国内镜像（如 `https://npmmirror.com/mirrors/node/`）；
  2. 内置专用 `http.Client` 加载 `cfg.CABundleFile` 追加至系统 `x509.CertPool`，并显式配置代理函数（优先 `cfg.HTTPSProxy`/`cfg.HTTPProxy`/`cfg.NoProxy`，缺省走 `http.ProxyFromEnvironment`）；
  3. 完整保真 60 秒停滞 Watchdog（`stallTimeout = 60 * time.Second`）：每 5 秒检查 `lastByteAt`，超时调用 `resp.Body.Close()` 立即打断阻塞 `Read`；每完成 10%（或未知总长每 10 MiB）写入一行 `"已下载 X.X MB / Y.Y MB"` 到 `InstallJob`；
  4. 完整保真 `SHASUMS256.txt` SHA-256 严格校验（不一致拒绝解压）、`tar -xzf` 解压、检验 `bin/node` 可执行权限，并原子替换 `<nodeRoot>/current`（先删旧再 `os.Rename`，失败清理 `.staging-*`）；
  5. `installMu sync.Mutex` 保证全服同一时刻仅运行一次私有 Node 安装，并发到达者等待前一次完成。

```go
package provider

const nodeStallTimeout = 60 * time.Second

func (s *NodeRuntimeService) installLatestLTS(ctx context.Context, job *InstallJob) (string, error) {
	dist := strings.TrimRight(s.cfg.DistMirror, "/") + "/"
	job.Step("查询 Node.js 最新 LTS 版本（" + dist + "index.json）")
	version, err := s.latestLTS(ctx, dist)
	if err != nil { return "", err }
	plat, err := nodePlatform() // darwin|linux + x64|arm64，其余报错
	if err != nil { return "", err }

	dirName := "node-" + version + "-" + plat
	file := dirName + ".tar.gz"
	root := s.paths.NodeRoot()
	if err := os.MkdirAll(root, 0o755); err != nil { return "", err }
	staging, err := os.MkdirTemp(root, ".staging-")
	if err != nil { return "", err }
	defer os.RemoveAll(staging)

	archive := filepath.Join(staging, file)
	job.Step("下载 " + dist + version + "/" + file)
	if err := s.downloadWithWatchdog(ctx, dist+version+"/"+file, archive, job); err != nil {
		return "", err
	}
	job.Step("校验 SHA-256")
	expected, err := s.expectedSHA256(ctx, dist, version, file)
	if err != nil { return "", err }
	if err := verifyFileSHA256(archive, expected); err != nil { return "", err }

	job.Step("解压到 " + filepath.Join(root, "current"))
	if err := runTarExtract(ctx, s.launcher, archive, staging); err != nil { return "", err }
	unpacked := filepath.Join(staging, dirName)
	if !isExecutableFile(filepath.Join(unpacked, "bin", "node")) {
		return "", errors.New("Node archive has no bin/node")
	}
	current := filepath.Join(root, "current")
	_ = os.RemoveAll(current)
	if err := os.Rename(unpacked, current); err != nil { return "", err }
	return version, nil
}

func (s *NodeRuntimeService) downloadWithWatchdog(ctx context.Context, url, target string, job *InstallJob) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil { return err }
	resp, err := s.httpClient.Do(req)
	if err != nil { return err }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载 %s 失败：HTTP %d", url, resp.StatusCode)
	}

	var lastByteNano atomic.Int64
	lastByteNano.Store(time.Now().UnixNano())
	var stalled atomic.Bool
	done := make(chan struct{})
	defer close(done)
	go func() {
		tk := time.NewTicker(5 * time.Second)
		defer tk.Stop()
		for {
			select {
			case <-done:
				return
			case <-tk.C:
				if time.Since(time.Unix(0, lastByteNano.Load())) > nodeStallTimeout {
					stalled.Store(true)
					_ = resp.Body.Close() // 打断下方 Read 阻塞
					return
				}
			}
		}
	}()

	out, err := os.Create(target)
	if err != nil { return err }
	defer out.Close()

	buf := make([]byte, 64*1024)
	var written int64
	reported := 0
	total := resp.ContentLength
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil { return werr }
			written += int64(n)
			lastByteNano.Store(time.Now().UnixNano())
			tenth := int(written / (10 << 20))
			if total > 0 { tenth = int(written * 10 / total) }
			if tenth > reported {
				reported = tenth
				msg := fmt.Sprintf("已下载 %.1f MB", float64(written)/1048576.0)
				if total > 0 { msg += fmt.Sprintf(" / %.1f MB", float64(total)/1048576.0) }
				job.Line(msg)
			}
		}
		if rerr == io.EOF { break }
		if rerr != nil {
			if stalled.Load() {
				return fmt.Errorf("下载 %s 超过 %d 秒没有收到数据，已放弃。请检查服务器网络或 Node 镜像配置", url, int(nodeStallTimeout.Seconds()))
			}
			return rerr
		}
	}
	if total > 0 && written < total {
		return fmt.Errorf("下载中断（%.1f MB / %.1f MB）", float64(written)/1048576.0, float64(total)/1048576.0)
	}
	return nil
}
```

### 5.3 `InstallJob`、`InstallFailures` 与 `ProviderService` CLI 安装流水线

- **Java 参考**：`InstallJob.java:11-57`、`InstallFailures.java:13-79`、`ProviderService.java:249-428`
- **关键行为保真**：
  1. `InstallJob`：`MAX_LINES = 400` 环形队列，`Step(text)` 写入 `"▸ " + text`，`Finish(err)` 写入 `"✓ 完成"` 或 `"✗ 失败：" + err`；
  2. 串行安装队列：单个后台 goroutine 从无阻塞带缓冲任务队列顺序消费（npm 全局目录不支持并发写），`busy` map 对同 `providerID` 去重；
  3. **前置自动装 Node**：启动前检查 `nodeProblem(entry)`（缺 `node`/`npm` 或版本 `< entry.MinNode`），自动调用 `nodeRuntime.Ensure(ctx, entry.MinNode, job)`；
  4. **`runInstallCommand` 双看门狗 + 进程组查杀**：
     - 经 `ProcessLauncher.Start` 执行 `argv`（为 `npm` 自动追加 `--loglevel=http`、`MergeStderr: true`；命中私有 Node 时设置 `npm_config_prefix=<nodeRoot>/current`；自动通过 `CliPaths` 注入 `NPM_CONFIG_REGISTRY`、代理与 `NODE_EXTRA_CA_CERTS`）；
     - 剥离 ANSI 转义 `\x1b\[[;\d]*[A-Za-z]` 写入 `InstallJob` 并更新 `lastOutputAt`；
     - 独立监控循环每秒检测：`time.Since(lastOutputAt) > 3m`（静默超时）或总耗时 `> 30m` -> 调用 `proc.Kill()`（整组查杀 npm 及其所有子进程）并返回清晰中文原因；
  5. **`EACCES`/`EPERM` 自动私有 Node 降级**（`ProviderService.java:309-314`）：系统 npm 报权限不足且尚无私有 Node 时，自动执行 `nodeRuntime.InstallPrivateNow(ctx, job)` 后立即重试原安装命令；
  6. **`InstallFailures.Summarize(output, exitCode)`**：正则匹配 `npm (?:error|ERR!) code (\S+)` 映射网络 / 权限 / 404 / Node 版本 / 磁盘满 / TLS 证书 / 401·403 七类中文诊断（附提取出的 URL 或路径），否则提取前 3 条错误行。

### 5.4 `CredentialService` & `EndpointFailures`（严格租户隔离 + Layer-1 故障转移）

- **Java 参考**：`CredentialService.java:43-440`、`EndpointFailures.java:12-60`
- **关键行为保真**：
  1. **`Choose(ctx, providerKey, excluding)`**（`CredentialService.java:170-186`）：
     - 从 `store.List(ctx, providerKey)`（按 `priority ASC, created_at ASC`）过滤 `c.Enabled && c.ID != excluding`；
     - 优先返回首个 `c.Usable()`（`Enabled && Status != "error"`）；
     - **严格多租户隔离**：若过滤后池为空，直接返回 `&NoCompanyKeyError{ProviderKey: providerKey}`（HTTP `403`），**绝不回退宿主机环境变量或 CLI 登录态**；
     - 若全部已启用 Key 均处于 `ERROR`，兜底返回最高优先级的 `pool[0]` 尝试调用；
     - 构造 `domain.RunCredential` 时若 `c.BaseURL == ""`，显式填充目录的 `DefaultBaseURL`（防 CLI 继承宿主机残留配置）；
  2. **`Failover(ctx, providerKey, used, errMsg)`**（`CredentialService.java:149-163`）：
     - 由 `ChatService`/`TaskService` 在**零输出**失败时调用；
     - 先判 `EndpointFailures.IsEndpointFailure(errMsg)`：
       - 正则：`(?i)(api error|status|http|code)[^\n]{0,16}?\b(401|402|403|429|500|502|503|504|529)\b`；
       - 完整 33 条短语清单（认证拒绝、配额/余额不足、上游过载/5xx、网络断连，逐字对齐 `EndpointFailures.java:18-28`）；
     - 命中则将 `used.ID` 标为 `Status = "error"`、`LastError = truncate990(errMsg)` 落库，调用 `Choose(ctx, providerKey, used.ID)`；若切到新 Key（`next.ID != used.ID`），触发全部 `onSwitch` 回调并返回 `(next, true, nil)`；
  3. **10 分钟 / 1 小时定时复检 (`CheckAll`)**（`CredentialService.java:212-261`）：
     - `ProviderStatusChecker` 每 10 分钟扫描每个租户；
     - `dueForCheck(c)`：`c.Status != "ok" || c.CheckedAt == nil || c.CheckedAt.Add(1*time.Hour).Before(now)`（故障/未知每 10 分钟必检以促高优 Key 尽快恢复接管；健康 Key 每 1 小时检一次）；
     - 检测前后比较当前生效 Key ID，若变更则触发 `notifySwitch(ctx, providerKey)`（驱动 `ProviderService` 重新拉取模型列表与全量推理探测）；
  4. **新增/更新/排序与脱敏**（`CredentialService.java:304-439`）：
     - `Add` 校验非空、按 `SHA-256(trimmedKey)` 拒绝同供应商池内活跃重复 Key、优先级取 `max+1`、对 `claude` 默认 `bearer=true`、其余默认 `bearer=false`；
     - `Update` 未传 `apiKey` 时保留原密文与原哈希，任何修改重置 `status=unknown` 并异步触发 `checkSoon`；
     - `Mask(key)`：`< 12` 字符返回 `"••••"`，否则 `key[:4] + "…" + key[len-4:]`；解密失败返回 `"（无法解密）"`；
     - `BaseURL` 校验必须是 `http://` 或 `https://` 且含 Host，剥离末尾所有 `/`。

```go
package provider

func (s *CredentialService) ResolveCurrent(ctx context.Context, providerKey, excludeID string) (*domain.RunCredential, error) {
	if _, ok := s.catalog.Find(providerKey); !ok {
		return nil, nil
	}
	all, err := s.store.List(ctx, providerKey)
	if err != nil { return nil, err }
	var pool []domain.ProviderCredential
	for _, c := range all {
		if c.Enabled && c.ID != excludeID {
			pool = append(pool, c)
		}
	}
	if len(pool) == 0 {
		return nil, &NoCompanyKeyError{ProviderKey: providerKey}
	}
	for _, c := range pool {
		if c.Usable() {
			return s.toRunCredential(ctx, c)
		}
	}
	return s.toRunCredential(ctx, pool[0])
}

func (s *CredentialService) Failover(ctx context.Context, providerKey string, used *domain.RunCredential, errMsg string) (*domain.RunCredential, bool, error) {
	if _, ok := s.catalog.Find(providerKey); !ok || !IsEndpointFailure(errMsg) {
		return nil, false, nil
	}
	if used != nil && used.ID != "" {
		if cur, err := s.store.Find(ctx, used.ID); err == nil && cur != nil {
			now := time.Now().UTC()
			cur.Status = domain.CredStatusError
			cur.LastError = truncateRunes(strings.TrimSpace(errMsg), 990)
			cur.CheckedAt = &now
			cur.UpdatedAt = now
			_ = s.store.Save(ctx, *cur)
		}
	}
	excludeID := ""
	if used != nil { excludeID = used.ID }
	next, err := s.ResolveCurrent(ctx, providerKey, excludeID)
	if err != nil {
		// 仅剩这一个 Key（exclude 后为空）属于无备用可切，返回 false 让上层暴露原始错误
		var noKey *NoCompanyKeyError
		if errors.As(err, &noKey) { return nil, false, nil }
		return nil, false, err
	}
	if next == nil || (used != nil && next.ID == used.ID) {
		return nil, false, nil
	}
	s.notifySwitch(ctx, providerKey)
	return next, true, nil
}
```

### 5.5 `ProviderProbe`、`ProviderModelFetcher`、`ProviderInferenceProbe`、`ProviderStatusChecker` 与 `ModelCall`

- **Java 参考**：`ProviderProbe.java:46-185`、`ProviderModelFetcher.java:42-388`、`ProviderInferenceProbe.java:42-615`、`ProviderService.java:435-659`、`ProviderStatusChecker.java:29-148`、`ModelCall.java:29-103`
- **关键行为保真**：
  1. **`ProviderProbe`**：
     - `Version(binary, entry)`：若存在 `NPMPackage`，先从 `filepath.EvalSymlinks(binary)` 向上逐级查找 `package.json` 且 `name == entry.NPMPackage` 直接读取 semver（免起进程），失败再运行 `<binary> --version`（8s 超时）；
     - `LatestNPMVersion(pkg)`：执行 `npm view <pkg> version`（30s 超时，经 `CliPaths` 注入 `NPM_CONFIG_REGISTRY`、代理、CA）；
     - `IsNewer(latest, current)`：严格 3 段数值 semver 比较；
  2. **`ProviderModelFetcher`**：
     - 过滤非对话模型正则 `NON_CHAT`：`embed|tts|transcribe|realtime|image|rerank|intent-detect|whisper|moderation|dall-e|audio|speech|^mock-|^test-`；
     - **`claude`**：分页请求 `GET <base>/v1/models?limit=1000`（最多 20 页，header `anthropic-version: 2023-06-01`，20s 超时），标 `nativeModel = strings.HasPrefix(id, "claude-")`，解析 `capabilities.effort`（`effortSource="model"`），对原生模型无能力字段时回退解析 `claude --help` 的 `--effort <level> (...)`（`effortSource="cli"`）；按 `nativeModel DESC, id ASC` 排序；
     - **`codex`**：执行 `codex debug models`（有池凭据时附加 `-c model_provider="openai"`，60s 超时），过滤 `visibility == "hide"`，提取 `slug`、`display_name`、`supported_reasoning_levels`、`default_reasoning_level`、`context_window`；
     - 预留 `RegisterFetcher(providerKey, fn)` 供未来扩展 OpenAI 兼容/CLI 厂商；
  3. **`ProviderInferenceProbe` & `ProbeUnreachableError`**：
     - 探测语固定 `Prompt = "你好"`；HTTP 客户端复用企业代理与自定义 CA（连接 5s，推理 60s）；CLI 超时 `90s`；
     - **`CheckKey(ctx, providerKey, modelID, cred)`**：
       - `codex`（及 OpenAI 类扩展厂商）：优先零 Token `GET <base>/models`（Bearer，30s）——`200` 直接成功，`401`/`403` 直接失败，其余状态码降级走一次最小推理；
       - `claude`：若 `modelID == ""` 返回 `"还没有可用来测试的模型（等模型列表刷新后再检查）"`，否则走 `Check`（`source = "key_check"`）；
     - **`Check(ctx, providerKey, modelID, cred)`**：
       - `claude`：`POST <base>/v1/messages`（`max_tokens: 8`，`anthropic-version: 2023-06-01`），校验 `content` 为数组，提取 `usage`；
       - `codex`：`POST <base>/responses`（`input: "你好"`, `max_output_tokens: 16`，节省约 7k 系统提示词 Token），解析 `usage`（按 `uncached = max(0, input - cached)` 拆分）；
       - **不可达分类**：底层 DNS/连接/超时错误或 CLI 输出命中 8 条断连短语（`Failed to load cloud config bundle`, `error sending request`, `Connection refused`, `connection error`, `ENOTFOUND`, `ECONNREFUSED`, `ETIMEDOUT`, `stream disconnected`）一律返回 `*ProbeUnreachableError`；
       - 每次探测结束异步/尽力调用 `UsageRecorder.Record` 记账到 `user="system"`, `source="model_check"|"key_check"`, `cli=providerKey`；
  4. **`ProviderService.Sweep`（6 并发探测与不可达保活）**：
     - 每供应商同一时刻仅允许一轮探测（`checking[providerID]` 防重入）；
     - 新鲜窗口：定时清扫 `50m`、手动 Refresh 按钮 `1m`、新增供应商或切端点 `0`（全检）；
     - 6 并发 goroutine（semaphore channel `make(chan struct{}, 6)`）使用 `tenant.Detach(ctx)` 继承租户，遇到 `*ProbeUnreachableError` 计入 `unreachableCount` 且**不写入 `results`（完整保留原 `inferStatus`）**，遇到明确模型/鉴权错误记为 `inferStatus="error"`；
     - 仅当存在新结果时在 `writeMu` 内与当前数据库最新模型列表按 `id` 归并写回；返回 `reached = (unreachableCount == 0)`；
  5. **`ProviderStatusChecker` 四条节奏与 5 分钟重试**（`ProviderStatusChecker.java:71-147`）：
     - 全部按 `CompanyService.ForEachEnabled` 公平多租户轮转；
     - 节奏 1：`local` 每 `3s`（最小 `1s`）；
     - 启动立即异步跑一遍 `refreshLatest + refreshInference`；
     - 节奏 2：`latest + models` 每 `3600s`（最小 `60s`）；
     - 节奏 3：`inference` 每 `3600s`（最小 `60s`）——若某租户返回 `reached == false`，用 `retryPending[companyID]` 去重后在 `300s`（5 分钟）后自动单租户重试一次；
     - 节奏 4：`credentials.CheckAll` 初始延迟 `60s`、每 `600s`（10 分钟，最小 `60s`）；
  6. **`ModelCall.Ask(ctx, model, prompt, timeout, paying)`**（`ModelCall.java:57-102`）：
     - 经 `providers.CLIFor(ctx, model)`（优先 `nativeModel == true`）映射 `AgentType` 并从 `RunnerRegistry` 获取 Runner；
     - 创建临时 `sa-ask-*` 目录（`defer os.RemoveAll`），取 `credentials.Current(ctx, cli)`，以 `context.WithTimeout(ctx, timeout)` 调用 `runner.Run`（`AllowedTools: nil`, `SkipPermissions: false`, 无 MCP），并用 `UsageRecorder.Tap` 包裹 `EventSink` 记录 Token；超时或空答返回明确错误。

### 5.6 `IntegrationService` & `AppUrl`（V1 精简至 `feishu` & `dingtalk`）

- **Java 参考**：`IntegrationService.java:30-247`、`AppUrl.java:15-36`
- **V1 锁定定义表（删除 GitHub / Gitee / Notion / Confluence）**：

```go
package integration

var definitions = []Definition{
	{
		Key: KeyFeishu, Name: "飞书 / Lark", CallbackPath: "/api/v1/oauth/feishu-docs/callback",
		Fields: []FieldDef{
			{Key: "domain", Label: "版本", Secret: false, DefaultValue: FeishuDomainFeishu, Options: []api.IntegrationOption{
				{Value: FeishuDomainFeishu, Label: "飞书（中国版，open.feishu.cn）"},
				{Value: FeishuDomainLark, Label: "Lark（国际版，open.larksuite.com）"},
			}},
			{Key: "appId", Label: "App ID", Secret: false},
			{Key: "appSecret", Label: "App Secret", Secret: true},
		},
	},
	{
		Key: KeyDingTalk, Name: "钉钉", CallbackPath: "",
		Fields: []FieldDef{
			{Key: "clientId", Label: "Client ID（AppKey）", Secret: false},
			{Key: "clientSecret", Label: "Client Secret（AppSecret）", Secret: true},
		},
	},
}
```

- **核心语义保真**：
  1. `Update`：secret 字段为空时保留库中原密文，非空则用 `SecretCipher.Encrypt(ctx, val)` 加密；带 `Options` 字段严格白名单校验；当 `enabled == true` 时所有无默认值字段必须非空，否则 `400 InvalidArg`；停用时清理内存 `statuses[companyID+"/"+key]`；落库后通过 `eventbus.Publish(ctx, eventbus.IntegrationChanged{CompanyID, Key})` 触发 `SA-11` 飞书/钉钉长连接热重载；
  2. `List`：所有 secret 字段出参 `Value = nil`、仅返回 `Set = (stored != "")`；
  3. `AppUrl.Base(ctx, req)` 解析优先级：`SettingsService.PublicURL(ctx)` → 配置 `app.base_url`（`SUPERAGENT_BASE_URL`）→ 请求的 `X-Forwarded-Proto`/`Scheme` + `Host`，统一剥离末尾 `/`。

### 5.7 `ConnectorService`（V1 锁定 `feishu-docs` + `dingtalk-tools` + 3 合成行 + 通用自定义 MCP）

- **Java 参考**：`ConnectorService.java:47-565`
- **V1 锁定播种逻辑 (`OnCompanyStarted`)**（删除 `github`/`gitee`/`notion`/`confluence`）：
  1. `feishu-docs`（单次标志 `connector.seeded.feishu-docs`）：`Name="飞书 / Lark 文档"`、`AuthMethod=OAUTH`、`Enabled=true`、`Transport=HTTP`、`Endpoint=""`（在 `ownTools` 集合中，绝不作为外部 MCP 进程启动，由 `SA-08` `FeishuDocTools` 调用 `ConnectorService.UserToken`）；
  2. `dingtalk-tools`（单次标志 `connector.seeded.dingtalk-tools`）：`Name="钉钉工具"`、`AuthMethod=NONE`、`Enabled=false`（公司应用身份，默认关闭需管理员知情开启）、`Transport=STDIO`、`Endpoint="npx -y dingtalk-mcp@latest"`；
  3. `systemIDs = {"feishu-docs", "dingtalk-tools"}`：禁止 `Update`/`Delete`（返回 `403`），仅允许 `SetEnabled`；合成行 `superagent`/`feishu`/`dingtalk` 同样禁止变更（`403`）。
- **列表顺序 (`List`)**：
  1. 合成 `superagent` 行（`kind="builtin"`, `system=true`, 状态取自 `McpGrants.SelfStatus()`，管理员可见 `endpoint=selfURL` 与 `lastError`）；
  2. 若 `integrations.Ready(ctx, "feishu")`：合成 `feishu` 行（`kind="feishu"`, IM 连接状态、当前员工 `EventSource` 绑定态与未绑定时的 8 位 `bindCode`、管理员可见已绑定人数）；
  3. 若 `integrations.Ready(ctx, "dingtalk")`：合成 `dingtalk` 行（同上）；
  4. 库内所有 `Connector` 记录（管理员额外可见 `endpoint`、`tokenEnv`、`companySecretSet`、`authorizedCount`）。
- **自定义 MCP 连接器校验 (`validated`)**：
  - `AuthMethod == OAUTH` 仅允许已注册 `OAuthProvider` 的内置连接器；
  - `STDIO` 要求 `Endpoint` 非空；若 `AuthMethod != NONE`，`TokenEnv` 必须匹配 `^[A-Za-z_][A-Za-z0-9_]{0,127}$`；
  - `HTTP` / `SSE` 要求 `Endpoint` 匹配 `(?i)^https?://\S+`，且清空 `TokenEnv`；
  - `Update` 若变更 `AuthMethod`，立即 `credentials.DeleteAll(ctx, id)` 清空旧授权；`SHARED_TOKEN` 首次创建或无公司密文时必须提供 `secret`。
- **Agent 运行期装配 (`MCPServersFor(ctx, owner)`)**（`ConnectorService.java:232-324`）：
  ```go
  const dingTalkProfiles = "dingtalk-contacts,dingtalk-department,dingtalk-calendar,dingtalk-tasks,dingtalk-report"

  func (s *ConnectorService) SpecFor(ctx context.Context, id, owner string) (*domain.McpServerSpec, bool) {
      c, err := s.store.FindConnector(ctx, id)
      if err != nil || c == nil || c.Kind != domain.ConnectorKindMCP || !c.Enabled || c.Endpoint == "" {
          return nil, false
      }
      if c.ID == DingTalkToolsID {
          if ready, _ := s.integrations.Ready(ctx, integration.KeyDingTalk); !ready { return nil, false }
      }
      if c.AuthMethod == domain.AuthOAuth {
          if prov, ok := s.oauth.ProviderFor(c.ID); !ok || !s.oauth.IsConfigured(ctx, prov) { return nil, false }
      }
      var token string
      if c.AuthMethod != domain.AuthNone {
          who := domain.ConnectorCredentialCompany
          if c.AuthMethod.Personal() { who = owner }
          cred, err := s.credentials.Find(ctx, c.ID, who)
          if err != nil || cred == nil { return nil, false }
          if c.AuthMethod == domain.AuthOAuth {
              tok, ok := s.oauth.AccessToken(ctx, *cred)
              if !ok { return nil, false }
              token = tok
          } else {
              tok, err := s.cipher.Decrypt(ctx, cred.SecretCipher)
              if err != nil { return nil, false } // 解密失败静默跳过，不中断 Agent 运行
              token = tok
          }
      }
      spec := buildServerSpec(*c, token) // name 合法则用 c.ID，否则 "c_" + 前12位去横杠ID
      if c.ID == DingTalkToolsID {
          if spec.Env == nil { spec.Env = make(map[string]string) }
          spec.Env["DINGTALK_Client_ID"], _ = s.integrations.Value(ctx, integration.KeyDingTalk, "clientId")
          spec.Env["DINGTALK_Client_Secret"], _ = s.integrations.Value(ctx, integration.KeyDingTalk, "clientSecret")
          spec.Env["ACTIVE_PROFILES"] = dingTalkProfiles
      }
      return &spec, true
  }
  ```

### 5.8 `FeishuDocsOAuthClient` & `ConnectorOAuthService`

- **Java 参考**：`FeishuDocsOAuthClient.java:25-122`、`ConnectorOAuthService.java:37-212`
- **关键行为保真**：
  1. **`FeishuDocsOAuthClient`**：
     - `Key() = "feishu-docs"`、`Integration() = "feishu"`、`ClientIDField() = "appId"`、`ClientSecretField() = "appSecret"`；
     - `Base(ctx)` 动态读取 `integration.feishu.domain`：`"lark"` → `larkBaseURL`（默认 `https://open.larksuite.com`），其余 → `feishuBaseURL`（默认 `https://open.feishu.cn`）；
     - 授权地址：`<base>/open-apis/authen/v1/authorize?client_id=...&redirect_uri=...&response_type=code&scope=docx:document drive:drive wiki:wiki:readonly search:docs:read offline_access&state=...`；
     - 换取与刷新：JSON `POST <base>/open-apis/authen/v2/oauth/token`（15s 超时）；`status != 200 || code != 0 || access_token == ""` 时，若处于刷新且 `status < 500` 抛出 `*TokenRejectedError`，否则返回普通 `error`；
     - 获取展示名：`GET <base>/open-apis/authen/v1/user_info`（Bearer），取 `data.name` 兜底 `"feishu-user"`；
  2. **`ConnectorOAuthService` 防 CSRF 与双检锁刷新**：
     - `BuildAuthorizeURL`：清除过期 `state`（`STATE_TTL = 10m`），生成 32 位 hex 随机 `state`，存 `stateEntry{CompanyID, ConnectorID, Owner, RedirectURI, CreatedAt}`（**Go 版显式绑定 `CompanyID`，增强跨租户防护**）；
     - `HandleCallback`：原子取出并删除 `pendingStates[state]`（单次使用），强校验未过期、`entry.CompanyID == curCompany`、`entry.Owner == caller`、`provider == providerFor(entry.ConnectorID)`，否则返回 `OAuthError("invalid_state")`；
     - `AccessToken(ctx, cred)`：若 `cred.ExpiresAt == nil || now.Add(10*time.Minute).Before(*cred.ExpiresAt)` 直接解密返回；否则按 `<companyId>/<connectorId>/<owner>` 加专属 `sync.Mutex` 锁：
       1. 锁内重查 DB（双检：另一并发协程可能已刷新完成）；
       2. 调用 `provider.Refresh`；若新返回含 `Refresh` 则轮换保存，否则保留原 `RefreshToken`；
       3. 若返回 `*TokenRejectedError`（4xx 撤销/失效）-> **立即 `credentials.Delete(ctx, connectorID, owner)`** 并返回 `("", false)`；
       4. 若返回网络/5xx 错误 -> 保留原凭证，若 `now < ExpiresAt` 仍降级返回旧 `access_token`。

### 5.9 `ConnectorToolsService`（10 分钟缓存与内置/外部工具发现）

- **Java 参考**：`ConnectorToolsService.java:35-148`
- **关键行为保真**：
  1. 缓存键：`<companyID>|<connectorID>|<viewer>`，TTL `10m`；`refresh == false` 且未过期直接命中；拉取结果 `Error == ""` 写入缓存，有错则**删除**旧缓存；
  2. 4 个内置处理分支：
     - `superagent`：汇总所有支持 `CHAT` 或 `TASK` 的 `McpToolProvider` 工具（按名称去重）；
     - `feishu`：返回空工具列表 + `Note = "飞书消息本身不给 Agent 提供工具；授权飞书文档后，这里列出文档工具。"`；
     - `dingtalk`：校验 `integrations.Ready(ctx, "dingtalk")`（否则 `403`），过滤 `dingtalk_doc_*` 工具；
     - `feishu-docs`：校验 `connectors.AuthorizedBy(ctx, "feishu-docs", viewer)`（否则 `403`），过滤 `feishu_doc_*` 工具；
  3. 其余外部 MCP 连接器：取 `connectors.SpecFor(ctx, id, viewer)`（不可用返 `403 "连接启用并授权之后才能查看它的能力"`），调用 `McpClient.ListTools`（`STDIO` 超时 `120s`，`HTTP`/`SSE` 超时 `30s`），将 JSON Schema 的 `inputSchema.properties`/`required` 与 `annotations.{title,readOnlyHint,destructiveHint}` 解析为 `ConnectorToolsDto`。

---

## 6. 任务清单（TDD 粒度）

- [ ] T1: 核对并补全 `internal/core/{domain,store,api,eventbus}` 中 provider/integration/connector 领域模型、Store 接口、DTO 与 `IntegrationChanged` 事件
- [ ] T2: 实现 `internal/platform/provider/catalog.go`（可扩展 `CatalogRegistry`，V1 默认 `claude` & `codex`）与 `endpoint_failures.go` + 对应单测
- [ ] T3: 实现 `internal/platform/provider/{install_job.go,install_failures.go,node_runtime.go}`（400 行环、npm 诊断、私有 Node LTS 镜像/代理/CA/60s stall watchdog/SHA-256/原子替换）+ `httptest` 单元测试
- [ ] T4: 实现 `internal/platform/provider/{probe.go,api_access.go,model_fetcher.go,inference_probe.go}`（`package.json` 版本探测、Claude 分页与 Codex debug models、Anthropic/Responses 推理探测与 `ProbeUnreachableError`）+ 单元测试
- [ ] T5: 实现 `internal/platform/provider/credential_service.go`（严格租户隔离 `NoCompanyKeyError`、优先级池、Layer-1 Failover、10m/1h 复检、实现 `gateway.CredentialResolver`）+ 单元测试
- [ ] T6: 实现 `internal/platform/provider/{provider_service.go,status_checker.go,model_call.go}`（写锁串行、串行安装+3m/30m 进程组强杀+EACCES 自动私有 Node、6 并发 sweep、5m 不可达重试、`ModelCall`）+ 单元测试
- [ ] T7: 实现 `internal/platform/integration/{service.go,app_url.go}`（V1 仅 `feishu` & `dingtalk`、密文存储、事件发布、状态 map、`AppUrl`）+ 单元测试
- [ ] T8: 实现 `internal/platform/connector/{oauth_provider.go,feishu_oauth.go,oauth_service.go,service.go,tools_service.go}`（`feishu-docs` + `dingtalk-tools` + 3 合成行 + 自定义 MCP + 10m state/双检刷新/4xx 自删 + 10m 工具缓存）+ 单元测试
- [ ] T9: 全量回归与竞态/架构守卫验证：`go test -race -cover ./internal/platform/{provider,integration,connector}/...` + `internal/arch_test.go`

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：合入后将 `SA-05` 状态更新为 `已实施（commit <sha>）`
- [ ] `docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §4：同步确认精简连接器与集成契约
- [ ] `configs/config.example.yaml`：补充 `provider.checker.*`、`runtime.node_dist_mirror`、`runtime.npm_registry`、`oauth.feishu.base_url`、`oauth.lark.base_url`、`app.base_url` 配置项

---

## 8. 测试清单

| 测试点 | 落位 | 关键断言 |
|---|---|---|
| `ProviderCatalog` V1 默认条目与增量扩展 | `internal/platform/provider/catalog_test.go` | 默认仅含 `claude` & `codex`；允许通过 `Register` 扩展新供应商且拒绝重复 Key |
| `NodeRuntimeService` 镜像下载、60s 停滞、SHA-256 校验与原子替换 | `internal/platform/provider/node_runtime_test.go` | 命中自定义 `node_dist_mirror`；校验失败拒绝解压且清理 `.staging-*`；慢响应触发 stall 中断；正常解压替换 `<nodeRoot>/current` 且 `PrivatePrefixFor` 生效 |
| `InstallJob` 400 行环与 `InstallFailures` 诊断 | `internal/platform/provider/install_test.go` | 写入 500 行仅留最后 400 行；准确映射 `ECONNREFUSED`/`EACCES`/`E404`/`EBADENGINE`/`ENOSPC`/`SELF_SIGNED_CERT_IN_CHAIN`/`E401` 中文文案并提取 URL |
| `ProviderService` 安装看门狗、EACCES 降级私有 Node 与 6 并发 sweep | `internal/platform/provider/provider_service_test.go` | 静默超时与总超时整组查杀；系统 npm 报 `EACCES` 自动装私有 Node 重试；`ProbeUnreachableError` 保留模型原 `ok` 状态并返回 `reached=false`；`CLIFor` 优先 `nativeModel` |
| `ProviderStatusChecker` 5 分钟单租户去重重试 | `internal/platform/provider/status_checker_test.go` | 某租户 `reached=false` 时仅排期 1 次 5 分钟重试且不影响其他租户 |
| `CredentialService` 租户隔离、Layer-1 切换与 10m/1h 复检 | `internal/platform/provider/credential_service_test.go` | 空池必返 `403 NoCompanyKeyError`（绝不读宿主机 `ANTHROPIC_API_KEY`）；429/401/连接错误标 `error` 并切下一高优 Key 且发 `onSwitch`；软删后保留 `key_hash`；`dueForCheck` 区分 `error`/`unknown`(每轮) 与 `ok`(1h)；`Mask` 脱敏保真 |
| `EndpointFailures` 状态码与 33 条短语覆盖 | `internal/platform/provider/endpoint_failures_test.go` | 覆盖 `API Error: 429`/`HTTP 529`/`quota`/`额度`/`ECONNREFUSED` 等；普通工具错误不触发 failover；`IsModelFailure` 额外识别 `404` 与 `MODEL_GONE` |
| `ModelCall` 临时目录清理、超时与 Token 记账 | `internal/platform/provider/model_call_test.go` | 无工具执行后删除 `sa-ask-*` 临时目录；超时取消子进程；`UsageRecorder.Tap` 正确扣减记账 |
| `IntegrationService` 精简边界与密文保护 | `internal/platform/integration/service_test.go` | `List` 仅含 `feishu` & `dingtalk`；访问 `github`/`notion` 返 `404`；留空 secret 保留原密文；出参 secret 永为 `null`；保存发布 `IntegrationChanged` |
| `ConnectorService` 精简播种、合成行、绑定码与运行期装配 | `internal/platform/connector/service_test.go` | `OnCompanyStarted` 仅播 `feishu-docs`(启用) 与 `dingtalk-tools`(默认关闭)；列表顶部正确合成 `superagent`/`feishu`/`dingtalk`；编辑/删除系统连接器返 `403`；切换 `authMethod` 清空旧凭证；`MCPServersFor` 跳过 `feishu-docs` 与未授权连接器、为 `dingtalk-tools` 注入 3 个 env |
| `ConnectorOAuthService` & `FeishuDocsOAuthClient` 防 CSRF、双检刷新与 4xx 吊销 | `internal/platform/connector/oauth_test.go` | `feishu`/`lark` 域名随设置切换；`state` 单次使用、过期/跨用户/跨租户返 `invalid_state`；并发 10 协程过期前 10 分钟内仅触发 1 次上游刷新；4xx 自动删库凭证；5xx 且未过期降级返回原 Token |
| `ConnectorToolsService` 10 分钟缓存与内置/外部探测 | `internal/platform/connector/tools_service_test.go` | 10 分钟内二次调用命中缓存、`refresh=true` 强刷、错误驱逐缓存；内置 `superagent`/`feishu`/`dingtalk`/`feishu-docs` 过滤精确；外部 MCP 正确传递 120s/30s 超时 |

---

## 9. 验收门

- [ ] `go build ./... && go test -race ./internal/platform/provider/... ./internal/platform/integration/... ./internal/platform/connector/...` 全绿（零 data race）
- [ ] §2 交付物表 12 项逐一存在且落位完全符合 `internal/platform/{provider,integration,connector}` 结构
- [ ] `internal/arch_test.go` 通过：确认 L2 零引入 L3+ 与 `internal/storage/postgres`
- [ ] V1 裁剪门禁核对：`ProviderCatalog` 默认仅 `claude` & `codex`；`IntegrationService` 仅 `feishu` & `dingtalk`；`ConnectorService` 仅播种 `feishu-docs` & `dingtalk-tools`（仓库内无 `github`/`gitee`/`notion`/`confluence` 预置集成与 OAuth 死代码）
- [ ] §3 四张表列/索引与 `SA-02` `00005_init_assets_and_platform.sql` 100% 一致
- [ ] §8 测试清单 12 组测试全绿，目标包语句覆盖率 ≥ 80%
- [ ] `SA-01`/`SA-02`/`SA-03`/`SA-04` 前置测试回归全绿

---

## 10. 可提交节点

- **建议主 commit**：`feat(platform): 实现模型供应商/私有Node/多Key故障转移及飞书钉钉与自定义MCP连接器（spec 05，PR SA-05）`
- **推荐分层拆分提交（可选）**：
  1. `feat(provider): 实现 ProviderCatalog(claude/codex)、NodeRuntimeService、InstallJob 与 ProviderProbe`
  2. `feat(provider): 实现 CredentialService、EndpointFailures、ProviderModelFetcher、ProviderInferenceProbe、ProviderService 与 ModelCall`
  3. `feat(connector): 实现精简版 IntegrationService(feishu/dingtalk)、ConnectorService、FeishuDocsOAuth 与 ConnectorToolsService`
