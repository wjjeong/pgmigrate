// Package ddl generates PostgreSQL DDL from catalog descriptions. It is pure:
// it never touches a database.
package ddl

import (
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"pgmigrate/internal/catalog"
)

func ident(parts ...string) string { return pgx.Identifier(parts).Sanitize() }

// literal quotes s as a standard-conforming SQL string literal.
func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func seqOptions(s catalog.Sequence) string {
	cycle := "NO CYCLE"
	if s.Cycle {
		cycle = "CYCLE"
	}
	return fmt.Sprintf("START WITH %d INCREMENT BY %d MINVALUE %d MAXVALUE %d CACHE %d %s",
		s.Start, s.Increment, s.Min, s.Max, s.Cache, cycle)
}

// CreateTable returns, in order: CREATE SEQUENCE (non-identity), CREATE TABLE,
// ALTER SEQUENCE OWNED BY, CREATE INDEX, COMMENT. Foreign keys are excluded;
// see ForeignKeys.
func CreateTable(t *catalog.Table) []string {
	var out []string
	var serial []catalog.Sequence
	identity := map[string]catalog.Sequence{}
	for _, s := range t.Sequences {
		if s.Identity {
			identity[s.Column] = s
		} else {
			serial = append(serial, s)
			out = append(out, fmt.Sprintf("CREATE SEQUENCE %s AS %s %s", s.Ref.Quoted(), s.DataType, seqOptions(s)))
		}
	}

	var defs []string
	for _, c := range t.Columns {
		defs = append(defs, columnDef(c, identity))
	}
	for _, c := range t.Constraints {
		if c.Type == 'f' {
			continue
		}
		defs = append(defs, fmt.Sprintf("CONSTRAINT %s %s", ident(c.Name), c.Def))
	}
	unlogged := ""
	if t.Unlogged {
		unlogged = "UNLOGGED "
	}
	out = append(out, fmt.Sprintf("CREATE %sTABLE %s (%s)", unlogged, t.Ref.Quoted(), strings.Join(defs, ", ")))

	for _, s := range serial {
		out = append(out, fmt.Sprintf("ALTER SEQUENCE %s OWNED BY %s", s.Ref.Quoted(), ident(t.Ref.Schema, t.Ref.Name, s.Column)))
	}
	for _, ix := range t.Indexes {
		out = append(out, ix.Def)
	}
	if t.Comment != "" {
		out = append(out, fmt.Sprintf("COMMENT ON TABLE %s IS %s", t.Ref.Quoted(), literal(t.Comment)))
	}
	for _, c := range t.Columns {
		if c.Comment != "" {
			out = append(out, fmt.Sprintf("COMMENT ON COLUMN %s IS %s", ident(t.Ref.Schema, t.Ref.Name, c.Name), literal(c.Comment)))
		}
	}
	return out
}

func columnDef(c catalog.Column, identity map[string]catalog.Sequence) string {
	var b strings.Builder
	b.WriteString(ident(c.Name))
	b.WriteString(" ")
	b.WriteString(c.Type)
	if c.Collation != "" {
		b.WriteString(" COLLATE " + c.Collation)
	}
	if c.Generated != 0 {
		kind := "STORED"
		if c.Generated == 'v' {
			kind = "VIRTUAL"
		}
		fmt.Fprintf(&b, " GENERATED ALWAYS AS (%s) %s", c.GeneratedExpr, kind)
	} else if c.Default != "" {
		b.WriteString(" DEFAULT " + c.Default)
	}
	if c.Identity != 0 {
		when := "BY DEFAULT"
		if c.Identity == 'a' {
			when = "ALWAYS"
		}
		fmt.Fprintf(&b, " GENERATED %s AS IDENTITY", when)
		if s, ok := identity[c.Name]; ok {
			fmt.Fprintf(&b, " (SEQUENCE NAME %s %s)", s.Ref.Quoted(), seqOptions(s))
		}
	} else if c.NotNull {
		b.WriteString(" NOT NULL")
	}
	return b.String()
}

// ForeignKeys returns ALTER TABLE ... ADD CONSTRAINT statements for FKs only.
func ForeignKeys(t *catalog.Table) []string {
	var out []string
	for _, c := range t.Constraints {
		if c.Type == 'f' {
			out = append(out, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", t.Ref.Quoted(), ident(c.Name), c.Def))
		}
	}
	return out
}

// DropTable returns a DROP TABLE statement (never CASCADE).
func DropTable(ref catalog.TableRef) string { return "DROP TABLE " + ref.Quoted() }

// SetSequence returns a setval query for seq.
func SetSequence(seq catalog.TableRef, value int64, isCalled bool) string {
	return fmt.Sprintf("SELECT setval(%s::regclass, %d, %t)", literal(seq.Quoted()), value, isCalled)
}
