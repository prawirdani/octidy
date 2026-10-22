// Command octidy is an interactive TUI to delete opencode sessions.
//
// It is a thin wrapper over the existing opencode CLI:
//   - `opencode api session.list` to load sessions across all projects
//     (opencode v2 scopes `session list` to one project, so it under-reports;
//     v1 installs fall back to `opencode session list --format json`)
//   - `opencode session delete <id>` to delete a chosen session (and its
//     child sessions)
//
// No opencode storage internals are touched. Set OPENCODE_BIN to point at a
// non-default opencode executable.
//
// Source layout:
//   - opencode.go  data access (Session, load/delete)
//   - model.go     state machine (Bubble Tea model + update)
//   - view.go      rendering
//   - styles.go    lipgloss theme
//   - util.go      pure helpers
package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
)

// version is injected at build time via -ldflags "-X main.version=...".
// Dev builds keep the placeholder.
var version = "dev"

// appVersion resolves the version string. It prefers the ldflags-injected
// value (Makefile/CI); when that is absent it falls back to the module
// version stamped by `go install pkg@vX.Y.Z`.
func appVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "-h", "--help", "help":
			printUsage(os.Stdout)
			return
		case "-v", "--version", "version":
			fmt.Println("octidy", appVersion())
			return
		default:
			fmt.Fprintf(os.Stderr, "octidy: unknown argument %q\n\n", os.Args[1])
			printUsage(os.Stderr)
			os.Exit(2)
		}
	}

	sessions, err := loadSessions()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Println("No sessions to delete.")
		return
	}

	p := tea.NewProgram(newModel(sessions), tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	// After the alt-screen tears down, print a plain summary and set exit code.
	if fm, ok := finalModel.(model); ok && fm.state == stateDone {
		if fail := reportFailures(fm.results); fail > 0 {
			os.Exit(1)
		}
	}
}

// printUsage writes the help text to w.
func printUsage(w io.Writer) {
	fmt.Fprint(w, `octidy - interactive TUI to delete opencode sessions

Usage:
  octidy            launch the interactive session picker
  octidy -h         show this help
  octidy -v         show version

Environment:
  OPENCODE_BIN      path to the opencode executable (default: opencode)
`)
}

// reportFailures prints any failed deletes to stderr and returns the count.
func reportFailures(results []result) int {
	fail := 0
	for _, r := range results {
		if r.err != nil {
			fail++
			fmt.Fprintf(os.Stderr, "failed: %s: %s\n", r.title, oneLine(r.err.Error()))
		}
	}
	return fail
}
