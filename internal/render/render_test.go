package render

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;:]*[A-Za-z]")

func machine() (*Out, *bytes.Buffer) {
	b := &bytes.Buffer{}
	return &Out{W: b}, b
}

func pretty(width int) (*Out, *bytes.Buffer) {
	b := &bytes.Buffer{}
	return &Out{W: b, Pretty: true, Width: width, Home: "/home/me"}, b
}

func lines(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// themed fails when the output paints a background, uses reverse video or
// uses a colour outside the terminal's own 16.
func themed(t *testing.T, what, out string) {
	t.Helper()
	for _, m := range regexp.MustCompile("\x1b\\[([0-9;:]*)m").FindAllStringSubmatch(out, -1) {
		parts := strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' })
		for _, p := range parts {
			switch {
			case p == "7", p == "48", len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7',
				len(p) == 3 && p[:2] == "10" && p[2] >= '0' && p[2] <= '7':
				t.Errorf("%s paints a background or reverse video: %q", what, m[0])
			}
		}
	}
	if strings.Contains(out, "38;5;") || strings.Contains(out, "38;2;") {
		t.Errorf("%s uses a fixed colour: %q", what, out)
	}
}

func sampleItems() []Item {
	return []Item{
		{Title: "alpha", Note: "/home/me/skills/alpha", Fields: []string{"alpha", "synced", "/home/me/skills/alpha"},
			Details: []Detail{{"State", "synced", theme.OK}, {"Active", "yes", theme.Dim}}},
		{Title: "beta-long-name", Note: "/srv/beta", Fields: []string{"beta-long-name", "inactive", "/srv/beta"},
			Details: []Detail{{"State", "inactive", theme.Dim}, {"Active", "no", theme.Dim}}},
	}
}

func TestMachineListIsOneTabSeparatedLinePerItemWithNoColour(t *testing.T) {
	o, b := machine()
	o.List("Skills", sampleItems(), "no skills")
	want := "alpha\tsynced\t/home/me/skills/alpha\nbeta-long-name\tinactive\t/srv/beta\n"
	if b.String() != want {
		t.Errorf("got %q want %q", b.String(), want)
	}
}

func TestMachineCellsCannotBreakTheRecordFormat(t *testing.T) {
	o, b := machine()
	o.Table([]Column{{Title: "A"}, {Title: "B"}}, [][]string{{"a\tb", "c\nd\r"}}, "")
	got := b.String()
	if strings.Count(got, "\t") != 1 || strings.Count(got, "\n") != 1 || strings.Contains(got, "\r") {
		t.Errorf("a cell injected a separator: %q", got)
	}
}

func TestPrettyListShowsTwoLinesPerItemLikeNpxSkills(t *testing.T) {
	o, b := pretty(80)
	o.List("Skills", sampleItems(), "no skills")
	got := lines(ansi.Strip(b.String()))
	if got[0] != "Skills" || got[1] != "" {
		t.Fatalf("header: %q", got[:2])
	}
	want := []string{
		"alpha           ~/skills/alpha",
		"  State: synced  Active: yes",
		"beta-long-name  /srv/beta",
		"  State: inactive  Active: no",
	}
	if len(got) < 6 {
		t.Fatalf("got %q", got)
	}
	for i, w := range want {
		if strings.TrimRight(got[2+i], " ") != w {
			t.Errorf("line %d: %q want %q", 2+i, got[2+i], w)
		}
	}
}

func TestPrettyListShortensTheHomeDirectoryInPathsOnly(t *testing.T) {
	o, b := pretty(80)
	o.List("Skills", []Item{{Title: "home", Note: "/home/me"}, {Title: "other", Note: "/home/meow/x"}}, "")
	out := ansi.Strip(b.String())
	if !strings.Contains(out, "~\n") && !strings.Contains(out, "~ ") {
		t.Errorf("home not shortened: %q", out)
	}
	if !strings.Contains(out, "/home/meow/x") {
		t.Errorf("a different directory sharing the prefix was shortened: %q", out)
	}
}

func TestPrettyListIsSharedColouredAndInThePalette(t *testing.T) {
	o, b := pretty(80)
	o.List("Skills", sampleItems(), "")
	out := b.String()
	themed(t, "list", out)
	if !ansiRe.MatchString(out) {
		t.Error("no styling at all")
	}
	if strings.Contains(ansi.Strip(out), "\x1b") {
		t.Error("escape left after stripping")
	}
}

func TestPrettyListFitsTheWidth(t *testing.T) {
	for _, w := range []int{30, 50, 80} {
		o, b := pretty(w)
		o.List("Skills", []Item{{Title: strings.Repeat("n", 40), Note: strings.Repeat("/p", 60),
			Details: []Detail{{"State", strings.Repeat("s", 50), theme.OK}}}}, "")
		for i, l := range lines(b.String()) {
			if got := ansi.StringWidth(l); got > w {
				t.Errorf("width %d: line %d is %d wide: %q", w, i, got, ansi.Strip(l))
			}
		}
	}
}

func TestPrettyListEmptyShowsTheMessage(t *testing.T) {
	o, b := pretty(80)
	o.List("Skills", nil, "no skills found")
	if !strings.Contains(ansi.Strip(b.String()), "no skills found") {
		t.Errorf("got %q", b.String())
	}
	themed(t, "empty", b.String())
}

func TestPrettyTableHasAHeaderAndAlignedColumns(t *testing.T) {
	o, b := pretty(80)
	o.Table([]Column{{Title: "NAME"}, {Title: "TYPE"}, {Title: "WHEN"}},
		[][]string{{"main", "session", "now"}, {"sa-worker", "subagent", "later"}}, "")
	got := lines(ansi.Strip(b.String()))
	if len(got) != 3 {
		t.Fatalf("got %q", got)
	}
	col := strings.Index(got[0], "TYPE")
	if col < 0 || strings.Index(got[1], "session") != col || strings.Index(got[2], "subagent") != col {
		t.Errorf("columns do not line up:\n%s", strings.Join(got, "\n"))
	}
	if strings.Contains(got[0], "main") || !strings.HasPrefix(got[1], "main") {
		t.Errorf("rows in the wrong place: %q", got)
	}
}

func TestPrettyTableStylesCellsAndStaysInThePalette(t *testing.T) {
	o, b := pretty(80)
	o.Table([]Column{{Title: "NAME"}, {Title: "STATE", Style: func(c string) lipgloss.Style {
		if c == "bad" {
			return theme.Err
		}
		return theme.OK
	}}}, [][]string{{"x", "ok"}, {"y", "bad"}}, "")
	out := b.String()
	themed(t, "table", out)
	if !strings.Contains(out, "\x1b[31m") || !strings.Contains(out, "\x1b[32m") {
		t.Errorf("cell styles not applied: %q", out)
	}
}

func TestPrettyTableShrinksTheWidestColumnToFit(t *testing.T) {
	o, b := pretty(40)
	o.Table([]Column{{Title: "NAME"}, {Title: "PATH"}},
		[][]string{{"short", strings.Repeat("/very/long", 20)}}, "")
	for i, l := range lines(b.String()) {
		if ansi.StringWidth(l) > 40 {
			t.Errorf("line %d too wide: %q", i, ansi.Strip(l))
		}
	}
	if !strings.Contains(ansi.Strip(b.String()), "short") {
		t.Error("the short column was cut instead of the long one")
	}
}

func TestPrettyTableEmptyShowsTheMessage(t *testing.T) {
	o, b := pretty(80)
	o.Table([]Column{{Title: "A"}}, nil, "no sessions")
	if got := strings.TrimSpace(ansi.Strip(b.String())); got != "no sessions" {
		t.Errorf("got %q", got)
	}
}

func TestPrettyTableAlignsWideCharactersByCellWidth(t *testing.T) {
	o, b := pretty(80)
	o.Table([]Column{{Title: "A"}, {Title: "B"}}, [][]string{{"日本語", "x"}, {"ab", "y"}}, "")
	got := lines(ansi.Strip(b.String()))
	if ansi.StringWidth(got[1][:strings.Index(got[1], "x")]) != ansi.StringWidth(got[2][:strings.Index(got[2], "y")]) {
		t.Errorf("misaligned:\n%s", strings.Join(got, "\n"))
	}
}

func TestZeroWidthFallsBackToEightyColumns(t *testing.T) {
	o, b := pretty(0)
	o.Table([]Column{{Title: "A"}}, [][]string{{strings.Repeat("x", 200)}}, "")
	if w := ansi.StringWidth(lines(b.String())[1]); w > 80 || w < 70 {
		t.Errorf("width %d", w)
	}
}

func TestNoColorKeepsTheLayoutButDropsEveryEscape(t *testing.T) {
	o, b := pretty(80)
	o.NoColor = true
	o.List("Skills", sampleItems(), "")
	o.Table([]Column{{Title: "A", Style: func(string) lipgloss.Style { return theme.Err }}}, [][]string{{"x"}}, "")
	if strings.Contains(b.String(), "\x1b") {
		t.Errorf("NO_COLOR ignored: %q", b.String())
	}
	if !strings.Contains(b.String(), "Skills") || !strings.Contains(b.String(), "alpha") {
		t.Errorf("layout lost: %q", b.String())
	}
}

func TestPairsAreKeyEqualsValueInTheMachineForm(t *testing.T) {
	o, b := machine()
	o.Pairs([]Pair{{Key: "A", Value: "1"}, {Key: "B", Value: "x y"}}, "")
	if b.String() != "A=1\nB=x y\n" {
		t.Errorf("got %q", b.String())
	}
}

func TestPairsCannotBreakTheRecordFormat(t *testing.T) {
	o, b := machine()
	o.Pairs([]Pair{{Key: "A", Value: "1\nB=2"}}, "")
	if strings.Count(b.String(), "\n") != 1 {
		t.Errorf("a value injected a line: %q", b.String())
	}
}

func TestPairsAlignInThePrettyForm(t *testing.T) {
	o, b := pretty(80)
	o.Pairs([]Pair{{Key: "A", Value: "1"}, {Key: "LONGER", Value: "2"}}, "")
	got := lines(ansi.Strip(b.String()))
	if len(got) != 2 || strings.Index(got[0], "1") != strings.Index(got[1], "2") {
		t.Errorf("not aligned: %q", got)
	}
	themed(t, "pairs", b.String())
}

func TestShowChangesThePrettyCellOnly(t *testing.T) {
	cols := []Column{{Title: "T", Show: func(s string) string { return "<" + s + ">" }}}
	p, pb := pretty(80)
	p.Table(cols, [][]string{{"x"}}, "")
	m, mb := machine()
	m.Table(cols, [][]string{{"x"}}, "")
	if !strings.Contains(ansi.Strip(pb.String()), "<x>") || mb.String() != "x\n" {
		t.Errorf("pretty %q machine %q", pb.String(), mb.String())
	}
}

func TestStyleSeesTheMachineValueNotTheDisplayedOne(t *testing.T) {
	var saw string
	cols := []Column{{Show: func(s string) string { return "shown" }, Style: func(c string) lipgloss.Style { saw = c; return theme.OK }}}
	o, _ := pretty(80)
	o.Table(cols, [][]string{{"raw"}}, "")
	if saw != "raw" {
		t.Errorf("Style got %q", saw)
	}
}

func TestStatusPairsUseTokensForMachinesAndProseForPeople(t *testing.T) {
	rows := []Pair{
		{Key: "aws_profile", Label: "aws profile", Value: "ro", Style: theme.OK},
		{Key: "packages", Value: "2", Text: "2 need a decision", Style: theme.Warn},
	}
	m, mb := machine()
	m.Pairs(rows, "")
	if mb.String() != "aws_profile=ro\npackages=2\n" {
		t.Errorf("machine %q", mb.String())
	}
	p, pb := pretty(80)
	p.Pairs(rows, "")
	got := ansi.Strip(pb.String())
	if !strings.Contains(got, "aws profile") || !strings.Contains(got, "2 need a decision") || strings.Contains(got, "_") {
		t.Errorf("pretty %q", got)
	}
}

func TestDonePrintsASubjectForScriptsAndAMarkedLineForPeople(t *testing.T) {
	m, mb := machine()
	m.Done("activated", "alpha", "restart to apply")
	if mb.String() != "alpha\n" {
		t.Errorf("machine %q", mb.String())
	}
	m2, mb2 := machine()
	m2.Done("refreshed", "", "")
	if mb2.Len() != 0 {
		t.Errorf("no subject, nothing to read: %q", mb2.String())
	}
	p, pb := pretty(80)
	p.Done("activated", "alpha", "restart to apply")
	got := ansi.Strip(pb.String())
	if got != "✓ activated alpha  restart to apply\n" {
		t.Errorf("pretty %q", got)
	}
	themed(t, "done", pb.String())
}

func TestOutcomeIsATokenForScriptsAndTextForPeople(t *testing.T) {
	m, mb := machine()
	m.Outcome("already-running", "already running", "")
	p, pb := pretty(80)
	p.Outcome("already-running", "already running", "sessions kept")
	if mb.String() != "already-running\n" || ansi.Strip(pb.String()) != "✓ already running  sessions kept\n" {
		t.Errorf("machine %q pretty %q", mb.String(), pb.String())
	}
	themed(t, "outcome", pb.String())
}

func TestNoteSaysNothingToScripts(t *testing.T) {
	m, mb := machine()
	m.Note("no drift")
	p, pb := pretty(80)
	p.Note("no drift")
	if mb.Len() != 0 || !strings.Contains(ansi.Strip(pb.String()), "no drift") {
		t.Errorf("machine %q pretty %q", mb.String(), pb.String())
	}
}

func TestPrettyListShortensATitleThatIsAPath(t *testing.T) {
	o, b := pretty(80)
	o.List("Paths", []Item{{Title: "/home/me/.agents/skills"}, {Title: "/home/meow/x"}}, "")
	out := ansi.Strip(b.String())
	if !strings.Contains(out, "~/.agents/skills") || !strings.Contains(out, "/home/meow/x") {
		t.Errorf("got %q", out)
	}
}

func TestPrettyListWithoutNotesGivesTheTitleTheWholeLine(t *testing.T) {
	// A path is the title and there is no note: do not cut it to two fifths.
	long := "/srv/" + strings.Repeat("d/", 20) + "end"
	o, b := pretty(80)
	o.List("Paths", []Item{{Title: long}}, "")
	if !strings.Contains(ansi.Strip(b.String()), long) {
		t.Errorf("title was cut: %q", ansi.Strip(b.String()))
	}
	for _, l := range lines(b.String()) {
		if ansi.StringWidth(l) > 80 {
			t.Errorf("too wide: %q", ansi.Strip(l))
		}
	}
}
