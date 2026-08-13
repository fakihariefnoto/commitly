package main

import (
	"context"
	"debug/buildinfo"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version, commit and build date",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		caps := render.Detect()
		if globals.json {
			return versionJSON(cmd.Context())
		}
		if caps.Verbose > 0 {
			return versionVerbose(cmd.Context())
		}
		render.Result("%s", versionString())
		return nil
	},
}

func versionJSON(ctx context.Context) error {
	bi, commitInfo := buildInfo()
	method := detectInstallMethod(exePath())
	gitVer := ""
	if out, _, err := git.Run(ctx, "--version"); err == nil {
		gitVer = strings.TrimSpace(out)
	}
	payload := map[string]any{
		"version":        version,
		"commit":         commitInfo,
		"date":           date,
		"go":             bi,
		"platform":       runtime.GOOS + "/" + runtime.GOARCH,
		"install_method": method,
		"git_version":    gitVer,
	}
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}

func versionVerbose(ctx context.Context) error {
	bi, commitInfo := buildInfo()
	method := detectInstallMethod(exePath())
	gitVer := ""
	if out, _, err := git.Run(ctx, "--version"); err == nil {
		gitVer = strings.TrimSpace(out)
	}
	fmt.Fprintf(render.Out, "%s\n", versionString())
	fmt.Fprintf(render.Out, "  commit     %s\n", commitInfo)
	fmt.Fprintf(render.Out, "  built      %s\n", date)
	fmt.Fprintf(render.Out, "  go         %s\n", bi)
	fmt.Fprintf(render.Out, "  platform   %s/%s\n", runtime.GOOS, runtime.GOARCH)
	installNote := ""
	if method == "go install" {
		installNote = " (no git-cm symlink — run `commitly init` for the alias)"
	}
	fmt.Fprintf(render.Out, "  install    %s%s\n", method, installNote)
	fmt.Fprintf(render.Out, "  git        %s\n", gitVer)
	if cfgPath := configPath(); cfgPath != "" {
		fmt.Fprintf(render.Out, "  config     %s\n", cfgPath)
	}
	return nil
}

// buildInfo returns (go version, commit info) from the embedded build info,
// falling back to the ldflags-injected values. go install users get no
// ldflags, so this is the only honest source for them.
func buildInfo() (goVer, commitInfo string) {
	goVer = runtime.Version()
	commitInfo = commit
	if bi, err := buildinfo.ReadFile(exePath()); err == nil {
		if bi.GoVersion != "" {
			goVer = bi.GoVersion
		}
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				commitInfo = s.Value
				return
			}
		}
	}
	return goVer, commitInfo
}

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return "commitly"
	}
	return p
}

func configPath() string {
	if globals.configPath != "" {
		return globals.configPath
	}
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	dir := wd
	for {
		cand := filepath.Join(dir, ".commitly.yaml")
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
