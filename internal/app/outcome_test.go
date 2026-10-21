package app

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"pgmigrate/internal/migrate"
)

type recLog struct{ lines []string }

func (r *recLog) Printf(format string, args ...any) { r.lines = append(r.lines, fmt.Sprintf(format, args...)) }

func TestWriteOutcome(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		res       *migrate.Result
		err       error
		want, not string
	}{
		{&migrate.Result{Committed: true}, nil, "최종 결과: 커밋 완료", "롤백"},
		{&migrate.Result{Refused: true}, boom, "최종 결과: 실행 전 거부 (변경 없음): boom", "롤백"},
		{&migrate.Result{CommitUnknown: true}, boom, "최종 결과: 커밋 여부 불명확 — 타깃 DB를 직접 확인하세요: boom", "롤백"},
		{&migrate.Result{}, boom, "최종 결과: 롤백됨: boom", "커밋 완료"},
		{nil, boom, "최종 결과: 실행되지 않음 (변경 없음): boom", "롤백"},
	} {
		l := &recLog{}
		writeOutcome(l, tc.res, tc.err)
		got := strings.Join(l.lines, "\n")
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
			t.Errorf("%+v: got %q, want %q without %q", tc.res, got, tc.want, tc.not)
		}
	}
}
