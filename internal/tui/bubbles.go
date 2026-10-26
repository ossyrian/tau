package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

var noColor color.Color // nil: leave the terminal's own colour alone

// newList is a bubbles list with the title, filter, status bar and help
// turned off: the frame draws those. Rows follow the terminal's palette.
func newList(items []list.Item, w, h int) list.Model {
	// The marker and one space of padding are as wide as the two spaces of an
	// unselected row, so rows do not shift when the selection moves.
	marked := lipgloss.NewStyle().
		Border(lipgloss.Border{Left: selectedMarker}, false, false, false, true).
		BorderForeground(lipgloss.Color("2")).
		PaddingLeft(1)
	d := list.NewDefaultDelegate()
	d.Styles = list.DefaultItemStyles{
		NormalTitle:   lipgloss.NewStyle().PaddingLeft(2),
		NormalDesc:    lipgloss.NewStyle().PaddingLeft(2).Faint(true),
		SelectedTitle: marked.Bold(true).Foreground(lipgloss.Color("2")),
		SelectedDesc:  marked.Foreground(lipgloss.Color("2")).Faint(true),
		DimmedTitle:   lipgloss.NewStyle().PaddingLeft(2).Faint(true),
		DimmedDesc:    lipgloss.NewStyle().PaddingLeft(2).Faint(true),
		FilterMatch:   lipgloss.NewStyle().Underline(true),
	}
	d.SetSpacing(0)
	l := list.New(items, d, w, h)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	l.Styles.NoItems = lipgloss.NewStyle().PaddingLeft(2).Faint(true)
	return l
}

// newFilterList is newList with bubbles' own "/" filter on. The filter's
// input is drawn by the list; its title line stays empty.
func newFilterList() list.Model {
	l := newList(nil, 30, 10)
	l.SetFilteringEnabled(true)
	l.SetShowFilter(true)
	l.Styles.TitleBar = lipgloss.NewStyle()
	l.FilterInput.Prompt = "› "
	l.FilterInput.Placeholder = "search by name or path"
	l.FilterInput.SetStyles(inputStyles())
	l.KeyMap.Filter.SetHelp("/", "search")
	return l
}

// selectedMarker prefixes the selected row so it shows without colour.
const selectedMarker = "▌"

func newHelp(width int) help.Model {
	h := help.New()
	dim := lipgloss.NewStyle().Faint(true)
	h.Styles = help.Styles{
		Ellipsis:       dim,
		ShortKey:       keyStyle,
		ShortDesc:      dim,
		ShortSeparator: dim,
		FullKey:        keyStyle,
		FullDesc:       dim,
		FullSeparator:  dim,
	}
	h.SetWidth(width)
	return h
}

func newSpinner() spinner.Model {
	return spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(infoStyle))
}

func inputStyles() textinput.Styles {
	plain := lipgloss.NewStyle()
	dim := lipgloss.NewStyle().Faint(true)
	return textinput.Styles{
		Focused: textinput.StyleState{Text: plain, Placeholder: dim, Suggestion: dim, Prompt: accentStyle},
		Blurred: textinput.StyleState{Text: dim, Placeholder: dim, Suggestion: dim, Prompt: dim},
		Cursor:  textinput.CursorStyle{Color: noColor, Shape: tea.CursorBlock, Blink: false},
	}
}

func newInput(placeholder string, width int) textinput.Model {
	in := textinput.New()
	in.SetStyles(inputStyles())
	in.Prompt = "› "
	in.Placeholder = placeholder
	in.SetWidth(width)
	return in
}

// newSecretInput hides what is typed, for values like tokens.
func newSecretInput(placeholder string, width int) textinput.Model {
	in := newInput(placeholder, width)
	in.EchoMode = textinput.EchoPassword
	in.EchoCharacter = '•'
	return in
}
