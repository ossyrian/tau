package session

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fake emulates `docker` for the calls Manager makes.
type fake struct {
	running  bool
	sessions string // tmux list-sessions output; "" means none
	hasErr   error  // result of has-session
	preview  string // output of the capture-pane script
	calls    [][]string
	attached [][]string
	failOn   string // tmux subcommand that fails
}

var tmuxPrefix = []string{"exec", "-u", "pi", "tau", "tmux", "-L", "tau", "-f", "/etc/tau/tmux.conf"}

func (f *fake) Output(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if len(args) >= 2 && args[0] == "container" && args[1] == "inspect" {
		if f.running {
			return "true\n", nil
		}
		return "false\n", nil
	}
	if len(args) > 5 && args[4] == "sh" && strings.Contains(strings.Join(args, " "), "capture-pane") {
		return f.preview, nil
	}
	sub := args[len(tmuxPrefix)]
	if sub == f.failOn {
		return "", errors.New("tmux " + sub + " failed")
	}
	switch sub {
	case "list-sessions":
		if f.sessions == "" {
			return "", errors.New("no server running")
		}
		return f.sessions, nil
	case "has-session":
		return "", f.hasErr
	}
	return "", nil
}

func (f *fake) Run(args ...string) error {
	f.attached = append(f.attached, args)
	return nil
}

func (f *fake) tmux(sub ...string) []string { return append(append([]string{}, tmuxPrefix...), sub...) }

func (f *fake) did(args []string) bool {
	for _, c := range f.calls {
		if reflect.DeepEqual(c, args) {
			return true
		}
	}
	return false
}

func newMgr(f *fake) (*Manager, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return &Manager{
		D: f, Container: "tau", Profile: "default", User: "pi", PiCmd: "pi",
		Rand: func() string { return "abcd1234" },
		Loc:  time.UTC,
	}, out
}

const twoSessions = "main|1700000000|1700003600\nsa-worker|1700000100|1700000200\n"

func TestEveryCommandNeedsARunningContainer(t *testing.T) {
	cmds := map[string]func(*Manager) error{
		"create": func(m *Manager) error { _, err := m.Create(""); return err },
		"list":   func(m *Manager) error { _, err := m.Sessions(); return err },
		"kill":   func(m *Manager) error { _, err := m.Kill("x"); return err },
		"attach": func(m *Manager) error { return m.Attach("x") },
	}
	for name, run := range cmds {
		f := &fake{running: false, sessions: twoSessions}
		m, _ := newMgr(f)
		err := run(m)
		if err == nil || !strings.Contains(err.Error(), "tau start") {
			t.Errorf("%s: %v", name, err)
		}
		if len(f.attached) != 0 {
			t.Errorf("%s: acted on a stopped container", name)
		}
	}
}

func TestAttachNewAttachesToTheNamedSessionExactly(t *testing.T) {
	f := &fake{running: true}
	m, _ := newMgr(f)
	if err := m.AttachNew("work"); err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "-it", "-u", "pi", "tau", "tmux", "-L", "tau", "-f", "/etc/tau/tmux.conf", "attach", "-t", "=work"}
	if len(f.attached) != 1 || !reflect.DeepEqual(f.attached[0], want) {
		t.Errorf("attach: %v", f.attached)
	}
}

func TestCreateChecksExistenceWithAnExactMatch(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("no")}
	m, _ := newMgr(f)
	m.Create("work")
	if !f.did(f.tmux("has-session", "-t", "=work")) {
		t.Errorf("must use = for an exact match: %v", f.calls)
	}
}

func TestCreateRefusesAnExistingName(t *testing.T) {
	f := &fake{running: true, hasErr: nil}
	m, _ := newMgr(f)
	_, err := m.Create("work")
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("got %v", err)
	}
	if f.did(f.tmux("new-session", "-d", "-s", "work", "pi")) {
		t.Error("created anyway")
	}
}

func TestCreateRejectsBadNamesBeforeCredentialsOrTmux(t *testing.T) {
	for _, n := range []string{"a.b", "a:b", "a b", "a/b", "é", "a\n", "x;y"} {
		f := &fake{running: true, hasErr: errors.New("no")}
		m, _ := newMgr(f)
		if _, err := m.Create(n); err == nil || !strings.Contains(err.Error(), "[a-zA-Z0-9_-]") {
			t.Errorf("%q: %v", n, err)
		}
		if len(f.calls) != 1 { // only the running check
			t.Errorf("%q: went past validation: calls=%v", n, f.calls)
		}
	}
	for _, ok := range []string{"a", "A_b-9", "sa-1", "-lead"} {
		f := &fake{running: true, hasErr: errors.New("no")}
		m, _ := newMgr(f)
		if _, err := m.Create(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestCreateReportsTmuxFailure(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("no"), failOn: "new-session"}
	m, _ := newMgr(f)
	if name, err := m.Create("x"); err == nil || name != "" {
		t.Fatalf("want error, got %q %v", name, err)
	}
}

func TestKillResolvesThenKillsExactly(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions}
	m, _ := newMgr(f)
	got, err := m.Kill("sa-w")
	if err != nil {
		t.Fatal(err)
	}
	if !f.did(f.tmux("kill-session", "-t", "=sa-worker")) || got != "sa-worker" {
		t.Errorf("calls %v killed %q", f.calls, got)
	}
}

func TestKillReportsTmuxFailureWithoutClaimingSuccess(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions, failOn: "kill-session"}
	m, _ := newMgr(f)
	if got, err := m.Kill("main"); err == nil || got != "" {
		t.Errorf("err %v killed %q", err, got)
	}
}

func TestAttachResolvesAndAttaches(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions}
	m, _ := newMgr(f)
	if err := m.Attach("ma"); err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "-it", "-u", "pi", "tau", "tmux", "-L", "tau", "-f", "/etc/tau/tmux.conf", "attach", "-t", "=main"}
	if len(f.attached) != 1 || !reflect.DeepEqual(f.attached[0], want) {
		t.Errorf("attached %v", f.attached)
	}
}

func TestResolve(t *testing.T) {
	f := &fake{running: true, sessions: "work|1|1\nwork2|1|1\nother|1|1\n"}
	m, _ := newMgr(f)
	cases := []struct{ q, want, err string }{
		{"work", "work", ""}, // exact wins over the longer prefix match
		{"work2", "work2", ""},
		{"oth", "other", ""}, // unique prefix
		{"wor", "", "ambiguous"},
		{"nope", "", "no session matching 'nope'"},
		{"", "", "ambiguous"},              // empty prefix matches everything
		{"o.*", "", "no session matching"}, // prefix is literal, not a regex
	}
	for _, c := range cases {
		got, err := m.resolve(c.q)
		switch {
		case c.err == "" && (err != nil || got != c.want):
			t.Errorf("%q: got %q, %v", c.q, got, err)
		case c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)):
			t.Errorf("%q: got %q, %v", c.q, got, err)
		}
	}
}

func TestResolveWithNoSessionsSaysSo(t *testing.T) {
	m, _ := newMgr(&fake{running: true})
	if _, err := m.resolve("x"); err == nil || !strings.Contains(err.Error(), "no active sessions") {
		t.Errorf("got %v", err)
	}
}

func TestKillAndAttachRejectAnEmptyName(t *testing.T) {
	f := &fake{running: true, sessions: "only|1|1\n"}
	m, _ := newMgr(f)
	if _, err := m.Kill(""); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("kill: %v", err)
	}
	if err := m.Attach(""); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("attach: %v", err)
	}
	if f.did(f.tmux("kill-session", "-t", "=only")) || len(f.attached) != 0 {
		t.Error("an empty name matched the only session")
	}
}

func TestRandomNameIsEightLowercaseHex(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		n := RandomName()
		if len(n) != 8 || strings.Trim(n, "0123456789abcdef") != "" {
			t.Fatalf("bad name %q", n)
		}
		seen[n] = true
	}
	if len(seen) < 15 {
		t.Error("names are not random")
	}
}

func TestSessionsNeedsARunningContainerAndAllowsNone(t *testing.T) {
	m, _ := newMgr(&fake{running: false})
	if _, err := m.Sessions(); err == nil || !strings.Contains(err.Error(), "tau start") {
		t.Errorf("got %v", err)
	}
	m, _ = newMgr(&fake{running: true})
	if got, err := m.Sessions(); err != nil || len(got) != 0 {
		t.Errorf("got %v %v", got, err)
	}
}

func TestAttachArgsResolvesAndDoesNotAttach(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions}
	m, _ := newMgr(f)
	args, err := m.AttachArgs("sa-")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "-it", "-u", "pi", "tau", "tmux", "-L", "tau", "-f", "/etc/tau/tmux.conf", "attach", "-t", "=sa-worker"}
	if !reflect.DeepEqual(args, want) || len(f.attached) != 0 {
		t.Errorf("args %v attached %v", args, f.attached)
	}
}

func TestAttachArgsErrors(t *testing.T) {
	m, _ := newMgr(&fake{running: false})
	if _, err := m.AttachArgs("x"); err == nil {
		t.Error("stopped container accepted")
	}
	m, _ = newMgr(&fake{running: true, sessions: twoSessions})
	if _, err := m.AttachArgs("zzz"); err == nil {
		t.Error("unknown session accepted")
	}
}

func TestCreateMakesTheSessionWithoutAttaching(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("no")}
	m, out := newMgr(f)
	name, err := m.Create("work")
	if err != nil || name != "work" {
		t.Fatalf("name %q err %v", name, err)
	}
	if !f.did(f.tmux("new-session", "-d", "-s", "work", "pi")) || len(f.attached) != 0 || out.Len() != 0 {
		t.Errorf("calls %v attached %v out %q", f.calls, f.attached, out)
	}
}

func TestCreateValidatesAndPicksARandomName(t *testing.T) {
	m, _ := newMgr(&fake{running: true, hasErr: errors.New("no")})
	if name, err := m.Create(""); err != nil || name != "abcd1234" {
		t.Errorf("name %q err %v", name, err)
	}
	if _, err := m.Create("a.b"); err == nil {
		t.Error("bad name accepted")
	}
	m, _ = newMgr(&fake{running: true})
	if _, err := m.Create("dup"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("err %v", err)
	}
}

func TestPreviewReadsTheTailOfEachSessionWithOneExec(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions, preview: "@@@main\nline a\nline b\n\n@@@sa-worker\n\nworking...\n\n\n"}
	m, _ := newMgr(f)
	got, err := m.Preview(2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{"main": {"line a", "line b"}, "sa-worker": {"working..."}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v", got)
	}
	n := 0
	for _, c := range f.calls {
		if len(c) > 3 && c[0] == "exec" && c[len(c)-1] != "" && strings.Contains(strings.Join(c, " "), "capture-pane") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d capture execs: %v", n, f.calls)
	}
}

func TestPreviewKeepsOnlyTheLastNLines(t *testing.T) {
	f := &fake{running: true, sessions: "main|1|2\n", preview: "@@@main\n1\n2\n3\n4\n"}
	m, _ := newMgr(f)
	got, _ := m.Preview(2)
	if !reflect.DeepEqual(got["main"], []string{"3", "4"}) {
		t.Errorf("got %v", got)
	}
}

func TestPreviewPassesNamesAsArgumentsNotShellText(t *testing.T) {
	f := &fake{running: true, sessions: "we ird;x|1|2\n", preview: ""}
	m, _ := newMgr(f)
	m.Preview(3)
	for _, c := range f.calls {
		if strings.Contains(strings.Join(c, " "), "capture-pane") {
			script := c[len(c)-3] // the sh -c script comes before "sh" and the names
			if strings.Contains(script, "we ird") {
				t.Errorf("session name interpolated into the script: %q", script)
			}
			if c[len(c)-1] != "we ird;x" {
				t.Errorf("name not passed as an argument: %v", c)
			}
		}
	}
}

func TestPreviewWithNoSessions(t *testing.T) {
	m, _ := newMgr(&fake{running: true})
	got, err := m.Preview(3)
	if err != nil || len(got) != 0 {
		t.Errorf("got %v %v", got, err)
	}
}

func TestSessionsSkipsMalformedRows(t *testing.T) {
	f := &fake{running: true, sessions: "ok|1700000000|1700000000\ngarbage\nbad|x|y\n"}
	m, _ := newMgr(f)
	got, err := m.Sessions()
	if err != nil || len(got) != 1 || got[0].Name != "ok" {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestSubagentKindComesFromTheSaPrefixOnly(t *testing.T) {
	f := &fake{running: true, sessions: "sa-1|1700000000|1700000000\nsa|1700000000|1700000000\nxsa-1|1700000000|1700000000\n"}
	m, _ := newMgr(f)
	got, _ := m.Sessions()
	want := map[string]string{"sa-1": "subagent", "sa": "session", "xsa-1": "session"}
	if len(got) != 3 {
		t.Fatalf("got %+v", got)
	}
	for _, i := range got {
		if i.Kind != want[i.Name] {
			t.Errorf("%s typed %s want %s", i.Name, i.Kind, want[i.Name])
		}
	}
}

func TestACommandRunsAsTheProfilesUser(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions}
	m, _ := newMgr(f)
	m.Profile, m.User = "work", "p-work"
	if _, err := m.Sessions(); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	if !reflect.DeepEqual(last[:4], []string{"exec", "-u", "p-work", "tau"}) {
		t.Errorf("got %v", last)
	}
	args, err := m.AttachArgs("main")
	if err != nil || !reflect.DeepEqual(args[:5], []string{"exec", "-it", "-u", "p-work", "tau"}) {
		t.Errorf("attach: %v %v", args, err)
	}
}

func TestSessionsCarryTheirProfile(t *testing.T) {
	f := &fake{running: true, sessions: twoSessions}
	m, _ := newMgr(f)
	m.Profile, m.User = "work", "p-work"
	infos, _ := m.Sessions()
	if len(infos) == 0 {
		t.Fatal("no sessions")
	}
	for _, i := range infos {
		if i.Profile != "work" {
			t.Errorf("%s: profile %q", i.Name, i.Profile)
		}
	}
}

func TestASessionStartsInTheProfilesWorkspace(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("none")}
	m, _ := newMgr(f)
	m.Workspace = "/home/p-work/workspace"
	if _, err := m.Create("w"); err != nil {
		t.Fatal(err)
	}
	want := f.tmux("new-session", "-d", "-s", "w", "-c", "/home/p-work/workspace", "-e", "TAU_WORKSPACE=/home/p-work/workspace", "pi")
	if !f.did(want) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestASessionGetsTheProfilesVariablesInOrder(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("none")}
	m, _ := newMgr(f)
	m.Env = func() ([]string, error) { return []string{"A=1", "B=two words"}, nil }
	if _, err := m.Create("w"); err != nil {
		t.Fatal(err)
	}
	if !f.did(f.tmux("new-session", "-d", "-s", "w", "-e", "A=1", "-e", "B=two words", "pi")) {
		t.Errorf("calls: %v", f.calls)
	}
}

func TestAnEnvErrorStopsTheSession(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("none")}
	m, _ := newMgr(f)
	m.Env = func() ([]string, error) { return nil, errors.New("bad .env") }
	if _, err := m.Create("w"); err == nil || !strings.Contains(err.Error(), "bad .env") {
		t.Errorf("got %v", err)
	}
	for _, c := range f.calls {
		if len(c) > len(tmuxPrefix) && c[len(tmuxPrefix)] == "new-session" {
			t.Error("a session was started with a broken .env")
		}
	}
}

func TestASessionsPathHasTheShimsFirstAndItsOwnHome(t *testing.T) {
	f := &fake{running: true, hasErr: errors.New("none")}
	m, _ := newMgr(f)
	m.Home = "/home/p-work"
	if _, err := m.Create("w"); err != nil {
		t.Fatal(err)
	}
	// tmux ignores -e PATH, so the path is set by the command that tmux runs.
	var cmd string
	for _, c := range f.calls {
		if len(c) > len(tmuxPrefix) && c[len(tmuxPrefix)] == "new-session" {
			cmd = c[len(c)-1]
		}
	}
	want := "umask 002 && exec env PATH='/opt/tau/shims:/home/p-work/.local/bin:/home/p-work/scripts:"
	if !strings.HasPrefix(cmd, want) || !strings.HasSuffix(cmd, "' pi") {
		t.Errorf("got %q", cmd)
	}
	if strings.Contains(cmd, "/home/pi") {
		t.Errorf("another profile's home is on the PATH: %q", cmd)
	}
}

func TestShellArgsOpenZshAsTheProfileUserWithTheSessionEnvironment(t *testing.T) {
	f := &fake{running: true}
	m, _ := newMgr(f)
	m.Home = "/home/pi"
	m.Workspace = "/workspace"
	m.Env = func() ([]string, error) { return []string{"A=1"}, nil }
	got, err := m.ShellArgs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"exec", "-it", "-e", "A=1", "-e", "PATH=" + Path("/home/pi"), "-w", "/workspace", "-e", "TAU_WORKSPACE=/workspace",
		"-u", "pi", "tau", "sh", "-c", "umask 002 && exec zsh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestShellRunsOnTheTerminalAndNeedsARunningContainer(t *testing.T) {
	f := &fake{running: true}
	m, _ := newMgr(f)
	if err := m.Shell(); err != nil || len(f.attached) != 1 {
		t.Fatalf("err %v attached %v", err, f.attached)
	}
	stopped := &fake{running: false}
	m2, _ := newMgr(stopped)
	if err := m2.Shell(); err == nil || !strings.Contains(err.Error(), "tau start") || len(stopped.attached) != 0 {
		t.Errorf("err %v attached %v", err, stopped.attached)
	}
}

func TestShellFailsWhenTheEnvironmentCannotBeRead(t *testing.T) {
	m, _ := newMgr(&fake{running: true})
	m.Env = func() ([]string, error) { return nil, errors.New("bad env file") }
	if _, err := m.ShellArgs(); err == nil || !strings.Contains(err.Error(), "bad env file") {
		t.Errorf("got %v", err)
	}
}
