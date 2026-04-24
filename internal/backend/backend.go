package backend

import (
	"context"
	"io"
)

type FileType string

const (
	TypeData     FileType = "data"
	TypeIndex    FileType = "index"
	TypeSnapshot FileType = "snapshots"
	TypeKey      FileType = "keys"
	TypeLock     FileType = "locks"
)

type Handle struct {
	Type FileType
	Name string
}

type FileInfo struct {
	Name string
	Size int64
}

// Backend is the storage abstraction. Save must be atomic.
type Backend interface {
	Save(ctx context.Context, h Handle, rd io.Reader) error
	Load(ctx context.Context, h Handle) (io.ReadCloser, error)
	List(ctx context.Context, t FileType) ([]string, error)
	Remove(ctx context.Context, h Handle) error
	Stat(ctx context.Context, h Handle) (FileInfo, error)
	Exists(ctx context.Context, h Handle) (bool, error)
}
