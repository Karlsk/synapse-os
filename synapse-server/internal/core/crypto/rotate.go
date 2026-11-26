package crypto

import (
	"context"
	"crypto/cipher"
	"fmt"
	"strings"
)

func (c *SecretCipher) openMaster(sealedB64 string, aad []byte) (plain string, usedPrev bool, err error) {
	c.mu.RLock()
	active := c.masterAEAD
	prevs := c.previousAEADs
	c.mu.RUnlock()

	if plain, err = openAEAD(active, sealedB64, aad); err == nil {
		return plain, false, nil
	}
	for _, prev := range prevs {
		if plain, err = openAEAD(prev, sealedB64, aad); err == nil {
			return plain, true, nil
		}
	}
	return "", false, ErrDecryptFailed
}

// RotateMasterKey activates newMasterKeyB64 in-process and prepends the
// previous active KEK to the fallback list so existing DEKs and v1: secrets
// remain readable with zero downtime.
func (c *SecretCipher) RotateMasterKey(newMasterKeyB64 string) error {
	raw, err := decodeKey32(newMasterKeyB64)
	if err != nil {
		return err
	}
	newAEAD, err := newGCM(raw)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	prevs := make([]cipher.AEAD, 0, len(c.previousAEADs)+1)
	prevs = append(prevs, c.masterAEAD)
	prevs = append(prevs, c.previousAEADs...)
	c.previousAEADs = prevs
	c.masterAEAD = newAEAD
	return nil
}

// RewrapCompanyDEK idempotently re-wraps a company's existing 32-byte DEK under
// the currently active master KEK when it was sealed by a previous KEK.
// Existing v2:<companyID>: business ciphertexts remain untouched.
func (c *SecretCipher) RewrapCompanyDEK(ctx context.Context, companyID string) (bool, error) {
	if c.store == nil {
		return false, nil
	}
	c.dekMu.Lock()
	defer c.dekMu.Unlock()

	stored, exists, err := c.store.GetDataKeyCipher(ctx, companyID)
	if err != nil {
		return false, err
	}
	if !exists {
		return false, fmt.Errorf("%w: %s", ErrUnknownCompany, companyID)
	}
	if stored == "" {
		return false, nil
	}
	if !strings.HasPrefix(stored, prefixMaster) {
		return false, ErrInvalidCiphertext
	}

	aad := []byte("company:" + companyID)
	b64DEK, usedPrev, err := c.openMaster(stored[len(prefixMaster):], aad)
	if err != nil {
		return false, err
	}
	if !usedPrev {
		return false, nil
	}
	rawDEK, err := decodeKey32(b64DEK)
	if err != nil {
		return false, ErrDecryptFailed
	}

	c.mu.RLock()
	active := c.masterAEAD
	c.mu.RUnlock()

	resealed, err := sealAEAD(active, b64DEK, aad)
	if err != nil {
		return false, err
	}
	if err := c.store.SaveDataKeyCipher(ctx, companyID, prefixMaster+resealed); err != nil {
		return false, err
	}
	aead, err := newGCM(rawDEK)
	if err != nil {
		return false, err
	}
	c.dekCache.Store(companyID, aead)
	return true, nil
}

// ReencryptPlatformSecret idempotently re-encrypts a "v1:" platform ciphertext
// under the active master KEK when it was encrypted with a previous KEK.
func (c *SecretCipher) ReencryptPlatformSecret(_ context.Context, stored string) (string, bool, error) {
	if !strings.HasPrefix(stored, prefixMaster) {
		return "", false, ErrInvalidCiphertext
	}
	payload := stored[len(prefixMaster):]
	if payload == "" {
		return "", false, ErrInvalidCiphertext
	}
	plain, usedPrev, err := c.openMaster(payload, nil)
	if err != nil {
		return "", false, err
	}
	if !usedPrev {
		return stored, false, nil
	}

	c.mu.RLock()
	active := c.masterAEAD
	c.mu.RUnlock()

	resealed, err := sealAEAD(active, plain, nil)
	if err != nil {
		return "", false, err
	}
	return prefixMaster + resealed, true, nil
}

// InvalidateCompanyCache evicts the cached DEK for companyID, or clears all
// cached DEKs when companyID is empty.
func (c *SecretCipher) InvalidateCompanyCache(companyID string) {
	if companyID == "" {
		c.dekCache.Clear()
		return
	}
	c.dekCache.Delete(companyID)
}
