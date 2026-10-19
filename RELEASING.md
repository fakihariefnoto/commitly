# Releasing

GoReleaser drives all three channels: GitHub releases, the Homebrew formula in
[fakihariefnoto/homebrew-tap](https://github.com/fakihariefnoto/homebrew-tap),
and `go install` via the module path.

```
git tag v0.1.0  ──▶  goreleaser  ──▶  GitHub release (archives + checksums)
                                  ──▶  Formula/commitly.rb committed to the tap
                                  ──▶  go install github.com/fakihariefnoto/commitly/cmd/commitly@v0.1.0
```

## Credentials

Publishing needs a credential that can create a release on `commitly` and push
to `homebrew-tap`. **You probably already have one**: if `git push` works from
this machine, git stores a credential for github.com, and it can do both. No
personal access token has to exist.

The script resolves one in this order:

1. `TAP_GITHUB_TOKEN` in the environment
2. a token saved with `--store-token`, in the macOS keychain
3. **the credential git already stores for github.com** — the same one `git
   push` uses

Check what you have:

```sh
printf 'protocol=https\nhost=github.com\n\n' | git credential fill
```

If that prints a password, you are done. If it does not, either push something
once (git will offer to store a credential), or create a fine-grained PAT with
**Contents: read and write** on `fakihariefnoto/homebrew-tap` and pass it through
one of the first two steps.

### Locally

The version comes from the highest `v*` tag, not from a file, so the tag is the
single source of truth and cannot drift from what was published.

```sh
./scripts/release.sh --dry-run       # next patch, print the plan and stop
make release                         # next patch: 0.1.0 -> 0.1.1
make release V=minor                 # 0.2.0
make release V=major                 # 1.0.0
make release V=0.2.3                 # exactly that
```

(`--dry-run` goes to the script directly: `make release --dry-run` would be
consumed by make itself as its own `--dry-run` flag.)

With no tags at all the first release is `0.1.0`, not `0.1.1`: a tool that has
never been published should not look like it had a patch. A version that is not
newer than the last tag is refused, and so is one whose tag already exists, so a
version can never go backwards or collide.

The tag is read from the remote as well as the local repository, because a
release can be published from another machine.

The script refuses to run when it would produce a half-finished release:

| Check | Why |
|---|---|
| working tree is clean | an uncommitted change would not be in the release |
| on `main` | releases come from the trunk |
| not ahead of `origin/main` | the tag must point at pushed code |
| tag does not exist, locally or remotely | re-tagging is how versions get lost |
| token present | otherwise goreleaser fails after the tag is already pushed |

`TAP_GITHUB_TOKEN` in the environment overrides everything, which is useful in
CI or a one-off shell. The tap clone in `~/Projects/dist/...` is not used by the
release: goreleaser commits the formula through the API, so a stale local clone
cannot disagree with what gets published.

### In GitHub Actions, nothing on your machine

Add the PAT as a repository secret named `TAP_GITHUB_TOKEN`, then either push a
tag or run the **release** workflow manually with the tag as input. The workflow
runs the test suite first and refuses to publish if it fails.

### Is a secret safe in a public repository?

Yes. Repository secrets are not readable — by anyone, including you. Once saved
they are encrypted, shown only as a name, and cannot be read back through the UI
or the API. GitHub redacts them from logs, and workflows triggered by a fork's
pull request do not receive them at all.

The real trust boundary is not public visibility, it is **who can write to the
repository**: anyone able to merge a workflow file can add a step that prints a
secret. That is why the PAT should be fine-grained and scoped to the single tap
repository — if it leaked, the worst case is someone pushing to your tap, not
access to your account.

If you would rather hold no token in GitHub at all, use the local path; the
workflow is optional.

## What gets built

| | |
|---|---|
| targets | darwin, linux and windows, amd64 and arm64 |
| archive | `tar.gz` (`zip` on Windows), with `LICENSE`, `README.md`, `completions/` and `man/` beside the binary |
| cgo | disabled, so one machine cross-compiles everything |
| version | injected with `-ldflags -X main.version/commit/date` |
| checksums | `checksums.txt` per release |

The `git-cm` symlink is **not** inside the archive. GoReleaser v2 removed
archive `post_hooks`, so a symlink cannot be created there any more. Instead:

- **Homebrew** creates it with `bin.install_symlink bin/"commitly" => "git-cm"`,
  which is what `git cm` resolves against.
- **`go install` and manual extracts** run `commitly init`, which offers the
  `git config --global alias.cm` alternative.

One binary on disk, one name to keep in sync.

Archives deliberately have no wrapping directory: the formula does
`bin.install "commitly"` and expects the binary at the archive root.

## A note on `goreleaser check`

It exits non-zero:

```
DEPRECATED: brews should not be used anymore
```

That is deliberate. `brews` was superseded by `homebrew_casks`, but casks are
macOS-only and the formula here also serves the Linux archives. The warning is
expected until the tap either splits into a cask and a formula or drops Linux.

## Verifying a release without publishing

```sh
make snapshot     # goreleaser release --snapshot --clean --skip=publish
```

Builds the archives and the formula into `dist/` and publishes nothing. Confirm
that `dist/homebrew/Formula/commitly.rb` has both `on_macos` and `on_linux`
blocks and that an extracted binary reports its injected version:

```sh
tar xzf dist/commitly_*_darwin_arm64.tar.gz -C /tmp/commitly-check
/tmp/commitly-check/commitly --version
```

## If a release goes wrong

The tag is the problem, not the workflow. Delete it and start again:

```sh
git tag -d v0.1.0 && git push origin :refs/tags/v0.1.0
```

Then re-run. If the formula was already pushed to the tap, revert that commit in
the tap repository too, or the two will disagree about what `v0.1.0` is.

## Pre-release checklist

```sh
make check          # gofmt, go vet, tests
make snapshot       # confirm the artifacts build
git status          # clean
git log origin/main..HEAD   # nothing unpushed
```
