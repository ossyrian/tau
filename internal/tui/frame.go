package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const (
	minWidth  = 60
	minHeight = 12
)

func tooSmall(w, h int) bool { return w < minWidth || h < minHeight }

// sidebarWidth fits the section names, capped so a long name cannot squeeze
// the content.
func sidebarWidth(items []string) int {
	w := 0
	for _, s := range items {
		w = max(w, ansi.StringWidth(s))
	}
	return min(max(w+4, 12), 16)
}

// The border colour is ANSI "bright black", so the user's theme picks the shade.
var borderColor = lipgloss.Color("8")

// clip cuts every line to width columns, marking the cut with "…", and keeps
// at most rows lines. lipgloss wraps long lines and treats Height as a
// minimum, so both limits are applied before rendering.
func clip(lines []string, width, rows int) string {
	if len(lines) > rows {
		lines = lines[:rows]
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, width, "…")
	}
	return strings.Join(out, "\n")
}

// box returns a style whose border has the given sides and corners. The three
// boxes of the screen use corner glyphs that join into one drawing.
func box(b lipgloss.Border, top, right, bottom, left bool) lipgloss.Style {
	return lipgloss.NewStyle().Border(b, top, right, bottom, left).BorderForeground(borderColor)
}

// bodySize is the room a section has inside frame: columns and rows.
func bodySize(w, h int, sidebar []string) (cols, rows int) {
	return w - sidebarWidth(sidebar) - 2, h - 5
}

// frame lays the screen out in w×h cells: a title bar, then a sidebar and a
// body side by side, then a help line. Each part is a lipgloss box; the
// corner glyphs make the shared edges meet.
func frame(w, h int, title string, sidebar, body []string, help string) string {
	side := sidebarWidth(sidebar)
	rows := h - 5 // top border, title, divider, bottom border, help

	header := box(lipgloss.Border{Top: "─", Left: "│", Right: "│", TopLeft: "╭", TopRight: "╮"},
		true, true, false, true).
		Width(w).Height(1).
		Render(clip([]string{" " + title}, w-3, 1))

	sideBox := box(lipgloss.Border{Top: "─", Left: "│", Right: "│", Bottom: "─",
		TopLeft: "├", TopRight: "┬", BottomLeft: "╰", BottomRight: "┴"},
		true, true, true, true).
		Width(side).Height(rows + 2).
		Render(clip(sidebar, side-2, rows))

	bodyBox := box(lipgloss.Border{Top: "─", Right: "│", Bottom: "─",
		TopRight: "┤", BottomRight: "╯"},
		true, true, true, false).
		Width(w - side).Height(rows + 2).
		PaddingLeft(1).Render(clip(body, w-side-2, rows))

	panel := lipgloss.JoinHorizontal(lipgloss.Top, sideBox, bodyBox)
	helpLine := clip([]string{" " + help}, w, 1)
	return lipgloss.JoinVertical(lipgloss.Left, header, panel, helpLine)
}

// wrap wraps text to width columns in style st, as separate lines. Errors and
// explanations wrap; structured rows are cut.
func wrap(st lipgloss.Style, text string, width int) []string {
	return strings.Split(st.Width(max(width, 10)).Render(text), "\n")
}

// tabBar draws tab names with the active one marked.
func tabBar(names []string, active int) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = tabStyle.Render(n)
		if i == active {
			out[i] = activeTabStyle.Render(n)
		}
	}
	return strings.Join(out, "   ")
}
