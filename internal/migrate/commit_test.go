package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestCommitOutcomeUnknown(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		unknown bool
	}{
		{"server error", &pgconn.PgError{Code: "23505"}, false},
		{"wrapped server error", fmt.Errorf("x: %w", &pgconn.PgError{Code: "40001"}), false},
		{"commit turned rollback", pgx.ErrTxCommitRollback, false},
		{"wrapped commit rollback", fmt.Errorf("x: %w", pgx.ErrTxCommitRollback), false},
		{"connection lost", io.ErrUnexpectedEOF, true},
		{"timeout", context.DeadlineExceeded, true},
		{"other", errors.New("conn closed"), true},
	} {
		if got := commitOutcomeUnknown(tc.err); got != tc.unknown {
			t.Errorf("%s: commitOutcomeUnknown = %v, want %v", tc.name, got, tc.unknown)
		}
	}
}

func TestResultOutcome(t *testing.T) {
	for _, tc := range []struct {
		res  *Result
		want string
	}{
		{nil, "실행되지 않음 (변경 없음)"},
		{&Result{Committed: true}, "커밋 완료"},
		{&Result{Refused: true}, "실행 전 거부 (변경 없음)"},
		{&Result{CommitUnknown: true}, "커밋 여부 불명확 — 타깃 DB를 직접 확인하세요"},
		{&Result{}, "롤백됨"},
	} {
		if got := tc.res.Outcome(); got != tc.want {
			t.Errorf("%+v: Outcome = %q, want %q", tc.res, got, tc.want)
		}
	}
}
