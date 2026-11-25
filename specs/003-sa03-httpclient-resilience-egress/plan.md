# Implementation Plan: SA-03 L0 Outbound Network & Security Foundation — EgressGuard & Dual-Profile Resilient HTTP Client

**Branch**: `003-sa03-httpclient-resilience-egress` | **Date**: 2026-11-25 | **Spec**: [spec.md](./spec.md) | **PR Spec**: [docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md](../../docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md)

**Input**: Feature specification from `specs/003-sa03-httpclient-resilience-egress/spec.md` and frozen PR contract `docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md`.

## Summary

Implement two tightly cohesive L0 outbound network & security packages under `synapse-server/`:
1. `internal/core/egress` (`guard.go`, `guard_test.go`) — transport-level anti-SSRF and anti-DNS-rebinding guard (`ToURI`, `IsPublic` with mandatory `Unmap()`, `CheckURL`, `DialContext` dialing validated IP literals directly + `net.Dialer.Control` defense-in-depth, and `CheckRedirect` enforcing 6-hop cap + per-hop re-check + cross-origin `Authorization`/`Cookie`/`Proxy-Authorization` stripping).
2. `internal/infra/httpclient` (`profile.go`, `client.go`, `errors.go`, `limits.go`, `retry.go`, `breaker.go`, `observability.go` + 5 `*_test.go` suites) — physically separated `NewTrusted` (enterprise proxy + private CA, TLS 1.2+) and `NewUntrustedEgress` (mandatory `egress.Guard.DialContext`, `Proxy: nil`, `CheckRedirect`, default 20 MiB `io.LimitReader`), SSE/large-file `Streaming: true` mode (`http.Client.Timeout == 0` + per-read `IdleReadTimeout`), exponential backoff + full jitter retry (`429 Retry-After` seconds/HTTP-Date + `502/503/504`), bounded `(service, host)` `sony/gobreaker/v2` circuit breaking (`MaxHostEntries=256` LRU), redacted logging (`SanitizeURL`, zero header/body printing), and Prometheus metrics. Strictly classifies errors via `*ClientError` with **zero business fallback logic**.

## Technical Context

**Language/Version**: Go `1.26.1` (`synapse-server/`, `module github.com/Karlsk/synapse-os`, Go Modules, `CGO_ENABLED=0` static binary compatible)

**Primary Dependencies**:
- `internal/core/egress`: Go stdlib + `github.com/Karlsk/synapse-os/internal/core/apperror` ONLY (zero third-party deps)
- `internal/infra/httpclient`: `github.com/sony/gobreaker/v2` (`v2.4.0`), `github.com/prometheus/client_golang/prometheus` (`v1.25.0`), `internal/core/apperror`, `internal/core/egress`, `internal/infra/config`, `internal/infra/obs` (plus Go stdlib)

**Storage**: N/A (stateless L0 network/security layer; zero SQL, zero DB, zero Redis)

**Testing**: Colocated Go `*_test.go` suites with `t.Parallel()`, mock `egress.Resolver` (mixed public+private & DNS rebinding), local `httptest.Server` / `httptest.NewTLSServer`, `bytes.Buffer` redacted log capture, isolated `obs.Metrics` registry verification

**Target Platform**: Linux (`linux/amd64`, `linux/arm64`) & macOS (`darwin/arm64`)

**Project Type**: Backend Go L0 outbound network & SSRF security libraries

**Performance Goals**: Pooled HTTP/2-capable transports (`Trusted`: 100/20 idle conns; `Untrusted`: 64/10 idle conns); bounded `MaxHostEntries=256` LRU preventing arbitrary-host OOM; zero extra DNS round-trips on dialed IPs; streaming `Timeout=0`

**Constraints**: Zero Redis / ClickHouse / GORM / DB / `os/exec`; zero business fallback logic inside `httpclient`; `UntrustedEgressClient` MUST NEVER bypass `EgressGuard.DialContext` or `io.LimitReader`; `Streaming: true` MUST set `http.Client.Timeout == 0`; `gofmt -l .`, `go vet ./...`, `go build ./...`, `go test -race -cover` ≥ 85% per package

**Scale/Scope**: 8 Go source files + 6 Go test files (~1,100–1,400 LOC total)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Verify compliance with `.specify/memory/constitution.md` (v1.0.1) and `AGENTS.md`:

- [x] **V1 Scope & Zero Redundant Infra**: Zero Redis, zero ClickHouse, zero ORM/DI; uses only whitelisted `sony/gobreaker/v2` and `prometheus/client_golang`.
- [x] **L0–L6 Layering & Explicit Wiring**: `internal/core/egress` (L0) depends only on `internal/core/apperror` (L0); `internal/infra/httpclient` (L0) depends only on L0 `apperror`, `egress`, `config`, `obs`. Zero upward imports or L1–L6 dependencies.
- [x] **Multi-Tenant Fail-Closed & Crypto**: Outbound logs propagate `obs` `request_id`/`company_id` via `req.Context()`, redact sensitive query params and URL passwords, and NEVER print headers (`Authorization`/`Cookie`) or request/response bodies.
- [x] **Infrastructure Isolation**: Centralizes all outbound HTTP behind `TrustedClient` and `UntrustedEgressClient` (`DialContext`-level anti-DNS-rebinding `EgressGuard`, <=6 redirect re-checks, `io.LimitReader` 20 MiB cap). Zero `pgx`/`goose`/`os/exec`.
- [x] **PG17 Schema & Migrations**: N/A (stateless L0 network layer).
- [x] **Resilience & Concurrency**: Layered timeouts (`Dial=5s/10s`, `TLS=10s`, `Header=30s`, `Total=60s`, `Streaming Timeout=0` + `IdleReadTimeout`); exponential backoff + Full Jitter (`MaxAttempts=3`, streaming=`1`, `Retry-After <= 30s`, never retry `EgressDenied`/TLS/`Canceled`); `gobreaker/v2` per `(service, host)` with `MaxHostEntries=256` LRU; zero business fallback in `httpclient`; lock-free network I/O.
- [x] **API, MCP & Go Quality Gates**: `EgressGuard` uses exact Chinese error messages wrapping `apperror.ErrEgressDenied` (403); `ReadLimited` wraps `apperror.TooLarge` (413); verified via `gofmt`, `go vet`, `go build`, `go test -race -cover` (≥ 85%) + `SA-01`/`SA-02` regressions green.

*Post-Phase-1 Design Re-check*: PASS — All 7 gates satisfied with zero exceptions.

## Project Structure

### Documentation (this feature)

```text
specs/003-sa03-httpclient-resilience-egress/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── egress-httpclient-contracts.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
synapse-server/
├── README.md                                  # Updated with Dual-Profile selection rule & zero-business-fallback rule (§7)
└── internal/
    ├── core/
    │   └── egress/                            # [L0] Anti-SSRF & anti-DNS-rebinding guard
    │       ├── guard.go                       # Resolver, Options, Guard, PublicOnly, New/MustNew, ToURI, IsPublic, CheckURL, DialContext, CheckRedirect
    │       └── guard_test.go                  # §8 Tests #1 & #2
    └── infra/
        └── httpclient/                        # [L0] Dual-profile resilient HTTP client
            ├── profile.go                     # Profile, TimeoutPolicy, RetryPolicy, BreakerPolicy, Options, defaults, NewTrusted, NewUntrustedEgress
            ├── client.go                      # Client, Do, StdClient, Profile, WithIdempotent, WithService
            ├── errors.go                      # ErrorKind (8 constants), ClientError, KindOf, classifyError
            ├── limits.go                      # ReadLimited, bodyControlRT, limitedReadCloser, idleTimeoutReadCloser
            ├── retry.go                       # retryRT, parseRetryAfter, fullJitterBackoff, isRetryableRequest/Status/Error
            ├── breaker.go                     # breakerRT, bounded LRU host map (MaxHostEntries), gobreaker/v2 wrapper
            ├── observability.go               # observabilityRT, SanitizeURL, redacted slog & Prometheus metrics
            ├── profile_test.go                # §8 Test #3
            ├── limits_test.go                 # §8 Tests #4 & #5
            ├── retry_test.go                  # §8 Test #6
            ├── breaker_test.go                # §8 Test #7
            └── observability_test.go          # §8 Test #8
```

**Structure Decision**: Direct 1:1 mapping to `docs/SuperAgent-Go/DDD/L0/spec_03_httpclient_resilience_egress.md` §2 deliverables table under `synapse-server/`.

## Complexity Tracking

> No Constitution violations; table intentionally empty.
