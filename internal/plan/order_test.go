package plan

import (
	"errors"
	"reflect"
	"testing"

	"pgmigrate/internal/catalog"
)

func tref(n string) catalog.TableRef { return catalog.TableRef{Schema: "s", Name: n} }

func tbl(n string, parents ...string) *catalog.Table {
	t := &catalog.Table{Ref: tref(n)}
	for _, p := range parents {
		r := tref(p)
		t.Constraints = append(t.Constraints, catalog.Constraint{Name: n + "_" + p, Type: 'f', RefTable: &r})
	}
	return t
}

func tmap(ts ...*catalog.Table) map[catalog.TableRef]*catalog.Table {
	m := map[catalog.TableRef]*catalog.Table{}
	for _, t := range ts {
		m[t.Ref] = t
	}
	return m
}

func TestOrder(t *testing.T) {
	m := tmap(tbl("a", "p"), tbl("p"), tbl("q"), tbl("self", "self"), tbl("outsider", "notselected"))
	got, err := Order(m, []catalog.TableRef{tref("self"), tref("a"), tref("q"), tref("p"), tref("outsider")})
	if err != nil {
		t.Fatal(err)
	}
	want := []catalog.TableRef{tref("outsider"), tref("p"), tref("q"), tref("self"), tref("a")}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestOrder_Cycle(t *testing.T) {
	m := tmap(tbl("a", "b"), tbl("b", "a"))
	if _, err := Order(m, []catalog.TableRef{tref("a"), tref("b")}); !errors.Is(err, ErrCycle) {
		t.Errorf("err = %v", err)
	}
}
