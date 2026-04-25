package postgres

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func RestoreBase(ctx context.Context, r *repo.Repo, snap *repo.Snapshot, targetDir string) error {
	if snap.Tree == "" {
		return fmt.Errorf("snapshot %s has no tree", snap.ID)
	}
	tree, err := r.LoadTree(ctx, snap.Tree)
	if err != nil {
		return fmt.Errorf("load base backup tree: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return fmt.Errorf("mkdir target: %w", err)
	}

	// base.tar must be extracted first so pg_tblspc symlinks exist before we rewrite them.
	for _, node := range tree.Nodes {
		if node.Type != "file" || node.Name != "base.tar" {
			continue
		}
		slog.Info("extracting base", "blobs", len(node.Content))
		if err := extractBlobsAsTAR(ctx, r, node.Content, targetDir); err != nil {
			return fmt.Errorf("extract base.tar: %w", err)
		}
	}

	for _, node := range tree.Nodes {
		if node.Type != "file" || node.Name == "base.tar" {
			continue
		}
		oid := strings.TrimSuffix(strings.TrimPrefix(node.Name, "pg_tblspc_"), ".tar")
		localDataDir := filepath.Join(targetDir, "pg_tblspc_data", oid)
		if err := os.MkdirAll(localDataDir, 0o700); err != nil {
			return fmt.Errorf("mkdir tablespace data: %w", err)
		}
		slog.Info("extracting tablespace", "oid", oid, "blobs", len(node.Content))
		if err := extractBlobsAsTAR(ctx, r, node.Content, localDataDir); err != nil {
			return fmt.Errorf("extract tablespace %s: %w", oid, err)
		}
		// Replace the symlink pg_basebackup created (pointing to the original server path)
		// with one pointing to our locally extracted copy.
		symlinkPath := filepath.Join(targetDir, "pg_tblspc", oid)
		if orig, err := os.Readlink(symlinkPath); err == nil {
			slog.Info("tablespace symlink replaced", "oid", oid, "was", orig, "now", localDataDir)
		}
		os.Remove(symlinkPath)
		if err := os.MkdirAll(filepath.Dir(symlinkPath), 0o700); err != nil {
			return fmt.Errorf("mkdir pg_tblspc: %w", err)
		}
		if err := os.Symlink(localDataDir, symlinkPath); err != nil {
			return fmt.Errorf("symlink tablespace %s: %w", oid, err)
		}
	}
	return nil
}

func ExtractWAL(ctx context.Context, r *repo.Repo, walSnaps []*repo.Snapshot, walDir string) error {
	if err := os.MkdirAll(walDir, 0o700); err != nil {
		return err
	}
	total := 0
	for _, snap := range walSnaps {
		if snap.Tree == "" {
			continue
		}
		tree, err := r.LoadTree(ctx, snap.Tree)
		if err != nil {
			return fmt.Errorf("load wal tree %s: %w", snap.ID[:8], err)
		}
		for _, node := range tree.Nodes {
			if node.Type != "file" {
				continue
			}
			dest := filepath.Join(walDir, node.Name)
			if _, err := os.Stat(dest); err == nil {
				continue // already present
			}
			if err := extractBlobsToFile(ctx, r, node.Content, dest); err != nil {
				return fmt.Errorf("extract wal %s: %w", node.Name, err)
			}
			total++
		}
	}
	slog.Info("WAL segments extracted", "count", total, "dir", walDir)
	return nil
}

// targetTime and targetLSN are optional PITR targets (empty = recover to latest).
func WriteRecoveryConf(targetDir, walDir, targetTime, targetLSN string) error {
	sigPath := filepath.Join(targetDir, "recovery.signal")
	if err := os.WriteFile(sigPath, []byte{}, 0o600); err != nil {
		return fmt.Errorf("write recovery.signal: %w", err)
	}

	restoreCmd := fmt.Sprintf("cp %s/%%f %%p", walDir)

	var sb strings.Builder
	sb.WriteString("\n# Written by squirrel\n")
	sb.WriteString(fmt.Sprintf("restore_command = '%s'\n", restoreCmd))
	if targetTime != "" {
		sb.WriteString(fmt.Sprintf("recovery_target_time = '%s'\n", targetTime))
		sb.WriteString("recovery_target_action = 'promote'\n")
	}
	if targetLSN != "" {
		sb.WriteString(fmt.Sprintf("recovery_target_lsn = '%s'\n", targetLSN))
		sb.WriteString("recovery_target_action = 'promote'\n")
	}

	autoconf := filepath.Join(targetDir, "postgresql.auto.conf")
	f, err := os.OpenFile(autoconf, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open postgresql.auto.conf: %w", err)
	}
	defer f.Close()
	_, err = f.WriteString(sb.String())
	return err
}

func extractBlobsAsTAR(ctx context.Context, r *repo.Repo, blobIDs []string, targetDir string) error {
	pr, pw := io.Pipe()
	errCh := make(chan error, 1)

	go func() {
		defer pw.Close()
		for _, id := range blobIDs {
			data, err := r.LoadBlobByID(ctx, id)
			if err != nil {
				pw.CloseWithError(err)
				errCh <- err
				return
			}
			if _, err := pw.Write(data); err != nil {
				errCh <- err
				return
			}
		}
		errCh <- nil
	}()

	extractErr := extractTAR(pr, targetDir)
	pipeErr := <-errCh
	if extractErr != nil {
		return extractErr
	}
	return pipeErr
}

func extractTAR(rd io.Reader, targetDir string) error {
	tr := tar.NewReader(rd)
	cleanTarget := filepath.Clean(targetDir) + string(os.PathSeparator)

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		target := filepath.Join(targetDir, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), cleanTarget) {
			return fmt.Errorf("tar path traversal: %q", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)|0o700); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, os.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(f, tr)
			f.Close()
			if copyErr != nil {
				return copyErr
			}
		case tar.TypeSymlink:
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			link := filepath.Join(targetDir, filepath.FromSlash(hdr.Linkname))
			os.Remove(target)
			if err := os.Link(link, target); err != nil {
				return err
			}
		}
	}
	return nil
}

func extractBlobsToFile(ctx context.Context, r *repo.Repo, blobIDs []string, dest string) error {
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, id := range blobIDs {
		data, err := r.LoadBlobByID(ctx, id)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return err
		}
	}
	return nil
}
