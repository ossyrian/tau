package tui

import (
	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// master is the layout most sections share: a table above and, if the section
// has one, a scrollable detail pane below. Focus picks which of the two gets
// the keys: the table moves its cursor, the detail scrolls.
type master struct {
	focus
	pick *picker
	det  *detail // nil for a section with a single pane
	h    int     // the height View last drew, to tell which pane a row belongs to
}

func newMaster(withDetail bool, cols ...string) *master {
	m := &master{pick: newPicker(cols...)}
	if withDetail {
		m.det = newDetail()
	}
	return m
}

// Capturing is true while the filter has the keyboard.
func (m *master) Capturing() bool { return m.pick.Capturing() }

// Panes is 2 while the detail pane is drawn, else 1. With no rows a section
// shows a message in place of both.
func (m *master) Panes() int {
	if m.det == nil || m.pick.Total() == 0 {
		return 1
	}
	return 2
}

// PaneAt is the pane drawn y rows down: the list above the split, the detail below.
func (m *master) PaneAt(y int) int {
	if m.Panes() == 2 {
		if top, _ := splitHeight(m.h); y >= top {
			return 1
		}
	}
	return 0
}

// enterKey is what clicking the selected row sends: the same as pressing enter.
func enterKey() tea.Msg { return tea.KeyPressMsg{Code: tea.KeyEnter} }

// mouse handles a click or wheel turn at section-relative coordinates. The
// wheel acts on the pane under the pointer, whichever has the focus. A click
// selects a row; clicking the selected row again does what enter does.
func (m *master) mouse(msg tea.MouseMsg) tea.Cmd {
	at := msg.Mouse()
	switch msg.(type) {
	case tea.MouseWheelMsg:
		if m.PaneAt(at.Y) == 1 {
			return m.det.Update(msg)
		}
		return m.pick.Update(msg)
	case tea.MouseClickMsg:
		if at.Button == tea.MouseLeft && m.PaneAt(at.Y) == 0 {
			if again, ok := m.pick.Click(at.Y - 1); ok && again { // -1: the pane's border
				return enterKey
			}
		}
	}
	return nil
}

// focusIndex is the focused pane, or -1.
func (m *master) focusIndex() int { return int(m.focus) }

// detailFocused is true when the detail pane has the keyboard and is drawn.
func (m *master) detailFocused() bool { return m.on(1) && m.Panes() == 2 }

// Keys are the bindings that every master adds to the help line.
func (m *master) Keys() []bkey.Binding {
	if m.detailFocused() {
		return []bkey.Binding{binding("j", "↑↓/jk", "scroll"), binding("pgdown", "pgup/pgdn", "page")}
	}
	return m.pick.Keys()
}

// Update sends a key to the focused pane.
func (m *master) Update(msg tea.Msg) tea.Cmd {
	if mm, ok := msg.(tea.MouseMsg); ok {
		return m.mouse(mm)
	}
	if m.detailFocused() {
		return m.det.Update(msg)
	}
	return m.pick.Update(msg)
}

// View draws the panes in w×h cells. body is the detail text for the selected
// row; section code passes it already highlighted.
func (m *master) View(w, h int, listTitle, detTitle, body string) string {
	m.h = h
	if m.det == nil {
		iw, ih := inner(w, h)
		m.pick.SetSize(iw, ih)
		return pane(listTitle, w, h, m.on(0), m.pick.View())
	}
	top, bottom := splitHeight(h)
	iw, ih := inner(w, top)
	m.pick.SetSize(iw, ih)
	dw, dh := inner(w, bottom)
	m.det.SetSize(dw, dh)
	m.det.Set(body)
	if s := m.det.Scroll(); s != "" {
		detTitle += "  " + s
	}
	return column(
		pane(listTitle, w, top, m.on(0), m.pick.View()),
		pane(detTitle, w, bottom, m.on(1), m.det.View()),
	)
}
