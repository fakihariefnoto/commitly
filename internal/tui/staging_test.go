package tui

import (
	"testing"

	"github.com/fakihariefnoto/commitly/internal/git"
)

func testChanges() []git.FileChange {
	return []git.FileChange{
		{Path: "a.go", IndexStatus: "M", WorktreeStatus: ".", Additions: 3, Deletions: 1},
		{Path: "b.txt", Untracked: true, Additions: 2, Deletions: 0},
		{Path: "c.go", IndexStatus: ".", WorktreeStatus: "M", Additions: 1, Deletions: 1},
	}
}

func TestMark(t *testing.T) {
	cases := []struct {
		ch   git.FileChange
		want string
	}{
		{git.FileChange{Path: "a", IndexStatus: "M", WorktreeStatus: "."}, "M "},
		{git.FileChange{Path: "a", IndexStatus: ".", WorktreeStatus: "M"}, " M"},
		{git.FileChange{Path: "a", IndexStatus: "M", WorktreeStatus: "M"}, "MM"},
		{git.FileChange{Path: "a", Untracked: true}, "?"},
	}
	for _, c := range cases {
		if got := mark(c.ch); got != c.want {
			t.Errorf("mark(%+v)=%q want %q", c.ch, got, c.want)
		}
	}
}

func TestStatsBinary(t *testing.T) {
	ch := git.FileChange{Additions: -1, Deletions: -1}
	adds, dels := stats(ch)
	if adds != "bin" || dels != "bin" {
		t.Errorf("binary: %q %q", adds, dels)
	}
}
