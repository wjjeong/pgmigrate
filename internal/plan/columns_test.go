package plan

import (
	"reflect"
	"testing"

	"pgmigrate/internal/catalog"
)

func col(name, typ string) catalog.Column { return catalog.Column{Name: name, Type: typ} }

func TestTypeCompatible(t *testing.T) {
	cases := []struct {
		src, tgt string
		want     bool
	}{
		{"integer", "integer", true},
		{"character varying(10)", "character varying(50)", true},
		{"character varying(10)", "character varying(10)", true},
		{"character varying(50)", "character varying(10)", false},
		{"character varying(10)", "text", true},
		{"character varying", "text", true},
		{"character varying(10)", "character varying", true},
		{"character varying", "character varying(10)", false},
		{"text", "character varying(10)", false},
		{"integer", "bigint", false},
		{"numeric(10,2)", "numeric(12,2)", false},
	}
	for _, c := range cases {
		if got := TypeCompatible(col("a", c.src), col("a", c.tgt)); got != c.want {
			t.Errorf("%s -> %s: got %v want %v", c.src, c.tgt, got, c.want)
		}
	}
}

func TestMatchColumns(t *testing.T) {
	gen := catalog.Column{Name: "g", Type: "integer", Generated: 's'}
	src := []catalog.Column{col("id", "integer"), col("nm", "text"), gen, col("extra", "text"), col("n", "bigint")}
	tgt := []catalog.Column{
		col("n", "integer"), col("id", "integer"), col("nm", "text"), gen,
		{Name: "must", Type: "text", NotNull: true},
		{Name: "dflt", Type: "text", NotNull: true, Default: "'x'"},
		{Name: "ident", Type: "integer", NotNull: true, Identity: 'd'},
		{Name: "opt", Type: "text"},
	}
	cols, problems := MatchColumns(src, tgt)
	if want := []string{"id", "nm"}; !reflect.DeepEqual(cols, want) {
		t.Errorf("cols = %v, want %v", cols, want)
	}
	if len(problems) != 3 { // extra 없음, n 타입, must
		t.Fatalf("problems = %v", problems)
	}
	cols, problems = MatchColumns(src[:2], tgt[1:3])
	if len(problems) != 0 || !reflect.DeepEqual(cols, []string{"id", "nm"}) {
		t.Errorf("clean match: %v %v", cols, problems)
	}
}
