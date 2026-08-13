package lint

import (
	"strings"
	"testing"

	"github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
)

func cfg() config.Config {
	c := &config.Config{}
	// minimal config for tests
	c.Types = []config.CommitType{
		{Name: "feat", Description: "a"}, {Name: "fix", Description: "b"},
		{Name: "docs", Description: "c"}, {Name: "test", Description: "d"},
		{Name: "chore", Description: "e"},
	}
	c.Scope.Mode = "list"
	c.Scope.Values = []config.Scope{{Name: "api"}, {Name: "cli"}, {Name: "tui"}}
	c.Subject = config.SubjectConfig{MaxLength: 72, MinLength: 1, Case: "any", ForbidTrailingPeriod: true}
	c.Footers = config.FootersConfig{BreakingNeedsDescription: true}
	c.Footers.Keys = []config.FooterKey{{Token: "Closes", Separator: " #"}, {Token: "Reviewed-by", Separator: ": "}}
	c.Rules = map[string]string{}
	return *c
}

func validate(t *testing.T, raw string) []Violation {
	t.Helper()
	c := cfg()
	r := commit.Parse(raw)
	return Validate(r.Message, c)
}

func severities(v []Violation, id string) []string {
	var out []string
	for _, vi := range v {
		if vi.RuleID == id {
			out = append(out, vi.Severity)
		}
	}
	return out
}

func hasError(t *testing.T, v []Violation, id string) {
	t.Helper()
	for _, vi := range v {
		if vi.RuleID == id && vi.Severity == SevError {
			return
		}
	}
	t.Fatalf("expected error %q, got %+v", id, v)
}

func TestTypeRequired(t *testing.T) {
	hasError(t, validate(t, "wip"), "type-required")
	if len(validate(t, "feat: x")) > 0 {
		t.Fatal("valid message should pass")
	}
}

func TestTypeKnown(t *testing.T) {
	v := validate(t, "feet: x")
	hasError(t, v, "type-known")
}

func TestTypeCase(t *testing.T) {
	v := validate(t, "Feat: x")
	hasError(t, v, "type-case")
}

func TestScopeKnown(t *testing.T) {
	v := validate(t, "feat(nope): x")
	hasError(t, v, "scope-known")
	if len(validate(t, "feat(api): x")) != 0 {
		t.Fatal("known scope should pass")
	}
}

func TestScopeRequired(t *testing.T) {
	c := cfg()
	c.Scope.Required = true
	r := commit.Parse("feat: x")
	v := Validate(r.Message, c)
	hasError(t, v, "scope-required")
}

func TestSubjectRules(t *testing.T) {
	c := cfg()
	r := commit.Parse("feat: " + strings.Repeat("x", 100))
	v := Validate(r.Message, c)
	hasError(t, v, "subject-max-length")
}

func TestTrailingPeriodWarning(t *testing.T) {
	v := validate(t, "feat: subject.")
	if len(severities(v, "subject-no-trailing-period")) != 1 {
		t.Fatalf("expected warning, got %+v", v)
	}
}

func TestSubjectCase(t *testing.T) {
	c := cfg()
	c.Subject.Case = "lower"
	c.Rules["subject-case"] = "error"
	r := commit.Parse("feat: Add Capital")
	v := Validate(r.Message, c)
	hasError(t, v, "subject-case")
}

func TestSubjectFormat(t *testing.T) {
	v := validate(t, "feat:bump deps")
	hasError(t, v, "subject-format")
}

func TestBreakingNeedsDescription(t *testing.T) {
	v := validate(t, "feat!: change api")
	hasError(t, v, "breaking-needs-description")
	if len(validate(t, "feat!: change api\n\nBREAKING CHANGE: removed")) != 0 {
		t.Fatal("with footer should pass")
	}
}

func TestFooterTokenKnown(t *testing.T) {
	v := validate(t, "feat: x\n\nBogus: 1")
	hasError(t, v, "footer-token-known")
}

func TestSeverityOverride(t *testing.T) {
	c := cfg()
	c.Rules["subject-max-length"] = "warning"
	r := commit.Parse("feat: " + strings.Repeat("x", 200))
	v := Validate(r.Message, c)
	for _, vi := range v {
		if vi.RuleID == "subject-max-length" && vi.Severity == SevError {
			t.Fatal("expected downgraded to warning")
		}
	}
}

func TestSeverityOff(t *testing.T) {
	c := cfg()
	c.Rules["type-known"] = "off"
	r := commit.Parse("feet: x")
	v := Validate(r.Message, c)
	for _, vi := range v {
		if vi.RuleID == "type-known" {
			t.Fatal("rule should be off")
		}
	}
}

func TestHiddenTypeStillKnown(t *testing.T) {
	c := cfg()
	c.Types = append(c.Types, config.CommitType{Name: "wip", Hidden: true})
	r := commit.Parse("wip: something")
	v := Validate(r.Message, c)
	for _, vi := range v {
		if vi.RuleID == "type-known" {
			t.Fatal("hidden type must still validate as known")
		}
	}
}

// The AD-3 invariant: any message the form can assemble, Validate accepts.
func TestFormAcceptsWhatValidateAccepts(t *testing.T) {
	c := cfg()
	c.Subject.MaxLength = 72
	subjects := []string{"add live preview", "fix a bug", "handle empty list", "a short one"}
	types := []string{"feat", "fix", "docs", "test", "chore"}
	scopes := []string{"", "api", "cli", "tui"}
	breaking := []bool{false, true}
	for _, ty := range types {
		for _, sc := range scopes {
			for _, b := range breaking {
				for _, s := range subjects {
					m := commit.CommitMessage{Type: ty, Scope: sc, Breaking: b, Subject: s}
					if b {
						m.Footers = []commit.Footer{{Token: "BREAKING CHANGE", Value: "what breaks"}}
					}
					assembled := commit.Assemble(m, commit.AssembleOptions{BreakingBody: true})
					parsed := commit.Parse(assembled)
					v := Validate(parsed.Message, c)
					for _, vi := range v {
						if vi.Severity == SevError {
							t.Fatalf("form-produced message rejected: %q rule=%s", assembled, vi.RuleID)
						}
					}
				}
			}
		}
	}
}
