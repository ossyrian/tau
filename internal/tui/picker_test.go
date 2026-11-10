package tui

import (
	tea "charm.land/bubbletea/v2"
	"fmt"
	"github.com/ossyrian/tau/internal/skills"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func rowsOf(keys ...string) []pickerRow {
	out := make([]pickerRow, len(keys))
	for i, k := range keys {
		out[i] = pickerRow{Key: k, Cells: []string{"default", k, "detail of " + k}}
	}
	return out
}

func newTestPicker(w, h int, keys ...string) *picker {
	p := newPicker("PROFILE", "NAME", "DETAIL")
	p.SetSize(w, h)
	p.SetRows(rowsOf(keys...))
	return p
}

func selectedKey(p *picker) string { r, _ := p.Selected(); return r.Key }

func TestPickerCursorMovesWithTheTableKeys(t *testing.T) {
	p := newTestPicker(60, 8, "a", "b", "c")
	p.Update(key("down"))
	p.Update(key("j"))
	if selectedKey(p) != "c" {
		t.Errorf("selected %q", selectedKey(p))
	}
	p.Update(key("up"))
	if selectedKey(p) != "b" {
		t.Errorf("selected %q", selectedKey(p))
	}
}

func TestPickerFilterIsFuzzyAndNarrowsTheRows(t *testing.T) {
	p := newTestPicker(60, 8, "pr-babysitter", "python-linting", "env-tools")
	p.Update(key("/"))
	if !p.Capturing() {
		t.Fatal("/ should capture the keyboard")
	}
	for _, r := range "pylnt" { // not a substring of anything: only a fuzzy match finds it
		p.Update(key(string(r)))
	}
	if p.Len() != 1 || selectedKey(p) != "python-linting" {
		t.Errorf("rows %d, selected %q", p.Len(), selectedKey(p))
	}
}

func TestPickerEnterKeepsTheFilterAndEscClearsIt(t *testing.T) {
	p := newTestPicker(60, 8, "alpha", "beta")
	p.Update(key("/"))
	p.Update(key("b"))
	p.Update(key("enter"))
	if p.Capturing() || p.Len() != 1 {
		t.Errorf("after enter: capturing %v, rows %d", p.Capturing(), p.Len())
	}
	p.Update(key("esc"))
	if p.Len() != 2 || p.Filtering() {
		t.Errorf("after esc: rows %d, filtering %v", p.Len(), p.Filtering())
	}
}

func TestPickerSearchMatchesOnlyTheColumnsAskedFor(t *testing.T) {
	p := newPicker("NAME", "INSTALL")
	p.SetSize(60, 8)
	p.SetRows([]pickerRow{
		{Key: "pg", Cells: []string{"pg", "apt: postgres"}, Search: "pg apt: postgres"},
		{Key: "tool", Cells: []string{"tool", "script"}, Search: "tool script"},
	})
	p.Update(key("/"))
	for _, r := range "apt" {
		p.Update(key(string(r)))
	}
	if p.Len() != 1 || selectedKey(p) != "pg" {
		t.Errorf("rows %d, selected %q", p.Len(), selectedKey(p))
	}
}

func TestPickerKeepsTheCursorOnTheSameKeyWhenRowsReload(t *testing.T) {
	p := newTestPicker(60, 8, "a", "b", "c")
	p.Update(key("down"))
	p.SetRows(rowsOf("z", "a", "b", "c"))
	if selectedKey(p) != "b" {
		t.Errorf("selected %q", selectedKey(p))
	}
}

func TestPickerSurvivesNoRowsAndRowsShrinking(t *testing.T) {
	p := newTestPicker(60, 8, "a", "b")
	p.Update(key("down"))
	p.SetRows(nil)
	if _, ok := p.Selected(); ok {
		t.Error("nothing should be selected")
	}
	p.SetRows(rowsOf("only"))
	if selectedKey(p) != "only" {
		t.Errorf("selected %q", selectedKey(p))
	}
}

func TestPickerNeverDrawsWiderOrTallerThanItsSize(t *testing.T) {
	for _, size := range [][2]int{{20, 4}, {40, 6}, {80, 10}, {200, 30}} {
		keys := make([]string, 30)
		for i := range keys {
			keys[i] = fmt.Sprintf("row-%02d-%s", i, strings.Repeat("x", 40))
		}
		p := newTestPicker(size[0], size[1], keys...)
		p.Update(key("/"))
		p.Update(key("r"))
		lines := strings.Split(p.View(), "\n")
		if len(lines) > size[1] {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%v: a line is %d wide", size, w)
			}
		}
	}
}

func TestPickerKeepsTheTableKeysThatSectionsNeed(t *testing.T) {
	// d, b, f, u and space belong to the sections' own commands.
	p := newTestPicker(60, 4, "a", "b", "c", "d", "e", "f")
	for _, k := range []string{"d", "b", "f", "u", "space"} {
		p.Update(key(k))
		if selectedKey(p) != "a" {
			t.Fatalf("%q moved the cursor", k)
		}
	}
}

func TestTypingAFilterPutsTheCursorOnTheBestMatch(t *testing.T) {
	p := newTestPicker(60, 8, "alpha", "beta", "python-linting", "pylint")
	p.Update(key("down"))
	p.Update(key("down"))
	p.Update(key("down")) // on "pylint"
	p.Update(key("/"))
	for _, r := range "python" {
		p.Update(key(string(r)))
	}
	if selectedKey(p) != "python-linting" {
		t.Errorf("selected %q", selectedKey(p))
	}
}

func TestSkillsSearchMatchesTheNameAndNotTheStateOrPath(t *testing.T) {
	f := newFake(t)
	f.skills = []skills.Skill{
		{Name: "prune", Path: "/skills/prune", State: skills.Synced},
		{Name: "python-linting", Path: "/skills/python-linting", State: skills.Inactive},
	}
	m := newRoot(t, f)
	open(t, m, "skills")
	send(t, m, "/", "p", "y", "enter")
	if s := screen(m); !strings.Contains(s, "python-linting") || strings.Contains(s, "prune") {
		t.Errorf("py matched the wrong rows:\n%s", s)
	}
}

func TestClickSelectsTheRowDrawnOnThatLine(t *testing.T) {
	p := newTestPicker(60, 8, "a", "b", "c", "d")
	// line 0 is the header, so row "c" is on line 3
	if again, ok := p.Click(3); !ok || again || selectedKey(p) != "c" {
		t.Fatalf("click c: again %v ok %v selected %q", again, ok, selectedKey(p))
	}
	if again, ok := p.Click(3); !ok || !again {
		t.Errorf("a second click on the selected row should say so: again %v ok %v", again, ok)
	}
	for _, y := range []int{0, 5, 40, -1} { // the header, below the last row, far off
		if _, ok := p.Click(y); ok {
			t.Errorf("line %d is not a row", y)
		}
	}
	if selectedKey(p) != "c" {
		t.Errorf("a miss moved the cursor to %q", selectedKey(p))
	}
}

func TestClickFindsTheRightRowAfterTheTableHasScrolled(t *testing.T) {
	keys := make([]string, 30)
	for i := range keys {
		keys[i] = fmt.Sprintf("row-%02d", i)
	}
	p := newTestPicker(60, 6, keys...) // 5 body lines
	for i := 0; i < 12; i++ {
		p.Update(key("down"))
	}
	for line := 1; line <= 5; line++ {
		if _, ok := p.Click(line); !ok {
			t.Fatalf("line %d not a row", line)
		}
		// the row clicked is the one drawn on that line
		body := strings.Split(p.table.View(), "\n")[line]
		if !strings.Contains(body, selectedKey(p)) {
			t.Errorf("line %d shows %q but selected %q", line, strings.TrimSpace(body), selectedKey(p))
		}
	}
}

func TestWheelMovesThePickersCursor(t *testing.T) {
	p := newTestPicker(60, 8, "a", "b", "c")
	p.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	p.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	p.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	if selectedKey(p) != "b" {
		t.Errorf("selected %q", selectedKey(p))
	}
}
