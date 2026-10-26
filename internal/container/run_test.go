package container

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/profile"
)

func prof(t *testing.T, root, name string) profile.Profile {
	t.Helper()
	p, err := profile.Set{Root: root}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// baseSpec is a container with only the default profile, which owns the
// shared workspace.
func baseSpec(t *testing.T) Spec {
	t.Helper()
	h := t.TempDir()
	d := prof(t, h, "default")
	return Spec{
		Name: "tau", Image: "tau:latest", Ports: "8400-8409", Home: h,
		Profiles: []Member{{Profile: d, Workspace: d.Workspace(), WorkspaceDest: SharedWorkspace}},
	}
}

func has(args []string, seq ...string) bool {
	for i := 0; i+len(seq) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(seq)], seq) {
			return true
		}
	}
	return false
}

func noTracked(string) ([]string, error) { return nil, nil }

func TestRunArgsHoldNoCredentialsAndNoEnvFile(t *testing.T) {
	s := baseSpec(t)
	write(t, s.Profiles[0].Profile.EnvFile(), "SECRET=1\n")
	joined := strings.Join(RunArgs(s, &strings.Builder{}), " ")
	for _, bad := range []string{"--env-file", "--tmpfs", "AWS_", "SECRET", ".tau"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%q in %s", bad, joined)
		}
	}
	// A setuid binary must not let one profile's user become another's.
	if !strings.Contains(joined, "--security-opt no-new-privileges") {
		t.Errorf("no-new-privileges missing: %s", joined)
	}
}

func TestRunArgsMountEveryProfileUnderItsOwnHome(t *testing.T) {
	s := baseSpec(t)
	w := prof(t, s.Home, "work")
	s.Profiles = append(s.Profiles, Member{Profile: w, Workspace: w.Workspace(), WorkspaceDest: w.Home() + "/workspace"})
	a := RunArgs(s, &strings.Builder{})
	for _, seq := range [][]string{
		{"-v", w.PiDir() + ":/home/p-work/.pi"},
		{"-v", w.Scripts() + ":/home/p-work/scripts"},
		{"-v", w.Share() + ":/home/p-work/share"},
		{"-v", w.Treehouse() + ":/home/p-work/.treehouse"},
		{"-v", w.Workspace() + ":/home/p-work/workspace"},
	} {
		if !has(a, seq...) {
			t.Errorf("missing %v", seq)
		}
	}
	for i, v := range a {
		if strings.Contains(v, "p-work") && strings.Contains(v, "/home/pi") {
			t.Errorf("arg %d mixes profiles: %s", i, v)
		}
	}
}

func TestASharedProfileMountsNoWorkspaceOfItsOwn(t *testing.T) {
	s := baseSpec(t)
	w := prof(t, s.Home, "work")
	s.Profiles = append(s.Profiles, Member{Profile: w, WorkspaceDest: SharedWorkspace})
	a := RunArgs(s, &strings.Builder{})
	if has(a, "-v", w.Workspace()+":"+SharedWorkspace) {
		t.Error("the shared workspace was mounted twice")
	}
	shared := 0
	for _, v := range a {
		if strings.HasPrefix(v, w.Workspace()+":") {
			t.Errorf("own workspace mounted: %s", v)
		}
		if strings.HasPrefix(v, ":") {
			t.Errorf("a mount with no source: %s", v)
		}
		if strings.HasSuffix(v, ":"+SharedWorkspace) {
			shared++
		}
	}
	if shared != 1 {
		t.Errorf("the shared workspace is mounted %d times, want once (by the default profile)", shared)
	}
}

func TestRunArgsOptionalFilesOnlyWhenPresent(t *testing.T) {
	s := baseSpec(t)
	d := s.Profiles[0].Profile
	joined := strings.Join(RunArgs(s, &strings.Builder{}), " ")
	for _, bad := range []string{"/home/pi/.gitconfig:ro", "/home/pi/.config/tau/zshrc", "/home/pi/.vimrc"} {
		if strings.Contains(joined, bad) {
			t.Errorf("%s present without its file", bad)
		}
	}
	for _, f := range []string{d.GitConfig(), d.Zshrc(), d.Vimrc()} {
		write(t, f, "x")
	}
	a := RunArgs(s, &strings.Builder{})
	for _, seq := range [][]string{
		{"-v", d.GitConfig() + ":/home/pi/.gitconfig:ro"},
		{"-v", d.Zshrc() + ":/home/pi/.config/tau/zshrc"},
		{"-v", d.Vimrc() + ":/home/pi/.vimrc"},
	} {
		if !has(a, seq...) {
			t.Errorf("missing %v", seq)
		}
	}
}

func TestRunArgsOnlyGitconfigIsReadOnly(t *testing.T) {
	s := baseSpec(t)
	write(t, s.Profiles[0].Profile.GitConfig(), "x")
	write(t, s.Profiles[0].Profile.Zshrc(), "x")
	for i, v := range RunArgs(s, &strings.Builder{}) {
		if strings.HasSuffix(v, ":ro") && !strings.Contains(v, ".gitconfig") {
			t.Errorf("arg %d: unexpected read-only mount %s", i, v)
		}
	}
}

func TestRunArgsLiveMountsComeAfterTheWorkspaceMount(t *testing.T) {
	// Docker applies mounts in order; the live bind must sit on top of /workspace.
	s := baseSpec(t)
	live := filepath.Join(s.Home, "proj")
	os.MkdirAll(live, 0o755)
	s.Profiles[0].Live = map[string]string{"proj": live}
	a := strings.Join(RunArgs(s, &strings.Builder{}), "\n")
	if strings.Index(a, s.Profiles[0].Workspace+":/srv/shared/workspace\n") > strings.Index(a, live+":/srv/shared/workspace/proj") {
		t.Error("live mount precedes the workspace mount")
	}
}

func TestRunArgsWithNoPortsPublishesNothing(t *testing.T) {
	s := baseSpec(t)
	s.Ports = ""
	a := RunArgs(s, &strings.Builder{})
	if has(a, "-p") || !has(a, "-e", "TAU_PORTS=") {
		t.Errorf("args: %v", a)
	}
}

func TestPrepareCreatesDirs(t *testing.T) {
	s := baseSpec(t)
	repo := t.TempDir()
	if err := Prepare(s, repo, noTracked); err != nil {
		t.Fatal(err)
	}
	d0 := s.Profiles[0].Profile
	for _, d := range []string{s.Profiles[0].Workspace, d0.Scripts(), d0.Share(), d0.Treehouse(),
		filepath.Join(d0.PiDir(), "agent/skills"), filepath.Join(d0.PiDir(), "agent/treehouse")} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("%s not created", d)
		}
	}
}

func TestPrepareSeedsTrackedPiConfigOverTheRuntimeCopy(t *testing.T) {
	s := baseSpec(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, "pi-config/agent/skills/tau/SKILL.md"), "new\n")
	write(t, filepath.Join(repo, "pi-config/agent/sessions/private.jsonl"), "secret\n")
	write(t, filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/skills/tau/SKILL.md"), "old\n")
	write(t, filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/settings.json"), "{mine}\n")
	tracked := func(dir string) ([]string, error) {
		if dir != filepath.Join(repo, "pi-config") {
			t.Errorf("asked for tracked files in %s", dir)
		}
		return []string{"agent/skills/tau/SKILL.md"}, nil
	}
	if err := Prepare(s, repo, tracked); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/skills/tau/SKILL.md")); string(b) != "new\n" {
		t.Errorf("tracked file not refreshed: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/settings.json")); string(b) != "{mine}\n" {
		t.Errorf("user file touched: %q", b)
	}
	if _, err := os.Stat(filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/sessions/private.jsonl")); !os.IsNotExist(err) {
		t.Error("an untracked file was copied")
	}
}

func TestPrepareRefusesTrackedPathsThatEscape(t *testing.T) {
	s := baseSpec(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, "outside"), "x")
	os.MkdirAll(filepath.Join(repo, "pi-config"), 0o755)
	for _, bad := range []string{"../outside", "/etc/passwd", "a/../../outside"} {
		err := Prepare(s, repo, func(string) ([]string, error) { return []string{bad}, nil })
		if err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.Profiles[0].Profile.PiDir()), "outside")); !os.IsNotExist(err) {
		t.Error("wrote outside the pi config dir")
	}
}

func TestPrepareSkipsWhenRepoAndRuntimeAreTheSameDir(t *testing.T) {
	s := baseSpec(t)
	repo := t.TempDir()
	os.MkdirAll(filepath.Join(repo, "pi-config"), 0o755)
	// The profile's pi directory is <Dir>/pi, so this makes it the repo's pi-config.
	os.Rename(filepath.Join(repo, "pi-config"), filepath.Join(repo, "pi"))
	s.Profiles[0].Profile = profile.Profile{Name: "default", Dir: repo, User: "pi", UID: 1000}
	srcRepo := filepath.Join(repo, "nested")
	os.MkdirAll(srcRepo, 0o755)
	os.Symlink(filepath.Join(repo, "pi"), filepath.Join(srcRepo, "pi-config"))
	repo = srcRepo
	called := false
	if err := Prepare(s, repo, func(string) ([]string, error) { called = true; return nil, nil }); err != nil || called {
		t.Errorf("err %v called %v", err, called)
	}
}
func TestPrepareKeepsTheExecutableBitOfTrackedFiles(t *testing.T) {
	s := baseSpec(t)
	repo := t.TempDir()
	p := filepath.Join(repo, "pi-config/agent/bin/tool")
	write(t, p, "#!/bin/sh\n")
	os.Chmod(p, 0o755)
	if err := Prepare(s, repo, func(string) ([]string, error) { return []string{"agent/bin/tool"}, nil }); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/bin/tool"))
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v err %v", st.Mode(), err)
	}
}

func TestPrepareFixesTheModeOfAnExistingFile(t *testing.T) {
	// os.WriteFile keeps the mode of a file that already exists, so the copy must set it.
	s := baseSpec(t)
	repo := t.TempDir()
	write(t, filepath.Join(repo, "pi-config/agent/bin/tool"), "new\n")
	os.Chmod(filepath.Join(repo, "pi-config/agent/bin/tool"), 0o755)
	write(t, filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/bin/tool"), "old\n") // written 0644
	if err := Prepare(s, repo, func(string) ([]string, error) { return []string{"agent/bin/tool"}, nil }); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(s.Profiles[0].Profile.PiDir(), "agent/bin/tool")); st.Mode().Perm() != 0o755 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}
