package commands

import (
	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "squirrel",
	Short: "Fast, incremental, database-aware backups",
	Long:  "squirrel – like restic, but understands databases. PostgreSQL WAL + MySQL Binlog, content-addressed storage, AES-256-GCM encryption.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(backupCmd)
	rootCmd.AddCommand(snapshotsCmd)
	rootCmd.AddCommand(restoreCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(forgetCmd)
	rootCmd.AddCommand(pruneCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(scheduleCmd)
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(selfUpdateCmd)
	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(diffCmd)
	rootCmd.AddCommand(statsCmd)
	rootCmd.AddCommand(keyCmd)
	rootCmd.AddCommand(secretsCmd)
}
