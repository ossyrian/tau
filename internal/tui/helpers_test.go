package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
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
