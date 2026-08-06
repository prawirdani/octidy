package main

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// listLayout computes the column widths (title, time, dir) for a given overall
// terminal width. Kept separate so the geometry is testable and tunable in one
// place.
func listLayout(width int) (titleW, timeW, dirW int) {
	total := width - 6
	if total < 30 {
		total = 30
	}
	timeW = 10
	dirW = total / 3
	titleW = total - timeW - dirW - 6
	if titleW < 8 {
		titleW = 8
	}
	return titleW, timeW, dirW
}

// listContent renders the scrollable session rows (viewport body).
func (m model) listContent() string {
	titleW, timeW, dirW := listLayout(m.w)

	var b []byte
	for i, s := range m.sessions {
		cursor := "  "
		if i == m.cursor {
			cursor = "> "
		}
		check := "[ ]"
		if m.selected[i] {
			check = checkStyle.Render("[✓]")
		}
		title := truncate(s.Title, titleW)
		when := truncate(relTime(s.Updated), timeW)
		dir := truncate(s.Directory, dirW)

		titleR := normalRowStyle.Render(fmt.Sprintf("%-*s", titleW, title))
		if i == m.cursor {
			titleR = cursorRowStyle.Render(fmt.Sprintf("%-*s", titleW, title))
		}
		line := fmt.Sprintf("%s%s %s  %s  %s",
			cursor, check, titleR,
			dimStyle.Render(fmt.Sprintf("%-*s", timeW, when)),
			dimStyle.Render(dir),
		)
		b = append(b, line...)
		b = append(b, '\n')
	}
	return string(b)
}

// View dispatches to the per-state renderer.
func (m model) View() string {
	switch m.state {
	case stateConfirm:
		return m.viewConfirm()
	case stateDeleting, stateDone:
		return m.viewDeleting()
	default:
		return m.viewSelect()
	}
}

func (m model) viewSelect() string {
	title := titleStyle.Render(" opencode session cleaner ")
	count := countStyle.Render(fmt.Sprintf("%d selected", m.countSelected()))
	header := lipgloss.JoinHorizontal(lipgloss.Center, title, "  ",
		dimStyle.Render(fmt.Sprintf("%d sessions", len(m.sessions))), "  ", count)

	body := boxStyle.Width(m.w - 2).Render(m.vp.View())

	help := helpStyle.Render(
		"↑/↓ move · space select · a all · n none · enter delete · q quit")

	return fmt.Sprintf("%s\n%s\n%s", header, body, help)
}

func (m model) viewConfirm() string {
	sel := m.selectedSessions()
	var lines string
	for _, s := range sel {
		lines += "  " + errStyle.Render("✗") + " " +
			normalRowStyle.Render(truncate(s.Title, m.w-6)) + "\n"
	}
	warn := warnStyle.Render(fmt.Sprintf(
		"⚠  Delete %d session(s)? This is irreversible.", len(sel)))
	prompt := fmt.Sprintf("%s  %s",
		countStyle.Render("[y]"), dimStyle.Render("yes"))
	prompt2 := fmt.Sprintf("%s  %s",
		countStyle.Render("[N]"), dimStyle.Render("no (default)"))

	box := boxStyle.BorderForeground(yellow).Width(m.w - 2).Render(
		warn + "\n\n" + lines + "\n" + prompt + "    " + prompt2)
	return box
}

func (m model) viewDeleting() string {
	// Render the FULL selection in original order, with a live per-item status.
	// Pending rows show "…", finished rows flip to ✓/✗ as results arrive.
	lines := m.renderTargets()

	if m.state == stateDone {
		ok, fail := 0, 0
		for _, r := range m.results {
			if r.err != nil {
				fail++
			} else {
				ok++
			}
		}
		summary := okStyle.Render(fmt.Sprintf("Deleted %d", ok))
		if fail > 0 {
			summary += "  " + errStyle.Render(fmt.Sprintf("Failed %d", fail))
		}
		return boxStyle.Width(m.w - 2).Render(lines + "\n" + summary)
	}

	head := fmt.Sprintf("%s Deleting sessions…", m.spinner.View())
	progress := dimStyle.Render(fmt.Sprintf("%d / %d", m.done, m.total))
	return boxStyle.Width(m.w - 2).Render(head + "  " + progress + "\n\n" + lines)
}

// renderTargets renders every target session once, matching finished results
// by id. Linear scan per row is fine: selections are small.
func (m model) renderTargets() string {
	var b []byte
	for _, s := range m.targets {
		res, done := m.resultFor(s.ID)
		if done && res.err != nil {
			b = append(b, errStyle.Render("✗ "+truncate(s.Title, m.w-20))...)
			b = append(b, dimStyle.Render(" "+truncate(oneLine(res.err.Error()), 16))...)
		} else if done {
			b = append(b, okStyle.Render("✓ "+truncate(s.Title, m.w-6))...)
		} else {
			b = append(b, dimStyle.Render("… "+truncate(s.Title, m.w-6))...)
		}
		b = append(b, '\n')
	}
	return string(b)
}

func (m model) resultFor(id string) (result, bool) {
	for _, r := range m.results {
		if r.id == id {
			return r, true
		}
	}
	return result{}, false
}
