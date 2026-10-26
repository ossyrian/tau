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
	// View draws the section in exactly w×h cells. A section is one pane, or
	// a list pane above a detail pane.
	View(w, h int) string
	// Keys lists the bindings shown in the help line.
	Keys() []bkey.Binding
	// Capturing is true while a text input has the keyboard, so the root
	// does not take q, tab or digits.
	Capturing() bool
}
