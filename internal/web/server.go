// Package web serves the read-only local view of the history store
// (AD-10): GET-only, loopback-bound by default, all assets embedded. It
// renders the same ActivityView the terminal renderer uses — one model,
// two renderers.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/fakihariefnoto/commitly/internal/activity"
	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/render"
)

//go:embed templates/*.html assets/*
var embedded embed.FS

// Server renders the web view.
type Server struct {
	store *history.Store
	cfg   *config.Config
	caps  *render.Caps
	http  *http.Server
	tmpl  *template.Template
}

// New builds the server, parsing templates once.
func New(store *history.Store, cfg *config.Config, caps *render.Caps) *Server {
	s := &Server{store: store, cfg: cfg, caps: caps}
	s.tmpl = template.Must(template.New("").Funcs(template.FuncMap{
		"shortSHA":  shortSHA,
		"shortDate": shortDate,
		"timeAgo":   timeAgo,
		"typeLabel": typeLabel,
		"plural":    plural,
	}).ParseFS(embedded, "templates/*.html"))

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleDashboard)
	mux.HandleFunc("GET /repo/{key}", s.handleRepo)
	mux.HandleFunc("GET /api/activity.json", s.handleAPI)
	mux.HandleFunc("GET /static/", s.handleStatic)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(w, r)
	})

	s.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s
}

// ListenAndServeOn serves on an already-bound listener.
func (s *Server) ListenAndServeOn(ln net.Listener) error {
	return s.http.Serve(ln)
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// handleDashboard renders the activity list.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	f := parseFilters(r)
	entries, err := s.store.ReadAll()
	if err != nil {
		http.Error(w, "history store unreadable", http.StatusInternalServerError)
		return
	}
	view := activity.Build(entries, f)
	s.render(w, "dashboard.html", map[string]any{
		"Title":      "Commitly",
		"View":       view,
		"TypeCounts": view.TypeCounts,
	})
}

// handleRepo renders one repository.
func (s *Server) handleRepo(w http.ResponseWriter, r *http.Request) {
	key := path.Base(r.PathValue("key"))
	entries, err := s.store.ReadAll()
	if err != nil {
		http.Error(w, "history store unreadable", http.StatusInternalServerError)
		return
	}
	var group *activity.RepoGroup
	view := activity.Build(entries, activity.Filters{PerRepo: 0})
	for i := range view.Repos {
		if strings.HasPrefix(view.Repos[i].Key, key) {
			g := view.Repos[i]
			group = &g
			break
		}
	}
	if group == nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, "repo.html", map[string]any{
		"Title": "Commitly — " + group.Name,
		"Group": group,
	})
}

// handleAPI serves the same view model the page renders, for the page's
// own JS and for curl. Not a public API; no compatibility promise.
func (s *Server) handleAPI(w http.ResponseWriter, r *http.Request) {
	f := parseFilters(r)
	entries, err := s.store.ReadAll()
	if err != nil {
		http.Error(w, "history store unreadable", http.StatusInternalServerError)
		return
	}
	view := activity.Build(entries, f)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(view)
}

// handleStatic serves embedded assets.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	data, err := fs.ReadFile(embedded, "assets/"+name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(name))
	w.Write(data)
}

func (s *Server) render(w http.ResponseWriter, tmpl string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, tmpl, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func parseFilters(r *http.Request) activity.Filters {
	f := activity.Filters{PerRepo: 0}
	q := r.URL.Query()
	f.Repo = q.Get("repo")
	f.Type = q.Get("type")
	if since := q.Get("since"); since != "" {
		if t, err := time.Parse("2006-01-02", since); err == nil {
			f.Since = t
		} else if d, err := time.ParseDuration(since); err == nil {
			f.Since = time.Now().Add(-d)
		}
	}
	return f
}

func contentType(name string) string {
	switch {
	case strings.HasSuffix(name, ".css"):
		return "text/css; charset=utf-8"
	case strings.HasSuffix(name, ".js"):
		return "text/javascript; charset=utf-8"
	case strings.HasSuffix(name, ".svg"):
		return "image/svg+xml"
	case strings.HasSuffix(name, ".woff2"):
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}

func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func shortDate(t time.Time) string {
	return t.Format("2 Jan 2006")
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
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
		return t.Format("2 Jan")
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

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
