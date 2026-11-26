# Implementation Plan: SA-04 L0 Core Security, Event Bus, In-Memory Cache/Limiter & Architecture Guard

**Branch**: `004-sa04-tenant-crypto-bus-archguard` | **Date**: 2026-11-25 | **Spec**: [spec.md](./spec.md) | **PR Spec**: [docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md](../../docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md)

**Input**: Feature specification from `specs/004-sa04-tenant-crypto-bus-archguard/spec.md` and frozen PR contract `docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md`.

## Summary

Complete the L0 foundation layer (`SA-04`) by delivering 5 critical runtime/security primitives and mechanical repository guardrails:
1. `internal/core/tenant` (`context.go`, `paths.go`, `slug.go`, `tenant_test.go`) — Fail-Closed tenant `context.Context` (`WithCompany`, `Require`, `Optional`, `Detach`), 12-reserved-word `ValidateOrgSlug` (matching Java `OrganizationService.java` Chinese messages), and `Path`/`PathFor`/`SafeJoin` blocking `..` traversal and symlink escapes.
2. `internal/core/crypto` (`cipher.go`, `rotate.go`, `cipher_test.go`) — Java-compatible AES-256-GCM envelope encryption (`v1:` platform / `v2:<companyID>:` tenant + AAD binding), atomic `0600` `secret.key` generation, concurrent DEK creation safety, and zero-downtime KEK rotation (`PreviousKeysB64`, `RotateMasterKey`, `RewrapCompanyDEK`, `ReencryptPlatformSecret`, `InvalidateCompanyCache`).
3. `internal/core/eventbus` (`bus.go`, `bus_test.go`) — compile-time type-safe generic in-process pub/sub (`Subscribe[T]`, `Publish[T]`, `PublishAsync[T]`), lock-free subscriber invocation, single-subscriber panic isolation, `tenant.Detach(ctx)` async propagation, bounded concurrency semaphore (default 64), and graceful `Drain(ctx)`.
4. `internal/infra/memcache` (`cache.go`, `ratelimit.go`, `cache_test.go`, `ratelimit_test.go`) — zero-Redis 32-shard concurrent TTL cache (`ShardedCache[K, V]` with O(1) LRU eviction, `singleflight` `GetOrLoad`, `DeleteFunc`, `StartJanitor`) and O(1)-space weighted sliding-window anti-brute-force limiter (`SlidingWindowLimiter` with `Allow`/`AllowN`/`Peek`/`RecordFailure`/`Reset`/`PurgeExpired` and `MaxKeys=100_000` OOM cap).
5. `internal/arch_test.go` — Go AST (`go/parser` + `go/token`) repository guard enforcing all 6 non-negotiable red lines across `synapse-server/internal/...` and `go.mod`, accompanied by negative violation-detection tests.

## Technical Context

**Language/Version**: Go `1.26.1` (`synapse-server/`, `module github.com/Karlsk/synapse-os`, Go Modules, `CGO_ENABLED=0` static binary compatible)

**Primary Dependencies**:
- `internal/core/tenant`: Go stdlib + `internal/core/apperror` + `internal/infra/obs` ONLY
- `internal/core/crypto`: Go stdlib (`crypto/aes`, `crypto/cipher`, `crypto/rand`, `encoding/base64`, `os`, `path/filepath`, `sync`) + `internal/core/tenant` ONLY
- `internal/core/eventbus`: Go stdlib (`context`, `log/slog`, `reflect`, `runtime/debug`, `slices`, `sync`, `sync/atomic`) + `internal/core/tenant` ONLY
- `internal/infra/memcache`: Go stdlib (`container/list`, `context`, `fmt`, `hash/maphash`, `sync`, `sync/atomic`, `time`) + `golang.org/x/sync/singleflight` (already pinned in `go.mod`) — ZERO Redis
- `internal/arch_test.go`: Go stdlib (`go/parser`, `go/token`, `io/fs`, `os`, `path/filepath`, `strings`, `testing`) ONLY

**Storage**: N/A (L0 stateless primitives; `crypto.CompanyKeyStore` decouples via string-only port; zero `pgx`/`goose`/`database/sql`)

**Testing**: Colocated Go `*_test.go` suites with `t.Parallel()`, real `os.Symlink` escape fixtures in `t.TempDir()`, 32-goroutine race tests for DEK creation & singleflight cache, injectable clock for sliding-window limiter, and both positive full-repo AST validation + negative violation unit tests in `internal/arch_test.go`

**Target Platform**: Linux (`linux/amd64`, `linux/arm64`) & macOS (`darwin/arm64`)

**Project Type**: Backend Go L0 security, eventing, caching/rate-limiting, and AST governance packages

**Performance Goals**: 32-shard lock striping in `ShardedCache` & `SlidingWindowLimiter`; O(1) LRU eviction; O(1) space per key in sliding window; bounded 64-concurrency async event bus; lock-free subscriber invocation

**Constraints**: Zero Redis / ClickHouse / GORM / `pgx` / `goose` / `os/exec`; strict compliance with `internal/arch_test.go` all 6 rules; `gofmt -l .` zero diff; `go vet ./...` zero warnings; `go test -race -cover` ≥ 85% across all 4 new packages; full L0 regression (`SA-01`–`SA-04`) green

**Scale/Scope**: 8 production source files + 6 test files (including `internal/arch_test.go`) (~1,300–1,600 LOC total)

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Verify compliance with `.specify/memory/constitution.md` (v1.0.1) and `AGENTS.md`:

- [x] **V1 Scope & Zero Redundant Infra**: Zero Redis clients or distributed cache abstractions; `internal/infra/memcache` is pure in-process; `internal/arch_test.go` Rule 6 mechanically blocks Redis dependencies in `go.mod` and imports.
- [x] **L0–L6 Layering & Explicit Wiring**: All packages reside in L0 (`internal/core/{tenant,crypto,eventbus}` and `internal/infra/memcache`), strictly honoring Rule 2 (`core/*` never imports `infra/{config,db,httpclient,lifecycle,memcache}`, only `core/tenant` imports `infra/obs`); `internal/arch_test.go` mechanically enforces universal same-layer isolation across L1–L5 (Constitution v1.0.1 Principle II).
- [x] **Multi-Tenant Fail-Closed & Crypto**: `tenant.Require(ctx)` fails closed on missing/untrusted context; `tenant.Detach(ctx)` strips cancellation & DB tx while preserving `company_id`/`request_id`/`user_id`; `SecretCipher` enforces per-company AES-256-GCM DEK envelope + AAD binding + cross-tenant refusal + `0600` atomic key file + KEK rotation.
- [x] **Infrastructure Isolation**: Zero `pgx`/`goose`/`os/exec`/raw `http.Client` in `SA-04`; `internal/arch_test.go` Rules 4 & 5 mechanically enforce `pgx`/`goose` and `os/exec` isolation repository-wide.
- [x] **PG17 Schema & Migrations**: N/A (no DDL/SQL in SA-04).
- [x] **Resilience & Concurrency**: `eventbus` caps async concurrency at 64 with `Drain(ctx)` and panic recovery; `ShardedCache` and `SlidingWindowLimiter` enforce `MaxEntries`/`MaxKeys` OOM bounds; no I/O inside `sync.Mutex` (except serialized first-time DEK creation under dedicated `dekMu` matching `spec_04 §5.2`).
- [x] **API, MCP & Go Quality Gates**: `ValidateOrgSlug` returns `apperror.InvalidArg` with Java-compatible Chinese messages; `gofmt`, `go vet`, `go build`, `go test -race -cover` (≥ 85%) + `internal/arch_test.go` + full `SA-01`–`SA-03` regression green.

*Post-Phase-1 Design Re-check*: PASS — All 7 gates satisfied with zero exceptions.

## Project Structure

### Documentation (this feature)

```text
specs/004-sa04-tenant-crypto-bus-archguard/
├── plan.md
├── research.md
├── data-model.md
├── quickstart.md
├── contracts/
│   └── sa04-contracts.md
├── checklists/
│   └── requirements.md
└── tasks.md
```

### Source Code (repository root)

```text
synapse-server/
├── README.md                                  # Updated with L0-L6 arch_test rules & KEK zero-downtime rotation guide (§7)
└── internal/
    ├── arch_test.go                           # [Guard] 6 AST layering/isolation/zero-Redis rules + negative violation tests (§8 #8)
    ├── core/
    │   ├── tenant/                            # [L0] Fail-Closed tenant context, slug, paths
    │   │   ├── context.go                     # DefaultSentinelID, Err*, WithCompany, Require, Optional, Detach
    │   │   ├── paths.go                       # ValidateCompanyID, Path, PathFor, SafeJoin (EvalSymlinks defense)
    │   │   ├── slug.go                        # ValidateOrgSlug (12 reserved words + Java Chinese messages)
    │   │   └── tenant_test.go                 # §8 Tests #1 & #2
    │   ├── crypto/                            # [L0] AES-256-GCM envelope encryption + KEK rotation
    │   │   ├── cipher.go                      # Err*, CompanyKeyStore, Options, SecretCipher, NewSecretCipher, GenerateKeyB64, Encrypt, Decrypt, atomic 0600 key file
    │   │   ├── rotate.go                      # openMaster, RotateMasterKey, RewrapCompanyDEK, ReencryptPlatformSecret, InvalidateCompanyCache
    │   │   └── cipher_test.go                 # §8 Tests #3 & #4
    │   └── eventbus/                          # [L0] Generic in-process sync/async event bus
    │       ├── bus.go                         # Option, With*, Bus, New, Drain, Subscribe[T], Publish[T], PublishAsync[T]
    │       └── bus_test.go                    # §8 Test #5
    └── infra/
        └── memcache/                          # [L0] Zero-Redis sharded TTL cache + sliding-window limiter
            ├── cache.go                       # CacheOptions, CacheStats, ShardedCache[K,V] (32 shards, LRU, singleflight, janitor)
            ├── ratelimit.go                   # LimiterOptions, Decision, SlidingWindowLimiter (O(1) weighted window, MaxKeys cap)
            ├── cache_test.go                  # §8 Test #6
            └── ratelimit_test.go              # §8 Test #7
```

**Structure Decision**: Direct 1:1 mapping to `docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md` §2 deliverables table under `synapse-server/`.

## Complexity Tracking

> No Constitution violations; table intentionally empty.
