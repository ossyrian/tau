package tui

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/session"
)

func twoSessions(f *fakeAPI) {
	now := time.Now()
	f.sessions = []session.Info{
		{Name: "main", Profile: "default", Kind: "session", Created: now.Add(-3 * time.Hour), Active: now.Add(-2 * time.Minute)},
		{Name: "sa-worker", Profile: "default", Kind: "subagent", Created: now.Add(-10 * time.Minute), Active: now.Add(-5 * time.Second)},
	}
	f.preview = map[string][]string{
		"default/main":      {"> fix the build", "Running tests…", "3 passed"},
		"default/sa-worker": {"scanning repo", "reading files"},
	}
}

func inSessions(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := newRoot(t, f)
	send(t, m, "2")
	return m
}

func TestSessionsEnterAttaches(t *testing.T) {
	f := newFake(t)
	twoSessions(f)
	m := inSessions(t, f)
	send(t, m, "down")
	_, cmd := m.Update(key("enter"))
	var msg any
	for _, got := range collect(cmd) {
		msg = got
	}
	am, ok := msg.(attachMsg)
	if !ok {
		t.Fatalf("got %T", msg)
	}
	if !reflect.DeepEqual(am.args, []string{"exec", "-it", "tau", "attach", "sa-worker"}) {
		t.Errorf("args %v", am.args)
	}
}

func TestSessionsKillAsksFirst(t *testing.T) {
	f := newFake(t)
	twoSessions(f)
	m := inSessions(t, f)
	send(t, m, "k")
	if len(f.did()) != 0 || !strings.Contains(screen(m), "main") {
		t.Fatal("killed without asking")
	}
	send(t, m, "n")
	if len(f.did()) != 0 {
		t.Error("killed after declining")
	}
	send(t, m, "k", "y")
	if got := f.did(); len(got) != 1 || got[0] != "kill:default/main" {
		t.Errorf("calls %v", got)
	}
}

func TestSessionsNewAsksForANameThenCreatesAndAttaches(t *testing.T) {
	f := newFake(t)
	twoSessions(f)
	m := inSessions(t, f)
	send(t, m, "n")
	if !m.sections[m.active].Capturing() {
		t.Fatal("name input not focused")
	}
	for _, c := range "work" {
		send(t, m, string(c))
	}
	// Typing "q" or digits goes to the input, not to the root.
	if m.sections[m.active].Name() != "sessions" {
		t.Fatal("left the section while typing")
	}
	var attached []string
	next, cmd := m.Update(key("enter"))
	*m = next.(Root)
	queue := collect(cmd)
	for len(queue) > 0 {
		msg := queue[0]
		queue = queue[1:]
		if am, ok := msg.(attachMsg); ok {
			attached = am.args
		}
		next, c := m.Update(msg)
		*m = next.(Root)
		queue = append(queue, collect(c)...)
	}
	if calls := f.did(); len(calls) < 2 || calls[0] != "create:work" || calls[1] != "attach:/work" {
		t.Errorf("calls %v", calls)
	}
	if len(attached) == 0 || attached[len(attached)-1] != "work" {
		t.Errorf("attach args %v", attached)
	}
	if m.sections[m.active].Capturing() {
		t.Error("input still open")
	}
}

func TestSessionsEscCancelsTheNameInput(t *testing.T) {
	f := newFake(t)
	m := inSessions(t, f)
	send(t, m, "n", "esc")
	if m.sections[m.active].Capturing() {
		t.Error("still capturing")
	}
	if len(f.did()) != 0 {
		t.Errorf("calls %v", f.did())
	}
}

func TestSessionsEmptyAndStopped(t *testing.T) {
	f := newFake(t)
	if out := screen(inSessions(t, f)); !strings.Contains(out, "no sessions") {
		t.Errorf("empty:\n%s", out)
	}
	f.status.Container = container.State{}
	f.err = errors.New("container not running — 'tau start' first")
	m := inSessions(t, f)
	if out := screen(m); !strings.Contains(out, "not running") {
		t.Errorf("stopped:\n%s", out)
	}
	send(t, m, "k", "enter") // nothing selected: must not panic or call anything
	if len(f.did()) != 0 {
		t.Errorf("acted on nothing: %v", f.did())
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	cases := map[time.Duration]string{
		0: "just now", 20 * time.Second: "just now", 2 * time.Minute: "2m ago",
		3 * time.Hour: "3h ago", 50 * time.Hour: "2d ago",
	}
	for d, want := range cases {
		if got := age(now.Add(-d), now); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
	if age(now.Add(time.Hour), now) != "just now" {
		t.Error("a future time must not go negative")
	}
}

func TestSessionsOfAnotherProfileShowWhoseTheyAreAndActInThatProfile(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	f.sessions = []session.Info{
		{Name: "main", Profile: "default", Kind: "session", Created: now, Active: now},
		{Name: "main", Profile: "work", Kind: "session", Created: now, Active: now.Add(-time.Minute)},
	}
	f.preview = map[string][]string{"default/main": {"default screen"}, "work/main": {"work screen"}}
	m := inSessions(t, f)
	out := screen(m)
	if !strings.Contains(out, "work/main") {
		t.Errorf("profile not shown:\n%s", out)
	}
	if strings.Contains(out, "default/main") {
		t.Errorf("the default profile is not prefixed:\n%s", out)
	}
	send(t, m, "down")
	if !strings.Contains(screen(m), "── work/main") {
		t.Errorf("preview is not the work session's:\n%s", screen(m))
	}
	send(t, m, "k", "y")
	if got := f.did(); len(got) != 1 || got[0] != "kill:work/main" {
		t.Errorf("killed in the wrong profile: %v", got)
	}
}
