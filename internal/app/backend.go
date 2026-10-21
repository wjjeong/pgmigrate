// Package app wires config, catalog, plan, migrate and runlog together for the UI.
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/migrate"
	"pgmigrate/internal/plan"
	"pgmigrate/internal/runlog"
)

// Backend is what the UI talks to.
type Backend interface {
	Connect(ctx context.Context, src, tgt config.Conn, srcPassword, tgtPassword string) error
	ListTables(ctx context.Context) ([]catalog.TableInfo, error) // 소스 기준
	Plan(ctx context.Context, sel []plan.Selection) (*plan.Plan, error)
	Execute(ctx context.Context, p *plan.Plan, onEvent func(migrate.Event)) (res *migrate.Result, logPath string, err error)
	Close()
}

type pgBackend struct {
	logDir string
	opt    migrate.Options

	src, tgt         config.Conn
	srcPass, tgtPass string // 메모리에만 보관한다
	srcDB, tgtDB     *pgx.Conn
}

// NewPG returns a Backend that talks to real PostgreSQL servers.
func NewPG(logDir string, opt migrate.Options) Backend {
	return &pgBackend{logDir: logDir, opt: opt}
}

func dial(ctx context.Context, c config.Conn, password, role string) (*pgx.Conn, error) {
	cfg, err := c.PgxConfig(password)
	if err != nil {
		return nil, err
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("%s 연결 실패 (%s): %w", role, c.Name, err)
	}
	return conn, nil
}

func closeConn(c *pgx.Conn) {
	if c != nil {
		_ = c.Close(context.Background())
	}
}

func (b *pgBackend) Connect(ctx context.Context, src, tgt config.Conn, srcPassword, tgtPassword string) error {
	b.Close()
	if config.SameDatabase(src, tgt) {
		return errors.New("소스와 타깃이 같은 데이터베이스입니다")
	}
	s, err := dial(ctx, src, srcPassword, "소스")
	if err != nil {
		return err
	}
	t, err := dial(ctx, tgt, tgtPassword, "타깃")
	if err != nil {
		closeConn(s)
		return err
	}
	b.src, b.tgt, b.srcPass, b.tgtPass, b.srcDB, b.tgtDB = src, tgt, srcPassword, tgtPassword, s, t
	return nil
}

var errNotConnected = errors.New("연결되어 있지 않습니다")

func (b *pgBackend) ListTables(ctx context.Context) ([]catalog.TableInfo, error) {
	if b.srcDB == nil {
		return nil, errNotConnected
	}
	return catalog.ListTables(ctx, b.srcDB)
}

func (b *pgBackend) Plan(ctx context.Context, sel []plan.Selection) (*plan.Plan, error) {
	if b.srcDB == nil || b.tgtDB == nil {
		return nil, errNotConnected
	}
	return plan.Build(ctx, b.srcDB, b.tgtDB, b.src, b.tgt, sel)
}

func (b *pgBackend) Execute(ctx context.Context, p *plan.Plan, onEvent func(migrate.Event)) (*migrate.Result, string, error) {
	if b.srcDB == nil {
		return nil, "", errNotConnected
	}
	lf, err := runlog.Open(b.logDir, time.Now())
	if err != nil {
		return nil, "", err
	}
	defer lf.Close()
	path := lf.Path()
	writeHeader(lf, p)

	// 실행마다 새 연결을 연다 (Ruling 11).
	s, err := dial(ctx, b.src, b.srcPass, "소스")
	if err != nil {
		lf.Printf("실패: %v", err)
		return nil, path, err
	}
	defer closeConn(s)
	t, err := dial(ctx, b.tgt, b.tgtPass, "타깃")
	if err != nil {
		lf.Printf("실패: %v", err)
		return nil, path, err
	}
	defer closeConn(t)

	res, err := migrate.Execute(ctx, s, t, p, b.opt, lf, onEvent)
	if res != nil {
		for _, tr := range res.Tables {
			lf.Printf("결과 %s: 모드=%s 소스=%d 이전=%d 이후=%d (%s)",
				tr.Ref, tr.Mode, tr.SourceRows, tr.Before, tr.After, tr.Duration)
		}
	}
	switch {
	case err != nil:
		lf.Printf("최종 결과: 실패 (롤백됨): %v", err)
	case res != nil && res.Committed:
		lf.Printf("최종 결과: 커밋 완료")
	default:
		lf.Printf("최종 결과: 커밋되지 않음")
	}
	return res, path, err
}

func (b *pgBackend) Close() {
	closeConn(b.srcDB)
	closeConn(b.tgtDB)
	b.srcDB, b.tgtDB = nil, nil
}

var sevName = map[plan.Severity]string{plan.SevInfo: "정보", plan.SevWarning: "경고", plan.SevError: "오류"}

// writeHeader logs both endpoints (never passwords), per-table modes and issues.
func writeHeader(lf *runlog.File, p *plan.Plan) {
	for _, e := range []struct {
		role string
		c    config.Conn
	}{{"소스", p.Source}, {"타깃", p.Target}} {
		lf.Printf("%s: name=%s env=%s host=%s port=%d database=%s user=%s",
			e.role, e.c.Name, e.c.Env, e.c.Host, e.c.Port, e.c.Database, e.c.User)
	}
	for _, s := range p.Steps {
		lf.Printf("테이블 %s: 모드=%s DROP후재생성=%t", s.Sel.Ref, s.Sel.Mode, s.Sel.DropRecreate)
	}
	for _, i := range p.Issues {
		where := "전체"
		if i.Table != nil {
			where = i.Table.String()
		}
		lf.Printf("이슈 [%s] %s: %s", sevName[i.Severity], where, i.Message)
	}
}
