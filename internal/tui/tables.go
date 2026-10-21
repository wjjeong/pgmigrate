package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/app"
	"pgmigrate/internal/catalog"
	"pgmigrate/internal/plan"
)

type tableItem struct {
	info         catalog.TableInfo
	selected     bool
	mode         plan.Mode
	dropRecreate bool
}

type tablesState struct {
	items     []tableItem
	visible   []int // items 인덱스 (필터 적용 결과)
	cursor    int   // visible 안의 위치
	filter    textinput.Model
	filtering bool
	busy      bool
	message   string
}

// planBuiltMsg is the outcome of Backend.Plan.
type planBuiltMsg struct {
	p   *plan.Plan
	err error
}

func planCmd(b app.Backend, sel []plan.Selection) tea.Cmd {
	return func() tea.Msg {
		p, err := b.Plan(context.Background(), sel)
		return planBuiltMsg{p: p, err: err}
	}
}

func newTablesState(ts []catalog.TableInfo) tablesState {
	s := tablesState{filter: newInput("/ ")}
	for _, t := range ts {
		s.items = append(s.items, tableItem{info: t, mode: plan.ModeTruncate})
	}
	s.applyFilter()
	return s
}

func (s *tablesState) applyFilter() {
	q := strings.ToLower(strings.TrimSpace(s.filter.Value()))
	s.visible = s.visible[:0]
	for i, it := range s.items {
		if q == "" || strings.Contains(strings.ToLower(it.info.Ref.String()), q) {
			s.visible = append(s.visible, i)
		}
	}
	s.cursor = clampCursor(s.cursor, len(s.visible))
}

func (s *tablesState) current() *tableItem {
	if len(s.visible) == 0 {
		return nil
	}
	return &s.items[s.visible[s.cursor]]
}

func (s *tablesState) selections() []plan.Selection {
	var sel []plan.Selection
	for _, it := range s.items {
		if it.selected {
			sel = append(sel, plan.Selection{Ref: it.info.Ref, Mode: it.mode, DropRecreate: it.dropRecreate})
		}
	}
	return sel
}

func (m *Model) updateTables(msg tea.Msg) tea.Cmd {
	s := &m.tables
	if pm, ok := msg.(planBuiltMsg); ok {
		s.busy = false
		if pm.err != nil {
			s.message = "계획 생성 실패: " + pm.err.Error()
			return nil
		}
		s.message = ""
		m.plan = pm.p
		m.preview = previewState{}
		m.screen = screenPreview
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok || s.busy {
		return nil
	}
	if s.filtering {
		switch k.String() {
		case "enter", "esc":
			s.filtering = false
			s.filter.Blur()
			return nil
		}
		var cmd tea.Cmd
		s.filter, cmd = s.filter.Update(k)
		s.applyFilter()
		return cmd
	}
	if isQuitKey(k) {
		return tea.Quit
	}
	if cur, moved := moveCursor(k.String(), s.cursor, len(s.visible)); moved {
		s.cursor = cur
		return nil
	}
	s.message = ""
	it := s.current()
	switch k.String() {
	case "/":
		s.filtering = true
		return s.filter.Focus()
	case " ":
		if it != nil {
			it.selected = !it.selected
		}
	case "c", "t", "a":
		if it != nil {
			it.mode = map[string]plan.Mode{"c": plan.ModeCreate, "t": plan.ModeTruncate, "a": plan.ModeAppend}[k.String()]
			if it.mode != plan.ModeCreate {
				it.dropRecreate = false
			}
		}
	case "d":
		switch {
		case it == nil:
		case m.tgt.IsPrd():
			s.message = "타깃이 prd이므로 DropRecreate를 쓸 수 없습니다"
		case it.mode != plan.ModeCreate:
			s.message = "DropRecreate는 create 모드에서만 쓸 수 있습니다"
		default:
			it.dropRecreate = !it.dropRecreate
		}
	case "enter":
		sel := s.selections()
		if len(sel) == 0 {
			s.message = "테이블을 하나 이상 선택하세요"
			return nil
		}
		s.busy, s.message = true, "계획 생성 중…"
		return planCmd(m.b, sel)
	}
	return nil
}

func (m *Model) viewTables() string {
	s := &m.tables
	var b strings.Builder
	b.WriteString(m.header("테이블 선택"))
	if s.filtering || s.filter.Value() != "" {
		b.WriteString(s.filter.View() + "\n")
	}
	n := 0
	for _, it := range s.items {
		if it.selected {
			n++
		}
	}
	b.WriteString(fmt.Sprintf("%d개 중 %d개 선택\n", len(s.items), n))

	rows := m.height - 12
	if rows < 5 {
		rows = 5
	}
	start := 0
	if s.cursor >= rows {
		start = s.cursor - rows + 1
	}
	if len(s.visible) == 0 {
		b.WriteString(helpStyle.Render("  (테이블 없음)") + "\n")
	}
	for vi := start; vi < len(s.visible) && vi < start+rows; vi++ {
		it := s.items[s.visible[vi]]
		mark := "[ ]"
		if it.selected {
			mark = "[x]"
		}
		extra := ""
		if it.dropRecreate {
			extra += " " + errStyle.Render("DropRecreate")
		}
		if it.info.Partitioned {
			extra += " (파티션)"
		}
		// "> [x] " + 이름 + "  truncate  ~1234567" + extra
		name := pad(it.info.Ref.String(), 30)
		line := fmt.Sprintf("%s %s  %-8s  ~%d", mark, name, it.mode, it.info.EstRows)
		prefix := "  "
		if vi == s.cursor {
			prefix = cursorStyle.Render("> ")
		}
		b.WriteString(prefix + line + extra + "\n")
	}
	if s.message != "" {
		st := errStyle
		if s.busy {
			st = infoStyle
		}
		b.WriteString("\n" + st.Render(s.message) + "\n")
	}
	help := "space 선택 · c/t/a 모드 · d DropRecreate · / 필터 · Enter 미리보기 · q 종료"
	if s.filtering {
		help = "필터 입력 · Enter/Esc 완료"
	}
	b.WriteString("\n" + helpStyle.Render(truncate(help, m.width)) + "\n")
	return b.String()
}
