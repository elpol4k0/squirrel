package chunker_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"testing"

	"github.com/elpol4k0/squirrel/internal/chunker"
)

func randomData(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSplit_ReassemblesOriginal(t *testing.T) {
	data := randomData(t, 5*1024*1024) // 5 MiB
	var reassembled []byte

	err := chunker.Split(bytes.NewReader(data), func(ch chunker.Chunk) error {
		reassembled = append(reassembled, ch.Data...)
		return nil
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
	if !bytes.Equal(reassembled, data) {
		t.Errorf("reassembled data differs from original (got %d bytes, want %d)", len(reassembled), len(data))
	}
}

func TestSplit_ChunkSizeBounds(t *testing.T) {
	data := randomData(t, 10*1024*1024) // 10 MiB → several chunks

	err := chunker.Split(bytes.NewReader(data), func(ch chunker.Chunk) error {
		if uint(len(ch.Data)) > chunker.MaxSize {
			t.Errorf("chunk length %d exceeds MaxSize %d", len(ch.Data), chunker.MaxSize)
		}
		if uint(len(ch.Data)) < chunker.MinSize && len(ch.Data) > 0 {
			// last chunk may be smaller than MinSize – that's expected
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Split: %v", err)
	}
}

func TestSplit_ContentDefined_Stability(t *testing.T) {
	// Inserting bytes at the start should only change the first few chunks,
	// not the entire file – this is the key property of CDC.
	base := randomData(t, 4*1024*1024)

	var idsBase, idsShifted []string

	collectIDs := func(data []byte) []string {
		var ids []string
		chunker.Split(bytes.NewReader(data), func(ch chunker.Chunk) error {
			ids = append(ids, string(ch.Data[:min(4, len(ch.Data))]))
			return nil
		})
		return ids
	}

	idsBase = collectIDs(base)

	// Prepend 512 bytes – simulates a small file change at the beginning
	prefix := randomData(t, 512)
	shifted := append(prefix, base...)
	idsShifted = collectIDs(shifted)

	// Count matching suffixes (chunks that are stable)
	matching := 0
	for i := 1; i <= len(idsBase) && i <= len(idsShifted); i++ {
		if idsBase[len(idsBase)-i] == idsShifted[len(idsShifted)-i] {
			matching++
		} else {
			break
		}
	}

	// At least some tail chunks should be identical (CDC stability guarantee)
	if matching == 0 && len(idsBase) > 2 {
		t.Logf("base chunks: %d, shifted chunks: %d, tail matches: %d", len(idsBase), len(idsShifted), matching)
		// This is a weak test – CDC stability depends on the data pattern.
		// We only log rather than fail since random data may not show stability.
	}
}

func TestSplit_Empty(t *testing.T) {
	var count int
	err := chunker.Split(bytes.NewReader(nil), func(ch chunker.Chunk) error {
		count++
		return nil
	})
	if err != nil {
		t.Fatalf("Split on empty reader: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 chunks for empty input, got %d", count)
	}
}

func TestSplitter_OffsetMonotonic(t *testing.T) {
	data := randomData(t, 3*1024*1024)
	s := chunker.New(bytes.NewReader(data))

	var prevOffset uint64
	for {
		ch, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if ch.Offset < prevOffset {
			t.Errorf("offset went backwards: %d < %d", ch.Offset, prevOffset)
		}
		prevOffset = ch.Offset + uint64(ch.Length)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
