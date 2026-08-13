package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/fakihariefnoto/commitly/internal/activity"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/fakihariefnoto/commitly/internal/stats"
	"github.com/spf13/cobra"
)

var statusFlags struct {
	repo       string
	since      string
	typ        string
	limit      int
	perRepo    int
	oneline    bool
	st         bool
	period     string
	byRepo     bool
	clear      bool
	clearStats bool
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the commits you've made with commitly, grouped by repository",
	Long: `Show the commits you've made with commitly, grouped by repository.

Reads a local history file — the last 100 commits made through this tool,
across every repository. Nothing is read from your repositories and nothing
is ever uploaded.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runStatus(cmd.Context())
	},
}

func init() {
	f := statusCmd.Flags()
	f.StringVar(&statusFlags.repo, "repo", "", "only this repository (name or unique prefix)")
	f.StringVar(&statusFlags.since, "since", "", "only commits newer than this (24h, 7d, 2w, 2026-08-01)")
	f.StringVar(&statusFlags.typ, "type", "", "only this commit type (repeatable)")
	f.IntVar(&statusFlags.limit, "limit", 0, "show at most this many commits")
	f.IntVar(&statusFlags.perRepo, "per-repo", 5, "rows per repository group (0 for all)")
	f.BoolVar(&statusFlags.oneline, "oneline", false, "one line per commit, no grouping")
	f.BoolVar(&statusFlags.st, "stats", false, "show full statistics instead of the commit list")
	f.StringVar(&statusFlags.period, "period", "", "with --stats: week, month or all")
	f.BoolVar(&statusFlags.byRepo, "by-repo", false, "with --stats: per-repository breakdown")
	f.BoolVar(&statusFlags.clear, "clear", false, "delete the stored commit list (asks first)")
	f.BoolVar(&statusFlags.clearStats, "clear-stats", false, "delete the statistics counters (asks first)")
}

func runStatus(ctx context.Context) error {
	cfg, err := (&app{ctx: ctx, caps: render.Detect()}).loadConfig()
	if err != nil {
		return err
	}
	caps := render.Detect()

	if statusFlags.clear {
		return clearEntryStore(cfg, caps)
	}
	if statusFlags.clearStats {
		return clearCounterStore(cfg, caps)
	}

	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}

	if statusFlags.st {
		return renderStats(cfg, dir)
	}

	// Entry store.
	if !cfg.History.Enabled {
		render.Note("History recording is off, so there's nothing to show.")
		render.Note("")
		render.Note("Enable it with:")
		render.Note("  commitly config set history.enabled true")
		render.Note("")
		render.Note("Only future commits will be recorded — nothing is reconstructed retroactively.")
		return nil
	}

	store := history.OpenEntryStore(dir, cfg.History.MaxEntries)
	entries, err := store.ReadAll()
	if err != nil {
		return render.Fail("could not read the history store", err.Error())
	}
	if unreadable := store.UnreadableLines(); unreadable > 0 {
		render.Note("%d history entries could not be read and were skipped.", unreadable)
	}

	filters := activity.Filters{Repo: statusFlags.repo, PerRepo: statusFlags.perRepo, Limit: statusFlags.limit}
	if statusFlags.since != "" {
		t, err := parseSince(statusFlags.since)
		if err != nil {
			return render.Usage(fmt.Sprintf("unparseable --since %q", statusFlags.since),
				"Use a duration (24h, 7d, 2w) or a date (2026-08-01).")
		}
		filters.Since = t
	}
	if statusFlags.typ != "" {
		filters.Type = statusFlags.typ
	}
	if len(entries) == 0 {
		render.Note("No commits recorded yet.")
		render.Note("")
		render.Note("Commits you make with `git cm` will show up here. Only commits made through")
		render.Note("commitly are recorded — nothing is read from your repositories.")
		render.Note("")
		render.Note("  git cm")
		return nil
	}

	view := activity.Build(entries, filters)

	if view.TotalEntries == 0 {
		render.Note("No commits match these filters.")
		if filters.Since.IsZero() && filters.Type == "" {
			render.Note("")
			render.Note("There are %d recorded commits. Try a wider range:", len(entries))
			render.Note("  commitly status --since 7d")
		}
		return nil
	}

	if globals.json {
		return statusJSON(view)
	}

	if statusFlags.oneline {
		printOneline(view)
		return nil
	}

	printActivity(view)
	return nil
}

func parseSince(s string) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse("2006-01-02", s); err == nil {
		return t, nil
	}
	dur := parseDuration(s)
	if dur < 0 {
		return time.Time{}, fmt.Errorf("bad duration")
	}
	return time.Now().Add(-dur), nil
}

func parseDuration(s string) time.Duration {
	units := []struct {
		suffix string
		d      time.Duration
	}{
		{"w", 7 * 24 * time.Hour},
		{"d", 24 * time.Hour},
		{"h", time.Hour},
		{"m", time.Minute},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			var n int
			if _, err := fmt.Sscanf(s, "%d", &n); err == nil {
				return time.Duration(n) * u.d
			}
		}
	}
	return -1
}

func printOneline(view *activity.ActivityView) {
	for _, g := range view.Repos {
		for _, e := range g.Entries {
			fmt.Fprintf(render.Out, "%s  %-14s  %s  %s  %s\n",
				e.CommittedAt.Format("2006-01-02 15:04"),
				g.Name,
				typeLabel(e.Type, e.Scope, e.Breaking),
				e.ShortSHA,
				e.Subject)
		}
	}
}

func typeLabel(typ, scope string, breaking bool) string {
	s := typ
	if scope != "" {
		s += "(" + scope + ")"
	}
	if breaking {
		s += "!"
	}
	return s
}

func printActivity(view *activity.ActivityView) {
	now := time.Now()
	oldest := ""
	if !view.OldestRetained.IsZero() {
		oldest = "last " + humanDuration(now.Sub(view.OldestRetained))
	}
	header := fmt.Sprintf("Activity — %d %s across %d %s",
		view.TotalEntries, plural(view.TotalEntries, "commit", "commits"),
		view.RepoCount, plural(view.RepoCount, "repository", "repositories"))
	if oldest != "" {
		header += ", " + oldest
	}
	render.Result("%s", header)
	var filterBits []string
	if view.SinceFilter != "" {
		filterBits = append(filterBits, "since "+view.SinceFilter)
	}
	if view.TypeFilter != "" {
		filterBits = append(filterBits, "type "+view.TypeFilter)
	}
	if view.RepoFilter != "" {
		filterBits = append(filterBits, "repo "+view.RepoFilter)
	}
	if len(filterBits) > 0 {
		render.Result("Filters: %s", strings.Join(filterBits, " · "))
	}
	render.Result("")

	for _, g := range view.Repos {
		rel := humanDuration(now.Sub(g.LatestAt))
		fmt.Fprintf(render.Out, "%s    %d %s · %s ago\n", g.Name, g.Count, plural(g.Count, "commit", "commits"), rel)
		if g.Path != "" {
			missing := ""
			if !g.PathExists {
				missing = "   ▲ this path no longer exists"
			}
			fmt.Fprintf(render.Out, "%s%s\n", g.Path, missing)
		}
		for _, e := range g.Entries {
			diff := now.Sub(e.CommittedAt)
			fmt.Fprintf(render.Out, "  %-12s  %-60s  %s  %s\n",
				typeLabel(e.Type, e.Scope, e.Breaking),
				truncate(e.Subject, 60),
				e.ShortSHA,
				shortAgo(diff))
		}
		if statusFlags.perRepo > 0 && g.Count > len(g.Entries) {
			fmt.Fprintf(render.Out, "  … %d more — commitly status --repo %s --per-repo 0\n", g.Count-len(g.Entries), g.Name)
		}
		render.Result("")
	}

	// Type tally.
	var tally []string
	for _, tc := range view.TypeCounts {
		tally = append(tally, fmt.Sprintf("%s %d", tc.Type, tc.Count))
	}
	render.Result("%s", strings.Join(tally, " · "))
	render.Result("")

	if view.Truncated {
		render.Result("Showing the last %d commits — older entries have been discarded.", view.TotalEntries)
	}
	render.Result("Browse in a browser: commitly serve")
}

func statusJSON(view *activity.ActivityView) error {
	type entryJSON struct {
		ID           string `json:"id"`
		SHA          string `json:"sha"`
		ShortSHA     string `json:"short_sha"`
		Branch       string `json:"branch"`
		Type         string `json:"type"`
		Scope        string `json:"scope"`
		Breaking     bool   `json:"breaking"`
		Subject      string `json:"subject"`
		HasBody      bool   `json:"has_body"`
		FilesChanged int    `json:"files_changed"`
		Insertions   int    `json:"insertions"`
		Deletions    int    `json:"deletions"`
		CommittedAt  string `json:"committed_at"`
	}
	type repoJSON struct {
		Key        string      `json:"key"`
		Name       string      `json:"name"`
		Path       string      `json:"path"`
		PathExists bool        `json:"path_exists"`
		Remote     string      `json:"remote"`
		Count      int         `json:"count"`
		LatestAt   string      `json:"latest_at"`
		Entries    []entryJSON `json:"entries"`
	}
	payload := map[string]any{
		"generated_at": view.GeneratedAt.Format(time.RFC3339),
		"total":        view.TotalEntries,
		"repo_count":   view.RepoCount,
		"truncated":    view.Truncated,
	}
	if !view.OldestRetained.IsZero() {
		payload["oldest_retained"] = view.OldestRetained.Format(time.RFC3339)
	}
	var repos []repoJSON
	for _, g := range view.Repos {
		rg := repoJSON{
			Key: g.Key, Name: g.Name, Path: g.Path, PathExists: g.PathExists,
			Remote: g.Remote, Count: g.Count, LatestAt: g.LatestAt.Format(time.RFC3339),
		}
		for _, e := range g.Entries {
			rg.Entries = append(rg.Entries, entryJSON{
				ID: e.ID, SHA: e.SHA, ShortSHA: e.ShortSHA, Branch: e.Branch,
				Type: e.Type, Scope: e.Scope, Breaking: e.Breaking, Subject: e.Subject,
				HasBody: e.HasBody, FilesChanged: e.FilesChanged,
				Insertions: e.Insertions, Deletions: e.Deletions,
				CommittedAt: e.CommittedAt.Format(time.RFC3339),
			})
		}
		repos = append(repos, rg)
	}
	payload["repos"] = repos
	payload["type_counts"] = typeCountMap(view.TypeCounts)
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}

func typeCountMap(tcs []activity.TypeCount) map[string]int {
	m := map[string]int{}
	for _, tc := range tcs {
		m[tc.Type] = tc.Count
	}
	return m
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(7*24)))
	}
}

func shortAgo(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	default:
		return fmt.Sprintf("%dw", int(d.Hours()/(7*24)))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// Statistics rendering.

func renderStats(cfg *config.Config, dir string) error {
	if !cfg.Stats.Enabled {
		render.Note("Statistics are off, so there's nothing to show.")
		render.Note("")
		render.Note("Enable them with:")
		render.Note("  commitly config set stats.enabled true")
		render.Note("")
		render.Note("Only future commits are counted — nothing is reconstructed from your")
		render.Note("repositories retroactively.")
		return nil
	}
	cs := history.OpenCounterStore(dir, cfg.Stats.RetentionDays, cfg.Stats.CompactThreshold)
	rows, err := cs.Read()
	if err != nil {
		return render.Fail("could not read the statistics store", err.Error())
	}

	repoTotal := countRepos(rows)
	view := stats.Build(rows, repoTotal, cfg.Stats.RetentionDays)

	if globals.json {
		return statsJSON(view)
	}

	switch statusFlags.period {
	case "week":
		printPeriod(view, view.Periods["week"])
	case "month":
		printPeriod(view, view.Periods["month"])
	case "all":
		printPeriod(view, view.Periods["all"])
	case "":
		printStatsFull(view)
	default:
		return render.Usage("invalid --period: must be week, month or all")
	}
	return nil
}

func countRepos(rows []history.CounterRow) int {
	m := map[string]bool{}
	for _, r := range rows {
		m[r.RepoKey] = true
	}
	return len(m)
}

func printPeriod(v *stats.View, p *stats.PeriodStat) {
	if p == nil {
		return
	}
	render.Result("Statistics — %s (%s to %s)", p.Label, p.From, p.To)
	render.Result("")
	render.Result("  %d commits · %d repositories · %d of %d active days",
		p.Commits, p.ReposTouched, p.ActiveDays, p.PeriodDays)
	render.Result("")
	for _, tc := range p.ByType {
		render.Result("  %-12s %d", tc.Type, tc.Count)
	}
	if !p.FullyCovered {
		render.Result("")
		render.Result("  ⚠ Counters only reach back to %s.", v.Coverage.EarliestRow)
	}
	return
}

func printStatsFull(v *stats.View) {
	render.Result("Statistics — %s", time.Now().Format("2 Jan 2006"))
	render.Result("")
	fmt.Fprintf(render.Out, "%-24s %14s %16s %14s\n", "", "THIS WEEK", "THIS MONTH", "ALL TIME")
	week, month, all := v.Periods["week"], v.Periods["month"], v.Periods["all"]
	weekDelta, monthDelta := deltaStr(week), deltaStr(month)
	fmt.Fprintf(render.Out, "%-24s %10d %8s %12d %10s %10d\n", "Commits", week.Commits, weekDelta, month.Commits, monthDelta, all.Commits)
	fmt.Fprintf(render.Out, "%-24s %14d %16d %14d\n", "Repositories", week.ReposTouched, month.ReposTouched, all.ReposTouched)
	fmt.Fprintf(render.Out, "%-24s %8d/%-5d %10d/%-5d %14d\n", "Active days", week.ActiveDays, week.PeriodDays, month.ActiveDays, month.PeriodDays, all.ActiveDays)

	// By type, top 6.
	render.Result("")
	render.Result("  By type")
	merged := mergeTypeCounts(week.ByType, month.ByType, all.ByType)
	for _, t := range merged[:min(6, len(merged))] {
		wk, mo, al := typeCount(week.ByType, t.Type), typeCount(month.ByType, t.Type), typeCount(all.ByType, t.Type)
		fmt.Fprintf(render.Out, "    %-12s %8d %-10s %10d %-10s %10d %-10s\n",
			t.Type, wk, bar(wk, maxCount(week.ByType)), mo, bar(mo, maxCount(month.ByType)), al, bar(al, maxCount(all.ByType)))
	}

	// Sparkline.
	render.Result("")
	render.Result("  Last 28 days")
	render.Result("    %s", sparklineStr(v.Sparkline))

	// Quality + adherence.
	render.Result("")
	fmt.Fprintf(render.Out, "  Message quality                    Convention adherence\n")
	adherence := v.Adherence
	if !adherence.Measured {
		fmt.Fprintf(render.Out, "    with a body      %4.0f%%               not measured\n", v.Quality.WithBodyPct)
	} else {
		fmt.Fprintf(render.Out, "    with a body      %4.0f%%               overall    %d/%d   %.0f%%\n", v.Quality.WithBodyPct, adherence.Conforming, adherence.Total, adherence.Rate*100)
	}
	fmt.Fprintf(render.Out, "    breaking         %4.0f%%               across %d repositories\n", v.Quality.BreakingPct, adherence.ReposCounted)
	fmt.Fprintf(render.Out, "    median subject  %3d chars\n", v.Quality.SubjectMedian)

	// Coverage footer.
	render.Result("")
	if v.Coverage.EarliestRow != "" {
		render.Result("Counters cover %s → today. All-time totals include", v.Coverage.EarliestRow)
		if v.Coverage.AllTimeIncludesFolded {
			render.Result("folded history from before that.")
		} else {
			render.Result("only retained rows.")
		}
	}
	return
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxCount(tcs []stats.TypeCount) int {
	m := 0
	for _, tc := range tcs {
		if tc.Count > m {
			m = tc.Count
		}
	}
	if m == 0 {
		m = 1
	}
	return m
}

func mergeTypeCounts(groups ...[]stats.TypeCount) []stats.TypeCount {
	m := map[string]int{}
	for _, g := range groups {
		for _, tc := range g {
			m[tc.Type] += tc.Count
		}
	}
	var out []stats.TypeCount
	for t, c := range m {
		out = append(out, stats.TypeCount{Type: t, Count: c})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}

func typeCount(tcs []stats.TypeCount, t string) int {
	for _, tc := range tcs {
		if tc.Type == t {
			return tc.Count
		}
	}
	return 0
}

func bar(n, max int) string {
	blocks := n * 10 / max
	return strings.Repeat("█", blocks)
}

func sparklineStr(points []stats.DayPoint) string {
	var sb strings.Builder
	max := 0
	for _, p := range points {
		if p.N > max {
			max = p.N
		}
	}
	if max == 0 {
		max = 1
	}
	for _, p := range points {
		h := p.N * 4 / max
		switch h {
		case 0:
			sb.WriteString("▁")
		case 1:
			sb.WriteString("▂")
		case 2:
			sb.WriteString("▄")
		case 3:
			sb.WriteString("▆")
		default:
			sb.WriteString("█")
		}
	}
	return sb.String()
}

func deltaStr(p *stats.PeriodStat) string {
	if p == nil || p.DeltaPct == nil {
		return ""
	}
	d := *p.DeltaPct
	switch {
	case d > 0:
		return fmt.Sprintf("▲ +%.0f%%", d)
	case d < 0:
		return fmt.Sprintf("▼ %.0f%%", d)
	default:
		return ""
	}
}

func statsJSON(v *stats.View) error {
	type periodJSON struct {
		Label        string         `json:"label"`
		From         string         `json:"from"`
		To           string         `json:"to"`
		Commits      int            `json:"commits"`
		ReposTouched int            `json:"repos_touched"`
		ActiveDays   int            `json:"active_days"`
		PeriodDays   int            `json:"period_days"`
		PrevCommits  *int           `json:"prev_commits"`
		DeltaPct     *float64       `json:"delta_pct"`
		ByType       map[string]int `json:"by_type"`
		FullyCovered bool           `json:"fully_covered"`
	}
	mk := func(p *stats.PeriodStat) *periodJSON {
		if p == nil {
			return nil
		}
		return &periodJSON{
			Label: p.Label, From: p.From, To: p.To, Commits: p.Commits,
			ReposTouched: p.ReposTouched, ActiveDays: p.ActiveDays, PeriodDays: p.PeriodDays,
			PrevCommits: p.PrevCommits, DeltaPct: p.DeltaPct,
			ByType:       typeCountMapStats(p.ByType),
			FullyCovered: p.FullyCovered,
		}
	}
	payload := map[string]any{
		"generated_at": v.GeneratedAt.Format(time.RFC3339),
		"periods": map[string]any{
			"week":  mk(v.Periods["week"]),
			"month": mk(v.Periods["month"]),
			"all":   mk(v.Periods["all"]),
		},
		"quality": map[string]any{
			"with_body_pct":          v.Quality.WithBodyPct,
			"breaking_pct":           v.Quality.BreakingPct,
			"subject_median":         v.Quality.SubjectMedian,
			"subject_over_limit_pct": v.Quality.SubjectOverLimitPct,
		},
		"adherence": map[string]any{
			"measured":          v.Adherence.Measured,
			"conforming":        v.Adherence.Conforming,
			"total":             v.Adherence.Total,
			"rate":              round2(v.Adherence.Rate),
			"own_conforming":    v.Adherence.OwnConforming,
			"own_total":         v.Adherence.OwnTotal,
			"others_conforming": v.Adherence.OthersConforming,
			"others_total":      v.Adherence.OthersTotal,
			"repos_counted":     v.Adherence.ReposCounted,
			"repos_total":       v.Adherence.ReposTotal,
			"since":             v.Adherence.Since,
		},
		"coverage": map[string]any{
			"earliest_row":             v.Coverage.EarliestRow,
			"earliest_hook_row":        v.Coverage.EarliestHookRow,
			"retention_days":           v.Coverage.RetentionDays,
			"all_time_includes_folded": v.Coverage.AllTimeIncludesFolded,
			"caveats":                  v.Coverage.Caveats,
		},
	}
	b, _ := json.Marshal(payload)
	render.Result("%s", string(b))
	return nil
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func typeCountMapStats(tcs []stats.TypeCount) map[string]int {
	m := map[string]int{}
	for _, tc := range tcs {
		m[tc.Type] = tc.Count
	}
	return m
}

// Clear handling.

func clearEntryStore(cfg *config.Config, caps *render.Caps) error {
	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}
	store := history.OpenEntryStore(dir, cfg.History.MaxEntries)
	entries, _ := store.ReadAll()
	count := len(entries)
	if !globals.yes {
		if !caps.StderrTTY {
			return render.Usage("--clear needs a terminal or --yes to proceed")
		}
		render.Note("Delete all recorded history?")
		render.Note("")
		render.Note("  %d commits across %d repositories", count, len(reposOf(entries)))
		render.Note("  %s", store.Path)
		render.Note("")
		render.Note("This cannot be undone. Your commits are not affected — only this local log.")
		render.Note("")
		render.Note("Delete? [y/N]: ")
		ans, _ := readAnswer()
		if !strings.EqualFold(ans, "y") {
			return nil
		}
	}
	if err := store.Clear(); err != nil {
		return render.Fail("could not clear the history store", err.Error())
	}
	render.Result("✓ Cleared %d entries.", count)
	return nil
}

func reposOf(entries []history.Entry) map[string]bool {
	m := map[string]bool{}
	for _, e := range entries {
		m[e.RepoKey] = true
	}
	return m
}

func clearCounterStore(cfg *config.Config, caps *render.Caps) error {
	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}
	cs := history.OpenCounterStore(dir, cfg.Stats.RetentionDays, cfg.Stats.CompactThreshold)
	rows, _ := cs.Read()
	commits := 0
	for _, r := range rows {
		commits += r.Commits
	}
	if !globals.yes {
		if !caps.StderrTTY {
			return render.Usage("--stats --clear needs a terminal or --yes to proceed")
		}
		render.Note("Delete all recorded statistics?")
		render.Note("")
		render.Note("  %d commits counted across %d repositories", commits, countRepos(rows))
		render.Note("  %s", cs.Path)
		render.Note("")
		render.Note("This cannot be undone, and history is not reconstructed — counting restarts")
		render.Note("from zero. Your commit list (commitly status) is not affected.")
		render.Note("")
		render.Note("Delete? [y/N]: ")
		ans, _ := readAnswer()
		if !strings.EqualFold(ans, "y") {
			return nil
		}
	}
	if err := cs.Clear(); err != nil {
		return render.Fail("could not clear the statistics store", err.Error())
	}
	render.Result("✓ Cleared %d recorded statistics.", commits)
	return nil
}
