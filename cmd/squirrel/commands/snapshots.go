package commands

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/repo"
)

var snapshotsCmd = &cobra.Command{
	Use:     "snapshots",
	Short:   "List snapshots in a repository",
	Example: "  squirrel snapshots --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		host, _ := cmd.Flags().GetString("host")
		tag, _ := cmd.Flags().GetString("tag")
		snapType, _ := cmd.Flags().GetString("type")
		latest, _ := cmd.Flags().GetBool("latest")
		jsonOut, _ := cmd.Flags().GetBool("json")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runSnapshots(repoPath, host, tag, snapType, latest, jsonOut)
	},
}

func init() {
	snapshotsCmd.Flags().String("repo", "", "repository URL (required)")
	snapshotsCmd.Flags().String("host", "", "filter by hostname")
	snapshotsCmd.Flags().String("tag", "", "filter by tag")
	snapshotsCmd.Flags().String("type", "", "filter by type (files, postgres-base, postgres-wal, mysql-dump, mysql-binlog, mysql-physical)")
	snapshotsCmd.Flags().Bool("latest", false, "show only the most recent snapshot")
	snapshotsCmd.Flags().Bool("json", false, "output as JSON")
}

func runSnapshots(repoPath, hostFilter, tagFilter, typeFilter string, latest, jsonOut bool) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}

	snaps, err := r.ListSnapshots(context.Background())
	if err != nil {
		return err
	}

	var filtered []*repo.Snapshot
	for _, s := range snaps {
		if hostFilter != "" && s.Hostname != hostFilter {
			continue
		}
		if tagFilter != "" && !containsTag(s.Tags, tagFilter) {
			continue
		}
		if typeFilter != "" && s.Meta["type"] != typeFilter {
			continue
		}
		filtered = append(filtered, s)
	}

	// ListSnapshots already sorts ascending by time; take the last element.
	if latest && len(filtered) > 0 {
		filtered = []*repo.Snapshot{filtered[len(filtered)-1]}
	}

	if len(filtered) == 0 {
		fmt.Println("no snapshots found")
		return nil
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(filtered)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tDATE\tHOST\tTYPE\tPATHS\tTAGS")
	fmt.Fprintln(w, strings.Repeat("-", 80))
	for _, s := range filtered {
		tags := strings.Join(s.Tags, ",")
		if tags == "" {
			tags = "-"
		}
		snapType := s.Meta["type"]
		if snapType == "" {
			snapType = "files"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID[:12],
			s.Time.Format("2006-01-02 15:04"),
			s.Hostname,
			snapType,
			strings.Join(s.Paths, ", "),
			tags,
		)
	}
	w.Flush()
	fmt.Printf("\n%d snapshot(s)\n", len(filtered))
	return nil
}

func containsTag(tags []string, tag string) bool {
	for _, t := range tags {
		if t == tag {
			return true
		}
	}
	return false
}
