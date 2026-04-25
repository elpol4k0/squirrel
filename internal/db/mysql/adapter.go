package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	gomysql "github.com/go-mysql-org/go-mysql/mysql"
	"github.com/go-mysql-org/go-mysql/replication"
	_ "github.com/go-sql-driver/mysql"

	"github.com/elpol4k0/squirrel/internal/repo"
)

type BinlogSegment struct {
	File   string
	Pos    uint32
	BlobID string
}

type Adapter struct {
	dsn        string
	host       string
	port       uint16
	user       string
	pass       string
	flavor     string
	flavorOnce sync.Once
}

// DSN: user:pass@tcp(host:port)/dbname or mysql://user:pass@host/db
func New(dsn string) (*Adapter, error) {
	host, port, user, pass, err := parseDSN(dsn)
	if err != nil {
		return nil, err
	}
	return &Adapter{dsn: dsn, host: host, port: port, user: user, pass: pass}, nil
}

// rowQuerier is implemented by both *sql.DB and *sql.Conn.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// showBinlogStatus tries SHOW BINARY LOG STATUS (MySQL 8.4+) then falls back to SHOW MASTER STATUS.
func showBinlogStatus(ctx context.Context, q rowQuerier) (file string, pos uint32, doDb, ignoreDb, gtidSet string, err error) {
	for _, query := range []string{"SHOW BINARY LOG STATUS", "SHOW MASTER STATUS"} {
		row := q.QueryRowContext(ctx, query)
		if err = row.Scan(&file, &pos, &doDb, &ignoreDb, &gtidSet); err == nil {
			return
		}
	}
	return
}

func (a *Adapter) BinlogPosition(ctx context.Context) (gomysql.Position, string, error) {
	db, err := sql.Open("mysql", a.dsn)
	if err != nil {
		return gomysql.Position{}, "", fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	file, pos, _, _, executedGTIDSet, err := showBinlogStatus(ctx, db)
	if err != nil {
		return gomysql.Position{}, "", fmt.Errorf("binlog status: %w", err)
	}
	slog.Info("binlog position", "file", file, "pos", pos)
	return gomysql.Position{Name: file, Pos: pos}, executedGTIDSet, nil
}

func (a *Adapter) Dump(ctx context.Context, r *repo.Repo, databases []string) (gomysql.Position, string, string, error) {
	db, err := sql.Open("mysql", a.dsn)
	if err != nil {
		return gomysql.Position{}, "", "", fmt.Errorf("open: %w", err)
	}
	defer db.Close()

	// Single-transaction snapshot for InnoDB; also records binlog position.
	conn, err := db.Conn(ctx)
	if err != nil {
		return gomysql.Position{}, "", "", err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "FLUSH TABLES WITH READ LOCK"); err != nil {
		return gomysql.Position{}, "", "", fmt.Errorf("flush tables: %w", err)
	}
	// Read binlog position while the lock is held.
	file, pos, _, _, executedGTIDSet, binlogErr := showBinlogStatus(ctx, conn)
	if binlogErr != nil {
		conn.ExecContext(ctx, "UNLOCK TABLES") //nolint:errcheck
		return gomysql.Position{}, "", "", fmt.Errorf("binlog status: %w", binlogErr)
	}
	binlogPos := gomysql.Position{Name: file, Pos: pos}
	slog.Info("dump binlog position", "file", file, "pos", pos)

	// Start consistent snapshot transaction before releasing the lock.
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		conn.ExecContext(ctx, "UNLOCK TABLES") //nolint:errcheck
		return gomysql.Position{}, "", "", err
	}
	conn.ExecContext(ctx, "UNLOCK TABLES") //nolint:errcheck

	if len(databases) == 0 {
		databases, err = listDatabases(ctx, tx)
		if err != nil {
			tx.Rollback() //nolint:errcheck
			return gomysql.Position{}, "", "", err
		}
	}

	treeID, err := dumpDatabases(ctx, r, tx, databases)
	tx.Rollback() //nolint:errcheck
	if err != nil {
		return gomysql.Position{}, "", "", err
	}

	return binlogPos, executedGTIDSet, treeID, nil
}

func (a *Adapter) StreamBinlog(ctx context.Context, r *repo.Repo, pos gomysql.Position) ([]BinlogSegment, error) {
	cfg := replication.BinlogSyncerConfig{
		ServerID: uint32(rand.Int31n(100000) + 1000), //nolint:gosec
		Flavor:   a.detectFlavor(ctx),
		Host:     a.host,
		Port:     a.port,
		User:     a.user,
		Password: a.pass,
	}
	syncer := replication.NewBinlogSyncer(cfg)
	defer syncer.Close()

	var segments []BinlogSegment
	var curFile string
	var curBuf []byte
	var curStartPos uint32

	flush := func(filename string, startPos uint32, data []byte) error {
		id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
		if err != nil {
			return fmt.Errorf("save binlog blob: %w", err)
		}
		seg := BinlogSegment{File: filename, Pos: startPos, BlobID: id.String()}
		segments = append(segments, seg)
		slog.Debug("binlog segment flushed", "file", filename, "pos", startPos, "bytes", len(data))
		return nil
	}

	err := syncer.StartBackupWithHandler(pos, 0,
		func(filename string) (io.WriteCloser, error) {
			// Called when a new binlog file starts.
			if curFile != "" && len(curBuf) > 0 {
				if err := flush(curFile, curStartPos, curBuf); err != nil {
					return nil, err
				}
				curBuf = nil
			}
			curFile = filename
			curStartPos = 0

			return &binlogWriter{
				onWrite: func(p []byte) (int, error) {
					select {
					case <-ctx.Done():
						return 0, ctx.Err()
					default:
					}
					if curStartPos == 0 && len(p) > 0 {
						curStartPos = pos.Pos
					}
					curBuf = append(curBuf, p...)
					// flush when a segment exceeds 64 MiB
					if len(curBuf) >= 64*1024*1024 {
						if err := flush(curFile, curStartPos, curBuf); err != nil {
							return 0, err
						}
						curBuf = nil
						curStartPos = 0
					}
					return len(p), nil
				},
				onClose: func() error { return nil },
			}, nil
		},
	)

	if curFile != "" && len(curBuf) > 0 {
		if ferr := flush(curFile, curStartPos, curBuf); ferr != nil && err == nil {
			err = ferr
		}
	}

	if ctx.Err() != nil {
		return segments, nil
	}
	return segments, err
}

type binlogWriter struct {
	onWrite func([]byte) (int, error)
	onClose func() error
}

func (w *binlogWriter) Write(p []byte) (int, error) { return w.onWrite(p) }
func (w *binlogWriter) Close() error                { return w.onClose() }

// Supports: user:pass@tcp(host:port)/db and mysql://user:pass@host:port/db
func parseDSN(dsn string) (host string, port uint16, user, pass string, err error) {
	// URL form
	if strings.HasPrefix(dsn, "mysql://") {
		u, e := url.Parse(dsn)
		if e != nil {
			return "", 0, "", "", fmt.Errorf("parse dsn: %w", e)
		}
		h := u.Hostname()
		p := u.Port()
		if p == "" {
			p = "3306"
		}
		portN, _ := strconv.ParseUint(p, 10, 16)
		pass, _ = u.User.Password()
		return h, uint16(portN), u.User.Username(), pass, nil
	}
	// go-sql-driver form: user:pass@tcp(host:port)/db
	at := strings.LastIndex(dsn, "@")
	if at < 0 {
		return "", 0, "", "", fmt.Errorf("invalid mysql DSN: missing @")
	}
	userInfo := dsn[:at]
	rest := dsn[at+1:]

	if colon := strings.Index(userInfo, ":"); colon >= 0 {
		user = userInfo[:colon]
		pass = userInfo[colon+1:]
	} else {
		user = userInfo
	}

	// rest: tcp(host:port)/db
	rest = strings.TrimPrefix(rest, "tcp(")
	if paren := strings.Index(rest, ")"); paren >= 0 {
		hostPort := rest[:paren]
		h, p, _ := strings.Cut(hostPort, ":")
		host = h
		if p == "" {
			p = "3306"
		}
		portN, _ := strconv.ParseUint(p, 10, 16)
		port = uint16(portN)
	}
	return host, port, user, pass, nil
}

func listDatabases(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	skip := map[string]bool{"information_schema": true, "performance_schema": true, "sys": true}
	var dbs []string
	for rows.Next() {
		var db string
		if err := rows.Scan(&db); err != nil {
			return nil, err
		}
		if !skip[db] {
			dbs = append(dbs, db)
		}
	}
	return dbs, rows.Err()
}

func (a *Adapter) StreamBinlogGTID(ctx context.Context, r *repo.Repo, gtidSetStr string) ([]BinlogSegment, error) {
	flavor := a.detectFlavor(ctx)

	var gtidSet gomysql.GTIDSet
	var parseErr error
	if flavor == "mariadb" {
		gtidSet, parseErr = gomysql.ParseMariadbGTIDSet(gtidSetStr)
	} else {
		gtidSet, parseErr = gomysql.ParseMysqlGTIDSet(gtidSetStr)
	}
	if parseErr != nil {
		return nil, fmt.Errorf("parse GTID set %q: %w", gtidSetStr, parseErr)
	}

	cfg := replication.BinlogSyncerConfig{
		ServerID: uint32(rand.Int31n(100000) + 1000), //nolint:gosec
		Flavor:   flavor,
		Host:     a.host,
		Port:     a.port,
		User:     a.user,
		Password: a.pass,
	}
	syncer := replication.NewBinlogSyncer(cfg)
	defer syncer.Close()

	streamer, err := syncer.StartSyncGTID(gtidSet)
	if err != nil {
		return nil, fmt.Errorf("start GTID sync: %w", err)
	}

	var segments []BinlogSegment
	var curFile string
	var curBuf []byte
	var curStartPos uint32

	flush := func(filename string, startPos uint32, data []byte) error {
		id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
		if err != nil {
			return fmt.Errorf("save binlog blob: %w", err)
		}
		segments = append(segments, BinlogSegment{File: filename, Pos: startPos, BlobID: id.String()})
		slog.Debug("GTID binlog segment flushed", "file", filename, "bytes", len(data))
		return nil
	}

	for {
		if ctx.Err() != nil {
			break
		}
		ev, err := streamer.GetEvent(ctx)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			return segments, fmt.Errorf("get binlog event: %w", err)
		}

		switch e := ev.Event.(type) {
		case *replication.RotateEvent:
			if curFile != "" && len(curBuf) > 0 {
				if err := flush(curFile, curStartPos, curBuf); err != nil {
					return segments, err
				}
				curBuf = nil
				curStartPos = 0
			}
			curFile = string(e.NextLogName)
		case *replication.XIDEvent, *replication.QueryEvent,
			*replication.RowsEvent, *replication.TableMapEvent:
			if curFile == "" {
				continue
			}
			raw := ev.RawData
			if curStartPos == 0 {
				curStartPos = ev.Header.LogPos - ev.Header.EventSize
			}
			curBuf = append(curBuf, raw...)
			if len(curBuf) >= 64*1024*1024 {
				if err := flush(curFile, curStartPos, curBuf); err != nil {
					return segments, err
				}
				curBuf = nil
				curStartPos = 0
			}
		}
	}

	if curFile != "" && len(curBuf) > 0 {
		if err := flush(curFile, curStartPos, curBuf); err != nil {
			return segments, err
		}
	}
	return segments, nil
}

func flavorFromVersion(version string) string {
	if strings.Contains(strings.ToLower(version), "mariadb") {
		return "mariadb"
	}
	return "mysql"
}

func (a *Adapter) detectFlavor(ctx context.Context) string {
	a.flavorOnce.Do(func() {
		db, err := sql.Open("mysql", a.dsn)
		if err != nil {
			a.flavor = "mysql"
			return
		}
		defer db.Close()
		var version string
		if err := db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
			a.flavor = "mysql"
			return
		}
		a.flavor = flavorFromVersion(version)
	})
	return a.flavor
}

// Needed so time.Duration is used somewhere to avoid import unused error.
var _ = time.Second
