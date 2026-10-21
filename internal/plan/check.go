package plan

import (
	"fmt"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/ddl"
)

// TargetFacts is everything Check needs to know about one target table.
type TargetFacts struct {
	Table           *catalog.Table // nil = 없음
	SchemaExists    bool
	SchemaCreate    bool
	Priv            catalog.Privileges
	ReferencedBy    []catalog.TableRef
	DependentViews  []catalog.TableRef
	MissingTypes    []string           // "schema.name"
	MissingFKTables []catalog.TableRef // create 시 선택 밖 FK 대상 중 타깃에 없는 것
	SeqUpdate       map[catalog.TableRef]bool
	Rows            int64
}

// Facts is the gathered database state the rules are checked against.
type Facts struct {
	Source     map[catalog.TableRef]*catalog.Table
	SourceRows map[catalog.TableRef]int64
	Target     map[catalog.TableRef]*TargetFacts
}

func refList(refs []catalog.TableRef) string {
	s := ""
	for i, r := range refs {
		if i > 0 {
			s += ", "
		}
		s += r.String()
	}
	return s
}

// Check applies rules R1-R17 to f and builds the Plan. It does no I/O.
func Check(src, tgt config.Conn, sel []Selection, f *Facts) *Plan {
	p := &Plan{Source: src, Target: tgt}
	add := func(ref *catalog.TableRef, sev Severity, format string, a ...any) {
		var cp *catalog.TableRef
		if ref != nil {
			c := *ref
			cp = &c
		}
		p.Issues = append(p.Issues, Issue{Table: cp, Severity: sev, Message: fmt.Sprintf(format, a...)})
	}

	if config.SameDatabase(src, tgt) { // R1
		add(nil, SevError, "소스와 타깃이 같은 데이터베이스입니다 (%s:%d/%s)", src.Host, src.Port, src.Database)
	}

	inSel := map[catalog.TableRef]bool{}
	refs := make([]catalog.TableRef, 0, len(sel))
	for _, s := range sel {
		inSel[s.Ref] = true
		refs = append(refs, s.Ref)
	}
	c := &checker{tgt: tgt, add: add}
	byRef := map[catalog.TableRef]Selection{}
	for _, s := range sel {
		byRef[s.Ref] = s
	}
	outside := func(in []catalog.TableRef, ok func(Selection) bool) []catalog.TableRef {
		var out []catalog.TableRef
		for _, r := range in {
			if sl, found := byRef[r]; !found || !ok(sl) {
				out = append(out, r)
			}
		}
		return out
	}
	ordered, err := Order(f.Source, refs)
	if err != nil { // R16
		add(nil, SevError, "선택한 테이블 사이에 FK 순환이 있어 순서를 정할 수 없습니다")
		ordered = refs
	}

	for _, ref := range ordered {
		s := byRef[ref]
		ref := ref
		st := Step{Sel: s, Source: f.Source[ref], SourceRows: f.SourceRows[ref]}
		tf := f.Target[ref]
		if tf == nil {
			tf = &TargetFacts{}
		}
		st.Target = tf.Table
		st.TargetRows = tf.Rows
		c.table(&st, tf, outside)
		p.Steps = append(p.Steps, st)
	}

	if p.HasDestructive() { // R17
		add(nil, SevWarning, "타깃의 기존 데이터를 삭제하는 작업(TRUNCATE 또는 DROP 후 재생성)이 포함되어 있습니다")
	}
	return p
}

type checker struct {
	tgt config.Conn
	add func(ref *catalog.TableRef, sev Severity, format string, a ...any)
}

type outsideFn func([]catalog.TableRef, func(Selection) bool) []catalog.TableRef

func (c *checker) table(st *Step, tf *TargetFacts, outside outsideFn) {
	s := st.Sel
	ref := s.Ref
	add := c.add
	src := st.Source
	if src == nil {
		add(&ref, SevError, "%s: 소스 테이블 정보가 없습니다", ref)
		return
	}
	tgt := tf.Table

	if src.Partitioned || src.IsPartition || src.Inherits { // R2
		add(&ref, SevError, "%s: 파티션 또는 상속 테이블은 지원하지 않습니다 (소스)", ref)
	}
	if tgt != nil && (tgt.Partitioned || tgt.IsPartition || tgt.Inherits) {
		add(&ref, SevError, "%s: 파티션 또는 상속 테이블은 지원하지 않습니다 (타깃)", ref)
	}
	if !tf.SchemaExists { // R3
		add(&ref, SevError, "%s: 타깃에 스키마 %s이(가) 없습니다", ref, ref.Schema)
	}

	if s.DropRecreate && s.Mode != ModeCreate {
		add(&ref, SevError, "%s: DROP 후 재생성은 새로 만들기 모드에서만 선택할 수 있습니다", ref)
	}
	switch s.Mode {
	case ModeCreate:
		c.create(st, tf, outside)
	case ModeTruncate, ModeAppend:
		c.load(st, tf, outside)
	}
}

func (c *checker) create(st *Step, tf *TargetFacts, outside outsideFn) {
	s, src := st.Sel, st.Source
	ref := s.Ref
	add := c.add
	if s.DropRecreate && !c.tgt.AllowDrop { // R5
		add(&ref, SevError, "%s: 타깃 설정에 allow_drop: true가 없어 DROP 후 재생성을 사용할 수 없습니다", ref)
	}
	if tf.Table != nil {
		if !s.DropRecreate { // R4
			add(&ref, SevError, "%s: 타깃에 이미 테이블이 있습니다 (DROP 후 재생성을 선택해야 합니다)", ref)
			return
		}
		if c.tgt.AllowDrop { // R6
			add(&ref, SevWarning, "%s: DROP 후 재생성하면 기존 GRANT, OWNER, 트리거, RLS 설정이 사라집니다", ref)
			if len(tf.DependentViews) > 0 {
				add(&ref, SevError, "%s: 이 테이블에 의존하는 뷰가 있어 DROP할 수 없습니다 (%s)", ref, refList(tf.DependentViews))
			}
			if out := outside(tf.ReferencedBy, func(o Selection) bool { return o.Mode == ModeCreate && o.DropRecreate }); len(out) > 0 {
				add(&ref, SevError, "%s: DROP 후 재생성 대상이 아닌 테이블이 이 테이블을 FK로 참조합니다 (%s)", ref, refList(out))
			}
			if !tf.Priv.Owner {
				add(&ref, SevError, "%s: DROP하려면 테이블 소유자 권한이 필요합니다", ref)
			}
		}
	}

	for _, col := range src.Columns {
		if col.Generated == 0 {
			st.Columns = append(st.Columns, col.Name)
		}
	}
	st.DDL = ddl.CreateTable(src)
	st.FKDDL = ddl.ForeignKeys(src)

	for _, t := range tf.MissingTypes { // R7
		add(&ref, SevError, "%s: 타깃에 타입 %s이(가) 없습니다", ref, t)
	}
	for _, t := range tf.MissingFKTables { // R7
		add(&ref, SevError, "%s: FK가 참조하는 테이블 %s이(가) 타깃에 없고 선택에도 없습니다", ref, t)
	}
	if tf.SchemaExists && !tf.SchemaCreate { // R8
		add(&ref, SevError, "%s: 타깃 스키마 %s에 CREATE 권한이 없습니다", ref, ref.Schema)
	}
	if src.TriggerCount > 0 { // R9
		add(&ref, SevWarning, "%s: 소스의 트리거 %d개는 타깃에 생성되지 않습니다", ref, src.TriggerCount)
	}
	if src.HasRLS {
		add(&ref, SevWarning, "%s: 소스의 행 수준 보안(RLS) 설정은 타깃에 생성되지 않습니다", ref)
	}
}

func (c *checker) load(st *Step, tf *TargetFacts, outside outsideFn) {
	s, src, tgt := st.Sel, st.Source, tf.Table
	ref := s.Ref
	add := c.add
	if tgt == nil { // R10
		add(&ref, SevError, "%s: 타깃에 테이블이 없습니다", ref)
		return
	}
	cols, problems := MatchColumns(src.Columns, tgt.Columns)
	st.Columns = cols
	for _, pr := range problems { // R11
		add(&ref, SevError, "%s: %s", ref, pr)
	}
	if s.Mode == ModeTruncate {
		if out := outside(tf.ReferencedBy, func(o Selection) bool { return o.Mode == ModeTruncate }); len(out) > 0 { // R12
			add(&ref, SevError, "%s: 함께 TRUNCATE하지 않는 테이블이 이 테이블을 FK로 참조하여 TRUNCATE할 수 없습니다 (%s)", ref, refList(out))
		}
		if !tf.Priv.Truncate { // R13
			add(&ref, SevError, "%s: 타깃 테이블에 TRUNCATE 권한이 없습니다", ref)
		}
	}
	if !tf.Priv.Insert { // R13
		add(&ref, SevError, "%s: 타깃 테이블에 INSERT 권한이 없습니다", ref)
	}
	if tgt.TriggerCount > 0 { // R14
		add(&ref, SevInfo, "%s: 타깃 트리거 %d개가 데이터 적재 중 실행됩니다", ref, tgt.TriggerCount)
	}
	for _, q := range tgt.Sequences { // R15
		if !tf.SeqUpdate[q.Ref] {
			add(&ref, SevError, "%s: 시퀀스 %s에 UPDATE 권한이 없어 값을 맞출 수 없습니다", ref, q.Ref)
		}
	}
}
