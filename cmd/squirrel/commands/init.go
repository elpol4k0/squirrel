package commands

import (
	"fmt"

	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
)

var repoPath string

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize a new squirrel repository",
	RunE: func(cmd *cobra.Command, args []string) error {
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return repo.Init(repoPath)
	},
}

func init() {
	initCmd.Flags().StringVar(&repoPath, "repo", "", "Path to the repository (required)")
}
