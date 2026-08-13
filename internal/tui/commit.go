package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
)

// CommitOpts feeds the wizard.
type CommitOpts struct {
	Types         []config.CommitType
	Scopes        []config.Scope
	ScopeMode     string
	SubjectMax    int
	SubjectMin    int
	BodyWrap      int
	Emoji         string
	EmojiPrefix   bool
	FooterKeys    []config.FooterKey
	Initial       commit.CommitMessage
	DefaultScope  string
	EditorForBody bool
	Accessible    bool
	Dark          bool

	// OnAddScope persists a newly-typed scope. toRepo is true when the user
	// chose to save it to the repo's .commitly.yaml; false saves to the user
	// config. The wizard asks which before saving, and always uses the scope
	// for the current commit regardless.
	OnAddScope func(scope string, toRepo bool) error
}

// ErrAborted is returned when the user cancels the wizard.
var ErrAborted = errors.New("aborted")

// IsAborted reports whether err is a wizard abort.
func IsAborted(err error) bool { return errors.Is(err, ErrAborted) }

// item adapts a menu option to bubbles/list.
type Option struct {
	Value       string
	Description string
}

type item struct {
	title string
	desc  string
}

func (i item) Title() string       { return i.title }
func (i item) Description() string { return i.desc }
func (i item) FilterValue() string { return i.title }

// stages the wizard walks through.
const (
	stType = iota
	stScope
	stNewScope
	stSaveScope
	stSubject
	stBreaking
	stBreakDesc
	stBody
	stFooters
	stConfirm
	stDone
)

type model struct {
	opts CommitOpts
	msg  commit.CommitMessage

	stage int
	menu  list.Model
	input textinput.Model

	// addScopeNote surfaces a config-write failure after the user adds a new
	// scope — the scope is still used, but they should know it wasn't saved.
	addScopeNote string

	confirmed bool
	aborted   bool
}

// loadMenu builds the Bubble Tea list for a stage. Height is sized from the
// option count so even 2–3 option menus (Yes/No, Commit/Edit again) show
// every option instead of clipping the tail in the viewport.
func (m *model) loadMenu(title string, options []Option) {
	height := len(options)*2 + 2
	if height > 24 {
		height = 24
	}
	m.menu = newList(title, options, height)
}

func newList(title string, options []Option, height int) list.Model {
	items := make([]list.Item, 0, len(options))
	for _, o := range options {
		items = append(items, item{title: o.Value, desc: o.Description})
	}
	d := list.NewDefaultDelegate()
	d.SetSpacing(0)
	l := list.New(items, d, 48, height)
	l.Title = title
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetFilteringEnabled(true)
	l.SetShowPagination(false)
	return l
}

func newInput(placeholder string) textinput.Model {
	i := textinput.New()
	i.Placeholder = placeholder
	i.CharLimit = 0
	i.Focus()
	return i
}

func newModel(opts CommitOpts) *model {
	m := &model{opts: opts, msg: opts.Initial}
	if opts.DefaultScope != "" && m.msg.Scope == "" {
		m.msg.Scope = opts.DefaultScope
	}
	m.stage = stType
	m.loadStage()
	return m
}

func (m *model) loadStage() {
	switch m.stage {
	case stType:
		opts := make([]Option, 0, len(m.opts.Types))
		for _, t := range m.opts.Types {
			opts = append(opts, Option{Value: t.Name, Description: t.Description})
		}
		m.loadMenu("Type", opts)
	case stScope:
		if m.opts.ScopeMode == "list" || m.opts.ScopeMode == "" {
			opts := []Option{{Value: "(none)"}}
			for _, s := range m.opts.Scopes {
				opts = append(opts, Option{Value: s.Name, Description: s.Description})
			}
			if m.opts.OnAddScope != nil {
				opts = append(opts, Option{Value: "+ Add new scope…", Description: "writes it to this repo's .commitly.yaml"})
			}
			m.loadMenu("Scope", opts)
		} else {
			m.input = newInput("scope (optional)")
		}
	case stNewScope:
		m.input = newInput("new scope name")
	case stSaveScope:
		m.loadMenu("Save this scope to:", []Option{
			{Value: "User config", Description: "your ~/.config/commitly/config.yaml — not committed"},
			{Value: "This repo", Description: ".commitly.yaml — committed and shared with the team"},
			{Value: "Don't save", Description: "use it for this commit only"},
		})
	case stSubject:
		m.input = newInput("subject — what did you change?")
		m.input.SetValue(m.msg.Subject)
	case stBreaking:
		m.loadMenu("Breaking change?", []Option{{Value: "No"}, {Value: "Yes"}})
	case stBreakDesc:
		m.input = newInput("what breaks?")
	case stBody:
		m.input = newInput("body (optional)")
		m.input.SetValue(m.msg.Body)
	case stFooters:
		m.input = newInput(`footers (optional), e.g. "Closes #12"`)
		m.input.SetValue(footerText(m.msg.Footers))
	case stConfirm:
		m.loadMenu("Commit this message?", []Option{{Value: "Commit"}, {Value: "Edit again"}})
	}
}

func (m *model) selection() string {
	if it, ok := m.menu.SelectedItem().(item); ok {
		return it.title
	}
	return ""
}

// scopeAskable reports whether the wizard should ask for a scope. Scope is
// only asked when the repo configures one (a list of values), or when the
// mode explicitly allows free input. With no scopes configured, the step is
// skipped entirely.
func (m *model) scopeAskable() bool {
	switch m.opts.ScopeMode {
	case "off":
		return false
	case "free", "auto":
		return true
	default: // "list", ""
		return len(m.opts.Scopes) > 0
	}
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.aborted = true
			return m, tea.Quit
		case "esc":
			if m.stage == stType {
				// Esc on the first step exits the wizard.
				m.aborted = true
				return m, tea.Quit
			}
			m.preserveInput()
			m.stage--
			// Jump back over the add-scope sub-steps to the scope menu.
			if m.stage == stNewScope || m.stage == stSaveScope {
				m.stage = stScope
			}
			if m.stage == stScope && !m.scopeAskable() {
				m.stage--
			}
			m.loadStage()
			return m, nil
		case "enter":
			return m, m.advance()
		}
	}

	// Route to the active widget.
	if m.stage == stSubject || m.stage == stBreakDesc || m.stage == stBody || m.stage == stFooters || m.stage == stNewScope || m.stage == stScope && (m.opts.ScopeMode == "free" || m.opts.ScopeMode == "auto") {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	var cmd tea.Cmd
	m.menu, cmd = m.menu.Update(msg)
	return m, cmd
}

// advance records the current stage's value and moves to the next stage.
func (m *model) advance() tea.Cmd {
	switch m.stage {
	case stType:
		m.msg.Type = m.selection()
		if !m.scopeAskable() {
			// No scopes configured — skip straight to the subject.
			m.stage = stSubject
			m.loadStage()
			return nil
		}
	case stScope:
		if m.opts.ScopeMode == "list" || m.opts.ScopeMode == "" {
			if len(m.opts.Scopes) > 0 {
				switch s := m.selection(); s {
				case "(none)":
					m.msg.Scope = ""
				case "+ Add new scope…":
					m.stage = stNewScope
					m.loadStage()
					return nil
				default:
					m.msg.Scope = s
				}
			} else {
				m.msg.Scope = m.input.Value()
			}
		} else {
			m.msg.Scope = m.input.Value()
		}
	case stNewScope:
		scope := strings.TrimSpace(m.input.Value())
		if scope != "" {
			m.msg.Scope = scope
		}
		m.stage = stSaveScope
		m.loadStage()
		return nil
	case stSaveScope:
		scope := m.msg.Scope
		if scope != "" && m.opts.OnAddScope != nil {
			toRepo := m.selection() == "This repo"
			if m.selection() != "Don't save" {
				if err := m.opts.OnAddScope(scope, toRepo); err != nil {
					m.addScopeNote = "scope \"" + scope + "\" not saved: " + err.Error()
				}
			}
		}
		m.stage = stSubject
		m.loadStage()
		return nil
	case stSubject:
		m.msg.Subject = strings.TrimSpace(m.input.Value())
		if len(m.msg.Subject) < m.opts.SubjectMin || len(m.msg.Subject) > m.opts.SubjectMax {
			m.input.SetValue("")
			return nil
		}
	case stBreaking:
		// Re-answering breaking replaces any prior BREAKING CHANGE footer,
		// so switching No→Yes→No (or via "Edit again") never leaves a stale
		// or doubled footer behind.
		m.msg.Footers = removeBreakingFooters(m.msg.Footers)
		m.msg.Breaking = m.selection() == "Yes"
		if !m.msg.Breaking {
			// skip the break-desc stage
			m.stage = stBody
			m.loadStage()
			return nil
		}
	case stBreakDesc:
		if desc := strings.TrimSpace(m.input.Value()); desc != "" {
			m.msg.Footers = append(m.msg.Footers, commit.Footer{Token: "BREAKING CHANGE", Value: desc})
		}
	case stBody:
		m.msg.Body = strings.TrimSpace(m.input.Value())
	case stFooters:
		// The footers input is pre-filled with the current non-breaking
		// footers; replace them with what was typed, keeping any breaking
		// footer from the breaking step.
		var next []commit.Footer
		for _, f := range m.msg.Footers {
			if isBreakingFooter(f) {
				next = append(next, f)
			}
		}
		for _, line := range strings.Split(m.input.Value(), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			token, value, hashes := splitFooter(line)
			next = append(next, commit.Footer{Token: token, Value: value, Hashes: hashes})
		}
		m.msg.Footers = next
	case stConfirm:
		if m.selection() == "Commit" {
			m.confirmed = true
			return tea.Quit
		}
		// Edit again: keep type/scope/subject/body, but re-answer breaking
		// and footers fresh — otherwise they'd accumulate on the re-run.
		m.msg.Footers = nil
		m.msg.Breaking = false
		m.stage = stType - 1
	}

	m.stage++
	// The new-scope input + save-target are sub-steps of the scope stage,
	// only reachable via "+ Add new scope…". Advancing normally skips past
	// both.
	if m.stage == stNewScope || m.stage == stSaveScope {
		m.stage += 2
	}
	if m.stage >= stDone {
		m.confirmed = true
		return tea.Quit
	}
	m.loadStage()
	return nil
}

func (m *model) View() string {
	var body string
	switch m.stage {
	case stType, stScope, stSaveScope, stBreaking, stConfirm:
		body = m.menu.View()
	case stSubject, stBreakDesc, stBody, stFooters, stNewScope:
		body = m.input.View()
	}

	previewText := ""
	if m.stage != stType && m.stage != stConfirm {
		previewText = previewBox(m.msg, m.opts)
	}

	out := body
	if previewText != "" {
		out += "\n\n" + previewText + "\n"
	}
	if m.addScopeNote != "" {
		out += "\n▲ " + m.addScopeNote + "\n"
	}
	return out
}

// preserveInput copies the active input's current value back into msg before
// an esc-back, so navigating away and returning doesn't lose typed text.
func (m *model) preserveInput() {
	switch m.stage {
	case stSubject:
		m.msg.Subject = m.input.Value()
	case stBody:
		m.msg.Body = m.input.Value()
	case stScope:
		if m.opts.ScopeMode == "free" || m.opts.ScopeMode == "auto" {
			m.msg.Scope = m.input.Value()
		}
	}
}

// footerText rebuilds the footer input's text from parsed footers, so an
// esc-back + forward doesn't lose what was typed.
func footerText(footers []commit.Footer) string {
	var parts []string
	for _, f := range footers {
		if isBreakingFooter(f) {
			continue
		}
		if f.Hashes {
			parts = append(parts, f.Token+" #"+f.Value)
		} else {
			parts = append(parts, f.Token+": "+f.Value)
		}
	}
	return strings.Join(parts, "\n")
}

func isBreakingFooter(f commit.Footer) bool {
	return strings.EqualFold(f.Token, "BREAKING CHANGE") || strings.EqualFold(f.Token, "BREAKING-CHANGE")
}

// removeBreakingFooters drops any BREAKING CHANGE footer from the list.
func removeBreakingFooters(footers []commit.Footer) []commit.Footer {
	var out []commit.Footer
	for _, f := range footers {
		if isBreakingFooter(f) {
			continue
		}
		out = append(out, f)
	}
	return out
}

// RunCommitWizard runs the interactive commit wizard and returns the
// composed message. It uses the standard Bubble Tea list component, renders
// inline (no full-screen), and keeps a live preview visible while composing.
func RunCommitWizard(ctx context.Context, opts CommitOpts) (commit.CommitMessage, error) {
	m := newModel(opts)
	programOpts := []tea.ProgramOption{}
	if ctx != nil {
		programOpts = append(programOpts, tea.WithContext(ctx))
	}
	p := tea.NewProgram(m, programOpts...)
	final, err := p.Run()
	if err != nil {
		return commit.CommitMessage{}, err
	}
	cm := final.(*model)
	if cm.aborted || !cm.confirmed {
		return commit.CommitMessage{}, ErrAborted
	}
	return cm.msg, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func assembleOpts(opts CommitOpts) commit.AssembleOptions {
	ao := commit.AssembleOptions{BodyWrap: opts.BodyWrap}
	if opts.Emoji != "" {
		ao.Emoji = opts.Emoji
		ao.EmojiPrefix = opts.EmojiPrefix
	}
	return ao
}

func splitFooter(line string) (token, value string, hashes bool) {
	if i := strings.Index(line, " #"); i > 0 {
		return line[:i], strings.TrimSpace(line[i+2:]), true
	}
	if i := strings.Index(line, ": "); i > 0 {
		return line[:i], strings.TrimSpace(line[i+2:]), false
	}
	return line, "", false
}

var _ = fmt.Sprintf
