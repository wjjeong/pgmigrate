package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pgmigrate/internal/migrate"
)

type resultState struct {
	res     *migrate.Result
	logPath string
	err     error
}

func (m *Model) updateResult(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyMsg); ok && isQuitKey(k) {
		return tea.Quit
	}
	return nil
}

func (m *Model) viewResult() string {
	s := &m.result
	var b strings.Builder
	b.WriteString(m.header("결과"))
	if s.res != nil && s.res.Committed {
		b.WriteString(okStyle.Render("커밋 완료") + "\n\n")
	} else {
		b.WriteString(errStyle.Render("커밋되지 않음 (롤백)") + "\n\n")
	}
	if s.res != nil && len(s.res.Tables) > 0 {
		b.WriteString("  " + pad("테이블", 28) + " " + pad("모드", 8) + " " + strings.Join([]string{padLeft("소스", 9), padLeft("이전", 9), padLeft("이후", 9), padLeft("시간", 8)}, " ") + "\n")
		for _, t := range s.res.Tables {
			b.WriteString(fmt.Sprintf("  %s %-8s %9d %9d %9d %8s\n",
				pad(t.Ref.String(), 28), t.Mode, t.SourceRows, t.Before, t.After, t.Duration.Round(10*time.Millisecond)))
		}
		b.WriteString("\n")
	}
	if s.err != nil {
		b.WriteString(errStyle.Render("오류: "+s.err.Error()) + "\n\n")
	}
	if s.logPath != "" {
		b.WriteString("로그: " + s.logPath + "\n")
	} else {
		b.WriteString("로그: (없음)\n")
	}
	b.WriteString("\n" + helpStyle.Render("q 종료") + "\n")
	return b.String()
}
