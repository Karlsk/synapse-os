# Phase 0 Research & Design Decisions: SA-04 L0 Core Security, Event Bus, MemCache & Arch Guard

## 1. Fail-Closed Tenant Context & Symlink-Safe `SafeJoin` (`internal/core/tenant`)

- **Decision**:
  - Use an unexported `type companyCtxKey struct{}` inside `package tenant`. `WithCompany` validates `companyID` (`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$` and `!strings.Contains(id, "..")`), writes BOTH `companyCtxKey{}` and `obs.WithCompanyID(ctx, companyID)`. `Require(ctx)` reads ONLY `companyCtxKey{}` so callers who only set `obs.WithCompanyID` cannot spoof tenant authorization.
  - `Detach(ctx)` creates a fresh `context.Background()`, copies `companyCtxKey{}` if present, and copies `obs` metadata via `obs.CopyMeta(ctx, out)` — cleanly stripping both parent cancellation AND any DB transaction handle without importing `internal/infra/db`.
  - `ValidateOrgSlug` matches Java `OrganizationService.java:L73-L82` verbatim: trims + lowercases, enforces `^[a-z][a-z0-9-]{1,30}[a-z0-9]$`, rejects the 12 reserved words with exact Chinese messages wrapped via `apperror.InvalidArg`.
  - `SafeJoin(baseDir, relParts...)`: rejects any absolute path element in `relParts` (`filepath.IsAbs(part)`), cleans and verifies `filepath.Rel(absBase, target)` does not start with `..`, and if `absBase` exists on disk, resolves `filepath.EvalSymlinks(absBase)` and `filepath.EvalSymlinks(nearestExistingAncestor(target))` to guarantee no symlink escapes `realBase`.
- **Rationale**: Defeats lexical traversal, Windows/Unix absolute element injection, and partial-path symlink escapes (where the leaf file does not exist yet but a parent directory is a symlink pointing outside the tenant root).

## 2. Java-Compatible AES-256-GCM Envelope Encryption & Zero-Downtime KEK Rotation (`internal/core/crypto`)

- **Decision**:
  - Wire formats match Java `SecretCipher.java:L36-L162` byte-for-byte:
    - Platform secret: `v1:<base64(nonce12 || ciphertext || tag16)>`, `AAD = nil`
    - Company DEK (`company.data_key_cipher`): `v1:<base64(nonce12 || sealed(base64(rawDEK32)) || tag16)>`, `AAD = "company:" + companyID`
    - Tenant secret: `v2:<companyID>:<base64(nonce12 || ciphertext || tag16)>`, `AAD = companyID`
  - Atomic `0600` key file creation: write to temp file in same directory (`os.CreateTemp(dir, ".secret.key.*")`), `f.Chmod(0o600)`, `f.WriteString(b64)`, `f.Sync()`, `f.Close()`, `os.Rename(tmp, keyFile)`.
  - Concurrent first-time DEK creation: double-checked locking using `dekCache sync.Map` fast-path + `dekMu sync.Mutex` slow-path, checking `dekCache` again under `dekMu` before reading/saving via `CompanyKeyStore`.
  - KEK rotation: `masterAEAD` + `previousAEADs` guarded by `mu sync.RWMutex`; `openMaster` tries `masterAEAD` then iterates `previousAEADs`; `RotateMasterKey` prepends old `masterAEAD` into `previousAEADs`; `RewrapCompanyDEK` and `ReencryptPlatformSecret` re-seal with active `masterAEAD` only when `usedPrev == true` (idempotent).
- **Rationale**: Zero-downtime rotation without re-encrypting tens of thousands of tenant secret rows, while maintaining 100% backward compatibility with Java ciphertexts.

## 3. Generic Event Bus (`internal/core/eventbus`)

- **Decision**:
  - Use Go 1.22+ `reflect.TypeFor[T]()` as map key (`map[reflect.Type][]subEntry`), atomic monotonic `nextID` for safe `unsubscribe()`, snapshot slice under `sync.RWMutex` and execute handlers outside the lock (preventing nested `Publish`/`Subscribe` deadlocks).
  - `PublishAsync[T]` calls `tenant.Detach(ctx)` synchronously BEFORE spawning goroutines, bounds concurrent execution via buffered semaphore channel `sem chan struct{}` (default cap `64`), tracks active goroutines in `sync.WaitGroup`, and `Drain(ctx)` waits for `wg.Wait()` or `ctx.Done()`.
- **Rationale**: Guarantees panic isolation, zero deadlocks, bounded goroutine fan-out, and deterministic shutdown draining.

## 4. Zero-Redis Sharded Cache & Weighted Sliding-Window Limiter (`internal/infra/memcache`)

- **Decision**:
  - `ShardedCache[K, V]`: power-of-two shards (default 32), key-to-shard routing via `hash/maphash.Comparable(seed, key)` (Go 1.24+ stdlib generic comparable hashing — zero reflection, zero allocations!), per-shard `container/list` + `map[K]*list.Element` for O(1) LRU eviction when `maxPerShard > 0`, `singleflight.Group` keyed by `fmt.Sprintf("%T:%v", key, key)` (or comparable key string) in `GetOrLoad` with error non-caching, and `StartJanitor(ctx, interval)` ticking `PurgeExpired()` until `ctx.Done()`.
  - `SlidingWindowLimiter`: 32-shard `maphash.String(seed, key)`, O(1) 2-window weighted counter `{prevCount, currCount, windowStart, lastSeen}`, injectable `nowFn func() time.Time` (default `time.Now`) for deterministic clock-advance tests, `MaxKeys` default `100_000` (distributed across shards as `maxPerShard = max(1, ceil(MaxKeys/shards))`), evicting expired entries first and oldest `lastSeen` entry if still full.
- **Rationale**: Eliminates Redis completely for V1 while maintaining high concurrency under `-race` and strict memory bounds against random-IP flood attacks.

## 5. AST Architecture Guard (`internal/arch_test.go`)

- **Decision**:
  - Extract pure testable rule evaluator `checkImportRule(srcRelPkg, impPath string) error` and `checkGoMod(content string) []string` so BOTH `TestArchitectureGuard` (scanning real `synapse-server/internal/...` and `go.mod`) AND `TestArchRulesDetectViolations` (verifying negative violations: `agent->task`, `task->agent`, `storage->runtime`, `storage->platform`, `web->pgx`, `core->infra/db`, `platform->os/exec`, `go-redis`) run on every `go test ./...`.
- **Rationale**: Proves the guard itself is active and regression-proof against all 6 rules from Constitution v1.0.1 Principle II/IV/I.
