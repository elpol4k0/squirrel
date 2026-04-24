package commands

import (
	"context"
	"fmt"

	"github.com/elpol4k0/squirrel/internal/repo"
	"github.com/spf13/cobra"
)

var (
	forgetRepo    string
	forgetLast    int
	forgetDaily   int
	forgetWeekly  int
	forgetMonthly int
	forgetYearly  int
	forgetPrune   bool
	forgetDryRun  bool
)

var forgetCmd = &cobra.Command{
	Use:   "forget",
	Short: "Remove snapshots according to a retention policy",
	Example: `  squirrel forget --repo /mnt/backup/myrepo --keep-daily 7 --keep-weekly 4 --prune
  squirrel forget --repo /mnt/backup/myrepo --keep-last 5 --dry-run`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if forgetRepo == "" {
			return fmt.Errorf("--repo is required")
		}
		if forgetLast == 0 && forgetDaily == 0 && forgetWeekly == 0 && forgetMonthly == 0 && forgetYearly == 0 {
			return fmt.Errorf("at least one --keep-* flag is required")
		}
		return runForget()
	},
}

func init() {
	forgetCmd.Flags().StringVar(&forgetRepo, "repo", "", "Repository path (required)")
	forgetCmd.Flags().IntVar(&forgetLast, "keep-last", 0, "Keep the N most recent snapshots")
	forgetCmd.Flags().IntVar(&forgetDaily, "keep-daily", 0, "Keep the last N days with a snapshot")
	forgetCmd.Flags().IntVar(&forgetWeekly, "keep-weekly", 0, "Keep the last N weeks with a snapshot")
	forgetCmd.Flags().IntVar(&forgetMonthly, "keep-monthly", 0, "Keep the last N months with a snapshot")
	forgetCmd.Flags().IntVar(&forgetYearly, "keep-yearly", 0, "Keep the last N years with a snapshot")
	forgetCmd.Flags().BoolVar(&forgetPrune, "prune", false, "Run prune after removing snapshots")
	forgetCmd.Flags().BoolVar(&forgetDryRun, "dry-run", false, "Show what would be removed without deleting")
}

func runForget() error {
	ctx := context.Background()

	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(forgetRepo, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		return err
	}

	policy := repo.RetentionPolicy{
		KeepLast:    forgetLast,
		KeepDaily:   forgetDaily,
		KeepWeekly:  forgetWeekly,
		KeepMonthly: forgetMonthly,
		KeepYearly:  forgetYearly,
	}
	keep, remove := policy.Apply(snaps)

	fmt.Printf("Snapshots: %d total, %d to keep, %d to remove\n", len(snaps), len(keep), len(remove))
	for _, s := range remove {
		fmt.Printf("  remove %s  %s  %v\n", s.ID[:12], s.Time.Format("2006-01-02 15:04"), s.Paths)
	}

	if forgetDryRun {
		fmt.Println("\n[dry-run] No snapshots were deleted.")
		return nil
	}

	for _, s := range remove {
		if err := r.DeleteSnapshot(ctx, s.ID); err != nil {
			return fmt.Errorf("delete snapshot %s: %w", s.ID[:12], err)
		}
	}
	fmt.Printf("Removed %d snapshot(s)\n", len(remove))

	if forgetPrune && len(remove) > 0 {
		fmt.Println("Running prune...")
		deleted, freed, err := r.Prune(ctx)
		if err != nil {
			return fmt.Errorf("prune: %w", err)
		}
		fmt.Printf("Prune: removed %d packfile(s), freed %s\n", deleted, humanBytes(freed))
	}
	return nil
}
