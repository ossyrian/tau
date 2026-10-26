package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// sizes are the terminal sizes the layout is checked at, from the smallest
// allowed to a wide one.
var sizes = [][2]int{{60, 12}, {80, 24}, {120, 40}, {200, 60}}

func resize(t *testing.T, m *Root, w, h int) {
	t.Helper()
	drive(t, m, tea.WindowSizeMsg{Width: w, Height: h})
}

func TestEverySectionFillsExactlyTheTerminalAtEverySize(t *testing.T) {
	f := newFake(t)
	f.sessions = nil
	m := newRoot(t, f)
	for _, size := range sizes {
		resize(t, m, size[0], size[1])
		for i := range m.sections {
			drive(t, m, key(string(rune('1'+i))))
			lines := strings.Split(m.View().Content, "\n")
			if len(lines) != size[1] {
				t.Errorf("%v %s: %d lines", size, m.sections[i].Name(), len(lines))
			}
			for n, l := range lines {
				if w := ansi.StringWidth(l); w > size[0] {
					t.Errorf("%v %s: line %d is %d wide", size, m.sections[i].Name(), n, w)
				}
			}
		}
	}
}

func TestPanesResizeWithTheTerminal(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	left80, right80, _ := m.layoutAt(80, 24)
	left200, right200, _ := m.layoutAt(200, 60)
	if left200 <= left80 || right200 <= right80 {
		t.Errorf("widths did not grow: left %d→%d, right %d→%d", left80, left200, right80, right200)
	}
	for _, size := range sizes {
		l, r, _ := m.layoutAt(size[0], size[1])
		if l+r != size[0] {
			t.Errorf("%v: columns add up to %d", size, l+r)
		}
	}
}

func TestSplitHeightAddsUpAndKeepsBothPanesUsable(t *testing.T) {
	for h := 8; h < 80; h++ {
		top, bottom := splitHeight(h)
		if top+bottom != h || top < 5 || bottom < 3 {
			t.Errorf("h=%d: %d + %d", h, top, bottom)
		}
	}
}

func TestTooSmallTerminalShowsAMessageAndNoPanes(t *testing.T) {
	m := newRoot(t, newFake(t))
	resize(t, m, 40, 8)
	if strings.Contains(m.View().Content, "╭") {
		t.Error("panes drawn in a terminal that is too small")
	}
}

func TestPaneIsExactlyItsSizeAndTheTitleSitsOnTheTopEdge(t *testing.T) {
	for _, size := range [][2]int{{12, 3}, {30, 5}, {80, 10}} {
		out := pane("a long title that cannot fit", size[0], size[1], true, strings.Repeat("body line\n", 20))
		lines := strings.Split(out, "\n")
		if len(lines) != size[1] {
			t.Errorf("%v: %d lines", size, len(lines))
		}
		for _, l := range lines {
			if w := ansi.StringWidth(l); w != size[0] {
				t.Errorf("%v: a line is %d wide", size, w)
			}
		}
		if !strings.Contains(ansi.Strip(lines[0]), "a") {
			t.Errorf("%v: no title on the top edge: %q", size, lines[0])
		}
		if strings.Contains(ansi.Strip(lines[1]), "a long title") {
			t.Errorf("%v: the title leaked into the body", size)
		}
	}
}
