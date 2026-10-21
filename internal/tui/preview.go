package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/plan"
)

const msgErrorsBlock = "오류를 해결해야 실행할 수 있습니다"

type previewState struct {
	showDDL bool
	offset  int // 스크롤 위치 (줄)
}

func (m *Model) updatePreview(msg tea.Msg) tea.Cmd {
	s := &m.preview
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	switch k.String() {
	case "q":
		return tea.Quit
	case "esc":
		m.screen = screenTables
	case "v":
		s.showDDL = !s.showDDL
		s.offset = 0
	case "up", "k":
		if s.offset > 0 {
			s.offset--
		}
	case "down", "j":
		s.offset++
	case "enter":
		if m.plan.HasErrors() {
			return nil // 화면 하단에 msgErrorsBlock이 항상 보인다
		}
		m.confirm = newConfirmState(m.plan)
		m.screen = screenConfirm
		return m.confirm.focusCmd()
	}
	return nil
}

func (m *Model) previewLines() []string {
	p := m.plan
	var lines []string
	if p.HasDestructive() {
		lines = append(lines, bannerStyle.Render("주의: 타깃 데이터를 삭제(TRUNCATE/DROP)하는 작업이 포함되어 있습니다"), "")
	}
	lines = append(lines, "실행 순서:")
	for i, st := range p.Steps {
		mode := string(st.Sel.Mode)
		if st.Sel.Mode == plan.ModeCreate && st.Sel.DropRecreate {
			mode += "+DropRecreate"
		}
		name := pad(st.Sel.Ref.String(), 28)
		lines = append(lines, fmt.Sprintf("%2d. %s  %-21s 소스 %d행 · 타깃 %d행",
			i+1, name, mode, st.SourceRows, st.TargetRows))
		if s := m.preview; s.showDDL {
			for _, d := range append(append([]string(nil), st.DDL...), st.FKDDL...) {
				for _, l := range strings.Split(d, "\n") {
					lines = append(lines, helpStyle.Render("      "+truncate(l, m.width-6)))
				}
			}
		}
	}
	if len(p.Issues) > 0 {
		lines = append(lines, "", "검사 결과:")
	}
	for _, is := range p.Issues {
		st, label := sevStyle(is.Severity)
		text := "[" + label + "] "
		if is.Table != nil {
			text += is.Table.String() + ": "
		}
		lines = append(lines, st.Render(truncate(text+is.Message, m.width)))
	}
	return lines
}

func (m *Model) viewPreview() string {
	s := &m.preview
	var b strings.Builder
	b.WriteString(m.header("미리보기"))
	lines := m.previewLines()
	rows := m.height - 9
	if rows < 5 {
		rows = 5
	}
	if maxOff := len(lines) - rows; s.offset > maxOff {
		s.offset = maxOff
	}
	if s.offset < 0 {
		s.offset = 0
	}
	end := s.offset + rows
	if end > len(lines) {
		end = len(lines)
	}
	b.WriteString(strings.Join(lines[s.offset:end], "\n") + "\n")
	if m.plan.HasErrors() {
		b.WriteString("\n" + errStyle.Render(msgErrorsBlock) + "\n")
	}
	help := "Enter 실행 확인 · v DDL 펼치기 · ↑/↓ 스크롤 · Esc 테이블 선택 · q 종료"
	b.WriteString("\n" + helpStyle.Render(truncate(help, m.width)) + "\n")
	return b.String()
}
