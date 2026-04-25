package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

const repackWasteThreshold = 0.10

func (r *Repo) ReferencedBlobs(ctx context.Context) (map[BlobID]bool, error) {
	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}
	refs := make(map[BlobID]bool)
	for _, snap := range snaps {
		if err := r.collectTreeRefs(ctx, snap.Tree, refs); err != nil {
			return nil, fmt.Errorf("snapshot %s: %w", snap.ID[:12], err)
		}
	}
	return refs, nil
}

func (r *Repo) collectTreeRefs(ctx context.Context, treeIDHex string, refs map[BlobID]bool) error {
	treeID, err := ParseBlobID(treeIDHex)
	if err != nil {
		return err
	}
	refs[treeID] = true

	tree, err := r.LoadTree(ctx, treeIDHex)
	if err != nil {
		return err
	}
	for _, node := range tree.Nodes {
		switch node.Type {
		case "file":
			for _, idHex := range node.Content {
				id, err := ParseBlobID(idHex)
				if err != nil {
					continue
				}
				refs[id] = true
			}
		case "dir":
			if err := r.collectTreeRefs(ctx, node.Subtree, refs); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *Repo) Prune(ctx context.Context) (deleted int, freed int64, err error) {
	refs, err := r.ReferencedBlobs(ctx)
	if err != nil {
		return 0, 0, err
	}

	packBlobs := make(map[string][]PackBlobLocation)
	r.Index.mu.RLock()
	for _, loc := range r.Index.blobs {
		packBlobs[loc.PackID] = append(packBlobs[loc.PackID], loc)
	}
	r.Index.mu.RUnlock()

	var deletedPacks []string
	for packID, locs := range packBlobs {
		refCount := 0
		for _, loc := range locs {
			if refs[loc.BlobID] {
				refCount++
			}
		}

		if refCount == 0 {
			fi, statErr := r.backend.Stat(ctx, backend.Handle{Type: backend.TypeData, Name: packID})
			if statErr == nil {
				freed += fi.Size
			}
			if err := r.backend.Remove(ctx, backend.Handle{Type: backend.TypeData, Name: packID}); err != nil {
				return deleted, freed, fmt.Errorf("remove pack %s: %w", packID[:12], err)
			}
			deletedPacks = append(deletedPacks, packID)
			deleted++
			continue
		}

		if refCount < len(locs) {
			newLocs, packFreed, rerr := r.repackMixed(ctx, packID, refs)
			if rerr != nil || len(newLocs) == 0 {
				continue
			}
			r.Index.mu.Lock()
			for _, loc := range locs {
				delete(r.Index.blobs, loc.BlobID)
			}
			for _, loc := range newLocs {
				r.Index.blobs[loc.BlobID] = loc
			}
			r.Index.mu.Unlock()
			freed += packFreed
			deleted++
		}
	}

	if len(deletedPacks) == 0 && deleted == 0 {
		return 0, 0, nil
	}

	if len(deletedPacks) > 0 {
		r.Index.mu.Lock()
		for _, packID := range deletedPacks {
			for id, loc := range r.Index.blobs {
				if loc.PackID == packID {
					delete(r.Index.blobs, id)
				}
			}
		}
		r.Index.mu.Unlock()
	}

	if err := r.rebuildIndex(ctx); err != nil {
		return deleted, freed, fmt.Errorf("rebuild index: %w", err)
	}
	return deleted, freed, nil
}

func (r *Repo) repackMixed(ctx context.Context, packID string, refs map[BlobID]bool) ([]PackBlobLocation, int64, error) {
	entries, err := readPackHeader(ctx, r.backend, r.masterKey, packID)
	if err != nil {
		return nil, 0, err
	}

	var totalBytes, wastedBytes int
	for _, e := range entries {
		id, _ := ParseBlobID(e.ID)
		totalBytes += e.Length
		if !refs[id] {
			wastedBytes += e.Length
		}
	}
	if totalBytes == 0 || float64(wastedBytes)/float64(totalBytes) <= repackWasteThreshold {
		return nil, 0, nil
	}

	rc, err := r.backend.Load(ctx, backend.Handle{Type: backend.TypeData, Name: packID})
	if err != nil {
		return nil, 0, err
	}
	packBytes, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		return nil, 0, err
	}

	packer := NewPacker(r.masterKey)
	for _, e := range entries {
		id, err := ParseBlobID(e.ID)
		if err != nil || !refs[id] {
			continue
		}
		if e.Offset+e.Length > len(packBytes) {
			return nil, 0, fmt.Errorf("blob out of bounds in pack %s", packID[:12])
		}
		enc := make([]byte, e.Length)
		copy(enc, packBytes[e.Offset:e.Offset+e.Length])
		packer.AddEncrypted(e.Type, id, enc, e.RawLength)
	}

	newPackID, newData, newLocs, err := packer.Finalize()
	if err != nil {
		return nil, 0, err
	}

	if err := r.backend.Save(ctx, backend.Handle{Type: backend.TypeData, Name: newPackID}, bytes.NewReader(newData)); err != nil {
		return nil, 0, fmt.Errorf("save repacked pack: %w", err)
	}

	fi, statErr := r.backend.Stat(ctx, backend.Handle{Type: backend.TypeData, Name: packID})
	if err := r.backend.Remove(ctx, backend.Handle{Type: backend.TypeData, Name: packID}); err != nil {
		return nil, 0, fmt.Errorf("remove old pack %s: %w", packID[:12], err)
	}

	var oldSize int64
	if statErr == nil {
		oldSize = fi.Size
	} else {
		oldSize = int64(len(packBytes))
	}
	freed := oldSize - int64(len(newData))

	return newLocs, freed, nil
}

// rebuildIndex replaces all index files with one consolidated file.
func (r *Repo) rebuildIndex(ctx context.Context) error {
	names, err := r.backend.List(ctx, backend.TypeIndex)
	if err != nil {
		return err
	}
	if err := r.Index.Save(ctx, r.backend, r.masterKey); err != nil {
		return err
	}
	for _, name := range names {
		r.backend.Remove(ctx, backend.Handle{Type: backend.TypeIndex, Name: name}) //nolint:errcheck
	}
	return nil
}

// caller must Prune afterwards to free the associated data blobs
func (r *Repo) DeleteSnapshot(ctx context.Context, id string) error {
	return r.backend.Remove(ctx, backend.Handle{Type: backend.TypeSnapshot, Name: id})
}

// reads pack headers directly without relying on the in-memory index (handles stale index after prune).
func readPackHeader(ctx context.Context, b backend.Backend, masterKey crypto.MasterKey, packID string) ([]blobEntry, error) {
	rc, err := b.Load(ctx, backend.Handle{Type: backend.TypeData, Name: packID})
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	rs, ok := rc.(io.ReadSeeker)
	if !ok {
		return nil, fmt.Errorf("backend does not support seeking")
	}

	if _, err := rs.Seek(-4, io.SeekEnd); err != nil {
		return nil, fmt.Errorf("seek to header length: %w", err)
	}
	var lenBuf [4]byte
	if _, err := io.ReadFull(rs, lenBuf[:]); err != nil {
		return nil, fmt.Errorf("read header length: %w", err)
	}
	hdrLen := int(lenBuf[0]) | int(lenBuf[1])<<8 | int(lenBuf[2])<<16 | int(lenBuf[3])<<24

	if _, err := rs.Seek(int64(-(4 + hdrLen)), io.SeekEnd); err != nil {
		return nil, fmt.Errorf("seek to header: %w", err)
	}
	encHdr := make([]byte, hdrLen)
	if _, err := io.ReadFull(rs, encHdr); err != nil {
		return nil, fmt.Errorf("read header: %w", err)
	}

	hdrJSON, err := crypto.Open(masterKey, encHdr)
	if err != nil {
		return nil, fmt.Errorf("decrypt header: %w", err)
	}
	var hdr packHeader
	if err := json.Unmarshal(hdrJSON, &hdr); err != nil {
		return nil, fmt.Errorf("unmarshal header: %w", err)
	}
	return hdr.Blobs, nil
}
