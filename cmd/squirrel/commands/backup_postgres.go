package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/db/postgres"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var pgBackupCmd = &cobra.Command{
	Use:   "postgres",
	Short: "Physical backup of a PostgreSQL instance",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		dsn, _ := cmd.Flags().GetString("dsn")
		slot, _ := cmd.Flags().GetString("slot")
		tags, _ := cmd.Flags().GetStringArray("tag")
		walOnly, _ := cmd.Flags().GetBool("wal-only")

		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		if dsn == "" {
			dsn = os.Getenv("SQUIRREL_PG_DSN")
		}
		if dsn == "" {
			return fmt.Errorf("--dsn or SQUIRREL_PG_DSN is required")
		}
		return runPGBackup(repoPath, dsn, slot, tags, walOnly)
	},
}

func init() {
	pgBackupCmd.Flags().String("repo", "", "repository URL")
	pgBackupCmd.Flags().String("dsn", "", "PostgreSQL DSN (e.g. postgres://user:pass@host/db?replication=database)")
	pgBackupCmd.Flags().String("slot", "squirrel", "replication slot name")
	pgBackupCmd.Flags().StringArray("tag", nil, "tag to attach to snapshot")
	pgBackupCmd.Flags().Bool("wal-only", false, "stream WAL only (no base backup); slot must already exist")

	backupCmd.AddCommand(pgBackupCmd)
}

func runPGBackup(repoURL, dsn, slotName string, tags []string, walOnly bool) error {
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

	adapter := postgres.New(dsn)

	if walOnly {
		return streamWALOnly(ctx, r, adapter, slotName)
	}

	slog.Info("creating replication slot", "slot", slotName)
	if err := adapter.CreateSlot(ctx, slotName); err != nil {
		slog.Warn("create slot (may already exist)", "err", err)
	}

	startLSN, sysident, treeID, err := adapter.BaseBackup(ctx, r)
	if err != nil {
		return fmt.Errorf("base backup: %w", err)
	}

	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush repo: %w", err)
	}

	snap, err := repo.NewSnapshot([]string{"postgres://" + dsn}, tags)
	if err != nil {
		return err
	}
	snap.Tree = treeID
	snap.Meta = map[string]string{
		"type":      "postgres-base",
		"start_lsn": startLSN.String(),
		"system_id": sysident.SystemID,
		"timeline":  fmt.Sprintf("%d", sysident.Timeline),
	}
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}
	fmt.Printf("snapshot %s  base backup complete  startLSN=%s\n", snap.ID, startLSN)

	slog.Info("starting WAL streaming (Ctrl-C to stop)")
	segments, err := adapter.StreamWAL(ctx, r, slotName, startLSN, sysident.Timeline)
	if err != nil {
		return fmt.Errorf("wal stream: %w", err)
	}

	return saveWALSnapshot(ctx, r, snap, slotName, segments, sysident.Timeline, tags)
}

func streamWALOnly(ctx context.Context, r *repo.Repo, adapter *postgres.Adapter, slotName string) error {
	sysident, err := adapter.IdentifySystem(ctx)
	if err != nil {
		return err
	}
	slog.Info("WAL-only streaming", "slot", slotName, "startLSN", sysident.XLogPos)
	segments, err := adapter.StreamWAL(ctx, r, slotName, sysident.XLogPos, sysident.Timeline)
	if err != nil {
		return err
	}
	if len(segments) == 0 {
		fmt.Println("no WAL segments received")
		return nil
	}
	return saveWALSnapshot(ctx, r, nil, slotName, segments, sysident.Timeline, nil)
}

func saveWALSnapshot(ctx context.Context, r *repo.Repo, baseSnap *repo.Snapshot, slotName string, segments []postgres.WALSegment, timeline int32, tags []string) error {
	if len(segments) == 0 {
		return nil
	}

	nodes := make([]repo.TreeNode, 0, len(segments))
	for _, seg := range segments {
		nodes = append(nodes, repo.TreeNode{
			Name:    seg.Name,
			Type:    "file",
			Content: []string{seg.BlobID},
		})
	}
	walTreeID, err := r.SaveTree(ctx, &repo.Tree{Nodes: nodes})
	if err != nil {
		return fmt.Errorf("save wal tree: %w", err)
	}

	if err := r.Flush(ctx); err != nil {
		return fmt.Errorf("flush wal data: %w", err)
	}

	var paths []string
	meta := map[string]string{
		"type":     "postgres-wal",
		"slot":     slotName,
		"timeline": fmt.Sprintf("%d", timeline),
		"segments": fmt.Sprintf("%d", len(segments)),
	}
	if baseSnap != nil {
		paths = baseSnap.Paths
		meta["base_snapshot"] = baseSnap.ID
		meta["system_id"] = baseSnap.Meta["system_id"]
	}

	snap, err := repo.NewSnapshot(paths, tags)
	if err != nil {
		return err
	}
	snap.Tree = walTreeID
	snap.Meta = meta
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		return fmt.Errorf("save wal snapshot: %w", err)
	}
	fmt.Printf("snapshot %s  %d WAL segment(s) stored\n", snap.ID, len(segments))
	return nil
}
