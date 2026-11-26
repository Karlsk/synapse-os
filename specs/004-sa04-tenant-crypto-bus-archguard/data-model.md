# Data Model: SA-04 L0 Core Security, Event Bus, MemCache & Arch Guard

> SA-04 has **zero database tables or SQL**. All types are stateless or in-memory runtime structures.

## 1. `internal/core/tenant`
- Constants & sentinels: `DefaultSentinelID = "superagent"`, `ErrMissingTenant`, `ErrInvalidCompany`, `ErrPathEscape`.
- Rules:
  - `companyID`: `^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$` and must not contain `".."`.
  - `orgSlug`: trimmed + lowercased, `^[a-z][a-z0-9-]{1,30}[a-z0-9]$`, rejects 12 reserved words (`admin`, `api`, `www`, `root`, `system`, `login`, `mcp`, `static`, `support`, `help`, `default`, `platform`).

## 2. `internal/core/crypto`
- Sentinels: `ErrCrossTenantSecret`, `ErrInvalidCiphertext`, `ErrDecryptFailed`, `ErrUnknownCompany`.
- Narrow store port: `CompanyKeyStore` (`GetDataKeyCipher`, `SaveDataKeyCipher`).
- `Options`: `MasterKeyB64`, `PreviousKeysB64`, `KeyFile` (default `./data/secret.key`), `Store`.
- Wire formats:
  - `v1:<base64(nonce12 || ct || tag16)>` (platform secret AAD=`nil`; company DEK AAD=`"company:"+companyID`)
  - `v2:<companyID>:<base64(nonce12 || ct || tag16)>` (tenant secret AAD=`companyID`)

## 3. `internal/core/eventbus`
- `Bus`: `mu sync.RWMutex`, `subs map[reflect.Type][]subEntry`, `sem chan struct{}` (cap default 64), `wg sync.WaitGroup`, `nextID atomic.Uint64`, `logger *slog.Logger`, `onPanic`.

## 4. `internal/infra/memcache`
- `CacheOptions`: `Shards` (default 32, rounded up to power of 2), `MaxEntries` (0 = unlimited), `CleanupInterval`.
- `CacheStats`: `Entries`, `Hits`, `Misses`, `Evictions`.
- `ShardedCache[K comparable, V any]`: striped shards with `container/list` LRU + `expiresAt`, `maphash.Seed`, `singleflight.Group`, atomic hit/miss/eviction counters.
- `LimiterOptions`: `Limit`, `Window`, `Shards` (default 32), `MaxKeys` (default `100_000`).
- `Decision`: `Allowed`, `Remaining`, `RetryAfter`, `ResetAt`.
- `SlidingWindowLimiter`: striped shards with O(1) `windowEntry{prevCount, currCount, windowStart, lastSeen}` + injectable `nowFn`.

## 5. `internal/arch_test.go`
- `layerOfTopPkg`: L0 (`core`, `infra`), L1 (`storage`, `runtime`), L2 (`platform`, `files`, `project`, `skills`), L3 (`agent`, `task`, `cost`, `docs`), L4 (`event`, `orchestrator`), L5 (`web`, `ui`).
- Enforces Rules 1–6 across all non-test `.go` files under `synapse-server/internal/` and `synapse-server/go.mod`.
