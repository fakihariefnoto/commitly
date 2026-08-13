// Package hook installs the optional extras: the git-cm alias, the
// commit-msg hook, shell completions and a starter .commitly.yaml. All are
// conveniences — git cm works before any of them run (AD-1, goal G2).
package hook

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Version is stamped at build time by the command layer.
var Version = "dev"

// HookScript returns the commit-msg hook content for the current version.
func HookScript() string {
	return fmt.Sprintf(`#!/bin/sh
# commitly commit-msg hook (version %s)
# Validates the message against Conventional Commits and this repo's
# .commitly.yaml. When stats.count_from_hook is on, counts it.
# Best-effort counting: a counter write failure never affects the verdict.
commitly lint --file "$1" --hook || exit 1
`, Version)
}

// AliasConfig returns the git config lines for the alias.
func AliasLines() []string {
	return []string{
		"alias.cm '!commitly commit'",
		"alias.cma '!commitly commit -a'",
	}
}

// InstallAlias sets the global git alias.
func InstallAlias(ctx context.Context, exec func(string) error) error {
	for _, line := range AliasLines() {
		parts := strings.SplitN(line, " ", 2)
		if err := exec("git config --global " + parts[0] + " " + parts[1]); err != nil {
			return fmt.Errorf("git config failed: %w", err)
		}
	}
	return nil
}

var hookMarkerRe = regexp.MustCompile(`commitly commit-msg hook \(version ([0-9.]+)\)`)

// HookState describes an existing hook file.
type HookState struct {
	Exists     bool
	IsCommitly bool
	Version    string // installed commitly version, when IsCommitly
}

// InspectHook reports the state of the hook at hooksDir/commit-msg.
func InspectHook(hooksDir string) (HookState, error) {
	path := filepath.Join(hooksDir, "commit-msg")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return HookState{}, nil
		}
		return HookState{}, err
	}
	state := HookState{Exists: true}
	if m := hookMarkerRe.FindSubmatch(data); m != nil {
		state.IsCommitly = true
		state.Version = string(m[1])
	}
	return state, nil
}

// WriteHook writes the hook, creating the hooks dir. Overwrite is refused
// for foreign hooks unless force is set.
func WriteHook(hooksDir string, force bool) (path string, overwrote bool, err error) {
	path = filepath.Join(hooksDir, "commit-msg")
	state, err := InspectHook(hooksDir)
	if err != nil {
		return "", false, err
	}
	if state.Exists && !state.IsCommitly && !force {
		return path, false, ErrForeignHook
	}
	overwrote = state.Exists
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return "", false, err
	}
	if err := os.WriteFile(path, []byte(HookScript()), 0o755); err != nil {
		return "", false, err
	}
	return path, overwrote, nil
}

// ErrForeignHook is returned when a non-commitly hook exists and force is off.
var ErrForeignHook = fmt.Errorf("existing hook not written by commitly")

// CommitlyFoundOnPATH reports whether a git-cm binary is reachable.
func CommitlyFoundOnPATH() bool {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		for _, name := range []string{"git-cm", "git-cm.exe"} {
			if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() {
				return true
			}
		}
	}
	return false
}

// StarterConfig returns a starter .commitly.yaml.
func StarterConfig(scopeMode string) string {
	modes := "list"
	if scopeMode != "" {
		modes = scopeMode
	}
	return fmt.Sprintf(`version: 1
extends: default

scope:
  mode: %s
`, modes)
}
