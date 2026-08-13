package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/fakihariefnoto/commitly/internal/git"
)

// DiffFunc returns the diff text to preview for a file change.
type DiffFunc func(fc git.FileChange) string

// fileItem adapts a FileChange to bubbles/list. selected renders a visible
// [x]/[ ] checkbox so toggling with space has immediate feedback.
type fileItem struct {
	change   git.FileChange
	selected bool
}

func (f fileItem) Title() string {
	box := "[ ]"
	if f.selected {
		box = "[x]"
	}
	return box + " " + f.change.Path
}

func (f fileItem) Description() string {
	adds, dels := stats(f.change)
	return fmt.Sprintf("%-3s  %s/%s", mark(f.change), adds, dels)
}

func (f fileItem) FilterValue() string { return f.change.Path }

type pickModel struct {
	list      list.Model
	selected  map[string]bool
	theme     lipgloss.Style
	confirmed bool
	aborted   bool
	result    []string
	keys      []git.FileChange
}

func (m pickModel) Init() tea.Cmd { return nil }

// rebuildItems re-syncs the list rows with the current selection so the
// [x]/[ ] checkboxes update, keeping the cursor where it was.
func (m *pickModel) rebuildItems() {
	cursor := m.list.Cursor()
	items := make([]list.Item, 0, len(m.keys))
	for _, ch := range m.keys {
		items = append(items, fileItem{change: ch, selected: m.selected[ch.Path]})
	}
	m.list.SetItems(items)
	m.list.Select(cursor)
}

func (m pickModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case " ":
			if it, ok := m.list.SelectedItem().(fileItem); ok {
				m.selected[it.change.Path] = !m.selected[it.change.Path]
				m.rebuildItems()
			}
			return m, nil
		case "a":
			all := true
			for _, ch := range m.keys {
				if !m.selected[ch.Path] {
					all = false
					break
				}
			}
			for _, ch := range m.keys {
				m.selected[ch.Path] = !all
			}
			m.rebuildItems()
			return m, nil
		case "enter":
			m.confirmed = true
			for _, ch := range m.keys {
				if m.selected[ch.Path] {
					m.result = append(m.result, ch.Path)
				}
			}
			return m, tea.Quit
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m pickModel) View() string {
	selectedCount := 0
	for _, ch := range m.keys {
		if m.selected[ch.Path] {
			selectedCount++
		}
	}
	return m.list.View() +
		"\n\n" +
		lipgloss.NewStyle().Foreground(lipgloss.Color("#5C7066")).
			Render(fmt.Sprintf("%d selected · space toggle · a all · enter continue · ctrl-c abort", selectedCount))
}

// RunStagingPicker presents the changed files as a filterable arrow-key list
// (space toggles, enter confirms) and returns the selected paths. Nothing is
// staged until the caller runs `git add` (AD-5).
func RunStagingPicker(ctx context.Context, changes []git.FileChange, diffFn DiffFunc) ([]string, error) {
	sorted := append([]git.FileChange(nil), changes...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	items := make([]list.Item, 0, len(sorted))
	for _, ch := range sorted {
		items = append(items, fileItem{change: ch})
	}
	d := list.NewDefaultDelegate()
	d.SetSpacing(0)
	l := list.New(items, d, 48, minInt(len(items), 12))
	l.Title = "Stage changes"
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.SetShowPagination(false)

	m := pickModel{list: l, selected: map[string]bool{}, keys: sorted}
	programOpts := []tea.ProgramOption{}
	if ctx != nil {
		programOpts = append(programOpts, tea.WithContext(ctx))
	}
	p := tea.NewProgram(m, programOpts...)
	final, err := p.Run()
	if err != nil {
		return nil, err
	}
	pm := final.(pickModel)
	if pm.aborted || !pm.confirmed {
		return nil, ErrAborted
	}
	return pm.result, nil
}

func mark(ch git.FileChange) string {
	if ch.Untracked {
		return "?"
	}
	if ch.IsStaged() && ch.IsUnstaged() {
		return "MM"
	}
	if ch.IsStaged() {
		return "M "
	}
	return " M"
}

func stats(ch git.FileChange) (adds, dels string) {
	if ch.Additions < 0 {
		return "bin", "bin"
	}
	return fmt.Sprintf("+%d", ch.Additions), fmt.Sprintf("-%d", ch.Deletions)
}

var _ = strings.TrimSpace
