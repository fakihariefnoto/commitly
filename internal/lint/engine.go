// Package lint is the single rules engine (AD-3). The live form validator,
// the lint command and the commit-msg hook all call Validate — one pure
// function from (message, config) to violations. If they can disagree, the
// tool can commit a message its own hook rejects.
package lint

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
)

// Severity levels.
const (
	SevError   = "error"
	SevWarning = "warning"
	SevOff     = "off"
)

// Violation names a rule, its position in the message, and the fix.
type Violation struct {
	RuleID   string
	Severity string
	Message  string
	Hint     string
	Line     int
	Column   int
}

// Rule is the static registry entry.
type Rule struct {
	ID         string
	DefaultSev string
	Describe   string
}

// Registry is the fixed set of rules. Config tunes parameters and severity;
// it can never define a new rule (AD-7).
var Registry = []Rule{
	{ID: "type-required", DefaultSev: SevError},
	{ID: "type-known", DefaultSev: SevError},
	{ID: "type-case", DefaultSev: SevError},
	{ID: "scope-known", DefaultSev: SevError},
	{ID: "scope-required", DefaultSev: SevError},
	{ID: "subject-required", DefaultSev: SevError},
	{ID: "subject-max-length", DefaultSev: SevError},
	{ID: "subject-min-length", DefaultSev: SevError},
	{ID: "subject-case", DefaultSev: SevWarning},
	{ID: "subject-no-trailing-period", DefaultSev: SevWarning},
	{ID: "subject-format", DefaultSev: SevError},
	{ID: "blank-line-before-body", DefaultSev: SevError},
	{ID: "blank-line-before-footer", DefaultSev: SevError},
	{ID: "footer-token-known", DefaultSev: SevError},
	{ID: "footer-format", DefaultSev: SevError},
	{ID: "breaking-needs-description", DefaultSev: SevError},
}

// severity resolves the effective severity for a rule id.
func severity(cfg config.Config, id string) string {
	if s, ok := cfg.Rules[id]; ok {
		switch s {
		case SevError, SevWarning, SevOff:
			return s
		}
	}
	for _, r := range Registry {
		if r.ID == id {
			return r.DefaultSev
		}
	}
	return SevError
}

// ValidateError signals that a message violated error-severity rules. It
// maps to exit code 3 so CI can distinguish "your commits are wrong" from
// "the tool broke".
type ValidateError struct {
	Errors   int
	Warnings int
}

func (e *ValidateError) Error() string {
	return fmt.Sprintf("%d errors, %d warnings", e.Errors, e.Warnings)
}

// ValidationExit maps this error to exit code 3.
func (e *ValidateError) ValidationExit() int { return 3 }

// ValidateResultErr builds the validation-failed error.
func ValidateResultErr(errs, warns int) error {
	return &ValidateError{Errors: errs, Warnings: warns}
}

// Validate returns violations for a message against the config. It is pure:
// no I/O, no git, no terminal. Every violation carries a hint.
func Validate(m commit.CommitMessage, cfg config.Config) []Violation {
	raw := m.Raw
	if raw == "" {
		// Composed message: assemble a raw representation for position work.
		raw = commit.Assemble(m, commit.AssembleOptions{})
	}

	parsed := commit.Parse(raw)
	use := m
	if parsed.OK {
		use = parsed.Message
	} else {
		// Unparsed messages only get type-required (the "wip" case). Raw is
		// never reconstructed.
		var v []Violation
		add(&v, cfg, Violation{
			RuleID:   "type-required",
			Severity: severity(cfg, "type-required"),
			Message:  "no type prefix found",
			Hint:     `expected "<type>[(scope)][!]: <description>"`,
			Line:     1,
			Column:   1,
		})
		return v
	}

	var v []Violation
	// Header-derived positions.
	prefixLen := headerPrefixLen(use)

	// type-required
	if use.Type == "" {
		add(&v, cfg, Violation{RuleID: "type-required", Message: "no type prefix found", Hint: `expected "<type>[(scope)][!]: <description>"`, Line: 1, Column: 1})
	} else if cfg.FindType(use.Type) == nil {
		if cfg.FindType(strings.ToLower(use.Type)) != nil {
			// Known but wrong case — report type-case, not type-known.
			add(&v, cfg, Violation{
				RuleID:  "type-case",
				Message: fmt.Sprintf("type must be lowercase: %q → %q", use.Type, strings.ToLower(use.Type)),
				Hint:    strings.ToLower(use.Type),
				Line:    1, Column: 1,
			})
		} else {
			add(&v, cfg, Violation{
				RuleID:  "type-known",
				Message: fmt.Sprintf("unknown commit type %q", use.Type),
				Hint:    "allowed: " + strings.Join(cfg.TypeNames(), " "),
				Line:    1, Column: 1,
			})
		}
	} else if use.Type != strings.ToLower(use.Type) {
		add(&v, cfg, Violation{
			RuleID:  "type-case",
			Message: fmt.Sprintf("type must be lowercase: %q → %q", use.Type, strings.ToLower(use.Type)),
			Hint:    strings.ToLower(use.Type),
			Line:    1, Column: 1,
		})
	}

	// scope rules
	if use.HasScope() {
		if cfg.Scope.Mode == "list" && len(cfg.Scope.Values) > 0 && !scopeKnown(cfg, use.Scope) {
			suggestion := closest(use.Scope, cfg.ScopeNames())
			msg := fmt.Sprintf("unknown scope %q", use.Scope)
			if suggestion != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", suggestion)
			}
			add(&v, cfg, Violation{
				RuleID:  "scope-known",
				Message: msg,
				Hint:    "allowed: " + strings.Join(cfg.ScopeNames(), " "),
				Line:    1, Column: len(use.Type) + 2,
			})
		}
	} else if cfg.Scope.Required {
		add(&v, cfg, Violation{
			RuleID:  "scope-required",
			Message: "a scope is required in this repository",
			Hint:    "add a scope, e.g. feat(api):",
			Line:    1, Column: prefixLen + 1,
		})
	}

	// subject rules
	if use.Subject == "" {
		add(&v, cfg, Violation{
			RuleID:  "subject-required",
			Message: "subject is empty",
			Hint:    "write a short description after the colon",
			Line:    1, Column: prefixLen + 1,
		})
	} else {
		if len(use.Subject) > cfg.Subject.MaxLength {
			add(&v, cfg, Violation{
				RuleID:  "subject-max-length",
				Message: fmt.Sprintf("subject is %d characters, limit is %d", len(use.Subject), cfg.Subject.MaxLength),
				Hint:    fmt.Sprintf("shorten it, or raise subject.max_length past %d", len(use.Subject)),
				Line:    1, Column: prefixLen + len(use.Subject),
			})
		}
		if len(use.Subject) < cfg.Subject.MinLength {
			add(&v, cfg, Violation{
				RuleID:  "subject-min-length",
				Message: fmt.Sprintf("subject is %d characters, minimum is %d", len(use.Subject), cfg.Subject.MinLength),
				Hint:    "make the description longer",
				Line:    1, Column: prefixLen + len(use.Subject),
			})
		}
		if !subjectCaseOK(cfg.Subject.Case, use.Subject) {
			add(&v, cfg, Violation{
				RuleID:  "subject-case",
				Message: "subject should be " + cfg.Subject.Case + "case",
				Hint:    subjectCaseHint(cfg.Subject.Case, use.Subject),
				Line:    1, Column: prefixLen + 1,
			})
		}
		if cfg.Subject.ForbidTrailingPeriod && strings.HasSuffix(use.Subject, ".") {
			add(&v, cfg, Violation{
				RuleID:  "subject-no-trailing-period",
				Message: "subject should not end with a period",
				Hint:    "drop the trailing period",
				Line:    1, Column: prefixLen + len(use.Subject),
			})
		}
	}

	// subject-format: missing space after the colon.
	if !headerFormatOK(raw) {
		add(&v, cfg, Violation{
			RuleID:  "subject-format",
			Message: "missing space after the colon",
			Hint:    `use "<type>[(scope)]: <subject>" with a space`,
			Line:    1, Column: prefixLen,
		})
	}

	// blank line before body
	if use.HasBody() && !blankLineBefore(raw, bodyStart(raw)) {
		add(&v, cfg, Violation{
			RuleID:  "blank-line-before-body",
			Message: "a blank line must separate the header from the body",
			Hint:    "insert a blank line after the subject line",
			Line:    1, Column: prefixLen + len(use.Subject) + 1,
		})
	}

	// footers
	footerPos := map[string]int{} // token → line (1-based) in raw
	lines := strings.Split(raw, "\n")
	for i, ln := range lines {
		token, _, _ := splitFooterLine(ln)
		if token != "" {
			footerPos[token] = i + 1
		}
	}
	if len(use.Footers) > 0 {
		if !blankLineBefore(raw, footerStart(raw, lines)) {
			add(&v, cfg, Violation{
				RuleID:  "blank-line-before-footer",
				Message: "a blank line must separate the body from the footers",
				Hint:    "insert a blank line before the first footer",
				Line:    footerStart(raw, lines), Column: 1,
			})
		}
		for _, f := range use.Footers {
			if !footerKnown(cfg, f.Token) {
				add(&v, cfg, Violation{
					RuleID:  "footer-token-known",
					Message: fmt.Sprintf("unknown footer token %q", f.Token),
					Hint:    "known tokens: " + strings.Join(footerTokens(cfg), ", "),
					Line:    footerPos[f.Token], Column: 1,
				})
			}
			if f.Value == "" {
				add(&v, cfg, Violation{
					RuleID:  "footer-format",
					Message: fmt.Sprintf("footer %q has no value", f.Token),
					Hint:    `use "<token>: <value>" or "<token> #<value>"`,
					Line:    footerPos[f.Token], Column: 1,
				})
			}
		}
	}

	// breaking-needs-description
	if use.Breaking && cfg.Footers.BreakingNeedsDescription {
		has := false
		for _, f := range use.Footers {
			if strings.EqualFold(f.Token, "BREAKING CHANGE") || strings.EqualFold(f.Token, "BREAKING-CHANGE") {
				has = true
			}
		}
		if !has {
			add(&v, cfg, Violation{
				RuleID:  "breaking-needs-description",
				Message: `"!" requires a BREAKING CHANGE: footer describing the break`,
				Hint:    `add "BREAKING CHANGE: <what breaks>" below the body`,
				Line:    1, Column: prefixLen - 1,
			})
		}
	}

	sort.SliceStable(v, func(i, j int) bool {
		if v[i].Line != v[j].Line {
			return v[i].Line < v[j].Line
		}
		return v[i].Column < v[j].Column
	})
	return v
}

// add appends a violation if its effective severity is not off.
func add(v *[]Violation, cfg config.Config, vi Violation) {
	sev := severity(cfg, vi.RuleID)
	if sev == SevOff {
		return
	}
	vi.Severity = sev
	if vi.Hint == "" {
		vi.Hint = ruleHint(vi.RuleID)
	}
	*v = append(*v, vi)
}

func ruleHint(id string) string {
	return id
}

func headerPrefixLen(m commit.CommitMessage) int {
	n := len(m.Type)
	if m.HasScope() {
		n += len(m.Scope) + 2
	}
	if m.Breaking {
		n++
	}
	return n + 2
}

func scopeKnown(cfg config.Config, scope string) bool {
	for _, s := range cfg.Scope.Values {
		if s.Name == scope {
			return true
		}
	}
	return false
}

func footerKnown(cfg config.Config, token string) bool {
	if strings.EqualFold(token, "BREAKING CHANGE") || strings.EqualFold(token, "BREAKING-CHANGE") {
		return true
	}
	for _, k := range cfg.Footers.Keys {
		if k.Token == token {
			return true
		}
	}
	return false
}

func footerTokens(cfg config.Config) []string {
	var out []string
	for _, k := range cfg.Footers.Keys {
		out = append(out, k.Token)
	}
	return out
}

func subjectCaseOK(caseRule, subject string) bool {
	switch caseRule {
	case "lower":
		return subject == strings.ToLower(subject)
	case "sentence":
		if subject == "" {
			return true
		}
		first := subject[:1]
		return first == strings.ToUpper(first) && first != strings.ToLower(first)
	default:
		return true
	}
}

func subjectCaseHint(caseRule, subject string) string {
	switch caseRule {
	case "lower":
		return strings.ToLower(subject)
	case "sentence":
		if subject == "" {
			return ""
		}
		return strings.ToUpper(subject[:1]) + subject[1:]
	}
	return ""
}

var headerFormatRe = regexp.MustCompile(`^[a-z0-9-]+(\([^)]*\))?(!)?: `)

// headerFormatOK reports whether the raw header has a space after the colon.
func headerFormatOK(raw string) bool {
	first := raw
	if i := strings.IndexByte(raw, '\n'); i >= 0 {
		first = raw[:i]
	}
	return headerFormatRe.MatchString(first)
}

func splitFooterLine(line string) (token, value string, hashes bool) {
	if idx := strings.Index(line, " #"); idx > 0 {
		return line[:idx], strings.TrimSpace(line[idx+2:]), true
	}
	if idx := strings.Index(line, ": "); idx > 0 {
		return line[:idx], strings.TrimSpace(line[idx+2:]), false
	}
	return "", "", false
}

// bodyStart returns the 1-based line where the body begins, or 0.
func bodyStart(raw string) int {
	lines := strings.Split(raw, "\n")
	// after header (line 1), skip blank lines, first non-blank is body.
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			return i + 1
		}
	}
	return 0
}

// footerStart returns the 1-based line where the footer block begins.
func footerStart(raw string, lines []string) int {
	// find the last blank-separated block that contains footer-shaped lines.
	start := 0
	for i := 0; i < len(lines); i++ {
		token, _, _ := splitFooterLine(strings.TrimSpace(lines[i]))
		if token != "" {
			// back up to line after previous blank
			j := i
			for j > 0 && strings.TrimSpace(lines[j-1]) != "" {
				j--
			}
			start = j + 1
		}
	}
	return start
}

// blankLineBefore reports whether a blank line precedes the given 1-based line.
func blankLineBefore(raw string, line int) bool {
	if line <= 1 {
		return false
	}
	lines := strings.Split(raw, "\n")
	if line-2 < 0 || line-2 >= len(lines) {
		return false
	}
	return strings.TrimSpace(lines[line-2]) == ""
}

// closest returns the nearest known string to s, or "".
func closest(s string, known []string) string {
	best := ""
	bestDist := 1 << 30
	for _, k := range known {
		d := editDistance(strings.ToLower(s), strings.ToLower(k))
		if d < bestDist && d <= 2 {
			bestDist = d
			best = k
		}
	}
	return best
}

func editDistance(a, b string) int {
	la, lb := len(a), len(b)
	dp := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		dp[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := dp[0]
		dp[0] = i
		for j := 1; j <= lb; j++ {
			tmp := dp[j]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[j] = min3(dp[j]+1, dp[j-1]+1, prev+cost)
			prev = tmp
		}
	}
	return dp[lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
