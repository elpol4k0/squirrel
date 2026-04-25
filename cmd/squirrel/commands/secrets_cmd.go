package commands

import (
	"fmt"

	"github.com/99designs/keyring"
	"github.com/spf13/cobra"
)

const defaultKeyringService = "squirrel"

var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage secrets stored in the OS keyring",
	Long: `Store, list, and delete secrets in the operating-system keyring
(macOS Keychain, Windows Credential Manager, libsecret on Linux).

Secrets stored here can be referenced in config.yml using the ${keyring:service/key} syntax.`,
}

var secretsSetCmd = &cobra.Command{
	Use:     "set <key>",
	Short:   "Store a secret value in the OS keyring",
	Example: "  squirrel secrets set repo/s3-primary",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		service, _ := cmd.Flags().GetString("service")
		value, err := readTerminalPassword(fmt.Sprintf("Value for %q: ", args[0]))
		if err != nil {
			return err
		}
		ring, err := openKeyring(service)
		if err != nil {
			return err
		}
		if err := ring.Set(keyring.Item{Key: args[0], Data: value}); err != nil {
			return fmt.Errorf("store secret: %w", err)
		}
		fmt.Printf("Secret %q stored (service: squirrel-%s)\n", args[0], service)
		return nil
	},
}

var secretsListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List secret keys stored in the OS keyring",
	Example: "  squirrel secrets list",
	RunE: func(cmd *cobra.Command, args []string) error {
		service, _ := cmd.Flags().GetString("service")
		ring, err := openKeyring(service)
		if err != nil {
			return err
		}
		keys, err := ring.Keys()
		if err != nil {
			return fmt.Errorf("list secrets: %w", err)
		}
		if len(keys) == 0 {
			fmt.Printf("no secrets in service squirrel-%s\n", service)
			return nil
		}
		for _, k := range keys {
			fmt.Println(k)
		}
		return nil
	},
}

var secretsDeleteCmd = &cobra.Command{
	Use:     "delete <key>",
	Short:   "Remove a secret from the OS keyring",
	Example: "  squirrel secrets delete repo/s3-primary",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		service, _ := cmd.Flags().GetString("service")
		ring, err := openKeyring(service)
		if err != nil {
			return err
		}
		if err := ring.Remove(args[0]); err != nil {
			return fmt.Errorf("delete secret %q: %w", args[0], err)
		}
		fmt.Printf("Secret %q deleted\n", args[0])
		return nil
	},
}

func init() {
	for _, sub := range []*cobra.Command{secretsSetCmd, secretsListCmd, secretsDeleteCmd} {
		sub.Flags().String("service", defaultKeyringService, "keyring namespace (squirrel-<service>)")
	}
	secretsCmd.AddCommand(secretsSetCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
}

func openKeyring(service string) (keyring.Keyring, error) {
	ring, err := keyring.Open(keyring.Config{
		ServiceName: "squirrel-" + service,
	})
	if err != nil {
		return nil, fmt.Errorf("open OS keyring: %w", err)
	}
	return ring, nil
}
