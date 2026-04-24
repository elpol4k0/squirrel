package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage squirrel configuration",
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a skeleton config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		force, _ := cmd.Flags().GetBool("force")
		return runConfigInit(cfgPath, force)
	},
}

var configValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate the config file",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		return runConfigValidate(cfgPath)
	},
}

var configShowCmd = &cobra.Command{
	Use:   "show <profile>",
	Short: "Show the effective config for a profile (secrets masked by default)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		reveal, _ := cmd.Flags().GetBool("reveal")
		return runConfigShow(cfgPath, args[0], reveal)
	},
}

func init() {
	configInitCmd.Flags().String("config", config.DefaultConfigPath(), "output path")
	configInitCmd.Flags().Bool("force", false, "overwrite existing file")

	configValidateCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")

	configShowCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")
	configShowCmd.Flags().Bool("reveal", false, "show secret values in plain text")

	configMigrateCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")

	configCmd.AddCommand(configInitCmd, configValidateCmd, configShowCmd, configMigrateCmd)
}

const skeletonConfig = `version: 1

repositories:
  local:
    url: /tmp/squirrel-repo
    password: ${env:SQUIRREL_PASSWORD}

  # s3-example:
  #   url: s3:my-bucket/squirrel
  #   password: ${keyring:repo/s3}
  #   env:
  #     AWS_ACCESS_KEY_ID: ${env:AWS_ACCESS_KEY_ID}
  #     AWS_SECRET_ACCESS_KEY: ${env:AWS_SECRET_ACCESS_KEY}

defaults:
  retention:
    keep-daily: 7
    keep-weekly: 4
    keep-monthly: 12

profiles:
  _base:
    repository: local
    hooks:
      post-success:
        - webhook: ""   # e.g. https://hc-ping.com/your-uuid
      post-failure:
        - command: ["echo", "backup failed: ${SQUIRREL_PROFILE}"]

  files-home:
    extends: _base
    type: files
    paths:
      - /home
    excludes:
      - "*.tmp"
    schedule: "0 2 * * *"
    retention:
      keep-daily: 14
      prune: true

  # postgres-prod:
  #   extends: _base
  #   type: postgres
  #   dsn: ${keyring:db/postgres}
  #   schedule: "0 */6 * * *"

  # mysql-prod:
  #   extends: _base
  #   type: mysql
  #   dsn: ${env:MYSQL_DSN}
  #   schedule: "0 */12 * * *"
`

var configMigrateCmd = &cobra.Command{
	Use:   "migrate",
	Short: "Migrate config file to the current schema version",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		return config.Migrate(cfgPath)
	},
}

func runConfigInit(cfgPath string, force bool) error {
	if _, err := os.Stat(cfgPath); err == nil && !force {
		return fmt.Errorf("config already exists at %s (use --force to overwrite)", cfgPath)
	}
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(cfgPath, []byte(skeletonConfig), 0o600); err != nil {
		return err
	}
	fmt.Printf("config created at %s\n", cfgPath)
	return nil
}

func runConfigValidate(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	if err := config.Validate(cfg); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "repositories:\t%d\n", len(cfg.Repositories))
	fmt.Fprintf(w, "profiles:\t%d\n", len(cfg.Profiles))
	w.Flush()
	fmt.Println("config OK")
	return nil
}

func runConfigShow(cfgPath, profileName string, reveal bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	p, err := config.ResolveProfile(cfg, profileName)
	if err != nil {
		return err
	}

	// Mask secrets unless --reveal
	if !reveal {
		p.DSN = maskSecret(p.DSN)
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		// mask password in DSN URLs
		parts := strings.SplitN(s, "://", 2)
		return parts[0] + "://<masked>"
	}
	// go-mysql DSN: user:pass@tcp(...)
	if at := strings.LastIndex(s, "@"); at > 0 {
		return "<masked>@" + s[at+1:]
	}
	return "<masked>"
}
