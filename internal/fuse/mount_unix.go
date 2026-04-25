//go:build linux || darwin

package fuse

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"syscall"
	"time"

	gofuse "github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"

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

	root := &snapRoot{r: r, ctx: ctx, treeID: snap.Tree}

	opts := &gofuse.Options{
		MountOptions: fuse.MountOptions{
			Name:       "squirrel",
			FsName:     "squirrel:" + snap.ID[:8],
			Debug:      false,
			AllowOther: os.Getuid() == 0,
		},
	}

	server, err := gofuse.Mount(mountPoint, root, opts)
	if err != nil {
		return fmt.Errorf("fuse mount: %w", err)
	}
	slog.Info("snapshot mounted", "id", snap.ID[:8], "at", mountPoint)
	fmt.Printf("snapshot %s mounted at %s (Ctrl-C or `umount %s` to unmount)\n", snap.ID[:8], mountPoint, mountPoint)

	// Wait for context cancellation or unmount
	done := make(chan struct{})
	go func() {
		server.Wait()
		close(done)
	}()

	select {
	case <-ctx.Done():
		server.Unmount() //nolint:errcheck
		<-done
	case <-done:
	}
	return nil
}

type snapRoot struct {
	gofuse.Inode
	r      *repo.Repo
	ctx    context.Context
	treeID string
}

var _ = (gofuse.NodeReaddirer)((*snapDir)(nil))
var _ = (gofuse.NodeLookuper)((*snapDir)(nil))
var _ = (gofuse.NodeGetattrer)((*snapDir)(nil))

func (s *snapRoot) OnAdd(ctx context.Context) {
	tree, err := s.r.LoadTree(s.ctx, s.treeID)
	if err != nil {
		slog.Error("mount: load root tree", "err", err)
		return
	}
	populateDir(ctx, s.r, &s.Inode, tree)
}

func (s *snapRoot) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	out.Mode = 0o555 | syscall.S_IFDIR
	return 0
}

type snapDir struct {
	gofuse.Inode
	r       *repo.Repo
	treeID  string
	mode    uint32
	modTime int64
}

func (d *snapDir) OnAdd(ctx context.Context) {
	tree, err := d.r.LoadTree(context.Background(), d.treeID)
	if err != nil {
		return
	}
	populateDir(ctx, d.r, &d.Inode, tree)
}

func (d *snapDir) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	mode := d.mode
	if mode == 0 {
		mode = 0o555
	}
	out.Mode = (mode & 0o7777) | syscall.S_IFDIR
	if d.modTime != 0 {
		t := time.Unix(0, d.modTime)
		out.SetTimes(nil, &t, nil)
	}
	return 0
}

func (d *snapDir) Readdir(ctx context.Context) (gofuse.DirStream, syscall.Errno) {
	children := d.Children()
	entries := make([]fuse.DirEntry, 0, len(children))
	for name, child := range children {
		entries = append(entries, fuse.DirEntry{
			Name: name,
			Mode: child.StableAttr().Mode,
			Ino:  child.StableAttr().Ino,
		})
	}
	return gofuse.NewListDirStream(entries), 0
}

func (d *snapDir) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*gofuse.Inode, syscall.Errno) {
	child := d.Inode.GetChild(name)
	if child == nil {
		return nil, syscall.ENOENT
	}
	return child, 0
}

type snapFile struct {
	gofuse.Inode
	r       *repo.Repo
	blobIDs []string
	size    int64
	mode    uint32
	modTime int64
}

func (f *snapFile) Getattr(ctx context.Context, fh gofuse.FileHandle, out *fuse.AttrOut) syscall.Errno {
	mode := f.mode
	if mode == 0 {
		mode = 0o444
	}
	out.Mode = (mode & 0o7777) | syscall.S_IFREG
	out.Size = uint64(f.size)
	if f.modTime != 0 {
		t := time.Unix(0, f.modTime)
		out.SetTimes(nil, &t, nil)
	}
	return 0
}

func (f *snapFile) Open(ctx context.Context, flags uint32) (gofuse.FileHandle, uint32, syscall.Errno) {
	return &snapFileHandle{r: f.r, blobIDs: f.blobIDs}, fuse.FOPEN_DIRECT_IO, 0
}

type snapFileHandle struct {
	r       *repo.Repo
	blobIDs []string
	data    []byte
	loaded  bool
}

func (h *snapFileHandle) Read(ctx context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if !h.loaded {
		var all []byte
		for _, id := range h.blobIDs {
			chunk, err := h.r.LoadBlobByID(ctx, id)
			if err != nil {
				return nil, syscall.EIO
			}
			all = append(all, chunk...)
		}
		h.data = all
		h.loaded = true
	}
	end := int(off) + len(dest)
	if end > len(h.data) {
		end = len(h.data)
	}
	if int(off) >= len(h.data) {
		return fuse.ReadResultData(nil), 0
	}
	return fuse.ReadResultData(h.data[off:end]), 0
}

func populateDir(ctx context.Context, r *repo.Repo, parent *gofuse.Inode, tree *repo.Tree) {
	for _, node := range tree.Nodes {
		n := node
		switch n.Type {
		case "dir":
			dir := &snapDir{r: r, treeID: n.Subtree, mode: n.Mode, modTime: n.ModTime}
			child := parent.NewPersistentInode(ctx, dir, gofuse.StableAttr{Mode: syscall.S_IFDIR})
			parent.AddChild(n.Name, child, true)
		case "file":
			file := &snapFile{r: r, blobIDs: n.Content, size: n.Size, mode: n.Mode, modTime: n.ModTime}
			child := parent.NewPersistentInode(ctx, file, gofuse.StableAttr{Mode: syscall.S_IFREG})
			parent.AddChild(n.Name, child, true)
		}
	}
}
