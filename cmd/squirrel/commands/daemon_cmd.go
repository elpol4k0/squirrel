package commands

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/config"
	"github.com/elpol4k0/squirrel/internal/daemon"
	"github.com/elpol4k0/squirrel/internal/metrics"
)

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Run squirrel as a background scheduler (for containers / systemd without timers)",
	Example: `  squirrel daemon
  squirrel daemon --profile prod-postgres --profile prod-mysql --metrics :9090`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		profiles, _ := cmd.Flags().GetStringArray("profile")
		metricsAddr, _ := cmd.Flags().GetString("metrics")

		if metricsAddr != "" {
			go func() {
				slog.Info("metrics server starting", "addr", metricsAddr)
				if err := metrics.Serve(metricsAddr); err != nil {
					slog.Error("metrics server error", "err", err)
				}
			}()
		}

		return daemon.Run(cfgPath, profiles, func(ctx context.Context, cfg *config.Config, profileName string) error {
			return RunProfile(ctx, cfg, profileName)
		})
	},
}

func init() {
	daemonCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")
	daemonCmd.Flags().StringArray("profile", nil, "profiles to schedule (default: all with a schedule)")
	daemonCmd.Flags().String("metrics", "", "address to serve Prometheus metrics on (e.g. :9090)")

	_ = fmt.Sprintf // keep fmt import used
}
