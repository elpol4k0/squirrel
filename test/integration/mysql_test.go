//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/testcontainers/testcontainers-go"
	tcmysql "github.com/testcontainers/testcontainers-go/modules/mysql"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/elpol4k0/squirrel/internal/db/mysql"
	"github.com/elpol4k0/squirrel/internal/repo"
)

func mysqlVersion() string {
	if v := os.Getenv("MYSQL_VERSION"); v != "" {
		return v
	}
	return "8"
}

func startMySQL(t *testing.T) (dsn string, cleanup func()) {
	t.Helper()
	ctx := context.Background()

	image := fmt.Sprintf("mysql:%s", mysqlVersion())

	container, err := tcmysql.RunContainer(ctx,
		testcontainers.WithImage(image),
		tcmysql.WithDatabase("testdb"),
		tcmysql.WithUsername("squirrel"),
		tcmysql.WithPassword("squirrel"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("port: 3306  MySQL Community Server").
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start mysql container: %v", err)
	}

	host, _ := container.Host(ctx)
	port, _ := container.MappedPort(ctx, "3306")

	dsn = fmt.Sprintf("squirrel:squirrel@tcp(%s:%s)/testdb", host, port.Port())
	cleanup = func() { container.Terminate(ctx) } //nolint:errcheck
	return dsn, cleanup
}

func seedMySQL(t *testing.T, dsn string) {
	t.Helper()
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	defer db.Close()

	stmts := []string{
		`CREATE TABLE IF NOT EXISTS items (id INT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(100))`,
		`INSERT INTO items (name) VALUES ('alpha'), ('beta'), ('gamma')`,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("seed %q: %v", stmt, err)
		}
	}
}

func TestMySQL_BinlogPosition(t *testing.T) {
	dsn, cleanup := startMySQL(t)
	defer cleanup()

	adapter, err := mysql.New(dsn)
	if err != nil {
		t.Fatalf("mysql.New: %v", err)
	}

	pos, _, err := adapter.BinlogPosition(context.Background())
	if err != nil {
		t.Fatalf("BinlogPosition: %v", err)
	}
	if pos.Name == "" {
		t.Error("binlog file name should not be empty")
	}
	t.Logf("binlog position: %s:%d", pos.Name, pos.Pos)
}

func TestMySQL_Dump_CreatesSnapshot(t *testing.T) {
	dsn, cleanup := startMySQL(t)
	defer cleanup()
	seedMySQL(t, dsn)

	repoDir := t.TempDir()
	if err := repo.InitWithPassword(repoDir, []byte("testpw")); err != nil {
		t.Fatalf("InitWithPassword: %v", err)
	}
	r, err := repo.Open(repoDir, []byte("testpw"))
	if err != nil {
		t.Fatalf("Open repo: %v", err)
	}

	adapter, err := mysql.New(dsn)
	if err != nil {
		t.Fatalf("mysql.New: %v", err)
	}
	ctx := context.Background()

	// Dump returns (binlogPos, executedGTIDSet, treeID, error).
	pos, _, treeID, err := adapter.Dump(ctx, r, []string{"testdb"})
	if err != nil {
		t.Fatalf("Dump: %v", err)
	}
	t.Logf("dump treeID=%s binlogPos=%s:%d", treeID, pos.Name, pos.Pos)

	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	snap, _ := repo.NewSnapshot([]string{"mysql"}, nil)
	snap.Meta = map[string]string{
		"type":        "mysql-dump",
		"binlog_file": pos.Name,
		"binlog_pos":  fmt.Sprintf("%d", pos.Pos),
	}
	if err := r.SaveSnapshot(ctx, snap); err != nil {
		t.Fatalf("SaveSnapshot: %v", err)
	}

	snaps, _ := r.ListSnapshots(ctx)
	if len(snaps) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snaps))
	}
	if snaps[0].Meta["type"] != "mysql-dump" {
		t.Errorf("snapshot type: got %q", snaps[0].Meta["type"])
	}
}

func TestMySQL_Dump_StoredBlobsNonEmpty(t *testing.T) {
	dsn, cleanup := startMySQL(t)
	defer cleanup()
	seedMySQL(t, dsn)

	repoDir := t.TempDir()
	repo.InitWithPassword(repoDir, []byte("pw"))
	r, _ := repo.Open(repoDir, []byte("pw"))

	adapter, _ := mysql.New(dsn)
	ctx := context.Background()

	if _, _, _, err := adapter.Dump(ctx, r, []string{"testdb"}); err != nil {
		t.Fatalf("Dump: %v", err)
	}
	if err := r.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	if r.Index.Count() == 0 {
		t.Error("index is empty after dump – no blobs were stored")
	}
}
