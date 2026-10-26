package cmd

import (
	"errors"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
)

type fakeLife struct {
	st    container.State
	calls []string
}

func (f *fakeLife) Start() (container.Outcome, error) {
	f.calls = append(f.calls, "start")
	return container.Started, nil
}
func (f *fakeLife) Stop() (container.Outcome, error) {
	f.calls = append(f.calls, "stop")
	return container.Stopped, nil
}
func (f *fakeLife) Restart() (container.Outcome, error) {
	f.calls = append(f.calls, "restart")
	return container.Restarted, nil
}
func (f *fakeLife) State() (container.State, error) { return f.st, nil }

type fakeSk struct {
	list  []skills.Skill
	paths []skills.PathInfo
	err   error
	drift int
	log   *[]string
}

func (f fakeSk) rec(s string) error {
	if f.log != nil {
		*f.log = append(*f.log, s)
	}
	return f.err
}
func (f fakeSk) List() ([]skills.Skill, error)       { return f.list, f.err }
func (f fakeSk) Paths() ([]skills.PathInfo, error)   { return f.paths, f.err }
func (f fakeSk) Activate(n string) (string, error)   { return filepath.Base(n), f.rec("on:" + n) }
func (f fakeSk) Deactivate(n string) (string, error) { return filepath.Base(n), f.rec("off:" + n) }
func (f fakeSk) AddPath(p string) error              { return f.rec("add:" + p) }
func (f fakeSk) RemovePath(p string) error           { return f.rec("rm:" + p) }
func (f fakeSk) RenamePath(o, n string) error        { return f.rec("mv:" + o + ">" + n) }
func (f fakeSk) Drift() (int, error)                 { return f.drift, nil }

// coreHarness wires a Core with fakes into h.app.
func coreHarness(t *testing.T, base, runtime string) (*harness, *fakeLife) {
	t.Helper()
	h := newHarness(t, base, runtime)
	life := &fakeLife{}
	h.app.Core = &core.Core{
		Home: h.home, EnvFile: filepath.Join(h.home, ".env"),
		Profile: profile.Profile{Name: "default", Dir: h.home, User: "pi", UID: 1000},
		Life:    life, Skill: fakeSk{}, Images: stubImages{status: build.ImageCurrent},
	}
	return h, life
}

func TestStatusReportsEachFact(t *testing.T) {
	h, life := coreHarness(t, goodBase, "packages:\n  x: {script: echo x}\n")
	life.st = container.State{Exists: true, Running: true, Env: map[string]string{}}
	h.app.Core.Skill = fakeSk{drift: 1}
	if code := h.app.Run([]string{"status"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	row := rowOf(t, h.out.String(), "default")
	assertHasWords(t, row, "running", "current")
	if !strings.Contains(strings.Join(row, " "), "need a decision") || !strings.Contains(strings.Join(row, " "), "pending restart") {
		t.Errorf("drift and pending skills not reported: %q", row)
	}
}

func TestStatusSaysWhetherTheContainerIsRunningStoppedOrMissing(t *testing.T) {
	for _, c := range []struct {
		st   container.State
		want string
	}{
		{container.State{}, "not"},
		{container.State{Exists: true}, "stopped"},
		{container.State{Exists: true, Running: true, Env: map[string]string{}}, "running"},
	} {
		h, life := coreHarness(t, goodBase, "")
		life.st = c.st
		h.app.Run([]string{"status"})
		assertHasWords(t, rowOf(t, h.out.String(), "default"), c.want)
	}
}

func TestStatusProblemsGoToStderrAndFailTheCommand(t *testing.T) {
	h, _ := coreHarness(t, "packages: [bad", "")
	if code := h.app.Run([]string{"status"}); code != 1 || !strings.Contains(h.err.String(), "config") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if h.out.Len() == 0 {
		t.Error("the rest of the status was not printed")
	}
}

func TestSkillsListShowsNameStateAndPath(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	h.app.Core.Skill = fakeSk{list: []skills.Skill{
		{Name: "foo", Path: "/p/foo", Active: true, State: skills.Synced},
		{Name: "barbaz", Path: "/p/barbaz", State: skills.Inactive},
	}}
	if code := h.app.Run([]string{"skills", "list"}); code != 0 {
		t.Fatal(h.err)
	}
	assertHasWords(t, rowOf(t, h.out.String(), "foo"), "synced", "/p/foo")
	assertHasWords(t, rowOf(t, h.out.String(), "barbaz"), "inactive", "/p/barbaz")
}

func TestSkillsActivateAndDeactivate(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	var log []string
	h.app.Core.Skill = fakeSk{log: &log}
	h.app.Run([]string{"skills", "activate", "voice"})
	h.app.Run([]string{"skills", "deactivate", "/p/old"})
	if h.out.String() != "voice\nold\n" {
		t.Errorf("out %q", h.out)
	}
	if !reflect.DeepEqual(log, []string{"on:voice", "off:/p/old"}) {
		t.Errorf("calls %v", log)
	}
}

func TestSkillsPaths(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	var log []string
	h.app.Core.Skill = fakeSk{log: &log, paths: []skills.PathInfo{
		{Path: "~/.agents/skills", Found: true, Skills: 3}, {Path: "/gone"},
	}}
	if code := h.app.Run([]string{"skills", "paths", "list"}); code != 0 {
		t.Fatal(h.err)
	}
	assertHasWords(t, rowOf(t, h.out.String(), "~/.agents/skills"), "found", "3")
	assertHasWords(t, rowOf(t, h.out.String(), "/gone"), "missing", "0")
	h.out.Reset()
	h.app.Run([]string{"skills", "paths", "add", "/x"})
	h.app.Run([]string{"skills", "paths", "rm", "/y"})
	if h.out.String() != "/x\n/y\n" || !reflect.DeepEqual(log, []string{"add:/x", "rm:/y"}) {
		t.Errorf("out %q calls %v", h.out, log)
	}
}

func TestSkillsErrorsAndUsage(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	h.app.Core.Skill = fakeSk{err: errors.New("nope")}
	for _, args := range [][]string{
		{"skills", "list"}, {"skills", "activate", "x"}, {"skills", "deactivate", "x"},
		{"skills", "paths", "list"}, {"skills", "paths", "add", "/x"}, {"skills", "paths", "rm", "/x"},
	} {
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	for _, args := range [][]string{
		{"skills", "bogus"}, {"skills", "list", "x"},
		{"skills", "activate"}, {"skills", "activate", "a", "b"}, {"skills", "deactivate"},
		{"skills", "paths", "add"}, {"skills", "paths", "rm"}, {"skills", "paths", "add", "a", "b"},
		{"skills", "paths", "bogus"}, {"skills", "add", "/x"}, {"skills", "remove", "x"},
	} {
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}

func TestEnvCommands(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	for _, args := range [][]string{{"env", "set", "TOKEN", "abcdefgh1234"}, {"env", "set", "PLAIN=hello"}, {"env", "set", "EQ=a=b"}} {
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
	}
	h.out.Reset()
	h.app.Run([]string{"env", "list"})
	assertHasWords(t, rowOf(t, h.out.String(), "TOKEN"), "••••••••1234")
	if strings.Contains(h.out.String(), "hello") || strings.Contains(h.out.String(), "abcdefgh") {
		t.Errorf("list shows a secret:\n%s", h.out)
	}
	h.out.Reset()
	h.app.Run([]string{"env", "list", "--show"})
	assertHasWords(t, rowOf(t, h.out.String(), "TOKEN"), "abcdefgh1234")
	assertHasWords(t, rowOf(t, h.out.String(), "PLAIN"), "hello")
	assertHasWords(t, rowOf(t, h.out.String(), "EQ"), "a=b")
	h.out.Reset()
	if code := h.app.Run([]string{"env", "unset", "PLAIN"}); code != 0 || h.out.String() != "PLAIN\n" {
		t.Errorf("exit %d out %q", code, h.out)
	}
	if code := h.app.Run([]string{"env", "unset", "PLAIN"}); code != 1 || !strings.Contains(h.err.String(), "PLAIN") {
		t.Errorf("unset twice: exit %d err %q", code, h.err)
	}
}

func TestEnvNeverEchoesSecretsOnSet(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	h.app.Run([]string{"env", "set", "SECRET", "supersecretvalue"})
	if strings.Contains(h.out.String()+h.err.String(), "supersecretvalue") {
		t.Errorf("secret echoed: %q %q", h.out, h.err)
	}
	if h.out.String() != "SECRET\n" {
		t.Errorf("out %q", h.out)
	}
}

func TestEnvUsageAndErrors(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	for _, args := range [][]string{{"env", "bogus"}, {"env", "set"}, {"env", "set", "A"}, {"env", "set", "A", "b", "c"}, {"env", "unset"}, {"env", "list", "x"}} {
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
	if code := h.app.Run([]string{"env", "set", "1BAD", "x"}); code != 1 {
		t.Errorf("bad name: exit %d", code)
	}
}

func TestCoreCommandsWithoutACoreFailCleanly(t *testing.T) {
	h := newHarness(t, goodBase, "")
	for _, cmd := range []string{"status", "refresh", "skills", "env"} {
		args := []string{cmd}
		switch cmd {
		case "skills":
			args = append(args, "list")
		case "env":
			args = append(args, "list")
		}
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%s: exit %d", cmd, code)
		}
	}
}

func TestInteractiveFlagStartsTheTUI(t *testing.T) {
	for _, flag := range []string{"-i", "--interactive"} {
		h, _ := coreHarness(t, goodBase, "")
		ran := 0
		h.app.TUI = func() error { ran++; return nil }
		if code := h.app.Run([]string{flag}); code != 0 || ran != 1 {
			t.Errorf("%s: exit %d ran %d", flag, code, ran)
		}
	}
}

func TestInteractiveNeedsATerminalAndATUI(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	h.app.TUI = func() error { t.Error("ran without a terminal"); return nil }
	h.app.Interactive = false
	if code := h.app.Run([]string{"-i"}); code != 1 || !strings.Contains(h.err.String(), "terminal") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	h.app.Interactive = true
	h.app.TUI = nil
	if code := h.app.Run([]string{"-i"}); code != 1 {
		t.Errorf("no TUI: exit %d", code)
	}
	h.app.TUI = func() error { return errors.New("render broke") }
	if code := h.app.Run([]string{"-i"}); code != 1 || !strings.Contains(h.err.String(), "render broke") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if code := h.app.Run([]string{"-i", "extra"}); code != 1 {
		t.Errorf("extra arg: exit %d", code)
	}
}

type stubImages struct {
	status build.ImageStatus
}

func (s stubImages) Plan([]build.ProfileConfig) (build.Plan, error) {
	return build.Plan{Hash: "h"}, nil
}
func (s stubImages) Build(build.Plan, bool, io.Writer) error  { return nil }
func (s stubImages) Status(string) (build.ImageStatus, error) { return s.status, nil }

var _ = session.Info{}

func TestEnvSetWarnsOnACredentialNameOnly(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	h.app.Run([]string{"env", "set", "API_TOKEN", "x"})
	if !strings.Contains(h.err.String(), "tau proxies env set") {
		t.Errorf("no warning: %q", h.err)
	}
	h.err.Reset()
	h.app.Run([]string{"env", "set", "EDITOR", "vim"})
	if h.err.Len() != 0 {
		t.Errorf("warned about a plain name: %q", h.err)
	}
}
