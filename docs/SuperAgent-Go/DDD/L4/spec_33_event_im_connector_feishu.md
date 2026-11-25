# spec 33 | L4 IM 接入层 — `IMConnector` 插件管理、共享身份绑定与飞书/Lark 长连接

- **状态**：`draft`
- **篇号顺序**：33（L4 事件接入层首篇；落地可插拔 `spi.IMConnector` 管理器与零反射 `context` 热重载飞书/Lark 长连接插件，为二期企微留好统一插件槽位）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-33`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §3.2
  - 归档母本：[_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md](../_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md) §1, §5.3
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-event/src/main/java/com/superagent/event/feishu/FeishuConnector.java` (`L1-L594`)
    - `superagent-event/src/main/java/com/superagent/event/feishu/FeishuMessages.java` (`L1-L74`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`、`ShutdownManager`、`obs` Prometheus `superagent_im_connected`）
  - [L0/spec_03 (SA-03)](../L0/spec_03_httpclient_resilience_egress.md)（`httpclient.TrustedClient`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`memcache` 进程内缓存、`eventbus`）
  - [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md)（`EventSourceStore`）
  - [L2/spec_20 (SA-20)](../L2/spec_20_platform_feishu_dingtalk_connectors.md)（`IntegrationService` `feishu` 与 `IntegrationChanged`）
- **下游消费者**：`SA-34`（复用 `IdentityBinder` 与 `IMRegistry`）、`SA-35`（`InboxService` 接收 `Ingest` 与调用 `NotifyOwner`）、`SA-41`（Server 启动与停机装配）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

企业内部部署通常不愿对外开放公网回调端口，通过飞书/Lark 官方出站 WebSocket 长连接即可安全实时接收员工私聊与群消息，并零配置按手机号或 8 位绑定码完成身份映射。

本篇（`SA-33`）在 1.5 人天粒度内交付：
1. **彻底淘汰原版 Java 反射停连黑科技**：每家租户的长连接持有独立 `connCtx, cancel := context.WithCancel(rootCtx)`，配置变更（`IntegrationChanged`）或优雅停机直接 `cancel()`，零泄漏精准释放 WebSocket、心跳与协程；
2. **共享 `IdentityBinder` 身份绑定内核**：统一抽取 8 位一次性绑定码校验、5 次输错锁定、6 小时未绑定引导冷却、手机号自动同步与 1:1 归属绑定，供飞书、钉钉与未来企微复用；
3. **12 种飞书消息类型 1:1 保真解析**：含 `post` 多语种富文本、`mentions` 占位符替换、`8,000` 字截断、毫秒级入站 ACK 与 4-Worker 异步解耦。

### 1.2 本篇边界

- **本篇负责**：`internal/event/im/registry.go`、`internal/event/identity_bind.go`、`internal/event/feishu/{connector.go,messages.go}` 及全套单测。
- **本篇不负责**：钉钉 Stream 连接器（独立落位 `SA-34`）；7 步分诊引擎与收件箱服务（独立落位 `SA-35`）。**彻底裁撤原版 `SecretaryCleanup.java`**。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `IMRegistry`（`spi.IMConnector` 注册管理与 `IntegrationChanged`/`CompanyStarted` 事件路由） | `internal/event/im/registry.go` | 新增 |
| 2 | 共享身份绑定助手 `IdentityBinder`（8 位绑定码、5 次防爆破锁定、6h 提示冷却，纯内存 `memcache`） | `internal/event/identity_bind.go` | 新增 |
| 3 | 飞书/Lark 12 种消息类型解析与 `mentions` 替换器 | `internal/event/feishu/messages.go` | 新增 |
| 4 | `FeishuIMConnector`（双域名、连接前 `bot/v3/info` 校验、零反射 `context` 热重载、群成员 10m 缓存、4-Worker 消费、`NotifyOwner`） | `internal/event/feishu/connector.go` | 新增 |
| 5 | 身份绑定、12 消息类型与飞书 Mock WS 热重载单元测试 | `internal/event/identity_bind_test.go`、`internal/event/feishu/{messages,connector}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md) 已净化落盘的 `event_source`（已删除 `secretary_agent_id` 与 5 个废弃空列）。群成员缓存（10m）、姓名/群名缓存、绑定码失败计数与 6h 提示冷却均使用 L0 `internal/infra/memcache` 纯进程内缓存（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package im

type Registry struct { /* mu sync.RWMutex, connectors map[string]spi.IMConnector */ }
func NewRegistry() *Registry
func (r *Registry) Register(c spi.IMConnector)
func (r *Registry) Get(typ string) (spi.IMConnector, bool)
func (r *Registry) All() []spi.IMConnector
func (r *Registry) StartAll(bg *lifecycle.BackgroundGroup) error
func (r *Registry) CloseAll() error
```

```go
package event

type IngestTarget interface {
	Ingest(ctx context.Context, src model.EventSource, ev model.InboxEvent, fromOwner, fromBot bool) (*model.InboxEvent, error)
}

type IdentityBinder struct { /* sources store.EventSourceStore, users store.UserStore, cache *memcache.Cache */ }
func (b *IdentityBinder) TryBindCode(ctx context.Context, sourceType, appID, externalID, rawText string) (bound bool, reply string, err error)
func (b *IdentityBinder) ShouldSendHint(companyID, sourceType, externalID string) bool // 6h 冷却
func (b *IdentityBinder) SyncByPhone(ctx context.Context, sourceType, appID string, phoneToExtID map[string]string) int
```

```go
package feishu

var _ spi.IMConnector = (*Connector)(nil)

const MaxMessageChars = 8_000

func ExtractText(msgType, rawContent string, mentions map[string]string) string

type Connector struct { /* integrations, sources, binder, ingest, companies, httpClient *httpclient.Client, mu, conns map[string]*feishuConn, companyMu sync.Map */ }
func NewConnector(deps Deps) *Connector
func (c *Connector) Type() string // "feishu"
func (c *Connector) Start(ctx context.Context) error
func (c *Connector) ReconnectCompany(ctx context.Context, companyID string) error
func (c *Connector) NotifyOwner(ctx context.Context, src model.EventSource, text string) error
func (c *Connector) Close() error
```

### 4.2 路由契约

本篇为企业 IM 出站长连接接入层，不暴露公网入站 Webhook 路由。

### 4.3 错误语义

- 网络超时或飞书凭证/权限错误绝不 panic 崩溃进程，而是调用 `IntegrationService.SetStatus(ctx, "feishu", "error", humanDesc)` 并更新 Prometheus `superagent_im_connected{company_id,type="feishu"} = 0`；
- 绑定码同一外部账号连续输错 5 次锁定 30 分钟（纯内存 `memcache`）；
- JSON 损坏的飞书消息返回 `"[无法解析的消息]"`，永不崩 worker 协程。

### 4.4 SQL 契约

- 绑定成功后立即通过 `EventSourceStore.Save` 清空 `bind_code = ''` 并写入 `app_id` 与 `owner_external_id`（`LinkedTo(currentAppID)` 强校验防跨应用 ID 复用）。

---

## 5. 行为语义

### 5.1 零反射 `context.CancelFunc` 单租户热重载（改进自 `FeishuConnector.java:529-546`）

- 每个 `companyID` 持有独立 `companyMu` 锁与活动 `feishuConn{ctx, cancel, done}`；
- 收到 `CompanyStarted` 或 `IntegrationChanged(key=="feishu")` 时调用 `ReconnectCompany(ctx, companyID)`：先 `cancel()` 旧连接并等待旧连接 `<-done` 退出、将指标置 0 → 检查 `integrations.Ready(tctx, "feishu")` → 调 `GET /open-apis/bot/v3/info` 校验连通与凭证 → 派生新 `connCtx` 启动 WS 读循环与 4-Worker 异步消费协程；
- 所有 WS 入站事件先判 `c.isLive(conn)`（指针相等），杜绝旧连接幽灵回调，并毫秒级投入容量 256 的有界队列实现快速 ACK。

### 5.2 12 种飞书消息类型与群聊路由（对齐 `FeishuMessages.java:19-73`, `FeishuConnector.java:295-492`）

- 完整支持 `text`、`post`（自动解包 `zh_cn` 等首语种节点）、`image`、`file`、`audio`、`media`、`sticker`、`interactive`、`share_chat`、`share_user`、`merge_forward` 及兜底 `[<msgType>]`，替换 `@_user_N` 占位符并截断至 `8,000` rune + `"…"`；
- 私聊（`p2p`）：优先 `binder.TryBindCode`，未绑定走 6h 冷却回复引导，已绑定以 `mentionsMe=true, fromOwner=true` 投递；
- 群聊（`group`）：收件人 = `(@提及的已绑定员工) ∪ (群成员 API 查得的已绑定员工)` 排除发送者；群成员翻页上限 20 页（2,000 人），按 `chatID` 缓存 10 分钟（无权限静默降级为仅 `@提及`）；姓名缓存仅缓存非空值。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/event/identity_bind_test.go`（红灯：8 位绑定码成功清空、5 次锁定、6h 冷却、手机号同步）→ 实现 `im/registry.go` 与 `identity_bind.go`。
- [ ] **T2**：编写 `internal/event/feishu/messages_test.go`（红灯：12 消息类型、`post` 多语种、`mentions` 替换、8k rune 截断与坏 JSON）→ 实现 `feishu/messages.go`。
- [ ] **T3**：编写 `internal/event/feishu/connector_test.go`（红灯：Mock WS + `httptest` 验证 `ReconnectCompany` 无泄漏关闭旧连接、双域名、群成员降级与 `NotifyOwner`）→ 实现 `feishu/connector.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/event/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-33` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `IdentityBinder` 绑定码与防爆破锁定 | `internal/event/identity_bind_test.go` | 正确绑定码绑定并清 `bind_code`；连续 5 次错误锁定；6h 内不重复发引导 |
| 2 | 12 种飞书消息解析与 8k 截断 | `internal/event/feishu/messages_test.go` | 12 类型全部保真；`@_user_1` 替换为 `@张三`；9000 汉字截断为 8000+`…`；损坏 JSON 返 `"[无法解析的消息]"` |
| 3 | `FeishuIMConnector` 零反射 `context` 热重载与优雅停机 | `internal/event/feishu/connector_test.go` | 二次调用 `ReconnectCompany` 立刻关闭旧 WS 且旧连接遗留帧被 `isLive` 丢弃；`Close()` 无 goroutine 泄漏 |
| 4 | 双域名切换、群成员缓存与 `NotifyOwner` | `internal/event/feishu/connector_test.go` | `domain="lark"` 命中 `open.larksuite.com`；群成员无权限静默降级为仅 `@提及`；`NotifyOwner` 成功调用私聊发送接口 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/event/...` 全绿
- [ ] 零 `SecretaryCleanup` 残留，零反射停连，零 Redis 依赖，行覆盖率 ≥ 80%
- [ ] `internal/arch_test.go` 通过（`internal/event` 零引入 `internal/orchestrator`/`internal/agent`）

---

## 10. 可提交节点

- 建议 commit：`feat(event-feishu): add IMRegistry, IdentityBinder and context-managed FeishuIMConnector (spec 33, SA-33)`
