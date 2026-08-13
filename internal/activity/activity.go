// Package activity computes the shared ActivityView consumed by both
// `commitly status` and the served page — one computation, two renderers
// (AD-3's second application). It is a pure function of entries + filters.
package activity

import (
	"os"
	"sort"
	"time"

	"github.com/fakihariefnoto/commitly/internal/history"
)

// TypeCount is a type → count pair.
type TypeCount struct {
	Type  string
	Count int
}

// RepoGroup is one repository's entries, ordered by recency.
type RepoGroup struct {
	Key        string
	Name       string
	Path       string
	PathExists bool
	Remote     string
	Count      int
	LatestAt   time.Time
	Entries    []history.Entry
}

// ActivityView is the model both renderers consume.
type ActivityView struct {
	TotalEntries   int
	RepoCount      int
	Truncated      bool
	OldestRetained time.Time
	SinceFilter    string
	RepoFilter     string
	TypeFilter     string
	Repos          []RepoGroup
	TypeCounts     []TypeCount
	GeneratedAt    time.Time
}

// Filters narrows the entry set before grouping.
type Filters struct {
	Repo    string    // name or unique prefix
	Since   time.Time // zero = all
	Type    string
	Limit   int // 0 = all
	PerRepo int // 0 = all (default caller value 5)
}

// Build computes the view. Entries are expected newest-first (as ReadAll
// returns them).
func Build(entries []history.Entry, f Filters) *ActivityView {
	v := &ActivityView{GeneratedAt: time.Now()}
	if f.Repo != "" {
		v.RepoFilter = f.Repo
	}
	if !f.Since.IsZero() {
		v.SinceFilter = f.Since.Format("2006-01-02")
	}
	if f.Type != "" {
		v.TypeFilter = f.Type
	}

	// Filter.
	filtered := make([]history.Entry, 0, len(entries))
	for _, e := range entries {
		if f.Type != "" && e.Type != f.Type {
			continue
		}
		if !f.Since.IsZero() && e.CommittedAt.Before(f.Since) {
			continue
		}
		if f.Repo != "" && !matchRepo(f.Repo, e) {
			continue
		}
		filtered = append(filtered, e)
	}

	if f.Limit > 0 && len(filtered) > f.Limit {
		filtered = filtered[:f.Limit]
	}

	// Group by repo, ordered by latest activity desc.
	groups := map[string]*RepoGroup{}
	var order []string
	for _, e := range filtered {
		g, ok := groups[e.RepoKey]
		if !ok {
			g = &RepoGroup{
				Key:    e.RepoKey,
				Name:   e.RepoName,
				Path:   e.Path,
				Remote: e.RemoteURL,
			}
			g.PathExists = pathExists(e.Path)
			groups[e.RepoKey] = g
			order = append(order, e.RepoKey)
		}
		g.Entries = append(g.Entries, e)
		g.Count++
		if e.CommittedAt.After(g.LatestAt) {
			g.LatestAt = e.CommittedAt
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		return groups[order[i]].LatestAt.After(groups[order[j]].LatestAt)
	})
	for _, key := range order {
		g := groups[key]
		if f.PerRepo > 0 && len(g.Entries) > f.PerRepo {
			g.Entries = g.Entries[:f.PerRepo]
		}
		v.Repos = append(v.Repos, *g)
	}

	// Totals + type tally (over the full filtered set, not truncated per-repo).
	typeCounts := map[string]int{}
	for _, e := range filtered {
		typeCounts[e.Type]++
	}
	for t, c := range typeCounts {
		v.TypeCounts = append(v.TypeCounts, TypeCount{Type: t, Count: c})
	}
	sort.SliceStable(v.TypeCounts, func(i, j int) bool {
		if v.TypeCounts[i].Count != v.TypeCounts[j].Count {
			return v.TypeCounts[i].Count > v.TypeCounts[j].Count
		}
		return v.TypeCounts[i].Type < v.TypeCounts[j].Type
	})

	v.TotalEntries = len(filtered)
	v.RepoCount = len(v.Repos)

	// Cap truth: the store itself was capped at max_entries; ReadAll hands
	// back at most that many, so any fuller history is truncated.
	if len(entries) > 0 {
		v.OldestRetained = entries[len(entries)-1].CommittedAt
	}
	return v
}

// matchRepo matches a name or unique prefix against a repo's name.
func matchRepo(prefix string, e history.Entry) bool {
	if e.RepoName == prefix {
		return true
	}
	if len(prefix) <= len(e.RepoName) && e.RepoName[:len(prefix)] == prefix {
		return true
	}
	return false
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
