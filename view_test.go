package main

import (
	"fmt"
	"strings"
	"testing"
)

// listContent must render every session title on the first frame (regression:
// the box rendered empty before WindowSizeMsg arrived).
func TestListContentRendersAllTitles(t *testing.T) {
	m := sampleModel()
	out := m.listContent()
	for _, want := range []string{"Reading PRD.md", "Greeting", "Tanstack"} {
		if !strings.Contains(out, want) {
			t.Errorf("listContent missing %q\n---\n%s", want, out)
		}
	}
	if n := strings.Count(strings.TrimRight(out, "\n"), "\n"); n != 2 { // 3 rows => 2 interior newlines
		t.Errorf("expected 3 rows, got %d newlines:\n%s", n, out)
	}
}

func TestCursorMarkerRendered(t *testing.T) {
	m := sampleModel()
	if !strings.Contains(m.listContent(), ">") {
		t.Error("cursor row marker '>' not rendered")
	}
}

// The deleting view must render EVERY target up front (pending rows as "…"),
// flipping to ✓/✗ for finished ones — not just the completed results. This is
// the fix for the list only flashing at the end of a fast parallel delete.
func TestViewDeletingShowsAllTargetsWithStatus(t *testing.T) {
	m := sampleModel()
	m.targets = m.sessions // all three selected
	m.total = 3
	m.state = stateDeleting

	// ses_a finished OK, ses_b failed, ses_c still pending.
	m.results = []result{
		{id: "ses_a", title: "Reading PRD.md"},
		{id: "ses_b", title: "Greeting", err: fmt.Errorf("boom")},
	}
	m.done = 2

	out := m.viewDeleting()
	for _, want := range []string{"Reading PRD.md", "Greeting", "Tanstack"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewDeleting missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "✓ Reading PRD.md") {
		t.Error("completed-ok row missing ✓ marker")
	}
	if !strings.Contains(out, "✗ Greeting") {
		t.Error("failed row missing ✗ marker")
	}
	// Pending row (not in results) shows the dim "…" marker.
	if !strings.Contains(out, "… ") || !strings.Contains(out, "2 / 3") {
		t.Errorf("pending marker / progress missing\n---\n%s", out)
	}
}

func TestListLayout(t *testing.T) {
	// Wide terminal: columns sum (plus gaps) stay within width.
	titleW, timeW, dirW := listLayout(120)
	if titleW < 8 || timeW != 10 || dirW <= 0 {
		t.Errorf("listLayout(120)=(%d,%d,%d) unexpected", titleW, timeW, dirW)
	}
	// Tiny/degenerate width must still yield sane minimums, never negative.
	titleW, timeW, dirW = listLayout(0)
	if titleW < 8 || timeW != 10 || dirW < 0 {
		t.Errorf("listLayout(0)=(%d,%d,%d) must clamp to minimums", titleW, timeW, dirW)
	}
}
