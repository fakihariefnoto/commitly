package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	ccommit "github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/lint"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
)

var (
	lintFile    string
	lintCommit  string
	lintRange   string
	lintHook    bool
	lintMaxWarn int
)

var lintCmd = &cobra.Command{
	Use:   "lint [<message>]",
	Short: "Check commit messages against the convention",
	Long: `Check commit messages against the Conventional Commits specification and
this repository's .commitly.yaml.

Reads the message from an argument, a file, stdin, a commit, or a range.
Exits 3 when a message violates a rule, so CI can branch on it.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		caps := render.Detect()
		a := &app{ctx: cmd.Context(), caps: caps}
		srcCount := 0
		for _, s := range []string{lintFile, lintCommit, lintRange} {
			if s != "" {
				srcCount++
			}
		}
		if srcCount > 1 {
			return render.Usage("choose one source: --file, --commit, or --range")
		}
		if len(args) == 1 && srcCount > 0 {
			return render.Usage("a message argument can't be combined with --file/--commit/--range")
		}

		cfg, err := a.loadConfig()
		if err != nil {
			return err
		}

		switch {
		case lintRange != "":
			return lintRevisionRange(cmd.Context(), cfg, lintRange)
		case lintCommit != "":
			return lintOneCommit(cmd.Context(), cfg, lintCommit)
		default:
			var raw string
			switch {
			case len(args) == 1:
				raw = args[0]
			case lintFile != "" && lintFile != "-":
				data, err := os.ReadFile(lintFile)
				if err != nil {
					return render.Fail("cannot read "+lintFile, err.Error())
				}
				raw = string(data)
			default:
				data, err := io.ReadAll(os.Stdin)
				if err != nil {
					return render.Fail("cannot read stdin", err.Error())
				}
				raw = string(data)
			}
			return lintMessage(cmd.Context(), cfg, raw, "", lintHook)
		}
	},
}

func init() {
	lintCmd.Flags().StringVarP(&lintFile, "file", "f", "", "read the message from a file (\"-\" for stdin)")
	lintCmd.Flags().StringVarP(&lintCommit, "commit", "c", "", "lint an existing commit")
	lintCmd.Flags().StringVarP(&lintRange, "range", "r", "", "lint every commit in a range (main..HEAD)")
	lintCmd.Flags().BoolVar(&lintHook, "hook", false, "terse output for use in a commit-msg hook")
	lintCmd.Flags().IntVar(&lintMaxWarn, "max-warnings", -1, "fail if warnings exceed this count")
}

// lintMessage validates one message and, in hook mode, counts it.
func lintMessage(ctx context.Context, cfg *config.Config, raw, shortSHA string, isHook bool) error {
	parsed := ccommit.Parse(raw)
	violations := lint.Validate(parsed.Message, *cfg)

	errs, warns := 0, 0
	for _, v := range violations {
		if v.Severity == lint.SevError {
			errs++
		} else {
			warns++
		}
	}

	// Hook counting (AD-14): append a counter whether it passed or failed.
	if isHook && cfg.Stats.CountFromHook && cfg.Stats.Enabled {
		countHook(ctx, cfg, parsed, violations)
	}

	if globals.json {
		return lintJSON(parsed, violations, errs, warns)
	}
	if !globals.quiet {
		printLintHuman(parsed, violations, errs, warns, isHook)
	}

	if errs > 0 {
		return lint.ValidateResultErr(errs, warns)
	}
	if lintMaxWarn >= 0 && warns > lintMaxWarn {
		return lint.ValidateResultErr(0, warns)
	}
	return nil
}

func countHook(ctx context.Context, cfg *config.Config, parsed ccommit.ParseResult, violations []lint.Violation) {
	root, err := git.Root(ctx)
	if err != nil {
		return
	}
	repoKey := git.RepoKey(ctx, root)
	conforming := parsed.OK && len(violations) == 0
	row := &history.CounterRow{
		Date:     time.Now().Format("2006-01-02"),
		RepoKey:  repoKey,
		Source:   history.SrcHook,
		Commits:  1,
		Own:      1,
		Breaking: b2i(parsed.Message.Breaking),
		WithBody: b2i(parsed.Message.HasBody()),
	}
	if conforming {
		row.Conforming = 1
	} else {
		row.NonConforming = 1
	}
	if parsed.OK {
		row.Type = parsed.Message.Type
		row.SubjectLenSum = len(parsed.Message.Subject)
		row.SubjectHist = make([]int, 20)
		row.SubjectHist[histIndex(len(parsed.Message.Subject))] = 1
	}
	st := history.OpenCounterStore(config.StateDir(), cfg.Stats.RetentionDays, cfg.Stats.CompactThreshold)
	_ = st.Append(row) // best-effort (AD-11)
}

func histIndex(length int) int {
	if length <= 0 {
		return 0
	}
	b := length / 10
	if b >= 20 {
		return 19
	}
	return b
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// lintRevisionRange lints every commit in a range.
func lintRevisionRange(ctx context.Context, cfg *config.Config, rangeSpec string) error {
	if !git.IsInsideWorkTree(ctx) {
		return render.Fail("--range needs a git repository (or any parent up to /)",
			"To lint a message without a repository, pass it directly:",
			`  commitly lint "feat: your message"`)
	}
	parts := strings.SplitN(rangeSpec, "..", 2)
	since, until := "", "HEAD"
	if len(parts) == 2 {
		since, until = parts[0], parts[1]
	} else {
		since = parts[0] + "^"
	}
	logs, err := git.Log(ctx, since, until, false)
	if err != nil {
		return render.Fail("could not read the range "+rangeSpec, err.Error())
	}
	if len(logs) == 0 {
		render.Note("No commits in %s — nothing to lint.", rangeSpec)
		return nil
	}

	passed, failed := 0, 0
	render.Note("Linting %d commits in %s", len(logs), rangeSpec)
	for _, l := range logs {
		parsed := ccommit.Parse(l.RawMessage)
		v := lint.Validate(parsed.Message, *cfg)
		errs := countErrs(v)
		if errs > 0 {
			failed++
			render.Note("  ✗ %s  %s", l.ShortSHA, firstSubjectLine(l.RawMessage))
			for _, vi := range v {
				if vi.Severity == lint.SevError {
					render.Note("     ✗ %-20s %s (line %d, col %d)", vi.RuleID, vi.Message, vi.Line, vi.Column)
				}
			}
		} else {
			passed++
			render.Note("  ✓ %s  %s", l.ShortSHA, firstSubjectLine(l.RawMessage))
		}
	}
	render.Note("")
	render.Note("%d passed, %d failed.", passed, failed)
	if failed > 0 {
		return lint.ValidateResultErr(failed, 0)
	}
	return nil
}

// lintOneCommit lints a single existing commit.
func lintOneCommit(ctx context.Context, cfg *config.Config, rev string) error {
	out, _, err := git.Run(ctx, "log", "-1", "--format=%B", rev)
	if err != nil {
		return render.Fail(fmt.Sprintf("unknown revision %q", rev),
			"Check that the ref exists:",
			"  git log --oneline -10")
	}
	return lintMessage(ctx, cfg, out, rev, false)
}

func countErrs(v []lint.Violation) int {
	n := 0
	for _, vi := range v {
		if vi.Severity == lint.SevError {
			n++
		}
	}
	return n
}

func firstSubjectLine(raw string) string {
	if i := strings.IndexByte(raw, '\n'); i >= 0 {
		return raw[:i]
	}
	return raw
}

func lintJSON(parsed ccommit.ParseResult, violations []lint.Violation, errs, warns int) error {
	type vJSON struct {
		Rule     string `json:"rule"`
		Severity string `json:"severity"`
		Message  string `json:"message"`
		Hint     string `json:"hint"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	vs := make([]vJSON, 0, len(violations))
	for _, vi := range violations {
		vs = append(vs, vJSON{vi.RuleID, vi.Severity, vi.Message, vi.Hint, vi.Line, vi.Column})
	}
	payload := map[string]any{
		"source":     "message",
		"passed":     errs == 0,
		"errors":     errs,
		"warnings":   warns,
		"raw":        parsed.Message.Raw,
		"valid":      errs == 0,
		"violations": vs,
	}
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}

func printLintHuman(parsed ccommit.ParseResult, violations []lint.Violation, errs, warns int, isHook bool) {
	if errs == 0 && warns == 0 {
		if globals.quiet {
			return
		}
		if isHook {
			// hook mode passes silently — git prints nothing on success
			return
		}
		render.Note("✓ %s", strings.TrimSpace(parsed.Message.Raw))
		return
	}

	if isHook {
		// Terse: one line per error + the offending text + the pointer.
		for _, vi := range violations {
			if vi.Severity == lint.SevError {
				render.Note("commitly: %s (line %d, col %d)", vi.Message, vi.Line, vi.Column)
			}
		}
		render.Note("")
		render.Note("  %s", strings.TrimSpace(parsed.Message.Raw))
		render.Note("")
		render.Note("Compose one interactively instead:  git cm")
		return
	}

	render.Note("✗ %s", strings.TrimSpace(parsed.Message.Raw))
	render.Note("")
	for _, vi := range violations {
		mark := "✗"
		if vi.Severity == lint.SevWarning {
			mark = "▲"
		}
		loc := ""
		if vi.Line > 0 {
			loc = fmt.Sprintf(" (line %d", vi.Line)
			if vi.Column > 0 {
				loc += fmt.Sprintf(", col %d", vi.Column)
			}
			loc += ")"
		}
		render.Note("  %s %-24s %s%s", mark, vi.RuleID, vi.Message, loc)
		if vi.Hint != "" && vi.Hint != vi.RuleID {
			render.Note("  %-26s %s", "", vi.Hint)
		}
	}
	render.Note("")
	render.Note("%d errors, %d warnings.", errs, warns)
	render.Note("")
	if errs > 0 {
		render.Note("Compose a compliant message interactively:")
		render.Note("  git cm")
	} else {
		render.Note("Warnings don't fail — promote a rule to `error` in .commitly.yaml")
		render.Note("when the team is ready.")
	}
}
