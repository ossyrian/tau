package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/profile"
)

// withProfiles makes a "work" profile in the harness's home and returns its directory.
func withProfiles(t *testing.T, h *harness, workConfig string) string {
	t.Helper()
	p, err := profile.Set{Root: h.home}.Create("work")
	if err != nil {
		t.Fatal(err)
	}
	if workConfig != "" {
		write(t, p.ConfigFile(), workConfig)
	}
	h.app.Profiles = profile.Set{Root: h.home}.List
	return p.Dir
}

func TestProfileNewCreatesItsDirectories(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run([]string{"profiles", "new", "work"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if st, err := os.Stat(filepath.Join(h.home, "profiles", "work", "pi")); err != nil || !st.IsDir() {
		t.Error("pi directory not created")
	}
	if h.out.String() != "work\n" {
		t.Errorf("out %q", h.out)
	}
}

func TestProfileNewRefusesBadAndDuplicateNames(t *testing.T) {
	h := newHarness(t, "", "")
	for _, bad := range []string{"Bad Name", "default", "root"} {
		if code := h.app.Run([]string{"profiles", "new", bad}); code != 1 {
			t.Errorf("%q accepted", bad)
		}
	}
	h.app.Run([]string{"profiles", "new", "work"})
	if code := h.app.Run([]string{"profiles", "new", "work"}); code != 1 || !strings.Contains(h.err.String(), "already exists") {
		t.Errorf("duplicate: %d %q", code, h.err)
	}
}

func TestProfileLsListsTheDefaultFirstWithItsWorkspaceMode(t *testing.T) {
	h := newHarness(t, "", "")
	dir := withProfiles(t, h, "workspaces:\n  mode: shared\n")
	_ = dir
	if code := h.app.Run([]string{"profiles", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	lines := strings.Split(strings.TrimRight(h.out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("want a header and two profiles, got %q", lines)
	}
	assertHasWords(t, strings.Fields(lines[1]), "default", "pi", "owner")
	assertHasWords(t, strings.Fields(lines[2]), "work", "p-work", "shared")
}

func TestProfileWorkspacesSetsTheMode(t *testing.T) {
	h := newHarness(t, "", "")
	dir := withProfiles(t, h, "packages: {}\n")
	if code := h.app.Run([]string{"profiles", "workspaces", "work", "shared"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if !strings.Contains(string(data), "mode: shared") || !strings.Contains(string(data), "packages") {
		t.Errorf("config:\n%s", data)
	}
	if code := h.app.Run([]string{"profiles", "workspaces", "work", "sometimes"}); code != 1 {
		t.Error("a bad mode was accepted")
	}
	if code := h.app.Run([]string{"profiles", "workspaces", "default", "shared"}); code != 1 || !strings.Contains(h.err.String(), "owns the shared workspace") {
		t.Errorf("default: %d %q", code, h.err)
	}
	if code := h.app.Run([]string{"profiles", "workspaces", "ghost", "shared"}); code != 1 {
		t.Error("a missing profile was accepted")
	}
}

func TestTheProfileFlagPicksWhichConfigACommandReads(t *testing.T) {
	h := newHarness(t, "packages:\n  basepkg: {script: echo b}\n", "")
	withProfiles(t, h, "packages:\n  workpkg: {script: echo w}\n")
	h.app.Run([]string{"packages", "list"})
	if !strings.Contains(h.out.String(), "basepkg") || strings.Contains(h.out.String(), "workpkg") {
		t.Errorf("default: %q", h.out)
	}
	for _, args := range [][]string{{"packages", "list", "--profile", "work"}, {"-p", "work", "packages", "list"}} {
		h.out.Reset()
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
		if !strings.Contains(h.out.String(), "workpkg") || strings.Contains(h.out.String(), "basepkg") {
			t.Errorf("%v: %q", args, h.out)
		}
	}
}

func TestTAUPROFILEIsTheDefaultSelectionAndTheFlagBeatsIt(t *testing.T) {
	h := newHarness(t, "packages:\n  basepkg: {script: echo b}\n", "")
	withProfiles(t, h, "packages:\n  workpkg: {script: echo w}\n")
	h.app.Env = func(k string) string {
		return map[string]string{"TAU_HOME": h.home, "TAU_REPO": h.repo, "TAU_PROFILE": "work"}[k]
	}
	h.app.Run([]string{"packages", "list"})
	if !strings.Contains(h.out.String(), "workpkg") {
		t.Errorf("env: %q", h.out)
	}
	h.out.Reset()
	h.app.Run([]string{"packages", "list", "-p", "default"})
	if !strings.Contains(h.out.String(), "basepkg") {
		t.Errorf("flag over env: %q", h.out)
	}
}

func TestAnUnknownProfileFailsBeforeAnythingRuns(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: echo a}\n", "")
	if code := h.app.Run([]string{"packages", "list", "-p", "ghost"}); code != 1 || !strings.Contains(h.err.String(), "tau profiles new ghost") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if h.out.Len() != 0 {
		t.Errorf("printed %q", h.out)
	}
}

func TestSelectingAProfileSwitchesTheServices(t *testing.T) {
	h := newHarness(t, "", "")
	withProfiles(t, h, "")
	var picked []string
	h.app.Select = func(name string) error { picked = append(picked, name); return nil }
	h.app.Run([]string{"packages", "list"})
	h.app.Run([]string{"packages", "list", "-p", "work"})
	if strings.Join(picked, ",") != "default,work" {
		t.Errorf("picked %v", picked)
	}
}

func TestBuildCoversEveryProfileInOneImage(t *testing.T) {
	h := newHarness(t, "packages:\n  basepkg: {script: echo b}\n", "")
	withProfiles(t, h, "packages:\n  workpkg: {script: echo w, as: user}\n")
	if code := h.app.Run([]string{"build", "--print"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	out := h.out.String()
	for _, want := range []string{"echo b", "echo w", "useradd -m -u", "USER p-work"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestBuildNamesTheProfileWithUnresolvedDrift(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Interactive = false
	dir := withProfiles(t, h, "packages:\n  a: {script: echo a}\n")
	write(t, filepath.Join(dir, "config.runtime.yaml"), "packages:\n  a: {script: echo a}\n  extra: {script: echo x}\n")
	code := h.app.Run([]string{"build", "--print"})
	if code != 1 || !strings.Contains(h.err.String(), "in profile work") || !strings.Contains(h.err.String(), "extra") {
		t.Errorf("exit %d err %q", code, h.err)
	}
}

func TestBuildIgnoreDriftSkipsEveryProfilesDrift(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Interactive = false
	dir := withProfiles(t, h, "packages:\n  a: {script: echo a}\n")
	write(t, filepath.Join(dir, "config.runtime.yaml"), "packages:\n  extra: {script: echo x}\n")
	if code := h.app.Run([]string{"build", "--print", "--ignore-drift"}); code != 0 {
		t.Errorf("exit %d: %s", code, h.err)
	}
}
