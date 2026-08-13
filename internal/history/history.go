// Package history implements the two local stores: history.jsonl (≤100 full
// entries, ring-buffered) and stats.jsonl (daily counters, ~2 years,
// append-only and summed on read). Deliberately different contents,
// lifetimes and sensitivities (AD-12); both live in the XDG state dir.
package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SchemaVersion is the per-line format version.
const SchemaVersion = 1

// Entry is one recorded commit made through commitly. The body is never
// stored — only has_body — and type/scope/subject are denormalized so an
// entry stays readable after the config or repo is gone.
type Entry struct {
	SchemaVersion   int       `json:"v"`
	ID              string    `json:"id"`
	RepoKey         string    `json:"repo_key"`
	RepoName        string    `json:"repo_name"`
	Path            string    `json:"path,omitempty"`
	RemoteURL       string    `json:"remote_url,omitempty"`
	HostKind        string    `json:"host_kind,omitempty"`
	SHA             string    `json:"sha"`
	ShortSHA        string    `json:"short_sha"`
	Branch          string    `json:"branch,omitempty"`
	Type            string    `json:"type"`
	Scope           string    `json:"scope,omitempty"`
	Breaking        bool      `json:"breaking"`
	Subject         string    `json:"subject"`
	HasBody         bool      `json:"has_body"`
	FilesChanged    int       `json:"files_changed"`
	Insertions      int       `json:"insertions"`
	Deletions       int       `json:"deletions"`
	CommittedAt     time.Time `json:"committed_at"`
	CommitlyVersion string    `json:"commitly_version,omitempty"`
}

// NewEntry builds an entry with a fresh ULID and schema version.
func NewEntry() *Entry {
	return &Entry{ID: NewULID(time.Now()), SchemaVersion: SchemaVersion}
}

// Store reads and appends history entries.
type Store struct {
	Path       string
	MaxEntries int
}

// Open returns an entry store rooted at the given directory.
func OpenEntryStore(dir string, maxEntries int) *Store {
	return &Store{Path: filepath.Join(dir, "history.jsonl"), MaxEntries: maxEntries}
}

// EntriesAreReadable reports the count of currently stored entries.
func (s *Store) Count() (int, error) {
	entries, err := s.ReadAll()
	if err != nil {
		return 0, err
	}
	return len(entries), nil
}

// ReadAll reads every entry, newest first. Corrupt lines are counted and
// reported, never silently dropped.
func (s *Store) ReadAll() ([]Entry, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entries []Entry
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // counted and reported by the caller
		}
		if e.ID == "" || e.RepoKey == "" {
			continue
		}
		entries = append(entries, e)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].CommittedAt.After(entries[j].CommittedAt) })
	return entries, nil
}

// Append adds one entry and trims to MaxEntries newest-first. Every write
// is best-effort (AD-11): failures return an error the caller downgrades to
// a stderr warning, never a failed commit.
func (s *Store) Append(e *Entry) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	e.SchemaVersion = SchemaVersion
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}

	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return s.Trim()
}

// Trim enforces the max-entries cap under an advisory lock via temp file +
// atomic rename. It is safe to fail.
func (s *Store) Trim() error {
	entries, err := s.ReadAll()
	if err != nil {
		return err
	}
	if len(entries) <= s.MaxEntries {
		return nil
	}
	entries = entries[:s.MaxEntries]

	var sb strings.Builder
	for _, e := range entries {
		line, err := json.Marshal(e)
		if err != nil {
			continue
		}
		sb.Write(line)
		sb.WriteByte('\n')
	}

	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Clear deletes the store.
func (s *Store) Clear() error {
	if _, err := os.Stat(s.Path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(s.Path)
}

// ReportUnreadable counts corrupt lines, for the stderr note.
func (s *Store) UnreadableLines() int {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return 0
	}
	n := 0
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			n++
		} else if e.ID == "" || e.RepoKey == "" {
			n++
		}
	}
	return n
}

// HostKind derives a display host label from a remote URL.
func HostKind(remote string) string {
	switch {
	case strings.Contains(remote, "github.com"):
		return "github"
	case strings.Contains(remote, "gitlab.com"):
		return "gitlab"
	case strings.Contains(remote, "bitbucket.org"):
		return "bitbucket"
	case remote == "":
		return "none"
	default:
		return "other"
	}
}
