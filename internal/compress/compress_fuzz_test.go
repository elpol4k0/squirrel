package compress_test

import (
	"bytes"
	"testing"

	"github.com/elpol4k0/squirrel/internal/compress"
)

// FuzzDecompress_NeverPanics verifies that garbage input never panics the decoder.
func FuzzDecompress_NeverPanics(f *testing.F) {
	f.Add(compress.Compress([]byte("hello world")))
	f.Add([]byte{})
	f.Add([]byte{0x28, 0xb5, 0x2f, 0xfd}) // valid zstd magic, truncated frame

	f.Fuzz(func(t *testing.T, data []byte) {
		compress.Decompress(data) //nolint:errcheck
	})
}

// FuzzCompressDecompress_RoundTrip verifies lossless compression for arbitrary input.
func FuzzCompressDecompress_RoundTrip(f *testing.F) {
	f.Add([]byte("the quick brown fox jumps over the lazy dog"))
	f.Add([]byte{})
	f.Add(bytes.Repeat([]byte{0xAB}, 8192))

	f.Fuzz(func(t *testing.T, data []byte) {
		compressed := compress.Compress(data)
		got, err := compress.Decompress(compressed)
		if err != nil {
			t.Fatalf("Decompress of Compress output: %v", err)
		}
		if !bytes.Equal(got, data) {
			t.Errorf("round-trip mismatch: got %d bytes, want %d", len(got), len(data))
		}
	})
}
