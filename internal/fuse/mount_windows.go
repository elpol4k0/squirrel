//go:build windows && cgo

package fuse

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func Mount(ctx context.Context, r *repo.Repo, snapID, mountPoint string) error {
	snap, err := r.FindSnapshot(ctx, snapID)
	if err != nil {
		return err
	}
	if snap.Tree == "" {
		return fmt.Errorf("snapshot %s has no file tree (postgres/mysql snapshots cannot be mounted)", snap.ID[:8])
	}

	fs := &snapFS{r: r, ctx: ctx, nodes: make(map[string]*fsNode)}
	if err := fs.loadTree(snap.Tree, "/"); err != nil {
		return fmt.Errorf("load tree: %w", err)
	}

	host := fuse.NewFileSystemHost(fs)
	host.SetCapReaddirPlus(true)

	slog.Info("snapshot mounted", "id", snap.ID[:8], "at", mountPoint)
	fmt.Printf("snapshot %s mounted at %s (Ctrl-C or unmount to stop)\n", snap.ID[:8], mountPoint)

	go func() {
		<-ctx.Done()
		host.Unmount()
	}()

	if ok := host.Mount(mountPoint, nil); !ok {
		return fmt.Errorf("fuse mount failed (is WinFsp installed? https://winfsp.dev)")
	}
	return nil
}

type nodeKind int

const (
	kindDir nodeKind = iota
	kindFile
)

type fsNode struct {
	kind     nodeKind
	mode     uint32
	modTime  int64
	size     int64
	blobIDs  []string
	children []string
}

type openHandle struct {
	data []byte
}

type snapFS struct {
	fuse.FileSystemBase
	mu      sync.RWMutex
	nodes   map[string]*fsNode
	r       *repo.Repo
	ctx     context.Context
	handles sync.Map
	nextFH  atomic.Uint64
}

func (fs *snapFS) loadTree(treeID, parentPath string) error {
	tree, err := fs.r.LoadTree(fs.ctx, treeID)
	if err != nil {
		return err
	}

	parent := fs.ensureDir(parentPath)

	for _, n := range tree.Nodes {
		childPath := path.Join(parentPath, n.Name)
		parent.children = append(parent.children, n.Name)

		switch n.Type {
		case "dir":
			mode := n.Mode
			if mode == 0 {
				mode = 0o555
			}
			fs.nodes[childPath] = &fsNode{kind: kindDir, mode: mode, modTime: n.ModTime}
			if n.Subtree != "" {
				if err := fs.loadTree(n.Subtree, childPath); err != nil {
					return err
				}
			}
		case "file":
			mode := n.Mode
			if mode == 0 {
				mode = 0o444
			}
			fs.nodes[childPath] = &fsNode{kind: kindFile, mode: mode, modTime: n.ModTime, size: n.Size, blobIDs: n.Content}
		}
	}
	return nil
}

func (fs *snapFS) ensureDir(p string) *fsNode {
	if n, ok := fs.nodes[p]; ok {
		return n
	}
	n := &fsNode{kind: kindDir, mode: 0o555}
	fs.nodes[p] = n
	return n
}

func (fs *snapFS) Statfs(pth string, stat *fuse.Statfs_t) int {
	stat.Bsize = 4096
	stat.Frsize = 4096
	stat.Namemax = 255
	return 0
}

func (fs *snapFS) Getattr(pth string, stat *fuse.Stat_t, fh uint64) int {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	if pth == "/" {
		stat.Mode = fuse.S_IFDIR | 0o555
		return 0
	}

	node, ok := fs.nodes[pth]
	if !ok {
		return -fuse.ENOENT
	}
	fillStat(stat, node)
	return 0
}

func (fs *snapFS) Readdir(pth string, fill func(name string, stat *fuse.Stat_t, ofst int64) bool, ofst int64, fh uint64) int {
	fs.mu.RLock()
	node := fs.nodes[pth]
	fs.mu.RUnlock()

	fill(".", nil, 0)
	fill("..", nil, 0)

	if node == nil {
		return 0
	}

	for _, name := range node.children {
		childPath := path.Join(pth, name)
		var st *fuse.Stat_t
		if child, ok := fs.nodes[childPath]; ok {
			st = &fuse.Stat_t{}
			fillStat(st, child)
		}
		if !fill(name, st, 0) {
			break
		}
	}
	return 0
}

func (fs *snapFS) Open(pth string, flags int) (int, uint64) {
	fs.mu.RLock()
	node, ok := fs.nodes[pth]
	fs.mu.RUnlock()

	if !ok || node.kind != kindFile {
		return -fuse.ENOENT, ^uint64(0)
	}

	var data []byte
	for _, id := range node.blobIDs {
		chunk, err := fs.r.LoadBlobByID(fs.ctx, id)
		if err != nil {
			slog.Error("mount: load blob", "id", id, "err", err)
			return -fuse.EIO, ^uint64(0)
		}
		data = append(data, chunk...)
	}

	fh := fs.nextFH.Add(1)
	fs.handles.Store(fh, &openHandle{data: data})
	return 0, fh
}

func (fs *snapFS) Read(pth string, buff []byte, ofst int64, fh uint64) int {
	v, ok := fs.handles.Load(fh)
	if !ok {
		return -fuse.EIO
	}
	h := v.(*openHandle)
	if ofst >= int64(len(h.data)) {
		return 0
	}
	end := ofst + int64(len(buff))
	if end > int64(len(h.data)) {
		end = int64(len(h.data))
	}
	return copy(buff, h.data[ofst:end])
}

func (fs *snapFS) Release(pth string, fh uint64) int {
	fs.handles.Delete(fh)
	return 0
}

func fillStat(stat *fuse.Stat_t, node *fsNode) {
	if node.kind == kindDir {
		stat.Mode = fuse.S_IFDIR | node.mode
	} else {
		stat.Mode = fuse.S_IFREG | node.mode
		stat.Size = node.size
	}
	if node.modTime != 0 {
		ts := fuse.NewTimespec(time.Unix(0, node.modTime))
		stat.Atim = ts
		stat.Mtim = ts
		stat.Ctim = ts
	}
}
