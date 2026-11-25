---
name: spec-dev
description: >-
  按定稿 PR 级 Spec 驱动开发 synapse-os 的一个 feature：输入 Spec 文档路径
  （docs/SuperAgent-Go/DDD/L0-L6/spec_NN_*.md），自动完成前置核查 →
  /speckit-specify → /speckit-clarify → /speckit-plan → /speckit-tasks（固定停等确认）
  → /speckit-analyze → /speckit-implement → Spec 级验收报告，全程施加硬/软两类门禁，
  保证产出严格贴合 AGENTS.md、constitution.md 与 Spec 冻结契约。
  当用户说「按 spec 开发 / 实施 SA-NN / 实施 spec NN / 用 spec-kit 做某篇 spec」时使用。
compatibility: "Requires synapse-os workspace with .specify/, .agents/skills/speckit-*, AGENTS.md, and docs/SuperAgent-Go/DDD/"
metadata:
  argument-hint: "Spec 文档路径，如：docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md"
---

# 按 Spec 驱动开发 Skill (`synapse-os`)

## User Input

```text
$ARGUMENTS
```

参数是 PR 级 Spec 文档路径（`docs/SuperAgent-Go/DDD/L<0-6>/spec_<NN>_*.md`，对应看板 `SA-01` ~ `SA-42`）。流程原理见 [docs/spec-kit-execution-guide.md](../../docs/spec-kit-execution-guide.md)——本 Skill 是其可执行版本，条款以本文为准执行，无需向用户复读手册原文。

## 总纪律（贯穿全程）

> **能机器判的绝不留给人，机器判不了的绝不自行发挥。**

- **硬门禁**（在 `synapse-server/` 下执行，不过不放行，不许绕）：
  1. `gofmt -l .` 必须零输出；
  2. `go vet ./...` 零告警；
  3. `go build ./...` 全绿；
  4. `go test -race ./...` 全绿（含 `internal/arch_test.go` L0–L6 AST 分层/禁循环/禁越权导入/零 Redis 守卫，以及 `internal/storage/postgres/guard_test.go` 租户 `company_id` 主键守卫——只要对应守卫已落地就必须全绿）；
  5. 本篇涉及包覆盖率不低于改动前基线，目标 ≥ 80%（若 Spec §9 有更高要求如 L0 ≥ 85%，按 §9 执行）：`go test -race -cover ./<涉及包>/...`；
  6. 涉及 `internal/infra/db` 或 `internal/storage/postgres` 迁移/Store 时：核对 `-- +goose Up` 无 `DROP TABLE`、非全局表 `company_id VARCHAR(64) NOT NULL` 位于主键首列、零物理外键；环境存在 `TEST_POSTGRES_DSN` 时必跑 `TEST_POSTGRES_DSN="..." go test -race ./internal/infra/db/... ./internal/storage/postgres/...`（未配置 DSN 时必须在验收报告中显式标明 PG 集成测试为待人工/CI 验证项，不得伪造通过）。
- **软门禁（歧义必停原则）**：遇到下列任一情况或**任何需求、契约、架构、边界、实现上的歧义**，**必须立即暂停、向用户清晰列出歧义点与可选方案、等待人工明确确认后再继续**，严禁自行猜测、自动补全答案或替用户做折中决定：
  1. 任何存在多种合理解释、Spec 未写明细节、或 `/speckit-clarify` / `/speckit-analyze` 检出的未决歧义；
  2. 需要创建 Spec `§2 本篇交付物表` 之外的任何对外概念（导出类型/函数、REST 路由、SQL 表/列、配置项、MCP 工具、Prometheus 指标、CLI 子命令/Flag）；
  3. 需要修改任何 `§4 冻结契约`（导出符号签名、路由契约、错误码/文案、SQL 契约、行为语义）——Spec 修订必须经用户显式批准，严禁通过改 Spec 让实现通过；
  4. Spec 与 `AGENTS.md` / `.specify/memory/constitution.md` 冲突、Spec 内部自相矛盾、或 Spec 与仓库代码现状冲突；
  5. 需要修改前序 PR（`SA-01` ~ `SA-<N-1>`）已交付的导出契约（尤其是 `internal/core/{apperror,egress,tenant,crypto,eventbus,model,store,spi,api}` 与 `internal/infra/*`）；
  6. Spec 要求的第三方依赖不在 `synapse-server/go.mod` 中且不在 `AGENTS.md §2.1` 锁定技术栈清单内（如意外要求引入 Redis / ClickHouse / GORM / 反射 DI 一律停下拦截）；
  7. 需要偏离 L0–L6 单向分层或同层平行模块禁互引规则（L1–L5 任意不同顶层模块间禁止直接 `import`，必须经 L0 `core/spi`/`core/store`/`core/eventbus` 倒置解耦）、V1 商业裁剪边界（如恢复公网自助注册 `POST /api/v1/orgs`、24 性格预设、4 套已裁剪 SaaS 连接器）或 `cmd/synapse-server/wire.go` 零反射显式装配原则。
- **反作弊**：严禁删断言、滥用 `t.Skip`（仅允许 `db.NewTestSchemaDB` 在未设 `TEST_POSTGRES_DSN` 时按规范跳过）、调低覆盖率门槛或绕过 `-race` 让测试变绿。实现错修实现；怀疑 Spec/测试有误时停下报告。未全绿绝不宣称完成。
- **Git 绝对红线（严禁自动操作）**：全程**严禁自动执行 `git add`、`git commit`、`git push`**，亦禁止调用 `/speckit-git-commit` 自动提交钩子；任何暂存、提交、推送操作都必须等待用户显式下达指令。本 Skill 仅在第 7 步验收报告末尾给出建议 commit message。
- **单篇边界**：每次仅处理**一篇** Spec；连续多篇时必须逐篇通过各自的验收门再进入下一篇。

---

## 第 0 步：输入解析与 Spec 分流

按文件路径、目录层级与文档元数据头判型；无法判定直接询问用户，绝不猜测。

| Spec 类型 | 判据 | 处理 |
|---|---|---|
| PR 级规范分篇 | `docs/SuperAgent-Go/DDD/L[0-6]/spec_NN_*.md`，含元数据头 + `§2 交付物表` + `§4 冻结契约` + `§9 验收门`（状态为 `冻结待实施` 或 `draft`） | 走完整流程（第 1~7 步） |
| 归档总设 / 上位架构文档 | `docs/SuperAgent-Go/DDD/_epic/*`、`docs/SuperAgent-Go/01~05*.md`、`docs/SuperAgent/*` | 拒绝并说明：此类文档不直接产码，仅作分篇上位契约；引导改传 `docs/SuperAgent-Go/DDD/L0–L6/` 下对应 PR Spec |
| 已实施 | 状态头或 `DDD/README.md` 看板标为 `已实施` / `已合入` | 停下确认是否重复实施（如为增量改动，确认直接改或另出新 Spec） |
| 暂缓中 | 状态头含 `暂缓` | 停下等待用户明确开工指令 |
| 无效输入 / 未传路径 | 路径不存在或为空 | 读取 `docs/SuperAgent-Go/DDD/README.md` 看板，列出下一批可开工的未完成 PR（如 `SA-01`）请用户确认 |

---

## 第 1 步：开工纪律——必读、基线与依赖检查

1. **读三样**（完整读取，不凭记忆）：
   - 本篇 Spec 全文——重点 `§1 背景与范围`、`§2 交付物表`、`§3 数据库设计`、`§4 冻结契约`、`§5 行为语义`、`§6 任务清单`、`§7 文档同步`、`§8 测试清单`、`§9 验收门`、`§10 可提交节点`；
   - Spec 头「上位契约」列出的对应章节，以及 `AGENTS.md`、`.specify/memory/constitution.md`；
   - Spec 头「前置依赖」的前序 PR 交付物清单及 `docs/SuperAgent-Go/DDD/README.md` 看板状态（若本篇引用了 `../orgin/SuperAgent` Java 行号且语义有细节疑问，按需只读核验原版行为，但一切裁剪与缺陷修复以 Go Spec 为准）。
2. **前置依赖存在性检查**：核实前置 PR 的核心包/文件/导出符号已在 `synapse-server/` 落地（首篇 `SA-01` 无业务前置，仅需确认 `synapse-server/go.mod` 为 `go 1.26.1`）。任何前置缺失 → **停下报告「需先完成 SA-XX / spec NN」**，不得跳篇自造。
3. **构建与测试基线**：在 `synapse-server/` 下运行 `go build ./... && go test -race ./...`（若尚无任何 `.go` 源文件如首篇 `SA-01` 开工前，确认 `go.mod` 合法即可）。基线不绿立即停下报告，不在脏基线上开工。
4. **既有代码模式对齐**：若仓库已有同层或相邻包代码，先快速检索确认错误包装（`apperror`）、上下文传递（`tenant.Require` / `tenant.Detach`）、日志脱敏（`obs`）、Store/Handler 既有风格，确保新代码与仓内风格一致。
5. **分支准备**：`before_specify` hook（`/speckit-git-feature`）会按规范创建 feature 分支；若未自动切分支，则遵循规范创建分支（如 `001-sa01-config-obs-lifecycle`），避免直接在 `main` 上大改。

---

## 第 2 步：组装并执行 `/speckit-specify`

按 `.agents/skills/speckit-specify/SKILL.md` 执行，从 **Spec `§1 背景与范围`、`§5 行为语义`** 提炼输入（只写 WHAT / WHY，不把 `§3` DDL 与 `§4` Go 签名塞进 specify 层）：

```text
SA-NN（spec NN）需求：<Spec 标题>——<一句话产品与工程定位>

背景与价值：<提炼 Spec §1 背景与解决的问题；强调核心架构红线（L0–L6 单向分层、Fail-Closed 多租户、零 Redis/ClickHouse、单二进制 + PG17）>

用户/调用方场景：
1. <场景 1：把 §5 行为语义提炼为外部用户或下游模块可感知的端到端场景>
2. <场景 2>
3. <场景 3>

功能需求：
- FR1：<从 §1 / §5 逐条提炼，每条独立可测试>
- FR2：...

明确不做（边界）：<逐项照搬 Spec §1 本篇边界>

验收标准：以 <Spec 路径> §8/§9 为准：synapse-server 下 gofmt/go vet/go build/go test -race 全绿，关键回归点：<提炼 §8 关键断言>；人工/集成项见 §9。

依赖与假设：<前置依赖 SA-XX 现状、下游消费者 SA-YY，以及单测隔离假设>
```

---

## 第 3 步：`/speckit-clarify`（必跑 · 歧义必停）

按 `.agents/skills/speckit-clarify/SKILL.md` 执行。对识别出的每一个歧义点：
- 仅当本篇 Spec、`docs/SuperAgent-Go/` 上位定稿、`AGENTS.md` 或 `.specify/memory/constitution.md` 中**有白纸黑字、唯一无歧义的原文规定**时，才可引用原文自动回答；
- **只要存在任何未明确说明、多种解释空间、文档间不一致、或与既有代码现状存在偏差之处，必须立即暂停，向用户逐项列出歧义与候选方案，等待人工确认后方可继续**——严禁自行脑补或替用户做决定；
- V1 已明确拍板的边界（零 Redis、关闭公网注册、Claude+Codex 双 GA、飞书/钉钉+自定义 MCP、无 24 性格预设等）不重复发问；仅在确认**零未决歧义**后进入第 4 步。

---

## 第 4 步：组装并执行 `/speckit-plan`

按 `.agents/skills/speckit-plan/SKILL.md` 执行，输入由 **固定技术栈句 + 模块落位句 + 冻结契约句 + 测试策略句** 拼装：

- **固定技术栈句**（照抄，按本篇层级裁剪无关库说明）：
  `技术栈：Go 1.26.1（synapse-server/，module github.com/Karlsk/synapse-os，Go Modules，CGO_ENABLED=0 单静态二进制）+ Gin + PostgreSQL 17（jackc/pgx/v5 pgxpool + pressly/goose/v3 //go:embed 向前迁移，严禁 DROP TABLE 与重 ORM）+ gopkg.in/yaml.v3（KnownFields(true)）+ sony/gobreaker/v2 + robfig/cron/v3 + 标准库 log/slog、crypto/aes、embed.FS + prometheus/client_golang。零 Redis、零 ClickHouse、零反射 DI；短时缓存/限流走 internal/infra/memcache，跨实例互斥走 PG AdvisoryLock。数据库零物理外键、非全局表 company_id VARCHAR(64) NOT NULL 复合主键首列、TIMESTAMPTZ UTC、NUMERIC(16,6) 定点计费、JSONB、ON CONFLICT 幂等写。`
- **模块落位句**：从 Spec `§2 本篇交付物表` 逐行生成「交付物 → `synapse-server/...` 落位路径」映射，并显式注明所属层级（L0–L6）与禁止导入红线（如业务层禁 `pgx`/`goose`/`os/exec`、L1–L5 全部同层平行模块禁直接互引须经 L0 `core/spi`/`core/store`/`core/eventbus` 倒置解耦、出站 HTTP 必走 `httpclient` 双 Profile、Web 层零业务逻辑）。
- **冻结契约句**：
  `冻结契约逐字对齐 <Spec 路径> §4（导出类型/函数签名、路由路径与鉴权、错误码与中文/协议文案、SQL 形态、JSON camelCase 字段），引用条目使用 Spec 编号不自造同义词。HTTP 统一信封与 6 类协议例外（登录裸 JSON、登出空体、SSE、二进制/沙箱文本预览、OAuth 302、POST /mcp 统一 isError:true）严格保真。`
- **测试策略句**：
  `测试策略按 <Spec 路径> §8 执行：<测试文件与核心测试点清单>；采用同包 *_test.go 表驱动测试 + t.Parallel()；网络与外部依赖用 httptest/Mock 隔离；PG 集成测试使用 db.NewTestSchemaDB 临时 Schema 隔离（未设 TEST_POSTGRES_DSN 时自动 t.Skip）。完成标准为 synapse-server 下 gofmt -l . 零输出、go vet ./...、go build ./...、go test -race ./...（含 arch_test 与 guard_test）全绿且涉及包覆盖率达标（目标 ≥ 80% 或按 §9 要求）。`

---

## 第 5 步：`/speckit-tasks` + 固定软停点（不可跳过）

1. 按 `.agents/skills/speckit-tasks/SKILL.md` 生成 `tasks.md`。
2. **自动比对**：
   - 将 `tasks.md` 与 Spec `§2 交付物表` 逐项核对（有无漏项或多余导出项）；
   - 将任务顺序与 Spec `§6 任务清单（TDD 粒度）` 核对（确保测试任务先于或伴随对应实现）；
   - 检查 Spec `§4` 与 `AGENTS.md` 中的硬红线是否已落地为具体任务或任务约束。
3. 向用户输出清晰的比对清单（齐 / 缺什么 / 多什么），**停下等待用户明确确认**后再进入第 6 步。这是全流程唯一的固定人工确认停点，绝不跳过。

---

## 第 6 步：`/speckit-analyze`（必跑）+ `/speckit-implement`

1. 先按 `.agents/skills/speckit-analyze/SKILL.md` 执行跨工件一致性检查，核对 `spec.md` / `plan.md` / `tasks.md` 与 `.specify/memory/constitution.md`（7 条原则）及原 PR Spec 有无冲突；若有 CRITICAL/HIGH 冲突，修复留痕后再写代码（与 PR Spec 冲突时以 PR Spec 为准，不擅改 PR Spec）。
2. 按 `.agents/skills/speckit-implement/SKILL.md` 逐任务执行，附加以下编码门禁：
   - **写前**：核对依赖在 `synapse-server/go.mod` 与 `AGENTS.md §2.1` 白名单内；核对前置包导出签名；
   - **写中**：
     - 严格遵守 `AGENTS.md` 与 `constitution.md` 全部硬红线：L0–L6 单向分层、Fail-Closed `tenant.Require(ctx)` + `tenant.Detach(ctx)`、`SecretCipher` 无明文落盘/日志脱敏、禁 `exec.Command` / 裸 `http.Client` / 业务直连 `pgx`、`httpclient` 只分类不降级、流式 `Timeout=0`、锁外 I/O 与进程关闭、`ctx` 首参不入 struct、错误只处理一次（`%w` 包装 + `errors.Is`/`errors.As`）、生产路径禁 `panic`；
     - 严格 TDD：RED（依据 `§4` 契约先写 `*_test.go` 运行确认红灯——新符号未定义导致的编译失败属于 Go 正常 RED）→ GREEN（最小实现，不越界堆设计）→ REFACTOR（测试保护下整理可读性、消除冗余）；
   - **写后（任务级 DoD）**：每完成一个任务即在 `synapse-server/` 跑 `go build ./... && go test -race -count=1 ./<当前包>/...`，红灯当场修，不攒到最后；
   - **关键回归测试保真**：Spec `§8` 列出的测试点与关键断言必须逐条落地；
   - **范围红线**：严禁顺手重构无关文件或提前实现后续 PR 的功能；发现改进建议仅记录在收尾报告中供用户决策。

---

## 第 7 步：Spec 级收尾——证据 DoD 与验收报告

全部满足后才可宣布本篇完成，并在对话中输出完整、可审计的验收报告（不另开报告文件）：

1. **全量硬门禁实测证据**（贴关键命令与实测输出摘要）：
   - `gofmt -l .`（零输出）
   - `go vet ./...`
   - `go build ./...`
   - `go test -race ./...`（明确标出 `arch_test.go`、`guard_test.go` 状态）
   - `go test -race -cover ./<本篇涉及包>/...`（覆盖率对比基线与 `§9` 目标）
   - 涉及 DB/PG 存储时：`TEST_POSTGRES_DSN` 集成测试实测结果（或明确标为待配置 PG 环境人工/CI 验证）
2. **交付物与测试清单对账**：
   - `§2 交付物表` 逐项文件存在性与导出签名核对；
   - `§8 测试清单` 逐项用例落地核对；
   - 红线自检（零 Redis/ClickHouse/GORM、零违规 `pgx`/`exec.Command`/裸 `http.Client`、日志无明文密钥）。
3. **文档同步核对（Spec `§7`）**：
   - `docs/SuperAgent-Go/DDD/README.md` 看板状态更新（如由用户指示或按 `§7` 要求更新，未提交前标注状态）；
   - 其他 `§7` 列明的同步项（如 `configs/config.example.yaml` 等）。
4. **变更总结（Reviewer 导读）**——以 `git status --short` / `git diff --stat` 实测为准，固定三段：
   - **改动点**：按包分组列出新增/修改/删除的文件及一句话动机；触碰前序已交付文件或共享契约的单独高亮；
   - **重点 Review 清单**：按风险排序 3~6 条（分层/安全红线、冻结契约逐字保真点、并发/锁/超时边界），每条附精确文件与行号链接；
   - **如何验证**：可直接复制执行的命令块与预期输出，以及剩余**人工验证项清单**（如 `CGO_ENABLED=0` 静态编译、真 PG17 集成测试、`curl http://127.0.0.1:18080/api/v1/meta` 冒烟、UI 联调等）。
5. **收尾停止（禁止自动 Git 提交）**：明确告知用户「自动验收门已通过，剩余人工项如上」，附上 Spec `§10 可提交节点` 的建议 commit message，**严禁自动执行 `git add` / `git commit` / `git push`**，等待用户审阅并明确下达提交指令。
