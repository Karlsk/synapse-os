# spec 03 | Runtime 执行运行时引擎 — ProcessLauncher、Claude/Codex 适配器、McpGrants 与附件存储

- **状态**：`draft`
- **篇号顺序**：03（L1 层执行运行时底座；与 `spec_02` 并行实施）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` §3 看板中的 `SA-03`
- **上位契约**：`docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §1、§5.1；`docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md` §1–§3；`docs/SuperAgent-Go/DDD/README.md` §1–§2
- **前置依赖**：`L1/spec_01_core_tenant_crypto_egress.md`（`SA-01`，提供 `internal/core/{domain,spi,tenant,apperror,obs}`）
- **下游消费者**：`SA-05`（`NodeRuntimeService`/`ProviderProbe`/`ModelCall`/`ConnectorToolsService`/`ConnectorService` 消费 `CliPaths`、`ProcessLauncher`、`RunnerRegistry`、`McpClient`、`McpGrants`、`McpToolProvider`）、`SA-09`（`ChatService` 消费 `RunnerRegistry`、`ModelGateway`、`McpGrants`、`ImageAttachmentStore`）、`SA-10`（`TaskService`）、`SA-12`（`SuperAgentService`）、`SA-13`（`/mcp` 协议路由与图片附件 HTTP 端点）、`SA-14`（`wire.go` 装配与优雅停机 `WarmPool.CloseAll()`）
- **重构参照源码**：`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-runtime/src/main/java/com/superagent/runtime/{cli,mcp,attachment}/*`、`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-core/src/main/java/com/superagent/core/chat/*`、`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/superagent-web/src/main/java/com/superagent/web/mcp/McpController.java`

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

`internal/runtime` 是 `synapse-server` 驱动底层 AI 编码与对话 CLI（Claude Code、OpenAI Codex）、承载内置与外部 MCP 协议通信以及管理多模态图片附件的**执行引擎底座**。在原版 Java 实现中，`superagent-runtime` 直接通过 `ProcessBuilder` 裸起 10 家 CLI 进程、用自定义 `Cancellation` 对象管理取消、使用 JVM 线程驱动 `ClaudeWarmSessions` 长驻进程池，且将凭据解析散落在业务调用方中。

Go 重构版（`SA-03`）在严格保真核心运行语义的前提下完成四项架构立骨：
1. **四大一期立骨、二期零重构演进的 SPI 接口**：统一抽离 `ProcessLauncher`（V1 `LocalLauncher` 基于 `os/exec` + `Setpgid` 整组查杀，V2 可无缝切换 Docker/gVisor/Kata/远端 Worker）、`AgentRunner` + `RunnerRegistry` + `StreamParser` + `RunnerCapabilities`、`ModelGateway`（V1 `DirectCredentialGateway`，V2 可无缝切入集中式三协议网关 `ProxyModelGateway`）与 `McpToolProvider`；
2. **V1 锁定双 GA 执行引擎 + 开放扩展模板**：V1 正式交付 **`ClaudeRunner`（含 Go 并发 LRU 热进程池 `ClaudeWarmPool`）** 与 **`CodexRunner`** 两套 GA 适配器，同时提供可复用的通用 `JSONLineRunner` 模板，未来新增 DeepSeek / Qwen / Kimi / Gemini / GLM / CodeBuddy 等任意 CLI 只需新增单文件实现并在启动装配处注册一行，业务域零侵入；
3. **Go 原生并发与资源确定性回收**：以 `context.Context` 替代原版 `Cancellation` 回调锁；以 `SysProcAttr{Setpgid: true}` + `syscall.Kill(-pgid, SIGKILL)` 杜绝 CLI 衍生的 `node`/`bash` 孙进程僵尸泄漏；所有临时 `--settings` / `--mcp-config` 统一强制 `0600` 权限并在 `ProcessHandle.Close()` 确定性清理；
4. **私有化网络增强与缺陷修复**：`CliPaths` 统一叠加企业代理（`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`）、自签 CA（`NODE_EXTRA_CA_CERTS`）与私有 npm 源；统一 `McpToolError` 契约确保 L5 `/mcp` 端点把所有预期领域异常映射为 `isError: true`，彻底修复原版漏捕异常返回 `-32603` 的问题。

### 1.2 本篇边界与 V1 裁剪红线

- **本篇负责（L1 `internal/runtime/*`）**：
  - `launcher/`：`LocalLauncher`（实现 `spi.ProcessLauncher`）+ 跨平台进程组终止（`proc_unix.go` / `proc_windows.go`）+ `0600` 临时文件生命周期管理；
  - `clipaths/`：`CliPaths`（私有 Node `<nodeRoot>/current/bin` 优先发现 + 跨平台可执行探测 + 代理/CA/npm 源环境变量构建）；
  - `gateway/`：`DirectCredentialGateway`（实现 `spi.ModelGateway`，通过窄接口 `CredentialResolver` 委托 L2 `CredentialService`，遵守 `L1` 不直接 `import L2` 的分层红线）；
  - `runner/`：`RunnerRegistry`、`ClaudeRunner`、`ClaudeStreamParser`、`ClaudeWarmPool`、`CodexRunner`、`CodexStreamParser`、`Tokens` 工具、以及面向增量插件的通用 `JSONLineRunner` 模板；
  - `mcp/`：`McpGrants`（48-hex Bearer、`CHAT`/`TASK`/`SUPER`、`StandIn`/`Release`、`WorkspaceFile` `EvalSymlinks` 沙箱防逃逸、`RequireDocAccess`、启动异步 `SelfCheck`）、`McpToolProvider` / `McpTools` / `McpToolResult` / `McpToolError`、`McpClient`（`STDIO`/`HTTP`/`SSE`，协议 `2025-06-18`）；
  - `attachment/`：`ImageAttachmentStore`（UUID 强校验、魔数嗅探 PNG/JPEG/GIF/WebP、5 图 / 5 MiB 限制、`StoredImage` 响应头）。
- **本篇不负责（留给后续篇）**：
  - V1 裁剪的 8 家非 GA CLI 适配器（`Glm`、`Gemini`、`DeepSeek`、`Qwen`、`Kimi`、`Minimax`、`CodeBuddy`、`GrokAcp`/`AcpClient`）不在 V1 编译交付，仅通过 `JSONLineRunner` 模板保留标准扩展模式；
  - `CredentialService` 密钥池持久化、私有 Node 下载器与供应商探测由 `SA-05`（`L2`）实现，通过 `CredentialResolver` 端口注入本篇 `DirectCredentialGateway`；
  - 20 个业务 `McpToolProvider` 实现分散在 `SA-06`/`SA-08`/`SA-09`/`SA-10`/`SA-11`/`SA-12`；
  - Gin HTTP 端点 `POST/GET /mcp` 与图片下载端点在 `SA-13`（`L5`）装配（本篇冻结其所调用的运行时核心引擎与 DTO/错误契约）。

### 1.3 Java 原版源码精确映射表

| 目标 Go 组件 | Java 参照源码（绝对路径均基于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`） | 核心行号范围 | V1 演进/修复要点 |
|---|---|---|---|
| `spi.AgentRunner`, `domain.AgentRunRequest`, `domain.AgentEvent`, `domain.RunCredential` | `superagent-core/src/main/java/com/superagent/core/chat/{AgentRunner.java,AgentRunRequest.java,AgentEvent.java,AgentEventType.java,AgentType.java,RunCredential.java,ImageAttachment.java,Cancellation.java}` | `AgentRunner.java:11-36`, `AgentRunRequest.java:18-90`, `AgentEvent.java:10-21`, `AgentEventType.java:8-39`, `RunCredential.java:9-15`, `Cancellation.java:11-35` | `Cancellation` 替换为 Go `context.Context`；新增 `RunnerCapabilities` 与 `StreamParser` SPI |
| `launcher.LocalLauncher` | 分散在各 Runner 的 `ProcessBuilder` / `process.destroyForcibly()` / `Files.createTempFile` | `ClaudeCliAgentRunner.java:114-121,204-222,430-460`, `ClaudeWarmSessions.java:362-379` | 统一抽象为 `spi.ProcessLauncher`；开启 `Setpgid` 整组查杀；强制 `0600` 挂载临时文件 |
| `runner.ClaudeRunner` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/ClaudeCliAgentRunner.java` | `L48-479`（`run:154-175`, `attempt:178-297`, `buildCommand:316-379`, `stdinMessage:382-398`, `writeRunSettings:410-433`） | 经 `ProcessLauncher` 启动；保留 `RetryResume` / `RetryNewConversation` 双自愈与 `unreportedTokens` |
| `runner.ClaudeStreamParser` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/ClaudeStreamParser.java` | `L28-251`（`parse:56-76`, `resultTokens:174-192`, `unreportedTokens:198-215`, `parseResult:223-250`） | 完整保真 `modelUsage`/`usage`/`assistant` 去重与 `permissionDenials` 120 字符截断 |
| `runner.ClaudeWarmPool` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/ClaudeWarmSessions.java` | `L48-385`（`run:78-112`, `keep:130-142`, `closeIdle:144-157`, `fingerprint:179-197`, `Kept.turn:242-329`, `sinceLastResult:332-348`, `Kept.close:362-379`） | Go `container/list`+`map` O(1) LRU + 30s ticker goroutine + 锁外整组关闭 + `obs` 指标上报 |
| `runner.CodexRunner` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/CodexCliAgentRunner.java` | `L51-335`（`run:86-161`, `buildCommand:163-219`, `mcpServers:235-255`, `prompt:270-276`, `readThreadId/recordThreadId:282-312`） | 经 `ProcessLauncher` 启动；保留 36 位 UUID 正则防参数注入与 `SUPERAGENT_MCP_TOKEN_*` 环境变量隔离 |
| `runner.CodexStreamParser` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/CodexStreamParser.java` | `L30-161`（`parse:44-92`, `addUsage/tokens:109-127`, `toolLabel:137-160`） | 完整保真 4 类工具标签 120 字符截断与 `FromTotalInput` 不重叠拆桶 |
| `runner.JSONLineRunner` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/JsonLineCliRunner.java` | `L34-272` | 通用扩展基座模板（面向 V1.x/V2 增量接入 DeepSeek/Qwen/Kimi/Gemini 等） |
| `runner.Tokens` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/Tokens.java` | `L13-53` | 完整保真 `Emit`/`Parse`/`FromTotalInput`/`WithModel` |
| `clipaths.CliPaths` | `superagent-runtime/src/main/java/com/superagent/runtime/cli/CliPaths.java` | `L22-113` | 私有 Node 优先 + 新增企业代理、`NODE_EXTRA_CA_CERTS`、`NPM_CONFIG_REGISTRY` 注入 |
| `mcp.McpGrants`, `McpToolProvider`, `McpTools` | `superagent-runtime/src/main/java/com/superagent/runtime/mcp/{McpGrants.java,McpToolProvider.java,McpTools.java,McpToolResult.java,McpToolException.java}`；`superagent-web/src/main/java/com/superagent/web/mcp/McpController.java` | `McpGrants.java:49-390`, `McpToolProvider.java:14-30`, `McpTools.java:9-84`, `McpController.java:35-144` | `filepath.EvalSymlinks` 沙箱校验；`McpToolError` + `apperror` 统一转 `isError: true` |
| `mcp.McpClient` | `superagent-runtime/src/main/java/com/superagent/runtime/mcp/McpClient.java` | `L36-397`（`converse:69-91`, `stdio:123-205`, `streamableHttp:209-266`, `sse:270-367`） | `STDIO` 统一经 `ProcessLauncher` 启动；支持 `2025-06-18` + `Mcp-Session-Id` + 代理/CA TLS |
| `attachment.ImageAttachmentStore` | `superagent-runtime/src/main/java/com/superagent/runtime/attachment/{ImageAttachmentStore.java,AttachmentNotFoundException.java}` | `ImageAttachmentStore.java:32-216` | 严格 UUID 校验 + 4 格式魔数嗅探 + `tenant.Path` 隔离 |

---

## 2. 本篇交付物表

| # | 交付物 | 落位（相对 `synapse-server/`） | 类型 |
|---|---|---|---|
| 1 | Runtime 核心 SPI 与领域模型补全（`ProcessLauncher`/`AgentRunner`/`StreamParser`/`ModelGateway`/`AgentRunRequest`/`RunCredential`） | `internal/core/spi/runtime.go`、`internal/core/domain/runtime.go` | 新增/补充 |
| 2 | `CliPaths` 可执行发现与企业代理/CA/npm 环境注入 | `internal/runtime/clipaths/clipaths.go` | 新增 |
| 3 | `LocalLauncher` 进程启动器（`os/exec` + `Setpgid` 整组查杀 + `0600` 临时文件） | `internal/runtime/launcher/local.go`、`proc_unix.go`、`proc_windows.go` | 新增 |
| 4 | `DirectCredentialGateway`（V1 `ModelGateway` 实现 + `CredentialResolver` 反转端口） | `internal/runtime/gateway/direct.go` | 新增 |
| 5 | `RunnerRegistry`、`Tokens` 工具与通用扩展模板 `JSONLineRunner` | `internal/runtime/runner/registry.go`、`tokens.go`、`jsonline.go` | 新增 |
| 6 | `ClaudeRunner` + `ClaudeStreamParser` + `ClaudeWarmPool`（LRU + 指纹 + StandIn + 差分计量） | `internal/runtime/runner/claude.go`、`claude_parser.go`、`claude_warm.go` | 新增 |
| 7 | `CodexRunner` + `CodexStreamParser`（UUID 会话校验 + TOML `-c` MCP + `--image=`） | `internal/runtime/runner/codex.go`、`codex_parser.go` | 新增 |
| 8 | `McpGrants` + `McpToolProvider`/`McpTools`/`McpToolError` + `McpDispatcher`（供 L5 `/mcp` 复用） | `internal/runtime/mcp/grants.go`、`provider.go`、`tools.go`、`dispatcher.go` | 新增 |
| 9 | `McpClient` 外部 MCP 工具发现客户端（`STDIO` / Streamable `HTTP` / Legacy `SSE`） | `internal/runtime/mcp/client.go` | 新增 |
| 10 | `ImageAttachmentStore`（UUID + 魔数嗅探 + 5 图 / 5 MiB + `StoredImage`） | `internal/runtime/attachment/image_store.go` | 新增 |
| 11 | 同包单元测试套件（Mock CLI 脚本 / Fake `ProcessLauncher` / `httptest` MCP 桩） | `internal/runtime/{clipaths,launcher,gateway,runner,mcp,attachment}/*_test.go` | 新增 |

> **分层守卫检查**：`internal/runtime/**` 只允许导入 `internal/core/**` 与 Go 标准库，**严禁导入** `internal/storage`、`internal/platform`、`internal/agent`、`internal/task`、`internal/web`（由 `internal/arch_test.go` 强制门禁）。

---

## 3. 数据库设计

本篇属于 L1 执行运行时层，**不新增 PostgreSQL 17 表或迁移脚本**。所有持久化交互遵循以下运行时契约与本地磁盘布局规范：

### 3.1 内存态与磁盘态存储契约

| 组件 | 存储介质 | 路径 / 结构 | 权限与生命周期 | 租户隔离方式 |
|---|---|---|---|---|
| `McpGrants` | 进程内存 | `sync.RWMutex` 保护 `grants[token]`、`companies[token]`、`standIns[standing]` | 随单回合 `Open*` 签发、`defer Close` 确定性吊销；无磁盘落地 | 每个 Grant 强绑定 `companyID = tenant.Require(ctx)` |
| `ClaudeWarmPool` | 进程内存 | `sync.Mutex` 保护 `container/list` + `map[sessionID]*list.Element`（LRU 上限默认 `20`，空闲超时 `10m`） | 空闲 `30s` 周期清扫；停机 `CloseAll()` 优雅退出；不跨重启复用 | 指纹包含 `workspaceDir`（已按租户隔离）与加密密钥哈希 |
| 临时配置文件（`--settings` / `--mcp-config`） | OS 临时目录 | `superagent-settings-*.json`、`superagent-mcp-*.json` | 强制 `0600`（仅属主可读，防同机泄露 API Key / Bearer Token）；`ProcessHandle.Close()` 确定性删除 | 每回合独立临时文件，不写入工作区 |
| `CodexRunner` 线程文件 | 工作区磁盘 | `<workspaceDir>/.superagent/codex-sessions/<sessionId>` | `0600` 文件；读写均强制正则匹配 36 位 UUID，阻断工作区篡改导致的 argv 注入 | 继承租户级 `workspaceDir` |
| `ImageAttachmentStore` | 独立附件目录 | `<attachments.dir>/<companyId>/<ownerId>/<messageId>/<n>.<ext>` | 独立于 Agent 工作区防篡改；`ownerId`/`messageId` 严格 36 位 UUID 校验；不可变写、无自动 TTL 清理 | `tenant.Path(ctx, root)` 强校验 `companyId`（拒绝 `..`） |

### 3.2 与 PG17 `agent_event` / `token_usage` 消费边界

- 运行时产生的 `domain.AgentEvent` 通过同步回调 `spi.EventSink` 流式递交给上层（`SA-09` `ChatService`、`SA-10` `TaskService`、`SA-05` `ModelCall`），由上层补充 `MessageID` 并决定落库 `agent_event`（PG17 `BIGSERIAL` 主键）或由 `UsageRecorder.Tap` 拦截 `TOKENS` 写入 `token_usage`（**绝不将 `TOKENS` 透传到 SSE 或写入 `agent_event`**）。

---

## 4. 冻结契约

### 4.1 导出符号（完整 Go Structs & Interfaces）

#### 4.1.1 核心领域模型与 SPI（`internal/core/domain/runtime.go`、`internal/core/spi/runtime.go`）

```go
package domain

import "time"

type AgentType string

const (
	AgentTypeClaude AgentType = "CLAUDE"
	AgentTypeCodex  AgentType = "CODEX"
	// 扩展预留：后续增量插件直接声明新常量并注册到 RunnerRegistry，无需修改核心逻辑
)

func ParseAgentType(raw string) (AgentType, error) // 空串默认 CLAUDE；大小写不敏感校验

func (t AgentType) CLIName() string // CLAUDE -> "claude", CODEX -> "codex"

type AgentEventType string

const (
	EventSystem     AgentEventType = "SYSTEM"
	EventThinking   AgentEventType = "THINKING"
	EventText       AgentEventType = "TEXT"
	EventToolUse    AgentEventType = "TOOL_USE"
	EventToolResult AgentEventType = "TOOL_RESULT"
	EventResult     AgentEventType = "RESULT"
	EventUsage      AgentEventType = "USAGE"
	EventTokens     AgentEventType = "TOKENS"
	EventError      AgentEventType = "ERROR"
)

type AgentEvent struct {
	SessionID string         `json:"sessionId"`
	MessageID string         `json:"messageId,omitempty"`
	Seq       int64          `json:"seq"`
	Type      AgentEventType `json:"type"`
	Payload   string         `json:"payload"`
	CreatedAt time.Time      `json:"createdAt"`
}

type ImageAttachment struct {
	Path      string `json:"path"`      // 磁盘绝对路径
	MediaType string `json:"mediaType"` // image/png | image/jpeg | image/gif | image/webp
}

type ImageUpload struct {
	MediaType string `json:"mediaType,omitempty"` // 忽略客户端声明，以魔数嗅探为准
	Data      string `json:"data"`                // 原始标准 Base64（不含 data: 前缀）
}

type RunCredential struct {
	ID      string // ProviderCredential.ID，供故障归因与 failover
	BaseURL string // 空串表示厂商默认端点
	APIKey  string // 绝不打印、绝不入 argv
	Bearer  bool   // true: ANTHROPIC_AUTH_TOKEN / Bearer；false: ANTHROPIC_API_KEY
}

func (c RunCredential) String() string // 脱敏返回 "RunCredential[<id> <baseUrl or 'default endpoint'>]"

type TokenUsage struct {
	Model        string   `json:"model,omitempty"`
	Input        int64    `json:"input"`
	CacheRead    int64    `json:"cacheRead"`
	CacheWrite   int64    `json:"cacheWrite"`
	CacheWrite1h int64    `json:"cacheWrite1h"`
	Output       int64    `json:"output"`
	Reasoning    int64    `json:"reasoning"`
	CLICostUSD   *float64 `json:"cliCostUsd,omitempty"`
}

func (u TokenUsage) Total() int64       { return u.Input + u.CacheRead + u.CacheWrite + u.Output }
func (u TokenUsage) NothingUsed() bool  { return u.Total() == 0 }

type ConnectorTransport string

const (
	TransportStdio ConnectorTransport = "STDIO"
	TransportHTTP  ConnectorTransport = "HTTP"
	TransportSSE   ConnectorTransport = "SSE"
)

type McpServerSpec struct {
	Name      string
	Transport ConnectorTransport
	URL       string
	Headers   map[string]string
	Command   string
	Args      []string
	Env       map[string]string
}

type AgentRunRequest struct {
	CompanyID       string
	OwnerUserID     string // 供二期沙箱路由 sa-<company>-<user12>
	SessionID       string
	AgentSessionID  string // 首轮为空；续跑为上次 SYSTEM 事件产出的外部 ID
	WorkspaceDir    string
	Prompt          string
	Model           string
	AllowedTools    []string
	SkipPermissions bool
	MCPConfig       string // Claude 形态 {"mcpServers": {...}}
	SystemPrompt    string
	Images          []ImageAttachment
	Memory          string // 仅在全新会话（首轮或降级重开）时拼入 systemPromptWithMemory
	Credential      *RunCredential
}

func (r AgentRunRequest) IsFirstTurn() bool
func (r AgentRunRequest) WithAgentSessionID(other string) AgentRunRequest
func (r AgentRunRequest) WithCredential(cred *RunCredential) AgentRunRequest
func (r AgentRunRequest) SystemPromptWithMemory() string
```

```go
package spi

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/domain"
)

// --- 1. ProcessLauncher: 面向二期沙箱隔离的进程启动抽象 ---

type TempFileSpec struct {
	Key     string      // 逻辑名（如 "settings" / "mcp-config"），用于模板引用或调试日志
	Prefix  string      // 如 "superagent-settings-"
	Suffix  string      // 如 ".json"
	Content []byte
	Mode    os.FileMode // 缺省强制 0600
}

type ProcessSpec struct {
	CompanyID    string
	OwnerUserID  string
	Command      string
	Args         []string
	WorkDir      string
	Env          map[string]string
	StripEnvKeys []string
	MountFiles   []TempFileSpec // 由 Launcher 在启动前创建 0600 临时文件，可通过 ArgMountPlaceholders 替换路径
	// ArgMountPlaceholders: 若 Args 中含 "${mount:<Key>}"，LocalLauncher 自动替换为实际临时文件绝对路径
	DiscardStderr bool
	MergeStderr   bool // true 时 Stderr 合并至 Stdout（如 npm/probe 场景）
}

type ProcessHandle interface {
	PID() int
	Alive() bool
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Stderr() io.ReadCloser // MergeStderr 或 DiscardStderr 时为 nil
	Wait() (exitCode int, err error)
	Terminate(grace time.Duration) error // 关闭 stdin -> SIGTERM 整组 -> grace 后 SIGKILL 整组
	Kill() error                         // 立即 SIGKILL 整个进程组 (-pgid)
	Close() error                        // 幂等清理全部 MountFiles 与句柄
}

type ProcessLauncher interface {
	Mode() string // V1: "local"；V2: "docker" | "gvisor" | "kata" | "worker"
	Start(ctx context.Context, spec ProcessSpec) (ProcessHandle, error)
}

// --- 2. AgentRunner / StreamParser / RunnerCapabilities ---

type RunnerCapabilities struct {
	Type            domain.AgentType `json:"type"`
	CLIName         string           `json:"cliName"`
	SupportImages   bool             `json:"supportImages"`
	SupportStdioMCP bool             `json:"supportStdioMcp"`
	SupportWarmPool bool             `json:"supportWarmPool"`
	Maturity        string           `json:"maturity"` // "ga" | "beta" | "experimental"
}

type EventSink func(ev domain.AgentEvent)

type AgentRunner interface {
	Type() domain.AgentType
	Capabilities() RunnerCapabilities
	Run(ctx context.Context, req domain.AgentRunRequest, sink EventSink) error
	RunChat(ctx context.Context, req domain.AgentRunRequest, sink EventSink) error
}

type StreamParser interface {
	ParseLine(line []byte) ([]domain.AgentEvent, error)
	Finish() []domain.AgentEvent
	Usage(fallbackModel string) (tokens []domain.TokenUsage, cliCostUSD *float64)
}

// --- 3. ModelGateway: 面向二期集中式代理网关的凭据抽象 ---

type ResolvedEndpoint struct {
	CredentialID string
	BaseURL      string
	APIKey       string
	Bearer       bool
	ProxyMode    bool // V1: false；V2 集中代理网关开启后 true
}

func (e *ResolvedEndpoint) ToRunCredential() *domain.RunCredential

type ModelGateway interface {
	Resolve(ctx context.Context, providerKey string, excludeCredID string) (*ResolvedEndpoint, error)
	ReportEndpointFailure(ctx context.Context, providerKey string, ep *ResolvedEndpoint, reason string) (*ResolvedEndpoint, error)
}
```

#### 4.1.2 运行时组件导出签名（`internal/runtime/*`）

```go
// internal/runtime/clipaths
type NetworkOptions struct {
	HTTPProxy    string
	HTTPSProxy   string
	NoProxy      string
	CABundleFile string
	NPMRegistry  string
}
type CliPaths struct { /* nodeRoot string, net NetworkOptions */ }
func New(nodeDir string, net NetworkOptions) *CliPaths
func (c *CliPaths) NodeRoot() string
func (c *CliPaths) PrivateNodeBin() string
func (c *CliPaths) SearchPath() []string
func (c *CliPaths) Find(cli string) (string, bool)
func (c *CliPaths) BuildEnv(base []string, extra map[string]string, strip []string) []string

// internal/runtime/launcher
type LocalLauncher struct { /* paths *clipaths.CliPaths */ }
func NewLocalLauncher(paths *clipaths.CliPaths) *LocalLauncher

// internal/runtime/gateway
// 依赖反转端口：由 SA-05 的 CredentialService 实现并在 wire.go 注入，避免 L1 import L2
type CredentialResolver interface {
	ResolveCurrent(ctx context.Context, providerKey string, excludeCredID string) (*domain.RunCredential, error)
	Failover(ctx context.Context, providerKey string, used *domain.RunCredential, errMsg string) (*domain.RunCredential, bool, error)
}
type DirectCredentialGateway struct { /* resolver CredentialResolver */ }
func NewDirectCredentialGateway(resolver CredentialResolver) *DirectCredentialGateway

// internal/runtime/runner
type Registry struct { /* mu sync.RWMutex; runners map[domain.AgentType]spi.AgentRunner */ }
func NewRegistry() *Registry
func (r *Registry) Register(runner spi.AgentRunner) error
func (r *Registry) Get(t domain.AgentType) (spi.AgentRunner, bool)
func (r *Registry) Capabilities() []spi.RunnerCapabilities

type ClaudeConfig struct {
	Command        string        // 默认 "claude"
	Timeout        time.Duration // 默认 600s
	WarmEnabled    bool          // 默认 true
	WarmIdle       time.Duration // 默认 10m
	WarmMax        int           // 默认 20 (>= 1)
}
func NewClaudeRunner(cfg ClaudeConfig, launcher spi.ProcessLauncher, grants *mcp.McpGrants) *ClaudeRunner
func (r *ClaudeRunner) Close() // 停机调用 warmPool.CloseAll()

type CodexConfig struct {
	Command string        // 默认 "codex"
	Timeout time.Duration // 默认 600s
}
func NewCodexRunner(cfg CodexConfig, launcher spi.ProcessLauncher) *CodexRunner

// internal/runtime/mcp
type GrantKind string
const (
	GrantChat  GrantKind = "CHAT"
	GrantTask  GrantKind = "TASK"
	GrantSuper GrantKind = "SUPER"
)
type Grant struct {
	Token, Owner, TaskID, AgentID, SessionID, MessageID string
	Kind                                                GrantKind
	Workspace                                           string
}
type SelfStatus struct {
	State     string     `json:"state"` // "unchecked" | "ok" | "error"
	Error     string     `json:"error,omitempty"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}
type McpToolResult struct {
	Text  string
	Error bool
}
type McpToolError struct { Message string }
type ToolDef map[string]any
type McpToolProvider interface {
	Kinds() []GrantKind
	Tools() []ToolDef
	Handles(name string) bool
	Call(ctx context.Context, grant *Grant, name string, args map[string]any) (McpToolResult, error)
}
type McpGrants struct { /* ... */ }
func NewMcpGrants(selfURL string, port int) *McpGrants
func (g *McpGrants) SetBoundPort(port int)
func (g *McpGrants) SelfCheckAsync(ctx context.Context)
func (g *McpGrants) SelfURL() string
func (g *McpGrants) SelfStatus() SelfStatus
func (g *McpGrants) OpenChat(ctx context.Context, owner, agentID, sessionID, messageID, workspaceDir string) (*Grant, error)
func (g *McpGrants) OpenTask(ctx context.Context, owner, taskID, workspaceDir string) (*Grant, error)
func (g *McpGrants) OpenSuper(ctx context.Context, owner, agentID, sessionID, messageID, workspaceDir string) (*Grant, error)
func (g *McpGrants) Close(grant *Grant)
func (g *McpGrants) Find(token string) (*Grant, bool)
func (g *McpGrants) CompanyOf(grant *Grant) (string, error)
func (g *McpGrants) StandIn(standing, current string)
func (g *McpGrants) Release(standing string)
func (g *McpGrants) McpConfig(grant *Grant) string
func (g *McpGrants) WorkspaceFile(grant *Grant, file string) (string, error)
func WithServers(baseConfig string, servers []domain.McpServerSpec) string
func TokenIn(mcpConfig string) string
func RequireDocAccess(grant *Grant, tool string) error

// McpDispatcher: 无 HTTP 框架依赖的协议调度器，由 L5 internal/web/mcp 薄壳调用
type McpDispatcher struct { /* grants *McpGrants; providers []McpToolProvider */ }
func NewMcpDispatcher(grants *McpGrants, providers ...McpToolProvider) *McpDispatcher
func (d *McpDispatcher) HandleRPC(ctx context.Context, bearerToken string, rawBody []byte) (statusCode int, respBody []byte)

// McpClient: 外部 MCP 只读 tools/list 客户端
type McpClient struct { /* launcher spi.ProcessLauncher; httpClient *http.Client */ }
func NewMcpClient(launcher spi.ProcessLauncher, httpClient *http.Client) *McpClient
func (c *McpClient) ListTools(ctx context.Context, spec domain.McpServerSpec, timeout time.Duration) ([]map[string]any, error)

// internal/runtime/attachment
const (
	MaxImages       = 5
	MaxBytes        = 5 * 1024 * 1024 // 5 MiB
	ImageOnlyPrompt = "See the attached image(s)."
)
type StoredImage struct {
	Bytes     []byte
	MediaType string
}
type ImageAttachmentStore struct { /* root string */ }
func NewImageAttachmentStore(dir string) *ImageAttachmentStore
func (s *ImageAttachmentStore) Save(ctx context.Context, ownerID, messageID string, uploads []domain.ImageUpload) ([]domain.ImageAttachment, error)
func (s *ImageAttachmentStore) List(ctx context.Context, ownerID, messageID string) ([]domain.ImageAttachment, error)
func (s *ImageAttachmentStore) Read(ctx context.Context, ownerID, messageID string, index int) (*StoredImage, error)
func (s *ImageAttachmentStore) URLs(ctx context.Context, basePath, ownerID, messageID string) ([]string, error)
func Sniff(b []byte) (mediaType string, ok bool)
func Extension(mediaType string) string
```

### 4.2 路由契约（与 L5 `SA-13` 的协议对接）

本篇交付 `McpDispatcher` 与 `ImageAttachmentStore`，L5（`SA-13`）直接挂载以下路由并透传：

| 路径 | 方法 | 鉴权 | 响应规格 |
|---|---|---|---|
| `/mcp` | `GET` | 无（公开路由白名单） | 固定返回 `405 Method Not Allowed`（无服务端推送 SSE） |
| `/mcp` | `POST` | `Authorization: Bearer <48-hex>` 走 `McpGrants.Find`（无效返回 `401`） | JSON-RPC 2.0 `application/json`；无 `id`/`id: null` 的通知返回 `202 Accepted` 空体；协议版本 `"2024-11-05"`、`serverInfo: {"name":"superagent","version":"0.1.0"}` |
| `/api/v1/sessions/:id/messages/:messageId/images/:index` | `GET` | 登录态 + `ChatService.RequireAccess` | `200 OK`、`Content-Type: <sniffed>`、`X-Content-Type-Options: nosniff`、`Cache-Control: max-age=2592000, private` |
| `/api/v1/tasks/:id/messages/:messageId/images/:index` | `GET` | 登录态 + `TaskService` 归属校验 | 同上 |

### 4.3 错误语义

| 场景 | 错误类型 / 返回码 | 精确文案 / 行为 |
|---|---|---|
| `/mcp` 缺少或无效 Bearer Token | HTTP `401 Unauthorized` | 空体（对齐 `McpController.java:60-62`） |
| `/mcp` 未知 JSON-RPC 方法 | HTTP `200` JSON-RPC `error.code = -32601` | `"Method not found: <method>"` |
| `/mcp` 工具抛出 `*McpToolError` 或已知领域错误（`apperror.InvalidArg`/`NotFound`/`Forbidden`/`Conflict`/`Busy`） | HTTP `200` JSON-RPC `result` + `"isError": true` | `content[0].text = "错误：" + err.Error()`（**V1 修复原版 `TaskMcpTools` 漏捕领域异常导致返回 `-32603` 的缺陷**） |
| `/mcp` 未预期内部错误 / panic | HTTP `200` JSON-RPC `error.code = -32603` | `"Internal error: <msg>"` |
| `RequireDocAccess` 越权（`SUPER` 后台 / `SUPER` 写文档） | `*McpToolError` | 后台：`"后台处理不能读写用户的文档。"`；写文档：`"SuperAgent 只读文档；新建或修改文档请用 start_task 交给成员去做。"` |
| `WorkspaceFile` 越界 / 符号链接逃逸 / 非常规文件 | `apperror.InvalidArg`（调用方可包为 `*McpToolError`） | `"Not a file in the task workspace: <file>"` / `"No such file in the task workspace: <file>"` |
| `ImageAttachmentStore.Save` 超限或非法魔数 | `apperror.InvalidArg`（HTTP `400`） | 超 5 张：`"At most 5 images per message"`；非法 base64：`"Image is not valid base64"`；超 5 MiB：`"Image exceeds 5 MB"`；非法格式：`"Only PNG, JPEG, GIF and WebP images are supported"` |
| `ImageAttachmentStore.Read` 索引越界或非法 UUID | `apperror.NotFound`（HTTP `404`） | 对应原版 `AttachmentNotFoundException` |
| `McpClient` 上游 401/403 或超时 | `error` | 401/403：`"授权无效或已过期，请重新授权"`；超时：`"MCP 服务没有及时响应"`；STDIO 早退：`"MCP 服务启动后就退出了（退出码 <code>）"` |

### 4.4 协议与进程安全契约

1. **禁止密钥入参**：所有模型 API Key 与 MCP Bearer Token 仅允许通过子进程环境变量或 `0600` 临时文件（`--settings` / `--mcp-config`）传递，**严禁出现在 `argv` 与日志中**。
2. **禁止 Shell 注入**：`LocalLauncher` 仅调用 `exec.Command(resolvedBin, args...)`，绝不经 `sh -c` / `cmd /c`。
3. **强制整组终止**：Unix 平台一律 `SysProcAttr{Setpgid: true}`，取消/超时时先对 `-pgid` 发 `SIGTERM` 或直接 `SIGKILL`，杜绝孤儿子进程泄漏。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 `CliPaths`：私有 Node 优先与企业网络环境注入

- **Java 参考**：`CliPaths.java:24-112`（`EXTRA_BIN_DIRS`, `searchPath`, `candidates`, `find`, `apply`）
- **V1 增强**：在完整保真私有 Node 优先顺序的基础上，增加企业代理（`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`）、自定义根证书（`NODE_EXTRA_CA_CERTS`）及私有 npm 镜像源（`NPM_CONFIG_REGISTRY`）自动叠加（仅当配置非空且子进程 `extra` 未显式覆盖时注入）。

```go
package clipaths

var extraBinDirs = func() []string {
	home, _ := os.UserHomeDir()
	return []string{
		"/opt/homebrew/bin",
		"/usr/local/bin",
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, ".local", "bin"),
	}
}()

// SearchPath 顺序（保序去重）：1) <nodeRoot>/current/bin  2) 宿主机 PATH  3) extraBinDirs
func (c *CliPaths) SearchPath() []string {
	seen := make(map[string]struct{})
	var out []string
	add := func(d string) {
		d = strings.TrimSpace(d)
		if d == "" { return }
		if _, ok := seen[d]; !ok {
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	add(c.PrivateNodeBin())
	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		add(d)
	}
	for _, d := range extraBinDirs {
		add(d)
	}
	return out
}

// Find 支持 Windows (.exe/.cmd/.bat/bare) 与 Unix (bare) 差异，以及含路径分隔符的直接路径
func (c *CliPaths) Find(cli string) (string, bool) {
	cli = strings.TrimSpace(cli)
	if cli == "" { return "", false }
	candidates := []string{cli}
	if runtime.GOOS == "windows" {
		candidates = []string{cli + ".exe", cli + ".cmd", cli + ".bat", cli}
	}
	check := func(dir, base string) (string, bool) {
		for _, name := range candidates {
			p := filepath.Join(dir, name)
			if isExecutableFile(p) { return p, true }
		}
		return "", false
	}
	if strings.ContainsAny(cli, `/\`) {
		dir := filepath.Dir(cli)
		return check(dir, filepath.Base(cli))
	}
	for _, dir := range c.SearchPath() {
		if p, ok := check(dir, cli); ok { return p, true }
	}
	return "", false
}

// BuildEnv 合并宿主机环境 -> 清除 strip -> 写入 PATH / 企业代理 / CA / npm registry -> 写入 extra
func (c *CliPaths) BuildEnv(base []string, extra map[string]string, strip []string) []string {
	env := envSliceToMap(base)
	for _, k := range strip { delete(env, k) }
	env["PATH"] = strings.Join(c.SearchPath(), string(os.PathListSeparator))
	setIfNotEmpty := func(k, v string) {
		if strings.TrimSpace(v) != "" { env[k] = strings.TrimSpace(v) }
	}
	setIfNotEmpty("HTTP_PROXY", c.net.HTTPProxy)
	setIfNotEmpty("http_proxy", c.net.HTTPProxy)
	setIfNotEmpty("HTTPS_PROXY", c.net.HTTPSProxy)
	setIfNotEmpty("https_proxy", c.net.HTTPSProxy)
	setIfNotEmpty("NO_PROXY", c.net.NoProxy)
	setIfNotEmpty("no_proxy", c.net.NoProxy)
	setIfNotEmpty("NODE_EXTRA_CA_CERTS", c.net.CABundleFile)
	setIfNotEmpty("NPM_CONFIG_REGISTRY", c.net.NPMRegistry)
	for k, v := range extra { env[k] = v } // 调用方显式指定优先
	return envMapToSlice(env)
}
```

### 5.2 `LocalLauncher`：进程组隔离、0600 临时文件与取消联动

- **设计要点**：
  1. 启动前为每个 `spec.MountFiles` 创建临时文件，强制 `os.Chmod(f, 0600)` 后写入内容；若任一创建或 `cmd.Start()` 失败，立即清理已创建的全部临时文件，绝不泄漏敏感凭据；
  2. 将 `spec.Args` 中的 `${mount:<Key>}` 占位符替换为真实临时文件路径；
  3. 通过 `CliPaths.Find(spec.Command)` 解析二进制绝对路径，找不到则保留原名让 `cmd.Start()` 返回明确 OS 错误；
  4. Unix 下通过 `setProcGroup(cmd)` 设置 `cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}`；
  5. 启动独立 watch goroutine 监听 `<-ctx.Done()`：若 `ctx` 取消且进程仍存活，立即调用 `handle.Kill()`（对 `-pgid` 发 `SIGKILL`），保证取消语义无竞态（取代 Java `Cancellation.onCancel` 的锁同步）；
  6. `Wait()` 只调用一次 `cmd.Wait()` 并缓存结果，同时通过 `close(doneCh)` 通知 watch goroutine 退出，防止 goroutine 泄漏。

```go
package launcher

func (l *LocalLauncher) Start(ctx context.Context, spec spi.ProcessSpec) (spi.ProcessHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 1. 创建 0600 临时文件
	mounted := make(map[string]string, len(spec.MountFiles))
	var createdPaths []string
	cleanupFiles := func() {
		for _, p := range createdPaths { _ = os.Remove(p) }
	}
	for _, mf := range spec.MountFiles {
		mode := mf.Mode
		if mode == 0 { mode = 0o600 }
		f, err := os.CreateTemp("", mf.Prefix+"*"+mf.Suffix)
		if err != nil { cleanupFiles(); return nil, fmt.Errorf("create temp %s: %w", mf.Key, err) }
		p := f.Name()
		createdPaths = append(createdPaths, p)
		if err := f.Chmod(mode); err != nil { _ = f.Close(); cleanupFiles(); return nil, err }
		if _, err := f.Write(mf.Content); err != nil { _ = f.Close(); cleanupFiles(); return nil, err }
		if err := f.Close(); err != nil { cleanupFiles(); return nil, err }
		mounted[mf.Key] = p
	}

	// 2. 替换占位符并解析可执行文件
	args := make([]string, len(spec.Args))
	for i, a := range spec.Args {
		if strings.HasPrefix(a, "${mount:") && strings.HasSuffix(a, "}") {
			key := a[len("${mount:") : len(a)-1]
			if path, ok := mounted[key]; ok { a = path }
		}
		args[i] = a
	}
	bin := spec.Command
	if l.paths != nil {
		if found, ok := l.paths.Find(bin); ok { bin = found }
	}
	cmd := exec.Command(bin, args...)
	cmd.Dir = spec.WorkDir
	if l.paths != nil {
		cmd.Env = l.paths.BuildEnv(os.Environ(), spec.Env, spec.StripEnvKeys)
	} else {
		cmd.Env = mergeEnv(os.Environ(), spec.Env, spec.StripEnvKeys)
	}
	setProcGroup(cmd) // Unix: SysProcAttr{Setpgid: true}

	stdin, err := cmd.StdinPipe()
	if err != nil { cleanupFiles(); return nil, err }
	stdout, err := cmd.StdoutPipe()
	if err != nil { cleanupFiles(); return nil, err }
	var stderr io.ReadCloser
	if spec.MergeStderr {
		cmd.Stderr = cmd.Stdout
	} else if spec.DiscardStderr {
		cmd.Stderr = io.Discard
	} else {
		stderr, err = cmd.StderrPipe()
		if err != nil { cleanupFiles(); return nil, err }
	}
	if err := cmd.Start(); err != nil {
		cleanupFiles()
		return nil, err
	}

	h := &localHandle{
		cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr,
		tempFiles: createdPaths, done: make(chan struct{}),
	}
	// 3. 后台监听 ctx 取消，整组强杀（热进程池使用独立长生命周期 ctx，单回合取消改用 Terminate/Kill）
	go func() {
		select {
		case <-ctx.Done():
			_ = h.Kill()
		case <-h.done:
		}
	}()
	return h, nil
}

// Terminate: 优雅关闭 stdin -> SIGTERM(-pgid) -> grace 后 SIGKILL(-pgid)
func (h *localHandle) Terminate(grace time.Duration) error {
	_ = h.stdin.Close()
	if !h.Alive() { return nil }
	_ = signalProcGroup(h.cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-h.done:
		return nil
	case <-time.After(grace):
		return h.Kill()
	}
}

func (h *localHandle) Kill() error {
	if !h.Alive() { return nil }
	return killProcGroup(h.cmd.Process.Pid) // Unix: syscall.Kill(-pid, syscall.SIGKILL)
}
```

### 5.3 `DirectCredentialGateway`（V1 `ModelGateway` 实现）

- **设计要点**：实现 `spi.ModelGateway`，通过构造注入的 `CredentialResolver`（在 `wire.go` 中由 `SA-05` 的 `CredentialService` 实现）完成多租户密钥解析与零输出故障切换；无公司密钥时原样透传 `403 NoCompanyKeyError`，绝不回退宿主机环境变量。

```go
package gateway

func (g *DirectCredentialGateway) Resolve(ctx context.Context, providerKey, excludeCredID string) (*spi.ResolvedEndpoint, error) {
	cred, err := g.resolver.ResolveCurrent(ctx, providerKey, excludeCredID)
	if err != nil { return nil, err }
	if cred == nil { return nil, nil }
	return &spi.ResolvedEndpoint{
		CredentialID: cred.ID, BaseURL: cred.BaseURL, APIKey: cred.APIKey, Bearer: cred.Bearer, ProxyMode: false,
	}, nil
}

func (g *DirectCredentialGateway) ReportEndpointFailure(ctx context.Context, providerKey string, ep *spi.ResolvedEndpoint, reason string) (*spi.ResolvedEndpoint, error) {
	if ep == nil { return nil, nil }
	next, ok, err := g.resolver.Failover(ctx, providerKey, ep.ToRunCredential(), reason)
	if err != nil || !ok || next == nil { return nil, err }
	return &spi.ResolvedEndpoint{
		CredentialID: next.ID, BaseURL: next.BaseURL, APIKey: next.APIKey, Bearer: next.Bearer, ProxyMode: false,
	}, nil
}
```

### 5.4 `ClaudeRunner` + `ClaudeStreamParser`

- **Java 参考**：`ClaudeCliAgentRunner.java:133-478`、`ClaudeStreamParser.java:56-250`
- **关键行为保真清单**：
  1. **环境变量与 `--settings` 双重覆写**（`L133-151, 410-433`）：
     - 恒定注入 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1`；
     - `StripEnvKeys = ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL"]`（当 `req.Credential != nil` 时清除宿主机残留，防止 API Key 与 Auth Token 并存导致 Claude 优先选错）；
     - 只要 `req.Credential != nil || len(req.Images) > 0`，必须经 `ProcessSpec.MountFiles` 生成 `0600` 临时 `--settings` JSON：
       - 若有图片：`"enableArtifact": false`（规避 Claude 边缘节点报 `"request is not representable on this edge"`）；
       - 若有凭据：`"env": {"ANTHROPIC_BASE_URL": ..., "ANTHROPIC_API_KEY": <non-bearer ? key : "">, "ANTHROPIC_AUTH_TOKEN": <bearer ? key : "">}`（另一键显式置空字符串，压制宿主机 `~/.claude/settings.json`）；
     - 若生成 settings 失败，立即发 `ERROR` 中止，**绝不冒险降级继续**。
  2. **命令构造与防注入**（`L316-379`）：
     - 固定前缀：`claude -p --output-format stream-json --include-partial-messages --verbose`；
     - `--model <model>`（非空时）；
     - `--append-system-prompt <req.SystemPromptWithMemory() if firstTurn else req.SystemPrompt>`（非空时）；
     - `--mcp-config ${mount:mcp-config}`（若 `MCPConfig` 非空）+ 恒定 `--strict-mcp-config`；
     - 首轮 `--session-id <SessionID>`，续跑 `--resume <AgentSessionID>`；
     - `SkipPermissions` -> `--dangerously-skip-permissions`，否则若 `AllowedTools` 非空 -> `--allowedTools "<space-joined>"`；
     - `--settings ${mount:settings}`（若有）；
     - 若 `kept == true || len(req.Images) > 0` -> `--input-format stream-json`（消息经 stdin 发送）；否则追加 `-- <Prompt>` 防 `-`/`--` 参数注入。
  3. **图片 Base64 Stdin 消息**（`L382-398`）：构造单行 JSON `{"type":"user","message":{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"...","data":"..."}}, {"type":"text","text":"..."}]}}\n`。
  4. **双分支自愈状态机**（`L154-175, 242-282`）：
     - `retryResume`：首轮（`IsFirstTurn()`）非零退出、未 `concluded`、未取消、且 stderr 尾部包含 `"is already in use"` -> 自动以 `req.WithAgentSessionID(req.SessionID)` 切换 `--resume` 重试；
     - `retryNewConversation`：续跑（`!IsFirstTurn()`）收到 `ERROR` 事件且内容包含 `"No conversation found with session ID"` -> 抑制该错误，发出 `THINKING` 提示（`"Previous conversation for this session was not found; continuing in a new conversation (same session id, same workspace — earlier context is not available)."`），以 `req.WithAgentSessionID("")` 重开新会话（**自动重注 `Memory`**）。
  5. **权限拒绝与 Token 补记**（`L247-295`）：
     - 每条 `permission_denials` 发射 `TOOL_RESULT: "Permission denied: <tool>[: <detail<=120>]"`；
     - 收到 `result` 发 `USAGE`（`total_cost_usd`）和 `TOKENS`（优先 `modelUsage`，兜底 `usage`，缺省 model 填 `req.Model` 或 `"default"`）；
     - `defer` 中若未见 `result`（取消/超时/崩溃），仍发射 `parser.UnreportedTokens()`（按 `assistant` 帧 `message.id` 去重汇总），确保中断回合也精确计费。

```go
package runner

const (
	claudeSessionInUse  = "is already in use"
	claudeNoConversation = "No conversation found with session ID"
)

type claudeRetry int
const (
	retryNone claudeRetry = iota
	retryResume
	retryNewConversation
)

func (r *ClaudeRunner) Capabilities() spi.RunnerCapabilities {
	return spi.RunnerCapabilities{
		Type: domain.AgentTypeClaude, CLIName: "claude",
		SupportImages: true, SupportStdioMCP: true, SupportWarmPool: r.warm != nil, Maturity: "ga",
	}
}

func (r *ClaudeRunner) RunChat(ctx context.Context, req domain.AgentRunRequest, sink spi.EventSink) error {
	if r.warm != nil && r.warm.Run(ctx, req, sink) {
		return nil
	}
	return r.Run(ctx, req, sink)
}

func (r *ClaudeRunner) Run(ctx context.Context, req domain.AgentRunRequest, sink spi.EventSink) error {
	var seq atomic.Int64
	retry := r.attempt(ctx, req, sink, &seq)
	if retry == retryResume && ctx.Err() == nil {
		retry = r.attempt(ctx, req.WithAgentSessionID(req.SessionID), sink, &seq)
	}
	if retry == retryNewConversation && ctx.Err() == nil {
		sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventThinking,
			"Previous conversation for this session was not found; continuing in a new conversation "+
				"(same session id, same workspace — earlier context is not available)."))
		r.attempt(ctx, req.WithAgentSessionID(""), sink, &seq)
	}
	return nil
}

func (r *ClaudeRunner) attempt(ctx context.Context, req domain.AgentRunRequest, sink spi.EventSink, seq *atomic.Int64) claudeRetry {
	var stdinMsg []byte
	if len(req.Images) > 0 {
		var err error
		stdinMsg, err = buildClaudeStdinMessage(req)
		if err != nil {
			emitError(sink, req.SessionID, seq, "Cannot read attached image: "+err.Error())
			return retryNone
		}
	}
	spec, err := r.buildProcessSpec(req, false) // 内含 settings/mcp MountFiles (0600) + StripEnvKeys + args
	if err != nil {
		emitError(sink, req.SessionID, seq, "Cannot prepare the run's settings: "+err.Error())
		return retryNone
	}

	turnCtx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	proc, err := r.launcher.Start(turnCtx, spec)
	if err != nil {
		emitError(sink, req.SessionID, seq, "Failed to start agent: "+err.Error())
		return retryNone
	}
	defer proc.Close() // 清理 0600 临时文件

	if stdinMsg != nil {
		_, _ = proc.Stdin().Write(stdinMsg)
	}
	_ = proc.Stdin().Close()

	// 异步排空 stderr（2048 字节滚动尾缓冲）
	stderrTail := newRollingTail(2048)
	stderrDone := make(chan struct{})
	go func() { defer close(stderrDone); drainStream(proc.Stderr(), stderrTail) }()

	parser := NewClaudeStreamParser()
	defer func() {
		// 中断/超时/崩溃前已完成的 assistant 调用仍补记 Token
		EmitTokens(sink, req.SessionID, seq.Add(1)-1, WithModel(parser.UnreportedTokens(), req.Model))
	}()

	var conversationMissing, concluded bool
	scanner := newLargeLineScanner(proc.Stdout(), 16*1024*1024) // 单行上限 16 MiB 防长 tool_result 截断
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 { continue }
		ev, ok := parser.Parse(line)
		if !ok { continue }
		if ev.Type == domain.EventError && !req.IsFirstTurn() && strings.Contains(ev.Payload, claudeNoConversation) {
			conversationMissing = true
			continue
		}
		for _, denial := range ev.PermissionDenials {
			sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventToolResult, "Permission denied: "+denial))
		}
		sink(newEvent(req.SessionID, seq.Add(1)-1, ev.Type, ev.Payload))
		if ev.Type == domain.EventResult || ev.Type == domain.EventError { concluded = true }
		if ev.CostUSD != nil {
			sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventUsage, strconv.FormatFloat(*ev.CostUSD, 'f', -1, 64)))
		}
		if len(ev.Tokens) > 0 {
			EmitTokens(sink, req.SessionID, seq.Add(1)-1, WithModel(ev.Tokens, req.Model))
		}
	}

	exitCode, waitErr := proc.Wait()
	if errors.Is(turnCtx.Err(), context.DeadlineExceeded) {
		emitError(sink, req.SessionID, seq, fmt.Sprintf("Agent timed out after %ds", int64(r.cfg.Timeout.Seconds())))
		return retryNone
	}
	if waitErr != nil && ctx.Err() == nil && !concluded && !conversationMissing {
		// 等待 stderr 排空（最多 2s）
		select { case <-stderrDone: case <-time.After(2 * time.Second): }
		why := stderrTail.String()
		if req.IsFirstTurn() && strings.Contains(why, claudeSessionInUse) {
			return retryResume
		}
		msg := fmt.Sprintf("claude exited with code %d", exitCode)
		if why != "" { msg += ": " + why }
		emitError(sink, req.SessionID, seq, msg)
	}
	if conversationMissing { return retryNewConversation }
	return retryNone
}
```

- **`ClaudeStreamParser` 精确解析契约**（对齐 `ClaudeStreamParser.java:56-250`）：
  - `type == "system" && subtype == "init"` → `EventSystem(session_id)`；
  - `type == "stream_event"`：`content_block_delta` + `text_delta` → `EventText`、`thinking_delta` → `EventThinking`；`content_block_start` + `tool_use` → `EventToolUse(name)`；
  - `type == "user"`：遍历 `message.content[]`，首条非空 `tool_result`（支持纯文本或 `[{type:"text",text:...}]` 数组拼接）→ `EventToolResult`；
  - `type == "assistant"`：以 `message.id` 为键记录最新 `message.usage` 与 `message.model`（覆写同 `message.id` 的多块重复帧），不发射事件；
  - `type == "result"`：置 `resultSeen = true`；若 `is_error == true && result == ""`，用 `"; "` 拼接 `errors[]`（再兜底 `subtype` 或 `"error"`）；提取 `total_cost_usd`、`permission_denials[]`（`tool_name` + 首选 `tool_input.command` / `tool_input.file_path` 截断 120 字符）及 `resultTokens()`（优先遍历 `modelUsage`，兜底 `usage` + `cache_creation.ephemeral_1h_input_tokens` + `output_tokens_details.thinking_tokens`）。

### 5.5 `ClaudeWarmPool`：Go 并发长驻热进程池（LRU + 8 维指纹 + StandIn + 差分计量）

- **Java 参考**：`ClaudeWarmSessions.java:48-385`
- **并发与状态设计**：
  1. **O(1) LRU**：`mu sync.Mutex` 保护 `lru *list.List`（队头最新、队尾最旧）与 `bySession map[string]*list.Element`；每次池容量变化同步更新 Prometheus 指标 `obs.CLIWarmPoolSize.Set(float64(p.lru.Len()))`；
  2. **执行期移出池**：`Run()` 从 `bySession` 取出并删除元素后再执行回合，确保绝无并发争抢、绝不被后台 `30s` 清扫误杀；
  3. **8 维 SHA-256 指纹**（`ClaudeWarmSessions.java:179-197`）：按固定换行分隔拼接 `workspaceDir`、`model`、`systemPrompt`（不含 `memory`）、`allowedTools`、`skipPermissions`、`len(images) > 0`、剥离自身 Bearer Token 的 `mcpConfig`、以及 `credential(baseUrl, bearer, apiKey)`，取 `sha256.Sum256` 十六进制，绝不明文留存 API Key；
  4. **Stand-In 令牌生命周期**（`L244-248, 320-322`）：热进程 `--mcp-config` 永久固化启动回合的 `standing` Token；每轮开始调用 `grants.StandIn(standing, current)`，`defer grants.Release(standing)`，空闲期调用 `/mcp` 一律 `401`；
  5. **三态 Outcome 与冷降级**（`L101-111, 270-328`）：
     - `outcomeNothing`：尚未产出任何非 `SYSTEM` 输出（`!said`）时收到 `NO_CONVERSATION`、EOF 或读取错误 -> 关闭该热进程并返回 `false`，由 `ClaudeRunner.RunChat` 无缝降级到冷启动 `Run()`（触发 `RetryResume`/`RetryNewConversation`）；
     - `outcomeDone`：收到 `RESULT` 或 `ERROR` -> 若 `kept.alive() && ctx.Err() == nil`，归还 LRU（队头插入），若超 `max` 则弹出队尾并在**锁外**优雅关闭 `Terminate(2*time.Second)` + `Close()`；
     - `outcomeBroken`：已产生输出后（`said == true`）发生超时/EOF/错误 -> 若未取消则发超时或异常 `ERROR`、`defer` 补记 `UnreportedTokens()`、关闭热进程，返回 `true`（不再冷重试避免重复输出）；
  6. **累积成本与 Token 差分减法**（`L282-290, 332-348`）：热进程每轮 `result` 返回进程启动以来的累计总额；`Kept` 维护 `reportedCost float64` 与 `reported map[string]domain.TokenUsage`，每轮取 `max(0, curr - prev)` 并剔除 `NothingUsed()` 的模型。

```go
package runner

type warmOutcome int
const (
	outcomeDone warmOutcome = iota
	outcomeNothing
	outcomeBroken
)

type ClaudeWarmPool struct {
	runner   *ClaudeRunner
	grants   *mcp.McpGrants
	idleTTL  time.Duration
	maxSize  int
	mu       sync.Mutex
	lru      *list.List
	bySess   map[string]*list.Element
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewClaudeWarmPool(runner *ClaudeRunner, grants *mcp.McpGrants, idle time.Duration, max int) *ClaudeWarmPool {
	if max < 1 { max = 1 }
	p := &ClaudeWarmPool{
		runner: runner, grants: grants, idleTTL: idle, maxSize: max,
		lru: list.New(), bySess: make(map[string]*list.Element), stopCh: make(chan struct{}),
	}
	go p.reaperLoop() // time.NewTicker(30 * time.Second)
	return p
}

func (p *ClaudeWarmPool) Run(ctx context.Context, req domain.AgentRunRequest, sink spi.EventSink) bool {
	fp := warmFingerprint(req)
	kept := p.take(req.SessionID)
	if kept != nil && (!kept.proc.Alive() || kept.fingerprint != fp) {
		kept.closeGraceful()
		kept = nil
	}
	if kept == nil {
		var err error
		kept, err = p.start(req, fp)
		if err != nil {
			return false // 启动失败无缝降级冷启动 Run()
		}
	}
	outcome := kept.turn(ctx, req, sink, p.grants, p.runner.cfg.Timeout)
	if outcome == outcomeNothing && ctx.Err() == nil {
		kept.closeGraceful()
		return false
	}
	if outcome == outcomeDone && kept.proc.Alive() && ctx.Err() == nil {
		p.putBack(req.SessionID, kept)
	} else {
		kept.closeGraceful()
	}
	return true
}

func (k *keptClaude) turn(ctx context.Context, req domain.AgentRunRequest, sink spi.EventSink, grants *mcp.McpGrants, timeout time.Duration) warmOutcome {
	var seq atomic.Int64
	currentTok := mcp.TokenIn(req.MCPConfig)
	if grants != nil && k.standingToken != "" && currentTok != "" {
		grants.StandIn(k.standingToken, currentTok)
		defer grants.Release(k.standingToken)
	}

	turnCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// 监控本轮取消或超时 -> 终止长驻进程以解除 out.Scan() 阻塞（不绑定到全局长生命周期）
	turnDone := make(chan struct{})
	defer close(turnDone)
	var timedOut atomic.Bool
	go func() {
		select {
		case <-turnCtx.Done():
			if errors.Is(turnCtx.Err(), context.DeadlineExceeded) { timedOut.Store(true) }
			k.closeGraceful()
		case <-turnDone:
		}
	}()

	payload, err := buildWarmUserMessage(req)
	if err != nil { return outcomeNothing }
	if _, err := k.proc.Stdin().Write(payload); err != nil { return outcomeNothing }

	if k.conversation != "" {
		// 复用进程已就绪：立即发 SYSTEM 终止上层冷启动计时
		sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventSystem, k.conversation))
	}

	parser := NewClaudeStreamParser()
	said := false
	defer func() {
		if said {
			EmitTokens(sink, req.SessionID, seq.Add(1)-1, WithModel(parser.UnreportedTokens(), req.Model))
		}
	}()

	for k.scanner.Scan() {
		line := bytes.TrimSpace(k.scanner.Bytes())
		if len(line) == 0 { continue }
		ev, ok := parser.Parse(line)
		if !ok { continue }
		if ev.Type == domain.EventError && !said && strings.Contains(ev.Payload, claudeNoConversation) {
			return outcomeNothing
		}
		if ev.Type == domain.EventSystem {
			k.conversation = ev.Payload
		} else {
			said = true
		}
		for _, d := range ev.PermissionDenials {
			sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventToolResult, "Permission denied: "+d))
		}
		sink(newEvent(req.SessionID, seq.Add(1)-1, ev.Type, ev.Payload))
		if ev.CostUSD != nil {
			delta := math.Max(0, *ev.CostUSD-k.reportedCost)
			k.reportedCost = math.Max(k.reportedCost, *ev.CostUSD)
			sink(newEvent(req.SessionID, seq.Add(1)-1, domain.EventUsage, strconv.FormatFloat(delta, 'f', -1, 64)))
		}
		if len(ev.Tokens) > 0 {
			EmitTokens(sink, req.SessionID, seq.Add(1)-1, WithModel(k.sinceLastResult(ev.Tokens), req.Model))
		}
		if ev.Type == domain.EventResult || ev.Type == domain.EventError {
			return outcomeDone
		}
	}

	if !said { return outcomeNothing }
	if ctx.Err() == nil {
		if timedOut.Load() {
			emitError(sink, req.SessionID, &seq, fmt.Sprintf("Agent timed out after %ds", int64(timeout.Seconds())))
		} else {
			why := k.stderrTail.String()
			msg := "claude exited before finishing"
			if why != "" { msg += ": " + why }
			emitError(sink, req.SessionID, &seq, msg)
		}
	}
	return outcomeBroken
}

// sinceLastResult: 逐模型差分减法（对齐 ClaudeWarmSessions.java:332-348）
func (k *keptClaude) sinceLastResult(totals []domain.TokenUsage) []domain.TokenUsage {
	var out []domain.TokenUsage
	for _, t := range totals {
		prev, existed := k.reported[t.Model]
		k.reported[t.Model] = t
		d := t
		if existed {
			d = domain.TokenUsage{
				Model: t.Model,
				Input: max(0, t.Input-prev.Input), CacheRead: max(0, t.CacheRead-prev.CacheRead),
				CacheWrite: max(0, t.CacheWrite-prev.CacheWrite), CacheWrite1h: max(0, t.CacheWrite1h-prev.CacheWrite1h),
				Output: max(0, t.Output-prev.Output), Reasoning: max(0, t.Reasoning-prev.Reasoning),
			}
			if t.CLICostUSD != nil {
				var prevCost float64
				if prev.CLICostUSD != nil { prevCost = *prev.CLICostUSD }
				v := math.Max(0, *t.CLICostUSD-prevCost)
				d.CLICostUSD = &v
			}
		}
		if !d.NothingUsed() { out = append(out, d) }
	}
	return out
}
```

### 5.6 `CodexRunner` + `CodexStreamParser`

- **Java 参考**：`CodexCliAgentRunner.java:51-335`、`CodexStreamParser.java:30-161`
- **关键行为保真清单**：
  1. **能力声明**：`SupportImages: true, SupportStdioMCP: false, SupportWarmPool: false, Maturity: "ga"`；
  2. **UUID 防参数注入**（`CodexCliAgentRunner.java:61, 282-312`）：线程文件 `<workspaceDir>/.superagent/codex-sessions/<sessionId>` 处于 Agent 可写目录，读写两侧均严格校验 `^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`，非法值静默丢弃并回退开启新线程；
  3. **权限映射与模型防注入**（`L169-218`）：
     - `SkipPermissions` → `--dangerously-bypass-approvals-and-sandbox`；
     - 含 `{"Edit","Write","MultiEdit","NotebookEdit","Bash"}` → `-s workspace-write`；否则 `-s read-only`；
     - 若 `req.Model` 以 `"-"` 开头直接拒绝报错（防 flag 注入）；
     - 图片强制用 `--image=<absPath>`（避免 `-i path` 吞掉末尾 `-` stdin 标记）；
  4. **TOML `-c` MCP 翻译与凭据隔离**（`L102-111, 176-199, 222-255`）：
     - 有凭据时注入 env `CODEX_API_KEY` + `-c model_provider="openai"` +（若有 `BaseURL`）`-c openai_base_url=<jsonQuoted>`；
     - 解析 `MCPConfig.mcpServers`：仅保留服务名符合 `^[A-Za-z0-9_-]{1,64}$` 且具有非空 `url` 的 HTTP 服务器（静默跳过 STDIO），为每项注入 `-c mcp_servers.<name>.url=<jsonQuoted>`、`-c mcp_servers.<name>.default_tools_approval_mode="approve"`，若有 `Authorization: Bearer <tok>` 则注入 `-c mcp_servers.<name>.bearer_token_env_var="SUPERAGENT_MCP_TOKEN_<UPPER>"` 并将 `<tok>` 放入进程 env；
  5. **System Prompt 与记忆拼接**（`L270-276`）：`<system>\n<trimmedSystem>\n</system>\n\n<prompt>` 经 stdin 写入并立即关闭 stdin；每轮均重复 `SystemPrompt`（使人设修改实时生效），但仅当 `freshThread == true` 时拼入 `Memory`；
  6. **`CodexStreamParser` 解析与 Token 拆分**（`CodexStreamParser.java:44-160`）：
     - `thread.started` → `EventSystem(thread_id)`（首次持久化至 `.superagent/codex-sessions/<sessionId>`）；
     - `item.started` / `item.completed` 工具项按 `item.id` 去重映射：`command_execution` → `Bash: <cmd>`、`file_change` → `Edit: <paths>`、`mcp_tool_call` → `MCP: <server>.<tool>`、`web_search` → `WebSearch: <query>`（详情截断至 120 字符）；
     - `item.completed` 的 `agent_message` → `EventText` + 更新 `lastMessage`；`reasoning`（非空）→ `EventThinking`；`error` 仅记 Debug 日志（非致命配置警告）；
     - `turn.completed` → 置 `finished=true`、累加 5 维 token、发 `EventResult(lastMessage)`；`turn.failed` → 置 `finished=true`、累加 token、发 `EventError`；顶层 `error` → `EventError`；
     - `defer` 必经 `FromTotalInput(model, totalInput, cachedInput, cacheWrite, output, reasoning)` 拆分不重叠的 `uncachedInput = max(0, totalInput - cachedInput - cacheWrite)`，绝不发射 `USAGE`。

### 5.7 扩展未来 CLI 的标准模式（`JSONLineRunner` 模板）

- **Java 参考**：`JsonLineCliRunner.java:34-272`
- **零侵入接入规范**：新增任意 NDJSON CLI（如 DeepSeek / Qwen / Kimi / Gemini / CodeBuddy / MiniMax）只需实现 `JSONLineAdapter` 接口并交由 `NewJSONLineRunner(adapter, launcher)` 包装，在 `cmd/synapse-server/wire.go` 中调用 `registry.Register(...)` 及 `catalog.Register(...)`，**`ChatService`/`TaskService`/`ModelCall`/`McpGrants` 零改动**：

```go
package runner

type JSONLineAdapter interface {
	Type() domain.AgentType
	Capabilities() spi.RunnerCapabilities
	Command() string
	Timeout() time.Duration
	Precondition(req domain.AgentRunRequest) error
	BuildProcessSpec(req domain.AgentRunRequest, resumeID string) (spi.ProcessSpec, []byte, error)
	NewParser() spi.StreamParser
}

// JSONLineRunner 统一处理：前置条件 -> .superagent/<cli>-session-<sessionId> 读写 ->
// ProcessLauncher 启动 -> stderr 过滤 NODE_TLS_REJECT_UNAUTHORIZED/--trace-warnings ->
// NDJSON 行解析 -> finish() -> 超时/退出码诊断 -> 发射 TOKENS
type JSONLineRunner struct {
	adapter  JSONLineAdapter
	launcher spi.ProcessLauncher
}
```

### 5.8 `McpGrants`、`McpToolProvider`、`McpDispatcher` 与 `McpClient`

- **Java 参考**：`McpGrants.java:49-390`、`McpTools.java:9-84`、`McpController.java:35-144`、`McpClient.java:36-397`
- **关键行为保真与加固**：
  1. **48-hex Token 与租户绑定**（`McpGrants.java:248-263, 306-312`）：24 字节 `crypto/rand` 编码为 48 位小写 hex；签发前强调用 `tenant.Require(ctx)` 并写入 `companies[token]`；`Find(token)` 先查 `grants[token]`，再查 `standIns[token] -> grants[active]`；
  2. **完整工具白名单与 `FILES_HINT`**（`McpGrants.java:62-132`）：原样导出 `InboxTools(6)`、`FolderTools(7)`、`FileTools(11)`、`SuperFileTools(11)`、`SkillTools(5)`、`SkillChatTools(1)`、`DocTools(12)`、`DocReadTools(7)`、`HistoryTools(2)`、`WorkTools` 与 `FilesHint` 常量，以及 `RequireDocAccess(grant, tool)`；
  3. **`WorkspaceFile` 抗符号链接逃逸**（`McpGrants.java:367-389`）：
     ```go
     func (g *McpGrants) WorkspaceFile(grant *Grant, file string) (string, error) {
         if grant == nil || grant.Workspace == "" {
             return "", apperror.InvalidArg("This run has no workspace")
         }
         file = strings.TrimSpace(file)
         if file == "" { return "", apperror.InvalidArg("file is required") }
         wsReal, err := filepath.EvalSymlinks(grant.Workspace)
         if err != nil { return "", apperror.InvalidArg("Cannot resolve workspace: " + err.Error()) }
         wsReal = filepath.Clean(wsReal)
         cand := file
         if !filepath.IsAbs(cand) { cand = filepath.Join(wsReal, cand) }
         if _, err := os.Stat(cand); err != nil {
             return "", apperror.InvalidArg("No such file in the task workspace: " + file)
         }
         real, err := filepath.EvalSymlinks(cand)
         if err != nil { return "", apperror.InvalidArg("Cannot read " + file + ": " + err.Error()) }
         real = filepath.Clean(real)
         // 使用 filepath.Rel 防止 "/ws" 误前缀匹配 "/ws-other"
         rel, err := filepath.Rel(wsReal, real)
         if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
             return "", apperror.InvalidArg("Not a file in the task workspace: " + file)
         }
         info, err := os.Stat(real)
         if err != nil || !info.Mode().IsRegular() {
             return "", apperror.InvalidArg("Not a file in the task workspace: " + file)
         }
         return real, nil
     }
     ```
     > **V1 安全加固**：相比 Java `Path.startsWith`（按段比较）的等价安全语义，Go 绝不能用 `strings.HasPrefix(real, wsReal)`，必须用 `filepath.Rel` 严格校验无 `..` 穿越。
  4. **启动异步自检 `SelfCheckAsync`**（`McpGrants.java:192-228`）：服务监听就绪后（若未显式配置 `mcp.self_url`，用实际绑定端口拼接 `http://127.0.0.1:<port>/mcp`），在 `tenant.WithCompany(ctx, tenant.DefaultSentinelID)` 下签发临时 `GrantChat("__selfcheck__")` 发起 `POST /mcp` `{"jsonrpc":"2.0","id":1,"method":"ping"}`（5s 连接 / 10s 请求超时），校验 `HTTP 200` 且响应含 `"result"` 后写入 `SelfStatus{State:"ok"}` 或 `"error"`，`defer Close(probe)`；
  5. **`McpDispatcher` 统一错误拦截（V1 缺陷修复）**（`McpController.java:54-104`）：
     - 在 `tools/call` 中，若 `provider.Call(...)` 返回 `*McpToolError` **或任何 `apperror.Kind` 属于 `{InvalidArg, NotFound, Forbidden, Conflict, Busy}` 的领域错误**，统一返回 HTTP `200` + `result: {"content":[{"type":"text","text":"错误：" + err.Error()}],"isError":true}`，让模型能够自主纠错重试；仅未知系统错误返 `-32603`；
  6. **`McpClient` 三传输只读探测**（`McpClient.java:38-397`）：
     - 协议固定 `"2025-06-18"`、`clientInfo: {"name":"superagent","version":"1"}`，依次执行 `initialize` → `notifications/initialized` → 分页 `tools/list`（最多 `MaxPages = 20`）；
     - **`STDIO`**：经 `ProcessLauncher.Start` 启动（`DiscardStderr: true`），通过 `bufio.Scanner` 过滤以 `{` 开头的 JSON 行送入带缓冲 channel（256），写入或 500ms 轮询等待期间检测进程早退并报告退出码，`defer proc.Kill() + proc.Close()`；
     - **`HTTP`（Streamable HTTP）**：每条消息 POST `Content-Type: application/json`、`Accept: application/json, text/event-stream`、`MCP-Protocol-Version: 2025-06-18` + `spec.Headers`，捕获并回传 `Mcp-Session-Id`，按响应 `Content-Type` 分别解析直出 JSON 或 `text/event-stream`（首个匹配 `id` 的 `data:` 帧）；
     - **`SSE`（Legacy HTTP+SSE）**：`GET spec.URL`（`Accept: text/event-stream`）后台解析 `event:`/`data:`，等待 `event: endpoint` 并用 `baseURL.ResolveReference` 解析相对 POST 地址，后续每条请求 POST 到目标 URL 并在 SSE 队列按 `id` 匹配 `event: message`（或无 `event` 名）帧；`defer` 取消 GET ctx 并关闭流；
     - **说明**：`McpClient` 刻意不施加公网 `EgressGuard`（企业管理员可能接入内网 LAN MCP 服务），HTTP Client 复用配置了企业代理与 CA 证书池的 Transport。

### 5.9 `ImageAttachmentStore`：UUID 强校验与 4 格式魔数嗅探

- **Java 参考**：`ImageAttachmentStore.java:32-216`
- **关键行为保真**：
  1. 路径布局：`<root>/<companyId>/<ownerId>/<messageId>/<n>.<ext>`，其中 `<companyId>` 由 `tenant.Path(ctx, root)` 强校验生成，`<ownerId>` 与 `<messageId>` 必须匹配 `^[0-9a-fA-F-]{36}$`（非法在 `Save` 返 `InvalidArg`，在 `List` 返空切片，在 `Read` 返 `NotFound`）；
  2. `Save` 先完整校验并解码全部上传项（最多 `5` 张、标准 Base64 解码、单张 `<= 5 MiB`、`Sniff(bytes)` 仅认 PNG/JPEG/GIF/WebP 魔数，**完全忽略客户端 `mediaType`**），全部通过后才建目录按 `0.<ext>`, `1.<ext>`, ... 原子落盘；
  3. `Sniff` 导出供 `SA-04`（`BrandingService` Logo、`AccountService` 头像）和 `SA-06`（`SkillService` 技能图片）复用。

---

## 6. 任务清单（TDD 粒度）

- [ ] T1: 在 `internal/core/domain/runtime.go` 与 `internal/core/spi/runtime.go` 冻结 `AgentRunRequest`、`AgentEvent`、`TokenUsage`、`RunCredential`、`McpServerSpec`、`ProcessLauncher`、`AgentRunner`、`StreamParser`、`ModelGateway` 全部契约与单测（`runtime_test.go`）
- [ ] T2: 实现 `internal/runtime/clipaths`（私有 Node 优先 + 跨平台探测 + 代理/CA/npm 环境注入）及 `clipaths_test.go`
- [ ] T3: 实现 `internal/runtime/launcher`（`LocalLauncher`、`proc_unix.go` `Setpgid` 进程组强杀、`proc_windows.go`、`0600` 临时文件挂载与回收）及 `local_test.go`（验证孙进程组查杀与临时文件删除）
- [ ] T4: 实现 `internal/runtime/gateway`（`DirectCredentialGateway` + `CredentialResolver` 端口）及 `direct_test.go`
- [ ] T5: 实现 `internal/runtime/mcp`（`McpGrants` 含 `StandIn`/`WorkspaceFile` 符号链接测试/`SelfCheck`、`McpToolProvider`/`McpTools`/`McpToolError`、`McpDispatcher` 领域错误转 `isError: true`、`McpClient` STDIO/HTTP/SSE 三传输）及完整 `*_test.go`
- [ ] T6: 实现 `internal/runtime/runner`（`Registry`、`Tokens`、`JSONLineRunner` 模板、`ClaudeStreamParser`、`ClaudeRunner`、`ClaudeWarmPool`、`CodexStreamParser`、`CodexRunner`）及基于 Fake CLI / Mock `ProcessLauncher` 的全场景单元测试
- [ ] T7: 实现 `internal/runtime/attachment`（`ImageAttachmentStore`、`Sniff`、`Extension`）及 `image_store_test.go`
- [ ] T8: 全量回归与竞态/架构守卫验证：`go test -race -cover ./internal/core/... ./internal/runtime/...` + `internal/arch_test.go`（确认 L1 零反向依赖）

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：合入后将 `SA-03` 状态更新为 `已实施（commit <sha>）`
- [ ] `docs/SuperAgent-Go/03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md`：若实施中微调任何导出签名，同步回写
- [ ] `configs/config.example.yaml`：补充 `runtime.node_dir`、`runtime.attachments_dir`、`runtime.claude.*`（含 `warm.*`）、`runtime.codex.*`、`mcp.self_url`、`network.*` 配置项说明

---

## 8. 测试清单

| 测试点 | 落位 | 关键断言 |
|---|---|---|
| `CliPaths` 私有 Node 优先与代理/CA 叠加 | `internal/runtime/clipaths/clipaths_test.go` | `<nodeRoot>/current/bin` 排在系统 `PATH` 前；同名二进制命中私有目录；`HTTPS_PROXY`/`NODE_EXTRA_CA_CERTS`/`NPM_CONFIG_REGISTRY` 正确注入且可被 `extra` 覆写 |
| `LocalLauncher` 进程组强杀与 0600 临时文件清理 | `internal/runtime/launcher/local_test.go` | 子进程派生 `sleep` 孙进程后取消 `ctx`，父子孙整组 PID 均消失；临时文件权限为 `0600`；`Close()` 后临时文件已删除 |
| `ClaudeStreamParser` 全帧映射与中断补记 | `internal/runtime/runner/claude_parser_test.go` | `init`/`text_delta`/`thinking_delta`/`tool_use`/`tool_result`（字符串与数组）/ `result`（`errors[]` 兜底、`permission_denials` 120 字符截断、`modelUsage` 优先于 `usage`）/ 未收 `result` 时 `UnreportedTokens()` 按 `message.id` 去重 |
| `ClaudeRunner` 命令拼装与双分支自愈重试 | `internal/runtime/runner/claude_test.go` | `--settings` 双重覆写 env 且另一键为空串；带图时含 `enableArtifact:false` + `--input-format stream-json`；`--` 防 `-foo` 注入；首轮 `"is already in use"` 自动 `--resume`；续跑 `"No conversation found"` 抑制错误发 `THINKING` 并重注 `Memory` 重开 |
| `ClaudeWarmPool` LRU、指纹、StandIn、差分计量与降级 | `internal/runtime/runner/claude_warm_test.go` | 8 维任一变化触发重建、仅换 per-turn MCP Token **不**触发重建；回合期 `StandIn` 生效、结束后立刻 `401`；多轮累计 `costUSD`/`tokens` 正确做差分减法；超容淘汰队尾 LRU；`OutcomeNothing` 无缝降级冷启动；`OutcomeBroken` 补记 `UnreportedTokens` |
| `CodexRunner` & `CodexStreamParser` 安全性与拆分 | `internal/runtime/runner/codex_test.go` | 篡改 `.superagent/codex-sessions/<id>` 为 `--dangerously-...` 被 UUID 正则拦截；`model` 以 `-` 开头报错；HTTP MCP 走 `-c` + `SUPERAGENT_MCP_TOKEN_*` env 且跳过 STDIO；`--image=` 紧接 `-`；`FromTotalInput` 五维不重叠 |
| `JSONLineRunner` 模板扩展性 | `internal/runtime/runner/jsonline_test.go` | 注册虚拟 `DummyAdapter` 实现 `AgentRunner`，验证会话文件读写、stderr 过滤 Node 告警、超时终止与 `TOKENS` 发射 |
| `McpGrants` 租户绑定、符号链接防逃逸与自检 | `internal/runtime/mcp/grants_test.go` | 48-hex Token 绑定 `companyID`；`WorkspaceFile` 拒绝指向工作区外的软链接与 `../` 兄弟前缀目录 `/ws-evil`；`RequireDocAccess` 拦截 `SUPER` 后台与写操作；`SelfCheckAsync` 命中 `httptest` 更新 `ok`/`error` |
| `McpDispatcher` 协议规范与领域错误修复 | `internal/runtime/mcp/dispatcher_test.go` | 无 Token 返 `401`；无 `id` 通知返 `202`；`initialize` 返 `2024-11-05`；`McpToolError` 与 `apperror.NotFound`/`Busy` 均返 HTTP 200 + `isError: true` + `"错误：..."` |
| `McpClient` STDIO / HTTP / SSE 三传输 | `internal/runtime/mcp/client_test.go` | STDIO（Fake 脚本含早退退出码检测）、Streamable HTTP（JSON + SSE + `Mcp-Session-Id` + 2 页 `nextCursor`）、Legacy SSE（`endpoint` 相对路径合并 + `id` 关联）及 401 中文错误 |
| `ImageAttachmentStore` 校验与魔数嗅探 | `internal/runtime/attachment/image_store_test.go` | 接受 PNG/JPEG/GIF/WebP 真实魔数（忽略伪造 `mediaType`）；拒绝 SVG/HTML/超 5 张/超 5 MiB/非 UUID `ownerId` 路径穿越；`List`/`Read`/`URLs` 顺序正确 |

---

## 9. 验收门

- [ ] `go build ./... && go test -race ./internal/core/... ./internal/runtime/...` 全绿（零 data race）
- [ ] §2 交付物表 11 项逐一存在且落位完全符合 `internal/runtime/*` 包结构
- [ ] `internal/arch_test.go` 通过：确认 `internal/runtime/**` 零引入 `internal/storage`、`internal/platform`、`internal/agent`、`internal/task`、`internal/web`
- [ ] §8 测试清单 11 组测试全部落地且核心断言保真；`internal/runtime/...` 语句覆盖率 ≥ 80%
- [ ] 安全门禁：全包 grep 确认 API Key / Bearer Token 零拼接进 `argv` 或普通日志；临时文件创建全部为 `0600`；`WorkspaceFile` 使用 `EvalSymlinks` + `filepath.Rel`
- [ ] `SA-01` 前置单测全绿回归

---

## 10. 可提交节点

- **建议主 commit**：`feat(runtime): 实现 ProcessLauncher、Claude(含热池)/Codex 适配器、McpGrants/McpClient 与图片附件存储（spec 03，PR SA-03）`
- **推荐分层拆分提交（可选）**：
  1. `feat(runtime): 定义 Runtime SPI、CliPaths、LocalLauncher 与 DirectCredentialGateway`
  2. `feat(runtime): 实现 McpGrants、McpDispatcher、McpClient 与 ImageAttachmentStore`
  3. `feat(runtime): 实现 RunnerRegistry、JSONLineRunner 模板、ClaudeRunner/WarmPool 与 CodexRunner`
