# Frozen Go Package Contracts: SA-01 (L0 Foundation)

Source of truth: [docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md](../../../docs/SuperAgent-Go/DDD/L0/spec_01_config_obs_error_lifecycle.md) `§4.1`–`§4.4`. All exported symbol names, parameter lists, and return types below MUST match verbatim.

## 1. `internal/core/apperror` (Stdlib-only Leaf Package)

- Constants: `CodeOK`, `CodeInvalidArgument`, `CodeUnauthorized`, `CodePasswordChangeRequired`, `CodeForbidden`, `CodeNotFound`, `CodeMethodNotAllowed`, `CodeConflict`, `CodeBusy`, `CodeTooLarge`, `CodeRateLimited`, `CodeServiceUnavailable`, `CodeInternal`
- Sentinel: `var ErrEgressDenied = errors.New("egress denied")`
- Types & Methods:
  - `type Code string`
  - `type AppError struct { Code Code; HTTPStatus int; Message string; Err error }`
    - `func (e *AppError) Error() string`
    - `func (e *AppError) Unwrap() error`
    - `func (e *AppError) Is(target error) bool`
  - Constructors: `InvalidArg`, `Unauthorized`, `PasswordChangeRequired`, `Forbidden`, `EgressDenied`, `NotFound`, `MethodNotAllowed`, `Conflict`, `Busy`, `TooLarge`, `RateLimited`, `ServiceUnavailable`, `Internal`, `Wrap`
  - Converters: `FromError(err error) *AppError`, `HTTPStatusOf(err error) int`, `CodeOf(err error) Code`
  - Composite link errors:
    - `type UnreadableLinkError struct { URL string; Reason string; Hint string }` + `Error() string`
    - `type UnreadableMaterialsError struct { Items []*UnreadableLinkError }` + `Error() string` + `AsAppError() *AppError`

## 2. `internal/infra/config`

- Types: `Config` (22 sections), `NetworkRuntime`, `Overrides` + 22 section structs & nested `PostgresConfig`, `ClaudeConfig`, `CodexConfig`
- Functions:
  - `func Default() *Config`
  - `func Load(path string, ov Overrides) (*Config, error)`
  - `func LoadFromBytes(yamlData []byte, lookupEnv func(string) (string, bool), ov Overrides) (*Config, error)`
  - `func (c *Config) Validate() error`
  - `func BuildNetworkRuntime(netCfg NetworkConfig, rtCfg RuntimeConfig) (*NetworkRuntime, error)`

## 3. `internal/infra/obs`

- Constants: `RequestIDHeader = "X-Request-Id"`, `AttrRequestID = "request_id"`, `AttrCompanyID = "company_id"`, `AttrUserID = "user_id"`, `RedactedValue = "[REDACTED]"`
- Context & ID helpers: `WithRequestID`, `RequestIDFrom`, `WithCompanyID`, `CompanyIDFrom`, `WithUserID`, `UserIDFrom`, `CopyMeta`, `SanitizeRequestID`
- Logging & Redaction: `LoggerConfig`, `NewLogger(cfg LoggerConfig) (*slog.Logger, func() error, error)`, `NewContextHandler(next slog.Handler) slog.Handler`, `NewRedactingHandler(next slog.Handler) slog.Handler`, `RedactDSN(raw string) string`
- Metrics: `Metrics` (fields `ChatTurnsTotal`, `TaskRunsTotal`, `TaskQueueDepth`, `TokensTotal`, `CLIWarmPoolSize`, `IMConnected`, `HTTPRequestsSecs`), `NewMetrics(appName string) (*Metrics, error)`, `Registerer() prometheus.Registerer`, `Gatherer() prometheus.Gatherer`

## 4. `internal/infra/lifecycle`

- `type Loop interface { Name() string; Run(ctx context.Context) error }`
- `type BackgroundGroup struct` + `type GroupOption func(*BackgroundGroup)`
  - `WithLogger(l *slog.Logger) GroupOption`
  - `WithOnPanic(fn func(name string, recovered any, stack []byte)) GroupOption`
  - `NewBackgroundGroup(parent context.Context, opts ...GroupOption) *BackgroundGroup`
  - `Context() context.Context`, `Go(name string, fn func(ctx context.Context) error)`, `GoLoop(loop Loop)`, `Active() int`, `Stop(ctx context.Context) error`
- `type ShutdownPhase int` + `PhaseHTTP(10)`, `PhaseBackground(20)`, `PhaseConnections(30)`, `PhaseStorage(40)`, `PhaseFlush(50)`
- `type ShutdownManager struct`
  - `NewShutdownManager(totalBudget time.Duration, logger *slog.Logger) *ShutdownManager`
  - `Register(phase ShutdownPhase, name string, budget time.Duration, fn func(ctx context.Context) error)`
  - `Shutdown(ctx context.Context) error`
- `NotifySignalContext(parent context.Context) (context.Context, context.CancelFunc)`
