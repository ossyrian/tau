package tui

import (
	"os"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
)

// bigSkills is two skills, so the cursor has somewhere to go, with a long SKILL.md.
func bigSkills(t *testing.T, f *fakeAPI) {
	t.Helper()
	f.skills = []skills.Skill{
		{Name: "alpha", Path: writeSkill(t, "alpha", strings.Repeat("alpha line\n", 80)), State: skills.Inactive},
		{Name: "beta", Path: writeSkill(t, "beta", "beta body\n"), State: skills.Inactive},
	}
}

func writeSkill(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/SKILL.md", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestTabCyclesTheMenuAndEveryPaneOfTheSection(t *testing.T) {
	f := newFake(t)
	bigSkills(t, f)
	m := newRoot(t, f)
	open(t, m, "skills") // focus is on the first pane
	want := []int{1, -1, 0, 1}
	for i, w := range want {
		send(t, m, "tab")
		if m.focus != w {
			t.Errorf("tab %d: focus %d want %d", i+1, m.focus, w)
		}
	}
	send(t, m, "shift+tab")
	if m.focus != 0 {
		t.Errorf("shift+tab: focus %d want 0", m.focus)
	}
}

func TestTabSkipsTheDetailPaneOfASingleSectionAndOfAnEmptyOne(t *testing.T) {
	m := newRoot(t, newFake(t))
	open(t, m, "env") // one pane
	send(t, m, "tab")
	if m.focus != -1 {
		t.Errorf("env: tab went to %d, not the menu", m.focus)
	}
	f := newFake(t)
	f.skills = nil
	m = newRoot(t, f)
	open(t, m, "skills") // no rows, so only a message
	send(t, m, "tab")
	if m.focus != -1 {
		t.Errorf("empty skills: tab went to %d, not the menu", m.focus)
	}
}

func TestHLMoveBetweenTheMenuAndTheMainArea(t *testing.T) {
	f := newFake(t)
	bigSkills(t, f)
	m := newRoot(t, f)
	open(t, m, "skills")
	send(t, m, "h")
	if m.focus != -1 {
		t.Fatalf("h: focus %d", m.focus)
	}
	send(t, m, "l")
	if m.focus != 0 {
		t.Errorf("l: focus %d", m.focus)
	}
	send(t, m, "left")
	send(t, m, "right")
	if m.focus != 0 {
		t.Errorf("left, right: focus %d", m.focus)
	}
}

func TestInTheMenuJKAndTheArrowsPickTheSection(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	send(t, m, "h")
	start := m.active
	send(t, m, "j")
	if m.active != start+1 {
		t.Errorf("j: section %d want %d", m.active, start+1)
	}
	send(t, m, "down")
	send(t, m, "k")
	send(t, m, "up")
	if m.active != start {
		t.Errorf("down k up: section %d want %d", m.active, start)
	}
	before := len(f.did())
	send(t, m, "x") // a section command does nothing in the menu
	if len(f.did()) != before {
		t.Errorf("a section key reached the section: %v", f.did()[before:])
	}
}

func TestJKMoveTheCursorOrScrollTheDetailByFocus(t *testing.T) {
	f := newFake(t)
	bigSkills(t, f)
	m := newRoot(t, f)
	open(t, m, "skills")
	sec := m.sections[m.active].(*skillsSection)
	m.View() // the viewport is sized and filled when drawn, as it is before every key in a real run
	send(t, m, "j")
	if sec.list.pick.table.Cursor() != 1 {
		t.Fatalf("j did not move the cursor: %d", sec.list.pick.table.Cursor())
	}
	send(t, m, "k")
	m.View()
	send(t, m, "tab", "j", "j", "j")
	if y := sec.list.det.vp.YOffset(); y != 3 {
		t.Errorf("detail offset %d want 3", y)
	}
	if c := sec.list.pick.table.Cursor(); c != 0 {
		t.Errorf("scrolling moved the table cursor to %d", c)
	}
	m.View()
	send(t, m, "k")
	if y := sec.list.det.vp.YOffset(); y != 2 {
		t.Errorf("detail offset %d want 2 after k", y)
	}
}

func TestMovingTheFocusRedrawsTheBorders(t *testing.T) {
	f := newFake(t)
	bigSkills(t, f)
	m := newRoot(t, f)
	open(t, m, "skills")
	seen := map[string]bool{m.View().Content: true}
	for i := 0; i < 2; i++ { // detail, then menu
		send(t, m, "tab")
		seen[m.View().Content] = true
	}
	if len(seen) != 3 {
		t.Errorf("%d distinct screens for 3 focus positions", len(seen))
	}
}

func TestKillingASessionMovedOffKSoJCanMoveDown(t *testing.T) {
	f := newFake(t)
	f.sessions = []session.Info{{Profile: "default", Name: "main", Kind: "main"}}
	m := newRoot(t, f)
	open(t, m, "sessions")
	send(t, m, "d")
	if m.prompt == nil {
		t.Error("d did not ask to kill the session")
	}
}
