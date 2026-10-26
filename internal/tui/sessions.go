package tui

import (
	"fmt"
	"strings"
	"time"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/session"
)

// previewLines is how many lines of each session's screen to fetch.
const previewLines = 12

// sessionRow is one list item: a session and the last line on its screen.
type sessionRow struct {
	info session.Info
	last string
	now  time.Time
}

func (r sessionRow) FilterValue() string { return r.info.Profile + " " + r.info.Name }
func (r sessionRow) Title() string {
	t := r.info.Name
	if r.info.Profile != "" && r.info.Profile != "default" {
		t = dimStyle.Render(r.info.Profile+"/") + t
	}
	if r.info.Kind == "subagent" {
		t += dimStyle.Render("  subagent")
	}
	return t
}
func (r sessionRow) Description() string {
	d := age(r.info.Active, r.now)
	if r.last != "" {
		d += "  " + r.last
	}
	return d
}

// age reads "just now", "5m ago", "3h ago" or "2d ago".
func age(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours())/24)
}

type sessionsLoadedMsg struct {
	infos   []session.Info
	preview map[string][]string
	err     error
}

type sessions struct {
	api     API
	list    list.Model
	infos   []session.Info
	preview map[string][]string
	err     error
	loaded  bool
	naming  bool
	input   textinput.Model
	w, h    int
}

func newSessions(api API) section {
	return &sessions{api: api, list: newList(nil, 30, 10), input: newInput("session name (empty for a random one)", 40)}
}

func (s *sessions) Name() string    { return "sessions" }
func (s *sessions) Capturing() bool { return s.naming }
func (s *sessions) Keys() []bkey.Binding {
	if s.naming {
		return []bkey.Binding{binding("enter", "enter", "create"), binding("esc", "esc", "cancel")}
	}
	return []bkey.Binding{binding("enter", "enter", "attach"), binding("n", "n", "new"), binding("k", "k", "kill")}
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
	if len(s.infos) == 0 {
		return session.Info{}, false
	}
	i := s.list.Index()
	if i < 0 || i >= len(s.infos) {
		return session.Info{}, false
	}
	return s.infos[i], true
}

func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func (s *sessions) setData(m sessionsLoadedMsg) {
	keep := ""
	if cur, ok := s.selected(); ok {
		keep = core.SessionKey(cur)
	}
	s.err, s.loaded = m.err, true
	s.infos, s.preview = m.infos, m.preview
	now := time.Now()
	items := make([]list.Item, len(s.infos))
	for i, in := range s.infos {
		items[i] = sessionRow{in, lastLine(s.preview[core.SessionKey(in)]), now}
	}
	s.list.SetItems(items)
	s.list.Select(0)
	for i, in := range s.infos {
		if core.SessionKey(in) == keep {
			s.list.Select(i)
		}
	}
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
		return s.browse(msg)
	}
	return nil
}

func (s *sessions) browse(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "enter":
		in, ok := s.selected()
		if !ok {
			return nil
		}
		return s.attach(in.Profile, in.Name)
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
	var cmd tea.Cmd
	s.list, cmd = s.list.Update(msg)
	return cmd
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

func (s *sessions) View(cols, rows int) []string {
	if s.err != nil {
		return append([]string{""}, wrap(dimStyle, s.err.Error(), cols-1)...)
	}
	if !s.loaded {
		return []string{"", dimStyle.Render("loading…")}
	}
	var out []string
	if s.naming {
		out = append(out, titleStyle.Render("New session"), "", s.input.View(), "")
	}
	if len(s.infos) == 0 {
		return append(out, "", dimStyle.Render("no sessions yet — press n to start one"))
	}

	// The list takes the top part; the selected session's screen sits below.
	previewRows := min(previewLines, max((rows-len(out))/2, 3))
	listRows := max(rows-len(out)-previewRows-2, 2)
	s.list.SetSize(cols, listRows)
	out = append(out, strings.Split(s.list.View(), "\n")...)

	if in, ok := s.selected(); ok {
		title := in.Name
		if in.Profile != "" && in.Profile != "default" {
			title = in.Profile + "/" + in.Name
		}
		out = append(out, "", dimStyle.Render("── "+title+" "+strings.Repeat("─", max(cols-len(title)-5, 0))))
		pv := s.preview[core.SessionKey(in)]
		if len(pv) > previewRows {
			pv = pv[len(pv)-previewRows:]
		}
		for _, l := range pv {
			out = append(out, ansi.Truncate(l, cols-1, "…"))
		}
	}
	return out
}
