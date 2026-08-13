package commit

import "strings"

// AssembleOptions controls rendering.
type AssembleOptions struct {
	// Emoji to insert per emoji.enabled / emoji.position.
	Emoji        string
	EmojiPrefix  bool // true = before the type, false = after the type
	BodyWrap     int  // soft-wrap column for the body; 0 disables
	BreakingBody bool // emit the BREAKING CHANGE footer when Breaking and a description is supplied
}

// Assemble renders the message per Conventional Commits v1.0.0:
//
//	<type>[(<scope>)][!]: <subject>
//	<blank>
//	<body>
//	<blank>
//	<footer>
//	<footer>
func Assemble(m CommitMessage, opts AssembleOptions) string {
	var sb strings.Builder

	head := m.Type
	if opts.Emoji != "" {
		if opts.EmojiPrefix {
			head = opts.Emoji + " " + head
		} else {
			head = head + " " + opts.Emoji
		}
	}
	if m.HasScope() {
		head += "(" + m.Scope + ")"
	}
	if m.Breaking {
		head += "!"
	}
	head += ": " + m.Subject
	sb.WriteString(head)

	body := m.Body
	if opts.BodyWrap > 0 && body != "" {
		body = wrap(body, opts.BodyWrap)
	}
	if body != "" {
		sb.WriteString("\n\n")
		sb.WriteString(body)
	}

	footers := m.Footers
	for _, f := range footers {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(f.Token)
		if f.Hashes {
			sb.WriteString(" #")
		} else {
			sb.WriteString(": ")
		}
		sb.WriteString(f.Value)
	}

	return sb.String()
}

// wrap soft-wraps text at col, keeping paragraph breaks.
func wrap(text string, col int) string {
	var out []string
	for _, para := range strings.Split(text, "\n") {
		if para == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(para)
		line := ""
		for _, w := range words {
			if line == "" {
				line = w
				continue
			}
			if len(line)+1+len(w) <= col {
				line += " " + w
			} else {
				out = append(out, line)
				line = w
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
