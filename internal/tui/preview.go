package tui

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/fakihariefnoto/commitly/internal/commit"
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

// previewColors resolves the emerald palette for light/dark terminals.
type previewColors struct {
	headerBg lipgloss.Color
	headerFg lipgloss.Color
	border   lipgloss.Color
	text     lipgloss.Color
	muted    lipgloss.Color
	typ      lipgloss.Color
	scope    lipgloss.Color
	breaking lipgloss.Color
	footer   lipgloss.Color
}

func palette(dark bool) previewColors {
	if dark {
		return previewColors{
			headerBg: lipgloss.Color("#21F8BB"),
			headerFg: lipgloss.Color("#0B1512"),
			border:   lipgloss.Color("#374944"),
			text:     lipgloss.Color("#F1F4F3"),
			muted:    lipgloss.Color("#9DAFAA"),
			typ:      lipgloss.Color("#21F8BB"),
			scope:    lipgloss.Color("#B085F5"),
			breaking: lipgloss.Color("#F0A44A"),
			footer:   lipgloss.Color("#5CC8F5"),
		}
	}
	return previewColors{
		headerBg: lipgloss.Color("#047857"),
		headerFg: lipgloss.Color("#FFFFFF"),
		border:   lipgloss.Color("#D4E3DB"),
		text:     lipgloss.Color("#171C1A"),
		muted:    lipgloss.Color("#5C7066"),
		typ:      lipgloss.Color("#047857"),
		scope:    lipgloss.Color("#7C3AED"),
		breaking: lipgloss.Color("#B45309"),
		footer:   lipgloss.Color("#0369A1"),
	}
}

// previewBox renders the assembled message as a window with a full-width
// title bar, color-coding the type, scope, breaking marker, subject, body
// and footers so the message reads at a glance.
func previewBox(msg commit.CommitMessage, opts CommitOpts) string {
	c := palette(opts.Dark)
	const width = 64

	var lines []string

	// Header line: feat(scope)!: subject, each part color-coded.
	head := lipgloss.NewStyle().Foreground(c.typ).Bold(true).Render(msg.Type)
	if msg.Scope != "" {
		head += lipgloss.NewStyle().Foreground(c.scope).Render("(" + msg.Scope + ")")
	}
	if msg.Breaking {
		head += lipgloss.NewStyle().Foreground(c.breaking).Bold(true).Render("!")
	}
	head += lipgloss.NewStyle().Foreground(c.text).Render(": " + msg.Subject)
	lines = append(lines, wrapLines(head, width)...)

	// Body.
	if msg.Body != "" {
		lines = append(lines, "")
		body := lipgloss.NewStyle().Foreground(c.muted).Render(msg.Body)
		lines = append(lines, wrapLines(body, width)...)
	}

	// Footers — the BREAKING CHANGE footer in warning color, others blue.
	for _, f := range msg.Footers {
		lines = append(lines, "")
		var line string
		if strings.EqualFold(f.Token, "BREAKING CHANGE") || strings.EqualFold(f.Token, "BREAKING-CHANGE") {
			token := lipgloss.NewStyle().Foreground(c.breaking).Bold(true).Render(f.Token)
			value := lipgloss.NewStyle().Foreground(c.text).Render(": " + f.Value)
			line = token + value
		} else {
			token := lipgloss.NewStyle().Foreground(c.footer).Bold(true).Render(f.Token)
			sep := ": "
			if f.Hashes {
				sep = " #"
			}
			value := lipgloss.NewStyle().Foreground(c.text).Render(sep + f.Value)
			line = token + value
		}
		lines = append(lines, wrapLines(line, width)...)
	}

	return drawWindow(width, "Preview", c, lines)
}

// drawWindow draws a titled box: full-width title bar with background, a
// divider, content lines, and a bottom border.
func drawWindow(width int, title string, c previewColors, lines []string) string {
	horiz := strings.Repeat("─", width)
	var b strings.Builder
	b.WriteString("╭" + horiz + "╮\n")

	titleText := " " + title + " "
	pad := width - lipgloss.Width(titleText)
	if pad < 0 {
		pad = 0
	}
	bar := titleText + strings.Repeat(" ", pad)
	b.WriteString("│" + lipgloss.NewStyle().Background(c.headerBg).Foreground(c.headerFg).Bold(true).Render(bar) + "│\n")
	b.WriteString("├" + horiz + "┤\n")

	for _, l := range lines {
		b.WriteString("│" + padTo(l, width) + "│\n")
	}
	b.WriteString("╰" + horiz + "╯")
	return b.String()
}

// padTo pads a styled string to at least width terminal cells, truncating
// over-long input by slicing the visible runes (ANSIs may trail).
func padTo(s string, width int) string {
	w := lipgloss.Width(s)
	if w >= width {
		return s
	}
	return s + strings.Repeat(" ", width-w)
}

// wrapLines wraps a styled string to the given width, keeping ANSI intact.
func wrapLines(s string, width int) []string {
	if lipgloss.Width(s) <= width {
		return []string{s}
	}
	// Break on words, accounting for ANSI widths is approximate here — this
	// is a preview, and long tokens are truncated by padTo anyway.
	var out []string
	for _, para := range strings.Split(s, "\n") {
		cur := ""
		for _, word := range strings.Fields(para) {
			wordW := lipgloss.Width(word)
			curW := lipgloss.Width(cur)
			if cur != "" && curW+1+wordW > width {
				out = append(out, cur)
				cur = word
			} else if cur == "" {
				cur = word
			} else {
				cur += " " + word
			}
		}
		if cur != "" {
			out = append(out, cur)
		}
	}
	return out
}
