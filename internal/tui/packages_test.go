package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

const pkgBase = "packages:\n  postgres: {apt: [postgresql-client]}\n  tool:\n    script: echo hi\n    as: user\n    needs: [postgres]\n"
const pkgRuntime = pkgBase + "  extra: {script: echo extra}\n"

func inPackages(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := newRoot(t, f)
	send(t, m, "4")
	return m
}

func driftStatus(f *fakeAPI) {
	f.status.Drift = config.Diff{RuntimeOnly: []string{"extra"}}
	f.status.NeedsResolution = true
}

func TestPackagesTabsSwitchWithArrowsAndWrap(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, "")
	m := inPackages(t, f)
	p := m.sections[m.active].(*packages)
	if p.tab != 0 {
		t.Fatal("not on the first tab")
	}
	send(t, m, "right")
	if p.tab != 1 {
		t.Errorf("right: %d", p.tab)
	}
	send(t, m, "left", "left")
	if p.tab != len(packageTabs)-1 {
		t.Errorf("left wraps: %d", p.tab)
	}
	send(t, m, "right")
	if p.tab != 0 {
		t.Errorf("right wraps: %d", p.tab)
	}
	send(t, m, "l")
	if p.tab != 1 {
		t.Errorf("l: %d", p.tab)
	}
	send(t, m, "h")
	if p.tab != 0 {
		t.Errorf("h: %d", p.tab)
	}
}

func TestPackagesChangesTabShowsTheDriftAndResolvesIt(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, pkgRuntime)
	driftStatus(f)
	m := inPackages(t, f)
	send(t, m, "right")
	out := screen(m)
	if !strings.Contains(out, "extra") || !strings.Contains(out, "echo extra") {
		t.Errorf("drift detail missing:\n%s", out)
	}
	// Accept: the package moves into config.yaml, and the runtime file goes.
	send(t, m, "a")
	base, _ := os.ReadFile(filepath.Join(f.home, "config.yaml"))
	if !strings.Contains(string(base), "extra") {
		t.Errorf("config.yaml lacks the accepted package:\n%s", base)
	}
	if _, err := os.Stat(filepath.Join(f.home, "config.runtime.yaml")); !os.IsNotExist(err) {
		t.Error("runtime file should be gone once everything is resolved")
	}
	if !strings.Contains(screen(m), "no changes") {
		t.Errorf("still showing drift:\n%s", screen(m))
	}
}

func TestPackagesRejectDiscardsTheRuntimePackage(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, pkgRuntime)
	driftStatus(f)
	m := inPackages(t, f)
	send(t, m, "right", "r")
	base, _ := os.ReadFile(filepath.Join(f.home, "config.yaml"))
	if strings.Contains(string(base), "extra") {
		t.Error("rejected package leaked into config.yaml")
	}
}

func TestPackagesResolvingRefreshesTheStatusBadge(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, pkgRuntime)
	driftStatus(f)
	m := inPackages(t, f)
	send(t, m, "right")
	f.status.Drift = config.Diff{}
	f.status.NeedsResolution = false
	send(t, m, "a")
	if len(m.status.Drift.RuntimeOnly) != 0 {
		t.Error("the root kept the old status after a decision")
	}
}

func TestPackagesBuildRunsAndShowsOutput(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, "")
	f.buildOut = "Step 1/9 : FROM debian\nSuccessfully built abc123\n"
	m := inPackages(t, f)
	send(t, m, "right", "right", "b")
	if got := f.did(); len(got) != 1 || got[0] != "build" {
		t.Fatalf("calls %v", got)
	}
	out := screen(m)
	if !strings.Contains(out, "Successfully built abc123") {
		t.Errorf("build output not shown:\n%s", out)
	}
	if !strings.Contains(out, "built") {
		t.Errorf("no completion notice:\n%s", out)
	}
}

func TestPackagesBuildWithoutCache(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, "")
	m := inPackages(t, f)
	send(t, m, "right", "right", "B")
	if len(f.did()) != 0 {
		t.Fatal("a full rebuild should ask first: it is slow")
	}
	send(t, m, "y")
	if got := f.did(); len(got) != 1 || got[0] != "build:nocache" {
		t.Errorf("calls %v", got)
	}
}

func TestPackagesBuildOfALargeLogKeepsTheTail(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, pkgBase, "")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		b.WriteString("line " + strings.Repeat("x", 5) + "\n")
	}
	b.WriteString("THE END\n")
	f.buildOut = b.String()
	m := inPackages(t, f)
	send(t, m, "right", "right", "b")
	if !strings.Contains(screen(m), "THE END") {
		t.Error("the last line of the log is not visible")
	}
}

func TestPackagesBuildLogStripsTerminalControlSequences(t *testing.T) {
	// Docker prints colour and cursor codes. They must not reach the screen.
	f := newFake(t)
	writeConfig(t, f, pkgBase, "")
	f.buildOut = "\x1b[2J\x1b[1;1Hhello\x1b[0m\r\nworld\x07\n"
	m := inPackages(t, f)
	send(t, m, "right", "right", "b")
	raw := m.View().Content
	if strings.Contains(raw, "\x1b[2J") || strings.Contains(raw, "\x07") {
		t.Errorf("control sequence from the build log reached the terminal: %q", raw)
	}
	if strings.Contains(screen(m), "[2J") || strings.Contains(screen(m), "[1;1H") {
		t.Errorf("escape sequence left as visible junk:\n%s", screen(m))
	}
	if !strings.Contains(screen(m), "hello") || !strings.Contains(screen(m), "world") {
		t.Errorf("text lost:\n%s", screen(m))
	}
}

func TestPackagesKeysDoNotLeakAcrossTabs(t *testing.T) {
	// "a" accepts on the changes tab only.
	f := newFake(t)
	writeConfig(t, f, pkgBase, pkgRuntime)
	driftStatus(f)
	m := inPackages(t, f)
	send(t, m, "a") // installed tab
	base, _ := os.ReadFile(filepath.Join(f.home, "config.yaml"))
	if strings.Contains(string(base), "extra") {
		t.Error("a resolved a change from the wrong tab")
	}
	send(t, m, "right", "right", "a") // build tab
	if len(f.did()) != 0 {
		t.Errorf("calls %v", f.did())
	}
}
