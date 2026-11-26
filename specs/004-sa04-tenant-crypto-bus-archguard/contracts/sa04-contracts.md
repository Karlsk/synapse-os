# Frozen Go Package Contracts: SA-04 (`tenant`, `crypto`, `eventbus`, `memcache`, `arch_test`)

Source of truth: [docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md](../../../docs/SuperAgent-Go/DDD/L0/spec_04_tenant_crypto_bus_archguard.md) `§4.1` & `§5.5`. All exported symbol names, parameter lists, and return types MUST match verbatim.

## 1. `internal/core/tenant`
- `const DefaultSentinelID = "superagent"`
- `var ErrMissingTenant, ErrInvalidCompany, ErrPathEscape`
- `func WithCompany(ctx context.Context, companyID string) (context.Context, error)`
- `func Require(ctx context.Context) (string, error)`
- `func Optional(ctx context.Context) (string, bool)`
- `func Detach(ctx context.Context) context.Context`
- `func ValidateCompanyID(id string) error`
- `func ValidateOrgSlug(raw string) (string, error)`
- `func Path(ctx context.Context, root string) (string, error)`
- `func PathFor(root, companyID string) (string, error)`
- `func SafeJoin(baseDir string, relParts ...string) (string, error)`

## 2. `internal/core/crypto`
- `var ErrCrossTenantSecret, ErrInvalidCiphertext, ErrDecryptFailed, ErrUnknownCompany`
- `type CompanyKeyStore interface { GetDataKeyCipher(ctx, companyID) (string, bool, error); SaveDataKeyCipher(ctx, companyID, cipher) error }`
- `type Options struct { MasterKeyB64 string; PreviousKeysB64 []string; KeyFile string; Store CompanyKeyStore }`
- `type SecretCipher struct`
- `func NewSecretCipher(opts Options) (*SecretCipher, error)`
- `func GenerateKeyB64() (string, error)`
- `func (c *SecretCipher) Encrypt(ctx context.Context, plain string) (string, error)`
- `func (c *SecretCipher) Decrypt(ctx context.Context, stored string) (string, error)`
- `func (c *SecretCipher) RotateMasterKey(newMasterKeyB64 string) error`
- `func (c *SecretCipher) RewrapCompanyDEK(ctx context.Context, companyID string) (bool, error)`
- `func (c *SecretCipher) ReencryptPlatformSecret(ctx context.Context, stored string) (string, bool, error)`
- `func (c *SecretCipher) InvalidateCompanyCache(companyID string)`

## 3. `internal/core/eventbus`
- `type Option func(*Bus)`
- `func WithLogger(l *slog.Logger) Option`
- `func WithMaxAsyncConcurrency(n int) Option`
- `func WithOnPanic(fn func(eventName string, recovered any, stack []byte)) Option`
- `type Bus struct`
- `func New(opts ...Option) *Bus`
- `func (b *Bus) Drain(ctx context.Context) error`
- `func Subscribe[T any](b *Bus, fn func(ctx context.Context, ev T)) (unsubscribe func())`
- `func Publish[T any](ctx context.Context, b *Bus, ev T)`
- `func PublishAsync[T any](ctx context.Context, b *Bus, ev T)`

## 4. `internal/infra/memcache`
- `type CacheOptions struct`, `type CacheStats struct`, `type ShardedCache[K comparable, V any] struct`
  - `NewShardedCache`, `Get`, `Set`, `GetOrLoad`, `Delete`, `DeleteFunc`, `PurgeExpired`, `StartJanitor`, `Len`, `Stats`
- `type LimiterOptions struct`, `type Decision struct`, `type SlidingWindowLimiter struct`
  - `NewSlidingWindowLimiter`, `Allow`, `AllowN`, `Peek`, `RecordFailure`, `Reset`, `PurgeExpired`

## 5. `internal/arch_test.go`
- `package internal_test`: positive full-repo AST guard (`TestArchitectureGuard`) + negative violation tests (`TestArchRulesDetectViolations`).
