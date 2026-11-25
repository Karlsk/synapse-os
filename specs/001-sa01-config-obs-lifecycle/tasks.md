# Tasks: SA-01 L0 Core Foundation — Config, Observability, Domain Errors & Lifecycle Governance

**Input**: Design documents from `/specs/001-sa01-config-obs-lifecycle/` and frozen PR Spec `docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md`

**Prerequisites**: `plan.md` (required), `spec.md` (required), `research.md`, `data-model.md`, `contracts/l0-contracts.md`, `quickstart.md`

**Tests**: TDD is MANDATORY per PR Spec `§6` & `§8` — every test task MUST be written first (RED) before its corresponding implementation (GREEN) and refactored under race-checked test protection.

**Organization**: Tasks are grouped by user story (`US1`–`US4`, mapping 1:1 to the 4 independent L0 foundation packages) so each package can be implemented and verified independently.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (`[US1]`..`[US4]`)
- Exact file paths included in every task description

## Path Conventions

- Backend module root: `synapse-server/` (`module github.com/Karlsk/synapse-os`, Go `1.26.1`)
- L0 packages: `synapse-server/internal/core/apperror/`, `synapse-server/internal/infra/{config,obs,lifecycle}/`
- Config template & docs: `synapse-server/configs/config.example.yaml`, `synapse-server/README.md`, `docs/SuperAgent-Go/DDD/README.md`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify pinned dependencies and L0 directory skeleton (maps to PR Spec `§6 T1`)

- [X] T001 Verify `synapse-server/go.mod` contains `go 1.26.1` and required whitelisted dependencies (`gopkg.in/yaml.v3`, `golang.org/x/net`, `github.com/prometheus/client_golang`) with zero Redis/ClickHouse/GORM entries, and verify directory skeleton exists for `synapse-server/internal/core/apperror/`, `synapse-server/internal/infra/{config,obs,lifecycle}/`, and `synapse-server/configs/`

**Checkpoint**: Module dependencies and L0 package directories verified; all 4 L0 stories have zero mutual dependencies and can proceed cleanly.

---

## Phase 2: User Story 1 - Unified Domain Error Classification & Safe External Mapping (Priority: P1) 🎯 MVP

**Goal**: Deliver zero-dependency leaf package `internal/core/apperror` with exact 13 codes, HTTP status mapping, `ErrEgressDenied` sentinel, `FromError` sanitization, and `UnreadableMaterialsError` formatting (maps to PR Spec `§2 #1`, `§6 T2`, `§8 #1`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/core/apperror/...` passes with ≥ 85% coverage and `go list -f '{{join .Imports "\n"}}' ./internal/core/apperror` lists only Go stdlib packages.

### Tests for User Story 1 (TDD RED First) ⚠️

- [X] T002 [P] [US1] Write TDD table-driven unit tests covering all 13 `Code` constants, all 12 domain error constructors + `Wrap`, `EgressDenied` matching `errors.Is(err, ErrEgressDenied)` with HTTP 403, `AppError.Error()`/`Unwrap()`/`Is()` (including nil receiver), `FromError` sanitizing unknown internal errors to HTTP 500 `"internal server error"`, `HTTPStatusOf`/`CodeOf`, and `UnreadableLinkError`/`UnreadableMaterialsError` Chinese multi-line formatting + HTTP 409 conversion in `synapse-server/internal/core/apperror/errors_test.go`

### Implementation for User Story 1 (GREEN → REFACTOR)

- [X] T003 [US1] Implement stdlib-only `Code`, `ErrEgressDenied`, `AppError`, 13 error constructors, `Wrap`, `FromError`, `HTTPStatusOf`, `CodeOf`, `UnreadableLinkError`, and `UnreadableMaterialsError` matching PR Spec `§4.1A` verbatim in `synapse-server/internal/core/apperror/errors.go`

**Checkpoint**: `internal/core/apperror` compiles, passes `go test -race -cover` (≥ 85%), and imports strictly Go stdlib only.

---

## Phase 3: User Story 2 - Enterprise Multi-Source Configuration, Fail-Fast Validation & Proxy/CA Runtime (Priority: P1)

**Goal**: Deliver strongly typed 22-section `internal/infra/config` (zero Redis), `CLI > Env > YAML > Default` loader with `KnownFields(true)`, aggregated fail-fast `Validate()`, `BuildNetworkRuntime` (proxy + loopback `NO_PROXY` guarantee + enterprise PEM CA + child env), and `configs/config.example.yaml` (maps to PR Spec `§2 #2,#3,#4`, `§6 T3`, `§8 #2,#3,#4`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/infra/config/...` passes with ≥ 85% coverage, including strict parsing and validation of real `synapse-server/configs/config.example.yaml`.

### Tests for User Story 2 (TDD RED First) ⚠️

- [X] T004 [P] [US2] Write TDD unit tests covering `CLI > Env > YAML > Default` precedence, `SERVER_PORT` bare-port normalization, `SYNAPSE_DATA_DIR` 7-path relocation, `KnownFields(true)` typo rejection, aggregated multi-error `Validate()` (invalid DSN, non-32B base64 `secret.key`, `max_conns < min_conns`, invalid proxy URL, invalid `log_level`/`log_format`, corrupt `ca_bundle_file`, invalid file limits, `task.max_parallel <= 0`, invalid timezone), and loading/validating `synapse-server/configs/config.example.yaml` with zero Redis keys in `synapse-server/internal/infra/config/config_test.go`
- [X] T005 [P] [US2] Write TDD unit tests covering `BuildNetworkRuntime` loopback `NO_PROXY` protection (`127.0.0.1`, `localhost`, `::1` always return `nil` proxy even with custom `no_proxy: "corp.local"`), external host proxy routing, self-signed PEM CA addition to `CertPool`/`TLSConfig` (TLS 1.2+), corrupt/missing CA file fail-fast error, and `ChildEnv` (`HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `NPM_CONFIG_REGISTRY`) in `synapse-server/internal/infra/config/network_test.go`

### Implementation for User Story 2 (GREEN → REFACTOR)

- [X] T006 [US2] Implement 22-section `Config` structs (including `PostgresConfig` defaults per `§4.4`, `PlatformConfig`, zero Redis) and `Default()` in `synapse-server/internal/infra/config/config.go`
- [X] T007 [US2] Implement `Overrides`, `Load`, `LoadFromBytes` (`yaml.Decoder.KnownFields(true)`), and `applyEnvOverrides` (supporting `SYNAPSE_*`, `SERVER_PORT`, `DATABASE_URL`, `SECRET_KEY`, `SUPERAGENT_*`, `HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`, `NODE_EXTRA_CA_CERTS`, `NPM_CONFIG_REGISTRY`, `SYNAPSE_DATA_DIR`) in `synapse-server/internal/infra/config/load.go`
- [X] T008 [US2] Implement fail-fast aggregated `Config.Validate()` using `errors.Join` matching PR Spec `§5.2` in `synapse-server/internal/infra/config/validate.go`
- [X] T009 [US2] Implement `NetworkRuntime`, `BuildNetworkRuntime`, and `ensureLocalNoProxy` (`127.0.0.1,localhost,::1` floor + dedup) using `golang.org/x/net/http/httpproxy` in `synapse-server/internal/infra/config/network.go`
- [X] T010 [P] [US2] Create complete commented commercial configuration template matching `config.go` YAML tags 100% with zero Redis sections in `synapse-server/configs/config.example.yaml`

**Checkpoint**: `internal/infra/config` compiles, passes `go test -race -cover` (≥ 85%), and validates real `configs/config.example.yaml` with zero unknown fields and zero Redis entries.

---

## Phase 4: User Story 3 - Context-Aware Redacting Logging & Isolated Metrics Registry (Priority: P2)

**Goal**: Deliver `internal/infra/obs` with request/company/user context propagation, `SanitizeRequestID` log-injection defense, `ContextHandler -> RedactingHandler -> Text/JSON` logger (with optional `0640` file dual-write and `RedactDSN`), and isolated Prometheus Registry carrying `application` label + 6 `superagent_*` business metrics + HTTP latency histogram (maps to PR Spec `§2 #5,#6`, `§6 T4`, `§8 #5,#6,#7`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/infra/obs/...` passes with ≥ 85% coverage, verifying zero plaintext secret/DSN leakage and collision-free repeated `NewMetrics` creation.

### Tests for User Story 3 (TDD RED First) ⚠️

- [X] T011 [P] [US3] Write TDD unit tests covering `SanitizeRequestID` (valid preservation vs CRLF/space/empty/>64-char 32-hex replacement), `With*`/`*From`/`CopyMeta` context injection in JSON/text logs, `RedactingHandler` (top-level sensitive keys, `WithAttrs`, nested `slog.Group`, `WithGroup`, inline `Bearer`/`Basic` & PostgreSQL DSN password scrubbing), `RedactDSN`, `NewLogger` (`0640` file dual-write + close + invalid level/format error), and repeated `NewMetrics("synapse-server")` isolation verifying all 6 `superagent_*` metrics + `http_server_requests_seconds` carry `application="synapse-server"` in `synapse-server/internal/infra/obs/obs_test.go`

### Implementation for User Story 3 (GREEN → REFACTOR)

- [X] T012 [P] [US3] Implement `RequestIDHeader`, `AttrRequestID`, `AttrCompanyID`, `AttrUserID`, `WithRequestID`, `RequestIDFrom`, `WithCompanyID`, `CompanyIDFrom`, `WithUserID`, `UserIDFrom`, `CopyMeta`, and `SanitizeRequestID` in `synapse-server/internal/infra/obs/context.go`
- [X] T013 [P] [US3] Implement `RedactedValue`, `NewRedactingHandler` (redacting across `Handle`, `WithAttrs`, `WithGroup`, recursive `slog.KindGroup`, sensitive key patterns, `Bearer`/`Basic`, and DSN credentials), and `RedactDSN` in `synapse-server/internal/infra/obs/redact.go`
- [X] T014 [US3] Implement `LoggerConfig`, `NewLogger` (level/format parsing, `ContextHandler -> RedactingHandler -> Text/JSON`, optional `0640` file dual-write + close handle), and `NewContextHandler` in `synapse-server/internal/infra/obs/logger.go`
- [X] T015 [P] [US3] Implement `Metrics`, `NewMetrics` (fresh `prometheus.NewRegistry()` + `WrapRegistererWith` `application` label + Go/Process collectors + 6 `superagent_*` metrics + `http_server_requests_seconds`), `Registerer()`, and `Gatherer()` in `synapse-server/internal/infra/obs/metrics.go`

**Checkpoint**: `internal/infra/obs` compiles and passes `go test -race -cover` (≥ 85%) with zero global registry pollution and zero secret leakage.

---

## Phase 5: User Story 4 - Managed Background Execution & 30-Second Reverse-Order Graceful Shutdown (Priority: P2)

**Goal**: Deliver `internal/infra/lifecycle` with panic-isolated `BackgroundGroup` (stuck-task timeout reporting, post-stop rejection) and 5-phase reverse-order `ShutdownManager` (intra-phase LIFO, total budget clamping, `NotifySignalContext`) (maps to PR Spec `§2 #7`, `§6 T5`, `§8 #8,#9`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/infra/lifecycle/...` passes with ≥ 85% coverage under `-race`.

### Tests for User Story 4 (TDD RED First) ⚠️

- [X] T016 [P] [US4] Write TDD unit tests covering `BackgroundGroup` (panic recovery firing `WithOnPanic` + log while sibling goroutine survives until `Stop`, `GoLoop`, `Active()`, post-stop task rejection, non-canceled error aggregation, stuck goroutine exceeding `Stop` timeout listing stuck task name) and `ShutdownManager` (strict `PhaseHTTP -> PhaseBackground -> PhaseConnections -> PhaseStorage -> PhaseFlush` order, intra-phase LIFO order, 200ms total budget clamping slow hook and reporting exhaustion for skipped hooks, `NotifySignalContext` cancellation) in `synapse-server/internal/infra/lifecycle/lifecycle_test.go`

### Implementation for User Story 4 (GREEN → REFACTOR)

- [X] T017 [P] [US4] Implement `Loop`, `GroupOption`, `WithLogger`, `WithOnPanic`, `BackgroundGroup`, `NewBackgroundGroup`, `Context`, `Go`, `GoLoop`, `Active`, and `Stop` matching PR Spec `§4.1D` & `§5.4` in `synapse-server/internal/infra/lifecycle/group.go`
- [X] T018 [P] [US4] Implement `ShutdownPhase` (`PhaseHTTP`..`PhaseFlush`), `ShutdownManager`, `NewShutdownManager`, `Register`, `Shutdown`, and `NotifySignalContext` matching PR Spec `§4.1D` & `§5.4` in `synapse-server/internal/infra/lifecycle/shutdown.go`

**Checkpoint**: `internal/infra/lifecycle` compiles and passes `go test -race -cover` (≥ 85%) with zero data races or goroutine leaks.

---

## Phase 6: Polish, Documentation Sync & Mandatory Quality Gates

**Purpose**: Complete PR Spec `§7` documentation sync and `§6 T6` / `§9` acceptance gates

- [X] T019 [P] Create `synapse-server/README.md` documenting L0 foundation package responsibilities (`apperror`, `config`, `obs`, `lifecycle`) and the environment variable override precedence table (`spec_01 §7`)
- [X] T020 [P] Update `docs/SuperAgent-Go/DDD/README.md` implementation board row for `SA-01` to reflect implementation status (pending user commit per Git red line, `spec_01 §7`)
- [X] T021 Run all hard quality gates in `synapse-server/`: `gofmt -l .` (zero output), `go vet ./...`, `go build ./...`, `go test -race -cover ./internal/core/apperror/... ./internal/infra/config/... ./internal/infra/obs/... ./internal/infra/lifecycle/...` (verify ≥ 85% coverage per package), `go test -race ./...`, verify `internal/core/apperror` imports Go stdlib only (`go list`), and verify zero Redis references in `configs/config.example.yaml` and `go.mod`

---

## Dependencies & Execution Order

### Phase Dependencies

- **Phase 1 (Setup)**: No dependencies — verify `go.mod` and directories first.
- **Phases 2–5 (US1 `apperror`, US2 `config`, US3 `obs`, US4 `lifecycle`)**: All depend only on Phase 1 and have **zero mutual dependencies** — they can execute sequentially in priority order (`US1 -> US2 -> US3 -> US4`) or in parallel.
- **Phase 6 (Polish & Gates)**: Depends on Phases 2–5 completing.

### Within Each User Story (Strict TDD)

1. Write `*_test.go` first → run `go test` to confirm RED (compile/assertion failure).
2. Implement production `*.go` matching frozen `spec_01 §4.1` verbatim → run `go test -race -count=1 ./<pkg>/...` to confirm GREEN.
3. Refactor for clarity/coverage (≥ 85%) and re-verify GREEN before moving to next story.

### Parallel Opportunities

- All 4 test files (`T002`, `T004`+`T005`, `T011`, `T016`) are in distinct packages and can be drafted in parallel.
- Within `US3`, `context.go` (`T012`), `redact.go` (`T013`), and `metrics.go` (`T015`) have no mutual dependencies before `logger.go` (`T014`).
- Within `US4`, `group.go` (`T017`) and `shutdown.go` (`T018`) are independent files.
- Documentation tasks (`T019`, `T020`) are independent of each other.
