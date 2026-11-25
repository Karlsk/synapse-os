# spec 19 | L2 平台管理 — 私有 Node.js 自动托管与在线 CLI 安装升级引擎

- **状态**：`draft`
- **篇号顺序**：19（L2 供应商子域第二篇；承接 `SA-18` 供应商目录与密钥池，交付商业私有化环境下的私有 Node.js 托管与在线 `npm install -g` 安装升级引擎）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-19`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §2
  - 归档母本：[_epic/L2/spec_05_platform_provider_node_credentials_connectors.md](../_epic/L2/spec_05_platform_provider_node_credentials_connectors.md) §5.2–§5.3
  - [SuperAgent/07_模型提供商管理_多密钥容灾_成本路由与用量分析.md](../../../SuperAgent/07_模型提供商管理_多密钥容灾_成本路由与用量分析.md)
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-platform/src/main/java/com/superagent/platform/provider/NodeRuntimeService.java` (`L43-L336`)
    - `superagent-platform/src/main/java/com/superagent/platform/provider/InstallJob.java` (`L11-L57`)
    - `superagent-platform/src/main/java/com/superagent/platform/provider/InstallFailures.java` (`L13-L79`)
    - `superagent-platform/src/main/java/com/superagent/platform/provider/ProviderService.java` (`L249-L428`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`config` 网络/镜像/代理/CA、`lifecycle.BackgroundGroup`）
  - [L0/spec_03 (SA-03)](../L0/spec_03_httpclient_resilience_egress.md)（`httpclient.TrustedClient`）
  - [L1/spec_11 (SA-11)](../L1/spec_11_runtime_launcher_runner_framework.md)（`ProcessLauncher`、`CliPaths`）
  - [L2/spec_18 (SA-18)](./spec_18_platform_provider_credentials_modelcall.md)（`ProviderCatalog`、`ProviderService`）
- **下游消费者**：`SA-39`（`/api/v1/providers/node*` 与 `/api/v1/providers/:id/{install,upgrade,install-log}` HTTP 路由）、`SA-41`（Server 启动装配与优雅停机）、`SA-42`（Docker 镜像可写 `/opt/cli` 与私有 Node 目录挂载）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

商业私有化客户服务器往往处于企业代理、私有根证书、无 root 权限或宿主机系统 Node.js 版本过旧的环境中。为实现“后台管理页面一键安装/升级 Claude Code 与 Codex CLI”，必须提供一套开箱即用、抗弱网中断、全链路中文故障诊断的私有 Node.js 与在线 npm 安装引擎。

本篇（`SA-19`）专注实现该安装子系统（1.5 人天独立 PR），完整保留并增强原版三大亮点：
1. **私有 Node.js LTS 自动托管**：支持自定义镜像源（`node_dist_mirror`，如内网 Artifactory 或 `npmmirror`）、企业代理与私有 CA、**60 秒无字节停滞 Watchdog**、**`SHASUMS256.txt` 严格校验**与临时目录原子替换；
2. **串行在线 npm 安装队列 + 双超时整组强杀**：3 分钟静默超时 / 30 分钟总超时通过 `ProcessHandle.Kill()` 整组强杀，遇系统 npm `EACCES`/`EPERM` 权限错误自动安装私有 Node 并无感重试；
3. **7 类 npm 中文故障诊断与 400 行环形实时日志**：便于前端轮询展示安装进度与定位代理/证书/磁盘问题。

### 1.2 本篇边界

- **本篇负责**：`internal/platform/provider/installer`（或合并于 `internal/platform/provider` 子文件）：`NodeRuntimeService`、`InstallJob`、`InstallFailures`、`CliInstaller`（串行安装队列、前置 Ensure Node、双看门狗、EACCES 自动降级私有 Node）。
- **本篇不负责**：供应商 CRUD、API Key 池容灾、模型拉取与 6 并发推理探测（已由 `SA-18` 交付）；Gin HTTP 路由挂载（交由 `SA-39`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `InstallJob` 400 行线程安全环形日志缓冲与状态快照 | `internal/platform/provider/installer/install_job.go` | 新增 |
| 2 | `InstallFailures` 7 类 npm 错误码中文诊断器与 ANSI 剥离工具 | `internal/platform/provider/installer/install_failures.go` | 新增 |
| 3 | `NodeRuntimeService` 私有 Node LTS 镜像下载、60s 停滞检测、SHA-256 校验与原子替换 | `internal/platform/provider/installer/node_runtime.go` | 新增 |
| 4 | `CliInstaller` 串行 npm 安装队列、3m/30m 双看门狗与 `EACCES` 自动私有 Node 降级 | `internal/platform/provider/installer/cli_installer.go` | 新增 |
| 5 | `httptest` 镜像/停滞/校验失败/降级重试单测套件 | `internal/platform/provider/installer/{install_job,install_failures,node_runtime,cli_installer}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增或修改数据库表。安装日志与任务状态为纯内存态（`InstallJob` 按 `providerID` 或 `"node"` 键存放于进程内受 `sync.RWMutex` 保护的映射表，零 Redis 依赖）；安装成功后回调 `SA-18` `ProviderService.RefreshLocal` 将新探测到的版本号写回 `model_provider` 表。

磁盘落位严格遵循 `CliPaths`：
- 私有 Node 根目录：`<nodeRoot>/current/{bin/node,bin/npm}`（临时解压目录 `<nodeRoot>/.staging-*`，失败自动 `os.RemoveAll`）；
- CLI 安装前缀（命中私有 Node 时）：`npm_config_prefix=<nodeRoot>/current`。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package installer

const (
	MaxLogLines       = 400
	NodeStallTimeout  = 60 * time.Second
	InstallIdleLimit  = 3 * time.Minute
	InstallTotalLimit = 30 * time.Minute
)

type NodeStatusDto struct {
	Installed   bool   `json:"installed"`
	Version     string `json:"version,omitempty"`
	Source      string `json:"source"` // "private" | "system" | "none"
	Path        string `json:"path,omitempty"`
	Installing  bool   `json:"installing"`
}

type InstallLogDto struct {
	Running bool     `json:"running"`
	Done    bool     `json:"done"`
	Error   string   `json:"error,omitempty"`
	Lines   []string `json:"lines"`
}

type InstallJob struct { /* mu sync.RWMutex, lines []string, running, done bool, errMsg string */ }
func NewInstallJob() *InstallJob
func (j *InstallJob) Step(text string)
func (j *InstallJob) Line(text string)
func (j *InstallJob) Finish(err error)
func (j *InstallJob) Snapshot() InstallLogDto

func SummarizeFailure(output string, exitCode int) string
func StripANSI(s string) string

type NodeRuntimeService struct { /* cfg, paths, launcher, httpClient, installMu, activeJob */ }
func NewNodeRuntimeService(cfg config.NetworkRuntime, paths *runner.CliPaths, launcher spi.ProcessLauncher, httpClient *httpclient.Client) *NodeRuntimeService
func (s *NodeRuntimeService) Status(ctx context.Context) NodeStatusDto
func (s *NodeRuntimeService) InstallAsync(bg *lifecycle.BackgroundGroup) InstallLogDto
func (s *NodeRuntimeService) InstallLog() InstallLogDto
func (s *NodeRuntimeService) Ensure(ctx context.Context, minVersion string, job *InstallJob) error
func (s *NodeRuntimeService) InstallPrivateNow(ctx context.Context, job *InstallJob) (string, error)

type CliInstaller struct { /* catalog, node, paths, launcher, bg, queue chan installTask, mu, jobs map[string]*InstallJob, busy map[string]bool */ }
func (i *CliInstaller) Start(bg *lifecycle.BackgroundGroup)
func (i *CliInstaller) EnqueueInstall(ctx context.Context, providerID, providerKey string, upgrade bool, onDone func(ctx context.Context)) error
func (i *CliInstaller) Log(providerID string) InstallLogDto
```

### 4.2 路由契约（由 `L5/spec_39` 挂载，仅 `ADMIN` 可访问）

| 路径 | HTTP 方法 | 对应服务方法 |
|---|---|---|
| `/api/v1/providers/node` | `GET` | `NodeRuntimeService.Status` |
| `/api/v1/providers/node/install` | `POST` | `NodeRuntimeService.InstallAsync` |
| `/api/v1/providers/node/install-log` | `GET` | `NodeRuntimeService.InstallLog` |
| `/api/v1/providers/:id/install` | `POST` | `CliInstaller.EnqueueInstall(..., upgrade=false)` |
| `/api/v1/providers/:id/upgrade` | `POST` | `CliInstaller.EnqueueInstall(..., upgrade=true)` |
| `/api/v1/providers/:id/install-log` | `GET` | `CliInstaller.Log` |

### 4.3 错误语义

- 同一 `providerID` 正在安装时重复触发：返回 `apperror.Conflict("该 CLI 正在安装或升级中，请稍候")`（HTTP `409`）；
- 不支持的 OS/Arch 平台（非 `darwin|linux` + `amd64|arm64`）：返回明确错误信息写入 `InstallJob`；
- 60s 停滞超时：`下载 <url> 超过 60 秒没有收到数据，已放弃。请检查服务器网络或 Node 镜像配置`；
- SHA-256 校验不匹配：拒绝解压并清理 `.staging-*`，错误原文入日志。

### 4.4 SQL 契约

本篇无直接 SQL 操作。

---

## 5. 行为语义

### 5.1 `NodeRuntimeService` 镜像下载、60s 停滞 Watchdog 与原子替换（对齐 `NodeRuntimeService.java:43-336`）

1. **HTTP 底座复用**：使用 L0 `httpclient.TrustedClient`（已自动挂载企业 `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY` 与私有 CA `ExtraCABundle`），下载大文件时使用“无整体总超时”请求模式，由 60 秒停滞 Watchdog 精确守卫；
2. **下载基址**：优先 `cfg.NodeDistMirror`（默认 `https://nodejs.org/dist`），自动规范末尾 `/`；
3. **执行流程**：
   - `GET <dist>index.json` 选出最新 `lts != false` 的版本（如 `v22.x.y`）；
   - 按 `runtime.GOOS`/`runtime.GOARCH` 映射 `darwin|linux` + `x64|arm64` 拼接 `node-<ver>-<plat>.tar.gz`；
   - 流式写入 `<nodeRoot>/.staging-xxx/<file>`：独立 ticker 每 5 秒检查 `lastByteNano`，超过 60 秒未收到新字节立即 `resp.Body.Close()` 打断阻塞 `Read`；每完成 10%（未知总长每 10 MiB）向 `InstallJob` 追加 `"已下载 X.X MB / Y.Y MB"`；
   - 拉取 `<dist><ver>/SHASUMS256.txt` 比对文件 SHA-256，不一致立即报错并由 `defer os.RemoveAll(staging)` 清理；
   - 调用 `ProcessLauncher` 执行 `tar -xzf` 解压，校验 `<unpacked>/bin/node` 可执行权限后原子替换 `<nodeRoot>/current`。

### 5.2 `CliInstaller` 串行队列、双超时强杀与 `EACCES` 自动私有 Node 降级（对齐 `ProviderService.java:249-428`）

```go
package installer

func (i *CliInstaller) executeTask(ctx context.Context, t installTask) {
	defer func() {
		i.mu.Lock()
		delete(i.busy, t.providerID)
		i.mu.Unlock()
		if t.onDone != nil {
			t.onDone(t.tenantCtx)
		}
	}()
	entry, ok := i.catalog.Find(t.providerKey)
	if !ok {
		t.job.Finish(apperror.NotFound("未知供应商: " + t.providerKey))
		return
	}
	// 1. 前置检查 Node 版本（< entry.MinNode 或缺失则自动托管私有 Node）
	if i.nodeProblem(ctx, entry.MinNode) {
		if err := i.node.Ensure(ctx, entry.MinNode, t.job); err != nil {
			t.job.Finish(err)
			return
		}
	}
	// 2. 运行 npm install -g <pkg>@latest --no-fund --no-audit --loglevel=http
	out, code, err := i.runWithWatchdogs(ctx, entry, t.job)
	// 3. 若系统 npm 报 EACCES/EPERM 且尚未启用私有 Node -> 自动装私有 Node 后立即重试一次
	if err != nil && isPermissionFailure(out) && !i.node.HasPrivateNode() {
		t.job.Step("检测到系统目录无写入权限（EACCES），自动切换至私有 Node.js 重试")
		if _, nerr := i.node.InstallPrivateNow(ctx, t.job); nerr == nil {
			out, code, err = i.runWithWatchdogs(ctx, entry, t.job)
		}
	}
	if err != nil {
		t.job.Finish(errors.New(SummarizeFailure(out, code)))
		return
	}
	t.job.Finish(nil)
}
```

### 5.3 `InstallFailures` 7 类 npm 中文故障诊断（对齐 `InstallFailures.java:13-79`）

正则提取 `npm (?:error|ERR!) code (\S+)` 并映射 7 类诊断（附提取出的 URL/路径）：
1. 网络不可达（`ENOTFOUND`/`ECONNREFUSED`/`ETIMEDOUT`/`ECONNRESET`/`EAI_AGAIN`/`ENETUNREACH`）→ 提示检查服务器网络/代理/镜像源；
2. 权限不足（`EACCES`/`EPERM`）→ 提示目录不可写或私有 Node 路径权限；
3. 包不存在（`E404`）→ 提示检查 npm registry 镜像是否同步该包；
4. Node 版本过低（`EBADENGINE`）→ 提示升级私有 Node；
5. 磁盘已满（`ENOSPC`）→ 提示清理磁盘；
6. TLS 证书错误（`SELF_SIGNED_CERT_IN_CHAIN`/`UNABLE_TO_VERIFY_LEAF_SIGNATURE`/`CERT_HAS_EXPIRED`）→ 提示配置 `NODE_EXTRA_CA_CERTS`；
7. 仓库鉴权失败（`E401`/`E403`）→ 提示检查私有 npm 认证配置；
- 未命中以上错误码时提取最后 3 条非空错误行返回。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `install_job_test.go` 与 `install_failures_test.go`（红灯）→ 实现 400 行环形日志、ANSI 剥离与 7 类 npm 诊断器。
- [ ] **T2**：编写 `node_runtime_test.go`（使用 `httptest.Server` 模拟镜像源、慢流停滞与篡改 SHA-256）→ 实现 `NodeRuntimeService`。
- [ ] **T3**：编写 `cli_installer_test.go`（Mock `ProcessLauncher` 模拟 3m 静默超时、30m 总超时与 `EACCES` 自动私有 Node 重试）→ 实现 `CliInstaller`。
- [ ] **T4**：运行 `go test -race -cover ./internal/platform/provider/installer/...` 验证全部通过。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-19` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `runtime.node_dist_mirror`、`runtime.npm_registry`、`runtime.node_extra_ca_certs` 说明。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `InstallJob` 400 行环形淘汰与并发安全 | `internal/platform/provider/installer/install_job_test.go` | 写入 500 行仅保留最新 400 行；多协程并发读写 `-race` 零告警 |
| 2 | `InstallFailures` 7 类错误码与 ANSI 剥离 | `internal/platform/provider/installer/install_failures_test.go` | 准确映射 `ECONNREFUSED`/`EACCES`/`E404`/`EBADENGINE`/`ENOSPC`/`SELF_SIGNED_CERT_IN_CHAIN`/`E401` 且剥离 ANSI 颜色码 |
| 3 | `NodeRuntimeService` 镜像下载、SHA-256 与 60s 停滞 | `internal/platform/provider/installer/node_runtime_test.go` | 命中自定义镜像；SHA-256 篡改拒绝解压且删除 `.staging-*`；慢连接触发停滞中断（单测注入短超时）；正常解压原子替换 `current` |
| 4 | `CliInstaller` 串行去重、双看门狗与 `EACCES` 降级 | `internal/platform/provider/installer/cli_installer_test.go` | 重复提交同 ID 返 `409`；静默/总超时调用 `proc.Kill()`；首轮 `EACCES` 自动调用私有 Node 安装并成功重试 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/platform/provider/installer/...` 全绿
- [ ] §2 交付物表 5 项齐备，行覆盖率 ≥ 80%
- [ ] 零真实外网依赖（全用 `httptest.Server` + Mock `ProcessLauncher`）
- [ ] 后台安装 worker 接入 L0 `lifecycle.BackgroundGroup`，优雅停机时正确退出并终止运行中安装子进程

---

## 10. 可提交节点

- 建议 commit：`feat(platform-provider): add private NodeRuntimeService and serial online CliInstaller with watchdog and diagnostics (spec 19, SA-19)`
