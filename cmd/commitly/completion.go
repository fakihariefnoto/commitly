package main

import (
	"fmt"
	"os"

	"github.com/fakihariefnoto/commitly/internal/render"
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var completionCmd = &cobra.Command{
	Use:   "completion <bash|zsh|fish|powershell>",
	Short: "Generate a shell completion script",
	Long: `Generate a shell completion script.

If you installed via Homebrew, apt/dnf or Scoop, completions are already
installed — you don't need this. It's here for ` + "`go install`" + ` users and for
the packages themselves.

Completion is dynamic: --type and --scope complete from the effective
config, so a repository's custom types complete correctly.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		switch args[0] {
		case "bash":
			return rootCmd.GenBashCompletion(os.Stdout)
		case "zsh":
			return rootCmd.GenZshCompletion(os.Stdout)
		case "fish":
			return rootCmd.GenFishCompletion(os.Stdout, true)
		case "powershell":
			return rootCmd.GenPowerShellCompletion(os.Stdout)
		default:
			return render.Usage(fmt.Sprintf("unsupported shell %q", args[0]),
				"Supported: bash, zsh, fish, powershell")
		}
	},
}

var manCmd = &cobra.Command{
	Use:   "man",
	Short: "Generate the man page in roff format",
	Long: `Generate the man page in roff format.

Prints commitly.1 to stdout. With --output, writes a page per command
into a directory, which is what packaging needs.`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		header := &doc.GenManHeader{
			Title:   "commitly",
			Section: "1",
			Manual:  "commitly Manual",
		}
		if manOutput != "" {
			if err := os.MkdirAll(manOutput, 0o755); err != nil {
				return render.Fail("cannot write to "+manOutput, err.Error())
			}
			if err := doc.GenManTree(rootCmd, header, manOutput); err != nil {
				return render.Fail("cannot write to "+manOutput, err.Error())
			}
			render.Note("✓ Wrote man pages to %s", manOutput)
			return nil
		}
		return doc.GenMan(rootCmd, header, os.Stdout)
	},
}

var manOutput string

func init() {
	manCmd.Flags().StringVarP(&manOutput, "output", "o", "", "write per-command pages into this directory")
}
