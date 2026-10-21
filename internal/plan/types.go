// Package plan decides what a migration will do and whether it may run.
package plan

import (
	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
)

// Mode is how one table is migrated.
type Mode string

const (
	ModeCreate   Mode = "create"
	ModeTruncate Mode = "truncate"
	ModeAppend   Mode = "append"
)

// Selection is one table the user chose to migrate.
type Selection struct {
	Ref          catalog.TableRef
	Mode         Mode
	DropRecreate bool
}

// Severity grades an Issue.
type Severity int

const (
	SevInfo Severity = iota
	SevWarning
	SevError
)

// Issue is one finding of the rule check. Table is nil for global findings.
type Issue struct {
	Table    *catalog.TableRef
	Severity Severity
	Message  string
}

// Step is the planned work for one table.
type Step struct {
	Sel        Selection
	Source     *catalog.Table
	Target     *catalog.Table // 실행 전 타깃 구조 (create이고 없으면 nil)
	Columns    []string       // COPY 컬럼 (generated 제외, 소스 순서)
	DDL        []string       // create일 때 ddl.CreateTable 결과
	FKDDL      []string       // create일 때 ddl.ForeignKeys 결과
	SourceRows int64
	TargetRows int64 // 미리보기 시점 (없으면 0)
}

// Plan is the ordered set of steps plus all findings.
type Plan struct {
	Source, Target config.Conn
	Steps          []Step // 부모→자식 순
	Issues         []Issue
}

// HasErrors reports whether any Issue is an error.
func (p *Plan) HasErrors() bool {
	for _, i := range p.Issues {
		if i.Severity == SevError {
			return true
		}
	}
	return false
}

// HasDestructive reports whether any step truncates or drops a target table.
func (p *Plan) HasDestructive() bool {
	for _, s := range p.Steps {
		if s.Sel.Mode == ModeTruncate || s.Sel.DropRecreate {
			return true
		}
	}
	return false
}
