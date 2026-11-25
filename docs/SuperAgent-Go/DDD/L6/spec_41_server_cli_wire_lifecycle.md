# spec 41 | 服务端 CLI 入口、全仓手动 Wire 装配与 30s 优雅生命周期 — L6 进程控制面规格

- **状态**：`draft`
- **篇号顺序**：41（L6 Server 装配、生命周期与商业交付层 · 第 1/2 篇）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-41`
- **预估工作量**：`1.5 人天`（单人可 Review）
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1（工程目录与分层）、§2（`bootstrap-org` CLI）、§5（Prometheus 指标、8+3 后台循环与 30s 优雅停机）
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §4（`goose` 迁移与 `user_session` 维护）
  - [_epic/L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md](../_epic/L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md)（归档模块总设对照）
- **前置依赖**：
  - [L0/spec_01](../L0/spec_01_config_obs_error_lifecycle.md)–[L0/spec_04](../L0/spec_04_tenant_crypto_bus_archguard.md)（`internal/infra/{config,obs,lifecycle,db,httpclient,memcache}`、`internal/core/{apperror,tenant,crypto,eventbus}`、`internal/arch_test.go`）
  - [L1/spec_05](../L1/spec_05_core_domain_platform_assets_contracts.md)–[L1/spec_14](../L1/spec_14_runtime_model_gateway_mcp_client.md)（38 个 Store 契约与 PG17 实现、Runtime Launcher / Claude+Codex Runner / ModelGateway / MCP Client）
  - [L2/spec_15](../L2/spec_15_platform_tenant_bootstrap_auth.md)–[L2/spec_23](../L2/spec_23_assets_skills_mcp.md)（平台治理、Provider/Node/CLI 安装、飞书/钉钉连接器、文件/项目/技能）
  - [L3/spec_24](../L3/spec_24_cost_catalog_pricing_usage.md)–[L3/spec_32](../L3/spec_32_task_automation_mcp.md)（成本与路由、飞书/钉钉文档读取、Agent/Chat/Memory、Task/Pool/Watchdog/Automation）
  - [L4/spec_33](../L4/spec_33_event_im_connector_feishu.md)–[L4/spec_37](../L4/spec_37_orchestrator_mcp_tools.md)（飞书/钉钉 IM 长连接、收件箱分诊、个人 SuperAgent 编排）
  - [L5/spec_38](../L5/spec_38_web_gateway_mcp_ui_embed.md)–[L5/spec_40](../L5/spec_40_web_api_agent_task_event_orchestrator.md)（Gin 网关、`/mcp`、静态 UI 嵌入与 177 业务端点）
- **下游消费者**：[L6/spec_42 (SA-42)](./spec_42_docker_compose_offline_delivery.md)（Docker 镜像与离线发行包直接打包并调用本篇产出的 `synapse-server` 单二进制与 `configs/config.example.yaml`）

---

## 1. 背景与范围

本篇定义 `synapse-server`（`module github.com/Karlsk/synapse-os`，Go 1.26.1）的 **L6 进程控制面与全仓顶层集成入口 (`cmd/synapse-server`)**，将原版 Java `superagent-server` 模块（Spring Boot 自动扫描 + 散落在 15+ 类的 `@EventListener`/`SmartLifecycle`/`@PreDestroy`）重构为**编译期确定、零反射 DI、零 Redis 依赖、严格逆序 30 秒优雅停机**的商业级单二进制服务：

1. **四合一 CLI 子命令 (`main.go` + `bootstrap_org.go`)**：
   - `serve`（默认）、`migrate`、`bootstrap-org --id --name [--admin-user]`、`version`；
   - `bootstrap-org` 替代原版公网自助注册接口 `POST /api/v1/orgs`，沿用 `OrganizationService.java` 的 12 保留词校验与 14 位无歧义随机密码生成逻辑，并在控制台一次性输出初始口令（绝不落盘至结构化日志），强制首登改密。
2. **显式零反射 L0→L6 手动装配 (`wire.go`)**：
   - 在单一 `WireApp` 函数中按 **`L0 → L1 → L2 → L3 → L4 → L5 → L6`** 严格单向顺序实例化：
     - **L0 基础设施**：`config`（YAML+Env+Proxy+CA）、`obs`（脱敏 `slog` + 独立 Prometheus Registry）、`lifecycle`（`BackgroundGroup` + `ShutdownManager`）、`db`（PG17 `pgxpool` + `TxManager` + `AdvisoryLock`）、`httpclient`（`TrustedClient` 含 `gobreaker` 熔断 + `UntrustedEgressClient` 防 SSRF）、`memcache`（纯内存 TTL 缓存，**零 Redis**）、`eventbus`、`SecretCipher`；
     - **L1–L5 全量业务对象**：**38 个 PostgreSQL Store**（对应 47 张业务与会话表）、**V1 双 GA Runner**（Claude 含热进程池 + Codex）、**20 个内置 MCP ToolProvider**、**2 个 IM 长连接器**（飞书 WebSocket + 钉钉 Stream）、**L5 `web.NewRouter`（177 个端点 + 内嵌前端 UI）**；
   - 回接单向 SPI 回调（`chatSvc.RegisterTurnCustomizer`、`inboxSvc.SetProcessor`）与全量领域事件订阅，**编译即验证全仓零循环依赖**（由 `SA-04` 交付的 `internal/arch_test.go` 在每次 `go test ./...` 持续守卫）。
3. **确定性 7 步启动序与 4 阶段 30 秒逆序优雅停机 (`lifecycle.go`)**：
   - 基于 `SA-01` 的 `lifecycle.BackgroundGroup` 与 `lifecycle.ShutdownManager`，统一托管 **8 个核心业务后台循环 + 3 个维护循环**；
   - 先 `net.Listen` 获取真实端口并回调 `McpGrants.SetListenPortIfUnconfigured`，再开启 HTTP 服务、发布 `CompanyStarted`、执行 `McpGrants.SelfCheckAsync`、最后启动后台循环；
   - 收到 `SIGINT`/`SIGTERM` 时严格按 **Phase 1（HTTP ≤15s + SSE 取消）→ Phase 2（8+3 循环 ≤8s）→ Phase 3（IM WS + 活跃运行钩子 + ClaudeWarmPool `SIGTERM→2s→SIGKILL` ≤5s）→ Phase 4（PG 连接池关闭 + 日志刷盘 ≤2s）** 逆序收敛。
4. **Actuator 探针与 Prometheus 业务指标挂载 (`actuator.go`)**：
   - 通过 `web.Options.RegisterSystemRoutes` 挂载 `/actuator/health`（对齐 `when-authorized` 双态视图）、`/actuator/prometheus`（会话或 `prometheus_token` 常量时间鉴权，暴露 6 项 `superagent_*` 核心业务指标 + Go/Process/HTTP 指标）、`/actuator/info`。

**本篇边界**：
- 不重复实现 `internal/infra/*` 基础组件（已由 `SA-01`–`SA-04` 交付）与 `internal/arch_test.go`（已由 `SA-04` 交付，本篇在 CI/验收门中强制执行）；
- 不定义 `/api/v1` 业务路由与 `/mcp` 协议（已由 `SA-38`–`SA-40` 交付）；
- 不包含 `Dockerfile`、`docker-compose.yml` 与 Tarball 启停脚本（归属 [L6/spec_42 (SA-42)](./spec_42_docker_compose_offline_delivery.md)）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位 | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | CLI 入口分发（`serve` / `migrate` / `bootstrap-org` / `version`）与退出码映射 | `synapse-server/cmd/synapse-server/main.go` | 新增 | `superagent-server/src/main/java/com/superagent/server/SuperAgentServerApplication.java:L13-L20` |
| 2 | 命令行 Flag 叠加与 L0 `config` / `NetworkRuntime` 启动期桥接 | `synapse-server/cmd/synapse-server/cli_flags.go` | 新增 | `superagent-server/src/main/resources/application.yml:L1-L65`、`superagent-core/src/main/java/com/superagent/core/logging/LogFormat.java:L13-L22` |
| 3 | L0→L6 显式手动依赖装配（38 Store + Bus + 20 MCP + 双 GA Runner + 双 IM + Web） | `synapse-server/cmd/synapse-server/wire.go` | 新增 | Spring `@ComponentScan(com.superagent)` 全仓 DI 装配 |
| 4 | 确定性 7 步启动序、8+3 后台循环托管与 4 阶段 30s 逆序优雅停机 | `synapse-server/cmd/synapse-server/lifecycle.go` | 新增 | `superagent-platform/src/main/java/com/superagent/platform/tenant/CompanyService.java:L40-L45`、`superagent-runtime/src/main/java/com/superagent/runtime/mcp/McpGrants.java:L181-L223`、全仓 `SmartLifecycle`/`@PreDestroy` |
| 5 | `/actuator/health`、`/actuator/prometheus`、`/actuator/info` 端点实现与路由挂载 | `synapse-server/cmd/synapse-server/actuator.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/metrics/{MetricNames,MetricsConfiguration,CommonTagsMeterRegistryPostProcessor}.java`、`superagent-server/src/main/resources/application.yml:L21-L28` |
| 6 | 商业化组织初始化子命令 `bootstrap-org`（12 保留词 + 14 位安全密码） | `synapse-server/cmd/synapse-server/bootstrap_org.go` | 新增 | `superagent-platform/src/main/java/com/superagent/platform/tenant/OrganizationService.java:L32-L119` |
| 7 | 完整注释版商业化配置模板（单库 PG17、零 Redis、企业代理/CA、全模块默认项） | `synapse-server/configs/config.example.yaml` | 新增 | `superagent-server/src/main/resources/application.yml:L1-L65` |
| 8 | CLI/Bootstrap/Actuator/Wire/Lifecycle 全套单测 | `synapse-server/cmd/synapse-server/{main,bootstrap_org,actuator,lifecycle}_test.go` | 新增 | 新建 Go 单元测试套件 |

---

## 3. 数据库设计

本篇不新增业务表；L6 控制面对 PG17（47 张业务/会话表定义与迁移脚本见 `SA-02`、`SA-07`–`SA-10`）承担以下 4 项运行期职责：

| 数据库操作 | 触发点 | 涉及表 / 机制 | 约束与行为 |
|---|---|---|---|
| **版本化向前迁移** | `synapse-server migrate` 或 `serve` 且 `storage.auto_migrate: true` | `goose_db_version` + 全部 47 张表 | 调用 `db.MigrateUp(ctx, pool)` 执行内嵌 `//go:embed migrations/*.sql`；永远向前演进，严禁 `DROP TABLE` |
| **租户与首任管理员初始化** | `synapse-server bootstrap-org` | `company`、`app_user`、`connector`（经 `CompanyStarted` 播种飞书/钉钉连接器） | 先执行迁移（若开启 `auto_migrate`）；单事务内幂等校验 `company` 不存在后写入 `company(enabled=true)` 与 `app_user(roles=["ADMIN"], must_change_password=true)`；重复 slug 返回退出码 `1` |
| **健康探针连通性检测** | `GET /actuator/health` | PG17 连接池 (`pgxpool.Pool`) | `context.WithTimeout(ctx, 2*time.Second)` 执行 `pool.Ping(ctx)`；异常立即返 `503 Service Unavailable` |
| **持久化登录会话清理** | 维护循环 `SessionPruner`（每 1 小时） | `user_session`（`SA-07`） | `DELETE FROM user_session WHERE expires_at < NOW()`，配合 `db.AdvisoryLock` 避免多实例重复扫描 |

> **零 Redis 约束**：进程启动与装配全链路严禁初始化任何 Redis 客户端或读取 Redis 配置项；进程内短时缓存全部使用 L0 `internal/infra/memcache`，跨实例互斥（如 `AutomationScheduler` 与 `SessionPruner`）统一使用 PG17 原生 `db.AdvisoryLock`。

---

## 4. 冻结契约

### 4.1 CLI 子命令、参数覆盖与退出码契约

```text
synapse-server [serve] [--config <path>] [--listen <:18080>] [--ui-dir <dir>]
synapse-server migrate [--config <path>]
synapse-server bootstrap-org --id <slug> --name <displayName> [--admin-user <username>] [--config <path>]
synapse-server version
```

- **默认子命令**：无参数或首参数以 `-` 开头时自动按 `serve` 分发，兼容容器 `CMD` 与运维脚本。
- **退出码契约**：
  - `0`：正常退出（含 `SIGINT`/`SIGTERM` 在 30s 预算内优雅收敛、迁移完成、组织初始化成功、`version` 输出）；
  - `1`：运行期错误（配置文件解析/校验失败、PG17 不可达、迁移失败、租户 slug 已存在、监听端口冲突、停机强退异常）；
  - `2`：CLI 参数语法错误、未知子命令或 `bootstrap-org` 参数格式/保留词校验失败。
- **配置优先级（高 → 低）**：`CLI Flags > 环境变量（SYNAPSE_* / SERVER_PORT / SUPERAGENT_* / SECRET_KEY） > YAML 文件 (--config) > L0 config.Default()`。

### 4.2 `bootstrap-org` 校验与安全输出契约

完整对齐 `OrganizationService.java:L32-L119` 与商业私有化去公网注册要求：

1. `--id`：`strings.ToLower(strings.TrimSpace(...))`，正则严格匹配 `^[a-z][a-z0-9-]{1,30}[a-z0-9]$`（3–32 位），拦截全部 **12 个保留词**：`admin`, `api`, `www`, `root`, `system`, `login`, `mcp`, `static`, `support`, `help`, `default`, `platform`；
2. `--name`：trim 后长度 `1..64`（按 UTF-8 rune 计数）；
3. `--admin-user`：可选，trim 后为空则默认等于 `--id`，长度 `2..64`；
4. **口令熵与落库**：使用 `crypto/rand` 从 **55 位无歧义字符集** `ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789`（排除 `0/O/I/l/1`）生成 **14 位**随机密码；以 `bcrypt.DefaultCost` 哈希入库，强制置 `must_change_password = true`；
5. **输出安全**：初始明文密码仅写入 `stdout` 一次性提示运维人员保存，**严禁调用 `slog` 打印密码或传入任何日志字段**。

### 4.3 Actuator 端点与 6 项业务 Prometheus 指标契约

| Method | Path | 鉴权策略 | 响应契约 |
|---|---|---|---|
| `GET` | `/actuator/health` | 公开（对齐 `application.yml:L27-L28` `show-details: when-authorized`） | 未登录：`200 {"status":"UP"}` 或 `503 {"status":"DOWN"}`；<br>已登录：附 `components: {db: {status, latencyMs, error?}, mcp: {status, selfUrl, error?}, warmPool: {size, max}}` |
| `GET` | `/actuator/prometheus` | 已登录 Session **OR** `Authorization: Bearer <token>` 精确匹配 `observability.prometheus_token`（`crypto/subtle.ConstantTimeCompare`） | `200 text/plain; version=0.0.4`；未授权返 `401 {"code":"unauthorized","message":"请先登录","data":null}` |
| `GET` | `/actuator/info` | 已登录 Session | `200 {"app":"synapse-server","version":"...","commit":"...","buildTime":"...","goVersion":"..."}` |

**6 项核心业务 Prometheus 指标（由 `SA-01` `internal/infra/obs` 注册于独立 Registry，统一带 `application="synapse-server"` 常量标签）**：

| 指标名 | 类型 | 标签（禁用 `user_id`/`session_id` 高基数字段） | 绑定模块 |
|---|---|---|---|
| `superagent_chat_turns_total` | `CounterVec` | `company_id`, `cli`, `outcome` (`done\|error\|stopped`) | `ChatService`（`SA-28`） |
| `superagent_task_runs_total` | `CounterVec` | `company_id`, `flow` (`coding\|general\|complex`), `status` (`done\|error\|stopped`) | `TaskService`（`SA-30`） |
| `superagent_task_queue_depth` | `GaugeVec` | `company_id`, `status` (`pending\|running\|waiting`) | `TaskScheduler`（`SA-30`） |
| `superagent_tokens_total` | `CounterVec` | `company_id`, `cli`, `model`, `kind` (`input\|cache_read\|cache_write\|output\|reasoning`) | `TokenUsageService`（`SA-24`） |
| `superagent_cli_warm_pool_size` | `Gauge` | *(仅 `application`)* | `ClaudeWarmPool`（`SA-12`） |
| `superagent_im_connected` | `GaugeVec` | `company_id`, `channel` (`feishu\|dingtalk`) | `FeishuIMConnector`（`SA-33`）/ `DingTalkIMConnector`（`SA-34`） |

*(同步暴露标准 Go/Process 采集器与 Gin HTTP 耗时直方图 `http_server_requests_seconds` 按 `method`/`route`/`status` 分桶。)*

### 4.4 7 步启动序与 4 阶段 30s 逆序优雅停机契约

| 阶段 | 预算上限 | 必须完成的动作（顺序不可颠倒） |
|---|:---:|---|
| **Startup S1–S3** | 启动阻塞 | 加载并 `Validate()` 配置 → 初始化 `obs.Logger`/`obs.Metrics` → 建 PG17 Pool 并按配置执行 `goose Up` → 完成 `WireApp`（L0→L5 全量构造） |
| **Startup S4** | 启动阻塞 | `net.Listen("tcp", cfg.Server.Listen)` 绑定端口 → 回调 `McpGrants.SetListenPortIfUnconfigured(actualPort)` |
| **Startup S5–S7** | 启动阻塞 / 异步 | 后台启动 `HTTPServer.Serve(ln)` → 同步执行 `Companies.StartCompanies(rootCtx)`（播种飞书/钉钉连接器、恢复任务池、开启 IM 长连接）→ 异步触发 `McpGrants.SelfCheckAsync(rootCtx)` → 通过 `BackgroundGroup` 拉起 **8 核心 + 3 维护循环** |
| **Shutdown Phase 1** | ≤ 15s | `HTTPServer.Shutdown(ctx)` 停止接受新连接并排空在途普通请求；同步 cancel `sseRootCtx` 使活跃 SSE 流主动收敛 |
| **Shutdown Phase 2** | ≤ 8s | 取消 `loopCtx` 与 `rootCtx`，通过 `BackgroundGroup.Stop(ctx)` 等待全部 **8+3 后台循环** 安全退出 |
| **Shutdown Phase 3** | ≤ 5s | 关闭飞书/钉钉 IM 长连接 → 执行 `ChatService`/`TaskService`/`SuperAgentService`/`ProviderService` 活跃执行中止钩子 → `ClaudeWarmPool.CloseAll(2*time.Second)`（对热进程池内子进程组执行 `SIGTERM → 2s → SIGKILL`） |
| **Shutdown Phase 4** | ≤ 2s | `Pool.Close()` 归还并释放 PG17 连接池 → 刷盘并关闭日志文件句柄 |

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 CLI 入口分发 (`main.go`) 与 `bootstrap-org` (`bootstrap_org.go`)

对应 Java：`SuperAgentServerApplication.java:L13-L20`、`OrganizationService.java:L32-L119`。

```go
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
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
		fmt.Printf("synapse-server %s (commit=%s, built=%s, go=%s)\n",
			Version, Commit, BuildTime, runtime.Version())
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q (expected: serve | migrate | bootstrap-org | version)\n", sub)
		return 2
	}
}
```

```go
// bootstrap_org.go：商业私有化租户与首任管理员初始化
// 对齐 Java OrganizationService.java:L32-L119（保留 slug 校验、12 保留词与 14 位密码，彻底移除公网注册路由）
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

	cfg, err := loadAndValidateConfig(*cfgPath, cliOverrides{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.OpenPool(ctx, cfg.Storage.Postgres)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect postgres failed: %v\n", err)
		return 1
	}
	defer pool.Close()

	if cfg.Storage.AutoMigrate {
		if err := db.MigrateUp(ctx, pool); err != nil {
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

	// 最小装配：仅实例化租户开户所需 Store 与连接器播种服务（不启动 HTTP 与后台循环）
	provisioner, err := wireBootstrapProvisioner(cfg, pool)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wire bootstrap failed: %v\n", err)
		return 1
	}
	if err := provisioner.BootstrapOrg(ctx, slug, name, username, string(hash)); err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap-org failed: %v\n", err)
		return 1
	}

	// 明文密码仅输出至 stdout 一次，绝不写入任何 slog 日志流
	fmt.Printf("Organization initialized successfully:\n")
	fmt.Printf("  org:              %s\n", slug)
	fmt.Printf("  name:             %s\n", name)
	fmt.Printf("  admin_user:       %s\n", username)
	fmt.Printf("  initial_password: %s\n", rawPwd)
	fmt.Printf("Note: Save this password now. First sign-in will enforce password change.\n")
	return 0
}

func validateBootstrapArgs(slug, name, username string) error {
	if !orgSlugRe.MatchString(slug) {
		return fmt.Errorf("invalid --id %q: must match ^[a-z][a-z0-9-]{1,30}[a-z0-9]$", slug)
	}
	if _, bad := reservedOrg[slug]; bad {
		return fmt.Errorf("organization id %q is reserved", slug)
	}
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return fmt.Errorf("--name must be 1..64 characters")
	}
	if n := utf8.RuneCountInString(username); n < 2 || n > 64 {
		return fmt.Errorf("--admin-user must be 2..64 characters")
	}
	return nil
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

### 5.2 L0→L6 显式零反射手动装配 (`wire.go`)

> **工程红线**：零反射 DI、零 `init()` 隐式注册、**零 Redis**；严格按 **L0 → L1 → L2 → L3 → L4 → L5 → L6** 顺序显式装配全部 **38 个 PG17 Store**、双 HTTP 客户端（`TrustedClient` 熔断器 + `UntrustedEgressClient` 防 SSRF）、双 GA Runner、20 个 MCP ToolProvider、双 IM 连接器与 `web.NewRouter`。

```go
package main

type BackgroundLoop interface {
	Name() string
	Run(ctx context.Context)
}

type NamedCloser struct {
	Name string
	Fn   func() error
}

// App 持有完整装配的运行时对象，由 lifecycle.go 基于 L0 lifecycle.BackgroundGroup / ShutdownManager 编排。
type App struct {
	Config      *config.Config
	Pool        *pgxpool.Pool
	Metrics     *obs.Metrics
	Router      *gin.Engine
	HTTPServer  *http.Server
	Companies   *tenantplat.CompanyService
	McpGrants   *mcp.Grants
	WarmPool    *claude.WarmPool
	IMConns     []spi.IMConnector // [FeishuIMConnector, DingTalkIMConnector]
	CoreLoops   []BackgroundLoop  // 8 个核心业务循环
	MaintLoops  []BackgroundLoop  // 3 个维护/同步循环
	CloseHooks  []NamedCloser     // 活跃运行中止钩子（chat/task/superagent/provider）
	FlushLogger func() error      // Phase 4 最终日志刷盘关闭
}

func WireApp(ctx context.Context, cfg *config.Config, rootCtx, sseRootCtx context.Context) (*App, error) {
	// ======================== L0: 基础设施底座（零 Redis） ========================
	logger, closeLog, err := obs.NewLogger(cfg.Observability)
	if err != nil {
		return nil, err
	}
	slog.SetDefault(logger)

	metrics := obs.NewMetrics("synapse-server") // 独立 Prometheus Registry + 6 项 superagent_* 业务指标
	netRT, err := config.BuildNetworkRuntime(cfg.Network, cfg.Runtime)
	if err != nil {
		_ = closeLog()
		return nil, err
	}
	bus := eventbus.New(logger)
	cache := memcache.New(memcache.DefaultOptions()) // 纯进程内 TTL 缓存，零 Redis

	// 出站双客户端：TrustedClient（企业代理+自签CA+重试+gobreaker熔断） & UntrustedEgressClient（DialContext防SSRF）
	trustedHTTP := httpclient.NewTrustedClient(httpclient.TrustedOptions{
		RootCAs:      netRT.RootCAs,
		ProxyFunc:    netRT.ProxyFunc,
		Timeout:      30 * time.Second,
		MaxRetries:   2,
		BreakerName:  "synapse-trusted-outbound",
	})
	egressHTTP := httpclient.NewUntrustedEgressClient(httpclient.EgressOptions{
		RootCAs:   netRT.RootCAs,
		ProxyFunc: netRT.ProxyFunc,
		Timeout:   30 * time.Second,
	})

	// PG17 连接池 + TxManager + AdvisoryLock + 自动向前迁移
	pool, err := db.OpenPool(ctx, cfg.Storage.Postgres)
	if err != nil {
		_ = closeLog()
		return nil, err
	}
	if cfg.Storage.AutoMigrate {
		if err := db.MigrateUp(ctx, pool); err != nil {
			pool.Close()
			_ = closeLog()
			return nil, err
		}
	}
	txMgr := db.NewTxManager(pool)
	locks := db.NewAdvisoryLocker(pool)

	// ======================== L1: 38 PG17 Stores + Runtime ========================
	// SA-07(9) + SA-08(9) + SA-09(14) + SA-10(6) = 38 Store 实现，覆盖全部 47 张 PG17 表
	stores := postgres.NewAllStores(pool, txMgr)

	cipher, err := crypto.NewSecretCipher(cfg.Secret.Key, cfg.Secret.KeyFile, stores.Company, cache)
	if err != nil {
		pool.Close()
		_ = closeLog()
		return nil, err
	}

	cliPaths := runtimeplat.NewCLIPaths(cfg.Runtime.NodeDir)
	localLauncher := runtimeplat.NewLocalLauncher(cliPaths, netRT.ChildEnv)
	images := runtimeplat.NewImageAttachmentStore(cfg.Paths.Attachments)
	mcpGrants := mcp.NewGrants(cfg.MCP.SelfURL, cfg.Server.Listen)
	mcpClient := mcp.NewClient(localLauncher, egressHTTP)

	// ======================== L2: 平台治理、Provider/CLI、连接器、资产 ========================
	companies := tenantplat.NewCompanyService(stores.Company, bus)
	quotas := tenantplat.NewQuotaService(stores.Company, stores.User, stores.Usage, cfg.Quota)
	settingsSvc := settings.NewSettingsService(stores.SystemSetting, cipher, cache, bus)
	brandingSvc := settings.NewBrandingService(cfg.Paths.Branding)
	sessions := authplat.NewSessionStore(stores.UserSession, cache)
	authenticator := authplat.NewLocalAuthenticator(companies, stores.User)
	accountSvc := userplat.NewAccountService(stores.User, cfg.Paths.Avatars)
	userSvc := userplat.NewUserService(stores.User, quotas, accountSvc, nil /* V2 OwnerTransferHook */)
	peopleDir := userplat.NewPeopleDirectory(stores.User)
	noticeSvc := notice.NewNoticeService(stores.Notice, bus)

	integrationSvc := integration.NewIntegrationService(stores.SystemSetting, cipher, bus) // V1: feishu, dingtalk
	feishuOAuth := connector.NewFeishuDocsOAuthClient(integrationSvc, stores.ConnectorCred, cipher, trustedHTTP, cfg.OAuth)
	connectorSvc := connector.NewConnectorService(stores.Connector, stores.ConnectorCred, integrationSvc, cipher, mcpGrants)
	connectorToolsSvc := connector.NewConnectorToolsService(connectorSvc, mcpClient, cache)
	connectorOAuthSvc := connector.NewConnectorOAuthService(connectorSvc, feishuOAuth)

	nodeSvc := installer.NewNodeRuntimeService(cfg.Runtime, cliPaths, netRT.ChildEnv, trustedHTTP)
	credSvc := provider.NewCredentialService(stores.ProviderCred, cipher, bus)
	modelGateway := gateway.NewDirectCredentialGateway(credSvc)
	providerSvc := provider.NewProviderService(stores.ModelProvider, credSvc, nodeSvc, cliPaths, localLauncher, trustedHTTP, cfg.Provider)
	providerChecker := provider.NewStatusChecker(companies, providerSvc, credSvc, cfg.Provider.Checker)
	analyticsSvc := analytics.NewAnalyticsService(stores.Task, stores.Agent, stores.Usage, stores.User)

	// V1 GA 双 AgentRunner（Claude 含热进程池 + Codex），通过插件化 Registry 预留扩展位
	warmPool := claude.NewWarmPool(cfg.Agent.ClaudeWarm, metrics)
	claudeRunner := claude.NewRunner(localLauncher, modelGateway, mcpGrants, warmPool, cfg.Agent.Claude)
	codexRunner := codex.NewRunner(localLauncher, modelGateway, mcpGrants, cfg.Agent.Codex)
	runnerReg := runtimeplat.NewRunnerRegistry(claudeRunner, codexRunner)

	projectSvc := project.NewProjectService(stores.Project, stores.ProjectItem, stores.User, stores.Agent)
	fileStorageSvc := files.NewFileStorageService(stores.SystemSetting, stores.WorkFile, cipher,
		files.NewLocalProvider(cfg.Paths.Files), files.NewS3Provider(trustedHTTP))
	fileFolderSvc := files.NewFileFolderService(stores.FileFolder, stores.WorkFile)
	fileSvc := files.NewFileService(stores.WorkFile, fileStorageSvc, fileFolderSvc, projectSvc, cfg.Files)
	skillSvc := skills.NewSkillService(stores.Skill, stores.User, cfg.Paths.Skills)
	skillDelSvc := skills.NewSkillDeletionService(stores.SkillDeletion, skillSvc)

	// ======================== L3: 成本路由、文档、Agent/Chat、Task/Automation ========================
	priceSvc := cost.NewModelPriceService(stores.SystemSetting, providerSvc, trustedHTTP, cache, cfg.Cost)
	usageSvc := cost.NewTokenUsageService(stores.Usage, priceSvc, metrics)
	modelRouter := costrouter.NewTaskModelRouter(providerSvc, priceSvc, settingsSvc, runnerReg, usageSvc)

	feishuDocTools := docs.NewFeishuDocTools(integrationSvc, connectorSvc, feishuOAuth, trustedHTTP, mcpGrants)
	dingtalkDocTools := docs.NewDingTalkDocTools(integrationSvc, stores.EventSource, trustedHTTP, cfg.DingTalk, mcpGrants)
	linkReader := docs.NewLinkReaderService(feishuDocTools, egressHTTP) // V1 精简 2 级：飞书授权直读 + 通用 HTML/PDF

	agentLogSvc := agent.NewAgentLogService(stores.AgentLog, companies, cfg.AgentLog)
	agentSvc := agent.NewAgentService(stores.Agent, stores.AgentMember, stores.AgentParent, stores.Chat,
		stores.TurnTiming, projectSvc, skillSvc, settingsSvc, modelRouter, agentLogSvc)
	agentInviteSvc := agent.NewAgentInviteService(stores.AgentInvite, agentSvc)
	memorySvc := agentmem.NewMemoryService(stores.AgentMemory, stores.Chat, agentSvc, runnerReg, modelRouter, usageSvc, cfg.Memory)
	memoryLoop := agentmem.NewMemoryLoop(companies, memorySvc, cfg.Memory)
	fileTagger := agent.NewFileTagger(companies, fileSvc, fileFolderSvc, runnerReg, modelRouter, usageSvc, cfg.Files)
	chatSvc := agentchat.NewChatService(stores.Chat, stores.TurnTiming, agentSvc, fileSvc, skillSvc, images,
		connectorSvc, runnerReg, modelRouter, usageSvc, quotas, mcpGrants, agentLogSvc, memorySvc, metrics, cfg.Chat, rootCtx, sseRootCtx)

	taskPool := taskpool.NewSessionPool(cfg.Paths.Tasks)
	taskSvc := task.NewTaskService(stores.Task, stores.TaskPhase, stores.TaskRunState, stores.TaskAgent,
		stores.TaskLink, stores.TaskTag, stores.TaskModelChoice, stores.TurnTiming,
		agentSvc, projectSvc, fileSvc, skillSvc, images, connectorSvc, linkReader,
		runnerReg, modelRouter, usageSvc, quotas, mcpGrants, taskPool, bus, metrics, cfg.Task, rootCtx)
	taskScheduler := task.NewScheduler(companies, taskSvc, quotas, metrics, cfg.Task.Scheduler)
	taskWatchdog := taskpool.NewWatchdog(companies, taskSvc, cfg.Task.Watchdog)
	taskTagger := task.NewTaskTagger(stores.TaskTag, runnerReg, modelRouter, usageSvc)
	autoSvc := taskauto.NewAutomationService(stores.Automation, taskSvc, companies, locks, cfg.Automation)
	autoScheduler := taskauto.NewAutomationScheduler(companies, autoSvc, locks, cfg.Automation)

	// ======================== L4: IM 长连接、收件箱与个人 SuperAgent ========================
	feishuIM := feishu.NewIMConnector(integrationSvc, stores.EventSource, stores.User, trustedHTTP, metrics)
	dingtalkIM := dingtalk.NewIMConnector(integrationSvc, stores.EventSource, stores.User, trustedHTTP, metrics)
	imConns := []spi.IMConnector{feishuIM, dingtalkIM}
	inboxSvc := inbox.NewInboxService(companies, stores.InboxEvent, stores.InboxTodo, stores.EventSource,
		stores.Org, imConns, bus, cfg.Inbox)
	userInboxSvc := inbox.NewUserInboxService(stores.InboxEvent, stores.InboxTodo, stores.EventSource, taskSvc, bus)

	superAgentSvc := orchestrator.NewSuperAgentService(stores.SuperAgentState, stores.SuperAgentWork,
		agentSvc, chatSvc, taskSvc, autoSvc, userInboxSvc, runnerReg, modelRouter, usageSvc, mcpGrants, bus, cfg.SuperAgent)
	superAgentLoop := orchestrator.NewSuperAgentLoop(companies, superAgentSvc, cfg.SuperAgent)

	// 单向 SPI 回调延迟回接（保持 L3 不反向 import L4）
	chatSvc.RegisterTurnCustomizer(superAgentSvc.ChatCustomizer())
	inboxSvc.SetProcessor(superAgentSvc)

	// ======================== 显式领域事件订阅 ========================
	bus.SubscribeCompanyStarted(
		connectorSvc.OnCompanyStarted,
		taskSvc.OnCompanyStarted,
		superAgentSvc.OnCompanyStarted,
		noticeSvc.OnCompanyStarted,
		feishuIM.OnCompanyStarted,
		dingtalkIM.OnCompanyStarted,
	)
	bus.SubscribeIntegrationChanged(feishuIM.OnIntegrationChanged, dingtalkIM.OnIntegrationChanged)
	bus.SubscribeInboxNotifyRequested(inboxSvc.OnNotifyRequested)
	bus.SubscribeNoticeEvents(
		noticeSvc.OnNoticeEvent,
		agentchat.NewChatTaskResults(chatSvc, stores.Chat).OnTaskStatusChanged,
		userInboxSvc.OnNoticeEvent,
		taskTagger.OnTaskStatusChanged,
	)

	// ======================== 20 个内置 MCP ToolProvider 注册 ========================
	mcpProviders := []mcp.ToolProvider{
		agentmcp.NewCollabMcpTools(agentInviteSvc),
		agentmcp.NewCreateMcpTools(agentSvc),
		agentmcp.NewUpdateMcpTools(agentSvc),
		agentmcp.NewSkillMcpTools(agentSvc, skillSvc),
		agentmcp.NewChatHistoryMcpTools(stores.Chat, agentSvc),
		taskmcp.NewTaskMcpTools(taskSvc),
		taskmcp.NewComplexTaskMcpTools(taskSvc),
		taskmcp.NewAutomationMcpTools(autoSvc),
		files.NewFileMcpTools(fileSvc, fileFolderSvc, mcpGrants),
		files.NewFileFolderMcpTools(fileSvc, fileFolderSvc),
		files.NewFileManageMcpTools(fileSvc),
		skills.NewSkillMcpTools(skillSvc, skillDelSvc, mcpGrants),
		feishuDocTools,
		dingtalkDocTools,
		docs.NewReadLinkTools(linkReader),
		inbox.NewInboxMcpTools(userInboxSvc, inboxSvc),
		orchestratormcp.NewSuperAgentTools(superAgentSvc),
		orchestratormcp.NewSuperAgentTaskTools(superAgentSvc),
		orchestratormcp.NewSuperAgentAutomationTools(superAgentSvc),
		orchestratormcp.NewSuperAgentCollabTools(superAgentSvc),
	}

	// ======================== L5: Gin 网关 + 177 端点 + UI + Actuator ========================
	uiFS, err := ui.NewFS(cfg.Server.UIDir)
	if err != nil {
		pool.Close()
		_ = closeLog()
		return nil, err
	}
	actuator := NewActuator(pool, mcpGrants, warmPool, metrics, cfg.Observability.PrometheusToken)

	router, err := webserver.NewRouter(webserver.Dependencies{
		Authenticator: authenticator, Sessions: sessions, Accounts: accountSvc, Users: userSvc,
		Directory: peopleDir, OrgStore: stores.Org, Settings: settingsSvc, Branding: brandingSvc,
		Integrations: integrationSvc, Connectors: connectorSvc, ConnectorTools: connectorToolsSvc,
		ConnectorOAuth: connectorOAuthSvc, Providers: providerSvc, NodeRuntime: nodeSvc,
		Credentials: credSvc, ModelPrices: priceSvc, Files: fileSvc, FileFolders: fileFolderSvc,
		FileStorage: fileStorageSvc, Projects: projectSvc, Skills: skillSvc, SkillDeletions: skillDelSvc,
		Agents: agentSvc, AgentInvites: agentInviteSvc, AgentLogs: agentLogSvc, Chat: chatSvc,
		Images: images, Tasks: taskSvc, Automations: autoSvc, Inbox: userInboxSvc, Notices: noticeSvc,
		Usage: usageSvc, Analytics: analyticsSvc, SuperAgent: superAgentSvc,
		McpGrants: mcpGrants, McpProviders: mcpProviders, Metrics: metrics, DetachBaseCtx: rootCtx,
	}, webserver.Options{
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
	if err != nil {
		pool.Close()
		_ = closeLog()
		return nil, err
	}

	httpSrv := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout 保持 0，避免截断 10 分钟 SSE 与大文件下载流
	}

	return &App{
		Config: cfg, Pool: pool, Metrics: metrics, Router: router, HTTPServer: httpSrv,
		Companies: companies, McpGrants: mcpGrants, WarmPool: warmPool, IMConns: imConns,
		CoreLoops: []BackgroundLoop{
			taskScheduler,   // 1. TaskScheduler (wakeCh + 30s)
			taskWatchdog,    // 2. TaskWatchdog (startup recover + 30s/120s)
			autoScheduler,   // 3. AutomationScheduler (30s + PG AdvisoryLock)
			superAgentLoop,  // 4. SuperAgentLoop (3s)
			memoryLoop,      // 5. AgentMemoryLoop (5m)
			fileTagger,      // 6. FileTagger (90s delay + 60s)
			providerChecker, // 7. ProviderStatusChecker (3s/1h/1h/10m)
			inboxSvc,        // 8. InboxTicker (60s P0/P1/P2/30d prune)
		},
		MaintLoops: []BackgroundLoop{
			priceSvc,                                             // models.dev 价格同步 (10m check / 24h refresh)
			agentLogSvc,                                          // agent_log 保留期清理 (1h)
			authplat.NewSessionPruner(sessions, locks, time.Hour), // user_session 过期清理 (1h + PG AdvisoryLock)
		},
		CloseHooks: []NamedCloser{
			{Name: "chat", Fn: chatSvc.Close},
			{Name: "task", Fn: taskSvc.Close},
			{Name: "superagent", Fn: superAgentSvc.Close},
			{Name: "provider", Fn: providerSvc.Close},
		},
		FlushLogger: closeLog,
	}, nil
}
```

### 5.3 确定性 7 步启动序与 4 阶段 30s 逆序停机 (`lifecycle.go`)

```mermaid
flowchart LR
    subgraph Start["启动序列 (正序 S1..S7)"]
        S1["S1. Config + Validate<br/>+ Proxy/CA"] --> S2["S2. PG17 Pool<br/>+ goose Up"]
        S2 --> S3["S3. WireApp<br/>L0..L5 全量装配"]
        S3 --> S4["S4. net.Listen 绑定端口<br/>更新 McpGrants.SelfURL"]
        S4 --> S5["S5. HTTP Serve +<br/>StartCompanies"]
        S5 --> S6["S6. 异步 McpGrants<br/>SelfCheckAsync"]
        S6 --> S7["S7. BackgroundGroup<br/>启动 8 核心 + 3 维护循环"]
    end
    subgraph Stop["ShutdownManager 逆序停机 (总预算 30s)"]
        P1["Phase 1 (<=15s)<br/>HTTP Shutdown<br/>+ sseCancel()"] --> P2["Phase 2 (<=8s)<br/>BackgroundGroup.Stop<br/>+ rootCancel()"]
        P2 --> P3["Phase 3 (<=5s)<br/>IM WS Close + Hooks<br/>+ WarmPool 2s Kill"]
        P3 --> P4["Phase 4 (<=2s)<br/>PG Pool Close<br/>+ FlushLogger"]
    end
```

```go
func runServe(args []string) int {
	cfgPath, overrides, err := parseServeFlags(args)
	if err != nil {
		return 2
	}
	cfg, err := loadAndValidateConfig(cfgPath, overrides)
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		return 1
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	defer rootCancel()
	sseCtx, sseCancel := context.WithCancel(rootCtx)
	defer sseCancel()

	app, err := WireApp(rootCtx, cfg, rootCtx, sseCtx)
	if err != nil {
		slog.Error("wire app failed", "err", err)
		return 1
	}

	// S4: 先 bind TCP 获取真实端口（支持 :0 随机端口测试，对齐 McpGrants.onWebServer）
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		slog.Error("listen failed", "addr", cfg.Server.Listen, "err", err)
		app.Pool.Close()
		_ = app.FlushLogger()
		return 1
	}
	if tcpAddr, ok := ln.Addr().(*net.TCPAddr); ok && tcpAddr.Port > 0 {
		app.McpGrants.SetListenPortIfUnconfigured(tcpAddr.Port)
	}

	// 注册 L0 lifecycle.ShutdownManager（4 阶段严格逆序，总预算 cfg.Server.ShutdownTimeout 默认 30s）
	bg := lifecycle.NewBackgroundGroup(slog.Default())
	sm := lifecycle.NewShutdownManager(cfg.Server.ShutdownTimeout, slog.Default())
	registerShutdownPhases(sm, app, bg, rootCancel, sseCancel)

	// S5a: 开启 HTTP 监听
	serveErrCh := make(chan error, 1)
	go func() {
		slog.Info("synapse-server listening", "addr", ln.Addr().String(), "version", Version)
		if err := app.HTTPServer.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErrCh <- err
		}
	}()

	// S5b: 激活全部启用租户（发布 CompanyStarted）
	if err := app.Companies.StartCompanies(rootCtx); err != nil {
		slog.Error("start companies failed", "err", err)
		_ = sm.Shutdown(context.Background())
		return 1
	}

	// S6: 异步自检 POST /mcp ping
	app.McpGrants.SelfCheckAsync(rootCtx)

	// S7: 通过 BackgroundGroup（含自动 panic recover）拉起 8 核心 + 3 维护循环
	for _, loop := range append(append([]BackgroundLoop{}, app.CoreLoops...), app.MaintLoops...) {
		l := loop
		bg.Go(l.Name(), func(ctx context.Context) { l.Run(ctx) })
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	exitCode := 0
	select {
	case sig := <-sigCh:
		slog.Info("shutdown signal received", "signal", sig.String())
	case err := <-serveErrCh:
		slog.Error("http server fatal error", "err", err)
		exitCode = 1
	}

	if err := sm.Shutdown(context.Background()); err != nil {
		slog.Warn("graceful shutdown finished with warnings", "err", err)
		exitCode = 1
	}
	return exitCode
}

func registerShutdownPhases(
	sm *lifecycle.ShutdownManager,
	app *App,
	bg *lifecycle.BackgroundGroup,
	rootCancel, sseCancel context.CancelFunc,
) {
	// Phase 1 (<=15s): 停止接收新连接 + 排空普通 HTTP 请求 + 取消 SSE 根上下文
	sm.Register(lifecycle.PhaseHTTP, 15*time.Second, "http-and-sse", func(ctx context.Context) error {
		sseCancel()
		if err := app.HTTPServer.Shutdown(ctx); err != nil {
			_ = app.HTTPServer.Close()
			return err
		}
		return nil
	})
	// Phase 2 (<=8s): 停止全部 8+3 后台循环并取消派生后台任务
	sm.Register(lifecycle.PhaseBackground, 8*time.Second, "background-loops", func(ctx context.Context) error {
		rootCancel()
		return bg.Stop(ctx)
	})
	// Phase 3 (<=5s): 关闭飞书/钉钉 WS、中止活跃执行钩子并回收 ClaudeWarmPool
	sm.Register(lifecycle.PhaseConnections, 5*time.Second, "im-hooks-warmpool", func(_ context.Context) error {
		var errs []error
		for _, im := range app.IMConns {
			if err := im.Close(); err != nil {
				errs = append(errs, fmt.Errorf("im %s: %w", im.Type(), err))
			}
		}
		for _, h := range app.CloseHooks {
			if err := h.Fn(); err != nil {
				errs = append(errs, fmt.Errorf("hook %s: %w", h.Name, err))
			}
		}
		app.WarmPool.CloseAll(2 * time.Second)
		return errors.Join(errs...)
	})
	// Phase 4 (<=2s): 关闭 PG17 连接池与最终日志刷盘
	sm.Register(lifecycle.PhaseStorage, 1500*time.Millisecond, "pg-pool", func(_ context.Context) error {
		app.Pool.Close()
		return nil
	})
	sm.Register(lifecycle.PhaseFlush, 500*time.Millisecond, "logger-flush", func(_ context.Context) error {
		return app.FlushLogger()
	})
}
```

### 5.4 Actuator 探针与 Prometheus 挂载 (`actuator.go`)

```go
package main

type Actuator struct {
	pool     *pgxpool.Pool
	grants   *mcp.Grants
	warmPool *claude.WarmPool
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

	// when-authorized：未登录仅返回聚合状态，已登录返回各组件明细
	if _, authed := webserver.PrincipalFromContext(c.Request.Context()); !authed {
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
			"db":       dbComp,
			"mcp":      gin.H{"status": mcpSt.State, "selfUrl": a.grants.SelfURL(), "error": mcpSt.Error},
			"warmPool": gin.H{"size": a.warmPool.Size(), "max": a.warmPool.Max()},
		},
	})
}

func (a *Actuator) Prometheus(c *gin.Context) {
	if _, authed := webserver.PrincipalFromContext(c.Request.Context()); !authed {
		if !a.validPromToken(c.GetHeader("Authorization")) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "unauthorized", "message": "请先登录", "data": nil})
			return
		}
	}
	promhttp.HandlerFor(a.metrics.Registry(), promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
}

func (a *Actuator) validPromToken(authHeader string) bool {
	if a.promTok == "" {
		return false
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(authHeader, prefix) {
		return false
	}
	got := strings.TrimSpace(strings.TrimPrefix(authHeader, prefix))
	return subtle.ConstantTimeCompare([]byte(got), []byte(a.promTok)) == 1
}
```

### 5.5 完整商业化配置模板 (`synapse-server/configs/config.example.yaml`)

> 本模板必须被 `SA-01` 的 `internal/infra/config/config_test.go` 直接读取并通过 `KnownFields(true)` + `Validate()` 零报错校验，且严禁出现任何 Redis 配置段。

```yaml
server:
  listen: ":18080"                    # 可被 SYNAPSE_LISTEN / SERVER_PORT 覆盖
  shutdown_timeout: 30s
  ui_dir: ""                          # 开发期指向本地 static 目录热加载；生产留空使用内嵌 embed.FS
  session_cookie_name: "SYNAPSE_SESSION"
  session_cookie_secure: false        # 启用 HTTPS 反向代理时设为 true
  session_ttl: 168h                   # 7 天持久化登录会话（PG17 user_session 表）
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
  key: ""                             # 推荐通过环境变量 SECRET_KEY 传入 32 字节 Base64 KEK
  key_file: "./data/secret.key"       # key 为空时自动加载或 0600 生成

network:
  http_proxy: ""                      # 例: http://proxy.corp.local:8080
  https_proxy: ""
  no_proxy: "localhost,127.0.0.1,postgres"
  ca_bundle_file: ""                  # 企业自签根证书 PEM，同时注入 Go TLS 与子进程 NODE_EXTRA_CA_CERTS

runtime:
  node_dir: "./data/runtime/node"
  npm_registry: ""                    # 企业私有 npm 源或镜像，例: https://registry.npmmirror.com
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
  prometheus_token: ""                # 可选：Prometheus 独立抓取 Bearer Token
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1（CLI 子命令分发与 Flag 叠加）**：编写 `cmd/synapse-server/main_test.go`（默认 `serve`、`version`、未知子命令退出码 `2`、`--listen`/`--ui-dir` 覆盖优先级），实现 `main.go` 与 `cli_flags.go`。
- [ ] **T2（`bootstrap-org` 商业开户子命令）**：编写 `cmd/synapse-server/bootstrap_org_test.go`（slug 正则、12 保留词拦截、14 位无歧义密码与 BCrypt 校验、重复 slug 退出码 `1`、`must_change_password=true` 且密码不入日志），实现 `bootstrap_org.go`。
- [ ] **T3（Actuator 双态探针与 Prometheus 鉴权）**：编写 `cmd/synapse-server/actuator_test.go`（未登录 `/actuator/health` 仅返 `{"status":"UP"}`、PG ping 失败返 `503`、已登录含 `components.{db,mcp,warmPool}`、`/actuator/prometheus` 常量时间 token 校验与 6 项 `superagent_*` 指标断言），实现 `actuator.go`。
- [ ] **T4（全仓手动 `wire.go` 与 `config.example.yaml`）**：落地 `configs/config.example.yaml`（经 `SA-01` `KnownFields(true)` 校验）与 `cmd/synapse-server/wire.go`（严格 L0→L5 装配 38 Stores + 20 MCP + 双 Runner + 双 IM + 177 端点，确认零反射与零 Redis）。
- [ ] **T5（确定性 7 步启动序与 4 阶段 30s 逆序停机）**：编写 `cmd/synapse-server/lifecycle_test.go`（`:0` 随机端口回写 `McpGrants.SelfURL` + `SelfCheck`、`SIGTERM` 后严格按 `PhaseHTTP → PhaseBackground → PhaseConnections → PhaseStorage → PhaseFlush` 记录顺序退出），实现 `lifecycle.go`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：合入后将 `SA-41` 状态更新为 `已实施（commit <hash>）`；
- [ ] [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md)：核对 CLI 子命令参数、环境变量表与 8+3 后台循环清单完全一致。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | CLI 分发与 Flag 优先级 | `cmd/synapse-server/main_test.go` | 无参默认 `serve`；未知子命令返 `2`；`--listen`/`--ui-dir` 覆盖 Env 与 YAML |
| 2 | `config.example.yaml` 零未知字段校验 | `internal/infra/config/config_test.go` | `KnownFields(true)` 解析 `configs/config.example.yaml` 并通过 `Validate()`，且不含任何 Redis 键 |
| 3 | `bootstrap-org` 校验与安全输出 | `cmd/synapse-server/bootstrap_org_test.go` | 12 保留词与非法 slug 返 `2`；生成 14 位无歧义密码且通过 `bcrypt.CompareHashAndPassword`；重复 slug 返 `1`；`slog` 缓冲区零明文密码 |
| 4 | `/actuator/health` 双态与故障降级 | `cmd/synapse-server/actuator_test.go` | 未登录仅 `{"status":"UP"}`；已登录附 `db`/`mcp`/`warmPool` 明细；DB 断开返 `503 {"status":"DOWN"}` |
| 5 | `/actuator/prometheus` 鉴权与 6 项业务指标 | `cmd/synapse-server/actuator_test.go` | 无会话无 token 返 `401`；错误 token 返 `401`；合法 `prometheus_token` 返 `200` 且包含全部 6 项 `superagent_*` 指标与 `application="synapse-server"` |
| 6 | 7 步启动序与 4 阶段 30s 逆序停机 | `cmd/synapse-server/lifecycle_test.go` | 绑定 `:0` 自动回填 `McpGrants.SelfURL` 并 `ping` 自检成功；收到退出信号严格按 `HTTP/SSE → 8+3 Loops → IM/Hooks/WarmPool → PG Pool → Log Flush` 顺序退出 |
| 7 | 全仓分层 AST 守卫与多租户 SQL 守卫回归 | `internal/arch_test.go`、`internal/storage/postgres/guard_test.go` | `L0→L6` 零逆向导入、`agent ⊥ task`、`web ⊥ pgx`、38 Store 全量租户隔离零逃逸 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./...` 全仓 100% 绿灯（含 `internal/arch_test.go` 与 `internal/storage/postgres/guard_test.go`）
- [ ] §2 交付物 8 项逐项存在；`cmd/synapse-server/wire.go` 零反射 DI、完整装配 38 PG17 Stores + 20 MCP ToolProviders + Claude/Codex 双 GA Runner + 飞书/钉钉双 IM 连接器
- [ ] 全仓 `go.mod` 与 `configs/config.example.yaml` 确认 **零 Redis 客户端依赖与零 Redis 配置**
- [ ] §8 测试清单 7 类用例全部落地，`cmd/synapse-server` 覆盖率 ≥ 80%
- [ ] 本地冒烟验证：`synapse-server migrate` → `synapse-server bootstrap-org --id acme --name "Acme"` → `synapse-server serve` → `GET /actuator/health` 200 → `SIGINT` 优雅退出码 `0`

---

## 10. 可提交节点

- 单 PR `SA-41` 合入，建议拆为 2 个原子提交：
  1. `feat(server): add CLI subcommands, bootstrap-org, actuator endpoints and config template (spec 41, PR SA-41)`
  2. `feat(server): add explicit L0-L6 manual wire.go and 30s reverse-order lifecycle orchestration (spec 41, PR SA-41)`
