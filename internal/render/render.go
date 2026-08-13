// Package render owns the output discipline: stdout is the result, stderr is
// everything else. It detects terminal capability once, defines the error
// type that carries a next action, and maps failure kinds to exit codes.
package render

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// Out is the result stream (stdout).
var Out io.Writer = os.Stdout

// Err is the everything-else stream (stderr).
var Err io.Writer = os.Stderr

// Caps is the one-time terminal capability snapshot.
type Caps struct {
	StdoutTTY  bool
	StderrTTY  bool
	Color      bool
	ASCII      bool
	NoTUI      bool
	Accessible bool
	Quiet      bool
	Verbose    int
	NoColor    bool
	JSON       bool
}

// Detect computes the capability snapshot. COMMITLY_NO_TUI forces the
// non-interactive path for tests.
func Detect() *Caps {
	noColor := os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
	stdoutTTY := isTerminal(os.Stdout)
	stderrTTY := isTerminal(os.Stderr)
	color := stdoutTTY && !noColor && !isCI()
	if os.Getenv("FORCE_COLOR") != "" {
		color = true
	}
	if os.Getenv("NO_COLOR") == "0" {
		color = stdoutTTY
	}
	c := &Caps{
		StdoutTTY:  stdoutTTY,
		StderrTTY:  stderrTTY,
		Color:      color,
		ASCII:      os.Getenv("COMMITLY_ASCII") == "1",
		NoTUI:      os.Getenv("COMMITLY_NO_TUI") == "1",
		Accessible: os.Getenv("ACCESSIBLE") == "1",
	}
	// Count -v/--verbose from the raw args so capability detection agrees
	// with cobra's parsing (cobra needs Execute to run before flags resolve).
	for _, a := range os.Args[1:] {
		if a == "-v" || a == "--verbose" {
			c.Verbose++
		} else if a == "-vv" {
			c.Verbose += 2
		}
	}
	return c
}

func isCI() bool {
	return os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") != ""
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Exit codes — these are API (TRD I4); CI and hooks branch on them.
const (
	ExitOK       = 0
	ExitFailed   = 1 // the operation failed
	ExitUsage    = 2 // bad invocation
	ExitValidate = 3 // validation failed
	ExitAborted  = 130
)

// Error is a user-facing error: what failed, and what to do next. Every
// error must carry a next action — a violation without one is the
// commitlint experience this product exists to improve.
type Error struct {
	Kind    int // one of the Exit* constants
	What    string
	Next    []string
	Wrapped error
}

func (e *Error) Error() string {
	if e.Wrapped != nil {
		return e.Wrapped.Error()
	}
	return e.What
}

func (e *Error) Unwrap() error { return e.Wrapped }

// Fail builds a kind-1 error with a next action.
func Fail(what string, next ...string) *Error {
	return &Error{Kind: ExitFailed, What: what, Next: next}
}

// Usage builds a kind-2 error.
func Usage(what string, next ...string) *Error {
	return &Error{Kind: ExitUsage, What: what, Next: next}
}

// ValidateError builds a kind-3 error.
func ValidateError(what string, next ...string) *Error {
	return &Error{Kind: ExitValidate, What: what, Next: next}
}

// AbortError signals Ctrl-C: exit 130, draft saved, index untouched.
func AbortError() *Error {
	return &Error{Kind: ExitAborted, What: "Aborted"}
}

// KindOf extracts the exit-code kind from an error, defaulting to 1.
func KindOf(err error) int {
	if err == nil {
		return ExitOK
	}
	// Validation-failed signals exit 3.
	var v interface{ ValidationExit() int }
	if errors.As(err, &v) {
		return v.ValidationExit()
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	// Ctrl-C.
	if errors.Is(err, syscall.EINTR) || strings.Contains(err.Error(), "interrupt") {
		return ExitAborted
	}
	return ExitFailed
}

// PrintError renders an error to stderr: the failure and the next action.
func PrintError(err error, verbose bool) {
	kind := KindOf(err)
	prefix := "Error"
	if kind == ExitUsage {
		prefix = "Error"
	}
	var e *Error
	detail := ""
	if errors.As(err, &e) {
		detail = e.What
		if verbose && e.Wrapped != nil {
			detail = e.Wrapped.Error()
		}
	} else {
		detail = err.Error()
	}
	fmt.Fprintf(Err, "%s: %s\n", prefix, detail)
	if e != nil && len(e.Next) > 0 {
		fmt.Fprintln(Err, "\n"+strings.Join(e.Next, "\n"))
	}
}

// PrintWarnings prints config warnings, each prefixed ▲.
func PrintWarnings(ws []string) {
	if len(ws) == 0 {
		return
	}
	for _, w := range ws {
		fmt.Fprintf(Err, "▲ %s\n", w)
	}
}

// Note prints a stderr line.
func Note(format string, args ...any) {
	fmt.Fprintf(Err, format+"\n", args...)
}

// Result prints to stdout.
func Result(format string, args ...any) {
	fmt.Fprintf(Out, format+"\n", args...)
}
