package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const Version = 1

type Config struct {
	Version      int                   `koanf:"version"`
	Repositories map[string]RepoCfg    `koanf:"repositories"`
	Defaults     DefaultsCfg           `koanf:"defaults"`
	Profiles     map[string]ProfileCfg `koanf:"profiles"`
}

type RepoCfg struct {
	URL          string            `koanf:"url"`
	Password     string            `koanf:"password"` // may contain ${provider:path}
	PasswordFile string            `koanf:"password-file"`
	Env          map[string]string `koanf:"env"`
}

type DefaultsCfg struct {
	Retention RetentionCfg `koanf:"retention"`
}

type ProfileCfg struct {
	Extends    string `koanf:"extends"`
	Abstract   bool   `koanf:"abstract"` // profiles starting with _ are abstract by default
	Repository string `koanf:"repository"`
	Type       string `koanf:"type"` // "files" | "postgres" | "mysql"

	Paths    []string `koanf:"paths"`
	Excludes []string `koanf:"excludes"`

	DSN       string   `koanf:"dsn"`
	Databases []string `koanf:"databases"`
	Slot      string   `koanf:"slot"`

	// schedule: cron expression or @daily/@hourly/etc.
	Schedule string `koanf:"schedule"`

	Tags      []string     `koanf:"tags"`
	Retention RetentionCfg `koanf:"retention"`
	Hooks     HooksCfg     `koanf:"hooks"`
}

type RetentionCfg struct {
	KeepLast    int  `koanf:"keep-last"`
	KeepHourly  int  `koanf:"keep-hourly"`
	KeepDaily   int  `koanf:"keep-daily"`
	KeepWeekly  int  `koanf:"keep-weekly"`
	KeepMonthly int  `koanf:"keep-monthly"`
	KeepYearly  int  `koanf:"keep-yearly"`
	Prune       bool `koanf:"prune"`
}

type HooksCfg struct {
	PreBackup   []HookAction `koanf:"pre-backup"`
	PostSuccess []HookAction `koanf:"post-success"`
	PostFailure []HookAction `koanf:"post-failure"`
}

type HookAction struct {
	Command []string `koanf:"command"`
	Webhook string   `koanf:"webhook"`
}

func DefaultConfigPath() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("APPDATA")
		if base == "" {
			base = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(base, "squirrel", "config.yml")
	case "darwin":
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "squirrel", "config.yml")
	default: // linux
		if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
			return filepath.Join(xdg, "squirrel", "config.yml")
		}
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".config", "squirrel", "config.yml")
	}
}

func ResolveProfile(cfg *Config, name string) (ProfileCfg, error) {
	visited := map[string]bool{}
	return resolveProfile(cfg, name, visited)
}

func resolveProfile(cfg *Config, name string, visited map[string]bool) (ProfileCfg, error) {
	if visited[name] {
		return ProfileCfg{}, fmt.Errorf("circular profile inheritance at %q", name)
	}
	visited[name] = true

	p, ok := cfg.Profiles[name]
	if !ok {
		return ProfileCfg{}, fmt.Errorf("profile %q not found", name)
	}

	if p.Extends == "" {
		return p, nil
	}

	parent, err := resolveProfile(cfg, p.Extends, visited)
	if err != nil {
		return ProfileCfg{}, err
	}
	return mergeProfiles(parent, p), nil
}

// non-zero child fields win over parent
func mergeProfiles(parent, child ProfileCfg) ProfileCfg {
	result := parent
	if child.Repository != "" {
		result.Repository = child.Repository
	}
	if child.Type != "" {
		result.Type = child.Type
	}
	if len(child.Paths) > 0 {
		result.Paths = child.Paths
	}
	if len(child.Excludes) > 0 {
		result.Excludes = child.Excludes
	}
	if child.DSN != "" {
		result.DSN = child.DSN
	}
	if len(child.Databases) > 0 {
		result.Databases = child.Databases
	}
	if child.Slot != "" {
		result.Slot = child.Slot
	}
	if child.Schedule != "" {
		result.Schedule = child.Schedule
	}
	if len(child.Tags) > 0 {
		result.Tags = child.Tags
	}
	result.Retention = mergeRetention(parent.Retention, child.Retention)
	result.Hooks = mergeHooks(parent.Hooks, child.Hooks)
	result.Abstract = child.Abstract
	result.Extends = ""
	return result
}

func mergeRetention(parent, child RetentionCfg) RetentionCfg {
	r := parent
	if child.KeepLast > 0 {
		r.KeepLast = child.KeepLast
	}
	if child.KeepHourly > 0 {
		r.KeepHourly = child.KeepHourly
	}
	if child.KeepDaily > 0 {
		r.KeepDaily = child.KeepDaily
	}
	if child.KeepWeekly > 0 {
		r.KeepWeekly = child.KeepWeekly
	}
	if child.KeepMonthly > 0 {
		r.KeepMonthly = child.KeepMonthly
	}
	if child.KeepYearly > 0 {
		r.KeepYearly = child.KeepYearly
	}
	if child.Prune {
		r.Prune = true
	}
	return r
}

func mergeHooks(parent, child HooksCfg) HooksCfg {
	r := parent
	if len(child.PreBackup) > 0 {
		r.PreBackup = child.PreBackup
	}
	if len(child.PostSuccess) > 0 {
		r.PostSuccess = child.PostSuccess
	}
	if len(child.PostFailure) > 0 {
		r.PostFailure = child.PostFailure
	}
	return r
}
