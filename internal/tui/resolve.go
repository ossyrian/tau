// Package tui holds tau's terminal screens, built on bubbletea.
package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/config"
)

type item struct {
	name        string
	runtimeOnly bool // false means the definition differs between the files
}

// Resolve is the screen that asks the user what to do with each piece of drift
// between config.yaml and config.runtime.yaml. Each decision is written to disk
// at once, so quitting midway keeps what was already decided.
type Resolve struct {
	cfg      *config.Config
	items    []item
	cursor   int
	resolved int
	err      string
	settled  bool
}

// NewResolve lists the drift in cfg, in the order it appears in the runtime file.
func NewResolve(cfg *config.Config) Resolve {
	m := Resolve{cfg: cfg}
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
	return m
}

func toSet(l []string) map[string]bool {
	s := make(map[string]bool, len(l))
	for _, x := range l {
		s[x] = true
	}
	return s
}

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
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "q", "esc", "ctrl+c":
		return m, tea.Quit
	}
	next, finished := m.Handle(k.String())
	if finished {
		return next, tea.Quit
	}
	return next, nil
}

// Handle applies one key (a, r, up/k, down/j) and reports whether the last
// item was just resolved. It never quits the program.
func (m Resolve) Handle(key string) (next Resolve, finished bool) {
	switch key {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.items)-1 {
			m.cursor++
		}
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
	it := m.items[m.cursor]
	if err := m.cfg.Apply(it.name, actionFor(it.runtimeOnly, accept)); err != nil {
		m.err = fmt.Sprintf("%s: %v", it.name, err)
		return m, false
	}
	m.err = ""
	m.resolved++
	m.items = append(m.items[:m.cursor:m.cursor], m.items[m.cursor+1:]...)
	if m.cursor >= len(m.items) && m.cursor > 0 {
		m.cursor--
	}
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

func (m Resolve) View() tea.View { return tea.NewView(m.Body()) }

// Body is the screen's text, for embedding in another screen.
func (m Resolve) Body() string {
	var b strings.Builder
	b.WriteString(titleStyle.Render("config.runtime.yaml differs from config.yaml") + "\n")
	b.WriteString(dimStyle.Render("config.yaml is the source of truth. Decide what to do with each change.") + "\n\n")

	for i, it := range m.items {
		kind := "changed"
		if it.runtimeOnly {
			kind = "added at runtime"
		}
		line := fmt.Sprintf("%-24s %s", it.name, dimStyle.Render(kind))
		if i == m.cursor {
			line = cursorStyle.Render("> ") + line
		} else {
			line = "  " + line
		}
		b.WriteString(line + "\n")
	}
	if len(m.items) > 0 {
		b.WriteString("\n" + m.detail(m.items[m.cursor]))
	}
	if base := m.cfg.Diff().BaseOnly; len(base) > 0 {
		b.WriteString("\n" + dimStyle.Render("Also in config.yaml and not yet in the runtime copy (added on next build): "+strings.Join(base, ", ")) + "\n")
	}
	if m.err != "" {
		b.WriteString("\n" + errStyle.Render("error: "+m.err) + "\n")
	}
	b.WriteString("\n" + m.help() + "\n")
	return b.String()
}

func (m Resolve) detail(it item) string {
	var b strings.Builder
	if it.runtimeOnly {
		b.WriteString(titleStyle.Render("runtime") + "\n" + indent(m.cfg.Runtime.Packages[it.name].YAML()) + "\n")
		return b.String()
	}
	b.WriteString(titleStyle.Render("config.yaml") + "\n" + indent(m.cfg.Base.Packages[it.name].YAML()) + "\n")
	b.WriteString(titleStyle.Render("runtime") + "\n" + indent(m.cfg.Runtime.Packages[it.name].YAML()) + "\n")
	return b.String()
}

func (m Resolve) help() string {
	if len(m.items) == 0 {
		return ""
	}
	quit := "   q quit"
	if m.items[m.cursor].runtimeOnly {
		return dimStyle.Render("a add to config.yaml   r discard   ↑/↓ move" + quit)
	}
	return dimStyle.Render("a use the runtime version in config.yaml   r revert the runtime copy   ↑/↓ move" + quit)
}

func indent(s string) string {
	if s == "" {
		return "  (empty: uses the catalog definition)\n"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n") + "\n"
}
