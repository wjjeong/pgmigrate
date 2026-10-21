package plan

import (
	"reflect"
	"strings"
	"testing"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
)

var (
	srcConn  = config.Conn{Name: "s", Env: "dev", Host: "h1", Port: 5432, Database: "d"}
	tgtConn  = config.Conn{Name: "t", Env: "dev", Host: "h2", Port: 5432, Database: "d"}
	fullPriv = catalog.Privileges{Select: true, Insert: true, Truncate: true, Owner: true}
)

func simple(n string) *catalog.Table {
	return &catalog.Table{Ref: tref(n), Columns: []catalog.Column{col("id", "integer"), col("nm", "text")}}
}

// facts builds Facts where each named table exists on both sides with full rights.
func facts(names ...string) *Facts {
	f := &Facts{
		Source:     map[catalog.TableRef]*catalog.Table{},
		SourceRows: map[catalog.TableRef]int64{},
		Target:     map[catalog.TableRef]*TargetFacts{},
	}
	for _, n := range names {
		f.Source[tref(n)] = simple(n)
		f.Target[tref(n)] = &TargetFacts{Table: simple(n), SchemaExists: true, SchemaCreate: true, Priv: fullPriv}
	}
	return f
}

func sel(mode Mode, n string) Selection { return Selection{Ref: tref(n), Mode: mode} }

func run(f *Facts, s ...Selection) *Plan { return Check(srcConn, tgtConn, s, f) }

// expect asserts that exactly one issue of sev mentioning all substrs exists.
func has(t *testing.T, p *Plan, sev Severity, substrs ...string) *Issue {
	t.Helper()
	for i := range p.Issues {
		is := &p.Issues[i]
		if is.Severity != sev {
			continue
		}
		ok := true
		for _, s := range substrs {
			ok = ok && strings.Contains(is.Message, s)
		}
		if ok {
			return is
		}
	}
	t.Fatalf("issue sev=%d %v not found in %+v", sev, substrs, p.Issues)
	return nil
}

func errorCount(p *Plan) int {
	n := 0
	for _, i := range p.Issues {
		if i.Severity == SevError {
			n++
		}
	}
	return n
}

func TestCheck_R1(t *testing.T) {
	p := Check(srcConn, srcConn, []Selection{sel(ModeAppend, "a")}, facts("a"))
	is := has(t, p, SevError, "같은 데이터베이스")
	if is.Table != nil {
		t.Error("R1 must be global")
	}
}

func TestCheck_R2(t *testing.T) {
	for name, mut := range map[string]func(*Facts){
		"src partitioned": func(f *Facts) { f.Source[tref("a")].Partitioned = true },
		"src partition":   func(f *Facts) { f.Source[tref("a")].IsPartition = true },
		"tgt inherits":    func(f *Facts) { f.Target[tref("a")].Table.Inherits = true },
	} {
		f := facts("a")
		mut(f)
		t.Run(name, func(t *testing.T) { has(t, run(f, sel(ModeAppend, "a")), SevError, "파티션 또는 상속") })
	}
}

func TestCheck_R3(t *testing.T) {
	f := facts("a")
	f.Target[tref("a")] = &TargetFacts{}
	p := run(f, sel(ModeCreate, "a"))
	has(t, p, SevError, "스키마 s")
	for _, i := range p.Issues {
		if strings.Contains(i.Message, "CREATE 권한") {
			t.Errorf("R8 noise when schema missing: %s", i.Message)
		}
	}
}

func TestCheck_R4(t *testing.T) {
	p := run(facts("a"), sel(ModeCreate, "a"))
	has(t, p, SevError, "이미 테이블이 있습니다")
	if len(p.Steps) != 1 {
		t.Error("step must still be produced")
	}
}

func TestCheck_R5(t *testing.T) {
	prd := tgtConn
	prd.Env = "prd"
	p := Check(srcConn, prd, []Selection{{Ref: tref("a"), Mode: ModeCreate, DropRecreate: true}}, facts("a"))
	has(t, p, SevError, "prd")
	p = Check(srcConn, prd, []Selection{{Ref: tref("a"), Mode: ModeCreate, DropRecreate: true}}, createFacts())
	has(t, p, SevError, "prd")
}

func TestCheck_R6(t *testing.T) {
	s := Selection{Ref: tref("a"), Mode: ModeCreate, DropRecreate: true}
	p := run(facts("a"), s)
	has(t, p, SevWarning, "GRANT")
	if p.HasErrors() {
		t.Errorf("unexpected errors: %+v", p.Issues)
	}
	if len(p.Steps[0].DDL) == 0 {
		t.Error("DDL missing")
	}

	f := facts("a")
	f.Target[tref("a")].DependentViews = []catalog.TableRef{tref("v")}
	has(t, run(f, s), SevError, "뷰", "s.v")

	f = facts("a", "b")
	f.Target[tref("a")].ReferencedBy = []catalog.TableRef{tref("b")}
	has(t, run(f, s), SevError, "FK로 참조", "s.b")
	// referencing table inside the selection: OK only if also drop+recreate
	if p := run(f, s, sel(ModeAppend, "b")); !p.HasErrors() {
		t.Error("append of referencing table must error")
	}
	if p := run(f, s, Selection{Ref: tref("b"), Mode: ModeCreate, DropRecreate: true}); errorCount(p) != 0 {
		t.Errorf("drop+recreate of referencing table is OK: %+v", p.Issues)
	}

	f = facts("a")
	f.Target[tref("a")].Priv.Owner = false
	has(t, run(f, s), SevError, "소유자")
}

func createFacts() *Facts {
	f := facts("a")
	f.Target[tref("a")].Table = nil
	return f
}

func TestCheck_R7(t *testing.T) {
	f := createFacts()
	f.Target[tref("a")].MissingTypes = []string{"s.mood"}
	f.Target[tref("a")].MissingFKTables = []catalog.TableRef{tref("zz")}
	p := run(f, sel(ModeCreate, "a"))
	has(t, p, SevError, "s.mood")
	has(t, p, SevError, "s.zz")
}

func TestCheck_R8(t *testing.T) {
	f := createFacts()
	f.Target[tref("a")].SchemaCreate = false
	has(t, run(f, sel(ModeCreate, "a")), SevError, "CREATE 권한")
}

func TestCheck_R9(t *testing.T) {
	f := createFacts()
	f.Source[tref("a")].TriggerCount = 2
	f.Source[tref("a")].HasRLS = true
	p := run(f, sel(ModeCreate, "a"))
	has(t, p, SevWarning, "트리거")
	has(t, p, SevWarning, "RLS")
	if p.HasErrors() {
		t.Errorf("unexpected errors: %+v", p.Issues)
	}
}

func TestCheck_R10(t *testing.T) {
	f := facts("a")
	f.Target[tref("a")].Table = nil
	for _, m := range []Mode{ModeTruncate, ModeAppend} {
		p := run(f, sel(m, "a"))
		has(t, p, SevError, "타깃에 테이블이 없습니다")
		if errorCount(p) != 1 || len(p.Steps) != 1 {
			t.Errorf("%s: dependent rules must be skipped: %+v", m, p.Issues)
		}
	}
}

func TestCheck_R11(t *testing.T) {
	f := facts("a")
	f.Source[tref("a")].Columns = append(f.Source[tref("a")].Columns, col("gone", "text"), col("big", "bigint"))
	f.Target[tref("a")].Table.Columns = append(f.Target[tref("a")].Table.Columns, col("big", "integer"))
	p := run(f, sel(ModeAppend, "a"))
	has(t, p, SevError, "gone")
	has(t, p, SevError, "big")
}

func TestCheck_R12(t *testing.T) {
	f := facts("a", "b")
	f.Target[tref("a")].ReferencedBy = []catalog.TableRef{tref("b"), tref("c")}
	p := run(f, sel(ModeTruncate, "a"), sel(ModeTruncate, "b"))
	has(t, p, SevError, "s.c")
	if errorCount(p) != 1 {
		t.Errorf("only the outside table should error: %+v", p.Issues)
	}
	f.Target[tref("a")].ReferencedBy = []catalog.TableRef{tref("b")}
	if p := run(f, sel(ModeTruncate, "a"), sel(ModeTruncate, "b")); p.HasErrors() {
		t.Errorf("co-truncated referencing table is OK: %+v", p.Issues)
	}
	has(t, run(f, sel(ModeTruncate, "a"), sel(ModeAppend, "b")), SevError, "s.b")
	if p := run(f, sel(ModeAppend, "a")); p.HasErrors() {
		t.Errorf("append ignores referencing tables: %+v", p.Issues)
	}
}

func TestCheck_R13(t *testing.T) {
	f := facts("a")
	f.Target[tref("a")].Priv = catalog.Privileges{}
	p := run(f, sel(ModeTruncate, "a"))
	has(t, p, SevError, "TRUNCATE 권한")
	has(t, p, SevError, "INSERT 권한")
	p = run(f, sel(ModeAppend, "a"))
	has(t, p, SevError, "INSERT 권한")
	if errorCount(p) != 1 {
		t.Errorf("append needs only INSERT: %+v", p.Issues)
	}
}

func TestCheck_R14(t *testing.T) {
	f := facts("a")
	f.Target[tref("a")].Table.TriggerCount = 1
	p := run(f, sel(ModeAppend, "a"))
	has(t, p, SevInfo, "트리거")
	if p.HasErrors() {
		t.Errorf("info only: %+v", p.Issues)
	}
}

func TestCheck_R15(t *testing.T) {
	f := facts("a")
	seq := catalog.TableRef{Schema: "s", Name: "a_id_seq"}
	f.Target[tref("a")].Table.Sequences = []catalog.Sequence{{Ref: seq}}
	f.Target[tref("a")].SeqUpdate = map[catalog.TableRef]bool{seq: false}
	has(t, run(f, sel(ModeAppend, "a")), SevError, "a_id_seq")
	f.Target[tref("a")].SeqUpdate[seq] = true
	if p := run(f, sel(ModeAppend, "a")); p.HasErrors() {
		t.Errorf("unexpected: %+v", p.Issues)
	}
}

func TestCheck_R16(t *testing.T) {
	f := facts("a", "b")
	f.Source[tref("a")] = tbl("a", "b")
	f.Source[tref("b")] = tbl("b", "a")
	p := run(f, sel(ModeAppend, "a"), sel(ModeAppend, "b"))
	has(t, p, SevError, "순환")
	if len(p.Steps) != 2 || p.Steps[0].Sel.Ref != tref("a") {
		t.Errorf("selection order must be kept: %+v", p.Steps)
	}
}

func TestCheck_R17(t *testing.T) {
	p := run(facts("a"), sel(ModeTruncate, "a"))
	if is := has(t, p, SevWarning, "삭제"); is.Table != nil {
		t.Error("R17 must be global")
	}
	if !p.HasDestructive() {
		t.Error("HasDestructive")
	}
	p = run(facts("a"), sel(ModeAppend, "a"))
	if p.HasDestructive() {
		t.Error("append is not destructive")
	}
	for _, i := range p.Issues {
		if i.Severity == SevWarning {
			t.Errorf("no warning expected: %+v", i)
		}
	}
}

func TestCheck_IssueTableIsCopy(t *testing.T) {
	f := facts("a", "b")
	f.Target[tref("a")].Table = nil
	f.Target[tref("b")].Table = nil
	p := run(f, sel(ModeAppend, "a"), sel(ModeAppend, "b"))
	if len(p.Issues) != 2 || *p.Issues[0].Table == *p.Issues[1].Table {
		t.Errorf("issues must point at their own table: %+v", p.Issues)
	}
}

func TestCheck_HappyPath(t *testing.T) {
	f := facts("child", "parent")
	f.Source[tref("child")] = tbl("child", "parent")
	f.Source[tref("child")].Columns = simple("child").Columns
	f.Source[tref("parent")].Columns = append(simple("parent").Columns, catalog.Column{Name: "g", Type: "integer", Generated: 's'})
	f.SourceRows[tref("parent")] = 7
	f.Target[tref("parent")].Rows = 3
	p := run(f, sel(ModeAppend, "child"), sel(ModeTruncate, "parent"))
	if errorCount(p) != 0 {
		t.Fatalf("errors: %+v", p.Issues)
	}
	if len(p.Steps) != 2 || p.Steps[0].Sel.Ref != tref("parent") || p.Steps[1].Sel.Ref != tref("child") {
		t.Fatalf("steps: %+v", p.Steps)
	}
	if !reflect.DeepEqual(p.Steps[0].Columns, []string{"id", "nm"}) {
		t.Errorf("columns: %v", p.Steps[0].Columns)
	}
	if p.Steps[0].SourceRows != 7 || p.Steps[0].TargetRows != 3 || p.Steps[0].Target == nil {
		t.Errorf("step facts: %+v", p.Steps[0])
	}
	if len(p.Steps[0].DDL) != 0 {
		t.Error("no DDL outside create mode")
	}
}

func TestCheck_DropRecreateOnlyForCreate(t *testing.T) {
	for _, m := range []Mode{ModeTruncate, ModeAppend} {
		p := run(facts("a"), Selection{Ref: tref("a"), Mode: m, DropRecreate: true})
		has(t, p, SevError, "DROP 후 재생성")
	}
	p := &Plan{Steps: []Step{{Sel: Selection{Mode: ModeAppend, DropRecreate: true}}}}
	if p.HasDestructive() {
		t.Error("DropRecreate without create mode is not destructive")
	}
}
