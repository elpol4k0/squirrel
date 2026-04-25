package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	pgdb "github.com/elpol4k0/squirrel/internal/db/postgres"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var pgRestoreCmd = &cobra.Command{
	Use:   "postgres <snapshot-id>",
	Short: "Restore a PostgreSQL base backup (and WAL for PITR)",
	Example: `  squirrel restore postgres abc123 --repo /backup/myrepo --target /var/lib/postgresql/17/data
  squirrel restore postgres abc123 --repo /backup/myrepo --target /data --pitr "2026-04-20 14:30:00"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		targetDir, _ := cmd.Flags().GetString("target")
		pitrTime, _ := cmd.Flags().GetString("pitr")
		pitrLSN, _ := cmd.Flags().GetString("pitr-lsn")
		walDir, _ := cmd.Flags().GetString("wal-dir")
		verify, _ := cmd.Flags().GetBool("verify")

		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		if targetDir == "" {
			return fmt.Errorf("--target is required")
		}
		return runPGRestore(repoPath, args[0], targetDir, walDir, pitrTime, pitrLSN, verify)
	},
}

func init() {
	pgRestoreCmd.Flags().String("repo", "", "repository URL")
	pgRestoreCmd.Flags().String("target", "", "target data directory (must be empty or non-existent)")
	pgRestoreCmd.Flags().String("pitr", "", "point-in-time recovery target (e.g. \"2026-04-20 14:30:00\")")
	pgRestoreCmd.Flags().String("pitr-lsn", "", "point-in-time recovery target LSN (e.g. 0/5000028)")
	pgRestoreCmd.Flags().String("wal-dir", "", "directory for WAL segments (default: <target>/pg_wal_archive)")
	pgRestoreCmd.Flags().Bool("verify", false, "verify WAL archive coverage and recovery config after restore")

	restoreCmd.AddCommand(pgRestoreCmd)
}

func runPGRestore(repoURL, snapID, targetDir, walDir, pitrTime, pitrLSN string, verify bool) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	ctx := context.Background()

	snap, err := r.FindSnapshot(ctx, snapID)
	if err != nil {
		return err
	}
	if snap.Meta["type"] != "postgres-base" {
		return fmt.Errorf("snapshot %s is not a postgres-base snapshot (type=%q)", snap.ID[:8], snap.Meta["type"])
	}

	slog.Info("restoring base backup", "snapshot", snap.ID[:8], "target", targetDir)
	if err := pgdb.RestoreBase(ctx, r, snap, targetDir); err != nil {
		return fmt.Errorf("restore base backup: %w", err)
	}
	fmt.Printf("base backup extracted to %s\n", targetDir)

	// Collect WAL snapshots belonging to the same database system.
	systemID := snap.Meta["system_id"]
	walSnaps, err := collectWALSnapshots(ctx, r, snap.ID, systemID)
	if err != nil {
		return err
	}

	if walDir == "" {
		walDir = targetDir + "/pg_wal_archive"
	}

	if len(walSnaps) > 0 {
		slog.Info("extracting WAL segments", "snapshots", len(walSnaps), "walDir", walDir)
		if err := pgdb.ExtractWAL(ctx, r, walSnaps, walDir); err != nil {
			return fmt.Errorf("extract WAL: %w", err)
		}
	} else {
		slog.Warn("no WAL snapshots found; recovery will rely on existing WAL in pg_wal")
	}

	if err := pgdb.WriteRecoveryConf(targetDir, walDir, pitrTime, pitrLSN); err != nil {
		return fmt.Errorf("write recovery config: %w", err)
	}

	fmt.Printf("recovery.signal and postgresql.auto.conf written to %s\n", targetDir)
	if pitrTime != "" {
		fmt.Printf("PITR target: %s\n", pitrTime)
	}
	if pitrLSN != "" {
		fmt.Printf("PITR target LSN: %s\n", pitrLSN)
	}
	fmt.Println("start PostgreSQL to begin recovery")

	if verify {
		if err := pgdb.VerifyRecovery(targetDir, walDir); err != nil {
			return fmt.Errorf("verification failed: %w", err)
		}
	}
	return nil
}

func collectWALSnapshots(ctx context.Context, r *repo.Repo, baseSnapID, systemID string) ([]*repo.Snapshot, error) {
	all, err := r.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}

	baseSnap, err := r.FindSnapshot(ctx, baseSnapID)
	if err != nil {
		return nil, err
	}

	var wal []*repo.Snapshot
	for _, s := range all {
		if s.Meta["type"] != "postgres-wal" {
			continue
		}
		if s.Meta["system_id"] != systemID && s.Meta["base_snapshot"] != baseSnapID {
			continue
		}
		if s.Time.Before(baseSnap.Time) {
			continue
		}
		wal = append(wal, s)
	}
	return wal, nil
}
