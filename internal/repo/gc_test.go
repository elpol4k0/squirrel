package repo_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func TestPrune_RemovesUnreferencedPacks(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	// Backup 1: save a blob, write snapshot
	data1 := randomData(t, 128*1024)
	id1, _, _ := r.SaveBlob(ctx, repo.BlobData, data1)

	tree1 := repo.Tree{Nodes: []repo.TreeNode{{Name: "f1", Type: "file", Size: int64(len(data1)), Content: []string{id1.String()}}}}
	treeJSON1, _ := marshalTreeForTest(tree1)
	treeID1, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON1)
	r.Flush(ctx)

	snap1, _ := repo.NewSnapshot([]string{"/f1"}, nil)
	snap1.Tree = treeID1.String()
	r.SaveSnapshot(ctx, snap1)

	// Backup 2: different data
	data2 := randomData(t, 128*1024)
	id2, _, _ := r.SaveBlob(ctx, repo.BlobData, data2)
	tree2 := repo.Tree{Nodes: []repo.TreeNode{{Name: "f2", Type: "file", Size: int64(len(data2)), Content: []string{id2.String()}}}}
	treeJSON2, _ := marshalTreeForTest(tree2)
	treeID2, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON2)
	r.Flush(ctx)

	snap2, _ := repo.NewSnapshot([]string{"/f2"}, nil)
	snap2.Tree = treeID2.String()
	r.SaveSnapshot(ctx, snap2)

	// Delete snapshot 1 → its blobs become unreferenced
	if err := r.DeleteSnapshot(ctx, snap1.ID); err != nil {
		t.Fatal(err)
	}

	packsBefore, _ := listAllFiles(t, dir+"/data")

	deleted, freed, err := r.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}

	packsAfter, _ := listAllFiles(t, dir+"/data")

	t.Logf("packs before=%d after=%d deleted=%d freed=%d", len(packsBefore), len(packsAfter), deleted, freed)

	if len(packsAfter) >= len(packsBefore) {
		t.Error("expected fewer packfiles after prune")
	}
}

func TestPrune_KeepsReferencedBlobs(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	data := randomData(t, 64*1024)
	id, _, _ := r.SaveBlob(ctx, repo.BlobData, data)

	tree := repo.Tree{Nodes: []repo.TreeNode{{Name: "f", Type: "file", Size: int64(len(data)), Content: []string{id.String()}}}}
	treeJSON, _ := marshalTreeForTest(tree)
	treeID, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
	r.Flush(ctx)

	snap, _ := repo.NewSnapshot([]string{"/f"}, nil)
	snap.Tree = treeID.String()
	r.SaveSnapshot(ctx, snap)

	// Prune without deleting any snapshot – nothing should be removed
	deleted, _, err := r.Prune(ctx)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if deleted > 0 {
		t.Errorf("prune removed %d packs but all blobs are referenced", deleted)
	}

	// Blob must still be loadable
	r2, _ := repo.Open(dir, []byte(testPassword))
	loc, ok := r2.Index.Get(id)
	if !ok {
		t.Fatal("referenced blob removed from index after prune")
	}
	if _, err := r2.LoadBlob(ctx, loc); err != nil {
		t.Fatalf("LoadBlob after prune: %v", err)
	}
}

func TestDeleteSnapshot(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))
	snap, _ := repo.NewSnapshot([]string{"/x"}, nil)
	snap.Tree = strings.Repeat("a", 64)
	r.SaveSnapshot(ctx, snap)

	if err := r.DeleteSnapshot(ctx, snap.ID); err != nil {
		t.Fatalf("DeleteSnapshot: %v", err)
	}

	if _, err := os.Stat(dir + "/snapshots/" + snap.ID); !os.IsNotExist(err) {
		t.Error("snapshot file still exists after DeleteSnapshot")
	}
}

func TestReferencedBlobs(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	data := randomData(t, 32*1024)
	id, _, _ := r.SaveBlob(ctx, repo.BlobData, data)

	tree := repo.Tree{Nodes: []repo.TreeNode{{Name: "f", Type: "file", Size: int64(len(data)), Content: []string{id.String()}}}}
	treeJSON, _ := marshalTreeForTest(tree)
	treeID, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON)
	r.Flush(ctx)

	snap, _ := repo.NewSnapshot([]string{"/f"}, nil)
	snap.Tree = treeID.String()
	r.SaveSnapshot(ctx, snap)

	refs, err := r.ReferencedBlobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !refs[id] {
		t.Error("data blob not in referenced set")
	}
	if !refs[treeID] {
		t.Error("tree blob not in referenced set")
	}
}
