package git

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestStripCredentials(t *testing.T) {
	cases := []struct{ in, want string }{
		{"https://github.com/fakihariefnoto/commitly.git", "https://github.com/fakihariefnoto/commitly.git"},
		{"https://user:ghp_xxx@github.com/org/repo.git", "https://github.com/org/repo.git"},
		{"git@github.com:org/repo.git", "git@github.com:org/repo.git"},
	}
	for _, c := range cases {
		if got := StripCredentials(c.in); got != c.want {
			t.Errorf("StripCredentials(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseStatusV2(t *testing.T) {
	// Simulated porcelain v2 -z output: staged, unstaged, rename, untracked.
	out := "# branch.oid abc\x00" +
		"1 M. N... 100644 100644 100644 abc def a.txt\x00" +
		"1 .M N... 100644 100644 100644 abc def b.txt\x00" +
		"2 R. N... 100644 100644 100644 abc def R100 c.txt\x00old.txt\x00" +
		"? d.txt\x00"
	files := parseStatusV2(out)
	if len(files) != 4 {
		t.Fatalf("expected 4 files, got %d: %+v", len(files), files)
	}
	if !files[0].IsStaged() || files[0].Path != "a.txt" {
		t.Fatalf("a.txt: %+v", files[0])
	}
	if !files[1].IsUnstaged() || files[1].Path != "b.txt" {
		t.Fatalf("b.txt: %+v", files[1])
	}
	if files[2].Path != "c.txt" || files[2].OrigPath != "old.txt" {
		t.Fatalf("rename: %+v", files[2])
	}
	if !files[3].Untracked || files[3].Path != "d.txt" {
		t.Fatalf("untracked: %+v", files[3])
	}
}

func TestParseStatusV2PartiallyStaged(t *testing.T) {
	// A file staged AND modified → two rows, never flattened.
	out := "1 M. N... 100644 100644 100644 abc def f.go\x00" +
		"1 .M N... 100644 100644 100644 abc def f.go\x00"
	files := parseStatusV2(out)
	if len(files) != 2 {
		t.Fatalf("partial staging must produce two rows: %+v", files)
	}
	if !(files[0].IsStaged() && files[1].IsUnstaged()) {
		t.Fatalf("statuses not split: %+v", files)
	}
}

func TestMinGitVersion(t *testing.T) {
	if !atLeast("2.20") || !atLeast("2.45") {
		t.Fatal("atLeast should accept 2.20+")
	}
	if atLeast("2.19") || atLeast("1.9") {
		t.Fatal("atLeast should reject < 2.20")
	}
}

func atLeast(v string) bool {
	m := gitVersionRe.FindStringSubmatch("git version " + v)
	if m == nil {
		return false
	}
	major, minor := atoi(m[1]), atoi(m[2])
	if major > 2 {
		return true
	}
	if major == 2 && minor >= 20 {
		return true
	}
	return false
}

func hasGit() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func runIn(dir string, name string, args ...string) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		panic(name + " " + strings.Join(args, " ") + ": " + err.Error() + ": " + string(out))
	}
}

var _ = os.Getenv

func TestIntegrationLog(t *testing.T) {
	if !hasGit() {
		t.Skip("git not available")
	}
	ctx := context.Background()
	dir := t.TempDir()
	t.Chdir(dir)
	runIn(dir, "git", "init", "-q")
	runIn(dir, "git", "config", "user.email", "t@t.co")
	runIn(dir, "git", "config", "user.name", "T")
	runIn(dir, "git", "commit", "--allow-empty", "-m", "feat: first")
	logs, err := Log(ctx, "", "HEAD", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].RawMessage != "feat: first\n" {
		t.Fatalf("log: %+v", logs)
	}
}
