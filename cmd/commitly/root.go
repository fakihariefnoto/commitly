package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
)

// globalFlags carries the persistent flag values.
type globalFlags struct {
	configPath string
	json       bool
	quiet      bool
	verbose    int
	noColor    bool
	yes        bool
}

var globals globalFlags

// app bundles the shared runtime state commands need.
type app struct {
	caps *render.Caps
	cfg  *config.Config
	ctx  context.Context
}

// loadConfig resolves the effective config honoring --config.
func (a *app) loadConfig() (*config.Config, error) {
	if a.cfg != nil {
		return a.cfg, nil
	}
	opts := config.LoadOptions{
		ConfigPath:    globals.configPath,
		FlagOverrides: map[string]any{},
	}
	cfg, err := config.Load(opts)
	if err != nil {
		return nil, err
	}
	render.PrintWarnings(cfg.Warnings)
	a.cfg = cfg
	return cfg, nil
}

var rootCmd = &cobra.Command{
	Use:   "commitly",
	Short: "Compose Conventional Commits messages, interactively",
	Long: `Commitly turns writing a Conventional Commits message into a guided flow.

One binary, two names: installed as commitly, and reachable as ` + "`git cm`" + `
through a git-cm symlink, so it works with zero configuration.

Everything it does is local — nothing is ever uploaded.`,
	Version:       "placeholder",
	SilenceErrors: true,
	SilenceUsage:  true,
	Args:          cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		render.Out = os.Stdout
		render.Err = os.Stderr
	},
}

func init() {
	rootCmd.SetVersionTemplate(versionString() + "\n")
	pf := rootCmd.PersistentFlags()
	pf.StringVar(&globals.configPath, "config", "", "use this config file instead of discovery")
	pf.BoolVar(&globals.json, "json", false, "machine output on stdout, nothing decorative")
	pf.BoolVarP(&globals.quiet, "quiet", "q", false, "suppress non-essential stderr")
	pf.CountVarP(&globals.verbose, "verbose", "v", "echo every git command line to stderr (repeatable)")
	pf.BoolVar(&globals.noColor, "no-color", false, "force plain output")
	pf.BoolVarP(&globals.yes, "yes", "y", false, "skip every confirmation")

	rootCmd.AddCommand(
		commitCmd,
		lintCmd,
		statusCmd,
		changelogCmd,
		initCmd,
		configCmd,
		serveCmd,
		completionCmd,
		manCmd,
		versionCmd,
	)

	// Aliases.
	rootCmd.Aliases = nil
	commitCmd.Aliases = []string{"c"}
	statusCmd.Aliases = []string{"st"}
	changelogCmd.Aliases = []string{"cl"}
}

// run is the real main. argv[0] dispatch happens here, before cobra parses.
func run() int {
	render.Out = os.Stdout
	render.Err = os.Stderr

	caps := render.Detect()
	if caps.Verbose > 0 {
		git.Verbose = func(format string, args ...any) {
			fmt.Fprintf(render.Err, "%s\n", fmt.Sprintf(format, args...))
		}
	}

	// argv[0] dispatch (AD-1): invoked as git-cm → default to commit.
	if base := filepath.Base(os.Args[0]); base == "git-cm" || base == "git-cm.exe" {
		args := os.Args[1:]
		if len(args) > 0 {
			// git-cm exposes only the commit surface. Sub-verbs error.
			if args[0] == "lint" || args[0] == "status" || args[0] == "changelog" ||
				args[0] == "init" || args[0] == "config" || args[0] == "serve" ||
				args[0] == "version" || args[0] == "completion" || args[0] == "man" || args[0] == "help" {
				fmt.Fprintf(render.Err, "git-cm exposes only the commit flow.\n\nUse `commitly %s` for that command.\n", args[0])
				return render.ExitUsage
			}
		}
		commitCmd.SetArgs(args)
		return execute(commitCmd)
	}

	// --version handled by cobra automatically; bare commitly prints help.
	if len(os.Args) == 1 {
		rootCmd.SetArgs([]string{"--help"})
		return execute(rootCmd)
	}
	return execute(rootCmd)
}

func execute(cmd *cobra.Command) int {
	err := cmd.Execute()
	if err != nil {
		kind := render.KindOf(err)
		// Validation failures already printed their report; don't add an
		// Error: line on top of it.
		if kind != render.ExitValidate {
			caps := render.Detect()
			render.PrintError(err, caps.Verbose > 0)
		}
		return kind
	}
	return render.ExitOK
}

// detectInstallMethod inspects the binary path and sibling git-cm.
func detectInstallMethod(exe string) string {
	dir := filepath.Dir(exe)
	if siblingExists(dir, "git-cm") || siblingExists(dir, "git-cm.exe") {
		return "homebrew"
	}
	if strings.Contains(dir, "go-build") || strings.Contains(dir, "gopath") {
		return "go install"
	}
	return "standalone"
}

func siblingExists(dir, name string) bool {
	fi, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !fi.IsDir()
}

// versionString renders "commitly X (sha, date)".
func versionString() string {
	c, d := shortCommit(), shortDate()
	if c == "" && d == "" {
		return fmt.Sprintf("commitly %s", version)
	}
	return fmt.Sprintf("commitly %s (%s, %s)", version, c, d)
}

func shortCommit() string {
	if commit == "" || commit == "none" {
		return ""
	}
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func shortDate() string {
	if date == "" {
		return ""
	}
	if len(date) >= 10 {
		return date[:10]
	}
	return date
}

var _ = runtime.GOOS
