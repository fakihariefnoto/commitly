package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CounterRow is one append-only counter record. Counters only: date, repo
// key, source, and integers. No subject, no SHA, no branch, no path — this
// is what makes the long-lived file the less sensitive one (AD-12).
type CounterRow struct {
	SchemaVersion int    `json:"v"`
	Date          string `json:"d"` // local calendar day YYYY-MM-DD
	RepoKey       string `json:"r"`
	Source        string `json:"src"`         // "cm" | "hook" | "all_time"
	Type          string `json:"t,omitempty"` // commit type, for the type mix
	Commits       int    `json:"n"`
	Conforming    int    `json:"ok,omitempty"` // src=hook only
	NonConforming int    `json:"bad,omitempty"`
	Own           int    `json:"own,omitempty"`
	Breaking      int    `json:"brk"`
	WithBody      int    `json:"body"`
	SubjectLenSum int    `json:"slen_sum"`
	SubjectHist   []int  `json:"slen_hist,omitempty"` // 20 buckets over 0–200
	Insertions    int    `json:"ins"`
	Deletions     int    `json:"del"`
}

const (
	SrcCM      = "cm"       // commits made through commitly — volume
	SrcHook    = "hook"     // every commit in a hooked repo — adherence
	SrcAllTime = "all_time" // folded history that survived retention
)

// histBucketCount buckets subject lengths 0–200 into 20 buckets of 10.
const histBucketCount = 20

func histBucket(length int) int {
	if length <= 0 {
		return 0
	}
	b := length / 10
	if b >= histBucketCount {
		return histBucketCount - 1
	}
	return b
}

// CounterStore appends and reads counter rows. The hot path (append) is
// lock-free by design (AD-13): one O_APPEND write, atomic below PIPE_BUF.
type CounterStore struct {
	Path             string
	RetentionDays    int
	CompactThreshold int
}

// OpenCounterStore returns a counter store rooted at dir.
func OpenCounterStore(dir string, retentionDays, compactThreshold int) *CounterStore {
	return &CounterStore{Path: filepath.Join(dir, "stats.jsonl"), RetentionDays: retentionDays, CompactThreshold: compactThreshold}
}

// Append writes one row lock-free. Best-effort (AD-11).
func (s *CounterStore) Append(row *CounterRow) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	row.SchemaVersion = SchemaVersion
	if len(row.SubjectHist) == 0 {
		row.SubjectHist = nil
	}
	line, err := json.Marshal(row)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	cerr := f.Close()
	if err != nil {
		return err
	}
	return cerr
}

// Read sums duplicate (d, r, src) rows into one per key, then optionally
// triggers lazy compaction. Read is the only place compaction may run.
func (s *CounterStore) Read() ([]CounterRow, error) {
	rows, unreadable, err := s.readRaw()
	if err != nil {
		return nil, err
	}
	if unreadable > 0 {
		// reported by the caller; the read still succeeds
	}
	summed := sumRows(rows)
	if len(summed) > s.CompactThreshold {
		s.Compact(summed)
	}
	return summed, nil
}

func (s *CounterStore) readRaw() ([]CounterRow, int, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	var rows []CounterRow
	unreadable := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r CounterRow
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			unreadable++
			continue
		}
		rows = append(rows, r)
	}
	return rows, unreadable, nil
}

type rowKey struct{ date, repo, src, typ string }

// sumRows merges duplicate (d, r, src, type) keys and returns rows ordered by date.
func sumRows(rows []CounterRow) []CounterRow {
	merged := map[rowKey]*CounterRow{}
	var order []rowKey
	for _, r := range rows {
		k := rowKey{r.Date, r.RepoKey, r.Source, r.Type}
		if _, ok := merged[k]; !ok {
			order = append(order, k)
			cp := r
			merged[k] = &cp
			continue
		}
		m := merged[k]
		m.Commits += r.Commits
		m.Conforming += r.Conforming
		m.NonConforming += r.NonConforming
		m.Own += r.Own
		m.Breaking += r.Breaking
		m.WithBody += r.WithBody
		m.SubjectLenSum += r.SubjectLenSum
		if m.SubjectHist == nil && len(r.SubjectHist) > 0 {
			m.SubjectHist = make([]int, histBucketCount)
		}
		for i, c := range r.SubjectHist {
			if i < len(m.SubjectHist) {
				m.SubjectHist[i] += c
			}
		}
		m.Insertions += r.Insertions
		m.Deletions += r.Deletions
	}
	out := make([]CounterRow, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

// Compact merges rows, drops rows past retention, folds expiring dailies
// into a single all_time row, and replaces the file via temp file + rename.
// It is allowed to fail harmlessly.
func (s *CounterStore) Compact(summed []CounterRow) error {
	cutoff := time.Now().AddDate(0, 0, -s.RetentionDays).Format("2006-01-02")

	var kept []CounterRow
	var allTime CounterRow
	allTime.Source = SrcAllTime
	allTime.Date = "all"
	for _, r := range summed {
		if r.Source == SrcAllTime {
			addRow(&allTime, r)
			continue
		}
		if r.Date < cutoff {
			addRow(&allTime, r)
			continue
		}
		kept = append(kept, r)
	}
	if allTime.Commits > 0 || allTime.Conforming > 0 {
		kept = append(kept, allTime)
	}

	var sb strings.Builder
	for _, r := range kept {
		r.SchemaVersion = SchemaVersion
		line, err := json.Marshal(r)
		if err != nil {
			continue
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

func addRow(dst *CounterRow, src CounterRow) {
	dst.Commits += src.Commits
	dst.Conforming += src.Conforming
	dst.NonConforming += src.NonConforming
	dst.Own += src.Own
	dst.Breaking += src.Breaking
	dst.WithBody += src.WithBody
	dst.SubjectLenSum += src.SubjectLenSum
	if src.SubjectHist != nil {
		if dst.SubjectHist == nil {
			dst.SubjectHist = make([]int, histBucketCount)
		}
		for i, c := range src.SubjectHist {
			if i < len(dst.SubjectHist) {
				dst.SubjectHist[i] += c
			}
		}
	}
	dst.Insertions += src.Insertions
	dst.Deletions += src.Deletions
}

// Clear deletes the counter store.
func (s *CounterStore) Clear() error {
	if _, err := os.Stat(s.Path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(s.Path)
}

// EarliestRow returns the oldest retained daily date, or "".
func EarliestRow(rows []CounterRow) string {
	earliest := ""
	for _, r := range rows {
		if r.Date != "all" && (earliest == "" || r.Date < earliest) {
			earliest = r.Date
		}
	}
	return earliest
}
