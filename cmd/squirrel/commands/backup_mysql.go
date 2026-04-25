package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	mysqldb "github.com/elpol4k0/squirrel/internal/db/mysql"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var mysqlBackupCmd = &cobra.Command{
	Use:   "mysql",
	Short: "Logical backup of a MySQL/MariaDB instance with binlog streaming",
	Example: `  squirrel backup mysql --dsn "root:pw@tcp(localhost:3306)/" --repo /backup/myrepo
  squirrel backup mysql --dsn "root:pw@tcp(localhost:3306)/" --repo s3:mybucket/mysql --binlog-only`,
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		dsn, _ := cmd.Flags().GetString("dsn")
		tags, _ := cmd.Flags().GetStringArray("tag")
		databases, _ := cmd.Flags().GetStringArray("database")
		binlogOnly, _ := cmd.Flags().GetBool("binlog-only")
		physical, _ := cmd.Flags().GetBool("physical")
		dataDir, _ := cmd.Flags().GetString("data-dir")

		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		if dsn == "" {
			dsn = os.Getenv("SQUIRREL_MYSQL_DSN")
		}
		if dsn == "" {
			return fmt.Errorf("--dsn or SQUIRREL_MYSQL_DSN is required")
		}
		if physical {
			if dataDir == "" {
				return fmt.Errorf("--data-dir is required for physical backup")
			}
			return runMySQLPhysicalBackup(repoPath, dsn, dataDir, tags)
		}
		return runMySQLBackup(repoPath, dsn, databases, tags, binlogOnly)
	},
}

func init() {
	mysqlBackupCmd.Flags().String("repo", "", "repository URL")
	mysqlBackupCmd.Flags().String("dsn", "", "MySQL DSN (e.g. root:pw@tcp(host:3306)/)")
	mysqlBackupCmd.Flags().StringArray("tag", nil, "tag to attach to snapshot")
	mysqlBackupCmd.Flags().StringArray("database", nil, "databases to back up (default: all user databases)")
	mysqlBackupCmd.Flags().Bool("binlog-only", false, "stream binlog only (no full dump); requires a prior full backup")
	mysqlBackupCmd.Flags().Bool("physical", false, "physical backup (copy data directory files; requires --data-dir)")
	mysqlBackupCmd.Flags().String("data-dir", "", "MySQL data directory for physical backup (e.g. /var/lib/mysql)")

	backupCmd.AddCommand(mysqlBackupCmd)
}

func runMySQLBackup(repoURL, dsn string, databases, tags []string, binlogOnly bool) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	adapter, err := mysqldb.New(dsn)
	if err != nil {
		return fmt.Errorf("mysql adapter: %w", err)
	}

	if binlogOnly {
		return runMySQLBinlogOnly(ctx, r, adapter, dsn, tags)
	}

	// Full logical dump + record binlog position
	slog.Info("starting MySQL logical dump")
	binlogPos, gtidSet, treeID, err := adapter.Dump(ctx, r, databases)
	if err != nil {
		return fmt.Errorf("dump: %w", err)
	}

	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush repo: %w", err)
	}

	snap, err := repo.NewSnapshot([]string{"mysql://" + dsn}, tags)
	if err != nil {
		return err
	}
	snap.Tree = treeID
	snap.Meta = map[string]string{
		"type":        "mysql-dump",
		"binlog_file": binlogPos.Name,
		"binlog_pos":  fmt.Sprintf("%d", binlogPos.Pos),
		"gtid_set":    gtidSet,
	}
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	fmt.Printf("snapshot %s  dump complete  binlog=%s:%d\n", snap.ID, binlogPos.Name, binlogPos.Pos)

	slog.Info("starting binlog streaming (Ctrl-C to stop)")
	var segments []mysqldb.BinlogSegment
	if gtidSet != "" {
		segments, err = adapter.StreamBinlogGTID(ctx, r, gtidSet)
	} else {
		segments, err = adapter.StreamBinlog(ctx, r, binlogPos)
	}
	if err != nil {
		return fmt.Errorf("binlog stream: %w", err)
	}

	return saveBinlogSnapshot(ctx, r, snap, segments, tags)
}

func runMySQLBinlogOnly(ctx context.Context, r *repo.Repo, adapter *mysqldb.Adapter, dsn string, tags []string) error {
	pos, gtidSet, err := adapter.BinlogPosition(ctx)
	if err != nil {
		return err
	}
	var segments []mysqldb.BinlogSegment
	if gtidSet != "" {
		slog.Info("binlog-only GTID streaming")
		segments, err = adapter.StreamBinlogGTID(ctx, r, gtidSet)
	} else {
		slog.Info("binlog-only streaming", "file", pos.Name, "pos", pos.Pos)
		segments, err = adapter.StreamBinlog(ctx, r, pos)
	}
	if err != nil {
		return err
	}
	return saveBinlogSnapshot(ctx, r, nil, segments, tags)
}

func runMySQLPhysicalBackup(repoURL, dsn, dataDir string, tags []string) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	adapter, err := mysqldb.New(dsn)
	if err != nil {
		return fmt.Errorf("mysql adapter: %w", err)
	}

	slog.Info("starting MySQL physical backup", "dataDir", dataDir)
	binlogPos, gtidSet, treeID, err := adapter.PhysicalBackup(ctx, r, dataDir)
	if err != nil {
		return fmt.Errorf("physical backup: %w", err)
	}
	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	snap, err := repo.NewSnapshot([]string{"mysql-physical://" + dataDir}, tags)
	if err != nil {
		return err
	}
	snap.Tree = treeID
	snap.Meta = map[string]string{
		"type":        "mysql-physical",
		"binlog_file": binlogPos.Name,
		"binlog_pos":  fmt.Sprintf("%d", binlogPos.Pos),
		"gtid_set":    gtidSet,
		"data_dir":    dataDir,
	}
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	fmt.Printf("snapshot %s  physical backup complete  binlog=%s:%d\n", snap.ID, binlogPos.Name, binlogPos.Pos)
	return nil
}

func saveBinlogSnapshot(ctx context.Context, r *repo.Repo, dumpSnap *repo.Snapshot, segments []mysqldb.BinlogSegment, tags []string) error {
	if len(segments) == 0 {
		return nil
	}

	nodes := make([]repo.TreeNode, 0, len(segments))
	for _, seg := range segments {
		nodes = append(nodes, repo.TreeNode{
			Name:    fmt.Sprintf("%s@%d", seg.File, seg.Pos),
			Type:    "file",
			Content: []string{seg.BlobID},
		})
	}
	walTreeID, err := r.SaveTree(ctx, &repo.Tree{Nodes: nodes})
	if err != nil {
		return fmt.Errorf("save binlog tree: %w", err)
	}
	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush binlog data: %w", err)
	}

	var paths []string
	meta := map[string]string{
		"type":     "mysql-binlog",
		"segments": fmt.Sprintf("%d", len(segments)),
	}
	if dumpSnap != nil {
		paths = dumpSnap.Paths
		meta["dump_snapshot"] = dumpSnap.ID
	}

	snap, err := repo.NewSnapshot(paths, tags)
	if err != nil {
		return err
	}
	snap.Tree = walTreeID
	snap.Meta = meta
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save binlog snapshot: %w", err)
	}
	fmt.Printf("snapshot %s  %d binlog segment(s) stored\n", snap.ID, len(segments))
	return nil
}
