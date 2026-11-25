# spec 34 | L4 IM 接入层 — 钉钉 Stream Mode 长连接插件（`coder/websocket`、指数退避与群 `@bot` 归一化）

- **状态**：`draft`
- **篇号顺序**：34（L4 事件接入层第二篇；基于轻量 `coder/websocket` 原生实现钉钉 Stream Mode 长连接，零重型 SDK 依赖）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-34`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §3.2
  - 归档母本：[_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md](../_epic/L4/spec_11_event_feishu_dingtalk_inbox_triage.md) §1, §5.4
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-event/src/main/java/com/superagent/event/dingtalk/DingTalkConnector.java` (`L1-L584`)
    - `superagent-event/src/test/java/com/superagent/event/dingtalk/DingTalkConnectorTest.java`
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`、`obs` Prometheus 指标）
  - [L0/spec_03 (SA-03)](../L0/spec_03_httpclient_resilience_egress.md)（`httpclient.TrustedClient` 代理/私 CA）
  - [L2/spec_20 (SA-20)](../L2/spec_20_platform_feishu_dingtalk_connectors.md)（`IntegrationService` `dingtalk` 与 `IntegrationChanged`）
  - [L4/spec_33 (SA-33)](./spec_33_event_im_connector_feishu.md)（`IMRegistry`、`IdentityBinder`、`IngestTarget`）
- **下游消费者**：`SA-35`（`InboxService` 接收钉钉事件与调用 `NotifyOwner`）、`SA-41`（装配注册 `DingTalkIMConnector`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

与飞书一致，钉钉企业自建应用提供官方 **Stream Mode**（出站 WebSocket）能力，无需开放公网 HTTPS 回调即可接收单聊与群内 `@机器人` 消息，并通过批量单聊接口向员工直推紧急提醒。

本篇（`SA-34`）直接基于标准轻量 Go WebSocket 库 `github.com/coder/websocket` 1:1 复刻钉钉 Stream 协议（1.5 人天 PR），具备：
1. **完备协议帧处理**：OAuth2 AppToken 缓存、`/v1.0/gateway/connections/open` 申请 Endpoint/Ticket、`SYSTEM/ping` 毫秒回显、`SYSTEM/disconnect` 优雅重连、`CALLBACK` 毫秒 ACK + 异步 worker 投递；
2. **稳健重连与精准热重载**：断开按 `5s → 10s → 20s → 40s → 80s → 160s → 300s` 指数退避（建连成功即归零），全程监听 `connCtx.Done()` 保障 `ReconnectCompany`/`Close()` 毫秒退出；
3. **关键语义细节保真**：语音消息自动提取 `content.recognition` 转写文本；群聊中员工本人 `@机器人` 时将本人副本的 `chatType` 归一化为 `"p2p"`，防止被分诊规则 Step 2（“自己在群里的发言”）误过滤！

### 1.2 本篇边界

- **本篇负责**：`internal/event/dingtalk/{connector.go,stream.go,messages.go}` 及基于本地 Mock WS Server 的完整单测。
- **本篇不负责**：7 步分诊与收件箱业务（交由 `SA-35`）；钉钉云文档 5 个 MCP 工具（已由 `SA-26` 交付）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 钉钉 Stream 协议帧编解码、`/v1.0/gateway/connections/open` 握手与 `coder/websocket` 循环 | `internal/event/dingtalk/stream.go` | 新增 |
| 2 | 钉钉消息类型文本解析（文本/富文本/语音 `recognition`/图片/文件）与 8k 截断 | `internal/event/dingtalk/messages.go` | 新增 |
| 3 | `DingTalkIMConnector`（实现 `spi.IMConnector`：指数退避重连、手机号同步、`@bot` 归类 `p2p`、`NotifyOwner` 批量单聊推送） | `internal/event/dingtalk/connector.go` | 新增 |
| 4 | Mock WS Server 协议帧、重连退避、语音识别与群 `@bot` 单元测试 | `internal/event/dingtalk/connector_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 `event_source`（`type = "dingtalk"`，`app_id = clientId`，`owner_external_id = staffId/userid`）。AppToken 缓存（提前 300s 刷新）与租户连接表均为纯进程内结构（**零 Redis 依赖**）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package dingtalk

var _ spi.IMConnector = (*Connector)(nil)

const (
	BotTopic       = "/v1.0/im/bot/messages/get"
	MaxBackoffWait = 300 * time.Second
	MaxReadBytes   = 4 << 20
)

type Config struct {
	APIURL  string // 默认 https://api.dingtalk.com
	OAPIURL string // 默认 https://oapi.dingtalk.com
}

type Connector struct { /* cfg, integrations, sources, binder, ingest, companies, httpClient *httpclient.Client, mu, conns map[string]*dingConn, companyMu sync.Map */ }
func NewConnector(cfg Config, deps Deps) *Connector
func (c *Connector) Type() string // "dingtalk"
func (c *Connector) Start(ctx context.Context) error
func (c *Connector) ReconnectCompany(ctx context.Context, companyID string) error
func (c *Connector) NotifyOwner(ctx context.Context, src model.EventSource, text string) error
func (c *Connector) Close() error
```

### 4.2 路由契约

纯出站 Stream Mode WebSocket 客户端，不注册任何入站 HTTP 路由。

### 4.3 错误语义

- 握手失败、401/403 凭证错误、网络中断均转化为中文诊断写入 `IntegrationService.SetStatus(ctx, "dingtalk", "error", desc)`，同步更新 `superagent_im_connected{company_id,type="dingtalk"} = 0`，并按指数退避重试；
- 单条入站回调 JSON 异常仅打 `slog.WarnContext` 并照常回 ACK，防服务端重复轰炸。

### 4.4 SQL 契约

- 绑定或手机号自动同步后通过 `EventSourceStore.Save` 更新 `(company_id, owner, "dingtalk")`，`LinkedTo(clientID)` 强校验应用归属。

---

## 5. 行为语义

### 5.1 钉钉 Stream Mode 生命周期与毫秒 ACK（对齐 `DingTalkConnector.java:201-385`）

1. 获取 AppToken（`POST <apiURL>/v1.0/oauth2/accessToken`，提前 300s 续期）；
2. `POST <apiURL>/v1.0/gateway/connections/open` 传 `clientId`/`clientSecret`/`subscriptions: [{type:"CALLBACK", topic:"/v1.0/im/bot/messages/get"}]` 拿 `endpoint` 与 `ticket`；
3. `websocket.Dial(dialCtx, endpoint + "?ticket=" + ticket)`（15s 拨号超时，读上限 4 MiB），建连成功后立刻归零 `attempts`、置状态 `"connected"` 并异步执行手机号身份同步；
4. 帧分发循环：
   - `SYSTEM / ping` → 原样回发 `{"code":200,"headers":{"contentType":"application/json","messageId":...},"message":"OK","data":frame.Data}`；
   - `SYSTEM / disconnect` → 主动关闭当前 WS 触发外层循环立即重连；
   - `CALLBACK` → **立即回 200 ACK**，再将事件投递至 4-Worker 有界通道异步处理，防止业务耗时触发钉钉服务端超时重发；
5. 断开后按 `min(300s, 5s << min(attempts, 6))` 退避等待，可被 `<-connCtx.Done()` 瞬间打断退出。

### 5.2 语音转写提取、群 `@bot` 归一化与 `NotifyOwner`（对齐 `DingTalkConnector.java:390-584`）

- **消息解析**：`text` 取 `text.content`；`audio` 优先取 `content.recognition`（钉钉自带语音转文字）否则 `"[语音]"`；`richText` 顺序拼接段落；`picture`→`"[图片]"`；`file`→`"[文件]"`；超 8,000 rune 截断 + `"…"`；
- **群 `@bot` 归类 `p2p`**：`conversationType == "2"`（群聊）且发送者本身是已绑定员工并主动 `@机器人` 时，为其本人副本置 `chatType = "p2p"`、`mentionsMe = true`，避免在 `SA-35` 分诊 Step 2 中被当成“自己在群里闲聊”丢弃；
- **`NotifyOwner`**：调用 `POST <apiURL>/v1.0/robot/oToMessages/batchSend`（Header `x-acs-dingtalk-access-token`，`robotCode = clientId`，`userIds = [src.OwnerExternalID]`，`msgKey = "sampleText"`）。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/event/dingtalk/connector_test.go`（红灯：Mock HTTP + Mock `coder/websocket` Server 验证握手、`SYSTEM/ping`、`SYSTEM/disconnect` 重连、`CALLBACK` 毫秒 ACK、指数退避打断、语音 `recognition`、群 `@bot` 归类 `p2p`、`NotifyOwner`）。
- [ ] **T2**：实现 `stream.go`、`messages.go`、`connector.go` 使全部单测转绿。
- [ ] **T3**：运行 `go test -race -cover ./internal/event/dingtalk/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-34` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | Stream 握手、`ping` 回显与 `disconnect` 自动重连 | `internal/event/dingtalk/connector_test.go` | 正确请求 `/v1.0/gateway/connections/open`；收到 `ping` 立即回显相同 `data`；收到 `disconnect` 后自动重新握手 |
| 2 | 指数退避与 `ReconnectCompany`/`Close` 即时退出 | `internal/event/dingtalk/connector_test.go` | 退避序列验证；调用 `Close()` 后毫秒级退出且无 goroutine 泄漏 |
| 3 | 语音 `recognition` 提取与群 `@bot` 归一化 `p2p` | `internal/event/dingtalk/connector_test.go` | 语音消息提取转写正文；群聊 `@bot` 本人副本 `ChatType == "p2p"` |
| 4 | `NotifyOwner` 单聊推送与热重载 | `internal/event/dingtalk/connector_test.go` | `batchSend` 请求头与 `robotCode`/`userIds` 精确匹配；禁用集成后立刻断连 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/event/dingtalk/...` 全绿
- [ ] 行覆盖率 ≥ 80%，零真实外网依赖，零 Redis 引入

---

## 10. 可提交节点

- 建议 commit：`feat(event-dingtalk): implement DingTalk Stream Mode IMConnector with coder/websocket and backoff (spec 34, SA-34)`
