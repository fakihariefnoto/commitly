package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	ccommit "github.com/fakihariefnoto/commitly/internal/commit"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/git"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/render"
)

func interactiveAllowed(caps *render.Caps) bool {
	return caps.StdoutTTY && !caps.NoTUI
}

func previousMessage(ctx context.Context) (string, error) {
	out, _, err := git.Run(ctx, "log", "-1", "--format=%B")
	return out, err
}

func scopeKnown(cfg *config.Config, scope string) bool {
	for _, s := range cfg.Scope.Values {
		if s.Name == scope {
			return true
		}
	}
	return false
}

func splitFooterFlag(f string) (token, value string) {
	if i := strings.Index(f, ": "); i > 0 {
		return f[:i], strings.TrimSpace(f[i+2:])
	}
	if i := strings.Index(f, " #"); i > 0 {
		return f[:i], strings.TrimSpace(f[i+2:])
	}
	return f, ""
}

func didYouMean(s string, known []string) string {
	best := ""
	bestDist := 1 << 30
	for _, k := range known {
		d := editDist(strings.ToLower(s), strings.ToLower(k))
		if d < bestDist {
			bestDist = d
			best = k
		}
	}
	if bestDist <= 2 && best != "" {
		return best
	}
	return strings.TrimSpace(strings.Join(known, " "))
}

func editDist(a, b string) int {
	la, lb := len(a), len(b)
	dp := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		dp[j] = j
	}
	for i := 1; i <= la; i++ {
		prev := dp[0]
		dp[0] = i
		for j := 1; j <= lb; j++ {
			tmp := dp[j]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			dp[j] = min3(dp[j]+1, dp[j-1]+1, prev+cost)
			prev = tmp
		}
	}
	return dp[lb]
}

func min3(a, b, c int) int {
	if a < b && a < c {
		return a
	}
	if b < c {
		return b
	}
	return c
}

func draftPath(root string) string {
	return filepath.Join(root, ".git", "COMMITLY_DRAFT")
}

func offerDraft(ctx context.Context, root string) error {
	path := draftPath(root)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	// A draft with no type or no subject is junk (an abort before anything
	// was typed) — discard it silently rather than offering it.
	pr := ccommit.Parse(string(data))
	if !pr.OK || pr.Message.Type == "" || pr.Message.Subject == "" {
		os.Remove(path)
		return nil
	}
	render.Note("A saved draft was found:")
	render.Note("")
	render.Note("  %s", firstSubject(string(data)))
	render.Note("")
	render.Note("Restore it? [Y/n/d]  (d = discard and start fresh)")
	answer, err := readAnswer()
	if err != nil || strings.ToLower(strings.TrimSpace(answer)) == "d" {
		os.Remove(path)
		return nil
	}
	if answer == "" || strings.EqualFold(answer, "y") {
		commitFlags.typ, commitFlags.message = draftFields(string(data))
	}
	return nil
}

func draftFields(raw string) (typ, subject string) {
	pr := ccommit.Parse(raw)
	if pr.OK {
		return pr.Message.Type, pr.Message.Subject
	}
	return "", ""
}

func saveDraft(ctx context.Context, root string, message, reason string) {
	path := draftPath(root)
	os.WriteFile(path, []byte(message+"\n# "+reason+"\n"), 0o600)
}

func readAnswer() (string, error) {
	var b [1]byte
	n, err := os.Stdin.Read(b[:])
	if err != nil || n == 0 {
		return "", err
	}
	var rest strings.Builder
	one := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(one[:])
		if n > 0 {
			if one[0] == '\n' {
				break
			}
			rest.WriteByte(one[0])
		}
		if err != nil {
			break
		}
	}
	return strings.TrimSpace(string(b[0]) + rest.String()), nil
}

// recordEntry appends the history entry and counter (AD-11, best-effort).
func recordEntry(ctx context.Context, cfg *config.Config, root string, res *git.CommitResult, m ccommit.CommitMessage, files []string) {
	if !cfg.History.Enabled {
		return
	}
	key := git.RepoKey(ctx, root)
	remote := git.RemoteURL(ctx)
	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}
	entry := history.NewEntry()
	entry.RepoKey = key
	entry.RepoName = git.RepoName(root)
	entry.Path = root
	entry.RemoteURL = remote
	entry.HostKind = history.HostKind(remote)
	entry.SHA = res.SHA
	entry.ShortSHA = res.ShortSHA
	entry.Branch = res.Branch
	entry.Type = m.Type
	entry.Scope = m.Scope
	entry.Breaking = m.Breaking
	entry.Subject = m.Subject
	entry.HasBody = m.HasBody()
	entry.FilesChanged = res.FilesChanged
	entry.Insertions = res.Insertions
	entry.Deletions = res.Deletions
	entry.CommittedAt = time.Now()
	entry.CommitlyVersion = version

	st := history.OpenEntryStore(dir, cfg.History.MaxEntries)
	_ = st.Append(entry)

	if cfg.Stats.Enabled {
		row := &history.CounterRow{
			Date:     time.Now().Format("2006-01-02"),
			RepoKey:  key,
			Source:   history.SrcCM,
			Type:     m.Type,
			Commits:  1,
			Breaking: b2i(m.Breaking),
			WithBody: b2i(m.HasBody()),
		}
		if m.Subject != "" {
			row.SubjectLenSum = len(m.Subject)
			row.SubjectHist = make([]int, 20)
			row.SubjectHist[histIndex(len(m.Subject))] = 1
		}
		row.Insertions = res.Insertions
		row.Deletions = res.Deletions
		cs := history.OpenCounterStore(dir, cfg.Stats.RetentionDays, cfg.Stats.CompactThreshold)
		_ = cs.Append(row)
	}
}
