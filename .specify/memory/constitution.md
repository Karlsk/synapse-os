<!--
Sync Impact Report
==================
- Version change: (template) -> 1.0.0
- Ratification rationale: Initial formal ratification of the Synapse-OS
  (SuperAgent Go Commercial Private-Deployment V1) Constitution derived from
  AGENTS.md hard engineering rules.
- Modified / Defined Principles:
  - [PRINCIPLE_1_NAME] -> I. Enterprise-First & V1 Commercial Scope (NON-NEGOTIABLE)
  - [PRINCIPLE_2_NAME] -> II. Strict L0-L6 Unidirectional Layering & Explicit Wiring
  - [PRINCIPLE_3_NAME] -> III. Fail-Closed Multi-Tenant Isolation & Envelope Encryption
  - [PRINCIPLE_4_NAME] -> IV. Infrastructure & Egress Isolation
  - [PRINCIPLE_5_NAME] -> V. PostgreSQL 17 Schema Discipline & Forward-Only Migrations
  - Added -> VI. Resilience, Bounded Concurrency & Graceful Shutdown
  - Added -> VII. API Contract Fidelity, Go Style & Verification Discipline
- Added Sections:
  - Technology Stack & Operational Constraints
  - Development Workflow & Quality Gates
  - Governance
- Removed Sections: None
- Template Propagation Status:
  - .specify/templates/plan-template.md: updated (added Synapse-OS Constitution Check gates & Go workspace layout)
  - .specify/templates/spec-template.md: verified aligned (technology-agnostic user-story & requirements format preserved)
  - .specify/templates/tasks-template.md: updated (aligned Go path conventions, mandatory test/race/arch/guard verification gates)
  - AGENTS.md: verified aligned (canonical runtime engineering rulebook)
- Follow-up TODOs: None
-->

# Synapse-OS Constitution

## Core Principles

### I. Enterprise-First & V1 Commercial Scope (NON-NEGOTIABLE)
Synapse-OS MUST be designed from the enterprise and employee collaboration
perspective so that all conversations, tasks, files, and skills accumulate as
company digital assets rather than single-user personal utilities.

- Minimal commercial delivery MUST require only a single static Go binary and a
  single PostgreSQL 17 database. Introducing Redis clients, distributed cache
  abstractions, or ClickHouse drivers is strictly FORBIDDEN in V1.
- Public self-registration MUST remain closed: `POST /api/v1/orgs` MUST NOT be
  registered, and `GET /api/v1/orgs/registration` MUST always return
  `{"open":false}`. Tenant onboarding MUST go through `bootstrap-org` CLI or
  platform super-admin controls.
- V1 GA runtimes are locked to Claude Code (with WarmPool) and Codex; prebuilt
  enterprise integrations are scoped to Feishu and DingTalk plus generic custom
  MCP connectors. Legacy consumer presets (24 character presets, 3 seed skills,
  WeChat/Google/anonymous Feishu scrapers, GitHub/Gitee/Notion/Confluence
  prebuilt connectors, and deprecated columns) MUST NOT be ported.

### II. Strict L0–L6 Unidirectional Layering & Explicit Wiring
All backend packages under `synapse-server/internal/` MUST strictly follow
unidirectional downward dependencies:
`L6 (cmd) -> L5 (web/ui) -> L4 (orchestrator/event) -> L3 (agent/task/cost/docs) -> L2 (platform/files/project/skills) -> L1 (core contracts / storage / runtime) -> L0 (infra / core primitives)`.

- Lower layers MUST NOT import higher layers. Sibling packages `internal/agent`
  and `internal/task` MUST NOT import each other; shared logic MUST sink to L2
  or decouple through L1 `internal/core/spi`.
- Architectural boundaries MUST be mechanically enforced on every test run via
  AST analysis in `internal/arch_test.go`.
- Dependency injection MUST be explicit and reflection-free inside
  `cmd/synapse-server/wire.go`; reflection DI containers are FORBIDDEN.
- `internal/web` MUST remain transport-only (validation, auth, service dispatch,
  response mapping) with zero business logic. Built-in MCP tools MUST implement
  `McpToolProvider` inside their owning domain package.

### III. Fail-Closed Multi-Tenant Isolation & Envelope Encryption
Every company resource is tenant-scoped and MUST fail closed when tenant
identity is missing or mismatched.

- Tenant identity MUST flow explicitly via `context.Context`. All tenant-scoped
  reads and writes MUST invoke `tenant.Require(ctx)` and fail immediately when
  absent; falling back to a default tenant is FORBIDDEN.
- Asynchronous background goroutines (chat streams, task workers, async events)
  MUST derive contexts via `tenant.Detach(ctx)` to preserve `company_id`,
  `request_id`, and `user_id` while stripping HTTP cancellation and DB
  transaction handles.
- Multi-tenant background loops MUST use `FairRunner` rotation with per-tenant
  `recover()` isolation so one tenant failure never starves or crashes others.
- New settings MUST default to per-company scope; adding new global settings is
  FORBIDDEN (only CLI installation, model price catalog, and built-in MCP
  address are platform-wide).
- All tenant secrets MUST be encrypted via `SecretCipher` (per-tenant
  AES-256-GCM DEK envelope with `company_id` AAD). Secrets, tokens, and DSN
  passwords MUST NEVER be written in plaintext to disk or logs. Missing tenant
  API keys MUST return HTTP `403` and MUST NEVER fall back to host environment
  variables.

### IV. Infrastructure & Egress Isolation
Low-level I/O boundaries MUST be centralized behind auditable contracts so
security controls and V2 sandboxing require zero domain-layer rewrites.

- **Database isolation**: Only `internal/infra/db`, `internal/storage/postgres`,
  and `cmd/synapse-server` MAY import `pgx` or `goose`. All business packages
  MUST depend solely on `internal/core/store` interfaces and `TxManager`.
- **Subprocess isolation**: Business and runtime adapters MUST NOT call
  `os/exec.Command` directly. Every CLI and STDIO MCP subprocess MUST launch
  through `spi.ProcessLauncher` (enforcing `Setpgid: true` process-group kills,
  host secret env scrubbing, and `0600` temp files).
- **Outbound HTTP isolation**: Direct `http.Client` construction is FORBIDDEN.
  All outbound HTTP traffic MUST use `internal/infra/httpclient` with physical
  separation between `TrustedClient` (enterprise proxy + private CA for trusted
  upstreams) and `UntrustedEgressClient` (mandatory `DialContext`-level
  anti-DNS-Rebinding `EgressGuard`, <=6 redirect re-checks, and `io.LimitReader`
  body cap).

### V. PostgreSQL 17 Schema Discipline & Forward-Only Migrations
Database schemas MUST stay portable, auditable, and strictly tenant-partitioned.

- Table and column names MUST use lowercase singular `snake_case`; indexes MUST
  use `idx_<table>_<cols>` or `uq_<table>_<cols>`; migrations MUST reside in
  `internal/storage/postgres/migrations/<5-digit>_<snake_case>.sql` embedded via
  `//go:embed`.
- Except global tables (`company`, `user_session`, `goose_db_version`), 100% of
  tables MUST declare `company_id VARCHAR(64) NOT NULL` as the first column of
  the composite primary key (`PRIMARY KEY (company_id, id)`), enforced by
  `internal/storage/postgres/guard_test.go`.
- `goose/v3` migrations MUST be forward-only; `-- +goose Up` MUST NOT contain
  `DROP TABLE` (validated by `ValidateMigrations` prior to execution).
- Schemas MUST use zero physical foreign keys (preserving audit history across
  soft deletes), `TIMESTAMPTZ NOT NULL` written in UTC (`TimeZone=UTC`),
  `NUMERIC(16,6)` for currency (fixed-point or micro-USD integer arithmetic in
  Go; `float64` billing accumulation is FORBIDDEN), and `JSONB` for JSON
  payloads. Hard-to-migrate PG-specific features (`CREATE TYPE ... AS ENUM`,
  stored procedures, array columns) MUST NOT be used.
- Idempotent writes MUST use `ON CONFLICT ... DO NOTHING` or `DO UPDATE`;
  `app_user` MUST NEVER be overwritten on `username` conflict and MUST update
  explicitly by `(company_id, id)`. Slow-query tracers MUST NEVER log bound SQL
  parameter values.

### VI. Resilience, Bounded Concurrency & Graceful Shutdown
All I/O, background execution, memory structures, and shutdown sequences MUST
enforce explicit bounds and deterministic recovery.

- **Layered timeouts**: `pgxpool` defaults (`MaxConns=16`, `MinConns=2`,
  `ConnectTimeout=5s`, `StatementTimeout=30s`, `SlowQueryThreshold=200ms`,
  health `Ping=2s`); standard HTTP (`Dial=5s/10s`, `TLS=10s`, `Header=30s`,
  `Total=60s`). Streaming/SSE clients (`Streaming: true`) MUST set
  `http.Client.Timeout = 0` and control lifetime via `ctx` and `IdleReadTimeout`
  (chat SSE 600s; task link prefetch max 8 URLs sharing 90s).
- **Retries & circuit breaking**: Retries MUST use exponential backoff + Full
  Jitter (default `MaxAttempts=3`, streaming forced to `1`) only for idempotent
  requests on `429` (`Retry-After <= 30s`), `502/503/504`, and transient network
  errors; NEVER retry `EgressDenied`, TLS errors, or `context.Canceled`.
  `sony/gobreaker/v2` circuit breakers MUST scope by `service + host` with
  `MaxHostEntries=256`. `httpclient` MUST only return structured `*ClientError`
  classifications and MUST NEVER embed business fallback strategies.
- **Bounded goroutines & memory**: Unmanaged `go func()` loops are FORBIDDEN;
  background loops MUST register with `lifecycle.BackgroundGroup` (panic
  recovery). Async event bus concurrency MUST cap at 64 with `Drain`.
  `ClaudeWarmPool` MUST use O(1) LRU (max 20, idle 10m, 8-dim SHA-256
  fingerprint) and close processes outside locks. `LiveTurn` SSE fan-out MUST be
  non-blocking. All `memcache` caches/limiters MUST set `MaxEntries`/`MaxKeys`,
  untrusted responses MUST cap at 20 MiB, and file uploads at 50 MiB.
- **Graceful shutdown**: `SIGINT`/`SIGTERM` MUST execute 4-phase reverse-order
  shutdown within 30s (HTTP drain <=15s -> background loops <=8s -> IM WS &
  active runs/WarmPool `SIGTERM->2s->SIGKILL` <=5s -> `pgxpool` close <=2s),
  with container/script `stop_grace_period >= 35s`.

### VII. API Contract Fidelity, Go Style & Verification Discipline
External contracts and Go source quality MUST remain consistent, predictable,
and verified before merge.

- **API & protocol rules**: Business REST routes MUST live under
  `/api/v1/<plural-kebab-case>`; Gin middleware MUST preserve exact 6-stage
  order (`Recovery -> TraceID -> Auth -> Tenant -> PasswordChange ->
  RequireAdmin`). `X-Request-Id` MUST be validated against
  `^[A-Za-z0-9._-]{1,64}$` before propagation. Standard endpoints MUST return
  `{"code":"ok","message":"success","data":...}` with errors mapped centrally
  by `HandleError` via `errors.As`, strictly preserving the 6 protocol
  exceptions (raw login JSON, empty 200 logout, SSE with `X-Accel-Buffering: no`
  and per-frame `Flush()`, binary/sandboxed text previews with `nosniff` +
  `CSP: sandbox`, OAuth 302, and raw JSON-RPC 2.0 `POST /mcp` wrapping domain
  errors as HTTP `200` with `result.isError: true`).
- **DTOs & config schemas**: JSON fields MUST use `camelCase` via explicit tags;
  DTOs MUST decouple from DB entities and strip sensitive fields; pagination
  MUST be 1-based with hard `size` caps (`100`/`200`); YAML loading MUST enable
  `KnownFields(true)` with fail-fast `Validate()`.
- **Idiomatic Go**: Packages MUST use short lowercase single words; files use
  `snake_case.go`; interfaces use capability names without `I` prefix or `Impl`
  suffix; acronyms keep uniform case (`ID`, `URL`, `HTTP`, `MCP`, `JSON`, `SSE`,
  `API`, `UUID`, `SQL`). Every `error` MUST be handled explicitly once (wrap
  with `%w`, inspect with `errors.Is`/`errors.As`, never log-and-return in
  lower layers, no `panic` in production paths). Accept interfaces, return
  concrete types; `ctx context.Context` MUST be the first parameter and MUST
  NEVER be stored in struct fields; NEVER perform I/O or process teardown while
  holding `sync.Mutex`.
- **Testing & comments**: Prefer self-documenting code (comment only "why",
  invariants, and concurrency/security traps). Write convergent table-driven
  tests with `t.Parallel()`; PG integration tests MUST isolate via
  `db.NewTestSchemaDB` and auto-skip when `TEST_POSTGRES_DSN` is unset.

## Technology Stack & Operational Constraints

The following stack is locked for V1 (`synapse-server/` module
`github.com/Karlsk/synapse-os`):

- **Language & Build**: Go 1.26.1, Go Modules (`go.mod`/`go.sum` committed),
  zero CGO (`CGO_ENABLED=0`), single static binary delivery.
- **Core Libraries**: `gin-gonic/gin`, `jackc/pgx/v5` (`pgxpool`),
  `pressly/goose/v3` (`//go:embed`), `gopkg.in/yaml.v3`, `sony/gobreaker/v2`,
  `robfig/cron/v3`, `prometheus/client_golang`, `golang.org/x/crypto/bcrypt`,
  `aws/aws-sdk-go-v2/service/s3`, Feishu Go SDK, `coder/websocket`,
  `golang.org/x/net/html`, and Go standard library (`log/slog`, `crypto/aes`,
  `crypto/cipher`, `crypto/subtle`, `embed.FS`).
- **CLI Runtime & Frontend**: Node.js 24 + npm (supporting `NPM_CONFIG_REGISTRY`
  and private Node mirrors); V1 embeds static SPA via `embed.FS` with `--ui-dir`
  hot reload; V2 evolves modern frontend under `synapse-web/`.
- **Container Delivery**: Multi-stage `golang:1.26-bookworm` ->
  `node:24-bookworm-slim` + `tini`, running as non-root `uid=10001` with
  `NPM_CONFIG_PREFIX=/opt/cli` and `postgres:17-alpine`.
- **Prohibited Dependencies**: Redis clients, ClickHouse drivers, reflection DI
  containers, and heavy ORMs (e.g., GORM) are strictly prohibited.

## Development Workflow & Quality Gates

Every PR and implementation phase MUST pass all verification gates in
`synapse-server/` prior to merge:

1. **Formatting & Static Analysis**: `gofmt -l .` produces zero output and
   `go vet ./...` exits cleanly.
2. **Build & Race-Checked Tests**: `go build ./...` and `go test -race ./...`
   pass completely, including AST boundary checks (`internal/arch_test.go`) and
   tenant schema guards (`internal/storage/postgres/guard_test.go`).
3. **Database Integration Verification**: When touching `internal/infra/db` or
   `internal/storage/postgres`, run
   `TEST_POSTGRES_DSN="postgres://..." go test -race ./internal/infra/db/... ./internal/storage/postgres/...`.
4. **Release Build Smoke Check**: `CGO_ENABLED=0 go build -trimpath -o bin/synapse-server ./cmd/synapse-server`
   succeeds and `GET /api/v1/meta` responds healthy when served.

## Governance

This constitution supersedes ad-hoc engineering practices and informal shortcuts
across `synapse-os`. All specifications (`/speckit-specify`), technical plans
(`/speckit-plan`), task breakdowns (`/speckit-tasks`), and code reviews MUST
explicitly verify compliance against Principles I–VII; any intentional
exception MUST be documented in the plan's **Complexity Tracking** table with
rationale and rejected simpler alternatives.

- **Amendment Procedure**: Changes to this constitution MUST be proposed via
  explicit review, include an updated Sync Impact Report at the top of this
  file, and propagate required adjustments to `.specify/templates/` and
  `AGENTS.md`.
- **Versioning Policy**: Semantic versioning (`MAJOR.MINOR.PATCH`):
  - `MAJOR`: Backward-incompatible principle removals or redefinitions.
  - `MINOR`: New principle/section additions or materially expanded rules.
  - `PATCH`: Clarifications, wording improvements, or non-semantic fixes.
- **Runtime Guidance**: Use [AGENTS.md](../../AGENTS.md) as the concise
  day-to-day operational rulebook mirroring this constitution, and
  `docs/SuperAgent-Go/DDD/` for PR-level specifications.

**Version**: 1.0.0 | **Ratified**: 2026-11-25 | **Last Amended**: 2026-11-25
