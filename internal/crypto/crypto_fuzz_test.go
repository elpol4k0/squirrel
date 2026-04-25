package crypto_test

import (
	"bytes"
	"testing"

	"github.com/elpol4k0/squirrel/internal/crypto"
)

// FuzzOpen_NeverPanics ensures garbage ciphertext never causes a panic.
func FuzzOpen_NeverPanics(f *testing.F) {
	key, _ := crypto.GenerateMasterKey()
	seed, _ := crypto.Seal(key, []byte("seed"))
	f.Add(seed)
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01, 0x02, 0x03})

	f.Fuzz(func(t *testing.T, data []byte) {
		var k crypto.MasterKey
		crypto.Open(k, data) //nolint:errcheck
	})
}

// FuzzSealOpen_RoundTrip verifies the seal→open identity for arbitrary plaintext.
func FuzzSealOpen_RoundTrip(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xFF}, 256))

	f.Fuzz(func(t *testing.T, plaintext []byte) {
		key, err := crypto.GenerateMasterKey()
		if err != nil {
			t.Skip()
		}
		ct, err := crypto.Seal(key, plaintext)
		if err != nil {
			t.Fatalf("Seal: %v", err)
		}
		got, err := crypto.Open(key, ct)
		if err != nil {
			t.Fatalf("Open after Seal: %v", err)
		}
		if !bytes.Equal(got, plaintext) {
			t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(plaintext))
		}
	})
}

// FuzzSeal_WrongKeyFails verifies a different key never decrypts successfully.
func FuzzSeal_WrongKeyFails(f *testing.F) {
	f.Add([]byte("secret data"))

	f.Fuzz(func(t *testing.T, plaintext []byte) {
		k1, _ := crypto.GenerateMasterKey()
		k2, _ := crypto.GenerateMasterKey()
		if k1 == k2 {
			t.Skip() // astronomically rare
		}
		ct, _ := crypto.Seal(k1, plaintext)
		if _, err := crypto.Open(k2, ct); err == nil {
			t.Error("Open with wrong key succeeded – authentication bypass")
		}
	})
}
