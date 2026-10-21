package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/plan"
)

// confirmState always asks for the exact target database name.
type confirmState struct {
	input   textinput.Model
	message string
}

func newConfirmState() confirmState {
	return confirmState{input: newInput("> ")}
}

func (s *confirmState) focusCmd() tea.Cmd { return s.input.Focus() }

func (m *Model) updateConfirm(msg tea.Msg) tea.Cmd {
	s := &m.confirm
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "esc":
		m.screen = screenPreview
		return nil
	case "enter":
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

func (m *Model) viewConfirm() string {
	s := &m.confirm
	var b strings.Builder
	b.WriteString(m.header("실행 확인"))
	b.WriteString("테이블 " + strconv.Itoa(len(m.plan.Steps)) + "개를 타깃 " + errStyle.Bold(true).Render(m.plan.Target.Database) + "에 마이그레이션합니다.\n")
	if m.plan.HasDestructive() {
		b.WriteString(errStyle.Render("타깃 데이터가 삭제됩니다.") + "\n")
		var truncs, drops []string
		for _, st := range m.plan.Steps {
			switch {
			case st.Sel.Mode == plan.ModeTruncate:
				truncs = append(truncs, st.Sel.Ref.String())
			case st.Sel.Mode == plan.ModeCreate && st.Sel.DropRecreate && st.Target != nil:
				drops = append(drops, st.Sel.Ref.String())
			}
		}
		if len(truncs) > 0 {
			b.WriteString(errStyle.Render("  TRUNCATE: "+strings.Join(truncs, ", ")) + "\n")
		}
		if len(drops) > 0 {
			b.WriteString(errStyle.Render("  DROP 후 재생성: "+strings.Join(drops, ", ")) + "\n")
		}
	}
	b.WriteString("\n")
	b.WriteString("계속하려면 타깃 데이터베이스 이름 " + cursorStyle.Render(m.plan.Target.Database) +
		" 을(를) 정확히 입력하고 Enter를 누르세요.\n\n")
	b.WriteString(s.input.View() + "\n")
	if s.message != "" {
		b.WriteString("\n" + errStyle.Render(s.message) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("Enter 실행 · Esc 미리보기로 · Ctrl+C 종료") + "\n")
	return b.String()
}
