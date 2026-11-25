# Spec-Kit 执行指导：从 `docs/SuperAgent-Go/DDD/` PR Spec 到 Go 代码

`docs/SuperAgent-Go/DDD/L0–L6/` 下的 **42 篇 PR 级 Spec（`SA-01` ~ `SA-42`）** 具有双重身份：既是冻结的**工程契约文档**（导出签名 / PG17 DDL / 路由 / 错误语义 / 验收门），也是 Spec-Kit 的**开发输入原料**。本手册说明如何把一篇 PR Spec 稳定转化为 `specs/` 下的可执行 Feature 与高质量 Go 1.26 代码——全部 42 篇均可按同一套流程循环推进。

> **本流程已固化为可执行 Skill**：日常直接调用 `/spec-dev <Spec 文档路径>`（见 [.agents/skills/spec-dev/SKILL.md](../.agents/skills/spec-dev/SKILL.md)），它会按本文门禁自动编排前置核查与 Spec-Kit 各阶段、在 `tasks` 产出后停等人工确认。本文档保留作原理说明与手动逐命令兜底参考。

---

## 一、总原则：一次只喂一篇 PR Spec

每篇 Spec 的「`§2 本篇交付物表`」就是一个独立 Feature / PR 的硬边界。**篇号与 `SA-NN` 编号遵循分层 DAG 实施顺序**（见 [docs/SuperAgent-Go/DDD/README.md](./SuperAgent-Go/DDD/README.md) §2–§3：严格从 `L0 → L1 → L2 → L3 → L4 → L5 → L6` 推进），这是拆成 42 篇单人可审 PR 的核心目的。

两层输入严格分离，不要混喂：

- **`/speckit-specify` 只喂 WHAT / WHY**——业务或工程背景、用户/调用方场景、可测试的功能需求、明确边界、验收标准与前置依赖，不带具体 Go 签名与 SQL DDL；
- **`/speckit-plan` 喂 HOW**——锁定技术栈、包落位映射（`§2`）、数据库设计（`§3`）、冻结契约（`§4` 的导出符号 / 路由 / 错误语义 / SQL）、测试策略（`§8`）。

`synapse-os` 的 PR Spec 10 节结构天然匹配这一分工：

| Spec 章节 | 对应 Spec-Kit 阶段 | 用途 |
|---|---|---|
| `§1 背景与范围`、`§5 行为语义` | `/speckit-specify` | 提炼需求、用户场景、功能需求与本篇边界 |
| `§2 本篇交付物表` | `/speckit-plan`、`/speckit-tasks` | 模块落位映射与任务完整性对账清单 |
| `§3 数据库设计`、`§4 冻结契约` | `/speckit-plan` | 逐字冻结签名、DDL、路由与错误语义 |
| `§6 任务清单（TDD 粒度）` | `/speckit-tasks`、`/speckit-implement` | TDD 红绿拆分与执行顺序参考 |
| `§7 文档同步`、`§8 测试清单`、`§9 验收门` | `/speckit-implement`、验收报告 | 机器判卷门禁与人工抽查清单 |
| `§10 可提交节点` | 验收通过后由用户触发提交 | 标准 Commit Message 建议 |

全流程两条铁律：
1. **Spec 即契约，歧义必停人工确认**——在任意阶段（输入分流、`/speckit-clarify`、`/speckit-plan`、`/speckit-analyze`、`/speckit-implement`）发现任何需求/契约/实现歧义、Spec 内部矛盾、Spec 与 `AGENTS.md` / `constitution.md` 冲突、或 Spec 与代码现状冲突时，**必须立即暂停并请求人工确认，严禁自行猜测、脑补答案或偷偷改 Spec 凑通过**。
2. **严禁自动 `git add` / `git commit` / `git push`**——全程只修改工作区文件，绝不自动暂存、提交或推送，亦不触发任何 `/speckit-git-commit` 自动提交钩子；提交时机完全由用户在验收后手动或显式指令触发。

---

## 二、前提确认与事实源优先级

开工前只需确认以下前提（已在仓库就绪，无需重复初始化）：

1. **项目宪章已就绪**：[.specify/memory/constitution.md](../.specify/memory/constitution.md)（v1.0.0，7 条不可妥协核心原则：企业定位与 V1 商业边界 / L0–L6 单向分层与零反射装配 / Fail-Closed 多租户与信封加密 / 基础设施与出站隔离 / PG17 Schema 与向前迁移 / 弹性并发与 30s 优雅停机 / API 契约与 Go 工程规范）。
2. **模板已对齐 Go 工程**：[.specify/templates/plan-template.md](../.specify/templates/plan-template.md) 已内置 7 项 Constitution Check 门禁；[.specify/templates/tasks-template.md](../.specify/templates/tasks-template.md) 已对齐 `synapse-server/internal/...` 路径与 `-race`/`arch_test`/`guard_test` 收尾门禁。
3. **Antigravity 技能已就绪**：[.agents/skills/](../.agents/skills) 已安装 15 个 `speckit-*` 技能及本项目专属的 `spec-dev` 技能。
4. **`specs/` 留痕目录隔离**：`/speckit-specify` 生成的特征目录位于根目录 `specs/<NNN>-<slug>/`，与 `docs/SuperAgent-Go/DDD/` 互不覆盖。
5. **事实源优先级（冲突裁决顺位）**：
   > **`AGENTS.md` & `.specify/memory/constitution.md`（最高硬红线） > `docs/SuperAgent-Go/01~05` & `_epic/` 上位定稿 > `docs/SuperAgent-Go/DDD/L0–L6/spec_NN` 冻结契约 > `specs/` 下 Spec-Kit 生成留痕 > `../orgin/SuperAgent` 原版 Java 参考**
   - `specs/` 是执行留痕，与 PR Spec 出入时以 PR Spec 为准并回修 `specs/` 留痕，不擅改 PR Spec 定稿；
   - `../orgin/SuperAgent` 仅用于核验原版业务细节，所有 V1 商业裁剪与缺陷修复一律以 Go Spec 为准。

---

## 三、完整执行序列（手动兜底流程）

> 推荐使用 `/spec-dev docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md` 一键编排以下全部步骤；如需手动分步运行，按第 0~6 步依次执行。

### 第 0 步：前置核查（开工前三分钟）

1. 核查 `docs/SuperAgent-Go/DDD/README.md` 看板，确认本篇的「前置依赖」PR 均已落地；
2. 在 `synapse-server/` 跑基线检查（首篇 `SA-01` 尚未有 `.go` 文件时确认 `go.mod` 为 `go 1.26.1` 即可）：

```bash
cd synapse-server
gofmt -l .
go vet ./...
go build ./... && go test -race ./...
```

基线不绿或前置交付物缺失，先报告处理，不在脏基线上开工。

### 第 1 步：`/speckit-specify`（生成规格）

将按第四章骨架提炼的需求陈述整段作为参数（`before_specify` hook 会自动建立 feature 分支）：

```text
/speckit-specify <粘贴按第四章骨架提炼的 WHAT/WHY 需求全文>
```

### 第 2 步：`/speckit-clarify`（必跑 · 歧义必停）

```text
/speckit-clarify
```

挑出规格里的歧义点（最多 3~5 个）。仅当本篇 Spec、上位架构文档、`AGENTS.md` 与 `constitution.md` 有唯一无歧义的原文规定时才引用作答；V1 已拍板边界不重复发问；**凡存在任何未写明细节、多种理解或与代码现状偏差，必须立即暂停由人工拍板后再继续**。

### 第 3 步：`/speckit-plan`（技术方案）

```text
/speckit-plan <按第四章「plan 参数骨架」组装：固定技术栈句 + 模块落位句 + 冻结契约句 + 测试策略句>
```

确认生成的 `plan.md` 中 `## Constitution Check` 7 项门禁逐条通过。

### 第 4 步：`/speckit-tasks`（拆任务 + 人工确认停点）

```text
/speckit-tasks
```

产出 `tasks.md` 后**必须停下来做一次人工对账**：
- 逐项核对 `tasks.md` 是否完整覆盖 Spec `§2 交付物表`（不漏项、不私增导出概念）；
- 核对任务顺序是否符合 Spec `§6 任务清单` 的 TDD 粒度；
- 核对 Spec `§4` 与 `AGENTS.md` 的硬红线（如分层、租户校验、流式 `Timeout=0`、日志脱敏）是否体现为任务约束。

### 第 5 步：`/speckit-analyze`（必跑，一致性检查）

```text
/speckit-analyze
```

交叉核对 `spec.md` / `plan.md` / `tasks.md` 与 `.specify/memory/constitution.md` 有无违背或遗漏（L0–L6 分层、Fail-Closed 多租户、基础设施隔离、PG17 规范、API 6 类例外、零 Redis）。

### 第 6 步：`/speckit-implement`（逐任务 TDD 执行与验收）

```text
/speckit-implement
```

执行期遵守三条铁律：
1. **逐任务 TDD**：按 `RED`（依 `§4` 写单测先红）→ `GREEN`（最小实现）→ `REFACTOR` 推进，每完成一个任务立刻在 `synapse-server/` 跑 `go build ./... && go test -race -count=1 ./<当前包>/...`；
2. **硬门禁全绿**：全部任务完成后，必须通过 `gofmt -l .`（零输出）、`go vet ./...`、`go build ./...`、`go test -race ./...`（含 `arch_test.go` 与 `guard_test.go`）及 `§9` 覆盖率要求；涉及 DB/Store 时若配置了 `TEST_POSTGRES_DSN` 必跑 PG17 集成测试；
3. **范围、歧义与 Git 纪律**：只做本篇交付物清单内的事，不顺手重构无关模块；实现期遇到任何歧义立即暂停人工确认；**严禁自动执行 `git add` / `git commit` / `git push`**，验收报告确认后再由用户显式决定按 `§10 可提交节点` 提交。

---

## 四、输入模板示例（以首篇 `SA-01` / `spec_01` 为例）

> 跑 `/speckit-specify` 时粘贴「spec 需求」整段（只含 WHAT/WHY）；冻结签名与落位留给 `/speckit-plan`。

### 4.1 `/speckit-specify` 输入示例（`SA-01`）

```text
SA-01（spec 01）需求：L0 基础底座——配置管理、可观测性、领域错误体系与生命周期治理

背景与价值：synapse-os 是面向企业私有化联网部署的 Go 1.26 单二进制 AI 协作工作系统（单库 PG17，零 Redis/ClickHouse）。本篇作为全仓首篇 L0 无业务前置依赖底座，为后续 SA-02 ~ SA-42 提供四大统一基础能力：兼容 /api/v1 契约的统一领域错误语义、支持企业代理与自签 CA 的强校验配置加载器、上下文透传且自动脱敏的可观测性（日志与独立 Prometheus 指标），以及带 panic 隔离与 30s 逆序优雅停机的后台协程治理。

用户/调用方场景：
1. 下游业务模块或 L5 Web 网关抛出/捕获领域错误时，可统一归一化为机器可读错误码与 HTTP 状态码，未知内部错误自动脱敏不泄漏密码或堆栈；
2. 私有化运维人员通过 YAML 文件、环境变量（含原版 SERVER_PORT / SECRET_KEY 兼容）与企业代理/自签根证书配置启动服务；拼写错误或非法参数在启动前一次性聚合 fail-fast 报错，本地回环地址（127.0.0.1/localhost）强制绕过代理以保证内置 /mcp 自检可用；
3. 研发与运维排障时，结构化日志自动附带 request_id / company_id / user_id，所有密码/Token/Authorization/DSN 密码段自动脱敏为 [REDACTED]，且独立 Prometheus Registry 暴露核心业务指标不污染全局注册表；
4. 服务运行期间后台循环发生 panic 时自动捕获记录堆栈不拖垮主进程；收到 SIGINT/SIGTERM 时按严格分阶段逆序在 30 秒总预算内完成停机收敛。

功能需求：
- FR1：统一领域错误码（13 个标准 Code）、AppError 错误链（Unwrap/Is）、EgressDenied 标识、UnreadableMaterialsError 格式化与 FromError 脱敏归一化；
- FR2：YAML（严格未知字段报错）+ 环境变量覆盖 + 默认值配置树、启动前全量聚合校验、企业代理（保底本地 NO_PROXY）与自定义私有 CA 证书池构建，并交付完整注释的 configs/config.example.yaml；
- FR3：入站 X-Request-Id 清洗防日志注入、上下文三元组透传 slog ContextHandler、嵌套字段与 DSN 敏感值递归脱敏 RedactingHandler、独立 Prometheus Registry 与 6 项核心业务指标；
- FR4：BackgroundGroup 协程托管（防停后提交、panic recover、超时列出卡住任务名）与 ShutdownManager 30 秒分阶段 LIFO 逆序停机编排。

明确不做（边界）：严禁引入任何 Redis 配置或分布式缓存抽象；不实现 PostgreSQL 连接池与迁移（SA-02）、不实现出站 HTTP 客户端与 EgressGuard（SA-03）、不实现多租户 Fail-Closed TenantContext 与信封加密（SA-04）、不实现 CLI 入口 main.go 与 Actuator HTTP 路由（SA-41/SA-42）。

验收标准：以 docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md §8/§9 为准：synapse-server 下 gofmt/go vet/go build/go test -race 全绿，核心包覆盖率 ≥ 85%，configs/config.example.yaml 经单测加载校验通过且零 Redis 配置，internal/core/apperror 仅依赖 Go 标准库。

依赖与假设：无业务前置依赖，仅依赖 synapse-server/go.mod（go 1.26.1）；下游为 SA-02 ~ SA-42 全部模块；全部单测纯内存隔离不依赖外部数据库或网络。
```

### 4.2 `/speckit-plan` 参数骨架（组装时替换尖括号内容）

```text
技术栈：Go 1.26.1（synapse-server/，module github.com/Karlsk/synapse-os，Go Modules，CGO_ENABLED=0 单静态二进制）+ Gin + PostgreSQL 17（jackc/pgx/v5 pgxpool + pressly/goose/v3 //go:embed 向前迁移，严禁 DROP TABLE 与重 ORM）+ gopkg.in/yaml.v3（KnownFields(true)）+ sony/gobreaker/v2 + robfig/cron/v3 + 标准库 log/slog、crypto/aes、embed.FS + prometheus/client_golang。零 Redis、零 ClickHouse、零反射 DI；短时缓存/限流走 internal/infra/memcache，跨实例互斥走 PG AdvisoryLock。数据库零物理外键、非全局表 company_id VARCHAR(64) NOT NULL 复合主键首列、TIMESTAMPTZ UTC、NUMERIC(16,6) 定点计费、JSONB、ON CONFLICT 幂等写。

模块落位：<从 Spec §2 交付物表逐行映射「交付物 → synapse-server/... 路径」，标明 L0–L6 层级与依赖红线；例如 SA-01：internal/core/apperror/errors.go（零内部/第三方依赖叶子包）、internal/infra/config/{config,load,validate,network}.go、configs/config.example.yaml、internal/infra/obs/{context,logger,redact,metrics}.go、internal/infra/lifecycle/{group,shutdown}.go>。

冻结契约逐字对齐 <Spec 路径> §4（导出类型/函数签名、路由路径与鉴权、错误码与文案、SQL 形态、JSON camelCase 字段），引用条目用 Spec 编号不自造同义词。

测试策略按 <Spec 路径> §8 执行：<列出测试文件与核心回归测试点>；同包 *_test.go 表驱动测试 + t.Parallel()；外部依赖用 httptest/Mock，PG 集成测试用 db.NewTestSchemaDB 隔离。完成标准为 synapse-server 下 gofmt -l . 零输出、go vet ./...、go build ./...、go test -race ./...（含 arch_test 与 guard_test）全绿，涉及包覆盖率达标（≥ 80%，按 §9 要求执行）。
```

---

## 五、后续 42 篇推进路线与注意事项

1. **按 `DDD/README.md` DAG 顺序逐层推进**：
   - **L0 底座（4 篇）**：`SA-01` → 并行 `SA-02` / `SA-03` / `SA-04`（`SA-04` 落地后全仓激活 `internal/arch_test.go` AST 守卫）
   - **L1 契约·存储·运行时（10 篇）**：`SA-05` → `SA-06` → 存储组 `SA-07 ~ SA-10`（`SA-07` 激活 `guard_test.go` 租户主键守卫）与运行时组 `SA-11 ~ SA-14`
   - **L2 平台与资产（9 篇）**：`SA-15 ~ SA-23`
   - **L3 核心业务域（9 篇）**：`SA-24 ~ SA-32`（`agent` 与 `task` 同层严禁互引）
   - **L4 IM 与编排（5 篇）**：`SA-33 ~ SA-37`
   - **L5 Web 网关与 UI（3 篇）**：`SA-38 ~ SA-40`
   - **L6 装配与商业交付（2 篇）**：`SA-41` → `SA-42`
2. **每完成一篇及时打勾**：合入后同步更新 [docs/SuperAgent-Go/DDD/README.md](./SuperAgent-Go/DDD/README.md) 看板状态，再进入下一篇。
3. **不产码文档不走本流程**：`docs/SuperAgent-Go/01~05*.md`、`docs/SuperAgent-Go/DDD/_epic/*`、`docs/SuperAgent/*`、`docs/Design/*` 均属上位设计或参考文档，只作契约溯源与引用，不直接喂给 `/spec-dev`。
4. **常用快捷指令**：
   ```text
   /spec-dev docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md
   ```
