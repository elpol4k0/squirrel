package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// A backup of the original is written to <path>.bak.<timestamp> before any change.
func Migrate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	cfg, err := Load(path)
	if err != nil {
		// Try to parse despite errors to get version
		return fmt.Errorf("cannot read config version: %w", err)
	}

	if cfg.Version == Version {
		fmt.Printf("config is already at version %d, nothing to migrate\n", Version)
		return nil
	}
	if cfg.Version > Version {
		return fmt.Errorf("config version %d is newer than this binary (%d); upgrade squirrel", cfg.Version, Version)
	}

	bak := fmt.Sprintf("%s.bak.%s", path, time.Now().Format("20060102T150405"))
	if err := os.WriteFile(bak, data, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}
	fmt.Printf("backup written to %s\n", filepath.Base(bak))

	for from := cfg.Version; from < Version; from++ {
		if err := applyMigration(path, from); err != nil {
			return fmt.Errorf("migration v%d→v%d: %w", from, from+1, err)
		}
		fmt.Printf("migrated v%d → v%d\n", from, from+1)
	}
	return nil
}

// applyMigration applies the migration from version `from` to `from+1`.
// Add cases here as the config schema evolves.
func applyMigration(path string, from int) error {
	switch from {
	// case 1:
	//   return migrateV1toV2(path)
	default:
		return fmt.Errorf("no migration defined for v%d→v%d", from, from+1)
	}
}
