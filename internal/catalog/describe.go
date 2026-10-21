package catalog

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ErrNotFound is returned by Describe when the table does not exist.
var ErrNotFound = errors.New("table not found")

// Column describes one (non-dropped) table column.
type Column struct {
	Name          string
	Type          string // format_type(atttypid, atttypmod)
	TypeSchema    string
	TypeName      string // pg_type.typname
	TypeKind      byte   // pg_type.typtype: 'b','e','d','c','r','m'
	UserType      bool   // TypeSchema is not pg_catalog
	NotNull       bool
	Default       string // pg_get_expr(adbin); "" when none or generated
	Identity      byte   // 0, 'a' (ALWAYS), 'd' (BY DEFAULT)
	Generated     byte   // 0, 's' (STORED), 'v' (VIRTUAL)
	GeneratedExpr string
	Collation     string // quoted name only when different from the type default
	Comment       string
}

// Constraint describes a table constraint (NOT NULL constraints are excluded).
type Constraint struct {
	Name       string
	Type       byte // 'p','u','c','f','x'
	Def        string
	RefTable   *TableRef // set when Type == 'f'
	Deferrable bool
}

// Index is a standalone index (indexes owned by constraints are excluded).
type Index struct{ Name, Def string }

// Sequence is a sequence owned by a table column (serial or identity).
type Sequence struct {
	Ref       TableRef
	Column    string
	Identity  bool // pg_depend deptype 'i'
	DataType  string
	Start     int64
	Increment int64
	Min, Max  int64
	Cache     int64
	Cycle     bool
	LastValue int64
	IsCalled  bool
}

// Table is the structural description of one table.
type Table struct {
	Ref          TableRef
	Columns      []Column     // attnum order
	Constraints  []Constraint // by name
	Indexes      []Index      // by name
	Sequences    []Sequence   // owned sequences
	Comment      string
	Unlogged     bool
	Partitioned  bool // relkind 'p'
	IsPartition  bool // relispartition
	Inherits     bool // traditional inheritance parent or child
	TriggerCount int  // non-internal triggers
	HasRLS       bool // relrowsecurity
}

// Describe reads the structure of ref. It makes no assumption about which
// database (source or target) q points at.
func Describe(ctx context.Context, q Querier, ref TableRef) (*Table, error) {
	t := &Table{Ref: ref}
	var oid uint32
	const headSQL = `
SELECT c.oid, COALESCE(obj_description(c.oid, 'pg_class'), ''),
       c.relpersistence = 'u', c.relkind = 'p', c.relispartition, c.relrowsecurity,
       (SELECT count(*) FROM pg_trigger g WHERE g.tgrelid = c.oid AND NOT g.tgisinternal),
       -- 파티션 관계는 pg_inherits에도 있으므로 전통적 상속만 센다.
       (NOT c.relispartition AND EXISTS (SELECT 1 FROM pg_inherits i WHERE i.inhrelid = c.oid))
       OR EXISTS (SELECT 1 FROM pg_inherits i JOIN pg_class ch ON ch.oid = i.inhrelid
                  WHERE i.inhparent = c.oid AND NOT ch.relispartition)
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p')`
	var trig int64
	err := q.QueryRow(ctx, headSQL, ref.Schema, ref.Name).Scan(&oid, &t.Comment,
		&t.Unlogged, &t.Partitioned, &t.IsPartition, &t.HasRLS, &trig, &t.Inherits)
	if err != nil {
		if isNoRows(err) {
			return nil, fmt.Errorf("%s: %w", ref, ErrNotFound)
		}
		return nil, fmt.Errorf("%s 조회 실패: %w", ref, err)
	}
	t.TriggerCount = int(trig)

	if err := describeColumns(ctx, q, oid, t); err != nil {
		return nil, fmt.Errorf("%s 컬럼 조회 실패: %w", ref, err)
	}
	if err := describeConstraints(ctx, q, oid, t); err != nil {
		return nil, fmt.Errorf("%s 제약조건 조회 실패: %w", ref, err)
	}
	if err := describeIndexes(ctx, q, oid, t); err != nil {
		return nil, fmt.Errorf("%s 인덱스 조회 실패: %w", ref, err)
	}
	if err := describeSequences(ctx, q, oid, t); err != nil {
		return nil, fmt.Errorf("%s 시퀀스 조회 실패: %w", ref, err)
	}
	return t, nil
}

func describeColumns(ctx context.Context, q Querier, oid uint32, t *Table) error {
	const sql = `
SELECT a.attname, format_type(a.atttypid, a.atttypmod), tn.nspname, ty.typname, ty.typtype::text,
       a.attnotnull,
       CASE WHEN a.attgenerated = '' THEN COALESCE(pg_get_expr(d.adbin, d.adrelid), '') ELSE '' END,
       a.attidentity::text, a.attgenerated::text,
       CASE WHEN a.attgenerated <> '' THEN COALESCE(pg_get_expr(d.adbin, d.adrelid), '') ELSE '' END,
       CASE WHEN a.attcollation <> 0 AND a.attcollation <> ty.typcollation
            THEN CASE WHEN cn.nspname = 'pg_catalog' THEN quote_ident(co.collname)
                      ELSE quote_ident(cn.nspname) || '.' || quote_ident(co.collname) END
            ELSE '' END,
       COALESCE(col_description(a.attrelid, a.attnum), '')
FROM pg_attribute a
JOIN pg_type ty ON ty.oid = a.atttypid
JOIN pg_namespace tn ON tn.oid = ty.typnamespace
LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
LEFT JOIN pg_collation co ON co.oid = a.attcollation
LEFT JOIN pg_namespace cn ON cn.oid = co.collnamespace
WHERE a.attrelid = $1 AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`
	rows, err := q.Query(ctx, sql, oid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Column
		var kind, ident, gen string
		if err := rows.Scan(&c.Name, &c.Type, &c.TypeSchema, &c.TypeName, &kind, &c.NotNull,
			&c.Default, &ident, &gen, &c.GeneratedExpr, &c.Collation, &c.Comment); err != nil {
			return err
		}
		c.TypeKind = firstByte(kind)
		c.Identity = firstByte(ident)
		c.Generated = firstByte(gen)
		c.UserType = c.TypeSchema != "pg_catalog"
		t.Columns = append(t.Columns, c)
	}
	return rows.Err()
}

func describeConstraints(ctx context.Context, q Querier, oid uint32, t *Table) error {
	const sql = `
SELECT con.conname, con.contype::text, pg_get_constraintdef(con.oid, true), con.condeferrable,
       rn.nspname, rc.relname
FROM pg_constraint con
LEFT JOIN pg_class rc ON rc.oid = con.confrelid AND con.contype = 'f'
LEFT JOIN pg_namespace rn ON rn.oid = rc.relnamespace
WHERE con.conrelid = $1 AND con.contype <> 'n'
ORDER BY con.conname COLLATE "C"`
	rows, err := q.Query(ctx, sql, oid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c Constraint
		var typ string
		var rs, rn *string
		if err := rows.Scan(&c.Name, &typ, &c.Def, &c.Deferrable, &rs, &rn); err != nil {
			return err
		}
		c.Type = firstByte(typ)
		if c.Type == 'f' && rs != nil && rn != nil {
			c.RefTable = &TableRef{*rs, *rn}
		}
		t.Constraints = append(t.Constraints, c)
	}
	return rows.Err()
}

func describeIndexes(ctx context.Context, q Querier, oid uint32, t *Table) error {
	const sql = `
SELECT ic.relname, pg_get_indexdef(i.indexrelid)
FROM pg_index i JOIN pg_class ic ON ic.oid = i.indexrelid
WHERE i.indrelid = $1
  AND NOT EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.indexrelid AND con.conrelid = i.indrelid)
ORDER BY ic.relname COLLATE "C"`
	rows, err := q.Query(ctx, sql, oid)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ix Index
		if err := rows.Scan(&ix.Name, &ix.Def); err != nil {
			return err
		}
		t.Indexes = append(t.Indexes, ix)
	}
	return rows.Err()
}

func describeSequences(ctx context.Context, q Querier, oid uint32, t *Table) error {
	const sql = `
SELECT sn.nspname, s.relname, a.attname, d.deptype = 'i', format_type(sq.seqtypid, NULL),
       sq.seqstart, sq.seqincrement, sq.seqmin, sq.seqmax, sq.seqcache, sq.seqcycle
FROM pg_depend d
JOIN pg_class s ON s.oid = d.objid AND s.relkind = 'S'
JOIN pg_namespace sn ON sn.oid = s.relnamespace
JOIN pg_sequence sq ON sq.seqrelid = s.oid
JOIN pg_attribute a ON a.attrelid = d.refobjid AND a.attnum = d.refobjsubid
WHERE d.classid = 'pg_class'::regclass AND d.refclassid = 'pg_class'::regclass
  AND d.refobjid = $1 AND d.deptype IN ('a', 'i')
ORDER BY a.attnum, s.relname COLLATE "C"`
	rows, err := q.Query(ctx, sql, oid)
	if err != nil {
		return err
	}
	var seqs []Sequence
	for rows.Next() {
		var s Sequence
		if err := rows.Scan(&s.Ref.Schema, &s.Ref.Name, &s.Column, &s.Identity, &s.DataType,
			&s.Start, &s.Increment, &s.Min, &s.Max, &s.Cache, &s.Cycle); err != nil {
			rows.Close()
			return err
		}
		seqs = append(seqs, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// 같은 연결에서 다음 쿼리를 실행하려면 위 rows를 먼저 닫아야 한다.
	for i := range seqs {
		s := &seqs[i]
		if err := q.QueryRow(ctx, "SELECT last_value, is_called FROM "+s.Ref.Quoted()).Scan(&s.LastValue, &s.IsCalled); err != nil {
			return fmt.Errorf("%s 현재 값 조회 실패: %w", s.Ref, err)
		}
	}
	t.Sequences = seqs
	return nil
}

func firstByte(s string) byte {
	if s == "" {
		return 0
	}
	return s[0]
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }
