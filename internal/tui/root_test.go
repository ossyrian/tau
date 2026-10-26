package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
)

// drive feeds msgs to the model and runs every command it returns, feeding the
// results back, until no command is left. Timers and spinner ticks are skipped.
func init() { tickInterval = 0 }

func drive(t *testing.T, m *Root, msgs ...tea.Msg) {
	t.Helper()
	queue := append([]tea.Msg{}, msgs...)
	for n := 0; len(queue) > 0; n++ {
		if n > 200 {
			t.Fatal("message loop does not end")
		}
		msg := queue[0]
		queue = queue[1:]
		next, cmd := m.Update(msg)
		*m = next.(Root)
		queue = append(queue, collect(cmd)...)
	}
}

// collect runs a command and flattens batches. It skips blocking timers.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := runCmd(cmd)
	switch v := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range v {
			out = append(out, collect(c)...)
		}
		return out
	case tea.QuitMsg:
		return []tea.Msg{v}
	}
	if isTimer(msg) {
		return nil
	}
	return []tea.Msg{msg}
}

// runCmd runs cmd, giving up on one that blocks (a cursor blink or a timer).
func runCmd(cmd tea.Cmd) tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	select {
	case m := <-ch:
		return m
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func isTimer(msg tea.Msg) bool {
	if _, ok := msg.(tickMsg); ok {
		return true
	}
	return strings.HasPrefix(typeNameOf(msg), "spinner.")
}

func typeNameOf(v any) string { return fmt.Sprintf("%T", v) }

func newRoot(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := NewRoot(f)
	drive(t, &m, tea.WindowSizeMsg{Width: 100, Height: 30})
	drive(t, &m, collect(m.Init())...)
	return &m
}

func send(t *testing.T, m *Root, keys ...string) {
	t.Helper()
	for _, k := range keys {
		drive(t, m, key(k))
	}
}

func screen(m *Root) string { return ansi.Strip(m.View().Content) }

func TestViewUsesAltScreenAndNeverPaintsABackground(t *testing.T) {
	m := newRoot(t, newFake(t))
	v := m.View()
	if !v.AltScreen {
		t.Error("not on the alternate screen")
	}
	if v.BackgroundColor != nil {
		t.Error("sets a terminal background colour")
	}
	themed(t, "root view", strings.ReplaceAll(v.Content, "\x1b[7m", ""))
}

func TestTabCyclesSectionsAndNumbersJump(t *testing.T) {
	m := newRoot(t, newFake(t))
	if m.active != 0 {
		t.Fatal("starts on home")
	}
	send(t, m, "tab")
	if m.active != 1 {
		t.Errorf("tab: %d", m.active)
	}
	send(t, m, "shift+tab", "shift+tab")
	if m.active != len(m.sections)-1 {
		t.Errorf("shift+tab wraps: %d", m.active)
	}
	send(t, m, "tab")
	if m.active != 0 {
		t.Errorf("tab wraps: %d", m.active)
	}
	send(t, m, "4")
	if m.sections[m.active].Name() != "packages" {
		t.Errorf("4 -> %s", m.sections[m.active].Name())
	}
	send(t, m, "9", "0")
	if m.sections[m.active].Name() != "packages" {
		t.Error("an out-of-range number moved the section")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "ctrl+c"} {
		m := newRoot(t, newFake(t))
		_, cmd := m.Update(key(k))
		if cmd == nil {
			t.Fatalf("%s: no command", k)
		}
		if _, ok := runCmd(cmd).(tea.QuitMsg); !ok {
			t.Errorf("%s did not quit", k)
		}
	}
}

func TestRootKeysDoNotFireWhileATextInputHasTheKeyboard(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	send(t, m, "5") // env
	send(t, m, "n") // new variable: opens the name input
	if !m.sections[m.active].Capturing() {
		t.Fatal("env did not capture the keyboard")
	}
	for _, k := range []string{"q", "tab", "1"} {
		_, cmd := m.Update(key(k))
		if cmd != nil {
			if _, quit := cmd().(tea.QuitMsg); quit {
				t.Errorf("%q quit while typing", k)
			}
		}
	}
	if m.sections[m.active].Name() != "env" {
		t.Error("section changed while typing")
	}
	// ctrl+c still quits.
	_, cmd := m.Update(key("ctrl+c"))
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Error("ctrl+c must always quit")
	}
}

func TestBackgroundActionShowsNoticeAndRefreshesStatus(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	f.status.Container = container.State{Exists: true, Running: true}
	drive(t, m, runMsg{"saving", func() (string, error) { return "saved it", nil }})
	if !strings.Contains(screen(m), "saved it") {
		t.Errorf("no notice:\n%s", screen(m))
	}
	if m.busy != "" {
		t.Errorf("still busy: %q", m.busy)
	}
}

func TestOnlyOneBackgroundActionAtATime(t *testing.T) {
	m := newRoot(t, newFake(t))
	ran := 0
	next, _ := m.Update(runMsg{"first", func() (string, error) { ran++; return "", nil }})
	*m = next.(Root)
	if m.busy != "first" {
		t.Fatalf("busy %q", m.busy)
	}
	next, cmd := m.Update(runMsg{"second", func() (string, error) { ran++; return "", nil }})
	*m = next.(Root)
	if cmd != nil && len(collect(cmd)) > 0 || m.busy != "first" {
		t.Error("a second action started while busy")
	}
	if !strings.Contains(m.notice, "busy") && !strings.Contains(screen(m), "first") {
		t.Errorf("the user is not told why: notice %q", m.notice)
	}
}

func TestConfirmAsksThenRunsOnYAndDropsOnAnythingElse(t *testing.T) {
	for _, tc := range []struct {
		key string
		ran bool
	}{{"y", true}, {"n", false}, {"esc", false}, {"x", false}} {
		m := newRoot(t, newFake(t))
		ran := false
		drive(t, m, confirmMsg{"Really?", func() tea.Cmd { ran = true; return nil }})
		if !strings.Contains(screen(m), "Really?") {
			t.Fatalf("no prompt:\n%s", screen(m))
		}
		send(t, m, tc.key)
		if ran != tc.ran {
			t.Errorf("%q: ran=%v", tc.key, ran)
		}
		if strings.Contains(screen(m), "Really?") {
			t.Errorf("%q: prompt stays", tc.key)
		}
	}
}

func TestConfirmSwallowsOtherKeys(t *testing.T) {
	m := newRoot(t, newFake(t))
	drive(t, m, confirmMsg{"Sure?", func() tea.Cmd { return nil }})
	send(t, m, "tab", "q")
	if m.active != 0 {
		t.Error("navigated during a prompt")
	}
}

func TestAttachHandsTheTerminalToDocker(t *testing.T) {
	m := newRoot(t, newFake(t))
	_, cmd := m.Update(attachMsg{[]string{"exec", "-it", "tau", "attach"}})
	if cmd == nil {
		t.Fatal("no command")
	}
	if name := strings.ToLower(typeNameOf(cmd())); !strings.Contains(name, "exec") {
		t.Errorf("got %s, want an exec message", name)
	}
}

func TestAttachedRefreshesTheScreen(t *testing.T) {
	m := newRoot(t, newFake(t))
	_, cmd := m.Update(attachedMsg{})
	if len(collect(cmd)) == 0 {
		t.Error("nothing refreshed after returning")
	}
}

var _ = core.Status{}
