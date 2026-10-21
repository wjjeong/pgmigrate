//go:build integration

package plan

import (
	"context"
	"fmt"
	"testing"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/testutil"
)

func TestBuild_Integration(t *testing.T) {
	src, tgt := testutil.Source(t), testutil.Target(t)
	s := testutil.Schema(t, src, tgt)
	for _, sql := range []string{
		fmt.Sprintf(`CREATE TYPE %s.mood AS ENUM ('sad','ok')`, s),
		fmt.Sprintf(`CREATE TABLE %s.parent (id int PRIMARY KEY, nm text)`, s),
		fmt.Sprintf(`CREATE TABLE %s.child (id int PRIMARY KEY, pid int REFERENCES %s.parent(id), m %s.mood)`, s, s, s),
		fmt.Sprintf(`INSERT INTO %s.parent VALUES (1,'a'),(2,'b')`, s),
	} {
		testutil.MustExec(t, src, sql)
	}
	testutil.MustExec(t, tgt, fmt.Sprintf(`CREATE TABLE %s.parent (id int PRIMARY KEY, nm text)`, s))
	testutil.MustExec(t, tgt, fmt.Sprintf(`INSERT INTO %s.parent VALUES (9,'z')`, s))

	parent, child := catalog.TableRef{Schema: s, Name: "parent"}, catalog.TableRef{Schema: s, Name: "child"}
	sel := []Selection{{Ref: child, Mode: ModeCreate}, {Ref: parent, Mode: ModeTruncate}}
	sc := config.Conn{Host: "localhost", Port: 55432, Database: "postgres"}
	tc := config.Conn{Host: "localhost", Port: 55433, Database: "postgres"}
	ctx := context.Background()

	p, err := Build(ctx, src, tgt, sc, tc, sel)
	if err != nil {
		t.Fatal(err)
	}
	has(t, p, SevError, "타입", s+".mood")
	if p.Steps[0].SourceRows != 2 || p.Steps[0].TargetRows != 1 {
		t.Errorf("parent rows: %+v", p.Steps[0])
	}

	testutil.MustExec(t, tgt, fmt.Sprintf(`CREATE TYPE %s.mood AS ENUM ('sad','ok')`, s))
	p, err = Build(ctx, src, tgt, sc, tc, sel)
	if err != nil {
		t.Fatal(err)
	}
	if p.HasErrors() {
		t.Fatalf("errors: %+v", p.Issues)
	}
	if len(p.Steps) != 2 || p.Steps[0].Sel.Ref != parent || p.Steps[1].Sel.Ref != child {
		t.Errorf("steps order: %+v", p.Steps)
	}
	if len(p.Steps[1].DDL) == 0 || len(p.Steps[1].FKDDL) == 0 {
		t.Errorf("child DDL missing: %+v", p.Steps[1])
	}
}
