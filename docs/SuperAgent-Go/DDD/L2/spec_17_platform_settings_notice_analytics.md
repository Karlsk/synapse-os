# spec 17 | L2 平台管理 — 系统配置、品牌定制、待办通知中心与统计分析

- **状态**：`draft`
- **篇号顺序**：17（L2 平台核心域收口篇；交付全字段原子化系统设置、私有化品牌 Logo、事件驱动待办通知中心与企业运营统计）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-17`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §2–§3
  - 归档母本：[_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md](../_epic/L2/spec_04_platform_tenant_auth_user_settings_notice.md) §4–§5.7
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-platform/src/main/java/com/superagent/platform/settings/SettingsService.java` (`L46-L243`)
    - `superagent-platform/src/main/java/com/superagent/platform/settings/BrandingService.java` (`L40-L98`)
    - `superagent-platform/src/main/java/com/superagent/platform/notice/NoticeService.java` (`L83-L324`)
    - `superagent-platform/src/main/java/com/superagent/platform/analytics/AnalyticsService.java` (`L48-L107`)
- **前置依赖**：
  - [L0/spec_02 (SA-02)](../L0/spec_02_db_pgx_tx_migration.md)（`TxManager` 原子配置提交）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（`tenant`、`eventbus`、`memcache`）
  - [L1/spec_05 (SA-05)](../L1/spec_05_core_domain_platform_assets_contracts.md)（`SystemSettingStore`/`NoticeStore`/`ModelChecker`）
  - [L1/spec_10 (SA-10)](../L1/spec_10_pg_storage_event_orchestrator.md)（`NoticeStore` PG 实现）
  - [L2/spec_15 (SA-15)](./spec_15_platform_tenant_bootstrap_auth.md)（`CompanyStarted` 事件流）
- **下游消费者**：`SA-18`（读取默认/备选模型）、`SA-20`（读取 `PublicURL` 拼 OAuth 回调）、`SA-25`（读取 `TaskFixedModels` 路由开关）、`SA-35`（监听 `NoticeIgnored` 同步收件箱待办）、`SA-39`（设置/品牌/通知/分析 HTTP 路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

面向商业私有化部署客户，管理员需要在控制台配置企业名称/简称、默认与备用大模型、固定任务模型清单、对外 `publicUrl` 以及定制登录页与侧边栏品牌 Logo；普通员工需要统一的“待办通知中心”聚合任务待确认/失败/完成、SuperAgent 计划审批、IM 收件箱待办与 Agent 共享动态；管理员还需要查看企业级 7–90 天任务执行趋势与模型费用分布。

本篇（`SA-17`）在 1:1 保真 Java 原版语义的基础上完成两项关键工程升级：
1. **`SettingsService` 全字段先校验后事务提交**：消除原版 Java 边校验边写库导致的半途失败脏数据问题；
2. **`NoticeService` PG17 原子幂等 Upsert**：以数据库 `ON CONFLICT (company_id, owner, notice_key)` 替代原版 JVM `synchronized`，天然无锁竞态。

### 1.2 本篇边界

- **本篇负责**：
  - `internal/platform/settings`：`SettingsService`（全部租户 KV 配置项读写、原子校验与事务写入、L0 `memcache` 短时热缓存）、`BrandingService`（每租户自定义光栅 Logo + `//go:embed` 默认 PNG + SHA-256 强 ETag）；
  - `internal/platform/notice`：`NoticeService`（列表/统计/完成/忽略/重开/全部已读、订阅 5 类业务事件 + `CompanyStarted` 幂等回填、发布 `NoticeIgnored`）；
  - `internal/platform/analytics`：`AnalyticsService`（7–90 天夹紧、零填充日趋势、7 日活跃发起人、成功率、`decimal.Decimal` 费用汇总与模型分布）。
- **本篇不负责**：HTTP 路由与 Gin Handler 注册（交由 `SA-39`）；模型推理探活实现（通过 `spi.ModelChecker` 反转解耦，由 `SA-18` 实现）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 系统配置服务 `SettingsService`（10 项核心配置、全字段预检、事务写入与热缓存） | `internal/platform/settings/settings.go` | 新增 |
| 2 | 品牌 Logo 服务 `BrandingService` 与默认嵌入式 Logo | `internal/platform/settings/branding.go`、`assets/superagent-logo.png` | 新增 |
| 3 | 待办通知中心 `NoticeService`（CRUD、5 类领域事件监听与首次回填） | `internal/platform/notice/notice.go` | 新增 |
| 4 | 运营统计分析服务 `AnalyticsService` | `internal/platform/analytics/analytics.go` | 新增 |
| 5 | 设置、品牌、通知与统计单元测试套件 | `internal/platform/settings/{settings,branding}_test.go`、`internal/platform/notice/notice_test.go`、`internal/platform/analytics/analytics_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表，直接复用既有表：
- `system_setting`（`SA-07`：`company_id, setting_key` 联合主键），本篇冻结写入以下 11 个标准键：
  `agent.default_model`、`company.name`、`company.short_name`、`model.backup_models`、`task.fixed_models`、`task.fixed_models_on`、`app.public_url`、`complex.max_subtasks`、`complex.max_parallel`、`codebuddy.internet_environment`、`notice.backfilled`。
- `notice`（`SA-10`：唯一约束 `(company_id, owner, notice_key)` 支持原子幂等 Upsert）。
- 品牌 Logo 文件落位：`<brandingDir>/<companyID>/logo`（无租户上下文回退默认常量 `"superagent"` 或内嵌 PNG）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package settings

type SystemSettingsDto struct {
	AgentDefaultModel            string   `json:"agentDefaultModel"`
	CompanyName                  string   `json:"companyName"`
	CompanyShortName             string   `json:"companyShortName"`
	BackupModels                 []string `json:"backupModels"`
	TaskFixedModels              []string `json:"taskFixedModels"`
	TaskFixedModelsOn            bool     `json:"taskFixedModelsOn"`
	PublicURL                    string   `json:"publicUrl"`
	ComplexMaxSubtasks           int      `json:"complexMaxSubtasks"`
	ComplexMaxParallel           int      `json:"complexMaxParallel"`
	CodeBuddyInternetEnvironment string   `json:"codebuddyInternetEnvironment"`
}

type UpdateSettingsPatch struct {
	AgentDefaultModel            string    `json:"agentDefaultModel"`
	CompanyName                  string    `json:"companyName"`
	CompanyShortName             string    `json:"companyShortName"`
	BackupModels                 *[]string `json:"backupModels"`
	TaskFixedModels              *[]string `json:"taskFixedModels"`
	TaskFixedModelsOn            *bool     `json:"taskFixedModelsOn"`
	PublicURL                    *string   `json:"publicUrl"`
	ComplexMaxSubtasks           *int      `json:"complexMaxSubtasks"`
	ComplexMaxParallel           *int      `json:"complexMaxParallel"`
	CodeBuddyInternetEnvironment *string   `json:"codebuddyInternetEnvironment"`
}

type SettingsService struct { /* store, tx, models spi.ModelChecker, cache */ }
func (s *SettingsService) Get(ctx context.Context) (*SystemSettingsDto, error)
func (s *SettingsService) Update(ctx context.Context, req UpdateSettingsPatch) (*SystemSettingsDto, error)
func (s *SettingsService) AgentDefaultModel(ctx context.Context) (string, error)
func (s *SettingsService) BackupModels(ctx context.Context) ([]string, error)
func (s *SettingsService) TaskFixedModels(ctx context.Context) (models []string, enabled bool, err error)
func (s *SettingsService) PublicURL(ctx context.Context) (string, error)
func (s *SettingsService) ComplexLimits(ctx context.Context) (maxSubtasks, maxParallel int, err error)

type LogoPayload struct {
	Bytes     []byte
	MediaType string
	ETag      string // 形如 "\"<sha256前16位>\""
}

type BrandingService struct { /* rootDir string, defaultLogo []byte */ }
func (s *BrandingService) Logo(ctx context.Context) (*LogoPayload, error)
func (s *BrandingService) HasCustomLogo(ctx context.Context) (bool, error)
func (s *BrandingService) SetLogo(ctx context.Context, base64Data string) error
func (s *BrandingService) ResetLogo(ctx context.Context) error
```

```go
package notice

const ListLimit = 500

type NoticeStats struct {
	ToConfirm     int `json:"toConfirm"`
	ToDo          int `json:"toDo"`
	UnreadNotices int `json:"unreadNotices"`
	DoneToday     int `json:"doneToday"`
	DoneThisWeek  int `json:"doneThisWeek"`
	DoneThisMonth int `json:"doneThisMonth"`
	DoneTotal     int `json:"doneTotal"`
}

type NoticeService struct { /* store, tasks, todos, sources, settings, users, bus, loc */ }
func (s *NoticeService) RegisterHandlers(bus *eventbus.Bus)
func (s *NoticeService) List(ctx context.Context, owner string) ([]model.Notice, error)
func (s *NoticeService) Stats(ctx context.Context, owner string) (*NoticeStats, error)
func (s *NoticeService) MarkDone(ctx context.Context, owner, id string) (*model.Notice, error)
func (s *NoticeService) Ignore(ctx context.Context, owner, id string) (*model.Notice, error)
func (s *NoticeService) Reopen(ctx context.Context, owner, id string) (*model.Notice, error)
func (s *NoticeService) MarkAllRead(ctx context.Context, owner string) (int, error)
```

```go
package analytics

const ActiveDays = 7

type DailyBucket struct {
	Date    string `json:"date"`
	Created int    `json:"created"`
	Done    int    `json:"done"`
	Failed  int    `json:"failed"`
}

type ModelDist struct {
	Model *string         `json:"model"`
	Tasks int             `json:"tasks"`
	Spend decimal.Decimal `json:"spend"`
}

type OverviewDto struct {
	Users        int             `json:"users"`
	EnabledUsers int             `json:"enabledUsers"`
	ActiveUsers  int             `json:"activeUsers"`
	Agents       int             `json:"agents"`
	Automations  int             `json:"automations"`
	Tasks        int             `json:"tasks"`
	Done         int             `json:"done"`
	Failed       int             `json:"failed"`
	SuccessRate  *float64        `json:"successRate"`
	TotalSpend   decimal.Decimal `json:"totalSpend"`
	Daily        []DailyBucket   `json:"daily"`
	Models       []ModelDist     `json:"models"`
}

type AnalyticsService struct { /* users, agents, automations, tasks, loc */ }
func (s *AnalyticsService) Overview(ctx context.Context, days int) (*OverviewDto, error)
```

### 4.2 路由契约（由 `L5/spec_39` 挂载）

- `GET /api/v1/settings`（登录）、`PUT /api/v1/settings`（`ADMIN`）；
- `GET/PUT/DELETE /api/v1/settings/logo`（`ADMIN`）；
- `GET /api/v1/branding/logo`（**公开免登录、免强制改密**，支持 `If-None-Match` 返 `304`，头含 `X-Content-Type-Options: nosniff` 与 `Cache-Control: no-cache`）；
- `GET /api/v1/notices`、`GET /api/v1/notices/stats`、`POST /api/v1/notices/:id/{done,ignore,reopen}`、`POST /api/v1/notices/read-all`（登录）；
- `GET /api/v1/admin/analytics?days=14`（`ADMIN`）。

### 4.3 错误语义

- 新增模型未通过探活：`400 invalid_argument` `"Model %s has not passed the inference check"`；
- `taskFixedModelsOn == true` 且列表为空：`400 invalid_argument` `"开启任务固定模型时，至少要选一个模型"`；
- `complexMaxSubtasks` 不在 `[1, 50]` 或 `complexMaxParallel` 不在 `[1, 20]`：`400 invalid_argument`；
- Logo 非 PNG/JPEG/GIF/WebP 或 `> 1 MB`：`400 invalid_argument`；
- 通知不存在、不属于当前用户或 `kind == TRACK`：`404 not_found` `"No such item: %s"`。

### 4.4 SQL 契约

- `NoticeStore.Save` 对 `(company_id, owner, notice_key)` 采用原子 `ON CONFLICT DO UPDATE`，更新时不覆写 `id`、`link_type`、`link_id`、`created_at`；
- `SettingsService.Update` 在 `TxManager.WithinTx` 内批量 `Put` 并在提交成功后失效当前租户的 `memcache` 键。

---

## 5. 行为语义

### 5.1 `SettingsService` 全字段原子预检与已保存模型豁免（对齐 `SettingsService.java:68-122`）

```go
package settings

func (s *SettingsService) Update(ctx context.Context, req UpdateSettingsPatch) (*SystemSettingsDto, error) {
	all, err := s.store.All(ctx)
	if err != nil { return nil, err }
	// 1. 全部校验先于任何数据库写，避免半截脏写
	curDefault := strings.TrimSpace(all[keyAgentDefaultModel])
	newDefault := strings.TrimSpace(req.AgentDefaultModel)
	if newDefault != "" && newDefault != curDefault && !s.models.HasRunnableModel(ctx, newDefault) {
		return nil, apperror.InvalidArg("Model " + newDefault + " has not passed the inference check")
	}
	companyName, err := validateMaxRunes(req.CompanyName, 100)
	if err != nil { return nil, err }
	shortName, err := validateMaxRunes(req.CompanyShortName, 20)
	if err != nil { return nil, err }
	// 仅对本次新增的模型做 HasRunnableModel 校验，已保存模型即使当前失效也豁免
	savedFixed := splitCSV(all[keyTaskFixedModels])
	newFixed := savedFixed
	if req.TaskFixedModels != nil {
		newFixed, err = s.validateModelList(ctx, *req.TaskFixedModels, savedFixed)
		if err != nil { return nil, err }
	}
	fixedOn := all[keyTaskFixedOn] == "true"
	if req.TaskFixedModelsOn != nil { fixedOn = *req.TaskFixedModelsOn }
	if fixedOn && len(newFixed) == 0 {
		return nil, apperror.InvalidArg("开启任务固定模型时，至少要选一个模型")
	}
	// 校验 backupModels、publicUrl（仅 http/https 且去尾斜杠）、complexMaxSubtasks(1..50)、complexMaxParallel(1..20)
	// 2. 事务内统一提交，成功后清理租户缓存
	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		return s.writeAll(txCtx, req, newDefault, companyName, shortName, newFixed, fixedOn)
	})
	if err != nil { return nil, err }
	if cid, ok := tenant.Optional(ctx); ok { s.cache.Delete(cid) }
	return s.Get(ctx)
}
```

### 5.2 `NoticeService` 5 类事件映射与幂等回填（对齐 `NoticeService.java:83-324`）

1. **`List(ctx, owner)`**：读取最新 500 条并剔除内部追踪项 `kind == TRACK`；
2. **`Stats(ctx, owner)`**：`toConfirm`/`toDo`/`unreadNotices` 统计 `OPEN` 态 `CONFIRM`/`ACTION`/`NOTICE`；`doneToday` / `doneThisWeek`（按配置时区周一 `00:00` 起） / `doneThisMonth` / `doneTotal` 直接聚合该用户 `status == DONE` 的任务；
3. **5 类事件监听**：
   - `TaskStatusChanged`（`key = "task:" + taskId`）：`PENDING` 无旧记录写 `TRACK`；`RUNNING` 时若原为 `OPEN` 的 `CONFIRM` 退回 `TRACK`；`NEEDS_CONFIRMATION` 写 `CONFIRM/OPEN`（`"等待你确认后继续"`）；`ERROR` 写 `CONFIRM/OPEN`（`"执行失败：" + err`）；`DONE` 写 `DONE/OPEN`（去 Markdown 符号截 200 rune）；`STOPPED`/`DELETED` 置 `IGNORED`；
   - `PlanChanged`（`key = "plan:" + owner`）：`open=true` → `CONFIRM/OPEN`（`linkType="superagent"`）；`open=false` → 已有 `OPEN` 置 `DONE`；
   - `InboxTodoChanged`（`key = "todo:" + todoId`）：Upsert `ACTION` 并按状态映射到 `OPEN`/`IGNORED`/`DONE`；
   - `AgentShared`（`key = "shared:" + agentId`）：`NOTICE/OPEN`（`linkType="agent"`）；
   - `DefaultModelChanged`（`key = "default-model"`）：扇出给所有 `Enabled && IsAdmin()` 的管理员，原地刷新同一卡片；
4. **`Ignore` 联动**：校验非 `TRACK` 且属于当前用户 → 置 `IGNORED` → 发布 `eventbus.NoticeIgnored{Owner, LinkType, LinkID}`；
5. **`CompanyStarted` 首次回填**：查 `system_setting["notice.backfilled"]`，未执行过则导入存量 `NEEDS_CONFIRMATION`/`ERROR` 任务与 `OPEN` 收件箱待办并置位 `"true"`。

### 5.3 `AnalyticsService`（对齐 `AnalyticsService.java:48-107`）

- `span := max(7, min(days, 90))`；预填充 `[today-(span-1)..today]` 零值日桶；
- 排除 `DELETED` 任务与 Agent；`activeUsers` 统计 `created_at > now - 7d` 的去重非空 `created_by`；
- `done+failed == 0` 时 `SuccessRate = nil`，否则 `float64(done)/float64(done+failed)`；`TotalSpend` 与各模型 `Spend` 全程 `decimal.Decimal` 累加；`Models` 按 `Tasks DESC` 排序，空模型名输出 `nil`。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/platform/settings/settings_test.go`（红灯）→ 实现 `SettingsService`（全字段原子预检、已保存失效模型豁免、事务写与 `memcache` 缓存失效）。
- [ ] **T2**：编写 `internal/platform/settings/branding_test.go`（红灯）→ 实现 `BrandingService`（无租户回退内嵌 PNG、1 MB 光栅校验、SHA-256 ETag 与原子落盘）。
- [ ] **T3**：编写 `internal/platform/notice/notice_test.go`（红灯）→ 实现 `NoticeService`（列表/统计/状态变更/全部已读/5 类事件监听/`NoticeIgnored`/幂等回填）。
- [ ] **T4**：编写 `internal/platform/analytics/analytics_test.go`（红灯）→ 实现 `AnalyticsService`（7–90d 夹紧、零填充日桶、活跃用户、十进制费用聚合与模型排序）。
- [ ] **T5**：运行 `go test -race -cover ./internal/platform/{settings,notice,analytics}/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-17` 状态更新；
- [ ] `configs/config.example.yaml`：补充 `branding.dir`、`notice.timezone` 配置注释。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `SettingsService` 全字段原子预检与模型豁免 | `internal/platform/settings/settings_test.go` | 任意一字段非法时**零写库**；已保存失效模型不阻塞修改公司名；新增未过探活模型返 `400`；`fixedOn=true` 且空列表返 `400` |
| 2 | `BrandingService` 回退、光栅限制与 ETag | `internal/platform/settings/branding_test.go` | 无租户返回内嵌默认 PNG；拒绝 SVG/>1 MB；上传合法 WebP 后 ETag 变更，`ResetLogo` 恢复默认 |
| 3 | `NoticeService` 5 类事件与原地去重演进 | `internal/platform/notice/notice_test.go` | 同 `task:<id>` 历经 `pending(TRACK)`→`needs_confirmation(CONFIRM)`→`done(DONE)` 不产生重复行；操作 `TRACK` 返 `404`；`Ignore` 发布 `NoticeIgnored`；`DefaultModelChanged` 仅扇出启用管理员 |
| 4 | `NoticeService` 幂等回填与 `MarkAllRead` | `internal/platform/notice/notice_test.go` | 连续触发 2 次 `CompanyStarted` 仅回填 1 次；`MarkAllRead` 仅将 `OPEN` 的 `DONE`/`NOTICE` 标 `DONE`，不动 `CONFIRM`/`ACTION` |
| 5 | `AnalyticsService` 日趋势、成功率与十进制汇总 | `internal/platform/analytics/analytics_test.go` | `days=3`→7、`days=200`→90；无终态任务时 `SuccessRate==nil`；排除 `DELETED` 记录；`decimal.Decimal` 精确相加 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/platform/{settings,notice,analytics}/...` 全绿
- [ ] §2 交付物表 5 项齐备，核心包覆盖率 ≥ 80%
- [ ] `internal/arch_test.go` 通过（`platform/settings` 与 `platform/provider` 零循环依赖，零 Redis 引入）

---

## 10. 可提交节点

- 建议 commit 1：`feat(platform-settings): add atomic SettingsService and BrandingService with embedded fallback logo (spec 17, SA-17)`
- 建议 commit 2：`feat(platform-notice): add event-driven NoticeService and AnalyticsService (spec 17, SA-17)`
