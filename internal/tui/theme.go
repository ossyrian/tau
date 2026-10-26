package tui

import "github.com/ossyrian/tau/internal/theme"

// The palette lives in package theme, shared with the command line.
var (
	titleStyle  = theme.Title
	dimStyle    = theme.Dim
	accentStyle = theme.Accent
	okStyle     = theme.OK
	warnStyle   = theme.Warn
	errStyle    = theme.Err
	infoStyle   = theme.Info

	selectedStyle  = theme.Selected
	borderStyle    = theme.Border
	tabStyle       = theme.Tab
	activeTabStyle = theme.ActiveTab

	// cursorStyle marks the selected row.
	cursorStyle = selectedStyle
	// keyStyle marks a key name in help text.
	keyStyle = theme.Key
)
