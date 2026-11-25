# Data Model: SA-01 L0 Core Foundation

> SA-01 is an L0 stateless runtime foundation with **zero database tables or SQL migrations**. This document defines the in-memory data structures, configuration schema, default values, and validation invariants.

## 1. Domain Error Entities (`internal/core/apperror`)

| Entity | Fields | Invariants / Behavior |
|---|---|---|
| `Code` | `string` | 13 frozen codes: `ok`, `invalid_argument`, `unauthorized`, `password_change_required`, `forbidden`, `not_found`, `method_not_allowed`, `conflict`, `busy`, `too_large`, `rate_limited`, `service_unavailable`, `internal_error` |
| `AppError` | `Code Code`, `HTTPStatus int`, `Message string`, `Err error` | `Error()` handles nil receiver -> `""`, else `Message` -> `Err.Error()` -> `string(Code)`; `Unwrap()` returns `Err`; `Is(target)` matches `*AppError` by non-empty `Code` |
| `UnreadableLinkError` | `URL string`, `Reason string`, `Hint string` | `Error()` returns `Reason` (fallback `URL`) |
| `UnreadableMaterialsError` | `Items []*UnreadableLinkError` | `Error()` formats exact Chinese multi-line list; `AsAppError()` wraps as HTTP 409 `CodeConflict` |

## 2. Configuration Tree (`internal/infra/config`)

All 22 sections of `Config` (zero Redis sections anywhere):

| Section | Struct | Fields (`yaml` tag) & Defaults |
|---|---|---|
| `server` | `ServerConfig` | `Listen` (`listen`, `":18080"`), `ShutdownTimeout` (`shutdown_timeout`, `30s`), `UIDir` (`ui_dir`, `""`), `SessionCookieName` (`session_cookie_name`, `"SYNAPSE_SESSION"`), `SessionCookieSecure` (`session_cookie_secure`, `false`), `SessionTTL` (`session_ttl`, `168h`), `TrustProxyHeaders` (`trust_proxy_headers`, `true`) |
| `app` | `AppConfig` | `BaseURL` (`base_url`, `""`) |
| `storage` | `StorageConfig` | `AutoMigrate` (`auto_migrate`, `true`), `Postgres` (`postgres`, `PostgresConfig`) |
| `storage.postgres` | `PostgresConfig` | `DSN` (`dsn`, `"postgres://synapse:synapse@127.0.0.1:5432/synapse?sslmode=disable&TimeZone=UTC"`), `MaxConns` (`max_conns`, `16`), `MinConns` (`min_conns`, `2`), `MaxConnLifetime` (`max_conn_lifetime`, `30m`), `MaxConnIdleTime` (`max_conn_idle_time`, `5m`), `SlowQueryThreshold` (`slow_query_threshold`, `200ms`) |
| `secret` | `SecretConfig` | `Key` (`key`, `""`), `KeyFile` (`key_file`, `"./data/secret.key"`) |
| `network` | `NetworkConfig` | `HTTPProxy` (`http_proxy`, `""`), `HTTPSProxy` (`https_proxy`, `""`), `NoProxy` (`no_proxy`, `"localhost,127.0.0.1,postgres"`), `CABundleFile` (`ca_bundle_file`, `""`) |
| `runtime` | `RuntimeConfig` | `NodeDir` (`node_dir`, `"./data/runtime/node"`), `NPMRegistry` (`npm_registry`, `""`), `NodeDistMirror` (`node_dist_mirror`, `"https://nodejs.org/dist"`) |
| `paths` | `PathsConfig` | `Workspaces` (`"./data/workspaces"`), `Tasks` (`"./data/tasks"`), `Files` (`"./data/files"`), `Attachments` (`"./data/attachments"`), `Avatars` (`"./data/avatars"`), `Branding` (`"./data/branding"`), `Skills` (`"./data/skills"`) |
| `quota` | `QuotaConfig` | `MaxUsers` (`max_users`, `0`), `MaxRunningTasks` (`max_running_tasks`, `0`), `MonthlyTokens` (`monthly_tokens`, `0`) |
| `agent` | `AgentConfig` | `DefaultModel` (`default_model`, `""`), `AllowedTools` (`allowed_tools`, `"Read,Glob,Grep,LS,Bash"`), `SkipPermissions` (`skip_permissions`, `false`), `SessionFailureLimit` (`session_failure_limit`, `10`), `Claude` (`claude`, `ClaudeConfig`), `Codex` (`codex`, `CodexConfig`) |
| `agent.claude` | `ClaudeConfig` | `Command` (`command`, `"claude"`), `TimeoutSeconds` (`timeout_seconds`, `600`), `WarmEnabled` (`warm_enabled`, `true`), `WarmIdleMinutes` (`warm_idle_minutes`, `10`), `WarmMax` (`warm_max`, `20`) |
| `agent.codex` | `CodexConfig` | `Command` (`command`, `"codex"`), `TimeoutSeconds` (`timeout_seconds`, `600`) |
| `chat` | `ChatConfig` | `SSETimeout` (`sse_timeout`, `10m`) |
| `mcp` | `MCPConfig` | `EnabledInChat` (`enabled_in_chat`, `true`), `SelfURL` (`self_url`, `""`) |
| `files` | `FilesConfig` | `UploadMaxMB` (`upload_max_mb`, `50`), `MaxMB` (`max_mb`, `200`), `AutoTag` (`auto_tag`, `true`) |
| `task` | `TaskConfig` | `AllowedTools` (`allowed_tools`, `"Read,Glob,Grep,LS,Edit,Write,Bash"`), `SkipPermissions` (`skip_permissions`, `false`), `MaxParallel` (`max_parallel`, `50`), `SchedulerSweep` (`scheduler_sweep`, `30s`), `WatchdogEnabled` (`watchdog_enabled`, `true`), `WatchdogInterval` (`watchdog_interval`, `30s`), `WatchdogStall` (`watchdog_stall`, `120s`) |
| `automation` | `AutomationConfig` | `SchedulerEnabled` (`scheduler_enabled`, `true`), `SchedulerInterval` (`scheduler_interval`, `30s`) |
| `superagent` | `SuperAgentConfig` | `Enabled` (`enabled`, `true`), `TickInterval` (`tick_interval`, `3s`), `QuietWindow` (`quiet_window`, `10s`), `MaxWait` (`max_wait`, `60s`), `TeamLimit` (`team_limit`, `6`), `Timezone` (`timezone`, `"Asia/Shanghai"`) |
| `memory` | `MemoryConfig` | `Enabled` (`enabled`, `true`), `Interval` (`interval`, `5m`), `MinMessages` (`min_messages`, `10`), `SettleMinutes` (`settle_minutes`, `2`), `IdleMinutes` (`idle_minutes`, `30`), `AgentsPerTick` (`agents_per_tick`, `3`), `CarryChars` (`carry_chars`, `60000`), `Timezone` (`timezone`, `"Asia/Shanghai"`) |
| `provider` | `ProviderConfig` | `CheckerEnabled` (`checker_enabled`, `true`), `LocalInterval` (`local_interval`, `3s`), `LatestInterval` (`latest_interval`, `1h`), `InferInterval` (`infer_interval`, `1h`), `CredentialInterval` (`credential_interval`, `10m`) |
| `cost` | `CostConfig` | `CatalogURL` (`catalog_url`, `"https://models.dev/api.json"`), `CatalogCheckInterval` (`catalog_check_interval`, `10m`) |
| `inbox` | `InboxConfig` | `RetentionDays` (`retention_days`, `30`) |
| `agent_log` | `AgentLogConfig` | `RetentionDays` (`retention_days`, `30`), `MaxPerAgent` (`max_per_agent`, `5000`) |
| `observability` | `ObservabilityConfig` | `LogLevel` (`log_level`, `"info"`), `LogFormat` (`log_format`, `"text"`), `LogFile` (`log_file`, `""`), `PrometheusToken` (`prometheus_token`, `""`) |
| `platform` | `PlatformConfig` | `SuperAdminToken` (`super_admin_token`, `""`) |

### Validation Rules (`Config.Validate()`)
1. `server.listen` non-empty after trim.
2. `server.shutdown_timeout > 0`.
3. `storage.postgres.dsn` non-empty and starts with `postgres://` or `postgresql://`.
4. `storage.postgres.min_conns >= 1` and `max_conns >= min_conns`.
5. If `secret.key` non-empty: valid standard Base64 decoding to exactly 32 bytes.
6. If `network.http_proxy` / `network.https_proxy` non-empty: valid URL with scheme `http`/`https` and non-empty `Host`.
7. If `network.ca_bundle_file` non-empty: readable file containing at least one valid PEM certificate.
8. `observability.log_level` in `debug|info|warn|error` (case-insensitive).
9. `observability.log_format` in `text|json` (case-insensitive).
10. `files.upload_max_mb > 0` and `files.max_mb >= files.upload_max_mb`.
11. `task.max_parallel > 0`.
12. `superagent.timezone` valid IANA timezone via `time.LoadLocation`.

## 3. Observability Entities (`internal/infra/obs`)

- `LoggerConfig`: `Level`, `Format`, `File`, `Output`
- `Metrics`: `ChatTurnsTotal`, `TaskRunsTotal`, `TaskQueueDepth`, `TokensTotal`, `CLIWarmPoolSize`, `IMConnected`, `HTTPRequestsSecs`, internal `registry *prometheus.Registry`, `registerer prometheus.Registerer`

## 4. Lifecycle Entities (`internal/infra/lifecycle`)

- `Loop`: interface `{ Name() string; Run(ctx context.Context) error }`
- `BackgroundGroup`: manages derived `ctx`/`cancel`, `wg`, `active`, `logger`, `onPanic`, `mu`, `stopped`, `running map[string]int`, `errs []error`
- `ShutdownPhase`: `PhaseHTTP(10)`, `PhaseBackground(20)`, `PhaseConnections(30)`, `PhaseStorage(40)`, `PhaseFlush(50)`
- `ShutdownManager`: manages `totalBudget`, `logger`, `mu`, `nextSeq`, `hooks []shutdownHook`
