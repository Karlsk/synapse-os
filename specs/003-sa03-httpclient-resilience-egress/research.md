# Phase 0 Research & Design Decisions: SA-03 L0 EgressGuard & Resilient HTTP Client

## 1. Anti-DNS-Rebinding `EgressGuard` (`internal/core/egress/guard.go`)

- **Decision**:
  - `ToURI(raw)`: Trim whitespace and percent-encode any UTF-8 byte `> 0x7e`, `<= 0x20`, or in `"<>\\^\`{|}` as `%02X` before `url.Parse` (1:1 faithful to Java `EgressGuard.java:L96-L113`), returning `apperror.EgressDenied("不是有效的网址")` on empty/invalid URL.
  - `IsPublic(addr)`: Always call `addr = addr.Unmap()` first to defeat `::ffff:127.0.0.1` and `::ffff:169.254.169.254`, then reject unspecified, loopback, link-local unicast/multicast, multicast, private, interface-local multicast, IPv6 `fc00::/7` ULA, IPv6 `2001:db8::/32` documentation, and IPv4 `0/8`, `10/8`, `127/8`, `224/4+`, `100.64/10` CGNAT, `169.254/16` link-local/cloud metadata, `172.16/12`, `192.168/16`, `192.0.0/24`, `192.0.2/24`, `198.18/15`, `198.51.100/24`, `203.0.113/24`.
  - `DialContext`: Parse `host:port`, check port allowlist, check host allowlist, reject forbidden local names (`localhost`, `*.localhost`, `*.local`, `*.internal`), resolve all IPs via `Resolver.LookupNetIP` (handling IP literals directly without DNS lookup), reject the entire group if ANY resolved IP is neither public nor CIDR-allowlisted, then connect directly to the validated IP literal (`net.JoinHostPort(ip.String(), port)`) with a `net.Dialer.Control` callback re-checking the final kernel target IP right before `connect()`.
  - `CheckRedirect`: Reject when `len(via) >= g.maxRedirects` (default 6) with `"重定向次数过多"`, re-run `CheckURL`, and strip `Authorization`, `Cookie`, and `Proxy-Authorization` headers whenever scheme/host/port changes across hops.
- **Rationale**: Closes the Java pre-check-vs-connect DNS rebinding window completely while keeping TLS SNI intact (Go's `http.Transport` sets `TLSClientConfig.ServerName` from the original request host before invoking `DialContext`).

## 2. Dual-Profile Isolation & Layered RoundTripper Assembly (`profile.go`, `client.go`, `limits.go`)

- **Decision**:
  - RoundTripper stack (inner -> outer): `http.Transport` -> `bodyControlRT` (body size cap + streaming idle read timeout) -> `breakerRT` (per `service:host` `gobreaker/v2`) -> `retryRT` (exponential backoff + Full Jitter) -> `observabilityRT` (redacted slog + Prometheus).
  - `NewUntrustedEgress`: Always enforces `DialContext = guard.DialContext`, `Proxy = nil`, `CheckRedirect = guard.CheckRedirect`, and `MaxResponseBytes = 20 MiB` (default when `<= 0`).
  - `Streaming: true`: Forces `http.Client.Timeout = 0` and `Retry.MaxAttempts = 1` (disables automatic retry).
  - `IdleReadTimeout`: Wrap `resp.Body` in `idleTimeoutReadCloser` that arms a `time.AfterFunc(idleTimeout, ...)` before each `orig.Read(p)` (closing `orig` on idle timeout and returning a `*ClientError{Kind: KindTimeout}`), stopping the timer immediately when `Read` returns. Also respects `req.Context().Done()` and cleans up on `Close()`.
  - `Client.Do(req)`: Unwraps `*url.Error` produced by `http.Client` (e.g., from `CheckRedirect` or `http.Client.Timeout`) and classifies errors into `*ClientError` so callers reliably inspect both `KindOf(err)` and `errors.Is(err, apperror.ErrEgressDenied)`.
- **Rationale**: Guarantees `CheckRedirect` egress errors and `http.Client.Timeout` errors surface consistently as `*ClientError`, and guarantees `IdleReadTimeout` works on any streaming response body without leaking goroutines or capping total stream lifetime.

## 3. Retry & `Retry-After` Semantics (`retry.go`)

- **Decision**:
  - Retry only when request is idempotent (`GET`, `HEAD`, `OPTIONS`, `PUT`, `DELETE`, or `WithIdempotent(ctx)`) AND body is replayable (`req.Body == nil || req.Body == http.NoBody || req.GetBody != nil`).
  - Retryable statuses: `429`, `502`, `503`, `504`. On `429`, parse `Retry-After` (integer seconds or HTTP-Date via `http.ParseTime`) clamped to `[0, MaxRetryAfter]`; otherwise use Full Jitter exponential backoff `rand([0, min(maxBackoff, base * 2^(attempt-1))])`.
  - Never retry `apperror.ErrEgressDenied`, `KindCircuitOpen`, `context.Canceled`, `context.DeadlineExceeded`, or `x509` TLS certificate errors.
  - When `maxAttempts == 1`, return `(*http.Response, nil)` directly on HTTP status codes (caller/breaker inspects status); when `maxAttempts > 1` and retries are exhausted on `429`/`502`/`503`/`504`, drain/close body and return `*ClientError` (`KindRateLimited` or `KindUpstream5xx`) per `spec_03 §4.3`.

## 4. Per-`(service, host)` Bounded LRU Circuit Breaker (`breaker.go`)

- **Decision**:
  - Maintain a `container/list` + `map[string]*list.Element` LRU cache capped at `MaxHostEntries` (default 256) protected by `sync.Mutex` (lock held ONLY for O(1) map/list lookup/insertion, NEVER during `cb.Execute` network I/O).
  - Configure `gobreaker.Settings`: `IsSuccessful` returns `true` for `nil`, `apperror.ErrEgressDenied`, and `context.Canceled`; `ReadyToTrip` trips when `ConsecutiveFailures >= policy.ConsecutiveFail` OR (`Requests >= policy.MinRequests` AND failure ratio `>= policy.FailureRatio`); `OnStateChange` updates `synapse_http_client_breaker_state` gauge (`0=closed`, `1=half_open`, `2=open`).
  - When `opts.Breaker == (BreakerPolicy{})` (complete zero value), apply default `Enabled: true` alongside numeric defaults; when caller explicitly configures fields with `Enabled: false`, respect `Enabled: false`.
- **Rationale**: Prevents arbitrary-host OOM on `UntrustedEgressClient`, isolates failures per host, prevents user-fed private URLs from tripping breakers, and satisfies lock-free I/O rules.
