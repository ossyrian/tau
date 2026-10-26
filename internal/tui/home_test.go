package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/ossyrian/tau/internal/container"
)

func TestHomeStartRunsWithoutAsking(t *testing.T) {
	f := newFake(t)
	f.status.Container = container.State{}
	m := newRoot(t, f)
	send(t, m, "s")
	if got := f.did(); len(got) != 1 || got[0] != "start" {
		t.Errorf("calls %v", got)
	}
	if m.busy != "" {
		t.Error("still busy after the job finished")
	}
}

func TestHomeStopAndRestartAskFirstBecauseSessionsAreLost(t *testing.T) {
	for _, k := range []struct{ key, call string }{{"x", "stop"}, {"r", "restart"}} {
		f := newFake(t)
		m := newRoot(t, f)
		send(t, m, k.key)
		if len(f.did()) != 0 {
			t.Fatalf("%s ran without asking", k.call)
		}
		if !strings.Contains(screen(m), "sessions") {
			t.Errorf("%s: the prompt does not say sessions are lost:\n%s", k.call, screen(m))
		}
		send(t, m, "n")
		if len(f.did()) != 0 {
			t.Errorf("%s ran after declining", k.call)
		}
		send(t, m, k.key, "y")
		if got := f.did(); len(got) != 1 || got[0] != k.call {
			t.Errorf("%s: calls %v", k.call, got)
		}
	}
}

func TestHomeStartOnARunningContainerIsHarmless(t *testing.T) {
	// The lifecycle reports "already running"; the screen just passes it on.
	f := newFake(t)
	m := newRoot(t, f)
	send(t, m, "s")
	if m.noticeErr {
		t.Error("treated as an error")
	}
}

func TestUptimeFormat(t *testing.T) {
	cases := map[time.Duration]string{
		0: "0m", 30 * time.Second: "0m", 5 * time.Minute: "5m", 90 * time.Minute: "1h 30m",
		25 * time.Hour: "1d 1h", 49*time.Hour + 5*time.Minute: "2d 1h",
	}
	for d, want := range cases {
		if got := uptime(d); got != want {
			t.Errorf("%v: got %q want %q", d, got, want)
		}
	}
}
