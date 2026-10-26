package cmd

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ossyrian/tau/internal/session"
)

// sessionDocker records the docker calls Manager makes.
type sessionDocker struct {
	calls [][]string
	run   [][]string
	out   map[string]string // tmux subcommand -> output
	err   map[string]error
}

func (d *sessionDocker) Output(args ...string) (string, error) {
	d.calls = append(d.calls, args)
	if args[0] == "container" && args[1] == "inspect" {
		return "true\n", nil
	}
	sub := args[9]
	if e := d.err[sub]; e != nil {
		return "", e
	}
	return d.out[sub], nil
}
func (d *sessionDocker) Run(args ...string) error { d.run = append(d.run, args); return nil }

func withSessions(h *harness) *sessionDocker {
	d := &sessionDocker{out: map[string]string{}, err: map[string]error{"has-session": errors.New("none")}}
	h.app.Sessions = &session.Manager{
		D: d, Container: "tau", Profile: "default", User: "pi", PiCmd: "pi",
		Rand: func() string { return "rand0001" }, Loc: time.UTC,
	}
	if h.app.Core != nil {
		h.app.Core.Sess = h.app.Sessions // production wires both to one manager
	}
	return d
}

func TestSessionNewVariants(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"sessions", "new"}, "rand0001"},
		{[]string{"sessions", "new", "foo"}, "foo"},
		{[]string{"sessions", "new", "--name", "foo"}, "foo"},
		{[]string{"sessions", "new", "--name=foo"}, "foo"},
	}
	for _, c := range cases {
		h := newHarness(t, "", "")
		d := withSessions(h)
		if code := h.app.Run(c.args); code != 0 {
			t.Errorf("%v: exit %d: %s", c.args, code, h.err)
			continue
		}
		if h.out.String() != c.want+"\n" || len(d.run) != 1 {
			t.Errorf("%v: out %q run %v", c.args, h.out, d.run)
		}
	}
}

func TestSessionNewRejectsBadUsage(t *testing.T) {
	for _, args := range [][]string{
		{"sessions", "new", "--bogus"},
		{"sessions", "new", "a", "b"},
		{"sessions", "new", "--name"},
		{"sessions", "new", "--name", "a", "b"},
		{"sessions", "new", "--name", "a", "--name", "b"},
		{"sessions", "new", "foo", "--name", "bar"},
	} {
		h := newHarness(t, "", "")
		d := withSessions(h)
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
		if len(d.calls) != 0 || len(d.run) != 0 {
			t.Errorf("%v: reached docker", args)
		}
	}
}

func TestSessionList(t *testing.T) {
	h := newHarness(t, "", "")
	d := withSessions(h)
	d.out["list-sessions"] = "main|1700000000|1700000000\n"
	if code := h.app.Run([]string{"sessions", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	h.app.Now = func() time.Time { return time.Unix(1700000000, 0).Add(2 * time.Hour) }
	h.out.Reset()
	if code := h.app.Run([]string{"sessions", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	assertHasWords(t, rowOf(t, h.out.String(), "main"), "session", "default", "2", "hours", "ago")
	if code := h.app.Run([]string{"sessions", "list", "extra"}); code != 1 {
		t.Errorf("extra arg: %d", code)
	}
}

func TestSessionKillAndAttachTakeExactlyOneName(t *testing.T) {
	for _, sub := range []string{"kill", "attach"} {
		h := newHarness(t, "", "")
		d := withSessions(h)
		d.out["list-sessions"] = "main|1700000000|1700000000\n"
		if code := h.app.Run([]string{"sessions", sub, "ma"}); code != 0 {
			t.Errorf("%s: exit %d: %s", sub, code, h.err)
		}
		for _, bad := range [][]string{{"sessions", sub}, {"sessions", sub, "a", "b"}} {
			if code := h.app.Run(bad); code != 1 {
				t.Errorf("%v: exit %d", bad, code)
			}
		}
	}
}

func TestSessionErrorsExitOne(t *testing.T) {
	h := newHarness(t, "", "")
	d := withSessions(h)
	d.out["list-sessions"] = "main|1700000000|1700000000\n"
	if code := h.app.Run([]string{"sessions", "kill", "nope"}); code != 1 || !strings.Contains(h.err.String(), "no session matching") {
		t.Errorf("exit %d err %q", code, h.err)
	}
}

func TestSessionWithoutManagerFailsCleanly(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run([]string{"sessions", "list"}); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestShellOpensZshInTheContainer(t *testing.T) {
	h := newHarness(t, "", "")
	d := withSessions(h)
	if code := h.app.Run([]string{"shell"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if len(d.run) != 1 || d.run[0][len(d.run[0])-1] != "umask 002 && exec zsh" {
		t.Errorf("ran %v", d.run)
	}
	if code := h.app.Run([]string{"shell", "extra"}); code != 1 {
		t.Errorf("extra arg: exit %d", code)
	}
}

func TestShellWithoutManagerFailsCleanly(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run([]string{"shell"}); code != 1 {
		t.Errorf("exit %d", code)
	}
}
