package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/backend"
	"github.com/elpol4k0/squirrel/internal/repo"
)

type repoStats struct {
	Snapshots  int   `json:"snapshots"`
	Blobs      int   `json:"blobs"`
	Packs      int   `json:"packs"`
	PackBytes  int64 `json:"pack_bytes"`
	IndexFiles int   `json:"index_files"`
}

var statsCmd = &cobra.Command{
	Use:     "stats",
	Short:   "Show repository statistics (snapshot count, blob count, storage size)",
	Example: "  squirrel stats --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		jsonOut, _ := cmd.Flags().GetBool("json")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runStats(repoPath, jsonOut)
	},
}

func init() {
	statsCmd.Flags().String("repo", "", "repository URL (required)")
	statsCmd.Flags().Bool("json", false, "output as JSON")
}

func runStats(repoPath string, jsonOut bool) error {
	ctx := context.Background()

	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}

	packs, err := r.Backend().List(ctx, backend.TypeData)
	if err != nil {
		return fmt.Errorf("list packs: %w", err)
	}

	var totalPackBytes int64
	for _, pack := range packs {
		fi, err := r.Backend().Stat(ctx, backend.Handle{Type: backend.TypeData, Name: pack})
		if err == nil {
			totalPackBytes += fi.Size
		}
	}

	indexFiles, _ := r.Backend().List(ctx, backend.TypeIndex)

	st := repoStats{
		Snapshots:  len(snaps),
		Blobs:      r.Index.Count(),
		Packs:      len(packs),
		PackBytes:  totalPackBytes,
		IndexFiles: len(indexFiles),
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(st)
	}

	fmt.Printf("Snapshots:    %d\n", st.Snapshots)
	fmt.Printf("Unique blobs: %d\n", st.Blobs)
	fmt.Printf("Pack files:   %d  (%s)\n", st.Packs, humanBytes(st.PackBytes))
	fmt.Printf("Index files:  %d\n", st.IndexFiles)
	return nil
}
