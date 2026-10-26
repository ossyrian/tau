package tui

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/help"
	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	"github.com/charmbracelet/x/ansi"
)

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

type row struct{ title, desc string }

func (r row) Title() string       { return r.title }
func (r row) Description() string { return r.desc }
func (r row) FilterValue() string { return r.title }

func TestListFollowsTheTerminalPalette(t *testing.T) {
	l := newList([]list.Item{row{"alpha", "first"}, row{"beta", "second"}}, 40, 10)
	themed(t, "list", l.View())
	l.Select(1)
	themed(t, "list with selection", l.View())
	if !strings.Contains(l.View(), "beta") {
		t.Error("items not shown")
	}
}

func TestListSelectionIsMarkedWithoutColourAlone(t *testing.T) {
	// A monochrome terminal must still show the selection.
	l := newList([]list.Item{row{"alpha", "x"}, row{"beta", "y"}}, 40, 10)
	plain := ansi.Strip(l.View())
	if !strings.Contains(plain, selectedMarker+" alpha") || strings.Contains(plain, selectedMarker+" beta") {
		t.Errorf("selection marker:\n%s", plain)
	}
	col := func(s, word string) int {
		for _, ln := range strings.Split(s, "\n") {
			if i := strings.Index(ln, word); i >= 0 {
				return ansi.StringWidth(ln[:i])
			}
		}
		return -1
	}
	before := col(plain, "alpha")
	l.Select(1)
	moved := ansi.Strip(l.View())
	if !strings.Contains(moved, selectedMarker+" beta") || strings.Contains(moved, selectedMarker+" alpha") {
		t.Errorf("marker did not move:\n%s", moved)
	}
	if col(moved, "alpha") != before {
		t.Errorf("rows shift when the selection moves:\n%s\n%s", plain, moved)
	}
}

func TestHelpFollowsTheTerminalPalette(t *testing.T) {
	h := newHelp(80)
	out := h.ShortHelpView([]bkey.Binding{
		bkey.NewBinding(bkey.WithKeys("q"), bkey.WithHelp("q", "quit")),
		bkey.NewBinding(bkey.WithKeys("tab"), bkey.WithHelp("tab", "next")),
	})
	themed(t, "help", out)
	if !strings.Contains(ansi.Strip(out), "q quit") {
		t.Errorf("got %q", ansi.Strip(out))
	}
}

func TestSpinnerFollowsTheTerminalPalette(t *testing.T) {
	themed(t, "spinner", newSpinner().View())
}

func TestInputFollowsTheTerminalPalette(t *testing.T) {
	in := newInput("name", 30)
	in.SetValue("hello")
	in.Focus()
	out := in.View()
	// The virtual cursor is reverse video on one cell, in the terminal's own
	// colours. Everything else must be plain.
	themed(t, "input", strings.ReplaceAll(out, "\x1b[7m", ""))
	if !strings.Contains(ansi.Strip(out), "hello") {
		t.Errorf("got %q", ansi.Strip(out))
	}
	in.Blur()
	themed(t, "blurred input", in.View())
}

func TestSecretInputHidesWhatIsTyped(t *testing.T) {
	in := newSecretInput("value", 30)
	in.SetValue("supersecret")
	if strings.Contains(in.View(), "supersecret") {
		t.Error("secret shown")
	}
	if in.EchoMode != textinput.EchoPassword {
		t.Error("not a password input")
	}
}

var _ = spinner.Dot
var _ = help.Model{}
