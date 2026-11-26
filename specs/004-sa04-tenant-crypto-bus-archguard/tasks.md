# Tasks: SA-04 L0 Core Security, Event Bus, In-Memory Cache/Limiter & Architecture Guard

**Input**: Design documents from `/specs/004-sa04-tenant-crypto-bus-archguard/` and frozen PR Spec `docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md`

**Prerequisites**: `plan.md` (required), `spec.md` (required), `research.md`, `data-model.md`, `contracts/sa04-contracts.md`, `quickstart.md`

**Tests**: TDD is MANDATORY per PR Spec `§6` & `§8` — every test suite is written first (RED) before its implementation (GREEN) and verified under `-race -cover` (≥ 85% per package), plus `internal/arch_test.go` positive and negative rule checks.

**Organization**: Grouped by user story (`US1`–`US4`) aligning 1:1 with PR Spec `§2` deliverables and `§6` TDD tasks.

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: User story label (`[US1]`..`[US4]`)
- Exact file paths included in every task description

## Path Conventions

- Backend module root: `synapse-server/` (`module github.com/Karlsk/synapse-os`, Go `1.26.1`)
- Target L0 packages: `synapse-server/internal/core/{tenant,crypto,eventbus}/`, `synapse-server/internal/infra/memcache/`, `synapse-server/internal/arch_test.go`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Verify pinned dependencies and L0 directory skeleton (maps to PR Spec `§6 T1`)

- [X] T001 Verify `synapse-server/go.mod` pins `golang.org/x/sync` with zero Redis/ClickHouse/GORM entries, and verify directory skeleton for `synapse-server/internal/core/{tenant,crypto,eventbus}/` and `synapse-server/internal/infra/memcache/`

---

## Phase 2: User Story 1 - Fail-Closed Multi-Tenant Context, Detached Propagation & Path/Symlink Defense (Priority: P1) 🎯 MVP

**Goal**: Deliver `internal/core/tenant` with Fail-Closed `Require`, `Detach`, 12-reserved-word `ValidateOrgSlug`, and traversal/symlink-safe `TenantPaths` & `SafeJoin` (maps to PR Spec `§2 #1`, `§6 T2`, `§8 #1,#2`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/core/tenant/...` passes with ≥ 85% coverage.

### Tests for User Story 1 (TDD RED First) ⚠️

- [X] T002 [P] [US1] Write TDD RED unit tests covering Fail-Closed `Require` (empty ctx and obs-only spoofed ctx rejected with `ErrMissingTenant`), `Optional`, `Detach` (survives parent cancellation while preserving `company_id`/`request_id`/`user_id`), `ValidateCompanyID`, `ValidateOrgSlug` (all 12 reserved words + Java Chinese error messages), `Path`, `PathFor`, and `SafeJoin` (rejecting `../evil`, `a..b`, absolute paths, and real `os.Symlink` escape outside `t.TempDir()`) in `synapse-server/internal/core/tenant/tenant_test.go`

### Implementation for User Story 1 (GREEN → REFACTOR)

- [X] T003 [P] [US1] Implement `DefaultSentinelID`, `ErrMissingTenant`, `ErrInvalidCompany`, `ErrPathEscape`, `WithCompany`, `Require`, `Optional`, and `Detach` in `synapse-server/internal/core/tenant/context.go`
- [X] T004 [P] [US1] Implement `ValidateCompanyID`, `Path`, `PathFor`, and `SafeJoin` (with `EvalSymlinks` nearest-existing-ancestor defense) in `synapse-server/internal/core/tenant/paths.go`
- [X] T005 [P] [US1] Implement `ValidateOrgSlug` (12 reserved words + Java `OrganizationService.java` Chinese error messages via `apperror.InvalidArg`) in `synapse-server/internal/core/tenant/slug.go`

---

## Phase 3: User Story 2 - Per-Tenant AES-256-GCM Envelope Encryption & Zero-Downtime KEK Rotation (Priority: P1)

**Goal**: Deliver `internal/core/crypto` with Java-compatible `v1:`/`v2:<companyID>:` AES-256-GCM encryption, atomic `0600` key file creation, concurrent DEK safety, and zero-downtime KEK rotation (maps to PR Spec `§2 #2`, `§6 T3`, `§8 #3,#4`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/core/crypto/...` passes with ≥ 85% coverage.

### Tests for User Story 2 (TDD RED First) ⚠️

- [X] T006 [P] [US2] Write TDD RED unit tests covering `v1:`/`v2:<companyID>:` roundtrips, cross-tenant `ErrCrossTenantSecret`, tampered prefix `ErrDecryptFailed`, 32-goroutine concurrent initial DEK generation persisting only once, atomic `0600` `secret.key` creation, K1->K2 KEK rotation (`PreviousKeysB64` + `RotateMasterKey`), idempotent `RewrapCompanyDEK` & `ReencryptPlatformSecret`, `InvalidateCompanyCache`, and lossless decryption after removing K1 in `synapse-server/internal/core/crypto/cipher_test.go`

### Implementation for User Story 2 (GREEN → REFACTOR)

- [X] T007 [US2] Implement `ErrCrossTenantSecret`, `ErrInvalidCiphertext`, `ErrDecryptFailed`, `ErrUnknownCompany`, `CompanyKeyStore`, `Options`, `SecretCipher`, `NewSecretCipher` (atomic `0600` key file creation), `GenerateKeyB64`, `Encrypt`, and `Decrypt` in `synapse-server/internal/core/crypto/cipher.go`
- [X] T008 [US2] Implement `openMaster`, `RotateMasterKey`, `RewrapCompanyDEK`, `ReencryptPlatformSecret`, and `InvalidateCompanyCache` in `synapse-server/internal/core/crypto/rotate.go`

---

## Phase 4: User Story 3 - Type-Safe In-Process Generic Event Bus (Priority: P2)

**Goal**: Deliver `internal/core/eventbus` with generic `Subscribe[T]`/`Publish[T]`/`PublishAsync[T]`, single-subscriber panic recovery, `tenant.Detach(ctx)` async propagation, bounded concurrency (64), and `Drain(ctx)` (maps to PR Spec `§2 #3`, `§6 T4`, `§8 #5`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/core/eventbus/...` passes with ≥ 85% coverage.

### Tests for User Story 3 (TDD RED First) ⚠️

- [X] T009 [P] [US3] Write TDD RED unit tests covering ordered synchronous `Publish[T]`, single-subscriber panic recovery firing `WithOnPanic` while sibling subscriber runs, `Unsubscribe`, nested `Publish` without deadlock, `PublishAsync[T]` surviving parent cancellation with `tenant.Require` & `obs.RequestIDFrom`, `WithMaxAsyncConcurrency` cap enforcement, and `Drain(ctx)` waiting for all in-flight handlers in `synapse-server/internal/core/eventbus/bus_test.go`

### Implementation for User Story 3 (GREEN → REFACTOR)

- [X] T010 [US3] Implement `Option`, `WithLogger`, `WithMaxAsyncConcurrency`, `WithOnPanic`, `Bus`, `New`, `Drain`, `Subscribe[T]`, `Publish[T]`, and `PublishAsync[T]` matching PR Spec `§4.1C` & `§5.3` in `synapse-server/internal/core/eventbus/bus.go`

---

## Phase 5: User Story 4 - Zero-Redis Sharded Cache, Sliding-Window Limiter & AST Architecture Guard (Priority: P1)

**Goal**: Deliver zero-Redis `internal/infra/memcache` (`ShardedCache[K,V]` + `SlidingWindowLimiter`) and full-repo AST guard `internal/arch_test.go` enforcing all 6 non-negotiable architecture rules (maps to PR Spec `§2 #4,#5`, `§6 T5,T6`, `§8 #6,#7,#8`).

**Independent Test**: `cd synapse-server && go test -race -cover ./internal/infra/memcache/... && go test -race -v ./internal` passes with ≥ 85% coverage.

### Tests for User Story 4 (TDD RED First) ⚠️

- [X] T011 [P] [US4] Write TDD RED unit tests for `ShardedCache[K, V]` covering 32-shard concurrent `-race` access, TTL expiration, `MaxEntries` LRU eviction keeping `Len() <= MaxEntries`, 32-goroutine `GetOrLoad` singleflight calling `loader` exactly once, loader error non-caching, `Delete`/`DeleteFunc` tenant-prefix bulk invalidation, `PurgeExpired`, `StartJanitor`, and `Stats` in `synapse-server/internal/infra/memcache/cache_test.go`
- [X] T012 [P] [US4] Write TDD RED unit tests for `SlidingWindowLimiter` covering `Limit=5, Window=15m` (5 `RecordFailure` allowed, 6th & read-only `Peek` blocked with `RetryAfter > 0`, `Reset` unlocking immediately, injectable clock advancing 15m auto-unlocking), `Allow`/`AllowN`, `PurgeExpired`, and `MaxKeys` memory cap under random IP flood in `synapse-server/internal/infra/memcache/ratelimit_test.go`

### Implementation for User Story 4 (GREEN → REFACTOR)

- [X] T013 [P] [US4] Implement `CacheOptions`, `CacheStats`, `ShardedCache[K comparable, V any]` (`NewShardedCache`, `Get`, `Set`, `GetOrLoad`, `Delete`, `DeleteFunc`, `PurgeExpired`, `StartJanitor`, `Len`, `Stats`) using `hash/maphash.Comparable` + per-shard LRU + `singleflight.Group` in `synapse-server/internal/infra/memcache/cache.go`
- [X] T014 [P] [US4] Implement `LimiterOptions`, `Decision`, `SlidingWindowLimiter` (`NewSlidingWindowLimiter`, `Allow`, `AllowN`, `Peek`, `RecordFailure`, `Reset`, `PurgeExpired`) using O(1) 2-window weighted sliding window and bounded `MaxKeys` eviction in `synapse-server/internal/infra/memcache/ratelimit.go`
- [X] T015 [US4] Implement `synapse-server/internal/arch_test.go` enforcing all 6 AST/`go.mod` rules (L0–L5 unidirectional layering, L0 core purity & sub-DAG, universal L1–L5 same-layer parallel module isolation, `pgx`/`goose` isolation, `os/exec` launcher isolation, zero Redis) with both positive full-repo scan (`TestArchitectureGuard`) and negative violation detection suite (`TestArchRulesDetectViolations`)

---

## Phase 6: Polish, Documentation Sync & Full L0 Regression Gates

**Purpose**: Complete PR Spec `§7` documentation sync and `§6 T6` / `§9` acceptance gates

- [X] T016 [P] Update `synapse-server/README.md` with `SA-04` package responsibilities, L0–L6 `internal/arch_test.go` guard rules, and zero-downtime KEK master-key rotation operational guide (`spec_04 §7`)
- [X] T017 [P] Update `docs/SuperAgent-Go/DDD/README.md` implementation board row for `SA-04` (`spec_04 §7`)
- [X] T018 Run all hard quality gates in `synapse-server/`: `gofmt -l .` (zero output), `go vet ./...`, `go build ./...`, `go test -race -count=1 -cover ./internal/core/tenant/... ./internal/core/crypto/... ./internal/core/eventbus/... ./internal/infra/memcache/...` (verify ≥ 85% coverage per package), `go test -race -v ./internal` (verify `arch_test.go`), `go test -race ./...` (full L0 regression `SA-01`–`SA-04`), and confirm zero Redis dependencies in `go.mod` and source

---

## Dependencies & Execution Order

- **Phase 1 (`T001`)**: Verify dependencies and directories.
- **Phase 2 (`US1` `tenant`)**: Independent L0 package; unlocks `crypto` and `eventbus`.
- **Phases 3–5 (`US2` `crypto`, `US3` `eventbus`, `US4` `memcache`)**: `memcache` is independent of `tenant`; `crypto` & `eventbus` depend on `tenant`. All TDD RED tests (`T002`, `T006`, `T009`, `T011`, `T012`) written before corresponding implementations.
- **`T015` (`internal/arch_test.go`)**: Runs against all L0 packages (`SA-01`–`SA-04`) once implemented.
- **Phase 6 (`T016`–`T018`)**: Documentation sync and final full-L0 verification gates.
