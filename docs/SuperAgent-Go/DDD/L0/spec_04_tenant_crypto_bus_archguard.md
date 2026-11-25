# spec 04 | L0 租户安全、信封加密、进程内事件总线、内存缓存限流与架构守卫

- **状态**：`冻结待实施`
- **篇号顺序**：04（L0 安全与运行原语收官篇，冻结全仓 L0–L6 静态分层防线）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-04`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1, §2, §4.1
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
  - 归档母本：[spec_01_core_tenant_crypto_egress.md](../_epic/L1/spec_01_core_tenant_crypto_egress.md)（`tenant`/`crypto`/`eventbus`/`arch_test.go` 部分）
- **前置依赖**：[L0/spec_01 (SA-01)](./spec_01_config_obs_error_lifecycle.md)（依赖 `internal/core/apperror`、`internal/infra/obs`）
- **下游消费者**：`SA-05`–`SA-42`（所有领域模型、持久化 Store、认证会话、供应商凭证加密、IM 通知、链接缓存与全仓编译期架构守卫）

---

## 1. 背景与范围

本篇完成 **L0 基础设施底座层的最后 5 个关键构件**，将 Java 原版多租户隔离、AES-256-GCM 信封加密、Spring 进程内事件发布与本地缓存/限流机制 1:1 等价重构并加固，同时在 `internal/arch_test.go` 中立下全仓 L0–L6 静态包依赖红线，为后续 38 篇业务 PR（`SA-05`–`SA-42`）保驾护航：
1. **Fail-Closed 多租户上下文与路径越界防御 (`internal/core/tenant`)**：替代 Java `ThreadLocal` 版 `TenantContext.java:L16-L96` 与 `TenantPaths.java:L10-L29`；通过 Go `context.Context` 强约束 `tenant.Require(ctx)`（缺失即 Fail-Closed 报错），提供 `tenant.Detach(ctx)`（剥离 HTTP 取消与数据库事务句柄，保留 `company_id`/`request_id`/`user_id`），提供 `TenantPaths` + `SafeJoin` 抵御 `..` 路径穿越与符号链接逃逸，并冻结 `ValidateOrgSlug` 12 保留词规则。
2. **每租户 AES-256-GCM DEK 信封加密与主密钥 KEK 轮换 (`internal/core/crypto`)**：1:1 密文协议兼容 Java `SecretCipher.java:L36-L162`（`v1:` 平台密文 / `v2:<companyID>:` 租户密文 + AAD 强绑定），并在 V1 新增**零停机主密钥 KEK 轮换**（支持 `PreviousKeysB64` 多主密钥回退解封 + `RewrapCompanyDEK` 仅重封 32 字节公司 DEK 而无需重加密海量租户凭证）。
3. **进程内泛型同步/异步事件总线 (`internal/core/eventbus`)**：替代 Spring `ApplicationEventPublisher`（`CompanyStarted.java`、`IntegrationChanged.java`、`NoticeEvents.java`、`InboxNotifyRequested.java`），提供编译期类型安全的泛型 `Subscribe[T]` / `Publish[T]` / `PublishAsync[T]`、单订阅者 `panic recover` 隔离、异步 `tenant.Detach(ctx)` 透传、有界并发度与优雅排空 `Drain(ctx)`。
4. **纯进程内分片 TTL 缓存与防爆破滑动窗口限流器 (`internal/infra/memcache`)**：**严禁引入 Redis**；纯 Go 标准库实现 32 分片并发安全 TTL 缓存（含单飞 `GetOrLoad` 防击穿、LRU/容量上限淘汰与定期清理任务，供 `LinkReader` 10 分钟缓存、`ModelPrice`、设置/会话读缓存复用）以及滑动窗口防爆破限流器（供 `SA-15` 登录接口防口令爆破与暴力撞库，带最大 Key 上限防内存耗尽）。
5. **全仓 AST 分层与红线守卫 (`internal/arch_test.go`)**：利用 `go/parser` 静态解析 `synapse-server/internal/...` 全部非测试源码 `import`，强制 L0→L6 严格单向依赖、**绝对禁止 `internal/agent` ↔ `internal/task` 互引**、禁止业务层直引 `pgx`/`goose`/`os/exec` 以及禁止全仓引入任何 Redis 驱动依赖。

**本篇边界（1.5 人天）**：
- 本篇不定义具体业务实体（`domain.Company`/`Agent`/`Task` 等归属 `SA-05`），因此 `crypto.CompanyKeyStore` 使用纯原始字符串窄接口，与 L1 解耦；
- 本篇不定义具体业务事件结构体（`CompanyStarted`、`TaskStatusChanged` 等在 `SA-05` 或各自归属域定义并直接注册到本篇泛型 `eventbus.Bus`）；
- 本篇**严禁引入任何 Redis 客户端库或预留 Redis 抽象接口**。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 | 对应 Java 参考源 |
|---|---|---|---|---|
| 1 | Fail-Closed 多租户 `context.Context`、`Detach`、`ValidateOrgSlug` 与防穿越/防符号链接逃逸 `TenantPaths` | `internal/core/tenant/context.go`、`paths.go`、`slug.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/tenant/TenantContext.java:L16-L96`、`TenantPaths.java:L10-L29`、`superagent-platform/src/main/java/com/superagent/platform/tenant/OrganizationService.java:L32-L34` |
| 2 | AES-256-GCM 租户信封加密 `SecretCipher`（`v1:`/`v2:<companyID>:` 兼容 + `0600` 原子密钥文件 + KEK 主密钥轮换 `RewrapCompanyDEK`） | `internal/core/crypto/cipher.go`、`rotate.go` | 新增 | `superagent-core/src/main/java/com/superagent/core/security/SecretCipher.java:L36-L162` |
| 3 | 进程内泛型同步/异步事件总线 `Bus`（`Subscribe[T]`、`Publish[T]`、`PublishAsync[T]`、panic 隔离、受控并发与 `Drain`） | `internal/core/eventbus/bus.go` | 新增 | 替代 Spring `ApplicationEventPublisher`（参照 `CompanyStarted.java`、`IntegrationChanged.java`、`NoticeEvents.java:L7-L48`、`InboxNotifyRequested.java`） |
| 4 | 纯进程内分片 TTL 缓存 `ShardedCache[K,V]`（含 singleflight 防击穿与容量淘汰）+ 防爆破滑动窗口限流器 `SlidingWindowLimiter`（零 Redis） | `internal/infra/memcache/cache.go`、`ratelimit.go` | 新增 | 升级替代 `superagent-docs/src/main/java/com/superagent/docs/link/LinkReaderService.java:L24-L36` 本地缓存 + V1 登录防爆破新增 |
| 5 | 全仓 L0–L6 严格单向分层、禁 `agent↔task` 互引、禁越权 `pgx`/`os/exec` 与零 Redis AST 守卫测试 | `internal/arch_test.go` | 新增 | Go 工程编译期/测试期新防线 |
| 6 | 5 个模块的完整同包单元测试套件 | `internal/core/tenant/tenant_test.go`、`internal/core/crypto/cipher_test.go`、`internal/core/eventbus/bus_test.go`、`internal/infra/memcache/*_test.go` | 新增 | 对齐并超越 `SecretCipherTest.java:L24-L45` |

---

## 3. 数据库设计

本篇属于 L0 核心安全与运行原语层，**无数据库表、无 SQL 迁移脚本，严禁引入 `pgx` 或 `database/sql`**。

本篇通过 `crypto.CompanyKeyStore` 极简持久化端口与 L1 `company` 表解耦：
- `GetDataKeyCipher(ctx, companyID) (cipher string, exists bool, err error)` 读取 `company.data_key_cipher`；
- `SaveDataKeyCipher(ctx, companyID, cipher string) error` 原子回写被当前主密钥 KEK 封存后的 `v1:<sealedDEK>`（由 L1 `SA-07` `CompanyStore` 在 `wire.go` 通过轻量适配器实现）。

---

## 4. 冻结契约

### 4.1 导出符号

#### A. `internal/core/tenant`

```go
package tenant

import (
	"context"
	"errors"
)

const DefaultSentinelID = "superagent" // 仅用于登录前 Logo 与 /mcp 自检，非真实企业

var (
	ErrMissingTenant  = errors.New("missing tenant context")
	ErrInvalidCompany = errors.New("invalid company id")
	ErrPathEscape     = errors.New("path escapes tenant root")
)

func WithCompany(ctx context.Context, companyID string) (context.Context, error)
func Require(ctx context.Context) (string, error)
func Optional(ctx context.Context) (string, bool)
func Detach(ctx context.Context) context.Context // 剥离取消信号与 DB 事务，保留 companyID / request_id / user_id

func ValidateCompanyID(id string) error          // ^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$ 且不含 ".."
func ValidateOrgSlug(raw string) (string, error) // ^[a-z][a-z0-9-]{1,30}[a-z0-9]$ 且拒绝 12 保留词

// TenantPaths 路径越界防御
func Path(ctx context.Context, root string) (string, error)
func PathFor(root, companyID string) (string, error)
func SafeJoin(baseDir string, relParts ...string) (string, error) // 校验清理后路径与已存在路径的 EvalSymlinks 均不逃逸 baseDir
```

#### B. `internal/core/crypto`

```go
package crypto

import (
	"context"
	"errors"
)

var (
	ErrCrossTenantSecret = errors.New("secret belongs to another company")
	ErrInvalidCiphertext = errors.New("secret is not in a recognised format")
	ErrDecryptFailed     = errors.New("cannot decrypt secret (key mismatch or tampered)")
	ErrUnknownCompany    = errors.New("unknown company")
)

// CompanyKeyStore 零领域耦合窄端口：仅读写 company.data_key_cipher 列
type CompanyKeyStore interface {
	GetDataKeyCipher(ctx context.Context, companyID string) (cipher string, exists bool, err error)
	SaveDataKeyCipher(ctx context.Context, companyID, cipher string) error
}

type Options struct {
	MasterKeyB64    string          // 当前激活主密钥 KEK（32 字节标准 Base64）
	PreviousKeysB64 []string        // 历史 KEK 列表（用于零停机密钥轮换期间解密旧 DEK / 旧 v1: 密文）
	KeyFile         string          // MasterKeyB64 为空时读取或原子 0600 自动生成，默认 ./data/secret.key
	Store           CompanyKeyStore // nil 时退化为纯主密钥 v1: 模式（供工具与测试使用）
}

type SecretCipher struct { /* mu, masterAEAD, previousAEADs, store, dekCache */ }

func NewSecretCipher(opts Options) (*SecretCipher, error)
func GenerateKeyB64() (string, error) // 生成 32 字节加密随机标准 Base64 密钥

func (c *SecretCipher) Encrypt(ctx context.Context, plain string) (string, error)
func (c *SecretCipher) Decrypt(ctx context.Context, stored string) (string, error)

// 主密钥 KEK 轮换原语（零停机，仅重封公司 DEK，无需重加密租户业务密文）
func (c *SecretCipher) RotateMasterKey(newMasterKeyB64 string) error
func (c *SecretCipher) RewrapCompanyDEK(ctx context.Context, companyID string) (rewrapped bool, err error)
func (c *SecretCipher) ReencryptPlatformSecret(ctx context.Context, stored string) (updated string, changed bool, err error)
func (c *SecretCipher) InvalidateCompanyCache(companyID string)
```

#### C. `internal/core/eventbus`

```go
package eventbus

import (
	"context"
	"log/slog"
)

type Option func(*Bus)

func WithLogger(l *slog.Logger) Option
func WithMaxAsyncConcurrency(n int) Option // 默认 64，防止异步事件风暴无限创建 goroutine
func WithOnPanic(fn func(eventName string, recovered any, stack []byte)) Option

type Bus struct { /* mu, subs, sem, wg, logger, onPanic, closed */ }

func New(opts ...Option) *Bus
func (b *Bus) Drain(ctx context.Context) error // 等待所有在途异步事件处理器执行完毕

// 泛型编译期类型安全 API（支持返回 unsubscribe 取消函数）
func Subscribe[T any](b *Bus, fn func(ctx context.Context, ev T)) (unsubscribe func())
func Publish[T any](ctx context.Context, b *Bus, ev T)      // 同步按注册顺序执行，单订阅者 panic 隔离
func PublishAsync[T any](ctx context.Context, b *Bus, ev T) // 先 tenant.Detach(ctx) 再受控并发异步派发
```

#### D. `internal/infra/memcache`（纯进程内，零 Redis）

```go
package memcache

import (
	"context"
	"time"
)

// 1. 分片 TTL 内存缓存（支持 singleflight 防缓存击穿与定时清理）
type CacheOptions struct {
	Shards          int // 默认 32（自动向上取 2 的幂）
	MaxEntries      int // 全缓存最大条目数（0=不限，建议按场景显式设置防 OOM）
	CleanupInterval time.Duration
}

type CacheStats struct {
	Entries   int
	Hits      uint64
	Misses    uint64
	Evictions uint64
}

type ShardedCache[K comparable, V any] struct { /* shards, mask, maxPerShard, nowFn, stats, sf */ }

func NewShardedCache[K comparable, V any](opts CacheOptions) *ShardedCache[K, V]
func (c *ShardedCache[K, V]) Get(key K) (V, bool)
func (c *ShardedCache[K, V]) Set(key K, val V, ttl time.Duration)
func (c *ShardedCache[K, V]) GetOrLoad(ctx context.Context, key K, ttl time.Duration, loader func(ctx context.Context) (V, error)) (V, error)
func (c *ShardedCache[K, V]) Delete(key K)
func (c *ShardedCache[K, V]) DeleteFunc(predicate func(key K) bool) int // 按前缀/租户批量失效
func (c *ShardedCache[K, V]) PurgeExpired() int
func (c *ShardedCache[K, V]) StartJanitor(ctx context.Context, interval time.Duration) error // 兼容 lifecycle.Loop
func (c *ShardedCache[K, V]) Len() int
func (c *ShardedCache[K, V]) Stats() CacheStats

// 2. 防爆破滑动窗口限流器（纯进程内分片，零 Redis）
type LimiterOptions struct {
	Limit   int           // 窗口内允许最大次数（如登录失败 5 次）
	Window  time.Duration // 滑动窗口时长（如 15m）
	Shards  int           // 默认 32
	MaxKeys int           // 最大跟踪 Key 数，默认 100_000（防随机 IP 洪泛 OOM）
}

type Decision struct {
	Allowed    bool
	Remaining  int
	RetryAfter time.Duration
	ResetAt    time.Time
}

type SlidingWindowLimiter struct { /* shards, mask, limit, window, maxPerShard, nowFn */ }

func NewSlidingWindowLimiter(opts LimiterOptions) *SlidingWindowLimiter
func (l *SlidingWindowLimiter) Allow(key string) Decision
func (l *SlidingWindowLimiter) AllowN(key string, n int) Decision
func (l *SlidingWindowLimiter) Peek(key string) Decision            // 只读检查当前是否已被锁定（不计次，适合登录前先判锁）
func (l *SlidingWindowLimiter) RecordFailure(key string) Decision   // 记录 1 次失败（如密码错误时调用）
func (l *SlidingWindowLimiter) Reset(key string)                    // 登录成功后清零该账号失败窗口
func (l *SlidingWindowLimiter) PurgeExpired() int
```

### 4.2 路由契约

本篇不注册 HTTP 路由。
- `tenant.WithCompany` 由 L5 `TenantMiddleware`（`SA-36`）在鉴权通过后注入请求上下文；
- `memcache.SlidingWindowLimiter` 由 L5 `POST /api/v1/auth/login`（`SA-15`/`SA-36`）调用 `Peek` / `RecordFailure` / `Reset`，超限时返回 `apperror.RateLimited(...)`（HTTP `429 rate_limited`）并附带 `Retry-After` 响应头。

### 4.3 错误语义

| 场景 | 返回错误 | 映射 HTTP / 处理约定 |
|---|---|---|
| 缺失租户上下文 (`tenant.Require`) | `tenant.ErrMissingTenant` | 系统内部编程缺陷，L5 映射为 `500 internal_error` 并打 `ERROR` 日志，绝不回落默认租户 |
| 非法 `companyID` / 路径穿越 / 符号链接逃逸 | `tenant.ErrInvalidCompany` / `tenant.ErrPathEscape` | 业务边界转 `400 invalid_argument` 或 `403 forbidden` |
| 非法组织 slug / 12 保留词 (`ValidateOrgSlug`) | `apperror.InvalidArg(...)` | `400 invalid_argument`（与 `OrganizationService.java:L74-L82` 中文提示一致） |
| 跨租户密文解密 / 篡改密文前缀 | `crypto.ErrCrossTenantSecret` / `crypto.ErrDecryptFailed` | 严格拒止，绝不回落主密钥尝试解密 |
| 登录/接口触发滑动窗口限流 | `Decision{Allowed: false, RetryAfter: d}` | 调用方返回 `apperror.RateLimited(...)`（`429 rate_limited`） |

### 4.4 SQL 契约

本篇无 SQL 语句。但通过 `internal/arch_test.go` 强制约束全仓数据库驱动隔离：**除 `internal/infra/db`、`internal/storage/postgres` 与 `cmd/synapse-server` 外，任何包严禁 import `github.com/jackc/pgx/v5` 或 `github.com/pressly/goose/v3`**。

---

## 5. 行为语义（含 Java 源码对照、Go 契约定义与详细伪代码）

### 5.1 多租户上下文与 `TenantPaths` 越界防御 (`internal/core/tenant`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/tenant/TenantContext.java:L16-L96`、`TenantPaths.java:L10-L29`、`superagent-platform/src/main/java/com/superagent/platform/tenant/OrganizationService.java:L32-L34,L73-L82`。

```go
package tenant

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
	"github.com/Karlsk/synapse-os/internal/infra/obs"
)

var (
	safeCompanyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	orgSlugRe     = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	reservedSlugs = map[string]struct{}{
		"admin": {}, "api": {}, "www": {}, "root": {}, "system": {}, "login": {},
		"mcp": {}, "static": {}, "support": {}, "help": {}, "default": {}, "platform": {},
	}
)

type companyCtxKey struct{}

// WithCompany 同时绑定私有鉴权 key 与 obs 日志镜像 key；
// 安全边界铁律：Require() 仅读取私有 companyCtxKey{}，绝不信任 obs.CompanyIDFrom(ctx)。
func WithCompany(ctx context.Context, companyID string) (context.Context, error) {
	if err := ValidateCompanyID(companyID); err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, companyCtxKey{}, companyID)
	ctx = obs.WithCompanyID(ctx, companyID)
	return ctx, nil
}

func Require(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrMissingTenant
	}
	id, ok := ctx.Value(companyCtxKey{}).(string)
	if !ok || id == "" {
		return "", ErrMissingTenant
	}
	return id, nil
}

// Detach 构造独立生命周期上下文：
// 1) 剥离父 ctx 取消/超时信号（避免 SSE 断开或 HTTP 返回中断后台异步回合）；
// 2) 剥离可能存在的数据库事务句柄（避免异步协程使用已提交/关闭的 pgx.Tx）；
// 3) 完整保留租户 companyID 与可观测三元组（request_id / company_id / user_id）。
func Detach(ctx context.Context) context.Context {
	out := context.Background()
	if id, ok := Optional(ctx); ok {
		out = context.WithValue(out, companyCtxKey{}, id)
	}
	return obs.CopyMeta(ctx, out)
}

// SafeJoin 将用户提供的相对路径安全拼接到 baseDir 下，防御 "../" 穿越与已存在符号链接逃逸。
func SafeJoin(baseDir string, relParts ...string) (string, error) {
	absBase, err := filepath.Abs(filepath.Clean(baseDir))
	if err != nil {
		return "", fmt.Errorf("%w: invalid base dir: %v", ErrPathEscape, err)
	}
	parts := append([]string{absBase}, relParts...)
	target := filepath.Clean(filepath.Join(parts...))
	rel, err := filepath.Rel(absBase, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("%w: %q escapes %q", ErrPathEscape, target, absBase)
	}
	// 若 baseDir 与目标路径（或目标最近的已存在父目录）在磁盘上存在，校验 EvalSymlinks 防符号链接越界
	if realBase, err := filepath.EvalSymlinks(absBase); err == nil {
		checkPath := nearestExistingAncestor(target)
		if realTarget, err := filepath.EvalSymlinks(checkPath); err == nil {
			realRel, err := filepath.Rel(realBase, realTarget)
			if err != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(os.PathSeparator)) {
				return "", fmt.Errorf("%w: symlink %q escapes %q", ErrPathEscape, realTarget, realBase)
			}
		}
	}
	return target, nil
}
```

### 5.2 AES-256-GCM 租户信封加密与 KEK 轮换 (`internal/core/crypto`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/security/SecretCipher.java:L36-L162`、`SecretCipherTest.java:L24-L45`。

**密文协议与轮换语义**：
- 租户密文：`v2:<companyID>:<base64(nonce12 || ciphertext || tag16)>`，由该租户 32 字节 DEK 加密，AAD = `[]byte(companyID)`；与 Java 原版逐字节互通！
- 公司 DEK 密文（`company.data_key_cipher`）：`v1:<base64(nonce12 || sealed(base64(rawDEK32)) || tag16)>`，由主密钥 KEK 加密，AAD = `[]byte("company:" + companyID)`；
- 平台级密文（无租户上下文或 `Store == nil`）：`v1:<base64(nonce12 || ciphertext || tag16)>`，由主密钥 KEK 加密，AAD = `nil`；
- **密钥文件 `0600` 原子生成**：首次创建 `secret.key` 时写入同目录临时文件 → `os.Chmod(0600)` → `os.Rename`，防止多进程启动或异常断电导致半写损坏；
- **零停机 KEK 主密钥轮换流程**：
  1. 运维生成新 32B Base64 密钥 `K_new`，配置 `MasterKeyB64 = K_new` 并将旧密钥移入 `PreviousKeysB64 = [K_old]`；
  2. 服务无缝重启后，`openMaster` 优先尝试 `masterAEAD (K_new)`，失败则依次回退 `previousAEADs (K_old)` 解封公司 DEK；所有新写入/新 DEK 自动使用 `K_new`；
  3. 调用管理任务对全部公司执行 `RewrapCompanyDEK(ctx, cid)`：解封 DEK → 用 `K_new` 重封同一 DEK 写回 `company.data_key_cipher` → **租户表里成千上万条 `v2:<cid>:` 业务密文一行都不用改动！**
  4. 全量重封确认完成后，运维可安全移除 `PreviousKeysB64` 中的 `K_old`。

```go
package crypto

func (c *SecretCipher) openMaster(sealedB64 string, aad []byte) (plain string, usedPrev bool, err error) {
	c.mu.RLock()
	active := c.masterAEAD
	prevs := c.previousAEADs
	c.mu.RUnlock()

	if plain, err = openAEAD(active, sealedB64, aad); err == nil {
		return plain, false, nil
	}
	for _, prev := range prevs {
		if plain, err = openAEAD(prev, sealedB64, aad); err == nil {
			return plain, true, nil
		}
	}
	return "", false, ErrDecryptFailed
}

// RewrapCompanyDEK 仅在公司 DEK 仍由历史 KEK 封装时，用当前激活 KEK 重封并写回存储（幂等）。
func (c *SecretCipher) RewrapCompanyDEK(ctx context.Context, companyID string) (bool, error) {
	if c.store == nil {
		return false, nil
	}
	c.dekMu.Lock()
	defer c.dekMu.Unlock()

	stored, exists, err := c.store.GetDataKeyCipher(ctx, companyID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("%w: %s", ErrUnknownCompany, companyID)
	}
	if stored == "" {
		return false, nil // 尚未生成 DEK，首次使用时自然由新 KEK 封装
	}
	if !strings.HasPrefix(stored, prefixMaster) {
		return false, ErrInvalidCiphertext
	}
	aad := []byte("company:" + companyID)
	b64DEK, usedPrev, err := c.openMaster(stored[len(prefixMaster):], aad)
	if err != nil {
		return false, err
	}
	if !usedPrev {
		return false, nil // 已由当前激活 KEK 封装，幂等跳过
	}
	rawDEK, err := base64.StdEncoding.DecodeString(b64DEK)
	if err != nil || len(rawDEK) != keySize {
		return false, ErrDecryptFailed
	}

	c.mu.RLock()
	active := c.masterAEAD
	c.mu.RUnlock()

	resealed, err := sealAEAD(active, b64DEK, aad)
	if err != nil {
		return false, err
	}
	if err := c.store.SaveDataKeyCipher(ctx, companyID, prefixMaster+resealed); err != nil {
		return false, err
	}
	aead, err := newGCM(rawDEK)
	if err != nil {
		return false, err
	}
	c.dekCache.Store(companyID, aead)
	return true, nil
}

// RotateMasterKey 支持进程内热轮换：将旧 active 压入 previousAEADs 头部，激活新 KEK。
func (c *SecretCipher) RotateMasterKey(newMasterKeyB64 string) error {
	raw, err := decodeKey32(newMasterKeyB64)
	if err != nil {
		return err
	}
	newAEAD, err := newGCM(raw)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.previousAEADs = append([]cipher.AEAD{c.masterAEAD}, c.previousAEADs...)
	c.masterAEAD = newAEAD
	return nil
}
```

### 5.3 进程内泛型同步/异步事件总线 (`internal/core/eventbus/bus.go`)

```go
package eventbus

import (
	"context"
	"reflect"
	"runtime/debug"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

func Subscribe[T any](b *Bus, fn func(ctx context.Context, ev T)) func() {
	typ := reflect.TypeFor[T]()
	id := b.nextID.Add(1)
	entry := subEntry{
		id: id,
		fn: func(ctx context.Context, raw any) {
			fn(ctx, raw.(T))
		},
	}
	b.mu.Lock()
	b.subs[typ] = append(b.subs[typ], entry)
	b.mu.Unlock()

	return func() { b.unsubscribe(typ, id) }
}

func Publish[T any](ctx context.Context, b *Bus, ev T) {
	typ := reflect.TypeFor[T]()
	handlers := b.snapshot(typ) // 读锁拷贝切片，锁外执行，避免订阅回调再 Publish 死锁
	for _, h := range handlers {
		b.invokeSafe(ctx, typ.String(), h.fn, ev)
	}
}

func PublishAsync[T any](ctx context.Context, b *Bus, ev T) {
	typ := reflect.TypeFor[T]()
	handlers := b.snapshot(typ)
	if len(handlers) == 0 {
		return
	}
	// 关键：在回到调用方之前立即剥离 HTTP 取消与 DB 事务，保留 company_id / request_id / user_id
	detached := tenant.Detach(ctx)
	for _, h := range handlers {
		entry := h
		b.wg.Add(1)
		go func() {
			defer b.wg.Done()
			b.sem <- struct{}{} // 有界并发信号量
			defer func() { <-b.sem }()
			b.invokeSafe(detached, typ.String(), entry.fn, ev)
		}()
	}
}

func (b *Bus) invokeSafe(ctx context.Context, evName string, fn func(context.Context, any), ev any) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			b.logger.ErrorContext(ctx, "eventbus subscriber panicked",
				"event", evName, "panic", r, "stack", string(stack))
			if b.onPanic != nil {
				b.onPanic(evName, r, stack)
			}
		}
	}()
	fn(ctx, ev)
}
```

### 5.4 纯进程内分片 TTL 缓存与防爆破滑动窗口限流器 (`internal/infra/memcache`)

> **架构红线**：原版无 Redis，本包采用纯 Go 标准库进程内分片并发结构实现，**零 Redis 依赖**。
> **Java 源码对照**：升级替代 `superagent-docs/src/main/java/com/superagent/docs/link/LinkReaderService.java:L24-L36`（全局 `synchronized` 锁 + `LinkedHashMap`）。

- **`ShardedCache[K comparable, V any]`**：
  - 默认 32 分片（通过 `hash/maphash` 泛型键哈希定位分片，避免全局互斥锁竞争）；
  - `Get` 命中但已过期时在写锁或延迟删除路径淘汰并计为 miss；
  - `Set` 当分片条数达到 `maxPerShard` 时优先清理本分片过期项，仍满则淘汰最老插入/最少最近访问项（维护双向链表或轻量 LRU 队列，O(1) 淘汰）；
  - `GetOrLoad` 先查缓存，未命中时使用进程内按 Key 单飞（`singleflight`）合并并发回源请求，仅执行 1 次 `loader(ctx)`，并将成功结果以 `ttl` 写入缓存（`loader` 报错不缓存负结果）；
  - `StartJanitor(ctx, interval)` 实现 `lifecycle.Loop` 风格后台定期清扫过期键。
- **`SlidingWindowLimiter`**（双窗口加权滑动窗口算法，O(1) 空间与时间）：
  - 每个 Key 仅保存 `{prevCount, currCount, windowStart}` 24 字节，无需保存时间戳切片，抗高并发爆破零 GC 压力；
  - 有效计数估算：`effective = float64(prevCount) * (1 - elapsed/window) + float64(currCount)`；
  - `AllowN(key, n)`：若 `effective + n > limit` 则拒绝，返回 `Allowed=false` 及精确估算的 `RetryAfter`（至少 `1s`）；否则递增 `currCount += n` 返回 `Allowed=true` 与 `Remaining`；
  - 登录防爆破标准接入模式（供 `SA-15`）：收到登录请求先 `Peek(key)`，若已锁定直接 429；密码校验失败调 `RecordFailure(key)`；密码校验成功调 `Reset(key)` 立即释放计数；
  - 当单分片 Key 数达到 `maxPerShard`（全局默认 100,000）时先清扫过期窗格，仍满则驱逐最久未活跃的旧窗格，防随机 IP 攻击打爆内存。

```go
package memcache

func (l *SlidingWindowLimiter) evaluate(s *limiterShard, key string, now time.Time, add int) Decision {
	s.mu.Lock()
	defer s.mu.Unlock()

	ent, ok := s.entries[key]
	if !ok {
		if add == 0 {
			return Decision{Allowed: true, Remaining: l.limit, ResetAt: now.Add(l.window)}
		}
		if l.maxPerShard > 0 && len(s.entries) >= l.maxPerShard {
			s.evictStaleOrOldest(now, l.window)
		}
		ent = &windowEntry{windowStart: now, lastSeen: now}
		s.entries[key] = ent
	}

	// 推进滑动窗口
	elapsed := now.Sub(ent.windowStart)
	if elapsed >= 2*l.window {
		ent.prevCount = 0
		ent.currCount = 0
		ent.windowStart = now
		elapsed = 0
	} else if elapsed >= l.window {
		ent.prevCount = ent.currCount
		ent.currCount = 0
		ent.windowStart = ent.windowStart.Add(l.window)
		elapsed = now.Sub(ent.windowStart)
	}

	weight := 1.0 - float64(elapsed)/float64(l.window)
	if weight < 0 {
		weight = 0
	}
	effective := float64(ent.prevCount)*weight + float64(ent.currCount)
	if effective+float64(add) > float64(l.limit) {
		retryAfter := l.window - elapsed
		if retryAfter < time.Second {
			retryAfter = time.Second
		}
		return Decision{
			Allowed:    false,
			Remaining:  0,
			RetryAfter: retryAfter,
			ResetAt:    ent.windowStart.Add(l.window),
		}
	}
	if add > 0 {
		ent.currCount += add
		ent.lastSeen = now
		effective += float64(add)
	}
	rem := l.limit - int(effective)
	if rem < 0 {
		rem = 0
	}
	return Decision{
		Allowed:   true,
		Remaining: rem,
		ResetAt:   ent.windowStart.Add(l.window),
	}
}
```

### 5.5 全仓 L0–L6 AST 分层与红线守卫 (`internal/arch_test.go`)

使用标准库 `go/parser` + `go/token` 遍历 `synapse-server/internal/...` 全部非测试 `.go` 文件及 `synapse-server/go.mod`，在每次 `go test ./...` 强制执行 **6 条不可逾越的架构红线**：

```go
package internal_test

// 1. L0–L5 严格层级映射（cmd/synapse-server 为 L6 装配根，允许向下引用全部层）
var layerOfTopPkg = map[string]int{
	"core":         0, // L0: apperror, tenant, crypto, egress, eventbus + L0/L1 纯契约 domain, store, spi, api
	"infra":        0, // L0: config, obs, lifecycle, db, httpclient, memcache
	"storage":      1, // L1: postgres Store 实现
	"runtime":      1, // L1: launcher, runner, gateway, mcp, clipaths, attachment
	"platform":     2, // L2: tenant, user, auth, settings, notice, integration, connector, provider, analytics
	"files":        2, // L2
	"project":      2, // L2
	"skills":       2, // L2
	"agent":        3, // L3
	"task":         3, // L3
	"cost":         3, // L3
	"docs":         3, // L3
	"event":        4, // L4
	"orchestrator": 4, // L4
	"web":          5, // L5
	"ui":           5, // L5
}

// 2. 强制规则校验清单：
// Rule 1 (单向分层): 严禁任意包 import 层级严格大于自身的 internal/* 包（targetLayer > srcLayer）。
// Rule 2 (L0 内部纯度与子 DAG):
//   - internal/core/apperror 零内部包依赖；
//   - internal/core/* 严禁 import internal/infra/{config,db,httpclient,lifecycle,memcache}（仅 internal/core/tenant 允许单向引用 internal/infra/obs 透传日志元信息）；
//   - internal/core/* 严禁 import gin、pgx、goose、database/sql、os/exec；
//   - internal/infra/* 严禁 import L1–L5 包。
// Rule 3 (同层平行模块全面禁互引):
//   - 凡 srcLayer >= 1 且 targetLayer == srcLayer 且 srcTopPkg != targetTopPkg 的跨模块 import 一律禁止！
//   - 全面覆盖：L1 (storage <-> runtime)、L2 (platform / files / project / skills 之间)、
//     L3 (agent / task / cost / docs 之间)、L4 (event <-> orchestrator)、L5 (web <-> ui)；
//   - 同层协作必须通过 L0 internal/core/{spi,store,eventbus} 倒置解耦，并在 L6 cmd/synapse-server/wire.go 装配。
// Rule 4 (数据库驱动隔离): 仅 internal/infra/db、internal/storage/postgres、cmd/synapse-server 允许 import github.com/jackc/pgx/v5 或 github.com/pressly/goose/v3。
// Rule 5 (子进程隔离): 仅 internal/runtime/launcher 允许 import os/exec（业务层与 platform/provider/installer 均经 spi.ProcessLauncher 启动子进程）。
// Rule 6 (零 Redis 红线): 解析 go.mod 与全仓 import，严禁出现任何 Redis 驱动包（如 github.com/redis/go-redis、github.com/gomodule/redigo 等）。
```

*(同时提供纯内存单测 `TestArchRulesDetectViolations`，用临时 AST 违规样例验证 6 条守卫规则确实能精准抓出 `agent→task`、`L1→L2`、`web→pgx`、`core→db` 与非法 Redis import，防止守卫测试退化为空跑。)*

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：建立 `internal/core/{tenant,crypto,eventbus}` 与 `internal/infra/memcache` 目录骨架。
- [ ] **T2**：编写 `internal/core/tenant/tenant_test.go`（红灯：Fail-Closed `Require`、`Detach` 剥离取消信号保留三元组、`ValidateOrgSlug` 12 保留词、`PathFor` 与 `SafeJoin` 防 `..` 及真实 `os.Symlink` 越界逃逸）→ 实现 `context.go`、`paths.go`、`slug.go`。
- [ ] **T3**：编写 `internal/core/crypto/cipher_test.go`（红灯：`v1:`/`v2:<companyID>:` 跨租户拒止、篡改前缀拒止、并发 32 协程首次生成 DEK 幂等、`secret.key` `0600` 权限、KEK 轮换 `PreviousKeysB64` 解密 + `RewrapCompanyDEK` 后移除旧 KEK 仍正常解密）→ 实现 `cipher.go`、`rotate.go`。
- [ ] **T4**：编写 `internal/core/eventbus/bus_test.go`（红灯：泛型同步顺序分发、单订阅者 panic 隔离、`Unsubscribe` 生效、`PublishAsync` 父 ctx 取消后仍携带 `company_id`/`request_id`、`MaxAsyncConcurrency` 限流与 `Drain`）→ 实现 `bus.go`。
- [ ] **T5**：编写 `internal/infra/memcache/cache_test.go` 与 `ratelimit_test.go`（红灯：分片并发 `-race`、TTL 过期、容量上限淘汰、`GetOrLoad` 并发 32 协程仅回源 1 次、`DeleteFunc` 租户批量失效；滑动窗口防爆破 5 次失败触发锁定、`Peek` 不增计数、`Reset` 解锁、时间推进自动解封、`MaxKeys` 内存封顶）→ 实现 `cache.go`、`ratelimit.go`。
- [ ] **T6**：编写并验证 `internal/arch_test.go`（含真实全仓扫描 + 违规样本反向单测），运行 `cd synapse-server && go build ./... && go test -race -cover ./internal/...` 全绿。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：将 `SA-04` 状态更新为 `已实施（commit <hash>）`；
- [ ] `synapse-server/README.md`：记录 L0–L6 架构分层守卫规则（`internal/arch_test.go`）与 KEK 主密钥零停机轮换运维操作指引。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---:|---|---|---|
| 1 | Fail-Closed 租户上下文与 `Detach` 隔离 | `internal/core/tenant/tenant_test.go` | 空 `ctx` 调 `Require` 返回 `ErrMissingTenant`；仅注入 `obs.WithCompanyID` 不调用 `tenant.WithCompany` 时 `Require` 仍拒绝；父 `ctx` 取消后 `Detach(ctx)` 的 `Err()==nil` 且完整保留 `companyID`、`request_id`、`user_id` |
| 2 | `TenantPaths` / `SafeJoin` 防穿越与符号链接逃逸 | `internal/core/tenant/tenant_test.go` | 拒绝 `../evil`、`a..b`、绝对路径、12 保留词 slug；在 `t.TempDir()` 下构造指向根外目录的符号链接，`SafeJoin` 返回 `ErrPathEscape` |
| 3 | `SecretCipher` 租户隔离、并发生成 DEK 与 `0600` 文件 | `internal/core/crypto/cipher_test.go` | `acme` 密文以 `v2:acme:` 开头且 DEK 密文以 `v1:` 开头；`beta` 解密返回 `ErrCrossTenantSecret`；将前缀篡改为 `v2:beta:` 因 AAD/DEK 不匹配返回 `ErrDecryptFailed`；32 协程并发首次加密仅写入 1 次 DEK；自动创建的 `secret.key` 权限精确为 `0600` |
| 4 | `SecretCipher` 主密钥 KEK 零停机轮换 | `internal/core/crypto/cipher_test.go` | 使用 `K1` 加密租户密文 `C` → 以 `MasterKey=K2, PreviousKeys=[K1]` 重建实例仍可解密 `C` → 调用 `RewrapCompanyDEK(ctx, "acme")` 返回 `(true, nil)`（再次调用返回 `(false, nil)` 幂等）→ 以仅含 `MasterKey=K2`（移除 `K1`）重建实例仍可无损解密原始密文 `C` |
| 5 | `eventbus` 泛型同步/异步、panic 隔离与 `Drain` | `internal/core/eventbus/bus_test.go` | 订阅者 1 panic 时订阅者 2 正常执行并触发 `OnPanic`；`Unsubscribe()` 后不再收到事件；`PublishAsync` 在父 ctx 取消后仍读取到 `tenant.Require(ctx)` 与 `obs.RequestIDFrom(ctx)`；`Drain(ctx)` 准确等待所有异步处理器完成 |
| 6 | `ShardedCache` 分片并发、TTL、容量淘汰与 singleflight | `internal/infra/memcache/cache_test.go` | 过期键返回 `(_, false)`；超出 `MaxEntries` 触发淘汰使 `Len() <= MaxEntries`；32 协程并发对同 Key 调用 `GetOrLoad` 时 `loader` 原子计数精确为 `1`；`DeleteFunc` 按租户前缀精准清除；`-race` 零竞态 |
| 7 | `SlidingWindowLimiter` 防爆破、时间滑动与防 OOM | `internal/infra/memcache/ratelimit_test.go` | `Limit=5, Window=15m`：前 5 次 `RecordFailure` 允许，第 6 次及 `Peek` 返回 `Allowed=false` 且 `RetryAfter > 0`；调用 `Reset` 立即恢复允许；使用可注入时钟推进 15m 后自动解封；灌入超 `MaxKeys` 个随机 IP Key 后内存条目数严格封顶 |
| 8 | `internal/arch_test.go` 正向全仓校验与反向违规检出 | `internal/arch_test.go` | 对当前 L0 真实仓库校验 100% 通过；构造模拟违规 AST/import（`agent→task`、`task→agent`、`storage→platform`、`web→pgx`、`core→infra/db`、`go-redis`）均被精确检出报错 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race -cover ./internal/...` 全绿（含 `internal/arch_test.go`）
- [ ] §2 交付物表 6 项逐一落地且导出签名与 §4.1 逐字一致
- [ ] §8 测试清单 8 类用例全部通过，核心包行覆盖率 ≥ 85%
- [ ] 核对 `go.mod` 与全仓源码：**零 Redis 依赖、零 Redis 抽象**
- [ ] L0 全量回归（`SA-01` + `SA-02` + `SA-03` + `SA-04`）在 `-race` 下 100% 通过

---

## 10. 可提交节点

- 建议 commit 1（多租户与信封加密含 KEK 轮换）：`feat(core): add fail-closed TenantContext/TenantPaths and AES-256-GCM SecretCipher with KEK rotation (spec 04, PR SA-04)`
- 建议 commit 2（泛型事件总线、零 Redis 分片内存缓存/限流与 AST 架构守卫）：`feat(l0): add generic eventbus, in-memory sharded cache/sliding-window limiter and L0-L6 arch_test guard (spec 04, PR SA-04)`
