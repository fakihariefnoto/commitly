// Package changelog renders markdown release notes from conventional commit
// history. It reads repository truth (git log), not the local history store
// — a release's changelog must cover every contributor's commits.
package changelog

import (
	"fmt"
	"sort"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
)

// Group is commits of one type rendered under one heading.
type Group struct {
	Type    string
	Heading string
	Commits []CommitRef
}

// CommitRef is one conventional commit in a group.
type CommitRef struct {
	SHA      string
	Scope    string
	Subject  string
	Breaking bool
}

// Breaking is a collected breaking change across all types.
type Breaking struct {
	Scope       string
	Description string
	SHA         string
}

// Changelog is the rendered model.
type Changelog struct {
	Version      string
	Since        string
	Until        string
	Total        int
	Conventional int
	Skipped      int
	Breaking     []Breaking
	Groups       []Group
	Unparsed     []Unparsed
}

// Unparsed is a non-conventional commit, counted and named — never dropped.
type Unparsed struct {
	SHA     string
	Subject string
}

// Options configures generation.
type Options struct {
	Since              string
	Until              string
	Version            string
	IncludeMerges      bool
	AllTypes           bool
	LinkCommits        bool
	RepoURL            string
	GroupBreakingFirst bool
}

// Build parses git log records into a Changelog.
func Build(logs []git.CommitLog, cfg config.Config, opts Options) *Changelog {
	c := &Changelog{
		Version: opts.Version,
		Since:   opts.Since,
		Until:   opts.Until,
	}

	groups := map[string]*Group{}
	order := []string{}

	for _, l := range logs {
		c.Total++
		if l.IsMerge && !opts.IncludeMerges {
			c.Skipped++
			c.Unparsed = append(c.Unparsed, Unparsed{SHA: l.ShortSHA, Subject: "merge commit"})
			continue
		}
		res := commit.Parse(l.RawMessage)
		if !res.OK {
			c.Skipped++
			c.Unparsed = append(c.Unparsed, Unparsed{SHA: l.ShortSHA, Subject: firstLine(l.RawMessage)})
			continue
		}
		m := res.Message
		ct := cfg.FindType(m.Type)
		if ct == nil {
			c.Skipped++
			c.Unparsed = append(c.Unparsed, Unparsed{SHA: l.ShortSHA, Subject: firstLine(l.RawMessage)})
			continue
		}
		if ct.ChangelogHidden && !opts.AllTypes {
			c.Skipped++
			c.Unparsed = append(c.Unparsed, Unparsed{SHA: l.ShortSHA, Subject: firstLine(l.RawMessage)})
			continue
		}
		c.Conventional++

		ref := CommitRef{SHA: l.ShortSHA, Scope: m.Scope, Subject: m.Subject, Breaking: m.Breaking}
		if m.Breaking {
			desc := m.Subject
			for _, f := range m.Footers {
				if strings.EqualFold(f.Token, "BREAKING CHANGE") && f.Value != "" {
					desc = f.Value
				}
			}
			c.Breaking = append(c.Breaking, Breaking{Scope: m.Scope, Description: desc, SHA: l.ShortSHA})
		}

		g, ok := groups[m.Type]
		if !ok {
			heading := ct.Changelog
			if heading == "" {
				heading = Titleize(m.Type)
			}
			g = &Group{Type: m.Type, Heading: heading}
			groups[m.Type] = g
			order = append(order, m.Type)
		}
		g.Commits = append(g.Commits, ref)
	}

	sort.SliceStable(order, func(i, j int) bool {
		return typeRank(groups[order[i]].Type) < typeRank(groups[order[j]].Type)
	})
	for _, t := range order {
		c.Groups = append(c.Groups, *groups[t])
	}
	return c
}

// typeRank orders feat/fix first, then the config declaration order.
func typeRank(t string) int {
	switch t {
	case "feat":
		return 0
	case "fix":
		return 1
	}
	return 2
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// Titleize upper-cases the first letter.
func Titleize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Render produces the markdown.
func (c *Changelog) Render(opts Options) string {
	var sb strings.Builder

	if c.Version == "" {
		c.Version = "Unreleased"
	}
	sb.WriteString("## " + c.Version + "\n")

	if opts.GroupBreakingFirst && len(c.Breaking) > 0 {
		sb.WriteString("\n### ⚠ BREAKING CHANGES\n\n")
		for _, b := range c.Breaking {
			sb.WriteString("* ")
			if b.Scope != "" {
				sb.WriteString("**" + b.Scope + ":** ")
			}
			sb.WriteString(b.Description)
			if opts.LinkCommits && opts.RepoURL != "" {
				sb.WriteString(fmt.Sprintf(" ([%s](%s/commit/%s))", b.SHA, opts.RepoURL, b.SHA))
			} else if opts.LinkCommits {
				sb.WriteString(" (" + b.SHA + ")")
			}
			sb.WriteString("\n")
		}
	}

	for _, g := range c.Groups {
		sb.WriteString("\n### " + g.Heading + "\n\n")
		for _, r := range g.Commits {
			sb.WriteString("* ")
			if r.Scope != "" {
				sb.WriteString("**" + r.Scope + ":** ")
			}
			sb.WriteString(r.Subject)
			if opts.LinkCommits && opts.RepoURL != "" {
				sb.WriteString(fmt.Sprintf(" ([%s](%s/commit/%s))", r.SHA, opts.RepoURL, r.SHA))
			} else if opts.LinkCommits {
				sb.WriteString(" (" + r.SHA + ")")
			}
			sb.WriteString("\n")
		}
	}

	return sb.String()
}
