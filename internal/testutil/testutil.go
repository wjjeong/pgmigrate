//go:build integration

// Package testutil provides helpers for integration tests against the
// docker-compose.test.yml databases.
package testutil

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	defaultSrc = "postgres://postgres:postgres@localhost:55432/postgres"
	defaultTgt = "postgres://postgres:postgres@localhost:55433/postgres"
)

// Source connects to the source test database.
func Source(t *testing.T) *pgx.Conn {
	t.Helper()
	return connect(t, "PGMIGRATE_TEST_SRC", defaultSrc)
}

// Target connects to the target test database.
func Target(t *testing.T) *pgx.Conn {
	t.Helper()
	return connect(t, "PGMIGRATE_TEST_TGT", defaultTgt)
}

func connect(t *testing.T, env, def string) *pgx.Conn {
	t.Helper()
	dsn := os.Getenv(env)
	if dsn == "" {
		dsn = def
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("테스트 DB에 접속할 수 없습니다 (%s): %v\n먼저 `docker compose -f docker-compose.test.yml up -d`를 실행하세요.", env, err)
	}
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	return c
}

// Schema creates a unique schema "t_<rand>" on every conn and drops it on cleanup.
func Schema(t *testing.T, conns ...*pgx.Conn) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("난수 생성 실패: %v", err)
	}
	name := "t_" + hex.EncodeToString(b)
	q := pgx.Identifier{name}.Sanitize()
	for _, c := range conns {
		MustExec(t, c, "CREATE SCHEMA "+q)
	}
	t.Cleanup(func() {
		for _, c := range conns {
			// 테스트 정리 전용: 프로덕션 코드가 아니므로 CASCADE 금지 규칙의 예외 (Ruling 1).
			_, _ = c.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+q+" CASCADE")
		}
	})
	return name
}

// MustExec runs sql and fails the test on error.
func MustExec(t *testing.T, c *pgx.Conn, sql string) {
	t.Helper()
	if _, err := c.Exec(context.Background(), sql); err != nil {
		t.Fatalf("SQL 실행 실패: %v\n%s", err, sql)
	}
}
