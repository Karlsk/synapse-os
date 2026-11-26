# AGENTS.md

本文件定义 `synapse-os`（SuperAgent Go 商业私有化 V1）的**强制工程规范与硬红线**，供所有 AI Coding Agent 与研发人员遵循。详细分册设计与 PR 级 Spec 见 [docs/SuperAgent-Go/README.md](docs/SuperAgent-Go/README.md) 与 [docs/SuperAgent-Go/DDD/README.md](docs/SuperAgent-Go/DDD/README.md)；原版 Java 行为参照 `../orgin/SuperAgent`。

---

## 1. 项目定位与 V1 边界

- **产品定位**：面向企业私有化部署（允许联网）的商业化 AI 协作工作系统。以**公司与员工视角**设计，所有对话、任务、文件、技能沉淀为企业数字资产，而非个人工具。
- **V1 核心边界**：
  - **多租户强隔离 + 商业化开户**：保留全表 `company_id` 隔离与租户级信封加密；**关闭公网自助注册**（禁止暴露 `POST /api/v1/orgs`，`GET /api/v1/orgs/registration` 恒返 `{"open":false}`），改为 `bootstrap-org` CLI / 平台超管开户。
  - **插件化运行时**：V1 落地 **Claude Code（含热进程池）** 与 **Codex** 两个 GA 适配器；内置 `/mcp` 网关与**飞书/钉钉**预置生态 + 通用自定义 MCP。
  - **极简基础设施（零 Redis / 零 ClickHouse）**：最小商业交付仅依赖 **单一 Go 静态二进制 + 单库 PostgreSQL 17**。严禁引入 Redis 客户端或预留分布式缓存抽象。
  - **严格裁剪**：不搬运 GitHub/Gitee/Notion/Confluence 预置连接器、24 套性格预设、3 个消费级种子技能、微信/Google/匿名飞书爬虫及原版废弃列（`*.tier`、`character_key`、`tracked_json` 等）。

---

## 2. 技术栈、包管理器与构建命令

### 2.1 核心技术栈（硬锁定）

坚持**少依赖、强并发、零 CGO、单静态二进制**原则：

| 层次 | 选型 | 说明与硬约束 |
|---|---|---|
| **语言与运行时** | **Go 1.26.1** | 后端主模块位于 `synapse-server/`（`module github.com/Karlsk/synapse-os`） |
| **Web / HTTP / SSE** | `github.com/gin-gonic/gin` | REST `/api/v1`、流式 SSE、Multipart 50 MiB 上传、`/mcp` 端点 |
| **数据库与驱动** | **PostgreSQL 17** + `github.com/jackc/pgx/v5` (`pgxpool`) | V1 单库方案；禁止使用重 ORM，手写 SQL 封装在 `internal/storage/postgres` |
| **Schema 迁移** | `github.com/pressly/goose/v3` + `//go:embed` | 版本化 SQL 内嵌单二进制，只进不退 |
| **配置解析** | `gopkg.in/yaml.v3` | 开启 `KnownFields(true)` + 环境变量覆盖 + 启动 fail-fast 校验 |
| **弹性与调度** | `github.com/sony/gobreaker/v2`、`github.com/robfig/cron/v3` | `service+host` 熔断；5 字段 Cron + IANA 时区 DST 安全推算 |
| **加密与认证** | 标准库 `crypto/aes`、`crypto/cipher`、`crypto/sha256`、`crypto/subtle` + `golang.org/x/crypto/bcrypt` | 每租户 AES-256-GCM DEK 信封加密、常量时间 Token 校验、密码哈希 |
| **可观测性** | 标准库 `log/slog` + `github.com/prometheus/client_golang` | 脱敏上下文日志 + 独立 Prometheus Registry（`/actuator/prometheus`） |
| **对象存储（可选）** | `github.com/aws/aws-sdk-go-v2/service/s3` | 兼容 S3 / MinIO / 阿里云 OSS / 腾讯云 COS / R2（默认支持本地磁盘） |
| **IM 与文档解析** | 飞书官方 Go SDK / 钉钉 Stream（`github.com/coder/websocket`）+ `golang.org/x/net/html` + PDF 提取库 | 每公司独立出站 WebSocket；支持企业代理与自签 CA |
| **前端与资产嵌入** | 标准库 `embed.FS`（V1 过渡静态 SPA）；二期 `synapse-web/` 演进现代前端 | 开发期支持 `--ui-dir` 直读热刷新 |
| **容器与交付** | `golang:1.26-bookworm`（编译）→ `node:24-bookworm-slim` + `tini`（非 root `uid=10001`）+ `postgres:17-alpine` | 预装 `@anthropic-ai/claude-code` 与 `@openai/codex`（`NPM_CONFIG_PREFIX=/opt/cli`） |

- **明确禁用依赖**：严禁引入 **Redis 客户端**、**ClickHouse 驱动**、**反射 DI 容器**、**重型 ORM（如 GORM）**。

### 2.2 包管理器
- **后端（`synapse-server/`）**：**Go Modules**（`go mod tidy` / `go get`），`go.mod` 与 `go.sum` 必入库。
- **CLI 运行时 / 现代前端（`synapse-web/`）**：**Node.js 24 + npm**（托管 CLI 支持 `NPM_CONFIG_REGISTRY` 与私有 Node 镜像源）。V1 过渡期直接内嵌静态 SPA 至 `internal/ui/static/`，无需前端打包步骤。

### 2.3 构建、测试与运行命令（在 `synapse-server/` 目录执行）

```bash
# 依赖整理与静态检查
go mod tidy
gofmt -l .                                  # 必须零输出
go vet ./...

# 构建与测试（合并前必须全绿）
go build ./...
go test -race ./...                         # 全仓单测 + 竞态检测（含 arch_test 与 guard_test）
go test -race ./internal/core/...           # 按包快速验证
TEST_POSTGRES_DSN="postgres://..." go test -race ./internal/infra/db/... ./internal/storage/postgres/...

# 单二进制静态发布编译
CGO_ENABLED=0 go build -trimpath -o bin/synapse-server ./cmd/synapse-server

# 运行与运维子命令（默认监听 :18080）
go run ./cmd/synapse-server serve --config configs/config.example.yaml --ui-dir internal/ui/static
go run ./cmd/synapse-server migrate --config configs/config.example.yaml
go run ./cmd/synapse-server bootstrap-org --id acme --name "Acme Corp" --config configs/config.example.yaml
go run ./cmd/synapse-server version

# 冒烟验证
curl -fsS http://127.0.0.1:18080/api/v1/meta
```

---

## 3. 关键架构硬规则

1. **L0–L6 严格单向分层与同层平行模块禁互引（AST 强制拦截）**：
   `L6 (cmd) → L5 (web/ui) → L4 (orchestrator/event) → L3 (agent/task/cost/docs) → L2 (platform/files/project/skills) → L1 (storage/runtime) → L0 (core contracts & primitives / infra)`
   - **禁止低层依赖高层**（`targetLayer > srcLayer` 一律禁止）；
   - **禁止 L1–L5 任意同层平行模块间相互依赖**（`srcLayer >= 1 && targetLayer == srcLayer && srcTopPkg != targetTopPkg` 一律禁止，包括 L1 `storage`↔`runtime`、L2 `platform`/`files`/`project`/`skills` 之间、L3 `agent`/`task`/`cost`/`docs` 之间、L4 `event`↔`orchestrator`、L5 `web`↔`ui`）；同层协作必须通过 L0 `internal/core/spi`、`internal/core/store` 或 `internal/core/eventbus` 倒置解耦，并在 L6 `cmd/synapse-server/wire.go` 装配。
   - 由 `internal/arch_test.go` 在 `go test ./...` 中做静态 AST 校验，任何违规导入直接失败。
2. **显式零反射装配**：全仓依赖仅在 `cmd/synapse-server/wire.go` 手动显式注入，严禁反射黑盒 DI 容器。
3. **Fail-Closed 多租户强隔离**：
   - 所有租户上下文通过 `context.Context` 显式传递；查询与写入内部强制调用 `tenant.Require(ctx)`，**缺失即报错，严禁回落默认租户**。
   - 后台异步 goroutine（如对话流、任务执行、事件异步派发）必须使用 `tenant.Detach(ctx)`（剥离 HTTP 取消与 DB 事务句柄，保留 `company_id`/`request_id`/`user_id`）。
   - 多租户定时巡检统一使用 `FairRunner` 轮转 + 租户级 `recover()` 隔离。
   - 新增配置默认按公司隔离，禁止新增全局设置（仅 CLI 安装、模型价格目录、内置 MCP 地址为平台级）。
4. **基础设施访问隔离红线**：
   - **禁直连数据库**：除 `internal/infra/db`、`internal/storage/postgres`、`cmd/synapse-server` 外，任何包**严禁 import `pgx` 或 `goose`**；业务层只依赖 `internal/core/store` 接口与 `TxManager`。
   - **禁裸起子进程**：严禁业务或适配器直接调用 `os/exec.Command`，所有 CLI 与 STDIO MCP 必须经 `spi.ProcessLauncher`（强制 `Setpgid: true` 整组查杀 + 清理宿主机敏感环境变量 + `0600` 临时文件）。
   - **禁裸建 HTTP Client**：所有出站 HTTP 必须经 `internal/infra/httpclient` 双 Profile 物理隔离：
     - `TrustedClient`：飞书/钉钉/LLM/Node 镜像等可信上游（注入企业代理与自签 CA）；
     - `UntrustedEgressClient`：用户外链/Webhook/自定义外部 MCP（强制 `DialContext` 级抗 DNS Rebinding `EgressGuard` + 逐跳重定向复检 ≤6 跳 + `io.LimitReader` 响应体上限）。
   - **禁业务进 Web 层**：`internal/web` 只做参数校验、鉴权、调用 Service 与响应映射，严禁包含业务逻辑；MCP 工具就近在所属领域包实现 `McpToolProvider`。
   - **凭据与密钥红线**：所有租户密钥必须经 `SecretCipher`（AES-256-GCM 租户 DEK 信封加密 + AAD 绑定 `company_id`），严禁明文落盘、严禁日志打印密钥/Token/DSN 密码段、无租户 Key 时直接返回 `403` 绝不回退宿主机环境变量。

---

## 4. 项目目录架构

```text
synapse-os/
├── synapse-server/                         # Go 1.26 后端主工程
│   ├── cmd/synapse-server/                 # [L6] CLI 入口(serve/migrate/bootstrap-org/version)、wire.go、lifecycle.go、actuator.go
│   ├── configs/config.example.yaml         # 全量注释配置模板
│   └── internal/
│       ├── arch_test.go                    # 全仓 AST 分层、禁循环、禁越权导入、零 Redis 守卫
│       ├── core/                           # [L0/L1] 核心原语与契约（零 pgx/gin/exec 依赖）
│       │   ├── apperror/                   # [L0] 统一领域错误码与 AppError 叶子包
│       │   ├── egress/                     # [L0] 抗 DNS Rebinding SSRF 守卫
│       │   ├── tenant/                     # [L0] Fail-Closed 租户 Context、Detach、SafeJoin 路径防穿越
│       │   ├── crypto/                     # [L0] AES-256-GCM 租户信封加密与 KEK 轮换
│       │   ├── eventbus/                   # [L0] 进程内泛型同步/异步事件总线
│       │   ├── model/ (或 domain/)         # [L1] 领域实体与枚举
│       │   ├── store/                      # [L1] 38 个持久化 Store 接口与 TxManager
│       │   ├── spi/                        # [L1] 跨模块插件 SPI (ProcessLauncher/AgentRunner/ModelGateway/IMConnector/Authenticator...)
│       │   └── api/                        # [L1] HTTP Request/Response DTO 与路径常量
│       ├── infra/                          # [L0] 通用运行时基础设施（零业务逻辑）
│       │   ├── config/                     # YAML(KnownFields) + Env 加载、fail-fast Validate、代理/私有 CA
│       │   ├── obs/                        # 脱敏 slog ContextHandler + 独立 Prometheus Registry
│       │   ├── lifecycle/                  # BackgroundGroup(panic recover) + 30s 逆序 ShutdownManager
│       │   ├── db/                         # pgxpool、Context TxManager(含 SAVEPOINT)、goose 执行器、AdvisoryLock、慢查询 Tracer
│       │   ├── httpclient/                 # TrustedClient / UntrustedEgressClient、重试、gobreaker 熔断
│       │   └── memcache/                   # 纯进程内分片 TTL 缓存(含 singleflight) + 滑动窗口防爆破限流器
│       ├── storage/postgres/               # [L1] PG17 Store 实现 (store_*.go)、//go:embed migrations/*.sql、guard_test.go
│       ├── runtime/                        # [L1] launcher/、runner/、claude/(含 WarmPool)、codex/、gateway/、mcp/、attachment/
│       ├── platform/                       # [L2] tenant/、auth/、user/、settings/、notice/、analytics/、provider/、integration/、connector/
│       ├── files/ | project/ | skills/     # [L2] 工作文件(Local/S3)、项目空间、技能库
│       ├── agent/ | task/ | cost/ | docs/  # [L3] 对话/LiveTurn/记忆、任务/编码流/自动化、定价/路由、飞书/钉钉文档与 LinkReader
│       ├── event/ | orchestrator/          # [L4] 飞书/钉钉长连接与分诊、个人 SuperAgent 双半编排
│       ├── web/                            # [L5] Gin 6 级中间件、统一响应/错误映射、业务 Handler、/mcp 端点
│       └── ui/                             # [L5] //go:embed 静态前端资产（支持 --ui-dir 热刷新）
├── synapse-web/                            # 后续独立演进的现代前端工作区
├── synapse-build/                          # Docker 多阶段非 root 镜像、PG17 Compose、离线 Tarball 脚本
└── docs/                                   # PRD、架构设计与 L0–L6 PR 级 Spec
```

---

## 5. 命名规范与数据库 Schema 规范

### 5.1 代码与包命名
- **包名**：简短、全小写单词（如 `tenant`、`httpclient`、`memcache`），禁止下划线、驼峰或无意义复数；避免 `util`/`common`/`helper` 垃圾桶包。
- **文件名**：`snake_case.go`；单测 `*_test.go`；PG Store 统一 `store_<domain>.go`。
- **标识符**：遵循 Go 规范 `MixedCaps`/`mixedCaps`；常见缩写词保持大小写一致（`ID`、`URL`、`HTTP`、`MCP`、`JSON`、`SSE`、`API`、`UUID`、`SQL`，如 `userID`、`companyID`、`ParseURL`，禁止 `userId`、`ParseUrl`）。
- **接口命名**：按行为命名（`Store`、`Runner`、`Launcher`、`Provider`），**严禁 `I` 前缀或 `Impl` 后缀**。

### 5.2 数据库命名与 Schema 硬约束（PostgreSQL 17）
- **命名规则**：
  - 表名与列名全小写 `snake_case`，表名使用**单数名词**（如 `company`、`app_user`、`agent_event`、`task_phase`、`user_session`）。
  - 普通索引：`idx_<表名>_<核心列>`；唯一索引：`uq_<表名>_<核心列>`。
  - 迁移脚本：`internal/storage/postgres/migrations/<5位递增序号>_<snake_case>.sql`（如 `00001_init_tenant_and_auth.sql`）。
- **租户主键守卫**：除全局表（`company`、`user_session`、`goose_db_version`）外，**100% 必须包含 `company_id VARCHAR(64) NOT NULL` 且位于复合主键首列**（`PRIMARY KEY (company_id, id)`），并由 `internal/storage/postgres/guard_test.go` 自动拦截。
- **迁移安全**：`goose/v3` 永远向前演进；**严禁 `-- +goose Up` 中出现 `DROP TABLE`**（由 `ValidateMigrations` 启动前硬校验）。
- **类型与兼容性**：
  - **零物理外键**（逻辑关联以保障软删除后历史审计完整）；
  - 全部时间列使用 `TIMESTAMPTZ NOT NULL`，强制 UTC 写入（`time.Now().UTC().Truncate(time.Millisecond)`），DSN 固定 `TimeZone=UTC`；
  - 金额列使用 `NUMERIC(16,6)`，领域计算用定点数或微美元整数，**严禁 `float64` 累加账单**；
  - JSON 载荷列使用 `JSONB`；禁止使用难迁移的 PG 专有特性（`CREATE TYPE ... AS ENUM`、存储过程、数组列）。
- **幂等写入**：去重插入用 `INSERT ... ON CONFLICT (...) DO NOTHING`；配置/状态更新用 `ON CONFLICT (...) DO UPDATE SET ...`（`app_user` 严禁按 `username` 冲突覆盖，必须按 `(company_id, id)` 显式更新）。
- **日志安全**：慢查询 Tracer 严禁打印 SQL 绑定参数明文。

---

## 6. 性能、弹性与并发治理硬规范

| 维度 | 硬性指标与实现约束 |
|---|---|
| **数据库连接池** | `pgxpool` 默认 `MaxConns=16`、`MinConns=2`、`MaxConnLifetime=30m`、`MaxConnIdleTime=5m`、`ConnectTimeout=5s`、`StatementTimeout=30s`、`SlowQueryThreshold=200ms`；`/actuator/health` `Ping` 超时 `2s`。 |
| **HTTP 分层超时** | 标准模式：`Dial=5s(Trusted)/10s(Untrusted)`、`TLSHandshake=10s`、`ResponseHeader=30s`、`Total=60s`。<br>**SSE / 大文件流式模式 (`Streaming: true`)**：**强制 `http.Client.Timeout = 0`**（严禁设总超时截断长流），通过 `ctx` 与 `IdleReadTimeout` 控流；对话 SSE 超时 `600s`；任务外链并发预读上限 8 URL 共享 `90s`。 |
| **弹性重试** | 指数退避（`200ms → 5s`）+ Full Jitter，默认 `MaxAttempts=3`（**流式模式强制 `1` 不重试**）；仅重试幂等请求（GET 或显式 `WithIdempotent`）的 `429`（遵循 `Retry-After` 且钳制 `≤30s`）、`502/503/504` 与瞬态网络错；**绝不重试 `EgressDenied` / TLS 错误 / `context.Canceled`**。 |
| **熔断与降级边界** | 基于 `sony/gobreaker/v2` 按 `service + host` 维度熔断（默认周期 `60s`、冷却 `30s`、最小请求 `5`、失败率 `0.6` 或连败 `5`、`MaxHostEntries=256` 防任意域名 OOM）。<br>**职责铁律**：`httpclient` **只返回结构化 `*ClientError` + `ErrorKind`，严禁内置业务降级策略**（切备用 Key、回落抓取等一律由业务调用方决策）。 |
| **Goroutine 与池化** | **禁止裸起无托管 `go func()`**：后台循环统一注册至 `lifecycle.BackgroundGroup`（内置 `panic recover`）；异步事件总线 `eventbus.PublishAsync` 受控并发上限 `64` 并支持 `Drain`；IM 长连接每连接固定 4-worker 有界通道异步入库；`ClaudeWarmPool` 采用 O(1) LRU（上限 `20`、空闲 `10m`、8 维 SHA-256 指纹、取用移出池、**锁外关闭进程**）；`LiveTurn` SSE 扇出非阻塞投递（慢订阅者丢弃，绝不阻塞 CLI stdout）。 |
| **防 OOM 与限流** | 所有内存缓存/限流器（`memcache`）必须设 `MaxEntries` / `MaxKeys`（如限流器默认 `100_000`）；`UntrustedEgressClient` 强制 `io.LimitReader`（默认 `20 MiB`）；文件上传硬限 `50 MiB`。 |
| **优雅停机** | 监听 `SIGINT`/`SIGTERM`，严格执行 **30s 四阶段逆序停机**：① HTTP 排空 + 取消 SSE（≤15s）→ ② 停止全部后台循环（≤8s）→ ③ 关闭 IM WS + 强杀活跃运行与热进程池 `SIGTERM→2s→SIGKILL`（≤5s）→ ④ 关闭 `pgxpool` 与刷盘（≤2s）。容器/脚本 `stop_grace_period` 必须 `≥35s`。 |

---

## 7. API、协议与 Schema 规范

### 7.1 URL 与路由规范
- **统一前缀**：业务 REST API 固定挂载 `/api/v1/<复数资源>`，路径全小写 `kebab-case`，动作子路径用清晰动词（如 `POST /api/v1/tasks/:id/stop`、`POST /api/v1/projects/:id/archive`）。
- **保留端点**：运维探针 `/actuator/{health,prometheus,info}`；内置 MCP 固定 `/mcp`（`POST` 受理，`GET` 恒返 `405`）。
- **Gin 6 级中间件顺序（严禁颠倒）**：`Recovery` → `TraceID` → `Auth` → `Tenant` → `PasswordChange` → `RequireAdmin`。
- **追踪头**：入站校验 `X-Request-Id`（`^[A-Za-z0-9._-]{1,64}$`，非法即替换为 32 位 hex 防日志注入），写入 `ctx`、`slog` 与响应头。

### 7.2 统一响应格式与标准例外
- **标准信封**（绝大多数 `/api/v1/*` 接口）：
  ```json
  { "code": "ok", "message": "success", "data": {} }
  ```
  错误时 `data` 恒为 `null`，由 `internal/web/response.go` 的 `HandleError` 通过 `errors.As` 统一映射 `apperror`：
  `invalid_argument(400)`、`unauthorized(401)`、`password_change_required(403)`、`forbidden(403)`、`not_found(404)`、`method_not_allowed(405)`、`conflict(409)`、`busy(409)`、`too_large(413)`、`rate_limited(429)`、`internal_error(500)`、`service_unavailable(503)`。
- **严格守住 6 类协议例外（不得擅自套信封）**：
  1. `POST /api/v1/auth/login`：表单入参，成功返裸 JSON `{userId, username, roles}` + `HttpOnly; SameSite=Lax` Cookie；失败返 `401 {"code":"unauthorized","message":"..."}`。
  2. `POST /api/v1/auth/logout`：返 `200` 空体。
  3. **SSE 实时流**：`Content-Type: text/event-stream`、`X-Accel-Buffering: no`，帧格式 `event: <type>\ndata: {"type":"...","seq":...,"payload":"..."}\n\n` 且每帧立即 `Flush()`。
  4. **二进制下载/预览**：直接返字节流 + `X-Content-Type-Options: nosniff`；文本预览（`.md/.txt/.html/.json/.csv`）强制 `Content-Type: text/plain; charset=utf-8` + `Content-Security-Policy: sandbox`。
  5. **OAuth 端点**：返回 `302 Found`。
  6. **`POST /mcp`**：裸 JSON-RPC 2.0（`protocolVersion: "2024-11-05"`，无 `id` 通知返 `202`）；**所有工具领域异常一律捕获并返回 HTTP `200` + `result: {"content":[{"type":"text","text":"错误：..."}],"isError":true}`**，严禁泄漏 `-32603`。

### 7.3 Schema / DTO 与分页规范
- **字段命名**：JSON 序列化字段严格 **`camelCase`**（与前端及 `/api/v1` 契约完全一致）；Go 结构体显式标注 `json:"..."` tag。
- **DTO 与实体解耦**：HTTP Request/Response DTO 定义在 `internal/core/api` 或 `internal/web`，严禁直接序列化数据库实体或返回密码哈希、加密密文、内部堆栈；防越权探测场景（文件/代理/资源归属不符）按契约统一返 `404`。
- **分页约定**：1-based `page`（默认 `1`）、`size`（默认 `20`，必须设硬上限 `100`/`200` 防止全表扫）。
- **配置 Schema**：YAML 解析强制开启 `KnownFields(true)`，启动前执行 `Validate()` fail-fast，绝不允许静默忽略拼写错误配置项。

---

## 8. Go 编码风格硬规则（Effective Go / Google Go Style）

- **格式化与零告警**：提交前必须通过 `gofmt`、`go vet ./...`、`go test -race ./...`。
- **错误处理**：
  - 必须显式处理每一个 `error`，禁止 `_ =` 静默忽略关键错误（`defer f.Close()` 仅在只读句柄可忽略，写入句柄必须检查关闭错误）。
  - 使用 `fmt.Errorf("action context: %w", err)` 保留错误链；判错一律用 `errors.Is` / `errors.As`，禁止比较 `err.Error()` 字符串。
  - **错误只处理一次**：禁止底层“既打 `slog.Error` 又 `return err`”导致日志风暴；让错误向上传播至中心处理器（`HandleError` 或异步流终态事件）统一记录与翻译。
  - 生产路径**严禁 `panic`**（仅允许初始化期 `Must*` 常量或不可恢复编程断言）。
- **接口与函数设计**：
  - **接受接口，返回具体类型**；接口保持最小方法集；由消费方或契约层（`core/store`、`core/spi`）定义接口。
  - `ctx context.Context` 永远作为首个参数，**严禁将 `context.Context` 存入 struct 字段**。
  - 优先卫语句（early return）降低嵌套层级；零值可用的结构体设计优先；优先构造注入（`NewXxx(deps...)`），禁止包级可变全局单例状态。
- **并发安全**：
  - 谁创建/写入 channel 谁关闭；绝不向可能已关闭的 channel 发送数据。
  - 缩小 `sync.Mutex` 临界区，**严禁在持锁状态下执行网络/磁盘/数据库 I/O 或关闭子进程**。
  - 所有锁、并发容器与后台协程必须经 `go test -race` 验证零数据竞态。
- **注释与测试**：
  - 代码自文档化，**只注释“为什么 / 不变量 / 并发与安全陷阱”**，禁止复述代码字面含义的废话注释；导出符号写规范 GoDoc（以符号名开头）。
  - 优先表驱动测试（Table-Driven Tests）+ `t.Parallel()`；保持测试收敛——覆盖关键行为、错误分支与安全红线，不写无意义的排列组合噪音测试；PG 集成测试使用 `db.NewTestSchemaDB` 独立临时 Schema 隔离，未设 `TEST_POSTGRES_DSN` 时自动 `t.Skip`。

---

<!-- SPECKIT START -->
For additional context about technologies to be used, project structure,
shell commands, and other important information, read [specs/004-sa04-tenant-crypto-bus-archguard/plan.md](specs/004-sa04-tenant-crypto-bus-archguard/plan.md)
<!-- SPECKIT END -->
