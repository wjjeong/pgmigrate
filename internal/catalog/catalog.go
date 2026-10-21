// Package catalog lists tables and counts rows in a PostgreSQL database.
package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Querier is satisfied by both *pgx.Conn and pgx.Tx.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

var (
	_ Querier = (*pgx.Conn)(nil)
	_ Querier = pgx.Tx(nil)
)

// TableRef identifies a table by schema and name.
type TableRef struct{ Schema, Name string }

// Quoted returns the safely quoted "schema"."name".
func (r TableRef) Quoted() string { return pgx.Identifier{r.Schema, r.Name}.Sanitize() }

// String returns schema.name for display.
func (r TableRef) String() string { return r.Schema + "." + r.Name }

// TableInfo describes a table found by ListTables.
type TableInfo struct {
	Ref         TableRef
	EstRows     int64
	Partitioned bool
}

// ListTables returns ordinary and partitioned tables outside system schemas,
// ordered by schema, name (byte order, independent of database collation).
func ListTables(ctx context.Context, q Querier) ([]TableInfo, error) {
	const sql = `
SELECT n.nspname, c.relname, GREATEST(c.reltuples, 0)::bigint, c.relkind = 'p'
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg\_toast%'
  AND n.nspname NOT LIKE 'pg\_temp\_%'
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C"`
	rows, err := q.Query(ctx, sql)
	if err != nil {
		return nil, fmt.Errorf("테이블 목록 조회 실패: %w", err)
	}
	defer rows.Close()
	var out []TableInfo
	for rows.Next() {
		var ti TableInfo
		if err := rows.Scan(&ti.Ref.Schema, &ti.Ref.Name, &ti.EstRows, &ti.Partitioned); err != nil {
			return nil, fmt.Errorf("테이블 목록 읽기 실패: %w", err)
		}
		out = append(out, ti)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("테이블 목록 조회 실패: %w", err)
	}
	return out, nil
}

// CountRows returns the exact row count of ref.
func CountRows(ctx context.Context, q Querier, ref TableRef) (int64, error) {
	var n int64
	if err := q.QueryRow(ctx, "SELECT count(*) FROM "+ref.Quoted()).Scan(&n); err != nil {
		return 0, fmt.Errorf("%s 행 수 조회 실패: %w", ref, err)
	}
	return n, nil
}
