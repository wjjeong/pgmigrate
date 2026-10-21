// Package migrate executes a plan against the source and target databases.
// All target changes happen in one transaction: either everything commits or
// nothing does.
package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/ddl"
	"pgmigrate/internal/plan"
	"pgmigrate/internal/runlog"
)

// Options tunes Execute.
type Options struct{ LockTimeout time.Duration } // 0이면 10s

const defaultLockTimeout = 10 * time.Second

// commitTimeout bounds COMMIT, which is not cancellable once sent.
const commitTimeout = 60 * time.Second

// Phase names a stage of Execute, reported through Event.
type Phase string

const (
	PhaseStart    Phase = "start"
	PhaseDDL      Phase = "ddl"
	PhaseTruncate Phase = "truncate"
	PhaseCopy     Phase = "copy"
	PhaseVerify   Phase = "verify"
	PhaseSequence Phase = "sequence"
	PhaseCommit   Phase = "commit"
	PhaseDone     Phase = "done"
	PhaseFailed   Phase = "failed"
)

// Event reports progress. Table is nil for events that are not about one table.
type Event struct {
	Table *catalog.TableRef
	Phase Phase
	Rows  int64
	Err   error
}

// TableResult is the outcome for one table.
type TableResult struct {
	Ref                       catalog.TableRef
	Mode                      plan.Mode
	SourceRows, Before, After int64
	Duration                  time.Duration
}

// Result is the outcome of Execute. At most one of Committed, Refused and
// CommitUnknown is true; none of them means the target was rolled back.
type Result struct {
	Tables    []TableResult
	Committed bool
	// Refused: Execute refused before touching any database (no changes).
	Refused bool
	// CommitUnknown: COMMIT was sent but its outcome is unknown (e.g. the
	// connection was lost); the target may or may not have committed.
	CommitUnknown bool
}

// Outcome returns the Korean label of the result shown in the UI and the log.
// A nil Result means Execute never ran (no changes).
func (r *Result) Outcome() string {
	switch {
	case r == nil:
		return "실행되지 않음 (변경 없음)"
	case r.Committed:
		return "커밋 완료"
	case r.Refused:
		return "실행 전 거부 (변경 없음)"
	case r.CommitUnknown:
		return "커밋 여부 불명확 — 타깃 DB를 직접 확인하세요"
	}
	return "롤백됨"
}

// commitOutcomeUnknown reports whether a COMMIT error leaves the outcome
// unknown. A server error or a COMMIT answered with ROLLBACK means the
// transaction was definitely rolled back; anything else (connection loss,
// timeout, ...) may have happened after the server committed.
func commitOutcomeUnknown(err error) bool {
	var pgErr *pgconn.PgError
	return !errors.As(err, &pgErr) && !errors.Is(err, pgx.ErrTxCommitRollback)
}

// copySelectSQL returns the source query whose rows are copied for s.
// It is a variable so tests can inject a failing query.
var copySelectSQL = func(s plan.Step) string {
	return "SELECT " + quoteCols(s.Columns) + " FROM " + s.Sel.Ref.Quoted()
}

func quoteCols(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = pgx.Identifier{c}.Sanitize()
	}
	return strings.Join(q, ", ")
}

// errTargetCopyFailed unblocks the source COPY goroutine when the target side
// stops reading; it is never reported to the caller.
var errTargetCopyFailed = errors.New("타깃 COPY가 중단되었습니다")

type runner struct {
	ctx      context.Context
	src, tgt *pgx.Conn
	srcTx    pgx.Tx
	tgtTx    pgx.Tx
	log      runlog.Logger
	emit     func(Event)
	res      *Result
}

// stepError is an error tied to a table (nil for statement-wide work) and phase.
type stepError struct {
	ref   *catalog.TableRef
	phase Phase
	err   error
}

func fail(ref *catalog.TableRef, phase Phase, err error) *stepError {
	return &stepError{ref: ref, phase: phase, err: err}
}

// Execute runs p. On failure it rolls back both transactions and returns a
// non-nil Result (Committed=false) together with the error; Refused is set when
// it refused before touching any database, CommitUnknown when COMMIT failed in
// a way that leaves the outcome unknown. ctx cancellation is honoured only
// until COMMIT is sent.
func Execute(ctx context.Context, src, tgt *pgx.Conn, p *plan.Plan, opt Options, log runlog.Logger, onEvent func(Event)) (*Result, error) {
	if log == nil {
		log = runlog.Discard()
	}
	r := &runner{ctx: ctx, src: src, tgt: tgt, log: log, res: &Result{}}
	r.emit = func(e Event) {
		if onEvent != nil {
			onEvent(e)
		}
	}

	// DB를 건드리기 전에 거부해야 하는 경우.
	var refuse error
	if p.HasErrors() {
		refuse = errors.New("계획에 오류가 있어 실행할 수 없습니다")
	} else if !p.Target.AllowDrop {
		for _, s := range p.Steps {
			if s.Sel.DropRecreate {
				refuse = fmt.Errorf("%s: 타깃 설정에 allow_drop: true가 없어 DROP 후 재생성을 실행할 수 없습니다", s.Sel.Ref)
				break
			}
		}
	}
	if refuse != nil {
		r.res.Refused = true
		log.Printf("실행 거부: %v", refuse)
		r.emit(Event{Phase: PhaseFailed, Err: refuse})
		return r.res, refuse
	}

	for _, s := range p.Steps {
		r.res.Tables = append(r.res.Tables, TableResult{Ref: s.Sel.Ref, Mode: s.Sel.Mode})
	}
	if se := r.run(p, opt); se != nil {
		return r.res, r.abort(se)
	}
	return r.res, nil
}

// abort rolls back both sides and reports the wrapped error.
func (r *runner) abort(se *stepError) error {
	err := se.err
	if cerr := r.ctx.Err(); cerr != nil && se.phase != PhaseCommit && !errors.Is(err, cerr) {
		// 취소 중에 서버가 돌려준 오류(57014 등)도 취소로 판별되게 한다.
		// COMMIT은 취소되지 않으므로 그 오류에는 붙이지 않는다.
		err = fmt.Errorf("%w: %w", cerr, err)
	}
	if se.ref != nil {
		err = fmt.Errorf("%s: %s: %w", se.ref, se.phase, err)
	} else {
		err = fmt.Errorf("%s: %w", se.phase, err)
	}
	r.log.Printf("오류: %v", err)
	r.rollback()
	r.emit(Event{Table: se.ref, Phase: PhaseFailed, Err: err})
	return err
}

func (r *runner) rollback() {
	// ctx가 취소되었어도 롤백은 반드시 보낸다.
	bg := context.Background()
	if r.tgtTx != nil {
		r.log.Printf("[타깃] ROLLBACK")
		if err := r.tgtTx.Rollback(bg); err != nil {
			r.log.Printf("[타깃] ROLLBACK 실패 (연결이 끊기면 서버가 트랜잭션을 버립니다): %v", err)
		}
	}
	if r.srcTx != nil {
		r.log.Printf("[소스] ROLLBACK")
		if err := r.srcTx.Rollback(bg); err != nil {
			r.log.Printf("[소스] ROLLBACK 실패: %v", err)
		}
	}
}

func (r *runner) tgtExec(sql string) error {
	r.log.Printf("[타깃] %s", sql)
	_, err := r.tgtTx.Exec(r.ctx, sql)
	return err
}

func (r *runner) count(q catalog.Querier, side string, ref catalog.TableRef) (int64, error) {
	r.log.Printf("[%s] SELECT count(*) FROM %s", side, ref.Quoted())
	return catalog.CountRows(r.ctx, q, ref)
}

func refPtr(ref catalog.TableRef) *catalog.TableRef { return &ref }

func joinRefs(steps []plan.Step) string {
	q := make([]string, len(steps))
	for i, s := range steps {
		q[i] = s.Sel.Ref.Quoted()
	}
	return strings.Join(q, ", ")
}

// timed runs f and adds its duration to table i.
func (r *runner) timed(i int, f func() error) error {
	start := time.Now()
	err := f()
	r.res.Tables[i].Duration += time.Since(start)
	return err
}

func (r *runner) run(p *plan.Plan, opt Options) *stepError {
	r.emit(Event{Phase: PhaseStart})
	lt := opt.LockTimeout
	if lt <= 0 {
		lt = defaultLockTimeout
	}
	ms := lt.Milliseconds()
	if ms < 1 {
		ms = 1 // 0ms는 "제한 없음"이 되므로 피한다.
	}

	// 2. 소스: 일관된 스냅샷을 읽기 전용으로.
	r.log.Printf("[소스] BEGIN ISOLATION LEVEL REPEATABLE READ READ ONLY")
	tx, err := r.src.BeginTx(r.ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fail(nil, PhaseStart, fmt.Errorf("소스 트랜잭션 시작 실패: %w", err))
	}
	r.srcTx = tx
	// 소스 COPY도 잠금을 무한정 기다리지 않게 한다 (그동안 타깃 잠금을 쥐고 있다).
	lockSQL := fmt.Sprintf("SET LOCAL lock_timeout = '%dms'", ms)
	r.log.Printf("[소스] %s", lockSQL)
	if _, err := r.srcTx.Exec(r.ctx, lockSQL); err != nil {
		return fail(nil, PhaseStart, err)
	}

	// 3. 타깃: 하나의 트랜잭션.
	r.log.Printf("[타깃] BEGIN")
	if tx, err = r.tgt.Begin(r.ctx); err != nil {
		return fail(nil, PhaseStart, fmt.Errorf("타깃 트랜잭션 시작 실패: %w", err))
	}
	r.tgtTx = tx
	for _, sql := range []string{
		lockSQL,
		"SET LOCAL standard_conforming_strings = on", // ddl의 문자열 리터럴이 이 설정에 의존한다.
	} {
		if err := r.tgtExec(sql); err != nil {
			return fail(nil, PhaseStart, err)
		}
	}

	var loads, truncs []plan.Step
	var loadIdx []int
	for i, s := range p.Steps {
		if s.Sel.Mode == plan.ModeTruncate || s.Sel.Mode == plan.ModeAppend {
			loads = append(loads, s)
			loadIdx = append(loadIdx, i)
		}
		if s.Sel.Mode == plan.ModeTruncate {
			truncs = append(truncs, s)
		}
	}

	// 4. DROP 후 재생성: 자식부터 (Steps 역순).
	for i := len(p.Steps) - 1; i >= 0; i-- {
		s := p.Steps[i]
		if s.Sel.Mode != plan.ModeCreate || !s.Sel.DropRecreate || s.Target == nil {
			continue
		}
		ref := s.Sel.Ref
		r.emit(Event{Table: refPtr(ref), Phase: PhaseDDL})
		if err := r.timed(i, func() error { return r.tgtExec(ddl.DropTable(ref)) }); err != nil {
			return fail(refPtr(ref), PhaseDDL, err)
		}
	}

	// 5. 기존 테이블을 한 문장으로 잠그고, 잠근 뒤에 기존 행 수를 센다.
	if len(loads) > 0 {
		if err := r.tgtExec("LOCK TABLE " + joinRefs(loads) + " IN EXCLUSIVE MODE"); err != nil {
			return fail(nil, PhaseStart, fmt.Errorf("테이블 잠금 실패: %w", err))
		}
		for k, s := range loads {
			i := loadIdx[k]
			var n int64
			err := r.timed(i, func() (err error) { n, err = r.count(r.tgtTx, "타깃", s.Sel.Ref); return })
			if err != nil {
				return fail(refPtr(s.Sel.Ref), PhaseStart, err)
			}
			r.res.Tables[i].Before = n
		}
	}

	// 6. 새 테이블 생성 (FK 제외).
	for i, s := range p.Steps {
		if s.Sel.Mode != plan.ModeCreate {
			continue
		}
		ref := s.Sel.Ref
		r.emit(Event{Table: refPtr(ref), Phase: PhaseDDL})
		if err := r.timed(i, func() error { return r.tgtExecAll(s.DDL) }); err != nil {
			return fail(refPtr(ref), PhaseDDL, err)
		}
	}

	// 7. TRUNCATE 대상을 한 문장으로 비운다.
	if len(truncs) > 0 {
		for _, s := range truncs {
			r.emit(Event{Table: refPtr(s.Sel.Ref), Phase: PhaseTruncate})
		}
		if err := r.tgtExec("TRUNCATE " + joinRefs(truncs)); err != nil {
			return fail(nil, PhaseTruncate, err)
		}
	}

	// 8. 데이터 복사 (부모→자식).
	for i, s := range p.Steps {
		ref := s.Sel.Ref
		r.emit(Event{Table: refPtr(ref), Phase: PhaseCopy})
		var n int64
		err := r.timed(i, func() (err error) { n, err = r.copy(s); return })
		if err != nil {
			return fail(refPtr(ref), PhaseCopy, err)
		}
		r.res.Tables[i].SourceRows = n
	}

	// 9. 새 테이블의 FK.
	for i, s := range p.Steps {
		if s.Sel.Mode != plan.ModeCreate || len(s.FKDDL) == 0 {
			continue
		}
		ref := s.Sel.Ref
		r.emit(Event{Table: refPtr(ref), Phase: PhaseDDL})
		if err := r.timed(i, func() error { return r.tgtExecAll(s.FKDDL) }); err != nil {
			return fail(refPtr(ref), PhaseDDL, err)
		}
	}

	// 10. 검증.
	for i, s := range p.Steps {
		ref := s.Sel.Ref
		tr := &r.res.Tables[i]
		var after int64
		err := r.timed(i, func() (err error) { after, err = r.count(r.tgtTx, "타깃", ref); return })
		if err != nil {
			return fail(refPtr(ref), PhaseVerify, err)
		}
		tr.After = after
		r.emit(Event{Table: refPtr(ref), Phase: PhaseVerify, Rows: after})
		want := tr.SourceRows
		if s.Sel.Mode == plan.ModeAppend {
			want = tr.Before + tr.SourceRows
		}
		r.log.Printf("검증 %s: 소스 %d행, 기존 %d행, 결과 %d행 (기대 %d행)", ref, tr.SourceRows, tr.Before, after, want)
		if after != want {
			return fail(refPtr(ref), PhaseVerify,
				fmt.Errorf("행 수가 맞지 않습니다 (기대 %d행, 실제 %d행)", want, after))
		}
	}

	// 11. 시퀀스. setval은 롤백되지 않으므로 검증이 끝난 뒤 커밋 직전에 한다.
	for i, s := range p.Steps {
		ref := s.Sel.Ref
		r.emit(Event{Table: refPtr(ref), Phase: PhaseSequence})
		if err := r.timed(i, func() error { return r.sequences(s) }); err != nil {
			return fail(refPtr(ref), PhaseSequence, err)
		}
	}

	// 12. 커밋. 여기까지 취소되지 않았다면 이후의 취소는 적용하지 않는다:
	// 커밋 도중에 끊으면 서버는 커밋했는데 결과를 모르는 상태가 될 수 있다.
	if err := r.ctx.Err(); err != nil {
		return fail(nil, PhaseCommit, err)
	}
	r.emit(Event{Phase: PhaseCommit})
	r.log.Printf("[타깃] COMMIT")
	cctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), commitTimeout)
	err = r.tgtTx.Commit(cctx)
	cancel()
	r.tgtTx = nil // 성공 여부와 관계없이 트랜잭션은 끝났다 (다시 ROLLBACK하지 않는다).
	if err != nil {
		if commitOutcomeUnknown(err) {
			r.res.CommitUnknown = true
			return fail(nil, PhaseCommit, fmt.Errorf("커밋 여부 불명확 — 타깃 DB를 직접 확인하세요: %w", err))
		}
		return fail(nil, PhaseCommit, fmt.Errorf("커밋 실패 (롤백됨): %w", err))
	}
	r.res.Committed = true
	r.log.Printf("[소스] ROLLBACK")
	if err := r.srcTx.Rollback(context.Background()); err != nil {
		r.log.Printf("[소스] ROLLBACK 실패 (타깃은 이미 커밋됨): %v", err)
	}
	r.srcTx = nil
	r.log.Printf("완료: %d개 테이블 커밋", len(r.res.Tables))
	r.emit(Event{Phase: PhaseDone})
	return nil
}

func (r *runner) tgtExecAll(sqls []string) error {
	for _, sql := range sqls {
		if err := r.tgtExec(sql); err != nil {
			return err
		}
	}
	return nil
}

// copy streams s from the source snapshot into the target and returns the
// number of rows the source sent. The source goroutine is always joined.
func (r *runner) copy(s plan.Step) (int64, error) {
	ref := s.Sel.Ref
	out := fmt.Sprintf("COPY (%s) TO STDOUT", copySelectSQL(s))
	in := fmt.Sprintf("COPY %s (%s) FROM STDIN", ref.Quoted(), quoteCols(s.Columns))
	r.log.Printf("[소스] %s", out)
	r.log.Printf("[타깃] %s", in)

	pr, pw := io.Pipe()
	type result struct {
		tag pgconn.CommandTag
		err error
	}
	done := make(chan result, 1)
	go func() {
		tag, err := r.src.PgConn().CopyTo(r.ctx, pw, out)
		pw.CloseWithError(err) // nil이면 타깃 쪽은 EOF를 받는다.
		done <- result{tag, err}
	}()
	tgtTag, tgtErr := r.tgt.PgConn().CopyFrom(r.ctx, pr, in)
	// 타깃이 먼저 끝났다면(실패 포함) 소스 고루틴의 Write를 풀어 준다.
	pr.CloseWithError(errTargetCopyFailed)
	srcRes := <-done

	if srcRes.err != nil && !errors.Is(srcRes.err, errTargetCopyFailed) {
		return 0, fmt.Errorf("소스 COPY 실패: %w", srcRes.err)
	}
	if tgtErr != nil {
		return 0, fmt.Errorf("타깃 COPY 실패: %w", tgtErr)
	}
	if srcRes.err != nil {
		// 타깃이 성공했는데 소스가 중단된 경우: 있을 수 없지만 무시하지 않는다.
		return 0, fmt.Errorf("소스 COPY 실패: %w", srcRes.err)
	}
	n := srcRes.tag.RowsAffected()
	r.log.Printf("복사 %s: 소스 %d행, 타깃 %d행", ref, n, tgtTag.RowsAffected())
	if tgtTag.RowsAffected() != n {
		return 0, fmt.Errorf("복사한 행 수가 다릅니다 (소스 %d행, 타깃 %d행)", n, tgtTag.RowsAffected())
	}
	return n, nil
}

type seqValue struct {
	last     int64
	isCalled bool
}

// next is the value nextval would return.
func next(v seqValue, inc int64) int64 {
	if v.isCalled {
		return v.last + inc
	}
	return v.last
}

func (r *runner) readSeq(q catalog.Querier, side string, seq catalog.TableRef) (seqValue, error) {
	sql := "SELECT last_value, is_called FROM " + seq.Quoted()
	r.log.Printf("[%s] %s", side, sql)
	var v seqValue
	if err := q.QueryRow(r.ctx, sql).Scan(&v.last, &v.isCalled); err != nil {
		return v, fmt.Errorf("%s 시퀀스 %s 값 조회 실패: %w", side, seq, err)
	}
	return v, nil
}

func (r *runner) setSeq(seq catalog.TableRef, v seqValue) error {
	sql := ddl.SetSequence(seq, v.last, v.isCalled)
	r.log.Printf("[타깃] %s", sql)
	if _, err := r.tgtTx.Exec(r.ctx, sql); err != nil {
		return fmt.Errorf("시퀀스 %s 설정 실패: %w", seq, err)
	}
	return nil
}

// sequences aligns the target sequences of s. Source values are read inside
// the source transaction; sequence values are not part of the snapshot, so they
// are at least as high as any copied row.
func (r *runner) sequences(s plan.Step) error {
	if s.Source == nil {
		return nil
	}
	if s.Sel.Mode == plan.ModeCreate {
		for _, sq := range s.Source.Sequences {
			v, err := r.readSeq(r.srcTx, "소스", sq.Ref)
			if err != nil {
				return err
			}
			// 새 테이블의 시퀀스는 소스와 같은 이름으로 만들어졌다.
			if err := r.setSeq(sq.Ref, v); err != nil {
				return err
			}
		}
		return nil
	}
	if s.Target == nil {
		return nil
	}
	srcByCol := map[string]catalog.Sequence{}
	for _, sq := range s.Source.Sequences {
		srcByCol[sq.Column] = sq
	}
	for _, tq := range s.Target.Sequences {
		sq, ok := srcByCol[tq.Column]
		if !ok {
			r.log.Printf("시퀀스 %s: 소스 컬럼 %s에 시퀀스가 없어 그대로 둡니다", tq.Ref, tq.Column)
			continue
		}
		sv, err := r.readSeq(r.srcTx, "소스", sq.Ref)
		if err != nil {
			return err
		}
		tv, err := r.readSeq(r.tgtTx, "타깃", tq.Ref)
		if err != nil {
			return err
		}
		// 타깃 시퀀스는 절대 낮추지 않는다.
		v := tv
		if next(sv, sq.Increment) > next(tv, tq.Increment) {
			v = sv
		}
		if err := r.setSeq(tq.Ref, v); err != nil {
			return err
		}
	}
	return nil
}
