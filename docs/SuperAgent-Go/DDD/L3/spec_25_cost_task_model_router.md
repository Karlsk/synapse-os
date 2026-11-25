# spec 25 | L3 成本管理 — 修复版任务智能路由（固定模型闭环、单候选跳过与 LLM-as-Judge）

- **状态**：`draft`
- **篇号顺序**：25（L3 成本域收口篇；补齐原版 Java 缺失的固定模型方法并新增单候选短路优化，完整实现 `spi.TaskModelSelector`）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-25`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md](../../04_核心业务域Go并发设计_对话_任务_编排_IM与成本路由.md) §4
  - 归档母本：[_epic/L3/spec_07_cost_catalog_router_usage.md](../_epic/L3/spec_07_cost_catalog_router_usage.md) §1.1, §5.3
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-cost/src/main/java/com/superagent/cost/routing/TaskModelRouter.java` (`L1-L249`)
    - `superagent-cost/src/test/java/com/superagent/cost/routing/TaskModelRouterTest.java` (`L1-L114`)
- **前置依赖**：
  - [L1/spec_05 (SA-05)](../L1/spec_05_core_domain_platform_assets_contracts.md)（`TaskModelChoice`）
  - [L1/spec_06 (SA-06)](../L1/spec_06_core_domain_runtime_agent_task_contracts.md)（`Task`、`spi.TaskModelSelector`）
  - [L2/spec_18 (SA-18)](../L2/spec_18_platform_provider_credentials_modelcall.md)（`ModelCall` 裁判调用）
  - [L3/spec_24 (SA-24)](./spec_24_cost_catalog_pricing_usage.md)（`ModelPriceService.Candidates`/`ChatModels`）
- **下游消费者**：`SA-30`（`TaskService` 选模与复杂任务调度）、`SA-31`（任务失败升级重跑调用 `Escalate`）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

后台异步任务既需要按难度选择“最便宜够用”的模型降本，也需要在执行受挫时按价格阶梯自动升档，更需要严格尊重企业管理员设定的“任务固定模型列表（`task.fixed-models`）”。

本篇（`SA-25`）独立交付 `TaskModelRouter`（1.5 人天独立 PR），重点解决原版 Java 代码的两个核心问题：
1. **修复原版 Java 缺失实现**：原版 `TaskModelRouter.java:68, 98` 调用的 `fixedStart()`/`fixedNext()` 缺失且 `strongest()` 漏掉固定模型短路；Go V1 完整补齐并在全部模式（`Choose`/`Strongest`/`Escalate`）中严格执行“绝不越界回退集合外模型”，无可用或升档耗尽时返回 `Refused()`（`Model == ""`, `By == "fixed"`）；
2. **V1 成本与延迟优化（单候选跳过裁判）**：普通模式下当 `forTasks` 仅有 1 个模型时，直接短路选用（`By = "default"`），省去一次无意义的 180s LLM 裁判调用。

### 1.2 本篇边界

- **本篇负责**：`internal/cost/router.go`（`TaskModelRouter` 及裁判 Prompt/JSON 解析、固定模型分区与阶梯升级算法）与 `internal/cost/router_test.go`。
- **本篇不负责**：定价目录与五维计费（已由 `SA-24` 交付）；`task_model_choice` 落库与任务重试编排（由 `SA-30`/`SA-31` 消费本篇 `spi.TaskModelSelector`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | `TaskModelRouter`（实现 `spi.TaskModelSelector`：固定模型全闭环、单候选短路、3 裁判降级、价格阶梯升档） | `internal/cost/router.go` | 新增 |
| 2 | 裁判提示词与宽松 JSON 裁决解析器 | `internal/cost/router_verdict.go` | 新增 |
| 3 | 全路径路由单元测试（100% 镜像并超越 `TaskModelRouterTest.java`） | `internal/cost/router_test.go` | 新增 |

---

## 3. 数据库设计

本篇无直接 SQL 写操作。产出的 `*model.TaskModelChoice` 由下游 `SA-30` 持久化至 `task_model_choice`（已清理原版废弃无用 `tier` 写入，仅写入 `decided_by`/`difficulty`/`model`/`cli`/`judge`/`candidates`/`reasoning`）。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package cost

const (
	MaxJudges     = 3
	JudgeTimeout  = 180 * time.Second
	BriefMaxRunes = 4000
)

var _ spi.TaskModelSelector = (*TaskModelRouter)(nil)

type ModelAsker interface {
	Ask(ctx context.Context, modelID, prompt string, timeout time.Duration, uctx model.UsageContext) (string, error)
}

type TaskModelRouter struct { /* prices *ModelPriceService, settings SettingsReader, asker ModelAsker */ }
func NewTaskModelRouter(prices *ModelPriceService, settings SettingsReader, asker ModelAsker) *TaskModelRouter
func (r *TaskModelRouter) Enabled(ctx context.Context) bool
func (r *TaskModelRouter) Choose(ctx context.Context, task model.Task) (*model.TaskModelChoice, error)
func (r *TaskModelRouter) Escalate(ctx context.Context, task model.Task, failure string) (*model.TaskModelChoice, error)
func (r *TaskModelRouter) Strongest(ctx context.Context, task model.Task) (*model.TaskModelChoice, error)
```

### 4.2 路由契约

本篇为纯领域决策服务，不新增 HTTP 端点（任务模型选型审计由 `SA-40` 任务详情接口返回）。

### 4.3 错误语义

- 固定模型全部不可用或升档耗尽：**不返回 Go `error`**，而是返回 `TaskModelChoice{Model: "", By: model.ChoiceByFixed, Reasoning: "..."}`（调用方检测 `choice.Refused()` 将任务直接置 `ERROR` 并记录审计原因）；
- 普通模式裁判全部超时/失败/返回非法 JSON：**不抛错**，自动降级为 `By = model.ChoiceByFallback` 选用 `defaultFor(forTasks)`；
- 普通模式 `Escalate` 已达默认上限模型：返回 `(nil, nil)` 通知调用方停止升档。

### 4.4 SQL 契约

本篇仅通过 `ModelPriceService` 与 `SettingsReader` 读取内存/设置数据，不直接执行 SQL。

---

## 5. 行为语义

### 5.1 决策主路径（对齐 `TaskModelRouter.java:65-249` + V1 补齐修复）

1. **`Choose(ctx, task)`**：
   - 若 `task.fixed-models` 开启且非空 → `fixedStart`（从固定集合中挑已通过探活且 `ForTasks==true` 且 `Blended` 最便宜者，跳过项写入 `Reasoning`；全部不可用返回 `Refused`）；
   - 普通模式过滤 `forTasks`：
     - `len == 0` → 返回 `nil`；
     - **`len == 1`（V1 优化）** → 跳过裁判，直接返回 `By = "default"`；
     - `len >= 2` → 依次尝试最多 `3` 个裁判（`ChatModels` 优先 + 全部可运行最贵补齐，允许 `ForTasks==false` 的强模型担任裁判），超时 `180s`，记账到 `source = "model_choice"`；
     - 裁判返回 JSON 合法且模型在 `forTasks` 内 → `By = "judge"`；若模型越界 → 按裁判给出的 `difficulty`（`easy` 最便宜 / `medium` 中位 / `hard` 默认上限）回退并附注说明；全部失败 → `By = "fallback"` 选 `defaultFor`。
2. **`Strongest(ctx, task)`（V1 修复）**：
   - 若 `task.fixed-models` 开启且非空 → 同样受固定集合约束调用 `fixedStart`（绝不越界）；
   - 否则直接返回 `defaultFor(forTasks)`（`By = "default"`, `Difficulty = "hard"`）。
3. **`Escalate(ctx, task, failure)`（V1 修复）**：
   - 若 `task.fixed-models` 开启且非空 → `fixedNext`：在固定集合按 `Blended ASC` 寻下一个严格更贵可用模型（已无更贵可尝试尾部未定价固定模型），全部耗尽返回 `Refused`；
   - 普通模式：在 `forTasks` 中找满足 `current.Blended() < c.Blended() <= top.Blended()` 的最便宜模型；已在 `top` 则返 `nil`。

```go
// internal/cost/router.go — 关键修复点示例
func (r *TaskModelRouter) fixedNext(ctx context.Context, task model.Task, fixed []string, failure string) *model.TaskModelChoice {
	usable, _ := r.partitionFixed(ctx, fixed)
	current := findCandidate(usable, task.Model)
	var next *Candidate
	if current != nil && current.Priced() {
		for i := range usable {
			if usable[i].ID != task.Model && usable[i].Priced() && usable[i].Blended() > current.Blended() {
				next = &usable[i]
				break
			}
		}
		if next == nil {
			for i := range usable {
				if usable[i].ID != task.Model && !usable[i].Priced() {
					next = &usable[i]
					break
				}
			}
		}
	} else {
		seen := current == nil
		for i := range usable {
			if usable[i].ID == task.Model { seen = true; continue }
			if seen { next = &usable[i]; break }
		}
	}
	if next == nil {
		return &model.TaskModelChoice{
			TaskID: task.ID, By: model.ChoiceByFixed, Model: "",
			Candidates: describeCandidates(usable),
			Reasoning:  fmt.Sprintf("在固定模型 %s 上失败：%s。固定列表中没有更贵的可用模型了，不回退到集合外模型。", task.Model, clipRunes(failure, 300)),
			CreatedAt:  time.Now().UTC(),
		}
	}
	return &model.TaskModelChoice{
		TaskID: task.ID, By: model.ChoiceByFixed, Model: next.ID, CLI: next.CLI,
		Candidates: describeCandidates(usable),
		Reasoning:  fmt.Sprintf("在固定模型 %s 上失败：%s。换固定列表中下一个更贵的 %s 重新执行。", task.Model, clipRunes(failure, 300), next.ID),
		CreatedAt:  time.Now().UTC(),
	}
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `internal/cost/router_test.go`（红灯：覆盖固定模型全路径 + 单候选短路 + 3 裁判降级 + 越界难度回退 + 价格阶梯升档到顶）。
- [ ] **T2**：实现 `internal/cost/router_verdict.go`（支持剥离 Markdown 代码围栏的 JSON 裁决提取）。
- [ ] **T3**：实现 `internal/cost/router.go` 使全部单测全绿，运行 `go test -race -cover ./internal/cost/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-25` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | 固定模型模式 `Choose`/`Strongest`/`Escalate`/`Refused` | `internal/cost/router_test.go` | `fixed=["sonnet","gpt-big","glm","gone"]`：`Choose`/`Strongest` 均选 `glm` 且 `ModelAsker` 调用=0；`Escalate(glm)`→`sonnet`；`Escalate(sonnet)`→`Refused()==true`；全不可用→`Refused()==true` |
| 2 | 单候选跳裁判优化 | `internal/cost/router_test.go` | `len(forTasks)==1` 时直接返回该模型（`By="default"`）且 `ModelAsker` 调用=0 |
| 3 | 3 裁判冗余与越界难度回退 | `internal/cost/router_test.go` | 裁判 1 失败由裁判 2 接替；越界模型按 `easy`/`medium`/`hard` 回退；全部非法 JSON 降级 `By="fallback"` |
| 4 | 普通模式价格阶梯升档到顶 | `internal/cost/router_test.go` | `haiku → glm → sonnet → opus`，已在 `opus` 时 `Escalate` 返回 `nil` |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/cost/...` 全绿
- [ ] 对照 `TaskModelRouterTest.java:74-94` 全部断言 100% 通过且新增单候选跳裁判用例通过
- [ ] 行覆盖率 ≥ 85%，零外部网络请求，零 Redis 依赖

---

## 10. 可提交节点

- 建议 commit：`feat(cost): implement fixed-mode and single-candidate optimized TaskModelRouter (spec 25, SA-25)`
