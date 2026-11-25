# synapse-server

`synapse-server` 是 **Synapse-OS（SuperAgent Go 商业私有化 V1）** 的 Go 后端主工程（`module github.com/Karlsk/synapse-os`，Go `1.26.1`）。遵循严格 L0–L6 单向分层、零 Redis / 零 ClickHouse / 零重 ORM / 零反射 DI 与单静态二进制 + 单库 PostgreSQL 17 交付原则。

---

## L0 基础设施底座包职责（`SA-01`）

| 包路径 | 层级 | 依赖范围 | 核心职责 |
|---|---|---|---|
| `internal/core/apperror` | L0（叶子包） | **仅 Go 标准库** | 13 类统一领域错误码（`Code`）、HTTP 状态映射、`ErrEgressDenied` SSRF 哨兵、`FromError` 未知错误脱敏、`UnreadableLinkError` / `UnreadableMaterialsError` 复合资料错误 |
| `internal/infra/config` | L0 | `gopkg.in/yaml.v3`、`golang.org/x/net/http/httpproxy` | 22 段强类型配置树（零 Redis）、`KnownFields(true)` 严格 YAML 解析、多源覆盖加载器、全量聚合 fail-fast `Validate()`、企业代理（保底 `127.0.0.1,localhost,::1` `NO_PROXY`）与自签 PEM CA 证书池 / 子进程环境变量构建 |
| `internal/infra/obs` | L0 | `github.com/prometheus/client_golang` | `request_id`/`company_id`/`user_id` 上下文透传与 `CopyMeta`、`SanitizeRequestID` 防日志注入、`ContextHandler -> RedactingHandler -> Text/JSON` 脱敏 `slog`（支持 `0640` 文件双写）与 `RedactDSN`、独立隔离 Prometheus Registry（携带 `application` 标签及 6 项 `superagent_*` 业务指标 + HTTP 耗时直方图） |
| `internal/infra/lifecycle` | L0 | **仅 Go 标准库** | `BackgroundGroup`（协程生命周期托管、`panic recover` 记堆栈与回调、停机后拒绝新任务、超时列出卡住任务名）与 `ShutdownManager`（5 阶段升序 / 同阶段 LIFO 逆序停机、30s 默认总预算裁剪、`SIGINT`/`SIGTERM` 信号监听） |

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

# 编译与竞态/覆盖率测试
go build ./...
go test -race -cover ./internal/core/apperror/... ./internal/infra/config/... ./internal/infra/obs/... ./internal/infra/lifecycle/...
go test -race ./...
```
