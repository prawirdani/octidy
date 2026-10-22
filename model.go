package main

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// doneLinger is how long the final "Deleted N" summary stays on screen before
// the program exits, so the result is actually visible.
const doneLinger = 1200 * time.Millisecond

// quitMsg is sent after doneLinger elapses on the done screen.
type quitMsg struct{}

// state is the top-level screen the TUI is showing.
type state int

const (
	stateSelect   state = iota // choosing which sessions to delete
	stateConfirm               // y/N gate before deleting
	stateDeleting              // deletes running, results streaming in
	stateDone                  // all deletes finished; app about to quit
)

// Default viewport geometry so the list renders on the very first frame,
// before any WindowSizeMsg arrives (some terminals/pipes deliver it late).
const (
	defWidth  = 80
	defHeight = 24
)

// maxConcurrent caps how many deletes run at once. opencode's store is
// single-writer SQLite, so we fan out but keep a lid on it rather than
// spawning one subprocess per selected session.
// ponytail: bump if the DB write path stops being the bottleneck.
const maxConcurrent = 8

// result is the outcome of one delete, shown live in the deleting view.
type result struct {
	id    string
	title string
	err   error
}

// deleteDoneMsg is emitted after one delete finishes so the spinner keeps
// animating and results render live.
type deleteDoneMsg result

type model struct {
	sessions []Session
	cursor   int // indexes into the VISIBLE slice, not m.sessions
	selected map[int]bool
	state    state

	// filter is the live query typed after '/'. filtering is true while the
	// user is typing it. Both are a VIEW over m.sessions — never a mutation —
	// so selected/cursor indices stay valid and no session is deleted by the
	// wrong index. Resolve cursor -> real index via visibleIndexes().
	filter    string
	filtering bool

	vp      viewport.Model
	vpReady bool
	spinner spinner.Model
	targets []Session // immutable snapshot of the full selection (for rendering)
	queue   []Session // sessions pending deletion (not yet started)
	total   int       // total deletes requested this run
	done    int       // deletes finished (results arrive out of order)
	results []result
	w, h    int

	// deleter performs a single delete. Defaults to deleteSession; swapped for
	// a fake in tests so the confirm->deleting->done flow is exercisable without
	// a real opencode install or a TTY.
	deleter func(id string) error

	// collect resolves the on-disk artifacts owned by the sessions about to be
	// deleted, keyed by session id. It must run before the deletes; afterwards
	// the stored parts that point at those files are gone. Swappable in tests.
	collect func(ids []string) map[string][]string

	// artifacts holds what collect found, pruned once the TUI exits.
	artifacts map[string][]string
}

func newModel(sessions []Session) model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	m := model{
		sessions: sessions,
		selected: make(map[int]bool),
		state:    stateSelect,
		spinner:  sp,
		w:        defWidth,
		h:        defHeight,
		deleter:  deleteSession,
	}
	m.vp = viewport.New(defWidth-4, defHeight-8)
	m.vpReady = true
	m.vp.SetContent(m.listContent())
	return m
}

func (m model) Init() tea.Cmd { return m.spinner.Tick }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		// Reserve rows for title, help, borders, summary.
		vpHeight := msg.Height - 8
		if vpHeight < 3 {
			vpHeight = 3
		}
		m.vp.Width = msg.Width - 4
		m.vp.Height = vpHeight
		m.vp.SetContent(m.listContent())
		return m, nil

	case tea.KeyMsg:
		switch m.state {
		case stateSelect:
			return m.updateSelect(msg)
		case stateConfirm:
			return m.updateConfirm(msg)
		}

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case quitMsg:
		return m, tea.Quit

	case deleteDoneMsg:
		m.results = append(m.results, result(msg))
		m.done++
		if m.done >= m.total {
			m.state = stateDone
			// Linger so the "Deleted N" summary actually paints before we quit.
			return m, tea.Tick(doneLinger, func(time.Time) tea.Msg { return quitMsg{} })
		}
		// Backfill: as one delete finishes, start the next queued one so we
		// keep ~maxConcurrent in flight.
		if len(m.queue) > 0 {
			return m, m.deleteNext()
		}
		return m, nil
	}
	return m, nil
}

// visibleIndexes returns the indices into m.sessions whose Title OR Directory
// contains the filter (case-insensitive substring). With an empty filter every
// index is visible, in order. This is the single source of truth for the
// cursor -> real-session-index mapping: never reslice m.sessions itself.
func (m model) visibleIndexes() []int {
	if m.filter == "" {
		idx := make([]int, len(m.sessions))
		for i := range m.sessions {
			idx[i] = i
		}
		return idx
	}
	q := strings.ToLower(m.filter)
	var out []int
	for i, s := range m.sessions {
		if strings.Contains(strings.ToLower(s.Title), q) ||
			strings.Contains(strings.ToLower(s.Directory), q) {
			out = append(out, i)
		}
	}
	return out
}

// clampCursor keeps the cursor within the current visible list (0 when empty).
func (m *model) clampCursor() {
	n := len(m.visibleIndexes())
	if n == 0 {
		m.cursor = 0
		return
	}
	if m.cursor > n-1 {
		m.cursor = n - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m model) updateSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Filtering mode: capture typing into m.filter. ctrl+c still quits.
	if m.filtering {
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEnter:
			m.filtering = false // stop typing; the filter stays applied
		case tea.KeyEsc:
			m.filter = "" // esc clears the filter and stops typing
			m.filtering = false
		case tea.KeyBackspace:
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case tea.KeyRunes, tea.KeySpace:
			m.filter += string(msg.Runes)
		}
		m.clampCursor()
		m.vp.SetContent(m.listContent())
		m.ensureCursorVisible()
		return m, nil
	}

	vis := m.visibleIndexes()
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "/":
		m.filtering = true
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(vis)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		if len(vis) > 0 {
			m.cursor = len(vis) - 1
		}
	case " ":
		// Self-guard the irreversible-delete path: only toggle when the cursor
		// points at a real visible row, regardless of clamp state.
		if m.cursor < len(vis) {
			real := vis[m.cursor]
			m.selected[real] = !m.selected[real]
		}
	case "a":
		// Select the visible set only (all sessions when no filter).
		for _, real := range vis {
			m.selected[real] = true
		}
	case "n":
		// Clear the visible set only; selections filtered away persist.
		for _, real := range vis {
			delete(m.selected, real)
		}
	case "enter":
		if m.countSelected() > 0 {
			m.state = stateConfirm
		}
		return m, nil
	}
	m.vp.SetContent(m.listContent())
	m.ensureCursorVisible()
	return m, nil
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.targets = m.selectedSessions()
		m.queue = m.targets
		// Capture the artifacts before opencode cascades the parts that
		// reference them; they are pruned after the TUI exits.
		if m.collect != nil {
			ids := make([]string, 0, len(m.targets))
			for _, s := range m.targets {
				ids = append(ids, s.ID)
			}
			m.artifacts = m.collect(ids)
		}
		m.total = len(m.targets)
		m.done = 0
		m.state = stateDeleting
		// Fan out: start up to maxConcurrent deletes at once. tea.Batch runs
		// each command concurrently (one goroutine per command).
		cmds := []tea.Cmd{m.spinner.Tick}
		for i := 0; i < maxConcurrent && len(m.queue) > 0; i++ {
			cmds = append(cmds, m.deleteNext())
		}
		return m, tea.Batch(cmds...)
	case "n", "N", "esc", "ctrl+c":
		m.state = stateSelect
	}
	return m, nil
}

// deleteNext pops one session off the queue and returns a command that deletes
// it. Callers must ensure the queue is non-empty. The command captures its own
// Session, so many can run concurrently without shared mutation.
func (m *model) deleteNext() tea.Cmd {
	s := m.queue[0]
	m.queue = m.queue[1:]
	return func() tea.Msg {
		err := m.deleter(s.ID)
		return deleteDoneMsg{id: s.ID, title: s.Title, err: err}
	}
}

func (m model) countSelected() int {
	n := 0
	for _, v := range m.selected {
		if v {
			n++
		}
	}
	return n
}

func (m model) selectedSessions() []Session {
	var out []Session
	for i, s := range m.sessions {
		if m.selected[i] {
			out = append(out, s)
		}
	}
	return out
}

// ensureCursorVisible scrolls the viewport so the cursor row stays on screen.
func (m *model) ensureCursorVisible() {
	if !m.vpReady {
		return
	}
	top := m.vp.YOffset
	bottom := top + m.vp.Height - 1
	if m.cursor < top {
		m.vp.SetYOffset(m.cursor)
	} else if m.cursor > bottom {
		m.vp.SetYOffset(m.cursor - m.vp.Height + 1)
	}
}
