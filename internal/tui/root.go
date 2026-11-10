package tui

import (
	"fmt"
	"time"

	"charm.land/bubbles/v2/help"
	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/relay"
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
	focus    int // -1 the menu; 0 and up, a pane of the active section
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

// Update handles a message, then points every section's focus at the right pane.
func (m Root) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	next.syncFocus()
	return next, cmd
}

// syncFocus keeps the focus on a pane that exists (a section can lose its
// detail pane) and tells each section whether it has the keyboard.
func (m *Root) syncFocus() {
	if m.focus >= m.sections[m.active].Panes() {
		m.focus = 0
	}
	for i, s := range m.sections {
		if i == m.active {
			s.Focus(m.focus)
		} else {
			s.Focus(-1)
		}
	}
}

func (m Root) update(msg tea.Msg) (Root, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.help.SetWidth(msg.Width - 1)
		left, _, rows := m.layout()
		iw, _ := inner(left, rows)
		m.nav.SetSize(iw, rows-statusHeight-2)
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

	case execMsg:
		done := func(err error) tea.Msg { return execDoneMsg{err} }
		if msg.clip != nil {
			return m, tea.Exec(relay.New(msg.cmd, msg.clip), done)
		}
		return m, tea.ExecProcess(msg.cmd, done)

	case execDoneMsg:
		m.notice, m.noticeErr = "", false
		if msg.err != nil {
			m.notice, m.noticeErr = msg.err.Error(), true
		}
		return m, tea.Batch(m.refresh(), m.forwardAll(reloadMsg{}))

	case tea.KeyPressMsg:
		return m.key(msg)

	case tea.MouseMsg:
		return m.mouse(msg)
	}
	return m, m.forwardAll(msg)
}

func (m Root) key(msg tea.KeyPressMsg) (Root, tea.Cmd) {
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
		m.cycle(1)
		return m, nil
	case "shift+tab":
		m.cycle(-1)
		return m, nil
	}
	if len(k) == 1 && k[0] >= '1' && k[0] <= '9' {
		if i := int(k[0] - '1'); i < len(m.sections) {
			m.focus = 0
			return m, m.goTo(i)
		}
		return m, nil
	}
	if m.focus < 0 {
		return m.menuKey(msg)
	}
	if k == "h" || k == "left" {
		m.focus = -1
		return m, nil
	}
	return m, cur.Update(msg)
}

// cycle moves the focus d steps through the menu and the active section's
// panes, in that order, and wraps.
func (m *Root) cycle(d int) {
	n := 1 + m.sections[m.active].Panes()
	m.focus = ((m.focus+1+d)%n+n)%n - 1
}

// menuKey handles a key while the menu has the focus: the list's cursor keys
// pick the section, and l, → or enter move into it.
func (m Root) menuKey(msg tea.KeyPressMsg) (Root, tea.Cmd) {
	switch msg.String() {
	case "l", "right", "enter":
		m.focus = 0
		return m, nil
	}
	var cmd tea.Cmd
	m.nav, cmd = m.nav.Update(msg)
	if i := m.nav.Index(); i != m.active {
		return m, m.goTo(i)
	}
	return m, cmd
}

// mouse routes a click or wheel turn by where it lands: the menu, or the main
// area. A click in the main area focuses the pane under it, then goes to the
// section with coordinates relative to the section's top-left corner. A text
// input or a confirmation owns the screen, so they ignore the mouse.
func (m Root) mouse(msg tea.MouseMsg) (Root, tea.Cmd) {
	if m.prompt != nil || m.sections[m.active].Capturing() || tooSmall(m.w, m.h) {
		return m, nil
	}
	left, _, rows := m.layout()
	at := msg.Mouse()
	_, click := msg.(tea.MouseClickMsg)
	if click && at.Button != tea.MouseLeft {
		return m, nil
	}
	switch {
	case at.Y >= rows: // the help line
		return m, nil
	case at.X >= left:
		cur := m.sections[m.active]
		if click {
			if p := cur.PaneAt(at.Y); p < cur.Panes() {
				m.focus = p
			}
		}
		m.notice, m.noticeErr = "", false
		return m, cur.Update(shifted(msg, -left, 0))
	case at.Y < statusHeight: // the status pane has nothing to click
		return m, nil
	}
	return m.menuMouse(msg, at.Y-statusHeight-1) // -1: the pane's border
}

// menuMouse handles a click or wheel turn on line y of the menu list.
func (m Root) menuMouse(msg tea.MouseMsg, y int) (Root, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.MouseWheelMsg:
		switch msg.Button {
		case tea.MouseWheelUp:
			return m, m.goTo(max(m.active-1, 0))
		case tea.MouseWheelDown:
			return m, m.goTo(min(m.active+1, len(m.sections)-1))
		}
	case tea.MouseClickMsg:
		// The list may be paged: line 0 is item Index-Cursor.
		if i := m.nav.Index() - m.nav.Cursor() + y; y >= 0 && i < len(m.sections) {
			m.focus = 0
			return m, m.goTo(i)
		}
	}
	return m, nil
}

// shifted moves a mouse message's position by (dx, dy).
func shifted(msg tea.MouseMsg, dx, dy int) tea.MouseMsg {
	mm := msg.Mouse()
	mm.X, mm.Y = mm.X+dx, mm.Y+dy
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mm)
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mm)
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mm)
	}
	return tea.MouseMotionMsg(mm)
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
	var keys []bkey.Binding
	if m.focus < 0 {
		keys = []bkey.Binding{binding("j", "↑↓/jk", "section"), binding("l", "→/l", "open")}
	} else {
		keys = append(keys, m.sections[m.active].Keys()...)
	}
	keys = append(keys, binding("tab", "tab", "pane"), binding("q", "q", "quit"))
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
		v = tea.NewView(tooSmallView(m.w, m.h))
	} else {
		left, right, rows := m.layout()
		sidebar := column(
			pane("status", left, statusHeight, false, m.statusBody()),
			pane("tau", left, rows-statusHeight, m.focus < 0, m.nav.View()),
		)
		main := m.sections[m.active].View(right, rows)
		// MaxWidth cuts the help line to the terminal; JoinVertical would
		// otherwise widen every line to match it.
		help := lipgloss.NewStyle().MaxWidth(m.w).MaxHeight(1).Render(m.helpLine())
		v = tea.NewView(column(row(sidebar, main), help))
	}
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "tau"
	return v
}
