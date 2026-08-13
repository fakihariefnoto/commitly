// Package git is the single choke point for every git subprocess (AD-2).
// Nothing outside this package may exec git. All repository reads and
// writes go through the user's git binary using machine-readable formats.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MinimumGitVersion is the floor recorded in TRD.md (I1).
const MinimumGitVersion = "2.20"

// Verbose reports git command lines to stderr. Set by the CLI from --verbose.
var Verbose = func(format string, args ...any) {}

// Run executes a git subprocess with a per-call deadline, capturing stdout
// and stderr separately. The command line is echoed under Verbose.
func Run(ctx context.Context, args ...string) (stdout, stderr string, err error) {
	full := append([]string{"-c", "core.quotePath=false"}, args...)
	Verbose("git %s", strings.Join(full, " "))

	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", full...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()
	Verbose("  exit=%d", cmd.ProcessState.ExitCode())
	return stdout, stderr, err
}

var gitVersionRe = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// CheckVersion verifies the installed git is at or above MinimumGitVersion.
func CheckVersion(ctx context.Context) error {
	out, _, err := Run(ctx, "--version")
	if err != nil {
		return fmt.Errorf("could not run git: %v", err)
	}
	m := gitVersionRe.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return fmt.Errorf("unrecognized git version output: %q", out)
	}
	major, minor := atoi(m[1]), atoi(m[2])
	if major > 2 || (major == 2 && minor >= 20) {
		return nil
	}
	return fmt.Errorf("git %d.%d is too old — commitly needs at least %s", major, minor, MinimumGitVersion)
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// Root returns the repository top-level path.
func Root(ctx context.Context) (string, error) {
	out, stderr, err := Run(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(stderr, "not a git repository") || strings.Contains(stderr, "not a git repository") {
			return "", ErrNotARepo
		}
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ErrNotARepo is returned when the working directory is outside any repo.
var ErrNotARepo = errors.New("not a git repository (or any parent up to /)")

// IsInsideWorkTree reports whether cwd is inside a work tree.
func IsInsideWorkTree(ctx context.Context) bool {
	out, _, err := Run(ctx, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// HooksDir resolves the effective hooks directory via rev-parse --git-path,
// honoring core.hooksPath, worktrees and submodules.
func HooksDir(ctx context.Context) (string, error) {
	out, _, err := Run(ctx, "rev-parse", "--git-path", "hooks")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// RepoKey returns a stable repo identity: the root-commit SHA, falling back
// to the absolute path when the repo has no commits yet.
func RepoKey(ctx context.Context, root string) string {
	out, _, err := Run(ctx, "rev-list", "--max-parents=0", "HEAD")
	if err == nil {
		lines := strings.Fields(out)
		if len(lines) > 0 {
			return lines[len(lines)-1]
		}
	}
	return root
}

// RepoName returns the basename of the repo root.
func RepoName(root string) string {
	root = strings.TrimRight(root, "/")
	if i := strings.LastIndexByte(root, '/'); i >= 0 {
		return root[i+1:]
	}
	return root
}

// UserEmail returns the configured user.email.
func UserEmail(ctx context.Context) string {
	out, _, err := Run(ctx, "config", "--get", "user.email")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// RemoteURL returns the normalized, credential-stripped origin remote URL.
func RemoteURL(ctx context.Context) string {
	out, _, err := Run(ctx, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return StripCredentials(strings.TrimSpace(out))
}

var credRe = regexp.MustCompile(`(://)([^/@]+)@`)

// StripCredentials removes any userinfo from a remote URL so a token
// embedded in a remote never reaches a plaintext store.
func StripCredentials(remote string) string {
	if strings.HasPrefix(remote, "http://") || strings.HasPrefix(remote, "https://") {
		if u, err := url.Parse(remote); err == nil {
			if u.User != nil {
				u.User = nil
				return u.String()
			}
		}
	}
	return remote
}

// MostRecentTag returns the most recent tag reachable from HEAD, or "".
func MostRecentTag(ctx context.Context) string {
	out, _, err := Run(ctx, "describe", "--tags", "--abbrev=0")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// CommitResult is the parsed output of a successful git commit.
type CommitResult struct {
	SHA          string
	ShortSHA     string
	Branch       string
	FilesChanged int
	Insertions   int
	Deletions    int
}

// Commit runs git commit with the message from stdin, plus passthrough flags.
func Commit(ctx context.Context, message string, args ...string) (*CommitResult, error) {
	cmdArgs := append([]string{"commit", "-F", "-"}, args...)
	full := append([]string{"-c", "core.quotePath=false"}, cmdArgs...)
	Verbose("git %s", strings.Join(full, " "))

	cctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(cctx, "git", full...)
	cmd.Stdin = strings.NewReader(message)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	if err != nil {
		return nil, &CommitError{Stdout: outBuf.String(), Stderr: errBuf.String(), Err: err}
	}

	res := parseCommitOutput(outBuf.String())
	return res, nil
}

// CommitError carries git's raw output for the caller to surface. The
// message may be on stdout (e.g. "nothing to commit") or stderr (a hook
// rejection), so both are kept.
type CommitError struct {
	Stdout string
	Stderr string
	Err    error
}

func (e *CommitError) Error() string {
	return e.Err.Error()
}

func (e *CommitError) Unwrap() error { return e.Err }

// parseCommitOutput extracts SHA, branch and short-stat from git commit output.
var (
	shaRe       = regexp.MustCompile(`\[([^\]]+)\s+([0-9a-f]{7,40})\]`)
	shortStatRe = regexp.MustCompile(`(\d+) files? changed`)
	insRe       = regexp.MustCompile(`(\d+) insertions?`)
	delRe       = regexp.MustCompile(`(\d+) deletions?`)
)

func parseCommitOutput(out string) *CommitResult {
	res := &CommitResult{}
	if m := shaRe.FindStringSubmatch(out); m != nil {
		res.Branch = m[1]
		res.ShortSHA = m[2]
	}
	if m := shortStatRe.FindStringSubmatch(out); m != nil {
		res.FilesChanged = atoi(m[1])
	}
	if m := insRe.FindStringSubmatch(out); m != nil {
		res.Insertions = atoi(m[1])
	}
	if m := delRe.FindStringSubmatch(out); m != nil {
		res.Deletions = atoi(m[1])
	}
	return res
}

// Add stages paths. The -- separator ensures a path starting with "-" is
// treated as a path, not a flag.
func Add(ctx context.Context, paths []string) error {
	args := []string{"add", "--"}
	args = append(args, paths...)
	_, stderr, err := Run(ctx, args...)
	if err != nil {
		return fmt.Errorf("git add failed: %s", strings.TrimSpace(stderr))
	}
	return nil
}

// CommitLog is one record read from git log.
type CommitLog struct {
	SHA         string
	ShortSHA    string
	AuthorName  string
	AuthorEmail string
	CommittedAt time.Time
	RawMessage  string
	IsMerge     bool
}

// Log reads commits in a range with NUL-separated fields. Bodies contain
// newlines and arbitrary text, so any line-delimited format is wrong.
func Log(ctx context.Context, since, until string, includeMerges bool) ([]CommitLog, error) {
	format := "%H%x00%h%x00%an%x00%ae%x00%aI%x00%s%x00%B%x00%P"
	args := []string{"log", "--format=" + format, "-z"}
	if !includeMerges {
		args = append(args, "--no-merges")
	}
	rangeSpec := until
	if since != "" {
		rangeSpec = since + ".." + until
	}
	args = append(args, rangeSpec)

	out, stderr, err := Run(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("git log failed: %s", strings.TrimSpace(stderr))
	}

	var logs []CommitLog
	fields := strings.Split(out, "\x00")
	for i := 0; i+6 < len(fields); i += 8 {
		cl := CommitLog{
			SHA:         fields[i],
			ShortSHA:    fields[i+1],
			AuthorName:  fields[i+2],
			AuthorEmail: fields[i+3],
		}
		if t, err := time.Parse(time.RFC3339, fields[i+4]); err == nil {
			cl.CommittedAt = t
		}
		// %B is the full raw message (subject + body), which is what lint
		// and changelog consume.
		cl.RawMessage = fields[i+6]
		if len(fields) >= i+8 && strings.Contains(fields[i+7], " ") {
			cl.IsMerge = true
		}
		logs = append(logs, cl)
	}
	sort.SliceStable(logs, func(i, j int) bool { return logs[i].CommittedAt.Before(logs[j].CommittedAt) })
	return logs, nil
}

// HasTag reports whether a ref exists.
func RefExists(ctx context.Context, ref string) bool {
	_, _, err := Run(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// AvailableTags lists tags, newest first.
func AvailableTags(ctx context.Context) []string {
	out, _, err := Run(ctx, "tag", "--sort=-v:refname")
	if err != nil {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(strings.TrimSpace(out), "\n") {
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}
