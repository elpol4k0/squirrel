package hooks

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/elpol4k0/squirrel/internal/config"
)

// errors are logged but don't abort – all hooks run regardless of failures
func Run(ctx context.Context, actions []config.HookAction, env map[string]string) error {
	var errs []string
	for _, a := range actions {
		if err := runAction(ctx, a, env); err != nil {
			slog.Warn("hook failed", "err", err)
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("hook errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

func runAction(ctx context.Context, a config.HookAction, env map[string]string) error {
	if len(a.Command) > 0 {
		return runCommand(ctx, a.Command, env)
	}
	if a.Webhook != "" {
		return runWebhook(ctx, a.Webhook, env)
	}
	return nil
}

func runCommand(ctx context.Context, args []string, env map[string]string) error {
	expanded := make([]string, len(args))
	for i, a := range args {
		expanded[i] = expandEnv(a, env)
	}
	slog.Info("running hook command", "cmd", expanded)
	cmd := exec.CommandContext(ctx, expanded[0], expanded[1:]...) //nolint:gosec
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("command %v: %w (output: %s)", expanded, err, out.String())
	}
	return nil
}

func runWebhook(ctx context.Context, rawURL string, env map[string]string) error {
	u := expandEnv(rawURL, env)
	slog.Info("calling webhook", "url", u)

	var lastErr error
	backoff := []time.Duration{0, 5 * time.Second, 30 * time.Second}
	for _, wait := range backoff {
		if wait > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return fmt.Errorf("webhook request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return nil
		}
		lastErr = fmt.Errorf("webhook returned %d", resp.StatusCode)
	}
	return fmt.Errorf("webhook %s failed after retries: %w", u, lastErr)
}

func expandEnv(s string, env map[string]string) string {
	for k, v := range env {
		s = strings.ReplaceAll(s, "${"+k+"}", v)
	}
	return s
}
