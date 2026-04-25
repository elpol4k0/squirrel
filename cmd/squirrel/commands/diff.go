package commands

import (
	"context"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/repo"
)

var diffCmd = &cobra.Command{
	Use:   "diff <snapshot-a> <snapshot-b>",
	Short: "Show files added, removed, or changed between two snapshots",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		statOnly, _ := cmd.Flags().GetBool("stat")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runDiff(repoPath, args[0], args[1], statOnly)
	},
}

func init() {
	diffCmd.Flags().String("repo", "", "repository URL")
	diffCmd.Flags().Bool("stat", false, "show only summary counts, not individual files")
}

func runDiff(repoURL, idA, idB string, statOnly bool) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	ctx := context.Background()

	snapA, err := r.FindSnapshot(ctx, idA)
	if err != nil {
		return fmt.Errorf("snapshot A: %w", err)
	}
	snapB, err := r.FindSnapshot(ctx, idB)
	if err != nil {
		return fmt.Errorf("snapshot B: %w", err)
	}

	if snapA.Tree == "" || snapB.Tree == "" {
		return fmt.Errorf("diff only works on file snapshots (postgres/mysql snapshots have no file tree)")
	}

	filesA, err := flattenTree(ctx, r, snapA.Tree, "")
	if err != nil {
		return fmt.Errorf("read snapshot A: %w", err)
	}
	filesB, err := flattenTree(ctx, r, snapB.Tree, "")
	if err != nil {
		return fmt.Errorf("read snapshot B: %w", err)
	}

	added, removed, changed, unchanged := 0, 0, 0, 0

	var w *tabwriter.Writer
	if !statOnly {
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "STATUS\tPATH\tSIZE\n")
	}

	for path, nodeB := range filesB {
		nodeA, exists := filesA[path]
		if !exists {
			if !statOnly {
				fmt.Fprintf(w, "+\t%s\t%d\n", path, nodeB.Size)
			}
			added++
		} else if !slicesEqual(nodeA.Content, nodeB.Content) {
			if !statOnly {
				fmt.Fprintf(w, "M\t%s\t%d\n", path, nodeB.Size)
			}
			changed++
		} else {
			unchanged++
		}
	}
	for path, nodeA := range filesA {
		if _, exists := filesB[path]; !exists {
			if !statOnly {
				fmt.Fprintf(w, "-\t%s\t%d\n", path, nodeA.Size)
			}
			removed++
		}
	}
	if !statOnly {
		w.Flush()
		fmt.Println()
	}

	fmt.Printf("+%d added  -%d removed  M%d modified  =%d unchanged\n",
		added, removed, changed, unchanged)
	return nil
}

func flattenTree(ctx context.Context, r *repo.Repo, treeID, prefix string) (map[string]repo.TreeNode, error) {
	tree, err := r.LoadTree(ctx, treeID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]repo.TreeNode)
	for _, node := range tree.Nodes {
		fullPath := node.Name
		if prefix != "" {
			fullPath = prefix + "/" + node.Name
		}
		switch node.Type {
		case "file":
			result[fullPath] = node
		case "dir":
			if node.Subtree != "" {
				sub, err := flattenTree(ctx, r, node.Subtree, fullPath)
				if err != nil {
					return nil, err
				}
				for k, v := range sub {
					result[k] = v
				}
			}
		}
	}
	return result, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
