package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

type Index struct {
	mu    sync.RWMutex
	blobs map[BlobID]PackBlobLocation
}

func NewIndex() *Index {
	return &Index{blobs: make(map[BlobID]PackBlobLocation)}
}

func (idx *Index) Has(id BlobID) bool {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	_, ok := idx.blobs[id]
	return ok
}

func (idx *Index) Get(id BlobID) (PackBlobLocation, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	loc, ok := idx.blobs[id]
	return loc, ok
}

func (idx *Index) Add(locs []PackBlobLocation) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	for _, loc := range locs {
		idx.blobs[loc.BlobID] = loc
	}
}

func (idx *Index) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.blobs)
}

type indexBlobJSON struct {
	ID     string `json:"id"`
	PackID string `json:"pack_id"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

func (idx *Index) Save(ctx context.Context, b backend.Backend, masterKey crypto.MasterKey) error {
	idx.mu.RLock()
	entries := make([]indexBlobJSON, 0, len(idx.blobs))
	for _, loc := range idx.blobs {
		entries = append(entries, indexBlobJSON{
			ID:     loc.BlobID.String(),
			PackID: loc.PackID,
			Offset: loc.Offset,
			Length: loc.Length,
		})
	}
	idx.mu.RUnlock()

	data, err := json.Marshal(entries)
	if err != nil {
		return fmt.Errorf("marshal index: %w", err)
	}
	enc, err := crypto.Seal(masterKey, data)
	if err != nil {
		return fmt.Errorf("seal index: %w", err)
	}
	return b.Save(ctx, backend.Handle{Type: backend.TypeIndex, Name: randomHex(16)}, bytes.NewReader(enc))
}

func (idx *Index) Load(ctx context.Context, b backend.Backend, masterKey crypto.MasterKey) error {
	names, err := b.List(ctx, backend.TypeIndex)
	if err != nil {
		return fmt.Errorf("list index: %w", err)
	}
	for _, name := range names {
		rc, err := b.Load(ctx, backend.Handle{Type: backend.TypeIndex, Name: name})
		if err != nil {
			return fmt.Errorf("load index %s: %w", name, err)
		}
		enc, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return fmt.Errorf("read index %s: %w", name, err)
		}
		plain, err := crypto.Open(masterKey, enc)
		if err != nil {
			return fmt.Errorf("decrypt index %s: %w", name, err)
		}
		var entries []indexBlobJSON
		if err := json.Unmarshal(plain, &entries); err != nil {
			return fmt.Errorf("unmarshal index %s: %w", name, err)
		}
		idx.mu.Lock()
		for _, e := range entries {
			id, err := ParseBlobID(e.ID)
			if err != nil {
				continue
			}
			idx.blobs[id] = PackBlobLocation{BlobID: id, PackID: e.PackID, Offset: e.Offset, Length: e.Length}
		}
		idx.mu.Unlock()
	}
	return nil
}
