package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/ossyrian/tau/internal/workspace"
)

type workspacesLoadedMsg struct {
	profile string
	entries []workspace.Entry
	err     error
}

// Naming steps of the add-workspace flow.
type wsStep int

const (
	wsBrowse wsStep = iota
	wsDir
	wsName
)

// workspaces is two panes: the profile's workspace entries as a table, and the
// selected entry's files below.
type workspaces struct {
	api     API
	profile string
	m       *master
	entries []workspace.Entry
	err     error
	loaded  bool

	step  wsStep
	live  bool
	dir   string
	input textinput.Model
}

func newWorkspaces(api API) section {
	return &workspaces{
		api:   api,
		m:     newMaster(true, "PROFILE", "NAME", "KIND", "AGENT SEES", "HOST PATH"),
		input: newInput("", 60),
	}
}

func (w *workspaces) Name() string    { return "workspaces" }
func (w *workspaces) Capturing() bool { return w.step != wsBrowse || w.m.Capturing() }

func (w *workspaces) Keys() []bkey.Binding {
	if w.step != wsBrowse {
		return []bkey.Binding{binding("enter", "enter", "next"), binding("esc", "esc", "cancel")}
	}
	keys := w.m.Keys()
	if w.m.Capturing() {
		return keys
	}
	return append([]bkey.Binding{binding("a", "a", "add copy"), binding("A", "A", "add live"), binding("d", "d", "remove")}, keys...)
}

func (w *workspaces) Enter() tea.Cmd {
	api := w.api
	return func() tea.Msg {
		e, err := api.Workspaces()
		return workspacesLoadedMsg{api.Status().Profile, e, err}
	}
}

func kindText(k workspace.Kind) string {
	if k == workspace.Live {
		return "live"
	}
	return "copy"
}

func (w *workspaces) setData(m workspacesLoadedMsg) {
	w.err, w.loaded, w.profile, w.entries = m.err, true, m.profile, m.entries
	rows := make([]pickerRow, len(w.entries))
	for i, e := range w.entries {
		rows[i] = pickerRow{Key: e.Name, Cells: []string{w.profile, e.Name, kindText(e.Kind), e.Container, e.Host}}
	}
	w.m.pick.SetRows(rows)
}

func (w *workspaces) selected() (workspace.Entry, bool) {
	r, ok := w.m.pick.Selected()
	if !ok {
		return workspace.Entry{}, false
	}
	for _, e := range w.entries {
		if e.Name == r.Key {
			return e, true
		}
	}
	return workspace.Entry{}, false
}

func (w *workspaces) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case workspacesLoadedMsg:
		w.setData(msg)
	case reloadMsg:
		return w.Enter()
	case tea.KeyPressMsg:
		switch {
		case w.step != wsBrowse:
			return w.adding(msg)
		case w.m.Capturing():
			return w.m.Update(msg)
		}
		return w.browse(msg)
	}
	return nil
}

func (w *workspaces) browse(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "a", "A":
		w.step, w.live, w.dir = wsDir, msg.String() == "A", ""
		w.input = newInput("directory on this machine, or ~/path", 60)
		return w.input.Focus()
	case "d":
		e, ok := w.selected()
		if !ok {
			return nil
		}
		warn := "Remove the copy " + e.Name + "? Edits the agent made to it are lost."
		if e.Kind == workspace.Live {
			warn = "Unregister " + e.Name + "? Your directory is untouched."
		}
		return confirm(warn, func() tea.Cmd {
			return run("removing "+e.Name, func() (string, error) {
				_, err := w.api.RemoveWorkspace(e.Name)
				return "removed " + e.Name + liveNote(e), err
			})
		})
	}
	return w.m.Update(msg)
}

func liveNote(e workspace.Entry) string {
	if e.Kind == workspace.Live {
		return " (applies on the next restart)"
	}
	return ""
}

// adding takes the directory, then an optional name.
func (w *workspaces) adding(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		w.step = wsBrowse
		w.input.Blur()
		return nil
	case "enter":
		text := strings.TrimSpace(w.input.Value())
		if w.step == wsDir {
			if text == "" {
				w.step = wsBrowse
				w.input.Blur()
				return nil
			}
			w.dir, w.step = text, wsName
			w.input = newInput("name (empty: the directory's name)", 40)
			return w.input.Focus()
		}
		dir, name, live := w.dir, text, w.live
		w.step = wsBrowse
		w.input.Blur()
		return run("adding "+dir, func() (string, error) {
			res, err := w.api.AddWorkspace(dir, name, live)
			if err != nil {
				return "", err
			}
			note := "added " + res.Entry.Name
			if live {
				note += " — the agent can edit your files (applies on the next restart)"
			}
			return note, nil
		})
	}
	var cmd tea.Cmd
	w.input, cmd = w.input.Update(msg)
	return cmd
}

// maxListed caps how many files the detail pane lists: a workspace can hold a
// whole repository.
const maxListed = 500

// contents lists the files of a directory, directories first, one level deep
// into each. It stops at maxListed lines.
func contents(dir string) string {
	var b strings.Builder
	n := 0
	var walk func(path, indent string, depth int)
	walk = func(path, indent string, depth int) {
		entries, err := os.ReadDir(path)
		if err != nil {
			fmt.Fprintf(&b, "%s%s\n", indent, theme.Err.Render(err.Error()))
			return
		}
		sort.SliceStable(entries, func(i, j int) bool { return entries[i].IsDir() && !entries[j].IsDir() })
		for _, e := range entries {
			if n >= maxListed {
				return
			}
			n++
			name := e.Name()
			if e.IsDir() {
				fmt.Fprintf(&b, "%s%s\n", indent, theme.Info.Render(name+"/"))
				if depth < 1 && name != ".git" {
					walk(filepath.Join(path, name), indent+"  ", depth+1)
				}
				continue
			}
			fmt.Fprintf(&b, "%s%s\n", indent, name)
		}
	}
	walk(dir, "", 0)
	if n >= maxListed {
		b.WriteString(theme.Dim.Render(fmt.Sprintf("… first %d entries\n", maxListed)))
	}
	if n == 0 {
		return theme.Dim.Render("empty")
	}
	return b.String()
}

func (w *workspaces) View(wd, ht int) string {
	if msg := stateMessage(w.err, w.loaded); msg != "" {
		return messagePane("workspaces", wd, ht, msg)
	}
	if w.step != wsBrowse {
		title := "directory to add"
		if w.step == wsName {
			title = "name for " + w.dir
		}
		if w.live && w.step == wsDir {
			title += " (live: the agent can edit it)"
		}
		return column(pane(title, wd, 3, true, w.input.View()), w.body(wd, ht-3))
	}
	return w.body(wd, ht)
}

func (w *workspaces) body(wd, ht int) string {
	if len(w.entries) == 0 {
		return messagePane("workspaces", wd, ht, "no workspaces — press a to add a copy of a project, A to mount one live")
	}
	title, text := "files", ""
	if e, ok := w.selected(); ok {
		title = e.Name + " files"
		text = contents(e.Host)
	}
	return w.m.View(wd, ht, "workspaces", title, text)
}
