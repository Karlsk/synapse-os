# Tasks: SA-03 L0 Outbound Network & Security Foundation — EgressGuard & Dual-Profile Resilient HTTP Client

**Input**: Design documents from `/specs/003-sa03-httpclient-resilience-egress/` and frozen PR Spec `docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md`

**Prerequisites**: `plan.md` (required), `spec.md` (required), `research.md`, `data-model.md`, `contracts/egress-httpclient-contracts.md`, `quickstart.md`

**Tests**: TDD is MANDATORY per PR Spec `§6` & `§8` — every test suite is written first (RED) before its production code (GREEN) and verified under `-race -cover` (≥ 85% per package).

**Organization**: Grouped by user story (`US1`–`US4`) aligning 1:1 with PR Spec `§2` deliverables and `§6` TDD tasks.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (`[US1]`..`[US4]`)
- Exact file paths included in every task description

## Path Conventions

- Backend module root: `synapse-server/` (`module github.com/Karlsk/synapse-os`, Go `1.26.1`)
- L0 packages: `synapse-server/internal/core/egress/` and `synapse-server/internal/infra/httpclient/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify pinned dependencies and L0 directory skeleton (maps to PR Spec `§6 T1`)

- [X] T001 Verify `synapse-server/go.mod` pins `github.com/sony/gobreaker/v2` and `github.com/prometheus/client_golang` with zero Redis/ClickHouse/GORM entries, and verify directory skeleton for `synapse-server/internal/core/egress/` and `synapse-server/internal/infra/httpclient/`

---

## Phase 2: User Story 1 - Anti-SSRF & Anti-DNS-Rebinding Egress Guard (Priority: P1) 🎯 MVP

**Goal**: Deliver transport-level `internal/core/egress` blocking all private/loopback/metadata/CGNAT/ULA/IPv4-mapped-IPv6 ranges, DNS rebinding, and excessive redirects while allowing admin whitelists (maps to PR Spec `§2 #1`, `§6 T2`, `§8 #1,#2`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/core/egress/...` passes with ≥ 85% coverage and imports only Go stdlib + `internal/core/apperror`.

### Tests for User Story 1 (TDD RED First) ⚠️

- [X] T002 [P] [US1] Write TDD RED unit tests covering all forbidden IPs/CIDRs (`127.0.0.1`, `10.1.2.3`, `172.20.0.1`, `192.168.1.1`, `169.254.169.254`, `[::1]`, `[fd00::1]`, `[::ffff:127.0.0.1]`, `[::ffff:169.254.169.254]`, `100.64.0.1`, `0.0.0.0`, doc/test nets), forbidden hostnames (`localhost`, `printer.local`, `svc.internal`), disallowed schemes (`file://`) & ports (`:22`), admin allowlists (`10.8.0.0/16`, `.corp.local`, exact host), `ToURI` Chinese/space percent-encoding, mock `Resolver` mixed public+private rejection & DNS rebinding defense in `DialContext`, 7-hop redirect cap (`"重定向次数过多"`), and cross-origin `Authorization`/`Cookie` header stripping in `synapse-server/internal/core/egress/guard_test.go`

### Implementation for User Story 1 (GREEN → REFACTOR)

- [X] T003 [US1] Implement `Resolver`, `Options`, `Guard`, `PublicOnly`, `New`, `MustNew`, `ToURI`, `IsPublic` (with mandatory `Unmap()`), `CheckURL`, `DialContext` (direct validated IP literal dial + `net.Dialer.Control` defense-in-depth), and `CheckRedirect` matching PR Spec `§4.1A` & `§4.3` verbatim in `synapse-server/internal/core/egress/guard.go`

---

## Phase 3: User Story 2 - Physically Isolated Dual-Profile Clients, Response Body Caps & SSE Streaming Mode (Priority: P1)

**Goal**: Deliver `NewTrusted` / `NewUntrustedEgress` physical separation, structured `*ClientError`, `io.LimitReader` body caps (`KindResponseTooLarge` / 413), and `Streaming: true` (`Timeout == 0` + `IdleReadTimeout`) (maps to PR Spec `§2 #2,#3`, `§6 T3`, `§8 #3,#4,#5`).

**Independent Test**: `cd synapse-server && go test -race -run 'Test(Profile|Trusted|Untrusted|Limit|Stream|ReadLimited)' ./internal/infra/httpclient/...` passes cleanly.

### Tests for User Story 2 (TDD RED First) ⚠️

- [X] T004 [P] [US2] Write TDD RED unit tests verifying `NewTrusted` with self-signed TLS `httptest.NewTLSServer` CA (`config.NetworkRuntime`) and proxy routing vs `NewUntrustedEgress` immediately blocking `127.0.0.1` with `*ClientError{Kind: KindEgressDenied}` (wrapping `apperror.ErrEgressDenied`) without establishing a TCP connection in `synapse-server/internal/infra/httpclient/profile_test.go`
- [X] T005 [P] [US2] Write TDD RED unit tests verifying `MaxResponseBytes` (1024B cap against 2048B response) & `ReadLimited` return `*ClientError{Kind: KindResponseTooLarge}` wrapping `apperror.CodeTooLarge` (413), and `Streaming: true` forces `StdClient().Timeout == 0` (reading 60ms-interval chunks for 300ms beyond `TotalTimeout: 50ms`) while `IdleReadTimeout: 150ms` aborts stalled streams >200ms in `synapse-server/internal/infra/httpclient/limits_test.go`

### Implementation for User Story 2 (GREEN → REFACTOR)

- [X] T006 [P] [US2] Implement `ErrorKind` (8 constants), `ClientError` (`Error`/`Unwrap`), `KindOf`, and `classifyError` in `synapse-server/internal/infra/httpclient/errors.go`
- [X] T007 [P] [US2] Implement `ReadLimited`, `bodyControlRT`, `limitedReadCloser`, and `idleTimeoutReadCloser` in `synapse-server/internal/infra/httpclient/limits.go`
- [X] T008 [US2] Implement `Client`, `Do` (normalizing `*url.Error` into `*ClientError` while preserving `apperror.ErrEgressDenied`), `StdClient`, `Profile`, `WithIdempotent`, and `WithService` in `synapse-server/internal/infra/httpclient/client.go`
- [X] T009 [US2] Implement `Profile`, `TimeoutPolicy`, `RetryPolicy`, `BreakerPolicy`, `Options`, profile defaults, `NewTrusted`, `NewUntrustedEgress`, and `buildClient` RoundTripper assembly in `synapse-server/internal/infra/httpclient/profile.go`

---

## Phase 4: User Story 3 - Exponential Backoff Retry with `Retry-After` & Host-Scoped Circuit Breaking (Priority: P2)

**Goal**: Deliver bounded retry `RoundTripper` (`429 Retry-After` seconds/HTTP-Date + `502/503/504`, idempotency guard) and bounded `(service, host)` `sony/gobreaker/v2` circuit breaker with zero business fallback (maps to PR Spec `§2 #4,#5`, `§6 T4,T5`, `§8 #6,#7`).

**Independent Test**: `cd synapse-server && go test -race -run 'Test(Retry|Breaker)' ./internal/infra/httpclient/...` passes cleanly under `-race`.

### Tests for User Story 3 (TDD RED First) ⚠️

- [X] T010 [P] [US3] Write TDD RED unit tests covering `429 Retry-After` (seconds, HTTP-Date, clamping to `MaxRetryAfter`), `502/503/504` retries succeeding on attempt 3, retry exhaustion returning `*ClientError` (`KindRateLimited`/`KindUpstream5xx`), non-idempotent POST sending only 1 attempt, `WithIdempotent` + `GetBody` POST retrying, zero retry on `EgressDenied`/TLS/`context.Canceled`, and context cancel aborting retry sleep in `synapse-server/internal/infra/httpclient/retry_test.go`
- [X] T011 [P] [US3] Write TDD RED unit tests covering 5 consecutive 503s on Host A opening circuit to return `KindCircuitOpen` without dialing Host A while Host B stays healthy, HalfOpen probe recovery to Closed after cooldown, 10x 404 not tripping breaker, 10x `EgressDenied` not tripping breaker, and `MaxHostEntries` LRU eviction bound in `synapse-server/internal/infra/httpclient/breaker_test.go`

### Implementation for User Story 3 (GREEN → REFACTOR)

- [X] T012 [P] [US3] Implement `retryRT`, `parseRetryAfter`, `fullJitterBackoff`, `isRetryableRequest`/`isRetryableStatus`/`isRetryableError`, `resetRequestBody`, and `sleepWithContext` in `synapse-server/internal/infra/httpclient/retry.go`
- [X] T013 [P] [US3] Implement `breakerRT` with `container/list` bounded LRU (`MaxHostEntries=256`), per-`(service, host)` `gobreaker.CircuitBreaker[*http.Response]` (`IsSuccessful` allowing `ErrEgressDenied` & `context.Canceled`, 5xx/429 trip signal, `OnStateChange` metric) in `synapse-server/internal/infra/httpclient/breaker.go`

---

## Phase 5: User Story 4 - Redacted Outbound Logging & Prometheus Client Telemetry (Priority: P2)

**Goal**: Deliver redacted outbound logging (`SanitizeURL`, zero header/body printing, `obs` context metadata) and 4 Prometheus client metrics (maps to PR Spec `§2 #6`, `§6 T5`, `§8 #8`).

**Independent Test**: `cd synapse-server && go test -race -run 'TestObservability' ./internal/infra/httpclient/...` passes cleanly.

### Tests for User Story 4 (TDD RED First) ⚠️

- [X] T014 [P] [US4] Write TDD RED unit tests verifying `?access_token=topsecret&page=1` redacts `topsecret` while preserving `page=1`, `Authorization: Bearer sk-12345678` never appears in logs, log entry includes `service`/`profile`/`method`/`host`/`status`/`duration_ms`/`request_id`/`company_id`, and Prometheus registry exposes `synapse_http_client_requests_total`, `synapse_http_client_request_duration_seconds`, `synapse_http_client_retries_total`, and `synapse_http_client_breaker_state` in `synapse-server/internal/infra/httpclient/observability_test.go`

### Implementation for User Story 4 (GREEN → REFACTOR)

- [X] T015 [US4] Implement `observabilityRT`, `sanitizeURL`, redacted structured log emission, and Prometheus metric registration (`synapse_http_client_*`) in `synapse-server/internal/infra/httpclient/observability.go`

---

## Phase 6: Polish, Documentation Sync & Mandatory Quality Gates

**Purpose**: Complete PR Spec `§7` documentation sync and `§6 T6` / `§9` acceptance gates

- [X] T016 [P] Update `synapse-server/README.md` with `internal/core/egress` & `internal/infra/httpclient` L0 responsibilities, Dual-Profile selection rule (`NewUntrustedEgress` for user URLs, never `NewTrusted`), and zero-business-fallback boundary (`spec_03 §7`)
- [X] T017 [P] Update `docs/SuperAgent-Go/DDD/README.md` implementation board row for `SA-03` (`spec_03 §7`)
- [X] T018 Run full hard gates in `synapse-server/`: `gofmt -l .` (zero output), `go vet ./...`, `go build ./...`, `go test -race -count=1 -cover ./internal/core/egress/... ./internal/infra/httpclient/...` (verify ≥ 85% coverage per package), `go test -race ./...` (verify `SA-01` & `SA-02` regressions green), and verify package import boundaries and zero business fallback logic

---

## Dependencies & Execution Order

- **Phase 1 (`T001`)**: Verify dependencies and directory skeleton.
- **Phase 2 (`US1` `egress`)**: Independent of `httpclient`; TDD RED (`T002`) -> GREEN (`T003`).
- **Phases 3–5 (`US2`–`US4` `httpclient`)**: All belong to `package httpclient`; TDD RED tests (`T004`, `T005`, `T010`, `T011`, `T014`) written before production files (`T006`–`T009`, `T012`, `T013`, `T015`).
- **Phase 6 (`T016`–`T018`)**: Documentation sync and full verification gates.
