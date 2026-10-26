package container

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeDocker struct {
	exists, running bool
	calls           [][]string
	failOn          string // first arg of the call that fails, e.g. "run"
	order           []string
}

func (f *fakeDocker) Output(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	f.order = append(f.order, args[0])
	if args[0] == f.failOn {
		return "", errors.New(args[0] + " failed")
	}
	switch args[0] {
	case "container":
		if len(args) > 1 && args[1] == "inspect" && len(args) > 2 && args[2] == "-f" {
			if f.running {
				return "true\n", nil
			}
			return "false\n", nil
		}
		if !f.exists {
			return "", errors.New("no such container")
		}
	case "rm":
		f.exists, f.running = false, false
	case "run":
		f.exists, f.running = true, true
	}
	return "", nil
}

func (f *fakeDocker) did(first string) [][]string {
	var out [][]string
	for _, c := range f.calls {
		if c[0] == first {
			out = append(out, c)
		}
	}
	return out
}

func newLC(t *testing.T, f *fakeDocker) *Lifecycle {
	t.Helper()
	return &Lifecycle{D: f, Spec: baseSpec(t), Repo: t.TempDir(), Stderr: &strings.Builder{}, Tracked: noTracked}
}

func TestStartWhenRunningDoesNothing(t *testing.T) {
	f := &fakeDocker{exists: true, running: true}
	lc := newLC(t, f)
	got, err := lc.Start()
	if err != nil {
		t.Fatal(err)
	}
	if got != AlreadyRunning || len(f.did("run")) != 0 || len(f.did("rm")) != 0 {
		t.Errorf("outcome %q calls %v", got, f.calls)
	}
}

func TestStartCreatesTheHostDirectoriesBeforeDockerRun(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	if _, err := lc.Start(); err != nil {
		t.Fatal(err)
	}
	m := lc.Spec.Profiles[0]
	for _, d := range []string{m.Workspace, m.Profile.Scripts(), m.Profile.Share(), m.Profile.Treehouse()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("%s missing", d)
		}
	}
}

func TestStartReportsDockerRunFailureWithNoOutcome(t *testing.T) {
	f := &fakeDocker{failOn: "run"}
	lc := newLC(t, f)
	got, err := lc.Start()
	if err == nil || !strings.Contains(err.Error(), "run failed") {
		t.Fatalf("got %v", err)
	}
	if got != "" || len(f.did("exec")) != 0 {
		t.Errorf("went on after failure: outcome %q", got)
	}
}

func TestStartPrepareFailureStopsBeforeDocker(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	lc.Tracked = func(string) ([]string, error) { return nil, errors.New("git broke") }
	// Tracked is only called when the repo has a pi-config dir.
	write(t, filepath.Join(lc.Repo, "pi-config", "x"), "x")
	if _, err := lc.Start(); err == nil || !strings.Contains(err.Error(), "git broke") {
		t.Fatalf("got %v", err)
	}
	if len(f.did("run")) != 0 {
		t.Error("ran docker after a prepare failure")
	}
}

func TestStopRemovesAStoppedContainerToo(t *testing.T) {
	f := &fakeDocker{exists: true, running: false}
	lc := newLC(t, f)
	got, _ := lc.Stop()
	if len(f.did("rm")) != 1 || got != Stopped {
		t.Errorf("outcome %q", got)
	}
}

func TestStopWithNoContainer(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	got, err := lc.Stop()
	if err != nil || got != NotRunning || len(f.did("rm")) != 0 {
		t.Errorf("err %v outcome %q", err, got)
	}
}

func TestStopReportsRemoveFailure(t *testing.T) {
	f := &fakeDocker{exists: true, failOn: "rm"}
	lc := newLC(t, f)
	if got, err := lc.Stop(); err == nil || got != "" {
		t.Errorf("err %v outcome %q", err, got)
	}
}

func TestRestartRecreatesARunningContainer(t *testing.T) {
	f := &fakeDocker{exists: true, running: true}
	lc := newLC(t, f)
	got, err := lc.Restart()
	if err != nil {
		t.Fatal(err)
	}
	if got != Restarted || len(f.did("run")) != 1 || len(f.did("rm")) != 1 {
		t.Errorf("outcome %q calls %v", got, f.calls)
	}
}

func TestRestartIsANoopWhenNotRunning(t *testing.T) {
	for _, exists := range []bool{false, true} {
		f := &fakeDocker{exists: exists, running: false}
		lc := newLC(t, f)
		got, err := lc.Restart()
		if err != nil {
			t.Fatal(err)
		}
		if got != NotRunning || len(f.did("run")) != 0 || len(f.did("rm")) != 0 {
			t.Errorf("exists=%v outcome %q calls %v", exists, got, f.calls)
		}
	}
}

func TestOutcomeTextIsForPeopleAndTheTokenIsForScripts(t *testing.T) {
	for o, want := range map[Outcome][2]string{
		Started: {"started", ""}, AlreadyRunning: {"already running", ""},
		Stopped: {"stopped", "sessions lost"}, NotRunning: {"not running", ""},
		Restarted: {"restarted", "previous sessions lost"},
	} {
		if text, hint := o.Text(); text != want[0] || hint != want[1] {
			t.Errorf("%s: %q %q", o, text, hint)
		}
		if strings.ContainsAny(string(o), " \t\n") {
			t.Errorf("token %q has whitespace", o)
		}
	}
}

func TestStartMountsTheResolvedSkillsAndPrintsWarnings(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	stderr := &strings.Builder{}
	lc.Stderr = stderr
	lc.Skills = func(string) ([]SkillMount, []string, error) {
		return []SkillMount{{"voice", "/h/voice"}}, []string{"active skill not found, skipping: ghost"}, nil
	}
	if _, err := lc.Start(); err != nil {
		t.Fatal(err)
	}
	run := f.did("run")[0]
	if !has(run, "-v", "/h/voice:/home/pi/.pi/agent/skills/voice") {
		t.Errorf("skill not mounted: %v", run)
	}
	if !strings.Contains(stderr.String(), "ghost") {
		t.Errorf("warning lost: %q", stderr)
	}
}

func TestStartWithBrokenSkillsConfigKeepsTheRunningContainer(t *testing.T) {
	f := &fakeDocker{running: true, exists: true}
	lc := newLC(t, f)
	lc.Skills = func(string) ([]SkillMount, []string, error) { return nil, nil, errors.New("config.yaml:3: bad") }
	_, err := lc.Restart()
	if err == nil || !strings.Contains(err.Error(), "config.yaml:3") {
		t.Fatalf("err %v", err)
	}
	if len(f.did("rm")) != 0 || len(f.did("run")) != 0 {
		t.Errorf("a broken config touched the container: %v", f.order)
	}
}

func TestEachProfileGetsItsOwnSkills(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	w := prof(t, lc.Spec.Home, "work")
	lc.Spec.Profiles = append(lc.Spec.Profiles, Member{Profile: w, Workspace: w.Workspace(), WorkspaceDest: w.Home() + "/workspace"})
	asked := map[string]int{}
	lc.Skills = func(p string) ([]SkillMount, []string, error) {
		asked[p]++
		return []SkillMount{{p + "-skill", "/h/" + p}}, nil, nil
	}
	if _, err := lc.Start(); err != nil {
		t.Fatal(err)
	}
	run := f.did("run")[0]
	if !has(run, "-v", "/h/default:/home/pi/.pi/agent/skills/default-skill") ||
		!has(run, "-v", "/h/work:/home/p-work/.pi/agent/skills/work-skill") {
		t.Errorf("skills: %v", run)
	}
	if asked["default"] != 1 || asked["work"] != 1 {
		t.Errorf("asked %v", asked)
	}
	if len(lc.Spec.Profiles[0].Skills) != 0 {
		t.Error("the spec held by the lifecycle was changed")
	}
}

func TestABrokenSkillsConfigNamesTheProfile(t *testing.T) {
	f := &fakeDocker{running: true, exists: true}
	lc := newLC(t, f)
	lc.Skills = func(string) ([]SkillMount, []string, error) { return nil, nil, errors.New("bad") }
	if _, err := lc.Restart(); err == nil || !strings.Contains(err.Error(), "default") {
		t.Errorf("got %v", err)
	}
}

func TestMembersAreReadAtEachStart(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	w := prof(t, lc.Spec.Home, "work")
	calls := 0
	lc.Members = func() ([]Member, error) {
		calls++
		return []Member{lc.Spec.Profiles[0], {Profile: w, Workspace: w.Workspace(), WorkspaceDest: w.Home() + "/workspace"}}, nil
	}
	if _, err := lc.Start(); err != nil {
		t.Fatal(err)
	}
	if !has(f.did("run")[0], "-v", w.PiDir()+":/home/p-work/.pi") || calls != 1 {
		t.Errorf("members not used: calls=%d %v", calls, f.did("run"))
	}
	if st, err := os.Stat(w.PiDir()); err != nil || !st.IsDir() {
		t.Error("the new profile's directories were not prepared")
	}
}

func TestAMembersErrorStopsTheStartAndKeepsTheContainer(t *testing.T) {
	f := &fakeDocker{running: true, exists: true}
	lc := newLC(t, f)
	lc.Members = func() ([]Member, error) { return nil, errors.New("profiles clash") }
	if _, err := lc.Restart(); err == nil || !strings.Contains(err.Error(), "profiles clash") {
		t.Errorf("got %v", err)
	}
	if len(f.did("rm")) != 0 {
		t.Error("removed the container")
	}
}

func TestAFailedAfterIsReportedButTheContainerStays(t *testing.T) {
	f := &fakeDocker{}
	lc := newLC(t, f)
	lc.After = func() error { return errors.New("sso expired") }
	got, err := lc.Start()
	if err == nil || !strings.Contains(err.Error(), "sso expired") || !strings.Contains(err.Error(), "tau proxy reload") {
		t.Fatalf("got %v", err)
	}
	if got == Started {
		t.Error("claimed a clean start")
	}
	if len(f.did("rm")) > 0 && f.did("rm")[len(f.did("rm"))-1][0] == "rm" && !f.running {
		t.Error("removed the container")
	}
}
