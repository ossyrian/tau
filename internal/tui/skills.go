package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/skills"
)

// skillsMode is what the text input is for, while it has the keyboard.
type skillsMode int

const (
	modeBrowse skillsMode = iota
	modeAddPath
	modeRenamePath
)

type skillsLoadedMsg struct {
	profile string
	skills  []skills.Skill
	paths   []skills.PathInfo
	err     error
}

// skillsSection is two panes: the skills as a table, and the selected skill's
// SKILL.md below it. The "p" key swaps the table for the skill paths.
type skillsSection struct {
	api     API
	profile string

	all    []skills.Skill
	paths  []skills.PathInfo
	err    error
	loaded bool

	list    *master // skills and SKILL.md
	pathTab *master // skill directories, a single pane
	onPaths bool

	mode   skillsMode
	input  textinput.Model
	rename string // the path being renamed
}

func newSkills(api API) section {
	return &skillsSection{
		api:     api,
		list:    newMaster(true, "PROFILE", "NAME", "STATE", "PATH"),
		pathTab: newMaster(false, "PROFILE", "PATH", "STATE", "SKILLS"),
		input:   newInput("", 50),
	}
}

func (s *skillsSection) Name() string { return "skills" }

func (s *skillsSection) current() *master {
	if s.onPaths {
		return s.pathTab
	}
	return s.list
}

func (s *skillsSection) Capturing() bool { return s.mode != modeBrowse || s.current().Capturing() }

func (s *skillsSection) Keys() []bkey.Binding {
	if s.mode != modeBrowse {
		return []bkey.Binding{binding("enter", "enter", "save"), binding("esc", "esc", "cancel")}
	}
	keys := s.current().Keys()
	if s.current().Capturing() {
		return keys
	}
	if s.onPaths {
		return append([]bkey.Binding{binding("a", "a", "add"), binding("e", "e", "rename"), binding("d", "d", "remove"), binding("p", "p", "skills")}, keys...)
	}
	return append([]bkey.Binding{binding("space", "space", "toggle"), binding("p", "p", "paths"), binding("r", "r", "restart")}, keys...)
}

func (s *skillsSection) Enter() tea.Cmd {
	api := s.api
	return func() tea.Msg {
		profile := api.Status().Profile
		l, err := api.Skills()
		if err != nil {
			return skillsLoadedMsg{profile: profile, err: err}
		}
		p, err := api.SkillPaths()
		return skillsLoadedMsg{profile: profile, skills: l, paths: p, err: err}
	}
}

func (s *skillsSection) selectedSkill() (skills.Skill, bool) {
	r, ok := s.list.pick.Selected()
	if !ok {
		return skills.Skill{}, false
	}
	for _, k := range s.all {
		if k.Name == r.Key {
			return k, true
		}
	}
	return skills.Skill{}, false
}

func (s *skillsSection) selectedPath() (skills.PathInfo, bool) {
	r, ok := s.pathTab.pick.Selected()
	if !ok {
		return skills.PathInfo{}, false
	}
	for _, p := range s.paths {
		if p.Path == r.Key {
			return p, true
		}
	}
	return skills.PathInfo{}, false
}

func (s *skillsSection) setData(m skillsLoadedMsg) {
	s.err, s.loaded, s.profile, s.all, s.paths = m.err, true, m.profile, m.skills, m.paths
	rows := make([]pickerRow, len(s.all))
	for i, k := range s.all {
		rows[i] = pickerRow{Key: k.Name, Cells: []string{s.profile, k.Name, string(k.State), k.Path}, Search: k.Name}
	}
	s.list.pick.SetRows(rows)
	prows := make([]pickerRow, len(s.paths))
	for i, p := range s.paths {
		state := "found"
		if !p.Found {
			state = "missing"
		}
		prows[i] = pickerRow{Key: p.Path, Cells: []string{s.profile, p.Path, state, fmt.Sprint(p.Skills)}}
	}
	s.pathTab.pick.SetRows(prows)
}

func (s *skillsSection) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case skillsLoadedMsg:
		s.setData(msg)
		return nil
	case reloadMsg:
		return s.Enter()
	case tea.KeyPressMsg:
		switch {
		case s.mode != modeBrowse:
			return s.editing(msg)
		case s.current().Capturing():
			return s.current().Update(msg)
		}
		return s.browse(msg)
	}
	return nil
}

func (s *skillsSection) browse(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "p" {
		s.onPaths = !s.onPaths
		return nil
	}
	if s.onPaths {
		return s.browsePaths(msg)
	}
	switch msg.String() {
	case "space", "enter":
		return s.toggle()
	case "r":
		return confirm("Restart the container to apply skill changes? Running sessions are lost.", func() tea.Cmd {
			return run("restarting", func() (string, error) { return outcomeNotice(s.api.Restart()) })
		})
	}
	return s.list.Update(msg)
}

// toggle flips the selected skill. It only edits config.yaml, and pressing
// again undoes it, so it asks nothing.
func (s *skillsSection) toggle() tea.Cmd {
	k, ok := s.selectedSkill()
	if !ok {
		return nil
	}
	if k.Active {
		return run("deactivating "+k.Name, func() (string, error) {
			_, err := s.api.DeactivateSkill(skillRef(k))
			return "deactivated " + k.Name + " (restart to apply)", err
		})
	}
	return run("activating "+k.Name, func() (string, error) {
		_, err := s.api.ActivateSkill(skillRef(k))
		return "activated " + k.Name + " (restart to apply)", err
	})
}

// skillRef is what to pass to activate and deactivate. A stale skill is in no
// search path, so only its path can name it again.
func skillRef(k skills.Skill) string {
	if k.State == skills.Stale && k.Path != "" {
		return k.Path
	}
	return k.Name
}

func (s *skillsSection) browsePaths(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "a":
		s.mode = modeAddPath
		s.input = newInput("absolute path, or ~/path", 50)
		return s.input.Focus()
	case "e":
		p, ok := s.selectedPath()
		if !ok {
			return nil
		}
		s.mode, s.rename = modeRenamePath, p.Path
		s.input = newInput("", 50)
		s.input.SetValue(p.Path)
		s.input.CursorEnd()
		return s.input.Focus()
	case "d":
		p, ok := s.selectedPath()
		if !ok {
			return nil
		}
		return confirm(fmt.Sprintf("Remove %s from the skill paths? The directory stays.", p.Path), func() tea.Cmd {
			return run("removing path", func() (string, error) {
				return "removed " + p.Path, s.api.RemoveSkillPath(p.Path)
			})
		})
	}
	return s.pathTab.Update(msg)
}

func (s *skillsSection) editing(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		s.mode = modeBrowse
		s.input.Blur()
		return nil
	case "enter":
		path, mode, old := strings.TrimSpace(s.input.Value()), s.mode, s.rename
		s.mode = modeBrowse
		s.input.Blur()
		switch {
		case path == "":
			return nil
		case mode == modeRenamePath && path == old:
			return nil
		case mode == modeRenamePath:
			return run("renaming path", func() (string, error) {
				return "renamed to " + path + " (restart to apply)", s.api.RenameSkillPath(old, path)
			})
		}
		return run("adding path", func() (string, error) {
			return "added " + path, s.api.AddSkillPath(path)
		})
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	return cmd
}

// skillText is the SKILL.md of a skill, highlighted as Markdown.
func skillText(k skills.Skill) string {
	if k.Path == "" {
		return "This skill is not in any search path, so there is no file to show."
	}
	data, err := os.ReadFile(filepath.Join(k.Path, "SKILL.md"))
	if err != nil {
		return "cannot read SKILL.md: " + err.Error()
	}
	return highlight("markdown", string(data))
}

func (s *skillsSection) View(w, h int) string {
	if msg := stateMessage(s.err, s.loaded); msg != "" {
		return messagePane("skills", w, h, msg)
	}
	if s.mode != modeBrowse {
		title := "add a skill path"
		if s.mode == modeRenamePath {
			title = "rename skill path"
		}
		return column(pane(title, w, 3, true, s.input.View()), s.body(w, h-3))
	}
	return s.body(w, h)
}

func (s *skillsSection) body(w, h int) string {
	if s.onPaths {
		if len(s.paths) == 0 {
			return messagePane("skill paths", w, h, "no skill paths — press a to add a directory")
		}
		return s.pathTab.View(w, h, "skill paths", "", "")
	}
	if len(s.all) == 0 {
		return messagePane("skills", w, h, "no skills found — press p, then a, to add a directory")
	}
	title, body := "SKILL.md", ""
	if k, ok := s.selectedSkill(); ok {
		title = k.Name + "/SKILL.md"
		body = skillText(k)
	}
	return s.list.View(w, h, "skills", title, body)
}
