package commands

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/config"
	"github.com/elpol4k0/squirrel/internal/schedule"
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage scheduled backup profiles",
}

var scheduleInstallCmd = &cobra.Command{
	Use:   "install <profile> [profile...]",
	Short: "Install systemd timer / launchd plist / Windows Task for profiles",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		return runScheduleInstall(cfgPath, args)
	},
}

var scheduleRemoveCmd = &cobra.Command{
	Use:   "remove <profile> [profile...]",
	Short: "Remove scheduled entries for profiles",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		for _, name := range args {
			if err := schedule.Remove(name); err != nil {
				return err
			}
		}
		return nil
	},
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed schedule entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		names, err := schedule.List()
		if err != nil {
			return err
		}
		if len(names) == 0 {
			fmt.Println("no squirrel schedule entries installed")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME")
		for _, n := range names {
			fmt.Fprintln(w, n)
		}
		w.Flush()
		return nil
	},
}

func init() {
	scheduleInstallCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")

	scheduleCmd.AddCommand(scheduleInstallCmd, scheduleRemoveCmd, scheduleListCmd)
}

func runScheduleInstall(cfgPath string, profileNames []string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	bin, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine binary path: %w", err)
	}

	for _, name := range profileNames {
		p, err := config.ResolveProfile(cfg, name)
		if err != nil {
			return err
		}
		if p.Schedule == "" {
			return fmt.Errorf("profile %q has no schedule defined", name)
		}
		e := schedule.Entry{
			Profile:     name,
			Schedule:    p.Schedule,
			BinaryPath:  bin,
			ConfigPath:  cfgPath,
			Description: fmt.Sprintf("squirrel backup: %s", name),
		}
		if err := schedule.Install(e); err != nil {
			return fmt.Errorf("install schedule for %s: %w", name, err)
		}
	}
	return nil
}
