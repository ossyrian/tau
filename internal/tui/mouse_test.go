package tui

import (
	"github.com/ossyrian/tau/internal/envfile"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/session"
)

func click(x, y int) tea.Msg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func wheel(x, y int, b tea.MouseButton) tea.Msg {
	return tea.MouseWheelMsg{X: x, Y: y, Button: b}
}

// mainX is a column inside the main area; menuX one inside the menu.
func mainX(m *Root) int { l, _, _ := m.layout(); return l + 3 }
func menuX() int        { return 3 }

// menuY is the screen line of the i-th menu item: under the status pane and
// the menu pane's border.
func menuY(i int) int { return statusHeight + 1 + i }

func TestMouseIsOn(t *testing.T) {
	m := newRoot(t, newFake(t))
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Error("the view does not ask for the mouse")
	}
}

func TestClickingAMenuItemOpensItsSectionAndFocusesIt(t *testing.T) {
	m := newRoot(t, newFake(t))
	send(t, m, "h")
	for i, s := range m.sections {
		drive(t, m, click(menuX(), menuY(i)))
		if m.sections[m.active].Name() != s.Name() || m.focus != 0 {
			t.Errorf("click on item %d: section %q focus %d, want %q 0", i, m.sections[m.active].Name(), m.focus, s.Name())
		}
	}
}

func TestClickingOffTheListOrOnTheStatusPaneDoesNothing(t *testing.T) {
	m := newRoot(t, newFake(t))
	before, focus := m.active, m.focus
	drive(t, m, click(menuX(), 1))                        // the status pane
	drive(t, m, click(menuX(), menuY(len(m.sections)+3))) // below the last item
	drive(t, m, click(5, m.h-1))                          // the help line
	drive(t, m, tea.MouseClickMsg{X: menuX(), Y: menuY(2), Button: tea.MouseRight})
	if m.active != before || m.focus != focus {
		t.Errorf("section %d→%d focus %d→%d", before, m.active, focus, m.focus)
	}
}

func TestWheelOverTheMenuStepsThroughTheSections(t *testing.T) {
	m := newRoot(t, newFake(t))
	start := m.active
	drive(t, m, wheel(menuX(), menuY(0), tea.MouseWheelDown))
	if m.active != start+1 {
		t.Errorf("wheel down: section %d want %d", m.active, start+1)
	}
	drive(t, m, wheel(menuX(), menuY(0), tea.MouseWheelUp), wheel(menuX(), menuY(0), tea.MouseWheelUp))
	if m.active != 0 {
		t.Errorf("wheel up past the top: section %d", m.active)
	}
}

func skillsFake(t *testing.T) *fakeAPI {
	f := newFake(t)
	bigSkills(t, f)
	return f
}

func TestClickingARowSelectsItAndClickingItAgainActivatesIt(t *testing.T) {
	f := skillsFake(t)
	m := newRoot(t, f)
	open(t, m, "skills")
	send(t, m, "tab", "tab", "tab") // park the focus away from the list
	m.View()                        // the program draws before every event
	row2 := 3                       // border, header, alpha, beta
	drive(t, m, click(mainX(m), row2))
	sec := m.sections[m.active].(*skillsSection)
	if m.focus != 0 || sec.list.pick.table.Cursor() != 1 {
		t.Fatalf("focus %d cursor %d", m.focus, sec.list.pick.table.Cursor())
	}
	if hasCall(f, "activate:beta") {
		t.Fatal("the first click already activated it")
	}
	m.View()
	drive(t, m, click(mainX(m), row2))
	if !hasCall(f, "activate:beta") {
		t.Errorf("second click did not activate: %v", f.did())
	}
}

func TestClickingTheDetailPaneFocusesItWithoutMovingTheCursor(t *testing.T) {
	f := skillsFake(t)
	m := newRoot(t, f)
	open(t, m, "skills")
	m.View()
	_, _, rows := m.layout()
	top, _ := splitHeight(rows)
	drive(t, m, click(mainX(m), top+2))
	if m.focus != 1 {
		t.Errorf("focus %d, want the detail pane", m.focus)
	}
	if c := m.sections[m.active].(*skillsSection).list.pick.table.Cursor(); c != 0 {
		t.Errorf("cursor moved to %d", c)
	}
}

func TestWheelScrollsThePaneUnderThePointerWhateverHasFocus(t *testing.T) {
	f := skillsFake(t)
	m := newRoot(t, f)
	open(t, m, "skills")
	m.View()
	sec := m.sections[m.active].(*skillsSection)
	_, _, rows := m.layout()
	top, _ := splitHeight(rows)
	drive(t, m, wheel(mainX(m), top+2, tea.MouseWheelDown)) // focus is on the list
	if y := sec.list.det.vp.YOffset(); y != 3 {
		t.Errorf("detail offset %d, want 3 (one wheel notch)", y)
	}
	if m.focus != 0 {
		t.Errorf("scrolling moved the focus to %d", m.focus)
	}
	m.View()
	drive(t, m, wheel(mainX(m), 3, tea.MouseWheelDown))
	if c := sec.list.pick.table.Cursor(); c != 1 {
		t.Errorf("list cursor %d, want 1", c)
	}
}

func TestClickingTheSelectedSessionAgainAttachesLikeEnter(t *testing.T) {
	f := newFake(t)
	f.sessions = []session.Info{{Profile: "default", Name: "main", Kind: "main"}}
	m := newRoot(t, f)
	open(t, m, "sessions")
	m.View()
	// The only row is already selected, so one click is the second.
	_, cmd := m.Update(click(mainX(m), 2))
	k, ok := runCmd(cmd).(tea.KeyPressMsg)
	if !ok || k.Code != tea.KeyEnter {
		t.Fatalf("clicking the selected row should send enter, got %v", runCmd(cmd))
	}
}

func TestTheMouseIsIgnoredWhileAnInputOrAPromptHasTheScreen(t *testing.T) {
	f := skillsFake(t)
	m := newRoot(t, f)
	open(t, m, "skills")
	send(t, m, "/") // the filter has the keyboard
	drive(t, m, click(menuX(), menuY(0)))
	if m.sections[m.active].Name() != "skills" {
		t.Error("a click left the section while typing a filter")
	}
	send(t, m, "esc", "r") // r asks to restart
	if m.prompt == nil {
		t.Fatal("no prompt to test with")
	}
	drive(t, m, click(menuX(), menuY(0)))
	if m.sections[m.active].Name() != "skills" || m.prompt == nil {
		t.Error("a click got past a confirmation")
	}
}

func TestShiftedMovesThePositionAndKeepsTheMessageType(t *testing.T) {
	msg := shifted(click(40, 7).(tea.MouseMsg), -25, 0)
	if at := msg.Mouse(); at.X != 15 || at.Y != 7 {
		t.Errorf("got %v", at)
	}
	if _, ok := msg.(tea.MouseClickMsg); !ok {
		t.Errorf("click became %T", msg)
	}
	if _, ok := shifted(wheel(1, 1, tea.MouseWheelUp).(tea.MouseMsg), 0, 0).(tea.MouseWheelMsg); !ok {
		t.Error("a wheel turn changed type")
	}
}

func TestClickingTheSelectedVariableAgainEditsIt(t *testing.T) {
	f := newFake(t)
	f.vars = []envfile.Var{{Key: "A", Value: "1"}, {Key: "B", Value: "2"}}
	m := newRoot(t, f)
	open(t, m, "env")
	sec := m.sections[m.active].(*envSection)
	m.View()
	drive(t, m, click(mainX(m), 3)) // border, header, A, B → B
	if sec.vars.step != varBrowse || sec.vars.pick.table.Cursor() != 1 {
		t.Fatalf("first click: step %d cursor %d", sec.vars.step, sec.vars.pick.table.Cursor())
	}
	m.View()
	drive(t, m, click(mainX(m), 3))
	if sec.vars.step != varValue || sec.vars.key != "B" {
		t.Errorf("second click: step %d key %q, want editing B", sec.vars.step, sec.vars.key)
	}
}

func TestEnterEditsTheSelectedVariable(t *testing.T) {
	f := newFake(t)
	f.vars = []envfile.Var{{Key: "A", Value: "1"}}
	m := newRoot(t, f)
	open(t, m, "env")
	send(t, m, "enter")
	sec := m.sections[m.active].(*envSection)
	if sec.vars.step != varValue || sec.vars.key != "A" {
		t.Errorf("step %d key %q", sec.vars.step, sec.vars.key)
	}
}
