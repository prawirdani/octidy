// Command octidy is an interactive TUI to delete opencode sessions.
//
// It is a thin wrapper over the existing opencode CLI:
//   - `opencode session list --format json` to load sessions
//   - `opencode session delete <id>`        to delete a chosen session
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
	"os"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
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
