package render

import (
	"bytes"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
)

var cols = []Column{{Title: "NAME"}, {Title: "TYPE"}, {Title: "WHEN"}}

func rows() [][]string {
	return [][]string{{"main", "session", "now"}, {"sa-worker", "subagent", "later"}}
}

func TestTableLayoutIsTheSameOnATerminalOnceColourIsStripped(t *testing.T) {
	var plain, pretty bytes.Buffer
	(&Out{W: &plain}).Table(cols, rows())
	(&Out{W: &pretty, Pretty: true}).Table(cols, rows())
	if got := ansi.Strip(pretty.String()); got != plain.String() {
		t.Errorf("terminal:\n%q\npiped:\n%q", got, plain.String())
	}
	if !strings.Contains(pretty.String(), "\x1b") {
		t.Error("a terminal gets colour")
	}
}

func TestTableHasAHeaderAndOneLinePerRow(t *testing.T) {
	var b bytes.Buffer
	(&Out{W: &b}).Table(cols, rows())
	if n := strings.Count(b.String(), "\n"); n != 3 {
		t.Errorf("want 3 lines, got %q", b.String())
	}
	if first := strings.SplitN(b.String(), "\n", 2)[0]; first != strings.ToUpper(first) {
		t.Errorf("header is not upper case: %q", first)
	}
}

func TestTableWithNoRowsIsTheHeaderAlone(t *testing.T) {
	var b bytes.Buffer
	(&Out{W: &b}).Table(cols, nil)
	if n := strings.Count(b.String(), "\n"); n != 1 {
		t.Errorf("got %q", b.String())
	}
}

func TestTableHasNoColourInAPipeOrWithNoColor(t *testing.T) {
	styled := []Column{{Title: "A", Style: func(string) lipgloss.Style { return theme.Err }}}
	for name, o := range map[string]*Out{
		"pipe":     {W: &bytes.Buffer{}},
		"no-color": {W: &bytes.Buffer{}, Pretty: true, NoColor: true},
	} {
		o.Table(styled, [][]string{{"x"}})
		if s := o.W.(*bytes.Buffer).String(); strings.Contains(s, "\x1b") {
			t.Errorf("%s: %q", name, s)
		}
	}
}

func TestTableAlignsColumnsWhenCellsAreColoured(t *testing.T) {
	var b bytes.Buffer
	c := []Column{{Title: "A", Style: func(string) lipgloss.Style { return theme.OK }}, {Title: "B"}}
	(&Out{W: &b, Pretty: true}).Table(c, [][]string{{"long-cell", "1"}, {"x", "2"}})
	got := strings.Split(ansi.Strip(b.String()), "\n")
	if strings.Index(got[1], "1") != strings.Index(got[2], "2") {
		t.Errorf("misaligned:\n%s", ansi.Strip(b.String()))
	}
}

func TestTableLastColumnHasNoTrailingSpaces(t *testing.T) {
	var b bytes.Buffer
	(&Out{W: &b}).Table(cols, [][]string{{"a", "b", ""}, {"a", "b", "long"}})
	for _, l := range strings.Split(strings.TrimRight(b.String(), "\n"), "\n") {
		if l != strings.TrimRight(l, " ") {
			t.Errorf("trailing space: %q", l)
		}
	}
}

func TestTableKeepsEachRowOnOneLine(t *testing.T) {
	var b bytes.Buffer
	(&Out{W: &b}).Table(cols, [][]string{{"a\nb", "c\td", "e"}})
	if n := strings.Count(b.String(), "\n"); n != 2 {
		t.Errorf("got %q", b.String())
	}
}

func TestTableColoursStayInThePalette(t *testing.T) {
	var b bytes.Buffer
	c := []Column{{Title: "A", Style: func(string) lipgloss.Style { return theme.Warn }}}
	(&Out{W: &b, Pretty: true}).Table(c, [][]string{{"x"}})
	if strings.Contains(b.String(), "38;5;") || strings.Contains(b.String(), "38;2;") || strings.Contains(b.String(), "48;") {
		t.Errorf("fixed colour or background: %q", b.String())
	}
}

func TestDoneGivesScriptsOnlyTheSubjectAndPeopleASingleLine(t *testing.T) {
	var s, p bytes.Buffer
	(&Out{W: &s}).Done("set", "KEY", "hint")
	if strings.TrimSpace(s.String()) != "KEY" {
		t.Errorf("script %q", s.String())
	}
	(&Out{W: &p, Pretty: true, NoColor: true}).Done("set", "KEY", "hint")
	if strings.Count(p.String(), "\n") != 1 || !strings.Contains(p.String(), "KEY") {
		t.Errorf("terminal %q", p.String())
	}
}

func TestNotesAreForPeopleAndNeverReachAScript(t *testing.T) {
	var s bytes.Buffer
	(&Out{W: &s}).Note("hi")
	(&Out{W: &s}).Outcome("token", "text", "")
	if strings.TrimSpace(s.String()) != "token" {
		t.Errorf("script %q", s.String())
	}
}
