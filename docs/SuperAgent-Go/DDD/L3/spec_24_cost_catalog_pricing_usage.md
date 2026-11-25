# spec 24 | L3 成本管理 — `models.dev` 价格目录、4 级定价冷却与五维 Token 计费

- **状态**：`draft`
- **篇号顺序**：24（L3 成本域首篇；实现 `spi.ModelRouter` 与 `spi.UsageRecorder`，为对话/任务/系统小活提供定价与五维计费底座）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-24`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §4
  - 归档母本：[_epic/L3/spec_07_cost_catalog_router_usage.md](../_epic/L3/spec_07_cost_catalog_router_usage.md) §3–§5.2, §5.4
  - [SuperAgent/07_模型提供商管理_多密钥容灾_成本路由与用量分析.md](../../../SuperAgent/07_模型提供商管理_多密钥容灾_成本路由与用量分析.md)
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-cost/src/main/java/com/superagent/cost/ModelCatalog.java` (`L1-L98`)
    - `superagent-cost/src/main/java/com/superagent/cost/ModelPriceService.java` (`L1-L408`)
    - `superagent-cost/src/main/java/com/superagent/cost/usage/TokenUsageService.java` (`L1-L229`)
    - `superagent-cost/src/main/resources/model-catalog-snapshot.json`（339 条快照）
    - `superagent-web/src/main/java/com/superagent/web/model/ModelPriceController.java` (`L1-L46`)
    - `superagent-web/src/main/java/com/superagent/web/usage/UsageController.java` (`L1-L74`)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（`BackgroundGroup`）
  - [L0/spec_03 (SA-03)](../L0/spec_03_httpclient_resilience_egress.md)（`httpclient.TrustedClient`：代理/私有 CA/熔断/重试）
  - [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md)（`UsageStore`/`SystemSettingStore`）
  - [L2/spec_17 (SA-17)](../L2/spec_17_platform_settings_notice_analytics.md)（`SettingsService`）
  - [L2/spec_18 (SA-18)](../L2/spec_18_platform_provider_credentials_modelcall.md)（`ProviderService`/`ProviderCatalog`）
- **下游消费者**：`SA-25`（`TaskModelRouter` 消费 `Candidates`）、`SA-28`（对话消费 `ChatModels`/`UsageRecorder`）、`SA-30`（任务消费 `UsageRecorder`）、`SA-36`（SuperAgent 消费 `ModelRouter`/`UsageRecorder`）、`SA-39`（价格与用量 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

商业私有化客户常同时接入 Claude Code、Codex 与多家中转网关，需要对每个模型给出可核对的美元成本核算，并在对话默认模型故障时自动无感切换至备用模型且不重复轰炸管理员告警。

本篇（`SA-24`）将 `ModelCatalog`、`ModelPriceService` 与 `TokenUsageService` 收口在 1.5 人天的原子 PR 中，实现：
1. **`models.dev` 339 条全球定价同步 + 内嵌离线快照**：通过 `//go:embed model-catalog-snapshot.json` 保证断网/纯内网开箱即用，联网时通过 L0 `TrustedClient`（自动支持企业代理、私有 CA 与熔断，失败降级保留本地快照）定期拉取最新目录；
2. **4 级价格解析 + 10 分钟租户隔离冷却 + 边沿告警去重**：手工价 > 在线目录 > 租户落库缓存 > 内嵌快照；按 `companyID/modelID` 隔离冷却避免租户串扰；
3. **五维不重叠 Token 计费与 PG17 单库聚合**：零 ClickHouse 依赖，精确区分 `input`、`cacheRead`（0.1x）、`cacheWrite`（1.25x）、`cacheWrite1h`（2.0x 子集）与 `output`（含 `reasoning` 子集）。

### 1.2 本篇边界

- **本篇负责**：`internal/cost/{model-catalog-snapshot.json,catalog.go,price_service.go,usage_service.go,period.go}` 及全套单测。
- **本篇不负责**：`TaskModelRouter`（独立落位于 `L3/spec_25`）；Gin 路由挂载（交由 `SA-39`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 内嵌 `models.dev` 离线快照（14 厂商 339 条） | `internal/cost/model-catalog-snapshot.json` | 新增 |
| 2 | `ModelCatalog`（5 步 ID 规范化、14 官方厂商优先与中位数择优） | `internal/cost/catalog.go` | 新增 |
| 3 | `ModelPriceService`（4 级价格、10m 租户冷却、边沿去重 `DefaultModelChanged`、实现 `spi.ModelRouter`） | `internal/cost/price_service.go` | 新增 |
| 4 | `TokenUsageService`（5 维计费、复杂任务父子汇总、多维报表、实现 `spi.UsageRecorder`）与 `ParseUsageWindow` | `internal/cost/usage_service.go`、`period.go` | 新增 |
| 5 | 目录、价格冷却与五维计费单测套件 | `internal/cost/{catalog,price_service,usage_service}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，复用 [L1/spec_08 (SA-08)](../L1/spec_08_pg_storage_assets_cost.md) 已落盘的 `token_usage`（`NUMERIC(16,6)` 费用列、只追加写）与 `system_setting`（租户级 3 组键族：`cost.price.<id>` 手工价、`cost.fact.<id>` 离线持久化缓存、`cost.task-off.<id>` 禁用于任务标志）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package cost

const (
	SettingPrefixFact         = "cost.fact."
	SettingPrefixManual       = "cost.price."
	SettingPrefixTaskOff      = "cost.task-off."
	DefaultCatalogURL         = "https://models.dev/api.json"
	FullRefreshInterval       = 24 * time.Hour
	RetryUnpricedInterval     = 1 * time.Hour
	ModelCoolDown             = 10 * time.Minute
	DefaultCacheReadFactor    = 0.10
	DefaultCacheWriteFactor   = 1.25
	DefaultCacheWrite1hFactor = 2.00
	SystemUsageUser           = "system"
)

type Fact struct {
	Input, Output          float64
	Released, Source       string
	CacheRead, CacheWrite  *float64
}
func (f Fact) Blended() float64 { return (3*f.Input + f.Output) / 4 }

type Candidate struct {
	ID, CLI, Released, Source         string
	Input, Output, CacheRead, CacheWrite *float64
	Manual, ForTasks                  bool
}
func (c Candidate) Priced() bool
func (c Candidate) Blended() float64

type ModelCatalog struct { /* immutable map[canonicalKey]Fact */ }
func ParseModelCatalog(r io.Reader) (*ModelCatalog, error)
func CanonicalModelKey(modelID string) string
func (c *ModelCatalog) Find(modelID string) (Fact, bool)
func (c *ModelCatalog) Size() int

// 实现 spi.ModelRouter
type ModelPriceService struct { /* companies, providers, settings, store, bus, httpClient *httpclient.Client, bundled, downloaded, cooldowns sync.Map, standingIn map[string]string */ }
func (s *ModelPriceService) Start(bg *lifecycle.BackgroundGroup)
func (s *ModelPriceService) Candidates(ctx context.Context) []Candidate
func (s *ModelPriceService) Price(ctx context.Context, modelID string) (model.ModelPrice, bool)
func (s *ModelPriceService) Cheapest(ctx context.Context) (*spi.ModelChoice, error)
func (s *ModelPriceService) ChatModels(ctx context.Context) ([]string, error)
func (s *ModelPriceService) ReportFailure(ctx context.Context, modelID, errMsg string)
func (s *ModelPriceService) Refresh(ctx context.Context) error
func (s *ModelPriceService) Overview(ctx context.Context) ModelPricesDto
func (s *ModelPriceService) SetPrice(ctx context.Context, modelID string, in, out, cr, cw *float64) (ModelPricesDto, error)
func (s *ModelPriceService) SetForTasks(ctx context.Context, modelID string, on bool) (ModelPricesDto, error)

// 实现 spi.UsageRecorder
type TokenUsageService struct { /* usage, prices, agents, users, tasks, links */ }
func (s *TokenUsageService) Record(ctx context.Context, uctx model.UsageContext, tokensJSON string) (*decimal.Decimal, error)
func (s *TokenUsageService) Cost(ctx context.Context, u model.TokenUsage) *decimal.Decimal
func (s *TokenUsageService) ForTask(ctx context.Context, taskID string) (TaskUsageDto, error)
func (s *TokenUsageService) Summary(ctx context.Context, userID string, from, to time.Time, loc *time.Location) (UsageSummaryDto, error)
func ParseUsageWindow(period, tz string, now time.Time) (from, to time.Time, loc *time.Location, err error)
```

### 4.2 路由契约（7 个端点由 `L5/spec_39` 挂载）

- `GET /api/v1/admin/model-prices`、`POST /api/v1/admin/model-prices/refresh`、`PUT /api/v1/admin/model-prices/price?model=<id>`、`PUT /api/v1/admin/model-prices/tasks?model=<id>`（`ADMIN`）；
- `GET /api/v1/tasks/:id/usage`、`GET /api/v1/usage?period=&tz=`（登录）；`GET /api/v1/admin/usage?period=&tz=`（`ADMIN`）。

### 4.3 错误语义

- `SetPrice` / `SetForTasks` 空模型 ID 或任一价格 `< 0`：`400 invalid_argument`；
- `ParseUsageWindow` 非 `"month"` 且非正整数：`400 invalid_argument`（整数自动钳位 `[1, 366]`）；
- 下载 `models.dev` 失败（含 L0 熔断 `ErrCircuitOpen`/超时）：**不抛错**，记录 `catalogError` 并降级使用既有内存/持久化/内嵌快照，1 小时后重试；
- `TokenUsageService.Record` 收到非法 JSON：打 `slog.WarnContext` 并返 `nil`，绝不阻断对话或任务主流程。

### 4.4 SQL 契约

- `UsageStore.Save` 只追加写，绝不回溯修改历史 `cost_usd`；
- `UsageStore.Between(ctx, userID, from, to)` 在 `userID == ""` 时查全租户记录，非空时精确按 `user_name = $4` 过滤。

---

## 5. 行为语义

### 5.1 `ModelCatalog` 5 步 ID 规范化与择优（对齐 `ModelCatalog.java:38-97`）

1. `TrimSpace` + `ToLower` → 截最后一个 `/` 前缀 → 截 `[variant]` 后缀 → 正则移除末尾 `-(\d{8}|\d{4}-\d{2}-\d{2}|latest)` → 将 `.` 替换为 `-`；
2. 多报价择优：优先匹配 14 个官方厂商 slug（`openai`, `anthropic`, `deepseek`, `moonshotai`, `moonshotai-cn`, `zai`, `zhipuai`, `alibaba`, `alibaba-cn`, `google`, `xai`, `mistral`, `minimax`, `minimax-cn`），否则按 `Blended()` 排序取中位数。

### 5.2 `ModelPriceService` 冷却与 `DefaultModelChanged` 边沿去重（对齐 `ModelPriceService.java:131-395`）

- 4 级解析：`cost.price.<id>` → 内存在线目录 → `cost.fact.<id>`（兼容 4 段历史格式）→ 内嵌 339 条快照；
- 冷却表键为 `companyID + "/" + modelID`（纯进程内，零 Redis），TTL 10 分钟；
- `ChatModels`：`agentDefaultModel != ""` 时按 `[default] → [backupModels] → [其余 runnable 按 Blended DESC]` 过滤冷却中模型，全不可用时保底 `[default]`；通过 `standingIn[companyID]` 仅在状态边沿（首次降级 / 恢复默认）发布一次 `eventbus.DefaultModelChanged`。

### 5.3 `TokenUsageService` 五维计费公式与报表归并（对齐 `TokenUsageService.java:83-228`）

- 未定价返 `nil`（设置汇总 `costPartial = true`）；
- `hourWrite = min(u.CacheWrite1h, u.CacheWrite)`；
- 费用（美元）：`(input*P_in + cacheRead*R_read + (cacheWrite-hourWrite)*R_write + hourWrite*P_in*2.0 + output*P_out) / 1e6`（`R_read` 缺省 `0.10*P_in`，`R_write` 缺省 `1.25*P_in`）；
- `ForTask` 合并父任务与 `TaskLinkStore.Children` 子任务；`Summary` 隔离 `user == "system"` 探活消耗，复杂任务子任务按 `rootTask()` 归并至顶层任务，空切片一律序列化为 `[]`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：拷贝 `model-catalog-snapshot.json` 并编写 `catalog_test.go` → 实现 `catalog.go`。
- [ ] **T2**：编写 `price_service_test.go`（4 级价格、冷却隔离、边沿去重、`httptest` 刷新降级）→ 实现 `price_service.go`。
- [ ] **T3**：编写 `usage_service_test.go`（五维精度对照 `0.0458055`、1h 缓存、复杂任务归并、时区切日）→ 实现 `usage_service.go` 与 `period.go`。
- [ ] **T4**：运行 `go test -race -cover ./internal/cost/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-24` 状态更新；
- [ ] `configs/config.example.yaml`：确认 `cost.catalog_url`、`cost.catalog_check_seconds` 配置项。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 339 条快照加载、5 步规范化与中位数择优 | `internal/cost/catalog_test.go` | 内嵌快照解析成功；`siliconflow/DeepSeek-V3.2[fp8]-20260501` → `deepseek-v3-2`；官方厂商优于第三方 |
| 2 | 4 级价格优先级与手工价清除 | `internal/cost/price_service_test.go` | 手工 > 在线 > 落库 > 内嵌；清手工价后回退目录价；负数返 `400` |
| 3 | 10m 冷却、租户隔离与边沿去重通知 | `internal/cost/price_service_test.go` | `acme` 冷却不影响 `globex`；仅发 1 次 `down=true` 与 1 次恢复 `down=false` |
| 4 | 五维计费精度、父子归并与时段窗口 | `internal/cost/usage_service_test.go` | `haiku(1,5)` 实测估算 `0.0458055` 误差 `<1e-9`；未知模型置 `costPartial=true`；`system` 不入 `byUser`；`period=999` 钳位 366 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/cost/...` 全绿
- [ ] §2 交付物表 5 项齐备，行覆盖率 ≥ 85%，零真实外网请求，零 Redis 依赖
- [ ] 后台目录同步协程接入 L0 `lifecycle.BackgroundGroup`

---

## 10. 可提交节点

- 建议 commit：`feat(cost): implement models.dev catalog, 4-tier ModelPriceService and 5-bucket TokenUsageService (spec 24, SA-24)`
