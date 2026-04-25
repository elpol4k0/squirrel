package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAutoAbstract_UnderscorePrefix(t *testing.T) {
	cfg := &Config{
		Version: Version,
		Repositories: map[string]RepoCfg{
			"local": {URL: "/tmp/repo", Password: "pw"},
		},
		Profiles: map[string]ProfileCfg{
			"_base": {
				Repository: "local",
				Type:       "files",
			},
			"prod": {
				Extends:    "_base",
				Repository: "local",
			},
		},
	}

	base, err := ResolveProfile(cfg, "_base")
	if err != nil {
		t.Fatalf("ResolveProfile _base: %v", err)
	}
	if !base.Abstract {
		t.Error("_base should be automatically abstract")
	}

	prod, err := ResolveProfile(cfg, "prod")
	if err != nil {
		t.Fatalf("ResolveProfile prod: %v", err)
	}
	if prod.Abstract {
		t.Error("prod should not be abstract")
	}
}

func TestProfileInheritance_Compression(t *testing.T) {
	cfg := &Config{
		Version: Version,
		Defaults: DefaultsCfg{
			Compression:      "zstd",
			CompressionLevel: 3,
		},
	}
	if cfg.Defaults.Compression != "zstd" {
		t.Errorf("Compression: got %q, want zstd", cfg.Defaults.Compression)
	}
	if cfg.Defaults.CompressionLevel != 3 {
		t.Errorf("CompressionLevel: got %d, want 3", cfg.Defaults.CompressionLevel)
	}
}

func TestNamedSecretProvider_Env(t *testing.T) {
	t.Setenv("TEST_DB_DSN", "postgres://user:pw@localhost/db")

	r := &tokenResolver{
		named: map[string]SecretProviderCfg{
			"my-secrets": {Type: "env"},
		},
	}

	got, err := r.resolve("${my-secrets:TEST_DB_DSN}")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "postgres://user:pw@localhost/db" {
		t.Errorf("got %q, want postgres://user:pw@localhost/db", got)
	}
}

func TestNamedSecretProvider_File(t *testing.T) {
	dir := t.TempDir()
	secretFile := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secretFile, []byte("supersecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &tokenResolver{
		named: map[string]SecretProviderCfg{
			"files": {Type: "file", File: secretFile},
		},
	}

	got, err := r.resolve("${files:ignored-path}")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "supersecret" {
		t.Errorf("got %q, want supersecret", got)
	}
}

func TestNamedSecretProvider_UnknownType(t *testing.T) {
	r := &tokenResolver{
		named: map[string]SecretProviderCfg{
			"bad": {Type: "nonexistent"},
		},
	}
	_, err := r.resolve("${bad:some/path}")
	if err == nil {
		t.Error("expected error for unknown provider type, got nil")
	}
}

func TestProfileInheritance_AbstractBlocked(t *testing.T) {
	cfg := &Config{
		Version: Version,
		Profiles: map[string]ProfileCfg{
			"_base": {Type: "files"},
		},
	}
	p, err := ResolveProfile(cfg, "_base")
	if err != nil {
		t.Fatalf("ResolveProfile: %v", err)
	}
	if !p.Abstract {
		t.Error("_base should be abstract")
	}
}

func TestResolveProfile_CircularDetected(t *testing.T) {
	cfg := &Config{
		Version: Version,
		Profiles: map[string]ProfileCfg{
			"a": {Extends: "b"},
			"b": {Extends: "a"},
		},
	}
	_, err := ResolveProfile(cfg, "a")
	if err == nil {
		t.Error("expected circular dependency error, got nil")
	}
}
