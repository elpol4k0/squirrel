package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	gomysql "github.com/go-mysql-org/go-mysql/mysql"

	"github.com/elpol4k0/squirrel/internal/repo"
)

// Briefly locks all tables for a consistent snapshot + binlog position, then streams all files.
func (a *Adapter) PhysicalBackup(ctx context.Context, r *repo.Repo, dataDir string) (gomysql.Position, string, string, error) {
	db, err := sql.Open("mysql", a.dsn)
	if err != nil {
		return gomysql.Position{}, "", "", fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	conn, err := db.Conn(ctx)
	if err != nil {
		return gomysql.Position{}, "", "", err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "FLUSH TABLES WITH READ LOCK"); err != nil {
		return gomysql.Position{}, "", "", fmt.Errorf("flush tables: %w", err)
	}

	var file, binlogDoDB, binlogIgnoreDB, gtidSet string
	var pos uint32
	row := conn.QueryRowContext(ctx, "SHOW MASTER STATUS")
	if err := row.Scan(&file, &pos, &binlogDoDB, &binlogIgnoreDB, &gtidSet); err != nil {
		conn.ExecContext(ctx, "UNLOCK TABLES")
		return gomysql.Position{}, "", "", fmt.Errorf("show master status: %w", err)
	}
	binlogPos := gomysql.Position{Name: file, Pos: pos}
	slog.Info("physical backup locked", "binlog", file, "pos", pos)

	treeID, err := streamDataDir(ctx, r, dataDir)

	conn.ExecContext(ctx, "UNLOCK TABLES") //nolint:errcheck

	if err != nil {
		return gomysql.Position{}, "", "", fmt.Errorf("stream data dir: %w", err)
	}

	slog.Info("physical backup complete", "dataDir", dataDir)
	return binlogPos, gtidSet, treeID, nil
}

func streamDataDir(ctx context.Context, r *repo.Repo, dataDir string) (string, error) {
	var nodes []repo.TreeNode

	err := filepath.WalkDir(dataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		blobIDs, size, err := streamFile(ctx, r, path)
		if err != nil {
			slog.Warn("skip file", "path", path, "err", err)
			return nil
		}

		nodes = append(nodes, repo.TreeNode{
			Name:    rel,
			Type:    "file",
			Size:    size,
			Content: blobIDs,
		})
		return nil
	})
	if err != nil {
		return "", err
	}

	treeID, err := r.SaveTree(ctx, &repo.Tree{Nodes: nodes})
	if err != nil {
		return "", fmt.Errorf("save physical tree: %w", err)
	}
	return treeID, nil
}

func streamFile(ctx context.Context, r *repo.Repo, path string) ([]string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()

	buf := make([]byte, 4*1024*1024)
	var blobIDs []string
	var total int64

	for {
		n, err := io.ReadFull(f, buf)
		if n > 0 {
			id, _, serr := r.SaveBlob(ctx, repo.BlobData, buf[:n])
			if serr != nil {
				return nil, 0, serr
			}
			blobIDs = append(blobIDs, id.String())
			total += int64(n)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, 0, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return blobIDs, total, nil
}

func RestorePhysical(ctx context.Context, r *repo.Repo, snap *repo.Snapshot, targetDir string) error {
	if snap.Tree == "" {
		return fmt.Errorf("snapshot %s has no tree", snap.ID)
	}
	tree, err := r.LoadTree(ctx, snap.Tree)
	if err != nil {
		return fmt.Errorf("load physical tree: %w", err)
	}

	for _, node := range tree.Nodes {
		if node.Type != "file" {
			continue
		}
		dest := filepath.Join(targetDir, filepath.FromSlash(node.Name))
		if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
			return err
		}
		if err := extractBlobsToFile(ctx, r, node.Content, dest); err != nil {
			return fmt.Errorf("restore %s: %w", node.Name, err)
		}
	}
	slog.Info("physical restore complete", "target", targetDir)
	return nil
}
