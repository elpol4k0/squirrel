package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func RestoreSQL(ctx context.Context, r *repo.Repo, snap *repo.Snapshot, dsn string) error {
	if snap.Tree == "" {
		return fmt.Errorf("snapshot %s has no tree", snap.ID)
	}
	tree, err := r.LoadTree(ctx, snap.Tree)
	if err != nil {
		return fmt.Errorf("load dump tree: %w", err)
	}

	db, err := sql.Open("mysql", dsn+"?multiStatements=true")
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	for _, node := range tree.Nodes {
		if node.Type != "file" {
			continue
		}
		slog.Info("restoring", "file", node.Name, "blobs", len(node.Content))
		if err := execBlobsAsSQL(ctx, r, db, node.Content); err != nil {
			return fmt.Errorf("restore %s: %w", node.Name, err)
		}
	}
	return nil
}

func execBlobsAsSQL(ctx context.Context, r *repo.Repo, db *sql.DB, blobIDs []string) error {
	for _, id := range blobIDs {
		data, err := r.LoadBlobByID(ctx, id)
		if err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, string(data)); err != nil {
			return fmt.Errorf("exec sql: %w", err)
		}
	}
	return nil
}

// files can be replayed later with mysqlbinlog
func ExtractBinlog(ctx context.Context, r *repo.Repo, binlogSnaps []*repo.Snapshot, binlogDir string) error {
	if err := os.MkdirAll(binlogDir, 0o700); err != nil {
		return err
	}
	total := 0
	for _, snap := range binlogSnaps {
		if snap.Tree == "" {
			continue
		}
		tree, err := r.LoadTree(ctx, snap.Tree)
		if err != nil {
			return fmt.Errorf("load binlog tree %s: %w", snap.ID[:8], err)
		}
		for _, node := range tree.Nodes {
			if node.Type != "file" {
				continue
			}
			dest := filepath.Join(binlogDir, node.Name)
			if _, err := os.Stat(dest); err == nil {
				continue // already present
			}
			if err := extractBlobsToFile(ctx, r, node.Content, dest); err != nil {
				return fmt.Errorf("extract binlog %s: %w", node.Name, err)
			}
			total++
		}
	}
	slog.Info("binlog segments extracted", "count", total, "dir", binlogDir)
	return nil
}

func WriteInnoDBRecoveryConf(targetDir string, level int) error {
	path := filepath.Join(targetDir, "squirrel-recovery.cnf")
	content := fmt.Sprintf("[mysqld]\ninnodb_force_recovery = %d\n", level)
	return os.WriteFile(path, []byte(content), 0o600)
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
