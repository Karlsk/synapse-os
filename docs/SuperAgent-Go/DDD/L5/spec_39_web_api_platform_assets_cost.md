# spec 39 | L5 Web API — 平台管理、供应商连接器、企业资产与成本用量 HTTP Handler（97 端点）

- **状态**：`draft`
- **篇号顺序**：39（L5 业务路由第一批；挂载账号/用户、资产、供应商、连接器 OAuth、定价、设置、通知、用量与分析共 97 条冻结端点）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-39`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §1
  - 归档母本：[_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md](../_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md) §4.2.3 (B/G/H), §5.4–§5.5
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-web/src/main/java/com/superagent/web/account/AccountController.java`、`superagent-web/src/main/java/com/superagent/web/user/{UserController,DirectoryController}.java`
    - `superagent-web/src/main/java/com/superagent/web/project/ProjectController.java` (`L21-L76`)
    - `superagent-web/src/main/java/com/superagent/web/file/{FileController,FileStorageController}.java`
    - `superagent-web/src/main/java/com/superagent/web/skill/SkillController.java` (`L26-L92`)
    - `superagent-web/src/main/java/com/superagent/web/connector/{ConnectorController,ConnectorOAuthController}.java`
    - `superagent-web/src/main/java/com/superagent/web/provider/ProviderController.java` (`L42-L146`)
    - `superagent-web/src/main/java/com/superagent/web/model/ModelPriceController.java` (`L24-L45`)
    - `superagent-web/src/main/java/com/superagent/web/settings/SettingsController.java` (`L33-L71`)
    - `superagent-web/src/main/java/com/superagent/web/integration/IntegrationController.java` (`L29-L38`)
    - `superagent-web/src/main/java/com/superagent/web/notice/NoticeController.java` (`L28-L58`)
    - `superagent-web/src/main/java/com/superagent/web/usage/UsageController.java` (`L38-L48`)
    - `superagent-web/src/main/java/com/superagent/web/analytics/AnalyticsController.java` (`L21-L24`)
- **前置依赖**：
  - [L5/spec_38 (SA-38)](./spec_38_web_gateway_mcp_ui_embed.md)（Gin 引擎、6 级中间件与 `AppURLResolver`）
  - [L2/spec_15](../L2/spec_15_platform_tenant_bootstrap_auth.md)–[L2/spec_23](../L2/spec_23_assets_skills_mcp.md)（全部 L2 平台与资产服务）
  - [L3/spec_24](../L3/spec_24_cost_catalog_pricing_usage.md)–[L3/spec_25](../L3/spec_25_cost_task_model_router.md)（定价与用量服务）
- **下游消费者**：`SA-40`（共享 `:id` 路由树校验）、`SA-41`（装配）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

本篇（`SA-39`）将已就绪的全部 L2 平台管理、企业资产以及 L3 定价/用量服务接入 Gin 路由器，一次性交付 **97 条** `/api/v1` 业务端点（1.5 人天 PR），重点保障：
1. **文件预览安全与 `Range` 视频拖拽**：`.html`/`.md` 强制 `text/plain; charset=utf-8` + `CSP: sandbox` + `nosniff`，PDF 例外允许内置阅读器，本地与 S3 均支持 `206 Partial Content`；
2. **OAuth 302 零 JSON 报错**：`GET /api/v1/oauth/:provider/{authorize,callback}` 无论成功、拒绝或配置缺失一律 `302 Found` 跳转至外部页或 `/?connected=<id>#connectors` / `/?connect_error=<code>#connectors`；
3. **Gin 路径参数归一化**：强制将原版 `PUT /api/v1/agents/{agentId}/project` 统一为 `PUT /api/v1/agents/:id/project`，杜绝 Gin 前缀树 panic。

### 1.2 本篇边界

- **本篇负责**：`internal/web/{handler_account_user.go,handler_assets.go,handler_platform.go}` 及其单测，共挂载 **97 条冻结端点**。
- **本篇不负责**：Agent/对话 SSE/任务/自动化/收件箱/SuperAgent 的 71 条端点（交由 `SA-40`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 个人账号、头像、用户管理与同事名录 Handler（10 端点） | `internal/web/handler_account_user.go` | 新增 |
| 2 | 项目（9）、工作文件/目录（14，含 `Range` 与防 XSS 预览）、存储后端（3）、技能（11）Handler（37 端点） | `internal/web/handler_assets.go` | 新增 |
| 3 | 连接器（9）、OAuth（2）、供应商/Node/Key 池（19）、模型定价（4）、设置/Logo（5）、企业集成（2）、待办通知（6）、个人与管理用量（2）、运营分析（1）Handler（50 端点） | `internal/web/handler_platform.go` | 新增 |
| 4 | 97 端点权限矩阵、文件安全预览、Range、OAuth 302 单测套件 | `internal/web/{handler_account_user,handler_assets,handler_platform}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表、不接触数据库驱动，零 Redis 依赖。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package web

func registerAccountUserRoutes(auth, noPwdGate, admin *gin.RouterGroup, deps Dependencies)
func registerAssetRoutes(auth, admin *gin.RouterGroup, deps Dependencies, opts Options)
func registerPlatformRoutes(auth, admin *gin.RouterGroup, deps Dependencies, appURL *AppURLResolver)
```

### 4.2 本篇冻结 97 条端点分组清单（对应母本 #8–#17, #84–#161, #167–#175）

1. **账号、头像、用户与通讯录（10 端点）**：
   - `PUT /api/v1/account/password` (`NoPwdGate`)、`PUT/DELETE /api/v1/account/avatar`、`GET /api/v1/avatars`、`GET /api/v1/avatars/:username`（二进制 30d 私有缓存）；
   - `GET/POST /api/v1/users` (`Admin`)、`PUT/DELETE /api/v1/users/:id` (`Admin`)、`GET /api/v1/directory/users`。
2. **项目、文件、存储与技能（37 端点）**：
   - 项目（9）：`GET/POST /api/v1/projects`、`GET/PUT /api/v1/projects/:id`、`POST /api/v1/projects/:id/{archive,reopen,members}`、`DELETE /api/v1/projects/:id/members/:username`、`PUT /api/v1/agents/:id/project`（**强制 `:id`**）；
   - 文件与目录（14）：`GET/POST /api/v1/files/folders`、`PUT/DELETE /api/v1/files/folders/:id`、`PUT /api/v1/files/:id/folder`、`GET /api/v1/files`、`GET /api/v1/files/:id`、`GET /api/v1/files/:id/content`（XSS 防护 + `Range`）、`POST /api/v1/files/{uploads,markdown}`、`PUT /api/v1/files/:id/{content,tags}`、`PUT/DELETE /api/v1/files/:id`；
   - 文件存储（3，`Admin`）：`GET/PUT /api/v1/admin/file-storage`、`POST /api/v1/admin/file-storage/test`；
   - 技能（11）：`GET /api/v1/skills/deletions`、`POST /api/v1/skills/deletions/:deletionId/{confirm,cancel}`、`GET/POST /api/v1/skills`、`GET/PUT/DELETE /api/v1/skills/:id`、`POST /api/v1/skills/:id/images`、`GET/DELETE /api/v1/skills/:id/images/:name`。
3. **连接器、OAuth、供应商、定价、设置、集成、通知、用量与分析（50 端点）**：
   - 连接器（9）：`GET /api/v1/connectors`、`GET /api/v1/connectors/:id/tools`、`POST /api/v1/connectors` (`Admin`)、`PUT/DELETE /api/v1/connectors/:id` (`Admin`)、`PUT /api/v1/connectors/:id/enabled` (`Admin`)、`PUT/DELETE /api/v1/connectors/:id/token`、`POST /api/v1/connectors/:id/bind-code`；
   - OAuth（2）：`GET /api/v1/oauth/:provider/{authorize,callback}`（`302`）；
   - 供应商/Node/Key 池（19，`Admin`）：`GET/POST /api/v1/providers`、`GET /api/v1/providers/catalog`、`GET/POST /api/v1/providers/node{/install,/install-log}`、`GET/DELETE /api/v1/providers/:id`、`POST /api/v1/providers/:id/{install,upgrade,models/refresh,check-latest}`、`GET /api/v1/providers/:id/install-log`、`GET/POST /api/v1/providers/:id/credentials`、`PUT /api/v1/providers/:id/credentials/order`、`PUT/DELETE /api/v1/providers/:id/credentials/:cid`、`POST /api/v1/providers/:id/credentials/:cid/check`；
   - 定价（4，`Admin`）：`GET /api/v1/admin/model-prices`、`POST /api/v1/admin/model-prices/refresh`、`PUT /api/v1/admin/model-prices/{price,tasks}?model=`；
   - 设置与 Logo（5）：`GET /api/v1/settings`、`PUT /api/v1/settings` (`Admin`)、`GET/PUT/DELETE /api/v1/settings/logo` (`Admin`)；
   - 集成（2，`Admin`）：`GET /api/v1/admin/integrations`、`PUT /api/v1/admin/integrations/:key`；
   - 通知（6）：`GET /api/v1/notices`、`GET /api/v1/notices/stats`、`POST /api/v1/notices/:id/{done,ignore,reopen}`、`POST /api/v1/notices/read-all`；
   - 用量与分析（3）：`GET /api/v1/usage`、`GET /api/v1/admin/usage` (`Admin`)、`GET /api/v1/admin/analytics` (`Admin`)。

### 4.3 错误语义

- 文件上传 `http.MaxBytesReader` 限流 `51 MiB`（文件本体 `<= 50 MB`），超限经 `HandleError` 固定返回 `413 too_large` `"单个文件不能超过 50 MB"`；
- `OAuthAuthorize`/`OAuthCallback` 捕获全部错误转为 `302 /?connect_error=<code>#connectors`，绝不返回 JSON 错页；
- 其余均由 `SA-38` `HandleError` 统一映射。

### 4.4 SQL 契约

本篇无直接 SQL 操作。

---

## 5. 行为语义

### 5.1 文件内容预览与 `Range` 流（对齐 `FileController.java:108-127`）

- 严格按 `inlineFileTypes` 白名单将 `.md/.markdown/.txt/.csv/.json/.html/.htm` 强制转 `text/plain; charset=utf-8`；
- 响应头必带 `X-Content-Type-Options: nosniff`、`Cache-Control: no-store`、RFC 5987 UTF-8 `Content-Disposition`；非 `.pdf` 一律附 `Content-Security-Policy: sandbox; default-src 'none'; img-src 'self' data:; media-src 'self'`；
- 若 `content.ReadSeeker != nil` 调用 `http.ServeContent` 自动处理 `Range`/`If-Range`/`206 Partial Content`。

### 5.2 二进制图片安全响应

头像（`/avatars/:username`）与技能配图（`/skills/:id/images/:name`）统一设 `X-Content-Type-Options: nosniff` 与 `Cache-Control: private, max-age=2592000`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `handler_account_user_test.go`（红灯）→ 实现 `handler_account_user.go`。
- [ ] **T2**：编写 `handler_assets_test.go`（红灯：`.html` `CSP: sandbox`、`.pdf` 豁免、`Range 206`、50 MB `413`、`:id` 统一无冲突）→ 实现 `handler_assets.go`。
- [ ] **T3**：编写 `handler_platform_test.go`（红灯：RBAC 细粒度分权、`/providers/node` 与 `/:id` 同级不冲突、`/credentials/order` 与 `/:cid` 不冲突、OAuth 全分支 `302`）→ 实现 `handler_platform.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/web/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-39` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 文件 XSS 安全预览与 HTTP `Range` | `internal/web/handler_assets_test.go` | `.html`/`.md` 强返 `text/plain` + `CSP: sandbox`；`.pdf` 无 sandbox；`Range: bytes=0-3` 返 `206`；超 50 MB 返 `413` |
| 2 | RBAC 精细分权与静态/参数路由无冲突 | `internal/web/handler_platform_test.go` | 普通用户 `GET /connectors`/`GET /settings` 200，`POST /connectors`/`PUT /settings`/`GET /providers` 403；`PUT /credentials/order` 准确命中静态段 |
| 3 | OAuth 302 全分支与 `AppURLResolver` | `internal/web/handler_platform_test.go` | 成功/拒绝/未知 provider 均 `302` 重定向且无 JSON 报错页 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/web/...` 全绿
- [ ] 97 条端点全部注册且 Gin 路由树零 panic，行覆盖率 ≥ 80%，零 Redis

---

## 10. 可提交节点

- 建议 commit：`feat(web-api): wire 97 platform, provider, connector, asset and cost HTTP handlers (spec 39, SA-39)`
