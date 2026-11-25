# spec 20 | L2 平台管理 — 飞书/钉钉企业集成、内置连接器 OAuth 与自定义 MCP 工具发现

- **状态**：`draft`
- **篇号顺序**：20（L2 供应商与连接器域收口篇；落实 V1 裁剪红线——保留飞书/钉钉生态与通用自定义 MCP，彻底移除 4 套公网 SaaS 预置连接器）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-20`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §4
  - 归档母本：[_epic/L2/spec_05_platform_provider_node_credentials_connectors.md](../_epic/L2/spec_05_platform_provider_node_credentials_connectors.md) §5.6–§5.9
  - [SuperAgent/03_内置MCP工具网关与外部连接器集成.md](../../../SuperAgent/03_内置MCP工具网关与外部连接器集成.md)
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-platform/src/main/java/com/superagent/platform/integration/IntegrationService.java` (`L30-L247`)
    - `superagent-web/src/main/java/com/superagent/web/integration/AppUrl.java` (`L15-L36`)
    - `superagent-platform/src/main/java/com/superagent/platform/connector/ConnectorService.java` (`L47-L565`)
    - `superagent-platform/src/main/java/com/superagent/platform/connector/FeishuDocsOAuthClient.java` (`L25-L122`)
    - `superagent-platform/src/main/java/com/superagent/platform/connector/ConnectorOAuthService.java` (`L37-L212`)
    - `superagent-platform/src/main/java/com/superagent/platform/connector/ConnectorToolsService.java` (`L35-L148`)
- **前置依赖**：
  - [L0/spec_03 (SA-03)](../L0/spec_03_httpclient_resilience_egress.md)（`TrustedClient` 供飞书/钉钉 OpenAPI；`UntrustedEgressClient` 供用户自定义外部 MCP）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`SecretCipher`、`eventbus`、`memcache`）
  - [L1/spec_07 (SA-07)](../L1/spec_07_pg_storage_platform_providers.md)（`ConnectorStore`/`ConnectorCredentialStore`/`SystemSettingStore`）
  - [L1/spec_14 (SA-14)](../L1/spec_14_runtime_model_gateway_mcp_client.md)（`McpGrants`/`McpClient`）
  - [L2/spec_17 (SA-17)](./spec_17_platform_settings_notice_analytics.md)（`SettingsService.PublicURL`）
- **下游消费者**：`SA-26`（飞书/钉钉文档工具读取 `UserToken`/`IntegrationService.Value`）、`SA-28`/`SA-30`（会话与任务运行期装配 `MCPServersFor`）、`SA-33`–`SA-35`（飞书/钉钉 IM 长连接监听 `IntegrationChanged` 与回写连接状态）、`SA-39`（集成/连接器/OAuth HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业内部落地时，员工最核心的协同载体是**飞书 / Lark** 与 **钉钉**，同时企业还会将自研 CMDB、工单、内网文档等能力通过标准 **MCP（HTTP / SSE / Stdio）** 接入平台。

本篇（`SA-20`）完整交付企业集成与外部连接器管理子系统，严格执行商业私有化 V1 裁剪边界：
1. **集成收敛为 `feishu` & `dingtalk`**：保存时自动每租户 DEK 加密 Secret 并发布 `IntegrationChanged` 驱动 L4 IM 长连接热重载；
2. **连接器仅预置飞书/钉钉生态 + 通用自定义 MCP**：租户启动时仅自动播种 `feishu-docs`（OAuth 个人授权）与 `dingtalk-tools`（企业应用 Stdio MCP，默认关闭需管理员显式开启），列表顶部合成 `superagent`/`feishu`/`dingtalk` 三张内置卡片；**彻底移除原版 GitHub / Gitee / Notion / Confluence 4 套公网 SaaS 预置及对应 OAuth 客户端**；
3. **飞书 OAuth 强安全双检刷新 + 自定义 MCP 抗 SSRF**：OAuth `state` 显式绑定 `CompanyID` + `Owner` 防跨租户 CSRF；刷新 Token 采用按人细粒度互斥锁双检且遇 4xx 撤销立即自删；外部自定义 HTTP/SSE MCP 强制走 L0 `UntrustedEgressClient`。

### 1.2 本篇边界

- **本篇负责**：
  - `internal/platform/integration`：`IntegrationService`（`feishu`/`dingtalk` 读写、密文保留与脱敏、运行态状态表、`IntegrationChanged` 事件）+ `AppUrl`（回调基址 3 级优先级解析）；
  - `internal/platform/connector`：`OAuthProvider` 接口、`FeishuDocsOAuthClient`、`ConnectorOAuthService`（10m State、双检锁刷新、4xx 自删）、`ConnectorService`（播种、CRUD、权限保护、合成行、`MCPServersFor`/`SpecFor`/`UserToken`）、`ConnectorToolsService`（10m 进程内缓存与内外置工具发现）。
- **本篇不负责**：飞书/钉钉文档 MCP 工具实现（交由 `SA-26`）；飞书/钉钉 IM 长连接实现（交由 `SA-33`/`SA-34`）；Gin 路由挂载（交由 `SA-39`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 企业集成服务 `IntegrationService`（`feishu` & `dingtalk`）与回调基址 `AppUrl` | `internal/platform/integration/service.go`、`app_url.go` | 新增 |
| 2 | OAuth 抽象、`FeishuDocsOAuthClient` 与 `ConnectorOAuthService`（防跨租户 CSRF + 双检锁刷新） | `internal/platform/connector/oauth_provider.go`、`feishu_oauth.go`、`oauth_service.go` | 新增 |
| 3 | 连接器治理与运行期 MCP 装配服务 `ConnectorService` | `internal/platform/connector/service.go` | 新增 |
| 4 | 工具发现与 10m 进程内缓存服务 `ConnectorToolsService` | `internal/platform/connector/tools_service.go` | 新增 |
| 5 | 集成、OAuth、连接器与工具发现单元测试套件 | `internal/platform/integration/service_test.go`、`internal/platform/connector/{oauth,service,tools}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，直接复用 `SA-07` 落盘的既有表：
- `system_setting`：存储 `integration.<key>.*`（Secret 字段经 `SecretCipher.Encrypt` 存 `v2:<companyID>:` 密文）及单次播种标志 `connector.seeded.feishu-docs`、`connector.seeded.dingtalk-tools`；
- `connector` 与 `connector_credential`（`owner=""` 表示公司共享凭证，个人 Token/OAuth 存 `owner=userID`）；
- `event_source`（仅读取当前员工的飞书/钉钉绑定态与绑定码供连接器卡片合成展示）。

OAuth 临时 `state`（TTL 10m）与工具发现缓存（TTL 10m）均使用 L0 `internal/infra/memcache` 纯进程内缓存，**零 Redis 依赖**。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package integration

const (
	KeyFeishu          = "feishu"
	KeyDingTalk        = "dingtalk"
	FeishuDomainFeishu = "feishu"
	FeishuDomainLark   = "lark"
)

type IntegrationService struct { /* settings, cipher, bus, mu, statuses map[string]statusEntry */ }
func (s *IntegrationService) List(ctx context.Context, baseURL string) ([]IntegrationDto, error)
func (s *IntegrationService) Update(ctx context.Context, key string, req SaveIntegrationRequest, baseURL string) (*IntegrationDto, error)
func (s *IntegrationService) Ready(ctx context.Context, key string) (bool, error)
func (s *IntegrationService) Value(ctx context.Context, key, field string) (string, bool)
func (s *IntegrationService) SetStatus(ctx context.Context, key, state, errMsg string)
func (s *IntegrationService) Status(ctx context.Context, key string) (state, lastError string)
func CallbackURL(key, baseURL string) string
func ResolveAppBaseURL(ctx context.Context, settingsPublicURL, cfgBaseURL string, r *http.Request) string
```

```go
package connector

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

type TokenRejectedError struct{ Message string }
func (e *TokenRejectedError) Error() string { return e.Message }

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

type ConnectorOAuthService struct { /* providers, integrations, credentials, cipher, states *memcache.Cache, refreshLocks sync.Map */ }
func (s *ConnectorOAuthService) IsConfigured(ctx context.Context, provider string) bool
func (s *ConnectorOAuthService) ProviderFor(connectorID string) (string, bool)
func (s *ConnectorOAuthService) BuildAuthorizeURL(ctx context.Context, provider, connectorID, owner, baseURL string) (string, error)
func (s *ConnectorOAuthService) HandleCallback(ctx context.Context, provider, code, state, caller string) (string, error)
func (s *ConnectorOAuthService) AccessToken(ctx context.Context, cred model.ConnectorCredential) (string, bool)

type ConnectorService struct { /* store, credentials, settings, integrations, oauth, cipher, sources, grants */ }
func (s *ConnectorService) OnCompanyStarted(ctx context.Context, ev eventbus.CompanyStarted)
func (s *ConnectorService) List(ctx context.Context, viewer string, admin bool) ([]ConnectorDto, error)
func (s *ConnectorService) Create(ctx context.Context, req SaveConnectorRequest, viewer string) (*ConnectorDto, error)
func (s *ConnectorService) Update(ctx context.Context, id string, req SaveConnectorRequest, viewer string) (*ConnectorDto, error)
func (s *ConnectorService) SetEnabled(ctx context.Context, id string, enabled bool, viewer string) (*ConnectorDto, error)
func (s *ConnectorService) Delete(ctx context.Context, id string) error
func (s *ConnectorService) Authorize(ctx context.Context, id, viewer, token string, admin bool) (*ConnectorDto, error)
func (s *ConnectorService) Revoke(ctx context.Context, id, viewer string, admin bool) (*ConnectorDto, error)
func (s *ConnectorService) NewBindCode(ctx context.Context, id, viewer string, admin bool) (*ConnectorDto, error)
func (s *ConnectorService) MCPServersFor(ctx context.Context, owner string) ([]model.MCPServerSpec, error)
func (s *ConnectorService) SpecFor(ctx context.Context, id, owner string) (*model.MCPServerSpec, bool)
func (s *ConnectorService) AuthorizedBy(ctx context.Context, id, viewer string) bool
func (s *ConnectorService) UserToken(ctx context.Context, connectorID, owner string) (string, bool)

type ConnectorToolsService struct { /* connectors, integrations, registry spi.McpToolRegistry, mcpClient, cache *memcache.Cache */ }
func (s *ConnectorToolsService) Tools(ctx context.Context, id, viewer string, refresh bool) (*ConnectorToolsDto, error)
```

### 4.2 路由契约（由 `L5/spec_39` 挂载）

- `GET /api/v1/admin/integrations`、`PUT /api/v1/admin/integrations/:key`（`ADMIN`）；
- `GET /api/v1/connectors`（全员）、`POST /api/v1/connectors`（`ADMIN`）、`PUT/DELETE /api/v1/connectors/:id`（`ADMIN`）、`PUT /api/v1/connectors/:id/enabled`（`ADMIN`）；
- `GET /api/v1/connectors/:id/tools`、`PUT/DELETE /api/v1/connectors/:id/token`、`POST /api/v1/connectors/:id/bind-code`（登录用户）；
- `GET /api/v1/oauth/:provider/authorize`、`GET /api/v1/oauth/:provider/callback`（登录用户，统一 `302` 重定向至授权页或 `/?connected=<id>#connectors` / `/?connect_error=<code>#connectors`）。

### 4.3 错误语义

- 访问已移除或未知集成（如 `github`/`notion`）：`404 not_found` `"No such integration: <key>"`；
- 尝试编辑/删除系统连接器（`feishu-docs`/`dingtalk-tools`）或 3 张合成卡片（`superagent`/`feishu`/`dingtalk`）：`403 forbidden`；
- `STDIO` 缺少命令或 `TokenEnv` 不匹配 `^[A-Za-z_][A-Za-z0-9_]{0,127}$`、`HTTP`/`SSE` 非 `http(s)://`：`400 invalid_argument`；
- 飞书 OAuth 刷新收到 4xx `*TokenRejectedError`：静默自删该用户 `ConnectorCredential` 并返 `("", false)`，前端立即提示重新授权；
- OAuth 回调校验失败：返回 `*OAuthError`（`no_oauth_provider`/`not_configured`/`unknown_connector`/`invalid_state`/`missing_code`/`exchange_failed`）供 L5 重定向 `/?connect_error=<code>#connectors`。

### 4.4 SQL 契约

- `ConnectorService.Update` 若变更 `AuthMethod`，立即调用 `ConnectorCredentialStore.DeleteAll(ctx, id)` 清空旧授权；
- `ConnectorOAuthService.AccessToken` 刷新时先在锁内重读 DB 双检，防止并发刷新互相覆盖 `refresh_token`。

---

## 5. 行为语义

### 5.1 `IntegrationService`（V1 仅保留 `feishu` & `dingtalk`）

- 仅定义两条集成：`feishu`（`domain` 白名单 `feishu`/`lark`、`appId`、`appSecret` 密文，回调 `/api/v1/oauth/feishu-docs/callback`）与 `dingtalk`（`clientId`、`clientSecret` 密文）；
- `Update`：留空 secret 保留库中原密文；`enabled=true` 时所有无默认值字段必填；停用时清理 `statuses[companyID+"/"+key]`；写库后发布 `eventbus.IntegrationChanged{Key: key}`；
- `List`：所有 secret 字段输出 `Value = nil`、仅返回 `Set = (stored != "")`。

### 5.2 `ConnectorService` 播种、合成行与运行期装配（对齐 `ConnectorService.java:47-565`）

1. **`OnCompanyStarted` 单次播种**：仅播种 `feishu-docs`（`OAUTH`, `Enabled=true`, `Endpoint=""`, 由 `SA-26` 内置工具承接，不作为外部 MCP 启动）与 `dingtalk-tools`（`NONE`, `Enabled=false`, `STDIO`, `Endpoint="npx -y dingtalk-mcp@latest"`），绝不播种 `github`/`gitee`/`notion`/`confluence`；
2. **`List` 合成顺序**：`superagent`（内置）→ `feishu`（当 `integrations.Ready("feishu")`）→ `dingtalk`（当 `integrations.Ready("dingtalk")`）→ 库内连接器；
3. **`SpecFor(ctx, id, owner)` 装配**：
   - 跳过 `Endpoint == ""`（如 `feishu-docs`）、未启用或所需集成未就绪者；
   - `AuthOAuth` 调用 `oauth.AccessToken(ctx, cred)`，普通 Token 用 `cipher.Decrypt`（解密失败静默跳过，不打断 Agent 对话）；
   - `dingtalk-tools` 自动注入 `DINGTALK_Client_ID`、`DINGTALK_Client_Secret` 与 `ACTIVE_PROFILES="dingtalk-contacts,dingtalk-department,dingtalk-calendar,dingtalk-tasks,dingtalk-report"`。

### 5.3 `FeishuDocsOAuthClient` 与 `ConnectorOAuthService`（对齐 `FeishuDocsOAuthClient.java:25-122`、`ConnectorOAuthService.java:37-212`）

```go
package connector

func (s *ConnectorOAuthService) AccessToken(ctx context.Context, cred model.ConnectorCredential) (string, bool) {
	now := time.Now().UTC()
	if cred.ExpiresAt == nil || now.Add(10*time.Minute).Before(*cred.ExpiresAt) {
		tok, err := s.cipher.Decrypt(ctx, cred.SecretCipher)
		return tok, err == nil && tok != ""
	}
	cid, _ := tenant.Require(ctx)
	lockKey := cid + "/" + cred.ConnectorID + "/" + cred.Owner
	muI, _ := s.refreshLocks.LoadOrStore(lockKey, &sync.Mutex{})
	mu := muI.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()

	// 双检：另一协程可能刚刚刷新完成
	latest, err := s.credentials.Find(ctx, cred.ConnectorID, cred.Owner)
	if err != nil || latest == nil { return "", false }
	if latest.ExpiresAt == nil || time.Now().UTC().Add(10*time.Minute).Before(*latest.ExpiresAt) {
		tok, derr := s.cipher.Decrypt(ctx, latest.SecretCipher)
		return tok, derr == nil && tok != ""
	}
	refTok, err := s.cipher.Decrypt(ctx, latest.RefreshCipher)
	if err != nil || refTok == "" { return "", false }
	provKey, ok := s.ProviderFor(latest.ConnectorID)
	if !ok { return "", false }
	prov := s.providers[provKey]
	clientID, _ := s.integrations.Value(ctx, prov.Integration(), prov.ClientIDField())
	clientSecret, _ := s.integrations.Value(ctx, prov.Integration(), prov.ClientSecretField())

	tokens, rerr := prov.Refresh(ctx, clientID, clientSecret, refTok)
	if rerr != nil {
		var rejected *TokenRejectedError
		if errors.As(rerr, &rejected) {
			_ = s.credentials.Delete(ctx, latest.ConnectorID, latest.Owner)
			return "", false
		}
		// 瞬时网络/5xx 故障：若旧 token 尚未绝对过期，降级返回旧 token
		if latest.ExpiresAt != nil && time.Now().UTC().Before(*latest.ExpiresAt) {
			tok, derr := s.cipher.Decrypt(ctx, latest.SecretCipher)
			return tok, derr == nil && tok != ""
		}
		return "", false
	}
	// 轮换写入新密文并返回 tokens.Access（保留原 refresh 若未返回新 refresh）
	return s.persistRefreshed(ctx, *latest, tokens)
}
```

### 5.4 `ConnectorToolsService`（对齐 `ConnectorToolsService.java:35-148`）

- 缓存键 `<companyID>|<connectorID>|<viewer>` 存于 L0 `memcache`（TTL `10m`）；有错即删缓存；
- 4 个内置分支：`superagent`（汇总 `McpToolRegistry` 中全部支持 `chat`/`task` 的内置工具）、`feishu`（返回空列表+中文说明）、`dingtalk`（过滤 `dingtalk_doc_*`）、`feishu-docs`（校验已授权后过滤 `feishu_doc_*`）；
- 外部自定义 MCP：经 `SA-14` `McpClient.ListTools`（HTTP/SSE 强制走 L0 `UntrustedEgressClient` 防 SSRF，超时 30s；Stdio 超时 120s）解析 `inputSchema` 与 `annotations`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/platform/integration/service_test.go`（红灯）→ 实现 `IntegrationService`（仅 `feishu`/`dingtalk`、密文保留、事件发布）与 `AppUrl`。
- [ ] **T2**：编写 `internal/platform/connector/oauth_test.go`（红灯，`httptest` 模拟飞书/Lark token 换取与刷新）→ 实现 `FeishuDocsOAuthClient` 与 `ConnectorOAuthService`（跨租户 CSRF 防护、并发双检锁、4xx 自删）。
- [ ] **T3**：编写 `internal/platform/connector/service_test.go`（红灯）→ 实现 `ConnectorService`（精简播种、3 合成行、系统连接器保护、`SpecFor` 与 `dingtalk-tools` 环境变量注入）。
- [ ] **T4**：编写 `internal/platform/connector/tools_test.go`（红灯）→ 实现 `ConnectorToolsService`（4 内置分支、外部 MCP 调用与 10m 内存缓存）。
- [ ] **T5**：运行 `go test -race -cover ./internal/platform/{integration,connector}/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-20` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `oauth.feishu.base_url`、`oauth.lark.base_url`、`app.base_url` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `IntegrationService` V1 精简边界与密文安全 | `internal/platform/integration/service_test.go` | `List` 仅含 `feishu`/`dingtalk`；访问 `github`/`notion` 返 `404`；留空 secret 保留原密文；出参 secret 恒 `nil`；保存发布 `IntegrationChanged` |
| 2 | `ConnectorOAuthService` 跨租户防 CSRF 与双检锁刷新 | `internal/platform/connector/oauth_test.go` | 租户 B 重放租户 A 的 `state` 返 `invalid_state`；10 协程并发刷新仅发起 1 次上游 HTTP 请求；4xx `*TokenRejectedError` 立即删除用户凭证；5xx 且未过期降级返回旧 token |
| 3 | `ConnectorService` 精简播种、保护与装配 | `internal/platform/connector/service_test.go` | `OnCompanyStarted` 仅播 `feishu-docs`(启) 与 `dingtalk-tools`(关)；编辑/删除系统连接器返 `403`；切换 `AuthMethod` 清旧凭证；`MCPServersFor` 跳过 `feishu-docs` 且为 `dingtalk-tools` 注入 `ACTIVE_PROFILES` |
| 4 | `ConnectorToolsService` 10m 缓存与内外置分支 | `internal/platform/connector/tools_test.go` | 命中 10m 缓存；`refresh=true` 强制刷新；失败清除旧缓存；未授权 `feishu-docs` 返 `403` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/platform/{integration,connector}/...` 全绿
- [ ] 代码中零残余 `github`/`gitee`/`notion`/`confluence` 预置连接器与 OAuth 客户端
- [ ] 飞书/Lark OAuth 走 L0 `TrustedClient`，自定义外部 HTTP/SSE MCP 走 L0 `UntrustedEgressClient`
- [ ] §2 交付物表 5 项齐备，覆盖率 ≥ 80%，零 Redis 引入

---

## 10. 可提交节点

- 建议 commit 1：`feat(platform-integration): add Feishu/DingTalk IntegrationService and AppUrl resolver (spec 20, SA-20)`
- 建议 commit 2：`feat(platform-connector): add FeishuDocs OAuth, ConnectorService and ConnectorToolsService (spec 20, SA-20)`
