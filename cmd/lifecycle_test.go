package cmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/container"
)

type fakeLC struct{ calls []string }

func (f *fakeLC) Start() (container.Outcome, error) {
	f.calls = append(f.calls, "start")
	return container.Started, nil
}
func (f *fakeLC) Stop() (container.Outcome, error) {
	f.calls = append(f.calls, "stop")
	return container.Stopped, nil
}
func (f *fakeLC) Restart() (container.Outcome, error) {
	f.calls = append(f.calls, "restart")
	return container.Restarted, nil
}

var _ Lifecycle = (*container.Lifecycle)(nil)

func TestLifecycleCommandsDispatch(t *testing.T) {
	for _, cmd := range []string{"start", "stop", "restart"} {
		h := newHarness(t, "", "")
		f := &fakeLC{}
		h.app.Lifecycle = f
		if code := h.app.Run([]string{cmd}); code != 0 {
			t.Errorf("%s: exit %d: %s", cmd, code, h.err)
		}
		if len(f.calls) != 1 || f.calls[0] != cmd {
			t.Errorf("%s: calls %v", cmd, f.calls)
		}
	}
}

type errLC struct{ fakeLC }

func (errLC) Start() (container.Outcome, error)   { return "", errors.New("start boom") }
func (errLC) Stop() (container.Outcome, error)    { return "", errors.New("stop boom") }
func (errLC) Restart() (container.Outcome, error) { return "", errors.New("restart boom") }

func TestLifecycleErrorsExitOne(t *testing.T) {
	for _, cmd := range []string{"start", "stop", "restart"} {
		h := newHarness(t, "", "")
		h.app.Lifecycle = errLC{}
		if code := h.app.Run([]string{cmd}); code != 1 || !strings.Contains(h.err.String(), cmd+" boom") {
			t.Errorf("%s: exit %d err %q", cmd, code, h.err)
		}
	}
}

func TestLifecycleWithoutBackendFailsCleanly(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run([]string{"start"}); code != 1 {
		t.Errorf("exit %d", code)
	}
}
