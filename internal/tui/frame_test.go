package tui

import (
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var sgr = regexp.MustCompile(`\x1b\[([0-9;:]*)m`)

// paintsBackground reports whether s sets a background colour or reverse video.
// Both would override the colours of the user's terminal theme.
func paintsBackground(s string) bool {
	for _, m := range sgr.FindAllStringSubmatch(s, -1) {
		parts := strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ':' })
		for i := 0; i < len(parts); i++ {
			switch p := parts[i]; {
			case p == "38" || p == "58": // skip the colour arguments of a foreground
				if i+1 < len(parts) && parts[i+1] == "5" {
					i += 2
				} else if i+1 < len(parts) && parts[i+1] == "2" {
					i += 4
				}
			case p == "7", p == "48", p == "49" && false:
				return true
			case len(p) == 2 && (p[0] == '4' && p[1] >= '0' && p[1] <= '7'):
				return true
			case len(p) == 3 && p[0] == '1' && p[1] == '0' && p[2] >= '0' && p[2] <= '7':
				return true
			}
		}
	}
	return false
}

func TestThemeStylesNeverPaintABackground(t *testing.T) {
	for name, st := range map[string]lipgloss.Style{
		"title": titleStyle, "dim": dimStyle, "accent": accentStyle, "ok": okStyle, "warn": warnStyle,
		"err": errStyle, "selected": selectedStyle, "border": borderStyle, "tab": tabStyle, "activeTab": activeTabStyle,
	} {
		if out := st.Render("x"); paintsBackground(out) {
			t.Errorf("%s paints a background: %q", name, out)
		}
	}
}

func TestThemeUsesOnlyTheTerminalsOwnPalette(t *testing.T) {
	// 256-colour and true-colour values do not follow the user's theme.
	for name, st := range map[string]lipgloss.Style{"accent": accentStyle, "ok": okStyle, "warn": warnStyle, "err": errStyle, "selected": selectedStyle} {
		out := st.Render("x")
		if strings.Contains(out, "38;5;") || strings.Contains(out, "38;2;") {
			t.Errorf("%s uses a fixed colour: %q", name, out)
		}
	}
}

func lines(s string) []string { return strings.Split(s, "\n") }

func TestFrameTruncatesLongLinesInsteadOfWrapping(t *testing.T) {
	long := strings.Repeat("x", 500)
	out := frame(50, 12, "t", []string{long}, []string{long, long}, long)
	for i, l := range lines(out) {
		if ansi.StringWidth(l) != 50 {
			t.Errorf("line %d is %d wide", i, ansi.StringWidth(l))
		}
	}
}

func TestFramePreservesStyledText(t *testing.T) {
	out := frame(40, 10, "t", []string{"a"}, []string{okStyle.Render("fine")}, "h")
	if !strings.Contains(out, "\x1b[") {
		t.Error("styling stripped")
	}
	if paintsBackground(out) {
		t.Error("frame paints a background")
	}
}
