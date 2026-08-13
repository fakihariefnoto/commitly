package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/changelog"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
)

var changelogFlags struct {
	since         string
	until         string
	version       string
	write         bool
	output        string
	noLinks       bool
	repoURL       string
	includeMerges bool
	allTypes      bool
}

var changelogCmd = &cobra.Command{
	Use:   "changelog",
	Short: "Build a markdown changelog from conventional commits",
	Long: `Build a markdown changelog from conventional commits.

Reads git history, groups commits by type, and renders release notes.
Breaking changes are collected across all types and listed first, because
that's what a reader is scanning for.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runChangelog(cmd.Context())
	},
}

func init() {
	f := changelogCmd.Flags()
	f.StringVar(&changelogFlags.since, "since", "", "start ref, exclusive (default: most recent tag)")
	f.StringVar(&changelogFlags.until, "until", "HEAD", "end ref, inclusive")
	f.StringVar(&changelogFlags.version, "version", "Unreleased", "heading for this release")
	f.BoolVarP(&changelogFlags.write, "write", "w", false, "prepend to CHANGELOG.md")
	f.StringVarP(&changelogFlags.output, "output", "o", "", "write to a file instead of stdout")
	f.BoolVar(&changelogFlags.noLinks, "no-links", false, "don't link commit hashes")
	f.StringVar(&changelogFlags.repoURL, "repo-url", "", "base URL for links (default: from origin)")
	f.BoolVar(&changelogFlags.includeMerges, "include-merges", false, "include merge commits")
	f.BoolVar(&changelogFlags.allTypes, "all-types", false, "include types hidden by changelog: false")
}

func runChangelog(ctx context.Context) error {
	if !git.IsInsideWorkTree(ctx) {
		return render.Fail("changelog needs a git repository (or any parent up to /)",
			"Run this inside a repository.")
	}
	if err := git.CheckVersion(ctx); err != nil {
		return render.Fail(err.Error())
	}
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}

	since := changelogFlags.since
	if since == "" {
		since = git.MostRecentTag(ctx)
		if since == "" {
			render.Note("▲ No tags found — using the full history.")
			render.Note("")
			render.Note("Narrow it with --since <ref> if that's not what you want.")
		}
	} else if !git.RefExists(ctx, since) {
		tags := git.AvailableTags(ctx)
		msg := fmt.Sprintf("unknown revision %q", since)
		if len(tags) > 0 {
			msg += "\n\nAvailable tags:\n  " + strings.Join(tags, "  ")
		}
		return render.Fail(msg)
	}

	logs, err := git.Log(ctx, since, changelogFlags.until, changelogFlags.includeMerges)
	if err != nil {
		return render.Fail("could not read git history", err.Error())
	}
	if len(logs) == 0 {
		render.Note("No commits in %s..%s.", sinceOrAll(since), changelogFlags.until)
		render.Note("")
		render.Note("Nothing has landed since the last tag. If that's unexpected, check the range:")
		render.Note("  git log --oneline %s..%s", sinceOrAll(since), changelogFlags.until)
		return nil
	}

	repoURL := changelogFlags.repoURL
	if repoURL == "" && cfg.Changelog.RepoURL != "" {
		repoURL = cfg.Changelog.RepoURL
	}
	if repoURL == "" {
		repoURL = git.RemoteURL(ctx)
		repoURL = strings.TrimSuffix(repoURL, ".git")
	}

	opts := changelog.Options{
		Since:              since,
		Until:              changelogFlags.until,
		Version:            changelogFlags.version,
		IncludeMerges:      changelogFlags.includeMerges,
		AllTypes:           changelogFlags.allTypes,
		LinkCommits:        !changelogFlags.noLinks,
		RepoURL:            repoURL,
		GroupBreakingFirst: cfg.Changelog.GroupBreakingFirst,
	}
	cl := changelog.Build(logs, *cfg, opts)

	if globals.json {
		return changelogJSON(cl)
	}

	md := cl.Render(opts)
	if len(cl.Groups) == 0 && len(cl.Breaking) == 0 {
		render.Note("▲ %d commits in the range, none of them conventional.", cl.Total)
		render.Note("")
		render.Note("Nothing to render. This usually means the range predates adopting the")
		render.Note("convention — start from where you adopted it:")
		render.Note("  commitly changelog --since <the-tag-or-sha-where-you-started>")
		return nil
	}

	// Summary to stderr; markdown to stdout (or file).
	summaryToStderr(cl, since)

	if changelogFlags.write {
		return writeChangelog(cl, md)
	}
	if changelogFlags.output != "" {
		if err := os.WriteFile(changelogFlags.output, []byte(md), 0o644); err != nil {
			return render.Fail("cannot write to "+changelogFlags.output, err.Error())
		}
		render.Note("✓ Wrote %d entries to %s", cl.Conventional, changelogFlags.output)
		return nil
	}
	render.Result("%s", md)
	return nil
}

func sinceOrAll(since string) string {
	if since == "" {
		return "full history"
	}
	return since
}

func summaryToStderr(cl *changelog.Changelog, since string) {
	rangeStr := sinceOrAll(since) + ".." + cl.Until
	render.Note("%d commits in %s — %d conventional, %d skipped.", cl.Total, rangeStr, cl.Conventional, cl.Skipped)
	if len(cl.Unparsed) > 0 {
		render.Note("  %d not conventional:", len(cl.Unparsed))
		for _, u := range cl.Unparsed {
			render.Note("    %s  %s", u.SHA, u.Subject)
		}
	}
	if cl.Total > 0 && !changelogFlags.includeMerges {
		render.Note("")
		render.Note("  Include merges with --include-merges.")
	}
}

func writeChangelog(cl *changelog.Changelog, md string) error {
	const path = "CHANGELOG.md"
	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return render.Fail("cannot read "+path, err.Error())
		}
		if err := os.WriteFile(path, []byte(md), 0o644); err != nil {
			return render.Fail("cannot write to "+path, err.Error())
		}
		render.Note("✓ Created CHANGELOG.md with %d entries under \"## %s\"", cl.Conventional, cl.Version)
		return nil
	}
	// Prepend beneath the title and any preamble, above the previous release.
	var out strings.Builder
	lines := strings.Split(string(existing), "\n")
	inserted := false
	for i, ln := range lines {
		if strings.HasPrefix(ln, "## ") {
			out.WriteString(md)
			out.WriteString("\n")
			inserted = true
		}
		out.WriteString(ln)
		if i < len(lines)-1 {
			out.WriteByte('\n')
		}
	}
	if !inserted {
		out.WriteString("\n")
		out.WriteString(md)
	}
	if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
		return render.Fail("cannot write to "+path, err.Error())
	}
	render.Note("✓ Prepended %d entries to CHANGELOG.md under \"## %s\"", cl.Conventional, cl.Version)
	render.Note("")
	render.Note("  Existing content preserved below. Review before committing:")
	render.Note("    git diff CHANGELOG.md")
	return nil
}

func changelogJSON(cl *changelog.Changelog) error {
	type cJSON struct {
		SHA      string `json:"sha"`
		Scope    string `json:"scope"`
		Subject  string `json:"subject"`
		Breaking bool   `json:"breaking"`
	}
	type gJSON struct {
		Type    string  `json:"type"`
		Heading string  `json:"heading"`
		Commits []cJSON `json:"commits"`
	}
	payload := map[string]any{
		"version":      cl.Version,
		"since":        cl.Since,
		"until":        cl.Until,
		"total":        cl.Total,
		"conventional": cl.Conventional,
		"skipped":      cl.Skipped,
		"breaking":     cl.Breaking,
	}
	var groups []gJSON
	for _, g := range cl.Groups {
		gg := gJSON{Type: g.Type, Heading: g.Heading}
		for _, c := range g.Commits {
			gg.Commits = append(gg.Commits, cJSON{c.SHA, c.Scope, c.Subject, c.Breaking})
		}
		groups = append(groups, gg)
	}
	payload["groups"] = groups
	payload["unparsed"] = cl.Unparsed
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}
