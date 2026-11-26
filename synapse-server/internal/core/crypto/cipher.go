// Package crypto implements per-tenant AES-256-GCM envelope encryption
// wire-compatible with the Java SecretCipher format ("v1:" platform secrets
// and "v2:<companyID>:" tenant secrets with AAD binding) plus zero-downtime
// master KEK rotation.
package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Karlsk/synapse-os/internal/core/tenant"
)

const (
	prefixMaster   = "v1:"
	prefixTenant   = "v2:"
	keySize        = 32
	nonceSize      = 12
	defaultKeyFile = "./data/secret.key"
)

var (
	// ErrCrossTenantSecret indicates a tenant-bound ciphertext was decrypted
	// under a missing or mismatched tenant context.
	ErrCrossTenantSecret = errors.New("secret belongs to another company")
	// ErrInvalidCiphertext indicates stored data does not match v1:/v2: wire format.
	ErrInvalidCiphertext = errors.New("secret is not in a recognised format")
	// ErrDecryptFailed indicates AEAD decryption or key unwrapping failed.
	ErrDecryptFailed = errors.New("cannot decrypt secret (key mismatch or tampered)")
	// ErrUnknownCompany indicates the referenced company does not exist in CompanyKeyStore.
	ErrUnknownCompany = errors.New("unknown company")
)

// CompanyKeyStore is a domain-agnostic narrow port persisting each company's
// KEK-wrapped data encryption key (company.data_key_cipher).
type CompanyKeyStore interface {
	GetDataKeyCipher(ctx context.Context, companyID string) (cipher string, exists bool, err error)
	SaveDataKeyCipher(ctx context.Context, companyID, cipher string) error
}

// Options configures a SecretCipher instance.
type Options struct {
	MasterKeyB64    string
	PreviousKeysB64 []string
	KeyFile         string
	Store           CompanyKeyStore
}

// SecretCipher encrypts and decrypts platform secrets (v1:) and tenant secrets
// (v2:<companyID>:) using per-company 32-byte AES-256-GCM DEKs wrapped by a
// rotatable master KEK.
type SecretCipher struct {
	mu            sync.RWMutex
	masterAEAD    cipher.AEAD
	previousAEADs []cipher.AEAD
	store         CompanyKeyStore

	dekMu    sync.Mutex
	dekCache sync.Map // companyID (string) -> cipher.AEAD
}

// GenerateKeyB64 generates a cryptographically random 32-byte AES-256 key
// encoded in standard Base64.
func GenerateKeyB64() (string, error) {
	raw := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", fmt.Errorf("generate random key: %w", err)
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// NewSecretCipher initializes a SecretCipher from Options. When MasterKeyB64 is
// empty, it reads KeyFile (default ./data/secret.key) or atomically generates
// one with 0600 permissions.
func NewSecretCipher(opts Options) (*SecretCipher, error) {
	masterB64 := strings.TrimSpace(opts.MasterKeyB64)
	if masterB64 == "" {
		keyFile := strings.TrimSpace(opts.KeyFile)
		if keyFile == "" {
			keyFile = defaultKeyFile
		}
		var err error
		masterB64, err = loadOrCreateKeyFile(keyFile)
		if err != nil {
			return nil, err
		}
	}

	masterRaw, err := decodeKey32(masterB64)
	if err != nil {
		return nil, fmt.Errorf("invalid master key: %w", err)
	}
	masterAEAD, err := newGCM(masterRaw)
	if err != nil {
		return nil, err
	}

	prevAEADs := make([]cipher.AEAD, 0, len(opts.PreviousKeysB64))
	for i, prevB64 := range opts.PreviousKeysB64 {
		trimmed := strings.TrimSpace(prevB64)
		if trimmed == "" {
			continue
		}
		raw, err := decodeKey32(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid previous key at index %d: %w", i, err)
		}
		aead, err := newGCM(raw)
		if err != nil {
			return nil, err
		}
		prevAEADs = append(prevAEADs, aead)
	}

	return &SecretCipher{
		masterAEAD:    masterAEAD,
		previousAEADs: prevAEADs,
		store:         opts.Store,
	}, nil
}

// Encrypt seals plain using the active tenant's DEK ("v2:<companyID>:<b64>"
// with AAD=companyID) when Store is configured and ctx carries a tenant, or
// falls back to the platform master KEK ("v1:<b64>" with AAD=nil) otherwise.
func (c *SecretCipher) Encrypt(ctx context.Context, plain string) (string, error) {
	if c.store != nil {
		if companyID, ok := tenant.Optional(ctx); ok {
			dek, err := c.getOrCreateDEK(ctx, companyID)
			if err != nil {
				return "", err
			}
			sealed, err := sealAEAD(dek, plain, []byte(companyID))
			if err != nil {
				return "", err
			}
			return prefixTenant + companyID + ":" + sealed, nil
		}
	}

	c.mu.RLock()
	active := c.masterAEAD
	c.mu.RUnlock()

	sealed, err := sealAEAD(active, plain, nil)
	if err != nil {
		return "", err
	}
	return prefixMaster + sealed, nil
}

// Decrypt opens a "v1:" platform ciphertext or "v2:<companyID>:" tenant
// ciphertext. Tenant ciphertexts enforce strict tenant match and AAD binding
// and never fall back to the master key.
func (c *SecretCipher) Decrypt(ctx context.Context, stored string) (string, error) {
	if strings.HasPrefix(stored, prefixTenant) {
		rest := stored[len(prefixTenant):]
		sep := strings.IndexByte(rest, ':')
		if sep <= 0 {
			return "", ErrInvalidCiphertext
		}
		companyID := rest[:sep]
		payload := rest[sep+1:]
		if payload == "" {
			return "", ErrInvalidCiphertext
		}
		if c.store == nil {
			return "", ErrCrossTenantSecret
		}
		currentTenant, ok := tenant.Optional(ctx)
		if !ok || currentTenant != companyID {
			return "", ErrCrossTenantSecret
		}
		dek, err := c.getDEK(ctx, companyID)
		if err != nil {
			return "", err
		}
		plain, err := openAEAD(dek, payload, []byte(companyID))
		if err != nil {
			return "", ErrDecryptFailed
		}
		return plain, nil
	}

	if strings.HasPrefix(stored, prefixMaster) {
		payload := stored[len(prefixMaster):]
		if payload == "" {
			return "", ErrInvalidCiphertext
		}
		plain, _, err := c.openMaster(payload, nil)
		if err != nil {
			return "", err
		}
		return plain, nil
	}

	return "", ErrInvalidCiphertext
}

func (c *SecretCipher) getDEK(ctx context.Context, companyID string) (cipher.AEAD, error) {
	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}

	c.dekMu.Lock()
	defer c.dekMu.Unlock()

	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}

	stored, exists, err := c.store.GetDataKeyCipher(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCompany, companyID)
	}
	if stored == "" {
		return nil, ErrDecryptFailed
	}
	aead, err := c.unwrapDEK(companyID, stored)
	if err != nil {
		return nil, err
	}
	c.dekCache.Store(companyID, aead)
	return aead, nil
}

func (c *SecretCipher) getOrCreateDEK(ctx context.Context, companyID string) (cipher.AEAD, error) {
	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}

	c.dekMu.Lock()
	defer c.dekMu.Unlock()

	if v, ok := c.dekCache.Load(companyID); ok {
		return v.(cipher.AEAD), nil
	}

	stored, exists, err := c.store.GetDataKeyCipher(ctx, companyID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("%w: %s", ErrUnknownCompany, companyID)
	}
	if stored != "" {
		aead, err := c.unwrapDEK(companyID, stored)
		if err != nil {
			return nil, err
		}
		c.dekCache.Store(companyID, aead)
		return aead, nil
	}

	rawDEK := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, rawDEK); err != nil {
		return nil, fmt.Errorf("generate company DEK: %w", err)
	}
	b64DEK := base64.StdEncoding.EncodeToString(rawDEK)

	c.mu.RLock()
	active := c.masterAEAD
	c.mu.RUnlock()

	sealed, err := sealAEAD(active, b64DEK, []byte("company:"+companyID))
	if err != nil {
		return nil, err
	}
	if err := c.store.SaveDataKeyCipher(ctx, companyID, prefixMaster+sealed); err != nil {
		return nil, err
	}
	aead, err := newGCM(rawDEK)
	if err != nil {
		return nil, err
	}
	c.dekCache.Store(companyID, aead)
	return aead, nil
}

func (c *SecretCipher) unwrapDEK(companyID, stored string) (cipher.AEAD, error) {
	if !strings.HasPrefix(stored, prefixMaster) {
		return nil, ErrInvalidCiphertext
	}
	b64DEK, _, err := c.openMaster(stored[len(prefixMaster):], []byte("company:"+companyID))
	if err != nil {
		return nil, err
	}
	rawDEK, err := decodeKey32(b64DEK)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	return newGCM(rawDEK)
}

func loadOrCreateKeyFile(keyFile string) (string, error) {
	if data, err := os.ReadFile(keyFile); err == nil {
		trimmed := strings.TrimSpace(string(data))
		if trimmed != "" {
			return trimmed, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read key file %q: %w", keyFile, err)
	}

	keyB64, err := GenerateKeyB64()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(keyFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create key directory %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".secret.key.*")
	if err != nil {
		return "", fmt.Errorf("create temp key file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod temp key file: %w", err)
	}
	if _, err := tmp.WriteString(keyB64 + "\n"); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write temp key file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("sync temp key file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp key file: %w", err)
	}
	if err := os.Rename(tmpPath, keyFile); err != nil {
		return "", fmt.Errorf("install key file %q: %w", keyFile, err)
	}
	cleanup = false
	return keyB64, nil
}

func decodeKey32(b64 string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("decode base64 key: %w", err)
	}
	if len(raw) != keySize {
		return nil, fmt.Errorf("key length = %d, want %d bytes", len(raw), keySize)
	}
	return raw, nil
}

func newGCM(rawKey []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(rawKey)
	if err != nil {
		return nil, fmt.Errorf("init aes cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("init aes-gcm: %w", err)
	}
	return aead, nil
}

func sealAEAD(aead cipher.AEAD, plain string, aad []byte) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	combined := aead.Seal(nonce, nonce, []byte(plain), aad)
	return base64.StdEncoding.EncodeToString(combined), nil
}

func openAEAD(aead cipher.AEAD, sealedB64 string, aad []byte) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(sealedB64)
	if err != nil || len(raw) < nonceSize+aead.Overhead() {
		return "", ErrDecryptFailed
	}
	plain, err := aead.Open(nil, raw[:nonceSize], raw[nonceSize:], aad)
	if err != nil {
		return "", ErrDecryptFailed
	}
	return string(plain), nil
}
