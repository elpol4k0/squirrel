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
	Snapshots    int     `json:"snapshots"`
	Blobs        int     `json:"blobs"`
	Packs        int     `json:"packs"`
	PackBytes    int64   `json:"pack_bytes"`
	IndexFiles   int     `json:"index_files"`
	LogicalBytes int64   `json:"logical_bytes,omitempty"`
	DedupRatio   float64 `json:"dedup_ratio,omitempty"`
}

var statsCmd = &cobra.Command{
	Use:     "stats",
	Short:   "Show repository statistics (snapshot count, blob count, storage size)",
	Example: "  squirrel stats --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		jsonOut, _ := cmd.Flags().GetBool("json")
		dedup, _ := cmd.Flags().GetBool("dedup")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runStats(repoPath, jsonOut, dedup)
	},
}

func init() {
	statsCmd.Flags().String("repo", "", "repository URL (required)")
	statsCmd.Flags().Bool("json", false, "output as JSON")
	statsCmd.Flags().Bool("dedup", false, "compute logical size and dedup ratio (walks all snapshot trees)")
}

func runStats(repoPath string, jsonOut, dedup bool) error {
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

	if dedup {
		var logicalBytes int64
		for _, snap := range snaps {
			if snap.Tree == "" {
				continue
			}
			tree, err := r.LoadTree(ctx, snap.Tree)
			if err != nil {
				continue
			}
			logicalBytes += sumTreeSize(ctx, r, tree)
		}
		st.LogicalBytes = logicalBytes
		if totalPackBytes > 0 {
			st.DedupRatio = float64(logicalBytes) / float64(totalPackBytes)
		}
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
	if dedup {
		fmt.Printf("Logical size: %s\n", humanBytes(st.LogicalBytes))
		fmt.Printf("Dedup ratio:  %.2fx\n", st.DedupRatio)
	}
	return nil
}

// sumTreeSize recursively sums the file sizes of all nodes in a tree.
func sumTreeSize(ctx context.Context, r *repo.Repo, tree *repo.Tree) int64 {
	var total int64
	for _, node := range tree.Nodes {
		if node.Type == "file" {
			total += node.Size
		} else if node.Type == "dir" && node.Subtree != "" {
			sub, err := r.LoadTree(ctx, node.Subtree)
			if err == nil {
				total += sumTreeSize(ctx, r, sub)
			}
		}
	}
	return total
}
