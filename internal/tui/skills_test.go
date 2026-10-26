package tui

import (
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/skills"
)

func someSkills(f *fakeAPI) {
	f.skills = []skills.Skill{
		{Name: "alpha", Path: "/h/agents/alpha", Active: true, State: skills.Synced},
		{Name: "beta", Path: "/h/agents/beta", Active: true, State: skills.Pending},
		{Name: "gamma", Path: "/h/house/gamma", State: skills.Inactive},
		{Name: "gone", Active: true, State: skills.Missing},
		{Name: "old", Path: "/h/old", State: skills.Stale},
	}
	f.paths = []skills.PathInfo{
		{Path: "~/.agents/skills", Found: true, Skills: 3},
		{Path: "/h/gone", Found: false},
	}
}

func inSkills(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := newRoot(t, f)
	send(t, m, "3")
	return m
}

func captured(m *Root) bool { return m.sections[m.active].Capturing() }

func TestSkillsToggleActivatesAnInactiveSkillAndDeactivatesAnActiveOne(t *testing.T) {
	for _, k := range []string{"space", "enter"} {
		f := newFake(t)
		someSkills(f)
		m := inSkills(t, f)
		send(t, m, k) // alpha: active
		send(t, m, "down", "down", k)
		want := []string{"deactivate:alpha", "activate:gamma"}
		got := f.did()
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("%s: calls %v, want %v", k, got, want)
		}
	}
}

func TestSkillsToggleOnAStaleSkillActivatesItByPath(t *testing.T) {
	// A stale skill is mounted but listed nowhere, so only its path names it.
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "down", "down", "down", "down", "space")
	if got := f.did(); len(got) != 1 || got[0] != "activate:/h/old" {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsToggleOnAMissingEntryDeactivatesIt(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "down", "down", "down", "space")
	if got := f.did(); len(got) != 1 || got[0] != "deactivate:gone" {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsSearchTakesTheKeyboardAndFilters(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "/")
	if !captured(m) {
		t.Fatal("the search bar did not take the keyboard")
	}
	typeText(t, m, "gam")
	out := screen(m)
	if !strings.Contains(out, "gamma") || strings.Contains(out, "alpha") || strings.Contains(out, "beta") {
		t.Errorf("not filtered:\n%s", out)
	}
	if !strings.Contains(out, "gam") {
		t.Errorf("the query is not shown:\n%s", out)
	}
}

func TestSkillsSearchEnterKeepsTheFilterAndFreesTheKeyboard(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "/")
	typeText(t, m, "gamma")
	send(t, m, "enter")
	if captured(m) {
		t.Error("still capturing")
	}
	if out := screen(m); strings.Contains(out, "alpha") {
		t.Errorf("filter was dropped:\n%s", out)
	}
	// The filtered row is the selected one.
	send(t, m, "space")
	if got := f.did(); len(got) != 1 || got[0] != "activate:gamma" {
		t.Errorf("toggled the wrong skill: %v", got)
	}
}

func TestSkillsSearchEscClearsTheFilter(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "/")
	typeText(t, m, "gamma")
	send(t, m, "esc")
	if captured(m) {
		t.Error("still capturing")
	}
	if out := screen(m); !strings.Contains(out, "alpha") {
		t.Errorf("filter stayed:\n%s", out)
	}
}

func TestSkillsEscOutsideSearchClearsAKeptFilter(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "/")
	typeText(t, m, "gamma")
	send(t, m, "enter", "esc")
	if out := screen(m); !strings.Contains(out, "alpha") {
		t.Errorf("filter stayed:\n%s", out)
	}
}

func TestSkillsApplyRestartsTheContainerAfterAsking(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "r")
	if len(f.did()) != 0 {
		t.Fatal("restarted without asking")
	}
	send(t, m, "y")
	if got := f.did(); len(got) != 1 || got[0] != "restart" {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsPathsAddAsksForAPathThenAdds(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "right", "a")
	if !captured(m) {
		t.Fatal("path input not focused")
	}
	typeText(t, m, "/h/new")
	send(t, m, "enter")
	if got := f.did(); len(got) != 1 || got[0] != "addpath:/h/new" {
		t.Errorf("calls %v", got)
	}
	if captured(m) {
		t.Error("input still open")
	}
}

func TestSkillsPathsRenameStartsFromTheCurrentPath(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "right", "e")
	if !captured(m) {
		t.Fatal("rename input not focused")
	}
	if out := screen(m); !strings.Contains(out, "~/.agents/skills") || !strings.Contains(out, "Rename") {
		t.Errorf("the current path is not prefilled:\n%s", out)
	}
	send(t, m, "backspace", "backspace", "backspace", "backspace", "backspace", "backspace")
	typeText(t, m, "other")
	send(t, m, "enter")
	if got := f.did(); len(got) != 1 || got[0] != "mvpath:~/.agents/skills>~/.agents/other" {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsPathsRenameAppliesToTheSelectedRow(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "right", "down", "e")
	send(t, m, "x", "enter")
	if got := f.did(); len(got) != 1 || !strings.HasPrefix(got[0], "mvpath:/h/gone>/h/gone") {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsPathsRenameUnchangedOrEmptyOrEscDoesNothing(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "right", "e", "enter") // unchanged
	send(t, m, "e", "x", "esc")       // cancelled
	send(t, m, "e")
	for i := 0; i < 40; i++ {
		send(t, m, "backspace")
	}
	send(t, m, "enter") // emptied
	if len(f.did()) != 0 {
		t.Errorf("calls %v", f.did())
	}
	if captured(m) {
		t.Error("input still open")
	}
}

func TestSkillsPathsRemoveAsksFirstAndNeverDeletesFiles(t *testing.T) {
	f := newFake(t)
	someSkills(f)
	m := inSkills(t, f)
	send(t, m, "right", "d")
	out := screen(m)
	if !strings.Contains(out, "~/.agents/skills") || !strings.Contains(out, "directory stays") {
		t.Errorf("prompt should name the path and say the directory stays:\n%s", out)
	}
	if len(f.did()) != 0 {
		t.Fatal("removed without asking")
	}
	send(t, m, "n")
	if len(f.did()) != 0 {
		t.Error("removed after declining")
	}
	send(t, m, "down", "d", "y")
	if got := f.did(); len(got) != 1 || got[0] != "rmpath:/h/gone" {
		t.Errorf("calls %v", got)
	}
}

func TestSkillsPathsRemoveWithNothingSelected(t *testing.T) {
	f := newFake(t)
	m := inSkills(t, f)
	send(t, m, "right", "d", "y")
	if len(f.did()) != 0 {
		t.Errorf("calls %v", f.did())
	}
}

func TestSkillsSearchSelectsTheBestMatchNotTheOldSelection(t *testing.T) {
	f := newFake(t)
	f.skills = []skills.Skill{
		{Name: "alpha", Path: "/h/pool/alpha", Active: true, State: skills.Synced},
		{Name: "gamma", Path: "/h/pool/gamma", State: skills.Inactive},
	}
	m := inSkills(t, f) // alpha is selected
	send(t, m, "/")
	typeText(t, m, "gam")
	send(t, m, "enter", "space")
	if got := f.did(); len(got) != 1 || got[0] != "activate:gamma" {
		t.Errorf("toggled the old selection: %v", got)
	}
}
