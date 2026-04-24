package commands

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
)

var (
	restoreRepo   string
	restoreTarget string
)

var restoreCmd = &cobra.Command{
	Use:   "restore <snapshot-id>",
	Short: "Restore files from a snapshot",
	Long:  "Restore restores all files in a snapshot to the target directory. Snapshot ID can be a prefix.",
	Example: `  squirrel restore ab12cd34 --repo /mnt/backup/myrepo --target /tmp/restored
  squirrel restore ab12 --repo /mnt/backup/myrepo --target ./out`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if restoreRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		if restoreTarget == "" {
			return fmt.Errorf("--target is required")
		}
		return runRestore(args[0], restoreRepo, restoreTarget)
	},
}

func init() {
	restoreCmd.Flags().StringVar(&restoreRepo, "repo", "", "Repository path (required)")
	restoreCmd.Flags().StringVar(&restoreTarget, "target", "", "Directory to restore files into (required)")
}

func runRestore(snapshotID, repoPath, targetDir string) error {
	ctx := context.Background()

	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	snap, err := r.FindSnapshot(ctx, snapshotID)
	if err != nil {
		return err
	}
	fmt.Printf("Restoring snapshot %s (%s)\n", snap.ID[:12], snap.Time.Format("2006-01-02 15:04:05"))

	tree, err := r.LoadTree(ctx, snap.Tree)
	if err != nil {
		return fmt.Errorf("load tree: %w", err)
	}

	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		return fmt.Errorf("create target dir: %w", err)
	}

	var totalBytes int64
	for _, node := range tree.Nodes {
		if err := restoreNode(ctx, r, node, targetDir); err != nil {
			return fmt.Errorf("restore %s: %w", node.Name, err)
		}
		totalBytes += node.Size
		fmt.Printf("  restored %s (%s)\n", node.Name, humanBytes(node.Size))
	}

	fmt.Printf("\nDone – restored %d file(s), %s total\n", len(tree.Nodes), humanBytes(totalBytes))
	return nil
}

func restoreNode(ctx context.Context, r *repo.Repo, node repo.TreeNode, targetDir string) error {
	if node.Type != "file" {
		return fmt.Errorf("unsupported node type %q (only \"file\" supported for now)", node.Type)
	}

	destPath := filepath.Join(targetDir, node.Name)
	f, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("create %s: %w", destPath, err)
	}
	defer f.Close()

	for _, blobIDHex := range node.Content {
		blobID, err := repo.ParseBlobID(blobIDHex)
		if err != nil {
			return err
		}
		loc, ok := r.Index.Get(blobID)
		if !ok {
			return fmt.Errorf("blob %s not found in index", blobIDHex[:12])
		}
		data, err := r.LoadBlob(ctx, loc)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return fmt.Errorf("write: %w", err)
		}
	}
	return nil
}
