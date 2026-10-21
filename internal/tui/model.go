// Package tui is the interactive terminal UI of pgmigrate.
// Every backend call runs inside a tea.Cmd so Update never blocks.
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/cursor"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/app"
	"pgmigrate/internal/config"
	"pgmigrate/internal/plan"
)

type screen int

const (
	screenConnect screen = iota
	screenTables
	screenPreview
	screenConfirm
	screenRun
	screenResult
)

// Model is the root bubbletea model. Each screen keeps its state in its own
// struct and its update/view code in its own file.
type Model struct {
	b   app.Backend
	cfg config.Config

	screen        screen
	width, height int

	src, tgt config.Conn
	plan     *plan.Plan

	connect connectState
	tables  tablesState
	preview previewState
	confirm confirmState
	run     runState
	result  resultState
}

// New returns the root model for the given backend and the configured
// source/target connections.
func New(b app.Backend, cfg config.Config) tea.Model {
	return &Model{b: b, cfg: cfg, src: cfg.Source, tgt: cfg.Target,
		connect: newConnectState(), width: 80, height: 24}
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		if msg.String() == "ctrl+c" && m.screen != screenRun {
			return m, tea.Quit
		}
	}
	switch m.screen {
	case screenConnect:
		return m, m.updateConnect(msg)
	case screenTables:
		return m, m.updateTables(msg)
	case screenPreview:
		return m, m.updatePreview(msg)
	case screenConfirm:
		return m, m.updateConfirm(msg)
	case screenRun:
		return m, m.updateRun(msg)
	case screenResult:
		return m, m.updateResult(msg)
	}
	return m, nil
}

func (m *Model) View() string {
	switch m.screen {
	case screenConnect:
		return m.viewConnect()
	case screenTables:
		return m.viewTables()
	case screenPreview:
		return m.viewPreview()
	case screenConfirm:
		return m.viewConfirm()
	case screenRun:
		return m.viewRun()
	case screenResult:
		return m.viewResult()
	}
	return ""
}

// header shows the chosen source and target.
func (m *Model) header(title string) string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("pgmigrate · "+title) + "\n")
	b.WriteString("소스: " + connLabel(m.src, m.width-6) + "\n")
	b.WriteString("타깃: " + connLabel(m.tgt, m.width-6) + "\n\n")
	return b.String()
}

// newInput returns a text input without a blinking cursor (no timer messages).
func newInput(prompt string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = prompt
	ti.Cursor.SetMode(cursor.CursorStatic)
	return ti
}

// isQuitKey reports whether k quits on screens without a focused text input.
func isQuitKey(k tea.KeyMsg) bool { return k.String() == "q" }

// clampCursor keeps a list cursor inside [0, n).
func clampCursor(c, n int) int {
	if c >= n {
		c = n - 1
	}
	if c < 0 {
		c = 0
	}
	return c
}

// moveCursor handles up/down/k/j for a list of n rows. ok is false for other keys.
func moveCursor(k string, c, n int) (int, bool) {
	switch k {
	case "up", "k":
		return clampCursor(c-1, n), true
	case "down", "j":
		return clampCursor(c+1, n), true
	}
	return c, false
}
