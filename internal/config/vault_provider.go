package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// path format: "secret/data/prod/db#password" (KVv2) or "secret/prod/db#password" (KVv1)
func resolveVault(path string) (string, error) {
	addr := os.Getenv("VAULT_ADDR")
	if addr == "" {
		addr = "https://127.0.0.1:8200"
	}
	token := os.Getenv("VAULT_TOKEN")
	if token == "" {
		return "", fmt.Errorf("VAULT_TOKEN is not set")
	}
	return resolveVaultWithAddress(addr, token, path)
}

func resolveVaultWithAddress(addr, token, path string) (string, error) {
	if addr == "" {
		addr = "https://127.0.0.1:8200"
	}

	secretPath, field, _ := strings.Cut(path, "#")
	url := strings.TrimRight(addr, "/") + "/v1/" + secretPath

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("vault request: %w", err)
	}
	req.Header.Set("X-Vault-Token", token)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("vault fetch: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("vault returned %d for %s", resp.StatusCode, secretPath)
	}

	// Parse response – try KVv2 (data.data.<field>) then KVv1 (data.<field>)
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("vault parse response: %w", err)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("vault response missing 'data'")
	}

	// KVv2 wraps in an extra data layer
	if inner, ok := data["data"].(map[string]interface{}); ok {
		data = inner
	}

	if field == "" {
		for _, v := range data {
			if s, ok := v.(string); ok {
				return s, nil
			}
		}
		return "", fmt.Errorf("no string field found in vault secret %s", secretPath)
	}

	val, ok := data[field]
	if !ok {
		return "", fmt.Errorf("vault secret %s has no field %q", secretPath, field)
	}
	s, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("vault field %q is not a string", field)
	}
	return s, nil
}
