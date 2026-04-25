package commands

import (
	"bytes"
	"strings"
	"testing"
)

func TestCompletion_Output(t *testing.T) {
	shells := []struct {
		name   string
		marker string
	}{
		{"bash", "__squirrel"},
		{"zsh", "squirrel"},
		{"fish", "squirrel"},
		{"powershell", "squirrel"},
	}

	for _, tc := range shells {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			completionCmd.SetOut(&buf)
			completionCmd.SetArgs([]string{tc.name})
			if err := completionCmd.RunE(completionCmd, []string{tc.name}); err != nil {
				t.Fatalf("completion %s: %v", tc.name, err)
			}
			out := buf.String()
			if !strings.Contains(out, tc.marker) {
				t.Errorf("completion %s: output missing %q (got %d bytes)", tc.name, tc.marker, len(out))
			}
		})
	}
}

func TestCompletion_UnknownShell(t *testing.T) {
	err := completionCmd.RunE(completionCmd, []string{"tcsh"})
	if err == nil {
		t.Error("expected error for unknown shell, got nil")
	}
	if !strings.Contains(err.Error(), "tcsh") {
		t.Errorf("error should mention the invalid shell name, got: %v", err)
	}
}
