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
	caps := render.Detect()
	pal := render.NewPalette(render.DetectDark())
	enabled := caps.Color
	for _, g := range view.Repos {
		for _, e := range g.Entries {
			fmt.Fprintf(render.Out, "%s  %s  %s  %s  %s\n",
				pal.Muted(e.CommittedAt.Format("2006-01-02 15:04"), enabled),
				pal.Primary(fmt.Sprintf("%-14s", g.Name), enabled),
				pal.Type(typeLabel(e.Type, e.Scope, e.Breaking), e.Breaking, enabled),
				pal.Muted(e.ShortSHA, enabled),
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
	caps := render.Detect()
	pal := render.NewPalette(render.DetectDark())
	enabled := caps.Color

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
	render.Result("%s", pal.Text(header, enabled))
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
		render.Result("%s", pal.Muted("Filters: "+strings.Join(filterBits, " · "), enabled))
	}
	render.Result("")

	for _, g := range view.Repos {
		rel := humanDuration(now.Sub(g.LatestAt))
		fmt.Fprintf(render.Out, "%s    %s\n",
			pal.Primary(g.Name, enabled),
			pal.Muted(fmt.Sprintf("%d %s · %s ago", g.Count, plural(g.Count, "commit", "commits"), rel), enabled))
		if g.Path != "" {
			missing := ""
			if !g.PathExists {
				missing = pal.Warning("   ▲ this path no longer exists", enabled)
			}
			fmt.Fprintf(render.Out, "%s%s\n", pal.Muted(g.Path, enabled), missing)
		}
		for _, e := range g.Entries {
			diff := now.Sub(e.CommittedAt)
			fmt.Fprintf(render.Out, "  %-12s  %-60s  %s  %s\n",
				pal.Type(typeLabel(e.Type, e.Scope, e.Breaking), e.Breaking, enabled),
				truncate(e.Subject, 60),
				pal.Muted(e.ShortSHA, enabled),
				pal.Muted(shortAgo(diff), enabled))
		}
		if statusFlags.perRepo > 0 && g.Count > len(g.Entries) {
			fmt.Fprintf(render.Out, "  %s\n", pal.Muted(fmt.Sprintf("… %d more — commitly status --repo %s --per-repo 0", g.Count-len(g.Entries), g.Name), enabled))
		}
		render.Result("")
	}

	// Type tally.
	var tally []string
	for _, tc := range view.TypeCounts {
		tally = append(tally, pal.Type(fmt.Sprintf("%s %d", tc.Type, tc.Count), false, enabled))
	}
	render.Result("%s", strings.Join(tally, " · "))
	render.Result("")

	if view.Truncated {
		render.Result("%s", pal.Muted(fmt.Sprintf("Showing the last %d commits — older entries have been discarded.", view.TotalEntries), enabled))
	}
	render.Result("%s", pal.Primary("Browse in a browser: commitly serve", enabled))
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
	caps := render.Detect()
	pal := render.NewPalette(render.DetectDark())
	enabled := caps.Color
	render.Result("%s", pal.Text("Statistics — "+p.Label+" ("+p.From+" to "+p.To+")", enabled))
	render.Result("")
	render.Result("  %s", pal.Text(fmt.Sprintf("%d commits · %d repositories · %d of %d active days",
		p.Commits, p.ReposTouched, p.ActiveDays, p.PeriodDays), enabled))
	render.Result("")
	for _, tc := range p.ByType {
		render.Result("  %s %d", pal.Type(tc.Type, false, enabled), tc.Count)
	}
	if !p.FullyCovered {
		render.Result("")
		render.Result("  %s", pal.Warning("⚠ Counters only reach back to "+v.Coverage.EarliestRow+".", enabled))
	}
	return
}

func printStatsFull(v *stats.View) {
	caps := render.Detect()
	pal := render.NewPalette(render.DetectDark())
	enabled := caps.Color

	render.Result("%s", pal.Primary("Statistics — "+time.Now().Format("2 Jan 2006"), enabled))
	render.Result("")
	fmt.Fprintf(render.Out, "%-24s %s %s %s\n", "",
		pal.Muted(fmt.Sprintf("%16s", "THIS WEEK"), enabled),
		pal.Muted(fmt.Sprintf("%16s", "THIS MONTH"), enabled),
		pal.Muted(fmt.Sprintf("%14s", "ALL TIME"), enabled))
	week, month, all := v.Periods["week"], v.Periods["month"], v.Periods["all"]
	weekDelta, monthDelta := coloredDelta(pal, enabled, week), coloredDelta(pal, enabled, month)
	fmt.Fprintf(render.Out, "%-24s %s %s %s\n", pal.Text("Commits", enabled),
		pal.Primary(fmt.Sprintf("%10d", week.Commits), enabled)+" "+weekDelta,
		pal.Primary(fmt.Sprintf("%12d", month.Commits), enabled)+" "+monthDelta,
		pal.Primary(fmt.Sprintf("%10d", all.Commits), enabled))
	fmt.Fprintf(render.Out, "%-24s %s %s %s\n", pal.Text("Repositories", enabled),
		pal.Muted(fmt.Sprintf("%14d", week.ReposTouched), enabled),
		pal.Muted(fmt.Sprintf("%16d", month.ReposTouched), enabled),
		pal.Muted(fmt.Sprintf("%14d", all.ReposTouched), enabled))
	fmt.Fprintf(render.Out, "%-24s %s %s %s\n", pal.Text("Active days", enabled),
		pal.Muted(fmt.Sprintf("%8d/%-5d", week.ActiveDays, week.PeriodDays), enabled),
		pal.Muted(fmt.Sprintf("%10d/%-5d", month.ActiveDays, month.PeriodDays), enabled),
		pal.Muted(fmt.Sprintf("%14d", all.ActiveDays), enabled))

	// By type, top 6.
	render.Result("")
	render.Result("  %s", pal.Text("By type", enabled))
	merged := mergeTypeCounts(week.ByType, month.ByType, all.ByType)
	for _, t := range merged[:min(6, len(merged))] {
		wk, mo, al := typeCount(week.ByType, t.Type), typeCount(month.ByType, t.Type), typeCount(all.ByType, t.Type)
		fmt.Fprintf(render.Out, "    %s %8d %-10s %10d %-10s %10d %-10s\n",
			pal.Type(t.Type, false, enabled), wk, bar(wk, maxCount(week.ByType)), mo, bar(mo, maxCount(month.ByType)), al, bar(al, maxCount(all.ByType)))
	}

	// Sparkline.
	render.Result("")
	render.Result("  %s", pal.Text("Last 28 days", enabled))
	render.Result("    %s", sparklineStr(v.Sparkline))

	// Quality + adherence.
	render.Result("")
	fmt.Fprintf(render.Out, "  %s                    %s\n", pal.Text("Message quality", enabled), pal.Text("Convention adherence", enabled))
	adherence := v.Adherence
	if !adherence.Measured {
		fmt.Fprintf(render.Out, "    %s %4.0f%%               %s\n", pal.Text("with a body", enabled), v.Quality.WithBodyPct, pal.Muted("not measured", enabled))
	} else {
		fmt.Fprintf(render.Out, "    %s %4.0f%%               %s %d/%d   %.0f%%\n",
			pal.Text("with a body", enabled), v.Quality.WithBodyPct,
			pal.Text("overall", enabled), adherence.Conforming, adherence.Total, adherence.Rate*100)
	}
	fmt.Fprintf(render.Out, "    %s %4.0f%%               %s %d %s\n",
		pal.Text("breaking", enabled), v.Quality.BreakingPct,
		pal.Muted("across", enabled), adherence.ReposCounted, pal.Muted("repositories", enabled))
	fmt.Fprintf(render.Out, "    %s  %3d chars\n", pal.Text("median subject", enabled), v.Quality.SubjectMedian)

	// Coverage footer.
	render.Result("")
	if v.Coverage.EarliestRow != "" {
		render.Result("%s", pal.Muted("Counters cover "+v.Coverage.EarliestRow+" → today. All-time totals include", enabled))
		if v.Coverage.AllTimeIncludesFolded {
			render.Result("%s", pal.Muted("folded history from before that.", enabled))
		} else {
			render.Result("%s", pal.Muted("only retained rows.", enabled))
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

// coloredDelta wraps a delta with an up/down color.
func coloredDelta(pal *render.Palette, enabled bool, p *stats.PeriodStat) string {
	if p == nil || p.DeltaPct == nil {
		return ""
	}
	d := *p.DeltaPct
	switch {
	case d > 0:
		return pal.Primary(deltaStr(p), enabled)
	case d < 0:
		return pal.Error(deltaStr(p), enabled)
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
