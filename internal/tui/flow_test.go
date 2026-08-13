package tui

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

func TestFullWizardFlow(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	p := tea.NewProgram(m, tea.WithOutput(io.Discard), tea.WithInput(strings.NewReader("")))
	go func() {
		time.Sleep(60 * time.Millisecond)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
		time.Sleep(40 * time.Millisecond)
		enter := func() { p.Send(tea.KeyMsg{Type: tea.KeyEnter}); time.Sleep(30 * time.Millisecond) }
		down := func() { p.Send(tea.KeyMsg{Type: tea.KeyDown}); time.Sleep(30 * time.Millisecond) }
		// staging not used here; start at type
		down()  // fix
		enter() // type
		enter() // scope (none)
		// subject: type via runes
		for _, c := range "add pagination" {
			p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
			time.Sleep(5 * time.Millisecond)
		}
		enter() // subject
		enter() // breaking No
		enter() // body
		enter() // footers
		enter() // confirm Commit
	}()
	final, err := p.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = ctx
	cm := final.(*model)
	if !cm.confirmed {
		t.Fatalf("not confirmed, stage=%d", cm.stage)
	}
	if cm.msg.Type != "fix" {
		t.Errorf("type=%q want fix", cm.msg.Type)
	}
	if cm.msg.Subject != "add pagination" {
		t.Errorf("subject=%q", cm.msg.Subject)
	}
	got := cm.msg
	_ = got
	if got.Scope != "" {
		t.Errorf("scope=%q", got.Scope)
	}
}

func TestStagingPickerFlow(t *testing.T) {
	changes := testChanges() // a.go, b.txt, c.go
	items := []list.Item{
		fileItem{change: changes[0]},
		fileItem{change: changes[1]},
		fileItem{change: changes[2]},
	}
	d := list.NewDefaultDelegate()
	d.SetSpacing(0)
	l := list.New(items, d, 48, 3)
	m := pickModel{list: l, selected: map[string]bool{}, keys: changes}
	p := tea.NewProgram(m, tea.WithOutput(io.Discard), tea.WithInput(strings.NewReader("")))
	go func() {
		time.Sleep(40 * time.Millisecond)
		p.Send(tea.WindowSizeMsg{Width: 100, Height: 40})
		time.Sleep(20 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeySpace}) // toggle a.go
		time.Sleep(20 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyDown}) // move to b.txt
		time.Sleep(20 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeySpace}) // toggle b.txt
		time.Sleep(20 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyEnter})
	}()
	final, err := p.Run()
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	pm := final.(pickModel)
	if !pm.confirmed {
		t.Fatal("not confirmed")
	}
	if len(pm.result) != 2 || pm.result[0] != "a.go" || pm.result[1] != "b.txt" {
		t.Errorf("result=%v", pm.result)
	}
}

func TestEscBackPreservesSubject(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	// type (fix) → scope (none) → subject, type text, then esc back
	m.msg.Type = "fix"
	m.stage = stSubject
	m.loadStage()
	for _, c := range "keep this subject" {
		m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
	}
	// esc back from the subject stage → scope, subject text preserved in msg
	final, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	nm := final.(*model)
	if nm.stage != stScope {
		t.Fatalf("stage=%d want stScope", nm.stage)
	}
	if nm.msg.Subject != "keep this subject" {
		t.Fatalf("subject not preserved: %q", nm.msg.Subject)
	}
	// forward again → subject input pre-filled from msg
	final2, _ := nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm2 := final2.(*model)
	if nm2.stage != stSubject {
		t.Fatalf("stage=%d want stSubject", nm2.stage)
	}
	if nm2.input.Value() != "keep this subject" {
		t.Fatalf("subject input not restored: %q", nm2.input.Value())
	}
}

func TestEscOnFirstStepExits(t *testing.T) {
	opts := testOpts()
	m := newModel(opts)
	final, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	_ = cmd
	nm := final.(*model)
	if !nm.aborted {
		t.Fatal("esc on first step should abort")
	}
}

func TestAddNewScope(t *testing.T) {
	var added string
	var toRepo bool
	opts := testOpts()
	opts.OnAddScope = func(scope string, repo bool) error { added = scope; toRepo = repo; return nil }
	m := newModel(opts)
	// scope menu should include the "+ Add new scope…" option
	m.stage = stScope
	m.loadStage()
	// select the last option (+ Add new scope…)
	for i := 0; i < 3; i++ { // none, api, cli, + add → index 3
		m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	// enter → goes to stNewScope
	final, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm := final.(*model)
	if nm.stage != stNewScope {
		t.Fatalf("stage=%d want stNewScope", nm.stage)
	}
	// type a new scope
	for _, c := range "web" {
		nm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{c}})
	}
	// enter → save-target menu
	final1, _ := nm.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm1 := final1.(*model)
	if nm1.stage != stSaveScope {
		t.Fatalf("stage=%d want stSaveScope", nm1.stage)
	}
	// pick "This repo" (2nd option), enter
	nm1.Update(tea.KeyMsg{Type: tea.KeyDown})
	final2, _ := nm1.Update(tea.KeyMsg{Type: tea.KeyEnter})
	nm2 := final2.(*model)
	if nm2.msg.Scope != "web" {
		t.Fatalf("scope=%q want web", nm2.msg.Scope)
	}
	if added != "web" {
		t.Fatalf("OnAddScope not called with %q", added)
	}
	if !toRepo {
		t.Fatal("expected toRepo=true")
	}
	if nm2.stage != stSubject {
		t.Fatalf("stage=%d want stSubject", nm2.stage)
	}
}
