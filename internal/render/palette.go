package render

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// DetectDark reports whether the terminal has a dark background using only
// non-blocking signals (COLORFGBG, TERM). No terminal queries.
func DetectDark() bool {
	if fg := os.Getenv("COLORFGBG"); fg != "" {
		parts := strings.Split(fg, ";")
		if len(parts) >= 2 {
			var bg int
			if _, err := fmt.Sscanf(parts[len(parts)-1], "%d", &bg); err == nil {
				return bg < 8
			}
		}
	}
	term := strings.ToLower(os.Getenv("TERM"))
	if strings.Contains(term, "dark") || strings.Contains(term, "linux") {
		return true
	}
	return false
}

// Palette holds the style-guide tokens resolved for light or dark terminals.
// Color fields are private; use the paint methods, which respect whether
// color is enabled.
type Palette struct {
	text     lipgloss.Color
	muted    lipgloss.Color
	primary  lipgloss.Color
	feature  lipgloss.Color
	fix      lipgloss.Color
	docs     lipgloss.Color
	quality  lipgloss.Color
	chore    lipgloss.Color
	breaking lipgloss.Color
	err      lipgloss.Color
	warning  lipgloss.Color
}

// NewPalette resolves the palette for the current terminal background.
func NewPalette(dark bool) *Palette {
	if dark {
		return &Palette{
			text:     lipgloss.Color("#F1F4F3"),
			muted:    lipgloss.Color("#9DAFAA"),
			primary:  lipgloss.Color("#21F8BB"),
			feature:  lipgloss.Color("#21F8BB"),
			fix:      lipgloss.Color("#E46258"),
			docs:     lipgloss.Color("#5CC8F5"),
			quality:  lipgloss.Color("#B085F5"),
			chore:    lipgloss.Color("#9DAFAA"),
			breaking: lipgloss.Color("#F0A44A"),
			err:      lipgloss.Color("#E46258"),
			warning:  lipgloss.Color("#F0A44A"),
		}
	}
	return &Palette{
		text:     lipgloss.Color("#171C1A"),
		muted:    lipgloss.Color("#5C7066"),
		primary:  lipgloss.Color("#047857"),
		feature:  lipgloss.Color("#047857"),
		fix:      lipgloss.Color("#C52B20"),
		docs:     lipgloss.Color("#0369A1"),
		quality:  lipgloss.Color("#7C3AED"),
		chore:    lipgloss.Color("#5C7066"),
		breaking: lipgloss.Color("#B45309"),
		err:      lipgloss.Color("#C52B20"),
		warning:  lipgloss.Color("#B45309"),
	}
}

// paint wraps s in a foreground color (and optional bold), or returns it
// plain when color is disabled.
func (p *Palette) paint(s string, c lipgloss.Color, bold bool, enabled bool) string {
	if !enabled {
		return s
	}
	st := lipgloss.NewStyle().Foreground(c)
	if bold {
		st = st.Bold(true)
	}
	return st.Render(s)
}

// Text paints plain body text.
func (p *Palette) Text(s string, enabled bool) string { return p.paint(s, p.text, false, enabled) }

// Muted paints secondary text (paths, SHAs, timestamps).
func (p *Palette) Muted(s string, enabled bool) string { return p.paint(s, p.muted, false, enabled) }

// Primary paints the brand color (repo names, emphasis).
func (p *Palette) Primary(s string, enabled bool) string { return p.paint(s, p.primary, true, enabled) }

// Error paints error-colored text.
func (p *Palette) Error(s string, enabled bool) string { return p.paint(s, p.err, true, enabled) }

// Warning paints warning-colored text.
func (p *Palette) Warning(s string, enabled bool) string { return p.paint(s, p.warning, true, enabled) }

// TypeColor returns the accent color for a commit type, or the neutral
// color when the type isn't in a known bucket.
func (p *Palette) TypeColor(typ string) lipgloss.Color {
	switch typ {
	case "feat":
		return p.feature
	case "fix", "revert":
		return p.fix
	case "docs":
		return p.docs
	case "refactor", "perf", "test", "style":
		return p.quality
	case "build", "ci", "chore", "deps":
		return p.chore
	}
	return p.muted
}

// Type paints a type label with its accent color; breaking overrides with
// the warning color (the most important fact in the row).
func (p *Palette) Type(s string, breaking bool, enabled bool) string {
	c := p.TypeColor(s)
	if breaking {
		c = p.breaking
	}
	return p.paint(s, c, true, enabled)
}
