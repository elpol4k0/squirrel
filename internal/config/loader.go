package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/99designs/keyring"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"
)

// Secrets in ${provider:path} syntax are resolved before returning.
func Load(path string) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("load config %s: %w", path, err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if cfg.Version != Version {
		return nil, fmt.Errorf("unsupported config version %d (expected %d)", cfg.Version, Version)
	}

	if err := resolveSecrets(&cfg); err != nil {
		return nil, fmt.Errorf("resolve secrets: %w", err)
	}

	return &cfg, nil
}

func Validate(cfg *Config) error {
	for name, p := range cfg.Profiles {
		if p.Extends != "" {
			if _, ok := cfg.Profiles[p.Extends]; !ok {
				return fmt.Errorf("profile %q extends unknown profile %q", name, p.Extends)
			}
		}
		if p.Repository != "" {
			if _, ok := cfg.Repositories[p.Repository]; !ok {
				return fmt.Errorf("profile %q references unknown repository %q", name, p.Repository)
			}
		}
	}
	return nil
}

func RepoPassword(r RepoCfg) ([]byte, error) {
	if r.PasswordFile != "" {
		data, err := os.ReadFile(r.PasswordFile)
		if err != nil {
			return nil, fmt.Errorf("read password-file: %w", err)
		}
		return []byte(strings.TrimRight(string(data), "\r\n")), nil
	}
	if r.Password != "" {
		return []byte(r.Password), nil
	}
	return nil, fmt.Errorf("no password configured for repository")
}

func resolveSecrets(cfg *Config) error {
	for k, repo := range cfg.Repositories {
		pw, err := resolveToken(repo.Password)
		if err != nil {
			return fmt.Errorf("repository %q password: %w", k, err)
		}
		repo.Password = pw
		for envK, envV := range repo.Env {
			v, err := resolveToken(envV)
			if err != nil {
				return fmt.Errorf("repository %q env %q: %w", k, envK, err)
			}
			repo.Env[envK] = v
		}
		cfg.Repositories[k] = repo
	}
	for k, p := range cfg.Profiles {
		dsn, err := resolveToken(p.DSN)
		if err != nil {
			return fmt.Errorf("profile %q dsn: %w", k, err)
		}
		p.DSN = dsn
		cfg.Profiles[k] = p
	}
	return nil
}

// returns s unchanged if it doesn't start with ${.
func resolveToken(s string) (string, error) {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return s, nil
	}
	inner := s[2 : len(s)-1]
	provider, path, _ := strings.Cut(inner, ":")

	switch provider {
	case "env":
		v := os.Getenv(path)
		if v == "" {
			return "", fmt.Errorf("env var %q is empty", path)
		}
		return v, nil

	case "file":
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read file %q: %w", path, err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil

	case "keyring":
		service, key, _ := strings.Cut(path, "/")
		ring, err := keyring.Open(keyring.Config{
			ServiceName: "squirrel-" + service,
		})
		if err != nil {
			return "", fmt.Errorf("open keyring: %w", err)
		}
		item, err := ring.Get(key)
		if err != nil {
			return "", fmt.Errorf("keyring get %s/%s: %w", service, key, err)
		}
		return string(item.Data), nil

	case "cmd":
		return resolveCmd(path)

	case "vault":
		return resolveVault(path)

	case "sops":
		return resolveSops(path)

	default:
		return "", fmt.Errorf("unknown secret provider %q (available: env, file, keyring, cmd, vault, sops)", provider)
	}
}
