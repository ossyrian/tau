package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/skills"
)

// terminal makes the harness behave as if stdout were a terminal.
func terminal(h *harness) {
	h.app.Pretty = true
	h.app.Width = 100
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

func TestPipedOutputHasNoEscapesAndNoHeaders(t *testing.T) {
	// The harness is not a terminal: every command must print plain records.
	h, life := coreHarness(t, goodBase, "")
	life.st = container.State{Exists: true, Running: true, Env: map[string]string{}}
	h.app.Lifecycle = &fakeLC{}
	withSessions(h)
	h.app.Core.Skill = fakeSk{
		list:  []skills.Skill{{Name: "a", Path: "/p/a", State: skills.Inactive}},
		paths: []skills.PathInfo{{Path: "/p", Found: true, Skills: 1}},
	}
	for _, args := range [][]string{
		{"status"}, {"skills", "list"}, {"skills", "paths", "list"}, {"env", "list"}, {"packages", "list"}, {"session", "list"}, {"start"},
	} {
		h.out.Reset()
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
		out := h.out.String()
		if strings.Contains(out, "\x1b") {
			t.Errorf("%v: escape codes in piped output: %q", args, out)
		}
		for _, hdr := range []string{"Skills\n", "Packages\n", "Sessions\n", "Skill paths\n", "State:"} {
			if strings.Contains(out, hdr) {
				t.Errorf("%v: header %q in piped output: %q", args, hdr, out)
			}
		}
	}
}

func TestPipedEmptyResultsPrintNothing(t *testing.T) {
	h, _ := coreHarness(t, "packages: {}\n", "")
	h.app.Core.Skill = fakeSk{}
	withSessions(h)
	for _, args := range [][]string{{"skills", "list"}, {"skills", "paths", "list"}, {"env", "list"}, {"packages", "list"}, {"packages", "diff"}, {"session", "list"}} {
		h.out.Reset()
		if code := h.app.Run(args); code != 0 {
			t.Fatalf("%v: %s", args, h.err)
		}
		if h.out.Len() != 0 {
			t.Errorf("%v: %q would read as a record", args, h.out)
		}
	}
}

func TestTerminalStatusIsALabelledBlock(t *testing.T) {
	h, life := coreHarness(t, goodBase, "packages:\n  x: {script: echo x}\n")
	terminal(h)
	life.st = container.State{Exists: true, Running: true, Env: map[string]string{}}
	h.app.Run([]string{"status"})
	out := plain(h)
	for _, want := range []string{"container", "running", "profile", "1 need a decision", "in sync"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "=") || strings.Contains(out, "profile=") {
		t.Errorf("machine syntax leaked into the terminal form:\n%s", out)
	}
	themed(t, h.out.String())
}

func TestTerminalSessionLsLooksLikeSkillsLs(t *testing.T) {
	h := newHarness(t, "", "")
	terminal(h)
	d := withSessions(h)
	d.out["list-sessions"] = "main|1700000000|1700003600\nsa-worker|1700000100|1700000200\n"
	if code := h.app.Run([]string{"session", "list"}); code != 0 {
		t.Fatal(h.err)
	}
	got := strings.Split(strings.TrimRight(plain(h), "\n"), "\n")
	if len(got) != 6 || got[0] != "Sessions" || got[1] != "" {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
	if strings.TrimRight(got[2], " ") != "main" || strings.TrimRight(got[4], " ") != "sa-worker" {
		t.Errorf("names: %q", got)
	}
	for _, want := range []string{"Type: session", "Created: 2023-11-14 22:13:20", "Last active: 2023-11-14 23:13:20"} {
		if !strings.Contains(got[3], want) {
			t.Errorf("details lack %q: %q", want, got[3])
		}
	}
	if !strings.Contains(got[5], "Type: subagent") {
		t.Errorf("subagent not marked: %q", got[5])
	}
	if strings.Contains(plain(h), "T22:") {
		t.Errorf("RFC 3339 leaked into the terminal form:\n%s", plain(h))
	}
	themed(t, h.out.String())
}

func TestPipedPackagesLsListsNameAsAndNeeds(t *testing.T) {
	h := newHarness(t, "packages:\n  pg: {apt: [x]}\n  tool: {script: s, as: user, needs: [pg]}\n", "")
	h.app.Run([]string{"packages", "list"})
	if h.out.String() != "pg\troot\t\ntool\tuser\tpg\n" {
		t.Errorf("got %q", h.out)
	}
}

func TestTerminalDiffReadsInWordsButPipedDiffUsesTokens(t *testing.T) {
	base, runtime := "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n  r: {script: r}\n"
	p := newHarness(t, base, runtime)
	p.app.Run([]string{"packages", "diff"})
	if p.out.String() != "runtime-only\tr\n" {
		t.Errorf("piped %q", p.out)
	}
	h := newHarness(t, base, runtime)
	terminal(h)
	h.app.Run([]string{"packages", "diff"})
	if !strings.Contains(plain(h), "added at runtime") || !strings.Contains(plain(h), "CHANGE") {
		t.Errorf("terminal %q", plain(h))
	}
}

func TestTerminalActionsGetAMarkedLineWithAHint(t *testing.T) {
	h, _ := coreHarness(t, goodBase, "")
	terminal(h)
	h.app.Core.Skill = fakeSk{}
	h.app.Run([]string{"skills", "activate", "voice"})
	if plain(h) != "✓ activated voice  restart to apply\n" {
		t.Errorf("got %q", plain(h))
	}
	themed(t, h.out.String())
}

func TestTerminalSkillsLsStatesTheStatusOnce(t *testing.T) {
	// State already says whether a skill is active; a second field would repeat it.
	h, _ := coreHarness(t, goodBase, "")
	terminal(h)
	h.app.Core.Skill = fakeSk{list: []skills.Skill{
		{Name: "a", Path: "/p/a", Active: true, State: skills.Synced},
		{Name: "b", Path: "/p/b", State: skills.Inactive},
	}}
	h.app.Run([]string{"skills", "list"})
	if strings.Contains(plain(h), "Active:") {
		t.Errorf("redundant field:\n%s", plain(h))
	}
}
