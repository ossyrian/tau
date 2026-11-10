package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/config"
)

func key(s string) tea.KeyPressMsg {
	switch s {
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func setup(t *testing.T, base, runtime string) (string, *config.Config) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(base), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.runtime.yaml"), []byte(runtime), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return dir, c
}

const (
	baseYAML    = "packages:\n  a: {script: sa}\n  x: {script: base-x}\n  late: {script: sl}\n"
	runtimeYAML = "packages:\n  a: {script: sa}\n  x: {script: runtime-x}\n  r1: {script: s1}\n  r2: {script: s2}\n"
)

// press sends keys and returns the final model and whether a quit was requested.
func press(m Resolve, keys ...string) (Resolve, bool) {
	quit := false
	for _, k := range keys {
		next, cmd := m.Update(key(k))
		m = next.(Resolve)
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				quit = true
			}
		}
	}
	return m, quit
}

// sized gives the screen a terminal to draw in.
func sized(m Resolve) Resolve {
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return next.(Resolve)
}

func names(m Resolve) []string {
	var out []string
	for _, it := range m.items {
		out = append(out, it.name)
	}
	return out
}

func TestNewListsOnlyDriftInRuntimeFileOrder(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := NewResolve(c)
	// x changed, r1/r2 runtime-only; "late" is base-only and must not be listed.
	if want := []string{"x", "r1", "r2"}; !reflect.DeepEqual(names(m), want) {
		t.Errorf("got %v want %v", names(m), want)
	}
	if m.items[0].runtimeOnly || !m.items[1].runtimeOnly {
		t.Error("kinds wrong")
	}
}

func TestAcceptPromotesRuntimeOnlyAndAdoptsChanged(t *testing.T) {
	dir, c := setup(t, baseYAML, runtimeYAML)
	m, _ := press(NewResolve(c), "a") // x is first: changed -> Adopt
	m, _ = press(m, "a")              // r1: runtime-only -> Promote
	base := mustLoad(t, dir).Base
	if got := base.Packages["x"].Script; !reflect.DeepEqual(got, []string{"runtime-x"}) {
		t.Errorf("x not adopted: %v", got)
	}
	if _, ok := base.Packages["r1"]; !ok {
		t.Error("r1 not promoted")
	}
	if want := []string{"r2"}; !reflect.DeepEqual(names(m), want) {
		t.Errorf("remaining: %v want %v", names(m), want)
	}
}

func TestRejectRevertsChangedAndDiscardsRuntimeOnly(t *testing.T) {
	dir, c := setup(t, baseYAML, runtimeYAML)
	baseBefore, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	m, _ := press(NewResolve(c), "r", "r") // x -> Revert, r1 -> Discard
	rt := mustLoad(t, dir).Runtime
	if got := rt.Packages["x"].Script; !reflect.DeepEqual(got, []string{"base-x"}) {
		t.Errorf("x not reverted: %v", got)
	}
	if _, ok := rt.Packages["r1"]; ok {
		t.Error("r1 not discarded")
	}
	if after, _ := os.ReadFile(filepath.Join(dir, "config.yaml")); string(after) != string(baseBefore) {
		t.Error("config.yaml changed on reject")
	}
	if want := []string{"r2"}; !reflect.DeepEqual(names(m), want) {
		t.Errorf("remaining: %v", names(m))
	}
}

func TestResolvingEverythingSettlesAndQuits(t *testing.T) {
	dir, c := setup(t, baseYAML, runtimeYAML)
	m, quit := press(NewResolve(c), "a", "r", "r")
	if !quit {
		t.Error("expected quit once no drift remains")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.runtime.yaml")); !os.IsNotExist(err) {
		t.Error("runtime file should be deleted after settling")
	}
	if !m.Done() || !strings.Contains(m.Summary(), "3") {
		t.Errorf("summary: %q", m.Summary())
	}
}

func TestQuitKeysLeaveAppliedWorkAndRemainingDrift(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		dir, c := setup(t, baseYAML, runtimeYAML)
		m, quit := press(NewResolve(c), "a", k)
		if !quit {
			t.Errorf("%s: no quit", k)
		}
		if _, err := os.Stat(filepath.Join(dir, "config.runtime.yaml")); err != nil {
			t.Errorf("%s: runtime file must remain while drift remains", k)
		}
		if m.Done() {
			t.Errorf("%s: not done, drift remains", k)
		}
		if got := mustLoad(t, dir).Base.Packages["x"].Script; !reflect.DeepEqual(got, []string{"runtime-x"}) {
			t.Errorf("%s: the applied decision was lost", k)
		}
	}
}

func TestCursorMovesAndActsOnTheSelectedItem(t *testing.T) {
	dir, c := setup(t, baseYAML, runtimeYAML)
	m, _ := press(NewResolve(c), "down", "down", "a") // r2
	if _, ok := mustLoad(t, dir).Base.Packages["r2"]; !ok {
		t.Error("r2 not promoted")
	}
	if want := []string{"x", "r1"}; !reflect.DeepEqual(names(m), want) {
		t.Errorf("remaining: %v", names(m))
	}
	if m.cursor() != 1 {
		t.Errorf("cursor should clamp to the last item, got %d", m.cursor())
	}
}

func TestCursorStaysInRange(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m, _ := press(NewResolve(c), "up", "up", "k")
	if m.cursor() != 0 {
		t.Errorf("cursor %d", m.cursor())
	}
	m, _ = press(m, "down", "down", "down", "down", "j")
	if m.cursor() != 2 {
		t.Errorf("cursor %d", m.cursor())
	}
}

func TestFailedActionShowsErrorAndKeepsItem(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: sa}\n",
		"packages:\n  a: {script: sa}\n  bad: {script: s, needs: [ghost]}\n  ok: {script: so}\n")
	m, _ := press(sized(NewResolve(c)), "a") // promoting bad would break config.yaml
	if !reflect.DeepEqual(names(m), []string{"bad", "ok"}) {
		t.Errorf("failed item must stay: %v", names(m))
	}
	if v := m.View().Content; !strings.Contains(v, "error:") || !strings.Contains(v, "ghost") {
		t.Errorf("error not shown:\n%s", v)
	}
	if strings.Contains(readFile(t, filepath.Join(dir, "config.yaml")), "bad") {
		t.Error("config.yaml was changed")
	}
	m, _ = press(m, "down", "a") // a later success clears the error
	if strings.Contains(m.View().Content, "error:") {
		t.Error("stale error still shown")
	}
}

func TestUnknownKeysAndMessagesDoNothing(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m, quit := press(NewResolve(c), "z", "1", "enter")
	if quit || len(m.items) != 3 {
		t.Error("an unbound key changed state")
	}
	next, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil || len(next.(Resolve).items) != 3 {
		t.Error("window size message changed state")
	}
}

func TestEmptyDriftIsDoneImmediately(t *testing.T) {
	_, c := setup(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n")
	m := NewResolve(c)
	if len(m.items) != 0 || m.Init() == nil {
		t.Error("with no drift Init should request a quit")
	}
}

func mustLoad(t *testing.T, dir string) *config.Config {
	t.Helper()
	c, err := config.Load(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHandleReportsFinishWithoutQuitting(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := NewResolve(c)
	for _, k := range []string{"a", "a"} {
		var finished bool
		m, finished = m.Handle(k)
		if finished {
			t.Fatal("finished early")
		}
	}
	m, finished := m.Handle("a")
	if !finished || !m.Done() {
		t.Errorf("finished %v done %v", finished, m.Done())
	}
}

func TestItemsCountsWhatIsLeft(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := NewResolve(c)
	if len(m.items) != 3 {
		t.Errorf("pending %d", len(m.items))
	}
	m, _ = m.Handle("a")
	if len(m.items) != 2 {
		t.Errorf("pending %d", len(m.items))
	}
}

func TestResolveTabMovesBetweenTheTableAndTheDetailPane(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := sized(NewResolve(c))
	if m.m.focusIndex() != 0 {
		t.Fatalf("starts on pane %d", m.m.focusIndex())
	}
	m, _ = press(m, "tab")
	if m.m.focusIndex() != 1 {
		t.Errorf("tab: pane %d", m.m.focusIndex())
	}
	m, _ = press(m, "shift+tab")
	if m.m.focusIndex() != 0 {
		t.Errorf("shift+tab: pane %d", m.m.focusIndex())
	}
}

func TestResolveJKAndTheArrowsMoveTheCursorAndScrollTheDetailByFocus(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := sized(NewResolve(c))
	m, _ = press(m, "j", "down", "j", "k", "up")
	if m.cursor() != 0 {
		t.Errorf("cursor %d", m.cursor())
	}
	m, _ = press(m, "j")
	m, _ = press(m, "tab", "j", "j")
	if m.cursor() != 1 {
		t.Errorf("scrolling the detail moved the cursor to %d", m.cursor())
	}
}

func TestResolveClickSelectsAndTheWheelMovesTheCursor(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	m := sized(NewResolve(c))
	m.View()                         // the program draws before every event
	next, _ := m.Update(click(4, 3)) // border, header, x, r1
	m = next.(Resolve)
	if m.cursor() != 1 {
		t.Fatalf("click: cursor %d", m.cursor())
	}
	next, _ = m.Update(wheel(4, 3, tea.MouseWheelDown))
	m = next.(Resolve)
	if m.cursor() != 2 {
		t.Errorf("wheel: cursor %d", m.cursor())
	}
	next, _ = m.Update(click(4, 20)) // the detail pane
	if next.(Resolve).m.focusIndex() != 1 {
		t.Error("clicking the detail pane did not focus it")
	}
}

func TestResolveFillsTheTerminalAndAsksForTheMouse(t *testing.T) {
	_, c := setup(t, baseYAML, runtimeYAML)
	v := sized(NewResolve(c)).View()
	if got := len(strings.Split(v.Content, "\n")); got != 30 {
		t.Errorf("%d lines in a 30-line terminal", got)
	}
	if v.MouseMode != tea.MouseModeCellMotion {
		t.Error("no mouse")
	}
	for _, want := range []string{"x (changed)", "base-x", "runtime-x", "late"} {
		if !strings.Contains(ansi.Strip(v.Content), want) {
			t.Errorf("screen lacks %q", want)
		}
	}
}
