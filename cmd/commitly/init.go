package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/hook"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
)

var initFlags struct {
	alias       *bool
	hookInstall *bool
	completions *bool
	configFile  *bool
	global      bool
	all         bool
	force       bool
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Set up the optional extras",
	Long: `Set up the optional extras.

You don't need this to use commitly — ` + "`git cm`" + ` works as soon as the binary is
installed. This adds the parts your install method couldn't:

  git alias        Only needed if you installed with ` + "`go install`" + `, which can't
                   create the git-cm symlink that makes ` + "`git cm`" + ` work natively.
  commit-msg hook  Validates commits made with plain ` + "`git commit`" + `, so history
                   stays consistent even when this tool wasn't used.
  completions      Tab-completion for your shell.
  .commitly.yaml   A starter config with this project's commit types.

Every step asks first and is safe to re-run.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runInit(cmd.Context())
	},
}

func init() {
	initCmd.Flags().BoolVar(&initFlags.all, "all", false, "every applicable step, no prompts")
	initCmd.Flags().BoolVar(&initFlags.global, "global", false, "alias globally (default) vs this repo")
	initCmd.Flags().BoolVar(&initFlags.force, "force", false, "replace an existing commit-msg hook")
}

func runInit(ctx context.Context) error {
	root := ""
	inRepo := git.IsInsideWorkTree(ctx)
	if inRepo {
		r, err := git.Root(ctx)
		if err == nil {
			root = r
		}
	}

	type step struct {
		name string
		run  func() (status, detail string, err error)
	}
	var results []stepResult

	alias := initFlags.all || initFlags.global
	hookInstall := initFlags.all || initFlags.force
	configFile := initFlags.all

	// Alias.
	if alias && !hook.CommitlyFoundOnPATH() && !globals.yes {
		render.Note("Install the `git cm` alias globally? [Y/n]")
		ans, _ := readAnswer()
		if strings.EqualFold(ans, "n") {
			alias = false
		}
	}
	if alias {
		err := hook.InstallAlias(ctx, func(c string) error {
			return exec.Command("sh", "-c", c).Run()
		})
		if err != nil {
			results = append(results, stepResult{Name: "alias", Status: "failed", Detail: err.Error()})
		} else {
			results = append(results, stepResult{Name: "alias", Status: "installed", Detail: "git config --global alias.cm '!commitly commit'"})
		}
	} else {
		if hook.CommitlyFoundOnPATH() {
			results = append(results, stepResult{Name: "alias", Status: "skipped", Detail: "git-cm already on PATH"})
		} else {
			results = append(results, stepResult{Name: "alias", Status: "skipped", Detail: "not requested"})
		}
	}

	// Hook — repo only.
	if inRepo {
		if hookInstall {
			if err := maybeShowHookDisclosure(ctx); err != nil {
				return err
			}
			path, overwrote, err := hook.WriteHook(hooksDir(ctx), initFlags.force)
			if err != nil {
				if errors.Is(err, hook.ErrForeignHook) {
					results = append(results, stepResult{Name: "hook", Status: "skipped", Detail: "existing foreign hook"})
					render.Note("▲ %s already exists and wasn't written by commitly.", filepath.Join(hooksDir(ctx), "commit-msg"))
					render.Note("")
					render.Note("  Not touching it. To chain commitly's check, add this line to your hook:")
					render.Note("")
					render.Note("      commitly lint --file \"$1\" --hook || exit 1")
					render.Note("")
					render.Note("  Or replace it entirely (your current hook is overwritten):")
					render.Note("")
					render.Note("      commitly init --hook --force")
				} else {
					results = append(results, stepResult{Name: "hook", Status: "failed", Detail: err.Error()})
				}
			} else {
				counting := "validation only, no counting"
				if configCountFromHook() {
					counting = "validation + adherence counting"
				}
				results = append(results, stepResult{Name: "hook", Status: "installed", Detail: path + "  (" + counting + ")"})
				if overwrote {
					render.Note("The installed commit-msg hook was written by commitly.")
					render.Note("Updating it to %s — no other changes.", version)
				}
			}
		} else {
			results = append(results, stepResult{Name: "hook", Status: "skipped", Detail: "not requested"})
		}

		// Starter config.
		if configFile {
			cfgPath := filepath.Join(root, ".commitly.yaml")
			if _, err := os.Stat(cfgPath); os.IsNotExist(err) {
				if err := os.WriteFile(cfgPath, []byte(hook.StarterConfig("list")), 0o644); err != nil {
					results = append(results, stepResult{Name: "config", Status: "failed", Detail: err.Error()})
				} else {
					results = append(results, stepResult{Name: "config", Status: "installed", Detail: cfgPath + " (11 types, scope mode: list)"})
				}
			} else {
				results = append(results, stepResult{Name: "config", Status: "skipped", Detail: ".commitly.yaml already present"})
			}
		} else {
			results = append(results, stepResult{Name: "config", Status: "skipped", Detail: "not requested"})
		}
	} else {
		render.Note("Not inside a git repository — showing global steps only.")
		render.Note("")
		render.Note("  The commit-msg hook and .commitly.yaml are per-repository.")
		render.Note("  Run this again inside one to set those up.")
	}

	if globals.json {
		b, _ := json.Marshal(map[string]any{"steps": results, "changed": countStatus(results, "installed"), "skipped": countStatus(results, "skipped")})
		render.Result("%s", string(b))
		return nil
	}

	changed := 0
	skipped := 0
	for _, r := range results {
		mark := "✓"
		if r.Status == "skipped" {
			mark = "—"
			skipped++
		} else if r.Status == "failed" {
			mark = "✗"
		} else {
			changed++
		}
		render.Result("%s %-14s %s", mark, r.Name, r.Detail)
	}
	render.Result("")
	if changed == 0 && skipped > 0 {
		render.Result("Nothing changed.")
	} else if skipped > 0 {
		render.Result("Done. %d step(s) skipped.", skipped)
	}
	return nil
}

func countStatus(results []stepResult, status string) int {
	n := 0
	for _, r := range results {
		if r.Status == status {
			n++
		}
	}
	return n
}

type stepResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

func maybeShowHookDisclosure(ctx context.Context) error {
	if !configCountFromHook() {
		return nil
	}
	render.Note("The commit-msg hook does two things:")
	render.Note("")
	render.Note("  1. Validates every commit message in this repository, including ones made")
	render.Note("     with plain `git commit`, and rejects non-conforming ones.")
	render.Note("")
	render.Note("  2. Counts them, so `commitly status --stats` can tell you what share of")
	render.Note("     this repo's commits actually follow the convention.")
	render.Note("")
	render.Note("  What the counter records, per commit:")
	render.Note("      date · repository id · conforming yes/no · commit type · yours or not")
	render.Note("")
	render.Note("  What it never records:")
	render.Note("      the commit message · the subject · the SHA · the file paths · anything")
	render.Note("      about the change itself")
	render.Note("")
	render.Note("  Where it goes:")
	render.Note("      ~/.local/state/commitly/stats.jsonl — your machine only, never uploaded.")
	render.Note("")
	render.Note("Install the hook with counting? [Y/n]: ")
	ans, _ := readAnswer()
	if strings.EqualFold(ans, "n") {
		render.Note("")
		render.Note("Install the hook, validation only? [v]: ")
		// Best-effort: disable counting in the user config.
		if !globals.yes {
			if err := disableHookCounting(); err != nil {
				return err
			}
		}
	}
	return nil
}

func disableHookCounting() error {
	cfg, err := configLoadQuiet()
	if err != nil {
		return err
	}
	return writeConfigKey(cfg.UserConfigPath, "stats.count_from_hook", false, false)
}

func configLoadQuiet() (*config.Config, error) {
	return (&app{ctx: context.Background(), caps: render.Detect()}).loadConfig()
}

func configCountFromHook() bool {
	cfg, err := configLoadQuiet()
	if err != nil {
		return true
	}
	return cfg.Stats.CountFromHook
}

func hooksDir(ctx context.Context) string {
	dir, err := git.HooksDir(ctx)
	if err != nil {
		return ".git/hooks"
	}
	return dir
}
