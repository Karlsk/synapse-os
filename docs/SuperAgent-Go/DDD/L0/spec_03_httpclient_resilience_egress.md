# spec 03 | L0 出站网络与安全底座 — 抗 DNS Rebinding EgressGuard 与双 Profile 弹性 HTTP 客户端

- **状态**：`冻结待实施`
- **篇号顺序**：03（L0 出站网络与弹性调用底座）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-03`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §4.2, §5.1
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
  - 归档母本：[spec_01_core_tenant_crypto_egress.md](../_epic/L1/spec_01_core_tenant_crypto_egress.md)（`EgressGuard` 部分）
- **前置依赖**：[L0/spec_01 (SA-01)](./spec_01_config_obs_error_lifecycle.md)（依赖 `internal/core/apperror`、`internal/infra/config`、`internal/infra/obs`）
- **下游消费者**：`SA-14`（模型供应商探活与私有 Node 下载）、`SA-18`–`SA-20`（连接器 OAuth / 外部自定义 MCP HTTP·SSE 客户端）、`SA-24`–`SA-26`（`models.dev` 价格目录 / 飞书·钉钉文档与 `LinkReader`）、`SA-33`–`SA-34`（飞书·钉钉 IM API 调用）

---

## 1. 背景与范围

Java 原版中出站 HTTP 调用零散分布在 10+ 个服务中各自 `HttpClient.newBuilder()`（如 `ProviderInferenceProbe.java:L50`、`NodeRuntimeService.java:L51`、`FeishuDocsOAuthClient.java:L34`、`DocApi.java:L20`、`Fetcher.java:L44`、`ModelPriceService.java:L303`），既缺乏统一的重试、熔断、脱敏日志与 Prometheus 观测，又在 `EgressGuard.java:L21-L22` 存在明确的 **DNS Rebinding 安全窗口**（先 `InetAddress.getAllByName` 预检，后由 `HttpClient` 二次解析连接）。

本篇交付 `internal/core/egress` 与 `internal/infra/httpclient` 两个高内聚 L0 包，彻底治理全仓所有出站 HTTP/HTTPS/SSE 流量：
1. **`DialContext` 级抗 SSRF / DNS Rebinding 守卫 (`internal/core/egress`)**：1:1 承接并安全升级 Java `EgressGuard.java:L24-L146`；在 TCP `DialContext` 阶段解析全部 DNS 记录、校验全部 IP（先 `Unmap()` 消除 `::ffff:127.0.0.1` / `::ffff:169.254.169.254` 绕过）、**直接对已校验通过的字面量 IP 发起 `connect()`**（Go `http.Transport` 自动维持原 Hostname 的 TLS SNI），并在 `CheckRedirect` 中限制最多 6 跳逐跳重检 + 跨源剥离 `Authorization`/`Cookie`。
2. **双 Profile 物理隔离 (`internal/infra/httpclient`)**：
   - **`TrustedClient`**：面向受信任的平台/企业目标（飞书 API、钉钉 API、企业自建/公网 LLM 端点、`models.dev`、Node.js 镜像下载），支持 `SA-01` 的企业出口代理与自签私有 CA，不拦截企业内网白名单地址；
   - **`UntrustedEgressClient`**：面向用户输入的不可信外链（`LinkReader`、用户 Webhook、外部自定义 HTTP/SSE MCP），**强制挂接 `egress.EgressGuard.DialContext` + 逐跳 `CheckRedirect` + 默认禁用绕过守卫的通用代理 + 强制 `io.LimitReader` 响应体上限（默认 20 MiB 防 OOM 炸弹）**。
3. **分层超时与 SSE 流式专用模式**：
   - 标准模式区分 `DialTimeout` / `TLSHandshakeTimeout` / `ResponseHeaderTimeout` / `TotalTimeout`；
   - **SSE / 大文件流式专用模式 (`Streaming: true`)**：强制 `http.Client.Timeout = 0`（无总超时，绝不截断 10 分钟以上 LLM/MCP SSE 长流或 Node.js 安装包下载），同时保留建连/握手/首字节超时与可选空闲读超时 `IdleReadTimeout`。
4. **弹性重试、轻量熔断与脱敏可观测**：
   - 指数退避 + Full Jitter 重试：仅针对幂等请求且可重放请求体，精准响应 `429 Too Many Requests`（解析秒数或 HTTP-Date 格式 `Retry-After`）与 `502`/`503`/`504` 及瞬态网络错误，绝不重试 `EgressDenied`/TLS 证书错/`context.Canceled`；
   - 基于 `github.com/sony/gobreaker/v2` 的 **`service + host` 维度熔断器**（`Untrusted` 模式内置最大 Host 桶数 LRU 防内存膨胀）；
   - 脱敏 `RoundTripper`（擦除 URL 敏感查询参数、不记录敏感 Header 与 Body）与 Prometheus 指标。
5. **严格的架构职责边界（硬性约束）**：**`internal/infra/httpclient` 严禁内置任何业务降级策略**（如切换备用 API Key、飞书授权失败回落匿名网页抓取、返回过期价格缓存等）；HTTP 包只返回结构化错误分类（`*ClientError` + `ErrorKind`），所有降级与回退策略**严格归属业务调用方**。

**本篇边界（1.5 人天）**：
- 本篇不实现具体业务 API 协议（飞书/钉钉/LLM/MCP/`LinkReader` 分别归属 `SA-14`、`SA-18`–`SA-20`、`SA-24`–`SA-26`、`SA-33`–`SA-34`）；
- 本篇零引入 Redis 或任何持久化依赖。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | 抗 DNS Rebinding `EgressGuard`（`ToURI`、`IsPublic` 含 `Unmap()`、`CheckURL`、`DialContext`、`CheckRedirect`） | `internal/core/egress/guard.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/net/EgressGuard.java:L24-L146`、`EgressDeniedException.java:L6-L11` |
| 2 | 双 Profile 工厂（`TrustedClient` / `UntrustedEgressClient`）与分层超时 / SSE 无总超时配置 | `internal/infra/httpclient/client.go`、`profile.go` | 新增 | 统一治理 `Fetcher.java:L31-L91`、`NodeRuntimeService.java:L51-L52,L240`、`ProviderInferenceProbe.java:L50`、`FeishuDocsOAuthClient.java:L34`、`DocApi.java:L20`、`ModelPriceService.java:L303-L304` |
| 3 | 结构化错误分类（`*ClientError` + `ErrorKind`）与响应体 `io.LimitReader` 限流器、流式 `IdleReadTimeout` 包装器 | `internal/infra/httpclient/errors.go`、`limits.go` | 新增 | 替代各处散落的 `IOException` 捕获与 `Fetcher.java:L117-L133` |
| 4 | 指数退避 + Full Jitter 重试 `RoundTripper`（含 `429 Retry-After` 与 `502/503/504`） | `internal/infra/httpclient/retry.go` | 新增 | V1 弹性治理新增 |
| 5 | 按 `service/host` 维度的 `sony/gobreaker/v2` 轻量熔断 `RoundTripper`（含 Host 槽位上限防膨胀） | `internal/infra/httpclient/breaker.go` | 新增 | V1 弹性治理新增 |
| 6 | 脱敏 `RoundTripper` 日志（URL Query/Header 脱敏）与 Prometheus 客户端指标 | `internal/infra/httpclient/observability.go` | 新增 | V1 可观测性补强 |
| 7 | `egress` 与 `httpclient` 全套同包单元测试（含 Mock Resolver 模拟 DNS Rebinding 与本地 `httptest.Server`） | `internal/core/egress/guard_test.go`、`internal/infra/httpclient/*_test.go` | 新增 | 对齐并超越 `superagent-core/src/test/java/com/superagent/core/net/EgressGuardTest.java:L10-L36` |

---

## 3. 数据库设计

本篇为纯 L0 网络安全与出站 HTTP 客户端底座，**无数据库表、无 SQL 迁移、无持久化状态**。

---

## 4. 冻结契约

### 4.1 导出符号

#### A. `internal/core/egress`

```go
package egress

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

type Options struct {
	Allowed      []string      // 管理员白名单：精确域名 wiki.corp.example / 后缀 .corp.example / IPv4或IPv6 CIDR
	AllowedPorts []int         // 空时默认 [80, 443, 8080, 8443]
	Resolver     Resolver      // nil 时默认 net.DefaultResolver
	DialTimeout  time.Duration // 默认 10s
	MaxRedirects int           // 默认 6（对齐 Fetcher.java:L34）
}

type Guard struct { /* allowedHosts, allowedSuffixes, allowedPrefixes, allowedPorts, resolver, dialTimeout, maxRedirects */ }

var PublicOnly = MustNew(Options{})

func New(opts Options) (*Guard, error)
func MustNew(opts Options) *Guard
func ToURI(raw string) (*url.URL, error)
func IsPublic(addr netip.Addr) bool
func (g *Guard) CheckURL(ctx context.Context, rawURL string) (*url.URL, error)
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error
```

#### B. `internal/infra/httpclient`

```go
package httpclient

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/egress"
	"github.com/Karlsk/synapse-os/internal/infra/config"
	"github.com/prometheus/client_golang/prometheus"
)

type Profile string

const (
	ProfileTrusted   Profile = "trusted"
	ProfileUntrusted Profile = "untrusted"
)

type TimeoutPolicy struct {
	DialTimeout           time.Duration // 默认 Trusted=5s, Untrusted=10s
	TLSHandshakeTimeout   time.Duration // 默认 10s
	ResponseHeaderTimeout time.Duration // 默认 30s
	TotalTimeout          time.Duration // 标准模式默认 60s；Streaming=true 时强制清零（无总超时）
	IdleReadTimeout       time.Duration // Streaming=true 可选单次 Read 空闲超时（0 表示不限，依赖 ctx）
}

type RetryPolicy struct {
	MaxAttempts   int           // 默认 3（Streaming=true 默认 1，不自动重试）
	BaseBackoff   time.Duration // 默认 200ms
	MaxBackoff    time.Duration // 默认 5s
	MaxRetryAfter time.Duration // 429 Retry-After 上限钳制，默认 30s
}

type BreakerPolicy struct {
	Enabled         bool          // 默认 true
	Interval        time.Duration // 滑动统计周期，默认 60s
	Timeout         time.Duration // Open -> HalfOpen 冷却时长，默认 30s
	MaxRequests     uint32        // HalfOpen 探针请求数，默认 2
	MinRequests     uint32        // 触发熔断最小请求数，默认 5
	FailureRatio    float64       // 失败率阈值，默认 0.6
	ConsecutiveFail uint32        // 连续失败触发阈值，默认 5
	MaxHostEntries  int           // 最大隔离 Host 槽位数，默认 256（防止 Untrusted 任意域名 OOM）
}

type Options struct {
	Service          string // 逻辑上游名，如 "feishu", "dingtalk", "llm", "models.dev", "link_reader", "mcp_external"
	Streaming        bool   // true = SSE/大文件流式专用模式（http.Client.Timeout == 0）
	Timeouts         TimeoutPolicy
	Retry            RetryPolicy
	Breaker          BreakerPolicy
	MaxResponseBytes int64               // Untrusted 强制生效，默认 20 MiB；Trusted 可选（0=不限）
	EgressGuard      *egress.Guard       // Untrusted 默认 egress.PublicOnly；Trusted 忽略此字段
	Network          *config.NetworkRuntime // Trusted 注入企业代理与私有 CA
	Logger           *slog.Logger
	Registerer       prometheus.Registerer
}

type Client struct {
	http    *http.Client
	profile Profile
	service string
}

// 双 Profile 显式构造器（物理隔离，无法误混用）
func NewTrusted(opts Options) (*Client, error)
func NewUntrustedEgress(opts Options) (*Client, error)

func (c *Client) Do(req *http.Request) (*http.Response, error)
func (c *Client) StdClient() *http.Client // 仅用于需注入第三方 SDK（如 S3/飞书 SDK）场景，仍完整保留 RoundTripper 链
func (c *Client) Profile() Profile

// 请求级上下文标记
func WithIdempotent(ctx context.Context) context.Context // 显式标记非 GET 请求（如幂等查询型 POST）允许重试
func WithService(ctx context.Context, service string) context.Context

// ReadLimited 安全读取受限响应体，超限时返回 *ClientError{Kind: KindResponseTooLarge}
func ReadLimited(r io.Reader, maxBytes int64) ([]byte, error)

// 结构化错误分类（本包仅分类不降级，降级由业务调用方根据 ErrorKind 决定）
type ErrorKind string

const (
	KindEgressDenied     ErrorKind = "egress_denied"
	KindCircuitOpen      ErrorKind = "circuit_open"
	KindTimeout          ErrorKind = "timeout"
	KindRateLimited      ErrorKind = "rate_limited"
	KindUpstream5xx      ErrorKind = "upstream_5xx"
	KindResponseTooLarge ErrorKind = "response_too_large"
	KindNetwork          ErrorKind = "network"
	KindCanceled         ErrorKind = "canceled"
)

type ClientError struct {
	Kind       ErrorKind
	Service    string
	Host       string
	StatusCode int
	RetryAfter time.Duration
	Attempts   int
	Err        error
}

func (e *ClientError) Error() string
func (e *ClientError) Unwrap() error
func KindOf(err error) (ErrorKind, bool)
```

### 4.2 路由契约

本篇为出站 HTTP 客户端基础设施，不暴露任何入站 HTTP 路由。

### 4.3 错误语义

1. **`egress.Guard` 拦截错误**：所有协议/端口/内网/元数据/DNS 解析失败/重定向超限错误均通过 `apperror.EgressDenied(msg)` 返回（HTTP `403 forbidden`，`errors.Is(err, apperror.ErrEgressDenied) == true`），中文提示文案与 Java `EgressGuard.java` 100% 对齐：
   - 非 `http`/`https` → `"只能读 http / https 链接"`
   - 缺域名 → `"链接里没有域名"`
   - 非法端口 → `"不读这个端口上的地址"`
   - 内网/回环/云元数据/CGNAT/ULA/IPv4-mapped IPv6 → `"这是内网或本机地址，不能读取"`
   - DNS 解析失败 → `"找不到这个域名"`
   - 重定向超过 6 跳 → `"重定向次数过多"`
2. **`httpclient` 错误分类边界**：
   - 当响应为 `4xx`（除 `429` 且启用重试耗尽外）或 `2xx`/`3xx` 终态时，`Do(req)` 返回 `(*http.Response, nil)`，业务调用方自行解析业务状态码；
   - 当发生传输异常、Egress 拒止、熔断拒绝、超时、响应体超限或重试耗尽（含 `429`/`502`/`503`/`504`）时，返回 `*ClientError`（若底层为 `apperror.ErrEgressDenied`，通过 `Unwrap()` 保留，使 `errors.Is(err, apperror.ErrEgressDenied)` 恒为 `true`；若响应体超限则 `Unwrap()` 返回 `apperror.TooLarge(...)`）；
   - **降级归属铁律**：`httpclient` **绝不**静默吞错、绝不返回兜底数据、绝不切换凭证或 URL；业务调用方按 `KindOf(err)` 自主决定降级（例如 `LinkReader` 遇飞书 403/5xx 回落公网页，`ModelGateway` 遇 `KindRateLimited`/`KindUpstream5xx` 切换次优先级 Key）。

### 4.4 SQL 契约

本篇无 SQL 语句。

---

## 5. 行为语义（含 Java 源码对照、Go 契约定义与详细伪代码）

### 5.1 抗 DNS Rebinding `EgressGuard` (`internal/core/egress/guard.go`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/net/EgressGuard.java:L24-L146`、`EgressDeniedException.java:L6-L11`、`superagent-docs/src/main/java/com/superagent/docs/link/Fetcher.java:L34,L72-L91`。

```go
package egress

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

// IsPublic 在执行任意网段判定前强制 Unmap()，堵死 ::ffff:127.0.0.1 与 ::ffff:169.254.169.254 绕过。
func IsPublic(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsLoopback() || addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsPrivate() ||
		addr.IsInterfaceLocalMulticast() {
		return false
	}
	if addr.Is6() {
		b := addr.As16()
		if (b[0] & 0xfe) == 0xfc { // fc00::/7 ULA
			return false
		}
		// 2001:db8::/32 文档保留段
		if b[0] == 0x20 && b[1] == 0x01 && b[2] == 0x0d && b[3] == 0xb8 {
			return false
		}
		return true
	}
	b := addr.As4()
	x, y, z := b[0], b[1], b[2]
	if x == 0 || x == 10 || x == 127 || x >= 224 ||
		(x == 100 && y >= 64 && y <= 127) ||            // RFC6598 CGNAT 100.64.0.0/10
		(x == 169 && y == 254) ||                       // 169.254.0.0/16 Link-Local / 云元数据
		(x == 172 && y >= 16 && y <= 31) ||             // RFC1918
		(x == 192 && y == 168) ||                       // RFC1918
		(x == 192 && y == 0 && (z == 0 || z == 2)) ||   // 192.0.0.0/24, 192.0.2.0/24
		(x == 198 && (y == 18 || y == 19)) ||           // RFC2544 198.18.0.0/15
		(x == 198 && y == 51 && z == 100) ||            // 198.51.100.0/24 TEST-NET-2
		(x == 203 && y == 0 && z == 113) {              // 203.0.113.0/24 TEST-NET-3
		return false
	}
	return true
}

// DialContext 抗 DNS Rebinding 核心防线：
// 1) 解析所有 IP 并校验全部记录（任一非法即全组拒绝）；
// 2) 直接用校验通过的 IP 字面量发起 TCP connect，零二次 DNS 查询；
// 3) net.Dialer.Control 在 socket connect 前二次复核最终内核目标 IP（纵深防御）。
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, apperror.EgressDenied("不是有效的网址")
	}
	portNum, err := strconv.Atoi(port)
	if err != nil || !g.isPortAllowed(portNum) {
		return nil, apperror.EgressDenied("不读这个端口上的地址")
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	allowBypass := g.allowedHost(h)

	dialer := &net.Dialer{
		Timeout:   g.dialTimeout,
		KeepAlive: 30 * time.Second,
		Control: func(_, rawAddr string, _ syscall.RawConn) error {
			if allowBypass {
				return nil
			}
			ipHost, _, err := net.SplitHostPort(rawAddr)
			if err != nil {
				return apperror.EgressDenied("不是有效的网址")
			}
			ip, err := netip.ParseAddr(ipHost)
			if err != nil || (!IsPublic(ip) && !g.allowedPrefix(ip)) {
				return apperror.EgressDenied("这是内网或本机地址，不能读取")
			}
			return nil
		},
	}

	if allowBypass {
		return dialer.DialContext(ctx, network, addr)
	}
	if isForbiddenLocalHost(h) {
		return nil, apperror.EgressDenied("这是内网或本机地址，不能读取")
	}
	ips, err := g.resolveAndValidate(ctx, h)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// CheckRedirect 逐跳重检目标 URL + 限制最多 MaxRedirects(默认 6) 跳 + 跨源剥离敏感请求头
func (g *Guard) CheckRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= g.maxRedirects {
		return apperror.EgressDenied("重定向次数过多")
	}
	if _, err := g.CheckURL(req.Context(), req.URL.String()); err != nil {
		return err
	}
	if prev := via[len(via)-1]; prev != nil && !sameOrigin(prev.URL, req.URL) {
		req.Header.Del("Authorization")
		req.Header.Del("Cookie")
		req.Header.Del("Proxy-Authorization")
	}
	return nil
}
```

### 5.2 双 Profile 构造与 SSE 流式无总超时模式 (`profile.go`, `limits.go`)

```go
package httpclient

// NewTrusted：面向企业/受信任上游（飞书/钉钉/LLM/models.dev/Node 镜像），注入企业代理与私有 CA，不挂 EgressGuard。
func NewTrusted(opts Options) (*Client, error) {
	applyDefaults(&opts, ProfileTrusted)
	tr := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: opts.Timeouts.DialTimeout, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   opts.Timeouts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeaderTimeout,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	if opts.Network != nil {
		if opts.Network.TLSConfig != nil {
			tr.TLSClientConfig = opts.Network.TLSConfig.Clone()
		}
		if opts.Network.ProxyFunc != nil {
			tr.Proxy = opts.Network.ProxyFunc
		}
	}
	return buildClient(ProfileTrusted, opts, tr, nil), nil
}

// NewUntrustedEgress：面向不可信外链（LinkReader/用户 Webhook/外部自定义 MCP）：
// 1) 强制 DialContext = guard.DialContext（抗 SSRF / DNS Rebinding）；
// 2) 强制 Proxy = nil（杜绝经由企业代理转发绕过本地 DNS 校验）；
// 3) 强制 CheckRedirect = guard.CheckRedirect（逐跳校验 + 6 跳上限 + 跨源去凭证）；
// 4) 强制 MaxResponseBytes（默认 20 MiB）io.LimitReader 包装防 OOM。
func NewUntrustedEgress(opts Options) (*Client, error) {
	applyDefaults(&opts, ProfileUntrusted)
	guard := opts.EgressGuard
	if guard == nil {
		guard = egress.PublicOnly
	}
	tr := &http.Transport{
		DialContext:           guard.DialContext,
		Proxy:                 nil, // 严禁默认走外部代理绕过 EgressGuard
		TLSHandshakeTimeout:   opts.Timeouts.TLSHandshakeTimeout,
		ResponseHeaderTimeout: opts.Timeouts.ResponseHeaderTimeout,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       60 * time.Second,
		ForceAttemptHTTP2:     true,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
	}
	return buildClient(ProfileUntrusted, opts, tr, guard.CheckRedirect), nil
}

func buildClient(profile Profile, opts Options, tr *http.Transport, checkRedirect func(*http.Request, []*http.Request) error) *Client {
	var rt http.RoundTripper = tr
	// 内层 -> 外层装配：Transport -> BodyLimit/IdleRead -> CircuitBreaker -> Retry -> Observability
	if opts.MaxResponseBytes > 0 || (opts.Streaming && opts.Timeouts.IdleReadTimeout > 0) {
		rt = &bodyControlRT{
			next:            rt,
			maxBytes:        opts.MaxResponseBytes,
			idleReadTimeout: opts.Timeouts.IdleReadTimeout,
			service:         opts.Service,
		}
	}
	if opts.Breaker.Enabled {
		rt = newBreakerRT(rt, opts.Service, opts.Breaker, opts.Registerer)
	}
	if !opts.Streaming && opts.Retry.MaxAttempts > 1 {
		rt = newRetryRT(rt, opts.Service, opts.Retry, opts.Registerer)
	}
	rt = newObservabilityRT(rt, profile, opts.Service, opts.Logger, opts.Registerer)

	totalTimeout := opts.Timeouts.TotalTimeout
	if opts.Streaming {
		totalTimeout = 0 // SSE / 大文件流式专用：严禁设置 http.Client.Timeout 截断长连接！
	}
	return &Client{
		http: &http.Client{
			Transport:     rt,
			Timeout:       totalTimeout,
			CheckRedirect: checkRedirect,
		},
		profile: profile,
		service: opts.Service,
	}
}
```

**响应体 `io.LimitReader` 硬上限 (`limits.go`)**：
- `bodyControlRT` 将 `resp.Body` 替换为 `limitedReadCloser{orig: resp.Body, lr: io.LimitReader(resp.Body, maxBytes+1), limit: maxBytes}`；
- 当调用方读取超过 `maxBytes` 时（读到第 `maxBytes+1` 字节），立即返回 `&ClientError{Kind: KindResponseTooLarge, Service: ..., Host: ..., Err: apperror.TooLarge("response body exceeds limit")}` 并关闭底层流；
- 若处于 `Streaming: true` 且配置了 `IdleReadTimeout > 0`，每次 `Read()` 通过 `time.AfterFunc(idleReadTimeout, cancelRead)` 监控单次块读停滞（有数据到达即 `Reset`，对齐 `NodeRuntimeService.java:L240` 看门狗思想，同时绝不限制总流式时长）。

### 5.3 指数退避 + Full Jitter 重试 (`retry.go`)

```go
package httpclient

func (rt *retryRT) RoundTrip(req *http.Request) (*http.Response, error) {
	canRetry := isRetryableRequest(req) // GET/HEAD/OPTIONS/PUT/DELETE 或 WithIdempotent(ctx)，且 Body==nil || GetBody!=nil
	maxAttempts := rt.policy.MaxAttempts
	if !canRetry {
		maxAttempts = 1
	}

	var lastErr error
	var lastStatus int
	var lastRetryAfter time.Duration

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			if err := resetRequestBody(req); err != nil {
				return nil, err
			}
		}
		resp, err := rt.next.RoundTrip(req)
		if err == nil {
			if !isRetryableStatus(resp.StatusCode) || attempt == maxAttempts {
				return resp, nil
			}
			// 429 / 502 / 503 / 504：记录 Retry-After 并排空关闭连接复用
			delay := rt.computeDelay(attempt, resp.Header.Get("Retry-After"))
			lastStatus = resp.StatusCode
			lastRetryAfter = delay
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
			_ = resp.Body.Close()

			rt.recordRetry(resp.StatusCode)
			if err := sleepWithContext(req.Context(), delay); err != nil {
				return nil, classifyError(rt.service, req.URL.Host, attempt, lastStatus, lastRetryAfter, err)
			}
			continue
		}

		// 致命/不可重试错误立即终止：EgressDenied、熔断开启、ctx 取消、TLS 证书校验失败
		if !isRetryableError(err) || attempt == maxAttempts {
			return nil, classifyError(rt.service, req.URL.Host, attempt, lastStatus, lastRetryAfter, err)
		}
		lastErr = err
		rt.recordRetry(0)
		delay := fullJitterBackoff(attempt, rt.policy.BaseBackoff, rt.policy.MaxBackoff)
		if sleepErr := sleepWithContext(req.Context(), delay); sleepErr != nil {
			return nil, classifyError(rt.service, req.URL.Host, attempt, lastStatus, lastRetryAfter, sleepErr)
		}
	}
	return nil, classifyError(rt.service, req.URL.Host, maxAttempts, lastStatus, lastRetryAfter, lastErr)
}

// parseRetryAfter 支持 RFC 9110 秒数（如 "3"）与 HTTP-Date（如 "Wed, 21 Oct 2026 07:28:00 GMT"），钳制在 MaxRetryAfter 内。
func parseRetryAfter(val string, now time.Time, maxWait time.Duration) (time.Duration, bool) {
	val = strings.TrimSpace(val)
	if val == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(val); err == nil && secs >= 0 {
		d := time.Duration(secs) * time.Second
		return min(d, maxWait), true
	}
	if t, err := http.ParseTime(val); err == nil {
		d := max(time.Duration(0), t.Sub(now))
		return min(d, maxWait), true
	}
	return 0, false
}
```

### 5.4 按 `service/host` 维度的 `sony/gobreaker/v2` 轻量熔断器 (`breaker.go`)

```go
package httpclient

import "github.com/sony/gobreaker/v2"

// breakerRT 为每个 (service, host) 维护独立 gobreaker.CircuitBreaker[*http.Response]；
// 超出 MaxHostEntries 时淘汰最久未用槽位，避免 UntrustedEgressClient 访问海量不同外链导致内存无限增长。
func (b *breakerRT) RoundTrip(req *http.Request) (*http.Response, error) {
	host := strings.ToLower(req.URL.Host)
	cb := b.getOrCreate(host) // key = b.service + ":" + host

	var capturedResp *http.Response
	resp, err := cb.Execute(func() (*http.Response, error) {
		r, err := b.next.RoundTrip(req)
		if err != nil {
			// EgressDenied 与 context.Canceled 属策略拦截/调用方取消，不应把上游健康度计为故障
			return nil, err
		}
		// 仅将 5xx 与 429 视为熔断器失败信号；400/401/403/404 等业务响应不触发熔断
		if r.StatusCode >= 500 || r.StatusCode == http.StatusTooManyRequests {
			capturedResp = r
			return r, &upstreamStatusErr{status: r.StatusCode}
		}
		return r, nil
	})
	if errors.Is(err, gobreaker.ErrOpenState) || errors.Is(err, gobreaker.ErrTooManyRequests) {
		return nil, &ClientError{
			Kind:    KindCircuitOpen,
			Service: b.service,
			Host:    host,
			Err:     apperror.ServiceUnavailable("upstream circuit breaker open: "+b.service, err),
		}
	}
	// 若失败仅因 5xx/429，需把原始 *http.Response 原样交回外层 retryRT（由 retryRT 决定是否重试或返给业务）
	var statusErr *upstreamStatusErr
	if errors.As(err, &statusErr) && capturedResp != nil {
		return capturedResp, nil
	}
	return resp, err
}
```

*(配置 `gobreaker.Settings.IsSuccessful` 令 `errors.Is(err, apperror.ErrEgressDenied)` 与 `errors.Is(err, context.Canceled)` 返回 `true`，防止用户恶意投喂私网 URL 把整个熔断器打爆！)*

### 5.5 脱敏 `RoundTripper` 日志与 Prometheus 指标 (`observability.go`)

- **URL 查询参数脱敏 (`SanitizeURL`)**：自动将 URL Query 中键名含 `token`/`access_token`/`api_key`/`key`/`secret`/`password`/`signature`/`sig`/`code` 的值替换为 `[REDACTED]`，同时移除 `u.User` 密码部分；
- **Header 零打印**：仅记录 `service`、`profile`、`method`、`host`、`url_sanitized`、`status`、`duration_ms`、`error_kind`、`request_id`/`company_id`（从 `req.Context()` 自动透传）；
- **Prometheus 指标**（注册至 `obs.Metrics.Registerer()`）：
  - `synapse_http_client_requests_total` (`CounterVec`: `service`, `profile`, `method`, `status_class`, `error_kind`)
  - `synapse_http_client_request_duration_seconds` (`HistogramVec`: `service`, `profile`, `method`)
  - `synapse_http_client_retries_total` (`CounterVec`: `service`, `reason`)
  - `synapse_http_client_breaker_state` (`GaugeVec`: `service`, `host`，`0=closed`/`1=half_open`/`2=open`)

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：在 `synapse-server/go.mod` 引入 `github.com/sony/gobreaker/v2`，创建 `internal/core/egress/` 与 `internal/infra/httpclient/` 目录骨架。
- [ ] **T2**：编写 `internal/core/egress/guard_test.go`（红灯：覆盖全部内网/元数据/IPv6/`::ffff:`/CGNAT/端口/协议/中文 URL/Mock Resolver DNS Rebinding/7 跳重定向/跨源删 Authorization）→ 实现 `guard.go`。
- [ ] **T3**：编写 `internal/infra/httpclient/limits_test.go` 与 `profile_test.go`（红灯：`TrustedClient` 走私有 CA + 代理；`UntrustedEgressClient` 拦截回环地址 + `io.LimitReader` 超过限额返 `KindResponseTooLarge`；SSE `Streaming=true` 无总超时不断流且 `IdleReadTimeout` 生效）→ 实现 `profile.go`、`client.go`、`errors.go`、`limits.go`。
- [ ] **T4**：编写 `internal/infra/httpclient/retry_test.go`（红灯：`429 Retry-After` 秒数与 HTTP-Date 解析、`502/503/504` 指数退避重试、非幂等 POST 不重试、`EgressDenied` 不重试、`ctx` 取消中止等待）→ 实现 `retry.go`。
- [ ] **T5**：编写 `internal/infra/httpclient/breaker_test.go` 与 `observability_test.go`（红灯：5 次 503 触发 Open 返 `KindCircuitOpen`、冷却期后 HalfOpen 探测恢复、404 不触发熔断、`EgressDenied` 不触发熔断、日志擦除 `?access_token=xxx`、Prometheus 指标计数正确）→ 实现 `breaker.go`、`observability.go`。
- [ ] **T6**：全量运行 `cd synapse-server && go build ./... && go test -race -cover ./internal/core/egress/... ./internal/infra/httpclient/...`。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-03` 状态更新为 `已实施（commit <hash>）`；
- [ ] `synapse-server/README.md`：记录双 Profile 选用铁律（外部用户输入 URL 一律使用 `NewUntrustedEgress`，严禁混用 `NewTrusted`）及「业务降级归业务调用方」规范。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | `EgressGuard` 私网/元数据/IPv6/`::ffff:`/端口/协议拦截 | `internal/core/egress/guard_test.go` | 拦截 `127.0.0.1`、`10.1.2.3`、`172.20.0.1`、`192.168.1.1`、`169.254.169.254`、`[::1]`、`[fd00::1]`、`[::ffff:127.0.0.1]`、`[::ffff:169.254.169.254]`、`100.64.0.1`、`0.0.0.0`、`localhost`、`printer.local`、`svc.internal`、`file://`、`:22`；全部返回 `apperror.ErrEgressDenied`（403）；管理员白名单 `10.8.0.0/16` 与 `.corp.local` 放行 |
| 2 | `EgressGuard` 抗 DNS Rebinding 与 6 跳重定向 | `internal/core/egress/guard_test.go` | Mock `Resolver` 返回混含公网+`169.254.169.254` 双记录时整组拒绝；Mock `Resolver` 首次返公网二次返 `127.0.0.1` 时 `DialContext` 仅直连首次校验通过的 IP 或拦截重绑定；第 7 跳重定向返回 `"重定向次数过多"`；跨源 302 重定向自动删除 `Authorization` 与 `Cookie` 头；中文与空格 URL 经 `ToURI` 准确编码 |
| 3 | 双 Profile 严格隔离（企业私有 CA/代理 vs Untrusted 强拦截） | `internal/infra/httpclient/profile_test.go` | `TrustedClient` 携带自签 TLS `httptest.NewTLSServer` CA 证书成功请求本地回环服务端并支持代理路由；`UntrustedEgressClient` 请求同一 `127.0.0.1` 服务端立即返回 `KindEgressDenied`（绝不建立 TCP 连接） |
| 4 | `UntrustedEgressClient` 响应体 `io.LimitReader` 限流防 OOM | `internal/infra/httpclient/limits_test.go` | 设 `MaxResponseBytes = 1024`，服务端返回 2048 字节时读取返回 `*ClientError{Kind: KindResponseTooLarge}` 且 `errors.As` 可解出 `apperror.CodeTooLarge`（413） |
| 5 | SSE 流式无总超时模式与空闲读超时 | `internal/infra/httpclient/limits_test.go` | `Streaming: true` 且设 `TotalTimeout: 50ms`（被自动清零）、`IdleReadTimeout: 150ms`：服务端每 60ms 推 1 帧共持续 300ms 完整读完不断流；服务端停滞 >200ms 时触发超时中止 |
| 6 | 指数退避 + Jitter 与 `429 Retry-After` / `502/503/504` | `internal/infra/httpclient/retry_test.go` | 前两次返 `429`（带 `Retry-After: 0` 或 `503`）第三次返 `200` 时 GET 自动成功且 `attempts==3`；普通 `POST`（无 `WithIdempotent`）遇 503 仅发 1 次不重试；带 `WithIdempotent` 且设 `GetBody` 的 POST 正常重试；遇 `KindEgressDenied` 零重试 |
| 7 | `service/host` 维度 `sony/gobreaker/v2` 熔断器 | `internal/infra/httpclient/breaker_test.go` | Host A 连续 5 次 503 后第 6 次请求未触达服务端直接返回 `KindCircuitOpen`，同时 Host B 正常放行（互不影响）；冷却期满 HalfOpen 探针返回 200 后恢复 Closed；连续 10 次 404 不触发熔断；连续 10 次 `EgressDenied` 不触发熔断 |
| 8 | 脱敏 `RoundTripper` 日志与 Prometheus 指标 | `internal/infra/httpclient/observability_test.go` | 请求 `https://api.example/v1?access_token=topsecret&page=1` 携带 `Authorization: Bearer sk-12345678`，日志缓冲区不含 `topsecret` 与 `sk-12345678`，但含 `service`、`status`、`duration_ms`、`request_id`；Prometheus 注册表采集到 `synapse_http_client_requests_total` 与 `synapse_http_client_breaker_state` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race -cover ./internal/core/egress/... ./internal/infra/httpclient/...` 全绿，行覆盖率 ≥ 85%
- [ ] §2 交付物表 7 项与 §4.1 导出符号逐项核对一致
- [ ] 确认 `UntrustedEgressClient` 在任何默认配置下均无法绕过 `EgressGuard.DialContext` 与 `io.LimitReader` 限流
- [ ] 确认 `Streaming: true` 构造的 `http.Client.Timeout == 0`，不会截断长时 SSE 流
- [ ] 代码审查确认 `internal/infra/httpclient` 不包含任何业务降级逻辑（仅返回结构化 `*ClientError`）
- [ ] `SA-01` 回归测试全绿

---

## 10. 可提交节点

- 建议 commit 1（抗 DNS Rebinding 出口守卫）：`feat(egress): add DialContext-level anti-SSRF and DNS-rebinding EgressGuard with 6-hop redirect checks (spec 03, PR SA-03)`
- 建议 commit 2（双 Profile 弹性 HTTP 客户端）：`feat(httpclient): add Trusted/Untrusted profiles, SSE streaming mode, retry/Retry-After, gobreaker and redacted metrics (spec 03, PR SA-03)`
