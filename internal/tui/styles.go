package tui

import (
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"pgmigrate/internal/config"
	"pgmigrate/internal/plan"
)

var (
	colorRed    = lipgloss.Color("9")
	colorYellow = lipgloss.Color("11")
	colorGreen  = lipgloss.Color("10")
	colorGray   = lipgloss.Color("8")
	colorCyan   = lipgloss.Color("14")

	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(colorCyan)
	cursorStyle = lipgloss.NewStyle().Bold(true)
	helpStyle   = lipgloss.NewStyle().Foreground(colorGray)
	errStyle    = lipgloss.NewStyle().Foreground(colorRed)
	warnStyle   = lipgloss.NewStyle().Foreground(colorYellow)
	okStyle     = lipgloss.NewStyle().Foreground(colorGreen)
	infoStyle   = lipgloss.NewStyle().Foreground(colorCyan)
	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("15")).Background(colorRed).Padding(0, 1)
)

// envStyle colors an environment label: prd red, qa yellow, anything else green.
func envStyle(env string) lipgloss.Style {
	if (config.Conn{Env: env}).IsPrd() {
		return lipgloss.NewStyle().Bold(true).Foreground(colorRed)
	}
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "qa":
		return lipgloss.NewStyle().Foreground(colorYellow)
	}
	return lipgloss.NewStyle().Foreground(colorGreen)
}

// connLabel renders "name [env] user@host:port/db" with the env colored.
func connLabel(c config.Conn, width int) string {
	env := c.Env
	if env == "" {
		env = "-"
	}
	rest := truncate(c.Name, 20) + " " + envStyle(c.Env).Render("["+env+"]")
	detail := " " + c.User + "@" + c.Host + ":" + strconv.Itoa(c.Port) + "/" + c.Database
	return rest + truncate(detail, width-lipgloss.Width(rest))
}

func sevStyle(s plan.Severity) (lipgloss.Style, string) {
	switch s {
	case plan.SevError:
		return errStyle, "오류"
	case plan.SevWarning:
		return warnStyle, "경고"
	}
	return infoStyle, "정보"
}

// truncate cuts s to at most w display cells, marking the cut with "…".
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

// pad truncates s to w display cells and right-pads it with spaces to exactly w
// (fmt's %-Ns counts runes, which misaligns wide Korean characters).
func pad(s string, w int) string {
	s = truncate(s, w)
	if n := ansi.StringWidth(s); n < w {
		s += strings.Repeat(" ", w-n)
	}
	return s
}

// padLeft right-aligns s in w display cells.
func padLeft(s string, w int) string {
	if n := ansi.StringWidth(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}
