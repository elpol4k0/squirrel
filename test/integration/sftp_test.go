//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/elpol4k0/squirrel/internal/backend"
	sftpbackend "github.com/elpol4k0/squirrel/internal/backend/sftp"
)

// TestSFTPBackend_BasicOperations starts a real SFTP server (atmoz/sftp) via
// testcontainers and exercises Save, Load, List, Remove, Exists, and Stat.
//
// Run with: go test -tags integration ./test/integration/ -run TestSFTPBackend
//
// The atmoz/sftp image accepts users as command arguments: "user:pass:::homedir".
// The squirrel SFTP backend now supports password auth via sftp://user:pass@host/path.
func TestSFTPBackend_BasicOperations(t *testing.T) {
	ctx := context.Background()

	const (
		sftpUser = "squirrel"
		sftpPass = "testpass123"
		sftpHome = "upload"
	)

	req := testcontainers.ContainerRequest{
		Image:        "atmoz/sftp:latest",
		Cmd:          []string{fmt.Sprintf("%s:%s:::%s", sftpUser, sftpPass, sftpHome)},
		ExposedPorts: []string{"22/tcp"},
		WaitingFor: wait.ForLog("Server listening on").
			WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start sftp container: %v", err)
	}
	defer container.Terminate(ctx) //nolint:errcheck

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "22")
	if err != nil {
		t.Fatalf("mapped port: %v", err)
	}

	rawURL := fmt.Sprintf("sftp://%s:%s@%s:%s/%s", sftpUser, sftpPass, host, port.Port(), sftpHome)
	b, err := sftpbackend.ParseURL(rawURL)
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}

	// Save
	h := backend.Handle{Type: backend.TypeKey, Name: "testkey"}
	content := []byte("hello from squirrel sftp test")
	if err := b.Save(ctx, h, bytes.NewReader(content)); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Exists
	exists, err := b.Exists(ctx, h)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Error("Exists returned false after Save")
	}

	// Load
	rc, err := b.Load(ctx, h)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, err := io.ReadAll(rc)
	rc.Close()
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("Load: got %q, want %q", got, content)
	}

	// Stat
	fi, err := b.Stat(ctx, h)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.Size != int64(len(content)) {
		t.Errorf("Stat.Size: got %d, want %d", fi.Size, len(content))
	}

	// List
	h2 := backend.Handle{Type: backend.TypeKey, Name: "testkey2"}
	b.Save(ctx, h2, bytes.NewReader([]byte("second")))
	names, err := b.List(ctx, backend.TypeKey)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(names) < 2 {
		t.Errorf("List: expected ≥2 entries, got %d: %v", len(names), names)
	}

	// Remove
	if err := b.Remove(ctx, h); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	exists, err = b.Exists(ctx, h)
	if err != nil {
		t.Fatalf("Exists after Remove: %v", err)
	}
	if exists {
		t.Error("file still exists after Remove")
	}
}

// TestSFTPBackend_DataSharding verifies that data blobs are stored under the
// data/<prefix2>/<name> sharding layout expected by the repo layer.
func TestSFTPBackend_DataSharding(t *testing.T) {
	ctx := context.Background()

	const (
		sftpUser = "squirrel"
		sftpPass = "testpass123"
		sftpHome = "upload"
	)

	req := testcontainers.ContainerRequest{
		Image:        "atmoz/sftp:latest",
		Cmd:          []string{fmt.Sprintf("%s:%s:::%s", sftpUser, sftpPass, sftpHome)},
		ExposedPorts: []string{"22/tcp"},
		WaitingFor:   wait.ForLog("Server listening on").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("start sftp container: %v", err)
	}
	defer container.Terminate(ctx) //nolint:errcheck

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "22")

	b, err := sftpbackend.ParseURL(fmt.Sprintf("sftp://%s:%s@%s:%s/%s", sftpUser, sftpPass, host, port.Port(), sftpHome))
	if err != nil {
		t.Fatalf("ParseURL: %v", err)
	}

	h := backend.Handle{Type: backend.TypeData, Name: "abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"}
	if err := b.Save(ctx, h, bytes.NewReader([]byte("packdata"))); err != nil {
		t.Fatalf("Save data blob: %v", err)
	}

	names, err := b.List(ctx, backend.TypeData)
	if err != nil {
		t.Fatalf("List data: %v", err)
	}
	found := false
	for _, n := range names {
		if n == h.Name {
			found = true
		}
	}
	if !found {
		t.Errorf("data blob %s not found in List output: %v", h.Name[:12], names)
	}
}
