package plan

import (
	"fmt"
	"strconv"
	"strings"

	"pgmigrate/internal/catalog"
)

const varcharPrefix = "character varying"

// varcharLen parses "character varying(n)". ok is false when t is not varchar;
// n is -1 when the length is unbounded.
func varcharLen(t string) (n int, ok bool) {
	if t == varcharPrefix {
		return -1, true
	}
	if strings.HasPrefix(t, varcharPrefix+"(") && strings.HasSuffix(t, ")") {
		v, err := strconv.Atoi(t[len(varcharPrefix)+1 : len(t)-1])
		if err != nil {
			return 0, false
		}
		return v, true
	}
	return 0, false
}

// TypeCompatible reports whether values of src can be copied into tgt as-is.
func TypeCompatible(src, tgt catalog.Column) bool {
	if src.Type == tgt.Type {
		return true
	}
	sn, sok := varcharLen(src.Type)
	if !sok {
		return false
	}
	if tgt.Type == "text" {
		return true
	}
	tn, tok := varcharLen(tgt.Type)
	if !tok {
		return false
	}
	if tn == -1 {
		return true
	}
	return sn != -1 && tn >= sn
}

// MatchColumns returns the COPY column list (source order, generated columns
// excluded on both sides) and the reasons the tables cannot be matched.
func MatchColumns(src, tgt []catalog.Column) (cols []string, problems []string) {
	tm := map[string]catalog.Column{}
	for _, c := range tgt {
		if c.Generated == 0 {
			tm[c.Name] = c
		}
	}
	used := map[string]bool{}
	for _, s := range src {
		if s.Generated != 0 {
			continue
		}
		t, ok := tm[s.Name]
		if !ok {
			problems = append(problems, fmt.Sprintf("컬럼 %s이(가) 타깃에 없습니다", s.Name))
			continue
		}
		used[s.Name] = true
		if !TypeCompatible(s, t) {
			problems = append(problems, fmt.Sprintf("컬럼 %s의 타입이 호환되지 않습니다 (소스 %s, 타깃 %s)", s.Name, s.Type, t.Type))
			continue
		}
		cols = append(cols, s.Name)
	}
	for _, t := range tgt {
		if t.Generated != 0 || used[t.Name] {
			continue
		}
		if t.NotNull && t.Default == "" && t.Identity == 0 {
			problems = append(problems, fmt.Sprintf("타깃 전용 컬럼 %s이(가) NOT NULL이고 기본값이 없습니다", t.Name))
		}
	}
	return cols, problems
}
