//go:build integration

package app

import (
	"context"
	"os"
	"strings"
	"testing"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/migrate"
	"pgmigrate/internal/plan"
	"pgmigrate/internal/testutil"
)

const testPass = "postgres" // docker 이미지는 TCP 접속에 비밀번호 인증을 강제한다

func conns() (config.Conn, config.Conn) {
	return config.Conn{Name: "src", Env: "dev", Host: "localhost", Port: 55432, Database: "postgres", User: "postgres", SSLMode: "disable"},
		config.Conn{Name: "tgt", Env: "dev", Host: "localhost", Port: 55433, Database: "postgres", User: "postgres", SSLMode: "disable"}
}

func TestBackend_EndToEnd(t *testing.T) {
	src, tgt := testutil.Source(t), testutil.Target(t)
	s := testutil.Schema(t, src, tgt)
	testutil.MustExec(t, src, "CREATE TABLE "+s+".items (id int primary key, name text)")
	testutil.MustExec(t, src, "INSERT INTO "+s+".items VALUES (1,'a'),(2,'b'),(3,'c')")

	sc, tc := conns()
	dir := t.TempDir()
	b := NewPG(dir, migrate.Options{})
	defer b.Close()
	ctx := context.Background()

	// 같은 DB는 거부된다.
	if err := b.Connect(ctx, sc, sc, testPass, testPass); err == nil {
		t.Fatal("같은 DB 연결이 거부되어야 합니다")
	}
	if err := b.Connect(ctx, sc, tc, testPass, testPass); err != nil {
		t.Fatal(err)
	}
	// 재연결은 이전 연결을 닫고 성공해야 한다.
	if err := b.Connect(ctx, sc, tc, testPass, testPass); err != nil {
		t.Fatal(err)
	}

	tables, err := b.ListTables(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref := catalog.TableRef{Schema: s, Name: "items"}
	found := false
	for _, ti := range tables {
		found = found || ti.Ref == ref
	}
	if !found {
		t.Fatalf("items 테이블이 목록에 없습니다: %+v", tables)
	}

	p, err := b.Plan(ctx, []plan.Selection{{Ref: ref, Mode: plan.ModeCreate}})
	if err != nil {
		t.Fatal(err)
	}
	if p.HasErrors() {
		t.Fatalf("계획 오류: %+v", p.Issues)
	}

	var events []migrate.Event
	res, logPath, err := b.Execute(ctx, p, func(e migrate.Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !res.Committed || len(events) == 0 {
		t.Fatalf("committed=%v events=%d", res.Committed, len(events))
	}
	var n int
	if err := tgt.QueryRow(ctx, "SELECT count(*) FROM "+s+".items").Scan(&n); err != nil || n != 3 {
		t.Fatalf("타깃 행 수 %d, err=%v", n, err)
	}

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("로그 파일이 없습니다: %v", err)
	}
	log := string(raw)
	// 비밀번호가 사용자명과 같은 값이라 리터럴 검색은 의미가 없으므로 비밀번호 표기 형태를 검사한다.
	for _, bad := range []string{"password", "Password", "postgres:" + testPass} {
		if strings.Contains(log, bad) {
			t.Fatalf("로그에 %q 포함:\n%s", bad, log)
		}
	}
	for _, want := range []string{"host=localhost", "port=55432", "port=55433", "user=postgres", "모드=create", "커밋 완료", s + ".items"} {
		if !strings.Contains(log, want) {
			t.Fatalf("로그에 %q 없음:\n%s", want, log)
		}
	}
}

func TestBackend_ConnectFailureClosesFirst(t *testing.T) {
	sc, tc := conns()
	tc.Port = 1 // 연결 불가
	b := NewPG(t.TempDir(), migrate.Options{})
	defer b.Close()
	if err := b.Connect(context.Background(), sc, tc, testPass, testPass); err == nil {
		t.Fatal("타깃 연결 실패 오류가 필요합니다")
	}
	if _, err := b.ListTables(context.Background()); err == nil {
		t.Fatal("실패한 Connect 후에는 연결이 없어야 합니다")
	}
}

// TestBackend_SameServerDifferentSpellingRefused: 호스트 표기만 다른 같은 DB는
// 문자열 비교를 통과하지만 서버 식별 정보 비교로 거부되어야 한다 (F2).
func TestBackend_SameServerDifferentSpellingRefused(t *testing.T) {
	sc, tc := conns()
	alias := sc
	alias.Name, alias.Host = "src-ip", "127.0.0.1"
	if config.SameDatabase(sc, alias) {
		t.Fatal("문자열 사전 검사가 이미 같다고 판단합니다; 테스트 전제가 깨졌습니다")
	}
	b := NewPG(t.TempDir(), migrate.Options{})
	defer b.Close()
	err := b.Connect(context.Background(), sc, alias, testPass, testPass)
	if err == nil || !strings.Contains(err.Error(), "같은 데이터베이스") {
		t.Fatalf("같은 DB 거부 오류가 필요합니다: %v", err)
	}
	if _, err := b.ListTables(context.Background()); err == nil {
		t.Fatal("거부된 Connect 후에는 연결이 없어야 합니다")
	}
	// 다른 서버의 DB는 허용된다.
	if err := b.Connect(context.Background(), sc, tc, testPass, testPass); err != nil {
		t.Fatal(err)
	}
}
