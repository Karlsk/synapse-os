# Feature Specification: SA-01 L0 Core Foundation — Config, Observability, Domain Errors & Lifecycle Governance

**Feature Branch**: `001-sa01-config-obs-lifecycle`

**Created**: 2026-11-25

**Status**: Draft

**Input**: PR Spec `docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md` (PR `SA-01`) — L0 基础底座：配置管理、可观测性、领域错误体系与生命周期治理。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Unified Domain Error Classification & Safe External Mapping (Priority: P1)

Downstream platform services, storage adapters, outbound network guards, and web handlers need a single, consistent error model so that any domain failure carries an explicit machine-readable error code, HTTP status semantic, and human-readable message without ever leaking raw internal errors (such as database credentials, SQL statements, or filesystem paths) to end users.

**Why this priority**: Every subsequent module (`SA-02` through `SA-42`) depends on a shared domain error taxonomy to classify failures, detect security denials (e.g., egress/SSRF blocks), and preserve `/api/v1` client compatibility.

**Independent Test**: Can be fully tested by constructing each error category, wrapping underlying causes, verifying programmatic classification and sentinel matching, verifying composite unreadable-material error formatting, and confirming unknown internal errors sanitize their external message.

**Acceptance Scenarios**:

1. **Given** any of the 12 domain error categories (invalid argument, unauthorized, password change required, forbidden, egress denied, not found, method not allowed, conflict, busy, too large, rate limited, service unavailable, internal error), **When** a caller constructs and inspects the error, **Then** its machine code and status classification match the enterprise API contract exactly, and wrapping preserves the underlying cause for programmatic inspection.
2. **Given** an egress/SSRF denial error, **When** a downstream module checks whether the failure was an egress block, **Then** the error matches the egress-denied sentinel while carrying forbidden status semantics.
3. **Given** an unclassified internal error containing sensitive operational details (such as a database connection string or password), **When** normalized for external response, **Then** it maps to an internal server error with a generic safe message (`"internal server error"`) while retaining the original cause for server-side diagnostics.
4. **Given** one or more unreadable reference links with reasons and optional hints, **When** aggregated into an unreadable-materials error, **Then** the formatted message matches the exact Chinese multi-line summary format and converts to a conflict error.

---

### User Story 2 - Enterprise Multi-Source Configuration, Fail-Fast Validation & Proxy/CA Runtime (Priority: P1)

Enterprise operations engineers deploying Synapse-OS on-premises (with corporate outbound proxies, internal self-signed root CAs, private package registries, and single-database PostgreSQL 17 deployments) need deterministic configuration loading that rejects typos immediately at startup, aggregates all invalid settings in one startup report, and builds a consistent proxy/TLS runtime for both internal clients and managed child processes.

**Why this priority**: All operational subsystems (database, outbound HTTP, runtime CLI launchers, logging, schedulers) depend on validated configuration and corporate network settings before startup can proceed safely.

**Independent Test**: Can be fully tested by loading YAML payloads with environment overrides and CLI overrides, asserting strict unknown-field rejection, asserting multi-error validation aggregation, loading the repository example configuration file, and verifying local loopback proxy bypass plus private CA certificate loading.

**Acceptance Scenarios**:

1. **Given** default configuration values, a YAML configuration file, environment variables (both current `SYNAPSE_*` and legacy `SERVER_PORT`/`SECRET_KEY`/`SUPERAGENT_*` compatibility names), and CLI overrides, **When** configuration is loaded, **Then** values resolve strictly in `CLI overrides > environment variables > YAML > defaults` priority order, bare port values in `SERVER_PORT` normalize to `:<port>`, and `SYNAPSE_DATA_DIR` relocates all 7 tenant data subdirectories consistently.
2. **Given** a YAML configuration containing an unknown or misspelled key, **When** configuration is parsed, **Then** loading fails immediately with a clear error rather than silently ignoring the typo.
3. **Given** a configuration with multiple invalid settings simultaneously (malformed database DSN, invalid encryption key length/encoding, inverted connection pool bounds, invalid log level/format, unreadable or invalid CA PEM bundle, invalid upload size limits, non-positive task concurrency, or invalid timezone), **When** validation runs, **Then** all validation errors are aggregated and returned together in a single startup error.
4. **Given** corporate HTTP/HTTPS proxy settings, a custom `no_proxy` list (such as `"corp.local"`), a valid enterprise PEM CA bundle, and a private package registry URL, **When** the network runtime is constructed, **Then** loopback addresses (`127.0.0.1`, `localhost`, `::1`) always bypass the proxy, the enterprise CA is added to the trust pool with TLS 1.2+ enforcement, and child-process environment variables include proxy settings, protected `NO_PROXY`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, and `NPM_CONFIG_REGISTRY`.
5. **Given** the shipped commercial example configuration template, **When** loaded and validated under strict unknown-field checking, **Then** it passes with zero errors and contains zero Redis configuration entries.

---

### User Story 3 - Context-Aware Redacting Logging & Isolated Metrics Registry (Priority: P2)

Security and operations teams need structured logs that automatically carry request/tenant/user correlation identifiers, prevent log-injection via malicious request IDs, automatically mask secrets/tokens/DSN passwords (including inside nested attribute groups), and expose core business/HTTP Prometheus metrics from an isolated registry without global state collisions.

**Why this priority**: Observability and secret redaction are mandatory across all L0–L6 services to guarantee zero credential leakage in logs and clean telemetry isolation during concurrent tests and runtime operation.

**Independent Test**: Can be fully tested by sanitizing valid and malicious request IDs, writing logs with context metadata and sensitive attributes (top-level, nested groups, inline bearer tokens, and database DSNs) into an in-memory buffer, and creating multiple metrics registries concurrently to inspect gathered metric families and labels.

**Acceptance Scenarios**:

1. **Given** an inbound request ID containing control characters (such as CRLF), spaces, empty content, or exceeding 64 characters, **When** sanitized, **Then** it is replaced with a freshly generated 32-character lowercase hexadecimal identifier; valid IDs matching `[A-Za-z0-9._-]{1,64}` are preserved verbatim.
2. **Given** a request context populated with `request_id`, `company_id`, and `user_id`, **When** a structured log entry is emitted with that context (or after copying metadata to a detached background context), **Then** all three attributes are automatically included in the log record.
3. **Given** log attributes containing sensitive key names (such as `password`, `api_key`, `Authorization`, `secret_cipher`, `token`, or nested inside attribute groups) or string values embedding `Bearer`/`Basic` credentials or PostgreSQL DSN passwords, **When** emitted through the logger, **Then** sensitive values are replaced with `[REDACTED]` and DSN passwords are masked as `***`, ensuring zero plaintext secrets appear in output.
4. **Given** an application name, **When** the metrics registry is initialized multiple times independently, **Then** each instance uses its own isolated registry without colliding with global state, and exposes the 6 core business metrics (`superagent_chat_turns_total`, `superagent_task_runs_total`, `superagent_task_queue_depth`, `superagent_tokens_total`, `superagent_cli_warm_pool_size`, `superagent_im_connected`) plus HTTP request duration and runtime collectors, all tagged with the constant `application` label.

---

### User Story 4 - Managed Background Execution & 30-Second Reverse-Order Graceful Shutdown (Priority: P2)

Platform operators need background worker loops to be centrally managed with panic isolation so a single crashing worker never terminates the server process, and need deterministic, bounded 30-second staged reverse-order shutdown upon termination signals so in-flight HTTP requests, background loops, external connections, database pools, and log buffers close cleanly in strict order.

**Why this priority**: Predictable startup/shutdown orchestration and crash-isolated background goroutines are core reliability invariants for all higher-layer schedulers, IM connectors, and server entrypoints.

**Independent Test**: Can be fully tested by launching concurrent workers where one panics and another continues until clean stop, testing a stuck worker that exceeds the stop timeout, and registering shutdown hooks across all 5 phases (including multiple hooks in the same phase and slow hooks) to verify execution ordering and total budget truncation.

**Acceptance Scenarios**:

1. **Given** multiple background tasks registered in a managed group, **When** one task panics unexpectedly, **Then** the panic is recovered, logged with stack trace, forwarded to any configured panic callback, and sibling tasks continue operating normally until the group is stopped.
2. **Given** a managed background group with active tasks, **When** stop is triggered and a task ignores cancellation beyond the stop deadline, **Then** stop returns a timeout error that explicitly lists the names of tasks that failed to exit in time; any new tasks submitted after stop begins are rejected safely.
3. **Given** shutdown hooks registered across the 5 ordered phases (HTTP drain, background loops, connections/warm pool, storage, flush) with multiple hooks inside the same phase, **When** shutdown executes, **Then** hooks run strictly in ascending phase order and in LIFO (reverse registration) order within the same phase.
4. **Given** a total shutdown budget (default 30 seconds, or shorter in tests) and a slow hook exceeding its phase or remaining total budget, **When** shutdown executes, **Then** each hook's context is clamped to the remaining total budget, errors are collected without aborting remaining phases while budget remains, and unexecuted hooks report budget exhaustion if total time expires.

---

### Edge Cases

- What happens when a nil error is passed to error normalization (`FromError`) or `AppError.Error()` is called on a nil receiver? Both handle nil safely without panicking (`FromError(nil)` returns `nil`, nil receiver returns `""`).
- What happens when `Load` is called with an empty config file path or empty YAML bytes? It falls back cleanly to built-in defaults, still applying environment overrides, CLI overrides, and fail-fast validation.
- What happens when `no_proxy` is empty, has extra whitespace, or contains duplicate entries (`localhost`, `127.0.0.1`, `::1`)? Normalization trims whitespace, deduplicates entries, and guarantees `127.0.0.1`, `localhost`, and `::1` are always present.
- What happens when `LoggerConfig.File` is configured? Logs dual-write to both the configured output (stdout by default) and the log file with `0640` permissions, and the returned close function flushes/closes the file cleanly.
- What happens when a log record uses `WithAttrs` or `WithGroup` prior to logging sensitive fields? Redaction applies consistently to pre-attached attributes, grouped attributes, and per-record attributes alike.
- What happens when `ShutdownManager` receives a parent context whose deadline is earlier than `totalBudget`? The effective deadline is the earlier of `now + totalBudget` and the parent context's deadline.
- What happens when a background worker exits with `context.Canceled` during normal shutdown? It is treated as a clean stop rather than recorded as an operational error.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide a zero-internal-dependency domain error taxonomy covering all 13 status/error codes (`ok`, `invalid_argument`, `unauthorized`, `password_change_required`, `forbidden`, `not_found`, `method_not_allowed`, `conflict`, `busy`, `too_large`, `rate_limited`, `service_unavailable`, `internal_error`), an `ErrEgressDenied` sentinel, standard constructors mapping 1:1 to expected HTTP status codes, cause unwrapping, code-based `Is` matching, and `FromError` normalization that masks unknown errors behind `"internal server error"`.
- **FR-002**: System MUST provide `UnreadableLinkError` and `UnreadableMaterialsError` producing the exact Chinese multi-line unreadable-materials summary format and converting to HTTP 409 `conflict` via `AsAppError()` and `FromError()`.
- **FR-003**: System MUST provide strongly typed configuration covering all 22 enterprise configuration sections (`server`, `app`, `storage`, `secret`, `network`, `runtime`, `paths`, `quota`, `agent`, `chat`, `mcp`, `files`, `task`, `automation`, `superagent`, `memory`, `provider`, `cost`, `inbox`, `agent_log`, `observability`, `platform`) with zero Redis configuration entries.
- **FR-004**: System MUST load configuration with strict `CLI Overrides > Environment Variables > YAML > Defaults` precedence, reject unknown YAML keys fail-fast, support both `SYNAPSE_*` and compatibility environment variables (`SERVER_PORT`, `DATABASE_URL`, `SECRET_KEY`, `SUPERAGENT_SECRET_KEY`, `HTTP_PROXY`/`http_proxy`, `HTTPS_PROXY`/`https_proxy`, `NO_PROXY`/`no_proxy`, `NODE_EXTRA_CA_CERTS`, `NPM_CONFIG_REGISTRY`, `SYNAPSE_DATA_DIR`, etc.), and validate all critical fields in one aggregated fail-fast error report.
- **FR-005**: System MUST build a unified network runtime combining corporate HTTP/HTTPS proxy routing, guaranteed loopback `NO_PROXY` (`127.0.0.1`, `localhost`, `::1`), system + optional enterprise PEM CA trust pool (TLS 1.2+), and standardized child-process environment variables (`HTTP_PROXY`/`http_proxy`, `HTTPS_PROXY`/`https_proxy`, `NO_PROXY`/`no_proxy`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `NPM_CONFIG_REGISTRY`).
- **FR-006**: System MUST ship a fully commented commercial configuration template (`configs/config.example.yaml`) whose keys match the configuration schema 100%, passes strict loading and validation via automated tests, and contains zero Redis settings.
- **FR-007**: System MUST provide observability context helpers (`request_id`, `company_id`, `user_id`, and metadata copy helper for detached contexts) and `SanitizeRequestID` validating `^[A-Za-z0-9._-]{1,64}$` with 32-char lowercase hex fallback on invalid input.
- **FR-008**: System MUST provide a structured logger builder supporting `debug|info|warn|error` levels, `text|json` formats, optional `0640` file dual-writing with close handle, automatic context metadata injection, recursive sensitive-key redaction (including groups and `WithAttrs`), inline `Bearer`/`Basic` token masking, and PostgreSQL DSN password masking (`RedactDSN`).
- **FR-009**: System MUST provide an isolated Prometheus metrics registry per instance (never mutating the global default registry) with constant `application` label wrapping, runtime Go/Process collectors, the 6 core business metrics (`superagent_chat_turns_total`, `superagent_task_runs_total`, `superagent_task_queue_depth`, `superagent_tokens_total`, `superagent_cli_warm_pool_size`, `superagent_im_connected`), and `http_server_requests_seconds` histogram, exposing `Registerer()` and `Gatherer()` accessors.
- **FR-010**: System MUST provide a managed `BackgroundGroup` supporting `Loop` and function workers, active worker tracking, panic recovery with stack logging and custom callback, post-stop submission rejection, and bounded `Stop(ctx)` returning stuck task names on timeout.
- **FR-011**: System MUST provide a `ShutdownManager` enforcing 5 ordered shutdown phases (`PhaseHTTP`, `PhaseBackground`, `PhaseConnections`, `PhaseStorage`, `PhaseFlush`), LIFO execution within each phase, per-hook timeout clamping within the remaining total budget (default 30s), error aggregation, and `NotifySignalContext` for `SIGINT`/`SIGTERM`.

### Explicit Boundaries (Out of Scope)

- Zero Redis configuration keys, clients, or distributed cache abstractions.
- No PostgreSQL connection pool initialization, SQL migrations, or `pgx`/`goose` dependencies (deferred to `SA-02`).
- No outbound HTTP client wrappers, retry/circuit-breaker logic, or SSRF `EgressGuard` implementation (deferred to `SA-03`).
- No Fail-Closed `TenantContext`, envelope encryption `SecretCipher`, `EventBus`, `memcache`, or `arch_test.go` (deferred to `SA-04`).
- No CLI entrypoint (`cmd/synapse-server/main.go`), Gin middleware, or `/actuator/*` HTTP route registration (deferred to `SA-38`/`SA-41`).

### Key Entities

- **Domain Error (`AppError`, `UnreadableLinkError`, `UnreadableMaterialsError`)**: Standardized failure classification carrying machine code, HTTP status semantic, external-safe message, and optional wrapped cause.
- **System Configuration & Network Runtime (`Config`, `Overrides`, `NetworkRuntime`)**: Strongly typed 22-section enterprise configuration tree plus resolved proxy, TLS trust pool, and child-process environment settings.
- **Observability Pipeline (`LoggerConfig`, `Metrics`)**: Context metadata carriers, log sanitization/redaction chain, and isolated telemetry registry.
- **Lifecycle Controllers (`Loop`, `BackgroundGroup`, `ShutdownPhase`, `ShutdownManager`)**: Managed background execution group and staged reverse-order shutdown coordinator.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of the 12 domain error categories, egress sentinel matching, unreadable-materials formatting, and unknown-error sanitization pass automated verification with zero leakage of raw internal error strings to external messages.
- **SC-002**: Configuration loading enforces strict priority (`CLI > Env > YAML > Default`), rejects any unknown YAML key immediately, aggregates 100% of simultaneous validation failures in a single error return, and validates the shipped commercial example template with zero errors and zero Redis keys.
- **SC-003**: Enterprise proxy/CA runtime guarantees loopback traffic (`127.0.0.1`, `localhost`, `::1`) bypasses proxies under any custom `no_proxy` configuration and propagates custom PEM CAs to both in-process TLS and child-process environment variables.
- **SC-004**: 100% of tested sensitive log keys, nested attribute groups, inline auth headers, and DSN credentials are redacted from log output, and invalid/injection request IDs are replaced with compliant 32-character hex identifiers.
- **SC-005**: Repeated metrics registry instantiations succeed with zero global registry collisions and expose all 6 core business metrics plus HTTP duration metrics carrying the constant `application` label.
- **SC-006**: Panic in a managed background task never terminates sibling workers, stuck tasks are explicitly named upon stop timeout, and staged shutdown completes strictly in phase-ascending / intra-phase-LIFO order within the configured budget.
- **SC-007**: All L0 foundation packages pass formatting, static analysis, build, race-checked test execution, and achieve at least 85% line coverage per PR Spec `§9` acceptance gates, with zero third-party or internal imports in the domain error leaf package.

## Assumptions

- Target workspace is `synapse-server/` with Go `1.26.1` module `github.com/Karlsk/synapse-os` and pre-pinned dependencies (`gopkg.in/yaml.v3`, `golang.org/x/net`, `github.com/prometheus/client_golang`) already present in `go.mod`.
- This PR (`SA-01`) has zero upstream business package dependencies and serves as the stable L0 foundation consumed by `SA-02`–`SA-42`.
- All unit tests run fully isolated without requiring an external PostgreSQL database, live network, or corporate proxy server (using in-memory buffers, temp PEM files, synthetic environment lookup functions, and short test timeouts).
- Exact sub-struct configuration fields for the 22 sections in `Config` align with `spec_01` §4.4/§5.2 and the unified commercial template in `spec_41` §5.5 (plus `platform.super_admin_token` from `_epic/L6/spec_14`).
