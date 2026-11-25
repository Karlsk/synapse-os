# Implementation Plan: SA-01 L0 Core Foundation — Config, Observability, Domain Errors & Lifecycle Governance

**Branch**: `001-sa01-config-obs-lifecycle` | **Date**: 2026-11-25 | **Spec**: [spec.md](./spec.md) | **PR Spec**: [docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md](../../docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md)

**Input**: Feature specification from `specs/001-sa01-config-obs-lifecycle/spec.md` and frozen PR contract `docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md`.

## Summary

Implement the 4 atomic L0 runtime foundation packages under `synapse-server/` plus the commercial example configuration template:
1. `internal/core/apperror` — zero-dependency leaf package providing all 13 `Code` constants, `ErrEgressDenied` sentinel, `AppError` constructors/unwrapping/`Is`/`FromError`/`HTTPStatusOf`/`CodeOf`, and `UnreadableLinkError` / `UnreadableMaterialsError`.
2. `internal/infra/config` + `configs/config.example.yaml` — 22-section strongly typed configuration tree (zero Redis), `gopkg.in/yaml.v3` (`KnownFields(true)`), `CLI > Env > YAML > Default` loader, aggregated fail-fast `Validate()`, and `BuildNetworkRuntime` (corporate HTTP/HTTPS proxy, guaranteed loopback `NO_PROXY`, system + custom PEM CA `x509.CertPool` with TLS 1.2+, and standardized child-process env map).
3. `internal/infra/obs` — request/company/user context metadata propagation (`CopyMeta`), `SanitizeRequestID` log-injection defense, `slog` `ContextHandler -> RedactingHandler -> Text/JSON` pipeline with optional `0640` file dual-write and `RedactDSN`, and isolated per-instance Prometheus Registry carrying constant `application` label + 6 `superagent_*` business metrics + `http_server_requests_seconds`.
4. `internal/infra/lifecycle` — panic-isolated `BackgroundGroup` (with stuck-task timeout reporting and post-stop rejection) and 5-phase reverse-order `ShutdownManager` (30s default budget clamping + `NotifySignalContext`).

## Technical Context

**Language/Version**: Go `1.26.1` (`synapse-server/`, `module github.com/Karlsk/synapse-os`, Go Modules, `CGO_ENABLED=0` static binary compatible)

**Primary Dependencies**:
- `internal/core/apperror`: Go standard library ONLY (`errors`, `fmt`, `net/http`, `strings`) — zero internal or third-party imports
- `internal/infra/config`: `gopkg.in/yaml.v3`, `golang.org/x/net/http/httpproxy`, Go stdlib (`crypto/tls`, `crypto/x509`, `encoding/base64`, `net/http`, `net/url`, `os`, `path/filepath`, `strconv`, `strings`, `time`)
- `internal/infra/obs`: `github.com/prometheus/client_golang/prometheus`, `github.com/prometheus/client_golang/prometheus/collectors`, Go stdlib (`context`, `crypto/rand`, `encoding/hex`, `io`, `log/slog`, `net/url`, `os`, `regexp`, `strings`)
- `internal/infra/lifecycle`: Go standard library ONLY (`context`, `errors`, `fmt`, `log/slog`, `os`, `os/signal`, `runtime/debug`, `slices`, `strings`, `sync`, `sync/atomic`, `syscall`, `time`)

**Storage**: N/A (L0 stateless runtime foundation; zero SQL migrations, zero `pgx`/`goose` imports; only defines `config.PostgresConfig` consumed by `SA-02`)

**Testing**: Colocated Go `*_test.go` table-driven unit tests with `t.Parallel()`, in-memory `bytes.Buffer` log capture, `t.TempDir()` PEM/log/config fixtures, synthetic `lookupEnv` functions, and `go test -race -cover`

**Target Platform**: Linux (`linux/amd64`, `linux/arm64`) & macOS (`darwin/arm64`) server runtime

**Project Type**: Backend Go server foundational libraries (`synapse-server/internal/core/apperror`, `synapse-server/internal/infra/{config,obs,lifecycle}`)

**Performance Goals**: Zero global lock contention in `obs.Metrics` (isolated registry), O(1) error classification without allocations on nil, bounded shutdown execution strictly clamped to `totalBudget` (default 30s), zero goroutine leaks under `-race`

**Constraints**: Zero Redis / ClickHouse / GORM / reflection DI; `internal/core/apperror` strictly stdlib-only; `gofmt -l .` zero diff; `go vet ./...` zero warnings; `go test -race ./...` green; core package line coverage ≥ 85% per PR Spec `§9`

**Scale/Scope**: 4 L0 packages + `configs/config.example.yaml` + `synapse-server/README.md` (~1,000–1,400 LOC including comprehensive TDD tests); consumed by `SA-02`–`SA-42`

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Verify compliance with `.specify/memory/constitution.md` (v1.0.1) and `AGENTS.md`:

- [x] **V1 Scope & Zero Redundant Infra**: Single Go binary + PG17 architecture preserved; `config.Config` and `configs/config.example.yaml` contain zero Redis/ClickHouse entries; only whitelisted `go.mod` dependencies used (`gopkg.in/yaml.v3`, `golang.org/x/net`, `prometheus/client_golang`).
- [x] **L0–L6 Layering & Explicit Wiring**: All 4 packages belong strictly to L0 (`internal/core/apperror` and `internal/infra/{config,obs,lifecycle}`), with zero upward imports and zero cross-layer violations (`apperror` has zero internal/external imports; `config`, `obs`, `lifecycle` have zero imports of L1–L6 or each other).
- [x] **Multi-Tenant Fail-Closed & Crypto**: `obs` context helpers carry `company_id` strictly for logging/tracing (explicitly documented never to be trusted for auth/tenant isolation); `RedactingHandler` and `RedactDSN` enforce zero plaintext secret/DSN leakage in logs; `Secret.Key` validates 32-byte base64 KEK fail-fast.
- [x] **Infrastructure Isolation**: Zero `pgx`/`goose` imports; zero `os/exec.Command` calls; zero raw `http.Client` construction (`config.BuildNetworkRuntime` only produces `*x509.CertPool`, `*tls.Config`, `ProxyFunc`, and `ChildEnv` for `SA-03` and `SA-11`).
- [x] **PG17 Schema & Migrations**: N/A for SA-01 (no DDL/SQL). `PostgresConfig` enforces UTC `TimeZone=UTC` default DSN and pool bounds (`MaxConns=16`, `MinConns=2`, `MaxConnLifetime=30m`, `MaxConnIdleTime=5m`, `SlowQueryThreshold=200ms`, `AutoMigrate=true`).
- [x] **Resilience & Concurrency**: `BackgroundGroup` wraps every goroutine with `panic recover()` + stack trace + optional callback; `ShutdownManager` enforces 5-phase ascending / intra-phase LIFO ordering clamped within 30s total budget; no I/O while holding `sync.Mutex`; `ctx` is always first parameter (except `BackgroundGroup` root context lifecycle manager per frozen §4.1 contract, which wraps `context.WithCancel(parent)`).
- [x] **API, MCP & Go Quality Gates**: `apperror` freezes all 13 `Code` values and HTTP status mappings matching `/api/v1` and Java `GlobalExceptionHandler`; `yaml.v3` uses `KnownFields(true)` + fail-fast `Validate()`; verified with `gofmt`, `go vet`, `go build`, `go test -race -cover` (≥85%).

*Post-Phase-1 Design Re-check*: PASS — All 7 gates remain satisfied with zero exceptions.

## Project Structure

### Documentation (this feature)

```text
specs/001-sa01-config-obs-lifecycle/
├── plan.md              # This file (/speckit-plan command output)
├── research.md          # Phase 0 output
├── data-model.md        # Phase 1 output
├── quickstart.md        # Phase 1 output
├── contracts/
│   └── l0-contracts.md  # Phase 1 frozen Go contract mirror
├── checklists/
│   └── requirements.md  # Spec quality checklist
└── tasks.md             # Phase 2 output (/speckit-tasks)
```

### Source Code (repository root)

```text
synapse-server/
├── README.md                                  # L0 package roles & env override precedence doc (Spec §7)
├── configs/
│   └── config.example.yaml                    # Commercial commented config template (zero Redis)
└── internal/
    ├── core/
    │   └── apperror/                          # [L0 leaf] Zero external/internal deps
    │       ├── errors.go                      # 13 Codes, ErrEgressDenied, AppError, constructors, FromError, Unreadable*
    │       └── errors_test.go                 # §8 Test #1
    └── infra/
        ├── config/                            # [L0 infra] YAML/Env/CLI config + proxy/CA runtime
        │   ├── config.go                      # 22 strongly typed config structs + Default()
        │   ├── load.go                        # Load, LoadFromBytes, applyEnvOverrides, CLI Overrides
        │   ├── validate.go                    # Aggregated fail-fast Config.Validate()
        │   ├── network.go                     # BuildNetworkRuntime, ensureLocalNoProxy, CA pool, ChildEnv
        │   ├── config_test.go                 # §8 Tests #2 & #3 (incl. configs/config.example.yaml guard)
        │   └── network_test.go                # §8 Test #4
        ├── obs/                               # [L0 infra] Context slog + redaction + isolated Prometheus
        │   ├── context.go                     # With/From RequestID/CompanyID/UserID, CopyMeta, SanitizeRequestID
        │   ├── redact.go                      # RedactingHandler (groups/WithAttrs/inline Bearer/DSN), RedactDSN
        │   ├── logger.go                      # LoggerConfig, NewLogger (text/json, 0640 file dual-write), ContextHandler
        │   ├── metrics.go                     # NewMetrics (isolated Registry, WrapRegistererWith), 6 business + HTTP metrics
        │   └── obs_test.go                    # §8 Tests #5, #6 & #7
        └── lifecycle/                         # [L0 infra] Managed goroutines & 30s staged shutdown
            ├── group.go                       # Loop, BackgroundGroup, GroupOption, panic recover, Stop timeout list
            ├── shutdown.go                    # ShutdownPhase (5), ShutdownManager (LIFO/budget), NotifySignalContext
            └── lifecycle_test.go              # §8 Tests #8 & #9
```

**Structure Decision**: Direct 1:1 mapping to `docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md` §2 deliverables table under `synapse-server/`. All files sit in L0 packages with colocated `*_test.go` unit test suites.

## Complexity Tracking

> No Constitution violations; table intentionally empty.
