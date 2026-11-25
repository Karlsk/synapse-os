# 00 | P1 通用群体智能平台 — 总体技术架构与 oryx-SuperAgent 的 Go 语言 1:1 改造工程设计

> **所属项目**：`P1 - 通用群体智能平台 (Universal Multi-Agent & Multi-Harness OS)`  
> **分册编号**：`00 - 总体技术架构与 Go 改造工程规范`  
> **技术栈标准**：后端 **Go 1.22 (`Gin` + `GORM` + `mcp-go` + `go-openai`)** + 前端 **React 18 (`TypeScript` + `Vite` + `Ant Design 5` + `ReactFlow` + `Monaco Editor`)** + 数据库 **PostgreSQL 17 (`pgvector`)** + **Redis 7**。  
> **上层汇总文档**：[P1_通用群体智能平台_DDD.md](file:///Users/gaorenjie/Documents/Company/aiops-brainstorming-gemini/设计文档/P1_通用群体智能平台_DDD.md)

---

## 1. 工程定位与对标 `oryx-labs/SuperAgent` 的 Go 改造哲学

### 1.1 为什么基于 `oryx-labs/SuperAgent` 思想用 Go 1:1 改造是最佳路径？
通过对 `oryx-labs/SuperAgent`（Java 21 + 多 Agent CLI 容器底座）及 `deepseek-ai/deepseek-harness` 的深度拆解，其核心成功经验在于：
1. **将“管控调度外壳（Orchestration Shell）”与“底层执行套具（Execution Harness）”彻底解耦**：
   - 上层系统负责管理 `Team（含 SuperAgent 提示词）`、`细粒度 WorkerAgent`、`Skill (SKILL.md)` 与 `MCP 工具网关`；
   - 底层支持调用多种成熟的 Agent Harness（如 `DeepSeek Harness`、`pi`、`Codex`、`Claude Code`）以及内置的轻量执行器。
2. **用 Go 替代 Java 21 的降维优势**：
   - 省去庞大笨重的 JVM 与 Spring Boot 样板代码，与我们 **P2（夜莺 Go 二开）** 和 **P4（Go 应用平台）** 实现 **100% 全栈 Go + PostgreSQL 17 语言与存储大一统**；
   - Go 原生的 `os/exec`（进程流控制）、`goroutine + errgroup`（并发调度多个细粒度 WorkerAgent）、`chan`（SSE 毫秒级流式广播）以及 `mark3labs/mcp-go` SDK，使得核心后端仅需 **约 2,000 ~ 2,500 行 Go 代码** 即可完整落地！

---

## 2. P1 前后端工程目录结构

### 2.1 P1 后端工程结构（Go 语言，`p1-superagent-os`）
```text
p1-superagent-os/
├── cmd/
│   └── server/
│       └── main.go                       # 启动 Gin API 服务与内部安全 MCP 代理网关
├── configs/
│   └── config.yaml                       # PG17、Redis、模型路由配置、各外部 Harness 二进制路径配置
├── internal/
│   ├── model/                            # PostgreSQL 17 GORM 实体模型 (含 JSONB 与 pgvector)
│   │   ├── team.go                       # Team 与 SuperAgent 配置模型
│   │   ├── worker_agent.go               # 细粒度专职 WorkerAgent 配置模型
│   │   ├── skill_mcp.go                  # Skill (SKILL.md) 与 MCP Server 注册模型
│   │   └── session_trace.go              # Team 运行会话、动态计划表(Plan)与 Worker 子会话轨迹
│   ├── coordinator/                      # 【核心模块1】SuperAgent 主控协调与动态重规划 (Re-plan) 引擎
│   │   ├── team_runner.go                # 启动 Team 会话，初始化动态计划板 (Plan Board)
│   │   ├── worker_dispatcher.go          # errgroup 并行/串行唤醒细粒度 WorkerAgent 并隔离上下文
│   │   └── plan_manager.go               # 处理 update_plan / finish_team_task 原语
│   ├── harness/                          # 【核心模块2】可插拔多 Harness 驱动适配层 (Multi-Harness Adapter)
│   │   ├── driver_interface.go           # 统一接口：HarnessDriver (Run / Cancel / StreamEvents)
│   │   ├── materializer.go               # 沙箱目录生成器 (动态渲染 AGENTS.md / skills/*.md / mcp.json)
│   │   ├── driver_native_go.go           # 内置极速 Go Harness (0ms启动，适合秒级细粒度Worker)
│   │   ├── driver_deepseek.go            # DeepSeek Harness (dsh) 适配器
│   │   ├── driver_pi.go                  # Pi Harness 适配器
│   │   └── driver_codex_claude.go        # Codex / Claude Code Headless JSONL 流式适配器
│   ├── mcp_guard_proxy/                  # 【核心模块3】P1 内置安全护栏 MCP 代理网关
│   │   ├── proxy_server.go               # 供所有 Harness 连接的本地受控 MCP Server 端点
│   │   ├── virtual_worker_tools.go       # 将旗下细粒度 WorkerAgent 动态映射为 MCP 工具供 SuperAgent 调用
│   │   ├── guardrail_filter.go           # 强制 STRICT_READ_ONLY 只读校验、参数正则拦截与脱敏
│   │   └── idempotent_cache.go           # 基于 Redis 的相同只读工具调用 15s 幂等缓存
│   ├── rag_pgvector/                     # 【核心模块4】基于 PG17 pgvector 的知识与案例检索
│   │   ├── chunker.go                    # Markdown/文档结构化切片
│   │   └── hybrid_search.go              # 向量余弦相似度 + 全文检索
│   └── api/                              # 面向 P4 与 P1 前端控制台的 REST & SSE 接口
└── pkg/
    └── sse/                              # 标准 Server-Sent Events 流式事件分发器
```

### 2.2 P1 偏技术型独立前端控制台工程结构（React 18，`p1-console-web`，支撑独立售卖）
```text
p1-console-web/
├── src/
│   ├── pages/
│   │   ├── 01_TeamStudio/                # 1. Team 战队与 SuperAgent 编排页（配置 Team Prompt、选 Harness、勾选细粒度 Worker）
│   │   ├── 02_WorkerAgentHub/            # 2. 细粒度 WorkerAgent 专家库（如 BGP状态分析Agent、BGP配置分析Agent 管理）
│   │   ├── 03_SkillIde/                  # 3. Skill 技能包在线 IDE（Monaco Markdown 编辑 SKILL.md + 版本管理）
│   │   ├── 04_McpAndHarnessGateway/      # 4. MCP 工具注册、只读风险分级与多 Harness 驱动健康检测页
│   │   └── 05_PlaygroundAndTraces/       # 5. 在线调试沙箱与全链路瀑布流 Trace 观测台（看 Plan 演进与 Worker 隔离日志）
```

---

## 3. 1~2 人团队 Go 改造开发排期表（共约 8 ~ 10 人天）

| 阶段 | 核心任务 | 产出物 | 预估人天 |
| :--- | :--- | :--- | :---: |
| **Day 1~2** | **PG17 表结构与四元配置 CRUD API** | `t_p1_team`、`t_p1_worker_agent`、`t_p1_skill`、`t_p1_mcp_server` 建表与管理接口 | `2 人天` |
| **Day 3~4** | **内置 Go Harness + 动态沙箱生成器** | `Materializer` 渲染 `SKILL.md`/`mcp.json` + `NativeGoDriver` 极速工具调用循环 | `2 人天` |
| **Day 5~6** | **外部多 Harness 适配器 + 安全 MCP 代理网关** | `DeepSeek/pi/Codex` 子进程 JSONL 流适配器 + 虚拟 Worker 工具映射 + 只读安全护栏 | `2.5 人天` |
| **Day 7~8** | **SuperAgent 动态规划协调器与技术控制台前端** | `update_plan`/`dispatch_workers` 并行协调器 + P1 偏技术型 React 控制台页面 | `2.5 人天` |
