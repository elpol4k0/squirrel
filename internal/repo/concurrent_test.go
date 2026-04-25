package repo_test

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// concurrent SaveBlob must produce no duplicates; run with -race
func TestSaveBlob_ConcurrentNoDuplicates(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const goroutines = 8
	const blobsPerGoroutine = 10
	total := goroutines * blobsPerGoroutine

	datas := make([][]byte, total)
	for i := range datas {
		datas[i] = randomData(t, 32*1024)
	}

	var wg sync.WaitGroup
	errs := make([]error, goroutines)
	for g := range goroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range blobsPerGoroutine {
				if _, _, err := r.SaveBlob(ctx, repo.BlobData, datas[g*blobsPerGoroutine+i]); err != nil {
					errs[g] = err
					return
				}
			}
		}(g)
	}
	wg.Wait()

	for g, err := range errs {
		if err != nil {
			t.Fatalf("goroutine %d: %v", g, err)
		}
	}

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Every blob must be in the index and loadable.
	for i, data := range datas {
		id, _, _ := r.SaveBlob(ctx, repo.BlobData, data) // will dedup-hit
		loc, ok := r.Index.Get(id)
		if !ok {
			t.Errorf("blob %d missing from index after concurrent save", i)
			continue
		}
		got, err := r.LoadBlob(ctx, loc)
		if err != nil {
			t.Errorf("LoadBlob %d: %v", i, err)
			continue
		}
		if !bytes.Equal(got, data) {
			t.Errorf("blob %d data mismatch", i)
		}
	}
}

// same blob from many goroutines must land in the index exactly once
func TestSaveBlob_ConcurrentDedup(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	shared := randomData(t, 64*1024)

	var wg sync.WaitGroup
	uploadCount := make([]int32, 16)
	for g := range int32(16) {
		wg.Add(1)
		go func(g int32) {
			defer wg.Done()
			_, uploaded, err := r.SaveBlob(ctx, repo.BlobData, shared)
			if err != nil {
				t.Errorf("goroutine %d: %v", g, err)
				return
			}
			if uploaded {
				uploadCount[g] = 1
			}
		}(g)
	}
	wg.Wait()

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Exactly one goroutine should have uploaded; the others deduplicated.
	var total int32
	for _, c := range uploadCount {
		total += c
	}
	if total != 1 {
		t.Errorf("expected 1 upload for shared blob, got %d", total)
	}

	// Blob must be present and intact.
	id, _, _ := r.SaveBlob(ctx, repo.BlobData, shared)
	loc, ok := r.Index.Get(id)
	if !ok {
		t.Fatal("shared blob missing from index")
	}
	got, err := r.LoadBlob(ctx, loc)
	if err != nil {
		t.Fatalf("LoadBlob: %v", err)
	}
	if !bytes.Equal(got, shared) {
		t.Error("shared blob data mismatch")
	}
}
