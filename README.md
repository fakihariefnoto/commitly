# commitly

> Compose [Conventional Commits](https://www.conventionalcommits.org/) messages without thinking about the format — in your terminal, or as `git cm`.

One static binary, two names: `commitly` for the full command surface, and a `git-cm` symlink so `git cm` just works with **zero configuration**. Everything runs locally and offline — nothing is ever uploaded.

## Why commitly?

Writing a good commit message is a habit, but remembering the format is friction. Most of us have typed a message, hit enter, and then watched CI reject it — or worse, shipped a `wip` commit into history that a changelog generator silently drops a year later.

Commitly removes that friction:

- **Stop memorizing the format.** Pick a type from a menu, tab through scope and subject, and a live preview shows the exact message as you compose it.
- **Consistent history, automatically.** The optional `commit-msg` hook validates every commit — even ones made with plain `git commit` — so the convention holds without anyone enforcing it.
- **Changelogs that work.** Conventional history turns into release notes with one command (`commitly changelog`), instead of diffing tags by hand.
- **See what you've actually done.** `commitly status` shows your recent commits across every repo; `commitly serve` renders the same view in your browser.
- **Zero cost to adopt.** No config file required, works offline, one static binary with no runtime to install. `git cm` works the moment it's on your `$PATH`.

If you've ever had a commit rejected for "missing space after the colon," this tool is for you.

## Screenshots

<div align="center">

## Install

`git cm` works immediately after any of these — no `init`, no alias, no config required.

| Channel                               | Command                                                                                                                                            |
| ------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Homebrew** (macOS)            | `brew tap fakihariefnoto/tapbrew install fakihariefnoto/tap/commitly`                                                                            |
| **Scoop** (Windows)             | `scoop bucket add fakihariefnoto https://github.com/fakihariefnoto/scoop-bucketscoop install commitly`                                           |
| **`.deb` / `.rpm`** (Linux) | `sudo dpkg -i commitly_*.deb` &nbsp;·&nbsp; `sudo rpm -i commitly_*.rpm`                                                                      |
| **`go install`**              | `go install github.com/fakihariefnoto/commitly/cmd/commitly@latestcommitly init` # adds the `git cm` alias (go install can't ship the symlink) |

## Quick start

```sh
git cm                    # compose a conventional commit, interactively
git cm -a                 # pick files to stage, then compose
commitly lint --range origin/main..HEAD
commitly changelog        # release notes since the last tag
commitly status           # what you've committed across every repo
commitly serve            # the same history as a local web page
```

## Commands

| Command                                           | Purpose                                                                        |
| ------------------------------------------------- | ------------------------------------------------------------------------------ |
| `commitly commit` (`git cm`)                  | Compose and create a conventional commit                                       |
| `commitly status` (`st`)                      | Your recent commits across every repo                                          |
| `commitly serve`                                | Browse the same history in a browser (read-only, localhost)                    |
| `commitly lint`                                 | Validate a message, a commit, or a range (exit`3` on failure — CI-friendly) |
| `commitly changelog` (`cl`)                   | Markdown release notes from conventional history                               |
| `commitly init`                                 | Optional: git alias,`commit-msg` hook, completions, `.commitly.yaml`       |
| `commitly config`                               | Inspect and edit configuration (`get` / `set` / `list` / `path`)       |
| `commitly completion` · `man` · `version` | Generated artifacts and version info                                           |

## Configuration

Config resolves from five sources, highest first: command flag → `COMMITLY_*` env → repo `.commitly.yaml` → user `~/.config/commitly/config.yaml` → built-in defaults. `commitly config get <key>` shows which source won.

A repo can pin its conventions in `.commitly.yaml` (types, scopes, subject rules, changelog headings). User-level settings (`history:`, `stats:`, `serve:`) are ignored in a repo config so a clone can never change how your personal activity is recorded.

## Privacy

Commitly records two local files in the OS state directory:

- `history.jsonl` — the newest 100 commits made *through* commitly (subjects, SHAs, repo paths). Written `0600`.
- `stats.jsonl` — ~2 years of daily counters. No message text, no SHAs, no paths.

Neither is ever uploaded. The `commit-msg` hook (optional, installed by `commitly init --hook`) can count conforming vs non-conforming commits for `status --stats` — disclosed in full before the first count, counters only, and disable-able while keeping validation.

## Updates

Update through whatever installed you: 

- `brew upgrade commitly`
- `scoop update commitly`
- `apt`/`dnf`
- `go install ...@latest`.

There is deliberately no `commitly self-update` — a self-updater would fight your package manager.

## License

MIT
