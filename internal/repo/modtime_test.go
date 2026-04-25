package repo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// TestTreeNode_ModTimeRoundTrip verifies that ModTime survives a JSON marshal/unmarshal cycle
// (i.e. the field is correctly written to and read from the encrypted snapshot).
func TestTreeNode_ModTimeRoundTrip(t *testing.T) {
	want := time.Date(2024, 6, 15, 10, 30, 0, 123456789, time.UTC)

	node := repo.TreeNode{
		Name:    "file.txt",
		Type:    "file",
		Size:    1024,
		Mode:    0o644,
		ModTime: want.UnixNano(),
		Content: []string{"aabbcc"},
	}

	data, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got repo.TreeNode
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ModTime != want.UnixNano() {
		t.Errorf("ModTime: got %d, want %d", got.ModTime, want.UnixNano())
	}
	if got.Mode != 0o644 {
		t.Errorf("Mode: got %o, want %o", got.Mode, 0o644)
	}
}

// TestTreeNode_ModTime_StoredInRepo saves a blob tree containing ModTime metadata
// to a real repo and verifies it can be loaded back correctly.
func TestTreeNode_ModTime_StoredInRepo(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, err := repo.Open(dir, []byte(testPassword))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	mtime := time.Date(2025, 1, 20, 8, 0, 0, 0, time.UTC).UnixNano()
	treeJSON, _ := json.Marshal(repo.Tree{Nodes: []repo.TreeNode{{
		Name:    "data.bin",
		Type:    "file",
		Size:    512,
		Mode:    0o600,
		ModTime: mtime,
		Content: []string{"deadbeef"},
	}}})

	treeID, _, err := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
	if err != nil {
		t.Fatalf("SaveBlob: %v", err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Reload from repo.
	r2, _ := repo.Open(dir, []byte(testPassword))
	tree, err := r2.LoadTree(ctx, treeID.String())
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if len(tree.Nodes) != 1 {
		t.Fatalf("nodes: got %d, want 1", len(tree.Nodes))
	}
	n := tree.Nodes[0]
	if n.ModTime != mtime {
		t.Errorf("ModTime: got %d, want %d", n.ModTime, mtime)
	}
	if n.Mode != 0o600 {
		t.Errorf("Mode: got %o, want %o", n.Mode, 0o600)
	}
}
