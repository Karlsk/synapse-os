package crypto

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

type memCompanyKeyStore struct {
	mu        sync.Mutex
	keys      map[string]string
	saveCount atomic.Int32
}

func newMemCompanyKeyStore(companies ...string) *memCompanyKeyStore {
	m := &memCompanyKeyStore{keys: make(map[string]string)}
	for _, c := range companies {
		m.keys[c] = ""
	}
	return m
}

func (s *memCompanyKeyStore) GetDataKeyCipher(_ context.Context, companyID string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.keys[companyID]
	return v, ok, nil
}

func (s *memCompanyKeyStore) SaveDataKeyCipher(_ context.Context, companyID, cipher string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.keys[companyID]; !ok {
		return ErrUnknownCompany
	}
	s.keys[companyID] = cipher
	s.saveCount.Add(1)
	return nil
}

func TestSecretCipherTenantIsolationConcurrentDEKAndKeyFile0600(t *testing.T) {
	t.Parallel()

	keyFile := filepath.Join(t.TempDir(), "nested", "secret.key")
	store := newMemCompanyKeyStore("acme", "beta")

	c, err := NewSecretCipher(Options{
		KeyFile: keyFile,
		Store:   store,
	})
	if err != nil {
		t.Fatalf("NewSecretCipher(auto keyFile) error = %v", err)
	}

	// Verify auto-generated secret.key has exact 0600 permissions
	info, err := os.Stat(keyFile)
	if err != nil {
		t.Fatalf("stat keyFile: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("secret.key permissions = %o, want 0600", perm)
	}

	acmeCtx, _ := tenant.WithCompany(context.Background(), "acme")
	betaCtx, _ := tenant.WithCompany(context.Background(), "beta")

	// 32 concurrent goroutines encrypting for acme first time must generate & save DEK only once
	const workers = 32
	var wg sync.WaitGroup
	ciphers := make([]string, workers)
	errs := make([]error, workers)
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ciphers[idx], errs[idx] = c.Encrypt(acmeCtx, "api-key-secret")
		}(i)
	}
	wg.Wait()

	for i := range workers {
		if errs[i] != nil {
			t.Fatalf("worker %d Encrypt error = %v", i, errs[i])
		}
		if !strings.HasPrefix(ciphers[i], "v2:acme:") {
			t.Fatalf("worker %d ciphertext %q missing v2:acme: prefix", i, ciphers[i])
		}
		plain, err := c.Decrypt(acmeCtx, ciphers[i])
		if err != nil || plain != "api-key-secret" {
			t.Fatalf("worker %d Decrypt = (%q, %v), want (api-key-secret, nil)", i, plain, err)
		}
	}
	if got := store.saveCount.Load(); got != 1 {
		t.Fatalf("expected exactly 1 DEK save across 32 concurrent workers, got %d", got)
	}
	storedDEK, _, _ := store.GetDataKeyCipher(context.Background(), "acme")
	if !strings.HasPrefix(storedDEK, "v1:") {
		t.Fatalf("stored company DEK %q must start with v1:", storedDEK)
	}

	// Cross-tenant decryption MUST fail with ErrCrossTenantSecret
	if _, err := c.Decrypt(betaCtx, ciphers[0]); !errors.Is(err, ErrCrossTenantSecret) {
		t.Fatalf("cross-tenant Decrypt = %v, want ErrCrossTenantSecret", err)
	}

	// Tampering prefix from v2:acme: to v2:beta: MUST fail with ErrDecryptFailed (AAD/DEK mismatch)
	tampered := "v2:beta:" + strings.TrimPrefix(ciphers[0], "v2:acme:")
	if _, err := c.Decrypt(betaCtx, tampered); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("tampered prefix Decrypt = %v, want ErrDecryptFailed", err)
	}

	// Unrecognised format rejected
	if _, err := c.Decrypt(acmeCtx, "plaintext-not-prefixed"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("invalid format Decrypt = %v, want ErrInvalidCiphertext", err)
	}

	// Unknown company fails cleanly
	unknownCtx, _ := tenant.WithCompany(context.Background(), "ghost")
	if _, err := c.Encrypt(unknownCtx, "secret"); !errors.Is(err, ErrUnknownCompany) {
		t.Fatalf("unknown company Encrypt = %v, want ErrUnknownCompany", err)
	}
}

func TestSecretCipherZeroDowntimeKEKRotation(t *testing.T) {
	t.Parallel()

	k1, err := GenerateKeyB64()
	if err != nil {
		t.Fatalf("GenerateKeyB64(k1): %v", err)
	}
	k2, err := GenerateKeyB64()
	if err != nil {
		t.Fatalf("GenerateKeyB64(k2): %v", err)
	}

	store := newMemCompanyKeyStore("acme")
	acmeCtx, _ := tenant.WithCompany(context.Background(), "acme")

	// Step 1: Encrypt tenant secret and platform secret under K1
	c1, err := NewSecretCipher(Options{MasterKeyB64: k1, Store: store})
	if err != nil {
		t.Fatalf("NewSecretCipher(k1): %v", err)
	}
	tenantCipher, err := c1.Encrypt(acmeCtx, "tenant-credential-xyz")
	if err != nil {
		t.Fatalf("Encrypt(tenant): %v", err)
	}
	platformCipher, err := c1.Encrypt(context.Background(), "platform-secret-abc")
	if err != nil {
		t.Fatalf("Encrypt(platform): %v", err)
	}
	if !strings.HasPrefix(platformCipher, "v1:") {
		t.Fatalf("platformCipher = %q, want v1: prefix", platformCipher)
	}

	// Step 2: Rebuild with MasterKey=K2, PreviousKeys=[K1]; both tenant and platform secrets still decrypt
	c2, err := NewSecretCipher(Options{
		MasterKeyB64:    k2,
		PreviousKeysB64: []string{k1},
		Store:           store,
	})
	if err != nil {
		t.Fatalf("NewSecretCipher(k2, prev=k1): %v", err)
	}
	if got, err := c2.Decrypt(acmeCtx, tenantCipher); err != nil || got != "tenant-credential-xyz" {
		t.Fatalf("Decrypt(tenant under K2+prev K1) = (%q, %v)", got, err)
	}
	if got, err := c2.Decrypt(context.Background(), platformCipher); err != nil || got != "platform-secret-abc" {
		t.Fatalf("Decrypt(platform under K2+prev K1) = (%q, %v)", got, err)
	}

	// Step 3: RewrapCompanyDEK returns (true, nil) once and (false, nil) idempotently on second call
	rewrapped, err := c2.RewrapCompanyDEK(context.Background(), "acme")
	if err != nil || !rewrapped {
		t.Fatalf("first RewrapCompanyDEK = (%v, %v), want (true, nil)", rewrapped, err)
	}
	rewrappedAgain, err := c2.RewrapCompanyDEK(context.Background(), "acme")
	if err != nil || rewrappedAgain {
		t.Fatalf("second RewrapCompanyDEK = (%v, %v), want (false, nil)", rewrappedAgain, err)
	}

	// ReencryptPlatformSecret upgrades v1: platform secret idempotently
	newPlatformCipher, changed, err := c2.ReencryptPlatformSecret(context.Background(), platformCipher)
	if err != nil || !changed {
		t.Fatalf("ReencryptPlatformSecret = (%v, %v), want changed=true", changed, err)
	}
	_, changedAgain, err := c2.ReencryptPlatformSecret(context.Background(), newPlatformCipher)
	if err != nil || changedAgain {
		t.Fatalf("second ReencryptPlatformSecret = (%v, %v), want changed=false", changedAgain, err)
	}

	// Step 4: Rebuild with ONLY MasterKey=K2 (K1 removed) -> ORIGINAL tenantCipher still decrypts losslessly!
	c3, err := NewSecretCipher(Options{MasterKeyB64: k2, Store: store})
	if err != nil {
		t.Fatalf("NewSecretCipher(k2 only): %v", err)
	}
	if got, err := c3.Decrypt(acmeCtx, tenantCipher); err != nil || got != "tenant-credential-xyz" {
		t.Fatalf("Decrypt(original tenantCipher under K2 only) = (%q, %v)", got, err)
	}
	if got, err := c3.Decrypt(context.Background(), newPlatformCipher); err != nil || got != "platform-secret-abc" {
		t.Fatalf("Decrypt(reencrypted platformCipher under K2 only) = (%q, %v)", got, err)
	}

	// Also verify in-process RotateMasterKey + InvalidateCompanyCache
	k3, _ := GenerateKeyB64()
	if err := c3.RotateMasterKey(k3); err != nil {
		t.Fatalf("RotateMasterKey(k3): %v", err)
	}
	c3.InvalidateCompanyCache("acme")
	if got, err := c3.Decrypt(acmeCtx, tenantCipher); err != nil || got != "tenant-credential-xyz" {
		t.Fatalf("Decrypt after RotateMasterKey+Invalidate = (%q, %v)", got, err)
	}
	// Reloading existing DEK on Encrypt after clearing all cache entries
	c3.InvalidateCompanyCache("")
	reEncrypted, err := c3.Encrypt(acmeCtx, "after-clear")
	if err != nil {
		t.Fatalf("Encrypt after cache clear: %v", err)
	}
	if got, err := c3.Decrypt(acmeCtx, reEncrypted); err != nil || got != "after-clear" {
		t.Fatalf("Decrypt(after-clear) = (%q, %v)", got, err)
	}
}

func TestSecretCipherEdgeCasesAndKeyFileReload(t *testing.T) {
	t.Parallel()

	// Re-loading persisted secret.key file produces identical master key
	keyFile := filepath.Join(t.TempDir(), "secret.key")
	c1, err := NewSecretCipher(Options{KeyFile: keyFile})
	if err != nil {
		t.Fatalf("NewSecretCipher(create): %v", err)
	}
	c2, err := NewSecretCipher(Options{KeyFile: keyFile})
	if err != nil {
		t.Fatalf("NewSecretCipher(reload): %v", err)
	}
	ct, err := c1.Encrypt(context.Background(), "persisted-secret")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if got, err := c2.Decrypt(context.Background(), ct); err != nil || got != "persisted-secret" {
		t.Fatalf("reloaded key Decrypt = (%q, %v)", got, err)
	}

	// Invalid key material rejected
	if _, err := NewSecretCipher(Options{MasterKeyB64: "not-base64!!!"}); err == nil {
		t.Fatalf("expected error on invalid MasterKeyB64")
	}
	if _, err := NewSecretCipher(Options{MasterKeyB64: "c2hvcnQ="}); err == nil {
		t.Fatalf("expected error on short MasterKeyB64")
	}
	validKey, _ := GenerateKeyB64()
	if _, err := NewSecretCipher(Options{MasterKeyB64: validKey, PreviousKeysB64: []string{"", "bad!!!"}}); err == nil {
		t.Fatalf("expected error on invalid PreviousKeysB64")
	}
	if err := c1.RotateMasterKey("bad!!!"); err == nil {
		t.Fatalf("expected RotateMasterKey error on invalid key")
	}

	// Nil-store cipher rejects v2: ciphertexts and no-ops RewrapCompanyDEK
	acmeCtx, _ := tenant.WithCompany(context.Background(), "acme")
	if _, err := c1.Decrypt(acmeCtx, "v2:acme:AAAA"); !errors.Is(err, ErrCrossTenantSecret) {
		t.Fatalf("nil-store v2 Decrypt = %v, want ErrCrossTenantSecret", err)
	}
	if rewrapped, err := c1.RewrapCompanyDEK(context.Background(), "acme"); err != nil || rewrapped {
		t.Fatalf("nil-store RewrapCompanyDEK = (%v, %v), want (false, nil)", rewrapped, err)
	}

	// Malformed ciphertexts and wrong-key platform decrypt
	for _, bad := range []string{"", "v1:", "v2:", "v2:acme", "v2::abc", "v2:acme:"} {
		if _, err := c1.Decrypt(acmeCtx, bad); !errors.Is(err, ErrInvalidCiphertext) {
			t.Errorf("Decrypt(%q) = %v, want ErrInvalidCiphertext", bad, err)
		}
	}
	if _, err := c1.Decrypt(context.Background(), "v1:AAAA"); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("Decrypt(truncated v1) = %v, want ErrDecryptFailed", err)
	}
	if _, _, err := c1.ReencryptPlatformSecret(context.Background(), "not-v1"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("ReencryptPlatformSecret(invalid) = %v, want ErrInvalidCiphertext", err)
	}
	if _, _, err := c1.ReencryptPlatformSecret(context.Background(), "v1:"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("ReencryptPlatformSecret(empty v1) = %v, want ErrInvalidCiphertext", err)
	}
	if _, _, err := c1.ReencryptPlatformSecret(context.Background(), "v1:AAAA"); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("ReencryptPlatformSecret(corrupt v1) = %v, want ErrDecryptFailed", err)
	}

	// Store-backed RewrapCompanyDEK edge cases: unknown, empty DEK, corrupt DEK
	store := newMemCompanyKeyStore("empty", "corrupt")
	store.keys["corrupt"] = "bad-prefix"
	sc, err := NewSecretCipher(Options{MasterKeyB64: validKey, Store: store})
	if err != nil {
		t.Fatalf("NewSecretCipher(store): %v", err)
	}
	if _, err := sc.RewrapCompanyDEK(context.Background(), "missing"); !errors.Is(err, ErrUnknownCompany) {
		t.Fatalf("RewrapCompanyDEK(missing) = %v, want ErrUnknownCompany", err)
	}
	if ok, err := sc.RewrapCompanyDEK(context.Background(), "empty"); err != nil || ok {
		t.Fatalf("RewrapCompanyDEK(empty) = (%v, %v), want (false, nil)", ok, err)
	}
	if _, err := sc.RewrapCompanyDEK(context.Background(), "corrupt"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("RewrapCompanyDEK(corrupt) = %v, want ErrInvalidCiphertext", err)
	}
	corruptCtx, _ := tenant.WithCompany(context.Background(), "corrupt")
	if _, err := sc.Encrypt(corruptCtx, "x"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("Encrypt(corrupt stored DEK) = %v, want ErrInvalidCiphertext", err)
	}
	if _, err := sc.Decrypt(corruptCtx, "v2:corrupt:AAAA"); !errors.Is(err, ErrInvalidCiphertext) {
		t.Fatalf("Decrypt(corrupt stored DEK) = %v, want ErrInvalidCiphertext", err)
	}
	// Stored DEK decrypts under master key but contains non-32-byte payload
	badDEKPayload, err := sc.Encrypt(context.Background(), "short") // v1: with AAD=nil -> fails AAD check in unwrapDEK
	if err != nil {
		t.Fatalf("Encrypt(badDEKPayload): %v", err)
	}
	store.keys["corrupt"] = badDEKPayload
	if _, err := sc.RewrapCompanyDEK(context.Background(), "corrupt"); !errors.Is(err, ErrDecryptFailed) {
		t.Fatalf("RewrapCompanyDEK(wrong AAD DEK) = %v, want ErrDecryptFailed", err)
	}
	// Store error propagation
	errStore := &failingCompanyKeyStore{getErr: errors.New("db unavailable"), saveErr: errors.New("db readonly")}
	esc, err := NewSecretCipher(Options{MasterKeyB64: validKey, Store: errStore})
	if err != nil {
		t.Fatalf("NewSecretCipher(errStore): %v", err)
	}
	if _, err := esc.Encrypt(acmeCtx, "x"); err == nil {
		t.Fatalf("expected Encrypt to propagate GetDataKeyCipher error")
	}
	if _, err := esc.Decrypt(acmeCtx, "v2:acme:AAAA"); err == nil {
		t.Fatalf("expected Decrypt to propagate GetDataKeyCipher error")
	}
	if _, err := esc.RewrapCompanyDEK(context.Background(), "acme"); err == nil {
		t.Fatalf("expected RewrapCompanyDEK to propagate GetDataKeyCipher error")
	}
	errStore.getErr = nil
	if _, err := esc.Encrypt(acmeCtx, "x"); err == nil {
		t.Fatalf("expected Encrypt to propagate SaveDataKeyCipher error")
	}
	// Unknown company Decrypt returns ErrUnknownCompany
	ghostCtx, _ := tenant.WithCompany(context.Background(), "ghost")
	if _, err := sc.Decrypt(ghostCtx, "v2:ghost:AAAA"); !errors.Is(err, ErrUnknownCompany) {
		t.Fatalf("Decrypt(unknown company) = %v, want ErrUnknownCompany", err)
	}
}

type failingCompanyKeyStore struct {
	getErr  error
	saveErr error
}

func (f *failingCompanyKeyStore) GetDataKeyCipher(_ context.Context, _ string) (string, bool, error) {
	if f.getErr != nil {
		return "", false, f.getErr
	}
	return "", true, nil
}

func (f *failingCompanyKeyStore) SaveDataKeyCipher(_ context.Context, _, _ string) error {
	return f.saveErr
}
