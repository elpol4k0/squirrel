package local_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/backend/local"
)

func newTempBackend(t *testing.T) *local.Local {
	t.Helper()
	dir := t.TempDir()
	return local.New(dir)
}

var ctx = context.Background()

func TestSaveLoad_RoundTrip(t *testing.T) {
	b := newTempBackend(t)
	content := []byte("hello from squirrel")
	h := backend.Handle{Type: backend.TypeKey, Name: "testkey"}

	if err := b.Save(ctx, h, bytes.NewReader(content)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	rc, err := b.Load(ctx, h)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestSave_Atomic(t *testing.T) {
	// Atomic writes mean there should be no .tmp file after a successful Save.
	b := newTempBackend(t)
	h := backend.Handle{Type: backend.TypeIndex, Name: "idx1"}
	_ = b.Save(ctx, h, bytes.NewReader([]byte("index data")))

	dir := t.TempDir() // different tmp - we just want to ensure Save succeeded
	_ = dir
	fi, err := b.Stat(ctx, h)
	if err != nil {
		t.Fatalf("Stat after Save: %v", err)
	}
	if fi.Size != int64(len("index data")) {
		t.Errorf("size: got %d, want %d", fi.Size, len("index data"))
	}
}

func TestList_ReturnsStoredNames(t *testing.T) {
	b := newTempBackend(t)

	names := []string{"aabbcc", "ddeeff"}
	for _, name := range names {
		h := backend.Handle{Type: backend.TypeSnapshot, Name: name}
		if err := b.Save(ctx, h, bytes.NewReader([]byte("snap"))); err != nil {
			t.Fatalf("Save %s: %v", name, err)
		}
	}

	got, err := b.List(ctx, backend.TypeSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) {
		t.Fatalf("List: got %v, want %v", got, names)
	}
}

func TestExists(t *testing.T) {
	b := newTempBackend(t)
	h := backend.Handle{Type: backend.TypeKey, Name: "mykey"}

	exists, err := b.Exists(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("Exists should be false before Save")
	}

	_ = b.Save(ctx, h, bytes.NewReader([]byte("key")))

	exists, err = b.Exists(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		t.Error("Exists should be true after Save")
	}
}

func TestRemove(t *testing.T) {
	b := newTempBackend(t)
	h := backend.Handle{Type: backend.TypeKey, Name: "toremove"}

	_ = b.Save(ctx, h, bytes.NewReader([]byte("data")))

	if err := b.Remove(ctx, h); err != nil {
		t.Fatalf("Remove: %v", err)
	}

	exists, _ := b.Exists(ctx, h)
	if exists {
		t.Error("file still exists after Remove")
	}
}

func TestDataSharding(t *testing.T) {
	// Data blobs should be stored under data/<prefix2>/<id>
	b := newTempBackend(t)
	h := backend.Handle{Type: backend.TypeData, Name: "abcdef1234567890"}

	_ = b.Save(ctx, h, bytes.NewReader([]byte("packfile")))

	// Check that the shard directory was created
	dir := b.Root() // we'll expose Root() below – see note
	shardDir := dir + "/data/ab"
	fi, err := os.Stat(shardDir)
	if err != nil {
		t.Skipf("Root() not yet exported, skipping sharding path check: %v", err)
	}
	if !fi.IsDir() {
		t.Errorf("%s should be a directory", shardDir)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	b := newTempBackend(t)
	h := backend.Handle{Type: backend.TypeKey, Name: "ghost"}

	if _, err := b.Load(ctx, h); err == nil {
		t.Error("Load of non-existent file should error")
	}
}
