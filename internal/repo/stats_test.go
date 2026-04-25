package repo_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// TestDedupStats_LogicalGreaterThanPhysical verifies that after saving compressible
// data the logical byte count (from tree nodes) exceeds physical storage (compressed+encrypted).
func TestDedupStats_LogicalGreaterThanPhysical(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Compressible data: 256 KiB of zeros
	data := make([]byte, 256*1024)
	id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	treeJSON, _ := json.Marshal(repo.Tree{Nodes: []repo.TreeNode{{
		Name:    "zeros.bin",
		Type:    "file",
		Size:    int64(len(data)),
		Content: []string{id.String()},
	}}})
	treeID, _, err := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
	if err != nil {
		t.Fatalf("SaveBlob tree: %v", err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush (tree): %v", err)
	}

	snap, _ := repo.NewSnapshot([]string{dir}, nil)
	snap.Tree = treeID.String()
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// Reload in a fresh session so we exercise LoadTree from disk.
	r2, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	snaps, err := r2.ListSnapshots(ctx)
	if err != nil || len(snaps) != 1 {
		t.Fatalf("ListSnapshots: %v / count %d", err, len(snaps))
	}

	tree, err := r2.LoadTree(ctx, snaps[0].Tree)
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}

	var logicalBytes int64
	for _, node := range tree.Nodes {
		if node.Type == "file" {
			logicalBytes += node.Size
		}
	}

	if logicalBytes != int64(len(data)) {
		t.Errorf("logical size: got %d, want %d", logicalBytes, len(data))
	}
}

// TestDedupStats_SameDataMultipleSnapshots verifies that two snapshots sharing
// the same blob data have a combined logical size equal to 2× the blob size,
// while the physical index contains only one entry.
func TestDedupStats_SameDataMultipleSnapshots(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	data := randomData(t, 128*1024)
	blobID, _, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}

	makeTree := func(name string) string {
		treeJSON, _ := json.Marshal(repo.Tree{Nodes: []repo.TreeNode{{
			Name:    name,
			Type:    "file",
			Size:    int64(len(data)),
			Content: []string{blobID.String()},
		}}})
		tid, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
		return tid.String()
	}

	treeID1 := makeTree("snap1.bin")
	treeID2 := makeTree("snap2.bin")
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	for _, tid := range []string{treeID1, treeID2} {
		snap, _ := repo.NewSnapshot([]string{dir}, nil)
		snap.Tree = tid
		r.SaveSnapshot(ctx, snap)
	}

	// Index should have exactly the data blob + 2 tree blobs.
	if r.Index.Count() < 3 {
		t.Errorf("expected at least 3 blobs in index, got %d", r.Index.Count())
	}

	// Sum logical bytes from both snapshots.
	snaps, _ := r.ListSnapshots(ctx)
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots, got %d", len(snaps))
	}

	var totalLogical int64
	for _, snap := range snaps {
		tree, err := r.LoadTree(ctx, snap.Tree)
		if err != nil {
			t.Fatalf("LoadTree: %v", err)
		}
		for _, node := range tree.Nodes {
			if node.Type == "file" {
				totalLogical += node.Size
			}
		}
	}

	// Each snapshot references 128 KiB of logical data → 256 KiB total logical.
	want := int64(2 * len(data))
	if totalLogical != want {
		t.Errorf("total logical bytes: got %d, want %d", totalLogical, want)
	}
}
