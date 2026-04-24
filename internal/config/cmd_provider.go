package config

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// path is the raw command string, e.g. "pass show squirrel/repo"
func resolveCmd(path string) (string, error) {
	parts := strings.Fields(path)
	if len(parts) == 0 {
		return "", fmt.Errorf("cmd provider: empty command")
	}
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("cmd provider %q: %w", path, err)
	}
	return strings.TrimRight(out.String(), "\r\n"), nil
}
