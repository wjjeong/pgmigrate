package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/migrate"
)

type tableStatus int

const (
	statusWaiting tableStatus = iota
	statusRunning
	statusDone
	statusFailed
)

var statusLabel = map[tableStatus]string{
	statusWaiting: "대기", statusRunning: "진행", statusDone: "완료", statusFailed: "실패",
}

var phaseLabel = map[migrate.Phase]string{
	migrate.PhaseStart:    "시작",
	migrate.PhaseDDL:      "DDL",
	migrate.PhaseTruncate: "TRUNCATE",
	migrate.PhaseCopy:     "복사",
	migrate.PhaseVerify:   "검증",
	migrate.PhaseSequence: "시퀀스",
	migrate.PhaseCommit:   "커밋",
	migrate.PhaseDone:     "완료",
	migrate.PhaseFailed:   "실패",
}

type runState struct {
	cancel     context.CancelFunc
	ch         chan tea.Msg
	status     []tableStatus   // plan.Steps 순서
	phase      []migrate.Phase // 테이블별 마지막 단계
	global     migrate.Phase
	cancelling bool
}

// runEventMsg carries one migrate.Event from the Execute goroutine.
type runEventMsg struct{ e migrate.Event }

// runDoneMsg is the final outcome of Execute.
type runDoneMsg struct {
	res     *migrate.Result
	logPath string
	err     error
}

// waitRun reads the next message of the running Execute; Update re-arms it
// after every event until runDoneMsg arrives.
func waitRun(ch chan tea.Msg) tea.Cmd { return func() tea.Msg { return <-ch } }

// startRun switches to the run screen and returns the Cmd that starts Execute.
func (m *Model) startRun() tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan tea.Msg, 64)
	n := len(m.plan.Steps)
	m.run = runState{cancel: cancel, ch: ch, status: make([]tableStatus, n), phase: make([]migrate.Phase, n)}
	m.screen = screenRun
	b, p := m.b, m.plan
	return func() tea.Msg {
		go func() {
			res, path, err := b.Execute(ctx, p, func(e migrate.Event) { ch <- runEventMsg{e} })
			ch <- runDoneMsg{res: res, logPath: path, err: err}
		}()
		return <-ch
	}
}

func (m *Model) updateRun(msg tea.Msg) tea.Cmd {
	s := &m.run
	switch msg := msg.(type) {
	case runEventMsg:
		m.applyEvent(msg.e)
		return waitRun(s.ch)
	case runDoneMsg:
		s.cancel() // 컨텍스트 자원 해제
		m.result = resultState{res: msg.res, logPath: msg.logPath, err: msg.err}
		m.screen = screenResult
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" && !s.cancelling {
			s.cancelling = true
			s.cancel() // 결과(runDoneMsg)가 올 때까지 기다린다
		}
	}
	return nil
}

func (m *Model) applyEvent(e migrate.Event) {
	s := &m.run
	if e.Table == nil {
		s.global = e.Phase
		for i, st := range s.status {
			switch {
			case e.Phase == migrate.PhaseFailed && st == statusRunning:
				s.status[i] = statusFailed
			case e.Phase == migrate.PhaseDone && st != statusFailed:
				s.status[i] = statusDone
			}
		}
		return
	}
	for i, st := range m.plan.Steps {
		if st.Sel.Ref != *e.Table {
			continue
		}
		s.phase[i] = e.Phase
		switch {
		case e.Phase == migrate.PhaseFailed:
			s.status[i] = statusFailed
		case e.Phase == migrate.PhaseVerify:
			s.status[i] = statusDone
		case s.status[i] != statusDone:
			s.status[i] = statusRunning
		}
	}
}

func (m *Model) viewRun() string {
	s := &m.run
	var b strings.Builder
	b.WriteString(m.header("실행"))
	if s.global != "" {
		b.WriteString("단계: " + phaseLabel[s.global] + "\n\n")
	}
	for i, st := range m.plan.Steps {
		label := statusLabel[s.status[i]]
		switch s.status[i] {
		case statusRunning:
			label = infoStyle.Render(label)
		case statusDone:
			label = okStyle.Render(label)
		case statusFailed:
			label = errStyle.Render(label)
		default:
			label = helpStyle.Render(label)
		}
		detail := ""
		if s.status[i] == statusRunning && s.phase[i] != "" {
			detail = " (" + phaseLabel[s.phase[i]] + ")"
		}
		b.WriteString(fmt.Sprintf("  %s  %s %-8s%s\n", label, pad(st.Sel.Ref.String(), 30), st.Sel.Mode, detail))
	}
	b.WriteString("\n")
	if s.cancelling {
		b.WriteString(warnStyle.Render("취소 중… 롤백이 끝날 때까지 기다립니다") + "\n")
	} else {
		b.WriteString(helpStyle.Render("Ctrl+C 취소 (전체 롤백)") + "\n")
	}
	return b.String()
}
