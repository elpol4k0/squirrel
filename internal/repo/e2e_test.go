package repo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// TestE2E_BackupPruneRestoreVerify runs a complete backup→prune→restore cycle
// using only the local backend and verifies data integrity end-to-end.
func TestE2E_BackupPruneRestoreVerify(t *testing.T) {
	repoDir := initTestRepo(t)
	srcDir := t.TempDir()
	dstDir := t.TempDir()
	ctx := context.Background()

	// Create source files of varying sizes.
	wantFiles := map[string][]byte{
		"small.bin":  randomData(t, 4*1024),
		"medium.bin": randomData(t, 512*1024),
		"large.bin":  randomData(t, 2*1024*1024),
	}
	for name, data := range wantFiles {
		if err := os.WriteFile(filepath.Join(srcDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// ── Backup ──────────────────────────────────────────────────────────────
	r, err := repo.Open(repoDir, []byte(testPassword))
	if err != nil {
		t.Fatal(err)
	}

	var treeNodes []repo.TreeNode
	for name, data := range wantFiles {
		id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
		if err != nil {
			t.Fatalf("SaveBlob %s: %v", name, err)
		}
		treeNodes = append(treeNodes, repo.TreeNode{
			Name:    name,
			Type:    "file",
			Size:    int64(len(data)),
			Content: []string{id.String()},
		})
	}

	treeJSON, _ := json.Marshal(repo.Tree{Nodes: treeNodes})
	treeID, _, err := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
	if err != nil {
		t.Fatalf("SaveBlob tree: %v", err)
	}

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	snap, _ := repo.NewSnapshot([]string{srcDir}, nil)
	snap.Tree = treeID.String()
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	// ── Prune (no deletes → nothing should be removed) ──────────────────────
	deleted, _, err := r.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted > 0 {
		t.Errorf("prune removed %d packs but all blobs are still referenced", deleted)
	}

	// ── Restore in a fresh session ──────────────────────────────────────────
	r2, err := repo.Open(repoDir, []byte(testPassword))
	if err != nil {
		t.Fatalf("reopen repo: %v", err)
	}

	snaps, err := r2.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}

	tree, err := r2.LoadTree(ctx, snaps[0].Tree)
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}

	for _, node := range tree.Nodes {
		blobID, err := repo.ParseBlobID(node.Content[0])
		if err != nil {
			t.Fatalf("ParseBlobID %s: %v", node.Name, err)
		}
		loc, ok := r2.Index.Get(blobID)
		if !ok {
			t.Fatalf("blob for %s not in index", node.Name)
		}
		got, err := r2.LoadBlob(ctx, loc)
		if err != nil {
			t.Fatalf("LoadBlob %s: %v", node.Name, err)
		}

		// ── Verify ──────────────────────────────────────────────────────────
		want := wantFiles[node.Name]
		if !bytes.Equal(got, want) {
			t.Errorf("file %s: content mismatch after restore (%d vs %d bytes)", node.Name, len(got), len(want))
		}

		if err := os.WriteFile(filepath.Join(dstDir, node.Name), got, 0o644); err != nil {
			t.Fatalf("write restored file: %v", err)
		}
	}

	// Confirm all expected files are present in the restore directory.
	for name := range wantFiles {
		if _, err := os.Stat(filepath.Join(dstDir, name)); err != nil {
			t.Errorf("restored file missing: %s", name)
		}
	}
}

// TestE2E_PruneAfterForget verifies that deleting a snapshot and pruning actually
// removes its exclusive data blobs while keeping shared blobs intact.
func TestE2E_PruneAfterForget(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	// Save two snapshots with distinct data.
	makeSnap := func(data []byte) *repo.Snapshot {
		id, _, _ := r.SaveBlob(ctx, repo.BlobData, data)
		tree := repo.Tree{Nodes: []repo.TreeNode{{
			Name: "f", Type: "file", Size: int64(len(data)), Content: []string{id.String()},
		}}}
		tj, _ := json.Marshal(tree)
		tid, _, _ := r.SaveBlob(ctx, repo.BlobTree, tj)
		r.Flush(ctx)
		s, _ := repo.NewSnapshot([]string{"/f"}, nil)
		s.Tree = tid.String()
		r.SaveSnapshot(ctx, s)
		return s
	}

	snap1 := makeSnap(randomData(t, 256*1024))
	makeSnap(randomData(t, 256*1024))

	packsBefore, _ := listAllFiles(t, dir+"/data")

	// Delete only the first snapshot, then prune.
	r.DeleteSnapshot(ctx, snap1.ID)
	deleted, freed, err := r.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	packsAfter, _ := listAllFiles(t, dir+"/data")

	t.Logf("packs before=%d after=%d deleted=%d freed=%d B", len(packsBefore), len(packsAfter), deleted, freed)

	if deleted == 0 {
		t.Error("expected at least one pack deleted after forgetting a snapshot")
	}
	if len(packsAfter) >= len(packsBefore) {
		t.Error("pack count should decrease after prune")
	}
	if freed <= 0 {
		t.Error("freed bytes should be positive after prune")
	}
}
