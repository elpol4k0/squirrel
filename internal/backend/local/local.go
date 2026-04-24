package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/elpol4k0/squirrel/internal/backend"
)

type Local struct {
	root string
}

func New(root string) *Local {
	return &Local{root: root}
}

func (l *Local) Root() string { return l.root }

func (l *Local) Setup(subdirs []string) error {
	for _, d := range subdirs {
		if err := os.MkdirAll(filepath.Join(l.root, d), 0o700); err != nil {
			return fmt.Errorf("mkdir %s: %w", d, err)
		}
	}
	return nil
}

func (l *Local) filePath(h backend.Handle) string {
	if h.Type == backend.TypeData && len(h.Name) >= 2 {
		// shard data into 256 subdirs to keep directory sizes manageable
		return filepath.Join(l.root, string(h.Type), h.Name[:2], h.Name)
	}
	return filepath.Join(l.root, string(h.Type), h.Name)
}

func (l *Local) Save(_ context.Context, h backend.Handle, rd io.Reader) error {
	p := l.filePath(h)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	tmp := p + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	if _, err := io.Copy(f, rd); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, p)
}

func (l *Local) Load(_ context.Context, h backend.Handle) (io.ReadCloser, error) {
	f, err := os.Open(l.filePath(h))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", l.filePath(h), err)
	}
	return f, nil
}

func (l *Local) List(_ context.Context, t backend.FileType) ([]string, error) {
	dir := filepath.Join(l.root, string(t))
	if t == backend.TypeData {
		return listSharded(dir)
	}
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

func listSharded(dir string) ([]string, error) {
	shards, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, shard := range shards {
		if !shard.IsDir() {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(dir, shard.Name()))
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	return names, nil
}

func (l *Local) Remove(_ context.Context, h backend.Handle) error {
	return os.Remove(l.filePath(h))
}

func (l *Local) Stat(_ context.Context, h backend.Handle) (backend.FileInfo, error) {
	fi, err := os.Stat(l.filePath(h))
	if err != nil {
		return backend.FileInfo{}, err
	}
	return backend.FileInfo{Name: fi.Name(), Size: fi.Size()}, nil
}

func (l *Local) Exists(_ context.Context, h backend.Handle) (bool, error) {
	_, err := os.Stat(l.filePath(h))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
