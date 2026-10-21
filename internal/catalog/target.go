package catalog

import (
	"context"
	"fmt"
)

// Privileges are the current user's rights on a table.
type Privileges struct{ Select, Insert, Truncate, Owner bool }

// TablePrivileges reports the current user's privileges on ref.
// Owner means the current user can act as the table owner.
func TablePrivileges(ctx context.Context, q Querier, ref TableRef) (Privileges, error) {
	const sql = `
SELECT has_table_privilege($1::regclass, 'SELECT'),
       has_table_privilege($1::regclass, 'INSERT'),
       has_table_privilege($1::regclass, 'TRUNCATE'),
       pg_has_role((SELECT relowner FROM pg_class WHERE oid = $1::regclass), 'USAGE')`
	var p Privileges
	if err := q.QueryRow(ctx, sql, ref.Quoted()).Scan(&p.Select, &p.Insert, &p.Truncate, &p.Owner); err != nil {
		return Privileges{}, fmt.Errorf("%s 권한 조회 실패: %w", ref, err)
	}
	return p, nil
}

func queryBool(ctx context.Context, q Querier, errMsg, sql string, args ...any) (bool, error) {
	var b bool
	if err := q.QueryRow(ctx, sql, args...).Scan(&b); err != nil {
		return false, fmt.Errorf("%s: %w", errMsg, err)
	}
	return b, nil
}

// SchemaExists reports whether the schema exists.
func SchemaExists(ctx context.Context, q Querier, schema string) (bool, error) {
	return queryBool(ctx, q, fmt.Sprintf("스키마 %q 존재 여부 조회 실패", schema),
		`SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1)`, schema)
}

// SchemaCreatePrivilege reports whether the current user may create objects in the schema.
func SchemaCreatePrivilege(ctx context.Context, q Querier, schema string) (bool, error) {
	return queryBool(ctx, q, fmt.Sprintf("스키마 %q CREATE 권한 조회 실패", schema),
		`SELECT has_schema_privilege($1, 'CREATE')`, schema)
}

// TypeExists reports whether a type named name exists in schema.
func TypeExists(ctx context.Context, q Querier, schema, name string) (bool, error) {
	return queryBool(ctx, q, fmt.Sprintf("타입 %s.%s 존재 여부 조회 실패", schema, name),
		`SELECT EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid = t.typnamespace
		 WHERE n.nspname = $1 AND t.typname = $2)`, schema, name)
}

// TableExists reports whether an ordinary or partitioned table exists.
func TableExists(ctx context.Context, q Querier, ref TableRef) (bool, error) {
	return queryBool(ctx, q, fmt.Sprintf("테이블 %s 존재 여부 조회 실패", ref),
		`SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		 WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p'))`, ref.Schema, ref.Name)
}

func queryRefs(ctx context.Context, q Querier, errMsg, sql string, args ...any) ([]TableRef, error) {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", errMsg, err)
	}
	defer rows.Close()
	var out []TableRef
	for rows.Next() {
		var r TableRef
		if err := rows.Scan(&r.Schema, &r.Name); err != nil {
			return nil, fmt.Errorf("%s: %w", errMsg, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", errMsg, err)
	}
	return out, nil
}

// ReferencingTables returns other tables with a foreign key referencing ref.
func ReferencingTables(ctx context.Context, q Querier, ref TableRef) ([]TableRef, error) {
	return queryRefs(ctx, q, fmt.Sprintf("%s을(를) 참조하는 테이블 조회 실패", ref), `
SELECT n.nspname, c.relname
FROM pg_constraint k
JOIN pg_class c ON c.oid = k.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE k.contype = 'f' AND k.confrelid = $1::regclass AND k.conrelid <> k.confrelid
GROUP BY n.nspname, c.relname
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C"`, ref.Quoted())
}

// DependentViews returns views and materialized views that depend on ref.
func DependentViews(ctx context.Context, q Querier, ref TableRef) ([]TableRef, error) {
	return queryRefs(ctx, q, fmt.Sprintf("%s에 의존하는 뷰 조회 실패", ref), `
SELECT n.nspname, c.relname
FROM pg_depend d
JOIN pg_rewrite r ON r.oid = d.objid
JOIN pg_class c ON c.oid = r.ev_class
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE d.classid = 'pg_rewrite'::regclass
  AND d.refclassid = 'pg_class'::regclass
  AND d.refobjid = $1::regclass
  AND c.oid <> d.refobjid
  AND c.relkind IN ('v', 'm')
GROUP BY n.nspname, c.relname
ORDER BY n.nspname COLLATE "C", c.relname COLLATE "C"`, ref.Quoted())
}

// SequenceUpdatePrivilege reports whether the current user may call setval on seq.
func SequenceUpdatePrivilege(ctx context.Context, q Querier, seq TableRef) (bool, error) {
	return queryBool(ctx, q, fmt.Sprintf("시퀀스 %s 권한 조회 실패", seq),
		`SELECT has_sequence_privilege($1::regclass, 'UPDATE')`, seq.Quoted())
}
