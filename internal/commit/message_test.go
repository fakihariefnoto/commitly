package commit

import "testing"

func TestParseBasic(t *testing.T) {
	r := Parse("feat(api): add pagination")
	if !r.OK {
		t.Fatalf("expected OK, got %q", r.Reason)
	}
	m := r.Message
	if m.Type != "feat" || m.Scope != "api" || m.Subject != "add pagination" {
		t.Fatalf("wrong fields: %+v", m)
	}
	if m.Breaking {
		t.Fatal("unexpected breaking")
	}
}

func TestParseNoScope(t *testing.T) {
	r := Parse("fix: handle empty list")
	if !r.OK || r.Message.Scope != "" || r.Message.Subject != "handle empty list" {
		t.Fatalf("bad parse: %+v", r)
	}
}

func TestParseBreakingMarker(t *testing.T) {
	r := Parse("feat(cli)!: drop flag")
	if !r.OK || !r.Message.Breaking {
		t.Fatalf("expected breaking: %+v", r)
	}
}

func TestParseBreakingFooter(t *testing.T) {
	r := Parse("feat: change api\n\nBREAKING CHANGE: removed endpoint")
	if !r.OK || !r.Message.Breaking {
		t.Fatalf("expected breaking via footer: %+v", r.Message)
	}
}

func TestParseBreakingHyphen(t *testing.T) {
	r := Parse("feat: change api\n\nBREAKING-CHANGE: removed endpoint")
	if !r.OK || !r.Message.Breaking {
		t.Fatalf("expected breaking via hyphenated footer: %+v", r.Message)
	}
}

func TestParseBodyAndFooters(t *testing.T) {
	r := Parse("feat(api): add x\n\nSome body\nover two lines.\n\nCloses #12\nReviewed-by: Sam")
	if !r.OK {
		t.Fatalf("expected OK: %s", r.Reason)
	}
	m := r.Message
	if m.Body != "Some body\nover two lines." {
		t.Fatalf("body: %q", m.Body)
	}
	if len(m.Footers) != 2 {
		t.Fatalf("footers: %+v", m.Footers)
	}
	if m.Footers[0].Token != "Closes" || m.Footers[0].Value != "12" || !m.Footers[0].Hashes {
		t.Fatalf("footer0: %+v", m.Footers[0])
	}
	if m.Footers[1].Token != "Reviewed-by" || m.Footers[1].Value != "Sam" || m.Footers[1].Hashes {
		t.Fatalf("footer1: %+v", m.Footers[1])
	}
}

func TestParseMultiParagraphBody(t *testing.T) {
	r := Parse("fix: x\n\npara one\n\npara two")
	if !r.OK || r.Message.Body != "para one\n\npara two" {
		t.Fatalf("body: %q", r.Message.Body)
	}
}

func TestParseCRLF(t *testing.T) {
	r := Parse("feat: x\r\n\r\nbody\r\n")
	if !r.OK || r.Message.Subject != "x" {
		t.Fatalf("crlf: %+v", r.Message)
	}
}

func TestParseNonConventional(t *testing.T) {
	r := Parse("fixed the parser thing")
	if r.OK {
		t.Fatal("expected not OK")
	}
	if r.Message.Raw != "fixed the parser thing" {
		t.Fatalf("raw not preserved: %q", r.Message.Raw)
	}
}

func TestParseEmpty(t *testing.T) {
	r := Parse("")
	if r.OK {
		t.Fatal("expected not OK for empty")
	}
}

func TestParseSubjectWithColon(t *testing.T) {
	r := Parse("docs: explain time: a note")
	if !r.OK || r.Message.Subject != "explain time: a note" {
		t.Fatalf("subject with colon: %+v", r.Message)
	}
}

func TestAssembleRoundTrip(t *testing.T) {
	cases := []CommitMessage{
		{Type: "feat", Subject: "add x"},
		{Type: "feat", Scope: "api", Subject: "add x"},
		{Type: "fix", Scope: "cli", Breaking: true, Subject: "drop flag"},
		{Type: "feat", Subject: "x", Body: "a\n\nb"},
		{Type: "fix", Subject: "x", Footers: []Footer{{Token: "Closes", Value: "12", Hashes: true}}},
		{Type: "feat", Breaking: true, Subject: "change api", Body: "why", Footers: []Footer{{Token: "BREAKING CHANGE", Value: "removed endpoint"}}},
	}
	for i, m := range cases {
		assembled := Assemble(m, AssembleOptions{})
		r := Parse(assembled)
		if !r.OK {
			t.Errorf("case %d: %q did not re-parse: %s", i, assembled, r.Reason)
			continue
		}
		got := r.Message
		if got.Type != m.Type || got.Scope != m.Scope || got.Breaking != m.Breaking || got.Subject != m.Subject || got.Body != m.Body {
			t.Errorf("case %d: round-trip mismatch: %+v vs %+v", i, got, m)
		}
	}
}

func TestAssembleBreakingTwoPart(t *testing.T) {
	m := CommitMessage{Type: "feat", Scope: "cli", Breaking: true, Subject: "drop --no-verify"}
	m.Footers = []Footer{{Token: "BREAKING CHANGE", Value: "--no-verify is removed"}}
	got := Assemble(m, AssembleOptions{BreakingBody: true})
	want := "feat(cli)!: drop --no-verify\n\nBREAKING CHANGE: --no-verify is removed"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestAssembleEmoji(t *testing.T) {
	m := CommitMessage{Type: "feat", Subject: "add x"}
	got := Assemble(m, AssembleOptions{Emoji: "✨", EmojiPrefix: false})
	if got != "feat ✨: add x" {
		t.Fatalf("emoji after-type: %q", got)
	}
	got = Assemble(m, AssembleOptions{Emoji: "✨", EmojiPrefix: true})
	if got != "✨ feat: add x" {
		t.Fatalf("emoji prefix: %q", got)
	}
}

func TestAssembleWrap(t *testing.T) {
	m := CommitMessage{Type: "fix", Subject: "x", Body: "one two three four five six seven eight nine ten eleven twelve"}
	got := Assemble(m, AssembleOptions{BodyWrap: 20})
	for _, line := range splitLines(got) {
		if len(line) > 20 && line != "" {
			// word may exceed column if a single word is longer
			if len(line) > 25 {
				t.Fatalf("line too long: %d: %q", len(line), line)
			}
		}
	}
}
