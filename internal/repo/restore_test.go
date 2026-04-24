package repo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func marshalTreeForTest(t repo.Tree) ([]byte, error) {
	return json.Marshal(t)
}

func TestLoadBlob_RoundTrip(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	data := randomData(t, 128*1024)
	id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	loc, ok := r.Index.Get(id)
	if !ok {
		t.Fatal("blob not in index after flush")
	}

	got, err := r.LoadBlob(ctx, loc)
	if err != nil {
		t.Fatalf("LoadBlob: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Error("LoadBlob returned different data")
	}
}

func TestListSnapshots(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	for i := 0; i < 3; i++ {
		snap, _ := repo.NewSnapshot([]string{"/tmp/file"}, nil)
		snap.Tree = strings.Repeat("a", 64)
		r.SaveSnapshot(ctx, snap)
	}

	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 3 {
		t.Errorf("expected 3 snapshots, got %d", len(snaps))
	}
}

func TestFindSnapshot_ByPrefix(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))
	snap, _ := repo.NewSnapshot([]string{"/etc/hosts"}, nil)
	snap.Tree = strings.Repeat("b", 64)
	r.SaveSnapshot(ctx, snap)

	found, err := r.FindSnapshot(ctx, snap.ID[:8])
	if err != nil {
		t.Fatalf("FindSnapshot: %v", err)
	}
	if found.ID != snap.ID {
		t.Errorf("wrong snapshot: got %s, want %s", found.ID, snap.ID)
	}
}

func TestFindSnapshot_NotFound(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))
	if _, err := r.FindSnapshot(ctx, "deadbeef"); err == nil {
		t.Error("expected error for unknown snapshot ID")
	}
}

func TestRestoreFile_RoundTrip(t *testing.T) {
	dir := initTestRepo(t)
	ctx := context.Background()

	r, _ := repo.Open(dir, []byte(testPassword))

	// simulate a small file split into multiple chunks
	fileData := randomData(t, 3*1024*1024)
	chunkSize := 512 * 1024
	var contentIDs []string
	for i := 0; i < len(fileData); i += chunkSize {
		end := i + chunkSize
		if end > len(fileData) {
			end = len(fileData)
		}
		id, _, err := r.SaveBlob(ctx, repo.BlobData, fileData[i:end])
		if err != nil {
			t.Fatal(err)
		}
		contentIDs = append(contentIDs, id.String())
	}

	tree := repo.Tree{
		Nodes: []repo.TreeNode{{
			Name:    "testfile.bin",
			Type:    "file",
			Size:    int64(len(fileData)),
			Content: contentIDs,
		}},
	}
	treeJSON, _ := marshalTreeForTest(tree)
	treeID, _, _ := r.SaveBlob(ctx, repo.BlobTree, treeJSON)

	if err := r.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	// load tree back and reassemble
	loadedTree, err := r.LoadTree(ctx, treeID.String())
	if err != nil {
		t.Fatalf("LoadTree: %v", err)
	}
	if len(loadedTree.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(loadedTree.Nodes))
	}

	var reassembled []byte
	for _, blobIDHex := range loadedTree.Nodes[0].Content {
		blobID, _ := repo.ParseBlobID(blobIDHex)
		loc, ok := r.Index.Get(blobID)
		if !ok {
			t.Fatalf("blob %s not in index", blobIDHex[:12])
		}
		data, err := r.LoadBlob(ctx, loc)
		if err != nil {
			t.Fatalf("LoadBlob: %v", err)
		}
		reassembled = append(reassembled, data...)
	}

	if !bytes.Equal(reassembled, fileData) {
		t.Errorf("reassembled data differs (got %d bytes, want %d)", len(reassembled), len(fileData))
	}
}
