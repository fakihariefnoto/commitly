package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"strings"

	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var configCmd = &cobra.Command{
	Use:   "config <command>",
	Short: "Inspect and change configuration",
	Long: `Inspect and change configuration.

Values are resolved from five sources, highest first:
  1. command flag        --type fix
  2. environment         COMMITLY_SUBJECT__MAX_LENGTH=100
  3. repo config         <repo>/.commitly.yaml
  4. user config         ~/.config/commitly/config.yaml
  5. built-in default

"config get" shows which source won — start there when a setting seems to be
ignored.`,
}

var configGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Print one value and where it came from",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		return configGet(cmd.Context(), args[0])
	},
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Write a value (--global for user, --local for this repo)",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		return configSet(cmd.Context(), args[0], args[1])
	},
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Print the full effective config",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return configList(cmd.Context())
	},
}

var configPathCmd = &cobra.Command{
	Use:   "path",
	Short: "Print every config file path, in precedence order",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return configPaths(cmd.Context())
	},
}

var configSetFlags struct {
	global bool
	local  bool
	unset  bool
}

func init() {
	configCmd.AddCommand(configGetCmd, configSetCmd, configListCmd, configPathCmd)
	fs := configSetCmd.Flags()
	fs.BoolVar(&configSetFlags.global, "global", false, "write to the user config")
	fs.BoolVar(&configSetFlags.local, "local", false, "write to this repo's .commitly.yaml")
	fs.BoolVar(&configSetFlags.unset, "unset", false, "remove the key so it falls back down the chain")
}

func configGet(ctx context.Context, key string) error {
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}
	val, ok := configValue(cfg, key)
	if !ok {
		return render.Usage(fmt.Sprintf("unknown config key %q", key),
			"Run `commitly config list` to see every key.")
	}
	if globals.json {
		payload := map[string]any{"key": key, "value": val, "source": sourceName(cfg.Provenance(key))}
		b, _ := json.Marshal(payload)
		render.Result("%s", string(b))
		return nil
	}
	render.Result("%v", val)
	render.Note("%s = %v", key, val)
	if p := cfg.Provenance(key); p != nil {
		render.Note("  from  %s", p.Source)
	}
	return nil
}

func sourceName(p *config.Provenance) string {
	if p == nil {
		return "default"
	}
	return p.Source.String()
}

func configSet(ctx context.Context, key, value string) error {
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}
	kind, ok := configKeyType(key)
	if !ok {
		return render.Usage(fmt.Sprintf("unknown config key %q", key),
			"Run `commitly config list` to see every key.")
	}
	typed, err := coerce(kind, value)
	if err != nil {
		return render.Usage(fmt.Sprintf("config key %s expects %s, got %q", key, kind, value))
	}

	userOnly := config.UserOnlyKey(key)
	global := configSetFlags.global
	local := configSetFlags.local
	if userOnly {
		if local {
			return render.Usage(fmt.Sprintf("%s cannot be set per repository", key),
				"It's a user-level setting, so cloning a project can't change whether your",
				"commits are recorded. Set it for yourself:",
				fmt.Sprintf("  commitly config set %s %s --global", key, value))
		}
		global = true
	}
	target := ""
	if global {
		target = cfg.UserConfigPath
	} else if local {
		target = cfg.RepoConfigPath
		if target == "" {
			// No repo config yet — create <repo>/.commitly.yaml.
			root, err := git.Root(context.Background())
			if err != nil {
				return render.Fail("--local needs a git repository",
					"Run this inside a repository, or omit --local to write to your user config.")
			}
			target = filepath.Join(root, ".commitly.yaml")
		}
	} else {
		// Default: user config. Repo config is opt-in via --local, so a
		// `config set` never silently creates a committed .commitly.yaml.
		target = cfg.UserConfigPath
	}

	if err := writeConfigKey(target, key, typed, configSetFlags.unset); err != nil {
		return render.Fail("could not write "+target, err.Error())
	}
	render.Note("✓ %s = %v", key, value)
	if configSetFlags.unset {
		render.Note("  %s unset in %s", key, target)
		if newVal, ok := configValue(cfg, key); ok {
			render.Note("  now resolves to %v from %s", newVal, sourceName(cfg.Provenance(key)))
		}
		return nil
	}
	if userOnly {
		render.Note("  written to %s", target)
		render.Note("")
		render.Note("  history.* and serve.* are user-level only — they're ignored if they appear")
		render.Note("  in a repository's .commitly.yaml, so a project can't change how your")
		render.Note("  personal activity is recorded.")
		return nil
	}
	render.Note("  written to %s", target)
	if target == cfg.RepoConfigPath {
		render.Note("")
		render.Note("  This file is committed, so everyone on the project gets this setting.")
	}
	return nil
}

func configList(ctx context.Context) error {
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}
	if globals.json {
		return configListJSON(cfg)
	}
	// Render as YAML.
	doc := map[string]any{
		"extends": cfg.Extends,
		"types":   typeListYAML(cfg),
		"scope": map[string]any{
			"mode": cfg.Scope.Mode, "required": cfg.Scope.Required,
			"values": cfg.Scope.Values, "auto": cfg.Scope.Auto,
		},
		"subject": map[string]any{
			"max_length": cfg.Subject.MaxLength, "min_length": cfg.Subject.MinLength,
			"case": cfg.Subject.Case, "forbid_trailing_period": cfg.Subject.ForbidTrailingPeriod,
		},
		"body": map[string]any{
			"ask": cfg.Body.Ask, "wrap": cfg.Body.Wrap, "required_for": cfg.Body.RequiredFor,
		},
		"footers": map[string]any{
			"ask": cfg.Footers.Ask, "keys": cfg.Footers.Keys,
			"breaking_needs_description": cfg.Footers.BreakingNeedsDescription,
		},
		"emoji": map[string]any{"enabled": cfg.Emoji.Enabled, "position": cfg.Emoji.Position},
		"history": map[string]any{
			"enabled": cfg.History.Enabled, "max_entries": cfg.History.MaxEntries,
		},
		"stats": map[string]any{
			"enabled": cfg.Stats.Enabled, "count_from_hook": cfg.Stats.CountFromHook,
			"retention_days": cfg.Stats.RetentionDays, "compact_threshold": cfg.Stats.CompactThreshold,
		},
		"serve": map[string]any{"port": cfg.Serve.Port, "host": cfg.Serve.Host, "theme": cfg.Serve.Theme},
	}
	b, _ := yaml.Marshal(doc)
	render.Result("%s", strings.TrimSpace(string(b)))
	return nil
}

func typeListYAML(cfg *config.Config) []map[string]any {
	var out []map[string]any
	for _, t := range cfg.Types {
		m := map[string]any{"name": t.Name, "description": t.Description}
		if t.Changelog != "" {
			m["changelog"] = t.Changelog
		}
		out = append(out, m)
	}
	return out
}

func configListJSON(cfg *config.Config) error {
	payload := map[string]any{
		"extends": cfg.Extends,
		"types":   cfg.TypeNames(),
		"scope":   cfg.Scope,
		"subject": cfg.Subject,
		"history": cfg.History,
		"stats":   cfg.Stats,
		"serve":   cfg.Serve,
	}
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}

func configPaths(ctx context.Context) error {
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}
	lines := []string{}
	if cfg.RepoConfigPath != "" {
		lines = append(lines, fmt.Sprintf("%s    repo    (exists)", cfg.RepoConfigPath))
	}
	if cfg.UserConfigPath != "" {
		exists := "missing"
		if fi, err := os.Stat(cfg.UserConfigPath); err == nil && !fi.IsDir() {
			exists = "exists"
		}
		lines = append(lines, fmt.Sprintf("%s    user    (%s)", cfg.UserConfigPath, exists))
	}
	lines = append(lines, "built-in defaults                               —")
	render.Result("%s", strings.Join(lines, "\n"))

	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}
	histPath := filepath.Join(dir, "history.jsonl")
	statsPath := filepath.Join(dir, "stats.jsonl")
	count := 0
	if es := history.OpenEntryStore(dir, cfg.History.MaxEntries); es != nil {
		if c, err := es.Count(); err == nil {
			count = c
		}
	}
	fmt.Fprintf(render.Out, "\nhistory store: %s    (exists, %d entries)\n", histPath, count)
	if _, err := os.Stat(statsPath); err == nil {
		fmt.Fprintf(render.Out, "stats store:   %s    (exists)\n", statsPath)
	} else {
		fmt.Fprintf(render.Out, "stats store:   %s    (missing)\n", statsPath)
	}
	return nil
}

func configValue(cfg *config.Config, key string) (any, bool) {
	switch key {
	case "extends":
		return cfg.Extends, true
	case "scope.mode":
		return cfg.Scope.Mode, true
	case "scope.values":
		return cfg.Scope.Values, true
	case "scope.required":
		return cfg.Scope.Required, true
	case "subject.max_length":
		return cfg.Subject.MaxLength, true
	case "subject.min_length":
		return cfg.Subject.MinLength, true
	case "subject.case":
		return cfg.Subject.Case, true
	case "subject.forbid_trailing_period":
		return cfg.Subject.ForbidTrailingPeriod, true
	case "body.ask":
		return cfg.Body.Ask, true
	case "body.wrap":
		return cfg.Body.Wrap, true
	case "emoji.enabled":
		return cfg.Emoji.Enabled, true
	case "emoji.position":
		return cfg.Emoji.Position, true
	case "footers.ask":
		return cfg.Footers.Ask, true
	case "footers.breaking_needs_description":
		return cfg.Footers.BreakingNeedsDescription, true
	case "changelog.group_breaking_first":
		return cfg.Changelog.GroupBreakingFirst, true
	case "changelog.include_merges":
		return cfg.Changelog.IncludeMerges, true
	case "changelog.link_commits":
		return cfg.Changelog.LinkCommits, true
	case "changelog.repo_url":
		return cfg.Changelog.RepoURL, true
	case "history.enabled":
		return cfg.History.Enabled, true
	case "history.max_entries":
		return cfg.History.MaxEntries, true
	case "history.store_path":
		return cfg.History.StorePath, true
	case "stats.enabled":
		return cfg.Stats.Enabled, true
	case "stats.count_from_hook":
		return cfg.Stats.CountFromHook, true
	case "stats.retention_days":
		return cfg.Stats.RetentionDays, true
	case "stats.compact_threshold":
		return cfg.Stats.CompactThreshold, true
	case "serve.port":
		return cfg.Serve.Port, true
	case "serve.host":
		return cfg.Serve.Host, true
	case "serve.open":
		return cfg.Serve.Open, true
	case "serve.theme":
		return cfg.Serve.Theme, true
	}
	return nil, false
}

func configKeyType(key string) (string, bool) {
	switch key {
	case "extends", "scope.mode", "subject.case", "emoji.position", "history.store_path",
		"serve.host", "serve.theme", "changelog.repo_url":
		return "string", true
	case "scope.required", "subject.forbid_trailing_period", "body.ask", "emoji.enabled",
		"footers.ask", "footers.breaking_needs_description", "changelog.group_breaking_first",
		"changelog.include_merges", "changelog.link_commits", "history.enabled",
		"stats.enabled", "stats.count_from_hook", "serve.open":
		return "bool", true
	case "subject.max_length", "subject.min_length", "body.wrap", "history.max_entries",
		"stats.retention_days", "stats.compact_threshold", "serve.port":
		return "number", true
	case "scope.values":
		return "list", true
	}
	return "", false
}

func coerce(kind, value string) (any, error) {
	switch kind {
	case "string":
		return value, nil
	case "list":
		// Comma-separated scope names → []string, written as clean YAML names.
		var scopes []string
		for _, part := range strings.Split(value, ",") {
			if name := strings.TrimSpace(part); name != "" {
				scopes = append(scopes, name)
			}
		}
		return scopes, nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true, nil
		case "false", "0", "no", "off":
			return false, nil
		}
		return nil, fmt.Errorf("not a bool")
	case "number":
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil {
			return nil, fmt.Errorf("not a number")
		}
		return n, nil
	}
	return nil, fmt.Errorf("unknown type")
}

// writeConfigKey edits a YAML file in place, preserving comments and order.
func writeConfigKey(path string, key string, value any, unset bool) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		data = []byte("version: 1\nextends: default\n")
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return err
	}
	if node.Kind == 0 {
		node.Kind = yaml.DocumentNode
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = *node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("config file is not a mapping")
	}
	parts := strings.Split(key, ".")
	if !unset {
		typedNode := &yaml.Node{}
		if err := typedNode.Encode(value); err != nil {
			return err
		}
		setNested(&node, parts, typedNode)
	} else {
		unsetNested(&node, parts)
	}
	out, err := yaml.Marshal(&node)
	if err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}

func setNested(m *yaml.Node, parts []string, value *yaml.Node) {
	// Find or create the chain of mapping nodes.
	for i, p := range parts {
		last := i == len(parts)-1
		idx := keyIndex(m, p)
		if idx >= 0 {
			child := m.Content[idx+1]
			if last {
				m.Content[idx+1] = value
				return
			}
			if child.Kind != yaml.MappingNode {
				nm := &yaml.Node{Kind: yaml.MappingNode}
				m.Content[idx+1] = nm
				m = nm
			} else {
				m = child
			}
			continue
		}
		// Append new key.
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: p}
		if last {
			m.Content = append(m.Content, keyNode, value)
			return
		}
		nm := &yaml.Node{Kind: yaml.MappingNode}
		m.Content = append(m.Content, keyNode, nm)
		m = nm
	}
}

func unsetNested(m *yaml.Node, parts []string) {
	cur := m
	for i, p := range parts {
		last := i == len(parts)-1
		idx := keyIndex(cur, p)
		if idx < 0 {
			return
		}
		if last {
			cur.Content = append(cur.Content[:idx], cur.Content[idx+2:]...)
			return
		}
		cur = cur.Content[idx+1]
		if cur.Kind != yaml.MappingNode {
			return
		}
	}
}

func keyIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}
