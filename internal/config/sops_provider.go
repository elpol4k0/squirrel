package config

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// path format: "secrets.enc.yaml#db.postgres.password" (dot-path into decrypted YAML/JSON)
func resolveSops(path string) (string, error) {
	file, dotPath, _ := strings.Cut(path, "#")
	if file == "" {
		return "", fmt.Errorf("sops provider: missing file path")
	}

	args := []string{"-d"}
	if dotPath != "" {
		args = append(args, "--extract", dotPathToJQ(dotPath))
	}
	args = append(args, file)

	var out, errBuf bytes.Buffer
	cmd := exec.Command("sops", args...) //nolint:gosec
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("sops decrypt %s: %w\n%s", file, err, errBuf.String())
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}

// dotPathToJQ converts "db.postgres.password" to '["db"]["postgres"]["password"]' for sops --extract.
func dotPathToJQ(dotPath string) string {
	parts := strings.Split(dotPath, ".")
	var sb strings.Builder
	for _, p := range parts {
		sb.WriteString(`["`)
		sb.WriteString(p)
		sb.WriteString(`"]`)
	}
	return sb.String()
}
