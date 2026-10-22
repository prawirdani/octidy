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
		case "--prune":
			runMaintenance(false)
			return
		case "--vacuum":
			runMaintenance(true)
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

	m := newModel(sessions)
	// Wired here, not in newModel, so tests stay hermetic (no store access).
	m.collect = collectArtifacts
	p := tea.NewProgram(m, tea.WithAltScreen())
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	fm, ok := finalModel.(model)
	if !ok {
		return
	}
	if fm.state == stateDone {
		// Leftover artifacts of the sessions that actually got deleted
		// (truncated tool output, captured shell output, legacy v1 storage
		// dirs). opencode does not remove these itself, and a failed delete
		// must keep its files because the session still references them.
		if files, bytes := prunePaths(deletedArtifacts(fm.results, fm.artifacts)); files > 0 {
			fmt.Printf("pruned %d artifact(s), %s freed\n", files, humanBytes(bytes))
		}
		if fail := reportFailures(fm.results); fail > 0 {
			os.Exit(1)
		}
	}
}

// deletedArtifacts flattens the collected artifacts of the sessions that
// actually got deleted. A failed delete keeps its files: the session still
// references them.
func deletedArtifacts(results []result, artifacts map[string][]string) []string {
	var paths []string
	for _, r := range results {
		if r.err == nil {
			paths = append(paths, artifacts[r.id]...)
		}
	}
	return paths
}

// runMaintenance performs the offline cleanup passes (orphan sweep, optional
// VACUUM) and prints what was reclaimed.
func runMaintenance(vacuum bool) {
	dir := dataDir()
	if dir == "" {
		fmt.Fprintln(os.Stderr, "error: cannot locate the opencode data directory")
		os.Exit(1)
	}
	st, err := sweepOrphans(dir, vacuum)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	fmt.Printf("removed %d orphan artifact(s) (%s), %d orphan row(s)\n",
		st.Files, humanBytes(st.Bytes), st.Rows)
	if vacuum {
		if st.Reclaimed > 0 {
			fmt.Printf("reclaimed %s from %s/opencode.db\n", humanBytes(st.Reclaimed), dir)
		} else {
			fmt.Println("database already compact; nothing to reclaim")
		}
	}
}

// printUsage writes the help text to w.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `octidy - interactive TUI to delete opencode sessions

Usage:
  octidy            launch the interactive session picker
  octidy --prune    remove orphaned sessions, messages and leftover files
  octidy --vacuum   --prune, then compact opencode.db (stop the service first)
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
