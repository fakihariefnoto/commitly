package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	ccommit "github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/lint"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/fakihariefnoto/commitly/internal/tui"
	"github.com/spf13/cobra"
)

var commitFlags struct {
	all       bool
	allFiles  bool
	typ       string
	scope     string
	message   string
	body      []string
	breaking  bool
	breakDesc string
	footer    []string
	amend     bool
	edit      bool
	dryRun    bool
	noDraft   bool
	signoff   bool
	gpgSign   string
	noVerify  bool
}

var commitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Compose and create a conventional commit",
	Long: `Compose and create a Conventional Commits message, interactively.

Walks you through type, scope, subject, breaking change, body and footers,
showing a live preview of the exact message that will be committed. With -a,
choose which changed files to stage first.

Every prompt has a flag equivalent, so the same command works in a script.
Without a terminal, missing values are an error rather than a hanging prompt.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runCommit(cmd.Context())
	},
}

func init() {
	f := commitCmd.Flags()
	f.BoolVarP(&commitFlags.all, "all", "a", false, "choose which changed files to stage")
	f.BoolVarP(&commitFlags.allFiles, "all-files", "A", false, "stage everything, no picker")
	f.StringVarP(&commitFlags.typ, "type", "t", "", "commit type (feat, fix, docs, ...)")
	f.StringVar(&commitFlags.scope, "scope", "", "scope, e.g. api")
	f.StringVarP(&commitFlags.message, "message", "m", "", "subject line, without the type prefix")
	f.StringArrayVarP(&commitFlags.body, "body", "b", nil, "body paragraph (repeatable)")
	f.BoolVar(&commitFlags.breaking, "breaking", false, "mark as a breaking change")
	f.StringVar(&commitFlags.breakDesc, "breaking-description", "", "text for the BREAKING CHANGE footer")
	f.StringArrayVar(&commitFlags.footer, "footer", nil, "footer as \"Token: value\" (repeatable)")
	f.BoolVar(&commitFlags.amend, "amend", false, "amend the previous commit")
	f.BoolVarP(&commitFlags.edit, "edit", "e", false, "write the body in $EDITOR")
	f.BoolVar(&commitFlags.dryRun, "dry-run", false, "print the message and exit, don't commit")
	f.BoolVar(&commitFlags.noDraft, "no-draft", false, "don't offer to restore a saved draft")
	f.BoolVarP(&commitFlags.signoff, "signoff", "s", false, "add a Signed-off-by trailer")
	f.StringVarP(&commitFlags.gpgSign, "gpg-sign", "S", "", "GPG-sign the commit (optional keyid)")
	f.BoolVar(&commitFlags.noVerify, "no-verify", false, "skip git's commit hooks")
}

func runCommit(ctx context.Context) error {
	caps := render.Detect()

	// Minimum git version (TRD I1).
	if err := git.CheckVersion(ctx); err != nil {
		return render.Fail(err.Error())
	}
	root, err := git.Root(ctx)
	if err != nil {
		return render.Fail("not a git repository (or any parent up to /)",
			"Run this inside a repository, or create one:",
			"  git init")
	}
	cfg, err := (&app{ctx: ctx, caps: caps}).loadConfig()
	if err != nil {
		return err
	}

	// Draft restore (offered, never automatic).
	if !commitFlags.noDraft && !commitFlags.amend {
		if err := offerDraft(ctx, root); err != nil {
			return err
		}
	}

	// Determine which files to stage.
	var filesToStage []string
	hadStaged := false
	if commitFlags.all || commitFlags.allFiles {
		changes, err := git.Status(ctx)
		if err != nil {
			return render.Fail("could not read git status", err.Error())
		}
		if commitFlags.all {
			selected, err := pickFiles(ctx, changes, caps)
			if err != nil {
				return err
			}
			filesToStage = selected
		} else {
			for _, ch := range changes {
				filesToStage = append(filesToStage, ch.Path)
			}
		}
		hadStaged = priorStaged(changes)
	} else if !commitFlags.amend && !commitFlags.dryRun {
		// Nothing staged? Offer the picker first when there are changes.
		changes, err := git.Status(ctx)
		if err != nil {
			return render.Fail("could not read git status", err.Error())
		}
		hasStaged := false
		hasChanges := false
		for _, ch := range changes {
			if ch.IsStaged() {
				hasStaged = true
			}
			if ch.IsStaged() || ch.IsUnstaged() || ch.Untracked {
				hasChanges = true
			}
		}
		if !hasStaged {
			if hasChanges && interactiveAllowed(caps) {
				// Step 1: pick what to stage, then compose.
				selected, err := pickFiles(ctx, changes, caps)
				if err != nil {
					return err
				}
				filesToStage = selected
			} else if !hasChanges {
				return render.Fail("nothing to commit",
					"Working tree is clean. Make a change first, then run `git cm`.")
			} else {
				return render.Fail("nothing staged",
					"Run `git cm -a` to choose files, or stage some first:",
					"  git add <file>")
			}
		}
	}

	// Compose the message.
	msg, err := composeMessage(ctx, cfg, caps, filesToStage, hadStaged)
	if err != nil {
		if render.KindOf(err) == render.ExitAborted {
			render.Note("Aborted. Nothing was staged and no commit was made.")
			// Only save a draft worth restoring — an abort before any real
			// fields were entered (no type or no subject) saves nothing.
			if !commitFlags.amend && !commitFlags.dryRun && msg.Type != "" && msg.Subject != "" {
				assembled := ccommit.Assemble(msg, ccommit.AssembleOptions{BodyWrap: cfg.Body.Wrap})
				saveDraft(ctx, root, assembled, "aborted")
				render.Note("Draft saved — re-run `git cm` to restore it.")
			}
		}
		return err
	}

	// Validate before touching git (AD-3).
	validate := lint.Validate(msg, *cfg)
	for _, vi := range validate {
		if vi.Severity == lint.SevError {
			return render.Fail(fmt.Sprintf("%s: %s", vi.RuleID, vi.Message),
				vi.Hint,
				"Run `git cm` to compose a compliant message.")
		}
	}

	// Assemble.
	opts := ccommit.AssembleOptions{
		BodyWrap:     cfg.Body.Wrap,
		BreakingBody: cfg.Footers.BreakingNeedsDescription,
	}
	if cfg.Emoji.Enabled {
		if ct := cfg.FindType(msg.Type); ct != nil {
			opts.Emoji = ct.Emoji
			opts.EmojiPrefix = cfg.Emoji.Position == "prefix"
		}
	}
	assembled := ccommit.Assemble(msg, opts)

	if commitFlags.dryRun {
		render.Result("%s", assembled)
		return nil
	}

	// Stage (AD-5): git add runs once, here, just before commit.
	if len(filesToStage) > 0 {
		if err := git.Add(ctx, filesToStage); err != nil {
			return render.Fail(err.Error())
		}
	}

	// git commit args passthrough.
	commitArgs := []string{}
	if commitFlags.amend {
		commitArgs = append(commitArgs, "--amend")
	}
	if commitFlags.noVerify {
		commitArgs = append(commitArgs, "--no-verify")
	}
	if commitFlags.signoff {
		commitArgs = append(commitArgs, "--signoff")
	}
	if commitFlags.gpgSign != "" {
		commitArgs = append(commitArgs, "-S"+commitFlags.gpgSign)
	}

	res, err := git.Commit(ctx, assembled, commitArgs...)
	if err != nil {
		var ce *git.CommitError
		if errors.As(err, &ce) {
			if ce.Stderr != "" {
				render.Note("%s", ce.Stderr)
			}
			if ce.Stdout != "" {
				render.Note("%s", ce.Stdout)
			}
			if strings.Contains(ce.Stderr, "commit-msg") || strings.Contains(ce.Stderr, "commitly") {
				saveDraft(ctx, root, assembled, "hook-rejected")
				render.Note("Commit rejected by the commit-msg hook. Your message was saved as a draft —")
				render.Note("re-run `git cm` to restore it.")
			}
		}
		return render.Fail("git commit failed", "Check the message above; fix and retry.")
	}

	// Record history (AD-11): after success, best-effort.
	recordEntry(ctx, cfg, root, res, msg, filesToStage)

	if globals.json {
		payload := map[string]any{
			"sha":           res.SHA,
			"short_sha":     res.ShortSHA,
			"branch":        res.Branch,
			"type":          msg.Type,
			"scope":         msg.Scope,
			"breaking":      msg.Breaking,
			"subject":       msg.Subject,
			"message":       assembled,
			"files_changed": res.FilesChanged,
			"insertions":    res.Insertions,
			"deletions":     res.Deletions,
			"committed_at":  time.Now().Format(time.RFC3339),
		}
		b, _ := json.Marshal(payload)
		render.Result("%s", string(b))
		return nil
	}

	render.Result("✓ [%s %s] %s", res.Branch, res.ShortSHA, firstSubject(assembled))
	render.Result("  %d files changed, %d insertions(+), %d deletions(-)",
		res.FilesChanged, res.Insertions, res.Deletions)
	return nil
}

func firstSubject(msg string) string {
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		return msg[:i]
	}
	return msg
}

func priorStaged(changes []git.FileChange) bool {
	for _, ch := range changes {
		if ch.IsStaged() {
			return true
		}
	}
	return false
}

// composeMessage gathers the message fields from flags or the interactive
// TUI. The form runs whenever a terminal is present and type+subject aren't
// already given — every prompt has a flag equivalent (G6), so the same
// command works in a script.
func composeMessage(ctx context.Context, cfg *config.Config, caps *render.Caps, filesToStage []string, hadStaged bool) (ccommit.CommitMessage, error) {
	interactive := interactiveAllowed(caps)
	hasAll := commitFlags.typ != "" && commitFlags.message != ""
	if interactive && !hasAll {
		return composeInteractive(ctx, cfg, caps, filesToStage)
	}
	return composeFromFlags(cfg, filesToStage)
}

// composeInteractive runs the sequential huh wizard with flag/amend/scope
// defaults.
func composeInteractive(ctx context.Context, cfg *config.Config, caps *render.Caps, filesToStage []string) (ccommit.CommitMessage, error) {
	opts := tui.CommitOpts{
		Types:         cfg.VisibleTypes(),
		Scopes:        cfg.Scope.Values,
		ScopeMode:     cfg.Scope.Mode,
		SubjectMax:    cfg.Subject.MaxLength,
		SubjectMin:    cfg.Subject.MinLength,
		BodyWrap:      cfg.Body.Wrap,
		FooterKeys:    cfg.Footers.Keys,
		EditorForBody: commitFlags.edit,
		Accessible:    caps.Accessible,
		Dark:          tui.DetectDark(),
		OnAddScope: func(scope string, toRepo bool) error {
			// Default: the user config. The repo config is opt-in, so adding
			// a scope never silently creates a committed .commitly.yaml.
			path := cfg.UserConfigPath
			if toRepo {
				if cfg.RepoConfigPath != "" {
					path = cfg.RepoConfigPath
				} else {
					root, err := git.Root(ctx)
					if err != nil {
						return fmt.Errorf("not a git repository")
					}
					path = filepath.Join(root, ".commitly.yaml")
				}
			}
			values, err := config.ScopeValuesFromFile(path)
			if err != nil {
				return err
			}
			for _, s := range values {
				if s == scope {
					return nil // already known
				}
			}
			values = append(values, scope)
			return writeConfigKey(path, "scope.values", values, false)
		},
	}

	if commitFlags.amend {
		if prev, err := previousMessage(ctx); err == nil && prev != "" {
			if pr := ccommit.Parse(prev); pr.OK {
				opts.Initial = pr.Message
			}
		}
	}
	if cfg.Scope.Mode == "auto" && len(filesToStage) > 0 {
		if s, amb, ok := cfg.DetectScope(filesToStage); ok && !amb {
			opts.DefaultScope = s
		}
	}

	msg, err := tui.RunCommitWizard(ctx, opts)
	if err != nil {
		if tui.IsAborted(err) {
			return msg, render.AbortError()
		}
		return msg, err
	}

	if msg.Scope != "" && cfg.Scope.Mode == "list" && len(cfg.Scope.Values) > 0 && !scopeKnown(cfg, msg.Scope) {
		return msg, render.Usage(fmt.Sprintf("unknown scope %q", msg.Scope),
			"allowed: "+strings.Join(cfg.ScopeNames(), " "))
	}
	return msg, nil
}

// composeFromFlags builds the message purely from flags (the non-interactive
// path). Missing required values error with exit 2 rather than prompting.
func composeFromFlags(cfg *config.Config, filesToStage []string) (ccommit.CommitMessage, error) {
	m := ccommit.CommitMessage{}

	if commitFlags.amend {
		if prev, err := previousMessage(context.Background()); err == nil && prev != "" {
			if pr := ccommit.Parse(prev); pr.OK {
				m = pr.Message
				m.Footers = nil
			}
		}
	}

	if commitFlags.typ != "" {
		m.Type = commitFlags.typ
	} else if m.Type == "" {
		return m, render.Usage("no commit type given and stdin is not a terminal",
			"Pass --type and --message, or run interactively:",
			`  commitly commit --type fix --message "handle empty scope list"`)
	}
	if ct := cfg.FindType(m.Type); ct == nil {
		return m, render.Usage(fmt.Sprintf("unknown commit type %q", m.Type),
			"Did you mean "+didYouMean(m.Type, cfg.TypeNames())+"?",
			"This repository allows:",
			"  "+strings.Join(cfg.TypeNames(), "  "),
			"Types come from .commitly.yaml — run `commitly config list` to see the source.")
	}

	if commitFlags.scope != "" {
		m.Scope = commitFlags.scope
	} else if cfg.Scope.Mode == "auto" && len(filesToStage) > 0 {
		if s, amb, matched := cfg.DetectScope(filesToStage); matched && !amb {
			m.Scope = s
		}
	}
	if m.Scope != "" && cfg.Scope.Mode == "list" && len(cfg.Scope.Values) > 0 && !scopeKnown(cfg, m.Scope) {
		return m, render.Usage(fmt.Sprintf("unknown scope %q", m.Scope),
			"allowed: "+strings.Join(cfg.ScopeNames(), " "))
	}

	if commitFlags.breakDesc != "" {
		m.Breaking = true
		m.Footers = append(m.Footers, ccommit.Footer{Token: "BREAKING CHANGE", Value: commitFlags.breakDesc})
	} else if commitFlags.breaking {
		m.Breaking = true
	}

	if commitFlags.message != "" {
		m.Subject = commitFlags.message
	} else if m.Subject == "" {
		return m, render.Usage("no commit message given and stdin is not a terminal",
			"Pass --message, or run interactively:",
			`  commitly commit --type fix --message "handle empty scope list"`)
	}

	if len(commitFlags.body) > 0 {
		m.Body = strings.Join(commitFlags.body, "\n\n")
	}

	for _, f := range commitFlags.footer {
		token, value := splitFooterFlag(f)
		m.Footers = append(m.Footers, ccommit.Footer{Token: token, Value: value})
	}
	return m, nil
}

func pickFiles(ctx context.Context, changes []git.FileChange, caps *render.Caps) ([]string, error) {
	if !interactiveAllowed(caps) {
		return nil, render.Usage("--all needs a terminal to pick files",
			"Use --all-files to stage everything, or stage some first:",
			"  git add <file>")
	}
	sort.SliceStable(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })

	return tui.RunStagingPicker(ctx, changes, func(fc git.FileChange) string {
		args := []string{"diff", "--", fc.Path}
		if fc.IsStaged() && !fc.IsUnstaged() {
			args = []string{"diff", "--cached", "--", fc.Path}
		}
		out, _, err := git.Run(context.Background(), args...)
		if err != nil {
			return ""
		}
		return out
	})
}
