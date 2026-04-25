package commands

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/db/postgres"
)

var pgCmd = &cobra.Command{
	Use:   "pg",
	Short: "PostgreSQL management commands",
}

var pgDropSlotCmd = &cobra.Command{
	Use:     "drop-slot",
	Short:   "Drop a PostgreSQL replication slot",
	Example: `  squirrel pg drop-slot --dsn "postgres://user:pw@host/db?replication=database" --slot squirrel`,
	RunE: func(cmd *cobra.Command, args []string) error {
		dsn, _ := cmd.Flags().GetString("dsn")
		slot, _ := cmd.Flags().GetString("slot")
		if dsn == "" {
			return fmt.Errorf("--dsn is required")
		}
		if slot == "" {
			return fmt.Errorf("--slot is required")
		}
		if err := postgres.New(dsn).DropSlot(context.Background(), slot); err != nil {
			return err
		}
		fmt.Printf("replication slot %q dropped\n", slot)
		return nil
	},
}

func init() {
	pgDropSlotCmd.Flags().String("dsn", "", "PostgreSQL DSN (with replication=database) (required)")
	pgDropSlotCmd.Flags().String("slot", "", "replication slot name to drop (required)")
	pgCmd.AddCommand(pgDropSlotCmd)
}
