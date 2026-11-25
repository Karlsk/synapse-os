# spec 04 | 平台治理底座（租户、认证会话、用户、组织、系统设置、品牌、通知与分析）

- **状态**：`draft`
- **篇号顺序**：04（L2 平台治理首篇；依赖 L1 Core/Storage，与 `spec_05` / `spec_06` 并行推进）
- **对应 PR**：[../README.md](../README.md) §3 看板中的 `SA-04`
- **上位契约**：
  - [../../01_Go工程架构_目录结构与核心底座设计.md](../../01_Go工程架构_目录结构与核心底座设计.md) §1–§3、§5
  - [../../02_PostgreSQL存储层与Store接口设计.md](../../02_PostgreSQL存储层与Store接口设计.md) §1–§4
  - [../../05_前端兼容策略_部署运维与分期研发实施路线.md](../../05_前端兼容策略_部署运维与分期研发实施路线.md) §1.1、§2.1、§3（M0/M3）
  - [../README.md](../README.md) §1（V1 商业私有化裁剪红线）
- **前置依赖**：
  - `L1/spec_01_core_tenant_crypto_egress.md`（`tenant.WithCompany`/`Require`/`Optional`/`Path`、`apperror`、`eventbus`、`spi`、`domain`、`obs`）
  - `L1/spec_02_storage_postgres_migrations.md`（`TxManager`、`CompanyStore`、`SessionStore`、`UserStore`、`OrgStore`、`SystemSettingStore`、`NoticeStore`、`UsageStore`、`AgentStore`、`TaskStore`、`AutomationStore`、`InboxTodoStore`、`EventSourceStore`）
  - `L1/spec_03_runtime_launcher_claude_codex_mcp.md`（仅复用 `internal/runtime/attachment.Sniff` 光栅图魔数嗅探）
- **下游消费者**：
  - `L2/spec_05_platform_provider_node_credentials_connectors.md`（消费 `CompanyService`、`SettingsService.PublicURL`）
  - `L2/spec_06_assets_files_projects_skills.md`（消费 `PeopleDirectory`、`CompanyStarted`）
  - `L3/spec_07_cost_catalog_router_usage.md`（消费 `SettingsService`、`CompanyService.ForEachEnabled`、`NoticeEvents.DefaultModelChanged`）
  - `L3/spec_09_agent_chat_liveturn_memory_collab.md` & `L3/spec_10_task_codingflow_pool_watchdog_automation.md`（消费 `QuotaService`、`CompanyService.ForEachEnabled`、`SettingsService`、`PeopleDirectory`、`NoticeEvents`）
  - `L4/spec_11_event_feishu_dingtalk_inbox_triage.md` & `L4/spec_12_orchestrator_superagent_dual_half.md`（消费 `CompanyService`、`OrgService`、`NoticeEvents`）
  - `L5/spec_13_web_gateway_mcp_endpoint_ui_embed.md`（挂接 Gin 中间件链与全量 REST Handler）
  - `L6/spec_14_server_bootstrap_lifecycle_docker_delivery.md`（装配 `bootstrap-org` CLI、`wire.go` 与后台协程）

---

## 1. 背景与范围

本篇交付 `synapse-server`（`module github.com/Karlsk/synapse-os`，Go `1.26.1` + Gin + PostgreSQL 17 `pgx/v5` + `goose`）的 **L2 平台治理核心域**，涵盖多租户生命周期、商业私有化组织初始化、可插拔认证与 PG17 持久化会话、用户预置管理、自助账号/头像、同事通讯录、只读组织架构与团队路径、租户配额守卫、系统设置与企业品牌 Logo、事件驱动待办通知中心，以及管理员平台统计分析。

### 1.1 Java 原版参考源码精确映射

| Go 目标子包 / 文件 | 对应 Java 原版源码（`/Users/gaorenjie/Documents/Coding/aiops-workspace/orgin/SuperAgent/`） | 行号范围 | V1 重构 / 裁剪要点 |
|---|---|---|---|
| `internal/platform/tenant/company.go`<br>`internal/platform/tenant/fair_ticker.go` | `superagent-platform/.../platform/tenant/CompanyService.java`<br>`superagent-core/.../core/tenant/Company.java`<br>`superagent-core/.../core/tenant/CompanyStarted.java` | `CompanyService.java:26-87`<br>`Company.java:10-35`<br>`CompanyStarted.java` | 1:1 保留 `ForEachEnabled` 轮转公平遍历与异常/panic 隔离；`CompanyStarted` 走进程内 `eventbus.Bus` |
| `internal/platform/tenant/bootstrap.go`<br>`internal/platform/tenant/license_hook.go` | `superagent-platform/.../platform/tenant/OrganizationService.java`<br>`superagent-web/.../web/org/OrgController.java` | `OrganizationService.java:27-134`<br>`OrgController.java:13-32` | **V1 商业化裁剪**：砍掉公网自助注册（`GET /api/v1/orgs/registration` 恒返 `open:false`，不暴露公网 `POST /api/v1/orgs`）；改为 `BootstrapProvisioner`（`bootstrap-org` CLI + 超管 Token API），保留 14 位无歧义随机密文、12 保留词、`must_change_password=true`，新增 `LicenseQuotaHook` SPI |
| `internal/platform/tenant/quota.go` | `superagent-platform/.../platform/tenant/QuotaService.java`<br>`superagent-platform/.../platform/tenant/QuotaExceededException.java` | `QuotaService.java:23-94`<br>`QuotaExceededException.java:6-11` | 保留 3 项配额覆盖优先级与 UTC 自然月 1 分钟缓存；PG17 侧改用 SQL `SUM(...)` 聚合避免全量拉明细进内存 |
| `internal/platform/auth/authenticator.go`<br>`internal/platform/auth/local.go`<br>`internal/platform/auth/session.go`<br>`internal/platform/auth/service.go` | `superagent-web/.../web/auth/SecurityConfig.java`<br>`superagent-web/.../web/auth/OrgLoginFilter.java`<br>`superagent-web/.../web/auth/AppUserDetailsService.java`<br>`superagent-web/.../web/auth/TenantFilter.java`<br>`superagent-web/.../web/auth/PasswordChangeFilter.java`<br>`superagent-web/.../web/auth/TenantUser.java`<br>`superagent-web/.../web/auth/AuthController.java` | `SecurityConfig.java:41-131`<br>`OrgLoginFilter.java:16-38`<br>`AppUserDetailsService.java:21-63`<br>`TenantFilter.java:19-38`<br>`PasswordChangeFilter.java:21-58`<br>`TenantUser.java:12-42`<br>`AuthController.java:26-56` | 抽象 `spi.Authenticator`（V1 `LocalAuthenticator` BCrypt，V2 零侵入接 OIDC/LDAP）；新增 PG17 `user_session` 替代 JVM 内存 `HttpSession`；停用/删号/重置密码同步吊销该用户会话；登录失败常量时间防枚举 |
| `internal/platform/user/user.go` | `superagent-platform/.../platform/user/UserService.java`<br>`superagent-platform/.../platform/user/UserNotFoundException.java`<br>`superagent-platform/.../platform/user/UsernameTakenException.java`<br>`superagent-core/.../core/user/User.java`<br>`superagent-web/.../web/user/UserController.java` | `UserService.java:29-156`<br>`UserNotFoundException.java:6-10`<br>`UsernameTakenException.java:6-10`<br>`User.java:19-52`<br>`UserController.java:20-48` | 保留唯一管理员删除/停用/降权守卫、`max_users` 配额校验；预留 `spi.OwnerTransferHook`（停用/删除触发）并同步吊销会话 |
| `internal/platform/user/account.go` | `superagent-platform/.../platform/user/AccountService.java`<br>`superagent-web/.../web/account/AccountController.java` | `AccountService.java:31-153`<br>`AccountController.java:21-58` | 保留最小 8 位且不得等于旧密码校验、清除 `must_change_password`、1 MB 光栅魔数嗅探头像与 SHA-256 原子落盘 |
| `internal/platform/user/directory.go` | `superagent-platform/.../platform/user/PeopleDirectory.java`<br>`superagent-web/.../web/user/DirectoryController.java` | `PeopleDirectory.java:17-48`<br>`DirectoryController.java:19-35` | 1:1 保留同事目录查询与 `Resolve(idOrUsername)` 双向解析 |
| `internal/platform/user/org.go`<br>`internal/platform/user/team_paths.go` | `superagent-platform/.../platform/org/OrgService.java`<br>`superagent-core/.../core/org/TeamPaths.java` | `OrgService.java:20-54`<br>`TeamPaths.java:7-59` | 1:1 保留只读组织概览与 8 层 × 64 字符团队路径校验/祖先展开；注：`ApiPaths.ORG` 暂不对公网开放写入口，留待 V2 目录同步 |
| `internal/platform/settings/settings.go` | `superagent-platform/.../platform/settings/SettingsService.java`<br>`superagent-web/.../web/settings/SettingsController.java`<br>`superagent-api/.../api/settings/SystemSettingsDto.java` | `SettingsService.java:12-244`<br>`SettingsController.java:23-72`<br>`SystemSettingsDto.java:16-60` | 全字段原子校验 + `WithinTx` 写入；通过窄接口 `ModelChecker` 解耦同层 `platform/provider`；保留全量系统键与 DTO 兼容 |
| `internal/platform/settings/branding.go` | `superagent-platform/.../platform/settings/BrandingService.java`<br>`superagent-web/.../web/settings/SettingsController.java` | `BrandingService.java:25-99`<br>`SettingsController.java:43-71` | 1 MB 光栅魔数嗅探原子落盘 + `//go:embed` 默认 PNG 回退 + `nosniff`/`no-cache`/`ETag` |
| `internal/platform/notice/notice.go` | `superagent-platform/.../platform/notice/NoticeService.java`<br>`superagent-platform/.../platform/notice/NoticeNotFoundException.java`<br>`superagent-core/.../core/notice/Notice.java`<br>`superagent-core/.../core/notice/NoticeEvents.java`<br>`superagent-web/.../web/notice/NoticeController.java` | `NoticeService.java:50-325`<br>`NoticeNotFoundException.java:6-10`<br>`Notice.java:11-50`<br>`NoticeEvents.java:7-48`<br>`NoticeController.java:20-59` | 完整订阅 5 类领域事件 + `CompanyStarted` 一次性回填 + 发布 `NoticeIgnored`；PG17 `ON CONFLICT (company_id, owner, notice_key)` 消除并发 Upsert 冲突 |
| `internal/platform/analytics/analytics.go` | `superagent-platform/.../platform/analytics/AnalyticsService.java`<br>`superagent-web/.../web/analytics/AnalyticsController.java` | `AnalyticsService.java:32-108`<br>`AnalyticsController.java:13-25` | 1:1 保留 7–90 天日趋势、7 日活跃任务发起人、任务成功率、`decimal.Decimal` 费用汇总与按模型聚合 |

### 1.2 本篇边界

- **本篇负责**：上述 L2 平台域的全部领域服务、业务校验、并发策略、SPI 实现与单元测试，以及对应 DTO/HTTP/SQL 契约冻结。
- **留给后续篇**：
  - `L2/spec_05`：模型供应商、密钥池、私有 Node 运行时、飞书/钉钉集成与外部 MCP 连接器。
  - `L3/spec_07`：Token 级账单明细（`/api/v1/usage`、`/api/v1/admin/usage`）与价格目录。
  - `L5/spec_13`：Gin 路由挂载、6 级中间件装配、HTTP Handler 胶水层。
  - `L6/spec_14`：`cmd/synapse-server` CLI 子命令入口（`bootstrap-org` 参数解析）与 `wire.go` 依赖注入。

---

## 2. 本篇交付物表

| # | 交付物 | 落位（相对 `synapse-server/`） | 类型 |
|---|---|---|---|
| 1 | `CompanyService`（租户启停、查询、新建、`ForEachEnabled` 公平轮转） | `internal/platform/tenant/company.go` | 新增 |
| 2 | `FairTicker`（带 `context.Context` 取消与 panic 隔离的后台周期轮转辅助器） | `internal/platform/tenant/fair_ticker.go` | 新增 |
| 3 | `BootstrapProvisioner`（替代公网注册：slug/保留词校验、14 位随机密码、初创管理员、`LicenseQuotaHook`） | `internal/platform/tenant/bootstrap.go` | 新增 |
| 4 | `QuotaService`（3 维配额优先级解析 + UTC 月度 Token 1m 缓存） | `internal/platform/tenant/quota.go` | 新增 |
| 5 | `LocalAuthenticator`（实现 `spi.Authenticator`，BCrypt + 防时序枚举） | `internal/platform/auth/local.go` | 新增 |
| 6 | `SessionManager`（PG17 `user_session` 创建/验证/滑动续期/`password_ok` 缓存/按用户批量吊销/过期清理） | `internal/platform/auth/session.go` | 新增 |
| 7 | `AuthService`（多 Authenticator 路由、`Login`/`Logout`/`ResolveSession`/`MustChangePassword`/`Me`） | `internal/platform/auth/service.go` | 新增 |
| 8 | `UserService`（CRUD、唯一管理员守卫、`max_users` 配额、预留 `spi.OwnerTransferHook` + 会话吊销） | `internal/platform/user/user.go` | 新增 |
| 9 | `AccountService`（自助改密清除强制标志 + 1 MB 光栅头像 SHA-256 原子落盘） | `internal/platform/user/account.go` | 新增 |
| 10 | `PeopleDirectory`（同事目录、`Resolve(idOrUsername)` 双向查找） | `internal/platform/user/directory.go` | 新增 |
| 11 | `OrgService` & `TeamPaths`（只读汇报线/团队树与 8 层路径规范化） | `internal/platform/user/org.go`<br>`internal/platform/user/team_paths.go` | 新增 |
| 12 | `SettingsService`（全量系统设置读写、全字段原子校验 + 事务写入、`ModelChecker` 窄接口） | `internal/platform/settings/settings.go` | 新增 |
| 13 | `BrandingService`（租户自定义 Logo 1 MB 光栅原子存储 + 内嵌默认 PNG 回退 + ETag） | `internal/platform/settings/branding.go`<br>`internal/platform/settings/assets/superagent-logo.png` | 新增 |
| 14 | `NoticeService`（待办列表/统计/状态变更 + 5 类领域事件监听 + `CompanyStarted` 回填 + `NoticeIgnored` 发布） | `internal/platform/notice/notice.go` | 新增 |
| 15 | `AnalyticsService`（7–90d 日趋势、7d 活跃发起人、成功率、模型维度聚合） | `internal/platform/analytics/analytics.go` | 新增 |
| 16 | Core 契约补全：`spi.Authenticator`、`spi.OwnerTransferHook`、`spi.LicenseQuotaHook`、`spi.ModelChecker` | `internal/core/spi/platform.go` | 修改/确认 |
| 17 | 全包单元测试套件（零真实网络，使用内存 Fake Store / PG 测试实例） | `internal/platform/{tenant,auth,user,settings,notice,analytics}/*_test.go` | 新增 |

---

## 3. 数据库设计

本篇直接读写以下 **7 张核心表**（DDL 由 `L1/spec_02` 的 `goose` 迁移统一创建，本节冻结字段、索引与读写语义；全部时间列统一 `TIMESTAMPTZ NOT NULL` UTC 写入，零物理外键）。

### 3.1 `company`（全局表，无 `company_id` 前缀，落位 `00001_init_tenant_and_auth.sql`）

```sql
CREATE TABLE company (
    id                VARCHAR(64)  PRIMARY KEY,
    name              VARCHAR(255) NOT NULL,
    enabled           BOOLEAN      NOT NULL DEFAULT TRUE,
    data_key_cipher   TEXT,
    max_users         INTEGER,               -- NULL=沿用全局配置，<=0=不限
    max_running_tasks INTEGER,               -- NULL=沿用全局配置，<=0=不限
    monthly_tokens    BIGINT,                -- NULL=沿用全局配置，<=0=不限
    created_at        TIMESTAMPTZ  NOT NULL,
    updated_at        TIMESTAMPTZ  NOT NULL
);
```
- **永不删表**：丢失 `data_key_cipher` 将导致租户全部加密密钥不可解。

### 3.2 `user_session`（V1 新增全局持久化会话表，落位 `00001_init_tenant_and_auth.sql`）

```sql
CREATE TABLE user_session (
    id          VARCHAR(128) PRIMARY KEY,    -- 32 字节加密安全随机 hex（64 字符）
    company_id  VARCHAR(64)  NOT NULL,       -- 登录归属租户
    user_id     VARCHAR(64)  NOT NULL,       -- 不可变 app_user.id（UUID）
    password_ok BOOLEAN      NOT NULL DEFAULT FALSE, -- 缓存强制改密检查通过状态
    expires_at  TIMESTAMPTZ  NOT NULL,       -- 过期时间（默认 7 天滑动或可配 TTL）
    created_at  TIMESTAMPTZ  NOT NULL,
    updated_at  TIMESTAMPTZ  NOT NULL
);
CREATE INDEX idx_user_session_expires ON user_session (expires_at);
CREATE INDEX idx_user_session_user    ON user_session (company_id, user_id);
```
- **架构守卫豁免**：`guard_test.go` 中将 `company`、`user_session`、`goose_db_version` 列为 3 张全局表；但 `user_session` 必须按 `(company_id, user_id)` 建索引以支持停用/删号/改密时精确清理单租户用户会话。

### 3.3 `app_user`（租户表，落位 `00001_init_tenant_and_auth.sql`）

```sql
CREATE TABLE app_user (
    company_id           VARCHAR(64)  NOT NULL,
    id                   VARCHAR(64)  NOT NULL,  -- 不可变 UUID，全系统所有权主键
    username             VARCHAR(190) NOT NULL,  -- 登录名，租户内唯一
    display_name         VARCHAR(255) NOT NULL DEFAULT '',
    roles                VARCHAR(512) NOT NULL DEFAULT 'USER', -- 逗号分隔裸角色名（USER,ADMIN）
    enabled              BOOLEAN      NOT NULL DEFAULT TRUE,
    password_hash        VARCHAR(255) NOT NULL DEFAULT '',     -- BCrypt 哈希，非 LOCAL 源为空
    phone                VARCHAR(32)  NOT NULL DEFAULT '',
    external_id          VARCHAR(255) NOT NULL DEFAULT '',
    source               VARCHAR(16)  NOT NULL DEFAULT 'LOCAL', -- LOCAL | PHONE | SSO | WECHAT
    must_change_password BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at           TIMESTAMPTZ  NOT NULL,
    updated_at           TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE UNIQUE INDEX uq_app_user_username ON app_user (company_id, username);
CREATE INDEX idx_app_user_phone          ON app_user (company_id, phone) WHERE phone <> '';
CREATE INDEX idx_app_user_external       ON app_user (company_id, external_id) WHERE external_id <> '';
```
- **保存铁律（对标原版 MySQL 注释）**：严禁使用 `ON CONFLICT (company_id, username) DO UPDATE`，必须按 `(company_id, id)` 先 `UPDATE`、影响行数为 0 再 `INSERT`，确保重名 `username` 触发唯一键冲突而绝不覆写他人账号。

### 3.4 `user_manager` 与 `user_team`（租户表，落位 `00001_init_tenant_and_auth.sql`）

```sql
CREATE TABLE user_manager (
    company_id VARCHAR(64)  NOT NULL,
    username   VARCHAR(190) NOT NULL, -- 实际存 app_user.id（沿用原表列名）
    manager    VARCHAR(190) NOT NULL, -- 主管 app_user.id
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, username)
);

CREATE TABLE user_team (
    company_id VARCHAR(64)  NOT NULL,
    username   VARCHAR(190) NOT NULL, -- app_user.id
    team_path  VARCHAR(514) NOT NULL, -- TeamPaths.Normalize 后的路径（最多 8 段 × 64 字符 + 7 斜杠）
    PRIMARY KEY (company_id, username, team_path)
);
CREATE INDEX idx_user_team_path ON user_team (company_id, team_path);
```

### 3.5 `notice`（租户表，落位 `00004_init_superagent_and_inbox.sql`）

```sql
CREATE TABLE notice (
    company_id VARCHAR(64)  NOT NULL,
    id         VARCHAR(64)  NOT NULL,
    owner      VARCHAR(128) NOT NULL, -- 接收人 app_user.id
    notice_key VARCHAR(200) NOT NULL, -- 去重逻辑键：task:<id> | plan:<owner> | todo:<id> | shared:<agentId> | default-model
    kind       VARCHAR(16)  NOT NULL, -- CONFIRM | ACTION | DONE | NOTICE | TRACK
    state      VARCHAR(16)  NOT NULL, -- OPEN | DONE | IGNORED
    title      VARCHAR(500) NOT NULL,
    body       TEXT         NOT NULL DEFAULT '',
    actor      VARCHAR(255) NOT NULL DEFAULT '',
    link_type  VARCHAR(32)  NOT NULL DEFAULT '', -- task | agent | superagent | todo | ''
    link_id    VARCHAR(128) NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ  NOT NULL,
    updated_at TIMESTAMPTZ  NOT NULL,
    PRIMARY KEY (company_id, id)
);
CREATE UNIQUE INDEX uk_notice_key   ON notice (company_id, owner, notice_key);
CREATE INDEX idx_notice_owner       ON notice (company_id, owner, updated_at DESC);
```

### 3.6 `system_setting`（租户表，落位 `00005_init_assets_and_platform.sql`）

```sql
CREATE TABLE system_setting (
    company_id    VARCHAR(64)   NOT NULL,
    setting_key   VARCHAR(128)  NOT NULL,
    setting_value VARCHAR(4096) NOT NULL, -- 空值直接 DELETE 行或置 ''
    updated_at    TIMESTAMPTZ   NOT NULL,
    PRIMARY KEY (company_id, setting_key)
);
```

#### 本篇维护与全库 `system_setting` 键全集说明（V1 裁剪后）

| Key / 模式 | 归属模块 | 格式与约束 | 默认值 / 说明 |
|---|---|---|---|
| `agent.default-model` | `SettingsService` | 模型 ID | 空（沿用 CLI 默认/最强可用模型） |
| `agent.backup-models` | `SettingsService` | 逗号分隔模型 ID 列表 | 空；对话容灾有序备选 |
| `company.name` | `SettingsService` | `<= 100` 字符 | 回退至 `company.name` |
| `company.short-name` | `SettingsService` | `<= 20` 字符 | 空 |
| `app.public-url` | `SettingsService` | `http(s)://host[:port][/path]`（去末尾 `/`、禁 query/fragment/userinfo） | 空（回退 `config.app.base_url` 或请求头） |
| `task.complex.max-subtasks` | `SettingsService` | 整数 `[1, 50]` | `10` |
| `task.complex.max-parallel` | `SettingsService` | 整数 `[1, 20]` | `5` |
| `task.fixed-models.on` | `SettingsService` | `"true"` 或删键 | `false` |
| `task.fixed-models` | `SettingsService` | 逗号分隔模型 ID 列表 | 空；开启开关时非空必填 |
| `codebuddy.internet-environment` | `SettingsService`（V1 兼容透传） | `internal\|public\|ioa\|cloudhosted\|selfhosted` | `"internal"`（V1 仅 GA Claude/Codex，保留此键防前端旧字段报错） |
| `notice.backfilled` | `NoticeService` | `"true"` | `CompanyStarted` 一次性回填幂等标记 |
| *(其余模块键)* | `spec_05`/`spec_06`/`spec_07` | `integration.{feishu,dingtalk}.*`、`connector.seeded.*`、`files.storage.*`、`cost.{price,fact,task-off}.*` | 详见对应篇（注意 V1 已删 `skill.seeded` 与 GitHub/Gitee/Notion/Confluence 键） |

### 3.7 磁盘资产目录（租户隔离，经 `tenant.Path` 校验）

- 头像：`<avatars_dir>/<companyId>/<sha256_hex(userId)>`（默认 `./data/avatars`）
- 企业 Logo：`<branding_dir>/<companyId>/logo`（默认 `./data/branding`）

---

## 4. 冻结契约

### 4.1 导出符号（Go 结构体、SPI 接口与服务签名）

#### A. L0 跨模块 SPI（`internal/core/spi/platform.go`）

```go
package spi

import "context"

// AuthPrincipal 认证通过后的不可变身份主体（UserID 为全局唯一 UUID，严禁用作 loginName 混淆）。
type AuthPrincipal struct {
	UserID             string
	CompanyID          string
	Username           string
	DisplayName        string
	Roles              []string // 裸角色名，始终含 "USER"，管理员含 "ADMIN"
	MustChangePassword bool
}

// Credentials 多认证源统一凭据（V1 仅使用 Kind="local"，V2 扩展 "oidc"/"ldap"）。
type Credentials struct {
	Kind     string // "local" | "oidc" | "ldap"
	Org      string
	Username string
	Password string
	Token    string // OIDC ID Token / LDAP Bind 扩展位
}

// Authenticator 可插拔认证器 SPI（对标 Java AppUserDetailsService，V2 零侵入接 OIDC/LDAP）。
type Authenticator interface {
	Kind() string
	Authenticate(ctx context.Context, cred Credentials) (*AuthPrincipal, error)
}

// LicenseQuotaHook 商业私有化 License 席位/租户上限预留钩子（V1 默认 NoopLicenseHook）。
type LicenseQuotaHook interface {
	CheckCanProvisionOrg(ctx context.Context, currentOrgs int) error
	CheckCanAddUser(ctx context.Context, companyID string, companyUsers int, totalUsers int) error
}

// OwnerTransferHook 员工停用/删除时的资产交接预留钩子（V1 默认 NoopOwnerTransferHook，V2 接离职交接）。
type OwnerTransferHook interface {
	OnUserDisabled(ctx context.Context, userID string) error
	OnUserDeleted(ctx context.Context, userID string) error
}

// ModelChecker SettingsService 用于校验模型是否已通过推理探活的窄接口（由 L2 provider.ProviderService 满足，避免同层循环依赖）。
type ModelChecker interface {
	HasRunnableModel(ctx context.Context, modelID string) bool
}
```

#### B. 多租户、初始化与配额服务（`internal/platform/tenant`）

```go
package tenant

// CompanyService 管理多租户生命周期与后台公平轮转。
type CompanyService struct { /* companies store.CompanyStore, bus eventbus.Bus, turn atomic.Uint64 */ }

func NewCompanyService(companies store.CompanyStore, bus eventbus.Bus) *CompanyService
func (s *CompanyService) StartAll(ctx context.Context) error
func (s *CompanyService) EnabledCompany(ctx context.Context, org string) (string, bool, error)
func (s *CompanyService) Exists(ctx context.Context, id string) (bool, error)
func (s *CompanyService) Create(ctx context.Context, id, name string, quotas *QuotaOverrides) (*domain.Company, error)
func (s *CompanyService) SetEnabled(ctx context.Context, id string, enabled bool) (*domain.Company, error)
func (s *CompanyService) UpdateQuotas(ctx context.Context, id string, q QuotaOverrides) (*domain.Company, error)
func (s *CompanyService) EnabledIDs(ctx context.Context) ([]string, error)
func (s *CompanyService) ForEachEnabled(ctx context.Context, work func(ctx context.Context) error)

type QuotaOverrides struct {
	MaxUsers        *int
	MaxRunningTasks *int
	MonthlyTokens   *int64
}

// ProvisionOrgRequest 替代原公网自注册：仅由 bootstrap-org CLI 或超管 API 调用。
type ProvisionOrgRequest struct {
	ID            string          // 组织英文短名，^[a-z][a-z0-9-]{1,30}[a-z0-9]$
	Name          string          // 组织名称，1..64 runes
	AdminUsername string          // 可选，留空默认等于 ID
	Quotas        *QuotaOverrides // 可选单租户配额覆盖
}

type ProvisionedOrg struct {
	Org      string `json:"org"`
	Name     string `json:"name"`
	Username string `json:"username"`
	Password string `json:"password"` // 14 位一次性明文密码，永不落库
}

type BootstrapProvisioner struct { /* companies *CompanyService, users store.UserStore, tx store.TxManager, license spi.LicenseQuotaHook, mu sync.Mutex */ }
func NewBootstrapProvisioner(companies *CompanyService, users store.UserStore, tx store.TxManager, license spi.LicenseQuotaHook) *BootstrapProvisioner
func (p *BootstrapProvisioner) ProvisionOrg(ctx context.Context, req ProvisionOrgRequest) (*ProvisionedOrg, error)

// QuotaService 三维配额校验与 UTC 月度 Token 1m 缓存。
type QuotaLimits struct {
	MaxUsers        int
	MaxRunningTasks int
	MonthlyTokens   int64
}
type QuotaService struct { /* companies store.CompanyStore, usage store.UsageStore, defaults QuotaLimits, cache sync.Map */ }
func NewQuotaService(companies store.CompanyStore, usage store.UsageStore, defaults QuotaLimits) *QuotaService
func (q *QuotaService) Limits(ctx context.Context) (QuotaLimits, error)
func (q *QuotaService) CheckCanAddUser(ctx context.Context, currentUsers int) error
func (q *QuotaService) CanStartTask(ctx context.Context, runningTasks int) (bool, error)
func (q *QuotaService) CheckTokens(ctx context.Context) error
func (q *QuotaService) TokensThisMonth(ctx context.Context) (int64, error)
func (q *QuotaService) InvalidateTokenCache(companyID string)
```

#### C. 认证与持久化会话服务（`internal/platform/auth`）

```go
package auth

type SessionConfig struct {
	CookieName     string        // 默认 "SYNAPSE_SID"（兼容原版 Cookie 语义）
	TTL            time.Duration // 默认 7 * 24 * time.Hour
	SlidingRefresh time.Duration // 剩余有效期低于一半时自动续期（默认 12h 节流）
	CookieSecure   bool          // 生产 HTTPS 开启；HttpOnly 恒 true，SameSite 恒 Lax
}

type Session struct {
	ID         string
	CompanyID  string
	UserID     string
	PasswordOK bool
	ExpiresAt  time.Time
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

type SessionManager struct { /* store store.SessionStore, cfg SessionConfig */ }
func NewSessionManager(s store.SessionStore, cfg SessionConfig) *SessionManager
func (m *SessionManager) Create(ctx context.Context, companyID, userID string, passwordOK bool) (*Session, error)
func (m *SessionManager) Get(ctx context.Context, sessionID string) (*Session, error)
func (m *SessionManager) MarkPasswordOK(ctx context.Context, sessionID string) error
func (m *SessionManager) Invalidate(ctx context.Context, sessionID string) error
func (m *SessionManager) InvalidateUser(ctx context.Context, companyID, userID string) error
func (m *SessionManager) PruneExpired(ctx context.Context) (int64, error)

type LocalAuthenticator struct { /* companies *tenant.CompanyService, users store.UserStore */ }
func NewLocalAuthenticator(companies *tenant.CompanyService, users store.UserStore) *LocalAuthenticator
func (a *LocalAuthenticator) Kind() string // "local"
func (a *LocalAuthenticator) Authenticate(ctx context.Context, cred spi.Credentials) (*spi.AuthPrincipal, error)

type LoginResult struct {
	Session   *Session
	Principal *spi.AuthPrincipal
}

type AuthService struct { /* authenticators map[string]spi.Authenticator, sessions *SessionManager, users store.UserStore, org store.OrgStore, settings *settings.SettingsService, account *user.AccountService */ }
func (s *AuthService) Login(ctx context.Context, cred spi.Credentials) (*LoginResult, error)
func (s *AuthService) Logout(ctx context.Context, sessionID string) error
func (s *AuthService) ResolvePrincipal(ctx context.Context, sessionID string) (*spi.AuthPrincipal, *Session, error)
func (s *AuthService) CheckPasswordGate(ctx context.Context, sess *Session) error // 若须改密返 apperror.ErrPasswordChangeRequired
func (s *AuthService) Me(ctx context.Context, p *spi.AuthPrincipal) (*api.MeDto, error)
```

#### D. 用户、账号、通讯录与组织服务（`internal/platform/user`）

```go
package user

type UserService struct { /* users store.UserStore, quotas *tenant.QuotaService, license spi.LicenseQuotaHook, transfer spi.OwnerTransferHook, sessions SessionInvalidator */ }
func (s *UserService) List(ctx context.Context) ([]api.UserDto, error)
func (s *UserService) Create(ctx context.Context, req api.SaveUserRequest) (*api.UserDto, error)
func (s *UserService) Update(ctx context.Context, id string, req api.SaveUserRequest) (*api.UserDto, error)
func (s *UserService) Delete(ctx context.Context, id string) error

type AccountService struct { /* users store.UserStore, avatarsDir string, sessions SessionPasswordMarker */ }
const (
	AvatarMaxBytes = 1024 * 1024 // 1 MB
	MinPasswordLen = 8
)
func (s *AccountService) CanChangePassword(ctx context.Context, userID string) (bool, error)
func (s *AccountService) MustChangePassword(ctx context.Context, userID string) (bool, error)
func (s *AccountService) ChangePassword(ctx context.Context, userID, currentSessionID, currentPwd, nextPwd string) error
func (s *AccountService) SetAvatar(ctx context.Context, userID, base64Data string) (int64, error)
func (s *AccountService) RemoveAvatar(ctx context.Context, userID string) error
func (s *AccountService) AvatarVersion(ctx context.Context, userID string) (*int64, error)
func (s *AccountService) AvatarVersions(ctx context.Context) (map[string]int64, error)
func (s *AccountService) Avatar(ctx context.Context, userID string) (*attachment.StoredImage, error)

type PeopleDirectory struct { /* users store.UserStore */ }
func (d *PeopleDirectory) Enabled(ctx context.Context) ([]domain.User, error)
func (d *PeopleDirectory) Find(ctx context.Context, userID string) (*domain.User, error)
func (d *PeopleDirectory) Resolve(ctx context.Context, idOrUsername string) (*domain.User, error)
func (d *PeopleDirectory) FindByUsername(ctx context.Context, username string) (*domain.User, error)
func (d *PeopleDirectory) ListDirectory(ctx context.Context) ([]api.DirectoryUserDto, error)

type OrgService struct { /* store store.OrgStore, users store.UserStore */ }
func (s *OrgService) Overview(ctx context.Context) (*api.OrgDto, error)
func (s *OrgService) AllTeams(ctx context.Context) ([]string, error)
func (s *OrgService) TeamsOf(ctx context.Context, userID string) ([]string, error)
func (s *OrgService) ManagerOf(ctx context.Context, userID string) (string, bool, error)

// TeamPaths 函数集合（internal/platform/user/team_paths.go）
const (
	TeamMaxSegment = 64
	TeamMaxDepth   = 8
)
func NormalizeTeamPath(path string) (string, error)
func TeamPathWithAncestors(team string) []string
func TeamPathWithin(team, root string) bool
```

#### E. 系统设置与品牌服务（`internal/platform/settings`）

```go
package settings

const (
	DefaultMaxSubtasks = 10
	DefaultMaxParallel = 5
	LogoMaxBytes       = 1024 * 1024 // 1 MB
)

type SettingsService struct { /* store store.SystemSettingStore, companies store.CompanyStore, models spi.ModelChecker, tx store.TxManager */ }
func (s *SettingsService) Get(ctx context.Context) (*api.SystemSettingsDto, error)
func (s *SettingsService) Update(ctx context.Context, req api.SystemSettingsDto) (*api.SystemSettingsDto, error)
func (s *SettingsService) AgentDefaultModel(ctx context.Context) (string, error)
func (s *SettingsService) BackupModels(ctx context.Context) ([]string, error)
func (s *SettingsService) TaskFixedModels(ctx context.Context) ([]string, error)
func (s *SettingsService) PublicURL(ctx context.Context) (string, error)
func (s *SettingsService) ComplexMaxSubtasks(ctx context.Context) (int, error)
func (s *SettingsService) ComplexMaxParallel(ctx context.Context) (int, error)
func (s *SettingsService) CodeBuddyInternetEnvironment(ctx context.Context) (string, error)

type BrandingService struct { /* rootDir string, defaultLogo []byte */ }
type LogoPayload struct {
	Bytes     []byte
	MediaType string
	ETag      string // 双引号弱/强 ETag，如 "\"<sha256前16位>\""
}
func (s *BrandingService) Logo(ctx context.Context) (*LogoPayload, error)
func (s *BrandingService) HasCustomLogo(ctx context.Context) (bool, error)
func (s *BrandingService) SetLogo(ctx context.Context, base64Data string) error
func (s *BrandingService) ResetLogo(ctx context.Context) error
```

#### F. 通知与分析服务（`internal/platform/notice` & `internal/platform/analytics`）

```go
package notice

const ListLimit = 500

type NoticeService struct {
	/* store, agents, automations, tasks, todos, sources, settings, users, bus, loc *time.Location */
}
func (s *NoticeService) RegisterHandlers(bus eventbus.Bus) // 订阅 5 类业务事件 + CompanyStarted
func (s *NoticeService) List(ctx context.Context, owner string) ([]api.NoticeDto, error)
func (s *NoticeService) Stats(ctx context.Context, owner string) (*api.NoticeStatsDto, error)
func (s *NoticeService) MarkDone(ctx context.Context, owner, id string) (*api.NoticeDto, error)
func (s *NoticeService) Ignore(ctx context.Context, owner, id string) (*api.NoticeDto, error)
func (s *NoticeService) Reopen(ctx context.Context, owner, id string) (*api.NoticeDto, error)
func (s *NoticeService) MarkAllRead(ctx context.Context, owner string, kinds []domain.NoticeKind) (int, error)
func (s *NoticeService) OnTaskStatusChanged(ctx context.Context, e eventbus.TaskStatusChanged)
func (s *NoticeService) OnPlanChanged(ctx context.Context, e eventbus.PlanChanged)
func (s *NoticeService) OnInboxTodoChanged(ctx context.Context, e eventbus.InboxTodoChanged)
func (s *NoticeService) OnAgentShared(ctx context.Context, e eventbus.AgentShared)
func (s *NoticeService) OnDefaultModelChanged(ctx context.Context, e eventbus.DefaultModelChanged)
func (s *NoticeService) BackfillOnCompanyStarted(ctx context.Context, e eventbus.CompanyStarted)

package analytics

const ActiveDays = 7

type AnalyticsService struct { /* users, agents, automations, tasks, loc *time.Location */ }
func (s *AnalyticsService) Overview(ctx context.Context, days int) (*api.AnalyticsDto, error)
```

### 4.2 路由契约（由 `L5/spec_13` 挂载，本篇冻结语义与 DTO）

| 方法 | 路径 | 权限 / 中间件豁免 | 请求体 / 参数 | 响应格式与特殊头 |
|---|---|---|---|---|
| `GET` | `/api/v1/orgs/registration` | 公开 | — | `ApiResponse<{"open": false}>`（V1 恒返 `false`） |
| `POST` | `/api/v1/platform/orgs` | **超管 Token**（`X-Super-Admin-Token`；未配 Token 返 404） | `ProvisionOrgRequest` | `ApiResponse<ProvisionedOrg>`（一次性打印密码） |
| `PUT` | `/api/v1/platform/orgs/:id/quotas` | **超管 Token** | `QuotaOverrides` | `ApiResponse<CompanyDto>` |
| `POST` | `/api/v1/platform/orgs/:id/enabled` | **超管 Token** | `{"enabled": bool}` | `ApiResponse<CompanyDto>` |
| `POST` | `/api/v1/auth/login` | 公开 | Form `org`、`username`、`password` | **裸 JSON（无 `ApiResponse` 壳）** 200 `{"userId","username","roles"}` + `Set-Cookie: HttpOnly; SameSite=Lax`；401 `{"code":"unauthorized","message":"组织、用户名或密码错误，或账号未授权"}` |
| `POST` | `/api/v1/auth/logout` | 已登录（免强制改密拦截） | — | HTTP 200 空体，清除 Cookie 并删 `user_session` |
| `GET` | `/api/v1/auth/me` | 已登录（免强制改密拦截） | — | `ApiResponse<MeDto>`（12 字段完全对齐原版 `MeDto.java`） |
| `PUT` | `/api/v1/account/password` | 已登录（免强制改密拦截） | `{"current","next"}` | `ApiResponse<null>`；清 `must_change_password` 并置会话 `password_ok=true` |
| `PUT` | `/api/v1/account/avatar` | 已登录 | `{"data":"<base64>"}` | `ApiResponse<int64>`（文件 mtime 毫秒） |
| `DELETE` | `/api/v1/account/avatar` | 已登录 | — | `ApiResponse<null>` |
| `GET` | `/api/v1/avatars` | 已登录 | — | `ApiResponse<map[userId]int64>` |
| `GET` | `/api/v1/avatars/:userId` | 已登录 | Path `userId`（原路径参数名 `username`） | 原始图片字节，`X-Content-Type-Options: nosniff`，`Cache-Control: private, max-age=2592000` |
| `GET` | `/api/v1/users` | `ADMIN` | — | `ApiResponse<[]UserDto>`（不返 `passwordHash`/`mustChangePassword`） |
| `POST` | `/api/v1/users` | `ADMIN` | `SaveUserRequest` | `ApiResponse<UserDto>` |
| `PUT` | `/api/v1/users/:id` | `ADMIN` | `SaveUserRequest` | `ApiResponse<UserDto>` |
| `DELETE` | `/api/v1/users/:id` | `ADMIN` | — | `ApiResponse<null>` |
| `GET` | `/api/v1/directory/users` | 已登录 | — | `ApiResponse<[]DirectoryUserDto>`（仅启用用户，按 `displayName` 大小写不敏感升序） |
| `GET` | `/api/v1/settings` | 已登录 | — | `ApiResponse<SystemSettingsDto>` |
| `PUT` | `/api/v1/settings` | `ADMIN` | `SystemSettingsDto` | `ApiResponse<SystemSettingsDto>` |
| `GET` | `/api/v1/settings/logo` | `ADMIN` | — | `ApiResponse<bool>` |
| `PUT` | `/api/v1/settings/logo` | `ADMIN` | `{"data":"<base64>"}` | `ApiResponse<null>` |
| `DELETE` | `/api/v1/settings/logo` | `ADMIN` | — | `ApiResponse<null>` |
| `GET` | `/api/v1/branding/logo` | 公开（免登录、免强制改密） | `If-None-Match`（可选） | 原始图片字节（或 `304`），`X-Content-Type-Options: nosniff`，`Cache-Control: no-cache`，`ETag` |
| `GET` | `/api/v1/notices` | 已登录 | — | `ApiResponse<[]NoticeDto>`（最新 500 条，排除 `TRACK`） |
| `GET` | `/api/v1/notices/stats` | 已登录 | — | `ApiResponse<NoticeStatsDto>` |
| `POST` | `/api/v1/notices/:id/done` | 已登录 | — | `ApiResponse<NoticeDto>` |
| `POST` | `/api/v1/notices/:id/ignore` | 已登录 | — | `ApiResponse<NoticeDto>`（发布 `NoticeIgnored`） |
| `POST` | `/api/v1/notices/:id/reopen` | 已登录 | — | `ApiResponse<NoticeDto>` |
| `POST` | `/api/v1/notices/read-all` | 已登录 | — | `ApiResponse<int>`（标记所有 `OPEN` 的 `DONE` + `NOTICE` 为 `DONE`） |
| `GET` | `/api/v1/admin/analytics` | `ADMIN` | Query `days`（默认 `14`，夹紧 `[7,90]`） | `ApiResponse<AnalyticsDto>` |

> **兼容细节**：`POST /api/v1/orgs` 公网路由在 V1 **不挂载**（或被统一拒止返回 `403 forbidden` `"这台服务器没有开放注册组织，请联系管理员开通"`），确保外部无法自助建租户。

### 4.3 错误语义

| 场景 | Go 领域错误 | HTTP 状态码 | JSON `code` | 冻结文案（对齐 Java 原版） |
|---|---|---|---|---|
| 租户配额超限（用户/月度 Token） | `apperror.Forbidden(...)` | `403` | `forbidden` | `"公司的账号数已到上限（%d 个）"` / `"公司本月的模型用量已到上限（%d tokens），下月恢复或请管理员调整额度"` |
| 登录凭据错误 / 租户停用 / 非本地账号 | `apperror.Unauthorized(...)` | `401` | `unauthorized` | `"组织、用户名或密码错误，或账号未授权"` |
| 未修改初始密码访问业务接口 | `apperror.PasswordChangeRequired(...)` | `403` | `password_change_required` | `"请先修改初始密码"` |
| 组织英文名冲突 / 用户名重复 | `apperror.Conflict(...)` | `409` | `conflict` | `"英文名「%s」已被其他组织使用，请换一个"` / `"Username already exists: %s"` |
| 唯一管理员防误删/停用/降权 | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"这是公司唯一的管理员，不能删除、停用或取消管理员角色"` |
| 改密校验失败 | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"这个账号不用密码登录"` / `"当前密码不正确"` / `"新密码至少 8 位"` / `"新密码不能与当前密码相同"` |
| 头像 / Logo 超限或非光栅格式 | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"头像图片不能超过 1 MB"` / `"头像只支持 PNG、JPEG、GIF、WebP 图片"` / `"Logo 图片不能超过 1 MB"` / `"Logo 只支持 PNG、JPEG、GIF、WebP 图片"` |
| 系统设置固定模型校验失败 | `apperror.InvalidArg(...)` | `400` | `invalid_argument` | `"Model %s has not passed the inference check"` / `"开启任务固定模型时，至少要选一个模型"` |
| 用户 / 待办不存在或 `TRACK` 项被操作 | `apperror.NotFound(...)` | `404` | `not_found` | `"No such user: %s"` / `"No such item: %s"` |

### 4.4 SQL 与持久化契约

1. **PG17 月度 Token 聚合（优化自 Java `QuotaService.java:88` 全表加载）**：
   ```sql
   SELECT COALESCE(SUM(input_tokens + cache_read + cache_write + output_tokens), 0)
   FROM token_usage
   WHERE company_id = $1 AND at >= $2 AND at < $3;
   ```
2. **PG17 `app_user` 安全 Upsert（严禁 `ON CONFLICT (company_id, username)`）**：
   ```sql
   -- Step 1: 先按主键 (company_id, id) 更新
   UPDATE app_user
   SET username=$3, display_name=$4, roles=$5, enabled=$6, password_hash=$7,
       phone=$8, external_id=$9, source=$10, must_change_password=$11, updated_at=$12
   WHERE company_id=$1 AND id=$2;
   -- Step 2: 若 RowsAffected()==0 再执行 INSERT（重名时触发 uq_app_user_username 23505 错）
   ```
3. **PG17 `notice` 原子幂等 Upsert（替代 Java `synchronized upsert`）**：
   ```sql
   INSERT INTO notice (company_id, id, owner, notice_key, kind, state, title, body, actor, link_type, link_id, created_at, updated_at)
   VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
   ON CONFLICT (company_id, owner, notice_key) DO UPDATE SET
       kind       = EXCLUDED.kind,
       state      = EXCLUDED.state,
       title      = EXCLUDED.title,
       body       = EXCLUDED.body,
       actor      = EXCLUDED.actor,
       updated_at = EXCLUDED.updated_at
   RETURNING id, owner, notice_key, kind, state, title, body, actor, link_type, link_id, created_at, updated_at;
   ```
   > 注意 `ON CONFLICT` 更新时不覆盖 `id`、`link_type`、`link_id`、`created_at`，与 Java `Notice.with(...)`（`Notice.java:42-45`）100% 一致！
4. **头像与 Logo 原子落盘**：写入同目录临时文件 `os.CreateTemp(dir, prefix)` -> `Sync()` + `Close()` -> `os.Rename(tmp, target)`，失败 `defer os.Remove(tmp)`。

---

## 5. 行为语义与 Java→Go 详细伪代码

### 5.1 多租户公平轮转与 `BootstrapProvisioner`

- **参考 Java**：`CompanyService.java:41-86`、`OrganizationService.java:32-119`。
- **关键行为**：
  1. `ForEachEnabled` 每次扫描拉取当前全部启用租户 ID，通过 `turn.Add(1)-1` 轮转起始偏移量，防止单租户长期抢占资源；每个租户在独立 `func()` 内执行并 `defer recover()` 拦截 panic，错误仅记录 `slog.Warn` 不中断后续租户。
  2. `BootstrapProvisioner.ProvisionOrg` 校验组织名（1–64 字符）、英文短名正则 `^[a-z][a-z0-9-]{1,30}[a-z0-9]$` 及 12 个保留词；在互斥锁与 `TxManager.WithinTx` 内调用 `license.CheckCanProvisionOrg` -> 查重 -> 创建公司 -> 生成 14 位 `crypto/rand` 密码（55 无歧义字符，排除 `I/O/l/0/1`）-> 创建 `ADMIN` 初始用户（`must_change_password=true`）；事务提交后在租户上下文发布 `CompanyStarted(org)`。

```go
// internal/platform/tenant/company.go — 对应 CompanyService.java:41-86
func (s *CompanyService) ForEachEnabled(ctx context.Context, work func(ctx context.Context) error) {
	ids, err := s.EnabledIDs(ctx)
	if err != nil {
		slog.WarnContext(ctx, "list enabled companies failed", "err", err)
		return
	}
	if len(ids) == 0 {
		return
	}
	start := int((s.turn.Add(1) - 1) % uint64(len(ids)))
	for i := range len(ids) {
		if ctx.Err() != nil {
			return
		}
		id := ids[(start+i)%len(ids)]
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.ErrorContext(ctx, "background work panicked for company", "company_id", id, "panic", r)
				}
			}()
			tctx, err := coretenant.WithCompany(ctx, id)
			if err != nil {
				slog.WarnContext(ctx, "invalid company id skipped", "company_id", id, "err", err)
				return
			}
			if err := work(tctx); err != nil {
				slog.WarnContext(tctx, "background work failed for company", "company_id", id, "err", err)
			}
		}()
	}
}

// internal/platform/tenant/bootstrap.go — 对应 OrganizationService.java:68-119（改为商业化离线/超管开户）
var (
	shortNameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}[a-z0-9]$`)
	reservedSlugs = map[string]struct{}{
		"admin": {}, "api": {}, "www": {}, "root": {}, "system": {}, "login": {},
		"mcp": {}, "static": {}, "support": {}, "help": {}, "default": {}, "platform": {},
	}
)
const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

func (p *BootstrapProvisioner) ProvisionOrg(ctx context.Context, req ProvisionOrgRequest) (*ProvisionedOrg, error) {
	cleanName := strings.TrimSpace(req.Name)
	if n := utf8.RuneCountInString(cleanName); n == 0 || n > 64 {
		return nil, apperror.InvalidArg("请填写组织名称（最多 64 个字）")
	}
	org := strings.ToLower(strings.TrimSpace(req.ID))
	if org == "" {
		return nil, apperror.InvalidArg("请填写组织的英文名")
	}
	if !shortNameRe.MatchString(org) {
		return nil, apperror.InvalidArg("英文名只能用小写字母、数字和连字符，以字母开头，3 到 32 位，例如 acme 或 acme-china")
	}
	if _, bad := reservedSlugs[org]; bad {
		return nil, apperror.InvalidArg("英文名「" + org + "」是保留词，请换一个")
	}
	adminUser := strings.TrimSpace(req.AdminUsername)
	if adminUser == "" {
		adminUser = org
	}
	password, err := generateRandomPassword(14) // crypto/rand + math/big，无模偏差
	if err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	allCompanies, err := p.companies.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.license.CheckCanProvisionOrg(ctx, len(allCompanies)); err != nil {
		return nil, err
	}
	if exists, err := p.companies.Exists(ctx, org); err != nil {
		return nil, err
	} else if exists {
		return nil, apperror.Conflict("英文名「" + org + "」已被其他组织使用，请换一个")
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	err = p.tx.WithinTx(ctx, func(txCtx context.Context) error {
		if _, err := p.companies.CreateWithoutEvent(txCtx, org, cleanName, req.Quotas, now); err != nil {
			return err
		}
		tctx, err := coretenant.WithCompany(txCtx, org)
		if err != nil {
			return err
		}
		return p.users.Save(tctx, domain.User{
			ID:                 uuid.NewString(),
			Username:           adminUser,
			DisplayName:        cleanName + " 管理员",
			Roles:              []string{"ADMIN"},
			Enabled:            true,
			PasswordHash:       string(hash),
			Source:             domain.UserSourceLocal,
			MustChangePassword: true,
			CreatedAt:          now,
			UpdatedAt:          now,
		})
	})
	if err != nil {
		return nil, err
	}
	// 事务提交后再发布 CompanyStarted，保证监听器可读到租户与初始管理员
	p.companies.PublishStarted(ctx, org)
	return &ProvisionedOrg{Org: org, Name: cleanName, Username: adminUser, Password: password}, nil
}
```

### 5.2 可插拔认证、防时序枚举与 PG17 持久化会话

- **参考 Java**：`AppUserDetailsService.java:35-62`、`PasswordChangeFilter.java:32-57`、`SecurityConfig.java:84-118`、`AuthController.java:40-55`。
- **关键行为**：
  1. `LocalAuthenticator.Authenticate`：查无租户/查无用户/停用/空哈希时，均执行一次 `dummyBcryptHash` 校验保持相近耗时，统一返回 `"组织、用户名或密码错误，或账号未授权"`（401），禁止暴露“组织不存在”还是“密码错误”。
  2. `AuthService.Login`：查注册表 `authenticators[cred.Kind]`（空默认 `"local"`），认证成功后调用 `sessions.Create(ctx, p.CompanyID, p.UserID, !p.MustChangePassword)` 写入 `user_session`（`id` 为 32 字节 `crypto/rand` 64 位小写 hex）。
  3. `AuthService.ResolvePrincipal`：读 `user_session` 校验未过期；再进租户上下文查 `app_user` 确认 `enabled == true`（若已被管理员停用，实时删会话并返 401）；当距离过期不足 TTL 一半且距上次 `updated_at` 超过 12 小时时异步/轻量滑动续期。
  4. `AuthService.CheckPasswordGate`：若 `sess.PasswordOK == true` 直接放行（零额外 DB 查）；否则查 `account.MustChangePassword(ctx, sess.UserID)`，若为 `true` 返回 `403 password_change_required`，若为 `false` 写回 `sessions.MarkPasswordOK(ctx, sess.ID)`。

```go
// internal/platform/auth/local.go — 对应 AppUserDetailsService.java:35-62
func (a *LocalAuthenticator) Authenticate(ctx context.Context, cred spi.Credentials) (*spi.AuthPrincipal, error) {
	org := strings.ToLower(strings.TrimSpace(cred.Org))
	username := strings.TrimSpace(cred.Username)
	if org == "" || username == "" || cred.Password == "" {
		return nil, apperror.Unauthorized("组织、用户名或密码错误，或账号未授权")
	}
	companyID, ok, err := a.companies.EnabledCompany(ctx, org)
	if err != nil {
		return nil, err
	}
	if !ok {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(cred.Password))
		return nil, apperror.Unauthorized("组织、用户名或密码错误，或账号未授权")
	}
	tctx, err := coretenant.WithCompany(ctx, companyID)
	if err != nil {
		return nil, err
	}
	u, err := a.users.FindByUsername(tctx, username)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.Enabled || strings.TrimSpace(u.PasswordHash) == "" {
		_ = bcrypt.CompareHashAndPassword(dummyBcryptHash, []byte(cred.Password))
		return nil, apperror.Unauthorized("组织、用户名或密码错误，或账号未授权")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(cred.Password)); err != nil {
		return nil, apperror.Unauthorized("组织、用户名或密码错误，或账号未授权")
	}
	return &spi.AuthPrincipal{
		UserID:             u.ID,
		CompanyID:          companyID,
		Username:           u.Username,
		DisplayName:        firstNonBlank(u.DisplayName, u.Username),
		Roles:              normalizePrincipalRoles(u.Roles), // 始终包含 "USER"
		MustChangePassword: u.MustChangePassword,
	}, nil
}
```

### 5.3 配额服务（`QuotaService`）

- **参考 Java**：`QuotaService.java:49-93`。
- **关键行为**：
  - `Limits`：读取当前 `Company`；非 nil 字段优先于全局默认，`<= 0` 表示不限。
  - `TokensThisMonth`：按 `YearMonth`（UTC）缓存于 `sync.Map`，当缓存为空、UTC 月份跨月或距上次读取已超过 `1m`（`time.Since(entry.at) > time.Minute`）时重新执行 `usage.SumTokensBetween(ctx, monthStartUTC, nowUTC)`。

```go
// internal/platform/tenant/quota.go — 对应 QuotaService.java:81-93
func (q *QuotaService) TokensThisMonth(ctx context.Context) (int64, error) {
	companyID, err := coretenant.Require(ctx)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	ym := fmt.Sprintf("%04d-%02d", now.Year(), now.Month())
	if v, ok := q.cache.Load(companyID); ok {
		entry := v.(usedCacheEntry)
		if entry.yearMonth == ym && now.Sub(entry.at) <= tokensFresh {
			return entry.tokens, nil
		}
	}
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	total, err := q.usage.SumTokensBetween(ctx, from, now)
	if err != nil {
		return 0, err
	}
	q.cache.Store(companyID, usedCacheEntry{yearMonth: ym, tokens: total, at: now})
	return total, nil
}
```

### 5.4 用户管理、唯一管理员守卫与离职交接预留（`UserService` / `AccountService` / `OrgService`）

- **参考 Java**：`UserService.java:48-155`、`AccountService.java:48-152`、`TeamPaths.java:16-56`。
- **关键行为**：
  1. `UserService.Create`：校验 `username` 非空唯一（409）-> 校验 `quotas.CheckCanAddUser(len(all))` 与 `license.CheckCanAddUser` -> `source==PHONE` 要求 `phone` 非空 -> 默认角色 `["USER"]` 大写去重 -> 管理员设了初始密码则 BCrypt 加密并置 `must_change_password=true`。
  2. `UserService.Update`：改名校验唯一性；重设密码则置 `must_change_password=true`（保留已有 `true` 标志）；执行 `requireAnAdminLeft(all, existing, updated)`；保存后若被停用（`existing.Enabled && !updated.Enabled`）或重设密码，调用 `sessions.InvalidateUser(ctx, companyID, id)`；若被停用触发 `transfer.OnUserDisabled(ctx, id)`。
  3. `UserService.Delete`：执行 `requireAnAdminLeft(all, existing, nil)` -> 触发 `transfer.OnUserDeleted(ctx, id)` -> 删除用户行并吊销会话（不可变 UUID 永不复用）。
  4. `AccountService.ChangePassword`：校验旧密码非空且 BCrypt 匹配 -> `utf8.RuneCountInString(next) >= 8` -> `next != current` -> 保存新哈希并置 `must_change_password=false` -> 置当前会话 `password_ok=true`（并吊销该用户其他旧会话，保障改密安全）。
  5. `AccountService.SetAvatar`：Base64 解码 -> `0 < len <= 1MB` -> `attachment.Sniff(bytes)` 仅放行 PNG/JPEG/GIF/WebP -> 原子写入 `<avatars>/<companyId>/<sha256_hex(userId)>` -> 返回 `stat.ModTime().UnixMilli()`。

```go
// internal/platform/user/user.go — 对应 UserService.java:112-119
func requireAnAdminLeft(all []domain.User, before domain.User, after *domain.User) error {
	wasAdmin := before.Enabled && before.IsAdmin()
	stillAdmin := after != nil && after.Enabled && after.IsAdmin()
	if !wasAdmin || stillAdmin {
		return nil
	}
	for _, u := range all {
		if u.ID != before.ID && u.Enabled && u.IsAdmin() {
			return nil
		}
	}
	return apperror.InvalidArg("这是公司唯一的管理员，不能删除、停用或取消管理员角色")
}
```

### 5.5 系统设置（全字段原子校验）与企业品牌 Logo

- **参考 Java**：`SettingsService.java:46-243`、`BrandingService.java:40-98`、`SettingsController.java:62-71`。
- **关键行为**：
  1. `SettingsService.Update`：先把所有字段（`agentDefaultModel`、`companyName`、`companyShortName`、`backupModels`、`taskFixedModels`、`taskFixedModelsOn`、`publicUrl`、`complexMaxSubtasks`、`complexMaxParallel`、`codebuddyInternetEnvironment`）全部完成解析与合法性检查，确认无任何错误后再在 `tx.WithinTx` 内统一写入 `system_setting`，彻底杜绝“前半截写入、后半截报错”的脏写（比 Java 原版 `SettingsService.java:82-101` 更加原子）。
  2. 新选模型校验规则：只有**本次新增**的模型才要求 `models.HasRunnableModel(ctx, id) == true`；原本已保存的模型即使当前探活失败也豁免，保证管理员仍可保存公司名、URL 等其他设置。
  3. `BrandingService.Logo`：支持无租户上下文（登录前页面），通过 `tenant.Optional(ctx)` 取 `companyID`，缺失时回退 `domain.DefaultCompanySentinelID` (`"superagent"`)；先读 `<branding>/<companyID>/logo` 并用 `attachment.Sniff` 校验光栅格式，失败则回退 `//go:embed assets/superagent-logo.png`；计算 SHA-256 前 8 字节十六进制作为强 ETag，支持 HTTP `If-None-Match` 304 协商。

```go
// internal/platform/settings/settings.go — 对应 SettingsService.java:68-122（升级为全字段原子校验+事务写）
func (s *SettingsService) Update(ctx context.Context, req api.SystemSettingsDto) (*api.SystemSettingsDto, error) {
	all, err := s.store.All(ctx)
	if err != nil {
		return nil, err
	}
	// 1. 全部校验先于任何写入
	curDefault := trimToNull(all[keyAgentDefaultModel])
	newDefault := trimToNull(req.AgentDefaultModel)
	if newDefault != "" && newDefault != curDefault && !s.models.HasRunnableModel(ctx, newDefault) {
		return nil, apperror.InvalidArg("Model " + newDefault + " has not passed the inference check")
	}
	companyName, err := validateName(req.CompanyName, 100)
	if err != nil {
		return nil, err
	}
	shortName, err := validateName(req.CompanyShortName, 20)
	if err != nil {
		return nil, err
	}
	var newBackups []string
	hasBackups := req.BackupModels != nil
	if hasBackups {
		newBackups, err = s.validateModels(ctx, *req.BackupModels, splitList(all[keyBackupModels]))
		if err != nil {
			return nil, err
		}
	}
	savedFixed := splitList(all[keyTaskFixedModels])
	newFixed := savedFixed
	if req.TaskFixedModels != nil {
		newFixed, err = s.validateModels(ctx, *req.TaskFixedModels, savedFixed)
		if err != nil {
			return nil, err
		}
	}
	fixedOn := all[keyTaskFixedOn] == "true"
	if req.TaskFixedModelsOn != nil {
		fixedOn = *req.TaskFixedModelsOn
	}
	if fixedOn && len(newFixed) == 0 {
		return nil, apperror.InvalidArg("开启任务固定模型时，至少要选一个模型")
	}
	var cleanPublicURL string
	hasPublicURL := req.PublicURL != nil
	if hasPublicURL {
		cleanPublicURL, err = validatePublicURL(*req.PublicURL)
		if err != nil {
			return nil, err
		}
	}
	if req.ComplexMaxSubtasks != nil {
		if err := validateBounded(*req.ComplexMaxSubtasks, 1, 50, "子任务上限"); err != nil {
			return nil, err
		}
	}
	if req.ComplexMaxParallel != nil {
		if err := validateBounded(*req.ComplexMaxParallel, 1, 20, "同时运行的子任务数"); err != nil {
			return nil, err
		}
	}
	var cleanCodeBuddyEnv string
	if req.CodeBuddyInternetEnvironment != nil {
		cleanCodeBuddyEnv, err = validateCodeBuddyEnv(*req.CodeBuddyInternetEnvironment)
		if err != nil {
			return nil, err
		}
	}

	// 2. 事务内原子提交全部键
	err = s.tx.WithinTx(ctx, func(txCtx context.Context) error {
		writes := []struct{ k, v string }{
			{keyAgentDefaultModel, newDefault},
			{keyCompanyName, companyName},
			{keyCompanyShortName, shortName},
			{keyTaskFixedModels, strings.Join(newFixed, ",")},
			{keyTaskFixedOn, boolTrueOrEmpty(fixedOn)},
		}
		if hasBackups {
			writes = append(writes, struct{ k, v string }{keyBackupModels, strings.Join(newBackups, ",")})
		}
		if hasPublicURL {
			writes = append(writes, struct{ k, v string }{keyPublicURL, cleanPublicURL})
		}
		if req.ComplexMaxSubtasks != nil {
			writes = append(writes, struct{ k, v string }{keyComplexMaxSubtasks, strconv.Itoa(*req.ComplexMaxSubtasks)})
		}
		if req.ComplexMaxParallel != nil {
			writes = append(writes, struct{ k, v string }{keyComplexMaxParallel, strconv.Itoa(*req.ComplexMaxParallel)})
		}
		if req.CodeBuddyInternetEnvironment != nil {
			writes = append(writes, struct{ k, v string }{keyCodeBuddyInternetEnv, cleanCodeBuddyEnv})
		}
		for _, w := range writes {
			if err := s.store.Put(txCtx, w.k, w.v); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.Get(ctx)
}
```

### 5.6 待办通知中心（`NoticeService` 5 类事件 + 回填 + `NoticeIgnored`）

- **参考 Java**：`NoticeService.java:83-324`。
- **关键行为**：
  1. `List` 返回最新 500 条（过滤 `kind != TRACK`）。
  2. `Stats`：`toConfirm`/`toDo`/`unreadNotices` 统计 `OPEN` 态的 `CONFIRM`/`ACTION`/`NOTICE`；`doneToday`/`doneThisWeek`（周一 00:00 起，按 `notice.timezone` 默认 `Asia/Shanghai`）/`doneThisMonth`/`doneTotal` 直接统计 `TaskStore` 中 `created_by == owner && status == DONE`（优先 `finished_at`，空退 `updated_at`）。
  3. 5 类事件映射严格对齐 `NoticeService.java:140-227`：
     - `TaskStatusChanged`（`key = "task:" + taskId`）：`pending`（仅无旧记录时写 `TRACK` 记来源）|`waiting`（空操作）|`running`（若当前为 `OPEN` 的 `CONFIRM` 则退回 `TRACK`）|`needs_confirmation`（`CONFIRM/OPEN` `"等待你确认后继续"`）|`error`（`CONFIRM/OPEN` `"执行失败..."`）|`done`（`DONE/OPEN`，去 Markdown 符号截 200 字符摘要）|其余如 `stopped`/`deleted`（置 `IGNORED`）。
     - `PlanChanged`（`key = "plan:" + owner`）：`open=true` -> `CONFIRM/OPEN` (`linkType="superagent"`)；`open=false` -> 已有 `OPEN` 置 `DONE`。
     - `InboxTodoChanged`（`key = "todo:" + todoId`）：Upsert `ACTION`（`actor="飞书"` 或 `"飞书 · "+source`，`linkType="todo"`），随后按状态同步 `open->OPEN`、`dismissed->IGNORED`、`done/tasked->DONE`。
     - `AgentShared`（`key = "shared:" + agentId`）：`NOTICE/OPEN`（`linkType="agent"`）。
     - `DefaultModelChanged`（`key = "default-model"`）：扇出至所有 `enabled && IsAdmin()` 的管理员，`down=true/false` 原地更新同一张卡片。
  4. `Ignore`：校验归属且非 `TRACK` -> 置 `IGNORED` -> 发布 `eventbus.NoticeIgnored{Owner, LinkType, LinkID}`（驱动 `UserInboxService` 联动忽略收件箱待办）。
  5. `BackfillOnCompanyStarted`：若 `system_setting` 尚无 `notice.backfilled`，一次性导入存量 `NEEDS_CONFIRMATION`/`ERROR` 任务与飞书/钉钉用户的 `OPEN` 收件箱待办，最后置 `notice.backfilled="true"`。

```go
// internal/platform/notice/notice.go — 对应 NoticeService.java:141-163 & 277-292
func (s *NoticeService) OnTaskStatusChanged(ctx context.Context, e eventbus.TaskStatusChanged) {
	s.guard(ctx, func() error {
		key := "task:" + e.TaskID
		existing, err := s.store.FindByKey(ctx, e.Owner, key)
		if err != nil {
			return err
		}
		switch e.Status {
		case "pending":
			if existing == nil {
				_, err = s.upsert(ctx, e.Owner, key, domain.NoticeKindTrack, e.Title, "", s.origin(ctx, e), "task", e.TaskID)
			}
			return err
		case "waiting":
			return nil
		case "running":
			if existing != nil && existing.Kind == domain.NoticeKindConfirm && existing.State == domain.NoticeStateOpen {
				return s.store.Save(ctx, existing.With(domain.NoticeKindTrack, domain.NoticeStateOpen, existing.Title, "", existing.Actor))
			}
			return nil
		case "needs_confirmation":
			_, err = s.upsert(ctx, e.Owner, key, domain.NoticeKindConfirm, e.Title, "等待你确认后继续", s.actor(ctx, existing, e), "task", e.TaskID)
			return err
		case "error":
			body := "执行失败"
			if e.Error != "" {
				body += "：" + e.Error
			}
			_, err = s.upsert(ctx, e.Owner, key, domain.NoticeKindConfirm, e.Title, body, s.actor(ctx, existing, e), "task", e.TaskID)
			return err
		case "done":
			_, err = s.upsert(ctx, e.Owner, key, domain.NoticeKindDone, e.Title, summarizeMarkdown(e.Result), s.actor(ctx, existing, e), "task", e.TaskID)
			return err
		default: // stopped / deleted
			if existing != nil {
				return s.store.Save(ctx, existing.WithState(domain.NoticeStateIgnored))
			}
			return nil
		}
	})
}

func summarizeMarkdown(result string) string {
	if strings.TrimSpace(result) == "" {
		return "已完成"
	}
	plain := reHeading.ReplaceAllString(result, "")
	plain = reMdChars.ReplaceAllString(plain, "")
	plain = strings.TrimSpace(reSpaces.ReplaceAllString(plain, " "))
	runes := []rune(plain)
	if len(runes) <= 200 {
		return plain
	}
	return string(runes[:200]) + "…"
}
```

### 5.7 平台统计分析（`AnalyticsService`）

- **参考 Java**：`AnalyticsService.java:48-107`。
- **关键行为**：
  - `span := max(7, min(days, 90))`；时区采用配置时区（默认 `time.Local`）。
  - 过滤 `status != DELETED` 的任务与 Agent；`activeUsers` 统计 `created_at > now - 7*24h` 的去重非空 `created_by`。
  - 预填充 `[today-(span-1) .. today]` 零值日桶（从旧到新）；`created` 按 `created_at` 归日，`done`/`failed` 仅在 `finished_at != nil` 时按 `finished_at` 归日。
  - `successRate`：当 `done+failed == 0` 返 `nil`，否则返 `float64(done) / float64(done+failed)`；`spend` 使用 `decimal.Decimal` 累加；`models` 按 `tasks DESC` 排序，空模型名输出 JSON `null`。

```go
// internal/platform/analytics/analytics.go — 对应 AnalyticsService.java:48-107
func (s *AnalyticsService) Overview(ctx context.Context, days int) (*api.AnalyticsDto, error) {
	span := max(7, min(days, 90))
	now := time.Now().In(s.loc)
	allUsers, err := s.users.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	allAgents, err := s.agents.List(ctx)
	if err != nil {
		return nil, err
	}
	allAutos, err := s.automations.List(ctx)
	if err != nil {
		return nil, err
	}
	allTasks, err := s.tasks.ListTasks(ctx)
	if err != nil {
		return nil, err
	}

	activeSince := now.Add(-ActiveDays * 24 * time.Hour)
	activeCreators := make(map[string]struct{})
	byDay := make(map[string]*[3]int, span)
	dayOrder := make([]string, 0, span)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, s.loc)
	for i := span - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		dayOrder = append(dayOrder, d)
		byDay[d] = &[3]int{}
	}

	type modelAgg struct {
		tasks int
		spend decimal.Decimal
	}
	byModel := make(map[string]*modelAgg)
	totalSpend := decimal.Zero
	var liveCount, doneCount, failedCount int

	for _, t := range allTasks {
		if t.Status == domain.TaskStatusDeleted {
			continue
		}
		liveCount++
		if t.CreatedAt.After(activeSince) && t.CreatedBy != "" {
			activeCreators[t.CreatedBy] = struct{}{}
		}
		if t.Status == domain.TaskStatusDone {
			doneCount++
		} else if t.Status == domain.TaskStatusError {
			failedCount++
		}
		if bucket, ok := byDay[t.CreatedAt.In(s.loc).Format("2006-01-02")]; ok {
			bucket[0]++
		}
		if t.FinishedAt != nil && (t.Status == domain.TaskStatusDone || t.Status == domain.TaskStatusError) {
			if bucket, ok := byDay[t.FinishedAt.In(s.loc).Format("2006-01-02")]; ok {
				if t.Status == domain.TaskStatusDone {
					bucket[1]++
				} else {
					bucket[2]++
				}
			}
		}
		totalSpend = totalSpend.Add(t.Spend)
		agg, ok := byModel[t.Model]
		if !ok {
			agg = &modelAgg{}
			byModel[t.Model] = agg
		}
		agg.tasks++
		agg.spend = agg.spend.Add(t.Spend)
	}
	var rate *float64
	if denom := doneCount + failedCount; denom > 0 {
		r := float64(doneCount) / float64(denom)
		rate = &r
	}
	// 组装 daily（按 dayOrder）与 models（按 tasks DESC，"" -> nil），以及 enabledUsers / liveAgents / enabledAutos 统计
	return buildAnalyticsDto(allUsers, allAgents, allAutos, len(activeCreators), liveCount, doneCount, failedCount, rate, totalSpend, dayOrder, byDay, byModel), nil
}
```

---

## 6. 任务清单（TDD 粒度）

- [ ] T1: 补齐/核对 `internal/core/spi/platform.go`（`Authenticator`、`LicenseQuotaHook`、`OwnerTransferHook`、`ModelChecker`）及 `internal/core/api` 中本篇全部 DTO（`MeDto`、`UserDto`、`SaveUserRequest`、`DirectoryUserDto`、`OrgDto`、`SystemSettingsDto`、`NoticeDto`、`NoticeStatsDto`、`AnalyticsDto`）。
- [ ] T2: 编写 `internal/platform/tenant/*_test.go` 与实现：`CompanyService`（含 `ForEachEnabled` 轮转与 panic 恢复）、`FairTicker`、`BootstrapProvisioner`（slug/保留词/14 位密码/License 钩子）、`QuotaService`（覆盖优先级 + 1m UTC 缓存）。
- [ ] T3: 编写 `internal/platform/auth/*_test.go` 与实现：`LocalAuthenticator`（BCrypt + 防枚举）、`SessionManager`（PG17 `user_session`、`password_ok` 缓存、按用户吊销、过期清理）、`AuthService`（`Login`/`Logout`/`ResolvePrincipal`/`CheckPasswordGate`/`Me`）。
- [ ] T4: 编写 `internal/platform/user/*_test.go` 与实现：`UserService`（唯一管理员守卫、`max_users`、会话吊销、`OwnerTransferHook`）、`AccountService`（改密 + 1 MB 光栅头像原子落盘）、`PeopleDirectory`、`OrgService` & `TeamPaths`。
- [ ] T5: 编写 `internal/platform/settings/*_test.go` 与实现：`SettingsService`（全字段原子校验 + 事务写入 + 豁免已保存失效模型）与 `BrandingService`（1 MB 光栅 Logo + 内嵌默认 PNG + ETag）。
- [ ] T6: 编写 `internal/platform/notice/*_test.go` 与实现：`NoticeService`（列表/统计/状态变更/5 类领域事件/`CompanyStarted` 回填/`NoticeIgnored` 发布）。
- [ ] T7: 编写 `internal/platform/analytics/*_test.go` 与实现：`AnalyticsService`（7–90d 日趋势、7d 活跃发起人、成功率、`decimal` 汇总、模型分布）。
- [ ] T8: 全量竞态与架构测试：`go build ./... && go test -race ./internal/platform/... ./internal/arch_test.go`。

---

## 7. 文档同步

- [ ] `docs/SuperAgent-Go/DDD/README.md` §3 看板：`SA-04` 状态更新。
- [ ] `docs/SuperAgent-Go/05_前端兼容策略_部署运维与分期研发实施路线.md`：核对 `bootstrap-org` CLI 参数与超管 API 开关说明。
- [ ] `configs/config.example.yaml`：补充 `platform.super_admin_token`、`auth.session_ttl`、`auth.cookie_secure`、`tenant.quota.*`、`avatars.dir`、`branding.dir`、`notice.timezone` 配置项注释。

---

## 8. 测试清单

| 测试点 | 落位 | 关键断言 |
|---|---|---|
| `ForEachEnabled` 轮转公平与单租户 panic 隔离 | `internal/platform/tenant/company_test.go` | 连续 3 轮起始租户依次轮换；租户 A panic 或返回 error 不影响租户 B/C 执行 |
| `BootstrapProvisioner` slug、12 保留词、冲突与密码复杂度 | `internal/platform/tenant/bootstrap_test.go` | 非法 slug 返 400、保留词返 400、重名返 409；生成 14 位无歧义密码且管理员 `must_change_password=true`；触发 `LicenseQuotaHook` 与 `CompanyStarted` |
| `QuotaService` 优先级、UTC 跨月刷新与 1m 缓存 | `internal/platform/tenant/quota_test.go` | 公司列非 nil 覆盖全局默认、`0` 视为不限；1m 内重复调用只查 1 次 `UsageStore`；超限返 403 |
| `LocalAuthenticator` 与 `SessionManager` 全生命周期 | `internal/platform/auth/auth_test.go` | 正确密码返回不可变 UUID `UserID` 且角色恒含 `USER`；非 LOCAL 空密码拒登；创建/续期/`MarkPasswordOK`/停用用户实时失效/按用户批量吊销 |
| `PasswordGate` 首次登录强制改密与缓存 | `internal/platform/auth/auth_test.go` | `must_change_password=true` 拦截返 `403 password_change_required`；改密成功后置 `password_ok=true` 不再拦截 |
| `UserService` 唯一管理员保护、`max_users` 与 `OwnerTransferHook` | `internal/platform/user/user_test.go` | 删除/停用/取消唯一管理员角色均返 400；超 `max_users` 返 403；停用/删除调用 `OwnerTransferHook` 并吊销会话 |
| `AccountService` 改密规则与光栅头像防 XSS | `internal/platform/user/account_test.go` | `<8` 位或等于旧密码返 400；SVG/假 PNG 头被 `Sniff` 拒绝；>1 MB 拒绝；合法 PNG 原子写入并按 mtime 返回版本 |
| `TeamPaths` 路径规范化与祖先展开 | `internal/platform/user/team_paths_test.go` | 容忍重复/首尾 `/`；拒绝 `.` 开头、`›`、`\`、控制字符、>8 层、>64 字符；`WithAncestors("a/b/c")==["a/b/c","a/b","a"]` |
| `SettingsService` 全字段原子校验与失效模型豁免 | `internal/platform/settings/settings_test.go` | 任一字段（如 `publicUrl` 或 `taskFixedModelsOn=true` 且空列表）非法时**零写入**；已保存失效模型不阻塞保存公司名；未过探活的新模型返 400 |
| `BrandingService` 公共回退与 ETag | `internal/platform/settings/branding_test.go` | 无租户上下文读默认 PNG；上传 SVG/超 1 MB 拒止；上传合法 WebP 后 ETag 变化，`ResetLogo` 恢复默认 |
| `NoticeService` 5 类事件、去重 Upsert、`TRACK` 隐藏与 `NoticeIgnored` | `internal/platform/notice/notice_test.go` | 同一 `task:<id>` 从 `pending(TRACK)`->`needs_confirmation(CONFIRM)`->`done(DONE)` 原地演进不重复；操作 `TRACK` 返 404；`Ignore` 发布 `NoticeIgnored`；`DefaultModelChanged` 仅扇出启用管理员；`Backfill` 幂等仅执行一次 |
| `AnalyticsService` 7–90d 夹紧、零填充日趋势与成功率 | `internal/platform/analytics/analytics_test.go` | `days=3` 夹紧 7、`days=200` 夹紧 90；`done+failed==0` 时 `successRate==nil`；排除 `DELETED` 任务/Agent；`spend` 精确十进制累加 |

---

## 9. 验收门

- [ ] `go build ./...` 零警告通过
- [ ] `go test -race ./internal/platform/{tenant,auth,user,settings,notice,analytics}/...` 100% 全绿
- [ ] `arch_test.go` 通过（确认 L2 `internal/platform/*` 零引用 L3/L4/L5，且 `platform/settings` 与 `platform/provider` 零循环引用）
- [ ] `guard_test.go` 通过（除 `company`/`user_session`/`goose_db_version` 外所有表查询均经 `tenant.Require`）
- [ ] §2 交付物表 17 项逐一存在且 §8 测试清单全部落地，包覆盖率 `>= 80%`
- [ ] 人工冒烟：执行 `synapse-server bootstrap-org --id acme --name "Acme"` -> `POST /api/v1/auth/login` -> 验证业务接口返回 `403 password_change_required` -> `PUT /api/v1/account/password` -> 修改头像/系统设置/Logo -> 服务重启后原 Cookie 会话仍有效

---

## 10. 可提交节点

- 建议 commit：`feat(platform-core): implement multi-tenant bootstrap, session auth, user/org, settings, notice and analytics (spec 04, PR SA-04)`
- 建议分层拆分提交（可选）：
  1. `feat(platform-tenant): add CompanyService, FairTicker, BootstrapProvisioner and QuotaService`
  2. `feat(platform-auth): add Authenticator SPI, LocalAuthenticator and Postgres SessionManager`
  3. `feat(platform-user): add UserService, AccountService, PeopleDirectory, OrgService and TeamPaths`
  4. `feat(platform-settings): add atomic SettingsService and BrandingService`
  5. `feat(platform-notice): add event-driven NoticeService and AnalyticsService`
