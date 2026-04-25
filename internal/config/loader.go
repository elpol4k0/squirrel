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

// resolves ${provider:path} secrets before returning
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
	r := &tokenResolver{named: cfg.Secrets}
	for k, repo := range cfg.Repositories {
		pw, err := r.resolve(repo.Password)
		if err != nil {
			return fmt.Errorf("repository %q password: %w", k, err)
		}
		repo.Password = pw
		for envK, envV := range repo.Env {
			v, err := r.resolve(envV)
			if err != nil {
				return fmt.Errorf("repository %q env %q: %w", k, envK, err)
			}
			repo.Env[envK] = v
		}
		cfg.Repositories[k] = repo
	}
	for k, p := range cfg.Profiles {
		dsn, err := r.resolve(p.DSN)
		if err != nil {
			return fmt.Errorf("profile %q dsn: %w", k, err)
		}
		p.DSN = dsn
		cfg.Profiles[k] = p
	}
	return nil
}

type tokenResolver struct {
	named map[string]SecretProviderCfg
}

func (r *tokenResolver) resolve(s string) (string, error) {
	if !strings.HasPrefix(s, "${") || !strings.HasSuffix(s, "}") {
		return s, nil
	}
	inner := s[2 : len(s)-1]
	provider, path, _ := strings.Cut(inner, ":")

	// 1Password native URI: ${op://vault/item/field}
	if strings.HasPrefix(inner, "op://") {
		return resolveOp(inner)
	}

	// named provider from the secrets: section takes priority
	if prov, ok := r.named[provider]; ok {
		return r.resolveNamed(prov, path)
	}

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

	case "age":
		return resolveAge(path)

	case "op":
		return resolveOp("op://" + path)

	default:
		return "", fmt.Errorf("unknown secret provider %q (available: env, file, keyring, cmd, vault, sops, age, op, or a named provider from secrets:)", provider)
	}
}

func (r *tokenResolver) resolveNamed(prov SecretProviderCfg, path string) (string, error) {
	switch prov.Type {
	case "sops":
		// path is the dot-path inside the decrypted file, e.g. "db/postgres/dsn" → "db.postgres.dsn"
		dotPath := strings.ReplaceAll(path, "/", ".")
		return resolveSops(prov.File + "#" + dotPath)

	case "vault":
		token, err := r.resolveVaultToken(prov.TokenFrom)
		if err != nil {
			return "", fmt.Errorf("vault token: %w", err)
		}
		return resolveVaultWithAddress(prov.Address, token, path)

	case "keyring":
		svc := prov.Service
		if svc == "" {
			svc = "squirrel"
		}
		ring, err := keyring.Open(keyring.Config{ServiceName: svc})
		if err != nil {
			return "", fmt.Errorf("open keyring: %w", err)
		}
		item, err := ring.Get(path)
		if err != nil {
			return "", fmt.Errorf("keyring get %s: %w", path, err)
		}
		return string(item.Data), nil

	case "age":
		// path is ignored; the whole file is decrypted and trimmed
		return resolveAge(prov.File)

	case "file":
		data, err := os.ReadFile(prov.File)
		if err != nil {
			return "", fmt.Errorf("read file %q: %w", prov.File, err)
		}
		return strings.TrimRight(string(data), "\r\n"), nil

	case "env":
		v := os.Getenv(path)
		if v == "" {
			return "", fmt.Errorf("env var %q is empty", path)
		}
		return v, nil

	default:
		return "", fmt.Errorf("named provider has unknown type %q", prov.Type)
	}
}

func (r *tokenResolver) resolveVaultToken(tokenFrom string) (string, error) {
	if tokenFrom == "" {
		if t := os.Getenv("VAULT_TOKEN"); t != "" {
			return t, nil
		}
		return "", fmt.Errorf("no vault token configured (set token-from or VAULT_TOKEN)")
	}
	provider, val, _ := strings.Cut(tokenFrom, ":")
	if provider == "env" {
		t := os.Getenv(val)
		if t == "" {
			return "", fmt.Errorf("vault token env var %q is empty", val)
		}
		return t, nil
	}
	return "", fmt.Errorf("unsupported token-from format %q (use env:VAR)", tokenFrom)
}

// resolveToken is the package-level helper for callers that have no named providers.
func resolveToken(s string) (string, error) {
	return (&tokenResolver{named: map[string]SecretProviderCfg{}}).resolve(s)
}
