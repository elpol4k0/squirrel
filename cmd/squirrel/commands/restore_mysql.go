package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	mysqldb "github.com/elpol4k0/squirrel/internal/db/mysql"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var mysqlRestoreCmd = &cobra.Command{
	Use:   "mysql <snapshot-id>",
	Short: "Restore a MySQL dump and extract binlog segments for PITR",
	Example: `  squirrel restore mysql abc123 --repo /backup/myrepo --dsn "root:pw@tcp(localhost:3306)/"
  squirrel restore mysql abc123 --repo /backup/myrepo --dsn "root:pw@tcp(host:3306)/" --binlog-dir /tmp/binlogs
  squirrel restore mysql abc123 --repo /backup/myrepo --target /var/lib/mysql --innodb-recovery 1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		dsn, _ := cmd.Flags().GetString("dsn")
		binlogDir, _ := cmd.Flags().GetString("binlog-dir")
		sqlOnly, _ := cmd.Flags().GetBool("sql-only")
		targetDir, _ := cmd.Flags().GetString("target")
		innodbRecovery, _ := cmd.Flags().GetInt("innodb-recovery")

		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runMySQLRestore(repoPath, args[0], dsn, binlogDir, targetDir, sqlOnly, innodbRecovery)
	},
}

func init() {
	mysqlRestoreCmd.Flags().String("repo", "", "repository URL")
	mysqlRestoreCmd.Flags().String("dsn", "", "MySQL DSN for the target server")
	mysqlRestoreCmd.Flags().String("binlog-dir", "", "directory to extract binlog files into (default: /tmp/squirrel-binlogs)")
	mysqlRestoreCmd.Flags().Bool("sql-only", false, "restore SQL dump only; skip binlog extraction")
	mysqlRestoreCmd.Flags().String("target", "", "target directory for physical restore")
	mysqlRestoreCmd.Flags().Int("innodb-recovery", 0, "InnoDB force recovery level (1-6) written to squirrel-recovery.cnf in --target")

	restoreCmd.AddCommand(mysqlRestoreCmd)
}

func runMySQLRestore(repoURL, snapID, dsn, binlogDir, targetDir string, sqlOnly bool, innodbRecovery int) error {
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

	switch snap.Meta["type"] {
	case "mysql-physical":
		return runMySQLPhysicalRestore(ctx, r, snap, targetDir, innodbRecovery)
	case "mysql-dump":
		if dsn == "" {
			return fmt.Errorf("--dsn is required for dump restore")
		}
		return runMySQLDumpRestore(ctx, r, snap, dsn, binlogDir, sqlOnly)
	default:
		return fmt.Errorf("snapshot %s has unsupported type %q", snap.ID[:8], snap.Meta["type"])
	}
}

func runMySQLPhysicalRestore(ctx context.Context, r *repo.Repo, snap *repo.Snapshot, targetDir string, innodbRecovery int) error {
	if targetDir == "" {
		return fmt.Errorf("--target is required for physical restore")
	}

	slog.Info("restoring MySQL physical backup", "snapshot", snap.ID[:8], "target", targetDir)
	if err := mysqldb.RestorePhysical(ctx, r, snap, targetDir); err != nil {
		return fmt.Errorf("physical restore: %w", err)
	}
	fmt.Printf("physical backup restored to %s\n", targetDir)

	if innodbRecovery > 0 {
		if err := mysqldb.WriteInnoDBRecoveryConf(targetDir, innodbRecovery); err != nil {
			return fmt.Errorf("write innodb recovery conf: %w", err)
		}
		fmt.Printf("InnoDB recovery config written: start mysqld with --defaults-extra-file=%s/squirrel-recovery.cnf\n", targetDir)
	}
	return nil
}

func runMySQLDumpRestore(ctx context.Context, r *repo.Repo, snap *repo.Snapshot, dsn, binlogDir string, sqlOnly bool) error {
	slog.Info("restoring MySQL dump", "snapshot", snap.ID[:8])
	if err := mysqldb.RestoreSQL(ctx, r, snap, dsn); err != nil {
		return fmt.Errorf("restore SQL: %w", err)
	}
	fmt.Printf("dump restored from snapshot %s\n", snap.ID[:8])

	if sqlOnly {
		return nil
	}

	binlogSnaps, err := collectBinlogSnapshots(ctx, r, snap.ID)
	if err != nil {
		return err
	}

	if len(binlogSnaps) == 0 {
		fmt.Println("no binlog snapshots found; done")
		return nil
	}

	if binlogDir == "" {
		binlogDir = "/tmp/squirrel-binlogs"
	}

	slog.Info("extracting binlog segments", "snapshots", len(binlogSnaps), "dir", binlogDir)
	if err := mysqldb.ExtractBinlog(ctx, r, binlogSnaps, binlogDir); err != nil {
		return fmt.Errorf("extract binlog: %w", err)
	}

	fmt.Printf("binlog files extracted to %s\n", binlogDir)
	fmt.Printf("to replay: mysqlbinlog --start-position=%s %s/*.* | mysql -u root -p\n",
		snap.Meta["binlog_pos"], binlogDir)
	return nil
}

func collectBinlogSnapshots(ctx context.Context, r *repo.Repo, dumpSnapID string) ([]*repo.Snapshot, error) {
	all, err := r.ListSnapshots(ctx)
	if err != nil {
		return nil, err
	}

	dumpSnap, err := r.FindSnapshot(ctx, dumpSnapID)
	if err != nil {
		return nil, err
	}

	var binlogs []*repo.Snapshot
	for _, s := range all {
		if s.Meta["type"] != "mysql-binlog" {
			continue
		}
		if s.Meta["dump_snapshot"] != dumpSnapID {
			continue
		}
		if s.Time.Before(dumpSnap.Time) {
			continue
		}
		binlogs = append(binlogs, s)
	}
	return binlogs, nil
}
