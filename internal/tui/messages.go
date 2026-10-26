package tui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
)

// Messages that sections send to the root model, and the root sends back.
type (
	// statusMsg carries a fresh snapshot to every section.
	statusMsg core.Status
	// tickMsg fires on a timer; the active section may refresh itself.
	tickMsg struct{}
	// runMsg asks the root to run fn in the background and show its notice.
	runMsg struct {
		label string
		fn    func() (notice string, err error)
	}
	// doneMsg is the result of a runMsg.
	doneMsg struct {
		notice string
		err    error
	}
	// confirmMsg asks the root to ask the user first; run happens on "y".
	confirmMsg struct {
		prompt string
		run    func() tea.Cmd
	}
	// attachMsg asks the root to hand the terminal to `docker <args>`.
	attachMsg struct{ args []string }
	// attachedMsg says the terminal came back.
	attachedMsg struct{ err error }
	// reloadMsg tells the active section to re-read its data.
	reloadMsg struct{}
)

// run is the usual way for a section to start background work.
func run(label string, fn func() (string, error)) tea.Cmd {
	return func() tea.Msg { return runMsg{label, fn} }
}

func confirm(prompt string, then func() tea.Cmd) tea.Cmd {
	return func() tea.Msg { return confirmMsg{prompt, then} }
}

// outcomeNotice words a lifecycle result for the notice line.
func outcomeNotice(o container.Outcome, err error) (string, error) {
	if err != nil {
		return "", err
	}
	text, hint := o.Text()
	if hint != "" {
		text += " (" + hint + ")"
	}
	return text, nil
}
