package git

import (
	"context"
	"strconv"
	"strings"
)

// FileChange is one path in the working tree or index. IndexStatus and
// WorktreeStatus are kept separate — a partially-staged file (staged edits
// plus further unstaged edits) is two rows, never flattened.
type FileChange struct {
	Path           string
	OrigPath       string // set for renames
	IndexStatus    string
	WorktreeStatus string
	Untracked      bool
	Additions      int // -1 for binary
	Deletions      int // -1 for binary
	Selected       bool
}

// IsStaged reports whether the index side of this row has a change.
func (f FileChange) IsStaged() bool { return f.IndexStatus != "" && f.IndexStatus != "." }

// IsUnstaged reports whether the worktree side has a change.
func (f FileChange) IsUnstaged() bool { return f.WorktreeStatus != "" && f.WorktreeStatus != "." }

// Status reads git status --porcelain=v2 -z, honoring renames.
func Status(ctx context.Context) ([]FileChange, error) {
	out, stderr, err := Run(ctx, "status", "--porcelain=v2", "--untracked-files=all", "-z")
	if err != nil {
		return nil, &CommitError{Stderr: stderr, Err: err}
	}
	files := parseStatusV2(out)
	if err := applyNumstats(ctx, files); err != nil {
		// Numstat is enrichment; a parse failure there must not hide the
		// file list.
		return files, nil
	}
	return files, nil
}

// parseStatusV2 parses porcelain v2 NUL-delimited output.
func parseStatusV2(out string) []FileChange {
	var files []FileChange
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		if rec == "" {
			continue
		}
		switch rec[0] {
		case '#':
			continue
		case '1', '2', 'u':
			fields := strings.Fields(rec)
			if len(fields) < 9 {
				continue
			}
			xy := fields[1]
			f := FileChange{
				IndexStatus:    xy[:1],
				WorktreeStatus: xy[1:2],
			}
			if rec[0] == '2' {
				// Rename: <Xscore> occupies field 8; path is field 9 and the
				// original path is the next NUL-separated record.
				if len(fields) < 10 {
					continue
				}
				f.Path = fields[9]
				if i+1 < len(records) {
					f.OrigPath = records[i+1]
					i++
				}
			} else {
				f.Path = fields[8]
			}
			files = append(files, f)
		case '?':
			if len(rec) > 1 {
				path := rec[1:]
				// porcelain v2 writes "? <path>" with a literal space.
				path = strings.TrimPrefix(path, " ")
				files = append(files, FileChange{Path: path, Untracked: true})
			}
		case '!':
			// ignored — never staged
		}
	}
	return files
}

// applyNumstats enriches files with +/- counts from the index and worktree.
func applyNumstats(ctx context.Context, files []FileChange) error {
	staged := numstat(ctx, "diff", "--cached", "--numstat")
	unstaged := numstat(ctx, "diff", "--numstat")
	for i := range files {
		if s, ok := staged[files[i].Path]; ok {
			files[i].Additions = s.adds
			files[i].Deletions = s.dels
		}
		if s, ok := unstaged[files[i].Path]; ok {
			if files[i].Additions == 0 && files[i].Deletions == 0 && !files[i].IsStaged() {
				files[i].Additions = s.adds
				files[i].Deletions = s.dels
			}
		}
	}
	return nil
}

type numstatRow struct{ adds, dels int }

func numstat(ctx context.Context, args ...string) map[string]numstatRow {
	out, _, err := Run(ctx, args...)
	if err != nil {
		return nil
	}
	m := map[string]numstatRow{}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) < 3 {
			continue
		}
		path := parts[len(parts)-1]
		adds, err1 := strconv.Atoi(parts[0])
		dels, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			m[path] = numstatRow{adds: -1, dels: -1}
			continue
		}
		m[path] = numstatRow{adds: adds, dels: dels}
	}
	return m
}
