//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/elpol4k0/squirrel/internal/db/postgres"
	"github.com/elpol4k0/squirrel/internal/repo"
)

func pgVersion() string {
	if v := os.Getenv("PG_VERSION"); v != "" {
		return v
	}
	return "17"
}

func startPostgres(t *testing.T) (dsn string, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	image := fmt.Sprintf("postgres:%s-alpine", pgVersion())

	container, err := tcpostgres.RunContainer(ctx,
		testcontainers.WithImage(image),
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("squirrel"),
		tcpostgres.WithPassword("squirrel"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	// Replication requires wal_level >= replica and the user must have REPLICATION privilege.
	setupReplication(t, connStr)

	replDSN := connStr + " replication=database"
	cleanup = func() { container.Terminate(ctx) } //nolint:errcheck
	return replDSN, cleanup
}

func setupReplication(t *testing.T, connStr string) {
	t.Helper()
	ctx := context.Background()

	conn, err := pgx.Connect(ctx, connStr)
	if err != nil {
		t.Fatalf("connect for setup: %v", err)
	}
	defer conn.Close(ctx)

	for _, stmt := range []string{
		"ALTER SYSTEM SET wal_level = replica",
		"ALTER USER squirrel REPLICATION",
	} {
		if _, err := conn.Exec(ctx, stmt); err != nil {
			t.Logf("setup stmt %q: %v (may be expected)", stmt, err)
		}
	}
}

func TestPostgres_IdentifySystem(t *testing.T) {
	dsn, cleanup := startPostgres(t)
	defer cleanup()

	adapter := postgres.New(dsn)
	ctx := context.Background()

	sysident, err := adapter.IdentifySystem(ctx)
	if err != nil {
		t.Fatalf("IdentifySystem: %v", err)
	}

	if sysident.SystemID == "" {
		t.Error("SystemID should not be empty")
	}
	t.Logf("systemID=%s timeline=%d xlogpos=%s", sysident.SystemID, sysident.Timeline, sysident.XLogPos)
}

func TestPostgres_BaseBackup(t *testing.T) {
	dsn, cleanup := startPostgres(t)
	defer cleanup()

	repoDir := t.TempDir()
	if err := repo.InitWithPassword(repoDir, []byte("testpw")); err != nil {
		t.Fatalf("InitWithPassword: %v", err)
	}

	r, err := repo.Open(repoDir, []byte("testpw"))
	if err != nil {
		t.Fatalf("Open repo: %v", err)
	}

	adapter := postgres.New(dsn)
	ctx := context.Background()

	startLSN, sysident, treeID, err := adapter.BaseBackup(ctx, r)
	if err != nil {
		t.Fatalf("BaseBackup: %v", err)
	}
	t.Logf("startLSN=%s systemID=%s treeID=%s", startLSN, sysident.SystemID, treeID)

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	// Verify a snapshot was written with the expected metadata.
	snap, _ := repo.NewSnapshot([]string{"postgres"}, nil)
	snap.Tree = treeID
	snap.Meta = map[string]string{
		"type":      "postgres-base",
		"start_lsn": startLSN.String(),
		"system_id": sysident.SystemID,
	}
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	snaps, err := r.ListSnapshots(ctx)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Meta["type"] != "postgres-base" {
		t.Errorf("snapshot type: got %q, want %q", snaps[0].Meta["type"], "postgres-base")
	}
}

func TestPostgres_SnapshotCountAfterMultipleBackups(t *testing.T) {
	dsn, cleanup := startPostgres(t)
	defer cleanup()

	repoDir := t.TempDir()
	repo.InitWithPassword(repoDir, []byte("pw"))
	r, _ := repo.Open(repoDir, []byte("pw"))

	adapter := postgres.New(dsn)
	ctx := context.Background()

	for i := range 2 {
		_, _, treeID, err := adapter.BaseBackup(ctx, r)
		if err != nil {
			t.Fatalf("BaseBackup %d: %v", i, err)
		}
		r.Flush(ctx)
		snap, _ := repo.NewSnapshot([]string{"postgres"}, nil)
		snap.Tree = treeID
		r.SaveSnapshot(ctx, snap)
	}

	snaps, _ := r.ListSnapshots(ctx)
	if len(snaps) != 2 {
		t.Errorf("expected 2 snapshots, got %d", len(snaps))
	}
}
