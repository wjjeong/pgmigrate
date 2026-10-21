package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
	"pgmigrate/internal/migrate"
	"pgmigrate/internal/plan"
)

// harness drives the model like the bubbletea runtime does, but synchronously:
// every returned Cmd runs in a goroutine and its message is fed back into Update.
// settle stops waiting when no message arrives for a short while, so a Cmd that
// blocks (e.g. waiting for the next run event) simply stays pending.
type harness struct {
	t           *testing.T
	m           tea.Model
	msgs        chan tea.Msg
	outstanding int
	quit        bool
}

func newHarness(t *testing.T, f *fakeBackend, cfg config.Config) *harness {
	t.Helper()
	h := &harness{t: t, m: New(f, cfg), msgs: make(chan tea.Msg, 64)}
	h.spawn(h.m.Init())
	h.settle()
	return h
}

func (h *harness) spawn(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	h.outstanding++
	go func() { h.msgs <- cmd() }()
}

func (h *harness) handle(msg tea.Msg) {
	switch msg := msg.(type) {
	case nil:
	case tea.QuitMsg:
		h.quit = true
	case tea.BatchMsg:
		for _, c := range msg {
			h.spawn(c)
		}
	default:
		h.update(msg)
	}
}

// update feeds one message without waiting for the resulting Cmds.
func (h *harness) update(msg tea.Msg) {
	m, cmd := h.m.Update(msg)
	h.m = m
	h.spawn(cmd)
}

func (h *harness) settle() {
	for h.outstanding > 0 {
		select {
		case msg := <-h.msgs:
			h.outstanding--
			h.handle(msg)
		case <-time.After(150 * time.Millisecond):
			return
		}
	}
}

func (h *harness) send(msg tea.Msg) {
	h.update(msg)
	h.settle()
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		h.send(key(k))
	}
}

func (h *harness) typeText(s string) { h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}) }

func (h *harness) view() string { return h.m.View() }

func (h *harness) mustContain(sub string) {
	h.t.Helper()
	if v := h.view(); !strings.Contains(v, sub) {
		h.t.Fatalf("view does not contain %q:\n%s", sub, v)
	}
}

func key(k string) tea.KeyMsg {
	switch k {
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
}

var (
	connDev     = config.Conn{Name: "dev", Host: "h1", Port: 5432, Database: "app", User: "u"}
	connDevCopy = config.Conn{Name: "dev-copy", Host: "h1", Port: 5432, Database: "app", User: "other"}
	connQA      = config.Conn{Name: "qa", Host: "h2", Port: 5432, Database: "app_qa", User: "qu"}
	connQADrop  = config.Conn{Name: "qa", Host: "h2", Port: 5432, Database: "app_qa", User: "qu", AllowDrop: true}
	cfgQA       = config.Config{Source: connDev, Target: connQA}
	cfgQADrop   = config.Config{Source: connDev, Target: connQADrop}
)

func ref(name string) catalog.TableRef { return catalog.TableRef{Schema: "public", Name: name} }

func tableInfos(names ...string) []catalog.TableInfo {
	var out []catalog.TableInfo
	for _, n := range names {
		out = append(out, catalog.TableInfo{Ref: ref(n), EstRows: 10})
	}
	return out
}

func simplePlan(tgt config.Conn, issues ...plan.Issue) *plan.Plan {
	return &plan.Plan{
		Source: connDev,
		Target: tgt,
		Steps: []plan.Step{{
			Sel:        plan.Selection{Ref: ref("a"), Mode: plan.ModeAppend},
			SourceRows: 3,
		}},
		Issues: issues,
	}
}

// toTables enters two empty passwords and waits for the table list.
func toTables(h *harness) {
	h.t.Helper()
	h.press("enter", "enter")
	h.mustContain("public.a")
}

// toConfirm goes through tables and preview selecting public.a.
func toConfirm(h *harness) {
	h.t.Helper()
	toTables(h)
	h.press(" ", "enter") // select a, build plan
	h.mustContain("미리보기")
	h.press("enter")
}

// confirmRun types the target database name on the confirm screen and runs.
func confirmRun(h *harness) {
	h.t.Helper()
	h.typeText(connQA.Database)
	h.press("enter")
}

func TestConnect_SummariesAndPasswords(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a")}
	h := newHarness(t, f, cfgQADrop)
	for _, s := range []string{"dev", "u@h1:5432/app", "qa", "qu@h2:5432/app_qa", "allow_drop", "소스 비밀번호", "타깃 비밀번호"} {
		h.mustContain(s)
	}
	h.typeText("spw")
	h.press("enter")
	h.typeText("tpw")
	if strings.Contains(h.view(), "spw") || strings.Contains(h.view(), "tpw") {
		t.Fatalf("password echoed:\n%s", h.view())
	}
	h.press("enter")
	if f.connectCalls != 1 {
		t.Fatalf("connect calls = %d, want 1", f.connectCalls)
	}
	if f.connSrc != cfgQADrop.Source || f.connTgt != cfgQADrop.Target || f.connPw != [2]string{"spw", "tpw"} {
		t.Fatalf("connect args = %+v %+v %q", f.connSrc, f.connTgt, f.connPw)
	}
	h.mustContain("public.a")
}

func TestConnect_NoAllowDropMarkWithoutAllowDrop(t *testing.T) {
	h := newHarness(t, &fakeBackend{}, cfgQA)
	if strings.Contains(h.view(), "allow_drop") {
		t.Fatalf("allow_drop shown for target without it:\n%s", h.view())
	}
}

func TestConnect_SameDatabaseBlocked(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a")}
	h := newHarness(t, f, config.Config{Source: connDev, Target: connDevCopy})
	h.mustContain("같은 데이터베이스")
	h.press("enter", "enter")
	if f.connectCalls != 0 {
		t.Fatalf("connect calls = %d, want 0", f.connectCalls)
	}
	h.mustContain("같은 데이터베이스")
}

func TestQuitWhenNotRunning(t *testing.T) {
	h := newHarness(t, &fakeBackend{}, cfgQA)
	h.press("ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c did not quit on connect screen")
	}
}

func TestTables_DropRecreateBlockedWithoutAllowDrop(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA)}
	h := newHarness(t, f, cfgQA)
	toTables(h)
	h.press(" ", "c", "d")
	h.mustContain("allow_drop: true")
	h.press("enter")
	want := []plan.Selection{{Ref: ref("a"), Mode: plan.ModeCreate}}
	if len(f.planCalls) != 1 || !reflect.DeepEqual(f.planCalls[0], want) {
		t.Fatalf("plan calls = %+v, want [%+v]", f.planCalls, want)
	}
}

func TestTables_ModeKeys(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a", "b", "c", "d"), plan: simplePlan(connQADrop)}
	h := newHarness(t, f, cfgQADrop)
	toTables(h)
	h.press(" ", "c", "t", "d") // a: truncate (default via t); d refused on truncate
	h.mustContain("create 모드에서만")
	h.press("down", " ", "c", "d") // b: create + DropRecreate
	h.press("down", " ", "a")      // c: append
	h.press("down", "c", "/")      // d: mode set but not selected; open filter
	h.typeText(".b")               // filter to public.b only
	h.press("enter")               // leave filter
	h.mustContain("public.b")
	if strings.Contains(h.view(), "public.c") {
		t.Fatalf("filter did not hide public.c:\n%s", h.view())
	}
	h.press("enter")
	want := []plan.Selection{
		{Ref: ref("a"), Mode: plan.ModeTruncate},
		{Ref: ref("b"), Mode: plan.ModeCreate, DropRecreate: true},
		{Ref: ref("c"), Mode: plan.ModeAppend},
	}
	if len(f.planCalls) != 1 || !reflect.DeepEqual(f.planCalls[0], want) {
		t.Fatalf("plan calls = %+v, want [%+v]", f.planCalls, want)
	}
	h.mustContain("미리보기")
}

func TestTables_DefaultModeTruncate(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA)}
	h := newHarness(t, f, cfgQA)
	toTables(h)
	h.press(" ", "enter")
	want := []plan.Selection{{Ref: ref("a"), Mode: plan.ModeTruncate}}
	if len(f.planCalls) != 1 || !reflect.DeepEqual(f.planCalls[0], want) {
		t.Fatalf("plan calls = %+v, want [%+v]", f.planCalls, want)
	}
}

func TestPreview_ErrorsBlockExecute(t *testing.T) {
	r := ref("a")
	p := simplePlan(connQA, plan.Issue{Table: &r, Severity: plan.SevError, Message: "타깃에 테이블이 없습니다"})
	f := &fakeBackend{tables: tableInfos("a"), plan: p}
	h := newHarness(t, f, cfgQA)
	toConfirm(h) // the final Enter must be refused
	h.mustContain("오류를 해결해야 실행할 수 있습니다")
	h.mustContain("타깃에 테이블이 없습니다")
	h.press("enter")
	if n := f.execCount(); n != 0 {
		t.Fatalf("execute calls = %d, want 0", n)
	}
	h.press("esc")
	h.mustContain("[x] public.a")
}

func TestPreview_DestructiveBannerAndDDL(t *testing.T) {
	p := simplePlan(connQA)
	p.Steps[0].Sel.Mode = plan.ModeTruncate
	p.Steps[0].DDL = []string{"CREATE TABLE x ()"}
	f := &fakeBackend{tables: tableInfos("a"), plan: p}
	h := newHarness(t, f, cfgQA)
	toTables(h)
	h.press(" ", "enter")
	h.mustContain("삭제")
	if strings.Contains(h.view(), "CREATE TABLE x") {
		t.Fatal("DDL shown before v")
	}
	h.press("v")
	h.mustContain("CREATE TABLE x")
}

func TestConfirm_AlwaysRequiresExactDBName(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA),
		result: &migrate.Result{Committed: true}, logPath: "run.log"}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	h.mustContain("app_qa")
	h.press("y", "enter") // y/n 확인은 없다
	if n := f.execCount(); n != 0 {
		t.Fatalf("execute calls = %d after y, want 0", n)
	}
	h.mustContain("일치하지 않습니다")
	h.press("esc")
	h.mustContain("미리보기")
	h.press("enter")
	h.typeText("APP_QA")
	h.press("enter")
	if n := f.execCount(); n != 0 {
		t.Fatalf("execute calls = %d after wrong name, want 0", n)
	}
	h.mustContain("일치하지 않습니다")
	h.press("ctrl+u") // textinput: delete before cursor
	h.typeText(" app_qa ")
	h.press("enter")
	if n := f.execCount(); n != 1 {
		t.Fatalf("execute calls = %d after exact name, want 1", n)
	}
	h.mustContain("커밋 완료")
}

func TestRunScreen_CtrlCCancels(t *testing.T) {
	r := ref("a")
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA), blockExec: true,
		logPath: "run.log",
		events:  []migrate.Event{{Phase: migrate.PhaseStart}, {Table: &r, Phase: migrate.PhaseCopy}}}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	confirmRun(h)
	h.mustContain("진행")
	h.update(key("ctrl+c"))
	if h.quit {
		t.Fatal("ctrl+c quit during run")
	}
	h.mustContain("취소 중")
	h.settle()
	if f.execCtx == nil || f.execCtx.Err() == nil {
		t.Fatal("execute context was not cancelled")
	}
	h.mustContain("롤백됨")
	h.mustContain("context canceled")
	h.press("q")
	if !h.quit {
		t.Fatal("q did not quit on result screen")
	}
}

func TestResult_ShowsLogPath(t *testing.T) {
	res := &migrate.Result{Committed: true, Tables: []migrate.TableResult{{
		Ref: ref("a"), Mode: plan.ModeAppend, SourceRows: 3, Before: 1, After: 4, Duration: 1500 * time.Millisecond,
	}}}
	path := `C:\pgmigrate\logs\20261021-120000.log`
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA), result: res, logPath: path}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	confirmRun(h)
	h.mustContain(path)
	h.mustContain("public.a")
}

func TestResult_ShowsError(t *testing.T) {
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA),
		result: &migrate.Result{}, execErr: errors.New("복사 실패"), logPath: "run.log"}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	confirmRun(h)
	h.mustContain("복사 실패")
	h.mustContain("롤백됨")
}

func TestResult_OutcomeWording(t *testing.T) {
	for _, tc := range []struct {
		name      string
		res       *migrate.Result
		err       error
		want, not string
	}{
		{"refused", &migrate.Result{Refused: true}, errors.New("계획에 오류"), "실행 전 거부 (변경 없음)", "롤백"},
		{"unknown", &migrate.Result{CommitUnknown: true}, errors.New("unexpected EOF"), "커밋 여부 불명확", "롤백"},
		{"rolled back", &migrate.Result{}, errors.New("복사 실패"), "롤백됨", "커밋 완료"},
		{"not run", nil, errors.New("연결 실패"), "실행되지 않음 (변경 없음)", "롤백"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA),
				result: tc.res, execErr: tc.err, logPath: "run.log"}
			h := newHarness(t, f, cfgQA)
			toConfirm(h)
			confirmRun(h)
			h.mustContain(tc.want)
			if strings.Contains(h.view(), tc.not) {
				t.Fatalf("view contains %q:\n%s", tc.not, h.view())
			}
		})
	}
}

func TestRunScreen_CtrlCDuringCommitNotCancelled(t *testing.T) {
	r := ref("a")
	f := &fakeBackend{tables: tableInfos("a"), plan: simplePlan(connQA), blockExec: true,
		logPath: "run.log",
		events: []migrate.Event{{Phase: migrate.PhaseStart}, {Table: &r, Phase: migrate.PhaseCopy},
			{Table: &r, Phase: migrate.PhaseVerify}, {Phase: migrate.PhaseCommit}}}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	confirmRun(h)
	h.mustContain("커밋")
	h.update(key("ctrl+c"))
	h.settle()
	if h.quit {
		t.Fatal("ctrl+c quit during run")
	}
	h.mustContain("커밋 중에는 취소할 수 없습니다")
	if strings.Contains(h.view(), "취소 중") {
		t.Fatalf("cancelling shown during commit:\n%s", h.view())
	}
	f.mu.Lock()
	ctx := f.execCtx
	f.mu.Unlock()
	if ctx == nil || ctx.Err() != nil {
		t.Fatal("execute context must not be cancelled once commit started")
	}
	h.m.(*Model).run.cancel() // fake Execute를 끝낸다
	h.settle()
}

func TestConfirm_ListsDestructiveTables(t *testing.T) {
	p := simplePlan(connQA)
	p.Steps = []plan.Step{
		{Sel: plan.Selection{Ref: ref("a"), Mode: plan.ModeTruncate}},
		{Sel: plan.Selection{Ref: ref("b"), Mode: plan.ModeCreate, DropRecreate: true}, Target: &catalog.Table{Ref: ref("b")}},
		{Sel: plan.Selection{Ref: ref("c"), Mode: plan.ModeAppend}},
		{Sel: plan.Selection{Ref: ref("d"), Mode: plan.ModeTruncate}},
	}
	f := &fakeBackend{tables: tableInfos("a"), plan: p}
	h := newHarness(t, f, cfgQA)
	toConfirm(h)
	h.mustContain("TRUNCATE: public.a, public.d")
	h.mustContain("DROP 후 재생성: public.b")
	if strings.Contains(h.view(), "public.c") {
		t.Fatalf("append table listed as destructive:\n%s", h.view())
	}
}
