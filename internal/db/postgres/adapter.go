package postgres

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/elpol4k0/squirrel/internal/repo"
)

const walSegSize = 16 * 1024 * 1024 // default PostgreSQL WAL segment size

type WALSegment struct {
	StartLSN pglogrepl.LSN
	BlobID   string
	Name     string // PG WAL filename, e.g. 000000010000000000000001
}

type Adapter struct {
	dsn string
}

func New(dsn string) *Adapter {
	return &Adapter{dsn: dsn}
}

func (a *Adapter) IdentifySystem(ctx context.Context) (pglogrepl.IdentifySystemResult, error) {
	conn, err := a.replConn(ctx)
	if err != nil {
		return pglogrepl.IdentifySystemResult{}, err
	}
	defer conn.Close(ctx)
	return pglogrepl.IdentifySystem(ctx, conn)
}

// Returns start LSN, system identifier, and the tree blob ID for restore.
func (a *Adapter) BaseBackup(ctx context.Context, r *repo.Repo) (pglogrepl.LSN, pglogrepl.IdentifySystemResult, string, error) {
	conn, err := a.replConn(ctx)
	if err != nil {
		return 0, pglogrepl.IdentifySystemResult{}, "", err
	}
	defer conn.Close(ctx)

	sysident, err := pglogrepl.IdentifySystem(ctx, conn)
	if err != nil {
		return 0, sysident, "", fmt.Errorf("identify system: %w", err)
	}
	slog.Info("connected", "systemID", sysident.SystemID, "timeline", sysident.Timeline, "xlogpos", sysident.XLogPos)

	result, err := pglogrepl.StartBaseBackup(ctx, conn, pglogrepl.BaseBackupOptions{
		Label:  "squirrel",
		Fast:   true,
		NoWait: true,
	})
	if err != nil {
		return 0, sysident, "", fmt.Errorf("start base backup: %w", err)
	}
	slog.Info("base backup started", "startLSN", result.LSN, "tablespaces", len(result.Tablespaces))

	var treeNodes []repo.TreeNode

	for i, ts := range result.Tablespaces {
		label := ts.Location
		if label == "" {
			label = "base"
		}
		slog.Info("streaming tablespace", "index", i, "location", label, "size", ts.Size)

		if err := pglogrepl.NextTableSpace(ctx, conn); err != nil {
			return 0, sysident, "", fmt.Errorf("next tablespace: %w", err)
		}

		rd := &copyDataReader{conn: conn, ctx: ctx}
		blobIDs, err := streamTAR(ctx, r, rd, label)
		if err != nil {
			return 0, sysident, "", fmt.Errorf("stream tablespace %s: %w", label, err)
		}

		name := label + ".tar"
		if label == "base" {
			name = "base.tar"
		}
		treeNodes = append(treeNodes, repo.TreeNode{
			Name:    name,
			Type:    "file",
			Content: blobIDs,
		})
	}

	finResult, err := pglogrepl.FinishBaseBackup(ctx, conn)
	if err != nil {
		return 0, sysident, "", fmt.Errorf("finish base backup: %w", err)
	}
	slog.Info("base backup finished", "endLSN", finResult.LSN)

	treeID, err := r.SaveTree(ctx, &repo.Tree{Nodes: treeNodes})
	if err != nil {
		return 0, sysident, "", fmt.Errorf("save base backup tree: %w", err)
	}

	return result.LSN, sysident, treeID, nil
}

func (a *Adapter) CreateSlot(ctx context.Context, slotName string) error {
	conn, err := a.replConn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close(ctx)

	_, err = pglogrepl.CreateReplicationSlot(ctx, conn, slotName, "",
		pglogrepl.CreateReplicationSlotOptions{Mode: pglogrepl.PhysicalReplication})
	if err != nil {
		return fmt.Errorf("create replication slot %q: %w", slotName, err)
	}
	slog.Info("replication slot created", "slot", slotName)
	return nil
}

// Runs until ctx is cancelled; returns all flushed segments.
func (a *Adapter) StreamWAL(ctx context.Context, r *repo.Repo, slotName string, startLSN pglogrepl.LSN, timelineID int32) ([]WALSegment, error) {
	conn, err := a.replConn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)

	if err := pglogrepl.StartReplication(ctx, conn, slotName, startLSN,
		pglogrepl.StartReplicationOptions{Mode: pglogrepl.PhysicalReplication, Timeline: timelineID}); err != nil {
		return nil, fmt.Errorf("start physical replication: %w", err)
	}
	slog.Info("WAL streaming started", "slot", slotName, "startLSN", startLSN)

	standbyDeadline := time.Now().Add(10 * time.Second)
	var walBuf []byte
	var walStartLSN pglogrepl.LSN
	var segments []WALSegment

	for {
		if time.Now().After(standbyDeadline) {
			if err := pglogrepl.SendStandbyStatusUpdate(ctx, conn, pglogrepl.StandbyStatusUpdate{
				WALWritePosition: startLSN,
				ReplyRequested:   false,
			}); err != nil {
				return segments, fmt.Errorf("send keepalive: %w", err)
			}
			standbyDeadline = time.Now().Add(10 * time.Second)
		}

		recvCtx, cancel := context.WithDeadline(ctx, standbyDeadline)
		msg, err := conn.ReceiveMessage(recvCtx)
		cancel()

		if err != nil {
			if pgconn.Timeout(err) {
				continue
			}
			if ctx.Err() != nil {
				break
			}
			return segments, fmt.Errorf("receive: %w", err)
		}

		switch m := msg.(type) {
		case *pgproto3.CopyData:
			if len(m.Data) == 0 {
				continue
			}
			switch m.Data[0] {
			case pglogrepl.XLogDataByteID:
				xld, err := pglogrepl.ParseXLogData(m.Data[1:])
				if err != nil {
					return segments, fmt.Errorf("parse xlog data: %w", err)
				}
				if walStartLSN == 0 {
					walStartLSN = xld.WALStart
				}
				walBuf = append(walBuf, xld.WALData...)
				startLSN = xld.WALStart + pglogrepl.LSN(len(xld.WALData))

				if len(walBuf) >= walSegSize {
					seg, err := flushWALSegment(ctx, r, walBuf, walStartLSN, timelineID)
					if err != nil {
						return segments, err
					}
					segments = append(segments, seg)
					walBuf = walBuf[:0]
					walStartLSN = 0
				}
			case pglogrepl.PrimaryKeepaliveMessageByteID:
				pkm, err := pglogrepl.ParsePrimaryKeepaliveMessage(m.Data[1:])
				if err != nil {
					return segments, fmt.Errorf("parse keepalive: %w", err)
				}
				if pkm.ReplyRequested {
					standbyDeadline = time.Now()
				}
			}
		case *pgproto3.CopyDone:
			break
		}
	}

	if len(walBuf) > 0 {
		seg, err := flushWALSegment(ctx, r, walBuf, walStartLSN, timelineID)
		if err != nil {
			return segments, err
		}
		segments = append(segments, seg)
	}
	return segments, nil
}

func flushWALSegment(ctx context.Context, r *repo.Repo, data []byte, startLSN pglogrepl.LSN, timeline int32) (WALSegment, error) {
	id, _, err := r.SaveBlob(ctx, repo.BlobData, data)
	if err != nil {
		return WALSegment{}, fmt.Errorf("save WAL segment at %s: %w", startLSN, err)
	}
	seg := WALSegment{
		StartLSN: startLSN,
		BlobID:   id.String(),
		Name:     walSegmentName(timeline, startLSN),
	}
	slog.Debug("WAL segment flushed", "name", seg.Name, "startLSN", startLSN, "bytes", len(data))
	return seg, nil
}

func walSegmentName(timeline int32, lsn pglogrepl.LSN) string {
	return fmt.Sprintf("%08X%08X%08X",
		uint32(timeline),
		uint32(uint64(lsn)>>32),
		uint32(uint64(lsn)&0xFFFFFFFF)/walSegSize,
	)
}

func (a *Adapter) replConn(ctx context.Context) (*pgconn.PgConn, error) {
	cfg, err := pgconn.ParseConfig(a.dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	cfg.RuntimeParams["replication"] = "database"
	conn, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("replication connect: %w", err)
	}
	return conn, nil
}

// copyDataReader wraps a replication connection CopyOut stream as an io.Reader.
type copyDataReader struct {
	conn *pgconn.PgConn
	ctx  context.Context
	buf  []byte
	done bool
}

func (r *copyDataReader) Read(p []byte) (int, error) {
	if r.done {
		return 0, io.EOF
	}
	if len(r.buf) > 0 {
		n := copy(p, r.buf)
		r.buf = r.buf[n:]
		return n, nil
	}
	for {
		msg, err := r.conn.ReceiveMessage(r.ctx)
		if err != nil {
			return 0, err
		}
		switch m := msg.(type) {
		case *pgproto3.CopyData:
			n := copy(p, m.Data)
			if n < len(m.Data) {
				r.buf = append(r.buf[:0], m.Data[n:]...)
			}
			return n, nil
		case *pgproto3.CopyDone:
			r.done = true
			return 0, io.EOF
		case *pgproto3.ErrorResponse:
			return 0, pgconn.ErrorResponseToPgError(m)
		}
	}
}

// streamTAR feeds TAR data from rd into the repo as blobs and returns their IDs in order.
func streamTAR(ctx context.Context, r *repo.Repo, rd io.Reader, label string) ([]string, error) {
	buf := make([]byte, 4*1024*1024)
	var blobIDs []string
	var total int64

	for {
		n, err := io.ReadFull(rd, buf)
		if n > 0 {
			id, _, serr := r.SaveBlob(ctx, repo.BlobData, buf[:n])
			if serr != nil {
				return nil, serr
			}
			blobIDs = append(blobIDs, id.String())
			total += int64(n)
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read tar: %w", err)
		}
	}
	slog.Info("tablespace streamed", "label", label, "bytes", total, "blobs", len(blobIDs))
	return blobIDs, nil
}
