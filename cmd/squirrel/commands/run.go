package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/elpol4k0/squirrel/internal/config"
	"github.com/elpol4k0/squirrel/internal/hooks"
	"github.com/elpol4k0/squirrel/internal/repo"
)

var runCmd = &cobra.Command{
	Use:   "run <profile> [profile...]",
	Short: "Run one or more backup profiles from the config file",
	Example: `  squirrel run prod-postgres
  squirrel run prod-postgres prod-mysql --config /etc/squirrel/config.yml`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfgPath, _ := cmd.Flags().GetString("config")
		parallel, _ := cmd.Flags().GetInt("parallel")
		return runProfiles(cfgPath, args, parallel)
	},
}

func init() {
	runCmd.Flags().String("config", config.DefaultConfigPath(), "config file path")
	runCmd.Flags().Int("parallel", 1, "number of profiles to run in parallel")
}

func runProfiles(cfgPath string, names []string, parallel int) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if parallel <= 1 {
		for _, name := range names {
			if err := RunProfile(ctx, cfg, name); err != nil {
				return fmt.Errorf("profile %s: %w", name, err)
			}
		}
		return nil
	}

	type result struct {
		name string
		err  error
	}
	sem := make(chan struct{}, parallel)
	results := make(chan result, len(names))

	for _, name := range names {
		n := name
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			results <- result{n, RunProfile(ctx, cfg, n)}
		}()
	}
	// drain
	for range names {
		r := <-results
		if r.err != nil {
			slog.Error("profile failed", "profile", r.name, "err", r.err)
		}
	}
	return nil
}

func RunProfile(ctx context.Context, cfg *config.Config, name string) error {
	p, err := config.ResolveProfile(cfg, name)
	if err != nil {
		return err
	}
	if p.Abstract {
		return fmt.Errorf("profile %q is abstract and cannot be run directly", name)
	}

	repoCfg, ok := cfg.Repositories[p.Repository]
	if !ok {
		return fmt.Errorf("profile %q: repository %q not found", name, p.Repository)
	}

	for k, v := range repoCfg.Env {
		os.Setenv(k, v) //nolint:errcheck
	}

	hookEnv := map[string]string{
		"SQUIRREL_PROFILE": name,
		"SQUIRREL_REPO":    repoCfg.URL,
	}

	if err := hooks.Run(ctx, p.Hooks.PreBackup, hookEnv); err != nil {
		slog.Warn("pre-backup hook failed", "profile", name, "err", err)
	}

	password, err := config.RepoPassword(repoCfg)
	if err != nil {
		return err
	}

	backupErr := dispatchProfile(ctx, cfg, p, name, repoCfg.URL, password)

	if backupErr != nil {
		slog.Error("backup failed", "profile", name, "err", backupErr)
		hooks.Run(ctx, p.Hooks.PostFailure, hookEnv) //nolint:errcheck
		return backupErr
	}

	hooks.Run(ctx, p.Hooks.PostSuccess, hookEnv) //nolint:errcheck

	if p.Retention.Prune && hasRetention(p.Retention) {
		slog.Info("applying retention policy", "profile", name)
		if err := applyRetention(ctx, repoCfg.URL, password, p.Retention); err != nil {
			slog.Error("retention failed", "profile", name, "err", err)
		}
	}
	return nil
}

func dispatchProfile(ctx context.Context, cfg *config.Config, p config.ProfileCfg, name, repoURL string, password []byte) error {
	slog.Info("running profile", "profile", name, "type", p.Type)

	switch strings.ToLower(p.Type) {
	case "files", "":
		return runFileBackupFromProfile(ctx, repoURL, password, p)
	case "postgres", "postgresql":
		return runPGBackupFromProfile(ctx, repoURL, password, p)
	case "mysql", "mariadb":
		return runMySQLBackupFromProfile(ctx, repoURL, password, p)
	default:
		return fmt.Errorf("unknown backup type %q", p.Type)
	}
}

func runFileBackupFromProfile(ctx context.Context, repoURL string, password []byte, p config.ProfileCfg) error {
	r, err := repo.Open(repoURL, password)
	if err != nil {
		return fmt.Errorf("open repo: %w", err)
	}
	for _, path := range p.Paths {
		if err := runBackup(repoURL, path, false, p.Tags, 0); err != nil {
			return err
		}
		_ = r
	}
	return nil
}

func runPGBackupFromProfile(ctx context.Context, repoURL string, password []byte, p config.ProfileCfg) error {
	slot := p.Slot
	if slot == "" {
		slot = "squirrel"
	}
	_ = password
	return runPGBackup(repoURL, p.DSN, slot, p.Tags, false)
}

func runMySQLBackupFromProfile(ctx context.Context, repoURL string, password []byte, p config.ProfileCfg) error {
	_ = password
	return runMySQLBackup(repoURL, p.DSN, p.Databases, p.Tags, false)
}

func hasRetention(r config.RetentionCfg) bool {
	return r.KeepLast > 0 || r.KeepHourly > 0 || r.KeepDaily > 0 ||
		r.KeepWeekly > 0 || r.KeepMonthly > 0 || r.KeepYearly > 0
}

func applyRetention(ctx context.Context, repoURL string, password []byte, r config.RetentionCfg) error {
	rep, err := repo.Open(repoURL, password)
	if err != nil {
		return err
	}
	snaps, err := rep.ListSnapshots(ctx)
	if err != nil {
		return err
	}
	policy := repo.RetentionPolicy{
		KeepLast:    r.KeepLast,
		KeepDaily:   r.KeepDaily,
		KeepWeekly:  r.KeepWeekly,
		KeepMonthly: r.KeepMonthly,
		KeepYearly:  r.KeepYearly,
	}
	_, remove := policy.Apply(snaps)
	for _, s := range remove {
		if err := rep.DeleteSnapshot(ctx, s.ID); err != nil {
			slog.Warn("delete snapshot failed", "id", s.ID, "err", err)
		}
	}
	if r.Prune {
		if _, _, err := rep.Prune(ctx); err != nil {
			return err
		}
	}
	return nil
}
