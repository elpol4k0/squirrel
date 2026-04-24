package commands

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	squirrelfuse "github.com/elpol4k0/squirrel/internal/fuse"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var mountCmd = &cobra.Command{
	Use:   "mount <snapshot-id> <mountpoint>",
	Short: "Mount a snapshot as a read-only filesystem (Linux/macOS only)",
	Example: `  squirrel mount abc123 /mnt/snapshot --repo /backup/myrepo
  # unmount with: umount /mnt/snapshot`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runMount(repoPath, args[0], args[1])
	},
}

func init() {
	mountCmd.Flags().String("repo", "", "repository URL")
}

func runMount(repoURL, snapID, mountPoint string) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("create mountpoint: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return squirrelfuse.Mount(ctx, r, snapID, mountPoint)
}
