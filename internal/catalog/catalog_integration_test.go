//go:build integration

package catalog

import (
	"context"
	"fmt"
	"testing"

	"pgmigrate/internal/testutil"
)

func TestListTables(t *testing.T) {
	src := testutil.Source(t)
	s := testutil.Schema(t, src)
	testutil.MustExec(t, src, fmt.Sprintf(`CREATE TABLE %s."Order" (id int)`, s))
	testutil.MustExec(t, src, fmt.Sprintf(`CREATE TABLE %s.공통코드 (id int)`, s))
	testutil.MustExec(t, src, fmt.Sprintf(`CREATE TABLE %s.p (id int) PARTITION BY RANGE (id)`, s))

	all, err := ListTables(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	var got []TableInfo
	for _, ti := range all {
		if ti.Ref.Schema == s {
			got = append(got, ti)
		}
	}
	if len(got) != 3 {
		t.Fatalf("테이블 3개 기대, 실제 %d: %+v", len(got), got)
	}
	// 정렬: 바이트/로케일과 무관하게 스키마, 이름 순서를 요구한다.
	for i := 1; i < len(all); i++ {
		a, b := all[i-1].Ref, all[i].Ref
		if a.Schema > b.Schema || (a.Schema == b.Schema && a.Name > b.Name) {
			t.Fatalf("정렬 순서 위반: %v > %v", a, b)
		}
	}
	want := map[string]bool{"Order": false, "공통코드": false, "p": true}
	for _, ti := range got {
		part, ok := want[ti.Ref.Name]
		if !ok {
			t.Fatalf("예상치 못한 테이블: %v", ti.Ref)
		}
		if ti.Partitioned != part {
			t.Errorf("%v Partitioned=%v, 기대 %v", ti.Ref, ti.Partitioned, part)
		}
		if ti.EstRows < 0 {
			t.Errorf("%v EstRows 음수", ti.Ref)
		}
	}
	if got[0].Ref.Name != "Order" || got[1].Ref.Name != "p" || got[2].Ref.Name != "공통코드" {
		t.Errorf("정렬 순서가 다름: %v %v %v", got[0].Ref, got[1].Ref, got[2].Ref)
	}
}

func TestCountRows(t *testing.T) {
	src := testutil.Source(t)
	s := testutil.Schema(t, src)
	testutil.MustExec(t, src, fmt.Sprintf(`CREATE TABLE %s."Order" (id int)`, s))
	testutil.MustExec(t, src, fmt.Sprintf(`INSERT INTO %s."Order" VALUES (1),(2),(3)`, s))
	n, err := CountRows(context.Background(), src, TableRef{Schema: s, Name: "Order"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("행 수 3 기대, 실제 %d", n)
	}
}
