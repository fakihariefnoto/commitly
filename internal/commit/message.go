// Package commit implements the Commitly message model: parsing and
// assembling Conventional Commits messages. It is pure — no git, no
// terminal, no files — so the whole spec is testable as table tests.
package commit

import "strings"

// Footer is a single commit footer (trailer), keyed by a known token.
type Footer struct {
	Token   string
	Value   string
	Hashes  bool // separator was " #" rather than ": "
	HasBody bool // footer carried a body (BREAKING CHANGE with description)
}

// CommitMessage is the central model of the tool. Subject is stored
// WITHOUT the type/scope prefix, so amend never double-prefixes. Raw is
// populated only when parsing and is echoed verbatim for messages that
// failed to parse — we never invent a reconstruction of something we
// couldn't understand.
type CommitMessage struct {
	Type     string
	Scope    string
	Breaking bool
	Subject  string
	Body     string
	Footers  []Footer
	Raw      string
}

// HasScope reports whether the message carries a scope.
func (m CommitMessage) HasScope() bool { return m.Scope != "" }

// HasBody reports whether the message carries a body.
func (m CommitMessage) HasBody() bool { return strings.TrimSpace(m.Body) != "" }

// ParseResult distinguishes a spec-compliant message from one that is not.
type ParseResult struct {
	Message CommitMessage
	OK      bool
	Reason  string // populated when OK is false
}

// Parse parses raw text into a CommitMessage. A message that is not
// Conventional does NOT error — lint and changelog both need to report on
// such messages rather than fail. Raw is always set on the returned message.
func Parse(raw string) ParseResult {
	raw = normalize(raw)
	m := CommitMessage{Raw: raw}

	if raw == "" {
		return ParseResult{Message: m, OK: false, Reason: "empty message"}
	}

	// Split header from body+footers on the first blank line.
	lines := splitLines(raw)
	header := lines[0]
	rest := lines[1:]
	// Collapse leading blank lines from rest if header was actually empty.
	for len(rest) > 0 && strings.TrimSpace(rest[0]) == "" {
		rest = rest[1:]
	}

	if !parseHeader(header, &m) {
		return ParseResult{Message: m, OK: false, Reason: "not a conventional message"}
	}

	// Remaining paragraphs: body block then footers block.
	body, footers, ok := parseBodyAndFooters(rest)
	m.Body = body
	m.Footers = footers
	if !ok {
		return ParseResult{Message: m, OK: false, Reason: "malformed body or footer block"}
	}

	// A BREAKING CHANGE/CHANGE footer sets Breaking. Both spellings are
	// spec synonyms.
	for _, f := range m.Footers {
		if strings.EqualFold(f.Token, "BREAKING CHANGE") || strings.EqualFold(f.Token, "BREAKING-CHANGE") {
			m.Breaking = true
		}
	}
	return ParseResult{Message: m, OK: true}
}

// parseHeader handles "<type>[(<scope>)][!]: <subject>".
func parseHeader(header string, m *CommitMessage) bool {
	colon := strings.Index(header, ":")
	if colon <= 0 {
		return false
	}
	head := header[:colon]
	subject := strings.TrimSpace(header[colon+1:])

	// Optional ! before the colon.
	if strings.HasSuffix(head, "!") {
		m.Breaking = true
		head = strings.TrimSuffix(head, "!")
	}

	// Optional (scope).
	if strings.HasSuffix(head, ")") {
		open := strings.LastIndex(head, "(")
		if open < 0 {
			return false
		}
		m.Scope = head[open+1 : len(head)-1]
		head = head[:open]
	}

	m.Type = strings.TrimSpace(head)
	m.Subject = subject

	if m.Type == "" {
		return false
	}
	return true
}

// parseBodyAndFooters splits the lines after the header into a body block
// and a footer block. A body may contain multiple paragraphs; the footer
// block is the trailing run of paragraphs that are entirely footer-shaped.
// This distinguishes "a\n\nb" (one two-paragraph body) from
// "a\n\nCloses #12" (body then footer) and "BREAKING CHANGE: x" (footer
// only).
func parseBodyAndFooters(lines []string) (body string, footers []Footer, ok bool) {
	// Group into paragraphs (runs of non-blank lines).
	var paras [][]string
	var cur []string
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			if len(cur) > 0 {
				paras = append(paras, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, ln)
	}
	if len(cur) > 0 {
		paras = append(paras, cur)
	}

	// Find the first paragraph index whose suffix is entirely footer-shaped.
	firstFooter := len(paras)
	for i := 0; i < len(paras); i++ {
		allFooters := true
		for _, ln := range paras[i] {
			if !isFooterLine(ln) {
				allFooters = false
				break
			}
		}
		if allFooters {
			firstFooter = i
			break
		}
	}
	// If a footer section was found, paragraphs after a non-footer paragraph
	// must all be footer-shaped (already guaranteed by firstFooter scan).

	var bodyParts []string
	for _, p := range paras[:firstFooter] {
		bodyParts = append(bodyParts, strings.Join(p, "\n"))
	}
	body = strings.Join(bodyParts, "\n\n")

	for _, p := range paras[firstFooter:] {
		for _, ln := range p {
			token, value, hashes := splitFooter(ln)
			footers = append(footers, Footer{Token: token, Value: value, Hashes: hashes})
		}
	}
	return body, footers, true
}

// isFooterLine reports whether a line looks like "Token: value" or
// "Token #value" or "BREAKING CHANGE: value".
func isFooterLine(line string) bool {
	_, _, _ = splitFooter(line)
	return containsFooterSeparator(line)
}

func containsFooterSeparator(line string) bool {
	if idx := strings.Index(line, ": "); idx > 0 {
		return true
	}
	if idx := strings.Index(line, " #"); idx > 0 {
		return true
	}
	return false
}

func splitFooter(line string) (token, value string, hashes bool) {
	if idx := strings.Index(line, " #"); idx > 0 {
		return line[:idx], strings.TrimSpace(line[idx+2:]), true
	}
	if idx := strings.Index(line, ": "); idx > 0 {
		return line[:idx], strings.TrimSpace(line[idx+2:]), false
	}
	return line, "", false
}

// splitLines splits on \n preserving empty lines.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// normalize normalizes line endings and trims trailing whitespace-only lines.
func normalize(raw string) string {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\r", "\n")
	return strings.TrimRight(raw, "\n \t")
}
