# spec 13 | Web 网关、内置 MCP 端点与静态 UI 嵌入 — L5 传输与展示层全量重构规格

- **状态**：`draft`
- **篇号顺序**：13（L5 网关与静态 UI 嵌入层；汇总消费全部 L2–L4 服务）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-13`
- **上位契约**：
  - `docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §3（认证插件、6 级中间件链）、§5.2（统一错误映射）
  - `docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §3（内置 `/mcp` 协议网关与 `isError: true` 修复）
  - `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md` §1（前端契约兼容、4 处精准微调与 `synapse-web` 平滑演进）
  - `docs/SuperAgent/10_外部API与实时通信协议全量契约.md`（175+ API 契约、SSE 与 `/mcp` 协议细节）
  - `docs/SuperAgent/11_前端交互与界面功能规格.md`（静态 SPA 视图与交互流）
- **前置依赖**：`spec_01`–`spec_12`（全部 L0–L4 核心契约、存储、运行时与业务服务接口冻结；M0 阶段可先交付中间件骨架 + 认证/用户/设置路由，M1–M4 按里程碑增量挂载对应领域 Handler）
- **下游消费者**：`L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md`（`wire.go` 装配全部 Handler 并挂载 `/actuator/*` 系统路由与优雅停机排空）

---

## 1. 背景与范围

本篇定义 `synapse-server`（`module github.com/Karlsk/synapse-os`，Go 1.26.1）的 **L5 接入与展示层**，包含 `internal/web` 与 `internal/ui` 两个顶层子包，完整等价重构原版 Java 模块 `superagent-web`（39 个源码文件）与 `superagent-ui`（44 个静态前端资产文件）：

1. **Gin HTTP 网关与 6 级中间件流水线 (`internal/web`)**：
   - 将原版 Spring Security FilterChain（`TraceIdFilter` → `OrgLoginFilter` → `UsernamePasswordAuthenticationFilter` → `AuthorizationFilter` → `TenantFilter` → `PasswordChangeFilter`）与 `@RestControllerAdvice`（`GlobalExceptionHandler`）重构为显式、零魔法、基于 `context.Context` 的 Gin 6 级中间件链（`Recovery` → `TraceID` → `Auth` → `Tenant` → `PasswordChange` → `RBAC`）；
   - 完整实现 V1 商业私有化保留的 **175 个 REST/页面/认证端点**，严格对齐原版 `/api/v1` 路径、camelCase JSON 字段与响应信封例外（登录裸 JSON、登出空体、SSE 实时流、支持 `Range` 与防 XSS 预览的二进制文件流、OAuth `302` 重定向）；
   - 落地 V1 锁定裁剪与前端兼容契约：**彻底移除 `POST /api/v1/orgs`**（公网自助注册改为 L6 CLI `bootstrap-org`），同时保留 `GET /api/v1/orgs/registration` 恒返 `{"open": false}` 与 `GET /api/v1/agents/characters` 恒返 `[]` 保障旧版静态 UI 零报错运行；保留飞书/钉钉 OAuth 与企业集成路由。
2. **内置 MCP Streamable-HTTP 网关 (`internal/web/mcp`)**：
   - 1:1 翻译 `McpController.java` 实现 `POST /mcp` JSON-RPC 2.0（`protocolVersion: "2024-11-05"`、`GET /mcp -> 405`、Notification 无 `id` 返回 `202 Accepted`、48 位 Bearer 令牌与 `StandIn` 解析、租户 `context.Context` 注入）；
   - **修复原版核心缺陷**：在 `tools/call` 分发层统一捕获 `McpToolError` 及全部可预期领域错误（`apperror.NotFound`/`Forbidden`/`Conflict`/`Busy`/`InvalidArg`），一律以 JSON-RPC `result` + `"isError": true`（文本前缀 `"错误："`）返回给大模型自愈重试，杜绝泄露 `-32603` 协议错误。
3. **内嵌静态 UI 与双模资产服务 (`internal/ui`)**：
   - 使用 Go 原生 `//go:embed all:static` 将微调版静态 SPA 编译进单二进制，同时支持开发期 `--ui-dir <dir>` 磁盘热加载；
   - 精确落地 4 处轻量静态 UI 调整（隐藏组织注册入口、去 24 性格预设改纯自定义 `persona`、仅展示飞书/钉钉生态 + 自定义 MCP、Claude/Codex GA 目录展示），并冻结向独立现代前端 `synapse-web` 的平滑交接契约。

**本篇边界（明确不涵盖，交由其他 Spec）**：
- `/actuator/health`、`/actuator/prometheus`、`/actuator/info` 探针与业务指标注册、TCP 监听与 30 秒优雅停机编排、全量 L0→L6 `wire.go` 装配 → 见 `L6/spec_14`（本篇通过 `Router.RegisterSystemRoutes` 预留挂载钩子）；
- 认证校验逻辑（BCrypt、`SessionStore` 落库 `user_session`、`LocalAuthenticator` 实现 `spi.Authenticator`）→ 见 `L2/spec_04`；
- 各领域 Service 内部业务规则、`LiveTurn` 广播器、`McpGrants` 令牌簿与 20 个 `McpToolProvider` 实现 → 见 `L1/spec_03` 及 `L2–L4/spec_04–12`。

---

## 2. 本篇交付物表

| # | 交付物 | 落位 | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | Gin 路由总装配、统一参数校验、`:id` 归一化、`NoRoute`/`NoMethod` | `synapse-server/internal/web/router.go` | 新增 | `ApiPaths.java`、`SecurityConfig.java` |
| 2 | 统一响应信封、`HandleError` 异常映射、SSE 写帧器、安全图片/文件头助手 | `synapse-server/internal/web/response.go` | 新增 | `ApiResponse.java`、`GlobalExceptionHandler.java`、`ImageAttachmentStore.java:44-54` |
| 3 | 6 级中间件链（`Recovery`/`TraceID`/`Auth`/`Tenant`/`PasswordChange`/`RequireAdmin` + `X-Frame-Options`） | `synapse-server/internal/web/middleware.go` | 新增 | `TraceIdFilter.java`、`OrgLoginFilter.java`、`TenantFilter.java`、`PasswordChangeFilter.java`、`SecurityConfig.java` |
| 4 | 公网基址推导器 `AppURLResolver`（支持 `X-Forwarded-*`） | `synapse-server/internal/web/app_url.go` | 新增 | `AppUrl.java` |
| 5 | Meta、登录页、表单登录/登出、`/auth/me`、组织注册状态兼容端点 | `synapse-server/internal/web/handler_meta_auth.go` | 新增 | `MetaController.java`、`LoginPageController.java`、`AuthController.java`、`OrgController.java` |
| 6 | 账号自服务（改密/头像）、用户管理（Admin）、同事名录 Handler | `synapse-server/internal/web/handler_account_user.go` | 新增 | `AccountController.java`、`UserController.java`、`DirectoryController.java` |
| 7 | Agent CRUD、协作邀请、长期记忆、运行日志与会话历史 Handler | `synapse-server/internal/web/handler_agent.go` | 新增 | `AgentController.java`、`AgentInviteController.java`、`AgentMemoryController.java`、`AgentLogController.java` |
| 8 | 个人 SuperAgent 编排器状态、后台记录、工作台账、计划审批 Handler | `synapse-server/internal/web/handler_superagent.go` | 新增 | `SuperAgentController.java` |
| 9 | 对话会话 CRUD、`POST .../messages` SSE 流、`GET .../live/stream` 同屏旁观、停止与改写 Handler | `synapse-server/internal/web/handler_chat.go` | 新增 | `ChatController.java`、`ChatService.java`（SSE 帧与 `watchLive`） |
| 10 | 异步任务（16 端点）与自动化/外部 Webhook Trigger（11 端点）Handler | `synapse-server/internal/web/handler_task.go` | 新增 | `TaskController.java`、`AutomationController.java` |
| 11 | 项目（9 端点）、工作文件与目录（14 端点，含 `Range` 与防 XSS 预览）、存储后端（3 端点）、技能（11 端点）Handler | `synapse-server/internal/web/handler_assets.go` | 新增 | `ProjectController.java`、`FileController.java`、`FileStorageController.java`、`SkillController.java` |
| 12 | 连接器（9 端点）、OAuth 302 握手（2 端点）、供应商/Node/Key 池（19 端点）、模型定价（4 端点）、系统设置与 Logo（6 端点）、企业集成（2 端点）、收件箱（5 端点）、待办通知（6 端点）、用量（3 端点）、运营分析（1 端点）Handler | `synapse-server/internal/web/handler_platform.go` | 新增 | `ConnectorController.java`、`ConnectorOAuthController.java`、`ProviderController.java`、`ModelPriceController.java`、`SettingsController.java`、`IntegrationController.java`、`InboxController.java`、`NoticeController.java`、`UsageController.java`、`AnalyticsController.java` |
| 13 | 内置 MCP Streamable-HTTP JSON-RPC 2.0 Handler（统一 `isError: true` 修复） | `synapse-server/internal/web/mcp/handler.go` | 新增 | `McpController.java` |
| 14 | `//go:embed all:static` + `--ui-dir` 热加载静态文件系统与 HTTP 处理器 | `synapse-server/internal/ui/embed.go` | 新增 | `superagent-ui` Spring 静态资源托管 |
| 15 | V1 精简微调版静态前端资产（44 个文件，含 4 处精准调整） | `synapse-server/internal/ui/static/*` | 新增 | `superagent-ui/src/main/resources/static/*` |
| 16 | Web 网关、中间件、SSE、文件安全头、MCP、UI 嵌入全套单元测试 | `synapse-server/internal/web/*_test.go`、`internal/web/mcp/handler_test.go`、`internal/ui/embed_test.go` | 新增 | 新建 Go 单元测试套件 |

---

## 3. 数据库设计

本篇属于 L5 传输与展示层，**不新增任何数据库表或 `goose` 迁移脚本**，且严格遵循分层铁律（`internal/web` 与 `internal/ui` 严禁直接导入 `pgx` 或 `internal/storage/postgres`）。

L5 中间件与 Handler 仅通过 L2–L4 Service 接口间接读写已在 `L1/spec_02` 冻结的表：

| 涉及表（定义于 `spec_02`） | L5 触发点 | 经由服务接口 | 关键约束 |
|---|---|---|---|
| `user_session`（全局表） | `AuthMiddleware`（读取会话）、`POST /api/v1/auth/login`（创建会话）、`POST /api/v1/auth/logout`（销毁会话）、`PasswordChangeMiddleware`（回写 `password_ok = true` 缓存） | `auth.SessionStore` | Cookie `SYNAPSE_SESSION`（可配置名）仅存 64 位随机 hex ID，服务端查库验过期并滑动续期 |
| `app_user`（租户表） | `PasswordChangeMiddleware`（首次检查 `must_change_password`）、`GET /api/v1/auth/me` | `user.AccountService`、`store.UserStore` | 必须在 `TenantMiddleware` 注入 `companyID` 后执行，Fail-Closed |
| `company`（全局表） | `POST /api/v1/auth/login` 校验企业是否启用 | `spi.Authenticator`（`LocalAuthenticator`） | 禁用企业与错误密码返回相同 401 文案防枚举 |

---

## 4. 冻结契约

### 4.1 导出符号

```go
package web

import (
	"context"
	"io/fs"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	"github.com/Karlsk/synapse-os/internal/runtime/mcp"
)

// Options 控制 Web 网关运行期行为，由 cmd/synapse-server 配置注入。
type Options struct {
	Version              string        // GET /api/v1/meta 返回，如 "1.0.0"
	PublicBaseURL        string        // 配置项 app.base_url（SUPERAGENT_BASE_URL），低于系统设置 publicUrl
	MaxUploadBytes       int64         // 默认 50 << 20 (50 MB)；multipart 表单上限 51 << 20
	SSETimerTimeout      time.Duration // 默认 10 * time.Minute（对应 superagent.chat.sse-timeout-ms: 600000）
	SessionCookieName    string        // 默认 "SYNAPSE_SESSION"
	SessionCookieSecure  bool          // HTTPS 部署开启 true；本地 HTTP 默认 false
	SessionTTL           time.Duration // 默认 7 * 24 * time.Hour
	TrustProxyHeaders    bool          // 是否信任 X-Forwarded-Proto / X-Forwarded-Host（企业 Nginx 反代建议 true）
	UIStaticFS           fs.FS         // 来自 internal/ui.NewFS(uiDir)
	RegisterSystemRoutes func(r *gin.Engine, authMW gin.HandlerFunc) // 供 L6 spec_14 挂载 /actuator/*
}

// Dependencies 聚合所有 L1–L4 服务接口，由 L6 cmd/synapse-server/wire.go 显式注入。
type Dependencies struct {
	Authenticator   spi.Authenticator
	Sessions        SessionManager
	Accounts        AccountService
	Users           UserService
	Directory       PeopleDirectory
	OrgStore        OrgReader
	Settings        SettingsService
	Branding        BrandingService
	Integrations    IntegrationService
	Connectors      ConnectorService
	ConnectorTools  ConnectorToolsService
	ConnectorOAuth  ConnectorOAuthService
	Providers       ProviderService
	NodeRuntime     NodeRuntimeService
	Credentials     CredentialService
	ModelPrices     ModelPriceService
	Files           FileService
	FileFolders     FileFolderService
	FileStorage     FileStorageService
	Projects        ProjectService
	Skills          SkillService
	SkillDeletions  SkillDeletionService
	Agents          AgentService
	AgentInvites    AgentInviteService
	AgentLogs       AgentLogService
	Chat            ChatService
	Images          ImageAttachmentReader
	Tasks           TaskService
	Automations     AutomationService
	Inbox           UserInboxService
	Notices         NoticeService
	Usage           TokenUsageService
	Analytics       AnalyticsService
	SuperAgent      SuperAgentService
	McpGrants       *mcp.Grants
	McpProviders    []mcp.ToolProvider
	DetachBaseCtx   context.Context // 进程根 context，用于 SSE 断连后后台回合生命周期与优雅停机感知
}

// NewRouter 构造完整装配的 *gin.Engine（含 6 级中间件、175 REST/页面/认证路由、2 /mcp 路由、静态 UI 与系统路由钩子）。
func NewRouter(deps Dependencies, opts Options) (*gin.Engine, error)

// PrincipalFromContext 从 Gin/标准 context 中读取已认证主体（无则返回 nil, false）。
func PrincipalFromContext(ctx context.Context) (*spi.AuthPrincipal, bool)
```

```go
package ui

import "io/fs"

// NewFS 当 uiDir 非空时返回磁盘 os.DirFS(uiDir)（开发热加载），否则返回内嵌 embed.FS 的 static 子树（生产单二进制）。
func NewFS(uiDir string) (fs.FS, error)
```

### 4.2 全量路由契约（V1 保留 175 个 REST/页面/认证端点 + 2 个 `/mcp` 端点 = 177 端点）

> **Gin 路由树防冲突红线（关键避坑）**：原版 Spring MVC 在 `ProjectController.java:70` 中声明 `/api/v1/agents/{agentId}/project`，而 `AgentController.java` 中同级路径参数名为 `{id}`。Gin（`httprouter`）在同一路径层级注册不同参数名会直接 **panic**。Go 版统一将所有 `/api/v1/agents/:id/*` 的路径参数名冻结为 **`:id`**（包括 `PUT /api/v1/agents/:id/project`）。同时开启 `engine.HandleMethodNotAllowed = true`。

#### 4.2.1 V1 裁剪与前端兼容路由决策表

| 原版 Java 路由 | 原版源码位置 | V1 Go 决策 | 具体契约 |
|---|---|---|---|
| `POST /api/v1/orgs` | `OrgController.java:27-31` | **彻底移除** | 不注册该路由（命中 `NoMethod`/`NoRoute` 返回 `405`/`404`），杜绝公网自助开户；改由 `synapse-server bootstrap-org` CLI 创建租户 |
| `GET /api/v1/orgs/registration` | `OrgController.java:22-25` | **保留兼容桩** | 公开端点，恒返回 `200 {"code":"ok","message":"success","data":{"open":false}}`，驱动 `login.js:114-118` 自动隐藏「注册新组织」入口 |
| `GET /api/v1/agents/characters` | `AgentController.java:53-56` | **保留兼容桩** | 认证端点，恒返回 `200 {"code":"ok","message":"success","data":[]}`，配合 `agent-form.js` 调整仅用自由 `persona` |
| `GET /api/v1/oauth/:provider/authorize` & `callback` | `ConnectorOAuthController.java:36-64` | **保留（V1 仅 `feishu-docs`）** | 路由通用保留，下层 `ConnectorOAuthService` V1 仅注册 `feishu-docs` 提供商（未知 provider `302 /?connect_error=no_oauth_provider#connectors`） |
| `GET/PUT /api/v1/admin/integrations/*` | `IntegrationController.java:29-38` | **保留（V1 仅 `feishu`/`dingtalk`）** | 下层 `IntegrationService` 仅返回并接受 `feishu`、`dingtalk` 两项企业集成 |
| `/api/v1/auth/phone/**`、`/oauth2/**`、`/api/v1/auth/wechat/**` | `SecurityConfig.java:63,67`（占位无实现） | **V1 不注册** | 保留 `spi.Authenticator` 扩展点供 V2 接 OIDC/LDAP/企业微信 SSO |

#### 4.2.2 响应信封与 7 类特殊格式例外矩阵

标准成功信封固定为 `{"code":"ok","message":"success","data":<T|null>}`（对齐 `ApiResponse.java:16-30`）；以下 **7 类端点严禁包裹 `ApiResponse` 外壳**：

| # | 端点 | HTTP 状态码 | Content-Type & 关键头 | 响应体格式 | Java 参考 |
|---|---|---|---|---|---|
| 1 | `POST /api/v1/auth/login` | `200` / `401` | `application/json; charset=utf-8`；成功写 `Set-Cookie`（`HttpOnly; SameSite=Lax`） | 成功裸 JSON：`{"userId":"...","username":"...","roles":["USER","ADMIN"]}`<br>失败裸 JSON：`{"code":"unauthorized","message":"组织、用户名或密码错误，或账号未授权"}` | `SecurityConfig.java:95-106` |
| 2 | `POST /api/v1/auth/logout` | `200` | 清除 Session Cookie（`Max-Age=-1`） | 空响应体（0 字节） | `SecurityConfig.java:88-90` |
| 3 | `POST /api/v1/sessions/:id/messages`<br>`GET /api/v1/sessions/:id/live/stream` | `200`（冲突 `409` 仍返 `ApiResponse`） | `text/event-stream; charset=utf-8`、`Cache-Control: no-cache, no-transform`、`Connection: keep-alive`、`X-Accel-Buffering: no` | 逐帧即时 Flush：`event: <type>\ndata: {"type":"...","seq":<int64>,"payload":<string|null>}\n\n` | `ChatController.java:75-81,98-102`、`ChatService.java:945-949,1069` |
| 4 | `GET /api/v1/files/:id/content` | `200` / `206 Partial Content` | 文本类（`.md/.markdown/.txt/.csv/.json/.html/.htm`）强制 `text/plain; charset=utf-8`；图/音视频/PDF 原 MIME；否则或 `?download=true` 为 `application/octet-stream` + `Content-Disposition: attachment`；**必带** `X-Content-Type-Options: nosniff`、`Cache-Control: no-store`、`Accept-Ranges: bytes`；除 `.pdf` 外**必带** `Content-Security-Policy: sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'` | 原始文件二进制流（支持 `Range` 分片拖拽播放） | `FileController.java:41-49,108-127` |
| 5 | `GET /api/v1/branding/logo`<br>`GET /api/v1/avatars/:username`<br>`GET /api/v1/sessions/:id/messages/:messageId/images/:index`<br>`GET /api/v1/tasks/:id/messages/:messageId/images/:index`<br>`GET /api/v1/skills/:id/images/:name` | `200`（Logo 支持 `304`） | 嗅探后的光栅 MIME（`image/png\|jpeg\|gif\|webp`）+ `X-Content-Type-Options: nosniff`；Logo 为 `Cache-Control: no-cache` + `ETag`；其余图片为 `Cache-Control: private, max-age=2592000`（30 天） | 原始图片字节 `[]byte` | `SettingsController.java:62-71`、`ImageAttachmentStore.java:47-53` |
| 6 | `GET /api/v1/oauth/:provider/authorize`<br>`GET /api/v1/oauth/:provider/callback` | `302 Found` | `Location: <external_authorize_url>` 或 `/?connected=<id>#connectors` 或 `/?connect_error=<code>#connectors` | 空体（绝不返回 JSON 错误页） | `ConnectorOAuthController.java:36-73` |
| 7 | `GET /mcp`、`POST /mcp` | `405` / `401` / `202` / `200` | `POST` 成功为 `application/json; charset=utf-8` | `GET` 空体 405；无效 Bearer 空体 401；Notification 空体 202；其余为裸 JSON-RPC 2.0 `{"jsonrpc":"2.0","id":...,"result"|"error":...}` | `McpController.java:49-143` |

#### 4.2.3 177 个全量端点清单（按域分组核对）

> Auth 列说明：`Public` = 无需登录；`Auth` = 已登录（含 `PasswordChange` 校验）；`Auth(NoPwdGate)` = 已登录但豁免首次强制改密拦截；`Admin` = 已登录且角色含 `ADMIN`；`Token` = 端点自校验令牌并绑定租户。

**A. 元信息、页面、品牌与认证（7 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 1 | `GET` | `/api/v1/meta` | `Public` | `ApiResponse<MetaResponse{name,status,version}>` | `MetaController.java:29-33` |
| 2 | `GET` | `/login` | `Public` | 已登录 `302 /`；未登录 `200 login.html` | `LoginPageController.java:15-19` |
| 3 | `GET` | `/api/v1/branding/logo` | `Public` | 二进制图片 + `ETag` + `no-cache`（无租户回退 `DEFAULT_ID`） | `SettingsController.java:62-71` |
| 4 | `GET` | `/api/v1/orgs/registration` | `Public` | V1 固定返回 `ApiResponse<{open:false}>` | `OrgController.java:22-25` |
| 5 | `POST` | `/api/v1/auth/login` | `Public` | 表单 `org`/`username`/`password` → 裸 JSON `{userId,username,roles}` | `SecurityConfig.java:84-106`, `OrgLoginFilter.java:26-37` |
| 6 | `POST` | `/api/v1/auth/logout` | `Auth(NoPwdGate)` | 销毁会话 + 清 Cookie → 空体 `200` | `SecurityConfig.java:88-90` |
| 7 | `GET` | `/api/v1/auth/me` | `Auth(NoPwdGate)` | `ApiResponse<MeDto>`（含 `mustChangePassword`、`org`、`teams` 等） | `AuthController.java:40-55` |

**B. 个人账号、头像、用户管理与同事名录（10 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 8 | `PUT` | `/api/v1/account/password` | `Auth(NoPwdGate)` | 修改密码并清除 `mustChangePassword` + 会话 `password_ok=true` | `AccountController.java:29-33` |
| 9 | `PUT` | `/api/v1/account/avatar` | `Auth` | 上传 base64 光栅头像（≤1 MB），返 `ApiResponse<int64>` 版本号 | `AccountController.java:36-39` |
| 10 | `DELETE` | `/api/v1/account/avatar` | `Auth` | 移除本人头像 | `AccountController.java:41-45` |
| 11 | `GET` | `/api/v1/avatars` | `Auth` | `ApiResponse<map[userID]int64>` 全部头像版本映射 | `AccountController.java:48-51` |
| 12 | `GET` | `/api/v1/avatars/:username` | `Auth` | 二进制图片（参数实为 `userID`），`private, max-age=2592000` | `AccountController.java:54-57` |
| 13 | `GET` | `/api/v1/users` | `Admin` | 租户用户列表 | `UserController.java:28-31` |
| 14 | `POST` | `/api/v1/users` | `Admin` | 创建用户（配额 + 初始密码设 `mustChangePassword=true`） | `UserController.java:33-36` |
| 15 | `PUT` | `/api/v1/users/:id` | `Admin` | 更新用户（末位管理员保护） | `UserController.java:38-41` |
| 16 | `DELETE` | `/api/v1/users/:id` | `Admin` | 删除用户（末位管理员保护 + 预留 `OwnerTransferHook`） | `UserController.java:43-47` |
| 17 | `GET` | `/api/v1/directory/users` | `Auth` | 启用同事名录 `{id,username,displayName}`，按 `displayName` 忽略大小写排序 | `DirectoryController.java:27-34` |

**C. Agent、协作邀请、长期记忆与运行日志（21 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 18 | `GET` | `/api/v1/agents` | `Auth` | 可见 Agent 列表（自有 + 协作 + 项目共享） | `AgentController.java:37-40` |
| 19 | `POST` | `/api/v1/agents` | `Auth` | 创建 Agent（可选 JSON body，忽略 `character`） | `AgentController.java:42-46` |
| 20 | `GET` | `/api/v1/agents/avatars` | `Auth` | 内置 Emoji 头像列表 | `AgentController.java:48-51` |
| 21 | `GET` | `/api/v1/agents/characters` | `Auth` | V1 兼容桩：恒返 `ApiResponse<[]>` | `AgentController.java:53-56` |
| 22 | `GET` | `/api/v1/agents/:id` | `Auth` | Agent 详情 | `AgentController.java:58-61` |
| 23 | `PUT` | `/api/v1/agents/:id` | `Auth` | 更新 Agent（仅 Owner） | `AgentController.java:88-92` |
| 24 | `DELETE` | `/api/v1/agents/:id` | `Auth` | 软删 Agent（仅 Owner，禁删 `SUPER`） | `AgentController.java:108-112` |
| 25 | `GET` | `/api/v1/agents/:id/history` | `Auth` | 跨会话分段对话历史 | `AgentController.java:64-67` |
| 26 | `GET` | `/api/v1/agents/:id/model` | `Auth` | `ApiResponse<{"model": string\|null}>` 实际生效模型 | `AgentController.java:70-73` |
| 27 | `GET` | `/api/v1/agents/:id/timings` | `Auth` | 7 阶段耗时埋点列表 | `AgentController.java:76-79` |
| 28 | `GET` | `/api/v1/agents/:id/tasks` | `Auth` | 校验 `agents.Get` 后返回 `tasks.ListForAgent(id)` | `AgentController.java:82-86` |
| 29 | `POST` | `/api/v1/agents/:id/members` | `Auth` | 邀请协作者（仅 Owner） | `AgentController.java:95-99` |
| 30 | `DELETE` | `/api/v1/agents/:id/members/:username` | `Auth` | 移除协作者或本人退出 | `AgentController.java:102-106` |
| 31 | `GET` | `/api/v1/agents/:id/invites` | `Auth` | Agent 提议的协作邀请卡片列表 | `AgentInviteController.java:25-28` |
| 32 | `POST` | `/api/v1/agents/:id/invites/:inviteId/confirm` | `Auth` | 确认协作邀请（仅 Owner） | `AgentInviteController.java:30-33` |
| 33 | `POST` | `/api/v1/agents/:id/invites/:inviteId/cancel` | `Auth` | 取消协作邀请（仅 Owner） | `AgentInviteController.java:35-38` |
| 34 | `GET` | `/api/v1/agents/:id/memory` | `Auth` | 读取长期记忆与蒸馏状态 | `AgentMemoryController.java:27-30` |
| 35 | `PUT` | `/api/v1/agents/:id/memory` | `Auth` | 手动编辑长期记忆（仅 Owner） | `AgentMemoryController.java:32-36` |
| 36 | `POST` | `/api/v1/agents/:id/memory/distill` | `Auth` | 立即触发记忆蒸馏（仅 Owner） | `AgentMemoryController.java:38-41` |
| 37 | `GET` | `/api/v1/agents/:id/logs` | `Auth` | 运行日志（仅 Owner；`category` 可选，`limit` 默认 300） | `AgentLogController.java:28-35` |
| 38 | `GET` | `/api/v1/agents/:id/sessions` | `Auth` | 会话轮换历史（仅 Owner） | `AgentLogController.java:38-42` |

**D. 个人 SuperAgent 编排器（6 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 39 | `GET` | `/api/v1/superagent` | `Auth` | 个人 SuperAgent 状态、计划、团队规模与上限 | `SuperAgentController.java:32-35` |
| 40 | `GET` | `/api/v1/superagent/invites` | `Auth` | SuperAgent 为下属 Agent 提议的邀请卡片 | `SuperAgentController.java:38-41` |
| 41 | `GET` | `/api/v1/superagent/background` | `Auth` | 后台轮次记录（`limit` 默认 200，硬上限 1000） | `SuperAgentController.java:44-47` |
| 42 | `GET` | `/api/v1/superagent/work` | `Auth` | 派发任务台账（`status` 默认 `all`，`memberId` 可选，`limit` 默认 100） | `SuperAgentController.java:53-59` |
| 43 | `GET` | `/api/v1/superagent/work/:taskId/events` | `Auth` | 任务监管时间线事件 | `SuperAgentController.java:61-64` |
| 44 | `POST` | `/api/v1/superagent/plans/:planId/approve` | `Auth` | 批准团队建组/派活计划（解锁服务端硬门禁） | `SuperAgentController.java:66-69` |

**E. 对话会话与 SSE 实时流（11 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 45 | `POST` | `/api/v1/sessions` | `Auth` | 新建会话（可选 body） | `ChatController.java:44-48` |
| 46 | `GET` | `/api/v1/sessions` | `Auth` | 个人会话列表 | `ChatController.java:50-53` |
| 47 | `GET` | `/api/v1/sessions/:id` | `Auth` | 校验访问权后返回会话详情与消息 | `ChatController.java:55-59` |
| 48 | `PUT` | `/api/v1/sessions/:id/title` | `Auth` | 重命名会话 | `ChatController.java:61-66` |
| 49 | `DELETE` | `/api/v1/sessions/:id` | `Auth` | 软删会话 | `ChatController.java:68-73` |
| 50 | `POST` | `/api/v1/sessions/:id/messages` | `Auth` | **SSE 流式回复**（先鉴权与占锁，冲突返 JSON 409 `busy`，成功切 `text/event-stream`） | `ChatController.java:75-81` |
| 51 | `GET` | `/api/v1/sessions/:id/messages/:messageId/images/:index` | `Auth` | 二进制消息配图（`nosniff` + 30d 私有缓存） | `ChatController.java:83-88` |
| 52 | `GET` | `/api/v1/sessions/:id/live` | `Auth` | 轮询当前在途回合快照 `LiveTurnDto` | `ChatController.java:91-95` |
| 53 | `GET` | `/api/v1/sessions/:id/live/stream` | `Auth` | **SSE 同屏旁观流**（重放历史增量 + 实时事件；无在途回合推单帧 `idle` 后结束） | `ChatController.java:98-102` |
| 54 | `POST` | `/api/v1/sessions/:id/stop` | `Auth` | 取消在途回合（触发 `cancelFunc()` 杀子进程组） | `ChatController.java:104-109` |
| 55 | `PUT` | `/api/v1/sessions/:id/messages/:messageId` | `Auth` | 编辑用户消息内容 | `ChatController.java:111-117` |

**F. 异步任务与自动化（28 端点：Task 16 + Usage/task 1 + Automation 11）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 56 | `POST` | `/api/v1/tasks` | `Auth` | 创建任务（预读链接校验 409 + 月度 Token 配额 403） | `TaskController.java:52-55` |
| 57 | `GET` | `/api/v1/tasks/agents` | `Auth` | 可运行 CLI 与其已存模型列表（V1 为 Claude/Codex） | `TaskController.java:58-66` |
| 58 | `GET` | `/api/v1/tasks` | `Auth` | 分页任务列表（`page=1,size=20,status,tag,agent,source,project`） | `TaskController.java:72-82` |
| 59 | `GET` | `/api/v1/tasks/facets` | `Auth` | 标签/Agent/来源分面统计与花费 | `TaskController.java:85-88` |
| 60 | `GET` | `/api/v1/tasks/stats` | `Auth` | 个人任务 KPI 统计 | `TaskController.java:102-105` |
| 61 | `GET` | `/api/v1/tasks/:id` | `Auth` | 任务详情（含 brief、messages、events、phases） | `TaskController.java:107-110` |
| 62 | `GET` | `/api/v1/tasks/:id/status` | `Auth` | 任务轻量状态轮询 | `TaskController.java:129-132` |
| 63 | `GET` | `/api/v1/tasks/:id/events` | `Auth` | 增量事件轮询（`offset` 默认 0） | `TaskController.java:112-116` |
| 64 | `GET` | `/api/v1/tasks/:id/relations` | `Auth` | 父/子/续跑任务关系图 | `TaskController.java:91-94` |
| 65 | `PUT` | `/api/v1/tasks/:id/tags` | `Auth` | 更新任务标签（仅 Owner） | `TaskController.java:96-100` |
| 66 | `GET` | `/api/v1/tasks/:id/model-choices` | `Auth` | 模型路由决策审计轨迹 | `TaskController.java:118-121` |
| 67 | `GET` | `/api/v1/tasks/:id/timing` | `Auth` | 任务等待与执行阶段时间轴 | `TaskController.java:124-127` |
| 68 | `GET` | `/api/v1/tasks/:id/usage` | `Auth` | 先验 `tasks.Status` 再返任务 Token 账单 | `UsageController.java:31-35` |
| 69 | `POST` | `/api/v1/tasks/:id/messages` | `Auth` | 追问/确认/继续引导任务 | `TaskController.java:134-139` |
| 70 | `GET` | `/api/v1/tasks/:id/messages/:messageId/images/:index` | `Auth` | 二进制任务消息配图（先验 `tasks.Status` 防探测 404） | `TaskController.java:141-146` |
| 71 | `POST` | `/api/v1/tasks/:id/stop` | `Auth` | 停止运行中任务（仅 Owner） | `TaskController.java:148-152` |
| 72 | `DELETE` | `/api/v1/tasks/:id` | `Auth` | 软删任务（仅 Owner） | `TaskController.java:154-158` |
| 73 | `GET` | `/api/v1/automations` | `Auth` | 自动化列表（Admin 看全公司，普通员工看本人） | `AutomationController.java:41-44` |
| 74 | `POST` | `/api/v1/automations` | `Auth` | 创建自动化（`source="manual"`；`type=api` 一次性返回明文 token） | `AutomationController.java:46-49` |
| 75 | `POST` | `/api/v1/automations/preview` | `Auth` | 预览后续触发时间（ISO-8601 UTC 数组） | `AutomationController.java:52-55` |
| 76 | `GET` | `/api/v1/automations/by-task/:taskId` | `Auth` | 按任务反查来源自动化（无则 `data: null`） | `AutomationController.java:58-61` |
| 77 | `GET` | `/api/v1/automations/:id` | `Auth` | 自动化详情 + 运行记录 + 后续预览 | `AutomationController.java:63-66` |
| 78 | `PUT` | `/api/v1/automations/:id` | `Auth` | 更新自动化 | `AutomationController.java:68-72` |
| 79 | `POST` | `/api/v1/automations/:id/enabled` | `Auth` | 启停切换（Query `value=true\|false`） | `AutomationController.java:74-78` |
| 80 | `POST` | `/api/v1/automations/:id/run` | `Auth` | 立即手动触发 | `AutomationController.java:80-83` |
| 81 | `POST` | `/api/v1/automations/:id/token` | `Auth` | 轮换 Webhook Token（一次性明文返回） | `AutomationController.java:85-88` |
| 82 | `DELETE` | `/api/v1/automations/:id` | `Auth` | 硬删自动化（保留 `automation_run` 历史） | `AutomationController.java:90-94` |
| 83 | `POST` | `/api/v1/automations/:id/trigger` | `Token` | 外部 Webhook 触发（Header `X-Automation-Token: <company>.<secret>`，内部绑定租户） | `AutomationController.java:96-101` |

**G. 知识资产：项目、工作文件与目录、文件存储、技能（37 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 84 | `GET` | `/api/v1/projects` | `Auth` | 所在活跃/归档项目列表 | `ProjectController.java:29-32` |
| 85 | `POST` | `/api/v1/projects` | `Auth` | 新建项目 | `ProjectController.java:34-37` |
| 86 | `GET` | `/api/v1/projects/:id` | `Auth` | 项目详情（非成员 404 防探测） | `ProjectController.java:39-42` |
| 87 | `PUT` | `/api/v1/projects/:id` | `Auth` | 更新名称/描述（仅 Owner） | `ProjectController.java:44-47` |
| 88 | `POST` | `/api/v1/projects/:id/archive` | `Auth` | 归档项目（成员只读，仅 Owner） | `ProjectController.java:49-52` |
| 89 | `POST` | `/api/v1/projects/:id/reopen` | `Auth` | 重开项目（仅 Owner） | `ProjectController.java:54-57` |
| 90 | `POST` | `/api/v1/projects/:id/members` | `Auth` | 添加成员（仅 Owner） | `ProjectController.java:59-63` |
| 91 | `DELETE` | `/api/v1/projects/:id/members/:username` | `Auth` | 移除成员（禁删 Owner） | `ProjectController.java:65-68` |
| 92 | `PUT` | `/api/v1/agents/:id/project` | `Auth` | 归属/移出 Agent 项目（**统一用 `:id`** 防 Gin 路由树冲突） | `ProjectController.java:70-75` |
| 93 | `GET` | `/api/v1/files/folders` | `Auth` | 个人 3 层虚拟目录树 | `FileController.java:59-62` |
| 94 | `POST` | `/api/v1/files/folders` | `Auth` | 新建个人目录 | `FileController.java:64-67` |
| 95 | `PUT` | `/api/v1/files/folders/:id` | `Auth` | 重命名个人目录 | `FileController.java:69-73` |
| 96 | `DELETE` | `/api/v1/files/folders/:id` | `Auth` | 删除目录（子项无损上提一级） | `FileController.java:76-80` |
| 97 | `PUT` | `/api/v1/files/:id/folder` | `Auth` | 移动文件至个人目录（空 `folderId` 置未归档） | `FileController.java:82-86` |
| 98 | `GET` | `/api/v1/files` | `Auth` | 文件列表（优先级 `project > task > agent`） | `FileController.java:89-100` |
| 99 | `GET` | `/api/v1/files/:id` | `Auth` | 文件元数据详情 | `FileController.java:102-105` |
| 100 | `GET` | `/api/v1/files/:id/content` | `Auth` | **二进制流式下载/预览**（`?download=false`，支持 `Range` + XSS 防护 + `CSP: sandbox`） | `FileController.java:108-127` |
| 101 | `POST` | `/api/v1/files/uploads` | `Auth` | Multipart `file` 上传（≤50 MB，超限 `413 too_large`；可选 `agent`/`task`/`folder`） | `FileController.java:133-143` |
| 102 | `POST` | `/api/v1/files/markdown` | `Auth` | 在线新建 `.md` 文件（可选 `folderId`） | `FileController.java:146-150` |
| 103 | `PUT` | `/api/v1/files/:id/content` | `Auth` | 在线编辑 `.md`/`.txt` 内容 | `FileController.java:152-156` |
| 104 | `PUT` | `/api/v1/files/:id` | `Auth` | 重命名文件 | `FileController.java:166-170` |
| 105 | `PUT` | `/api/v1/files/:id/tags` | `Auth` | 人工覆盖文件标签（锁定 `tag_state='user'`） | `FileController.java:173-177` |
| 106 | `DELETE` | `/api/v1/files/:id` | `Auth` | 删存储字节 + 软删元数据 | `FileController.java:179-183` |
| 107 | `GET` | `/api/v1/admin/file-storage` | `Admin` | 存储渠道总览与每渠道文件数 | `FileStorageController.java:24-27` |
| 108 | `PUT` | `/api/v1/admin/file-storage` | `Admin` | 写读删实测探活并切换活跃渠道 | `FileStorageController.java:30-33` |
| 109 | `POST` | `/api/v1/admin/file-storage/test` | `Admin` | 存储渠道干跑探活 | `FileStorageController.java:35-39` |
| 110 | `GET` | `/api/v1/skills/deletions` | `Auth` | Agent 提议删卡列表（必填 Query `agentId`） | `SkillController.java:37-40` |
| 111 | `POST` | `/api/v1/skills/deletions/:deletionId/confirm` | `Auth` | 确认执行技能软删（仅请求本人） | `SkillController.java:42-45` |
| 112 | `POST` | `/api/v1/skills/deletions/:deletionId/cancel` | `Auth` | 取消技能删卡（仅请求本人） | `SkillController.java:47-50` |
| 113 | `GET` | `/api/v1/skills` | `Auth` | 可见技能列表（企业 + 本人） | `SkillController.java:52-55` |
| 114 | `POST` | `/api/v1/skills` | `Auth` | 新建技能 | `SkillController.java:57-60` |
| 115 | `GET` | `/api/v1/skills/:id` | `Auth` | 技能详情 | `SkillController.java:62-65` |
| 116 | `PUT` | `/api/v1/skills/:id` | `Auth` | 更新技能（含 Admin 升降级 `space`） | `SkillController.java:67-70` |
| 117 | `DELETE` | `/api/v1/skills/:id` | `Auth` | 软删技能 | `SkillController.java:72-76` |
| 118 | `POST` | `/api/v1/skills/:id/images` | `Auth` | 上传技能 base64 光栅配图 | `SkillController.java:78-81` |
| 119 | `GET` | `/api/v1/skills/:id/images/:name` | `Auth` | 二进制技能配图（`nosniff` + 30d 私有缓存） | `SkillController.java:83-86` |
| 120 | `DELETE` | `/api/v1/skills/:id/images/:name` | `Auth` | 删除技能配图 | `SkillController.java:88-91` |

**H. 连接器、OAuth、模型供应商、定价、设置、集成、收件箱、通知与用量分析（55 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 121 | `GET` | `/api/v1/connectors` | `Auth` | 连接器列表（含内置/飞书/钉钉合成行；非 Admin 脱敏配置字段） | `ConnectorController.java:45-48` |
| 122 | `GET` | `/api/v1/connectors/:id/tools` | `Auth` | 探测 MCP 工具清单（Query `refresh=false`） | `ConnectorController.java:39-43` |
| 123 | `POST` | `/api/v1/connectors` | `Admin` | 新增自定义 MCP 连接器 | `ConnectorController.java:50-53` |
| 124 | `PUT` | `/api/v1/connectors/:id` | `Admin` | 编辑自定义 MCP 连接器 | `ConnectorController.java:55-59` |
| 125 | `PUT` | `/api/v1/connectors/:id/enabled` | `Admin` | 启停开关（系统连接器仅允此项） | `ConnectorController.java:62-66` |
| 126 | `DELETE` | `/api/v1/connectors/:id` | `Admin` | 删除自定义连接器 | `ConnectorController.java:68-72` |
| 127 | `PUT` | `/api/v1/connectors/:id/token` | `Auth` | 设置个人令牌（Admin 可设共享令牌） | `ConnectorController.java:74-79` |
| 128 | `DELETE` | `/api/v1/connectors/:id/token` | `Auth` | 撤销令牌授权 | `ConnectorController.java:81-84` |
| 129 | `POST` | `/api/v1/connectors/:id/bind-code` | `Auth` | 生成飞书/钉钉 8 位私聊绑定码 | `ConnectorController.java:86-89` |
| 130 | `GET` | `/api/v1/oauth/:provider/authorize` | `Auth` | **`302 Found`** 跳转飞书 OAuth 授权页（Query `connectorId`） | `ConnectorOAuthController.java:36-46` |
| 131 | `GET` | `/api/v1/oauth/:provider/callback` | `Auth` | **`302 Found`** 换 Token 并跳回 `/?connected=...#connectors` | `ConnectorOAuthController.java:48-64` |
| 132 | `GET` | `/api/v1/providers` | `Admin` | 已添模型 CLI 供应商列表 | `ProviderController.java:42-45` |
| 133 | `POST` | `/api/v1/providers` | `Admin` | 添加目录中的供应商（V1：`claude`/`codex`） | `ProviderController.java:76-79` |
| 134 | `GET` | `/api/v1/providers/catalog` | `Admin` | 静态供应商目录（V1 返回 Claude/Codex GA 项） | `ProviderController.java:71-74` |
| 135 | `GET` | `/api/v1/providers/node` | `Admin` | 系统/私有 Node.js 运行时状态 | `ProviderController.java:48-51` |
| 136 | `POST` | `/api/v1/providers/node/install` | `Admin` | 异步安装/升级私有 Node LTS | `ProviderController.java:54-57` |
| 137 | `GET` | `/api/v1/providers/node/install-log` | `Admin` | 私有 Node 安装环形日志（未跑过返 `data: null`） | `ProviderController.java:60-63` |
| 138 | `GET` | `/api/v1/providers/:id` | `Admin` | 供应商详情与模型探活表 | `ProviderController.java:81-84` |
| 139 | `DELETE` | `/api/v1/providers/:id` | `Admin` | 移除供应商 | `ProviderController.java:86-90` |
| 140 | `POST` | `/api/v1/providers/:id/install` | `Admin` | 异步安装 CLI | `ProviderController.java:92-95` |
| 141 | `POST` | `/api/v1/providers/:id/upgrade` | `Admin` | 异步升级 CLI | `ProviderController.java:97-100` |
| 142 | `GET` | `/api/v1/providers/:id/install-log` | `Admin` | CLI 安装/升级日志 | `ProviderController.java:66-69` |
| 143 | `POST` | `/api/v1/providers/:id/models/refresh` | `Admin` | 立即刷新模型清单并探活 | `ProviderController.java:102-105` |
| 144 | `POST` | `/api/v1/providers/:id/check-latest` | `Admin` | 立即检查 npm 最新版本 | `ProviderController.java:107-110` |
| 145 | `GET` | `/api/v1/providers/:id/credentials` | `Admin` | Key 池列表 | `ProviderController.java:114-117` |
| 146 | `POST` | `/api/v1/providers/:id/credentials` | `Admin` | 新增 API Key / 中转站 | `ProviderController.java:119-122` |
| 147 | `PUT` | `/api/v1/providers/:id/credentials/:cid` | `Admin` | 编辑凭证 | `ProviderController.java:124-128` |
| 148 | `DELETE` | `/api/v1/providers/:id/credentials/:cid` | `Admin` | 软删凭证 | `ProviderController.java:130-134` |
| 149 | `POST` | `/api/v1/providers/:id/credentials/:cid/check` | `Admin` | 立即推理探活单条凭证 | `ProviderController.java:137-140` |
| 150 | `PUT` | `/api/v1/providers/:id/credentials/order` | `Admin` | 调整 Key 池故障切换优先级 | `ProviderController.java:142-146` |
| 151 | `GET` | `/api/v1/admin/model-prices` | `Admin` | 模型目录定价总览 | `ModelPriceController.java:24-27` |
| 152 | `POST` | `/api/v1/admin/model-prices/refresh` | `Admin` | 立即拉取 `models.dev` 目录 | `ModelPriceController.java:30-34` |
| 153 | `PUT` | `/api/v1/admin/model-prices/price` | `Admin` | 覆盖单模型价格（`?model=` 防斜杠冲突） | `ModelPriceController.java:37-40` |
| 154 | `PUT` | `/api/v1/admin/model-prices/tasks` | `Admin` | 开关 `forTasks` 自动路由资格（`?model=`） | `ModelPriceController.java:42-45` |
| 155 | `GET` | `/api/v1/settings` | `Auth` | 读取公司默认设置（含默认模型） | `SettingsController.java:33-36` |
| 156 | `PUT` | `/api/v1/settings` | `Admin` | 修改公司设置 | `SettingsController.java:38-41` |
| 157 | `GET` | `/api/v1/settings/logo` | `Admin` | 是否配置自定义 Logo (`bool`) | `SettingsController.java:44-47` |
| 158 | `PUT` | `/api/v1/settings/logo` | `Admin` | 上传自定义 Logo（base64 光栅 ≤1 MB） | `SettingsController.java:49-53` |
| 159 | `DELETE` | `/api/v1/settings/logo` | `Admin` | 恢复默认 Logo | `SettingsController.java:55-59` |
| 160 | `GET` | `/api/v1/admin/integrations` | `Admin` | 企业集成列表（V1 仅 `feishu`/`dingtalk`，带 `AppURLResolver` 回调基址） | `IntegrationController.java:29-32` |
| 161 | `PUT` | `/api/v1/admin/integrations/:key` | `Admin` | 更新集成配置并发布 `IntegrationChanged` 热重载长连接 | `IntegrationController.java:34-38` |
| 162 | `GET` | `/api/v1/inbox/summary` | `Auth` | 外部消息绑定与今日汇总 | `InboxController.java:31-34` |
| 163 | `GET` | `/api/v1/inbox/events` | `Auth` | 外部消息流（Query `limit` 默认 200） | `InboxController.java:36-39` |
| 164 | `GET` | `/api/v1/inbox/todos` | `Auth` | 提取待办列表 | `InboxController.java:41-44` |
| 165 | `PUT` | `/api/v1/inbox/todos/:id` | `Auth` | 更新待办状态（`open\|tasked\|done\|dismissed`） | `InboxController.java:46-50` |
| 166 | `POST` | `/api/v1/inbox/todos/:id/task` | `Auth` | 一键转异步任务 | `InboxController.java:52-55` |
| 167 | `GET` | `/api/v1/notices` | `Auth` | 待办首页卡片列表 | `NoticeController.java:28-31` |
| 168 | `GET` | `/api/v1/notices/stats` | `Auth` | 待办首页 KPI 统计 | `NoticeController.java:33-36` |
| 169 | `POST` | `/api/v1/notices/:id/done` | `Auth` | 标记完成/已读 | `NoticeController.java:39-42` |
| 170 | `POST` | `/api/v1/notices/:id/ignore` | `Auth` | 忽略卡片（发布 `NoticeIgnored`） | `NoticeController.java:44-47` |
| 171 | `POST` | `/api/v1/notices/:id/reopen` | `Auth` | 重开卡片 | `NoticeController.java:49-52` |
| 172 | `POST` | `/api/v1/notices/read-all` | `Auth` | 一键已读全部 `DONE` 与 `NOTICE` 项，返变更数 `int` | `NoticeController.java:55-58` |
| 173 | `GET` | `/api/v1/usage` | `Auth` | 个人用量（`period=month\|1..366`, `tz` 可选） | `UsageController.java:38-42` |
| 174 | `GET` | `/api/v1/admin/usage` | `Admin` | 全公司用量（含 `byUser` 与 `system`） | `UsageController.java:44-48` |
| 175 | `GET` | `/api/v1/admin/analytics` | `Admin` | 运营大盘统计（Query `days` 默认 14） | `AnalyticsController.java:21-24` |

**I. 内置 MCP 协议网关（2 端点）**

| # | Method | Gin Path | Auth | 说明与信封 | Java 参考 |
|---:|---|---|---|---|---|
| 176 | `GET` | `/mcp` | `Public` | 恒返空体 `405 Method Not Allowed`（无服务端主动 SSE） | `McpController.java:49-52` |
| 177 | `POST` | `/mcp` | `Token` | Bearer 48-hex 令牌鉴权（无效 `401` 空体）；无 `id` 通知 `202` 空体；JSON-RPC 2.0 | `McpController.java:54-143` |

*(注：第 1–175 项合计精确 **175 个** REST/页面/认证端点，加上第 176–177 项 **2 个** `/mcp` 端点，共计 **177 个** L5 端点；L6 `spec_14` 额外挂载 `/actuator/health`、`/actuator/prometheus`、`/actuator/info`。)*

### 4.3 错误语义

#### 4.3.1 REST 全局错误映射表（对齐 `GlobalExceptionHandler.java:33-106`）

| Go 错误类型 / 触发条件 | HTTP 状态码 | `code` | `message` 语义 |
|---|---:|---|---|
| JSON 绑定/参数校验错误、`*apperror.InvalidArgError`、Query 类型转换失败 | `400` | `invalid_argument` | 精确字段错误或业务校验说明（如 `Parameter '<name>' has the wrong type`） |
| 未登录访问 `/api/*` 或登录凭证失败 | `401` | `unauthorized` | 未登录：`请先登录`；登录失败：`组织、用户名或密码错误，或账号未授权` |
| `PasswordChangeMiddleware` 拦截 | `403` | `password_change_required` | `请先修改初始密码`（直接写裸 `{"code":"password_change_required","message":"请先修改初始密码"}` 与原版字节对齐，前端兼容） |
| `*apperror.ForbiddenError`、非管理员访问 `Admin` 路由 | `403` | `forbidden` | 业务文案（如配额超限、无租户密钥、无权限编辑、SSRF 拦截） |
| `*apperror.NotFoundError`、`NoRoute`（`/api/*`） | `404` | `not_found` | 业务文案或 `No such endpoint`（资源无权访问统一 404 防探测） |
| `NoMethod`（非 `/mcp`） | `405` | `method_not_allowed` | `<METHOD> is not supported here` |
| `*apperror.ConflictError`（含 `UnreadableMaterials`、`TaskBusy`、`UsernameTaken`） | `409` | `conflict` | 业务冲突说明 |
| `*apperror.BusyError`（`ChatBusy`） | `409` | `busy` | 并发占用说明（触发前端 `chat-stream.js:33-41` 退回草稿） |
| 上传文件/请求体超过 `MaxUploadBytes`（`http.MaxBytesError`） | `413` | `too_large` | 固定文案：`单个文件不能超过 50 MB` |
| SSE 写连接中断（`context.Canceled`/`syscall.EPIPE`/`syscall.ECONNRESET`） | *(不回写)* | — | 仅 `slog.Debug`，避免向已提交的 `text/event-stream` 写 JSON |
| 其余未预期错误 / `RecoveryMiddleware` 捕获 panic | `500` | `internal_error` | 记录 `slog.Error` 堆栈，返回脱敏或简短错误消息 |

#### 4.3.2 MCP JSON-RPC 2.0 错误映射表（含 V1 统一 `isError: true` 修复）

| 场景 | HTTP 状态码 | JSON-RPC 形态 | 说明 |
|---|---:|---|---|
| 缺 `Authorization: Bearer ` 或 Token 不存在/已吊销/StandIn 未激活 | `401` | 空体 | 立即拒止 |
| 请求 JSON 语法损坏 | `200` | `error: {code: -32700, message: "Parse error"}` | JSON-RPC 2.0 标准 |
| 无 `id` 或 `"id": null`（如 `notifications/initialized`） | `202` | 空体 | 不执行方法分发 |
| 未知 `method` | `200` | `error: {code: -32601, message: "Method not found: <m>"}` | JSON-RPC 2.0 标准 |
| **未知工具、`*mcp.ToolError`、或任意预期领域错误（`apperror.InvalidArg`/`NotFound`/`Forbidden`/`Conflict`/`Busy`）** | `200` | **`result: {content: [{type: "text", text: "错误：<msg>"}], isError: true}`** | **V1 修复原版缺陷**：`TaskMcpTools` 等不再因漏包 `McpToolException` 导致返 `-32603`，模型可读取错误信息自纠 |
| 真正未预期基础设施故障（DB 断连、Panic 恢复） | `200` | `error: {code: -32603, message: "Internal error: <msg>"}` | 记 `slog.Warn` / `Error` |

### 4.4 静态资源与前端演进契约

1. **公开静态资源白名单（未登录放行）**：`/login`、`/login.html`、`/login.css`、`/login.js`、`/app.css`、`/i18n.js`、`/superagent-logo.png`、`/superagent-mark.png`、`/favicon.ico`、`/js/*`、`/vendor/*`（对齐 `SecurityConfig.java:60-61` 并补齐 `superagent-mark.png` 与 `vendor/*`）。
2. **受保护页面入口**：`/`、`/index.html` 需登录，未登录一律 `302 /login`；已登录访问 `/login` 一律 `302 /`。
3. **缓存策略**：HTML（`index.html`、`login.html`）固定 `Cache-Control: no-cache` 确保发版立即生效；`--ui-dir` 开发模式下所有静态文件均带 `Cache-Control: no-store`；生产内嵌模式下 `/vendor/*` 带 `Cache-Control: public, max-age=604800`，其余 JS/CSS 带 `Cache-Control: no-cache`。
4. **向 `synapse-web` 平滑交接**：未来 `synapse-web`（React/Vue + TS）只需将 Vite/Webpack dev proxy 指向 `:18080` 的 `/api`、`/mcp`、`/actuator`；生产构建产物 `dist/` 覆写进 `internal/ui/static`（并在 `NoRoute` 对无扩展名 SPA 路由按需 fallback 至 `index.html`）或由企业 Nginx 独立托管，Go 后端契约零变更。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 6 级中间件链与认证/改密拦截

对应 Java：`TraceIdFilter.java:27-48`、`OrgLoginFilter.java:16-38`、`SecurityConfig.java:50-131`、`TenantFilter.java:19-38`、`PasswordChangeFilter.java:21-58`。

```go
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/core/obs"
	"github.com/Karlsk/synapse-os/internal/core/spi"
	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

const (
	HeaderRequestID      = "X-Request-Id"
	HeaderAutomationTok  = "X-Automation-Token"
	principalCtxKeyStr   = "synapse.principal"
	sessionIDCtxKeyStr   = "synapse.session_id"
	sessionPwdOKCtxKey   = "synapse.session_pwd_ok"
)

var safeTraceIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// Stage 1: RecoveryMiddleware 捕获 panic，记完整堆栈，返回 500 internal_error（对齐 GlobalExceptionHandler:100-106）。
func RecoveryMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				obs.Logger(c.Request.Context()).Error("http handler panic recovered",
					"panic", rec, "path", c.Request.URL.Path, "stack", string(debug.Stack()))
				if !c.Writer.Written() {
					WriteError(c, http.StatusInternalServerError, "internal_error", "internal server error")
				}
				c.Abort()
			}
		}()
		// 同步设置安全响应头：允许同源 iframe 预览 PDF，禁止跨站点击劫持（SecurityConfig.java:58）
		c.Header("X-Frame-Options", "SAMEORIGIN")
		c.Next()
	}
}

// Stage 2: TraceIDMiddleware 校验或生成 32 位 hex TraceID（对齐 TraceIdFilter.java:34-47）。
func TraceIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		traceID := c.GetHeader(HeaderRequestID)
		if !safeTraceIDRe.MatchString(traceID) {
			var b [16]byte
			_, _ = rand.Read(b[:])
			traceID = hex.EncodeToString(b[:])
		}
		ctx := obs.WithTraceID(c.Request.Context(), traceID)
		c.Request = c.Request.WithContext(ctx)
		c.Header(HeaderRequestID, traceID)
		c.Next()
	}
}

// Stage 3: AuthMiddleware 解析持久化 Session Cookie；公开端点可选加载主体（供 /login 跳转和 /actuator/health 详情），
// 其余未登录时 /api/* 或 /mcp 返 401，页面路径 302 /login（对齐 SecurityConfig.java:59-118）。
func AuthMiddleware(sessions SessionManager, cookieName string) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		method := c.Request.Method

		if sid, err := c.Cookie(cookieName); err == nil && sid != "" {
			if sess, principal, ok := sessions.Resolve(c.Request.Context(), sid); ok {
				ctx := WithPrincipal(c.Request.Context(), principal)
				ctx = obs.WithUserID(ctx, principal.UserID)
				c.Request = c.Request.WithContext(ctx)
				c.Set(principalCtxKeyStr, principal)
				c.Set(sessionIDCtxKeyStr, sess.ID)
				c.Set(sessionPwdOKCtxKey, sess.PasswordOK)
			}
		}

		if isPublicRoute(method, path) {
			c.Next()
			return
		}

		if _, ok := PrincipalFromContext(c.Request.Context()); !ok {
			if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/mcp") {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"code":    "unauthorized",
					"message": "请先登录",
				})
				return
			}
			c.Redirect(http.StatusFound, "/login")
			c.Abort()
			return
		}
		c.Next()
	}
}

func isPublicRoute(method, path string) bool {
	switch path {
	case "/login", "/login.html", "/login.css", "/login.js", "/app.css",
		"/i18n.js", "/superagent-logo.png", "/superagent-mark.png", "/favicon.ico",
		"/api/v1/meta", "/actuator/health", "/api/v1/branding/logo",
		"/api/v1/auth/login", "/mcp":
		return true
	}
	if method == http.MethodGet && path == "/api/v1/orgs/registration" {
		return true
	}
	if strings.HasPrefix(path, "/js/") || strings.HasPrefix(path, "/vendor/") {
		return true
	}
	// POST /api/v1/automations/:id/trigger 由 AutomationService.Trigger 校验 X-Automation-Token
	if method == http.MethodPost && strings.HasPrefix(path, "/api/v1/automations/") && strings.HasSuffix(path, "/trigger") {
		rest := strings.TrimSuffix(strings.TrimPrefix(path, "/api/v1/automations/"), "/trigger")
		return rest != "" && !strings.Contains(rest, "/")
	}
	return false
}

// Stage 4: TenantMiddleware 从认证主体提取 CompanyID 注入 Fail-Closed TenantContext（对齐 TenantFilter.java:22-37）。
func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalFromContext(c.Request.Context())
		if !ok || p.CompanyID == "" {
			c.Next()
			return
		}
		ctx, err := tenant.WithCompany(c.Request.Context(), p.CompanyID)
		if err != nil {
			WriteError(c, http.StatusUnauthorized, "unauthorized", "invalid tenant context")
			c.Abort()
			return
		}
		ctx = obs.WithCompanyID(ctx, p.CompanyID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}

// Stage 5: PasswordChangeMiddleware 拦截首次登录未改密用户（对齐 PasswordChangeFilter.java:32-57）。
func PasswordChangeMiddleware(accounts AccountService, sessions SessionManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") ||
			strings.HasPrefix(path, "/api/v1/auth/") ||
			path == "/api/v1/account/password" ||
			path == "/api/v1/meta" ||
			strings.HasPrefix(path, "/api/v1/branding") ||
			strings.HasPrefix(path, "/api/v1/orgs") {
			c.Next()
			return
		}
		p, ok := PrincipalFromContext(c.Request.Context())
		if !ok {
			c.Next()
			return
		}
		if pwdOK, _ := c.GetBool(sessionPwdOKCtxKey); pwdOK {
			c.Next()
			return
		}
		mustChange, err := accounts.MustChangePassword(c.Request.Context(), p.UserID)
		if err != nil {
			HandleError(c, err)
			c.Abort()
			return
		}
		if mustChange {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":    "password_change_required",
				"message": "请先修改初始密码",
			})
			return
		}
		if sid := c.GetString(sessionIDCtxKeyStr); sid != "" {
			_ = sessions.MarkPasswordOK(c.Request.Context(), sid)
		}
		c.Next()
	}
}

// Stage 6: RequireAdminMiddleware 管理路由组 RBAC 守卫（对齐 SecurityConfig.java:71-82）。
func RequireAdminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := PrincipalFromContext(c.Request.Context())
		if !ok || !p.HasRole("ADMIN") {
			WriteError(c, http.StatusForbidden, "forbidden", "administrator role required")
			c.Abort()
			return
		}
		c.Next()
	}
}
```

### 5.2 登录/登出、`/auth/me` 与全局错误映射

对应 Java：`OrgLoginFilter.java:26-37`、`AppUserDetailsService.java:35-62`、`SecurityConfig.java:84-106`、`AuthController.java:40-55`、`GlobalExceptionHandler.java:33-106`。

```go
// POST /api/v1/auth/login：兼容前端 login.js 的 x-www-form-urlencoded 表单（org/username/password），
// 返回裸 JSON {userId, username, roles}（不包 ApiResponse 外壳！）。
func (h *MetaAuthHandler) Login(c *gin.Context) {
	org := strings.ToLower(strings.TrimSpace(c.PostForm("org")))
	username := strings.TrimSpace(c.PostForm("username"))
	password := c.PostForm("password")
	if org == "" || username == "" || password == "" {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    "unauthorized",
			"message": "组织、用户名或密码错误，或账号未授权",
		})
		return
	}

	principal, err := h.auth.Authenticate(c.Request.Context(), spi.Credentials{
		Kind:     "local",
		Org:      org,
		Username: username,
		Password: password,
	})
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"code":    "unauthorized",
			"message": "组织、用户名或密码错误，或账号未授权",
		})
		return
	}

	// 持久化会话落库 user_session，确保服务重启不踢人；角色恒包含 "USER"（AppUserDetailsService.java:55-62）
	sess, err := h.sessions.Create(c.Request.Context(), principal, h.opts.SessionTTL)
	if err != nil {
		HandleError(c, err)
		return
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.opts.SessionCookieName, sess.ID, int(h.opts.SessionTTL.Seconds()),
		"/", "", h.opts.SessionCookieSecure, true)

	// 严格裸 JSON，对齐 SecurityConfig.java:96-99
	c.JSON(http.StatusOK, gin.H{
		"userId":   principal.UserID,
		"username": principal.Username,
		"roles":    principal.Roles,
	})
}

// POST /api/v1/auth/logout：销毁服务端会话并清 Cookie，返回 HTTP 200 空响应体（SecurityConfig.java:88-90）。
func (h *MetaAuthHandler) Logout(c *gin.Context) {
	if sid := c.GetString(sessionIDCtxKeyStr); sid != "" {
		_ = h.sessions.Delete(c.Request.Context(), sid)
	}
	c.SetSameSite(http.SameSiteLaxMode)
	c.SetCookie(h.opts.SessionCookieName, "", -1, "/", "", h.opts.SessionCookieSecure, true)
	c.Status(http.StatusOK)
}

// HandleError 统一翻译领域错误与框架错误至标准 ApiResponse 信封（对齐 GlobalExceptionHandler.java:33-106）。
func HandleError(c *gin.Context, err error) {
	if err == nil {
		return
	}
	// 客户端断连（如 SSE 切换会话 AbortController）：静默调试日志，不回写
	if errors.Is(err, context.Canceled) || isBrokenPipe(err) {
		obs.Logger(c.Request.Context()).Debug("client disconnected mid-response", "err", err)
		return
	}
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		WriteError(c, http.StatusRequestEntityTooLarge, "too_large", "单个文件不能超过 50 MB")
		return
	}
	var inv *apperror.InvalidArgError
	if errors.As(err, &inv) {
		WriteError(c, http.StatusBadRequest, "invalid_argument", inv.Message)
		return
	}
	var nf *apperror.NotFoundError
	if errors.As(err, &nf) {
		WriteError(c, http.StatusNotFound, "not_found", nf.Message)
		return
	}
	var fb *apperror.ForbiddenError
	if errors.As(err, &fb) {
		WriteError(c, http.StatusForbidden, "forbidden", fb.Message)
		return
	}
	var cf *apperror.ConflictError
	if errors.As(err, &cf) {
		WriteError(c, http.StatusConflict, "conflict", cf.Message)
		return
	}
	var bs *apperror.BusyError
	if errors.As(err, &bs) {
		WriteError(c, http.StatusConflict, "busy", bs.Message)
		return
	}
	obs.Logger(c.Request.Context()).Error("unhandled error", "err", err)
	WriteError(c, http.StatusInternalServerError, "internal_error", err.Error())
}
```

### 5.3 SSE 实时对话与 `LiveTurn` 多人同屏旁观流

对应 Java：`ChatController.java:75-102`、`ChatService.java:595,765-780,817,945-949,1064-1075`、`chat-stream.js:67-139`。

**核心工程语义（必须严格保真）**：
1. **先同步鉴权与抢锁，再切 SSE 响应头**：`POST /api/v1/sessions/:id/messages` 必须先完成 `RequireAccess`、`LiveTurn` 并发抢占、消息入库与文件附件校验；若返回 `409 busy`、`404`、`403`（如归档项目或超月度配额），必须以常规 JSON `ApiResponse` 返回，确保前端 `chat-stream.js:33-45` 能正常恢复输入框草稿！
2. **断连不中断后台回合**：回合执行协程运行在 `tenant.Detach(c.Request.Context(), h.detachBaseCtx)` 下（保留 `companyID`/`userID`/`traceID`，取消信号绑定进程停机而非单个浏览器连接）。用户切换 Agent 触发前端 `AbortController.abort()` 时，仅结束当前 HTTP 响应协程并取消 `LiveTurn` 订阅，后台 CLI 继续跑完并持久化。
3. **写超时解除与反代防缓冲**：调用 `http.NewResponseController(c.Writer).SetWriteDeadline(time.Time{})` 解除 `http.Server.WriteTimeout` 对长 SSE 流的误杀，并写入 `X-Accel-Buffering: no` 阻断 Nginx 缓冲；每帧写出后强制 `flusher.Flush()`。
4. **`watchLive` 空闲事件**：`GET /api/v1/sessions/:id/live/stream` 若当前无运行回合，立即发送单帧 `event: idle\ndata: {"type":"idle","seq":-1,"payload":null}\n\n` 后正常结束（1:1 对齐 `ChatService.java:1069`）。

```go
// POST /api/v1/sessions/:id/messages
func (h *ChatHandler) SendMessage(c *gin.Context) {
	sessionID := c.Param("id")
	userID := mustPrincipal(c).UserID

	var req api.SendMessageRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		WriteError(c, http.StatusBadRequest, "invalid_argument", err.Error())
		return
	}
	if err := h.chat.RequireAccess(c.Request.Context(), sessionID, userID); err != nil {
		HandleError(c, err)
		return
	}

	// StartTurn 在同步阶段完成抢锁（冲突返 apperror.Busy -> 409 JSON）、附件交付与后台 goroutine 启动
	sub, err := h.chat.StartTurn(c.Request.Context(), sessionID, userID, req)
	if err != nil {
		HandleError(c, err)
		return
	}
	defer sub.Unsubscribe()

	streamSSE(c, sub.Backlog, sub.Events, sub.AlreadyEnded, h.sseTimeout)
}

// GET /api/v1/sessions/:id/live/stream
func (h *ChatHandler) WatchLive(c *gin.Context) {
	sessionID := c.Param("id")
	userID := mustPrincipal(c).UserID
	if err := h.chat.RequireAccess(c.Request.Context(), sessionID, userID); err != nil {
		HandleError(c, err)
		return
	}

	sub, active := h.chat.WatchLive(c.Request.Context(), sessionID)
	if !active {
		// 对齐 ChatService.java:1069：无在途回合推单帧 idle 并结束
		initSSEHeaders(c)
		_ = writeSSEFrame(c.Writer, api.StreamEvent{Type: "idle", Seq: -1, Payload: nil})
		return
	}
	defer sub.Unsubscribe()

	streamSSE(c, sub.Backlog, sub.Events, sub.AlreadyEnded, h.sseTimeout)
}

func initSSEHeaders(c *gin.Context) {
	// 解除 http.Server.WriteTimeout 对 SSE 长连接的影响
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Time{})

	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // 防止企业 Nginx 反代缓冲 SSE
	c.Status(http.StatusOK)
	c.Writer.Flush()
}

func writeSSEFrame(w gin.ResponseWriter, ev api.StreamEvent) error {
	raw, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// 严格对齐 SseEmitter.event().name(event.type()).data(event) 与 chat-stream.js:76,92
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, raw); err != nil {
		return err
	}
	w.Flush()
	return nil
}

func streamSSE(c *gin.Context, backlog []api.StreamEvent, ch <-chan api.StreamEvent, ended bool, timeout time.Duration) {
	initSSEHeaders(c)
	for _, ev := range backlog {
		if err := writeSSEFrame(c.Writer, ev); err != nil {
			return // 客户端断开
		}
	}
	if ended {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	reqCtx := c.Request.Context()

	for {
		select {
		case <-reqCtx.Done():
			return // 仅退出当前 SSE 连接；后台回合不受影响
		case <-timer.C:
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if err := writeSSEFrame(c.Writer, ev); err != nil {
				return
			}
		}
	}
}
```

### 5.4 文件内容服务（HTTP `Range` + XSS 防护 + `CSP: sandbox`）

对应 Java：`FileController.java:41-49,108-127`、`SettingsController.java:62-71`、`ImageAttachmentStore.java:44-54`。

```go
// 严格对齐 FileController.java:41-49：文本类一律强制 text/plain 防同源 HTML 执行 XSS
var inlineFileTypes = map[string]string{
	"md": "text/plain; charset=utf-8", "markdown": "text/plain; charset=utf-8",
	"txt": "text/plain; charset=utf-8", "csv": "text/plain; charset=utf-8",
	"json": "text/plain; charset=utf-8", "html": "text/plain; charset=utf-8",
	"htm": "text/plain; charset=utf-8",
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
	"gif": "image/gif", "webp": "image/webp", "svg": "image/svg+xml",
	"mp4": "video/mp4", "webm": "video/webm", "mov": "video/quicktime",
	"m4v": "video/mp4", "pdf": "application/pdf",
}

// GET /api/v1/files/:id/content?download=false
func (h *AssetsHandler) FileContent(c *gin.Context) {
	id := c.Param("id")
	download := c.DefaultQuery("download", "false") == "true"
	userID := mustPrincipal(c).UserID

	content, err := h.files.OpenContent(c.Request.Context(), id, userID)
	if err != nil {
		HandleError(c, err)
		return
	}
	defer content.Close()

	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(content.Name), "."))
	inlineType, canInline := inlineFileTypes[ext]
	inline := canInline && !download

	dispKind := "attachment"
	contentType := "application/octet-stream"
	if inline {
		dispKind = "inline"
		contentType = inlineType
	}

	hdr := c.Writer.Header()
	hdr.Set("Content-Type", contentType)
	// RFC 5987 UTF-8 文件名编码，兼容中文文件名下载（对齐 ContentDisposition.filename(name, UTF_8)）
	hdr.Set("Content-Disposition", formatContentDisposition(dispKind, content.Name))
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "no-store")
	if ext != "pdf" {
		// 仅 PDF 豁免 sandbox CSP 以允许浏览器内置 PDF 阅读器渲染（FileController.java:123-125）
		hdr.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'")
	}

	// 支持视频拖拽 HTTP Range（206 Partial Content）：Local 文件与 S3 RangedReader 均实现 io.ReadSeeker
	if content.ReadSeeker != nil {
		http.ServeContent(c.Writer, c.Request, content.Name, content.ModTime, content.ReadSeeker)
		return
	}
	if content.Size >= 0 {
		hdr.Set("Content-Length", strconv.FormatInt(content.Size, 10))
	}
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, content.Reader)
}

// GET /api/v1/branding/logo（对齐 SettingsController.java:62-71）
func (h *PlatformHandler) BrandingLogo(c *gin.Context) {
	img, err := h.branding.Logo(c.Request.Context())
	if err != nil {
		HandleError(c, err)
		return
	}
	sum := sha256.Sum256(img.Bytes)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "no-cache")
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, img.MediaType, img.Bytes)
}
```

### 5.5 OAuth 302 握手与 `AppURLResolver`

对应 Java：`ConnectorOAuthController.java:36-74`、`AppUrl.java:15-36`。

```go
// AppURLResolver 按「系统设置 publicUrl > 静态配置 app.base_url > 当前请求协议与主机」三级解析回调基址。
func (r *AppURLResolver) Base(ctx context.Context, req *http.Request) string {
	if set := strings.TrimSpace(r.settings.PublicURL(ctx)); set != "" {
		return strings.TrimRight(set, "/")
	}
	if r.configured != "" {
		return strings.TrimRight(r.configured, "/")
	}
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	host := req.Host
	if r.trustProxy {
		if fp := firstHeaderVal(req.Header.Get("X-Forwarded-Proto")); fp == "http" || fp == "https" {
			scheme = fp
		}
		if fh := firstHeaderVal(req.Header.Get("X-Forwarded-Host")); fh != "" {
			host = fh
		}
	}
	return scheme + "://" + host
}

// GET /api/v1/oauth/:provider/authorize & callback：无论成功、拒绝或配置缺失均 302 重定向，永不返 JSON 错误页。
func (h *PlatformHandler) OAuthAuthorize(c *gin.Context) {
	provider := c.Param("provider")
	connectorID := c.Query("connectorId")
	userID := mustPrincipal(c).UserID
	base := h.appURL.Base(c.Request.Context(), c.Request)

	target, err := h.oauth.BuildAuthorizeURL(c.Request.Context(), provider, connectorID, userID, base)
	if err != nil {
		c.Redirect(http.StatusFound, oauthReturnURL("connect_error", mapOAuthErrCode(err)))
		return
	}
	c.Redirect(http.StatusFound, target)
}

func (h *PlatformHandler) OAuthCallback(c *gin.Context) {
	provider := c.Param("provider")
	if errParam := strings.TrimSpace(c.Query("error")); errParam != "" {
		c.Redirect(http.StatusFound, oauthReturnURL("connect_error", errParam))
		return
	}
	connectorID, err := h.oauth.HandleCallback(
		c.Request.Context(), provider, c.Query("code"), c.Query("state"), mustPrincipal(c).UserID,
	)
	if err != nil {
		c.Redirect(http.StatusFound, oauthReturnURL("connect_error", mapOAuthErrCode(err)))
		return
	}
	c.Redirect(http.StatusFound, oauthReturnURL("connected", connectorID))
}

func oauthReturnURL(key, val string) string {
	return "/?" + key + "=" + url.QueryEscape(val) + "#connectors"
}
```

### 5.6 内置 `/mcp` JSON-RPC 2.0 端点（含统一 `isError: true` 修复）

对应 Java：`McpController.java:35-144`。

```go
package mcpweb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/core/obs"
	"github.com/Karlsk/synapse-os/internal/core/tenant"
	"github.com/Karlsk/synapse-os/internal/runtime/mcp"
)

const ProtocolVersion = "2024-11-05"

type Handler struct {
	grants    *mcp.Grants
	providers []mcp.ToolProvider
}

func NewHandler(grants *mcp.Grants, providers []mcp.ToolProvider) *Handler {
	return &Handler{grants: grants, providers: providers}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"` // 缺失或 literal "null" => Notification (202)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// GET /mcp -> 405 Method Not Allowed（无服务端主动 SSE，对齐 McpController.java:49-52）
func (h *Handler) NoStream(c *gin.Context) {
	c.Status(http.StatusMethodNotAllowed)
}

// POST /mcp -> Streamable-HTTP JSON-RPC 2.0（对齐 McpController.java:54-143 + V1 统一 isError:true 修复）
func (h *Handler) RPC(c *gin.Context) {
	auth := c.GetHeader("Authorization")
	var token string
	if strings.HasPrefix(auth, "Bearer ") {
		token = strings.TrimSpace(auth[7:])
	}
	grant, ok := h.grants.Find(token)
	if !ok {
		c.Status(http.StatusUnauthorized)
		return
	}

	var req rpcRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		writeRPCError(c, json.RawMessage("null"), -32700, "Parse error: "+err.Error())
		return
	}

	// 无 id 或 id == null：按 MCP 规范返回 202 Accepted 空体
	if len(req.ID) == 0 || string(bytes.TrimSpace(req.ID)) == "null" {
		c.Status(http.StatusAccepted)
		return
	}

	companyID, err := h.grants.CompanyOf(grant)
	if err != nil {
		c.Status(http.StatusUnauthorized)
		return
	}
	ctx, err := tenant.WithCompany(c.Request.Context(), companyID)
	if err != nil {
		writeRPCError(c, req.ID, -32603, "Internal error: invalid grant company")
		return
	}
	ctx = obs.WithCompanyID(ctx, companyID)
	ctx = obs.WithUserID(ctx, grant.Owner)

	switch req.Method {
	case "initialize":
		writeRPCOK(c, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "superagent", "version": "0.1.0"},
		})
	case "ping":
		writeRPCOK(c, req.ID, map[string]any{})
	case "tools/list":
		tools := make([]mcp.ToolDefinition, 0, 32)
		for _, p := range h.providersFor(grant.Kind) {
			tools = append(tools, p.Tools(ctx)...)
		}
		writeRPCOK(c, req.ID, map[string]any{"tools": tools})
	case "tools/call":
		res, rpcErr := h.callTool(ctx, grant, req.Params)
		if rpcErr != nil {
			writeRPCError(c, req.ID, rpcErr.Code, rpcErr.Message)
			return
		}
		writeRPCOK(c, req.ID, res)
	default:
		writeRPCError(c, req.ID, -32601, "Method not found: "+req.Method)
	}
}

func (h *Handler) callTool(ctx context.Context, grant mcp.Grant, rawParams json.RawMessage) (map[string]any, *rpcErrObj) {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return textContent("错误：工具参数格式错误："+err.Error(), true), nil
	}

	var target mcp.ToolProvider
	for _, p := range h.providersFor(grant.Kind) {
		if p.Handles(params.Name) {
			target = p
			break
		}
	}
	if target == nil {
		return textContent("错误：Unknown tool: "+params.Name, true), nil
	}

	out, err := target.Call(ctx, grant, params.Name, params.Arguments)
	if err != nil {
		// [V1 核心缺陷修复]：McpToolError 及全部可预期领域错误统一折返 isError: true，让模型可读并自纠
		if isDomainOrToolError(err) {
			return textContent("错误："+err.Error(), true), nil
		}
		obs.Logger(ctx).Warn("mcp tool failed unexpectedly", "tool", params.Name, "err", err)
		return nil, &rpcErrObj{Code: -32603, Message: "Internal error: " + err.Error()}
	}
	return textContent(out.Text, out.IsError), nil
}

func isDomainOrToolError(err error) bool {
	var (
		toolErr *mcp.ToolError
		inv     *apperror.InvalidArgError
		nf      *apperror.NotFoundError
		fb      *apperror.ForbiddenError
		cf      *apperror.ConflictError
		bs      *apperror.BusyError
	)
	return errors.As(err, &toolErr) || errors.As(err, &inv) || errors.As(err, &nf) ||
		errors.As(err, &fb) || errors.As(err, &cf) || errors.As(err, &bs)
}

func textContent(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}
```

### 5.7 内嵌静态 UI 与 4 处精准 V1 前端微调

对应 Java 静态资产：`superagent-ui/src/main/resources/static/`（44 个文件）。

#### 5.7.1 Go 双模静态资产加载 (`internal/ui/embed.go`)

```go
package ui

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
)

//go:embed all:static
var embeddedStatic embed.FS

// NewFS 生产模式从二进制内嵌 static/ 提供文件；传入 --ui-dir 时直读本地目录实现零重编热加载。
func NewFS(uiDir string) (fs.FS, error) {
	if uiDir != "" {
		info, err := os.Stat(uiDir)
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("invalid --ui-dir %q: must be an existing directory", uiDir)
		}
		return os.DirFS(uiDir), nil
	}
	return fs.Sub(embeddedStatic, "static")
}
```

#### 5.7.2 4 处精准 V1 静态 UI 微调规范（逐文件逐行核定）

| # | 目标文件 | 原版代码精确位置 | V1 商业私有化精准微调内容 | 后端配合契约 |
|---:|---|---|---|---|
| 1 | `login.html`、`login.js` | `login.html:21-26,49-78`、`login.js:111-118` | 1. 保留 `GET /api/v1/orgs/registration` 探测，后端恒返 `{open: false}` 使 `regFoot.hidden = true` 自动生效；<br>2. 在 `login.html` 中隐藏 V1 未接通的 `Phone`/`SSO`/`WeChat` 三个占位登录 Tab（添加 `hidden` 属性，保留 DOM 供 V2 接 OIDC/SSO 时按能力开关重新放开），默认直显组织+用户名+密码表单与首次改密流。 | `GET /api/v1/orgs/registration` 恒返 `{"open":false}`；移除 `POST /api/v1/orgs` |
| 2 | `index.html`、`js/agent-form.js` | `index.html:1314-1317`、`agent-form.js:66-104,116-117,145-146` | 1. 当 `GET /api/v1/agents/characters` 返回 `[]` 时，`renderCharacterPicker()` 自动隐藏 `<label>`（包含 `#afCharacter` 与 `#afCharacterHint`），默认进入自定义模式（`chosenCharacter = null`）；<br>2. `openAgentModal()` 不再默认传入 `"rigorous-engineer"`，而是直接传 `null`；`persona` 文本框必填或按 `persona` 提示自由填写，头像选择器保持随机/自由挑选。 | `GET /api/v1/agents/characters` 返 `[]`；`AgentService` 仅拼接自定义 `persona` |
| 3 | `js/connectors.js`、`js/integrations.js` | `connectors.js:62,71-77,466-468,521-531`、`integrations.js:57,161` | 1. 后端仅播种 `feishu-docs` 与 `dingtalk-tools` 并返回 `feishu`/`dingtalk` 两项集成，页面零硬删即可自然只显示飞书/钉钉生态 + 管理员「添加自定义 MCP」；<br>2. 微调 `integrations.js:57` 将 `dingtalk` 的 `kind` 也映射为 `"dingtalk"`（修复原版把钉钉当成 `mcp` 图标的小瑕疵）及 `integrations.js:161` 对 `dingtalk` 保存后也触发 5s 状态回刷。 | `IntegrationService` 仅返回 `feishu`/`dingtalk`；`ConnectorService` 仅播种飞书文档与钉钉工具 |
| 4 | `js/models.js` | `models.js:78-85,229-242` | 1. `GET /api/v1/providers/catalog` 在 V1 返回 `claude` 与 `codex`（均为 `supported: true, installable: true, preview: false, maturity: "ga"`）；<br>2. 在 `loadCatalog()` 与 `modelRow()` 中识别可选字段 `maturity === "ga"` 展示轻量 `GA` 标识徽章，保持与后续增量注册 `AgentRunner` 插件自动呈现卡片的完全兼容。 | `ProviderCatalog` V1 启用 `claude`/`codex` 两项 GA 目录 |

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1（基础信封与错误映射）**：编写 `internal/web/response_test.go`（覆盖全部 11 种错误映射、SSE 断连静默、`413 too_large` 固定中文文案），实现 `internal/web/response.go`。
- [ ] **T2（6 级中间件链）**：编写 `internal/web/middleware_test.go`（panic 恢复、TraceID 合法/非法/注入拦截、Auth API-401 vs Page-302、Webhook/MCP 放行、TenantContext 注入与 Fail-Closed、PasswordChange 拦截与豁免白名单及会话缓存、Admin RBAC 403），实现 `internal/web/middleware.go`。
- [ ] **T3（登录/登出/Me/兼容桩/账号用户）**：编写 `handler_meta_auth_test.go` 与 `handler_account_user_test.go`（验证登录裸 JSON、登出空体 200、`POST /api/v1/orgs` 不存在、`GET /api/v1/orgs/registration` 恒 `open:false`），实现 `handler_meta_auth.go` 与 `handler_account_user.go`。
- [ ] **T4（SSE 对话与 `LiveTurn` 同屏旁观）**：编写 `handler_chat_test.go`（使用 `httptest.NewServer` 真实 HTTP 流验证 `text/event-stream`、`X-Accel-Buffering: no`、409 `busy` 返 JSON、断连不中断后台回合、`watchLive` 空闲推 `idle`），实现 `handler_chat.go`。
- [ ] **T5（文件安全预览与 `Range` + 项目/技能）**：编写 `handler_assets_test.go`（验证 `.html`/`.md` 强制 `text/plain` + `CSP: sandbox`、`.pdf` 豁免 CSP、`Range: bytes=0-9` 返 `206`、50MB 超限 413、`:id` 统一无 Gin 冲突），实现 `handler_assets.go`。
- [ ] **T6（任务/自动化/Agent/SuperAgent/平台域 Handler）**：编写 `handler_agent_test.go`、`handler_task_test.go`、`handler_superagent_test.go`、`handler_platform_test.go`（含 `AppURLResolver` 三级优先级与 OAuth 全分支 302 跳转），实现对应 Handler 与 `app_url.go`。
- [ ] **T7（内置 `/mcp` JSON-RPC 2.0 端点）**：编写 `internal/web/mcp/handler_test.go`（`GET 405`、无效 Bearer `401`、Notification `202`、`initialize`/`ping`/`tools/list` 按 `GrantKind` 裁剪、`tools/call` 将 `McpToolError` 与 `apperror.*` 统一映射为 `isError: true`、未知方法 `-32601`、真实 infra 错 `-32603`），实现 `internal/web/mcp/handler.go`。
- [ ] **T8（`internal/ui` 嵌入与 4 处静态微调）**：拷贝 44 个静态资产至 `internal/ui/static/`，实施 §5.7.2 的 4 处精准微调，编写 `internal/ui/embed_test.go`（内嵌读取 + `--ui-dir` 热加载 + 鉴权与缓存头测试），实现 `internal/ui/embed.go` 与 `router.go` 静态挂载。
- [ ] **T9（全量 177 路由表自检单测）**：在 `internal/web/router_test.go` 中通过 `engine.Routes()` 断言 177 条契约路由零冲突、零遗漏且 `POST /api/v1/orgs` 不存在，`go test -race ./internal/web/... ./internal/ui/...` 全绿。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：合入后将 `SA-13` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md`：核对 4 处静态 UI 微调与 `synapse-web` 交接说明与落地代码一致；
- [ ] 若新增任何前后端兼容约定或 `synapse-web` 代理规则，同步更新 `synapse-web/README.md`（存在时）。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | 6 级中间件执行顺序与 TraceID 防注入 | `internal/web/middleware_test.go` | 合法 `X-Request-Id` 原样回显；含换行/超 64 字符替换为 32 位 hex；Panic 返 500 `internal_error` 且含 `X-Frame-Options: SAMEORIGIN` |
| 2 | 未登录双模拦截（API vs 页面）与白名单放行 | `internal/web/middleware_test.go` | 未登录请求 `/api/v1/agents` 返 `401 unauthorized`；请求 `/` 返 `302 /login`；请求 `/api/v1/meta`、`/api/v1/branding/logo`、`/api/v1/orgs/registration`、`/login`、`POST /api/v1/automations/abc/trigger` 正常放行 |
| 3 | 首次强制改密拦截与豁免清单 | `internal/web/middleware_test.go` | `mustChangePassword=true` 请求 `/api/v1/agents` 返 `403 password_change_required`；请求 `/api/v1/auth/me`、`PUT /api/v1/account/password` 放行；改密后 `password_ok` 缓存命中不再查库 |
| 4 | RBAC 精细分权（`/connectors` 与 `/settings`） | `internal/web/middleware_test.go` | 普通用户 `GET /api/v1/connectors`、`PUT /api/v1/connectors/c1/token`、`GET /api/v1/settings` 均 200；`POST /api/v1/connectors`、`PUT /api/v1/settings`、`GET /api/v1/providers` 均 `403 forbidden` |
| 5 | 登录裸 JSON / 登出空体 / 注册路由裁剪 | `internal/web/handler_meta_auth_test.go` | `POST /api/v1/auth/login` 返裸 `{userId,username,roles}` + `HttpOnly; SameSite=Lax` Cookie；`POST /api/v1/auth/logout` 空体 200；`GET /api/v1/orgs/registration` 返 `{open:false}`；`POST /api/v1/orgs` 返 404/405 |
| 6 | SSE 流式帧格式、反代头与断连隔离 | `internal/web/handler_chat_test.go` | 含 `X-Accel-Buffering: no`、`text/event-stream`；每帧匹配 `event: <type>\ndata: {...}\n\n`；409 忙返 JSON；客户端中途 cancel 不取消后台回合 context；`watchLive` 空闲发 `idle` 帧 |
| 7 | 文件防 XSS 预览、`CSP: sandbox` 与 HTTP `Range` | `internal/web/handler_assets_test.go` | `.html`/`.md` 返 `text/plain; charset=utf-8` + `X-Content-Type-Options: nosniff` + `CSP: sandbox`；`.pdf` 不含 `CSP: sandbox`；`?download=true` 返 `attachment`；`Range: bytes=0-3` 返 `206` 与 4 字节 |
| 8 | OAuth 302 全分支与 `AppURLResolver` 三级回退 | `internal/web/handler_platform_test.go` | 正常/拒绝/未知 connector 全部 `302` 至外站或 `/?connect_error=...#connectors`；`publicUrl > base_url > X-Forwarded-Proto/Host` 优先级精确 |
| 9 | `/mcp` JSON-RPC 2.0 与统一 `isError: true` 修复 | `internal/web/mcp/handler_test.go` | `GET /mcp` 405；坏 Token 401；无 `id` 202；`initialize` 返 `2024-11-05`；`StandIn` 令牌可解析并在 `Release` 后立即 401；`apperror.NotFound`/`Conflict`/`InvalidArg` 在 `tools/call` 均返 `200` + `result.isError=true` |
| 10 | 内嵌静态 UI 与 `--ui-dir` 热加载 + 177 路由全量断言 | `internal/ui/embed_test.go`、`internal/web/router_test.go` | 内嵌读取 `index.html`/`login.html`；临时目录 `--ui-dir` 修改即时可见；`engine.Routes()` 完整覆盖 177 路由无 panic、无参数冲突；`GET /api/v1/agents/characters` 返回 `[]` |

---

## 9. 验收门

- [ ] `go build ./... && go test -race ./internal/web/... ./internal/ui/...` 全绿，零竞态告警
- [ ] §2 交付物清单 16 项逐项存在，且 `internal/web` / `internal/ui` 零直接导入 `pgx` 或 `internal/storage/postgres`
- [ ] `router_test.go` 机器校验全部 177 条契约路由精确注册、参数名无 Gin 冲突、`POST /api/v1/orgs` 不存在
- [ ] §4.2.2 全部 7 类响应信封例外均有独立单测断言保真
- [ ] §8 测试清单 10 类用例全部落地，包覆盖率 ≥ 80%（`go test ./internal/web/... ./internal/ui/... -cover`）
- [ ] 人工联调冒烟：启动 `synapse-server serve --ui-dir ./internal/ui/static`，浏览器验证 `/login`（无注册入口）→ 登录 → 强制首次改密 → 创建 Agent（无 24 性格下拉）→ SSE 对话与切换 Agent 重连旁观 → 文件 HTML 文本安全预览与视频拖拽 → 连接器飞书/钉钉 + 自定义 MCP 展示

---

## 10. 可提交节点

- 建议拆分 3 个清晰递进提交，或单 PR `SA-13` 汇总合入：
  1. `feat(web): implement Gin 6-stage middleware pipeline, error mapper and auth/account handlers (spec 13, PR SA-13)`
  2. `feat(web): wire 175 V1 REST/SSE/file/oauth endpoints and /mcp JSON-RPC 2.0 gateway (spec 13, PR SA-13)`
  3. `feat(ui): embed static SPA with --ui-dir hot reload and 4 V1 commercial UI adjustments (spec 13, PR SA-13)`
