package tui

import (
	"context"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/app"
	"pgmigrate/internal/catalog"
	"pgmigrate/internal/config"
)

type connectStage int

const (
	stageSource connectStage = iota
	stageTarget
	stagePassword
)

type connectState struct {
	stage   connectStage
	cursor  int
	srcIdx  int
	pw      [2]textinput.Model // 0: 소스, 1: 타깃
	focus   int
	busy    bool
	message string
}

// tablesLoadedMsg is the outcome of Connect followed by ListTables.
type tablesLoadedMsg struct {
	tables []catalog.TableInfo
	err    error
}

func connectCmd(b app.Backend, src, tgt config.Conn, srcPw, tgtPw string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		if err := b.Connect(ctx, src, tgt, srcPw, tgtPw); err != nil {
			return tablesLoadedMsg{err: err}
		}
		ts, err := b.ListTables(ctx)
		return tablesLoadedMsg{tables: ts, err: err}
	}
}

func (m *Model) updateConnect(msg tea.Msg) tea.Cmd {
	c := &m.connect
	if lm, ok := msg.(tablesLoadedMsg); ok {
		c.busy = false
		if lm.err != nil {
			c.message = "연결 실패: " + lm.err.Error()
			return nil
		}
		m.tables = newTablesState(lm.tables)
		m.screen = screenTables
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok || c.busy {
		return nil
	}
	if c.stage == stagePassword {
		return m.updatePasswords(k)
	}
	if isQuitKey(k) {
		return tea.Quit
	}
	if cur, moved := moveCursor(k.String(), c.cursor, len(m.conns)); moved {
		c.cursor = cur
		return nil
	}
	switch k.String() {
	case "esc":
		if c.stage == stageTarget {
			c.stage, c.cursor, c.message = stageSource, c.srcIdx, ""
		}
	case "enter":
		if len(m.conns) == 0 {
			return nil
		}
		if c.stage == stageSource {
			c.srcIdx = c.cursor
			m.src = m.conns[c.cursor]
			c.stage, c.cursor, c.message = stageTarget, 0, ""
			return nil
		}
		tgt := m.conns[c.cursor]
		if config.SameDatabase(m.src, tgt) {
			c.message = "소스와 같은 데이터베이스는 타깃으로 고를 수 없습니다"
			return nil
		}
		m.tgt = tgt
		c.stage, c.message = stagePassword, ""
		c.pw[0] = newInput("소스 비밀번호: ")
		c.pw[1] = newInput("타깃 비밀번호: ")
		for i := range c.pw {
			c.pw[i].EchoMode = textinput.EchoPassword
		}
		c.focus = 0
		return c.pw[0].Focus()
	}
	return nil
}

func (m *Model) updatePasswords(k tea.KeyMsg) tea.Cmd {
	c := &m.connect
	switch k.String() {
	case "esc":
		c.stage, c.cursor, c.message = stageTarget, 0, ""
		return nil
	case "tab", "shift+tab", "up", "down":
		return c.setFocus(1 - c.focus)
	case "enter":
		if c.focus == 0 {
			return c.setFocus(1)
		}
		c.busy, c.message = true, "연결 중…"
		return connectCmd(m.b, m.src, m.tgt, c.pw[0].Value(), c.pw[1].Value())
	}
	var cmd tea.Cmd
	c.pw[c.focus], cmd = c.pw[c.focus].Update(k)
	return cmd
}

func (c *connectState) setFocus(i int) tea.Cmd {
	c.pw[c.focus].Blur()
	c.focus = i
	return c.pw[i].Focus()
}

func (m *Model) viewConnect() string {
	c := &m.connect
	var b strings.Builder
	b.WriteString(titleStyle.Render("pgmigrate · 연결") + "\n\n")
	switch c.stage {
	case stageSource, stageTarget:
		if c.stage == stageSource {
			b.WriteString("소스 연결을 고르세요\n\n")
		} else {
			b.WriteString("소스: " + connLabel(m.src, m.width-6) + "\n")
			b.WriteString("타깃 연결을 고르세요\n\n")
		}
		if len(m.conns) == 0 {
			b.WriteString(errStyle.Render("설정 파일에 연결이 없습니다") + "\n")
		}
		for i, cn := range m.conns {
			line := "  " + connLabel(cn, m.width-2)
			if i == c.cursor {
				line = cursorStyle.Render("> ") + connLabel(cn, m.width-2)
			}
			b.WriteString(line + "\n")
		}
	case stagePassword:
		b.WriteString("소스: " + connLabel(m.src, m.width-6) + "\n")
		b.WriteString("타깃: " + connLabel(m.tgt, m.width-6) + "\n\n")
		b.WriteString("비밀번호를 입력하세요 (비워 두면 pgpass 등 기본 인증을 씁니다)\n\n")
		b.WriteString(c.pw[0].View() + "\n" + c.pw[1].View() + "\n")
	}
	if c.message != "" {
		st := errStyle
		if c.busy {
			st = infoStyle
		}
		b.WriteString("\n" + st.Render(c.message) + "\n")
	}
	help := "↑/↓ 이동 · Enter 선택 · Esc 뒤로 · q 종료"
	if c.stage == stagePassword {
		help = "Tab 전환 · Enter 다음/연결 · Esc 뒤로 · Ctrl+C 종료"
	}
	b.WriteString("\n" + helpStyle.Render(help) + "\n")
	return b.String()
}
