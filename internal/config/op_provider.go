package config

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// uri must be a full op:// reference, e.g. op://vault/item/field
func resolveOp(uri string) (string, error) {
	out, err := exec.Command("op", "read", uri).Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg := strings.TrimSpace(string(exitErr.Stderr))
			if msg != "" {
				return "", fmt.Errorf("op read %s: %s", uri, msg)
			}
		}
		return "", fmt.Errorf("op read %s: %w (is the 1Password CLI installed and signed in?)", uri, err)
	}
	return strings.TrimRight(string(out), "\r\n"), nil
}
