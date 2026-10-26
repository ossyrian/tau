package tui

import (
	"strconv"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// detail is the lower pane: scrollable text about the selected row. The
// scrolling, soft wrapping and key handling are bubbles' viewport.
type detail struct {
	vp      viewport.Model
	content string // what the viewport shows now; reset only when it changes
}

func newDetail() *detail {
	vp := viewport.New()
	vp.SoftWrap = true
	return &detail{vp: vp}
}

// SetSize fits the viewport to w×h cells.
func (d *detail) SetSize(w, h int) {
	d.vp.SetWidth(w)
	d.vp.SetHeight(h)
}

// Set shows text. The scroll position resets only when the text differs from
// what is already shown, so a refresh does not throw the reader back to the top.
func (d *detail) Set(text string) {
	if text == d.content {
		return
	}
	d.content = text
	d.vp.SetContent(text)
	d.vp.GotoTop()
}

// Update scrolls on a key.
func (d *detail) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	d.vp, cmd = d.vp.Update(msg)
	return cmd
}

// View draws the visible part.
func (d *detail) View() string { return d.vp.View() }

// Scroll reads "42%" for the pane's title, or "" when everything fits.
func (d *detail) Scroll() string {
	if d.vp.TotalLineCount() <= d.vp.Height() {
		return ""
	}
	return strconv.Itoa(int(d.vp.ScrollPercent()*100)) + "%"
}
