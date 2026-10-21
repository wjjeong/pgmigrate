//go:build integration

package catalog

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"pgmigrate/internal/testutil"
)

func TestTargetFacts(t *testing.T) {
	ctx := context.Background()
	tgt := testutil.Target(t)
	s := testutil.Schema(t, tgt)

	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	role := "ro_" + hex.EncodeToString(b)
	qrole := pgx.Identifier{role}.Sanitize()

	for _, sql := range []string{
		fmt.Sprintf(`CREATE TYPE %s.mood AS ENUM ('a','b')`, s),
		fmt.Sprintf(`CREATE TABLE %s.parent (id serial PRIMARY KEY)`, s),
		fmt.Sprintf(`CREATE TABLE %s.child (id int PRIMARY KEY, pid int REFERENCES %s.parent(id))`, s, s),
		fmt.Sprintf(`CREATE TABLE %s."Selfref" (id int PRIMARY KEY, p int REFERENCES %s."Selfref"(id))`, s, s),
		fmt.Sprintf(`CREATE VIEW %s.v_parent AS SELECT id FROM %s.parent`, s, s),
		"CREATE ROLE " + qrole,
		fmt.Sprintf(`GRANT USAGE ON SCHEMA %s TO %s`, s, qrole),
		fmt.Sprintf(`GRANT SELECT ON %s.parent TO %s`, s, qrole),
	} {
		testutil.MustExec(t, tgt, sql)
	}
	t.Cleanup(func() {
		_, _ = tgt.Exec(context.Background(), "RESET ROLE")
		// 롤은 클러스터 전역이므로 스키마 삭제 뒤에 제거한다.
		_, _ = tgt.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+pgx.Identifier{s}.Sanitize()+" CASCADE")
		_, _ = tgt.Exec(context.Background(), "DROP ROLE IF EXISTS "+qrole)
	})

	parent := TableRef{s, "parent"}
	child := TableRef{s, "child"}

	t.Run("superuser privileges", func(t *testing.T) {
		p, err := TablePrivileges(ctx, tgt, parent)
		if err != nil {
			t.Fatal(err)
		}
		if !(p.Select && p.Insert && p.Truncate && p.Owner) {
			t.Fatalf("모두 true 기대: %+v", p)
		}
	})

	t.Run("read-only role", func(t *testing.T) {
		testutil.MustExec(t, tgt, "SET ROLE "+qrole)
		defer testutil.MustExec(t, tgt, "RESET ROLE")
		p, err := TablePrivileges(ctx, tgt, parent)
		if err != nil {
			t.Fatal(err)
		}
		if !p.Select || p.Insert || p.Truncate || p.Owner {
			t.Fatalf("Select만 true 기대: %+v", p)
		}
		ok, err := SchemaCreatePrivilege(ctx, tgt, s)
		if err != nil || ok {
			t.Fatalf("CREATE 권한 없음 기대: %v %v", ok, err)
		}
		ok, err = SequenceUpdatePrivilege(ctx, tgt, TableRef{s, "parent_id_seq"})
		if err != nil || ok {
			t.Fatalf("시퀀스 UPDATE 권한 없음 기대: %v %v", ok, err)
		}
	})

	t.Run("superuser schema/sequence", func(t *testing.T) {
		ok, err := SchemaCreatePrivilege(ctx, tgt, s)
		if err != nil || !ok {
			t.Fatalf("CREATE 권한 기대: %v %v", ok, err)
		}
		ok, err = SequenceUpdatePrivilege(ctx, tgt, TableRef{s, "parent_id_seq"})
		if err != nil || !ok {
			t.Fatalf("시퀀스 UPDATE 권한 기대: %v %v", ok, err)
		}
	})

	t.Run("exists", func(t *testing.T) {
		for _, c := range []struct {
			name string
			got  func() (bool, error)
			want bool
		}{
			{"schema yes", func() (bool, error) { return SchemaExists(ctx, tgt, s) }, true},
			{"schema no", func() (bool, error) { return SchemaExists(ctx, tgt, s+"_none") }, false},
			{"type yes", func() (bool, error) { return TypeExists(ctx, tgt, s, "mood") }, true},
			{"type no", func() (bool, error) { return TypeExists(ctx, tgt, s, "nomood") }, false},
			{"type wrong schema", func() (bool, error) { return TypeExists(ctx, tgt, "public", "mood") }, false},
			{"table yes", func() (bool, error) { return TableExists(ctx, tgt, parent) }, true},
			{"table mixed case yes", func() (bool, error) { return TableExists(ctx, tgt, TableRef{s, "Selfref"}) }, true},
			{"table no", func() (bool, error) { return TableExists(ctx, tgt, TableRef{s, "nope"}) }, false},
			{"view is not table", func() (bool, error) { return TableExists(ctx, tgt, TableRef{s, "v_parent"}) }, false},
		} {
			got, err := c.got()
			if err != nil || got != c.want {
				t.Errorf("%s: got %v err %v, want %v", c.name, got, err, c.want)
			}
		}
	})

	t.Run("referencing and views", func(t *testing.T) {
		refs, err := ReferencingTables(ctx, tgt, parent)
		if err != nil || len(refs) != 1 || refs[0] != child {
			t.Fatalf("child 하나 기대: %v %v", refs, err)
		}
		refs, err = ReferencingTables(ctx, tgt, TableRef{s, "Selfref"})
		if err != nil || len(refs) != 0 {
			t.Fatalf("자기 참조 제외 기대: %v %v", refs, err)
		}
		views, err := DependentViews(ctx, tgt, parent)
		if err != nil || len(views) != 1 || views[0] != (TableRef{s, "v_parent"}) {
			t.Fatalf("v_parent 기대: %v %v", views, err)
		}
		views, err = DependentViews(ctx, tgt, child)
		if err != nil || len(views) != 0 {
			t.Fatalf("뷰 없음 기대: %v %v", views, err)
		}
	})
}
