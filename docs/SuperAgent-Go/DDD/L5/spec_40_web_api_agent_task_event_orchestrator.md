# spec 40 | L5 Web API — Agent、SSE 对话流、任务、自动化、收件箱与 SuperAgent HTTP Handler（71 端点）

- **状态**：`draft`
- **篇号顺序**：40（L5 接入层收口篇；挂载全部 71 条核心交互与编排端点并交付 177 条全量路由树一致性守卫单测）
- **对应 PR**：`docs/SuperAgent-Go/DDD/README.md` 看板中的 `SA-40`
- **预估工作量**：`1.5 人天`
- **上位契约**：
  - [05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §1
  - 归档母本：[_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md](../_epic/L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md) §4.2.3 (C/D/E/F/Inbox), §5.3
  - Java 源码参考（均已核验存在于 `/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent`）：
    - `superagent-web/src/main/java/com/superagent/web/agent/{AgentController,AgentInviteController,AgentMemoryController}.java`、`superagent-web/src/main/java/com/superagent/web/agentlog/AgentLogController.java`
    - `superagent-web/src/main/java/com/superagent/web/superagent/SuperAgentController.java` (`L24-L70`)
    - `superagent-web/src/main/java/com/superagent/web/chat/ChatController.java` (`L44-L118`)
    - `superagent-web/src/main/java/com/superagent/web/task/TaskController.java` (`L52-L159`)
    - `superagent-web/src/main/java/com/superagent/web/automation/AutomationController.java` (`L41-L106`)
    - `superagent-web/src/main/java/com/superagent/web/inbox/InboxController.java` (`L31-L56`)
    - `superagent-web/src/main/java/com/superagent/web/usage/UsageController.java` (`L31-L35`)
- **前置依赖**：
  - [L5/spec_38 (SA-38)](./spec_38_web_gateway_mcp_ui_embed.md) & [L5/spec_39 (SA-39)](./spec_39_web_api_platform_assets_cost.md)
  - [L3/spec_27](../L3/spec_27_agent_registry_collab.md)–[L3/spec_32](../L3/spec_32_task_automation_mcp.md)（Agent/Chat/Task/Automation）
  - [L4/spec_35](../L4/spec_35_event_identity_triage_inbox.md)–[L4/spec_37](../L4/spec_37_orchestrator_mcp_tools.md)（Inbox/SuperAgent）
- **下游消费者**：`SA-41`（装配完整 `NewRouter` 启动 HTTP 服务）

---

## 1. 背景与范围

### 1.1 为什么做与用户价值

本篇（`SA-40`）完成 L5 全量路由拼图的最后 **71 条**核心交互与实时流式端点（与 `SA-38` 9 条 + `SA-39` 97 条精确合计 **177 条端点**），核心攻克：
1. **SSE 实时对话与 `LiveTurn` 多人同屏旁观流**：先同步鉴权与抢锁（`409 busy` 返回标准 JSON 保留前端草稿），成功后再切 `text/event-stream` + `X-Accel-Buffering: no` + `SetWriteDeadline(time.Time{})` 解除写超时，断连不中断后台 CLI 回合；`watchLive` 空闲即推单帧 `idle` 帧后结束；
2. **兼容桩与公开 Webhook**：`GET /api/v1/agents/characters` 恒返 `[]`；`POST /api/v1/automations/:id/trigger` 挂在免 Session 白名单路由组，由 `AutomationService.Trigger` 凭 `X-Automation-Token` 解析租户并常时校验；
3. **177 路由机器自检守卫（`router_test.go`）**：通过 `engine.Routes()` 机器校验全部 177 条契约路由精确注册、零参数名冲突且 `POST /api/v1/orgs` 不存在。

### 1.2 本篇边界

- **本篇负责**：`internal/web/{handler_agent.go,handler_chat.go,handler_task.go,handler_superagent.go,handler_inbox.go}`、`router_test.go` 及各 Handler 单测。
- **本篇不负责**：L6 CLI 与 `/actuator/*` 挂载（交由 `SA-41`）。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（`synapse-server/` 下） | 类型 |
|---|---|---|---|
| 1 | Agent、邀请、长期记忆与运行日志 Handler（21 端点，含 `/agents/characters -> []`） | `internal/web/handler_agent.go` | 新增 |
| 2 | 对话会话 CRUD、`POST .../messages` SSE 流与 `GET .../live/stream` 同屏旁观 Handler（11 端点） | `internal/web/handler_chat.go` | 新增 |
| 3 | 异步任务（17 含 `/tasks/:id/usage`）与自动化/公开 Webhook Trigger（11）Handler（28 端点） | `internal/web/handler_task.go` | 新增 |
| 4 | 个人 SuperAgent（6）与企业收件箱（5）Handler（11 端点） | `internal/web/handler_superagent.go`、`handler_inbox.go` | 新增 |
| 5 | SSE 真实流测试与 **177 路由全量机器校验单测** | `internal/web/{handler_agent,handler_chat,handler_task,handler_superagent,router}_test.go` | 新增 |

---

## 3. 数据库设计

本篇不新增表、不接触数据库驱动，零 Redis 依赖。

---

## 4. 冻结契约

### 4.1 导出符号

```go
package web

func registerAgentRoutes(auth *gin.RouterGroup, deps Dependencies)
func registerChatRoutes(auth *gin.RouterGroup, deps Dependencies, opts Options)
func registerTaskAutomationRoutes(public, auth *gin.RouterGroup, deps Dependencies)
func registerSuperAgentInboxRoutes(auth *gin.RouterGroup, deps Dependencies)
```

### 4.2 本篇冻结 71 条端点分组清单（对应母本 #18–#83, #162–#166）

1. **Agent/邀请/记忆/日志（21 端点）**：`GET/POST /api/v1/agents`、`GET /api/v1/agents/avatars`、`GET /api/v1/agents/characters`（**V1 兼容桩恒返 `[]`**）、`GET/PUT/DELETE /api/v1/agents/:id`、`GET /api/v1/agents/:id/{history,model,timings,tasks,invites,memory,logs,sessions}`、`POST /api/v1/agents/:id/members`、`DELETE /api/v1/agents/:id/members/:username`、`POST /api/v1/agents/:id/invites/:inviteId/{confirm,cancel}`、`PUT /api/v1/agents/:id/memory`、`POST /api/v1/agents/:id/memory/distill`。
2. **个人 SuperAgent（6 端点）**：`GET /api/v1/superagent`、`GET /api/v1/superagent/{invites,background,work}`、`GET /api/v1/superagent/work/:taskId/events`、`POST /api/v1/superagent/plans/:planId/approve`。
3. **对话与 SSE 实时流（11 端点）**：`POST/GET /api/v1/sessions`、`GET/DELETE /api/v1/sessions/:id`、`PUT /api/v1/sessions/:id/title`、`POST /api/v1/sessions/:id/messages`（SSE）、`GET /api/v1/sessions/:id/messages/:messageId/images/:index`、`GET /api/v1/sessions/:id/live`、`GET /api/v1/sessions/:id/live/stream`（SSE）、`POST /api/v1/sessions/:id/stop`、`PUT /api/v1/sessions/:id/messages/:messageId`。
4. **任务与自动化（28 端点）**：
   - Task（17）：`POST/GET /api/v1/tasks`、`GET /api/v1/tasks/{agents,facets,stats}`、`GET/DELETE /api/v1/tasks/:id`、`GET /api/v1/tasks/:id/{status,events,relations,model-choices,timing,usage}`、`PUT /api/v1/tasks/:id/tags`、`POST /api/v1/tasks/:id/{messages,stop}`、`GET /api/v1/tasks/:id/messages/:messageId/images/:index`；
   - Automation（11）：`GET/POST /api/v1/automations`、`POST /api/v1/automations/preview`、`GET /api/v1/automations/by-task/:taskId`、`GET/PUT/DELETE /api/v1/automations/:id`、`POST /api/v1/automations/:id/{enabled,run,token}`、`POST /api/v1/automations/:id/trigger`（**免 Session 公开白名单**）。
5. **企业收件箱（5 端点）**：`GET /api/v1/inbox/{summary,events,todos}`、`PUT /api/v1/inbox/todos/:id`、`POST /api/v1/inbox/todos/:id/task`。

### 4.3 错误语义

- SSE `SendMessage` 在同步抢锁阶段发生冲突返回常规 JSON `409 busy`（绝不先写 `text/event-stream` 头）；
- 客户端中止 SSE 连接（`reqCtx.Done()`）仅退出当前 HTTP 协程并 `sub.Unsubscribe()`，不报错、不取消后台回合；
- 消息配图与任务配图先校验 `RequireAccess` / `tasks.Status` 防越权探测（404），返回 `X-Content-Type-Options: nosniff` + `Cache-Control: private, max-age=2592000`。

### 4.4 SQL 契约

本篇无直接 SQL 操作。

---

## 5. 行为语义

### 5.1 SSE 流式写出与反代防缓冲（对齐 `ChatController.java:75-102`）

```go
func initSSEHeaders(c *gin.Context) {
	rc := http.NewResponseController(c.Writer)
	_ = rc.SetWriteDeadline(time.Time{}) // 解除 http.Server.WriteTimeout 对长流误杀
	h := c.Writer.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache, no-transform")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no") // 阻断企业 Nginx 缓冲
	c.Status(http.StatusOK)
	c.Writer.Flush()
}
```

`streamSSE` 先同步回放 `backlog` 帧（每帧 `event: <type>\ndata: <json>\n\n` 后立即 `Flush()`），再监听 `reqCtx.Done()`、`timer.C`（默认 `10m`）与 `<-ch`；`WatchLive` 无在途回合时立即发单帧 `event: idle\ndata: {"type":"idle","seq":-1,"payload":null}\n\n` 后正常返回。

---

## 6. 任务清单（TDD 粒度）

- [ ] **T1**：编写 `handler_chat_test.go`（使用 `httptest.NewServer` 真流测试 `X-Accel-Buffering: no`、409 JSON、客户端断连不中断后台、`watchLive` 空闲 `idle`）→ 实现 `handler_chat.go`。
- [ ] **T2**：编写 `handler_agent_test.go`、`handler_task_test.go`、`handler_superagent_test.go`（红灯）→ 实现 `handler_agent.go`、`handler_task.go`、`handler_superagent.go`、`handler_inbox.go`。
- [ ] **T3**：编写 `router_test.go` 通过 `engine.Routes()` 机器校验 **全部 177 条契约路由（9+97+71）** 精确注册、零参数冲突且 `POST /api/v1/orgs` 不存在。
- [ ] **T4**：运行 `go test -race -cover ./internal/web/...`。

---

## 7. 文档同步

- [ ] [DDD/README.md](../README.md) §3 看板：`SA-40` 状态更新。

---

## 8. 测试清单

| # | 测试点 | 落位 | 关键断言 |
|---|---|---|---|
| 1 | SSE 流帧格式、409 JSON、反代头与断连隔离 | `internal/web/handler_chat_test.go` | 含 `X-Accel-Buffering: no`；409 忙返 JSON 非 SSE；客户端中途 cancel 不取消后台回合；空闲推单帧 `idle` |
| 2 | `/agents/characters` 兼容桩与公开 Webhook 触发 | `internal/web/handler_agent_test.go`、`handler_task_test.go` | `GET /agents/characters` 返 `[]`；`POST /automations/:id/trigger` 免 Session Cookie 放行 |
| 3 | **177 条全量路由树机器校验** | `internal/web/router_test.go` | `NewRouter` 零 panic；`engine.Routes()` 100% 匹配全部 177 条契约路由；`POST /api/v1/orgs` 缺席 |

---

## 9. 验收门

- [ ] `cd synapse-server && go build ./... && go test -race ./internal/web/...` 全绿
- [ ] `router_test.go` 断言全部 **177 条端点** 精确匹配且无 Gin 参数冲突
- [ ] 行覆盖率 ≥ 80%，零 Redis 引入

---

## 10. 可提交节点

- 建议 commit：`feat(web-api): wire 71 agent, SSE chat, task, automation, inbox and SuperAgent handlers with 177-route guard (spec 40, SA-40)`
