# synapse-server

`synapse-server` 是 **Synapse-OS（SuperAgent Go 商业私有化 V1）** 的 Go 后端主工程（`module github.com/Karlsk/synapse-os`，Go `1.26.1`）。遵循严格 L0–L6 单向分层、零 Redis / 零 ClickHouse / 零重 ORM / 零反射 DI 与单静态二进制 + 单库 PostgreSQL 17 交付原则。

---

## L0 基础设施底座包职责（`SA-01`–`SA-04`）

| 包路径 | 层级 | 依赖范围 | 核心职责 |
|---|---|---|---|
| `internal/core/apperror` | L0（叶子包） | **仅 Go 标准库** | 13 类统一领域错误码（`Code`）、HTTP 状态映射、`ErrEgressDenied` SSRF 哨兵、`FromError` 未知错误脱敏、`UnreadableLinkError` / `UnreadableMaterialsError` 复合资料错误 |
| `internal/infra/config` | L0 | `gopkg.in/yaml.v3`、`golang.org/x/net/http/httpproxy` | 22 段强类型配置树（零 Redis）、`KnownFields(true)` 严格 YAML 解析、多源覆盖加载器、全量聚合 fail-fast `Validate()`、企业代理（保底 `127.0.0.1,localhost,::1` `NO_PROXY`）与自签 PEM CA 证书池 / 子进程环境变量构建 |
| `internal/infra/obs` | L0 | `github.com/prometheus/client_golang` | `request_id`/`company_id`/`user_id` 上下文透传与 `CopyMeta`、`SanitizeRequestID` 防日志注入、`ContextHandler -> RedactingHandler -> Text/JSON` 脱敏 `slog`（支持 `0640` 文件双写）与 `RedactDSN`、独立隔离 Prometheus Registry（携带 `application` 标签及 6 项 `superagent_*` 业务指标 + HTTP 耗时直方图） |
| `internal/infra/lifecycle` | L0 | **仅 Go 标准库** | `BackgroundGroup`（协程生命周期托管、`panic recover` 记堆栈与回调、停机后拒绝新任务、超时列出卡住任务名）与 `ShutdownManager`（5 阶段升序 / 同阶段 LIFO 逆序停机、30s 默认总预算裁剪、`SIGINT`/`SIGTERM` 信号监听） |
| `internal/infra/db` | L0 | `pgx/v5`、`goose/v3`、`prometheus`、`SA-01` | 调优 `pgxpool`（强制 `TimeZone=UTC` 与 `statement_timeout`、2s `Ping`、池指标）、`context` 透传 `TxManager`（嵌套 `SAVEPOINT` + panic 安全回滚）、非全局 `goose/v3` 嵌入迁移（禁 `-- +goose Up` `DROP TABLE` + 迁移锁）、PG 错误码映射、零参数泄漏慢查询 `QueryTracer`、PG 原生 `AdvisoryLock`（零 Redis）与单测隔离夹具 |
| `internal/core/egress` | L0 | **仅 Go 标准库** + `internal/core/apperror` | 传输层抗 SSRF 与抗 DNS Rebinding `EgressGuard`（`Unmap()` 防 `::ffff:` 绕过、`DialContext` 一次解析直连字面量 IP + `net.Dialer.Control` 纵深防御、6 跳 `CheckRedirect` + 跨源剥离 `Authorization`/`Cookie`） |
| `internal/infra/httpclient` | L0 | `sony/gobreaker/v2`、`prometheus`、`SA-01`、`egress` | 物理隔离双 Profile 出站 HTTP 客户端（`NewTrusted` 注入企业代理与自签 CA；`NewUntrustedEgress` 强制 `EgressGuard.DialContext` + `Proxy: nil` + `CheckRedirect` + 默认 `20 MiB` `io.LimitReader`）、SSE `Streaming: true`（`Timeout=0` + `IdleReadTimeout`）、指数退避+Full Jitter（`429 Retry-After`）、`(service, host)` 有界 LRU 熔断与脱敏观测 |
| `internal/core/tenant` | L0 | `internal/core/apperror`、`internal/infra/obs` | Fail-Closed 多租户 `context.Context`（`WithCompany`/`Require`/`Optional`/`Detach` 剥离取消信号与 DB 事务句柄并保留可观测三元组）、12 保留词 `ValidateOrgSlug` 与 `TenantPaths`/`SafeJoin`（防 `..` 穿越与 `EvalSymlinks` 符号链接逃逸） |
| `internal/core/crypto` | L0 | 标准库 `crypto/aes`、`internal/core/tenant` | 与 Java `SecretCipher` 逐字节兼容的每租户 AES-256-GCM DEK 信封加密（`v1:` 平台密文 / `v2:<companyID>:` 租户密文 + AAD 强绑定）、`0600` 原子 `secret.key` 生成与零停机主密钥 KEK 轮换（`PreviousKeysB64`/`RotateMasterKey`/`RewrapCompanyDEK`/`ReencryptPlatformSecret`） |
| `internal/core/eventbus` | L0 | 标准库 `reflect`/`slog`、`internal/core/tenant` | 进程内编译期类型安全泛型事件总线（`Subscribe[T]`/`Publish[T]`/`PublishAsync[T]`）、单订阅者 `panic recover` 隔离、异步 `tenant.Detach(ctx)` 透传、有界并发度（默认 64）与优雅排空 `Drain(ctx)` |
| `internal/infra/memcache` | L0 | `golang.org/x/sync/singleflight`（**零 Redis**） | 纯进程内 32 分片 TTL/LRU 并发缓存 `ShardedCache[K,V]`（`GetOrLoad` 单飞防击穿、`DeleteFunc` 租户批量失效、`StartJanitor`）与 O(1) 加权滑动窗口防爆破限流器 `SlidingWindowLimiter`（`Peek`/`RecordFailure`/`Reset`、`MaxKeys=100_000` 防 OOM） |
| `internal/arch_test.go` | L0–L6 守卫 | 标准库 `go/parser` | 编译/测试期全仓 AST 与 `go.mod` 静态架构守卫（强制 6 条不可逾越的工程红线） |

---

## 配置加载优先级与环境变量覆盖表

加载优先级（高 → 低）：**CLI Flags (`--listen` / `--ui-dir`) > 环境变量 > YAML 配置文件 (`--config`) > 代码内置默认值 (`config.Default()`)**。

| 配置路径 | 环境变量（按优先级从高到低） | 默认值 | 说明 |
|---|---|---|---|
| `server.listen` | `SYNAPSE_LISTEN`, `SERVER_PORT` | `:18080` | `SERVER_PORT` 纯端口自动补 `:` |
| `server.shutdown_timeout` | `SYNAPSE_SHUTDOWN_TIMEOUT` | `30s` | 优雅停机总预算 |
| `server.ui_dir` | `SYNAPSE_UI_DIR` | `""` | 非空启用磁盘热加载，空则内嵌 `embed.FS` |
| `server.session_cookie_secure` | `SYNAPSE_COOKIE_SECURE` | `false` | HTTPS 部署建议 `true` |
| `server.trust_proxy_headers` | `SYNAPSE_TRUST_PROXY` | `true` | 信任企业反代 `X-Forwarded-*` |
| `app.base_url` | `SYNAPSE_BASE_URL`, `SUPERAGENT_BASE_URL` | `""` | OAuth 公网回调回退基址 |
| `storage.postgres.dsn` | `SYNAPSE_PG_DSN`, `DATABASE_URL` | `postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC` | PG17 连接串（强制 UTC） |
| `storage.postgres.max_conns` | `SYNAPSE_PG_MAX_CONNS` | `16` | `pgxpool` 最大连接数（`min_conns: 2`） |
| `storage.auto_migrate` | `SYNAPSE_AUTO_MIGRATE` | `true` | 启动前自动执行 `goose` 迁移 |
| `secret.key` | `SECRET_KEY`, `SUPERAGENT_SECRET_KEY` | `""` | 32 字节标准 Base64 主密钥 KEK |
| `secret.key_file` | `SYNAPSE_SECRET_KEY_FILE` | `./data/secret.key` | `secret.key` 为空时加载或 `0600` 生成 |
| `network.http_proxy` | `HTTP_PROXY`, `http_proxy` | `""` | 企业出站 HTTP 代理 |
| `network.https_proxy` | `HTTPS_PROXY`, `https_proxy` | `""` | 企业出站 HTTPS 代理 |
| `network.no_proxy` | `NO_PROXY`, `no_proxy` | `localhost,127.0.0.1,postgres` | 运行时始终保底包含 `127.0.0.1,localhost,::1` |
| `network.ca_bundle_file` | `SYNAPSE_CA_BUNDLE_FILE`, `NODE_EXTRA_CA_CERTS` | `""` | 企业自签根证书 PEM，同时注入 Go TLS 与 CLI 子进程 |
| `runtime.node_dir` | `SYNAPSE_NODE_DIR` | `./data/runtime/node` | 私有 Node.js 托管目录 |
| `runtime.npm_registry` | `NPM_CONFIG_REGISTRY` | `""` | 企业私有/国内 npm 镜像源 |
| `runtime.node_dist_mirror` | `SYNAPSE_NODE_DIST_MIRROR` | `https://nodejs.org/dist` | Node.js 下载镜像源 |
| `paths.*` | `SYNAPSE_DATA_DIR` | `./data/{workspaces,tasks,files,attachments,avatars,branding,skills}` | 统一重定位 7 个租户隔离数据目录 |
| `observability.log_level` | `SYNAPSE_LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `observability.log_format` | `SYNAPSE_LOG_FORMAT` | `text` | `text` / `json` |
| `observability.log_file` | `SYNAPSE_LOG_FILE` | `""` | 非空时以 `0640` 权限双写至文件 |
| `observability.prometheus_token` | `SYNAPSE_PROMETHEUS_TOKEN` | `""` | Prometheus 独立抓取 Bearer Token |
| `platform.super_admin_token` | `SYNAPSE_SUPER_ADMIN_TOKEN` | `""` | 预留商业平台控制面 Token |

---

## 常用构建与验证命令

```bash
# 格式化与静态检查
gofmt -l .
go vet ./...

# 编译与竞态/覆盖率测试（离线，PG 集成测试未设 DSN 时自动 Skip）
go build ./...
go test -race -cover ./internal/core/apperror/... ./internal/infra/config/... ./internal/infra/obs/... ./internal/infra/lifecycle/... ./internal/infra/db/...
go test -race ./...

# 面向本地 PostgreSQL 17 运行完整集成测试（使用独立临时 Schema，可并发）
# 在仓库根目录启动本地 PG17：POSTGRES_PORT=15432 docker compose -f docker-compose.dev.yml up -d --wait
TEST_POSTGRES_DSN="postgres://synapse:synapse@127.0.0.1:15432/synapse?sslmode=disable&TimeZone=UTC" \
  go test -race -cover ./internal/infra/db/...
```

### `AdvisoryLock` 使用规范（零 Redis 互斥锁）

- **严禁直接对 `*pgxpool.Pool` 调用会话级 `pg_try_advisory_lock` / `pg_advisory_unlock`**（解锁若落到其他连接将导致锁永久泄漏）；
- 所有会话级咨询锁统一通过 `db.Locker().TryWithLock(...)` 或 `db.Locker().Acquire(...)` + `lease.Release(ctx)` 执行，由 `internal/infra/db` 保证独占单条物理连接并在同连接上成对解锁归还；
- 事务内互斥（随 `COMMIT`/`ROLLBACK` 自动释放）统一通过 `db.TryLockTx(txCtx, d.Querier(txCtx), namespace, key)` 执行。

### 出站 HTTP 双 Profile 选用与降级职责铁律

- **严禁业务包自建裸 `http.Client`**，全部出站 HTTP/HTTPS/SSE 请求必须经 `internal/infra/httpclient` 构造；
- **外部用户输入 URL（`LinkReader`、用户 Webhook、外部自定义 HTTP/SSE MCP）一律使用 `httpclient.NewUntrustedEgress`**，严禁混用 `NewTrusted`；仅飞书/钉钉 API、LLM 端点、`models.dev`、Node.js 镜像源等可信上游使用 `httpclient.NewTrusted`；
- **业务降级归业务调用方**：`internal/infra/httpclient` 只返回结构化 `*ClientError`（通过 `httpclient.KindOf(err)` 分类），严禁在 HTTP 客户端层内置切备用 Key、回落匿名抓取或返回过期缓存等业务降级策略。

---

### L0–L6 静态架构守卫规则（`internal/arch_test.go`）

每次执行 `go test ./...` 时，`internal/arch_test.go` 会通过 `go/parser` 静态扫描全仓非测试 Go 源码与 `go.mod`，强制拦截以下 **6 条架构红线**：

1. **Rule 1（单向分层）**：`L6(cmd) → L5(web/ui) → L4(orchestrator/event) → L3(agent/task/cost/docs) → L2(platform/files/project/skills) → L1(storage/runtime) → L0(core/infra)`，严禁低层引用高层（`targetLayer > srcLayer`）。
2. **Rule 2（L0 核心纯度与子 DAG）**：`internal/core/apperror` 零内部依赖；`internal/core/*` 严禁引入 `gin`/`pgx`/`goose`/`database/sql`/`os/exec`，且仅 `internal/core/tenant` 允许单向引用 `internal/infra/obs`。
3. **Rule 3（同层平行模块全面禁互引）**：凡 `L1`–`L5` 同层不同顶层模块间（如 `storage↔runtime`、`platform/files/project/skills`、`agent/task/cost/docs`、`event↔orchestrator`、`web↔ui`）一律禁止直接互相 import，须通过 L0 `internal/core/{spi,store,eventbus}` 倒置并在 L6 `wire.go` 装配。
4. **Rule 4（数据库驱动隔离）**：仅 `internal/infra/db`、`internal/storage/postgres`、`cmd/synapse-server` 允许引入 `pgx/v5` 或 `goose/v3`。
5. **Rule 5（子进程隔离）**：仅 `internal/runtime/launcher` 允许引入 `os/exec`。
6. **Rule 6（零 Redis 红线）**：全仓源码与 `go.mod` 严禁出现任何 Redis 客户端依赖。

---

### 主密钥 KEK 零停机轮换运维指引（`internal/core/crypto`）

租户业务凭证均由各租户独立 32 字节 DEK 加密（`v2:<companyID>:<b64>`），主密钥 KEK 仅用于封存 `company.data_key_cipher`（`v1:<b64>`）及少量平台级密文。轮换 KEK 时无需重加密海量租户业务表，按以下 4 步即可零停机完成：

1. **生成新主密钥**：使用 `crypto.GenerateKeyB64()`（或 `openssl rand -base64 32`）生成 32 字节标准 Base64 新密钥 `K_new`。
2. **双密钥滚动上线**：配置 `MasterKeyB64 = K_new` 并将原主密钥移入 `PreviousKeysB64 = [K_old]` 后重启（或调用进程内 `RotateMasterKey(K_new)`）。此时新写入与新公司 DEK 自动使用 `K_new`，历史 DEK 与 `v1:` 密文自动回退 `K_old` 无损解封。
3. **幂等重封租户 DEK 与平台密文**：遍历全部租户执行 `RewrapCompanyDEK(ctx, companyID)`（仅重封每家公司 1 行 `company.data_key_cipher`，已重封则幂等返回 `false, nil`），并对平台级 `v1:` 密文执行 `ReencryptPlatformSecret(ctx, stored)`。**租户业务表里的 `v2:<companyID>:` 密文一行无需改动**。
4. **下线旧密钥**：确认所有租户 DEK 与平台密文重封完成后，从 `PreviousKeysB64` 移除 `K_old` 并重启，完成轮换。
