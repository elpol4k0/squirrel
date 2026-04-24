package daemon

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/robfig/cron/v3"

	"github.com/elpol4k0/squirrel/internal/config"
)

type RunFunc func(ctx context.Context, cfg *config.Config, profileName string) error

// Blocks until SIGINT/SIGTERM. SIGHUP triggers a config reload without restart.
func Run(cfgPath string, profiles []string, runFn RunFunc) error {
	slog.Info("squirrel daemon starting")

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	c := cron.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := scheduleProfiles(c, cfg, profiles, ctx, runFn); err != nil {
		return err
	}

	c.Start()
	slog.Info("daemon running", "jobs", len(c.Entries()))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	for sig := range sigCh {
		if sig == syscall.SIGHUP {
			slog.Info("SIGHUP received, reloading config")
			c.Stop()
			newCfg, err := config.Load(cfgPath)
			if err != nil {
				slog.Error("reload config failed", "err", err)
			} else {
				cfg = newCfg
				c = cron.New()
				scheduleProfiles(c, cfg, profiles, ctx, runFn) //nolint:errcheck
				c.Start()
				slog.Info("config reloaded", "jobs", len(c.Entries()))
			}
			continue
		}
		slog.Info("shutting down daemon")
		c.Stop()
		return nil
	}
	return nil
}

func scheduleProfiles(c *cron.Cron, cfg *config.Config, selected []string, ctx context.Context, runFn RunFunc) error {
	for name, p := range cfg.Profiles {
		if p.Abstract || p.Schedule == "" {
			continue
		}
		if len(selected) > 0 && !contains(selected, name) {
			continue
		}
		profileName := name // capture
		_, err := c.AddFunc(p.Schedule, func() {
			slog.Info("running scheduled profile", "profile", profileName)
			if err := runFn(ctx, cfg, profileName); err != nil {
				slog.Error("profile failed", "profile", profileName, "err", err)
			}
		})
		if err != nil {
			return err
		}
		slog.Info("scheduled profile", "profile", name, "schedule", p.Schedule)
	}
	return nil
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
