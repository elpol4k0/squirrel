package config

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// resolveAge decrypts an age-encrypted file and returns its trimmed plaintext content.
// The identity (private key) is read from AGE_IDENTITY_FILE or ~/.config/age/keys.txt.
// Both armored (PEM-like) and binary age formats are supported.
func resolveAge(path string) (string, error) {
	identityPath := os.Getenv("AGE_IDENTITY_FILE")
	if identityPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home dir: %w", err)
		}
		identityPath = filepath.Join(home, ".config", "age", "keys.txt")
	}

	identFile, err := os.Open(identityPath)
	if err != nil {
		return "", fmt.Errorf("open age identity file %s: %w (set AGE_IDENTITY_FILE to override)", identityPath, err)
	}
	defer identFile.Close()

	identities, err := age.ParseIdentities(identFile)
	if err != nil {
		return "", fmt.Errorf("parse age identities from %s: %w", identityPath, err)
	}

	ciphertext, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read age-encrypted file %s: %w", path, err)
	}

	// Try PEM-armored format first, then fall back to binary.
	plain, err := tryDecryptAge(ciphertext, identities, true)
	if err != nil {
		plain, err = tryDecryptAge(ciphertext, identities, false)
		if err != nil {
			return "", fmt.Errorf("decrypt age file %s: %w", path, err)
		}
	}

	return strings.TrimRight(string(plain), "\r\n"), nil
}

func tryDecryptAge(ciphertext []byte, identities []age.Identity, armored bool) ([]byte, error) {
	var src io.Reader = bytes.NewReader(ciphertext)
	if armored {
		src = armor.NewReader(src)
	}
	r, err := age.Decrypt(src, identities...)
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}
