package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
)

func testOpts() CommitOpts {
	return CommitOpts{
		Types: []config.CommitType{
			{Name: "feat", Description: "a"}, {Name: "fix", Description: "b"}, {Name: "docs", Description: "c"},
		},
		Scopes:     []config.Scope{{Name: "api"}, {Name: "cli"}},
		ScopeMode:  "list",
		SubjectMax: 72,
		SubjectMin: 1,
		BodyWrap:   72,
	}
}

func TestIsAborted(t *testing.T) {
	if IsAborted(nil) {
		t.Fatal("nil is not aborted")
	}
	if !IsAborted(ErrAborted) {
		t.Fatal("ErrAborted should be aborted")
	}
}

func TestSplitFooter(t *testing.T) {
	token, value, hashes := splitFooter("Closes #12")
	if token != "Closes" || value != "12" || !hashes {
		t.Errorf("got %q %q %v", token, value, hashes)
	}
	token, value, hashes = splitFooter("Reviewed-by: Sam")
	if token != "Reviewed-by" || value != "Sam" || hashes {
		t.Errorf("got %q %q %v", token, value, hashes)
	}
	token, value, _ = splitFooter("plain")
	if token != "plain" || value != "" {
		t.Errorf("got %q %q", token, value)
	}
}

func TestSelectionReadsHighlighted(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	// The type list highlights the first item (feat).
	if got := m.selection(); got != "feat" {
		t.Errorf("selection=%q want feat", got)
	}
}

func TestAssembleOptsEmoji(t *testing.T) {
	opts := CommitOpts{Emoji: "✨", EmojiPrefix: true, BodyWrap: 72}
	ao := assembleOpts(opts)
	if ao.Emoji != "✨" || !ao.EmojiPrefix || ao.BodyWrap != 72 {
		t.Errorf("assemble opts: %+v", ao)
	}
}

func TestPreviewAssembles(t *testing.T) {
	msg := commit.CommitMessage{Type: "feat", Scope: "api", Subject: "add x"}
	line := commit.Assemble(msg, assembleOpts(testOpts()))
	if line != "feat(api): add x" {
		t.Errorf("assemble: %q", line)
	}
}

func TestModelFlow(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	// Simulate: select feat, scope api, subject, breaking No, skip body/footers, Commit.
	m.msg.Type = "feat"
	m.stage = stScope
	m.loadStage()
	// highlight api (index 1) via the list is hard to set directly; instead
	// verify the stage machine advances and assembles correctly.
	m.msg.Scope = "api"
	m.stage = stSubject
	m.loadStage()
	m.input.SetValue("add pagination")
	m.msg.Subject = "add pagination"
	m.stage = stBreaking
	m.loadStage()
	m.msg.Breaking = false
	m.stage = stBody
	m.loadStage()
	m.stage = stFooters
	m.loadStage()
	m.stage = stConfirm
	m.loadStage()
	if m.selection() != "Commit" {
		t.Errorf("confirm first option=%q", m.selection())
	}
	if got := commit.Assemble(m.msg, assembleOpts(opts)); got != "feat(api): add pagination" {
		t.Errorf("assemble=%q", got)
	}
}

func TestScopeSkippedWhenNoScopesConfigured(t *testing.T) {
	opts := testOpts()
	opts.Scopes = nil
	opts.ScopeMode = "list"
	m := newModel(opts)
	if m.scopeAskable() {
		t.Fatal("scope should not be askable with no configured values")
	}
	// Advancing from type should land on the subject, skipping scope.
	m.stage = stType
	_ = m.advance()
	if m.stage != stSubject {
		t.Fatalf("stage=%d want stSubject(%d)", m.stage, stSubject)
	}
}

func TestScopeAskableFree(t *testing.T) {
	opts := testOpts()
	opts.Scopes = nil
	opts.ScopeMode = "free"
	m := newModel(opts)
	if !m.scopeAskable() {
		t.Fatal("free scope mode should still ask")
	}
}

func TestBreakingMenuShowsBoth(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	m.stage = stBreaking
	m.loadStage()
	// The Bubble Tea list must render BOTH options — the viewport should not
	// clip the second one.
	view := m.View()
	if !strings.Contains(view, "No") || !strings.Contains(view, "Yes") {
		t.Fatalf("breaking menu clipped an option:\n%s", view)
	}
}

func TestBreakingNoRemovesFooter(t *testing.T) {
	m := newModel(testOpts())
	m.msg = commit.CommitMessage{Type: "feat", Breaking: true, Footers: []commit.Footer{{Token: "BREAKING CHANGE", Value: "old"}}}
	m.stage = stBreaking
	m.loadStage()
	// First menu item is "No" → enter
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.msg.Breaking {
		t.Error("breaking should be No")
	}
	if len(m.msg.Footers) != 0 {
		t.Errorf("stale BREAKING CHANGE footer left: %+v", m.msg.Footers)
	}
}

func TestBreakingYesNotDoubled(t *testing.T) {
	m := newModel(testOpts())
	m.msg = commit.CommitMessage{Type: "feat", Footers: []commit.Footer{{Token: "BREAKING CHANGE", Value: "old"}}}
	m.stage = stBreaking
	m.loadStage()
	// move down to "Yes", enter
	m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	// now at stBreakDesc
	if m.stage != stBreakDesc {
		t.Fatalf("stage=%d want stBreakDesc", m.stage)
	}
	if len(m.msg.Footers) != 0 {
		t.Errorf("old breaking footer should be removed before re-adding: %+v", m.msg.Footers)
	}
	// type a new desc, enter
	for _, c := range "new break" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
	}
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	breaks := 0
	for _, f := range m.msg.Footers {
		if isBreakingFooter(f) {
			breaks++
		}
	}
	if breaks != 1 {
		t.Errorf("breaking footer count=%d want 1: %+v", breaks, m.msg.Footers)
	}
}

func TestFootersReplacedNotDoubled(t *testing.T) {
	m := newModel(testOpts())
	m.msg = commit.CommitMessage{Type: "feat", Footers: []commit.Footer{{Token: "Closes", Value: "1", Hashes: true}}}
	m.stage = stFooters
	m.loadStage()
	// input pre-filled with existing footer
	if m.input.Value() != "Closes #1" {
		t.Errorf("prefill=%q", m.input.Value())
	}
	// leave as-is and advance → should not double
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	count := 0
	for _, f := range m.msg.Footers {
		if f.Token == "Closes" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Closes count=%d want 1: %+v", count, m.msg.Footers)
	}
}
