package crypto_test

import (
	"bytes"
	"testing"

	"github.com/elpol4k0/squirrel/internal/crypto"
)

func TestGenerateMasterKey_IsRandom(t *testing.T) {
	k1, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	k2, err := crypto.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	if k1 == k2 {
		t.Error("two consecutive GenerateMasterKey calls returned identical keys")
	}
}

func TestDeriveKey_Deterministic(t *testing.T) {
	password := []byte("hunter2")
	salt := bytes.Repeat([]byte{0xab}, crypto.SaltSize)

	k1 := crypto.DeriveKey(password, salt)
	k2 := crypto.DeriveKey(password, salt)

	if k1 != k2 {
		t.Error("DeriveKey is not deterministic for the same inputs")
	}
}

func TestDeriveKey_DifferentPasswords(t *testing.T) {
	salt := bytes.Repeat([]byte{0x01}, crypto.SaltSize)

	k1 := crypto.DeriveKey([]byte("correct horse"), salt)
	k2 := crypto.DeriveKey([]byte("wrong password"), salt)

	if k1 == k2 {
		t.Error("different passwords produced the same derived key")
	}
}

func TestSealOpen_RoundTrip(t *testing.T) {
	key, _ := crypto.GenerateMasterKey()
	plaintext := []byte("the quick brown fox jumps over the lazy dog")

	ciphertext, err := crypto.Seal(key, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Error("Seal returned plaintext unchanged")
	}

	recovered, err := crypto.Open(key, ciphertext)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(recovered, plaintext) {
		t.Errorf("Open: got %q, want %q", recovered, plaintext)
	}
}

func TestSealOpen_WrongKey(t *testing.T) {
	key1, _ := crypto.GenerateMasterKey()
	key2, _ := crypto.GenerateMasterKey()

	ct, _ := crypto.Seal(key1, []byte("secret"))
	if _, err := crypto.Open(key2, ct); err == nil {
		t.Error("Open with wrong key should fail but didn't")
	}
}

func TestSealOpen_TamperedCiphertext(t *testing.T) {
	key, _ := crypto.GenerateMasterKey()
	ct, _ := crypto.Seal(key, []byte("important data"))

	// flip a byte in the ciphertext portion
	ct[len(ct)-1] ^= 0xFF

	if _, err := crypto.Open(key, ct); err == nil {
		t.Error("Open should detect ciphertext tampering but didn't")
	}
}

func TestGenerateSalt_Length(t *testing.T) {
	salt, err := crypto.GenerateSalt()
	if err != nil {
		t.Fatal(err)
	}
	if len(salt) != crypto.SaltSize {
		t.Errorf("salt length: got %d, want %d", len(salt), crypto.SaltSize)
	}
}

func TestGenerateSalt_IsRandom(t *testing.T) {
	s1, _ := crypto.GenerateSalt()
	s2, _ := crypto.GenerateSalt()
	if bytes.Equal(s1, s2) {
		t.Error("two consecutive salts are identical")
	}
}

func TestSeal_NonceIsRandom(t *testing.T) {
	key, _ := crypto.GenerateMasterKey()
	plain := []byte("same plaintext")

	ct1, _ := crypto.Seal(key, plain)
	ct2, _ := crypto.Seal(key, plain)

	// Different nonces → different ciphertexts even for same input
	if bytes.Equal(ct1, ct2) {
		t.Error("two Seal calls for the same plaintext produced identical output (nonce reuse?)")
	}
}
