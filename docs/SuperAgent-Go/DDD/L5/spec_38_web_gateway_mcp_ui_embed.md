# spec 38 | L5 Web 网关与 UI — Gin 6 级中间件、认证、`/mcp` JSON-RPC 2.0 端点与静态 UI 嵌入

- **状态**：`draft`
- **篇号顺序**：38（L5 接入层首篇；搭建 Gin 网关底座、6 级中间件、认证与兼容桩、内置 `/mcp` 端点及 `embed.FS` 静态 UI 嵌入与 4 处精准适配）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-38`
- **预估工作量**：`2 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §3, §5.2
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §3
  - [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §1
  - 归档母本：[_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md](../_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md) §1–§5.2, §5.6–§5.7
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-web/src/main/java/com/superagent/web/auth/SecurityConfig.java` (`L60-L120`)
    - `superagent-web/src/main/java/com/superagent/web/TraceIdFilter.java`
    - `superagent-web/src/main/java/com/superagent/web/auth/OrgLoginFilter.java` (`L26-L37`)
    - `superagent-web/src/main/java/com/superagent/web/auth/TenantFilter.java`
    - `superagent-web/src/main/java/com/superagent/web/auth/PasswordChangeFilter.java`
    - `superagent-web/src/main/java/com/superagent/web/GlobalExceptionHandler.java` (`L33-L106`)
    - `superagent-web/src/main/java/com/superagent/web/mcp/McpController.java` (`L35-L144`)
    - `superagent-web/src/main/java/com/superagent/web/MetaController.java` (`L29-L33`)
    - `superagent-web/src/main/java/com/superagent/web/auth/AuthController.java` (`L40-L55`)
    - `superagent-web/src/main/java/com/superagent/web/auth/LoginPageController.java` (`L15-L19`)
    - `superagent-web/src/main/java/com/superagent/web/org/OrgController.java` (`L22-L31`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`apperror`、`obs`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant`）
  - [L1/spec_14 (SA-14)](../L1/spec_14_runtime_model_gateway_mcp_client.md)（`McpGrants`/`McpDispatcher`）
  - [L2/spec_15 (SA-15)](../L2/spec_15_platform_tenant_bootstrap_auth.md)（`Authenticator`/`SessionManager`）
  - [L2/spec_17 (SA-17)](../L2/spec_17_platform_settings_notice_analytics.md)（`BrandingService`/`SettingsService`）
- **下游消费者**：`SA-39`（挂载 97 条平台/资产/成本端点）、`SA-40`（挂载 71 条 Agent/SSE/Task/IM/SuperAgent 端点）、`SA-41`（装配 `NewRouter` 与 `RegisterSystemRoutes`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

本篇（`SA-38`）建立 `synapse-server` 的 Web 传输与展示底座，解决四大基础问题：
1. **显式零魔法的 Gin 6 级中间件链**：等价替换原版 Spring Security FilterChain（`Recovery` → `TraceID` → `Auth` → `Tenant` → `PasswordChange` → `RequireAdmin`），区分 API 未登录 `401` 与页面未登录 `302 /login`，统一附安全头 `X-Frame-Options: SAMEORIGIN`；
2. **统一信封与异常翻译**：标准接口返回 `{"code":"ok","message":"success","data":...}`，并严格守住 7 类响应例外（登录裸 JSON、登出空体 200、SSE 流、二进制文件/图片流、OAuth 302、`/mcp` 裸 JSON-RPC 2.0）；
3. **内置 `/mcp` Streamable-HTTP 网关与 `isError: true` 修复**：`POST /mcp` 支持 `protocolVersion: "2024-11-05"`、`GET /mcp -> 405`、Notification `202`，并将全部 `McpToolError` 与 `apperror.*` 统一封装为 `result.isError = true`（文本前缀 `"错误："`），杜绝 `-32603` 泄漏；
4. **单二进制 `//go:embed all:static` + `--ui-dir` 热加载 + 4 处精准静态 UI 微调**：无需独立构建即可开箱跑通现有前端，同时为后续演进 `synapse-web` 冻结稳定 API 契约。

### 1.2 本篇边界

- **本篇负责**：`internal/web/{router.go,middleware.go,response.go,app_url.go,handler_meta_auth.go}`、`internal/web/mcp/handler.go`、`internal/ui/{embed.go,static/*}` 及单测（覆盖 177 条总端点中的 **9 条基础端点**：Meta/页面/认证/品牌 7 + `/mcp` 2）。
- **本篇不负责**：97 条平台/资产/成本业务 Handler（交由 `SA-39`）；71 条 Agent/对话/任务/IM/编排 Handler（交由 `SA-40`）；`/actuator/*` 运维探针（交由 `SA-41` 经 `RegisterSystemRoutes` 挂载）。**严禁 import `pgx` 或 `internal/storage/postgres`**。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | Gin 路由总装配、`Dependencies`/`Options` 与 `:id` 防冲突规范 | `internal/web/router.go` | 新增 |
| 2 | 6 级中间件链（`Recovery`/`TraceID`/`Auth`/`Tenant`/`PasswordChange`/`RequireAdmin`） | `internal/web/middleware.go` | 新增 |
| 3 | 统一响应信封、`HandleError`、SSE 写帧器与 `AppURLResolver` | `internal/web/response.go`、`app_url.go` | 新增 |
| 4 | 元信息、登录页、表单登录/登出、`/auth/me`、Logo 与 `/orgs/registration` 兼容桩（7 端点） | `internal/web/handler_meta_auth.go` | 新增 |
| 5 | 内置 `/mcp` Streamable-HTTP JSON-RPC 2.0 端点（2 端点，含 `isError: true` 修复） | `internal/web/mcp/handler.go` | 新增 |
| 6 | 双模静态 UI 嵌入（`//go:embed all:static` + `--ui-dir`）与 4 处精准微调资产 | `internal/ui/embed.go`、`internal/ui/static/*` | 新增 |
| 7 | 中间件、认证、`/mcp` 与 UI 嵌入单测套件 | `internal/web/{middleware,response,handler_meta_auth}_test.go`、`internal/web/mcp/handler_test.go`、`internal/ui/embed_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表、不接触数据库驱动，所有会话与用户读取通过 `SA-15` 的 `SessionManager`/`Authenticator` 完成（PG17 `user_session`，零 Redis）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package web

type Options struct {
	Version              string
	PublicBaseURL        string
	MaxUploadBytes       int64
	SSETimerTimeout      time.Duration
	SessionCookieName    string // 默认 "SYNAPSE_SESSION"
	SessionCookieSecure  bool
	SessionTTL           time.Duration // 默认 7d
	TrustProxyHeaders    bool
	UIStaticFS           fs.FS
	RegisterSystemRoutes func(r *gin.Engine, authMW gin.HandlerFunc)
}

func NewRouter(deps Dependencies, opts Options) (*gin.Engine, error)
func PrincipalFromContext(ctx context.Context) (*spi.AuthPrincipal, bool)
func WriteOK(c *gin.Context, data any)
func WriteError(c *gin.Context, status int, code, msg string)
func HandleError(c *gin.Context, err error)
```

```go
package ui

func NewFS(uiDir string) (fs.FS, error)
```

### 4.2 本篇冻结 9 条端点契约（177 端点之 1–7, 176–177）

| # | Method | Path | Auth | 响应格式与关键约束 |
|---|---|---|---|---|
| 1 | `GET` | `/api/v1/meta` | `Public` | `ApiResponse<{name,status,version}>` |
| 2 | `GET` | `/login` | `Public` | 已登录 `302 /`；未登录 `200 login.html` |
| 3 | `GET` | `/api/v1/branding/logo` | `Public` | 二进制图片 + `ETag` + `If-None-Match 304` + `nosniff` + `no-cache` |
| 4 | `GET` | `/api/v1/orgs/registration` | `Public` | **V1 兼容桩**：恒返 `ApiResponse<{"open":false}>`（**彻底不注册 `POST /api/v1/orgs`**） |
| 5 | `POST` | `/api/v1/auth/login` | `Public` | **裸 JSON**：`200 {"userId","username","roles"}` + `HttpOnly; SameSite=Lax` Cookie；`401 {"code":"unauthorized","message":"..."}` |
| 6 | `POST` | `/api/v1/auth/logout` | `Auth(NoPwdGate)` | 删 `user_session` + 清 Cookie → **200 空体** |
| 7 | `GET` | `/api/v1/auth/me` | `Auth(NoPwdGate)` | `ApiResponse<MeDto>` |
| 176 | `GET` | `/mcp` | `Public` | 恒返 **405 空体** |
| 177 | `POST` | `/mcp` | `Bearer 48-hex` | 裸 JSON-RPC 2.0（`2024-11-05`；无 `id` 返 `202`；无效令牌 `401`） |

### 4.3 错误语义

- `HandleError` 全量映射：`*http.MaxBytesError` → `413 too_large`（`"单个文件不能超过 50 MB"`）；`InvalidArg` → `400`；`Unauthorized` → `401`；`PasswordChangeRequired` → `403 password_change_required`；`Forbidden` → `403`；`NotFound` → `404`；`Conflict` → `409 conflict`；`Busy` → `409 busy`；`context.Canceled`/broken pipe 静默 Debug 日志不回写；其余 → `500 internal_error`；
- `/mcp` `tools/call`：捕获 `McpToolError` 及全部 `apperror.{InvalidArg,NotFound,Forbidden,Conflict,Busy}` 一律返回 HTTP `200` + JSON-RPC `result: {content:[{type:"text",text:"错误："+msg}], isError:true}`。

### 4.4 SQL 契约

本篇无 SQL 操作。

---

## 5. 行为语义

### 5.1 6 级中间件执行序

1. `Recovery`：捕获 panic 记 `slog.ErrorContext` 返 `500 internal_error`，写 `X-Frame-Options: SAMEORIGIN`；
2. `TraceID`：校验 `X-Request-Id`（`^[A-Za-z0-9._-]{1,64}$`，非法替换为 32 位 hex 防日志注入）并注入 `ctx` 与响应头；
3. `Auth`：读 `SYNAPSE_SESSION` Cookie 校验 `user_session`，未登录对 `/api/*` 返 `401`，对页面路径返 `302 /login`（放行白名单：`/api/v1/meta`, `/api/v1/branding/logo`, `/api/v1/orgs/registration`, `/api/v1/auth/login`, `/login`, `/mcp`, `/api/v1/automations/:id/trigger`, `/actuator/health`, 静态 CSS/JS）；
4. `Tenant`：从 `principal.CompanyID` 调 `tenant.WithCompany` 注入 `ctx`（Fail-Closed）；
5. `PasswordChange`：豁免 `/api/v1/auth/me`, `/api/v1/auth/logout`, `/api/v1/account/password`，其余在 `mustChangePassword == true` 时拦截返 `403 password_change_required`；
6. `RequireAdmin`：管理员路由组强校验角色含 `"ADMIN"`，否则 `403 forbidden`。

### 5.2 4 处精准 V1 静态 UI 微调（`internal/ui/static/*`）

1. **`login.html` / `login.js`**：隐藏未接通的 `Phone`/`SSO`/`WeChat` 占位 Tab；依赖 `/orgs/registration -> {open:false}` 自动隐藏“注册新组织”；
2. **`index.html` / `js/agent-form.js`**：`/agents/characters` 返回 `[]` 时隐藏性格下拉框，新建默认 `chosenCharacter = null`，纯自定义 `persona`；
3. **`js/connectors.js` / `js/integrations.js`**：自然仅展示飞书/钉钉 + 自定义 MCP；补齐 `dingtalk` 图标类型与保存后 5s 状态回刷；
4. **`js/models.js`**：识别目录 `maturity === "ga"` 展示轻量 `GA` 徽章（V1 展示 Claude Code & Codex）。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `response_test.go` 与 `middleware_test.go`（红灯）→ 实现 `response.go`、`middleware.go`、`app_url.go`。
- [ ] **T2**：编写 `handler_meta_auth_test.go`（红灯：登录裸 JSON、登出空体、`POST /api/v1/orgs` 不存在、`GET /orgs/registration -> false`）→ 实现 `handler_meta_auth.go` 与 `router.go`。
- [ ] **T3**：编写 `internal/web/mcp/handler_test.go`（红灯：`GET 405`、`401`、Notification `202`、`StandIn` 令牌与 `isError: true` 修复）→ 实现 `internal/web/mcp/handler.go`。
- [ ] **T4**：拷贝 44 个静态资产至 `internal/ui/static/` 完成 4 处微调，编写 `internal/ui/embed_test.go` 并实现 `internal/ui/embed.go`。
- [ ] **T5**：运行 `go test -race -cover ./internal/web/... ./internal/ui/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-38` 状态更新；
- [ ] [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md)：核对 4 处静态微调一致。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 6 级中间件顺序、TraceID 防注入与双模未登录拦截 | `internal/web/middleware_test.go` | 含换行 `X-Request-Id` 被替换；API 未登 `401`、页面 `302 /login`；强制改密豁免 3 路径；非 Admin 返 `403` |
| 2 | 登录裸 JSON、登出空体与自注册移除 | `internal/web/handler_meta_auth_test.go` | 登录返裸 `{userId,username,roles}` + `HttpOnly; SameSite=Lax`；`GET /orgs/registration` 返 `open:false`；`POST /orgs` 返 404/405 |
| 3 | `/mcp` 协议与统一 `isError: true` 修复 | `internal/web/mcp/handler_test.go` | `GET 405`、坏令牌 `401`、无 `id` `202`、`initialize` 返 `2024-11-05`、`apperror.*` 均返 `200` + `result.isError=true` |
| 4 | 双模静态 UI 与 `--ui-dir` 热加载 | `internal/ui/embed_test.go` | 内嵌可读 `index.html`/`login.html`；临时目录 `--ui-dir` 修改实时生效 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/web/... ./internal/ui/...` 全绿
- [ ] `internal/arch_test.go` 通过（`internal/web`/`internal/ui` 零 import `pgx`、`internal/storage/postgres`、Redis）
- [ ] 行覆盖率 ≥ 80%，4 处静态微调逐项落实

---

## 10. 可提交节点

- 建议 commit 1：`feat(web): implement Gin 6-stage middleware, error mapper, auth endpoints and /mcp JSON-RPC 2.0 gateway (spec 38, SA-38)`
- 建议 commit 2：`feat(ui): embed static SPA with --ui-dir hot reload and 4 V1 commercial UI tweaks (spec 38, SA-38)`
