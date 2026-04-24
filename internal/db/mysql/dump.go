package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"github.com/elpol4k0/squirrel/internal/repo"
)

func dumpDatabases(ctx context.Context, r *repo.Repo, tx *sql.Tx, databases []string) (string, error) {
	var nodes []repo.TreeNode

	for _, dbName := range databases {
		slog.Info("dumping database", "db", dbName)
		blobIDs, err := dumpDatabase(ctx, r, tx, dbName)
		if err != nil {
			return "", fmt.Errorf("dump %s: %w", dbName, err)
		}
		nodes = append(nodes, repo.TreeNode{
			Name:    dbName + ".sql",
			Type:    "file",
			Content: blobIDs,
		})
	}

	treeID, err := r.SaveTree(ctx, &repo.Tree{Nodes: nodes})
	if err != nil {
		return "", fmt.Errorf("save dump tree: %w", err)
	}
	return treeID, nil
}

func dumpDatabase(ctx context.Context, r *repo.Repo, tx *sql.Tx, dbName string) ([]string, error) {
	var blobIDs []string
	const chunkSize = 4 * 1024 * 1024

	flush := func(buf *bytes.Buffer) error {
		if buf.Len() == 0 {
			return nil
		}
		id, _, err := r.SaveBlob(ctx, repo.BlobData, buf.Bytes())
		if err != nil {
			return err
		}
		blobIDs = append(blobIDs, id.String())
		buf.Reset()
		return nil
	}

	var buf bytes.Buffer

	header := fmt.Sprintf("-- squirrel dump of `%s`\n"+
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;\n"+
		"USE `%s`;\n\n", dbName, dbName, dbName)
	buf.WriteString(header)

	tables, err := listTables(ctx, tx, dbName)
	if err != nil {
		return nil, err
	}

	for _, table := range tables {
		ddl, err := showCreateTable(ctx, tx, dbName, table)
		if err != nil {
			return nil, err
		}
		buf.WriteString(fmt.Sprintf("DROP TABLE IF EXISTS `%s`;\n", table))
		buf.WriteString(ddl + ";\n\n")

		if err := dumpTableRows(ctx, tx, dbName, table, &buf, chunkSize, flush); err != nil {
			return nil, fmt.Errorf("dump rows %s.%s: %w", dbName, table, err)
		}

		if buf.Len() >= chunkSize {
			if err := flush(&buf); err != nil {
				return nil, err
			}
		}
	}

	if err := flush(&buf); err != nil {
		return nil, err
	}
	return blobIDs, nil
}

func listTables(ctx context.Context, tx *sql.Tx, dbName string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SHOW FULL TABLES FROM `"+dbName+"` WHERE Table_type = 'BASE TABLE'")
	if err != nil {
		return nil, fmt.Errorf("list tables %s: %w", dbName, err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name, tableType string
		if err := rows.Scan(&name, &tableType); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func showCreateTable(ctx context.Context, tx *sql.Tx, dbName, table string) (string, error) {
	row := tx.QueryRowContext(ctx, fmt.Sprintf("SHOW CREATE TABLE `%s`.`%s`", dbName, table))
	var tblName, ddl string
	if err := row.Scan(&tblName, &ddl); err != nil {
		return "", fmt.Errorf("show create table %s.%s: %w", dbName, table, err)
	}
	return ddl, nil
}

func dumpTableRows(ctx context.Context, tx *sql.Tx, dbName, table string, buf *bytes.Buffer, chunkSize int, flush func(*bytes.Buffer) error) error {
	rows, err := tx.QueryContext(ctx, fmt.Sprintf("SELECT * FROM `%s`.`%s`", dbName, table))
	if err != nil {
		return err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	if len(cols) == 0 {
		return nil
	}

	colNames := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = "`" + c + "`"
	}
	insertPrefix := fmt.Sprintf("INSERT INTO `%s` (%s) VALUES\n", table, strings.Join(colNames, ", "))

	vals := make([]interface{}, len(cols))
	valPtrs := make([]interface{}, len(cols))
	for i := range vals {
		valPtrs[i] = &vals[i]
	}

	const batchSize = 200
	rowCount := 0
	inBatch := false

	for rows.Next() {
		if err := rows.Scan(valPtrs...); err != nil {
			return err
		}

		if rowCount%batchSize == 0 {
			if inBatch {
				buf.WriteString(";\n")
				inBatch = false
			}
			buf.WriteString(insertPrefix)
			inBatch = true
		} else {
			buf.WriteString(",\n")
		}
		buf.WriteString("(")
		for i, v := range vals {
			if i > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(sqlValue(v))
		}
		buf.WriteString(")")
		rowCount++

		if buf.Len() >= chunkSize {
			if inBatch {
				buf.WriteString(";\n")
				inBatch = false
			}
			if err := flush(buf); err != nil {
				return err
			}
		}
	}
	if inBatch {
		buf.WriteString(";\n")
	}
	if rowCount > 0 {
		buf.WriteString("\n")
	}

	return rows.Err()
}

// sqlValue formats a Go value as a SQL literal.
func sqlValue(v interface{}) string {
	if v == nil {
		return "NULL"
	}
	switch val := v.(type) {
	case []byte:
		return "'" + escapeSQLString(string(val)) + "'"
	case string:
		return "'" + escapeSQLString(val) + "'"
	case bool:
		if val {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprintf("%v", val)
	}
}

func escapeSQLString(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch c {
		case '\'':
			b.WriteString("\\'")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case 0:
			b.WriteString("\\0")
		default:
			b.WriteRune(c)
		}
	}
	return b.String()
}
