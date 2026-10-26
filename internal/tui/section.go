package tui

import (
	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// section is one screen in the sidebar.
type section interface {
	// Name is the sidebar label.
	Name() string
	// Enter runs when the section becomes active; it loads data.
	Enter() tea.Cmd
	// Update handles a message and returns a command.
	Update(msg tea.Msg) tea.Cmd
	// View returns exactly the lines to draw inside cols×rows.
	View(cols, rows int) []string
	// Keys lists the bindings shown in the help line.
	Keys() []bkey.Binding
	// Capturing is true while a text input has the keyboard, so the root
	// does not take q, tab or digits.
	Capturing() bool
}

func binding(k, help, desc string) bkey.Binding {
	return bkey.NewBinding(bkey.WithKeys(k), bkey.WithHelp(help, desc))
}
