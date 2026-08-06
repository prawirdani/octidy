package main

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

// runProgram runs a model in a real (headless) tea.Program, sends a 'y' to
// trigger the delete fan-out from the confirm screen, and returns the final
// model once it quits. Deletes run in real goroutines, so this is the path to
// run under `go test -race`.
func runProgram(t *testing.T, m model) model {
	t.Helper()
	m.state = stateConfirm
	p := tea.NewProgram(m,
		tea.WithoutRenderer(),
		tea.WithInput(bytes.NewReader(nil)),
	)
	go func() {
		// Give Init/first loop a beat, then confirm.
		time.Sleep(5 * time.Millisecond)
		p.Send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	}()

	done := make(chan model, 1)
	go func() {
		fm, err := p.Run()
		if err != nil {
			t.Errorf("program run: %v", err)
		}
		done <- fm.(model)
	}()

	select {
	case fm := <-done:
		return fm
	case <-time.After(5 * time.Second):
		p.Kill()
		t.Fatal("program did not converge to stateDone")
		return model{}
	}
}

// sampleModel builds a model with three sessions for tests. Shared across the
// model/view test files.
func sampleModel() model {
	s := []Session{
		{ID: "ses_a", Title: "Reading PRD.md", Updated: time.Now().Add(-2 * time.Hour).UnixMilli(), Directory: "/home/x/test"},
		{ID: "ses_b", Title: "Greeting", Updated: time.Now().Add(-48 * time.Hour).UnixMilli(), Directory: "/home/x/app"},
		{ID: "ses_c", Title: "VC - Tanstack Start Todo App", Updated: time.Now().Add(-30 * time.Second).UnixMilli(), Directory: "/home/x/todo"},
	}
	return newModel(s)
}

func TestSelectionHelpers(t *testing.T) {
	m := sampleModel()
	if got := m.countSelected(); got != 0 {
		t.Fatalf("fresh model countSelected=%d want 0", got)
	}
	m.selected[1] = true
	if got := m.countSelected(); got != 1 {
		t.Fatalf("countSelected=%d want 1", got)
	}
	sel := m.selectedSessions()
	if len(sel) != 1 || sel[0].ID != "ses_b" {
		t.Fatalf("selectedSessions=%v want [ses_b]", sel)
	}
}

// The delete queue must drain one item per deleteNext call and finish with
// allDoneMsg.
// deleteNext pops one session per call and returns a delete command.
func TestDeleteNextPops(t *testing.T) {
	m := sampleModel()
	m.queue = []Session{{ID: "ses_a", Title: "a"}, {ID: "ses_b", Title: "b"}}

	cmd := m.deleteNext()
	if cmd == nil {
		t.Fatal("deleteNext returned nil with items queued")
	}
	if len(m.queue) != 1 {
		t.Fatalf("queue len=%d want 1 after one pop", len(m.queue))
	}
	// The command deletes the popped session and reports it.
	msg, ok := cmd().(deleteDoneMsg)
	if !ok || msg.title != "a" || msg.id != "ses_a" {
		t.Fatalf("cmd yielded %#v want deleteDoneMsg for ses_a/'a'", msg)
	}

	m.deleteNext()
	if len(m.queue) != 0 {
		t.Fatalf("queue len=%d want 0", len(m.queue))
	}
}

// updateConfirm("y") must start at most maxConcurrent deletes and leave the
// rest queued for backfill. This is the test that proves the pool is bounded.
func TestFanOutRespectsCap(t *testing.T) {
	m := sampleModel()
	// Build a selection larger than the cap.
	big := maxConcurrent + 5
	m.sessions = make([]Session, big)
	m.selected = make(map[int]bool)
	for i := range m.sessions {
		m.sessions[i] = Session{ID: fmt.Sprintf("ses_%d", i), Title: fmt.Sprintf("s%d", i)}
		m.selected[i] = true
	}
	m.state = stateConfirm

	next, cmd := m.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(model)

	if m.total != big {
		t.Fatalf("total=%d want %d", m.total, big)
	}
	// targets snapshots the full selection for rendering.
	if len(m.targets) != big {
		t.Fatalf("targets=%d want %d", len(m.targets), big)
	}
	// Started maxConcurrent => queue holds the remainder.
	if want := big - maxConcurrent; len(m.queue) != want {
		t.Fatalf("queue len=%d want %d (cap %d not respected)", len(m.queue), want, maxConcurrent)
	}
	// The returned batch = spinner tick + maxConcurrent delete cmds.
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatalf("expected tea.BatchMsg, got %T", cmd())
	}
	if len(batch) != maxConcurrent+1 {
		t.Fatalf("batch size=%d want %d (=cap+spinner)", len(batch), maxConcurrent+1)
	}
}

// Parallel end-to-end: every selected session is deleted exactly once and the
// run converges to stateDone with done==total, even with the concurrent
// fan-out + backfill path. Uses a real tea.Program so deletes run in
// goroutines (exercises the concurrency, verified with -race).
func TestParallelDeleteAllComplete(t *testing.T) {
	m := sampleModel()
	var (
		mu      sync.Mutex
		deleted = map[string]int{}
	)
	m.deleter = func(id string) error {
		time.Sleep(2 * time.Millisecond) // force overlap between goroutines
		mu.Lock()
		deleted[id]++
		mu.Unlock()
		return nil
	}
	for i := range m.sessions {
		m.selected[i] = true
	}
	total := len(m.sessions)

	fm := runProgram(t, m)

	if fm.state != stateDone {
		t.Fatalf("final state=%d want stateDone", fm.state)
	}
	if fm.done != total || len(fm.results) != total {
		t.Fatalf("done=%d results=%d want %d", fm.done, len(fm.results), total)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(deleted) != total {
		t.Fatalf("distinct deleted=%d want %d (%v)", len(deleted), total, deleted)
	}
	for id, n := range deleted {
		if n != 1 {
			t.Errorf("session %s deleted %d times want 1", id, n)
		}
	}
}

// Full flow: confirm -> deleting -> done, driven through Update with a fake
// deleter. Verifies the injected deleter is called for every selected id and
// that results/state land correctly. This is the seam the refactor added.
func TestConfirmToDoneFlow(t *testing.T) {
	m := sampleModel()
	var deleted []string
	m.deleter = func(id string) error {
		deleted = append(deleted, id)
		if id == "ses_c" {
			return fmt.Errorf("boom")
		}
		return nil
	}
	m.selected[0] = true // ses_a ok
	m.selected[2] = true // ses_c fails

	// Press 'y' on the confirm screen.
	m.state = stateConfirm
	next, cmd := m.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(model)
	if m.state != stateDeleting {
		t.Fatalf("state=%d want stateDeleting", m.state)
	}

	// Pump commands until we reach stateDone. Each delete cmd yields a
	// deleteDoneMsg; the terminal cmd yields allDoneMsg -> stateDone + quit.
	m = pump(t, m, cmd)

	if m.state != stateDone {
		t.Fatalf("final state=%d want stateDone", m.state)
	}
	if len(deleted) != 2 || deleted[0] != "ses_a" || deleted[1] != "ses_c" {
		t.Fatalf("deleter called with %v want [ses_a ses_c]", deleted)
	}
	if len(m.results) != 2 {
		t.Fatalf("results=%d want 2", len(m.results))
	}
	if fail := reportFailures(m.results); fail != 1 {
		t.Fatalf("reportFailures=%d want 1", fail)
	}
}

// pump repeatedly executes a command and feeds resulting messages back into
// Update until no command remains, ignoring spinner ticks (which would loop
// forever). Batched commands are flattened via tea.BatchMsg.
func pump(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	for i := 0; len(queue) > 0; i++ {
		if i > 1000 {
			t.Fatal("pump did not converge")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg.(tea.BatchMsg)...)
			continue
		case spinner.TickMsg:
			// Drop spinner ticks so the loop terminates.
			continue
		}
		next, nc := m.Update(msg)
		m = next.(model)
		if nc != nil {
			queue = append(queue, nc)
		}
		if m.state == stateDone {
			return m
		}
	}
	return m
}
