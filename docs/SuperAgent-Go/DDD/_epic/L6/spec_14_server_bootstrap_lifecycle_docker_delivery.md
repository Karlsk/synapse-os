# spec 14 | 服务端装配、生命周期治理与商业私有化交付 — L6 进程入口与交付工程规格

- **状态**：`draft`
- **篇号顺序**：14（L6 进程总装配与商业交付层；全仓最终集成篇）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-14`
- **上位契约**：
  - `docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §1（工程目录）、§2（`bootstrap-org` CLI）、§5（商业私有化代理/镜像源/CA 配置、Prometheus 指标与 30s 优雅停机）
  - `docs/SuperAgent-Go/02_PostgreSQL存储层与Store接口设计.md` §4（`goose` 迁移与定期表维护）
  - `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md` §2（双形态商业交付：独立二进制发行包 + Docker Compose）
- **前置依赖**：`spec_01`–`spec_13`（全部 L0–L5 模块与接口；M0 阶段先落地最小可启动闭环 `migrate`/`bootstrap-org`/`serve`，M1–M4 在 `wire.go` 逐里程碑接入对应服务与后台循环，M5 完成全量打包与交付验收）
- **下游消费者**：无（本篇为全系统 L6 顶层交付与生产部署入口）

---

## 1. 背景与范围

本篇定义 `synapse-server`（`module github.com/Karlsk/synapse-os`，Go 1.26.1）的 **L6 进程入口、手动依赖装配、生命周期治理、可观测性端点、全仓架构守卫以及商业私有化双形态交付物（Docker + 独立二进制发行包）**，完整等价重构并商业强化原版 Java 模块 `superagent-server` 与 `docker/`：

1. **单二进制多子命令 CLI (`cmd/synapse-server`)**：
   - 取代原版依赖 JVM + Spring Boot 自动扫描的 `SuperAgentServerApplication.java:13-20`；
   - 提供 `serve`（默认）、`migrate`、`bootstrap-org --id --name [--admin-user]`、`version` 四个确定性子命令，使商业私有化开户彻底脱离公网自助注册；
   - 完整支持企业联网私有化配置：YAML + 环境变量叠加、`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`、`network.ca_bundle_file`（同时注入 Go TLS `x509.CertPool` 与子进程 `NODE_EXTRA_CA_CERTS`）、`runtime.npm_registry` 与 `runtime.node_dist_mirror`。
2. **显式零反射依赖装配 (`wire.go`) 与分层 AST 守卫 (`internal/arch_test.go`)**：
   - 按严格分层顺序 **`L0 → L1 → L2 → L3 → L4 → L5 → L6`** 手动构造全部 ~35 个 PostgreSQL Store、事件总线订阅、Runner 插件、20 个 MCP ToolProvider、全部业务服务与 Gin 路由，任何缺参或循环引用在**编译期**立即暴露；
   - 通过 `internal/arch_test.go` 在每次 `go test ./...` 时用 `go/parser` 静态校验全仓包依赖 DAG（强制禁止逆层依赖及 `internal/agent` ↔ `internal/task` 互引）。
3. **确定性启停编排与可观测性 (`lifecycle.go`, `actuator.go`)**：
   - 将原版散落在 15+ 个类的 `@EventListener(ApplicationReadyEvent.class)`、`SmartLifecycle`、`@PreDestroy` 统一收口为确定性 7 步启动序与严格逆序 4 阶段 30 秒优雅停机（`SIGINT`/`SIGTERM`）；
   - 补齐原版仅有 `MetricNames.java` 空壳而无实际业务指标的缺口，在 `/actuator/prometheus` 正式暴露 6 项核心业务 Prometheus 指标 + Go 运行时与 HTTP 延迟直方图，在 `/actuator/health` 实测 `pgxpool.Ping` 与 `McpGrants.SelfStatus()`。
4. **商业私有化双形态交付 (`synapse-build/`)**：
   - **Docker 多阶段镜像 + Compose**：`golang:1.26-bookworm` 编译 → `node:24-bookworm-slim` 运行镜像，非 root 用户 `synapse` (`uid=10001`，满足 Claude Code 权限要求)、`tini` PID 1、可写 `NPM_CONFIG_PREFIX=/opt/cli`、预装 V1 GA 双 CLI（`@anthropic-ai/claude-code` & `@openai/codex`）、支持挂载企业自签 CA 与出站代理，标配 `postgres:17-alpine`；
   - **独立二进制发行包 (`synapse-os-<ver>-linux-<arch>.tar.gz`)**：单二进制内嵌静态 UI 与 `goose` SQL，配 `start.sh` / `stop.sh` / `common.sh` / `synapse.env`（35s 停机等待 > 30s 服务宽限）。

**本篇边界**：
- 不实现各领域具体业务逻辑与 SQL（分别属于 `spec_01`–`spec_12`）；
- 不定义 `/api/v1` 业务路由与 `/mcp` 协议（属于 `spec_13`，本篇仅通过 `web.Options.RegisterSystemRoutes` 挂载 `/actuator/*`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位 | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | CLI 入口分发（`serve` / `migrate` / `bootstrap-org` / `version`） | `synapse-server/cmd/synapse-server/main.go` | 新增 | `SuperAgentServerApplication.java:13-20` |
| 2 | 配置结构、YAML+Env 加载、企业代理与自定义 CA 全局 TLS 初始化 | `synapse-server/cmd/synapse-server/config.go` | 新增 | `application.yml:1-65`、`LogFormat.java:13-22` |
| 3 | L0→L6 显式手动依赖装配（35 Store + Bus + 20 MCP + Runner + IM + Web） | `synapse-server/cmd/synapse-server/wire.go` | 新增 | Spring `@ComponentScan(com.superagent)` 全仓 DI |
| 4 | 确定性 7 步启动序、8+3 后台循环管理与严格逆序 30s 优雅停机 | `synapse-server/cmd/synapse-server/lifecycle.go` | 新增 | `CompanyService.java:40-45`、`McpGrants.java:181-223`、全仓 `SmartLifecycle`/`@PreDestroy` |
| 5 | `/actuator/health`、`/actuator/prometheus`、`/actuator/info` 与 6 项业务指标注册 | `synapse-server/cmd/synapse-server/actuator.go` | 新增 | `MetricNames.java`、`MetricsConfiguration.java`、`CommonTagsMeterRegistryPostProcessor.java`、`application.yml:21-28` |
| 6 | 商业化组织初始化子命令 `bootstrap-org`（含 12 保留词与 14 位随机密码） | `synapse-server/cmd/synapse-server/bootstrap_org.go` | 新增 | `OrganizationService.java:32-119` |
| 7 | 完整注释版商业配置模板 | `synapse-server/configs/config.example.yaml` | 新增 | `application.yml:1-65` |
| 8 | 全仓 L0–L6 包依赖 AST 架构守卫测试 | `synapse-server/internal/arch_test.go` | 新增 | Go 工程防循环依赖新守卫 |
| 9 | 商业私有化多阶段 `Dockerfile`（Go 1.26 + Node 24 + `uid=10001` + `tini` + 双 CLI） | `synapse-build/docker/Dockerfile` | 新增 | `docker/Dockerfile:1-84` |
| 10 | 容器前台入口脚本（自签 CA 合并 `NODE_EXTRA_CA_CERTS` + 可选迁移 + `exec`） | `synapse-build/docker/entrypoint.sh` | 新增 | `docker/entrypoint.sh:1-12` |
| 11 | Docker Compose（`postgres:17-alpine` + `synapse-server`）与环境变量模板 | `synapse-build/docker/docker-compose.yml`、`synapse-build/docker/.env.example` | 新增 | `docker/docker-compose.yml:1-53`、`docker/.env.example:1-11` |
| 12 | 独立二进制发行包启停脚本与环境文件模板 | `synapse-build/dist/bin/common.sh`、`start.sh`、`stop.sh`、`synapse-build/dist/config/synapse.env` | 新增 | `superagent-common.sh:1-65`、`superagent-start.sh:1-80`、`superagent-stop.sh:1-8`、`superagent.env:1-22` |
| 13 | 跨平台 Release Tarball 构建脚本（`linux/amd64`, `linux/arm64`） | `synapse-build/scripts/package-tarball.sh` | 新增 | `superagent-server/src/assembly/release-tgz.xml:1-58` |
| 14 | 配置/CLI/启停/探针/指标全套单元测试 | `synapse-server/cmd/synapse-server/*_test.go` | 新增 | 新建 Go 单元测试套件 |

---

## 3. 数据库设计

本篇不新增业务表；L6 进程控制面对 PG17（Schema 定义见 `L1/spec_02`）承担以下 4 项数据库运行期职责：

| 数据库操作 | 触发点 | 涉及表 | 约束与行为 |
|---|---|---|---|
| **版本化 Schema 迁移** | `synapse-server migrate` 或 `serve` 启动且 `storage.auto_migrate: true` | `goose_db_version` + 全部 47 张业务/会话表 | 调用 `postgres.MigrateUp(ctx, pool)` 执行内嵌 `//go:embed migrations/*.sql`；永远向前演进，零删表 |
| **租户与首任管理员初始化** | `synapse-server bootstrap-org` | `company`、`app_user`、`connector`（经 `CompanyStarted`） | 先跑迁移或验库就绪；在同一事务或幂等校验下插入 `company(enabled=true)` 与 `app_user(roles=["ADMIN"], must_change_password=true)`，重复 slug 返回非零退出码 |
| **健康探针连通性检测** | `GET /actuator/health` | PG17 连接池 | 使用 `context.WithTimeout(ctx, 2*time.Second)` 执行 `pool.Ping(ctx)`；失败返 `503 Service Unavailable` |
| **过期会话表清理** | 后台维护循环 `SessionPruner`（每 1 小时） | `user_session` | `DELETE FROM user_session WHERE expires_at < NOW()`，防止持久化登录会话无限膨胀 |

---

## 4. 冻结契约

### 4.1 CLI 子命令与退出码契约

```text
synapse-server [serve] [--config <path>] [--listen <:18080>] [--ui-dir <dir>]
synapse-server migrate [--config <path>]
synapse-server bootstrap-org --id <slug> --name <displayName> [--admin-user <username>] [--config <path>]
synapse-server version
```

- **默认子命令**：无参数或直接传 `--config` 时默认执行 `serve`（兼容容器 `CMD` 与旧脚本参数习惯）。
- **退出码**：`0` = 正常退出/初始化成功；`1` = 运行期错误（配置错误、DB 不可达、租户已存在、迁移失败、停机超时强制退出）；`2` = CLI 参数语法非法。
- **`bootstrap-org` 校验与输出契约**（对齐 `OrganizationService.java:32-119`）：
  - `--id`：trim + 小写，严格匹配 `^[a-z][a-z0-9-]{1,30}[a-z0-9]$`（3–32 位），禁用 12 个保留词：`admin`, `api`, `www`, `root`, `system`, `login`, `mcp`, `static`, `support`, `help`, `default`, `platform`；
  - `--name`：trim 后 1–64 字符；
  - `--admin-user`：可选，默认等于 `--id`；
  - 密码生成：从 55 位无歧义字符集 `ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789` 使用 `crypto/rand` 生成 **14 位**随机密码，BCrypt 哈希后入库且设 `must_change_password = true`；
  - 控制台一次性清晰打印 `org`、`admin_user`、`initial_password`（提示首次登录强制修改），**严禁写入业务日志文件**。

### 4.2 配置文件与环境变量覆盖契约

配置优先级（高 → 低）：**CLI Flags > 环境变量 > YAML 配置文件 (`--config`) > 代码内置默认值**。

| YAML 路径 | 环境变量（兼容原版 + `SYNAPSE_*`） | 默认值 | 说明 |
|---|---|---|---|
| `server.listen` | `SYNAPSE_LISTEN` / `SERVER_PORT` | `:18080` | 监听地址（若设 `SERVER_PORT=18080` 自动拼 `:18080`） |
| `server.shutdown_timeout` | `SYNAPSE_SHUTDOWN_TIMEOUT` | `30s` | 优雅停机总宽限时间 |
| `server.ui_dir` | `SYNAPSE_UI_DIR` | `""` | 非空启用前端磁盘热加载，空则使用 `embed.FS` |
| `server.session_cookie_secure` | `SYNAPSE_COOKIE_SECURE` | `false` | HTTPS 部署建议 `true` |
| `server.trust_proxy_headers` | `SYNAPSE_TRUST_PROXY` | `true` | 信任企业反代 `X-Forwarded-Proto/Host` |
| `app.base_url` | `SYNAPSE_BASE_URL` / `SUPERAGENT_BASE_URL` | `""` | OAuth 公网回调回退基址 |
| `storage.postgres.dsn` | `SYNAPSE_PG_DSN` / `DATABASE_URL` | `postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC` | PG17 连接串（强制 UTC） |
| `storage.postgres.max_conns` | `SYNAPSE_PG_MAX_CONNS` | `16` | `pgxpool` 最大连接数（`min_conns: 2`） |
| `storage.auto_migrate` | `SYNAPSE_AUTO_MIGRATE` | `true` | `serve` 启动前自动运行 `goose.Up` |
| `secret.key` | `SECRET_KEY` / `SUPERAGENT_SECRET_KEY` | `""` | 32 字节 Base64 主密钥 KEK |
| `secret.key_file` | `SYNAPSE_SECRET_KEY_FILE` | `./data/secret.key` | `secret.key` 为空时自动加载或 `0600` 生成 |
| `network.http_proxy` | `HTTP_PROXY` / `http_proxy` | `""` | 出站 HTTP 代理，统一透传至 HTTP 客户端与 CLI/Node/npm 子进程 |
| `network.https_proxy` | `HTTPS_PROXY` / `https_proxy` | `""` | 出站 HTTPS 代理 |
| `network.no_proxy` | `NO_PROXY` / `no_proxy` | `localhost,127.0.0.1,postgres` | 不走代理白名单（始终追加 `127.0.0.1,localhost` 防 `/mcp` 自检走代理） |
| `network.ca_bundle_file` | `SYNAPSE_CA_BUNDLE_FILE` / `NODE_EXTRA_CA_CERTS` | `""` | 企业自签根证书 PEM 文件路径 |
| `runtime.node_dir` | `SYNAPSE_NODE_DIR` | `./data/runtime/node` | 私有 Node.js LTS 托管目录 |
| `runtime.npm_registry` | `NPM_CONFIG_REGISTRY` | `""` | 企业私有/国内 npm 镜像源（注入子进程） |
| `runtime.node_dist_mirror` | `SYNAPSE_NODE_DIST_MIRROR` | `https://nodejs.org/dist` | Node.js 下载镜像地址 |
| `paths.*` | `SYNAPSE_DATA_DIR`（派生默认值） | `./data/{workspaces,tasks,files,attachments,avatars,branding,skills}` | 7 大租户隔离数据目录根路径 |
| `quota.max_users` / `max_running_tasks` / `monthly_tokens` | `SUPERAGENT_QUOTA_*` | `0` / `0` / `0` | 全局默认租户配额（`0` = 不限） |
| `agent.*` / `task.*` / `automation.*` / `superagent.*` / `memory.*` / `provider.*` / `cost.*` / `mcp.*` | 对应原版 `SUPERAGENT_*` 覆盖 | 与 `application.yml` 及各域默认值 1:1 对齐 | 详见 §5.6 完整 `config.example.yaml` |
| `observability.log_level` / `log_format` / `log_file` | `SYNAPSE_LOG_LEVEL` / `SYNAPSE_LOG_FORMAT` / `SYNAPSE_LOG_FILE` | `info` / `text` / `""` | `slog` 级别、格式（`text`/`json`）与可选日志文件双写 |
| `observability.prometheus_token` | `SYNAPSE_PROMETHEUS_TOKEN` | `""` | 可选：允许外部 Prometheus 携带 `Bearer <token>` 免 Session 抓指标 |
| `platform.super_admin_token` | `SYNAPSE_SUPER_ADMIN_TOKEN` | `""` | 预留商业平台运维控制面 Token（V1 占位） |

### 4.3 Actuator 端点与 Prometheus 指标契约

| Method | Path | 鉴权策略 | 响应契约 |
|---|---|---|---|
| `GET` | `/actuator/health` | 公开（对齐 `application.yml:27-28` `when-authorized`） | 未登录：`200 {"status":"UP"}` 或 `503 {"status":"DOWN"}`；<br>已登录：附 `components: {db: {status, latencyMs}, mcp: {status, state, selfUrl, error}, warmPool: {size, max}}` |
| `GET` | `/actuator/prometheus` | 已登录会话 **OR** 匹配 `observability.prometheus_token`（`crypto/subtle.ConstantTimeCompare`） | 标准 Prometheus 文本暴露格式 (`text/plain; version=0.0.4`) |
| `GET` | `/actuator/info` | 已登录 | `200 {"app":"synapse-server","version":"...","commit":"...","buildTime":"...","goVersion":"go1.26.1"}` |

**6 项核心业务 Prometheus 指标（均固定附加公共常量标签 `application="synapse-server"`，对齐 `MetricNames.java:14-17`）**：

| 指标名 | 类型 | 标签（严格控基数，禁用 `user_id`/`session_id`） | 采集点 |
|---|---|---|---|
| `superagent_chat_turns_total` | `CounterVec` | `company_id`, `cli`, `outcome` (`done\|error\|stopped`) | `ChatService` 回合结束 |
| `superagent_task_runs_total` | `CounterVec` | `company_id`, `flow` (`coding\|general\|complex`), `status` (`done\|error\|stopped`) | `TaskService` 终态切换 |
| `superagent_task_queue_depth` | `GaugeVec` | `company_id`, `status` (`pending\|running\|waiting`) | `TaskScheduler` 每轮扫描更新 |
| `superagent_tokens_total` | `CounterVec` | `company_id`, `cli`, `model`, `kind` (`input\|cache_read\|cache_write\|output\|reasoning`) | `TokenUsageService` 记账钩子 |
| `superagent_cli_warm_pool_size` | `Gauge` | *(仅 `application`)* | `ClaudeWarmPool` 入池/出池/淘汰 |
| `superagent_im_connected` | `GaugeVec` | `company_id`, `channel` (`feishu\|dingtalk`) | `FeishuIMConnector` / `DingTalkIMConnector` 连接状态变更（`1` / `0`） |

*(同步注册标准 `collectors.NewGoCollector()`、`collectors.NewProcessCollector()` 与 Gin HTTP 请求耗时直方图 `http_server_requests_seconds` 按 `method`/`route`/`status` 分桶。)*

### 4.4 容器与独立发行包交付契约

1. **Docker 镜像安全与运行契约**：
   - 进程 UID/GID 固定为非 root **`10001:10001` (`synapse`)**（因 Claude Code 在 `uid=0` 时拒绝免确认权限标志，对齐 `docker/Dockerfile:32-35`）；
   - `NPM_CONFIG_PREFIX=/opt/cli` 属主 `synapse:synapse`，`PATH=/opt/cli/bin:$PATH`，预装 `@anthropic-ai/claude-code@latest` 与 `@openai/codex@latest`，且 Web 页面「在线升级 CLI」在容器内具备写入权限；
   - `ENTRYPOINT ["tini", "--", "/app/bin/docker-entrypoint.sh"]`，由 `tini` 回收 CLI 子进程衍生的僵尸进程；
   - 持久化挂载卷：`["/app/data", "/app/logs", "/home/synapse"]`；
   - 默认暴露端口 `18080`，`HEALTHCHECK` 探测 `http://127.0.0.1:${SERVER_PORT}/actuator/health`（每 30s，start-period 30s，Go 秒级冷启动显著快于原版 JVM 90s）。
2. **独立发行包启停契约**：
   - 解压目录结构：`bin/{synapse-server,start.sh,stop.sh,common.sh}`、`config/{config.yaml,synapse.env}`、`data/`、`logs/`；
   - `start.sh` 校验进程防重复（支持 `start.sh force` 先优雅停再启）、`setsid` 脱离终端会话并轮询 `/actuator/health` 直至 HTTP 200（默认超时 60s）；
   - `stop.sh` 发 `SIGTERM` 等待 `STOP_TIMEOUT=35s`（严格大于服务端 `30s` 优雅停机宽限）后再升级 `SIGKILL`。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 CLI 入口 (`main.go`) 与 `bootstrap-org` (`bootstrap_org.go`)

对应 Java：`SuperAgentServerApplication.java:13-20`、`OrganizationService.java:32-119`。

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
)

var (
	Version   = "dev"
	Commit    = "none"
	BuildTime = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	sub := "serve"
	if len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "serve":
		return runServe(args)
	case "migrate":
		return runMigrate(args)
	case "bootstrap-org":
		return runBootstrapOrg(args)
	case "version":
		fmt.Printf("synapse-server %s (commit=%s, built=%s)\n", Version, Commit, BuildTime)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q (expected: serve | migrate | bootstrap-org | version)\n", sub)
		return 2
	}
}
```

```go
// bootstrap_org.go：商业化租户与首任管理员初始化（替代原版公网 POST /api/v1/orgs，对齐 OrganizationService.java:32-119）
var (
	orgSlugRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	reservedOrg = map[string]struct{}{
		"admin": {}, "api": {}, "www": {}, "root": {}, "system": {}, "login": {},
		"mcp": {}, "static": {}, "support": {}, "help": {}, "default": {}, "platform": {},
	}
)

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789" // 55 unambiguous chars

func runBootstrapOrg(args []string) int {
	fs := flag.NewFlagSet("bootstrap-org", flag.ContinueOnError)
	cfgPath := fs.String("config", defaultConfigPath(), "path to config.yaml")
	orgID := fs.String("id", "", "organization English slug (3-32 lowercase alnum/hyphen)")
	orgName := fs.String("name", "", "organization display name (1-64 chars)")
	adminUser := fs.String("admin-user", "", "initial admin username (defaults to --id)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	slug := strings.ToLower(strings.TrimSpace(*orgID))
	name := strings.TrimSpace(*orgName)
	username := strings.TrimSpace(*adminUser)
	if username == "" {
		username = slug
	}

	if err := validateBootstrapArgs(slug, name, username); err != nil {
		fmt.Fprintf(os.Stderr, "validation error: %v\n", err)
		return 2
	}

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config failed: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := postgres.OpenPool(ctx, cfg.Storage.Postgres)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect postgres failed: %v\n", err)
		return 1
	}
	defer pool.Close()

	if cfg.Storage.AutoMigrate {
		if err := postgres.MigrateUp(ctx, pool); err != nil {
			fmt.Fprintf(os.Stderr, "migrate failed: %v\n", err)
			return 1
		}
	}

	rawPwd, err := generateRandomPassword(14)
	if err != nil {
		fmt.Fprintf(os.Stderr, "generate password failed: %v\n", err)
		return 1
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(rawPwd), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hash password failed: %v\n", err)
		return 1
	}

	// 仅装配初始化所需的最小依赖：CompanyStore + UserStore + SystemSettingStore + ConnectorService（播种飞书/钉钉连接器）
	app, err := WireBootstrap(cfg, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wire bootstrap failed: %v\n", err)
		return 1
	}
	if err := app.Provisioner.BootstrapOrg(ctx, slug, name, username, string(hash)); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap-org failed: %v\n", err)
		return 1
	}

	// 密码仅输出到控制台一次，不写入结构化日志
	fmt.Printf("Organization initialized successfully:\n")
	fmt.Printf("  org:              %s\n", slug)
	fmt.Printf("  name:             %s\n", name)
	fmt.Printf("  admin_user:       %s\n", username)
	fmt.Printf("  initial_password: %s\n", rawPwd)
	fmt.Printf("Note: Save this password now. First sign-in will enforce password change.\n")
	return 0
}

func generateRandomPassword(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[v.Int64()]
	}
	return string(out), nil
}
```

### 5.2 企业代理与自定义 CA 全局 TLS 初始化 (`config.go`)

```go
// ConfigureNetworkRuntime 统一处理企业代理与自签根证书：
// 1) 为进程内 Go HTTP 客户端注入自定义 RootCAs 与 Proxy 函数；
// 2) 返回供 LocalLauncher 注入所有 CLI / Node / npm 子进程的标准化环境变量映射。
func ConfigureNetworkRuntime(cfg NetworkConfig, rt RuntimeConfig) (*x509.CertPool, map[string]string, error) {
	// 确保 NO_PROXY 始终包含 127.0.0.1 与 localhost，避免内部 POST http://127.0.0.1:<port>/mcp 被企业代理劫持
	noProxy := ensureLocalNoProxy(cfg.NoProxy)
	childEnv := map[string]string{}
	if cfg.HTTPProxy != "" {
		childEnv["HTTP_PROXY"] = cfg.HTTPProxy
		childEnv["http_proxy"] = cfg.HTTPProxy
	}
	if cfg.HTTPSProxy != "" {
		childEnv["HTTPS_PROXY"] = cfg.HTTPSProxy
		childEnv["https_proxy"] = cfg.HTTPSProxy
	}
	if noProxy != "" {
		childEnv["NO_PROXY"] = noProxy
		childEnv["no_proxy"] = noProxy
	}
	if rt.NPMRegistry != "" {
		childEnv["NPM_CONFIG_REGISTRY"] = rt.NPMRegistry
	}

	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if cfg.CABundleFile != "" {
		pemBytes, err := os.ReadFile(cfg.CABundleFile)
		if err != nil {
			return nil, nil, fmt.Errorf("read network.ca_bundle_file %q: %w", cfg.CABundleFile, err)
		}
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, nil, fmt.Errorf("no valid PEM certificates in %q", cfg.CABundleFile)
		}
		// 透传给 Node.js CLI（claude/codex）与 curl/git 子进程
		childEnv["NODE_EXTRA_CA_CERTS"] = cfg.CABundleFile
		childEnv["SSL_CERT_FILE"] = cfg.CABundleFile
	}

	// 更新默认 http.Transport 的 TLS RootCAs 与代理解析
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		cloned := base.Clone()
		if cloned.TLSClientConfig == nil {
			cloned.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		cloned.TLSClientConfig.RootCAs = pool
		cloned.Proxy = proxyFuncFromConfig(cfg.HTTPProxy, cfg.HTTPSProxy, noProxy)
		http.DefaultTransport = cloned
	}
	return pool, childEnv, nil
}
```

### 5.3 L0→L6 显式手动依赖装配 (`wire.go`)

> **工程铁律**：零反射 DI、零 `init()` 隐式注册、严格按 **L0 → L1 → L2 → L3 → L4 → L5 → L6** 单向实例化；编译通过即证明全依赖图闭环无环。

```go
package main

// App 持有完整装配的运行时对象，交由 lifecycle.go 统一控制启动与逆序停机。
type App struct {
	Config      *Config
	Pool        *pgxpool.Pool
	Metrics     *obs.Metrics
	Router      *gin.Engine
	HTTPServer  *http.Server
	Companies   *tenantplat.CompanyService
	McpGrants   *mcp.Grants
	WarmPool    *runner.ClaudeWarmPool
	IMConns     []spi.IMConnector // [FeishuIMConnector, DingTalkIMConnector]
	CoreLoops   []BackgroundLoop  // 8 个核心业务后台循环
	MaintLoops  []BackgroundLoop  // 3 个维护/刷新后台循环
	CloseHooks  []NamedCloser     // ChatService / TaskService / ProviderService 等活跃执行中止钩子
}

func WireApp(ctx context.Context, cfg *Config, rootCtx context.Context) (*App, error) {
	// ======================== L0: 核心底座 ========================
	logger, closeLog, err := obs.NewLogger(cfg.Observability)
	if err != nil { return nil, err }
	slog.SetDefault(logger)

	metrics := obs.NewMetrics("synapse-server") // 注册 6 项业务指标 + Go/Process/HTTP 指标
	bus := eventbus.New(logger)
	certPool, childProxyEnv, err := ConfigureNetworkRuntime(cfg.Network, cfg.Runtime)
	if err != nil { return nil, err }
	egress := netguard.NewEgressGuard(netguard.Options{RootCAs: certPool, ProxyEnv: childProxyEnv})

	// ======================== L1: 存储与运行时 ========================
	pool, err := postgres.OpenPool(ctx, cfg.Storage.Postgres)
	if err != nil { return nil, err }
	if cfg.Storage.AutoMigrate {
		if err := postgres.MigrateUp(ctx, pool); err != nil {
			pool.Close()
			return nil, err
		}
	}
	txMgr := postgres.NewTxManager(pool)
	stores := postgres.NewAllStores(pool, txMgr) // 35 个 PG17 Store 实现 + SessionStore

	cipher, err := crypto.NewSecretCipher(cfg.Secret.Key, cfg.Secret.KeyFile, stores.Company)
	if err != nil { return nil, err }

	cliPaths := clipaths.New(cfg.Runtime.NodeDir)
	localLauncher := launcher.NewLocalLauncher(cliPaths, childProxyEnv)
	images := attachment.NewImageAttachmentStore(cfg.Paths.Attachments)
	mcpGrants := mcp.NewGrants(cfg.MCP.SelfURL, cfg.Server.Listen)
	mcpClient := mcp.NewClient(localLauncher, egress)

	// ======================== L2: 平台治理、文件、项目、技能 ========================
	companies := tenantplat.NewCompanyService(stores.Company, bus)
	quotas := tenantplat.NewQuotaService(stores.Company, stores.User, stores.Usage, cfg.Quota)
	settingsSvc := settings.NewSettingsService(stores.SystemSetting, cipher, bus)
	brandingSvc := settings.NewBrandingService(cfg.Paths.Branding)
	sessions := authplat.NewSessionStore(stores.Session)
	authenticator := authplat.NewLocalAuthenticator(companies, stores.User)
	accountSvc := userplat.NewAccountService(stores.User, cfg.Paths.Avatars)
	userSvc := userplat.NewUserService(stores.User, quotas, accountSvc, nil /* V2 OwnerTransferHook */)
	peopleDir := userplat.NewPeopleDirectory(stores.User)
	noticeSvc := notice.NewNoticeService(stores.Notice, bus)

	integrationSvc := integration.NewIntegrationService(stores.SystemSetting, cipher, bus) // V1: feishu, dingtalk
	feishuOAuth := connector.NewFeishuDocsOAuthClient(integrationSvc, stores.ConnectorCred, cipher, cfg.OAuth)
	connectorSvc := connector.NewConnectorService(stores.Connector, stores.ConnectorCred, integrationSvc, cipher, mcpGrants)
	connectorToolsSvc := connector.NewConnectorToolsService(connectorSvc, mcpClient)
	connectorOAuthSvc := connector.NewConnectorOAuthService(connectorSvc, feishuOAuth)

	nodeSvc := provider.NewNodeRuntimeService(cfg.Runtime, cliPaths, childProxyEnv, certPool)
	credSvc := provider.NewCredentialService(stores.ProviderCred, cipher, bus)
	modelGateway := gateway.NewDirectCredentialGateway(credSvc)
	providerSvc := provider.NewProviderService(stores.ModelProvider, credSvc, nodeSvc, cliPaths, localLauncher, cfg.Provider)
	providerChecker := provider.NewStatusChecker(companies, providerSvc, credSvc, cfg.Provider.Checker)
	analyticsSvc := analytics.NewAnalyticsService(stores.Task, stores.Agent, stores.Usage, stores.User)

	// 注册 V1 双 GA AgentRunner（Claude 含热进程池 + Codex）
	warmPool := runner.NewClaudeWarmPool(cfg.Agent.ClaudeWarm, metrics)
	claudeRunner := runner.NewClaudeRunner(localLauncher, modelGateway, mcpGrants, warmPool, cfg.Agent.Claude)
	codexRunner := runner.NewCodexRunner(localLauncher, modelGateway, mcpGrants, cfg.Agent.Codex)
	runnerReg := runner.NewRegistry(claudeRunner, codexRunner)

	projectSvc := project.NewProjectService(stores.Project, stores.ProjectItem, stores.User, stores.Agent) // 实现 spi.ProjectAccess
	fileStorageSvc := files.NewFileStorageService(stores.SystemSetting, stores.WorkFile, cipher,
		files.NewLocalProvider(cfg.Paths.Files), files.NewS3Provider(certPool))
	fileFolderSvc := files.NewFileFolderService(stores.FileFolder, stores.WorkFile)
	fileSvc := files.NewFileService(stores.WorkFile, fileStorageSvc, fileFolderSvc, projectSvc, cfg.Files)
	skillSvc := skills.NewSkillService(stores.Skill, stores.User, cfg.Paths.Skills)
	skillDelSvc := skills.NewSkillDeletionService(stores.SkillDeletion, skillSvc)

	// ======================== L3: 成本、文档、Agent、任务 ========================
	priceSvc := cost.NewModelPriceService(stores.SystemSetting, providerSvc, cfg.Cost)
	usageSvc := cost.NewTokenUsageService(stores.Usage, priceSvc, metrics)
	modelRouter := cost.NewTaskModelRouter(providerSvc, priceSvc, settingsSvc, runnerReg, usageSvc)

	feishuDocTools := docs.NewFeishuDocTools(integrationSvc, connectorSvc, feishuOAuth, mcpGrants)
	dingtalkDocTools := docs.NewDingTalkDocTools(integrationSvc, stores.EventSource, cfg.DingTalk, mcpGrants)
	linkReader := docs.NewLinkReaderService(feishuDocTools, egress) // V1 精简 2 级：飞书授权直读 + 通用 HTML/PDF

	agentLogSvc := agent.NewAgentLogService(stores.AgentLog, companies, cfg.AgentLog)
	agentSvc := agent.NewAgentService(stores.Agent, stores.AgentMember, stores.AgentParent, stores.Chat,
		stores.TurnTiming, projectSvc, skillSvc, settingsSvc, modelRouter, agentLogSvc)
	agentInviteSvc := agent.NewAgentInviteService(stores.AgentInvite, agentSvc)
	memorySvc := agent.NewMemoryService(stores.AgentMemory, stores.Chat, agentSvc, runnerReg, modelRouter, usageSvc, cfg.Memory)
	memoryLoop := agent.NewMemoryLoop(companies, memorySvc, cfg.Memory)
	fileTagger := agent.NewFileTagger(companies, fileSvc, fileFolderSvc, runnerReg, modelRouter, usageSvc, cfg.Files)
	chatSvc := agent.NewChatService(stores.Chat, stores.TurnTiming, agentSvc, fileSvc, skillSvc, images,
		connectorSvc, runnerReg, modelRouter, usageSvc, quotas, mcpGrants, agentLogSvc, memorySvc, metrics, cfg.Chat, rootCtx)

	taskPool := task.NewSessionPool(cfg.Paths.Tasks)
	taskSvc := task.NewTaskService(stores.Task, stores.TaskPhase, stores.TaskRunState, stores.TaskAgent,
		stores.TaskLink, stores.TaskTag, stores.TaskModelChoice, stores.TurnTiming,
		agentSvc, projectSvc, fileSvc, skillSvc, images, connectorSvc, linkReader,
		runnerReg, modelRouter, usageSvc, quotas, mcpGrants, taskPool, bus, metrics, cfg.Task, rootCtx)
	taskScheduler := task.NewScheduler(companies, taskSvc, quotas, metrics, cfg.Task.Scheduler)
	taskWatchdog := task.NewWatchdog(companies, taskSvc, cfg.Task.Watchdog)
	taskTagger := task.NewTaskTagger(stores.TaskTag, runnerReg, modelRouter, usageSvc)
	autoSvc := task.NewAutomationService(stores.Automation, taskSvc, companies, cfg.Automation)
	autoScheduler := task.NewAutomationScheduler(companies, autoSvc, cfg.Automation)

	// ======================== L4: IM 收件箱与个人 SuperAgent ========================
	feishuIM := event.NewFeishuIMConnector(integrationSvc, stores.EventSource, stores.User, metrics)
	dingtalkIM := event.NewDingTalkIMConnector(integrationSvc, stores.EventSource, stores.User, metrics)
	imConns := []spi.IMConnector{feishuIM, dingtalkIM}
	inboxSvc := event.NewInboxService(companies, stores.InboxEvent, stores.InboxTodo, stores.EventSource,
		stores.Org, imConns, bus, cfg.Inbox)
	userInboxSvc := event.NewUserInboxService(stores.InboxEvent, stores.InboxTodo, stores.EventSource, taskSvc, bus)

	superAgentSvc := orchestrator.NewSuperAgentService(stores.SuperAgentState, stores.SuperAgentWork,
		agentSvc, chatSvc, taskSvc, autoSvc, userInboxSvc, runnerReg, modelRouter, usageSvc, mcpGrants, bus, cfg.SuperAgent)
	superAgentLoop := orchestrator.NewSuperAgentLoop(companies, superAgentSvc, cfg.SuperAgent)

	// 延迟回接单向 SPI 回调（零循环依赖）：
	// - ChatService 注册 SuperAgentChat 定制器（AgentTurnCustomizer）
	// - InboxService 注册 InboxProcessor -> SuperAgentService
	chatSvc.RegisterTurnCustomizer(superAgentSvc.ChatCustomizer())
	inboxSvc.SetProcessor(superAgentSvc)

	// ======================== 领域事件订阅（全显式注册） ========================
	bus.SubscribeCompanyStarted(
		connectorSvc.OnCompanyStarted,     // 播种 feishu-docs & dingtalk-tools
		taskSvc.OnCompanyStarted,          // 恢复任务工作区/会话映射
		superAgentSvc.OnCompanyStarted,    // 对齐 SuperAgent 状态
		noticeSvc.OnCompanyStarted,        // 对齐未读通知
		feishuIM.OnCompanyStarted,         // 打开飞书出站 WebSocket
		dingtalkIM.OnCompanyStarted,       // 打开钉钉出站 Stream
	)
	bus.SubscribeIntegrationChanged(feishuIM.OnIntegrationChanged, dingtalkIM.OnIntegrationChanged)
	bus.SubscribeInboxNotifyRequested(inboxSvc.OnNotifyRequested)
	bus.SubscribeNoticeEvents(
		noticeSvc.OnNoticeEvent,
		agent.NewChatTaskResults(chatSvc, stores.Chat).OnTaskStatusChanged,
		userInboxSvc.OnNoticeEvent,
		taskTagger.OnTaskStatusChanged,
	)

	// ======================== 20 个内置 MCP ToolProvider 注册 ========================
	mcpProviders := []mcp.ToolProvider{
		agent.NewCollabMcpTools(agentInviteSvc),
		agent.NewCreateMcpTools(agentSvc),
		agent.NewUpdateMcpTools(agentSvc),
		agent.NewSkillMcpTools(agentSvc, skillSvc),
		agent.NewChatHistoryMcpTools(stores.Chat, agentSvc),
		task.NewTaskMcpTools(taskSvc),
		task.NewComplexTaskMcpTools(taskSvc),
		task.NewAutomationMcpTools(autoSvc),
		files.NewFileMcpTools(fileSvc, fileFolderSvc, mcpGrants),
		files.NewFileFolderMcpTools(fileSvc, fileFolderSvc),
		files.NewFileManageMcpTools(fileSvc),
		skills.NewSkillMcpTools(skillSvc, skillDelSvc, mcpGrants),
		feishuDocTools,
		dingtalkDocTools,
		docs.NewReadLinkTools(linkReader),
		event.NewInboxMcpTools(userInboxSvc, inboxSvc),
		orchestrator.NewSuperAgentTools(superAgentSvc),
		orchestrator.NewSuperAgentTaskTools(superAgentSvc),
		orchestrator.NewSuperAgentAutomationTools(superAgentSvc),
		orchestrator.NewSuperAgentCollabTools(superAgentSvc),
	}

	// ======================== L5: Gin 网关 + 静态 UI + Actuator 挂载 ========================
	uiFS, err := ui.NewFS(cfg.Server.UIDir)
	if err != nil { return nil, err }
	actuator := NewActuator(pool, mcpGrants, warmPool, metrics, cfg.Observability)

	router, err := web.NewRouter(web.Dependencies{
		Authenticator: authenticator, Sessions: sessions, Accounts: accountSvc, Users: userSvc,
		Directory: peopleDir, OrgStore: stores.Org, Settings: settingsSvc, Branding: brandingSvc,
		Integrations: integrationSvc, Connectors: connectorSvc, ConnectorTools: connectorToolsSvc,
		ConnectorOAuth: connectorOAuthSvc, Providers: providerSvc, NodeRuntime: nodeSvc,
		Credentials: credSvc, ModelPrices: priceSvc, Files: fileSvc, FileFolders: fileFolderSvc,
		FileStorage: fileStorageSvc, Projects: projectSvc, Skills: skillSvc, SkillDeletions: skillDelSvc,
		Agents: agentSvc, AgentInvites: agentInviteSvc, AgentLogs: agentLogSvc, Chat: chatSvc,
		Images: images, Tasks: taskSvc, Automations: autoSvc, Inbox: userInboxSvc, Notices: noticeSvc,
		Usage: usageSvc, Analytics: analyticsSvc, SuperAgent: superAgentSvc,
		McpGrants: mcpGrants, McpProviders: mcpProviders, DetachBaseCtx: rootCtx,
	}, web.Options{
		Version:              Version,
		PublicBaseURL:        cfg.App.BaseURL,
		MaxUploadBytes:       cfg.Files.UploadMaxBytes,
		SSETimerTimeout:      cfg.Chat.SSETimeout,
		SessionCookieName:    cfg.Server.SessionCookieName,
		SessionCookieSecure:  cfg.Server.SessionCookieSecure,
		SessionTTL:           cfg.Server.SessionTTL,
		TrustProxyHeaders:    cfg.Server.TrustProxyHeaders,
		UIStaticFS:           uiFS,
		RegisterSystemRoutes: actuator.RegisterRoutes,
	})
	if err != nil { return nil, err }

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// 注意：WriteTimeout 保持 0（或由 SSE Handler 经 ResponseController 清零），避免截断 10 分钟 SSE 与大文件流
	}

	// 8 个核心业务后台循环（与 01 号架构文档 §5.2 一一对应）+ 3 个维护循环
	coreLoops := []BackgroundLoop{
		taskScheduler,   // 1. TaskScheduler (wakeCh + 30s)
		taskWatchdog,    // 2. TaskWatchdog (startup recover + 30s/120s)
		autoScheduler,   // 3. AutomationScheduler (30s)
		superAgentLoop,  // 4. SuperAgentLoop (3s)
		memoryLoop,      // 5. AgentMemoryLoop (5m)
		fileTagger,      // 6. FileTagger (90s delay + 60s)
		providerChecker, // 7. ProviderStatusChecker (3s/1h/1h/10m)
		inboxSvc,        // 8. InboxTicker (60s P0/P1/P2/30d prune)
	}
	maintLoops := []BackgroundLoop{
		priceSvc,                                   // models.dev 价格同步 (600s check / 24h refresh)
		agentLogSvc,                                // agent_log 清理 (1h)
		authplat.NewSessionPruner(sessions, time.Hour), // user_session 过期清理 (1h)
	}

	return &App{
		Config: cfg, Pool: pool, Metrics: metrics, Router: router, HTTPServer: httpSrv,
		Companies: companies, McpGrants: mcpGrants, WarmPool: warmPool, IMConns: imConns,
		CoreLoops: coreLoops, MaintLoops: maintLoops,
		CloseHooks: []NamedCloser{
			{Name: "chat", Fn: chatSvc.Close},
			{Name: "task", Fn: taskSvc.Close},
			{Name: "superagent", Fn: superAgentSvc.Close},
			{Name: "provider", Fn: providerSvc.Close},
			{Name: "logger", Fn: closeLog},
		},
	}, nil
}
```

### 5.4 确定性 7 步启动序与严格逆序 30 秒优雅停机 (`lifecycle.go`)

对应 Java：`CompanyService.java:40-45`、`McpGrants.java:181-223`、全仓 `SmartLifecycle`/`@PreDestroy`（`ChatService.java:1085`、`TaskService.java:2244`、`TaskWatchdog.java:137`、`AutomationScheduler.java:67`、`SuperAgentLoop.java:66`、`AgentMemoryLoop.java:65`、`FileTagger.java:312`、`ProviderStatusChecker.java:144`、`ClaudeWarmSessions.java:114-121`）。

```mermaid
flowchart LR
    subgraph Start["启动序列 (正序)"]
        S1["1. Config + Proxy/CA"] --> S2["2. PG17 Pool + goose Up"]
        S2 --> S3["3. SecretCipher + Wire L0..L5"]
        S3 --> S4["4. net.Listen 绑定端口<br/>更新 McpGrants.SelfURL"]
        S4 --> S5["5. HTTP Serve +<br/>CompanyService.StartCompanies()"]
        S5 --> S6["6. 异步 McpGrants.SelfCheck()"]
        S6 --> S7["7. 启动 8 核心 + 3 维护循环"]
    end
    subgraph Stop["优雅停机序列 (SIGINT/SIGTERM, 总预算 30s)"]
        D1["Phase 1 (≤15s)<br/>HTTP Shutdown 排空普通请求<br/>+ 取消 SSE 根 ctx"] --> D2["Phase 2 (≤8s)<br/>停止 8+3 后台循环<br/>loopCancel() + wg.Wait()"]
        D2 --> D3["Phase 3 (≤5s)<br/>关闭飞书/钉钉 WS +<br/>ClaudeWarmPool.CloseAll<br/>(SIGTERM->2s->SIGKILL)"]
        D3 --> D4["Phase 4 (≤2s)<br/>关闭 pgxpool 连接池<br/>+ Flush 日志"]
    end
```

```go
func runServe(args []string) int {
	// ... 解析 --config / --listen / --ui-dir ...
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()

	app, err := WireApp(rootCtx, cfg, rootCtx)
	if err != nil {
		slog.Error("wire app failed", "err", err)
		return 1
	}

	// Step 4: 先 bind TCP 端口以获取真实监听端口（支持 :0 随机端口测试与命令行覆盖，对齐 McpGrants.onWebServer）
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		slog.Error("listen failed", "addr", cfg.Server.Listen, "err", err)
		app.Pool.Close()
		return 1
	}
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok && tcpAddr.Port > 0 {
		app.McpGrants.SetListenPortIfUnconfigured(tcpAddr.Port)
	}

	// Step 5a: 后台开始提供 HTTP 服务（使 McpGrants.SelfCheck 与 IM 回调立即具备响应能力）
	serveErrCh := make(chan error, 1)
	go func() {
		slog.Info("synapse-server listening", "addr", ln.Addr().String(), "version", Version)
		if err := app.HTTPServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
	}()

	// Step 5b: 初始化全部启用租户（发布 CompanyStarted -> 播种连接器、恢复任务池、连飞书/钉钉 WS）
	if err := app.Companies.StartCompanies(rootCtx); err != nil {
		slog.Error("start companies failed", "err", err)
		gracefulShutdown(app, rootCancel, nil, nil)
		return 1
	}

	// Step 6: 异步自检 POST /mcp ping（对齐 McpGrants.java:192-223）
	app.McpGrants.SelfCheckAsync(rootCtx)

	// Step 7: 启动 8 个核心后台循环 + 3 个维护循环
	loopCtx, loopCancel := context.WithCancel(rootCtx)
	var loopWG sync.WaitGroup
	allLoops := append(append([]BackgroundLoop{}, app.CoreLoops...), app.MaintLoops...)
	for _, loop := range allLoops {
		l := loop
		loopWG.Add(1)
		go func() {
			defer loopWG.Done()
			slog.Info("background loop started", "loop", l.Name())
			l.Run(loopCtx)
			slog.Info("background loop stopped", "loop", l.Name())
		}()
	}

	// 等待退出信号或致命监听失败
	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig.String())
	case err := <-serveErrCh:
		slog.Error("http server fatal error", "err", err)
	}

	// 严格逆序 30 秒优雅停机
	gracefulShutdown(app, rootCancel, loopCancel, &loopWG)
	return 0
}

func gracefulShutdown(app *App, rootCancel, loopCancel context.CancelFunc, loopWG *sync.WaitGroup) {
	totalTimeout := app.Config.Server.ShutdownTimeout // 默认 30s
	deadline := time.Now().Add(totalTimeout)
	slog.Info("starting reverse-order graceful shutdown", "budget", totalTimeout)

	// Phase 1 (≤15s): 停止接收新 HTTP 连接并排空在途普通请求；同步通知 SSE 长连接随 rootCtx 退出
	httpCtx, httpCancel := context.WithTimeout(context.Background(), minDuration(15*time.Second, time.Until(deadline)))
	if err := app.HTTPServer.Shutdown(httpCtx); err != nil {
		slog.Warn("http server shutdown warning", "err", err)
		_ = app.HTTPServer.Close()
	}
	httpCancel()

	// Phase 2 (≤8s): 停止全部 8+3 后台调度循环并等待安全返回
	if loopCancel != nil {
		loopCancel()
	}
	rootCancel() // 同步取消所有基于 rootCtx 派生的后台回合/任务
	if loopWG != nil {
		waitDone := make(chan struct{})
		go func() { loopWG.Wait(); close(waitDone) }()
		select {
		case <-waitDone:
			slog.Info("all background loops exited cleanly")
		case <-time.After(minDuration(8*time.Second, time.Until(deadline))):
			slog.Warn("timed out waiting for background loops")
		}
	}

	// Phase 3 (≤5s): 优雅关闭飞书/钉钉 WebSocket 长连接、中止活跃运行钩子并清空 Claude 热进程池（SIGTERM -> 2s -> SIGKILL）
	for _, im := range app.IMConns {
		if err := im.Close(); err != nil {
			slog.Warn("close IM connector warning", "type", im.Type(), "err", err)
		}
	}
	for _, h := range app.CloseHooks {
		if h.Name == "logger" {
			continue // 日志最后刷盘
		}
		_ = h.Fn()
	}
	app.WarmPool.CloseAll(2 * time.Second)

	// Phase 4 (≤2s): 关闭 PostgreSQL 连接池并刷新日志
	app.Pool.Close()
	for _, h := range app.CloseHooks {
		if h.Name == "logger" {
			_ = h.Fn()
		}
	}
	slog.Info("synapse-server shutdown complete")
}
```

### 5.5 Actuator 与 Prometheus 指标端点 (`actuator.go`)

```go
package main

type Actuator struct {
	pool     *pgxpool.Pool
	grants   *mcp.Grants
	warmPool *runner.ClaudeWarmPool
	metrics  *obs.Metrics
	promTok  string
}

func (a *Actuator) RegisterRoutes(r *gin.Engine, _ gin.HandlerFunc) {
	r.GET("/actuator/health", a.Health)
	r.GET("/actuator/info", a.Info)
	r.GET("/actuator/prometheus", a.Prometheus)
}

func (a *Actuator) Health(c *gin.Context) {
	pingCtx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
	defer cancel()
	start := time.Now()
	dbErr := a.pool.Ping(pingCtx)
	latencyMs := time.Since(start).Milliseconds()

	status := "UP"
	httpCode := http.StatusOK
	if dbErr != nil {
		status = "DOWN"
		httpCode = http.StatusServiceUnavailable
	}

	// 未登录仅暴露聚合状态，已登录返回各组件详情（对齐 application.yml:27-28 when-authorized）
	if _, authed := web.PrincipalFromContext(c.Request.Context()); !authed {
		c.JSON(httpCode, gin.H{"status": status})
		return
	}

	dbComp := gin.H{"status": "UP", "latencyMs": latencyMs}
	if dbErr != nil {
		dbComp["status"] = "DOWN"
		dbComp["error"] = dbErr.Error()
	}
	mcpSt := a.grants.SelfStatus()
	c.JSON(httpCode, gin.H{
		"status": status,
		"components": gin.H{
			"db": dbComp,
			"mcp": gin.H{
				"status":  mcpSt.State,
				"selfUrl": a.grants.SelfURL(),
				"error":   mcpSt.Error,
			},
			"warmPool": gin.H{
				"size": a.warmPool.Size(),
				"max":  a.warmPool.Max(),
			},
		},
	})
}

func (a *Actuator) Prometheus(c *gin.Context) {
	// 允许已登录用户 或 携带配置好的 observability.prometheus_token 的采集器抓取
	if _, authed := web.PrincipalFromContext(c.Request.Context()); !authed {
		if !a.validPromToken(c.GetHeader("Authorization")) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "unauthorized", "message": "请先登录"})
			return
		}
	}
	promhttp.HandlerFor(a.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
}
```

### 5.6 商业私有化配置文件、Docker 与独立发行包交付清单

#### 5.6.1 `synapse-server/configs/config.example.yaml`

```yaml
server:
  listen: ":18080"                    # 可被 SERVER_PORT / SYNAPSE_LISTEN 覆盖
  shutdown_timeout: 30s
  ui_dir: ""                          # 开发期指向本地 static 目录实现热加载；生产留空使用内嵌资源
  session_cookie_name: "SYNAPSE_SESSION"
  session_cookie_secure: false        # 启用 HTTPS 反向代理时设为 true
  session_ttl: 168h                   # 7 天持久化登录会话
  trust_proxy_headers: true

app:
  base_url: ""                        # 例: https://synapse.corp.example（系统设置 publicUrl 优先）

storage:
  auto_migrate: true                  # 启动前自动执行内嵌 goose SQL 迁移
  postgres:
    dsn: "postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC"
    max_conns: 16
    min_conns: 2
    max_conn_lifetime: 30m

secret:
  key: ""                             # 推荐通过环境变量 SECRET_KEY 传入 32 字节 Base64
  key_file: "./data/secret.key"       # key 为空时自动加载或 0600 生成

network:
  http_proxy: ""                      # 例: http://proxy.corp.local:8080
  https_proxy: ""
  no_proxy: "localhost,127.0.0.1,postgres"
  ca_bundle_file: ""                  # 企业自签根证书 PEM，同时注入 Go TLS 与子进程 NODE_EXTRA_CA_CERTS

runtime:
  node_dir: "./data/runtime/node"
  npm_registry: ""                    # 企业私有 npm 源或国内镜像，例: https://registry.npmmirror.com
  node_dist_mirror: "https://nodejs.org/dist"

paths:
  workspaces: "./data/workspaces"
  tasks: "./data/tasks"
  files: "./data/files"
  attachments: "./data/attachments"
  avatars: "./data/avatars"
  branding: "./data/branding"
  skills: "./data/skills"

quota:                                # 每租户默认上限（0 = 不限；company 表字段优先）
  max_users: 0
  max_running_tasks: 0
  monthly_tokens: 0

agent:
  default_model: ""
  allowed_tools: "Read,Glob,Grep,LS,Bash"
  skip_permissions: false
  session_failure_limit: 10
  claude:
    command: "claude"
    timeout_seconds: 600
    warm_enabled: true
    warm_idle_minutes: 10
    warm_max: 20
  codex:
    command: "codex"
    timeout_seconds: 600

chat:
  sse_timeout: 10m

mcp:
  enabled_in_chat: true
  self_url: ""                        # 留空自动推导 http://127.0.0.1:<actualPort>/mcp

files:
  upload_max_mb: 50
  max_mb: 200
  auto_tag: true

task:
  allowed_tools: "Read,Glob,Grep,LS,Edit,Write,Bash"
  skip_permissions: false
  max_parallel: 50
  scheduler_sweep: 30s
  watchdog_enabled: true
  watchdog_interval: 30s
  watchdog_stall: 120s

automation:
  scheduler_enabled: true
  scheduler_interval: 30s

superagent:
  enabled: true
  tick_interval: 3s
  quiet_window: 10s
  max_wait: 60s
  team_limit: 6
  timezone: "Asia/Shanghai"

memory:
  enabled: true
  interval: 5m
  min_messages: 10
  settle_minutes: 2
  idle_minutes: 30
  agents_per_tick: 3
  carry_chars: 60000
  timezone: "Asia/Shanghai"

provider:
  checker_enabled: true
  local_interval: 3s
  latest_interval: 1h
  infer_interval: 1h
  credential_interval: 10m

cost:
  catalog_url: "https://models.dev/api.json"
  catalog_check_interval: 10m

inbox:
  retention_days: 30

agent_log:
  retention_days: 30
  max_per_agent: 5000

observability:
  log_level: "info"                   # debug | info | warn | error
  log_format: "text"                  # text | json
  log_file: ""                        # 非空时双写至指定文件
  prometheus_token: ""                # 可选：独立监控抓取 Token
```

#### 5.6.2 商业私有化多阶段 `synapse-build/docker/Dockerfile`（对齐并升级 `docker/Dockerfile:1-84`）

```dockerfile
# Stage 1: Go 1.26.1 静态编译单二进制（内嵌静态 UI 与 goose SQL 迁移）
ARG GO_IMAGE=golang:1.26-bookworm
ARG NODE_IMAGE=node:24-bookworm-slim

FROM --platform=$BUILDPLATFORM ${GO_IMAGE} AS builder
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=1.0.0
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
ARG GOPROXY=https://proxy.golang.org,direct

ENV GOPROXY=${GOPROXY} \
    CGO_ENABLED=0
WORKDIR /src/synapse-server
COPY synapse-server/go.mod synapse-server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY synapse-server/ .
RUN --mount=type=cache,target=/root/.cache/go-build \
    GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildTime=${BUILD_TIME}" \
    -o /out/synapse-server ./cmd/synapse-server

# Stage 2: Node 24 运行时镜像 + 非 root uid=10001 + tini + V1 GA 双 CLI
FROM ${NODE_IMAGE} AS runtime

ARG NPM_REGISTRY=
ARG CLI_REFRESH=unset
ARG HTTP_PROXY=
ARG HTTPS_PROXY=
ARG NO_PROXY=

ENV LANG=C.UTF-8 \
    NPM_CONFIG_PREFIX=/opt/cli \
    NPM_CONFIG_UPDATE_NOTIFIER=false \
    NPM_CONFIG_FUND=false \
    NPM_CONFIG_AUDIT=false \
    PATH=/opt/cli/bin:/app/bin:${PATH} \
    SERVER_PORT=18080

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates curl git ripgrep tini procps less \
 && rm -rf /var/lib/apt/lists/*

# Claude Code 禁止在 root 下启用免确认权限标志，固定使用非 root 用户 synapse (10001:10001)
RUN groupadd --gid 10001 synapse \
 && useradd --uid 10001 --gid synapse --create-home --home-dir /home/synapse --shell /bin/bash synapse \
 && mkdir -p /opt/cli /app/bin /app/config /app/data /app/logs /app/certs \
 && chown -R synapse:synapse /opt/cli /app

USER synapse

# 可写 /opt/cli 前缀：确保容器内 Web 页面「在线升级 CLI」具备写入权限；V1 预装 Claude Code + Codex 双 GA CLI
RUN echo "CLIs refresh=${CLI_REFRESH}" \
 && if [ -n "${NPM_REGISTRY}" ]; then npm config set registry "${NPM_REGISTRY}"; fi \
 && npm install -g \
      @anthropic-ai/claude-code@latest \
      @openai/codex@latest \
 && npm cache clean --force \
 && node --version && claude --version && codex --version

WORKDIR /app
COPY --from=builder --chown=synapse:synapse /out/synapse-server /app/bin/synapse-server
COPY --chown=synapse:synapse synapse-server/configs/config.example.yaml /app/config/config.yaml
COPY --chown=synapse:synapse --chmod=0755 synapse-build/docker/entrypoint.sh /app/bin/docker-entrypoint.sh

LABEL org.opencontainers.image.title="Synapse OS (SuperAgent Go)" \
      org.opencontainers.image.description="Commercial on-premises AI collaborative work platform"

VOLUME ["/app/data", "/app/logs", "/home/synapse"]
EXPOSE 18080

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:${SERVER_PORT}/actuator/health" > /dev/null || exit 1

ENTRYPOINT ["tini", "--", "/app/bin/docker-entrypoint.sh"]
CMD ["serve", "--config", "/app/config/config.yaml"]
```

#### 5.6.3 容器入口 `synapse-build/docker/entrypoint.sh`

```sh
#!/bin/sh
set -eu
cd /app

# 企业内网自定义 CA 支持：挂载 /app/certs/corp-ca.pem 或通过 SYNAPSE_CA_BUNDLE_FILE 指定
if [ -n "${SYNAPSE_CA_BUNDLE_FILE:-}" ] && [ -f "${SYNAPSE_CA_BUNDLE_FILE}" ]; then
  export NODE_EXTRA_CA_CERTS="${SYNAPSE_CA_BUNDLE_FILE}"
  export SSL_CERT_FILE="${SYNAPSE_CA_BUNDLE_FILE}"
elif [ -f "/app/certs/corp-ca.pem" ]; then
  export SYNAPSE_CA_BUNDLE_FILE="/app/certs/corp-ca.pem"
  export NODE_EXTRA_CA_CERTS="/app/certs/corp-ca.pem"
  export SSL_CERT_FILE="/app/certs/corp-ca.pem"
fi

# 支持通过 docker compose run synapse bootstrap-org ... 或默认 serve 启动
exec /app/bin/synapse-server "$@"
```

#### 5.6.4 `synapse-build/docker/docker-compose.yml`（标配 `postgres:17-alpine`）

```yaml
services:
  postgres:
    image: postgres:17-alpine
    restart: unless-stopped
    environment:
      POSTGRES_DB: synapse
      POSTGRES_USER: synapse
      POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}
      TZ: UTC
    volumes:
      - pg-data:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U synapse -d synapse"]
      interval: 5s
      timeout: 5s
      retries: 20

  synapse:
    image: ${SYNAPSE_IMAGE:-synapse-os:latest}
    restart: unless-stopped
    depends_on:
      postgres:
        condition: service_healthy
    ports:
      - "${SYNAPSE_PORT:-18080}:18080"
    environment:
      SERVER_PORT: "18080"
      SYNAPSE_PG_DSN: "postgres://synapse:${POSTGRES_PASSWORD}@postgres:5432/synapse?sslmode=disable&TimeZone=UTC"
      SECRET_KEY: ${SECRET_KEY:-}
      SYNAPSE_BASE_URL: ${SYNAPSE_BASE_URL:-}
      SYNAPSE_COOKIE_SECURE: ${SYNAPSE_COOKIE_SECURE:-false}
      SYNAPSE_LOG_FILE: "/app/logs/synapse.log"
      HTTP_PROXY: ${HTTP_PROXY:-}
      HTTPS_PROXY: ${HTTPS_PROXY:-}
      NO_PROXY: "localhost,127.0.0.1,postgres"
      NPM_CONFIG_REGISTRY: ${NPM_CONFIG_REGISTRY:-}
      SYNAPSE_NODE_DIST_MIRROR: ${SYNAPSE_NODE_DIST_MIRROR:-https://nodejs.org/dist}
    extra_hosts:
      - "host.docker.internal:host-gateway"
    volumes:
      - synapse-data:/app/data
      - synapse-logs:/app/logs
      - synapse-home:/home/synapse
      # 企业自签根证书按需只读挂载：
      # - ./corp-ca.pem:/app/certs/corp-ca.pem:ro

volumes:
  pg-data:
  synapse-data:
  synapse-logs:
  synapse-home:
```

#### 5.6.5 独立发行包脚本要点（对齐 `superagent-common.sh` / `start.sh` / `stop.sh`）

- **`synapse-build/dist/bin/common.sh`**：定位 `APP_HOME`、`APP_BIN="$APP_HOME/bin/synapse-server"`、`PID_FILE="$APP_HOME/logs/synapse.pid"`、`CONSOLE_LOG="$APP_HOME/logs/console.out"`，切换 `cd "$APP_HOME"`，以 `set -a; . "$APP_HOME/config/synapse.env"; set +a` 加载部署环境变量，默认 `SERVER_PORT=18080`、`STOP_TIMEOUT=35`（严格大于 30s 优雅停机时间）；`running_pid()` 二次校验 `ps -p "$pid" -o command=` 匹配 `$APP_BIN` 防 PID 复用误杀。
- **`synapse-build/dist/bin/start.sh`**：支持 `[force]` 重启参数，免 Java 检查直接通过 `setsid "$APP_BIN" serve --config "$APP_HOME/config/config.yaml"` 后台启动并轮询 `http://127.0.0.1:$SERVER_PORT/actuator/health` 直至 HTTP 200（`START_TIMEOUT=60`）。
- **`synapse-build/dist/bin/stop.sh`**：调用 `stop_server` 发 `SIGTERM` 等待最多 `35s` 后才升 `SIGKILL` 并清理 `$PID_FILE`。

### 5.7 全仓分层包依赖 AST 守卫 (`internal/arch_test.go`)

```go
package internal_test

// TestLayerDependencies 使用 go/parser 扫描 internal/ 下全部非测试 .go 文件的 import 路径，强制保证：
// 1. 严格分层 DAG：L0(core) < L1(storage,runtime) < L2(platform,files,project,skills)
//                  < L3(agent,task,cost,docs) < L4(event,orchestrator) < L5(web,ui)
// 2. 禁止任何包逆层引用（如 L1->L2、L2->L3）
// 3. 禁止同层核心业务域互引（尤其 internal/agent 严禁导入 internal/task，反之亦然；
//    internal/web 与 internal/ui 严禁导入 internal/storage/postgres 或 github.com/jackc/pgx）
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1（配置加载与企业代理/CA 单测与实现）**：编写 `cmd/synapse-server/config_test.go`（YAML 默认值、环境变量覆盖优先级、自签 PEM 加载失败报错、`NO_PROXY` 自动保底 `127.0.0.1,localhost`），实现 `cmd/synapse-server/config.go` 与 `configs/config.example.yaml`。
- [ ] **T2（`bootstrap-org` CLI 单测与实现）**：编写 `cmd/synapse-server/bootstrap_org_test.go`（slug 正则、12 保留词拦截、14 位无歧义密码熵与 BCrypt 校验、重复租户退出码 1、成功设 `must_change_password=true`），实现 `cmd/synapse-server/bootstrap_org.go`。
- [ ] **T3（Actuator 与 6 项业务 Prometheus 指标单测与实现）**：编写 `cmd/synapse-server/actuator_test.go`（未登录 `/actuator/health` 仅返 `{"status":"UP"}`、DB 断开返 503、已登录含 `components`、`/actuator/prometheus` 鉴权与 `prometheus_token` 常量时间校验、6 项 `superagent_*` 指标抓取断言），实现 `cmd/synapse-server/actuator.go`。
- [ ] **T4（显式 `wire.go` 与全仓 `internal/arch_test.go`）**：编写 `internal/arch_test.go`（验证 L0–L6 分层红线、`agent ⊥ task`、`web ⊥ pgx`），实现 `cmd/synapse-server/wire.go`。
- [ ] **T5（7 步启动序与 4 阶段逆序 30s 优雅停机）**：编写 `cmd/synapse-server/lifecycle_test.go`（使用 `:0` 随机端口验证 `McpGrants.SetListenPortIfUnconfigured` + `SelfCheck`、验证发送 `SIGTERM` 后按严格顺序完成 `HTTP Shutdown -> Loops Stop -> IM & WarmPool Close -> PG Pool Close`），实现 `cmd/synapse-server/lifecycle.go` 与 `main.go`。
- [ ] **T6（Docker 多阶段构建与 Compose 交付）**：编写 `synapse-build/docker/Dockerfile`、`entrypoint.sh`、`docker-compose.yml`、`.env.example`；本地执行 `docker build` 验证非 root `uid=10001`、`claude --version`、`codex --version`、`/opt/cli` 可写与 `tini` 进程树。
- [ ] **T7（独立二进制发行包脚本与打包验证）**：编写 `synapse-build/dist/bin/{common.sh,start.sh,stop.sh}`、`config/synapse.env`、`scripts/package-tarball.sh`；运行 `shellcheck` 并实测解压 → `migrate` → `bootstrap-org` → `start.sh` → `stop.sh` 全流程。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：合入后将 `SA-14` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md`：核对部署命令、环境变量名、挂载卷与 M5 交付验收清单一致；
- [ ] 新增商业私有化运维部署手册（`synapse-build/README.md`），记录 `docker compose up` → `bootstrap-org` → 首登改密 → 自签 CA / 代理配置 → Prometheus 抓取配置全步骤。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | 配置多源覆盖与 `NO_PROXY` 保底 | `cmd/synapse-server/config_test.go` | Env 覆盖 YAML、CLI Flag 覆盖 Env；`SERVER_PORT` 兼容生效；`NO_PROXY` 必含 `127.0.0.1` 与 `localhost` |
| 2 | 自定义 CA PEM 加载与错误 fail-fast | `cmd/synapse-server/config_test.go` | 有效自签 PEM 加入 `CertPool` 并生成 `NODE_EXTRA_CA_CERTS`；损坏或不存在文件返回非 nil error 拒绝启动 |
| 3 | `bootstrap-org` 参数与保留词校验 | `cmd/synapse-server/bootstrap_org_test.go` | 12 保留词与非法 slug 返回退出码 2；生成密码长度 14 且全属 55 字符安全集；重复 slug 返回退出码 1 |
| 4 | `/actuator/health` 双态输出与 DB 故障降级 | `cmd/synapse-server/actuator_test.go` | 未登录仅 `{"status":"UP"}`；已登录包含 `components.{db,mcp,warmPool}`；PG ping 失败返 `503 {"status":"DOWN"}` |
| 5 | `/actuator/prometheus` 鉴权与 6 项业务指标存在性 | `cmd/synapse-server/actuator_test.go` | 未登录无 token 返 401；合法 `prometheus_token` 返 200 且响应文本含全部 6 项 `superagent_*` 指标与 `application="synapse-server"` 标签 |
| 6 | 启停顺序与 30s 逆序优雅停机 | `cmd/synapse-server/lifecycle_test.go` | 启动绑定 `:0` 自动更新 `McpGrants.SelfURL` 并 `ping` 自检 `ok`；`SIGTERM` 按严格 `HTTP -> 8+3 Loops -> IM/WarmPool -> PG Pool` 顺序记录退出序列 |
| 7 | 全仓分层 AST 依赖守卫 | `internal/arch_test.go` | 零跨层逆向导入；`internal/agent` 与 `internal/task` 零互引；`internal/web` 零导入 `pgx` |
| 8 | Shell 脚本静态检查与 Docker 构建冒烟 | `synapse-build/` CI 验证 | `shellcheck synapse-build/dist/bin/*.sh synapse-build/docker/entrypoint.sh` 零告警；容器内 `id -u == 10001`、`/opt/cli` 可写、`claude`/`codex` 可执行 |

---

## 9. 验收门

- [ ] `go build ./... && go test -race ./...` 全仓 100% 绿灯（含 `internal/arch_test.go` 与 `internal/storage/postgres/guard_test.go`）
- [ ] §2 交付物 14 项逐项存在，`cmd/synapse-server/wire.go` 零反射 DI 依赖
- [ ] §8 测试清单 8 类用例全部落地，核心启动/配置/探针包覆盖率 ≥ 80%
- [ ] 容器交付实测验收（15 分钟商业交付闭环）：
  1. `docker compose -f synapse-build/docker/docker-compose.yml up -d` 启动 `postgres:17-alpine` + `synapse`；
  2. `docker compose exec synapse synapse-server bootstrap-org --id acme --name "Acme 科技"` 成功打印一次性 14 位密码；
  3. 浏览器打开 `http://127.0.0.1:18080/login` 确认无注册入口 → 登录 → 强制首次改密 → 创建 Agent → 发起对话与任务 → `/actuator/prometheus` 抓到真实 `superagent_*` 业务计数；
  4. `docker compose stop -t 35 synapse` 观察日志确认严格逆序 4 阶段 0 报错停机、无僵尸 CLI 子进程残留。
- [ ] 独立二进制发行包实测：`synapse-build/scripts/package-tarball.sh` 产出 `tar.gz` → 解压 → `start.sh` 健康就绪 → `stop.sh` 优雅退出。

---

## 10. 可提交节点

- 建议拆分 3 个清晰提交或单 PR `SA-14` 合入：
  1. `feat(server): add CLI subcommands (serve/migrate/bootstrap-org), config loader, enterprise proxy/CA setup and actuator metrics (spec 14, PR SA-14)`
  2. `feat(server): add explicit L0-L6 manual wiring, 30s reverse-order lifecycle and AST layer arch_test (spec 14, PR SA-14)`
  3. `build(deploy): add commercial multi-stage Dockerfile, PG17 docker-compose and standalone tarball scripts (spec 14, PR SA-14)`
