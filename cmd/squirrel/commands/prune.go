package commands

import (
	"context"
	"fmt"

	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
)

var pruneRepo string

var pruneCmd = &cobra.Command{
	Use:     "prune",
	Short:   "Remove unreferenced data from the repository",
	Long:    "prune deletes packfiles whose blobs are no longer referenced by any snapshot, then rebuilds the index.",
	Example: `  squirrel prune --repo /mnt/backup/myrepo`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if pruneRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		return runPrune(pruneRepo)
	},
}

func init() {
	pruneCmd.Flags().StringVar(&pruneRepo, "repo", "", "Repository path (required)")
}

func runPrune(repoPath string) error {
	ctx := context.Background()

	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	fmt.Println("Analysing repository...")
	deleted, freed, err := r.Prune(ctx)
	if err != nil {
		return fmt.Errorf("prune: %w", err)
	}

	if deleted == 0 {
		fmt.Println("Nothing to prune.")
		return nil
	}
	fmt.Printf("Removed %d packfile(s), freed %s\n", deleted, humanBytes(freed))
	return nil
}
