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

// connectState holds the two password inputs for the configured source and
// target. Empty passwords fall back to PGPASSWORD/pgpass.
type connectState struct {
	pw      [2]textinput.Model // 0: 소스, 1: 타깃
	focus   int
	busy    bool
	message string
}

func newConnectState() connectState {
	var c connectState
	c.pw[0] = newInput("소스 비밀번호: ")
	c.pw[1] = newInput("타깃 비밀번호: ")
	for i := range c.pw {
		c.pw[i].EchoMode = textinput.EchoPassword
	}
	c.pw[0].Focus()
	return c
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
		c.message = ""
		m.tables = newTablesState(lm.tables)
		m.screen = screenTables
		return nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok || c.busy {
		return nil
	}
	switch k.String() {
	case "tab", "shift+tab", "up", "down":
		return c.setFocus(1 - c.focus)
	case "enter":
		if c.focus == 0 {
			return c.setFocus(1)
		}
		if config.SameDatabase(m.src, m.tgt) {
			return nil // 안내 문구는 화면에 항상 표시된다
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
	b.WriteString("소스: " + connLabel(m.src, m.width-6) + "\n")
	b.WriteString("타깃: " + connLabel(m.tgt, m.width-6) + "\n\n")
	if config.SameDatabase(m.src, m.tgt) {
		b.WriteString(errStyle.Render("소스와 타깃이 같은 데이터베이스입니다. 설정 파일을 고치세요.") + "\n")
	} else {
		b.WriteString("비밀번호를 입력하세요 (비워 두면 PGPASSWORD/pgpass 등 기본 인증을 씁니다)\n\n")
		b.WriteString(c.pw[0].View() + "\n" + c.pw[1].View() + "\n")
	}
	if c.message != "" {
		st := errStyle
		if c.busy {
			st = infoStyle
		}
		b.WriteString("\n" + st.Render(c.message) + "\n")
	}
	b.WriteString("\n" + helpStyle.Render("Tab 전환 · Enter 다음/연결 · Ctrl+C 종료") + "\n")
	return b.String()
}
