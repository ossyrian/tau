package tui

import (
	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// master is the layout most sections share: a table above and, if the section
// has one, a scrollable detail pane below. Tab-free focus: the table has the
// cursor keys, and the detail pane scrolls with shift-arrows and the page keys.
type master struct {
	pick *picker
	det  *detail // nil for a section with a single pane
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

// Keys are the bindings that every master adds to the help line.
func (m *master) Keys() []bkey.Binding {
	keys := m.pick.Keys()
	if m.det != nil {
		keys = append(keys, binding("pgdown", "pgup/pgdn", "scroll"))
	}
	return keys
}

// Update gives a key to the detail pane when it scrolls, else to the table.
func (m *master) Update(msg tea.Msg) tea.Cmd {
	if k, ok := msg.(tea.KeyPressMsg); ok && m.det != nil && !m.pick.Capturing() {
		switch k.String() {
		case "pgup", "pgdown", "ctrl+u", "ctrl+d", "shift+up", "shift+down":
			return m.det.Update(translate(k))
		}
	}
	return m.pick.Update(msg)
}

// translate maps the scroll keys onto the ones the viewport listens to.
func translate(k tea.KeyPressMsg) tea.Msg {
	switch k.String() {
	case "shift+up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "shift+down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return k
}

// View draws the panes in w×h cells. body is the detail text for the selected
// row; section code passes it already highlighted.
func (m *master) View(w, h int, listTitle, detTitle, body string) string {
	if m.det == nil {
		iw, ih := inner(w, h)
		m.pick.SetSize(iw, ih)
		return pane(listTitle, w, h, true, m.pick.View())
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
		pane(listTitle, w, top, true, m.pick.View()),
		pane(detTitle, w, bottom, false, m.det.View()),
	)
}
