package repo_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// ensure os import is used (ReadDir)
var _ = os.ReadDir

const testPassword = "correct-horse-battery-staple"

func initTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := repo.InitWithPassword(dir, []byte(testPassword)); err != nil {
		t.Fatalf("InitWithPassword: %v", err)
	}
	return dir
}

func TestInit_CreatesLayout(t *testing.T) {
	dir := initTestRepo(t)

	for _, sub := range []string{"keys", "data", "index", "snapshots", "wal", "locks"} {
		fi, err := os.Stat(dir + "/" + sub)
		if err != nil {
			t.Errorf("missing subdir %s: %v", sub, err)
			continue
		}
		if !fi.IsDir() {
			t.Errorf("%s should be a directory", sub)
		}
	}

	if _, err := os.Stat(dir + "/config"); err != nil {
		t.Errorf("missing config file: %v", err)
	}
}

func TestInit_KeyFileExists(t *testing.T) {
	dir := initTestRepo(t)

	entries, err := os.ReadDir(dir + "/keys")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 key file, got %d", len(entries))
	}
}

func TestOpen_WrongPassword(t *testing.T) {
	dir := initTestRepo(t)

	if _, err := repo.Open(dir, []byte("wrong password")); err == nil {
		t.Error("Open with wrong password should fail")
	}
}

func TestOpen_CorrectPassword(t *testing.T) {
	dir := initTestRepo(t)

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r == nil {
		t.Error("Open returned nil Repo")
	}
}

func TestSaveBlob_Dedup(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}

	data := randomData(t, 64*1024)

	id1, uploaded1, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatalf("first SaveBlob: %v", err)
	}
	if !uploaded1 {
		t.Error("first save should report uploaded=true")
	}

	id2, uploaded2, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatalf("second SaveBlob: %v", err)
	}
	if uploaded2 {
		t.Error("second save of same blob should report uploaded=false (dedup)")
	}
	if id1 != id2 {
		t.Error("same content should produce same BlobID")
	}
}

func TestFlush_WritesPackAndIndex(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		r.SaveBlob(ctx, repo.BlobData, randomData(t, 32*1024))
	}

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// After flush, data and index directories should be non-empty.
	dataEntries, _ := listAllFiles(t, dir+"/data")
	if len(dataEntries) == 0 {
		t.Error("no packfiles written after Flush")
	}

	indexEntries, _ := os.ReadDir(dir + "/index")
	if len(indexEntries) == 0 {
		t.Error("no index file written after Flush")
	}
}

func TestIndex_PersistsAcrossOpen(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	// Session 1: save a blob + flush
	r1, _ := repo.Open(dir, []byte(testPassword))
	data := randomData(t, 64*1024)
	id, _, _ := r1.SaveBlob(ctx, repo.BlobData, data)
	r1.Flush(ctx)

	// Session 2: open repo again – index should be loaded from disk
	r2, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}

	if !r2.Index.Has(id) {
		t.Error("index not loaded from disk: blob not found in second session")
	}

	// Saving the same blob again should be a dedup hit
	_, uploaded, err := r2.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatal(err)
	}
	if uploaded {
		t.Error("expected dedup hit in second session, but blob was re-uploaded")
	}
}

func TestSaveSnapshot(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	snap, _ := repo.NewSnapshot([]string{"/tmp/test.txt"}, []string{"test"})
	snap.Tree = strings.Repeat("a", 64)

	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}
	if snap.ID == "" {
		t.Error("snapshot ID should be set after Save")
	}

	entries, _ := os.ReadDir(dir + "/snapshots")
	if len(entries) == 0 {
		t.Error("no snapshot file written")
	}
}

// ─── helpers ──────────────────────────────────────────────────────────────

func randomData(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		t.Fatal(err)
	}
	return b
}

func listAllFiles(t *testing.T, dir string) ([]string, error) {
	t.Helper()
	var files []string
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() {
			sub, _ := listAllFiles(t, dir+"/"+e.Name())
			files = append(files, sub...)
		} else {
			files = append(files, dir+"/"+e.Name())
		}
	}
	return files, nil
}

// ensure bytes import is used
var _ = bytes.Equal
