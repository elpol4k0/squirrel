package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/compress"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

func (r *Repo) LoadBlob(ctx context.Context, loc PackBlobLocation) ([]byte, error) {
	rc, err := r.backend.Load(ctx, backend.Handle{Type: backend.TypeData, Name: loc.PackID})
	if err != nil {
		return nil, fmt.Errorf("open pack %s: %w", loc.PackID, err)
	}
	defer rc.Close()

	// Use seek when available (local backend returns *os.File)
	if rs, ok := rc.(io.ReadSeeker); ok {
		if _, err := rs.Seek(int64(loc.Offset), io.SeekStart); err != nil {
			return nil, fmt.Errorf("seek: %w", err)
		}
		enc := make([]byte, loc.Length)
		if _, err := io.ReadFull(rs, enc); err != nil {
			return nil, fmt.Errorf("read blob: %w", err)
		}
		return decryptBlob(r.masterKey, enc)
	}

	// fallback: read entire packfile (remote backends without seek)
	all, err := io.ReadAll(rc)
	if err != nil {
		return nil, fmt.Errorf("read pack: %w", err)
	}
	if loc.Offset+loc.Length > len(all) {
		return nil, fmt.Errorf("blob out of bounds in pack %s", loc.PackID)
	}
	return decryptBlob(r.masterKey, all[loc.Offset:loc.Offset+loc.Length])
}

func decryptBlob(key crypto.MasterKey, enc []byte) ([]byte, error) {
	compressed, err := crypto.Open(key, enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt blob: %w", err)
	}
	return compress.Decompress(compressed)
}

func (r *Repo) ListSnapshots(ctx context.Context) ([]*Snapshot, error) {
	names, err := r.backend.List(ctx, backend.TypeSnapshot)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	snaps := make([]*Snapshot, 0, len(names))
	for _, name := range names {
		snap, err := r.loadSnapshot(ctx, name)
		if err != nil {
			continue
		}
		snaps = append(snaps, snap)
	}
	sort.Slice(snaps, func(i, j int) bool {
		return snaps[i].Time.Before(snaps[j].Time)
	})
	return snaps, nil
}

func (r *Repo) FindSnapshot(ctx context.Context, prefix string) (*Snapshot, error) {
	names, err := r.backend.List(ctx, backend.TypeSnapshot)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	var matches []string
	for _, name := range names {
		if strings.HasPrefix(name, prefix) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no snapshot matches %q", prefix)
	case 1:
		return r.loadSnapshot(ctx, matches[0])
	default:
		return nil, fmt.Errorf("ambiguous snapshot prefix %q (%d matches)", prefix, len(matches))
	}
}

func (r *Repo) loadSnapshot(ctx context.Context, name string) (*Snapshot, error) {
	rc, err := r.backend.Load(ctx, backend.Handle{Type: backend.TypeSnapshot, Name: name})
	if err != nil {
		return nil, err
	}
	enc, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, err
	}
	plain, err := crypto.Open(r.masterKey, enc)
	if err != nil {
		return nil, fmt.Errorf("decrypt snapshot %s: %w", name, err)
	}
	var snap Snapshot
	if err := json.Unmarshal(plain, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot %s: %w", name, err)
	}
	snap.ID = name
	return &snap, nil
}

func (r *Repo) LoadBlobByID(ctx context.Context, hexID string) ([]byte, error) {
	id, err := ParseBlobID(hexID)
	if err != nil {
		return nil, err
	}
	loc, ok := r.Index.Get(id)
	if !ok {
		return nil, fmt.Errorf("blob %s not in index", hexID[:12])
	}
	return r.LoadBlob(ctx, loc)
}

func (r *Repo) SaveTree(ctx context.Context, tree *Tree) (string, error) {
	data, err := json.Marshal(tree)
	if err != nil {
		return "", fmt.Errorf("marshal tree: %w", err)
	}
	id, _, err := r.SaveBlob(ctx, BlobTree, data)
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

func (r *Repo) LoadTree(ctx context.Context, treeIDHex string) (*Tree, error) {
	treeID, err := ParseBlobID(treeIDHex)
	if err != nil {
		return nil, err
	}
	loc, ok := r.Index.Get(treeID)
	if !ok {
		return nil, fmt.Errorf("tree blob %s not found in index", treeIDHex[:12])
	}
	data, err := r.LoadBlob(ctx, loc)
	if err != nil {
		return nil, fmt.Errorf("load tree blob: %w", err)
	}
	var tree Tree
	if err := json.Unmarshal(data, &tree); err != nil {
		return nil, fmt.Errorf("unmarshal tree: %w", err)
	}
	return &tree, nil
}
