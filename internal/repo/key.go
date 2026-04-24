package repo

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

// keyFile lives at keys/<id>. The master key is wrapped with a password-derived
// key so changing the password only requires rewriting this file.
type keyFile struct {
	Salt      []byte `json:"salt"`
	Encrypted []byte `json:"encrypted"`
}

func writeKeyFile(ctx context.Context, b backend.Backend, password []byte, masterKey crypto.MasterKey) error {
	salt, err := crypto.GenerateSalt()
	if err != nil {
		return err
	}
	wrappingKey := crypto.DeriveKey(password, salt)
	enc, err := crypto.Seal(wrappingKey, masterKey[:])
	if err != nil {
		return fmt.Errorf("seal master key: %w", err)
	}
	data, err := json.Marshal(keyFile{Salt: salt, Encrypted: enc})
	if err != nil {
		return fmt.Errorf("marshal key file: %w", err)
	}
	return b.Save(ctx, backend.Handle{Type: backend.TypeKey, Name: randomHex(16)}, wrapReader(data))
}

func unlockKey(ctx context.Context, b backend.Backend, password []byte) (crypto.MasterKey, error) {
	names, err := b.List(ctx, backend.TypeKey)
	if err != nil {
		return crypto.MasterKey{}, fmt.Errorf("list keys: %w", err)
	}
	if len(names) == 0 {
		return crypto.MasterKey{}, fmt.Errorf("repository has no key files")
	}
	for _, name := range names {
		rc, err := b.Load(ctx, backend.Handle{Type: backend.TypeKey, Name: name})
		if err != nil {
			continue
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		var kf keyFile
		if err := json.Unmarshal(raw, &kf); err != nil {
			continue
		}
		wrappingKey := crypto.DeriveKey(password, kf.Salt)
		masterKeyBytes, err := crypto.Open(wrappingKey, kf.Encrypted)
		if err != nil {
			continue // wrong password
		}
		if len(masterKeyBytes) != crypto.KeySize {
			continue
		}
		var mk crypto.MasterKey
		copy(mk[:], masterKeyBytes)
		return mk, nil
	}
	return crypto.MasterKey{}, fmt.Errorf("wrong password or corrupt key files")
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
