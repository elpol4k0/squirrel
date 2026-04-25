package commands

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/repo"
)

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage repository keys (multiple passwords per repository)",
	Long: `A squirrel repository can have multiple key files, each wrapping the same
master key with a different password. This lets you grant access to multiple
users or rotate passwords without re-encrypting any data.`,
}

var keyListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List all key files in the repository",
	Example: "  squirrel key list --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runKeyList(repoPath)
	},
}

var keyAddCmd = &cobra.Command{
	Use:     "add",
	Short:   "Add a new password to the repository",
	Example: "  squirrel key add --repo /mnt/backup/myrepo",
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runKeyAdd(repoPath)
	},
}

var keyRemoveCmd = &cobra.Command{
	Use:     "remove <key-id>",
	Short:   "Remove a key file from the repository",
	Example: "  squirrel key remove --repo /mnt/backup/myrepo abcd1234ef56",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		repoPath, _ := cmd.Flags().GetString("repo")
		if repoPath == "" {
			return fmt.Errorf("--repo is required")
		}
		return runKeyRemove(repoPath, args[0])
	},
}

func init() {
	for _, sub := range []*cobra.Command{keyListCmd, keyAddCmd, keyRemoveCmd} {
		sub.Flags().String("repo", "", "repository URL (required)")
	}
	keyCmd.AddCommand(keyListCmd)
	keyCmd.AddCommand(keyAddCmd)
	keyCmd.AddCommand(keyRemoveCmd)
}

func runKeyList(repoPath string) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	keys, err := r.ListKeys(context.Background())
	if err != nil {
		return err
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "ID\n")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 32))
	for _, k := range keys {
		fmt.Fprintln(w, k)
	}
	w.Flush()
	fmt.Printf("\n%d key(s)\n", len(keys))
	return nil
}

func runKeyAdd(repoPath string) error {
	current, err := readTerminalPassword("Current repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, current)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	newPw, err := readTerminalPassword("New password: ")
	if err != nil {
		return err
	}
	confirm, err := readTerminalPassword("Confirm new password: ")
	if err != nil {
		return err
	}
	if string(newPw) != string(confirm) {
		return fmt.Errorf("passwords do not match")
	}
	id, err := r.AddKey(context.Background(), newPw)
	if err != nil {
		return err
	}
	fmt.Printf("Key added: %s\n", id)
	return nil
}

func runKeyRemove(repoPath, keyID string) error {
	password, err := readTerminalPassword("Repository password: ")
	if err != nil {
		return err
	}
	r, err := repo.Open(repoPath, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	if err := r.RemoveKey(context.Background(), keyID); err != nil {
		return err
	}
	fmt.Printf("Key %s removed\n", keyID)
	return nil
}
