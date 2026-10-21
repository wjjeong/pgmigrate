package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/plan"
)

type confirmState struct {
	prd     bool
	input   textinput.Model // prd일 때만 쓴다
	message string
}

func newConfirmState(p *plan.Plan) confirmState {
	s := confirmState{prd: p.Target.IsPrd()}
	if s.prd {
		s.input = newInput("> ")
	}
	return s
}

func (s *confirmState) focusCmd() tea.Cmd {
	if !s.prd {
		return nil
	}
	return s.input.Focus()
}

func (m *Model) updateConfirm(msg tea.Msg) tea.Cmd {
	s := &m.confirm
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if k.String() == "esc" {
		m.screen = screenPreview
		return nil
	}
	if s.prd {
		if k.String() == "enter" {
			if strings.TrimSpace(s.input.Value()) != m.plan.Target.Database {
				s.message = "데이터베이스 이름이 일치하지 않습니다"
				return nil
			}
			return m.startRun()
		}
		s.message = ""
		var cmd tea.Cmd
		s.input, cmd = s.input.Update(k)
		return cmd
	}
	switch k.String() {
	case "y", "Y":
		return m.startRun()
	case "n", "N":
		m.screen = screenPreview
	case "q":
		return tea.Quit
	}
	return nil
}

func (m *Model) viewConfirm() string {
	s := &m.confirm
	var b strings.Builder
	b.WriteString(m.header("실행 확인"))
	b.WriteString("테이블 " + strconv.Itoa(len(m.plan.Steps)) + "개를 타깃 " + envStyle(m.plan.Target.Env).Render(m.plan.Target.Database) + "에 마이그레이션합니다.\n")
	if m.plan.HasDestructive() {
		b.WriteString(errStyle.Render("타깃 데이터가 삭제됩니다.") + "\n")
	}
	b.WriteString("\n")
	if s.prd {
		b.WriteString(errStyle.Render("운영(prd) 타깃입니다.") + " 계속하려면 타깃 데이터베이스 이름 " +
			cursorStyle.Render(m.plan.Target.Database) + " 을(를) 정확히 입력하고 Enter를 누르세요.\n\n")
		b.WriteString(s.input.View() + "\n")
	} else {
		b.WriteString("실행할까요? (y/n)\n")
	}
	if s.message != "" {
		b.WriteString("\n" + errStyle.Render(s.message) + "\n")
	}
	help := "y 실행 · n/Esc 미리보기로"
	if s.prd {
		help = "Enter 실행 · Esc 미리보기로 · Ctrl+C 종료"
	}
	b.WriteString("\n" + helpStyle.Render(help) + "\n")
	return b.String()
}
