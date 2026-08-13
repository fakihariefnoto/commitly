// Package config resolves Commitly's effective configuration from its five
// sources (flag → env → repo → user → default), enforces the user-only key
// boundary, and provides provenance for every key so "why is this 72?" has
// an answer. Config is inert data: no field may ever be executed.
package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Source names a config source in precedence order (Flag wins).
type Source int

const (
	SrcFlag Source = iota
	SrcEnv
	SrcRepo
	SrcUser
	SrcDefault
)

var sourceNames = map[Source]string{
	SrcFlag:    "command flag",
	SrcEnv:     "environment",
	SrcRepo:    "repo config",
	SrcUser:    "user config",
	SrcDefault: "built-in default",
}

func (s Source) String() string { return sourceNames[s] }

// Provenance records where a resolved value came from.
type Provenance struct {
	Source Source
	Path   string // key path within the source file, e.g. "subject.max_length"
	Line   int    // 1-based; 0 when not applicable (env/flag/default)
	Value  any
}

// Config is the resolved, immutable effective configuration.
type Config struct {
	Extends string

	Types     []CommitType
	Scope     ScopeConfig
	Subject   SubjectConfig
	Body      BodyConfig
	Footers   FootersConfig
	Emoji     EmojiConfig
	Changelog ChangelogConfig
	Rules     map[string]string // rule id → severity (error|warning|off)

	History HistoryConfig
	Stats   StatsConfig
	Serve   ServeConfig

	// provenance records the winning source per key path.
	provenance map[string]Provenance

	// RepoConfigPath is the path of the repo config that was loaded, if any.
	RepoConfigPath string
	// UserConfigPath is the path of the user config, if it existed.
	UserConfigPath string
	// Warnings collected during load (unknown keys, user-only keys in repo).
	Warnings []string
}

type CommitType struct {
	Name            string
	Description     string
	Emoji           string
	Changelog       string // heading; "false" hides the type
	ChangelogHidden bool
	BumpsMinor      bool
	Hidden          bool
}

type ScopeConfig struct {
	Mode     string // list | free | auto | off
	Required bool
	Values   []Scope
	Auto     []ScopeMapping
}

type Scope struct {
	Name        string
	Description string
}

type ScopeMapping struct {
	Glob       string
	Scope      string
	Precedence int // first match wins, most specific first
}

type SubjectConfig struct {
	MaxLength            int
	MinLength            int
	Case                 string // any | lower | sentence
	ForbidTrailingPeriod bool
}

type BodyConfig struct {
	Ask         bool
	Wrap        int
	RequiredFor []string
}

type FooterKey struct {
	Token     string
	Separator string // ": " or " #"
}

type FootersConfig struct {
	Ask                      bool
	Keys                     []FooterKey
	BreakingNeedsDescription bool
}

type EmojiConfig struct {
	Enabled  bool
	Position string // prefix | after-type
}

type ChangelogConfig struct {
	GroupBreakingFirst bool
	IncludeMerges      bool
	LinkCommits        bool
	RepoURL            string
}

type HistoryConfig struct {
	Enabled    bool
	MaxEntries int
	StorePath  string
}

type StatsConfig struct {
	Enabled          bool
	CountFromHook    bool
	RetentionDays    int
	CompactThreshold int
}

type ServeConfig struct {
	Port  int
	Host  string
	Open  bool
	Theme string
}

// userOnlySections are ignored (with a warning) when they appear in a repo
// config: a repository must not be able to change how its contributors
// record or expose personal cross-repo activity.
var userOnlySections = map[string]bool{
	"history": true,
	"stats":   true,
	"serve":   true,
}

// TypeNames returns the names of all types, in declaration order.
func (c *Config) TypeNames() []string {
	out := make([]string, len(c.Types))
	for i, t := range c.Types {
		out[i] = t.Name
	}
	return out
}

// FindType returns the type with the given name, or nil.
func (c *Config) FindType(name string) *CommitType {
	for i := range c.Types {
		if c.Types[i].Name == name {
			return &c.Types[i]
		}
	}
	return nil
}

// VisibleTypes are those not marked hidden — offered in the picker.
func (c *Config) VisibleTypes() []CommitType {
	var out []CommitType
	for _, t := range c.Types {
		if !t.Hidden {
			out = append(out, t)
		}
	}
	return out
}

// ScopeNames returns the names of known scopes.
func (c *Config) ScopeNames() []string {
	out := make([]string, len(c.Scope.Values))
	for i, s := range c.Scope.Values {
		out[i] = s.Name
	}
	return out
}

// UserOnlyKey reports whether a dotted key path is user-level only.
func UserOnlyKey(key string) bool {
	first := key
	if i := strings.IndexByte(key, '.'); i >= 0 {
		first = key[:i]
	}
	return userOnlySections[first]
}

// DetectScope matches staged paths against scope.auto globs. Globs are
// evaluated most-specific-first (longest glob wins) and the first match
// decides. Returns the matched scope, or ("", false) when nothing matches.
// When paths map to two different scopes the returned scope is the most
// specific match and ambiguous is true.
func (c *Config) DetectScope(paths []string) (scope string, ambiguous bool, matched bool) {
	if c.Scope.Mode != "auto" || len(c.Scope.Auto) == 0 {
		return "", false, false
	}
	mappings := append([]ScopeMapping(nil), c.Scope.Auto...)
	for i := range mappings {
		mappings[i].Precedence = globSpecificity(mappings[i].Glob)
	}
	sort.SliceStable(mappings, func(a, b int) bool { return mappings[a].Precedence > mappings[b].Precedence })

	scopes := map[string]bool{}
	for _, p := range paths {
		for _, m := range mappings {
			if ok, err := filepath.Match(m.Glob, p); err == nil && ok {
				scopes[m.Scope] = true
				break
			}
		}
	}
	if len(scopes) == 0 {
		return "", false, false
	}
	if len(scopes) > 1 {
		return "", true, true
	}
	for s := range scopes {
		return s, false, true
	}
	return "", false, false
}

// globSpecificity scores a glob: more characters and more path separators
// make it more specific, so a longer match wins ties.
func globSpecificity(g string) int {
	score := len(g)
	for _, r := range g {
		if r == '/' {
			score += 10
		}
	}
	return score
}

// Provenance returns the recorded provenance for a dotted key path, or nil.
func (c *Config) Provenance(key string) *Provenance {
	if c.provenance == nil {
		return nil
	}
	if p, ok := c.provenance[key]; ok {
		cp := p
		return &cp
	}
	return nil
}

// AllProvenance returns every recorded provenance, sorted by path.
func (c *Config) AllProvenance() []Provenance {
	keys := make([]string, 0, len(c.provenance))
	for k := range c.provenance {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Provenance, 0, len(keys))
	for _, k := range keys {
		out = append(out, c.provenance[k])
	}
	return out
}

// LoadOptions carries the flag-level inputs to config resolution.
type LoadOptions struct {
	// ConfigPath forces a specific config file (highest file source).
	ConfigPath string
	// Env allows injecting the environment for tests.
	Env func(string) string
	// Cwd overrides the working directory (repo config discovery).
	Cwd string
	// FlagOverrides are command-flag values applied above env, keyed by
	// dotted path (e.g. "subject.max_length").
	FlagOverrides map[string]any
}

func (o LoadOptions) env(key string) string {
	if o.Env != nil {
		return o.Env(key)
	}
	return os.Getenv(key)
}

func (o LoadOptions) cwd() string {
	if o.Cwd != "" {
		return o.Cwd
	}
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return wd
}

// defaultConfig returns the built-in defaults as a nested map plus the
// structural defaults used when keys are absent.
func defaultConfig() *Config {
	return &Config{
		Extends: "default",
		Types: []CommitType{
			{Name: "feat", Description: "A new feature", Changelog: "Features", BumpsMinor: true},
			{Name: "fix", Description: "A bug fix", Changelog: "Bug Fixes"},
			{Name: "docs", Description: "Documentation only", Changelog: "Documentation"},
			{Name: "style", Description: "Changes that do not affect the meaning of the code", Changelog: "Styles"},
			{Name: "refactor", Description: "A code change that neither fixes a bug nor adds a feature", Changelog: "Code Refactoring"},
			{Name: "perf", Description: "A code change that improves performance", Changelog: "Performance Improvements"},
			{Name: "test", Description: "Adding missing tests or correcting existing tests", Changelog: "Tests"},
			{Name: "build", Description: "Changes that affect the build system or external dependencies", Changelog: "Build System"},
			{Name: "ci", Description: "Changes to our CI configuration files and scripts", Changelog: "Continuous Integration"},
			{Name: "chore", Description: "Other changes that don't modify src or test", Changelog: "Chores"},
			{Name: "revert", Description: "Reverts a previous commit", Changelog: "Reverts"},
		},
		Scope: ScopeConfig{
			Mode:     "list",
			Required: false,
		},
		Subject: SubjectConfig{
			MaxLength:            72,
			MinLength:            1,
			Case:                 "any",
			ForbidTrailingPeriod: true,
		},
		Body: BodyConfig{
			Ask:  true,
			Wrap: 72,
		},
		Footers: FootersConfig{
			Ask:                      true,
			Keys:                     []FooterKey{{Token: "Refs", Separator: " #"}, {Token: "Closes", Separator: " #"}, {Token: "Reviewed-by", Separator: ": "}},
			BreakingNeedsDescription: true,
		},
		Emoji: EmojiConfig{
			Enabled:  false,
			Position: "after-type",
		},
		Changelog: ChangelogConfig{
			GroupBreakingFirst: true,
			IncludeMerges:      false,
			LinkCommits:        true,
		},
		Rules: map[string]string{},
		History: HistoryConfig{
			Enabled:    true,
			MaxEntries: 100,
		},
		Stats: StatsConfig{
			Enabled:          true,
			CountFromHook:    true,
			RetentionDays:    730,
			CompactThreshold: 2000,
		},
		Serve: ServeConfig{
			Port:  7378,
			Host:  "127.0.0.1",
			Theme: "auto",
		},
		provenance: map[string]Provenance{},
	}
}

// Load resolves the effective configuration. It never fails on unknown keys
// (they warn); it fails with a wrapped error on malformed YAML or a forced
// config path that doesn't exist.
func Load(opts LoadOptions) (*Config, error) {
	cfg := defaultConfig()

	// Register defaults provenance lazily — see recordDefault.
	registerDefaults(cfg)

	// Repo config: nearest .commitly.yaml walking up from cwd to repo root.
	repoPath := findRepoConfig(opts.cwd())
	if repoPath != "" {
		repoCfg, lineMap, err := loadYAMLFile(repoPath)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid YAML\n\n  %v\n\nFix it, or bypass it temporarily:\n  commitly commit --config /dev/null", repoPath, err)
		}
		cfg.RepoConfigPath = repoPath
		applyFile(cfg, repoCfg, lineMap, SrcRepo, repoPath)
	}

	// User config.
	userPath := userConfigPath()
	cfg.UserConfigPath = userPath
	if fi, err := os.Stat(userPath); err == nil && !fi.IsDir() {
		userCfg, lineMap, err := loadYAMLFile(userPath)
		if err != nil {
			return nil, fmt.Errorf("%s is not valid YAML\n\n  %v", userPath, err)
		}
		applyFile(cfg, userCfg, lineMap, SrcUser, userPath)
	}

	// Environment: COMMITLY_* with __ for nesting.
	applyEnv(cfg, opts)

	// Flags (highest file-adjacent source).
	if opts.FlagOverrides != nil {
		applyOverrides(cfg, opts.FlagOverrides)
	}

	cfg.validateAndWarn()
	return cfg, nil
}

// registerDefaults marks every built-in default as default-source
// provenance so config get can report "built-in default".
func registerDefaults(cfg *Config) {
	_ = cfg
	// Values default to SrcDefault only if nothing higher sets them; the
	// final provenance pass fills gaps in finish().
}

// applyFile merges one YAML file's contents into the effective config.
func applyFile(cfg *Config, data map[string]any, lineMap map[string]int, src Source, path string) {
	for section := range userOnlySections {
		if _, ok := data[section]; ok && src == SrcRepo {
			if line, ok := lineMap[section]; ok {
				cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s sets a user-level key, ignored: %s (line %d)", path, section, line))
			} else {
				cfg.Warnings = append(cfg.Warnings, fmt.Sprintf("%s sets a user-level key, ignored: %s", path, section))
			}
			// Remove user-only sections from repo config.
			delete(data, section)
		}
	}

	// extends semantics: "default" merges repo types onto the built-ins;
	// absence replaces the list entirely (PRD Q2).
	extendsDefault := false
	if ev, ok := data["extends"].(string); ok {
		cfg.Extends = ev
		extendsDefault = ev == "default"
	}

	for k, v := range data {
		if k == "version" || k == "extends" {
			continue
		}
		if k == "types" && extendsDefault && len(cfg.Types) > 0 {
			merged := cfg.Types
			items, _ := v.([]any)
			for _, it := range items {
				m, ok := it.(map[string]any)
				if !ok {
					continue
				}
				name := str(m["name"])
				t := CommitType{
					Name:        name,
					Description: str(m["description"]),
					Emoji:       str(m["emoji"]),
				}
				switch ch := m["changelog"].(type) {
				case bool:
					t.ChangelogHidden = !ch
				case string:
					t.Changelog = ch
				}
				if b, ok := m["bumps"].(string); ok && strings.EqualFold(b, "minor") {
					t.BumpsMinor = true
				}
				if b, ok := m["hidden"].(bool); ok {
					t.Hidden = b
				}
				replaced := false
				for i := range merged {
					if merged[i].Name == name {
						merged[i] = t
						replaced = true
						break
					}
				}
				if !replaced {
					merged = append(merged, t)
				}
			}
			cfg.Types = merged
			cfg.provenance["types"] = Provenance{Source: src, Path: "types", Line: lineMap["types"], Value: len(cfg.Types)}
			continue
		}
		applyKey(cfg, k, v, src, lineMap[k])
	}
}

// applyKey sets one top-level section from a decoded map.
func applyKey(cfg *Config, key string, v any, src Source, line int) {
	setProvenance := func(path string, val any) {
		cfg.provenance[path] = Provenance{Source: src, Path: path, Line: line, Value: val}
	}

	switch key {
	case "types":
		items, ok := v.([]any)
		if !ok {
			return
		}
		var types []CommitType
		for _, it := range items {
			m, ok := it.(map[string]any)
			if !ok {
				continue
			}
			t := CommitType{
				Name:        str(m["name"]),
				Description: str(m["description"]),
				Emoji:       str(m["emoji"]),
			}
			switch ch := m["changelog"].(type) {
			case bool:
				t.ChangelogHidden = !ch
			case string:
				t.Changelog = ch
			}
			if b, ok := m["bumps"].(string); ok && strings.EqualFold(b, "minor") {
				t.BumpsMinor = true
			}
			if b, ok := m["hidden"].(bool); ok {
				t.Hidden = b
			}
			types = append(types, t)
		}
		cfg.Types = types
		setProvenance("types", len(types))
	case "scope":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if s, ok := m["mode"].(string); ok {
			cfg.Scope.Mode = s
			setProvenance("scope.mode", s)
		}
		if b, ok := m["required"].(bool); ok {
			cfg.Scope.Required = b
			setProvenance("scope.required", b)
		}
		if vals, ok := m["values"].([]any); ok {
			var scopes []Scope
			for _, it := range vals {
				if s, ok := it.(string); ok {
					scopes = append(scopes, Scope{Name: s})
				} else if sm, ok := it.(map[string]any); ok {
					scopes = append(scopes, Scope{Name: str(sm["name"]), Description: str(sm["description"])})
				}
			}
			cfg.Scope.Values = scopes
			setProvenance("scope.values", len(scopes))
		}
		if autos, ok := m["auto"].([]any); ok {
			var mappings []ScopeMapping
			for i, it := range autos {
				am, ok := it.(map[string]any)
				if !ok {
					continue
				}
				mappings = append(mappings, ScopeMapping{Glob: str(am["glob"]), Scope: str(am["scope"]), Precedence: i})
			}
			cfg.Scope.Auto = mappings
			setProvenance("scope.auto", len(mappings))
		}
	case "subject":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if i, ok := m["max_length"].(int); ok {
			cfg.Subject.MaxLength = i
			setProvenance("subject.max_length", i)
		}
		if i, ok := m["min_length"].(int); ok {
			cfg.Subject.MinLength = i
			setProvenance("subject.min_length", i)
		}
		if s, ok := m["case"].(string); ok {
			cfg.Subject.Case = s
			setProvenance("subject.case", s)
		}
		if b, ok := m["forbid_trailing_period"].(bool); ok {
			cfg.Subject.ForbidTrailingPeriod = b
			setProvenance("subject.forbid_trailing_period", b)
		}
	case "body":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["ask"].(bool); ok {
			cfg.Body.Ask = b
			setProvenance("body.ask", b)
		}
		if i, ok := m["wrap"].(int); ok {
			cfg.Body.Wrap = i
			setProvenance("body.wrap", i)
		}
		if r, ok := m["required_for"].([]any); ok {
			var req []string
			for _, it := range r {
				req = append(req, str(it))
			}
			cfg.Body.RequiredFor = req
			setProvenance("body.required_for", req)
		}
	case "footers":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["ask"].(bool); ok {
			cfg.Footers.Ask = b
			setProvenance("footers.ask", b)
		}
		if b, ok := m["breaking_needs_description"].(bool); ok {
			cfg.Footers.BreakingNeedsDescription = b
			setProvenance("footers.breaking_needs_description", b)
		}
		if keys, ok := m["keys"].([]any); ok {
			var out []FooterKey
			for _, it := range keys {
				km, ok := it.(map[string]any)
				if !ok {
					continue
				}
				out = append(out, FooterKey{Token: str(km["token"]), Separator: str(km["separator"])})
			}
			cfg.Footers.Keys = out
			setProvenance("footers.keys", len(out))
		}
	case "emoji":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["enabled"].(bool); ok {
			cfg.Emoji.Enabled = b
			setProvenance("emoji.enabled", b)
		}
		if s, ok := m["position"].(string); ok {
			cfg.Emoji.Position = s
			setProvenance("emoji.position", s)
		}
	case "changelog":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["group_breaking_first"].(bool); ok {
			cfg.Changelog.GroupBreakingFirst = b
			setProvenance("changelog.group_breaking_first", b)
		}
		if b, ok := m["include_merges"].(bool); ok {
			cfg.Changelog.IncludeMerges = b
			setProvenance("changelog.include_merges", b)
		}
		if b, ok := m["link_commits"].(bool); ok {
			cfg.Changelog.LinkCommits = b
			setProvenance("changelog.link_commits", b)
		}
		if s, ok := m["repo_url"].(string); ok {
			cfg.Changelog.RepoURL = s
			setProvenance("changelog.repo_url", s)
		}
	case "rules":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		for rule, sev := range m {
			cfg.Rules[rule] = str(sev)
			setProvenance("rules."+rule, str(sev))
		}
	case "history":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["enabled"].(bool); ok {
			cfg.History.Enabled = b
			setProvenance("history.enabled", b)
		}
		if i, ok := m["max_entries"].(int); ok {
			cfg.History.MaxEntries = i
			setProvenance("history.max_entries", i)
		}
		if s, ok := m["store_path"].(string); ok {
			cfg.History.StorePath = s
			setProvenance("history.store_path", s)
		}
	case "stats":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if b, ok := m["enabled"].(bool); ok {
			cfg.Stats.Enabled = b
			setProvenance("stats.enabled", b)
		}
		if b, ok := m["count_from_hook"].(bool); ok {
			cfg.Stats.CountFromHook = b
			setProvenance("stats.count_from_hook", b)
		}
		if i, ok := m["retention_days"].(int); ok {
			cfg.Stats.RetentionDays = i
			setProvenance("stats.retention_days", i)
		}
		if i, ok := m["compact_threshold"].(int); ok {
			cfg.Stats.CompactThreshold = i
			setProvenance("stats.compact_threshold", i)
		}
	case "serve":
		m, _ := v.(map[string]any)
		if m == nil {
			return
		}
		if i, ok := m["port"].(int); ok {
			cfg.Serve.Port = i
			setProvenance("serve.port", i)
		}
		if s, ok := m["host"].(string); ok {
			cfg.Serve.Host = s
			setProvenance("serve.host", s)
		}
		if b, ok := m["open"].(bool); ok {
			cfg.Serve.Open = b
			setProvenance("serve.open", b)
		}
		if s, ok := m["theme"].(string); ok {
			cfg.Serve.Theme = s
			setProvenance("serve.theme", s)
		}
	}
}

// applyEnv maps COMMITLY_FOO__BAR=1 to foo.bar.
func applyEnv(cfg *Config, opts LoadOptions) {
	prefix := "COMMITLY_"
	// Read known environment variables directly.
	known := []string{
		"SUBJECT__MAX_LENGTH", "SUBJECT__MIN_LENGTH", "SUBJECT__CASE",
		"SCOPE__MODE", "SCOPE__REQUIRED",
		"HISTORY__ENABLED", "HISTORY__MAX_ENTRIES", "HISTORY__STORE_PATH",
		"STATS__ENABLED", "STATS__COUNT_FROM_HOOK", "STATS__RETENTION_DAYS", "STATS__COMPACT_THRESHOLD",
		"SERVE__PORT", "SERVE__HOST", "SERVE__OPEN", "SERVE__THEME",
		"EMOJI__ENABLED", "EMOJI__POSITION",
		"BODY__ASK", "BODY__WRAP",
		"FOOTERS__ASK", "FOOTERS__BREAKING_NEEDS_DESCRIPTION",
		"CHANGELOG__REPO_URL", "CHANGELOG__LINK_COMMITS", "CHANGELOG__INCLUDE_MERGES",
	}
	for _, k := range known {
		val := opts.env(prefix + k)
		if val == "" {
			continue
		}
		path := strings.ToLower(strings.ReplaceAll(k, "__", "."))
		applyEnvKey(cfg, path, val, SrcEnv)
	}
}

func applyEnvKey(cfg *Config, path, val string, src Source) {
	switch path {
	case "subject.max_length":
		cfg.Subject.MaxLength = toInt(val, cfg.Subject.MaxLength)
	case "subject.min_length":
		cfg.Subject.MinLength = toInt(val, cfg.Subject.MinLength)
	case "subject.case":
		cfg.Subject.Case = val
	case "scope.mode":
		cfg.Scope.Mode = val
	case "scope.required":
		cfg.Scope.Required = toBool(val, cfg.Scope.Required)
	case "history.enabled":
		cfg.History.Enabled = toBool(val, cfg.History.Enabled)
	case "history.max_entries":
		cfg.History.MaxEntries = toInt(val, cfg.History.MaxEntries)
	case "history.store_path":
		cfg.History.StorePath = val
	case "stats.enabled":
		cfg.Stats.Enabled = toBool(val, cfg.Stats.Enabled)
	case "stats.count_from_hook":
		cfg.Stats.CountFromHook = toBool(val, cfg.Stats.CountFromHook)
	case "stats.retention_days":
		cfg.Stats.RetentionDays = toInt(val, cfg.Stats.RetentionDays)
	case "stats.compact_threshold":
		cfg.Stats.CompactThreshold = toInt(val, cfg.Stats.CompactThreshold)
	case "serve.port":
		cfg.Serve.Port = toInt(val, cfg.Serve.Port)
	case "serve.host":
		cfg.Serve.Host = val
	case "serve.open":
		cfg.Serve.Open = toBool(val, cfg.Serve.Open)
	case "serve.theme":
		cfg.Serve.Theme = val
	case "emoji.enabled":
		cfg.Emoji.Enabled = toBool(val, cfg.Emoji.Enabled)
	case "emoji.position":
		cfg.Emoji.Position = val
	case "body.ask":
		cfg.Body.Ask = toBool(val, cfg.Body.Ask)
	case "body.wrap":
		cfg.Body.Wrap = toInt(val, cfg.Body.Wrap)
	case "footers.ask":
		cfg.Footers.Ask = toBool(val, cfg.Footers.Ask)
	case "footers.breaking_needs_description":
		cfg.Footers.BreakingNeedsDescription = toBool(val, cfg.Footers.BreakingNeedsDescription)
	case "changelog.repo_url":
		cfg.Changelog.RepoURL = val
	case "changelog.link_commits":
		cfg.Changelog.LinkCommits = toBool(val, cfg.Changelog.LinkCommits)
	case "changelog.include_merges":
		cfg.Changelog.IncludeMerges = toBool(val, cfg.Changelog.IncludeMerges)
	}
	if cfg.provenance != nil {
		cfg.provenance[path] = Provenance{Source: src, Path: path, Value: val}
	}
}

func applyOverrides(cfg *Config, overrides map[string]any) {
	for path, v := range overrides {
		switch path {
		case "serve.port":
			if i, ok := v.(int); ok {
				cfg.Serve.Port = i
			}
		case "serve.host":
			if s, ok := v.(string); ok {
				cfg.Serve.Host = s
			}
		case "serve.open":
			if b, ok := v.(bool); ok {
				cfg.Serve.Open = b
			}
		}
		cfg.provenance[path] = Provenance{Source: SrcFlag, Path: path, Value: v}
	}
}

func (c *Config) validateAndWarn() {
	// Fill provenance for keys never explicitly set so `get` can report
	// built-in defaults truthfully.
	for _, path := range allKeys() {
		if _, ok := c.provenance[path]; !ok {
			c.provenance[path] = Provenance{Source: SrcDefault, Path: path}
		}
	}
	if c.Subject.MaxLength <= c.Subject.MinLength {
		c.Warnings = append(c.Warnings, "subject.max_length must be greater than subject.min_length; ignoring configured values")
	}
}

// allKeys lists every resolvable dotted key path.
func allKeys() []string {
	return []string{
		"types", "scope.mode", "scope.required", "scope.values", "scope.auto",
		"subject.max_length", "subject.min_length", "subject.case", "subject.forbid_trailing_period",
		"body.ask", "body.wrap", "body.required_for",
		"footers.ask", "footers.keys", "footers.breaking_needs_description",
		"emoji.enabled", "emoji.position",
		"changelog.group_breaking_first", "changelog.include_merges", "changelog.link_commits", "changelog.repo_url",
		"history.enabled", "history.max_entries", "history.store_path",
		"stats.enabled", "stats.count_from_hook", "stats.retention_days", "stats.compact_threshold",
		"serve.port", "serve.host", "serve.open", "serve.theme",
	}
}

// findRepoConfig walks up from cwd to the filesystem root looking for the
// nearest .commitly.yaml.
func findRepoConfig(start string) string {
	dir := start
	for {
		candidate := filepath.Join(dir, ".commitly.yaml")
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// userConfigPath returns the user config path per platform convention.
func userConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "commitly", "config.yaml")
	}
	if os.Getenv("APPDATA") != "" {
		return filepath.Join(os.Getenv("APPDATA"), "commitly", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.yaml"
	}
	return filepath.Join(home, ".config", "commitly", "config.yaml")
}

// StateDir resolves the XDG state dir for the two stores.
func StateDir() string {
	if os.Getenv("XDG_STATE_HOME") != "" {
		return filepath.Join(os.Getenv("XDG_STATE_HOME"), "commitly")
	}
	if os.Getenv("LOCALAPPDATA") != "" {
		return filepath.Join(os.Getenv("LOCALAPPDATA"), "commitly")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "state", "commitly")
}

// ScopeValuesFromFile reads just the scope.values names from one config file
// (no merging, no precedence) — used when appending a scope to a specific
// file without pulling in the repo/user/default chain.
func ScopeValuesFromFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = *node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != "scope" {
			continue
		}
		scopeNode := node.Content[i+1]
		if scopeNode.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(scopeNode.Content); j += 2 {
			if scopeNode.Content[j].Value != "values" {
				continue
			}
			vals := scopeNode.Content[j+1]
			if vals.Kind != yaml.SequenceNode {
				continue
			}
			var names []string
			for _, v := range vals.Content {
				if v.Kind == yaml.ScalarNode {
					names = append(names, v.Value)
				} else if v.Kind == yaml.MappingNode {
					for k := 0; k+1 < len(v.Content); k += 2 {
						if v.Content[k].Value == "name" {
							names = append(names, v.Content[k+1].Value)
						}
					}
				}
			}
			return names, nil
		}
	}
	return nil, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func toInt(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
		return n
	}
	return def
}

func toBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return def
}
