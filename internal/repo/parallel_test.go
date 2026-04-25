package repo_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// TestParallelUpload_AllBlobsRecoverable saves several blobs across multiple auto-flushes
// and verifies every blob is correctly stored and loadable after Flush completes.
func TestParallelUpload_AllBlobsRecoverable(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	const n = 12
	ids := make([]repo.BlobID, n)
	datas := make([][]byte, n)
	for i := range n {
		datas[i] = randomData(t, 64*1024)
		id, _, err := r.SaveBlob(ctx, repo.BlobData, datas[i])
		if err != nil {
			t.Fatalf("SaveBlob %d: %v", i, err)
		}
		ids[i] = id
	}

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for i, id := range ids {
		loc, ok := r.Index.Get(id)
		if !ok {
			t.Errorf("blob %d missing from index", i)
			continue
		}
		got, err := r.LoadBlob(ctx, loc)
		if err != nil {
			t.Errorf("LoadBlob %d: %v", i, err)
			continue
		}
		if !bytes.Equal(got, datas[i]) {
			t.Errorf("blob %d data mismatch after parallel upload", i)
		}
	}
}

// TestParallelUpload_IndexPersistedAcrossSessions verifies that all blob locations
// written during a parallel-upload session survive a repo close/reopen.
func TestParallelUpload_IndexPersistedAcrossSessions(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	var savedID repo.BlobID
	want := randomData(t, 128*1024)

	// Session 1: save and flush.
	{
		r, _ := repo.Open(dir, []byte(testPassword))
		// Save extra blobs to make the index non-trivial.
		for range 5 {
			r.SaveBlob(ctx, repo.BlobData, randomData(t, 32*1024))
		}
		id, _, _ := r.SaveBlob(ctx, repo.BlobData, want)
		savedID = id
		if err := r.Flush(ctx); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}

	// Session 2: reopen and verify.
	r2, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	loc, ok := r2.Index.Get(savedID)
	if !ok {
		t.Fatal("blob missing from index in second session")
	}
	got, err := r2.LoadBlob(ctx, loc)
	if err != nil {
		t.Fatalf("LoadBlob in second session: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Error("data mismatch across sessions")
	}
}
