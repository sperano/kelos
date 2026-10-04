package logview

import (
	"io"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// ANSI palette indexes, matching the session terminal UI.
const (
	colorRed    = "1"
	colorGreen  = "2"
	colorYellow = "3"
	colorCyan   = "6"
)

type styles struct {
	plain    lipgloss.Style
	muted    lipgloss.Style
	thinking lipgloss.Style
	bullet   lipgloss.Style
	tool     lipgloss.Style
	added    lipgloss.Style
	removed  lipgloss.Style
	success  lipgloss.Style
	warning  lipgloss.Style
	failure  lipgloss.Style
	active   lipgloss.Style
	done     lipgloss.Style
}

// newStyles returns the renderer's styles; without color every style is
// the identity so the layout survives on its own.
func newStyles(w io.Writer, color bool) styles {
	renderer := lipgloss.NewRenderer(w)
	base := renderer.NewStyle()
	s := styles{base, base, base, base, base, base, base, base, base, base, base, base}
	if !color {
		renderer.SetColorProfile(termenv.Ascii)
		return s
	}
	profile := termenv.NewOutput(w, termenv.WithUnsafe()).ColorProfile()
	if profile == termenv.Ascii {
		profile = termenv.ANSI
	}
	renderer.SetColorProfile(profile)

	s.muted = base.Faint(true)
	s.thinking = base.Faint(true).Italic(true)
	s.bullet = base.Foreground(lipgloss.Color(colorCyan))
	s.tool = base.Bold(true)
	s.added = base.Foreground(lipgloss.Color(colorGreen))
	s.removed = base.Foreground(lipgloss.Color(colorRed))
	s.success = base.Foreground(lipgloss.Color(colorGreen)).Bold(true)
	s.warning = base.Foreground(lipgloss.Color(colorYellow)).Bold(true)
	s.failure = base.Foreground(lipgloss.Color(colorRed)).Bold(true)
	s.active = base.Bold(true)
	s.done = base.Faint(true).Strikethrough(true)
	return s
}
