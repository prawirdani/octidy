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

// filterModel builds a model whose sessions deliberately have titles/dirs that
// filter into a DIFFERENT visible order than storage order, so index-mapping
// bugs surface. sessions: [0]=apple, [1]=banana, [2]=avocado.
func filterModel() model {
	s := []Session{
		{ID: "ses_0", Title: "apple", Directory: "/proj/red"},
		{ID: "ses_1", Title: "banana", Directory: "/proj/blue"},
		{ID: "ses_2", Title: "avocado", Directory: "/proj/red"},
	}
	return newModel(s)
}

func key(s string) tea.KeyMsg {
	switch s {
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// applyFilter drives the model through '/'-typing to set a filter, matching the
// real key path rather than poking m.filter directly.
func applyFilter(m model, q string) model {
	next, _ := m.updateSelect(key("/"))
	m = next.(model)
	for _, r := range q {
		next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	next, _ = m.updateSelect(key("enter")) // stop typing, keep filter
	return next.(model)
}

func TestVisibleIndexesMatch(t *testing.T) {
	m := filterModel()
	// Empty filter => all indices in order.
	if got := m.visibleIndexes(); len(got) != 3 || got[0] != 0 || got[2] != 2 {
		t.Fatalf("empty filter visible=%v want [0 1 2]", got)
	}
	// By title, case-insensitive: "AV" matches "avocado" only (index 2).
	m.filter = "AV"
	if got := m.visibleIndexes(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("filter 'AV' visible=%v want [2]", got)
	}
	// By directory, case-insensitive: "BLUE" matches ses_1's dir (index 1).
	m.filter = "BLUE"
	if got := m.visibleIndexes(); len(got) != 1 || got[0] != 1 {
		t.Fatalf("filter 'BLUE' (dir) visible=%v want [1]", got)
	}
	// "red" matches ses_0 + ses_2 by directory, indices 0 and 2 — order kept.
	m.filter = "red"
	if got := m.visibleIndexes(); len(got) != 2 || got[0] != 0 || got[1] != 2 {
		t.Fatalf("filter 'red' visible=%v want [0 2]", got)
	}
}

// CRITICAL: filtering must not misroute selection. Filter to a set whose first
// VISIBLE row is NOT storage index 0, toggle it, and assert the correct
// underlying session id is what selectedSessions() returns. Guards the
// data-loss bug (cursor is a visible position, not a session index).
func TestFilterSelectionMapsToCorrectSession(t *testing.T) {
	m := filterModel()
	// Filter "avocado": only storage index 2 is visible, at cursor position 0.
	m = applyFilter(m, "avocado")
	vis := m.visibleIndexes()
	if len(vis) != 1 || vis[0] != 2 {
		t.Fatalf("visible=%v want [2] under filter 'avocado'", vis)
	}
	// Cursor is at visible position 0. Space must select storage index 2.
	next, _ := m.updateSelect(key(" "))
	m = next.(model)

	sel := m.selectedSessions()
	if len(sel) != 1 || sel[0].ID != "ses_2" {
		t.Fatalf("selectedSessions=%v want [ses_2] — WRONG SESSION SELECTED (data-loss bug)", sel)
	}
	// The raw selected map must key on the storage index (2), never position 0.
	if m.selected[0] {
		t.Fatal("selected[0] set — cursor position leaked as session index")
	}
	if !m.selected[2] {
		t.Fatal("selected[2] not set — real index resolution failed")
	}
}

func TestFilterCursorClampsWhenListShrinks(t *testing.T) {
	m := filterModel()
	m.cursor = 2 // last row, no filter
	m = applyFilter(m, "banana")
	// Only 1 match => cursor must clamp to 0.
	if m.cursor != 0 {
		t.Fatalf("cursor=%d want 0 after filter shrank list", m.cursor)
	}
}

func TestEmptyMatchIsNoOp(t *testing.T) {
	m := filterModel()
	m = applyFilter(m, "zzz-nomatch")
	if got := m.visibleIndexes(); len(got) != 0 {
		t.Fatalf("visible=%v want empty", got)
	}
	// space and enter must not panic and must not select/confirm anything.
	next, _ := m.updateSelect(key(" "))
	m = next.(model)
	if m.countSelected() != 0 {
		t.Fatalf("countSelected=%d want 0 on empty match", m.countSelected())
	}
	next, _ = m.updateSelect(key("enter"))
	m = next.(model)
	if m.state != stateSelect {
		t.Fatalf("state=%d want stateSelect (enter must be no-op with nothing selected)", m.state)
	}
}

func TestSelectionPersistsAcrossFilterChange(t *testing.T) {
	m := filterModel()
	// Select banana (storage index 1) with no filter.
	next, _ := m.updateSelect(key("down")) // cursor -> 1
	m = next.(model)
	next, _ = m.updateSelect(key(" "))
	m = next.(model)
	if !m.selected[1] {
		t.Fatal("precondition: ses_1 should be selected")
	}
	// Filter it away, then clear the filter. Selection must survive.
	m = applyFilter(m, "avocado") // banana not visible
	m.filter = ""                 // clear filter
	sel := m.selectedSessions()
	if len(sel) != 1 || sel[0].ID != "ses_1" {
		t.Fatalf("selectedSessions=%v want [ses_1] — selection lost across filter", sel)
	}
}

func TestSelectAllNoneOperateOnVisibleSet(t *testing.T) {
	m := filterModel()
	// Filter "red" => visible storage indices [0, 2] (apple, avocado dirs).
	m = applyFilter(m, "red")
	// 'a' selects the visible set only.
	next, _ := m.updateSelect(key("a"))
	m = next.(model)
	if !m.selected[0] || !m.selected[2] || m.selected[1] {
		t.Fatalf("select-all under filter set %v want only 0,2", m.selected)
	}
	// Pre-select the filtered-away row, then 'n' must clear ONLY visible rows.
	m.selected[1] = true
	next, _ = m.updateSelect(key("n"))
	m = next.(model)
	if m.selected[0] || m.selected[2] {
		t.Fatalf("select-none left visible rows selected: %v", m.selected)
	}
	if !m.selected[1] {
		t.Fatal("select-none cleared a NON-visible selection (should persist)")
	}
}

// CURSOR>0 MAPPING: with visible storage indices [0,2], moving the cursor to
// visible position 1 and pressing space must select storage index 2 (not the
// visible position, not index 1). Proves mapping at a non-zero cursor.
func TestFilterSelectionMapsAtNonZeroCursor(t *testing.T) {
	m := filterModel()
	m = applyFilter(m, "red") // visible storage indices [0, 2]
	if vis := m.visibleIndexes(); len(vis) != 2 || vis[0] != 0 || vis[1] != 2 {
		t.Fatalf("visible=%v want [0 2]", vis)
	}
	next, _ := m.updateSelect(key("down")) // cursor -> visible position 1
	m = next.(model)
	next, _ = m.updateSelect(key(" "))
	m = next.(model)

	if !m.selected[2] {
		t.Fatal("selected[2] not set — non-zero cursor mapped wrong")
	}
	if m.selected[1] {
		t.Fatal("selected[1] set — cursor position leaked as session index")
	}
	sel := m.selectedSessions()
	if len(sel) != 1 || sel[0].ID != "ses_2" {
		t.Fatalf("selectedSessions=%v want [ses_2]", sel)
	}
}

// DELETE-UNDER-ACTIVE-FILTER end to end: a pre-selected but hidden session must
// survive into the delete targets alongside a visible selection, and no wrong
// id may appear. Drives updateConfirm('y') like the existing flow tests.
func TestDeleteUnderActiveFilterKeepsHiddenSelection(t *testing.T) {
	m := filterModel()
	m.deleter = func(id string) error { return nil }
	// Pre-select banana (storage index 1) with no filter.
	m.selected[1] = true
	// Apply a filter that HIDES banana; select a visible row too.
	m = applyFilter(m, "red") // visible [0, 2]
	next, _ := m.updateSelect(key(" "))
	m = next.(model) // toggles storage index 0 (cursor at visible pos 0)

	// Confirm delete.
	m.state = stateConfirm
	next, cmd := m.updateConfirm(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	m = next.(model)

	got := map[string]bool{}
	for _, s := range m.targets {
		got[s.ID] = true
	}
	if len(m.targets) != 2 || !got["ses_0"] || !got["ses_1"] {
		t.Fatalf("targets=%v want {ses_0, ses_1} (hidden pre-selection must survive)", m.targets)
	}
	if got["ses_2"] {
		t.Fatal("ses_2 in targets — wrong id leaked into delete set")
	}
	// Drain to done so the fake deleter path is exercised too.
	m = pump(t, m, cmd)
	if m.state != stateDone || m.done != 2 {
		t.Fatalf("state=%d done=%d want stateDone/2", m.state, m.done)
	}
}

// UNICODE BACKSPACE: backspace removes a whole rune, not a byte. "café" -> "caf".
func TestFilterBackspaceUnicode(t *testing.T) {
	m := filterModel()
	next, _ := m.updateSelect(key("/"))
	m = next.(model)
	for _, r := range "café" {
		next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = next.(model)
	}
	next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeyBackspace})
	m = next.(model)
	if m.filter != "caf" {
		t.Fatalf("filter=%q want %q (rune-safe backspace)", m.filter, "caf")
	}
}

// SPACE-IN-FILTER: a real space key (KeySpace WITH Runes) lands in the filter
// while typing and must NOT toggle any selection.
func TestFilterSpaceKeyTypesNotToggles(t *testing.T) {
	m := filterModel()
	next, _ := m.updateSelect(key("/"))
	m = next.(model)
	next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	m = next.(model)
	// Real space keypress carries Runes: []rune{' '}.
	next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = next.(model)
	next, _ = m.updateSelect(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	m = next.(model)

	if m.filter != "a b" {
		t.Fatalf("filter=%q want %q (space must type)", m.filter, "a b")
	}
	if m.countSelected() != 0 {
		t.Fatalf("countSelected=%d want 0 (space while filtering must not toggle)", m.countSelected())
	}
}

// EMPTY-SET NO-PANIC: a filter matching nothing makes space/enter/G no-ops with
// no selection and no panic; backspacing a short filter to empty resets state.
func TestEmptyMatchKeysNoPanic(t *testing.T) {
	m := filterModel()
	m = applyFilter(m, "zzz-nomatch")
	if len(m.visibleIndexes()) != 0 {
		t.Fatal("precondition: filter must match nothing")
	}
	for _, k := range []string{" ", "enter", "G"} {
		next, _ := m.updateSelect(key(k))
		m = next.(model)
	}
	if m.countSelected() != 0 {
		t.Fatalf("countSelected=%d want 0 on empty match", m.countSelected())
	}
	if m.state != stateSelect {
		t.Fatalf("state=%d want stateSelect", m.state)
	}

	// Backspace a short filter down to empty: filter "" and typing stopped.
	m2 := filterModel()
	next, _ := m2.updateSelect(key("/"))
	m2 = next.(model)
	next, _ = m2.updateSelect(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
	m2 = next.(model)
	next, _ = m2.updateSelect(tea.KeyMsg{Type: tea.KeyBackspace})
	m2 = next.(model)
	if m2.filter != "" {
		t.Fatalf("filter=%q want empty after backspace", m2.filter)
	}
	next, _ = m2.updateSelect(key("enter")) // stop typing
	m2 = next.(model)
	if m2.filtering {
		t.Fatal("filtering still true after enter")
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
		switch msg := msg.(type) {
		case tea.BatchMsg:
			queue = append(queue, msg...)
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
