#!/usr/bin/env bash
#
# Generate the artifacts that ship inside the archives but cannot be produced
# by Homebrew at install time: shell completions and the man page.
#
# Called by the `before` hooks in .goreleaser.yaml, and available to `make
# completions` for local packaging. Output is deterministic — it is derived
# from the cobra command tree, so regenerating it on the same commit produces
# byte-identical files and the archives stay reproducible.

set -euo pipefail

cd "$(dirname "$0")/.."

BIN=./bin/commitly
OUT_COMPLETIONS=completions
OUT_MAN=man

# Build once into bin/ rather than running `go run` five times: every
# invocation would otherwise recompile the command tree.
mkdir -p bin
go build -o "$BIN" ./cmd/commitly

mkdir -p "$OUT_COMPLETIONS" "$OUT_MAN"

"$BIN" completion bash >"$OUT_COMPLETIONS/commitly.bash"
"$BIN" completion zsh >"$OUT_COMPLETIONS/_commitly"
"$BIN" completion fish >"$OUT_COMPLETIONS/commitly.fish"
"$BIN" completion powershell >"$OUT_COMPLETIONS/commitly.ps1"
"$BIN" man >"$OUT_MAN/commitly.1"

printf 'generated %s and %s\n' "$OUT_COMPLETIONS" "$OUT_MAN"
