# 03 | SuperAgent 动态规划 (Re-plan)、细粒度 Worker 调度与 MCP 安全护栏网关详细设计

> **所属项目**：`P1 - 通用群体智能平台`  
> **分册编号**：`03 - SuperAgent 协调器与安全 MCP 网关`  
> **技术栈标准**：`Go 1.22` (`errgroup`, `mark3labs/mcp-go`, `PostgreSQL 17`)  
> **上层汇总文档**：[P1_通用群体智能平台_DDD.md](file:///Users/gaorenjie/Documents/Company/aiops-brainstorming-gemini/设计文档/P1_通用群体智能平台_DDD.md)

---

## 1. 模块设计核心：SuperAgent 是如何通过 MCP 虚拟工具调度细粒度 WorkerAgent 的？

为了让 `SuperAgent`（无论其底层绑定的是 `DeepSeek Harness`、`pi`、`Codex` 还是 `Native-Go Harness`）能够无缝遵循 **“拆解任务 $\rightarrow$ 制定/更新计划（Plan/Re-plan） $\rightarrow$ 派发细粒度 WorkerAgent $\rightarrow$ 根据返回继续规划或得出结论”** 的闭环，P1 的 `mcp_guard_proxy` 为 `SuperAgent` 自动暴露 **3 个内置控制原语工具**：

| 暴露给 SuperAgent 的内置 MCP 工具名 | 核心参数 | 作用与触发逻辑 |
| :--- | :--- | :--- |
| **`update_investigation_plan`** | `steps: [{step_id, goal, target_worker_code, status, reason}]` | **动态规划与重规划原语**：SuperAgent 在开始排查前或收到 Worker 结论后调用此工具，创建、修改、打勾完成或剪枝跳过计划步骤，实时广播给前端展示。 |
| **`dispatch_specialist_workers`** | `tasks: [{step_id, worker_code, instruction, context_facts}]` | **细粒度 Worker 派发原语（支持单发或并发派发多个 Worker）**：P1 拦截此调用后，用 `errgroup` 在后台并发启动对应的细粒度 `WorkerAgent`，每个 Worker 在独立子上下文中干完活后，仅将浓缩结论返回给 SuperAgent。 |
| **`complete_team_investigation`** | `final_report_json: {...}` | **结案汇总原语**：校验输出是否符合 `Team` 定义的 `output_json_schema`，校验通过后结束会话并返回给上层调用方（如 P4）。 |

---

## 2. SuperAgent 协调器与细粒度 Worker 并发派发 Go 核心实现

### 2.1 `SuperAgent` 系统提示词自动组装器 (`internal/coordinator/prompt_builder.go`)
当啟動一个 `Team` 时，P1 自动将用户在 `Team` 上配置的 `super_agent_prompt` 与旗下关联的所有细粒度 `WorkerAgent` 清单拼装成完整的主控指令：

```go
// internal/coordinator/prompt_builder.go
package coordinator

func BuildSuperAgentSystemPrompt(team model.Team, workers []model.WorkerAgent) string {
    var sb strings.Builder
    sb.WriteString("# 你的角色：Team 主控规划与协调智能体 (SuperAgent)\n\n")
    sb.WriteString(team.SuperAgentPrompt + "\n\n")

    sb.WriteString("## 你旗下可调度的细粒度专职专家 (WorkerAgents) 清单\n")
    sb.WriteString("请严格根据每位专家的职责边界按需点名调度，支持在单次调用中并行派发多个无依赖的专家：\n")
    for _, w := range workers {
        sb.WriteString(fmt.Sprintf("- **`%s` (%s)**：%s\n", w.WorkerCode, w.WorkerName, w.SpecialtyDescription))
    }

    sb.WriteString(`
## 强制工作循环准则 (Plan -> Dispatch -> Re-plan -> Conclude)
1. **先规划后行动**：在派发任何专家前，必须先调用 update_investigation_plan 制定清晰的排查计划表；
2. **细粒度精准派单**：调用 dispatch_specialist_workers 将具体子任务交给对应的 WorkerAgent（例如：需检查 BGP 会话是否存活时只调 worker_bgp_state_analyzer；只有怀疑配置或路由策略问题时才调 worker_bgp_config_route_analyzer）；
3. **动态修正计划 (Re-plan)**：每次收到 WorkerAgent 返回的调查结论后，必须审视现有假设是否被证实或排除，调用 update_investigation_plan 更新步骤状态并决定下一步；
4. **结论汇总**：当根因证据链与修复方案验证全部闭环后，调用 complete_team_investigation 输出最终结构化 JSON。
`)
    return sb.String()
}
```

### 2.2 并行调度细粒度 WorkerAgent 与主从上下文物理隔离实现 (`worker_dispatcher.go`)

```go
// internal/coordinator/worker_dispatcher.go
package coordinator

type WorkerTaskItem struct {
    StepID       string   `json:"step_id"`
    WorkerCode   string   `json:"worker_code"`
    Instruction  string   `json:"instruction"`
    ContextFacts []string `json:"context_facts"`
}

// DispatchWorkersConcurrently 由 SuperAgent 调用 dispatch_specialist_workers 工具时触发
func (c *Coordinator) DispatchWorkersConcurrently(ctx context.Context, runSession *model.TeamRun, tasks []WorkerTaskItem, sseChan chan<- harness.HarnessStreamEvent) (string, error) {
    g, gCtx := errgroup.WithContext(ctx)
    results := make([]string, len(tasks))

    for i, task := range tasks {
        idx, t := i, task
        g.Go(func() error {
            // 1. 加载该细粒度 WorkerAgent 的配置、绑定的专属 Skill (SKILL.md) 与授权 MCP 工具列表
            workerSpec, skills, err := c.repo.LoadWorkerWithSkills(gCtx, t.WorkerCode)
            if err != nil {
                return err
            }

            // 2. 创建上下文物理隔离的子执行记录 (t_p1_worker_execution)
            execID := uuid.NewString()
            // 为该 Worker 生成专用的受控 MCP 代理 URL (内含该 Worker 的工具白名单与会话安全模式 STRICT_READ_ONLY)
            workerMcpProxyURL := c.mcpProxy.CreateScopedEndpoint(runSession.RunID, execID, workerSpec.AllowedMcpTools, runSession.SafetyMode)

            // 3. 选用该 Worker 配置的 HarnessDriver (如细粒度状态分析用 NATIVE_GO，复杂脚本生成用 DEEPSEEK_HARNESS / CODEX)
            driver := c.harnessRegistry.GetDriver(workerSpec.HarnessType)

            // 4. 在独立子上下文中运行该细粒度 WorkerAgent！
            // Worker 内部调用 display 命令产生的几千行原始回显通过 sseChan 推送给前端展示并存入 t_p1_worker_execution，
            // 但绝不塞回给 SuperAgent，只将最终提炼的 conciseReport 返回给 SuperAgent！
            conciseReport, err := driver.Execute(gCtx, harness.HarnessExecRequest{
                ExecID:           execID,
                AgentCode:        workerSpec.WorkerCode,
                HarnessType:      workerSpec.HarnessType,
                ModelKey:         workerSpec.ModelKey,
                SystemPrompt:     workerSpec.WorkerSystemPrompt,
                UserTaskPrompt:   formatWorkerTaskPrompt(t.Instruction, t.ContextFacts),
                BoundSkills:      skills,
                LocalMcpProxyURL: workerMcpProxyURL,
                MaxSteps:         workerSpec.MaxToolSteps,
            }, sseChan)

            if err != nil {
                results[idx] = fmt.Sprintf("【专家 %s (%s) 执行异常】: %v", workerSpec.WorkerName, workerSpec.WorkerCode, err)
                return nil
            }

            // 5. 持久化 Worker 独立轨迹并组装返回给 SuperAgent 的高浓度结论摘要
            _ = c.repo.SaveWorkerExecution(gCtx, execID, runSession.RunID, t.StepID, workerSpec, conciseReport)
            results[idx] = fmt.Sprintf("### 专家汇报 [%s (`%s`)] (对应计划步骤 %s):\n%s",
                workerSpec.WorkerName, workerSpec.WorkerCode, t.StepID, conciseReport)
            return nil
        })
    }

    _ = g.Wait()
    return strings.Join(results, "\n\n---\n\n"), nil
}
```

---

## 3. P1 安全护栏 MCP 代理网关详细设计 (`internal/mcp_guard_proxy/guardrail_filter.go`)

不管底层跑的是 `Native-Go Harness`、`DeepSeek Harness`、`pi` 还是 `Codex`，所有业务工具调用都必须穿过 `P1 Guarded MCP Proxy`：

```go
// internal/mcp_guard_proxy/guardrail_filter.go
package mcp_guard_proxy

func (p *GuardedProxy) InterceptAndForwardToolCall(ctx context.Context, scope ScopedSession, toolName string, args map[string]interface{}) (*mcp.CallToolResult, error) {
    // 1. 校验该细粒度 WorkerAgent 是否被授权使用此 MCP 工具（最小权限隔离）
    if !slices.Contains(scope.AllowedTools, toolName) {
        return nil, fmt.Errorf("GUARDRAIL_BLOCKED: 智能体 %s 未被授权调用工具 %s", scope.WorkerCode, toolName)
    }

    // 2. 校验会话级只读安全锁 (MVP 默认全量开启 STRICT_READ_ONLY)
    toolMeta := p.registry.GetToolMeta(toolName)
    if scope.SafetyMode == "STRICT_READ_ONLY" && toolMeta.RiskLevel != "L0_READ_ONLY" {
        return nil, fmt.Errorf("GUARDRAIL_BLOCKED: 当前处于 STRICT_READ_ONLY 只读诊断模式，严禁调用非只读工具 %s", toolName)
    }

    // 3. 参数敏感词与高危命令正则扫描
    argsBytes, _ := json.Marshal(args)
    for _, regex := range p.registry.GetForbiddenRegexList(toolName) {
        if regex.Match(argsBytes) {
            return nil, fmt.Errorf("GUARDRAIL_BLOCKED: 工具 %s 参数命中安全红线规则 %s", toolName, regex.String())
        }
    }

    // 4. 转发至下游真实业务 MCP Server (如 P4 的 Network-Domain-MCP-Server) 并记录全量调用审计
    return p.forwardToUpstreamMcpServer(ctx, toolMeta.ServerCode, toolName, args)
}
```
