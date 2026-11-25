# 10. 外部 API 与实时通信协议全量契约

> 本文档完整梳理 SuperAgent 全部 **182 个 HTTP 接口**（按业务域分组）、统一 JSON 响应信封与分页约定、SSE 实时流协议、Webhook/OAuth 特殊协议，以及跨全部 20 个领域提供方的**内置 MCP 工具全量总索引**。全文严格保持技术栈无关。

---

## 1. 通用 HTTP 协议约定

- **版本前缀**：全部业务 REST API 统一挂载在 `/api/v1`；破坏性变更通过升级路径前缀版本演进。
- **标准响应信封**：
  ```json
  { "code": "ok", "message": "success", "data": "<载荷或 null>" }
  ```
  - 错误时 `code` 为机器可读短码（详见 [01_系统架构与多租户安全底座.md](./01_系统架构与多租户安全底座.md) §5.1），`data` 恒为 `null`。
  - **非标准信封的 5 类特殊端点**：
    1. `POST /api/v1/auth/login`：成功返回裸 JSON `{userId, username, roles}` 并签发 `HttpOnly/SameSite=Lax` 会话 Cookie；失败返 HTTP 401 `{code: "unauthorized", message}`；
    2. `POST /api/v1/auth/logout`：返回 HTTP 200 空体；
    3. 二进制下载/预览（Logo、头像、消息图片、技能图片、文件内容）：直接返回原始字节流 + 安全头；
    4. SSE 流端点（对话流、旁观流）：返回 `text/event-stream`；
    5. OAuth 端点返回 `302 Found`；`/mcp` 返回标准 JSON-RPC 2.0 报文。
- **分页约定**：1-based `page`（默认 1）、`size`（默认 20，最大 100/200）；任务列表返回增强分页 `{items, total, page, size, spend}`。
- **追踪头**：入站/出站统一透传 `X-Request-Id`。

---

## 2. 全量 HTTP 端点目录（182 个）

### 2.1 元数据、健康运维与登录页（7 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/meta` | 公开 | `{name: "SuperAgent", status: "up", version}` |
| `GET` | `/login` | 公开 | 未登录返回登录页 HTML，已登录 `302` 跳转 `/` |
| `GET` | `/api/v1/branding/logo` | 公开 | 企业自定义 Logo 或内置 PNG（`ETag`、`nosniff`、`no-cache`） |
| `GET` | `/actuator/health` | 公开（详情需登录） | 进程与存储健康探针 |
| `GET` | `/actuator/info` / `metrics` / `prometheus` | 已登录 | 运维信息与 Prometheus 指标抓取 |

### 2.2 组织注册、认证与个人账户（10 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/orgs/registration` | 公开 | `{open: boolean}` 查询是否开放自助注册 |
| `POST` | `/api/v1/orgs` | 公开 | `{name, englishName}` -> `{org, name, username, password}`（初始密码仅展示一次） |
| `POST` | `/api/v1/auth/login` | 公开 | 表单 `org`、`username`、`password` -> 签发会话 Cookie |
| `POST` | `/api/v1/auth/logout` | 已登录 | 销毁服务端会话 |
| `GET` | `/api/v1/auth/me` | 已登录 | 当前用户详情（`id/username/displayName/roles/admin/teams/companyName/avatarVersion/canChangePassword/org/mustChangePassword`） |
| `PUT` | `/api/v1/account/password` | 已登录 *(豁免改密拦截)* | `{current, next}`（≥8 位且不与旧密相同），清除 `mustChangePassword` |
| `PUT` | `/api/v1/account/avatar` | 已登录 | `{data}`（Base64 位图 ≤1 MB）-> 返回版本号（毫秒 mtime） |
| `DELETE` | `/api/v1/account/avatar` | 已登录 | 清除个人头像 |
| `GET` | `/api/v1/avatars` | 已登录 | `Map<userId, version>` 批量头像缓存版本号 |
| `GET` | `/api/v1/avatars/{username}` | 已登录 | 二进制头像（路径参数为 `userId`，30 天私有缓存） |

### 2.3 用户管理与同事通讯录（5 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/users` | `ADMIN` | 公司全量用户列表（隐藏密码哈希） |
| `POST` | `/api/v1/users` | `ADMIN` | 创建用户（校验 `max-users` 配额，填密码自动置强制改密） |
| `PUT` | `/api/v1/users/{id}` | `ADMIN` | 更新用户（含最小管理员保护；重置密码自动置强制改密） |
| `DELETE` | `/api/v1/users/{id}` | `ADMIN` | 硬删除用户（含最小管理员保护；不可变 UUID 隔离历史资产） |
| `GET` | `/api/v1/directory/users` | 已登录 | 启用同事脱敏列表 `{id, username, displayName}` |

### 2.4 Agent、协作邀请、长期记忆与运行日志（21 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/agents` | 已登录 | 懒初始化 `SUPER` 并置顶，返回自有 + 直接协作 + 项目共享 Agent |
| `POST` | `/api/v1/agents` | 已登录 | 创建标准 Agent 并自动建首会话与工作区 |
| `GET` | `/api/v1/agents/avatars` | 已登录 | 返回 24 个内置 Emoji 头像 |
| `GET` | `/api/v1/agents/characters` | 已登录 | 返回 24 个内置双语性格预设 |
| `GET` | `/api/v1/agents/{id}` | 成员可见 | Agent 详情（含直接成员、技能、项目、上级） |
| `PUT` | `/api/v1/agents/{id}` | 所有者 | 更新配置与技能绑定（`SUPER` 锁定名称/性格/人设） |
| `DELETE` | `/api/v1/agents/{id}` | 所有者 | 软删并清记忆（`SUPER` 不可删） |
| `GET` | `/api/v1/agents/{id}/history` | 成员可见 | 按时间正序拼接的多段 `ChatSegment` 完整对话史 |
| `GET` | `/api/v1/agents/{id}/model` | 成员可见 | `{model}` 解析后的当前有效对话模型 |
| `GET` | `/api/v1/agents/{id}/timings` | 成员可见 | 全部历史回合 7 阶段耗时与工具明细列表 |
| `GET` | `/api/v1/agents/{id}/tasks` | 成员可见 | 该 Agent 派生的任务列表 |
| `POST` | `/api/v1/agents/{id}/members` | 所有者 | `{usernames[]}` 直接邀请协作者并触发通知 |
| `DELETE` | `/api/v1/agents/{id}/members/{username}` | 所有者或本人 | 移除协作者或退出协作 |
| `GET` | `/api/v1/agents/{id}/invites` | 成员可见 | 对话内待确认/历史邀请卡片列表 |
| `POST` | `/api/v1/agents/{id}/invites/{inviteId}/confirm` | 所有者 | 批准卡片并正式添加协作者 |
| `POST` | `/api/v1/agents/{id}/invites/{inviteId}/cancel` | 所有者 | 取消邀请卡片 |
| `GET` | `/api/v1/agents/{id}/memory` | 成员可见 | `{memory, updatedAt, canEdit, distilling}` |
| `PUT` | `/api/v1/agents/{id}/memory` | 所有者 | `{memory}` 手动直编（≤10,000 字，空白清空） |
| `POST` | `/api/v1/agents/{id}/memory/distill` | 所有者 | 立即异步触发记忆蒸馏 |
| `GET` | `/api/v1/agents/{id}/logs` | **仅所有者** | `?category=&limit=300`（上限 1,000）运行日志 |
| `GET` | `/api/v1/agents/{id}/sessions` | **仅所有者** | 会话轮换历史列表（序号、原因、重启次数、轮数） |

### 2.5 个人 SuperAgent 编排器（6 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/superagent` | 已登录 | 个人主管状态快照（`agentId/sessionId/processedUntil/running/roundCutoff/plan/teamSize/teamLimit`） |
| `GET` | `/api/v1/superagent/invites` | 已登录 | 跨 Agent 在 SuperAgent 对话内提议的协作邀请卡 |
| `GET` | `/api/v1/superagent/background` | 已登录 | `?limit=200`（上限 1,000）脱离后台会话的原始输入/回复日志 |
| `GET` | `/api/v1/superagent/work` | 已登录 | `?status=all&memberId=&limit=100`（上限 500）分派工作台账 |
| `GET` | `/api/v1/superagent/work/{taskId}/events` | 已登录 | 单项工作追加式事件时间线 |
| `POST` | `/api/v1/superagent/plans/{planId}/approve` | 已登录 | 审批待定计划，解锁招募/更新/派活工具 |

### 2.6 对话会话与 SSE 实时流（11 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `POST` | `/api/v1/sessions` | 所有者 | 为指定 Agent 创建新会话 |
| `GET` | `/api/v1/sessions` | 已登录 | 会话列表 |
| `GET` | `/api/v1/sessions/{id}` | 成员可见 | 单会话详情与消息 |
| `PUT` | `/api/v1/sessions/{id}/title` | 成员可见 | 重命名会话 |
| `DELETE` | `/api/v1/sessions/{id}` | 成员可见 | 软删除会话 |
| `POST` | `/api/v1/sessions/{id}/messages` | 成员可见 | `{content, images[], fileIds[]}` -> **SSE `text/event-stream`**（600s 超时；忙返 409 `busy`） |
| `GET` | `/api/v1/sessions/{id}/messages/{messageId}/images/{index}` | 成员可见 | 二进制消息图片（`nosniff`、30 天私有缓存） |
| `GET` | `/api/v1/sessions/{id}/live` | 成员可见 | 活跃回合状态快照 + 最新 `sessionId` |
| `GET` | `/api/v1/sessions/{id}/live/stream` | 成员可见 | **SSE `text/event-stream`** 重放当前回合缓冲并订阅实时增量 |
| `POST` | `/api/v1/sessions/{id}/stop` | 成员可见 | 中止当前活跃回合 |
| `PUT` | `/api/v1/sessions/{id}/messages/{messageId}` | 成员可见 | 原地修改 `USER` 消息正文（纯记录修正） |

### 2.7 异步任务（17 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `POST` | `/api/v1/tasks` | 已登录 | 创建任务（月度配额 + 最多 8 链接 90s 预读，失败返 409） |
| `GET` | `/api/v1/tasks/agents` | 已登录 | 可用引擎及其通过探活的模型列表 |
| `GET` | `/api/v1/tasks` | 已登录 | `?page=1&size=20&status=&tag=&agent=&source=&project=` 分页顶层列表（隐藏子任务）+ 汇总花费 |
| `GET` | `/api/v1/tasks/facets` | 已登录 | 标签 / Agent / 来源聚合分面与花费 |
| `GET` | `/api/v1/tasks/stats` | 已登录 | 总数 / 活跃 / 待确认 / 今日完成 / 失败 / 本月花费 |
| `GET` | `/api/v1/tasks/{id}` | 所有者或项目成员 | 任务详情（含说明、消息、事件、编码三阶段） |
| `GET` | `/api/v1/tasks/{id}/status` | 所有者或项目成员 | 轻量轮询状态快照 |
| `GET` | `/api/v1/tasks/{id}/events` | 所有者或项目成员 | `?offset=0` 增量事件拉取 |
| `GET` | `/api/v1/tasks/{id}/relations` | 所有者或项目成员 | 父任务、前序、续做链、子任务及含子任务总花费 |
| `PUT` | `/api/v1/tasks/{id}/tags` | **仅所有者** | `{tags[]}` 替换标签（最多 8 个） |
| `GET` | `/api/v1/tasks/{id}/model-choices` | 所有者或项目成员 | 全量模型选型与升档审计记录 |
| `GET` | `/api/v1/tasks/{id}/timing` | 所有者或项目成员 | 完整任务等待段 + 回合段耗时时间线 |
| `GET` | `/api/v1/tasks/{id}/usage` | **仅所有者** | 单任务 Token/USD 账单（复杂任务含子任务归集与分拆） |
| `POST` | `/api/v1/tasks/{id}/messages` | **仅所有者** | 回复确认或追问/返工（支持图片与文件附件；运行中返 409） |
| `GET` | `/api/v1/tasks/{id}/messages/{messageId}/images/{index}` | 所有者 | 二进制任务消息图片 |
| `POST` | `/api/v1/tasks/{id}/stop` | **仅所有者** | 停止任务（复杂任务级联停子任务） |
| `DELETE` | `/api/v1/tasks/{id}` | **仅所有者** | 软删除（级联删子任务并释放会话保留） |

### 2.8 自动化调度与 Webhook 触发（11 个）

| Method | Path | 鉴权 | 关键入参 / 出参与说明 |
|---|---|---|---|
| `GET` | `/api/v1/automations` | 已登录（管理员看全公司） | 自动化列表 |
| `POST` | `/api/v1/automations` | 已登录 | 创建（`type=api` 响应中返回一次明文 `apiToken`） |
| `POST` | `/api/v1/automations/preview` | 已登录 | 预览草稿排期未来 3 次触发时刻 |
| `GET` | `/api/v1/automations/by-task/{taskId}` | 已登录 | 按生成任务反查所属自动化 |
| `GET` | `/api/v1/automations/{id}` | 所有者或管理员 | 详情 + 最近 20 次运行 + 未来 3 次排期 |
| `PUT` | `/api/v1/automations/{id}` | 所有者或管理员 | 增量更新配置 |
| `POST` | `/api/v1/automations/{id}/enabled` | 所有者或管理员 | `?value=true\|false` 启停开关 |
| `POST` | `/api/v1/automations/{id}/run` | 所有者或管理员 | 立即手动触发 |
| `POST` | `/api/v1/automations/{id}/token` | 所有者或管理员 | 轮换 API 触发令牌（明文仅返回一次） |
| `DELETE` | `/api/v1/automations/{id}` | 所有者或管理员 | 删除定义（保留历史运行与任务） |
| `POST` | `/api/v1/automations/{id}/trigger` | **Webhook 令牌头** | 头 `X-Automation-Token: <company>.<64-hex>` + 可选 `{input}`；常量时间校验 |

### 2.9 项目、工作文件、目录与文件存储管理（26 个）

- **项目（9 个）**：`GET/POST /api/v1/projects`、`GET/PUT /api/v1/projects/{id}`、`POST /api/v1/projects/{id}/archive`、`POST /api/v1/projects/{id}/reopen`、`POST /api/v1/projects/{id}/members`、`DELETE /api/v1/projects/{id}/members/{username}`、`PUT /api/v1/agents/{agentId}/project`。
- **个人目录（5 个）**：`GET/POST /api/v1/files/folders`、`PUT/DELETE /api/v1/files/folders/{id}`、`PUT /api/v1/files/{id}/folder`。
- **工作文件（9 个）**：`GET /api/v1/files?agent=&task=&project=`、`GET /api/v1/files/{id}`、`GET /api/v1/files/{id}/content?download=false`（支持 `Range` 与安全头）、`POST /api/v1/files/uploads`（Multipart ≤50MB）、`POST /api/v1/files/markdown`、`PUT /api/v1/files/{id}/content`、`PUT /api/v1/files/{id}`（重命名）、`PUT /api/v1/files/{id}/tags`、`DELETE /api/v1/files/{id}`。
- **文件存储后台（3 个，`ADMIN`）**：`GET/PUT /api/v1/admin/file-storage`、`POST /api/v1/admin/file-storage/test`（写入/回读/删除实测探活）。

### 2.10 技能、连接器、OAuth、模型供应商、价格、设置与集成（52 个）

- **技能与删除确认（11 个）**：`GET/POST /api/v1/skills`、`GET/PUT/DELETE /api/v1/skills/{id}`、`POST/GET/DELETE /api/v1/skills/{id}/images[/{name}]`、`GET /api/v1/skills/deletions?agentId=`、`POST /api/v1/skills/deletions/{deletionId}/(confirm|cancel)`。
- **连接器与 OAuth（11 个）**：`GET /api/v1/connectors`、`GET /api/v1/connectors/{id}/tools?refresh=`、`POST /api/v1/connectors` (`ADMIN`)、`PUT/DELETE /api/v1/connectors/{id}` (`ADMIN`)、`PUT /api/v1/connectors/{id}/enabled` (`ADMIN`)、`PUT/DELETE /api/v1/connectors/{id}/token`、`POST /api/v1/connectors/{id}/bind-code`、`GET /api/v1/oauth/{provider}/(authorize|callback)`（`302` 跳转）。
- **模型供应商、私有 Node、密钥池与定价（23 个，全部 `ADMIN`）**：
  - `GET/POST /api/v1/providers`、`GET /api/v1/providers/catalog`、`GET /api/v1/providers/node`、`POST /api/v1/providers/node/install`、`GET /api/v1/providers/node/install-log`；
  - `GET/DELETE /api/v1/providers/{id}`、`POST /api/v1/providers/{id}/(install|upgrade|models/refresh|check-latest)`、`GET /api/v1/providers/{id}/install-log`；
  - `GET/POST /api/v1/providers/{id}/credentials`、`PUT/DELETE /api/v1/providers/{id}/credentials/{cid}`、`POST /api/v1/providers/{id}/credentials/{cid}/check`、`PUT /api/v1/providers/{id}/credentials/order`；
  - `GET /api/v1/admin/model-prices`、`POST /api/v1/admin/model-prices/refresh`、`PUT /api/v1/admin/model-prices/price?model=`、`PUT /api/v1/admin/model-prices/tasks?model=`。
- **系统设置、品牌 Logo 与企业集成（7 个）**：`GET /api/v1/settings`（已登录）、`PUT /api/v1/settings` (`ADMIN`)、`GET/PUT/DELETE /api/v1/settings/logo` (`ADMIN`)、`GET /api/v1/admin/integrations` (`ADMIN`)、`PUT /api/v1/admin/integrations/{key}` (`ADMIN`)。

### 2.11 收件箱、通知中心、用量、分析与内置 MCP（16 个）

- **收件箱（5 个）**：`GET /api/v1/inbox/summary`、`GET /api/v1/inbox/events?limit=200`、`GET /api/v1/inbox/todos`、`PUT /api/v1/inbox/todos/{id}`、`POST /api/v1/inbox/todos/{id}/task`。
- **通知中心（6 个）**：`GET /api/v1/notices`、`GET /api/v1/notices/stats`、`POST /api/v1/notices/{id}/(done|ignore|reopen)`、`POST /api/v1/notices/read-all`。
- **用量与运营分析（3 个）**：`GET /api/v1/usage?period=&tz=`、`GET /api/v1/admin/usage?period=&tz=` (`ADMIN`)、`GET /api/v1/admin/analytics?days=14` (`ADMIN`)。
- **内置 MCP 协议端点（2 个）**：`POST /mcp`（Bearer 令牌鉴权 JSON-RPC 2.0）、`GET /mcp`（固定返回 `405`）。

> **参考实现预留未接线端点说明**：路径常量 `/api/v1/admin/org` 与只读组织服务虽已定义但无 HTTP 控制器暴露；认证白名单预留了 `/api/v1/auth/phone/**`、`/api/v1/auth/wechat/**`、`/oauth2/**`、`/login/oauth2/**`，但当前尚未实现对应登录处理器。

---

## 3. 内置 MCP 工具全量跨模块总索引（20 提供方）

所有工具均通过 `POST /mcp` 暴露，在 CLI 中以 `mcp__superagent__<工具名>` 形式授权与调用：

| # | 提供方 | 归属域 | `CHAT` | `TASK` | `SUPER` | 暴露工具数与核心清单 |
|---|---|---|:---:|:---:|:---:|---|
| 1 | `AgentCollabMcpTools` | Agent 协作 | ✓ | — | — | 4：`collab_find_people`, `collab_list_members`, `collab_invite`, `collab_remove` |
| 2 | `AgentCreateMcpTools` | Agent 管理 | ✓ | — | — | 1：`agent_create` |
| 3 | `AgentUpdateMcpTools` | Agent 管理 | ✓ | — | — | 1：`agent_update` |
| 4 | `AgentSkillMcpTools` | Agent 技能 | ✓ | — | ✓*(仅对话)* | 2：`skill_attach`, `skill_detach` |
| 5 | `ChatHistoryMcpTools` | 对话回溯 | ✓ | ✓ | — | 2：`chat_history_search`, `chat_history_read` |
| 6 | `TaskMcpTools` | 异步任务 | ✓ | — | — | 6：`create_task`, `list_tasks`, `get_task`, `send_task_message`, `stop_task`, `delete_task` |
| 7 | `ComplexTaskMcpTools` | 复杂子任务 | — | ✓*(仅COMPLEX)* | — | 3：`create_subtask`, `list_subtasks`, `subtask_output` |
| 8 | `AutomationMcpTools` | 自动化 | ✓ | — | — | 8：`create/list/get/update/set_enabled/run_now/delete_automation`, `get_current_time` |
| 9 | `FileMcpTools` | 文件交付 | ✓ | ✓ | — | 4：`file_write`, `file_save`, `file_get`, `file_list` |
| 10 | `FileFolderMcpTools` | 文件目录 | ✓ | ✓ | ✓*(仅对话)* | 7：`file_find`, `file_folders`, `file_move`, `file_create/rename/move/delete_folder` |
| 11 | `FileManageMcpTools` | 文件治理 | — | — | ✓*(仅对话)* | 4：`file_read`, `file_rename`, `file_set_tags`, `file_delete` |
| 12 | `SkillMcpTools` | 技能库 | ✓ | ✓*(禁删)* | ✓*(仅对话)* | 6：`skill_list`, `skill_get`, `skill_create`, `skill_update`, `skill_add_image`, `skill_delete` |
| 13 | `FeishuDocTools` | 飞书文档 | ✓ | ✓ | ✓*(对话只读)* | 6：`feishu_doc_search/list/read/create/append/replace` |
| 14 | `DingTalkDocTools` | 钉钉文档 | ✓ | ✓ | ✓*(对话只读)* | 5：`dingtalk_doc_spaces/list/read/create/replace` |
| 15 | `ReadLinkTools` | 链接阅读 | ✓ | ✓ | ✓ | 1：`read_link` |
| 16 | `InboxMcpTools` | 收件箱分诊 | — | — | ✓ | 6：`inbox_search`, `inbox_add_todo`, `inbox_notify`, `inbox_mark`, `todo_list`, `todo_update` |
| 17 | `SuperAgentTools` | 主管编排 | — | — | ✓ | 13：`list_team/projects/skills/assigned_tasks`, `propose_plan`, `hire/update/place_member`, `assign/get/reply_task`, `get_current_time`, `tell_user` |
| 18 | `SuperAgentTaskTools` | 主管快任务 | — | — | ✓*(仅对话)* | 1：`start_task` |
| 19 | `SuperAgentAutomationTools` | 主管自动化 | — | — | ✓*(仅对话)* | 7：复用自动化增删改查启停与立即运行（去重 `get_current_time`） |
| 20 | `SuperAgentCollabTools` | 主管协作 | — | — | ✓*(仅对话)* | 4：`collab_find_people`, `collab_list_members`, `collab_invite`, `collab_remove` |
