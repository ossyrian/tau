package tui

import (
	bkey "charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
)

// pane draws body inside a rounded border of exactly w×h cells, with title set
// into the top edge as lazygit does. The title is a second lipgloss layer laid
// over the box, so the border is never rebuilt by hand. An active pane has a
// coloured border; the others follow the terminal's dim border colour.
func pane(title string, w, h int, active bool, body string) string {
	edge := theme.Border
	if active {
		edge = theme.Accent
	}
	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(edge.GetForeground()).
		Width(w).Height(h).MaxWidth(w).MaxHeight(h).
		Render(body)
	if title == "" || w < 8 {
		return box
	}
	label := " " + ansi.Truncate(title, w-6, "…") + " "
	over := lipgloss.NewLayer(label).X(2).Z(1)
	return lipgloss.NewCompositor(lipgloss.NewLayer(box).AddLayers(over)).Render()
}

// inner is the room inside a pane of w×h cells.
func inner(w, h int) (int, int) { return max(w-2, 1), max(h-2, 1) }

// Sizes of the screen's regions. Everything is derived from the terminal size,
// so the panes grow and shrink with it.
const (
	minWidth  = 60
	minHeight = 12
)

func tooSmall(w, h int) bool { return w < minWidth || h < minHeight }

// sidebarWidth is a fifth of the screen, within bounds.
func sidebarWidth(w int) int { return min(max(w/5, 16), 28) }

// splitHeight divides h rows between a list pane on top and a detail pane
// below: two fifths and three fifths, each at least 5 rows.
func splitHeight(h int) (top, bottom int) {
	top = min(max(h*2/5, 5), max(h-5, 5))
	return top, max(h-top, 3)
}

// column places panes one above the other.
func column(panes ...string) string { return lipgloss.JoinVertical(lipgloss.Left, panes...) }

// row places panes side by side.
func row(panes ...string) string { return lipgloss.JoinHorizontal(lipgloss.Top, panes...) }

func binding(k, help, desc string) bkey.Binding {
	return bkey.NewBinding(bkey.WithKeys(k), bkey.WithHelp(help, desc))
}

// stateMessage is what a section shows in place of its data: the error, or
// "loading…" until the first load ends. It is "" when the data can be drawn.
func stateMessage(err error, loaded bool) string {
	switch {
	case err != nil:
		return err.Error()
	case !loaded:
		return "loading…"
	}
	return ""
}

// messagePane is a single pane holding text, for errors and empty states.
func messagePane(title string, w, h int, text string) string {
	iw, _ := inner(w, h)
	return pane(title, w, h, true, lipgloss.NewStyle().Width(iw).Foreground(theme.Dim.GetForeground()).Faint(true).Render(text))
}

// varsPane draws a variable table; while a value is being typed, the input
// takes a short pane above the table.
func varsPane(title string, w, h int, e *varEditor) string {
	if e.step == varBrowse {
		iw, ih := inner(w, h)
		e.pick.SetSize(iw, ih)
		return pane(title, w, h, true, e.pick.View())
	}
	pt, pb := e.prompt()
	top := min(5, h-4)
	iw, ih := inner(w, h-top)
	e.pick.SetSize(iw, ih)
	return column(pane(pt, w, top, true, pb), pane(title, w, h-top, false, e.pick.View()))
}
