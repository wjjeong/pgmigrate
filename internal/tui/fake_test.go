package tui

import (
	"context"
	"sync"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/migrate"
	"pgmigrate/internal/plan"
)

// fakeBackend records calls and returns canned values. It never touches a DB.
type fakeBackend struct {
	mu sync.Mutex

	tables  []catalog.TableInfo
	plan    *plan.Plan
	events  []migrate.Event
	result  *migrate.Result
	logPath string
	execErr error
	// blockExec makes Execute wait for ctx cancellation after sending events.
	blockExec bool

	connectCalls int
	connSrc      config.Conn
	connTgt      config.Conn
	connPw       [2]string
	planCalls    [][]plan.Selection
	execCalls    int
	execCtx      context.Context
}

func (f *fakeBackend) Connect(ctx context.Context, src, tgt config.Conn, srcPw, tgtPw string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connectCalls++
	f.connSrc, f.connTgt, f.connPw = src, tgt, [2]string{srcPw, tgtPw}
	return nil
}

func (f *fakeBackend) ListTables(ctx context.Context) ([]catalog.TableInfo, error) {
	return f.tables, nil
}

func (f *fakeBackend) Plan(ctx context.Context, sel []plan.Selection) (*plan.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.planCalls = append(f.planCalls, append([]plan.Selection(nil), sel...))
	return f.plan, nil
}

func (f *fakeBackend) Execute(ctx context.Context, p *plan.Plan, onEvent func(migrate.Event)) (*migrate.Result, string, error) {
	f.mu.Lock()
	f.execCalls++
	f.execCtx = ctx
	f.mu.Unlock()
	for _, e := range f.events {
		onEvent(e)
	}
	if f.blockExec {
		<-ctx.Done()
		onEvent(migrate.Event{Phase: migrate.PhaseFailed, Err: ctx.Err()})
		return &migrate.Result{Committed: false}, f.logPath, ctx.Err()
	}
	return f.result, f.logPath, f.execErr
}

func (f *fakeBackend) Close() {}

func (f *fakeBackend) execCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.execCalls
}
