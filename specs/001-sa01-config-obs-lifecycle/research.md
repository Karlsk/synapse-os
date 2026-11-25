# Phase 0 Research & Design Decisions: SA-01 L0 Core Foundation

## 1. Domain Error Taxonomy (`internal/core/apperror`)

- **Decision**: Implement `internal/core/apperror` as a pure Go standard-library leaf package (`errors`, `fmt`, `net/http`, `strings`) with exact 13 `Code` constants, `ErrEgressDenied` sentinel, `AppError` struct (`Error`, `Unwrap`, `Is`), 13 constructors + `Wrap`, `FromError`, `HTTPStatusOf`, `CodeOf`, and `UnreadableLinkError` / `UnreadableMaterialsError`.
- **Rationale**: Being a zero-dependency leaf package prevents import cycles across all L0–L6 packages and matches `spec_01` §9 gate #5. `FromError` sanitizes unknown errors to `"internal server error"` (HTTP 500) while keeping the root cause via `Unwrap()` for server-side logging. Verified Java `UnreadableLinkException.java` (`super(reason)`) and `UnreadableMaterialsException.java` (`L21-L31`) to guarantee byte-for-byte Chinese formatting compatibility.
- **Alternatives considered**: Using sentinel errors per HTTP status instead of `*AppError` with `Is(target)` — rejected because `*AppError` allows carrying dynamic contextual messages while still matching `errors.Is(err, &AppError{Code: CodeNotFound})` and `errors.As`.

## 2. Configuration Loading, Validation & Enterprise Network Runtime (`internal/infra/config`)

- **Decision**:
  - Use `gopkg.in/yaml.v3` `Decoder` with `dec.KnownFields(true)` so typos fail immediately.
  - Layer precedence strictly: `Default()` -> YAML decode -> `applyEnvOverrides(cfg, lookupEnv)` -> `Overrides` (`--listen`, `--ui-dir`) -> `cfg.Validate()`.
  - Aggregate all validation errors with `errors.Join(errs...)` so operators fix all config issues in one pass.
  - Use `golang.org/x/net/http/httpproxy` in `BuildNetworkRuntime` with `ensureLocalNoProxy` unconditionally appending missing `127.0.0.1`, `localhost`, and `::1` tokens (trimmed & deduplicated) so loopback `/mcp` self-checks never route through corporate proxies.
  - Load `x509.SystemCertPool()` (fallback `x509.NewCertPool()`) + optional PEM `CABundleFile`, construct `&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}`, and populate `ChildEnv` (`HTTP_PROXY`/`http_proxy`, `HTTPS_PROXY`/`https_proxy`, `NO_PROXY`/`no_proxy`, `NODE_EXTRA_CA_CERTS`, `SSL_CERT_FILE`, `NPM_CONFIG_REGISTRY`).
- **Rationale**: Matches `spec_01` §4.1/§5.2 and commercial on-prem requirements (`spec_41` §5.5). Pure `LoadFromBytes(yamlData, lookupEnv, ov)` makes priority and edge-case tests 100% deterministic and safe for `t.Parallel()` without mutating process env vars.
- **Alternatives considered**: Using Viper or reflection-based env binding — rejected due to unnecessary dependency bloat, silent unknown-key behavior, and non-explicit env precedence.

## 3. Context-Aware Redacting Logger & Isolated Prometheus Registry (`internal/infra/obs`)

- **Decision**:
  - Store `request_id`, `company_id`, `user_id` via unexported typed context keys; provide `CopyMeta(src, dst)` for `SA-04` `tenant.Detach`.
  - Compose handler chain outside-in: `ContextHandler` -> `RedactingHandler` -> `slog.TextHandler` or `slog.JSONHandler` writing to `cfg.Output` (default `os.Stdout`) or `io.MultiWriter(out, logFile)` when `cfg.File != ""` (opened with `O_CREATE|O_WRONLY|O_APPEND`, perm `0640`).
  - Redact sensitive attributes in `Handle` AND `WithAttrs`, preserving `WithGroup` scoping and recursively traversing `slog.KindGroup` values; also scrub inline `Bearer`/`Basic` credentials and PostgreSQL DSN passwords (`postgres://`/`postgresql://`) in string values.
  - Create a fresh `prometheus.NewRegistry()` inside `NewMetrics(appName)` (defaulting empty `appName` to `"synapse-server"`), wrap with `prometheus.WrapRegistererWith(prometheus.Labels{"application": appName}, reg)`, register `GoCollector`, `ProcessCollector`, 6 `superagent_*` metrics, and `http_server_requests_seconds` histogram.
- **Rationale**: Never touches `prometheus.DefaultRegisterer`, eliminating test panics and global state pollution. Dual-layer redaction (`WithAttrs` + `Handle` + recursive `KindGroup`) guarantees secrets cannot bypass redaction through pre-bound child loggers or grouped attributes.
- **Alternatives considered**: Post-serialization regex scrubbing on raw JSON/text bytes — rejected because it breaks structured JSON escaping and misses arbitrary sensitive key names with non-pattern values.

## 4. Background Group & Staged Reverse-Order Shutdown (`internal/infra/lifecycle`)

- **Decision**:
  - `BackgroundGroup` derives `(ctx, cancel)` from `parent` (default `context.Background()`), tracks active workers with `sync.WaitGroup`, `atomic.Int64`, and a mutex-guarded `running map[string]int` (reference-counted so duplicate task names do not clobber each other). Every worker runs with `defer recover()` logging stack traces via `debug.Stack()` and invoking optional `onPanic`. `Stop(ctx)` sets `stopped = true`, calls `cancel()`, waits for `wg.Wait()` or `ctx.Done()`, and on timeout returns an error wrapping `ctx.Err()` listing sorted remaining task names.
  - `ShutdownManager` clones hooks under lock, stable-sorts by ascending `phase` then descending registration sequence (`LIFO` within each phase), clamps each hook's timeout to `min(h.budget, time.Until(deadline))` (where `deadline = min(now+totalBudget, ctx.Deadline)`), runs hooks outside locks, aggregates all errors via `errors.Join`, and reports budget exhaustion for any skipped hook.
  - `NotifySignalContext` delegates to `signal.NotifyContext(parent, syscall.SIGINT, syscall.SIGTERM)`.
- **Rationale**: Strictly fulfills `spec_01` §4.1/§5.4 and Constitution Principle VI (no unmanaged goroutines, panic isolation, lock-free I/O, deterministic 30s reverse-order shutdown).
- **Alternatives considered**: `golang.org/x/sync/errgroup` alone — rejected because `errgroup` lacks panic recovery, stuck-task timeout diagnostics, post-stop rejection, and staged LIFO budget orchestration.
