// Package tui holds tau's terminal screens, built on bubbletea.
package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/help"
	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/theme"
)

type item struct {
	name        string
	runtimeOnly bool // false means the definition differs between the files
}

// Resolve is the screen that asks the user what to do with each piece of drift
// between config.yaml and config.runtime.yaml. Each decision is written to disk
// at once, so quitting midway keeps what was already decided. It is the same
// pair of panes the packages section draws: the changes as a table, and the
// selected one's YAML below it.
type Resolve struct {
	cfg      *config.Config
	items    []item
	m        *master
	help     help.Model
	w, h     int
	resolved int
	err      string
	settled  bool
}

// NewResolve lists the drift in cfg, in the order it appears in the runtime file.
func NewResolve(cfg *config.Config) Resolve {
	m := Resolve{cfg: cfg, m: newMaster(true, "NAME", "CHANGE"), help: newHelp(80)}
	d := cfg.Diff()
	only, changed := toSet(d.RuntimeOnly), toSet(d.Changed)
	if cfg.Runtime != nil {
		for _, name := range cfg.Runtime.Names {
			switch {
			case only[name]:
				m.items = append(m.items, item{name: name, runtimeOnly: true})
			case changed[name]:
				m.items = append(m.items, item{name: name})
			}
		}
	}
	m.fill()
	return m
}

func toSet(l []string) map[string]bool {
	s := make(map[string]bool, len(l))
	for _, x := range l {
		s[x] = true
	}
	return s
}

// fill puts the items in the table.
func (m *Resolve) fill() {
	rows := make([]pickerRow, len(m.items))
	for i, it := range m.items {
		rows[i] = pickerRow{Key: it.name, Cells: []string{it.name, it.kind()}}
	}
	m.m.pick.SetRows(rows)
}

func (it item) kind() string {
	if it.runtimeOnly {
		return "added at runtime"
	}
	return "changed"
}

// cursor is the index of the selected item.
func (m Resolve) cursor() int { return m.m.pick.table.Cursor() }

// Done reports whether all drift is resolved and config.runtime.yaml is gone.
func (m Resolve) Done() bool { return m.settled }

// Summary is the line to print after the screen exits.
func (m Resolve) Summary() string {
	switch {
	case m.settled:
		return fmt.Sprintf("Resolved %d item(s). config.runtime.yaml removed.", m.resolved)
	case len(m.items) == 0:
		return "No drift to resolve."
	default:
		return fmt.Sprintf("Resolved %d item(s); %d left. They will be asked again next time.", m.resolved, len(m.items))
	}
}

func (m Resolve) Init() tea.Cmd {
	if len(m.items) == 0 {
		return tea.Quit
	}
	return nil
}

func (m Resolve) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 1)
	case tea.MouseMsg:
		return m.mouse(msg)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			return m, tea.Quit
		case "tab", "shift+tab":
			m.m.Focus(1 - max(m.m.focusIndex(), 0))
			return m, nil
		case "a", "r":
			next, finished := m.Handle(msg.String())
			if finished {
				return next, tea.Quit
			}
			return next, nil
		}
		return m, m.m.Update(msg)
	}
	return m, nil
}

// mouse focuses the pane a click lands in; the table and the detail pane do the rest.
func (m Resolve) mouse(msg tea.MouseMsg) (Resolve, tea.Cmd) {
	if tooSmall(m.w, m.h) || msg.Mouse().Y >= m.h-1 {
		return m, nil
	}
	if _, ok := msg.(tea.MouseClickMsg); ok && msg.Mouse().Button == tea.MouseLeft {
		m.m.Focus(m.m.PaneAt(msg.Mouse().Y))
	}
	return m, m.m.Update(msg)
}

// Handle applies a (accept) or r (reject) to the selected item and reports
// whether the last item was just resolved. It never quits the program.
func (m Resolve) Handle(key string) (next Resolve, finished bool) {
	switch key {
	case "a":
		return m.act(true)
	case "r":
		return m.act(false)
	}
	return m, false
}

// actionFor is the change to make to a package that drifted. Accepting keeps
// the runtime version in config.yaml; rejecting drops it.
func actionFor(runtimeOnly, accept bool) config.Action {
	switch {
	case runtimeOnly && accept:
		return config.Promote
	case runtimeOnly:
		return config.Discard
	case accept:
		return config.Adopt
	}
	return config.Revert
}

// act accepts (keep the runtime version in config.yaml) or rejects (drop it) the selected item.
func (m Resolve) act(accept bool) (Resolve, bool) {
	if len(m.items) == 0 {
		return m, false
	}
	at := m.cursor()
	it := m.items[at]
	if err := m.cfg.Apply(it.name, actionFor(it.runtimeOnly, accept)); err != nil {
		m.err = fmt.Sprintf("%s: %v", it.name, err)
		return m, false
	}
	m.err = ""
	m.resolved++
	m.items = append(m.items[:at:at], m.items[at+1:]...)
	m.fill()
	m.m.pick.SetCursor(at) // the next item takes the place; at the end, the last one
	if len(m.items) > 0 {
		return m, false
	}
	settled, err := m.cfg.Settle()
	if err != nil {
		m.err = err.Error()
		return m, false
	}
	m.settled = settled
	return m, true
}

func (m Resolve) View() tea.View {
	var v tea.View
	if tooSmall(m.w, m.h) {
		v = tea.NewView(tooSmallView(m.w, m.h))
	} else {
		title, body := "changes", ""
		if len(m.items) > 0 {
			it := m.items[m.cursor()]
			title, body = it.name+" ("+it.kind()+")", highlight("yaml", driftYAML(m.cfg, it.name, it.runtimeOnly))
		}
		if base := m.cfg.Diff().BaseOnly; len(base) > 0 {
			body += "\n" + theme.Dim.Render("Also in config.yaml and not yet in the runtime copy (added on next build): "+strings.Join(base, ", "))
		}
		panes := m.m.View(m.w, m.h-1, "config.runtime.yaml differs from config.yaml", title, body)
		v = tea.NewView(column(panes, lipgloss.NewStyle().MaxWidth(m.w).MaxHeight(1).Render(m.helpLine())))
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// helpLine is the error if there is one, else the keys for the selected item.
func (m Resolve) helpLine() string {
	if m.err != "" {
		return theme.Err.Render("error: " + m.err)
	}
	accept, reject := "use the runtime version in config.yaml", "revert the runtime copy"
	if len(m.items) > 0 && m.items[m.cursor()].runtimeOnly {
		accept, reject = "add to config.yaml", "discard"
	}
	keys := []bkey.Binding{binding("a", "a", accept), binding("r", "r", reject)}
	return m.help.ShortHelpView(append(keys, m.m.Keys()...))
}
