package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestDefaultsNoConfig(t *testing.T) {
	c, err := Load(LoadOptions{Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.MaxLength != 72 {
		t.Fatalf("max_length: %d", c.Subject.MaxLength)
	}
	if len(c.Types) != 11 {
		t.Fatalf("types: %d", len(c.Types))
	}
	if c.History.MaxEntries != 100 {
		t.Fatalf("max_entries: %d", c.History.MaxEntries)
	}
	if c.Serve.Port != 7378 {
		t.Fatalf("port: %d", c.Serve.Port)
	}
}

func TestRepoConfigReplacesTypesByDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "types:\n  - name: feat\n  - name: fix\n")
	c, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Types) != 2 {
		t.Fatalf("expected replacement (2 types), got %d", len(c.Types))
	}
}

func TestExtendsDefaultMergesTypes(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "extends: default\ntypes:\n  - name: deps\n")
	c, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Types) != 12 {
		t.Fatalf("expected 12 (11 + deps), got %d", len(c.Types))
	}
}

func TestPrecedenceEnvOverRepoOverDefault(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "subject:\n  max_length: 50\n")
	env := map[string]string{"COMMITLY_SUBJECT__MAX_LENGTH": "100"}
	c, err := Load(LoadOptions{
		Cwd: dir,
		Env: func(k string) string { return env[k] },
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Subject.MaxLength != 100 {
		t.Fatalf("env should win: %d", c.Subject.MaxLength)
	}

	c2, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if c2.Subject.MaxLength != 50 {
		t.Fatalf("repo should beat default: %d", c2.Subject.MaxLength)
	}
}

func TestUserConfigOverDefault(t *testing.T) {
	dir := t.TempDir()
	// Simulate a user config by pointing the home env — fall back to a
	// config next to the repo is not possible; instead set XDG_CONFIG_HOME.
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "cfg"))
	write(t, filepath.Join(dir, "cfg", "commitly"), "config.yaml", "history:\n  max_entries: 250\n")
	c, err := Load(LoadOptions{Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if c.History.MaxEntries != 250 {
		t.Fatalf("user config should win: %d", c.History.MaxEntries)
	}
}

func TestUserOnlyKeysIgnoredInRepoConfig(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "stats:\n  count_from_hook: false\nserve:\n  port: 9000\n")
	c, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Stats.CountFromHook {
		t.Fatal("repo config must not disable hook counting")
	}
	if c.Serve.Port != 7378 {
		t.Fatal("repo config must not change serve port")
	}
	if len(c.Warnings) == 0 {
		t.Fatal("expected warnings about ignored user-only keys")
	}
}

func TestUnknownKeysWarnNotError(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "subject:\n  max_length: 60\n  future_key: 1\n")
	c, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err != nil {
		t.Fatal("unknown keys must not error")
	}
	if c.Subject.MaxLength != 60 {
		t.Fatalf("known key should still load: %d", c.Subject.MaxLength)
	}
}

func TestMalformedYAMLErrors(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".commitly.yaml", "subject:\n  max_length: : bad\n")
	_, err := Load(LoadOptions{Cwd: dir, Env: func(string) string { return "" }})
	if err == nil {
		t.Fatal("malformed YAML must error")
	}
}

func TestDetectScope(t *testing.T) {
	c := &Config{}
	c.Scope.Mode = "auto"
	c.Scope.Auto = []ScopeMapping{
		{Glob: "internal/tui/**", Scope: "tui"},
		{Glob: "internal/git/**", Scope: "git"},
		{Glob: "go.mod", Scope: "deps"},
	}
	if s, amb, ok := c.DetectScope([]string{"internal/tui/form.go"}); !ok || amb || s != "tui" {
		t.Fatalf("expected tui: %q %v %v", s, amb, ok)
	}
	if _, amb, ok := c.DetectScope([]string{"internal/tui/a.go", "internal/git/b.go"}); !ok || !amb {
		t.Fatal("expected ambiguity")
	}
	if _, _, ok := c.DetectScope([]string{"README.md"}); ok {
		t.Fatal("no match expected")
	}
}
