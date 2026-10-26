package tui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
)

// tickInterval is how often the status and the active section refresh.
var tickInterval = 3 * time.Second

// sidebarRowWidth is the fixed width of a sidebar row, so the layout does not
// move when a badge appears.
const sidebarRowWidth = 14

// Root is the interactive interface: a title bar, a sidebar of sections, the
// active section, and a help line.
type Root struct {
	api      API
	sections []section
	active   int
	w, h     int
	status   core.Status

	spin      spinner.Model
	busy      string // label of the running background job; "" when idle
	help      help.Model
	notice    string
	noticeErr bool
	prompt    *confirmMsg
}

// NewRoot builds the interface on top of api.
func NewRoot(api API) Root {
	return Root{
		api:  api,
		spin: newSpinner(),
		help: newHelp(80),
		sections: []section{
			newHome(api), newSessions(api), newSkills(api), newPackages(api), newEnv(api), newProxies(api),
		},
	}
}

func (m Root) refresh() tea.Cmd {
	api := m.api
	return func() tea.Msg { return statusMsg(api.Status()) }
}

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m Root) Init() tea.Cmd {
	return tea.Batch(m.refresh(), m.sections[m.active].Enter(), tick())
}

// forwardAll sends msg to every section and gathers their commands.
func (m *Root) forwardAll(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, s := range m.sections {
		cmds = append(cmds, s.Update(msg))
	}
	return tea.Batch(cmds...)
}

func (m *Root) goTo(i int) tea.Cmd {
	n := len(m.sections)
	m.active = ((i % n) + n) % n
	return m.sections[m.active].Enter()
}

func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 2)
		return m, nil

	case statusMsg:
		m.status = core.Status(msg)
		return m, m.forwardAll(msg)

	case tickMsg:
		return m, tea.Batch(m.refresh(), tick(), m.sections[m.active].Update(msg))

	case runMsg:
		if m.busy != "" {
			m.notice, m.noticeErr = "busy: "+m.busy+" is still running", true
			return m, nil
		}
		m.busy, m.notice, m.noticeErr = msg.label, "", false
		fn := msg.fn
		return m, tea.Batch(m.spin.Tick, func() tea.Msg {
			notice, err := fn()
			return doneMsg{notice, err}
		})

	case doneMsg:
		m.busy, m.notice, m.noticeErr = "", msg.notice, msg.err != nil
		if msg.err != nil {
			m.notice = msg.err.Error()
		}
		return m, tea.Batch(m.refresh(), m.forwardAll(reloadMsg{}))

	case spinner.TickMsg:
		if m.busy == "" {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case confirmMsg:
		m.prompt = &msg
		return m, nil

	case attachMsg:
		cmd := exec.Command("docker", msg.args...)
		return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return attachedMsg{err} })

	case attachedMsg:
		m.notice, m.noticeErr = "", false
		if msg.err != nil {
			m.notice, m.noticeErr = msg.err.Error(), true
		}
		return m, tea.Batch(m.refresh(), m.forwardAll(reloadMsg{}))

	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, m.forwardAll(msg)
}

func (m Root) key(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.String()
	if k == "ctrl+c" {
		return m, tea.Quit
	}
	if m.prompt != nil {
		p := m.prompt
		m.prompt = nil
		if k == "y" {
			return m, p.run()
		}
		return m, nil
	}
	cur := m.sections[m.active]
	if cur.Capturing() {
		return m, cur.Update(msg)
	}
	m.notice, m.noticeErr = "", false
	switch k {
	case "q":
		return m, tea.Quit
	case "tab":
		return m, m.goTo(m.active + 1)
	case "shift+tab":
		return m, m.goTo(m.active - 1)
	}
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		if i := int(k[0] - '1'); i < len(m.sections) {
			return m, m.goTo(i)
		}
		return m, nil
	}
	return m, cur.Update(msg)
}

// badge is the small count beside a section name, or "".
func (m Root) badge(name string) string {
	n := 0
	switch name {
	case "skills":
		n = m.status.SkillsPending
	case "packages":
		n = len(m.status.Drift.RuntimeOnly) + len(m.status.Drift.Changed)
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprint(n)
}

func (m Root) sidebar() []string {
	rows := make([]string, len(m.sections))
	for i, s := range m.sections {
		name := s.Name()
		badge := m.badge(name)
		label := fmt.Sprintf("%-*s", sidebarRowWidth-4, name)
		row := "  " + label + fmt.Sprintf("%2s", badge)
		switch {
		case i == m.active:
			row = selectedStyle.Render(selectedMarker+" "+label) + warnStyle.Render(fmt.Sprintf("%2s", badge))
		case badge != "":
			row = "  " + label + warnStyle.Render(fmt.Sprintf("%2s", badge))
		}
		rows[i] = row
	}
	return rows
}

func (m Root) title() string {
	var state string
	switch {
	case m.status.Container.Running:
		state = okStyle.Render("●") + " running"
	case m.status.Container.Exists:
		state = warnStyle.Render("○") + " stopped"
	default:
		state = dimStyle.Render("○ not running")
	}
	parts := []string{titleStyle.Render("tau"), state}
	switch m.status.Image {
	case build.ImageStale:
		parts = append(parts, warnStyle.Render("image stale"))
	case build.ImageMissing:
		parts = append(parts, warnStyle.Render("image missing"))
	}
	if m.busy != "" {
		parts = append(parts, m.spin.View()+" "+m.busy)
	}
	return strings.Join(parts, "  ")
}

func (m Root) helpLine() string {
	switch {
	case m.prompt != nil:
		return warnStyle.Render(m.prompt.prompt) + " " + keyStyle.Render("y") + dimStyle.Render(" yes · any other key cancels")
	case m.notice != "" && m.noticeErr:
		return errStyle.Render(m.notice)
	case m.notice != "":
		return okStyle.Render(m.notice)
	}
	keys := append([]bkey.Binding{}, m.sections[m.active].Keys()...)
	keys = append(keys,
		bkey.NewBinding(bkey.WithKeys("tab"), bkey.WithHelp("tab", "section")),
		bkey.NewBinding(bkey.WithKeys("q"), bkey.WithHelp("q", "quit")))
	return m.help.ShortHelpView(keys)
}

func (m Root) View() tea.View {
	var v tea.View
	if tooSmall(m.w, m.h) {
		msg := fmt.Sprintf("terminal too small: tau needs %d×%d, this is %d×%d", minWidth, minHeight, m.w, m.h)
		v = tea.NewView(lipgloss.NewStyle().Width(max(m.w, 1)).Render(msg))
	} else {
		side := m.sidebar()
		cols, rows := bodySize(m.w, m.h, side)
		body := m.sections[m.active].View(cols-1, rows)
		v = tea.NewView(frame(m.w, m.h, m.title(), side, body, m.helpLine()))
	}
	v.AltScreen = true
	v.WindowTitle = "tau"
	return v
}
