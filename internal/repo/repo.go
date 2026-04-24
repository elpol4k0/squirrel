package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/backend/azure"
	"github.com/elpol4k0/squirrel/internal/backend/gcs"
	"github.com/elpol4k0/squirrel/internal/backend/local"
	"github.com/elpol4k0/squirrel/internal/backend/s3"
	backendsftp "github.com/elpol4k0/squirrel/internal/backend/sftp"
	"github.com/elpol4k0/squirrel/internal/crypto"
)

const maxPackSize = 128 * 1024 * 1024

var repoSubdirs = []string{"keys", "data", "index", "snapshots", "wal", "locks"}

type repoConfig struct {
	Version         int    `json:"version"`
	ID              string `json:"id"`
	ChunkerPoly     uint64 `json:"chunker_polynomial"`
	CompressionAlgo string `json:"compression"`
}

type Repo struct {
	masterKey crypto.MasterKey
	Index     *Index
	backend   backend.Backend
	packer    *Packer
	// pending: blobs added to current packer but not yet in Index; needed for within-session dedup
	pending map[BlobID]struct{}
}

func openBackend(url string) (backend.Backend, error) {
	switch {
	case strings.HasPrefix(url, "s3:"):
		return s3.ParseURL(url)
	case strings.HasPrefix(url, "az:"):
		return azure.ParseURL(url)
	case strings.HasPrefix(url, "gs:"):
		return gcs.ParseURL(url)
	case strings.HasPrefix(url, "sftp://"):
		return backendsftp.ParseURL(url)
	default:
		return local.New(url), nil
	}
}

func Init(url string) error {
	password, err := readPassword("Enter new repository password: ")
	if err != nil {
		return err
	}
	confirm, err := readPassword("Confirm password: ")
	if err != nil {
		return err
	}
	if string(password) != string(confirm) {
		return fmt.Errorf("passwords do not match")
	}
	return InitWithPassword(url, password)
}

func InitWithPassword(url string, password []byte) error {
	ctx := context.Background()

	b, err := openBackend(url)
	if err != nil {
		return fmt.Errorf("open backend: %w", err)
	}

	// Local backend: create directory layout
	if lb, ok := b.(*local.Local); ok {
		if err := os.MkdirAll(lb.Root(), 0o700); err != nil {
			return fmt.Errorf("create repo dir: %w", err)
		}
		if err := lb.Setup(repoSubdirs); err != nil {
			return err
		}
	}

	if names, _ := b.List(ctx, backend.TypeKey); len(names) > 0 {
		return fmt.Errorf("repository at %s already initialised", url)
	}

	// master key is random so a password change only requires rewriting the key file
	masterKey, err := crypto.GenerateMasterKey()
	if err != nil {
		return err
	}
	if err := writeKeyFile(ctx, b, password, masterKey); err != nil {
		return err
	}

	cfg := repoConfig{
		Version:         1,
		ID:              randomHex(16),
		ChunkerPoly:     0x3DA3358B4DC173,
		CompressionAlgo: "zstd",
	}
	cfgJSON, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	enc, err := crypto.Seal(masterKey, cfgJSON)
	if err != nil {
		return err
	}

	// config is stored as a special file at the backend root
	if lb, ok := b.(*local.Local); ok {
		if err := os.WriteFile(lb.Root()+"/config", enc, 0o600); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	} else {
		if err := b.Save(ctx, backend.Handle{Type: "config", Name: "config"}, wrapReader(enc)); err != nil {
			return fmt.Errorf("write config: %w", err)
		}
	}

	slog.Info("repository initialised", "url", url)
	fmt.Printf("Repository initialised at %s\n", url)
	return nil
}

func Open(url string, password []byte) (*Repo, error) {
	ctx := context.Background()

	b, err := openBackend(url)
	if err != nil {
		return nil, fmt.Errorf("open backend: %w", err)
	}

	masterKey, err := unlockKey(ctx, b, password)
	if err != nil {
		return nil, fmt.Errorf("open repo: %w", err)
	}

	r := &Repo{
		masterKey: masterKey,
		Index:     NewIndex(),
		backend:   b,
		packer:    NewPacker(masterKey),
		pending:   make(map[BlobID]struct{}),
	}
	if err := r.Index.Load(ctx, b, masterKey); err != nil {
		return nil, fmt.Errorf("load index: %w", err)
	}
	return r, nil
}

func (r *Repo) SaveBlob(ctx context.Context, blobType BlobType, data []byte) (BlobID, bool, error) {
	id := computeID(data)
	if r.Index.Has(id) {
		return id, false, nil
	}
	if _, ok := r.pending[id]; ok {
		return id, false, nil
	}
	if _, err := r.packer.Add(blobType, data); err != nil {
		return BlobID{}, false, err
	}
	r.pending[id] = struct{}{}
	if r.packer.Size() >= maxPackSize {
		if err := r.flushPacker(ctx); err != nil {
			return BlobID{}, false, err
		}
	}
	return id, true, nil
}

func (r *Repo) Flush(ctx context.Context) error {
	if err := r.flushPacker(ctx); err != nil {
		return err
	}
	return r.Index.Save(ctx, r.backend, r.masterKey)
}

func (r *Repo) flushPacker(ctx context.Context) error {
	if r.packer.Len() == 0 {
		return nil
	}
	_, locs, err := r.packer.Flush(ctx, r.backend)
	if err != nil {
		return err
	}
	r.Index.Add(locs)
	r.packer = NewPacker(r.masterKey)
	r.pending = make(map[BlobID]struct{})
	return nil
}

func (r *Repo) SaveSnapshot(ctx context.Context, snap *Snapshot) error {
	return snap.Save(ctx, r.backend, r.masterKey)
}

func (r *Repo) Backend() backend.Backend    { return r.backend }
func (r *Repo) MasterKey() crypto.MasterKey { return r.masterKey }
