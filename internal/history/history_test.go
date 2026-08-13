package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newEntry() *Entry {
	e := NewEntry()
	e.RepoKey = "key1"
	e.RepoName = "repo"
	e.Type = "feat"
	e.Subject = "add x"
	e.CommittedAt = time.Now()
	return e
}

func TestAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	s := OpenEntryStore(dir, 100)
	e := newEntry()
	if err := s.Append(e); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ReadAll()
	if err != nil || len(entries) != 1 {
		t.Fatalf("read: %d entries err=%v", len(entries), err)
	}
	if entries[0].Subject != "add x" || entries[0].ID == "" {
		t.Fatalf("entry: %+v", entries[0])
	}
}

func TestTrimKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	s := OpenEntryStore(dir, 3)
	for i := 0; i < 10; i++ {
		e := newEntry()
		e.Subject = string(rune('a' + i))
		e.CommittedAt = time.Now().Add(time.Duration(i) * time.Minute)
		if err := s.Append(e); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := s.ReadAll()
	if len(entries) != 3 {
		t.Fatalf("expected 3 after trim, got %d", len(entries))
	}
	if entries[0].Subject != "j" {
		t.Fatalf("expected newest first, got %q", entries[0].Subject)
	}
}

func TestConcurrentAppends(t *testing.T) {
	dir := t.TempDir()
	s := OpenCounterStore(dir, 730, 100000)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Append(&CounterRow{Date: "2026-08-13", RepoKey: "r", Source: SrcCM, Commits: 1})
		}()
	}
	wg.Wait()
	rows, err := s.Read()
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 summed row, got %d err=%v", len(rows), err)
	}
	if rows[0].Commits != 50 {
		t.Fatalf("expected 50 commits, got %d", rows[0].Commits)
	}
}

func TestSumDuplicates(t *testing.T) {
	dir := t.TempDir()
	s := OpenCounterStore(dir, 730, 100000)
	for i := 0; i < 3; i++ {
		_ = s.Append(&CounterRow{Date: "2026-08-13", RepoKey: "r", Source: SrcCM, Commits: 4})
	}
	rows, _ := s.Read()
	if len(rows) != 1 || rows[0].Commits != 12 {
		t.Fatalf("sum: %+v", rows)
	}
}

func TestCounterRowCarriesNoMessageText(t *testing.T) {
	row := CounterRow{Date: "d", RepoKey: "r", Source: SrcCM, SubjectLenSum: 5}
	b, _ := json.Marshal(row)
	s := string(b)
	for _, field := range []string{"subject", "sha", "branch", "path", "message"} {
		if strings.Contains(s, `"`+field+`"`) {
			t.Fatalf("counter row leaks %q: %s", field, s)
		}
	}
}

func TestCompactionFoldsAllTime(t *testing.T) {
	dir := t.TempDir()
	s := OpenCounterStore(dir, 730, 2)
	// Two dailies past retention + one recent.
	old := time.Now().AddDate(0, 0, -800).Format("2006-01-02")
	recent := time.Now().Format("2006-01-02")
	_ = s.Append(&CounterRow{Date: old, RepoKey: "r", Source: SrcCM, Commits: 5})
	_ = s.Append(&CounterRow{Date: recent, RepoKey: "r", Source: SrcCM, Commits: 3})
	rows, _ := s.Read() // triggers compaction (2 rows > threshold... threshold 2, len==2 not > 2)
	// Force compaction explicitly.
	_ = s.Compact(rows)
	rows2, _ := s.Read()
	foundAll := false
	total := 0
	for _, r := range rows2 {
		total += r.Commits
		if r.Source == SrcAllTime {
			foundAll = true
			if r.Commits != 5 {
				t.Fatalf("all_time should fold the expired 5: %+v", r)
			}
		}
		if r.Date == old {
			t.Fatal("expired daily should be gone")
		}
	}
	if total != 8 {
		t.Fatalf("all-time total must survive retention: %d", total)
	}
	if !foundAll {
		t.Fatal("expected all_time row")
	}
}

func TestClear(t *testing.T) {
	dir := t.TempDir()
	s := OpenEntryStore(dir, 100)
	_ = s.Append(newEntry())
	if err := s.Clear(); err != nil {
		t.Fatal(err)
	}
	entries, _ := s.ReadAll()
	if len(entries) != 0 {
		t.Fatal("store not cleared")
	}
}

func TestULIDOrdering(t *testing.T) {
	a := NewULID(time.Now().Add(-time.Minute))
	b := NewULID(time.Now())
	if a >= b {
		t.Fatalf("ULIDs must be sortable by time: %s >= %s", a, b)
	}
	if len(a) != 26 {
		t.Fatalf("ULID length: %d", len(a))
	}
}

func TestStorePerms(t *testing.T) {
	dir := t.TempDir()
	s := OpenEntryStore(dir, 100)
	_ = s.Append(newEntry())
	fi, err := os.Stat(filepath.Join(dir, "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600, got %o", fi.Mode().Perm())
	}
}
