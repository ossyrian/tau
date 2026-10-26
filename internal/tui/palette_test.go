package tui

import (
	"regexp"
	"strings"
	"testing"
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
			case p == "7", p == "48":
				return true
			case len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7':
				return true
			case len(p) == 3 && p[0] == '1' && p[1] == '0' && p[2] >= '0' && p[2] <= '7':
				return true
			}
		}
	}
	return false
}

// themed checks that rendered output follows the terminal's palette: no
// background, no reverse video, no 256-colour or true-colour values.
func themed(t *testing.T, what, out string) {
	t.Helper()
	if paintsBackground(out) {
		t.Errorf("%s paints a background: %q", what, out)
	}
	if strings.Contains(out, "38;5;") || strings.Contains(out, "38;2;") || strings.Contains(out, "58;") {
		t.Errorf("%s uses a fixed colour: %q", what, out)
	}
}

func TestEveryScreenFollowsTheTerminalPalette(t *testing.T) {
	f := newFake(t)
	f.vars = nil
	m := newRoot(t, f)
	for i, s := range m.sections {
		drive(t, m, key(string(rune('1'+i))))
		themed(t, s.Name(), m.View().Content)
	}
}

func TestHighlightedMarkdownAndYAMLStayInThePalette(t *testing.T) {
	themed(t, "markdown", highlight("markdown", "# Head\n\n`code` **bold** *em* [l](u)\n\n- item\n"))
	themed(t, "yaml", highlight("yaml", "a:\n  b: 1\n  c: \"s\"\n  # comment\n  d: [x, y]\n"))
}

func TestHighlightKeepsEveryCharacterOfItsInput(t *testing.T) {
	for lang, src := range map[string]string{
		"markdown": "# Head\n\ntext with `code`\n",
		"yaml":     "a:\n  b: 1\n",
		"nonesuch": "plain\n",
	} {
		if got := stripSGR(highlight(lang, src)); got != src {
			t.Errorf("%s: %q became %q", lang, src, got)
		}
	}
}

func stripSGR(s string) string { return sgr.ReplaceAllString(s, "") }
