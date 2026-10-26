package tui

import (
	"fmt"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/skills"
)

var skillTabs = []string{"skills", "paths"}

const (
	tabSkills = iota
	tabPaths
)

// skillRow is one skill in the list.
type skillRow struct{ s skills.Skill }

func (r skillRow) FilterValue() string { return r.s.Name + " " + r.s.Path }
func (r skillRow) Title() string {
	mark := dimStyle.Render("○")
	if r.s.Active {
		mark = okStyle.Render("●")
	}
	return mark + " " + r.s.Name + "  " + stateStyle(r.s.State).Render(string(r.s.State))
}
func (r skillRow) Description() string { return "  " + r.s.Path }

func stateStyle(s skills.State) lipgloss.Style {
	switch s {
	case skills.Synced:
		return okStyle
	case skills.Pending, skills.Stale, skills.Configured:
		return warnStyle
	case skills.Missing:
		return errStyle
	}
	return dimStyle
}

// pathRow is one skill path in the list.
type pathRow struct{ p skills.PathInfo }

func (r pathRow) FilterValue() string { return r.p.Path }
func (r pathRow) Title() string       { return r.p.Path }
func (r pathRow) Description() string {
	if !r.p.Found {
		return errStyle.Render("not found")
	}
	if r.p.Skills == 1 {
		return "1 skill"
	}
	return fmt.Sprintf("%d skills", r.p.Skills)
}

type skillsLoadedMsg struct {
	skills []skills.Skill
	all    []skills.Skill
	paths  []skills.PathInfo
	err    error
}

// What the text input is for, while it has the keyboard.
type skillsMode int

const (
	modeBrowse skillsMode = iota
	modeAddPath
	modeRenamePath
)

type skillsSection struct {
	api API
	tab int

	all    []skills.Skill
	paths  []skills.PathInfo
	err    error
	loaded bool

	skillList list.Model
	pathList  list.Model

	mode   skillsMode
	input  textinput.Model
	rename string // the path being renamed
}

func newSkills(api API) section {
	return &skillsSection{
		api:       api,
		skillList: newFilterList(),
		pathList:  newList(nil, 30, 10),
		input:     newInput("", 50),
	}
}

func (s *skillsSection) Name() string { return "skills" }
func (s *skillsSection) Capturing() bool {
	return s.mode != modeBrowse || s.skillList.SettingFilter()
}

func (s *skillsSection) Keys() []bkey.Binding {
	switch {
	case s.skillList.SettingFilter():
		return []bkey.Binding{binding("enter", "enter", "keep filter"), binding("esc", "esc", "clear")}
	case s.mode != modeBrowse:
		return []bkey.Binding{binding("enter", "enter", "save"), binding("esc", "esc", "cancel")}
	}
	tabs := binding("left", "←/→", "tab")
	if s.tab == tabPaths {
		return []bkey.Binding{binding("a", "a", "add"), binding("e", "e", "rename"), binding("d", "d", "remove"), tabs}
	}
	return []bkey.Binding{
		binding("space", "space", "toggle"), binding("/", "/", "search"),
		binding("r", "r", "restart to apply"), tabs,
	}
}

func (s *skillsSection) Enter() tea.Cmd {
	api := s.api
	return func() tea.Msg {
		l, err := api.Skills()
		if err != nil {
			return skillsLoadedMsg{err: err}
		}
		p, err := api.SkillPaths()
		return skillsLoadedMsg{skills: l, paths: p, err: err}
	}
}

func (s *skillsSection) selectedSkill() (skills.Skill, bool) {
	r, ok := s.skillList.SelectedItem().(skillRow)
	return r.s, ok
}

func (s *skillsSection) selectedPath() (skills.PathInfo, bool) {
	i := s.pathList.Index()
	if i < 0 || i >= len(s.paths) {
		return skills.PathInfo{}, false
	}
	return s.paths[i], true
}

// setSkills replaces the list's items, keeping the selection on the same skill.
func (s *skillsSection) setSkills(all []skills.Skill) tea.Cmd {
	keep := ""
	if cur, ok := s.selectedSkill(); ok {
		keep = cur.Name
	}
	rows := make([]list.Item, len(all))
	for i, k := range all {
		rows[i] = skillRow{k}
	}
	cmd := s.skillList.SetItems(rows)
	for i, it := range s.skillList.VisibleItems() {
		if it.(skillRow).s.Name == keep {
			s.skillList.Select(i)
		}
	}
	return cmd
}

func (s *skillsSection) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case skillsLoadedMsg:
		s.err, s.loaded, s.all, s.paths = msg.err, true, msg.skills, msg.paths
		keepPath := ""
		if cur, ok := s.selectedPath(); ok {
			keepPath = cur.Path
		}
		cmd := s.setSkills(s.all)
		rows := make([]list.Item, len(s.paths))
		for i, p := range s.paths {
			rows[i] = pathRow{p}
		}
		s.pathList.SetItems(rows)
		s.pathList.Select(0)
		for i, p := range s.paths {
			if p.Path == keepPath {
				s.pathList.Select(i)
			}
		}
		return cmd
	case reloadMsg:
		return s.Enter()
	case tea.KeyPressMsg:
		switch {
		case s.skillList.SettingFilter():
			return s.updateList(&s.skillList, msg)
		case s.mode != modeBrowse:
			return s.editing(msg)
		}
		return s.browse(msg)
	default:
		return s.updateList(&s.skillList, msg) // filter results arrive as messages
	}
}

func (s *skillsSection) updateList(l *list.Model, msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	*l, cmd = l.Update(msg)
	return cmd
}

func (s *skillsSection) browse(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "right", "l":
		s.tab = (s.tab + 1) % len(skillTabs)
		return nil
	case "left", "h":
		s.tab = (s.tab + len(skillTabs) - 1) % len(skillTabs)
		return nil
	}
	if s.tab == tabPaths {
		return s.browsePaths(msg)
	}
	switch k {
	case "space", "enter":
		return s.toggle()
	case "r":
		return confirm("Restart the container to apply skill changes? Running sessions are lost.", func() tea.Cmd {
			return run("restarting", func() (string, error) { return outcomeNotice(s.api.Restart()) })
		})
	}
	var cmd tea.Cmd
	s.skillList, cmd = s.skillList.Update(msg)
	return cmd
}

// toggle flips the selected skill. It only edits config.yaml, and pressing
// again undoes it, so it asks nothing.
func (s *skillsSection) toggle() tea.Cmd {
	k, ok := s.selectedSkill()
	if !ok {
		return nil
	}
	// The screen shows config.yaml: a stale skill is shown as inactive.
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
	var cmd tea.Cmd
	s.pathList, cmd = s.pathList.Update(msg)
	return cmd
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

func (s *skillsSection) View(cols, rows int) []string {
	out := []string{tabBar(skillTabs, s.tab), ""}
	if s.err != nil {
		return append(out, wrap(errStyle, s.err.Error(), cols-1)...)
	}
	if !s.loaded {
		return append(out, dimStyle.Render("loading…"))
	}
	if s.tab == tabPaths {
		return append(out, s.pathsView(cols, rows-len(out))...)
	}
	return append(out[:1], s.skillsView(cols, rows-1)...) // the list's title row is the spacer
}

func (s *skillsSection) skillsView(cols, rows int) []string {
	var out []string
	if len(s.all) == 0 {
		return append(out, dimStyle.Render("no skills found — add a directory on the paths tab"))
	}
	if s.skillList.FilterState() == list.FilterApplied {
		out = append(out, dimStyle.Render("filter: ")+accentStyle.Render(s.skillList.FilterValue())+dimStyle.Render("   esc clears"))
	}
	hint := 0
	for _, k := range s.all {
		if k.State == skills.Pending || k.State == skills.Stale {
			hint = 2
		}
	}
	s.skillList.SetSize(cols, max(rows-len(out)-hint, 2))
	out = append(out, strings.Split(s.skillList.View(), "\n")...)
	if hint > 0 {
		out = append(out, "", warnStyle.Render("Pending changes: restart to apply (r)."))
	}
	return out
}

func (s *skillsSection) pathsView(cols, rows int) []string {
	var out []string
	switch s.mode {
	case modeAddPath:
		out = append(out, titleStyle.Render("Add a skill path"), "", s.input.View(), "")
	case modeRenamePath:
		out = append(out, titleStyle.Render("Rename skill path"), "", s.input.View(), "")
	}
	if len(s.paths) == 0 {
		return append(out, dimStyle.Render("no skill paths — press a to add a directory"))
	}
	s.pathList.SetSize(cols, max(rows-len(out), 2))
	return append(out, strings.Split(s.pathList.View(), "\n")...)
}
