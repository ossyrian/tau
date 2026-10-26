// Package theme holds the styles shared by the interactive interface and the
// command line. They use only the terminal's own 16 ANSI colours and text
// attributes, so the user's theme (and a transparent background) shows
// through. Nothing here sets a background colour or reverse video.
package theme

import "charm.land/lipgloss/v2"

var (
	Title  = lipgloss.NewStyle().Bold(true)
	Dim    = lipgloss.NewStyle().Faint(true)
	Accent = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green
	OK     = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	Warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow
	Err    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")) // red
	Info   = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan

	Selected  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("2"))
	Border    = lipgloss.NewStyle().Faint(true)
	Tab       = lipgloss.NewStyle().Faint(true)
	ActiveTab = lipgloss.NewStyle().Bold(true).Underline(true)
	Key       = lipgloss.NewStyle().Bold(true)
)
