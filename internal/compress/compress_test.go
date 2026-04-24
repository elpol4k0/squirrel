package compress_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/elpol4k0/squirrel/internal/compress"
)

func TestRoundTrip(t *testing.T) {
	inputs := [][]byte{
		[]byte("hello, world"),
		[]byte(strings.Repeat("aaaa", 1024)), // highly compressible
		randomBytes(t, 4096),                 // less compressible
		{},                                   // empty
	}

	for _, in := range inputs {
		compressed := compress.Compress(in)
		got, err := compress.Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress error: %v (input len=%d)", err, len(in))
		}
		if !bytes.Equal(got, in) {
			t.Errorf("round-trip mismatch (input len=%d)", len(in))
		}
	}
}

func TestCompress_ReducesSize(t *testing.T) {
	// highly repetitive data should compress well
	data := bytes.Repeat([]byte("squirrel backup"), 1000)
	compressed := compress.Compress(data)
	if len(compressed) >= len(data) {
		t.Errorf("expected compression: got %d bytes from %d bytes", len(compressed), len(data))
	}
}

func TestDecompress_CorruptInput(t *testing.T) {
	if _, err := compress.Decompress([]byte("not zstd")); err == nil {
		t.Error("expected error for corrupt input, got nil")
	}
}

// randomBytes returns n pseudo-random bytes derived from the test name seed.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}
