package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/skills"
)

// terminal makes the harness behave as if stdout were a terminal.
func terminal(h *harness) {
	h.app.Pretty = true
}

func plain(h *harness) string { return ansi.Strip(h.out.String()) }

var sgr = regexp.MustCompile("\x1b\\[([0-9;:]*)m")

// themed fails on a background, reverse video, or a colour outside the
// terminal's own 16.
func themed(t *testing.T, out string) {
	t.Helper()
	for _, m := range sgr.FindAllStringSubmatch(out, -1) {
		for _, p := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' }) {
			if p == "7" || p == "48" || (len(p) == 2 && p[0] == '4' && p[1] <= '7') {
				t.Errorf("paints a background: %q", m[0])
			}
		}
	}
	if strings.Contains(out, "38;5;") || strings.Contains(out, "38;2;") {
		t.Errorf("fixed colour: %q", out)
	}
}

// rowOf returns the words on the line that has key as one of its words. It
// does not depend on spacing or on the order of the columns.
func rowOf(t *testing.T, out, key string) []string {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if f := strings.Fields(l); slices.Contains(f, key) {
			return f
		}
	}
	t.Fatalf("no row for %q in:\n%s", key, out)
	return nil
}

// assertHasWords fails unless every want is a word of the row.
func assertHasWords(t *testing.T, row []string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !slices.Contains(row, w) {
			t.Errorf("row %q lacks %q", row, w)
		}
	}
}

// assertProfileFirst fails unless the header's first word is PROFILE and every
// other line starts with the profile's name.
func assertProfileFirst(t *testing.T, out, profile string) {
	t.Helper()
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if f := strings.Fields(lines[0]); len(f) == 0 || f[0] != "PROFILE" {
		t.Errorf("PROFILE is not the first column: %q", lines[0])
	}
	for _, l := range lines[1:] {
		if f := strings.Fields(l); len(f) == 0 || f[0] != profile {
			t.Errorf("row does not start with the profile %q: %q", profile, l)
		}
	}
}

func TestPipedOutputHasNoEscapes(t *testing.T) {
	// The harness is not a terminal: no command may print colour codes.
	h, life := coreHarness(t, goodBase, "")
	life.st = container.State{Exists: true, Running: true, Env: map[string]string{}}
	h.app.Lifecycle = &fakeLC{}
	withSessions(h)
	h.app.Core.Skill = fakeSk{
		list:  []skills.Skill{{Name: "a", Path: "/p/a", State: skills.Inactive}},
		paths: []skills.PathInfo{{Path: "/p", Found: true, Skills: 1}},
	}
	for _, args := range [][]string{
		{"status"}, {"skills", "list"}, {"skills", "paths", "list"}, {"env", "list"}, {"packages", "list"}, {"sessions", "list"}, {"start"},
	} {
		h.out.Reset()
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
		if strings.Contains(h.out.String(), "\x1b") {
			t.Errorf("%v: escape codes in piped output: %q", args, h.out)
		}
	}
}

func TestEveryListPrintsOnlyAnUpperCaseHeaderWhenEmpty(t *testing.T) {
	h, _ := coreHarness(t, "packages: {}\n", "")
	h.app.Core.Skill = fakeSk{}
	withSessions(h)
	wh, _, _ := wsHarness(t)
	for _, c := range []struct {
		h    *harness
		args []string
	}{
		{h, []string{"skills", "list"}}, {h, []string{"skills", "paths", "list"}}, {h, []string{"env", "list"}},
		{h, []string{"packages", "list"}}, {h, []string{"packages", "diff"}}, {h, []string{"sessions", "list"}},
		{wh, []string{"workspaces", "list"}},
	} {
		c.h.out.Reset()
		if code := c.h.app.Run(c.args); code != 0 {
			t.Fatalf("%v: %s", c.args, c.h.err)
		}
		lines := strings.Split(strings.TrimRight(c.h.out.String(), "\n"), "\n")
		if len(lines) != 1 || lines[0] != strings.ToUpper(lines[0]) || lines[0] == "" {
			t.Errorf("%v: want only an upper-case header, got %q", c.args, c.h.out)
		}
		assertProfileFirst(t, c.h.out.String(), "default")
	}
}

func TestEveryProfileListPutsTheProfileFirstOnEveryRow(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "packages:\n  r: {script: x}\n")
	h.app.Core.Skill = fakeSk{
		list:  []skills.Skill{{Name: "a", Path: "/p/a", State: skills.Inactive}},
		paths: []skills.PathInfo{{Path: "/p", Found: true, Skills: 1}},
	}
	withSessions(h).out["list-sessions"] = "main|1700000000|1700000000\n"
	os.WriteFile(filepath.Join(h.home, ".env"), []byte("A=1\n"), 0o600)
	for _, args := range [][]string{
		{"skills", "list"}, {"skills", "paths", "list"}, {"env", "list"},
		{"packages", "list"}, {"packages", "diff"}, {"sessions", "list"}, {"status"},
	} {
		h.out.Reset()
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
		assertProfileFirst(t, h.out.String(), "default")
	}
	ph, _ := proxyHarness(t)
	ph.app.Run([]string{"proxies", "list"})
	assertProfileFirst(t, ph.out.String(), "default")
	wh, _, _ := wsHarness(t)
	wh.app.Run([]string{"workspaces", "add", srcDir(t, "app")})
	wh.out.Reset()
	wh.app.Run([]string{"workspaces", "list"})
	assertProfileFirst(t, wh.out.String(), "default")
}

func TestTerminalListsStayInThePalette(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	terminal(h)
	h.app.Core.Skill = fakeSk{
		list:  []skills.Skill{{Name: "a", Path: "/p/a", State: skills.Synced}, {Name: "b", Path: "/p/b", State: skills.Missing}},
		paths: []skills.PathInfo{{Path: "/p", Found: true}, {Path: "/gone"}},
	}
	withSessions(h).out["list-sessions"] = "main|1700000000|1700000000\n"
	for _, args := range [][]string{{"skills", "list"}, {"skills", "paths", "list"}, {"sessions", "list"}, {"packages", "list"}, {"status"}} {
		h.out.Reset()
		h.app.Run(args)
		themed(t, h.out.String())
	}
	ph, _ := proxyHarness(t)
	terminal(ph)
	ph.app.Run([]string{"proxies", "list"})
	themed(t, ph.out.String())
}

func TestTheTableIsTheSameOnATerminalExceptForColour(t *testing.T) {
	piped, _ := coreHarness(t, goodBase, "packages:\n  x: {script: echo x}\n")
	piped.app.Run([]string{"status"})
	term, _ := coreHarness(t, goodBase, "packages:\n  x: {script: echo x}\n")
	terminal(term)
	term.app.Run([]string{"status"})
	if plain(term) != piped.out.String() {
		t.Errorf("terminal:\n%s\npiped:\n%s", plain(term), piped.out)
	}
	if !strings.Contains(term.out.String(), "\x1b") {
		t.Error("a terminal gets colour")
	}
	themed(t, term.out.String())
}

func TestNoColorKeepsTheTerminalFreeOfEscapes(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	terminal(h)
	h.app.NoColor = true
	h.app.Run([]string{"status"})
	if strings.Contains(h.out.String(), "\x1b") {
		t.Errorf("escapes: %q", h.out)
	}
}

func TestADoneLineOnATerminalIsOneLineNamingTheSubject(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	terminal(h)
	h.app.Core.Skill = fakeSk{}
	h.app.Run([]string{"skills", "activate", "voice"})
	if out := plain(h); strings.Count(out, "\n") != 1 || !strings.Contains(out, "voice") {
		t.Errorf("got %q", out)
	}
}

func TestAgo(t *testing.T) {
	a := &App{Now: func() time.Time { return time.Unix(1_000_000, 0) }}
	for secs, want := range map[int64]string{
		10: "less than a minute ago", 60: "1 minute ago", 150: "2 minutes ago",
		3600: "1 hour ago", 7200: "2 hours ago", 47 * 3600: "47 hours ago",
		48 * 3600: "2 days ago", 3 * 86400: "3 days ago",
	} {
		if got := a.ago(time.Unix(1_000_000-secs, 0)); got != want {
			t.Errorf("%ds: got %q want %q", secs, got, want)
		}
	}
}
