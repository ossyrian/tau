package tui

import (
	"fmt"
	"os/exec"
	"time"

	"charm.land/bubbles/v2/help"
	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/theme"
)

// tickInterval is how often the status and the active section refresh.
var tickInterval = 3 * time.Second

// statusHeight is the status pane: three lines inside a border.
const statusHeight = 5

// Root is the interactive interface, laid out as lazygit is: a column of panes
// on the left (status, then the list of sections), the active section's panes
// on the right, and a help line under both. Each pane has its own border. All
// sizes come from the terminal's, so the panes follow it as it is resized.
type Root struct {
	api      API
	sections []section
	nav      list.Model
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
	m := Root{
		api:  api,
		nav:  newNav(),
		spin: newSpinner(),
		help: newHelp(80),
		sections: []section{
			newHome(api), newSessions(api), newSkills(api), newPackages(api),
			newEnv(api), newProxies(api), newWorkspaces(api),
		},
	}
	m.syncNav()
	return m
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
	m.syncNav()
	return m.sections[m.active].Enter()
}

// badge is the count beside a section name, or "".
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

// syncNav makes the nav list match the sections, the badges and the selection.
func (m *Root) syncNav() {
	items := make([]list.Item, len(m.sections))
	for i, s := range m.sections {
		items[i] = navItem{s.Name(), m.badge(s.Name())}
	}
	m.nav.SetItems(items)
	m.nav.Select(m.active)
}

func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 1)
		return m, nil

	case statusMsg:
		m.status = core.Status(msg)
		m.syncNav()
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

// statusBody is the status pane's three lines.
func (m Root) statusBody() string {
	var state string
	switch {
	case m.status.Container.Running:
		state = theme.OK.Render("● running")
	case m.status.Container.Exists:
		state = theme.Warn.Render("○ stopped")
	default:
		state = theme.Dim.Render("○ not running")
	}
	image := theme.Dim.Render("image unknown")
	switch m.status.Image {
	case build.ImageCurrent:
		image = "image current"
	case build.ImageStale:
		image = theme.Warn.Render("image stale")
	case build.ImageMissing:
		image = theme.Warn.Render("image missing")
	}
	return lipgloss.JoinVertical(lipgloss.Left, state, image, theme.Dim.Render("profile ")+m.status.Profile)
}

func (m Root) helpLine() string {
	switch {
	case m.prompt != nil:
		return theme.Warn.Render(m.prompt.prompt) + " " + theme.Key.Render("y") + theme.Dim.Render(" yes · any other key cancels")
	case m.busy != "":
		return m.spin.View() + " " + m.busy
	case m.notice != "" && m.noticeErr:
		return theme.Err.Render(m.notice)
	case m.notice != "":
		return theme.OK.Render(m.notice)
	}
	keys := append([]bkey.Binding{}, m.sections[m.active].Keys()...)
	keys = append(keys,
		bkey.NewBinding(bkey.WithKeys("tab"), bkey.WithHelp("tab", "section")),
		bkey.NewBinding(bkey.WithKeys("q"), bkey.WithHelp("q", "quit")))
	return m.help.ShortHelpView(keys)
}

// layout divides the terminal: the left column's width, and the rows above the
// help line.
func (m Root) layout() (left, right, rows int) { return m.layoutAt(m.w, m.h) }

func (m Root) layoutAt(w, h int) (left, right, rows int) {
	left = sidebarWidth(w)
	return left, w - left, h - 1
}

func (m Root) View() tea.View {
	var v tea.View
	if tooSmall(m.w, m.h) {
		msg := fmt.Sprintf("terminal too small: tau needs %d×%d, this is %d×%d", minWidth, minHeight, m.w, m.h)
		v = tea.NewView(lipgloss.NewStyle().Width(max(m.w, 1)).Render(msg))
	} else {
		left, right, rows := m.layout()
		iw, _ := inner(left, rows)
		m.nav.SetSize(iw, rows-statusHeight-2)
		sidebar := column(
			pane("status", left, statusHeight, false, m.statusBody()),
			pane("tau", left, rows-statusHeight, false, m.nav.View()),
		)
		main := m.sections[m.active].View(right, rows)
		// MaxWidth cuts the help line to the terminal; JoinVertical would
		// otherwise widen every line to match it.
		help := lipgloss.NewStyle().MaxWidth(m.w).MaxHeight(1).Render(m.helpLine())
		v = tea.NewView(column(row(sidebar, main), help))
	}
	v.AltScreen = true
	v.WindowTitle = "tau"
	return v
}
