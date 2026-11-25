# spec 05 | L1 核心领域契约（一）— 平台治理、供应商连接器、知识资产与成本用量模型/Store/SPI

- **状态**：`冻结待实施`
- **篇号顺序**：05（L1 核心领域契约首篇；与 `L1/spec_06` 共同完成母本全部领域模型、38 个 Store 接口与 12+ 个跨模块/二期预留 SPI 的无损切分）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-05`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§4
  - [02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§3
  - [03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md](../../03_核心抽象与插件化引擎_Runtime_MCP与ModelGateway.md) §1.3, §4
  - 归档母本：[_epic/L1/spec_01_core_tenant_crypto_egress.md](../_epic/L1/spec_01_core_tenant_crypto_egress.md) §1–§5
  - [SuperAgent/01_系统架构与多租户安全底座.md](../../../SuperAgent/01_系统架构与多租户安全底座.md)
  - [SuperAgent/09_数据存储模型全量设计.md](../../../SuperAgent/09_数据存储模型全量设计.md)
- **前置依赖**：
  - [L0/spec_01 (SA-01)](../L0/spec_01_config_obs_error_lifecycle.md)（依赖 `internal/core/apperror`）
  - [L0/spec_04 (SA-04)](../L0/spec_04_tenant_crypto_bus_archguard.md)（依赖 `internal/core/tenant`、`internal/core/eventbus` 与 `internal/arch_test.go`）
- **下游消费者**：`SA-06`（运行/编排契约扩展）、`SA-07`（PG 平台与供应商存储）、`SA-08`（PG 资产与成本存储）、`SA-10`（`NoticeStore` 实现与 38 Store 收口装配）、`SA-15`–`SA-25`（L2 平台/租户/认证/组织/配置/通知/供应商/连接器/文件/项目/技能、L3 成本定价路由与五维用量）、`SA-39`（Web 平台与资产端路由）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

在 `synapse-server`（`module github.com/Karlsk/synapse-os`，`go 1.26.1`）完成 L0 运行基础设施（`SA-01`–`SA-04`）之后，进入业务持久化与领域服务实现前，必须先在零外部重依赖的契约层冻结**平台治理、供应商连接器、知识资产与成本用量**四大核心域的数据结构、抽象存储端口与可插拔 SPI。

本篇（`SA-05`）将归档母本 `_epic/L1/spec_01` 中面向「平台 + 供应商 + 资产 + 成本」的全部领域契约拆分为独立可审、单人 1.5 人天可交付的原子 PR，解决三大核心问题：
1. **消除存储与业务耦合**：所有 L2/L3 业务服务只面向 `internal/core/store` 纯接口编程，绝不直接触碰 `pgx/v5` 或 SQL，为 PostgreSQL 17 实现（`SA-07`/`SA-08`/`SA-10`）及未来多库扩展建立编译期隔离；
2. **锁定商业私有化 V1 基线与二期零侵入扩展点**：原生纳入 V1 新增的持久化会话 `UserSession`、`NUMERIC(16,6)` 精确计费（`shopspring/decimal`），同步冻结 `Authenticator`（OIDC/LDAP）、`OwnerTransferHook`（离职资产交接）、`LicenseQuotaHook`（商业席位控制）、`ModelGateway`（集中式代理网关）、`FileStore`/`FileStoreProvider`（Local/S3 对象存储）等核心 SPI；
3. **与 `SA-06` 严格无环分层**：本篇所有类型与接口 100% 自洽，**零引用** `SA-06` 中的 Agent/Chat/Task/Inbox/Orchestrator 类型，确保 `SA-05` 合入即可独立通过 `go build ./... && go test -race ./...`。

> **包路径与 38 Store 守恒口径说明**：
> - 本篇与 `SA-06` 的领域模型包统一落位为 **`internal/core/model`**（`package model`），完整无损继承归档母本 `_epic/L1/spec_01` 中 `internal/core/domain` 的全部结构体、枚举、常量与纯领域工具函数（全仓统一采用 `internal/core/model`，严禁并存 `domain` 与 `model` 两套重复包）。
> - **38 Store 接口精确守恒切分**：本篇定义 `TxManager` + **前 19 个持久化 Store 接口**（对应 24 张 PG17 表，覆盖 `SA-07` 的 9 个平台/供应商 Store、`SA-08` 的 9 个资产/成本 Store，以及与平台通知模型同域的 `NoticeStore`——其 PG 表 `notice` 随 `00006_event_orchestrator.sql` 在 `SA-10` 实现）；`SA-06` 定义**后 19 个持久化 Store 接口**（对应 23 张 PG17 表，覆盖 `SA-09` 的 14 个 Agent/Task Store 与 `SA-10` 的 5 个 Event/Orchestrator Store）。两篇并集 **`19 + 19 = 38` 个 Store 接口、`24 + 23 = 47` 张 PG17 表**，100% 覆盖母本，零遗漏、零重复。

### 1.2 本篇边界与 V1 裁剪红线

- **本篇负责**：
  - `internal/core/model`：平台/租户/用户/部门路径/会话/配额/配置/通知、模型供应商/目录/安装快照/凭据/集成/连接器、项目/工作文件/个人目录/技能、模型选型审计/定价路由值对象/Token 五维用量/回合耗时的全部领域结构体、枚举、常量与纯领域算法（`TeamPaths`、`WorkFile.CleanTags`、`WorkFileKindOf`、`RunCredential.String` 脱敏）；
  - `internal/core/store`：`TxManager`（与 `L0/spec_02` `db.TxManager` 同构）+ 19 个数据库 Store 接口；
  - `internal/core/spi`：平台、供应商、资产与成本侧 9 组核心 SPI（`Authenticator`、`OwnerTransferHook`、`LicenseQuotaHook`、`ModelGateway`、`ModelCaller`、`ModelChecker`、`FileStore`/`FileStorage` + `FileStoreProvider`、`ProjectAccess`、`ModelRouter`、`UsageRecorder`）；
  - `internal/core/eventbus/events_platform.go`：4 类平台/配置/成本/通知领域事件结构体（`CompanyStarted`、`IntegrationChanged`、`DefaultModelChanged`、`NoticeIgnored`），直接配合 `L0/spec_04` 泛型事件总线使用。
- **本篇不负责（留给其他篇）**：
  - Agent/Chat/Runtime/Task/Automation/Inbox/EventSource/SuperAgent 的领域模型、后 19 个 Store 接口、运行/MCP/IM/编排侧 SPI 及剩余 5 类事件 → 见 `L1/spec_06 (SA-06)`；
  - PostgreSQL 17 建表迁移脚本与 38 个 Store 实现 → 见 `L1/spec_07`–`spec_10 (SA-07`–`SA-10)`；
  - HTTP DTO 与 Gin 路由注册 → 见 `L5`；
- **硬性红线**：
  - **严禁 Redis**：本篇零 Redis 接口、零 Redis 缓存依赖（会话持久化使用 PG17 `SessionStore`，短时热缓存由 `L0/spec_04` 纯内存 `memcache` 承担）；
  - **严禁引入废弃概念与字段**：不引入公网自注册路由/异常，不引入 GitHub/Gitee/Notion/Confluence 集成常量，不引入消费级技能种子常量，不引入 `MaterialPrompt` 或任何实体 `*.tier` 废弃字段（仅在 `TaskModelChoice` 审计记录中保留历史选型档位说明字段 `Tier`，详见 §3）。

### 1.3 Java 原版源码精确映射表

所有路径均经核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`：

| 目标 Go 文件 | Java 参照源码（相对 `orgin/SuperAgent/`） | 核验行号范围 | 重构与 V1 演进要点 |
|---|---|---|---|
| `internal/core/model/platform.go` | `superagent-core/src/main/java/com/superagent/core/tenant/Company.java`、`user/{User,UserSource}.java`、`org/TeamPaths.java`、`notice/Notice.java` | `Company:1-35`, `User:1-52`, `UserSource:1-9`, `TeamPaths:1-59`, `Notice:1-50` | 新增 `UserSession`（持久化登录态替代 JVM `HttpSession`）与 `QuotaOverrides` 值对象；完整保真 `TeamPaths` 8 层/64 字符校验规则 |
| `internal/core/model/provider.go` | `superagent-core/src/main/java/com/superagent/core/provider/{ModelProvider,ProviderCredential,ProviderCatalog}.java`、`chat/RunCredential.java`、`connector/{Connector,ConnectorKind,ConnectorAuthMethod,ConnectorTransport,ConnectorCredential,McpServerSpec}.java`、`superagent-platform/src/main/java/com/superagent/platform/provider/InstallJob.java` | `ModelProvider:1-54`, `ProviderCredential:1-58`, `ProviderCatalog:1-106`, `RunCredential:1-15`, `Connector*:1-27`, `McpServerSpec:1-19`, `InstallJob:1-57` | `ModelsJSON` 改 `[]byte` 对齐 PG17 `JSONB`；`RunCredential.String()` 强脱敏禁打 `APIKey`；企业集成键收敛为 `feishu`/`dingtalk`；沉淀 `InstallJobSnapshot` 只读值对象 |
| `internal/core/model/assets.go` | `superagent-core/src/main/java/com/superagent/core/project/{Project,ProjectStatus,ProjectMember,ProjectItemKind}.java`、`file/{WorkFile,WorkFileKind,FileFolder}.java`、`skill/{Skill,SkillDeletion}.java` | `Project:1-31`, `ProjectMember:1-8`, `WorkFile:1-98`, `WorkFileKind:1-28`, `FileFolder:1-23`, `Skill:1-29`, `SkillDeletion:1-28` | `WorkFile.CleanTags` 按 Unicode rune 截断 20 字符防止中文乱码；`WorkFileKind` 统一小写枚举值对齐 PG 存储 |
| `internal/core/model/cost.go` | `superagent-core/src/main/java/com/superagent/core/task/TaskModelChoice.java`、`usage/{TokenUsage,UsageContext,UsageRecord}.java`、`timing/TurnTiming.java` | `TaskModelChoice:1-38`, `TokenUsage:1-26`, `UsageContext:1-20`, `UsageRecord:1-16`, `TurnTiming:1-31` | 金额字段统一采用 `github.com/shopspring/decimal` 对齐 PG17 `NUMERIC(16,6)`；新增 `ModelPriceFact`/`ModelPrice`/`ModelCandidate` 成本值对象 |
| `internal/core/store/{tx,platform,provider,assets,cost}_store.go` | `superagent-core/src/main/java/com/superagent/core/{tenant/CompanyStore,user/UserStore,org/OrgStore,settings/SystemSettingStore,notice/NoticeStore,connector/ConnectorStore,connector/ConnectorCredentialStore,provider/ModelProviderStore,provider/ProviderCredentialStore,project/ProjectStore,project/ProjectItemStore,file/WorkFileStore,file/FileFolderStore,skill/SkillStore,skill/SkillDeletionStore,task/TaskModelChoiceStore,usage/UsageStore,timing/TurnTimingStore}.java` | 全量 18 个 Java Store 接口 + V1 新增 `SessionStore` | 全部方法首参统一 `ctx context.Context` 透传租户与事务；新增 `TxManager` 与 `SessionStore` |
| `internal/core/spi/{auth,platform,provider,assets,cost}.go` | `superagent-core/src/main/java/com/superagent/core/{file/FileStore,file/FileStoreProvider,project/ProjectAccess,provider/ModelRouter,usage/UsageRecorder}.java`、`superagent-platform/src/main/java/com/superagent/platform/provider/ModelCall.java` | `FileStore:1-28`, `FileStoreProvider:1-31`, `ProjectAccess:1-36`, `ModelRouter:1-31`, `UsageRecorder:1-34`, `ModelCall:29-103` | 补齐二期预留 SPI（`Authenticator`/`OwnerTransferHook`/`LicenseQuotaHook`/`ModelGateway`）；`FileStore` 增补 `OpenRange` 支撑 S3 视频拖拽，并提供 `FileStorage` 类型别名；抽离 `ModelCaller`/`ModelChecker` |
| `internal/core/eventbus/events_platform.go` | `superagent-core/src/main/java/com/superagent/core/{tenant/CompanyStarted,integration/IntegrationChanged,notice/NoticeEvents}.java` | `CompanyStarted:8-9`, `IntegrationChanged:4-5`, `NoticeEvents:42-47` | 冻结 4 类平台/配置/成本/通知事件结构体，供 `SA-04` 泛型 `eventbus.Bus` 零反射发布订阅 |

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | 平台治理领域模型（`Company`/`QuotaOverrides`/`UserSession`/`User`/`UserSource`/`Notice`/`NoticeKind`/`NoticeState`）与 `TeamPaths` 纯领域校验工具 | `internal/core/model/platform.go` | 新增 |
| 2 | 模型供应商、目录规格、安装快照、凭据、脱敏 `RunCredential`、集成键与 MCP 连接器领域模型 | `internal/core/model/provider.go` | 新增 |
| 3 | 知识资产领域模型（`Project`/`ProjectMember`/`WorkFile`/`WorkFileKind`/`FileFolder`/`Skill`/`SkillDeletion`）与 `CleanTags`/`WorkFileKindOf` 工具 | `internal/core/model/assets.go` | 新增 |
| 4 | 成本路由选型审计、定价目录值对象、五维 Token 用量与回合耗时领域模型（`TaskModelChoice`/`ModelPriceFact`/`ModelPrice`/`ModelCandidate`/`TokenUsage`/`UsageContext`/`UsageRecord`/`TurnTiming`/`ToolTime`） | `internal/core/model/cost.go` | 新增 |
| 5 | 通用事务端口 `TxManager` 与前 19 个持久化 Store 接口（按域拆 5 文件） | `internal/core/store/tx.go`、`platform_store.go`、`provider_store.go`、`assets_store.go`、`cost_store.go` | 新增 |
| 6 | 平台、供应商、资产与成本侧 9 组 SPI（含 3 大二期预留 + `LicenseQuotaHook` + `FileStore`/`FileStorage` + `ModelCaller`/`ModelChecker` + `ProjectAccess` + `ModelRouter` + `UsageRecorder`） | `internal/core/spi/auth.go`、`platform.go`、`provider.go`、`assets.go`、`cost.go` | 新增 |
| 7 | 4 类平台/配置/成本/通知领域事件结构体（`CompanyStarted`/`IntegrationChanged`/`DefaultModelChanged`/`NoticeIgnored`） | `internal/core/eventbus/events_platform.go` | 新增 |
| 8 | 领域模型、纯算法工具、脱敏格式化、金额精度与 SPI 契约单测套件 | `internal/core/model/{platform,provider,assets,cost}_test.go`、`internal/core/spi/platform_assets_test.go` | 新增 |

> **分层守卫约束（由 `L0/spec_04` `internal/arch_test.go` 强制校验）**：
> - `internal/core/model` 仅允许依赖 Go 标准库、`github.com/shopspring/decimal` 与 `internal/core/apperror`；
> - `internal/core/store` 仅允许依赖 Go 标准库与 `internal/core/model`；
> - `internal/core/spi` 仅允许依赖 Go 标准库、`github.com/shopspring/decimal` 与 `internal/core/model`；
> - 上述所有包**严禁 import** `jackc/pgx`、`database/sql`、`gin-gonic/gin`、`os/exec`、任何上层业务包或任何 Redis 驱动。

---

## 3. 数据库设计

本篇属于 L1 核心领域契约层，**不编写 SQL DDL 或 `goose` 迁移脚本**，亦不持有任何数据库驱动依赖。本篇为下游 `SA-07`（10 表）、`SA-08`（13 表）与 `SA-10`（`notice` 表）冻结以下映射与字段约束：

1. **租户隔离契约（Fail-Closed）**：
   - 仅 2 个全局 Store（`CompanyStore` → `company`、`SessionStore` → `user_session`）跨租户可见；其中 `UserSession` 仍携带 `CompanyID` 字段供 `DeleteByUser(ctx, companyID, userID)` 按租户精确注销；
   - 其余 **17 个租户 Store**（对应 22 张表）的所有接口方法签名**绝不接收 `companyID` 参数**，下游实现层必须强制调用 `tenant.Require(ctx)`，缺失即返回 `tenant.ErrMissingTenant`。
2. **时间、金额与 JSONB 类型映射契约**：
   - 全部时间戳字段在模型中使用 `time.Time` / `*time.Time`，对应 PG17 `TIMESTAMPTZ`（落库前统一 `.UTC().Truncate(time.Millisecond)`）；
   - 费用金额 `TokenUsage.CLICostUSD`、`UsageRecord.CostUSD`、`UsageRecord.CLICostUSD` 统一使用 `*decimal.Decimal`（`github.com/shopspring/decimal`），对齐 PG17 `NUMERIC(16,6)`，杜绝二进制浮点账单漂移；
   - `ModelProvider.ModelsJSON` 采用 `[]byte` 对齐 PG17 `JSONB`；`TurnTiming.Tools` 为 `[]ToolTime`，在 PG17 `turn_timing.tools` 中序列化为 `JSONB` 数组（彻底替代原版制表符拼接文本）。
3. **废弃字段清零核验**：
   - 本篇所有实体零废弃列；
   - 特别说明：`TaskModelChoice.Tier` 属于成本路由审计历史文本（记录裁判分诊出的档位标签，对应 `task_model_choice.tier`），**绝非** `Agent`/`Task`/`Automation` 实体上已砍除的 `*.tier` 配置列。
4. **全量 38 Store 接口守恒映射总账（`SA-05` + `SA-06` ↔ `SA-07`–`SA-10`）**：

| 契约归属 | 分组 | Store 接口清单 | 接口数 | 对应 PG17 实现篇 | 对应表数 |
|---|---|---|---:|---|---:|
| **`SA-05`（本篇）** | 平台核心与通知 | `CompanyStore`[G]、`SessionStore`[G]、`UserStore`、`OrgStore`、`SystemSettingStore`、`NoticeStore` | **6** | `SA-07`(5) + `SA-10`(1 `notice`) | 7 |
| **`SA-05`（本篇）** | 供应商与连接器 | `ConnectorStore`、`ConnectorCredentialStore`、`ModelProviderStore`、`ProviderCredentialStore` | **4** | `SA-07`(4) | 4 |
| **`SA-05`（本篇）** | 知识资产 | `ProjectStore`、`ProjectItemStore`、`WorkFileStore`、`FileFolderStore`、`SkillStore`、`SkillDeletionStore` | **6** | `SA-08`(6) | 10 |
| **`SA-05`（本篇）** | 成本路由与用量耗时 | `TaskModelChoiceStore`、`UsageStore`、`TurnTimingStore` | **3** | `SA-08`(3) | 3 |
| *`SA-05` 小计* | — | **19 个持久化 Store**（+ 通用事务端口 `TxManager`） | **19** | `SA-07`(9)+`SA-08`(9)+`SA-10`(1) | **24** |
| `SA-06`（下篇） | Agent / Chat / Task / Automation / Event / SuperAgent | 详见 [L1/spec_06](./spec_06_core_domain_runtime_agent_task_contracts.md) §3 | **19** | `SA-09`(14) + `SA-10`(5) | **23** |
| **全仓合计** | — | **2 全局 Store + 36 租户 Store（100% 覆盖母本，零重复、零遗漏）** | **38** | `SA-07`–`SA-10`（6 个迁移文件） | **47** |

---

## 4. 冻结契约

### 4.1 导出符号

#### A. `internal/core/model`（平台、供应商、资产与成本领域模型全集）

```go
package model

import (
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// ==================== 1. 平台治理：租户、配额、会话、用户与通知 (platform.go) ====================

// Company 企业租户实体（全局表 company；Java: Company.java:L10-L35）
type Company struct {
	ID              string
	Name            string
	Enabled         bool
	DataKeyCipher   string // v1:<sealedDEK>，懒初始化前为空串
	MaxUsers        *int   // nil = 走全局配置默认，0 = 不限
	MaxRunningTasks *int
	MonthlyTokens   *int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// QuotaOverrides 单租户配额覆写值对象（nil 表示继承全局默认，0 表示无限制）
type QuotaOverrides struct {
	MaxUsers        *int
	MaxRunningTasks *int
	MonthlyTokens   *int64
}

// UserSession [V1 新增] 服务端持久化登录会话（全局表 user_session，替代 JVM 内存 HttpSession，零 Redis）
type UserSession struct {
	ID         string // 32 字节加密随机 hex（64 字符）
	CompanyID  string
	UserID     string
	PasswordOK bool
	ExpiresAt  time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// UserSource 用户账号来源枚举（Java: UserSource.java:L4-L9）
type UserSource string

const (
	UserSourceLocal  UserSource = "LOCAL"
	UserSourcePhone  UserSource = "PHONE"
	UserSourceSSO    UserSource = "SSO"
	UserSourceWeChat UserSource = "WECHAT"
)

// User 租户内预置员工账号（Java: User.java:L19-L52）
type User struct {
	ID                 string     // 不可变 UUID，全系统逻辑外键标识
	Username           string     // 租户内唯一登录名
	DisplayName        string
	Roles              []string   // 裸角色名（如 ["ADMIN"]；USER 隐式具备）
	Enabled            bool
	PasswordHash       string     // Argon2id / bcrypt 哈希，对外 DTO 永不序列化
	Phone              string
	ExternalID         string
	Source             UserSource
	CreatedAt          time.Time
	UpdatedAt          time.Time
	MustChangePassword bool
}

func (u User) IsAdmin() bool

// NoticeKind / NoticeState / Notice 待办首页通知模型（Java: Notice.java:L11-L50）
type NoticeKind string

const (
	NoticeKindConfirm NoticeKind = "CONFIRM"
	NoticeKindAction  NoticeKind = "ACTION"
	NoticeKindDone    NoticeKind = "DONE"
	NoticeKindNotice  NoticeKind = "NOTICE"
	NoticeKindTrack   NoticeKind = "TRACK" // 仅内部追踪，NoticeStore.List 强制过滤
)

type NoticeState string

const (
	NoticeStateOpen    NoticeState = "OPEN"
	NoticeStateDone    NoticeState = "DONE"
	NoticeStateIgnored NoticeState = "IGNORED"
)

type Notice struct {
	ID        string
	Owner     string
	Key       string
	Kind      NoticeKind
	State     NoticeState
	Title     string
	Body      string
	Actor     string
	LinkType  string // task | agent | superagent | todo
	LinkID    string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// TeamPaths 部门/团队层级路径纯领域函数（Java: TeamPaths.java:L7-L59）
const (
	MaxTeamSegmentLen = 64
	MaxTeamDepth      = 8
)

func NormalizeTeamPath(raw string) (string, error)
func TeamAncestors(team string) []string
func TeamWithin(team, root string) bool

// ==================== 2. 模型供应商、安装快照、凭据、集成与连接器 (provider.go) ====================

// 协议族常量与供应商静态目录值对象（Java: ProviderCatalog.java:L21-L53）
const (
	ProtocolAnthropic = "anthropic"
	ProtocolOpenAI    = "openai"
	ProtocolOther     = "other"
)

// IntegrationKey 商业私有化 V1 保留的企业集成标识（V1 裁剪 GitHub/Gitee/Notion/Confluence）
const (
	IntegrationKeyFeishu   = "feishu"
	IntegrationKeyDingTalk = "dingtalk"
)

type ProviderAccessSpec struct {
	Protocol       string // anthropic | openai | other
	DefaultBaseURL string
	KeyEnv         string
	BaseURLEnv     string
}

type ProviderCatalogEntry struct {
	Key            string
	Name           string
	CLI            string
	Supported      bool
	InstallCommand string
	UpgradeCommand string
	VersionArgs    []string
	NPMPackage     string
	AuthFiles      []string
	AuthEnvVars    []string
	MinNode        string
	Preview        bool
	Access         ProviderAccessSpec
}

func (e ProviderCatalogEntry) Installable() bool { return e.InstallCommand != "" }

// ModelProvider 供应商 CLI 运行状态与探测模型目录（Java: ModelProvider.java:L10-L54）
const (
	ProviderStatusUnsupported  = "unsupported"
	ProviderStatusNotInstalled = "not_installed"
	ProviderStatusNoAuth       = "no_auth"
	ProviderStatusOK           = "ok"
	ProviderStatusNeedsUpgrade = "needs_upgrade"
)

type ModelProvider struct {
	ID              string
	ProviderKey     string
	Installed       bool
	CanInfer        bool
	CurrentVersion  string
	LatestVersion   string
	Status          string // unsupported | not_installed | no_auth | ok | needs_upgrade
	LastCheckedAt   *time.Time
	ModelsJSON      []byte // PG17 JSONB
	ModelsError     string
	ModelsCheckedAt *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// InstallJobSnapshot 供应商 CLI 或私有 Node 安装/升级任务只读快照值对象（Java: InstallJob.java:L11-L57）
const InstallJobMaxLines = 400

type InstallJobSnapshot struct {
	Action     string
	Step       string // 运行中为当前步骤，结束后为空串
	StartedAt  time.Time
	FinishedAt *time.Time
	Running    bool
	Error      string
	Lines      []string // 最近最多 400 行日志
}

// ProviderCredential 供应商多密钥容灾池条目（Java: ProviderCredential.java:L10-L58）
const (
	CredSourceConsole = "console"
	CredSourceMachine = "machine"
	CredStatusOK      = "ok"
	CredStatusError   = "error"
	CredStatusUnknown = "unknown"
)

type ProviderCredential struct {
	ID          string
	ProviderKey string
	Label       string
	BaseURL     string
	KeyCipher   string // 经 SecretCipher 加密的 v2:<companyID>:... 密文
	Bearer      bool
	Priority    int
	Enabled     bool
	Status      string // ok | error | unknown
	LastError   string
	CheckedAt   *time.Time
	Deleted     bool // 软删除保留 key_hash 防止重复导入宿主机密钥
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Source      string // console | machine
	KeyHash     string // SHA-256 hex，用于去重
}

// RunCredential 单次 CLI 执行期解密后的临时凭据值对象（Java: RunCredential.java:L9-L15）
type RunCredential struct {
	ID      string
	BaseURL string
	APIKey  string // 仅内存短生命周期持有，严禁打日志或序列化
	Bearer  bool
}

// String 强制脱敏输出，绝不暴露 APIKey 明文
func (r RunCredential) String() string {
	return fmt.Sprintf("RunCredential{id=%q, baseURL=%q, bearer=%v, apiKey=<redacted>}", r.ID, r.BaseURL, r.Bearer)
}

// Connector / ConnectorCredential / MCPServerSpec（Java: core/connector/*.java）
type ConnectorKind string

const ConnectorKindMCP ConnectorKind = "MCP"

type ConnectorAuthMethod string

const (
	ConnectorAuthNone          ConnectorAuthMethod = "NONE"
	ConnectorAuthSharedToken   ConnectorAuthMethod = "SHARED_TOKEN"
	ConnectorAuthPersonalToken ConnectorAuthMethod = "PERSONAL_TOKEN"
	ConnectorAuthOAuth         ConnectorAuthMethod = "OAUTH"
)

type ConnectorTransport string

const (
	ConnectorTransportStdio ConnectorTransport = "STDIO"
	ConnectorTransportHTTP  ConnectorTransport = "HTTP"
	ConnectorTransportSSE   ConnectorTransport = "SSE"
)

type Connector struct {
	ID          string
	Name        string
	Description string
	Kind        ConnectorKind
	AuthMethod  ConnectorAuthMethod
	Enabled     bool
	Transport   ConnectorTransport
	Endpoint    string
	TokenEnv    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

const ConnectorCredentialCompanyOwner = ""

type ConnectorCredential struct {
	ConnectorID   string
	Owner         string // "" 表示租户共享凭证；否则为 app_user.id
	SecretCipher  string
	AccountLabel  string
	UpdatedAt     time.Time
	RefreshCipher string
	ExpiresAt     *time.Time
}

type MCPServerSpec struct {
	Name      string
	Transport ConnectorTransport
	URL       string
	Headers   map[string]string
	Command   string
	Args      []string
	Env       map[string]string
}

// ==================== 3. 知识资产：项目、工作文件、个人目录与技能 (assets.go) ====================

type ProjectStatus string

const (
	ProjectStatusActive   ProjectStatus = "ACTIVE"
	ProjectStatusArchived ProjectStatus = "ARCHIVED"
)

type ProjectItemKind string

const (
	ProjectItemAgent ProjectItemKind = "AGENT"
	ProjectItemTask  ProjectItemKind = "TASK"
	ProjectItemFile  ProjectItemKind = "FILE"
)

type Project struct {
	ID          string
	Name        string
	Description string
	Owner       string
	Status      ProjectStatus
	CreatedAt   time.Time
	UpdatedAt   time.Time
	ArchivedAt  *time.Time
}

type ProjectMember struct {
	ProjectID string
	Username  string // 实际存储 app_user.id
	AddedBy   string
	Active    bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type WorkFileKind string

const (
	WorkFileDocument WorkFileKind = "document"
	WorkFileImage    WorkFileKind = "image"
	WorkFileVideo    WorkFileKind = "video"
	WorkFileLink     WorkFileKind = "link"
	WorkFileOther    WorkFileKind = "other"
)

func WorkFileKindOf(filename string) WorkFileKind
func ParseWorkFileKind(raw string) WorkFileKind

const (
	WorkFileStorageInternal = "internal"
	WorkFileStorageLink     = "link"
	WorkFileOriginChat      = "chat"
	WorkFileOriginTask      = "task"
	WorkFileOriginDelivery  = "delivery"
	WorkFileOriginUpload    = "upload"
	TagStatePending         = ""
	TagStateAgent           = "agent"
	TagStateAuto            = "auto"
	TagStateUser            = "user"
	MaxWorkFileTags         = 5
	MaxWorkFileTagLen       = 20
)

type WorkFile struct {
	ID        string
	Name      string
	Kind      WorkFileKind
	MimeType  string
	Size      int64
	Storage   string // internal | link | <channel>
	Location  string
	Owner     string
	AgentID   string
	TaskID    string
	Origin    string // chat | task | delivery | upload
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
	Tags      []string
	TagState  string // "" | agent | auto | user
}

func CleanWorkFileTags(raw []string) []string
func (f WorkFile) WithTags(tags []string, state string) WorkFile
func (f WorkFile) WithContent(name string, kind WorkFileKind, mime string, size int64, now time.Time) WorkFile
func (f WorkFile) WithStorage(storage string, now time.Time) WorkFile
func (f WorkFile) MarkDeleted(now time.Time) WorkFile

type FileFolder struct {
	ID        string
	Owner     string
	Name      string
	ParentID  string
	Deleted   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Skill struct {
	ID          string
	Name        string
	Description string
	Category    string
	Content     string
	Space       string // personal:<userId> | team:<path> | company
	Enabled     bool
	Deleted     bool
	CreatedBy   string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type SkillDeletionStatus string

const (
	SkillDeletionPending   SkillDeletionStatus = "PENDING"
	SkillDeletionConfirmed SkillDeletionStatus = "CONFIRMED"
	SkillDeletionCancelled SkillDeletionStatus = "CANCELLED"
)

type SkillDeletion struct {
	ID        string
	SkillID   string
	SkillName string
	AgentID   string
	Requester string
	SessionID string
	MessageID string
	Status    SkillDeletionStatus
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ==================== 4. 成本定价、选型审计、五维 Token 用量与回合耗时 (cost.go) ====================

const (
	ChoiceByJudge      = "judge"
	ChoiceByUser       = "user"
	ChoiceByFallback   = "fallback"
	ChoiceByEscalation = "escalation"
	ChoiceByDefault    = "default"
	ChoiceByFixed      = "fixed"
)

// TaskModelChoice 任务模型路由决策不可变审计行（Java: TaskModelChoice.java:L20-L38）
type TaskModelChoice struct {
	TaskID     string
	Seq        int
	By         string // judge | user | fallback | escalation | default | fixed
	Difficulty string // easy | medium | hard | ""
	Tier       string // 仅作选型审计历史档位标记，非任务配置字段
	Model      string // 空串仅在 By==fixed 且全部候选耗尽拒绝执行时出现
	CLI        string
	Judge      string
	Candidates string
	Reasoning  string
	CreatedAt  time.Time
}

func (c TaskModelChoice) Refused() bool { return strings.TrimSpace(c.Model) == "" && c.By == ChoiceByFixed }
func (c TaskModelChoice) WithSeq(seq int) TaskModelChoice

// ModelPriceFact / ModelPrice / ModelCandidate 模型价格目录与路由候选值对象（USD / 1M tokens）
type ModelPriceFact struct {
	Input      decimal.Decimal
	Output     decimal.Decimal
	Released   string // ISO 日期，如 "2026-05-01"
	Source     string // 厂商 slug 或 "manual"
	CacheRead  *decimal.Decimal
	CacheWrite *decimal.Decimal
}

func (f ModelPriceFact) Blended() decimal.Decimal // (3*Input + Output) / 4

type ModelPrice struct {
	Input      decimal.Decimal
	Output     decimal.Decimal
	CacheRead  *decimal.Decimal
	CacheWrite *decimal.Decimal
	Source     string
}

type ModelCandidate struct {
	ID         string
	CLI        string
	Input      *decimal.Decimal
	Output     *decimal.Decimal
	CacheRead  *decimal.Decimal
	CacheWrite *decimal.Decimal
	Released   string
	Source     string
	Manual     bool
	ForTasks   bool
}

func (c ModelCandidate) Priced() bool { return c.Input != nil && c.Output != nil }
func (c ModelCandidate) Blended() (decimal.Decimal, bool)

// UsageSource 计费归因来源常量（Java: UsageContext.java:L10-L18）
const (
	UsageSourceTask        = "task"
	UsageSourceChat        = "chat"
	UsageSourceSuperAgent  = "superagent"
	UsageSourceBackground  = "superagent_background"
	UsageSourceModelChoice = "model_choice"
	UsageSourceMemory      = "memory"
	UsageSourceFileTags    = "file_tags"
	UsageSourceTaskTags    = "task_tags"
)

// TokenUsage 五维不重叠 Token 计数（Java: TokenUsage.java:L10-L26）
type TokenUsage struct {
	Model        string           `json:"model"`
	Input        int64            `json:"input"`
	CacheRead    int64            `json:"cacheRead"`
	CacheWrite   int64            `json:"cacheWrite"`
	CacheWrite1h int64            `json:"cacheWrite1h"`
	Output       int64            `json:"output"`
	Reasoning    int64            `json:"reasoning"`
	CLICostUSD   *decimal.Decimal `json:"cliCostUsd,omitempty"`
}

// UsageContext 单次模型调用计费归因上下文（Java: UsageContext.java:L8-L20）
type UsageContext struct {
	User      string
	Source    string
	AgentID   string
	TaskID    string
	SessionID string
	CLI       string
}

// UsageRecord 持久化 Token 用量与账单记录（Java: UsageRecord.java:L9-L16）
type UsageRecord struct {
	ID           string
	At           time.Time
	User         string
	Source       string
	AgentID      string
	TaskID       string
	SessionID    string
	CLI          string
	Model        string
	Input        int64
	CacheRead    int64
	CacheWrite   int64
	CacheWrite1h int64
	Output       int64
	Reasoning    int64
	CostUSD      *decimal.Decimal // NUMERIC(16,6)
	CLICostUSD   *decimal.Decimal // NUMERIC(16,6)
}

func (r UsageRecord) Total() int64 { return r.Input + r.CacheRead + r.CacheWrite + r.Output }

// ToolTime / TurnTiming 回合 7 阶段耗时与工具耗时统计（Java: TurnTiming.java:L11-L31）
type ToolTime struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Ms    int64  `json:"ms"`
}

type TurnTiming struct {
	SessionID  string
	MessageID  string
	Label      string
	StartedAt  time.Time
	FinishedAt time.Time
	PrepMs     int64
	StartupMs  int64
	ModelMs    int64
	ToolsMs    int64
	OutputMs   int64
	RetryMs    int64
	SaveMs     int64
	Attempts   int
	Tools      []ToolTime
}

func (t TurnTiming) TotalMs() int64 { return t.FinishedAt.Sub(t.StartedAt).Milliseconds() }
```

#### B. `internal/core/store`（`TxManager` + 本篇 19 个持久化 Store 接口）

```go
package store

import (
	"context"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/model"
)

// TxManager 跨 Store 原子事务边界（与 L0/spec_02 internal/infra/db.TxManager 签名完全兼容）
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
	WithinReadOnlyTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// ==================== 分组 A：平台核心与通知 (platform_store.go · 6 Store) ====================

// 1. [GLOBAL] 企业租户 Store（不调用 tenant.Require）
type CompanyStore interface {
	List(ctx context.Context) ([]model.Company, error)
	Find(ctx context.Context, id string) (*model.Company, error)
	Save(ctx context.Context, c model.Company) (*model.Company, error)
}

// 2. [GLOBAL][V1 新增] 服务端持久化登录会话 Store（不调用 tenant.Require，替代内存 HttpSession，零 Redis）
type SessionStore interface {
	Create(ctx context.Context, s model.UserSession) error
	Find(ctx context.Context, id string) (*model.UserSession, error)
	Touch(ctx context.Context, id string, passwordOK bool, expiresAt time.Time) error
	Delete(ctx context.Context, id string) error
	DeleteByUser(ctx context.Context, companyID, userID string) error
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// 3. [TENANT] 租户用户账号 Store
type UserStore interface {
	ListUsers(ctx context.Context) ([]model.User, error)
	FindByID(ctx context.Context, id string) (*model.User, error)
	FindByUsername(ctx context.Context, username string) (*model.User, error)
	FindByPhone(ctx context.Context, phone string) (*model.User, error)
	FindByExternalID(ctx context.Context, externalID string) (*model.User, error)
	Save(ctx context.Context, u model.User) (*model.User, error)
	Delete(ctx context.Context, id string) error
}

// 4. [TENANT] 组织汇报关系与团队归属 Store（user_manager + user_team）
type OrgStore interface {
	Manager(ctx context.Context, userID string) (string, bool, error)
	Managers(ctx context.Context) (map[string]string, error)
	SetManager(ctx context.Context, userID, managerID string) error // managerID == "" 清除
	Teams(ctx context.Context, userID string) ([]string, error)
	Memberships(ctx context.Context) (map[string][]string, error)
	SetTeams(ctx context.Context, userID string, teams []string) error
}

// 5. [TENANT] 租户级扁平 KV 系统配置 Store
type SystemSettingStore interface {
	All(ctx context.Context) (map[string]string, error)
	Put(ctx context.Context, key, value string) error
}

// 6. [TENANT] 待办首页通知 Store（PG 实现归属 SA-10 00006_event_orchestrator.sql）
type NoticeStore interface {
	Save(ctx context.Context, n model.Notice) error
	Find(ctx context.Context, id string) (*model.Notice, error)
	FindByKey(ctx context.Context, owner, key string) (*model.Notice, error)
	List(ctx context.Context, owner string, limit int) ([]model.Notice, error) // 强制过滤 kind <> 'TRACK'
}

// ==================== 分组 B：模型供应商与 MCP 连接器 (provider_store.go · 4 Store) ====================

// 7. [TENANT] MCP 连接器定义 Store
type ConnectorStore interface {
	ListConnectors(ctx context.Context) ([]model.Connector, error)
	FindConnector(ctx context.Context, id string) (*model.Connector, error)
	Save(ctx context.Context, c model.Connector) (*model.Connector, error)
	Delete(ctx context.Context, id string) error
}

// 8. [TENANT] 连接器加密凭证 Store
type ConnectorCredentialStore interface {
	Find(ctx context.Context, connectorID, owner string) (*model.ConnectorCredential, error)
	List(ctx context.Context, connectorID string) ([]model.ConnectorCredential, error)
	Save(ctx context.Context, c model.ConnectorCredential) (*model.ConnectorCredential, error)
	Delete(ctx context.Context, connectorID, owner string) error
	DeleteAll(ctx context.Context, connectorID string) error
}

// 9. [TENANT] 模型供应商状态与模型目录 Store
type ModelProviderStore interface {
	List(ctx context.Context) ([]model.ModelProvider, error)
	FindByID(ctx context.Context, id string) (*model.ModelProvider, error)
	FindByKey(ctx context.Context, providerKey string) (*model.ModelProvider, error)
	Save(ctx context.Context, p model.ModelProvider) (*model.ModelProvider, error)
	Delete(ctx context.Context, id string) error
}

// 10. [TENANT] 供应商多密钥容灾池 Store
type ProviderCredentialStore interface {
	List(ctx context.Context, providerKey string) ([]model.ProviderCredential, error)
	Find(ctx context.Context, id string) (*model.ProviderCredential, error)
	KnownKeyHashes(ctx context.Context, providerKey string) (map[string]struct{}, error) // 必须包含已软删记录
	Save(ctx context.Context, c model.ProviderCredential) error
	Delete(ctx context.Context, id string) error // 软删除，保留 key_hash
}

// ==================== 分组 C：知识资产域 (assets_store.go · 6 Store) ====================

// 11. [TENANT] 协作项目与成员 Store（project + project_member）
type ProjectStore interface {
	Save(ctx context.Context, p model.Project) error
	Find(ctx context.Context, id string) (*model.Project, error)
	List(ctx context.Context) ([]model.Project, error)
	SaveMember(ctx context.Context, m model.ProjectMember) error
	Members(ctx context.Context, projectID string) ([]model.ProjectMember, error)
	ProjectsOf(ctx context.Context, userID string) ([]string, error)
}

// 12. [TENANT] 项目资源归属 Store（project_item）
type ProjectItemStore interface {
	Assign(ctx context.Context, kind model.ProjectItemKind, itemID, projectID string) error
	Unassign(ctx context.Context, kind model.ProjectItemKind, itemID string) error
	ProjectOf(ctx context.Context, kind model.ProjectItemKind, itemID string) (string, bool, error)
	Items(ctx context.Context, kind model.ProjectItemKind, projectID string) ([]string, error)
}

// 13. [TENANT] 工作文件与对话溯源 Store（work_file + work_file_ref）
type WorkFileStore interface {
	Save(ctx context.Context, f model.WorkFile) error
	Find(ctx context.Context, id string) (*model.WorkFile, error)
	ListFor(ctx context.Context, owner string, agentIDs []string) ([]model.WorkFile, error)
	Untagged(ctx context.Context, limit int) ([]model.WorkFile, error)
	Link(ctx context.Context, fileID, sessionID, messageID string) error
	Messages(ctx context.Context, fileIDs []string) (map[string][]string, error)
	CountByStorage(ctx context.Context) (map[string]int64, error)
}

// 14. [TENANT] 个人虚拟目录与放置 Store（file_folder + file_placement）
type FileFolderStore interface {
	Save(ctx context.Context, f model.FileFolder) error
	Find(ctx context.Context, id string) (*model.FileFolder, error)
	List(ctx context.Context, owner string) ([]model.FileFolder, error)
	Owners(ctx context.Context) ([]string, error)
	Placements(ctx context.Context, owner string) (map[string]string, error)
	Place(ctx context.Context, owner, fileID, folderID, by string) error
}

// 15. [TENANT] 可复用技能库与绑定 Store（skill + skill_binding）
type SkillStore interface {
	List(ctx context.Context) ([]model.Skill, error)
	Find(ctx context.Context, id string) (*model.Skill, error)
	Save(ctx context.Context, s model.Skill) error
	Binding(ctx context.Context, ownerType, ownerID string) ([]string, bool, error) // bool=false 无记录，与空切片严格区分
	Bind(ctx context.Context, ownerType, ownerID string, skillIDs []string) error
}

// 16. [TENANT] 技能删除确认卡 Store（skill_deletion）
type SkillDeletionStore interface {
	Save(ctx context.Context, d model.SkillDeletion) error
	Find(ctx context.Context, id string) (*model.SkillDeletion, error)
	ForAgent(ctx context.Context, agentID string) ([]model.SkillDeletion, error)
}

// ==================== 分组 D：成本路由审计、五维用量与耗时 (cost_store.go · 3 Store) ====================

// 17. [TENANT] 任务模型路由决策不可变审计 Store（task_model_choice）
type TaskModelChoiceStore interface {
	Save(ctx context.Context, c model.TaskModelChoice) error
	List(ctx context.Context, taskID string) ([]model.TaskModelChoice, error)
}

// 18. [TENANT] Token 五维用量与费用账单 Store（token_usage）
type UsageStore interface {
	Save(ctx context.Context, r model.UsageRecord) error
	ForTask(ctx context.Context, taskID string) ([]model.UsageRecord, error)
	Between(ctx context.Context, userID string, from, to time.Time) ([]model.UsageRecord, error) // 左闭右开 [from, to)
}

// 19. [TENANT] 回合 7 阶段耗时与工具耗时 Store（turn_timing）
type TurnTimingStore interface {
	Save(ctx context.Context, t model.TurnTiming) error
	ForSessions(ctx context.Context, sessionIDs []string) ([]model.TurnTiming, error)
}
```

#### C. `internal/core/spi`（平台、供应商、资产与成本侧 9 组 SPI）

```go
package spi

import (
	"context"
	"io"
	"time"

	"github.com/Karlsk/synapse-os/internal/core/model"
	"github.com/shopspring/decimal"
)

// ==================== 1. [二期预留 #1] 可插拔认证器 SPI (auth.go) ====================

type AuthPrincipal struct {
	UserID             string
	CompanyID          string
	Username           string
	DisplayName        string
	Roles              []string // 始终含 "USER"，管理员含 "ADMIN"
	MustChangePassword bool
}

type Credentials struct {
	Kind     string // "local" | "oidc" | "ldap"
	Org      string
	Username string
	Password string
	Token    string // OIDC ID Token / SSO 扩展位
}

// Authenticator V1: LocalAuthenticator；V2: OIDCAuthenticator / LDAPAuthenticator
type Authenticator interface {
	Kind() string
	Authenticate(ctx context.Context, cred Credentials) (*AuthPrincipal, error)
}

// ==================== 2. [二期预留 #2 & 商业化] 离职交接与 License 配额钩子 (platform.go) ====================

type OwnerTransferSummary struct {
	AgentsTransferred      int
	TasksTransferred       int
	AutomationsTransferred int
}

// OwnerTransferHook V1 默认 NoopOwnerTransferHook；V2 接入完整离职资产交接
type OwnerTransferHook interface {
	TransferOwnership(ctx context.Context, fromUserID, toUserID string) (*OwnerTransferSummary, error)
	OnUserDisabled(ctx context.Context, userID string) error
	OnUserDeleted(ctx context.Context, userID string) error
}

// LicenseQuotaHook 商业私有化 License 席位/租户上限预留钩子（V1 默认 NoopLicenseQuotaHook）
type LicenseQuotaHook interface {
	CheckCanProvisionOrg(ctx context.Context, currentOrgs int) error
	CheckCanAddUser(ctx context.Context, companyID string, companyUsers int, totalUsers int) error
}

// ==================== 3. [二期预留 #3] 模型网关与模型轻调用 SPI (provider.go) ====================

type ResolvedEndpoint struct {
	CredentialID string
	BaseURL      string
	APIKey       string
	Bearer       bool
	ProxyMode    bool // V1: false；V2 集中式三协议代理网关开启后 true
}

func (e *ResolvedEndpoint) ToRunCredential() *model.RunCredential {
	if e == nil {
		return nil
	}
	return &model.RunCredential{
		ID:      e.CredentialID,
		BaseURL: e.BaseURL,
		APIKey:  e.APIKey,
		Bearer:  e.Bearer,
	}
}

// ModelGateway V1: DirectCredentialGateway；V2: ProxyModelGateway（真实 Key 留控制面，沙箱只拿短期代理令牌）
type ModelGateway interface {
	Resolve(ctx context.Context, providerKey, excludeCredID string) (*ResolvedEndpoint, error)
	ReportEndpointFailure(ctx context.Context, providerKey string, ep *ResolvedEndpoint, reason string) (*ResolvedEndpoint, error)
}

// ModelCaller 单轮无工具轻量模型问答接口（Java: ModelCall.java:L29-L103；供成本裁判/记忆提炼/文件与任务打标使用）
type ModelCaller interface {
	Ask(ctx context.Context, modelID, prompt string, timeout time.Duration, uctx *model.UsageContext) (string, error)
}

// ModelChecker 校验模型是否已通过推理探活的窄接口（解耦 settings 与 provider 同层依赖）
type ModelChecker interface {
	HasRunnableModel(ctx context.Context, modelID string) bool
}

// ==================== 4. 知识资产侧 SPI：文件存储与项目可见性 (assets.go) ====================

// FileStore 字节级对象存储后端（Java: FileStore.java:L13-L28；V1 补齐 OpenRange 支撑 S3 视频拖拽）
type FileStore interface {
	Put(ctx context.Context, id, sourcePath, contentType string) error
	Open(ctx context.Context, id string) (io.ReadCloser, error)
	OpenRange(ctx context.Context, id string, start, end int64) (io.ReadCloser, int64, error) // end < 0 表示至文件末尾
	Delete(ctx context.Context, id string) error
	LocalPath(ctx context.Context, id string) (string, bool)
}

// FileStorage 为 FileStore 的标准语义别名，兼容不同模块命名习惯且编译期完全等价
type FileStorage = FileStore

type FileStoreField struct {
	Key         string
	Label       string
	Secret      bool
	Required    bool
	Placeholder string
	Locates     bool // true=定位字段（修改需建新渠道保旧文件可读）；false=凭据/选项（原地轮换）
}

// FileStoreProvider 可插拔存储后端工厂（V1: local / s3；Java: FileStoreProvider.java:L10-L31）
type FileStoreProvider interface {
	Type() string
	Label() string
	Fields() []FileStoreField
	Open(ctx context.Context, cfg map[string]string) (FileStore, error)
}

// ProjectAccess 跨模块项目权限与归属 SPI（Java: ProjectAccess.java:L11-L36）
type ProjectAccess interface {
	AgentsThroughProjects(ctx context.Context, userID string) ([]string, error)
	SharesProject(ctx context.Context, agentID, userID string) (bool, error)
	ProjectOfAgent(ctx context.Context, agentID string) (string, bool, error)
	Archived(ctx context.Context, agentID string) (bool, error)
	VisibleThroughProject(ctx context.Context, kind model.ProjectItemKind, itemID, userID string) (bool, error)
	ItemsInProject(ctx context.Context, projectID string, kind model.ProjectItemKind, userID string) ([]string, error)
	RecordTask(ctx context.Context, taskID, agentID string) error
	RecordFile(ctx context.Context, fileID, agentID, taskID string) error
}

// ==================== 5. 成本路由与五维计量 SPI (cost.go) ====================

type ModelChoice struct {
	Model string
	CLI   string
}

// ModelRouter 对话选型、最便宜模型与冷却回报（Java: ModelRouter.java:L11-L30）
type ModelRouter interface {
	Cheapest(ctx context.Context) (*ModelChoice, error)
	ChatModels(ctx context.Context) ([]string, error)
	ReportFailure(ctx context.Context, modelID, errMsg string)
}

// UsageRecorder Token 五维计费落库端口（Java: UsageRecorder.java:L12-L16；TapUsage 位于 SA-06 runtime SPI）
type UsageRecorder interface {
	Record(ctx context.Context, uctx model.UsageContext, tokensJSON string) (*decimal.Decimal, error)
}
```

#### D. `internal/core/eventbus/events_platform.go`（4 类平台与成本领域事件）

```go
package eventbus

// CompanyStarted 每家启用的企业在系统就绪或新开户后发布一次（Java: CompanyStarted.java:L8-L9）
type CompanyStarted struct {
	CompanyID string
}

// IntegrationChanged 管理员保存企业集成（feishu/dingtalk）配置后发布（Java: IntegrationChanged.java:L4-L5）
type IntegrationChanged struct {
	Key string // feishu | dingtalk
}

// DefaultModelChanged 默认模型故障切换或恢复时面向管理员发布（Java: NoticeEvents.java:L42-L43）
type DefaultModelChanged struct {
	DefaultModel string
	InUse        string
	Down         bool
	Reason       string
}

// NoticeIgnored 员工忽略首页待办通知时发布，驱动对应模块联动（Java: NoticeEvents.java:L46-L47）
type NoticeIgnored struct {
	Owner    string
	LinkType string
	LinkID   string
}
```

### 4.2 路由契约

本篇为 L1 纯领域契约层，**不注册任何 HTTP / Gin 路由**。
下游 Web 层（`SA-39`）在暴露对应 REST 端点时须遵循以下实体到路由域的契约边界：
- 平台与租户：`Company` / `UserSession` / `User` / `Org` / `SystemSetting` / `Notice` → `/api/v1/{auth,account,users,directory/users,admin/org,settings,branding/logo,notices}`（**明确不再开放 `POST /api/v1/orgs` 公网自注册**，`GET /api/v1/orgs/registration` 恒返回 `{"open":false}`）；
- 供应商与连接器：`ModelProvider` / `ProviderCredential` / `Connector` / `ConnectorCredential` → `/api/v1/{providers,admin/integrations,connectors,oauth}`；
- 知识资产与成本：`Project` / `WorkFile` / `FileFolder` / `Skill` / `UsageRecord` / `TurnTiming` → `/api/v1/{projects,files,files/folders,admin/file-storage,skills,usage,admin/usage,admin/model-prices,admin/analytics}`。

### 4.3 错误语义

本篇所有纯领域校验工具与 SPI 契约统一返回 `L0/spec_01` 冻结的 `internal/core/apperror` 错误类型：

| 场景 | 返回错误 | 映射 HTTP 状态码 | 行为约束 |
|---|---|---|---|
| `NormalizeTeamPath` 空路径、超 8 层、单段超 64 字符、以 `.` 开头、含 `›` / `\` / 控制字符 | `apperror.InvalidArg(...)` | `400 invalid_argument` | 拒绝入库与展示，文案明确指出非法段名或路径 |
| `Store` 单条查询（`Find*`/`Manager`/`ProjectOf`/`Binding`）未命中记录 | `(nil, nil)` 或 `("", false, nil)` | 由 L2/L3 决定是否转 `404` | **严禁**向下游暴露 `pgx.ErrNoRows` 或 `sql.ErrNoRows` |
| `Store` 唯一约束碰撞（如同租户重名 `app_user.username`、重 `provider_key`） | `apperror.Conflict(...)` | `409 conflict` | 绝不静默覆盖既有实体 |
| `Store` 租户方法缺失 `companyID` | `tenant.ErrMissingTenant` | `500 internal_error` | Fail-Closed 立即拒止并记 `ERROR` 日志 |
| `Authenticator.Authenticate` 认证失败 / 账号禁用 | `apperror.Unauthorized(...)` | `401 unauthorized` | 不暴露账号是否存在细节 |
| `LicenseQuotaHook` 触发商业席位/租户数超限 | `apperror.Forbidden(...)` | `403 forbidden` | 阻断新增租户或新增用户，提示联系管理员扩容 |
| `ModelGateway.Resolve` 当前租户无可用 API Key | `apperror.Forbidden(...)` | `403 forbidden` | **严禁**回退宿主机环境变量 Key |

### 4.4 SQL 与接口边界契约

本篇不含 SQL 语句，但通过 `internal/core/store` 接口签名与 `L0/spec_04` `internal/arch_test.go` 冻结四条存储铁律：
1. **事务隐式透传铁律**：`store.TxManager`（`WithinTx` + `WithinReadOnlyTx`）与 `L0/spec_02` `db.TxManager` 保持 100% 方法签名一致，事务句柄隐藏于 `ctx`，业务层零感知 PG 事务类型；
2. **`SkillStore.Binding` 三态区分铁律**：无绑定记录必须返回 `(nil, false, nil)`，显式绑定为空列表必须返回 `([]string{}, true, nil)`，支撑「默认继承全部可见技能 vs 显式不绑定任何技能」的语义区分；
3. **`ProviderCredentialStore` 软删去重铁律**：`Delete` 仅软删除，保留 `key_hash`；`KnownKeyHashes` 必须包含已软删行，防止后台探活反复将管理员已删除的宿主机密钥重新导入；
4. **`UsageStore.Between` 左闭右开区间铁律**：严格采用 `[from, to)` 语义（`at >= from AND at < to`），`userID == ""` 时统计全租户，非空时仅统计单员工，保证按日/按月账单边界零重叠零漏算。

---

## 5. 行为语义与核心算法伪代码

### 5.1 `TeamPaths` 路径规范化与上下级判定 (`internal/core/model/platform.go`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/org/TeamPaths.java:L7-L59`。

```go
package model

import (
	"strings"
	"unicode/utf8"

	"github.com/Karlsk/synapse-os/internal/core/apperror"
)

func NormalizeTeamPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", apperror.InvalidArg("Team path is empty")
	}
	parts := strings.Split(raw, "/")
	segments := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue // 容忍 "a//b" 及首尾 "/"
		}
		if utf8.RuneCountInString(s) > MaxTeamSegmentLen ||
			strings.HasPrefix(s, ".") ||
			strings.ContainsRune(s, '›') ||
			hasInvalidTeamRune(s) {
			return "", apperror.InvalidArg("Invalid team name: " + s)
		}
		segments = append(segments, s)
	}
	if len(segments) == 0 || len(segments) > MaxTeamDepth {
		return "", apperror.InvalidArg("Invalid team path: " + raw)
	}
	return strings.Join(segments, "/"), nil
}

func hasInvalidTeamRune(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return true
		}
	}
	return false
}

// TeamAncestors 返回团队自身及全部祖先："a/b/c" -> ["a/b/c", "a/b", "a"]
func TeamAncestors(team string) []string {
	var out []string
	cur := team
	for cur != "" {
		out = append(out, cur)
		idx := strings.LastIndexByte(cur, '/')
		if idx < 0 {
			break
		}
		cur = cur[:idx]
	}
	return out
}

func TeamWithin(team, root string) bool {
	return team == root || strings.HasPrefix(team, root+"/")
}
```

### 5.2 `WorkFileKindOf` 与 `CleanWorkFileTags` 中文安全截断 (`internal/core/model/assets.go`)

> **Java 源码对照**：`superagent-core/src/main/java/com/superagent/core/file/WorkFileKind.java:L9-L27`、`WorkFile.java:L51-L66`。
> **V1 增强**：Go `CleanWorkFileTags` 按 `[]rune` 长度截断（`MaxWorkFileTagLen = 20`），避免按 UTF-8 字节截断导致中文标签尾部乱码。

```go
var tagSplitRe = regexp.MustCompile(`[\s,，;；]+`)

func WorkFileKindOf(filename string) WorkFileKind {
	n := strings.ToLower(strings.TrimSpace(filename))
	idx := strings.LastIndexByte(n, '.')
	if idx < 0 || idx == len(n)-1 {
		return WorkFileOther
	}
	switch n[idx+1:] {
	case "md", "markdown", "txt", "pdf", "doc", "docx", "ppt", "pptx", "xls", "xlsx", "csv", "json", "html", "htm":
		return WorkFileDocument
	case "png", "jpg", "jpeg", "gif", "webp", "svg", "bmp":
		return WorkFileImage
	case "mp4", "webm", "mov", "m4v":
		return WorkFileVideo
	default:
		return WorkFileOther
	}
}

func ParseWorkFileKind(raw string) WorkFileKind {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(WorkFileDocument):
		return WorkFileDocument
	case string(WorkFileImage):
		return WorkFileImage
	case string(WorkFileVideo):
		return WorkFileVideo
	case string(WorkFileLink):
		return WorkFileLink
	default:
		return WorkFileOther
	}
}

func CleanWorkFileTags(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, MaxWorkFileTags)
	out := make([]string, 0, MaxWorkFileTags)
	for _, t := range raw {
		c := strings.ReplaceAll(t, "#", "")
		c = strings.TrimSpace(tagSplitRe.ReplaceAllString(c, " "))
		if runes := []rune(c); len(runes) > MaxWorkFileTagLen {
			c = strings.TrimSpace(string(runes[:MaxWorkFileTagLen]))
		}
		if c == "" {
			continue
		}
		key := strings.ToLower(c)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, c)
		if len(out) == MaxWorkFileTags {
			break
		}
	}
	return out
}
```

### 5.3 成本值对象混合单价与金额零误差计算 (`internal/core/model/cost.go`)

- `ModelPriceFact.Blended()` 严格使用 `decimal` 计算 `(3*Input + Output) / 4`（`Input.Mul(decimal.NewFromInt(3)).Add(Output).Div(decimal.NewFromInt(4))`），避免 `float64` 在低价模型排序比较中的精度抖动；
- `ModelCandidate.Blended()` 在 `!c.Priced()`（`Input == nil || Output == nil`）时返回 `(decimal.Zero, false)`，调用方按未定价排至候选队列末尾；
- `UsageRecord.Total()` 返回 `Input + CacheRead + CacheWrite + Output`（严格对齐 `UsageRecord.java:L14`，`Reasoning` 与 `CacheWrite1h` 属于对应主桶细分观测值，避免重复累加）；
- `TaskModelChoice.Refused()` 仅当 `By == ChoiceByFixed && strings.TrimSpace(Model) == ""` 时返回 `true`，标识管理员锁定固定模型列表且全部候选失败，任务必须明确报错终止而不得静默回退到未授权模型。

### 5.4 `RunCredential` 与 `ResolvedEndpoint` 脱敏不变量

- `RunCredential.String()` 与 `GoString()` 均强制输出 `apiKey=<redacted>`，防止 `%v`/`%+v`/`slog` 意外泄漏租户明文 API Key；
- `ResolvedEndpoint.ToRunCredential()` 为 `nil` 安全转换：当上游返回 `nil` 时返回 `nil` 而不 panic；二期切换到 `ProxyModelGateway`（`ProxyMode = true`）时，`APIKey` 字段承载的是单回合短期代理令牌而非真实厂商 Key，上层执行器零改动。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：确认 `synapse-server/go.mod` 已引入 `github.com/shopspring/decimal`，建立 `internal/core/model`、`internal/core/store`、`internal/core/spi` 目录骨架。
- [ ] **T2**：**测试先行**编写 `internal/core/model/platform_test.go`（覆盖 `User.IsAdmin`、`TeamPaths` 边界、`QuotaOverrides`/`UserSession`）→ 实现 `internal/core/model/platform.go`。
- [ ] **T3**：**测试先行**编写 `internal/core/model/provider_test.go`（覆盖 `RunCredential.String()` 脱敏、`ProviderCatalogEntry.Installable()`、连接器与凭据常量）→ 实现 `internal/core/model/provider.go`。
- [ ] **T4**：**测试先行**编写 `internal/core/model/assets_test.go`（覆盖 `WorkFileKindOf` 全扩展名、`ParseWorkFileKind` 回落、`CleanWorkFileTags` 中文 rune 截断/去重/上限 5 个、`WorkFile.With*` 不可变更新）→ 实现 `internal/core/model/assets.go`。
- [ ] **T5**：**测试先行**编写 `internal/core/model/cost_test.go`（覆盖 `ModelPriceFact.Blended` 精确十进制、`ModelCandidate.Priced/Blended`、`TaskModelChoice.Refused`、`UsageRecord.Total`、`TurnTiming.TotalMs`）→ 实现 `internal/core/model/cost.go`。
- [ ] **T6**：落地 `internal/core/store/{tx,platform_store,provider_store,assets_store,cost_store}.go`（`TxManager` + 19 个持久化 Store 接口），核对每一方法签名与 `SA-07`/`SA-08`/`SA-10` 完全一致。
- [ ] **T7**：落地 `internal/core/spi/{auth,platform,provider,assets,cost}.go` 与 `internal/core/eventbus/events_platform.go`，编写 `internal/core/spi/platform_assets_test.go` 验证接口契约、类型别名 `FileStorage = FileStore`、`ResolvedEndpoint.ToRunCredential()` 与事件在 `SA-04` `eventbus.Bus` 上的发布/订阅往返。
- [ ] **T8**：全量执行 `cd synapse-server && go build ./... && go test -race -cover ./internal/core/... ./internal`（含 `internal/arch_test.go` 分层与零 Redis 守卫）。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` 实施看板：合入后将 `SA-05` 状态更新为 `已实施（commit <hash>）`；
- [ ] `docs/SuperAgent-Go/01_Go工程架构_目录结构与核心底座设计.md` §1 目录树：确认包路径标注与 `internal/core/model` 保持一致；
- [ ] `synapse-server/README.md`：更新 `internal/core/{model,store,spi}` 包地图，注明 `SA-05`（平台/供应商/资产/成本）与 `SA-06`（运行/Agent/任务/事件/编排）的契约分工。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | `TeamPaths` 合法规范化与非法拒止 | `internal/core/model/platform_test.go` | `" /研发//平台部/ "` → `"研发/平台部"`；空串、9 层嵌套、65 字符段、`.secret`、`a›b`、`a\b`、含 `\x00`/`\n` 均返 `apperror.CodeInvalidArgument`；`TeamAncestors("a/b/c")` 返回 `["a/b/c","a/b","a"]`；`TeamWithin("a/b","a") == true` 且 `TeamWithin("ab","a") == false` |
| 2 | `User.IsAdmin` 与 `UserSession` / `Notice` 枚举常量 | `internal/core/model/platform_test.go` | `Roles: ["USER","ADMIN"]` 返 `true`，`["USER"]` 返 `false`；`NoticeKind`/`NoticeState`/`UserSource` 常量值与 PG DDL 字符串 1:1 匹配 |
| 3 | `RunCredential` 强制脱敏与供应商目录值对象 | `internal/core/model/provider_test.go` | `fmt.Sprintf("%v", cred)` 与 `cred.String()` 绝不包含 `APIKey` 子串；`Installable()` 在 `InstallCommand == ""` 时为 `false`；集成常量仅含 `feishu`/`dingtalk` |
| 4 | `WorkFileKindOf`、`ParseWorkFileKind` 与 `CleanWorkFileTags` | `internal/core/model/assets_test.go` | `.md`/`.pdf`/`.xlsx` → `document`、`.png`/`.webp` → `image`、`.mp4` → `video`、未知扩展名与无扩展名 → `other`；`CleanWorkFileTags` 去 `#`、压缩中英文逗号分号空白、按 20 rune 截断中文不乱码、忽略大小写去重、严格截断至 5 个标签 |
| 5 | 成本定价精确十进制与 `TaskModelChoice.Refused` | `internal/core/model/cost_test.go` | `ModelPriceFact{Input: 3.00, Output: 15.00}.Blended()` 精确等于 `6.00`；未定价 `ModelCandidate.Blended()` 返回 `(0, false)`；`TaskModelChoice{By: ChoiceByFixed, Model: ""}.Refused() == true` 而 `By: ChoiceByDefault` 返 `false`；`UsageRecord.Total()` 精确等于 `Input+CacheRead+CacheWrite+Output`；`TurnTiming.TotalMs()` 与毫秒差一致 |
| 6 | SPI 接口契约与 `FileStorage` 等价别名 | `internal/core/spi/platform_assets_test.go` | `ResolvedEndpoint.ToRunCredential()` 处理 `nil` 不 panic 且字段无损拷贝；编译期验证 `FileStorage` 与 `FileStore` 可互换赋值；验证 `CompanyStarted`/`IntegrationChanged`/`DefaultModelChanged`/`NoticeIgnored` 在 `eventbus.Bus` 同步/异步发布无损送达 |
| 7 | 编译期架构守卫与零废弃字段/零 Redis 核验 | `internal/arch_test.go` | `internal/core/{model,store,spi}` 零引入 `pgx`/`database/sql`/`gin`/`os/exec`/Redis；结构体反射或编译断言确认无任何废弃 `Tier`/`Character` 字段 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race -cover ./internal/core/... ./internal` 全绿，核心包测试行覆盖率 ≥ 85%
- [ ] §2 交付物表 8 项逐一存在且落位与包名精确匹配
- [ ] §4.1 冻结的 **19 个持久化 Store 接口 + `TxManager`** 与 **9 组 SPI**（含 `Authenticator`、`OwnerTransferHook`、`LicenseQuotaHook`、`ModelGateway`、`ModelCaller`、`ModelChecker`、`FileStore`/`FileStorage` + `FileStoreProvider`、`ProjectAccess`、`ModelRouter`、`UsageRecorder`）签名逐字无漂移
- [ ] `internal/arch_test.go` 全绿：`internal/core/{model,store,spi}` 零上层反向依赖、零数据库驱动、零 Redis 依赖
- [ ] 核对本篇所有模型结构体零残留废弃字段、零公网自注册常量、零 GitHub/Gitee/Notion/Confluence 集成常量
- [ ] 与 `L1/spec_07`、`L1/spec_08` 的 Store 接口方法签名逐一核对 100% 匹配，为后续 `SA-06`/`SA-07`/`SA-08` 零阻塞合入

---

## 10. 可提交节点

- 建议 commit 1（领域模型与纯工具）：`feat(core): add platform, provider, assets and cost domain models and helpers (spec 05, SA-05)`
- 建议 commit 2（Store/SPI 契约与平台事件）：`feat(core): freeze TxManager, 19 platform/assets/cost Store interfaces, Phase-2 SPIs and platform events (spec 05, SA-05)`
