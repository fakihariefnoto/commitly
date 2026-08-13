// Package stats computes the statistics view from summed counter rows. It
// is pure and shared by `commitly status --stats` and the web dashboard.
// Periods are fixed (this week / this month / all time) and never reflect
// the entry-list filters (PRD Q17).
package stats

import (
	"math"
	"sort"
	"time"

	"github.com/fakihariefnoto/commitly/internal/history"
)

// TypeCount is a type → count pair.
type TypeCount struct {
	Type  string
	Count int
}

// PeriodStat is one fixed period with its preceding comparison.
type PeriodStat struct {
	Label        string
	From         string // YYYY-MM-DD
	To           string
	Commits      int
	ReposTouched int
	ActiveDays   int
	PeriodDays   int
	PrevCommits  *int
	DeltaPct     *float64 // nil when the previous period has no data
	ByType       []TypeCount
	FullyCovered bool
}

// Adherence reports the mine-vs-others split over hook rows only.
type Adherence struct {
	Measured         bool
	Conforming       int
	Total            int
	Rate             float64
	OwnConforming    int
	OwnTotal         int
	OthersConforming int
	OthersTotal      int
	ReposCounted     int
	ReposTotal       int
	Since            string
}

// Quality aggregates subject/body metrics.
type Quality struct {
	WithBodyPct         float64
	BreakingPct         float64
	SubjectMedian       int
	SubjectOverLimitPct float64
}

// DayPoint is one sparkline point.
type DayPoint struct {
	Date string
	N    int
}

// Coverage states what the figures can and cannot see.
type Coverage struct {
	EarliestRow           string
	EarliestHookRow       string
	RetentionDays         int
	AllTimeIncludesFolded bool
	Caveats               []string
}

// View is the full statistics model.
type View struct {
	GeneratedAt time.Time
	Periods     map[string]*PeriodStat // "week" | "month" | "all"
	Quality     Quality
	Adherence   Adherence
	Sparkline   []DayPoint
	Coverage    Coverage
	Rows        []history.CounterRow
}

// Build computes the view from summed counter rows.
func Build(rows []history.CounterRow, repoTotal int, retentionDays int) *View {
	now := time.Now()
	v := &View{GeneratedAt: now, Periods: map[string]*PeriodStat{}}

	// Split rows: volume (src=cm + all_time) vs adherence (src=hook).
	var volume, hook []history.CounterRow
	for _, r := range rows {
		if r.Source == history.SrcHook {
			hook = append(hook, r)
		} else {
			volume = append(volume, r)
		}
	}

	v.Coverage.EarliestRow = history.EarliestRow(volume)
	v.Coverage.EarliestHookRow = history.EarliestRow(hook)
	v.Coverage.RetentionDays = retentionDays
	for _, r := range rows {
		if r.Source == history.SrcAllTime {
			v.Coverage.AllTimeIncludesFolded = true
		}
	}

	v.Periods["week"] = period(volume, "week", now)
	v.Periods["month"] = period(volume, "month", now)
	v.Periods["all"] = allTime(volume)

	// Quality over volume rows.
	q := Quality{}
	var total int
	var withBody, breaking int
	lengths := []int{}
	for _, r := range volume {
		total += r.Commits
		withBody += r.WithBody
		breaking += r.Breaking
		for i, c := range r.SubjectHist {
			for n := 0; n < c; n++ {
				lengths = append(lengths, i*10+5)
			}
		}
	}
	if total > 0 {
		q.WithBodyPct = pct(withBody, total)
		q.BreakingPct = pct(breaking, total)
	}
	sort.Ints(lengths)
	if len(lengths) > 0 {
		q.SubjectMedian = lengths[len(lengths)/2]
		over := 0
		for _, l := range lengths {
			if l > 72 {
				over++
			}
		}
		q.SubjectOverLimitPct = pct(over, len(lengths))
	}
	v.Quality = q

	// Adherence from hook rows.
	a := Adherence{}
	if len(hook) > 0 {
		a.Measured = true
		repos := map[string]bool{}
		nonConforming := 0
		own := 0
		for _, r := range hook {
			a.Conforming += r.Conforming
			nonConforming += r.NonConforming
			own += r.Own
			repos[r.RepoKey] = true
			if r.Date != "all" && (a.Since == "" || r.Date < a.Since) {
				a.Since = r.Date
			}
		}
		a.Total = a.Conforming + nonConforming
		if a.Total > 0 {
			a.Rate = float64(a.Conforming) / float64(a.Total)
		}
		// Own split. The counters track own commits and overall conformance;
		// the per-author conformance split is approximated proportionally.
		a.OwnTotal = own
		if a.OwnTotal > 0 && a.Total > 0 {
			a.OwnConforming = int(math.Round(float64(own) * a.Rate))
		}
		a.OthersTotal = a.Total - a.OwnTotal
		a.OthersConforming = a.Conforming - a.OwnConforming
		a.ReposCounted = len(repos)
	}
	a.ReposTotal = repoTotal
	v.Adherence = a

	// Sparkline: last 28 days.
	v.Sparkline = sparkline(volume, now)

	// Coverage caveats.
	if v.Coverage.EarliestRow != "" {
		start, err := time.Parse("2006-01-02", v.Coverage.EarliestRow)
		if err == nil {
			monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
			if start.After(monthStart) {
				v.Periods["month"].FullyCovered = false
				v.Periods["all"].FullyCovered = false
				v.Coverage.Caveats = append(v.Coverage.Caveats,
					"Counters only reach back to "+v.Coverage.EarliestRow)
			}
		}
	} else {
		v.Periods["month"].FullyCovered = false
		v.Periods["all"].FullyCovered = false
	}

	v.Rows = rows
	return v
}

func pct(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return math.Round(float64(n)/float64(total)*1000) / 10
}

func period(rows []history.CounterRow, which string, now time.Time) *PeriodStat {
	var from, to time.Time
	switch which {
	case "week":
		wd := int(now.Weekday())
		if wd == 0 {
			wd = 7
		}
		from = time.Date(now.Year(), now.Month(), now.Day()-wd+1, 0, 0, 0, 0, time.Local)
		to = now
	case "month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		to = now
	}
	periodDays := to.Sub(from).Hours()/24 + 1
	if periodDays < 1 {
		periodDays = 1
	}

	ps := &PeriodStat{
		Label:      which,
		From:       from.Format("2006-01-02"),
		To:         to.Format("2006-01-02"),
		PeriodDays: int(periodDays),
	}

	// Current period and preceding same-length period.
	daySet := map[string]bool{}
	typeCounts := map[string]int{}
	repos := map[string]bool{}
	prevCommits := 0

	// Compare calendar dates as strings (YYYY-MM-DD is lexicographically
	// ordered); the store's dates are local calendar days, so instants and
	// timezones are irrelevant here.
	today := now.Format("2006-01-02")
	fromStr := from.Format("2006-01-02")
	prevStart := from.AddDate(0, 0, -int(periodDays)).Format("2006-01-02")

	for _, r := range rows {
		if r.Source == history.SrcAllTime {
			continue
		}
		if r.Date >= fromStr && r.Date <= today {
			ps.Commits += r.Commits
			daySet[r.Date] = true
			repos[r.RepoKey] = true
			if r.Type != "" {
				typeCounts[r.Type] += r.Commits
			}
		} else if r.Date < fromStr && r.Date >= prevStart {
			prevCommits += r.Commits
		}
	}

	ps.ActiveDays = len(daySet)
	ps.ReposTouched = len(repos)
	if ps.Commits > 0 {
		pc := prevCommits
		ps.PrevCommits = &pc
		delta := float64(ps.Commits-prevCommits) / float64(prevCommits) * 100
		if prevCommits == 0 {
			delta = 0
		}
		ps.DeltaPct = &delta
	}
	for t, c := range typeCounts {
		ps.ByType = append(ps.ByType, TypeCount{Type: t, Count: c})
	}
	sort.SliceStable(ps.ByType, func(i, j int) bool { return ps.ByType[i].Count > ps.ByType[j].Count })
	ps.FullyCovered = true
	return ps
}

func allTime(rows []history.CounterRow) *PeriodStat {
	ps := &PeriodStat{Label: "all", FullyCovered: true}
	daySet := map[string]bool{}
	repos := map[string]bool{}
	typeCounts := map[string]int{}
	for _, r := range rows {
		if r.Source == history.SrcAllTime {
			ps.Commits += r.Commits
			if r.Type != "" {
				typeCounts[r.Type] += r.Commits
			}
			continue
		}
		ps.Commits += r.Commits
		daySet[r.Date] = true
		repos[r.RepoKey] = true
		if r.Type != "" {
			typeCounts[r.Type] += r.Commits
		}
	}
	ps.ActiveDays = len(daySet)
	ps.ReposTouched = len(repos)
	for t, c := range typeCounts {
		ps.ByType = append(ps.ByType, TypeCount{Type: t, Count: c})
	}
	sort.SliceStable(ps.ByType, func(i, j int) bool { return ps.ByType[i].Count > ps.ByType[j].Count })
	return ps
}

func sparkline(rows []history.CounterRow, now time.Time) []DayPoint {
	start := now.AddDate(0, 0, -27)
	day := map[string]int{}
	for _, r := range rows {
		if r.Source == history.SrcAllTime {
			continue
		}
		if d, err := time.Parse("2006-01-02", r.Date); err == nil && !d.Before(start) {
			day[r.Date] += r.Commits
		}
	}
	var out []DayPoint
	for i := 0; i < 28; i++ {
		d := start.AddDate(0, 0, i)
		key := d.Format("2006-01-02")
		out = append(out, DayPoint{Date: key, N: day[key]})
	}
	return out
}
