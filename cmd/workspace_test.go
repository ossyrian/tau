package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/workspace"
)

// wsHarness is a harness whose Core has a real workspace store with fake copies.
func wsHarness(t *testing.T) (*harness, *fakeLife, string) {
	t.Helper()
	h := newHarness(t, "", "")
	life := &fakeLife{}
	h.app.Core = coreFor(h)
	h.app.Core.Life = life
	root := filepath.Join(h.home, "workspace")
	os.MkdirAll(root, 0o755)
	h.app.Core.Workspace = &workspace.Store{
		Dir: h.home, Root: root, Home: t.TempDir(), Mount: "/workspace",
		Clone: func(src, dst string) error {
			os.MkdirAll(dst, 0o755)
			return os.WriteFile(filepath.Join(dst, "f"), []byte("x"), 0o644)
		},
		Merge: func(src, dst string) error { return nil },
	}
	return h, life, root
}

func srcDir(t *testing.T, name string) string {
	t.Helper()
	d := filepath.Join(t.TempDir(), name)
	os.MkdirAll(d, 0o755)
	real, _ := filepath.EvalSymlinks(d)
	return real
}

func TestWorkspaceAddPrintsOnlyTheHostPathOnStdoutForTheShellWrappers(t *testing.T) {
	h, _, root := wsHarness(t)
	d := srcDir(t, "proj")
	if code := h.app.Run([]string{"workspace", "add", d}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if want := filepath.Join(root, "proj") + "\n"; h.out.String() != want {
		t.Errorf("stdout %q, want only the path %q", h.out, want)
	}
	if !strings.Contains(h.err.String(), "proj") {
		t.Errorf("no message on stderr: %q", h.err)
	}
}

func TestWorkspaceAddLivePrintsTheRealSourceAndWarnsAboutWriteAccess(t *testing.T) {
	h, _, _ := wsHarness(t)
	d := srcDir(t, "app")
	if code := h.app.Run([]string{"workspace", "add", d, "--live"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if h.out.String() != d+"\n" {
		t.Errorf("stdout %q", h.out)
	}
	for _, want := range []string{"read/write", "can edit or delete"} {
		if !strings.Contains(h.err.String(), want) {
			t.Errorf("stderr lacks %q: %q", want, h.err)
		}
	}
	if w, _ := config.LoadWorkspaces(h.home); w.Live["app"] != d {
		t.Errorf("config: %v", w.Live)
	}
}

func TestWorkspaceAddDefaultsToTheCurrentDirectoryAndTakesAName(t *testing.T) {
	h, _, root := wsHarness(t)
	d := srcDir(t, "here")
	old, _ := os.Getwd()
	os.Chdir(d)
	defer os.Chdir(old)
	if code := h.app.Run([]string{"workspace", "add", "--name", "renamed"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if h.out.String() != filepath.Join(root, "renamed")+"\n" {
		t.Errorf("stdout %q", h.out)
	}
}

func TestWorkspaceAddReportsRefreshAndNoChange(t *testing.T) {
	h, _, _ := wsHarness(t)
	d := srcDir(t, "p")
	h.app.Run([]string{"workspace", "add", d})
	h.err.Reset()
	h.app.Run([]string{"workspace", "add", d})
	if !strings.Contains(h.err.String(), "refreshed") {
		t.Errorf("refresh: %q", h.err)
	}
	l := srcDir(t, "l")
	h.app.Run([]string{"workspace", "add", l, "--live"})
	h.err.Reset()
	h.app.Run([]string{"workspace", "add", l, "--live"})
	if !strings.Contains(h.err.String(), "already") {
		t.Errorf("unchanged: %q", h.err)
	}
}

func TestWorkspaceAddFailuresExitOneAndPrintNothingOnStdout(t *testing.T) {
	h, _, _ := wsHarness(t)
	if code := h.app.Run([]string{"workspace", "add", filepath.Join(t.TempDir(), "none")}); code != 1 || h.out.Len() != 0 {
		t.Errorf("exit %d out %q", code, h.out)
	}
	if code := h.app.Run([]string{"workspace", "add", srcDir(t, "p"), "--name", "../x"}); code != 1 || h.out.Len() != 0 {
		t.Errorf("bad name: exit %d out %q", code, h.out)
	}
}

func TestWorkspaceLsPipedIsTabSeparated(t *testing.T) {
	h, _, root := wsHarness(t)
	d := srcDir(t, "app")
	h.app.Run([]string{"workspace", "add", d, "--live"})
	h.app.Run([]string{"workspace", "add", srcDir(t, "zed")})
	h.out.Reset()
	if code := h.app.Run([]string{"workspace", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	want := "app\tlive\t/workspace/app\t" + d + "\nzed\tlocal\t/workspace/zed\t" + filepath.Join(root, "zed") + "\n"
	if h.out.String() != want {
		t.Errorf("got %q\nwant %q", h.out, want)
	}
}

func TestWorkspaceLsOnATerminalShowsKindAndWhereTheAgentSeesIt(t *testing.T) {
	h, _, _ := wsHarness(t)
	h.app.Run([]string{"workspace", "add", srcDir(t, "app"), "--live"})
	h.out.Reset()
	terminal(h)
	h.app.Run([]string{"workspace", "list"})
	out := plain(h)
	for _, want := range []string{"app", "live", "/workspace/app"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestWorkspaceRmRemovesACopyAndSaysTheSourceIsUntouched(t *testing.T) {
	h, _, root := wsHarness(t)
	d := srcDir(t, "p")
	h.app.Run([]string{"workspace", "add", d})
	h.err.Reset()
	if code := h.app.Run([]string{"workspace", "rm", "p"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if _, err := os.Stat(filepath.Join(root, "p")); err == nil {
		t.Error("copy still there")
	}
	if !strings.Contains(h.err.String(), "untouched") {
		t.Errorf("stderr %q", h.err)
	}
}

func TestWorkspaceRmOfALiveEntryUnmountsOnRestartAndKeepsTheSource(t *testing.T) {
	h, life, _ := wsHarness(t)
	d := srcDir(t, "app")
	h.app.Run([]string{"workspace", "add", d, "--live"})
	life.st.Running = true
	h.err.Reset()
	if code := h.app.Run([]string{"workspace", "remove", "app"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if _, err := os.Stat(d); err != nil {
		t.Error("the live source was deleted")
	}
	if !strings.Contains(h.err.String(), "tau restart") {
		t.Errorf("stderr %q", h.err)
	}
}

func TestWorkspaceRmOfAnUnknownNameFails(t *testing.T) {
	h, _, _ := wsHarness(t)
	if code := h.app.Run([]string{"workspace", "rm", "nope"}); code != 1 || !strings.Contains(h.err.String(), "nope") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if code := h.app.Run([]string{"workspace", "rm"}); code != 1 {
		t.Errorf("no argument: exit %d", code)
	}
}

func TestWorkspaceCdPrintsTheHostPathAndNothingElse(t *testing.T) {
	h, _, root := wsHarness(t)
	d := srcDir(t, "app")
	h.app.Run([]string{"workspace", "add", d, "--live"})
	h.app.Run([]string{"workspace", "add", srcDir(t, "p")})
	h.out.Reset()
	h.err.Reset()
	for name, want := range map[string]string{"app": d, "p": filepath.Join(root, "p"), "": root} {
		h.out.Reset()
		args := []string{"workspace", "cd"}
		if name != "" {
			args = append(args, name)
		}
		if code := h.app.Run(args); code != 0 || h.out.String() != want+"\n" {
			t.Errorf("cd %q: exit %d out %q want %q err %q", name, code, h.out, want, h.err)
		}
	}
	h.out.Reset()
	if code := h.app.Run([]string{"workspace", "cd", "nope"}); code != 1 || h.out.Len() != 0 {
		t.Errorf("unknown: exit %d out %q", code, h.out)
	}
}

func TestWorkspaceWorksOnTheSelectedProfile(t *testing.T) {
	h, _, _ := wsHarness(t)
	dir := withProfiles(t, h, "")
	// A profile-specific store, as the real wiring builds one.
	h.app.Select = func(string) error {
		root := filepath.Join(dir, "workspace")
		os.MkdirAll(root, 0o755)
		h.app.Core.Workspace = &workspace.Store{Dir: dir, Root: root, Home: t.TempDir(), Mount: "/home/p-work/workspace",
			Clone: func(s, d string) error { return os.MkdirAll(d, 0o755) }, Merge: func(string, string) error { return nil }}
		return nil
	}
	d := srcDir(t, "p")
	if code := h.app.Run([]string{"workspace", "add", d, "-p", "work"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if h.out.String() != filepath.Join(dir, "workspace", "p")+"\n" {
		t.Errorf("out %q", h.out)
	}
	if w, _ := config.LoadWorkspaces(h.home); len(w.Live) != 0 {
		t.Error("the default profile's config was touched")
	}
}

func TestALiveAddWarnsThatTheAgentCanEditTheSource(t *testing.T) {
	h, _, _ := wsHarness(t)
	h.app.Run([]string{"workspace", "add", srcDir(t, "proj"), "--live"})
	if !strings.Contains(h.err.String(), "read/write") {
		t.Errorf("no warning: %q", h.err)
	}
}
