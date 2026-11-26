# Feature Specification: SA-04 L0 Core Security, Event Bus, In-Memory Cache/Limiter & Architecture Guard

**Feature Branch**: `004-sa04-tenant-crypto-bus-archguard`

**Created**: 2026-11-25

**Status**: Draft

**Input**: PR Spec `docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md` (PR `SA-04`) — L0 租户安全、信封加密、进程内事件总线、纯进程内分片缓存/防爆破限流器与全仓 L0–L6 AST 架构守卫。

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Fail-Closed Multi-Tenant Context, Detached Propagation & Path Traversal/Symlink Defense (Priority: P1)

Every tenant-scoped storage operation, background goroutine, and filesystem helper needs an unforgeable Fail-Closed tenant context that refuses missing or unauthenticated tenant IDs (never falling back to a default tenant or trusting logging metadata), detaches safely from HTTP cancellation and database transaction handles while preserving tenant and tracing metadata, enforces organization slug rules (including the 12 reserved words), and blocks `..` traversal and symlink escapes out of tenant roots.

**Why this priority**: Multi-tenant isolation is a non-negotiable security invariant required before any L1 store (`SA-07`–`SA-10`) or L2+ service (`SA-15`–`SA-40`) can be implemented.

**Independent Test**: Fully testable offline via unit tests checking missing-context rejection, spoofed-obs-metadata rejection, detached context preservation after parent cancellation, slug validation against all 12 reserved words, `..`/absolute path rejection, and real `os.Symlink` escape rejection inside `t.TempDir()`.

**Acceptance Scenarios**:

1. **Given** an empty context or a context containing only observability metadata (`obs.WithCompanyID`) without calling `tenant.WithCompany`, **When** `tenant.Require(ctx)` is invoked, **Then** it fails closed with `tenant.ErrMissingTenant`.
2. **Given** a request context populated via `tenant.WithCompany`, `obs.WithRequestID`, and `obs.WithUserID` that is subsequently canceled, **When** `tenant.Detach(ctx)` is created prior to background dispatch, **Then** the detached context has `Err() == nil` while preserving `tenant.Require(ctx)` and the observability triple (`request_id`, `company_id`, `user_id`), and strips any DB transaction handle.
3. **Given** organization slugs containing invalid characters, lengths outside 3–32, or any of the 12 reserved words (`admin`, `api`, `www`, `root`, `system`, `login`, `mcp`, `static`, `support`, `help`, `default`, `platform`), **When** validated via `ValidateOrgSlug`, **Then** each is rejected with `apperror.InvalidArg(...)` using clear Chinese messages; valid normalized slugs pass cleanly.
4. **Given** a tenant root directory and user-supplied path segments containing `../`, `a..b` in company IDs, absolute paths, or an on-disk symlink pointing outside the tenant root, **When** resolved via `PathFor` or `SafeJoin`, **Then** every escape attempt fails with `ErrInvalidCompany` or `ErrPathEscape`.

---

### User Story 2 - Per-Tenant AES-256-GCM Envelope Encryption & Zero-Downtime KEK Rotation (Priority: P1)

Enterprise deployments need per-company AES-256-GCM DEK envelope encryption (`v2:<companyID>:...` with AAD bound to `companyID`, and `v1:...` platform secrets) compatible with the legacy ciphertext wire protocol, atomic `0600` key-file generation when no key is pre-configured, strict cross-tenant decryption refusal, and zero-downtime master KEK rotation that only re-wraps each company's 32-byte DEK without re-encrypting stored tenant credentials.

**Why this priority**: All stored AI provider keys, IM integration secrets, and connector OAuth tokens depend on tenant-isolated envelope encryption and auditable KEK rotation.

**Independent Test**: Fully testable with an in-memory `CompanyKeyStore` verifying `v1:`/`v2:<companyID>:` roundtrips, cross-tenant rejection, tampered prefix rejection, 32-goroutine concurrent initial DEK creation idempotency, `0600` key file permissions, and full K1 -> K2 KEK rotation + `RewrapCompanyDEK` + K1 removal.

**Acceptance Scenarios**:

1. **Given** a `SecretCipher` backed by a `CompanyKeyStore` and tenant `acme` context, **When** plaintext is encrypted, **Then** the stored company DEK starts with `v1:` and the tenant ciphertext starts with `v2:acme:`, decryptable under `acme` context, while decrypting under `beta` returns `ErrCrossTenantSecret` and tampering the prefix to `v2:beta:` fails with `ErrDecryptFailed`.
2. **Given** 32 goroutines concurrently encrypting for the same company whose DEK does not yet exist, **When** all run in parallel under `-race`, **Then** only one DEK is generated and persisted, and all 32 ciphertexts decrypt cleanly.
3. **Given** `Options.MasterKeyB64` is empty and `KeyFile` points to a nonexistent path, **When** `NewSecretCipher` initializes, **Then** it atomically creates a 32-byte Base64 key file with exact `0600` permissions.
4. **Given** tenant ciphertext `C` encrypted under master key `K1`, **When** a new `SecretCipher` starts with `MasterKeyB64 = K2` and `PreviousKeysB64 = [K1]`, **Then** it still decrypts `C`; calling `RewrapCompanyDEK(ctx, "acme")` returns `(true, nil)` once and `(false, nil)` idempotently on repeat; after rebuilding with only `MasterKeyB64 = K2` (removing `K1`), original ciphertext `C` still decrypts losslessly without touching `C`.

---

### User Story 3 - Type-Safe In-Process Generic Event Bus (Priority: P2)

Cross-domain event notifications (company started, integration changed, notices, inbox notifications) need a compile-time type-safe generic in-process event bus supporting synchronous ordered delivery, single-subscriber panic isolation, unsubscription, bounded-concurrency asynchronous delivery using `tenant.Detach(ctx)`, and graceful `Drain(ctx)` during shutdown.

**Why this priority**: Enables L1–L4 modules to communicate cleanly without circular or same-layer dependencies, directly upholding Constitution Principle II.

**Independent Test**: Fully testable offline by subscribing multiple typed handlers (including one that panics), verifying ordering, `Unsubscribe()`, `PublishAsync` context detachment after parent cancellation, bounded concurrency, and `Drain(ctx)` waiting for all in-flight handlers.

**Acceptance Scenarios**:

1. **Given** two subscribers registered on the same event type where subscriber 1 panics, **When** `Publish` runs synchronously, **Then** subscriber 1's panic is recovered, logged, and forwarded to `WithOnPanic`, and subscriber 2 still executes cleanly; after calling `unsubscribe()`, a subscriber receives no further events.
2. **Given** a parent context with `tenant.WithCompany` and `obs.WithRequestID` that is canceled immediately after `PublishAsync`, **When** async handlers execute under bounded concurrency (`WithMaxAsyncConcurrency`), **Then** handlers still read valid `tenant.Require(ctx)` and `obs.RequestIDFrom(ctx)`, concurrent executions never exceed the configured bound, and `Drain(ctx)` waits until all in-flight handlers finish.

---

### User Story 4 - Zero-Redis Sharded TTL Cache, Anti-Brute-Force Sliding Window Limiter & AST Layer Guard (Priority: P1)

Single-binary on-premises deployments need zero-Redis in-process primitives—a 32-shard concurrent TTL cache with singleflight thundering-herd protection and bounded eviction, plus an O(1)-space sliding-window login brute-force limiter with memory caps—and a mechanical AST architecture test (`internal/arch_test.go`) enforcing all 6 L0–L6 layering and dependency red lines on every test run.

**Why this priority**: Locking down `internal/arch_test.go` at the end of L0 guarantees every future PR (`SA-05`–`SA-42`) is mechanically prevented from violating L0–L6 layering, universal same-layer isolation, DB/subprocess boundaries, or the zero-Redis rule.

**Independent Test**: Cache concurrency, TTL expiration, `MaxEntries` eviction, 32-goroutine `GetOrLoad` singleflight, and `DeleteFunc` are verified offline; limiter 5-failure lock, `Peek`, `Reset`, injectable-clock window slide, and `MaxKeys` cap are verified offline; `internal/arch_test.go` verifies the real repository AND runs negative AST unit tests proving all 6 rules catch violations.

**Acceptance Scenarios**:

1. **Given** a `ShardedCache[K, V]` with `MaxEntries` and TTL, **When** 32 goroutines concurrently call `GetOrLoad` for the same missing key, **Then** `loader` executes exactly once, expired entries return `(_, false)`, exceeding `MaxEntries` evicts entries so `Len() <= MaxEntries`, `DeleteFunc` removes matching tenant keys, and `StartJanitor` runs cleanly until context cancellation.
2. **Given** a `SlidingWindowLimiter` with `Limit=5, Window=15m, MaxKeys=N`, **When** 5 failures are recorded via `RecordFailure`, **Then** the 6th failure and read-only `Peek` return `Allowed=false` with `RetryAfter > 0`; `Reset` unlocks immediately; advancing the clock by 15m unlocks automatically; flooding >`MaxKeys` distinct keys keeps tracked entries bounded.
3. **Given** `internal/arch_test.go` scanning `synapse-server/internal/...` and `go.mod`, **When** `go test ./...` runs, **Then** the real L0 repository passes 100%, and negative test cases simulating `agent->task`, `task->agent`, `storage->platform`, `storage->runtime`, `web->pgx`, `core->infra/db`, unauthorized `os/exec`, and `go-redis` imports are all caught and reported accurately.

---

### Edge Cases

- What happens when `SecretCipher.Decrypt` receives an empty string? Aligning with Java `SecretCipher.java`, empty string returns `("", nil)` (or non-prefixed/malformed non-empty ciphertext returns `ErrInvalidCiphertext`).
- What happens when `SecretCipher.Encrypt` runs with `Store == nil` or without a tenant in `ctx`? Falls back to platform-level `v1:<sealed>` encryption with `AAD = nil` (documented in `spec_04 §4.1B` & `§5.2`).
- What happens when `SafeJoin` checks a target path whose leaf file does not exist yet, while an intermediate directory is a symlink escaping `baseDir`? `nearestExistingAncestor(target)` walks upward to the nearest existing directory and verifies its `EvalSymlinks` result stays inside `realBase`.
- What happens when `Publish` triggers a nested `Publish` or `Subscribe` from inside a subscriber callback? `Publish` snapshots handlers under read lock and invokes them outside the lock, preventing deadlocks.
- What happens when `GetOrLoad`'s `loader` returns an error? The error is returned without caching a negative entry so subsequent callers can retry.

## Requirements *(mandatory)*

### Functional Requirements

- **FR-001**: System MUST provide `internal/core/tenant` exporting `DefaultSentinelID`, `ErrMissingTenant`, `ErrInvalidCompany`, `ErrPathEscape`, `WithCompany`, `Require`, `Optional`, `Detach`, `ValidateCompanyID`, `ValidateOrgSlug`, `Path`, `PathFor`, and `SafeJoin` matching `spec_04 §4.1A` verbatim; `Require` MUST read only the private tenant key (never `obs.CompanyIDFrom`); `Detach` MUST strip cancellation and DB transaction handles while preserving `company_id`, `request_id`, and `user_id`; `SafeJoin` MUST block both lexical traversal and symlink escapes.
- **FR-002**: System MUST provide `internal/core/crypto` exporting `ErrCrossTenantSecret`, `ErrInvalidCiphertext`, `ErrDecryptFailed`, `ErrUnknownCompany`, `CompanyKeyStore`, `Options`, `SecretCipher`, `NewSecretCipher`, `GenerateKeyB64`, `Encrypt`, `Decrypt`, `RotateMasterKey`, `RewrapCompanyDEK`, `ReencryptPlatformSecret`, and `InvalidateCompanyCache` matching `spec_04 §4.1B` verbatim, preserving Java-compatible `v1:` and `v2:<companyID>:` AES-256-GCM wire formats, atomic `0600` key file creation, concurrent DEK creation safety, and zero-downtime KEK rotation.
- **FR-003**: System MUST provide `internal/core/eventbus` exporting `Option`, `WithLogger`, `WithMaxAsyncConcurrency`, `WithOnPanic`, `Bus`, `New`, `Drain`, `Subscribe[T]`, `Publish[T]`, and `PublishAsync[T]` matching `spec_04 §4.1C` verbatim, with lock-free handler invocation, panic recovery, `tenant.Detach(ctx)` async propagation, bounded concurrency (default 64), and graceful `Drain(ctx)`.
- **FR-004**: System MUST provide zero-Redis `internal/infra/memcache` exporting `CacheOptions`, `CacheStats`, `ShardedCache[K, V]` (`NewShardedCache`, `Get`, `Set`, `GetOrLoad`, `Delete`, `DeleteFunc`, `PurgeExpired`, `StartJanitor`, `Len`, `Stats`) and `LimiterOptions`, `Decision`, `SlidingWindowLimiter` (`NewSlidingWindowLimiter`, `Allow`, `AllowN`, `Peek`, `RecordFailure`, `Reset`, `PurgeExpired`) matching `spec_04 §4.1D` verbatim.
- **FR-005**: System MUST provide `internal/arch_test.go` mechanically enforcing all 6 architectural rules across `synapse-server/internal/...` and `go.mod`: (1) L0–L5 unidirectional downward layering, (2) L0 core purity & sub-DAG, (3) universal same-layer parallel module isolation across L1–L5 (`srcLayer >= 1 && targetLayer == srcLayer && srcTopPkg != targetTopPkg`), (4) `pgx`/`goose` driver isolation, (5) `os/exec` launcher isolation, and (6) zero Redis dependencies — plus negative unit tests verifying violation detection for every rule.

### Explicit Boundaries (Out of Scope)

- No L1 domain entities (`domain.Company`, `Agent`, `Task`, etc. — deferred to `SA-05`/`SA-06`).
- No business event structs (`CompanyStarted`, `TaskStatusChanged`, etc. — deferred to `SA-05`+).
- No Redis clients or distributed cache interfaces.
- No database tables, SQL, or `pgx` imports inside any `SA-04` package.

### Key Entities

- **Tenant Context & Paths (`internal/core/tenant`)**: Fail-Closed company identity carrier, detached lifecycle helper, org slug validator, and symlink-safe path joiner.
- **Envelope Cipher (`SecretCipher`, `CompanyKeyStore`)**: AES-256-GCM KEK/DEK envelope encryptor with multi-KEK fallback and DEK rewrap rotation.
- **Generic Event Bus (`Bus`)**: Type-safe synchronous/asynchronous in-process pub/sub with panic isolation and bounded concurrency.
- **In-Memory Cache & Limiter (`ShardedCache[K, V]`, `SlidingWindowLimiter`)**: 32-shard concurrent TTL/LRU/singleflight cache and O(1) weighted sliding-window rate limiter.
- **AST Architecture Guard (`internal/arch_test.go`)**: Automated compile/test-time import graph validator covering all 6 repository red lines.

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: 100% of Fail-Closed tenant context, `Detach`, 12-reserved-word slug, traversal, and real symlink escape tests pass under `-race`.
- **SC-002**: 100% of `SecretCipher` cross-tenant denial, tampered-prefix denial, 32-goroutine concurrent DEK creation, `0600` key-file permission, and K1->K2 KEK rotation + `RewrapCompanyDEK` + K1 removal tests pass under `-race`.
- **SC-003**: 100% of `eventbus` sync ordering, panic isolation, unsubscribe, detached async context, concurrency cap, and `Drain` tests pass under `-race`.
- **SC-004**: 100% of `ShardedCache` (TTL, `MaxEntries`, 32-goroutine singleflight `loader==1`, `DeleteFunc`) and `SlidingWindowLimiter` (5-failure lock, `Peek`, `Reset`, clock advance, `MaxKeys` cap) tests pass under `-race`.
- **SC-005**: `internal/arch_test.go` passes on the real repository and detects 100% of simulated rule violations across all 6 rules; all 4 new packages achieve ≥ 85% statement coverage under `-race`, and full L0 regression (`SA-01` + `SA-02` + `SA-03` + `SA-04`) is 100% green with zero Redis dependencies.

## Assumptions

- `SA-01` (`apperror`, `obs`), `SA-02` (`db`), and `SA-03` (`egress`, `httpclient`) are merged on `main` and green.
- `golang.org/x/sync` (`singleflight`) is already pinned in `synapse-server/go.mod`.
- Note on `tenant.Detach(ctx)` stripping DB transaction handles: because `internal/core/tenant` MUST NOT import `internal/infra/db` (per `arch_test.go` Rule 2), `Detach` builds a brand-new `context.Background()` and copies ONLY `companyCtxKey{}` and `obs.CopyMeta(ctx, out)` — which automatically and cleanly strips both parent cancellation AND any `db` transaction key without importing `internal/infra/db`!
