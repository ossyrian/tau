package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/workspace"
)

func TestTheRealAppIsFullyWired(t *testing.T) {
	t.Setenv("TAU_HOME", t.TempDir())
	app := newApp()
	for name, ok := range map[string]bool{
		"Stdout": app.Stdout != nil, "Stderr": app.Stderr != nil, "Env": app.Env != nil,
		"Docker": app.Docker != nil, "Resolve": app.Resolve != nil, "TUI": app.TUI != nil,
		"Sessions": app.Sessions != nil, "Lifecycle": app.Lifecycle != nil, "Core": app.Core != nil,
	} {
		if !ok {
			t.Errorf("%s is not wired", name)
		}
	}
}

func TestRepoDirPrefersTauRepo(t *testing.T) {
	t.Setenv("TAU_REPO", "/somewhere")
	if got := repoDir(); got != "/somewhere" {
		t.Errorf("got %q", got)
	}
}

func TestGitTrackedListsOnlyTrackedFiles(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"-c", "user.email=a@b", "-c", "user.name=a", "commit", "-q", "--allow-empty", "-m", "x"}} {
		if out, err := commandIn(dir, "git", args...); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	write(t, dir+"/tracked.txt", "x")
	write(t, dir+"/untracked.txt", "y")
	if out, err := commandIn(dir, "git", "add", "tracked.txt"); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	got, err := gitTracked(dir)
	if err != nil || len(got) != 1 || got[0] != "tracked.txt" {
		t.Errorf("got %v err %v", got, err)
	}
	if _, err := gitTracked(t.TempDir()); err == nil {
		t.Error("want an error outside a repository")
	}
}

func TestExecuteExitsOneWhenACommandFails(t *testing.T) {
	t.Setenv("TAU_HOME", t.TempDir())
	var code = -1
	exit = func(c int) { code = c }
	defer func() { exit = os.Exit }()
	oldArgs := os.Args
	os.Args = []string{"tau", "bogus-command"}
	defer func() { os.Args = oldArgs }()
	Execute()
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
}

func profileIn(t *testing.T, root, name, config string) profile.Profile {
	t.Helper()
	p, err := profile.Set{Root: root}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(p.Dir, 0o755)
	if config != "" {
		write(t, p.ConfigFile(), config)
	}
	return p
}

func TestTheDefaultProfileOwnsTheSharedWorkspace(t *testing.T) {
	root := t.TempDir()
	p := profileIn(t, root, "default", "")
	host, dest, err := workspaceOf(p)
	if err != nil || host != p.Workspace() || dest != container.SharedWorkspace {
		t.Errorf("got %q %q %v", host, dest, err)
	}
}

func TestAnIsolatedProfileHasItsOwnWorkspaceUnderItsHome(t *testing.T) {
	root := t.TempDir()
	for _, cfg := range []string{"", "workspaces:\n  mode: isolated\n"} {
		p := profileIn(t, root, "work", cfg)
		host, dest, err := workspaceOf(p)
		if err != nil || host != p.Workspace() || dest != "/home/p-work/workspace" {
			t.Errorf("%q: got %q %q %v", cfg, host, dest, err)
		}
		if got := sessionDir(p); got != "/home/p-work/workspace" {
			t.Errorf("%q: sessions start in %q", cfg, got)
		}
	}
}

func TestASharedProfileMountsNothingAndUsesTheSharedWorkspace(t *testing.T) {
	p := profileIn(t, t.TempDir(), "work", "workspaces:\n  mode: shared\n")
	host, dest, err := workspaceOf(p)
	if err != nil || host != "" || dest != container.SharedWorkspace {
		t.Errorf("got %q %q %v", host, dest, err)
	}
	if got := sessionDir(p); got != "/workspace" {
		t.Errorf("sessions start in %q", got)
	}
}

func TestABrokenWorkspacesSectionIsAnErrorNotASilentDefault(t *testing.T) {
	p := profileIn(t, t.TempDir(), "work", "workspaces:\n  mode: sometimes\n")
	if _, _, err := workspaceOf(p); err == nil {
		t.Error("a bad mode was accepted")
	}
	if got := sessionDir(p); got != "/workspace" {
		t.Errorf("fallback %q", got)
	}
}

func TestMembersListEveryProfileWithItsWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TAU_HOME", root)
	profileIn(t, root, "default", "")
	profileIn(t, root, "work", "workspaces:\n  mode: shared\n")
	profileIn(t, root, "play", "")
	ms, err := newEnvironment().members()
	if err != nil || len(ms) != 3 {
		t.Fatalf("got %v %v", ms, err)
	}
	want := map[string]string{"default": container.SharedWorkspace, "play": "/home/p-play/workspace", "work": container.SharedWorkspace}
	for _, m := range ms {
		if m.WorkspaceDest != want[m.Profile.Name] {
			t.Errorf("%s: dest %q", m.Profile.Name, m.WorkspaceDest)
		}
	}
	if ms[0].Profile.Name != "default" {
		t.Error("the default profile is not first")
	}
}

func TestBuildTagDefaultsToTAUIMAGE(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Env = func(k string) string {
		return map[string]string{"TAU_HOME": h.home, "TAU_REPO": h.repo, "TAU_IMAGE": "tau:test"}[k]
	}
	h.app.Run([]string{"build"})
	if len(h.docker) != 1 || !has(h.docker[0], "-t", "tau:test") {
		t.Errorf("docker %v", h.docker)
	}
}

func has(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		if strings.Join(args[i:i+len(seq)], "\x00") == strings.Join(seq, "\x00") {
			return true
		}
	}
	return false
}

func TestMembersCarryEachProfilesLiveWorkspaces(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TAU_HOME", root)
	profileIn(t, root, "default", "workspaces:\n  live:\n    proj: /src/proj\n")
	profileIn(t, root, "play", "workspaces:\n  live:\n    other: ~/dev/other\n")
	ms, err := newEnvironment().members()
	if err != nil {
		t.Fatal(err)
	}
	live := map[string]map[string]string{}
	for _, m := range ms {
		live[m.Profile.Name] = m.Live
	}
	if live["default"]["proj"] != "/src/proj" || live["play"]["other"] != "~/dev/other" || len(live["play"]) != 1 {
		t.Errorf("got %v", live)
	}
}

func TestASharedProfilesLiveWorkspaceMountsUnderTheSharedWorkspace(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TAU_HOME", root)
	profileIn(t, root, "default", "")
	profileIn(t, root, "work", "workspaces:\n  mode: shared\n  live:\n    mine: /src/mine\n")
	ms, _ := newEnvironment().members()
	for _, m := range ms {
		if m.Profile.Name == "work" && (m.WorkspaceDest != container.SharedWorkspace || m.Live["mine"] != "/src/mine") {
			t.Errorf("work: %+v", m)
		}
	}
}

func wsStoreOf(t *testing.T, name string) *workspace.Store {
	t.Helper()
	p, err := newEnvironment().set.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := newEnvironment().core(p).Workspace.(*workspace.Store)
	if !ok {
		t.Fatalf("%s: no workspace store", name)
	}
	return s
}

func TestEachProfilesWorkspaceStoreWorksOnItsOwnDirectoryAndMount(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TAU_HOME", root)
	profileIn(t, root, "default", "")
	profileIn(t, root, "work", "workspaces:\n  mode: shared\n")
	profileIn(t, root, "play", "")

	def := wsStoreOf(t, "default")
	if def.Root != filepath.Join(root, "workspace") || def.Mount != "/workspace" || def.Dir != root {
		t.Errorf("default: %+v", def)
	}
	// A shared profile edits the default's files, and its config is its own.
	work := wsStoreOf(t, "work")
	if work.Root != filepath.Join(root, "workspace") || work.Mount != "/workspace" || work.Dir != filepath.Join(root, "profiles", "work") {
		t.Errorf("shared profile: %+v", work)
	}
	// An isolated profile has its own directory, mounted in its home.
	play := wsStoreOf(t, "play")
	if play.Root != filepath.Join(root, "profiles", "play", "workspace") || play.Mount != "/home/p-play/workspace" || play.Dir != filepath.Join(root, "profiles", "play") {
		t.Errorf("isolated profile: %+v", play)
	}
	if def.Clone == nil || def.Merge == nil {
		t.Error("the store cannot copy")
	}
}
