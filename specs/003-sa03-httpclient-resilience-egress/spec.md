# Feature Specification: SA-03 L0 Outbound Network & Security Foundation — Anti-DNS-Rebinding EgressGuard & Dual-Profile Resilient HTTP Client

**Feature Branch**: `003-sa03-httpclient-resilience-egress`

**Created**: 2026-11-25

**Status**: Draft

**Input**: PR Spec `docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md` (PR `SA-03`) — L0 抗 DNS Rebinding `EgressGuard` 与双 Profile（`Trusted` / `Untrusted`）弹性 HTTP 客户端底座。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Anti-SSRF & Anti-DNS-Rebinding Egress Guard (Priority: P1)

Security-sensitive features that fetch user-supplied external links, webhooks, or external custom MCP endpoints need a transport-level egress guard that blocks private, loopback, link-local/cloud-metadata, CGNAT, IPv6 ULA, IPv4-mapped IPv6, forbidden hostnames, disallowed schemes/ports, and DNS rebinding attacks while permitting administrator-whitelisted corporate hosts/CIDRs and properly encoding pasted URLs containing spaces or Chinese characters.

**Why this priority**: Eliminating the pre-check-vs-connect DNS rebinding vulnerability is a core security prerequisite before any untrusted outbound request (`LinkReader`, custom MCP, webhooks) can be safely implemented.

**Independent Test**: Fully testable offline using table-driven URL/IP checks, a mock DNS resolver simulating mixed public+private answers and DNS rebinding, and a local redirect test server verifying the 6-hop cap and cross-origin credential stripping.

**Acceptance Scenarios**:

1. **Given** URLs or IPs targeting loopback (`127.0.0.1`, `[::1]`), RFC1918 private networks (`10.x`, `172.16-31.x`, `192.168.x`), cloud metadata (`169.254.169.254`), IPv6 ULA (`fc00::/7`), IPv4-mapped IPv6 (`::ffff:127.0.0.1`, `::ffff:169.254.169.254`), CGNAT (`100.64.0.0/10`), `0.0.0.0`, reserved test/doc ranges, forbidden suffixes (`localhost`, `*.local`, `*.internal`), non-HTTP(S) schemes (`file://`), or non-whitelisted ports (`:22`), **When** checked or dialed via the guard, **Then** every attempt is denied with the exact Chinese error message and wraps `apperror.ErrEgressDenied` (HTTP 403).
2. **Given** administrator allowlist entries (exact host, domain suffix `.corp.local`, or IPv4/IPv6 CIDR `10.8.0.0/16`), **When** matching URLs or resolved IPs are checked or dialed, **Then** they are explicitly allowed while non-matching private targets remain denied.
3. **Given** a mock resolver returning mixed public + private IPs (e.g., public IP + `169.254.169.254`) or changing its answer between resolution attempts (DNS rebinding), **When** dialing through the guard, **Then** mixed results are rejected as a group, validated dials connect directly to the validated IP literal (zero second DNS lookup), and socket-level `Control` re-verifies the target IP before `connect()`.
4. **Given** a redirect chain exceeding 6 hops or redirecting across origins with `Authorization`/`Cookie`/`Proxy-Authorization` headers, **When** `CheckRedirect` executes, **Then** hop 7 is denied with `"重定向次数过多"`, every hop is re-validated via `CheckURL`, and cross-origin redirects strip sensitive auth/cookie headers; pasted URLs with Chinese characters or spaces are percent-encoded cleanly by `ToURI`.

---

### User Story 2 - Physically Isolated Dual-Profile Clients, Response Body Caps & SSE Streaming Mode (Priority: P1)

Platform services calling trusted enterprise/API endpoints (Feishu, DingTalk, LLMs, `models.dev`, Node.js mirrors) need corporate proxy and private CA support without false-positive SSRF blocking, whereas services calling untrusted user URLs must be physically forced through `EgressGuard`, no proxy bypass, redirect re-checks, and response body caps; long-lived SSE and large-file downloads must never be cut off by a global total timeout while still guarding against stalled reads.

**Why this priority**: Physically separating `NewTrusted` and `NewUntrustedEgress` eliminates accidental SSRF bypasses and prevents OOM bombs or premature SSE stream truncation across all downstream L1–L4 modules.

**Independent Test**: Fully testable with local `httptest.NewTLSServer` (self-signed CA), mock proxy servers, oversized payload handlers, and chunked streaming handlers.

**Acceptance Scenarios**:

1. **Given** a `TrustedClient` configured with a custom enterprise CA bundle and proxy runtime from `SA-01`, **When** it calls a local TLS server using that self-signed CA (or routes through a proxy), **Then** the request succeeds seamlessly.
2. **Given** an `UntrustedEgressClient` with default options, **When** it attempts to call a local `127.0.0.1` server, **Then** the request is immediately rejected with `*ClientError{Kind: KindEgressDenied}` (wrapping `apperror.ErrEgressDenied`) without establishing a TCP connection, and `Proxy` is forced nil so no proxy can bypass local DNS validation.
3. **Given** an `UntrustedEgressClient` (or a client with `MaxResponseBytes` configured, default 20 MiB), **When** a server responds with more bytes than the limit (e.g., 2048 bytes against a 1024-byte cap), **Then** reading the response body fails with `*ClientError{Kind: KindResponseTooLarge}` wrapping `apperror.CodeTooLarge` (HTTP 413) and closes the underlying stream.
4. **Given** a client constructed with `Streaming: true` (even if `TotalTimeout` was populated with a short value such as `50ms`) and `IdleReadTimeout: 150ms`, **When** a server streams chunks every 60ms for 300ms total, **Then** `StdClient().Timeout` is `0` and the entire stream reads cleanly without truncation; when the server stalls for >200ms between chunks, the read aborts with a timeout error.

---

### User Story 3 - Exponential Backoff Retry with `Retry-After` & Host-Scoped Circuit Breaking (Priority: P2)

Downstream integrations calling flaky or rate-limited upstreams need automatic bounded retries with full jitter for idempotent requests on `429` (respecting `Retry-After` seconds and HTTP-Date clamped to `MaxRetryAfter`), `502`/`503`/`504`, and transient network errors, combined with per-`(service, host)` circuit breakers that isolate failing hosts without tripping on client 4xx errors, `EgressDenied`, or context cancellations, and without embedding any business fallback logic.

**Why this priority**: Centralizes network resilience consistently across all L1–L4 consumers while preserving strict separation between transport error classification and business fallback decisions.

**Independent Test**: Fully testable using stateful `httptest.Server` handlers returning controlled `429`/`503`/`404`/`200` sequences and asserting exact attempt counts, `Retry-After` honoring, circuit state transitions (`Closed -> Open -> HalfOpen -> Closed`), and host isolation.

**Acceptance Scenarios**:

1. **Given** a standard GET request (or a POST marked with `WithIdempotent(ctx)` and replayable `GetBody`) hitting an upstream that returns `429` (with `Retry-After`) or `502`/`503`/`504` twice before returning `200`, **When** executed through the client, **Then** it retries up to `MaxAttempts` and succeeds on attempt 3.
2. **Given** a non-idempotent POST without `WithIdempotent(ctx)`, or a request failing with `EgressDenied`, TLS certificate error, `KindCircuitOpen`, or `context.Canceled`, **When** executed through the client, **Then** zero retries occur; retries exhausting all attempts on `429`/`502`/`503`/`504` return a structured `*ClientError` (`KindRateLimited` or `KindUpstream5xx`).
3. **Given** two distinct hosts A and B under the same client, **When** host A fails consecutively with `503` (e.g., 5 times), **Then** host A's circuit opens and subsequent calls fail fast with `*ClientError{Kind: KindCircuitOpen}` without reaching host A while host B continues succeeding unaffected; after the open cooldown elapses, a successful half-open probe restores host A to `Closed`.
4. **Given** repeated `404` responses, `EgressDenied` denials, or `context.Canceled` errors, **When** routed through the circuit breaker, **Then** they never trip the circuit breaker open, and total tracked hosts stay bounded by `MaxHostEntries` (default 256) to prevent OOM.

---

### User Story 4 - Redacted Outbound Logging & Prometheus Client Telemetry (Priority: P2)

Security and SRE teams need every outbound HTTP request logged with sanitized URLs (stripping sensitive query parameters and URL userinfo passwords), zero header/body printing, propagated `request_id`/`company_id` context metadata, and Prometheus metrics tracking request totals, durations, retries, and circuit-breaker states.

**Why this priority**: Prevents outbound API keys/tokens in query strings or `Authorization` headers from leaking into logs while giving operators visibility into upstream health and retries.

**Independent Test**: Fully testable by issuing requests with sensitive query parameters (`?access_token=topsecret&page=1`) and `Authorization: Bearer ...` headers against an `httptest.Server`, inspecting the captured JSON log buffer, and gathering Prometheus metrics from an isolated `obs.Metrics` registry.

**Acceptance Scenarios**:

1. **Given** an outbound request to `https://api.example/v1?access_token=topsecret&page=1` with `Authorization: Bearer sk-12345678` and `obs` request/company context metadata, **When** executed through the client, **Then** the structured log records `service`, `profile`, `method`, `host`, `url_sanitized`, `status`, `duration_ms`, and `request_id`/`company_id`, redacts `access_token` to `[REDACTED]`, keeps `page=1`, and contains neither `topsecret` nor `sk-12345678`.
2. **Given** an isolated Prometheus registerer supplied in `Options.Registerer`, **When** requests, retries, and circuit-breaker transitions occur, **Then** `synapse_http_client_requests_total`, `synapse_http_client_request_duration_seconds`, `synapse_http_client_retries_total`, and `synapse_http_client_breaker_state` are populated accurately.

---

### Edge Cases

- What happens when `Options.Allowed` in `egress.New` contains an invalid CIDR string or empty/whitespace entries? Empty entries are ignored; malformed CIDR entries return an explicit error from `New` (and panic in `MustNew` during init).
- What happens when a redirect response has a relative `Location` header (e.g., `/next`)? Go's `http.Client` resolves it against the previous URL before calling `CheckRedirect`, which re-validates the resolved absolute URL.
- What happens when `Retry-After` header exceeds `MaxRetryAfter` (default 30s) or contains a past HTTP-Date? Values above `MaxRetryAfter` are clamped to `MaxRetryAfter`; past dates clamp to `0`.
- What happens when `IdleReadTimeout` is enabled on a streaming response and the caller cancels `req.Context()`? Cancellation unblocks immediately without waiting for `IdleReadTimeout`, and timers are stopped cleanly on `Close()` to avoid leaks.
- What happens when `Client.Do` receives a `nil` request or a request with an empty host? Fails cleanly with a structured error without panicking.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide `internal/core/egress` exporting `Resolver`, `Options`, `Guard`, `PublicOnly`, `New`, `MustNew`, `ToURI`, `IsPublic`, `CheckURL`, `DialContext`, and `CheckRedirect` matching `spec_03 §4.1A` verbatim, enforcing `Unmap()` before IP checks, blocking all non-public IPv4/IPv6 ranges and forbidden hostnames, connecting directly to validated IP literals in `DialContext` with `net.Dialer.Control` defense-in-depth, enforcing max 6 redirects with cross-origin auth/cookie header stripping, and returning exact Chinese messages wrapping `apperror.ErrEgressDenied`.
- **FR-002**: System MUST provide `internal/infra/httpclient` exporting `Profile`, `TimeoutPolicy`, `RetryPolicy`, `BreakerPolicy`, `Options`, `Client`, `NewTrusted`, `NewUntrustedEgress`, `Do`, `StdClient`, `Profile`, `WithIdempotent`, `WithService`, `ReadLimited`, `ErrorKind` (8 constants), `ClientError`, and `KindOf` matching `spec_03 §4.1B` verbatim.
- **FR-003**: `NewTrusted` MUST inject corporate proxy and private CA from `*config.NetworkRuntime` (TLS 1.2+) without attaching `EgressGuard`; `NewUntrustedEgress` MUST force `egress.Guard.DialContext`, `Proxy: nil`, `guard.CheckRedirect`, and default 20 MiB `MaxResponseBytes` (`io.LimitReader` wrapper returning `*ClientError{Kind: KindResponseTooLarge}` wrapping `apperror.TooLarge`).
- **FR-004**: `Streaming: true` MUST force `http.Client.Timeout = 0`, disable automatic retries (`MaxAttempts = 1`), retain connection/TLS/header timeouts, and support optional per-read `IdleReadTimeout` stall detection without capping total stream duration.
- **FR-005**: Standard mode MUST support exponential backoff + full jitter retries for idempotent replayable requests on `429` (honoring seconds and HTTP-Date `Retry-After` clamped to `MaxRetryAfter`), `502`/`503`/`504`, and transient network errors, NEVER retrying `EgressDenied`, `KindCircuitOpen`, TLS certificate errors, or `context.Canceled`.
- **FR-006**: Circuit breaking MUST use `github.com/sony/gobreaker/v2` scoped by `(service, host)` with bounded `MaxHostEntries` LRU eviction (default 256), counting only transport failures and `5xx`/`429` as failures while excluding `4xx` (other than `429`), `apperror.ErrEgressDenied`, and `context.Canceled`, and returning `*ClientError{Kind: KindCircuitOpen}` when open.
- **FR-007**: Observability `RoundTripper` MUST sanitize sensitive URL query parameters and URL userinfo passwords, NEVER log headers or bodies, preserve `obs` context metadata, and register `synapse_http_client_requests_total`, `synapse_http_client_request_duration_seconds`, `synapse_http_client_retries_total`, and `synapse_http_client_breaker_state` when a `Registerer` is supplied.
- **FR-008**: `internal/infra/httpclient` MUST NEVER implement any business fallback logic (no credential failover, no URL fallback, no stale-cache return); it only classifies and returns structured `*ClientError`.

### Explicit Boundaries (Out of Scope)

- No Feishu, DingTalk, LLM, `models.dev`, `LinkReader`, or MCP business protocol logic (deferred to `SA-14`, `SA-18`–`SA-20`, `SA-24`–`SA-26`, `SA-33`–`SA-34`).
- No Redis, database, or persistent storage dependencies.

### Key Entities

- **`egress.Guard`**: Transport-level anti-SSRF / anti-DNS-rebinding validator, dialer, and redirect policy.
- **`httpclient.Client` & `Options`**: Dual-profile outbound HTTP client wrapping layered RoundTrippers (`Transport -> BodyControl -> Breaker -> Retry -> Observability`).
- **`httpclient.ClientError` & `ErrorKind`**: Structured outbound error classification carrying `Kind`, `Service`, `Host`, `StatusCode`, `RetryAfter`, `Attempts`, and unwrapped root cause.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of private/loopback/metadata/CGNAT/ULA/IPv4-mapped-IPv6/forbidden-host/scheme/port vectors are rejected with `apperror.ErrEgressDenied`, while allowlisted corporate hosts/CIDRs pass, and DNS rebinding + 7-hop redirect + cross-origin header stripping tests pass.
- **SC-002**: Dual-profile isolation guarantees `TrustedClient` works with self-signed enterprise CA + proxy while `UntrustedEgressClient` blocks `127.0.0.1` without dialing and caps response bodies at `MaxResponseBytes` (`KindResponseTooLarge` / 413).
- **SC-003**: `Streaming: true` guarantees `StdClient().Timeout == 0`, streams continuously beyond `TotalTimeout`, and aborts stalled reads via `IdleReadTimeout`.
- **SC-004**: Retry, `Retry-After`, host-isolated `gobreaker/v2` circuit breaking, redacted logging, and Prometheus metrics all pass under `-race`, with ≥ 85% line coverage across both `internal/core/egress` and `internal/infra/httpclient`, zero regressions in `SA-01`/`SA-02`, and zero business fallback code inside `httpclient`.

## Assumptions

- `SA-01` (`internal/core/apperror`, `internal/infra/config`, `internal/infra/obs`) is merged on `main` and available.
- `github.com/sony/gobreaker/v2` (`v2.4.0`) and `github.com/prometheus/client_golang` (`v1.25.0`) are already pinned in `synapse-server/go.mod`.
- All unit tests run completely offline using mock `egress.Resolver` implementations and local `httptest.Server` / `httptest.NewTLSServer` instances.
