package tui

import (
	"fmt"
	"strings"
	"time"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/session"
)

// previewLines is how many lines of each session's screen to fetch.
const previewLines = 40

type sessionsLoadedMsg struct {
	infos   []session.Info
	preview map[string][]string
	err     error
}

// sessions lists every profile's sessions; the pane below shows the screen of
// the selected one.
type sessions struct {
	api     API
	m       *master
	infos   []session.Info
	preview map[string][]string
	err     error
	loaded  bool
	naming  bool
	input   textinput.Model
	now     func() time.Time
}

func newSessions(api API) section {
	return &sessions{
		api: api, now: time.Now,
		m:     newMaster(true, "PROFILE", "NAME", "TYPE", "CREATED", "LAST ACTIVE"),
		input: newInput("session name (empty for a random one)", 40),
	}
}

func (s *sessions) Name() string    { return "sessions" }
func (s *sessions) Capturing() bool { return s.naming || s.m.Capturing() }
func (s *sessions) Keys() []bkey.Binding {
	if s.naming {
		return []bkey.Binding{binding("enter", "enter", "create"), binding("esc", "esc", "cancel")}
	}
	keys := s.m.Keys()
	if s.m.Capturing() {
		return keys
	}
	return append([]bkey.Binding{binding("enter", "enter", "attach"), binding("n", "n", "new"), binding("k", "k", "kill")}, keys...)
}

func (s *sessions) load() tea.Cmd {
	api := s.api
	return func() tea.Msg {
		infos, err := api.Sessions()
		if err != nil {
			return sessionsLoadedMsg{err: err}
		}
		pv, _ := api.Preview(previewLines) // a failed preview is not worth hiding the list for
		return sessionsLoadedMsg{infos: infos, preview: pv}
	}
}

func (s *sessions) Enter() tea.Cmd { return s.load() }

func (s *sessions) selected() (session.Info, bool) {
	r, ok := s.m.pick.Selected()
	if !ok {
		return session.Info{}, false
	}
	for _, in := range s.infos {
		if core.SessionKey(in) == r.Key {
			return in, true
		}
	}
	return session.Info{}, false
}

func (s *sessions) setData(m sessionsLoadedMsg) {
	s.err, s.loaded = m.err, true
	s.infos, s.preview = m.infos, m.preview
	now := s.now()
	rows := make([]pickerRow, len(s.infos))
	for i, in := range s.infos {
		rows[i] = pickerRow{
			Key:    core.SessionKey(in),
			Cells:  []string{in.Profile, in.Name, in.Kind, render.Ago(now, in.Created), render.Ago(now, in.Active)},
			Search: in.Profile + " " + in.Name, // by name and by profile
		}
	}
	s.m.pick.SetRows(rows)
}

func (s *sessions) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case sessionsLoadedMsg:
		s.setData(msg)
		return nil
	case reloadMsg, tickMsg:
		return s.load()
	case tea.KeyPressMsg:
		if s.naming {
			return s.typing(msg)
		}
		if s.m.Capturing() {
			return s.m.Update(msg)
		}
		return s.browse(msg)
	}
	return nil
}

func (s *sessions) browse(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		if in, ok := s.selected(); ok {
			return s.attach(in.Profile, in.Name)
		}
		return nil
	case "n":
		s.naming = true
		s.input.SetValue("")
		return s.input.Focus()
	case "k":
		in, ok := s.selected()
		if !ok {
			return nil
		}
		return confirm(fmt.Sprintf("Kill session %q?", in.Name), func() tea.Cmd {
			return run("killing "+in.Name, func() (string, error) {
				name, err := s.api.KillSession(in.Profile, in.Name)
				return "killed " + name, err
			})
		})
	}
	return s.m.Update(msg)
}

func (s *sessions) typing(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.naming = false
		s.input.Blur()
		return nil
	case "enter":
		name := strings.TrimSpace(s.input.Value())
		s.naming = false
		s.input.Blur()
		return func() tea.Msg {
			created, err := s.api.CreateSession(name)
			if err != nil {
				return doneMsg{err: err}
			}
			args, err := s.api.AttachArgs("", created)
			if err != nil {
				return doneMsg{err: err}
			}
			return attachMsg{args}
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

func (s *sessions) attach(profile, name string) tea.Cmd {
	return func() tea.Msg {
		args, err := s.api.AttachArgs(profile, name)
		if err != nil {
			return doneMsg{err: err}
		}
		return attachMsg{args}
	}
}

func (s *sessions) View(w, h int) string {
	if msg := stateMessage(s.err, s.loaded); msg != "" {
		return messagePane("sessions", w, h, msg)
	}
	if s.naming {
		top := 3
		return column(pane("new session", w, top, true, s.input.View()), s.panes(w, h-top))
	}
	return s.panes(w, h)
}

func (s *sessions) panes(w, h int) string {
	if len(s.infos) == 0 {
		return messagePane("sessions", w, h, "no sessions yet — press n to start one")
	}
	title, body := "screen", ""
	if in, ok := s.selected(); ok {
		title = in.Profile + "/" + in.Name
		body = strings.Join(s.preview[core.SessionKey(in)], "\n")
	}
	return s.m.View(w, h, "sessions", title, body)
}
