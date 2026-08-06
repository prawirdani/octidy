package main

import (
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

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
	cursor   int
	selected map[int]bool
	state    state

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

	case deleteDoneMsg:
		m.results = append(m.results, result(msg))
		m.done++
		if m.done >= m.total {
			m.state = stateDone
			return m, tea.Quit
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

func (m model) updateSelect(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q", "esc":
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.sessions)-1 {
			m.cursor++
		}
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(m.sessions) - 1
	case " ":
		m.selected[m.cursor] = !m.selected[m.cursor]
	case "a":
		for i := range m.sessions {
			m.selected[i] = true
		}
	case "n":
		m.selected = make(map[int]bool)
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
