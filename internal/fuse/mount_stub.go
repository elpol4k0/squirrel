//go:build !linux && !darwin

package fuse

import (
	"context"
	"fmt"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func Mount(ctx context.Context, r *repo.Repo, snapID, mountPoint string) error {
	return fmt.Errorf("squirrel mount is not supported on this platform; use Linux or macOS (or install WinFsp on Windows and rebuild)")
}
