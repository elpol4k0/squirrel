package commands

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"

	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
)

var checkRepo string

var checkCmd = &cobra.Command{
	Use:     "check",
	Short:   "Verify repository integrity",
	Example: "  squirrel check --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		if checkRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		return runCheck(checkRepo)
	},
}

func init() {
	checkCmd.Flags().StringVar(&checkRepo, "repo", "", "Repository path (required)")
}

func runCheck(repoPath string) error {
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
	fmt.Printf("Checking %d snapshot(s)...\n", len(snaps))

	var checked, errs int

	for _, snap := range snaps {
		tree, err := r.LoadTree(ctx, snap.Tree)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR snapshot %s: load tree: %v\n", snap.ID[:12], err)
			errs++
			continue
		}
		e, c := checkTree(ctx, r, tree)
		errs += e
		checked += c
	}

	fmt.Printf("\n%d blob(s) checked", checked)
	if errs > 0 {
		fmt.Printf(", %d error(s) found\n", errs)
		return fmt.Errorf("repository has errors")
	}
	fmt.Println(" – no errors found")
	return nil
}

func checkTree(ctx context.Context, r *repo.Repo, tree *repo.Tree) (errs, checked int) {
	for _, node := range tree.Nodes {
		switch node.Type {
		case "file":
			for _, blobIDHex := range node.Content {
				blobID, err := repo.ParseBlobID(blobIDHex)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR invalid blob ID %q: %v\n", blobIDHex, err)
					errs++
					continue
				}
				loc, ok := r.Index.Get(blobID)
				if !ok {
					fmt.Fprintf(os.Stderr, "ERROR blob %s not in index\n", blobIDHex[:12])
					errs++
					continue
				}
				data, err := r.LoadBlob(ctx, loc)
				if err != nil {
					fmt.Fprintf(os.Stderr, "ERROR load blob %s: %v\n", blobIDHex[:12], err)
					errs++
					continue
				}
				got := sha256.Sum256(data)
				if got != blobID {
					fmt.Fprintf(os.Stderr, "ERROR blob %s: hash mismatch\n", blobIDHex[:12])
					errs++
					continue
				}
				checked++
			}
		case "dir":
			subtree, err := r.LoadTree(ctx, node.Subtree)
			if err != nil {
				fmt.Fprintf(os.Stderr, "ERROR subtree %s (%s): %v\n", node.Name, node.Subtree[:12], err)
				errs++
				continue
			}
			e, c := checkTree(ctx, r, subtree)
			errs += e
			checked += c
		}
	}
	return
}
