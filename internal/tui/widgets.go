package tui

import (
	"image/color"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/theme"
)

var noColor color.Color // nil: leave the terminal's own colour alone

func newHelp(width int) help.Model {
	h := help.New()
	h.Styles = help.Styles{
		Ellipsis: theme.Dim, ShortKey: theme.Key, ShortDesc: theme.Dim, ShortSeparator: theme.Dim,
		FullKey: theme.Key, FullDesc: theme.Dim, FullSeparator: theme.Dim,
	}
	h.SetWidth(width)
	return h
}

func newSpinner() spinner.Model {
	return spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(theme.Info))
}

func inputStyles() textinput.Styles {
	plain := lipgloss.NewStyle()
	return textinput.Styles{
		Focused: textinput.StyleState{Text: plain, Placeholder: theme.Dim, Suggestion: theme.Dim, Prompt: theme.Accent},
		Blurred: textinput.StyleState{Text: theme.Dim, Placeholder: theme.Dim, Suggestion: theme.Dim, Prompt: theme.Dim},
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

// newNav is the section list: a bubbles list with every extra turned off, one
// line per item.
func newNav() list.Model {
	d := list.NewDefaultDelegate()
	d.ShowDescription = false
	d.SetSpacing(0)
	d.SetHeight(1)
	d.Styles = list.DefaultItemStyles{
		NormalTitle:   lipgloss.NewStyle().PaddingLeft(2),
		SelectedTitle: lipgloss.NewStyle().PaddingLeft(1).Bold(true).Foreground(lipgloss.Color("2")).Border(lipgloss.Border{Left: "▌"}, false, false, false, true).BorderForeground(lipgloss.Color("2")),
		DimmedTitle:   lipgloss.NewStyle().PaddingLeft(2).Faint(true),
	}
	l := list.New(nil, d, 20, 10)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	l.DisableQuitKeybindings()
	return l
}

// navItem is one section in the nav list.
type navItem struct{ name, badge string }

func (n navItem) FilterValue() string { return n.name }
func (n navItem) Title() string {
	if n.badge == "" {
		return n.name
	}
	return n.name + "  " + theme.Warn.Render(n.badge)
}
func (n navItem) Description() string { return "" }
