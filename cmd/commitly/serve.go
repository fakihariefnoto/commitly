package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/fakihariefnoto/commitly/internal/config"
	"github.com/fakihariefnoto/commitly/internal/history"
	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/fakihariefnoto/commitly/internal/web"
	"github.com/spf13/cobra"
)

var serveFlags struct {
	port       int
	host       string
	open       bool
	noFallback bool
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Browse your commit history in a web page",
	Long: `Browse your commit history in a web page.

Starts a small local web server showing the same activity as ` + "`commitly status`" + `,
laid out for reading. The page is read-only: it displays history and nothing
else — you can't create or change commits from it.

Everything the page needs is compiled into this binary. It makes no external
requests, works with no internet connection, and nothing is uploaded anywhere.
The server listens on localhost only unless you explicitly change --host.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runServe(cmd.Context())
	},
}

func init() {
	f := serveCmd.Flags()
	f.IntVarP(&serveFlags.port, "port", "p", 7378, "port to listen on")
	f.StringVar(&serveFlags.host, "host", "127.0.0.1", "interface to bind")
	f.BoolVar(&serveFlags.open, "open", false, "open the page in your browser")
	f.BoolVar(&serveFlags.noFallback, "no-fallback", false, "fail if the port is taken, instead of using the next one")
}

func runServe(ctx context.Context) error {
	caps := render.Detect()
	cfg, err := (&app{ctx: ctx, caps: caps}).loadConfig()
	if err != nil {
		return err
	}

	if globals.json {
		render.Note("--json is not meaningful for serve; use `commitly status --json`.")
	}

	dir := cfg.History.StorePath
	if dir == "" {
		dir = config.StateDir()
	}
	store := history.OpenEntryStore(dir, cfg.History.MaxEntries)
	entries, err := store.ReadAll()
	if err != nil {
		return render.Fail("could not read the history store", err.Error())
	}
	if len(entries) == 0 {
		render.Note("No commits recorded yet — the page will show how to get started.")
	}

	// Bind, falling forward to the next free port.
	host := serveFlags.host
	port := serveFlags.port
	ln, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		if serveFlags.noFallback {
			return render.Fail(fmt.Sprintf("port %d is already in use", port),
				"Something else is listening there — possibly another `commitly serve`.",
				"Try a different port, or let it pick one:",
				"  commitly serve --port 8080",
				"  commitly serve")
		}
		// Fall forward.
		p := port + 1
		for p < port+100 {
			if l, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, p)); err == nil {
				ln = l
				port = p
				break
			}
			p++
		}
		if ln == nil {
			return render.Fail(fmt.Sprintf("port %d is already in use", port),
				"Try --no-fallback or a different --port.")
		}
		render.Note("▲ Port %d is in use — listening on %d instead.", port-1, port)
		render.Note("  (use --port to choose, or --no-fallback to fail instead)")
	}

	// Exposure warning for non-loopback binds.
	if !isLoopback(host) {
		render.Note("▲ Warning: binding to %s makes this page reachable from your network.", host)
		render.Note("")
		render.Note("  Anyone who can reach this machine on port %d can read your repository", port)
		render.Note("  names, paths, branches and commit subjects. There is no authentication.")
		render.Note("")
		if ip := firstNonLoopbackIP(); ip != "" {
			render.Note("  Reachable at:  http://%s:%d", ip, port)
		}
	}

	server := web.New(store, cfg, render.Detect())

	url := fmt.Sprintf("http://%s:%d", host, port)
	render.Result("%s", url)
	render.Note("commitly %s — serving %d commits across %d repositories", version, len(entries), repoCount(entries))
	render.Note("Listening on %s (%s)", url, loopLabel(host))
	render.Note("Press Ctrl-C to stop.")

	if serveFlags.open {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		server.Shutdown(shutdownCtx)
	}()

	if err := server.ListenAndServeOn(ln); err != nil {
		if err.Error() == "http: Server closed" {
			render.Note("^C")
			render.Note("Shutting down… done.")
			return nil
		}
		return render.Fail("server failed", err.Error())
	}
	return nil
}

func isLoopback(host string) bool {
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func loopLabel(host string) string {
	if isLoopback(host) {
		return "localhost only"
	}
	return host
}

func repoCount(entries []history.Entry) int {
	m := map[string]bool{}
	for _, e := range entries {
		m[e.RepoKey] = true
	}
	return len(m)
}

func firstNonLoopbackIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() && ipnet.IP.To4() != nil {
			return ipnet.IP.String()
		}
	}
	return ""
}

func openBrowser(url string) {
	openers := [][]string{
		{"open", url},
		{"xdg-open", url},
		{"cmd", "/c", "start", url},
	}
	for _, o := range openers {
		if err := execCmd(o...); err == nil {
			return
		}
	}
	render.Note("▲ Couldn't open a browser automatically.")
	render.Note("  Open this URL yourself: %s", url)
}

var execCmd = func(args ...string) error {
	cmd := exec.Command(args[0], args[1:]...)
	return cmd.Start()
}
