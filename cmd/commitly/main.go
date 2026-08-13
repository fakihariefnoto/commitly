// Command commitly is the entry layer: a cobra command tree with argv[0]
// dispatch so invocation as `git-cm` defaults to the commit subcommand
// (AD-1). Version, commit and date are injected at build time via ldflags.
package main

import (
	"os"

	"github.com/fakihariefnoto/commitly/internal/hook"
)

var (
	version = "dev"
	commit  = "none"
	date    = ""
)

func main() {
	hook.Version = version
	os.Exit(run())
}
