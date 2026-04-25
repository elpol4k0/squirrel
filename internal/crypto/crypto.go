package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

const (
	KeySize   = 32
	NonceSize = 12
	SaltSize  = 32
)

// argon2id params – OWASP recommended minimums
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
)

type MasterKey [KeySize]byte

func GenerateMasterKey() (MasterKey, error) {
	var key MasterKey
	if _, err := io.ReadFull(rand.Reader, key[:]); err != nil {
		return MasterKey{}, fmt.Errorf("generate master key: %w", err)
	}
	return key, nil
}

func DeriveKey(password []byte, salt []byte) MasterKey {
	raw := argon2.IDKey(password, salt, argonTime, argonMemory, argonThreads, KeySize)
	var key MasterKey
	copy(key[:], raw)
	return key
}

func GenerateSalt() ([]byte, error) {
	salt := make([]byte, SaltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	return salt, nil
}

// AES-256-GCM; output layout: nonce(12) || ciphertext || tag(16)
func Seal(key MasterKey, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, NonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

func Open(key MasterKey, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < NonceSize {
		return nil, fmt.Errorf("ciphertext too short")
	}
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	plaintext, err := gcm.Open(nil, ciphertext[:NonceSize], ciphertext[NonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}
