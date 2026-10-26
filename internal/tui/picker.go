package tui

import (
	"slices"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/sahilm/fuzzy"
)

// pickerRow is one row of a picker.
type pickerRow struct {
	// Key identifies the row across reloads, so the selection stays put.
	Key string
	// Cells are shown, one per column.
	Cells []string
	// Search is what the fuzzy filter matches. Empty means every cell.
	Search string
}

func (r pickerRow) searchText() string {
	if r.Search != "" {
		return r.Search
	}
	return strings.Join(r.Cells, " ")
}

// picker is a table of rows with a fuzzy filter: the one list component every
// section uses. The table, the cursor keys and the text input are bubbles'; the
// picker only joins them and fits the columns to the width.
type picker struct {
	cols   []string
	table  table.Model
	filter textinput.Model
	typing bool

	all   []pickerRow
	shown []pickerRow
	w, h  int
}

const (
	cellPad   = 2 // the table pads each cell by one column either side
	minColumn = 4
)

// newPicker makes a picker with the given column titles.
func newPicker(cols ...string) *picker {
	km := table.DefaultKeyMap()
	// The table keeps only the keys that move the cursor; sections use the
	// others (d, space, b, f) for their own commands.
	km.PageUp = bkey.NewBinding(bkey.WithKeys("pgup"))
	km.PageDown = bkey.NewBinding(bkey.WithKeys("pgdown"))
	km.HalfPageUp = bkey.NewBinding(bkey.WithKeys("ctrl+u"))
	km.HalfPageDown = bkey.NewBinding(bkey.WithKeys("ctrl+d"))

	t := table.New(table.WithFocused(true), table.WithKeyMap(km), table.WithStyles(table.Styles{
		Header:   theme.Dim.Padding(0, 1),
		Cell:     lipgloss.NewStyle().Padding(0, 1),
		Selected: theme.Selected,
	}))
	in := newInput("", 30)
	in.Prompt = "/ "
	p := &picker{cols: cols, table: t, filter: in}
	p.layout()
	return p
}

// Capturing is true while the filter has the keyboard.
func (p *picker) Capturing() bool { return p.typing }

// Filtering is true while a filter is typed or applied.
func (p *picker) Filtering() bool { return p.typing || p.filter.Value() != "" }

// Query is the text being matched.
func (p *picker) Query() string { return p.filter.Value() }

// Len is how many rows pass the filter.
func (p *picker) Len() int { return len(p.shown) }

// Selected returns the row under the cursor.
func (p *picker) Selected() (pickerRow, bool) {
	i := p.table.Cursor()
	if i < 0 || i >= len(p.shown) {
		return pickerRow{}, false
	}
	return p.shown[i], true
}

// SetRows replaces the rows, keeping the cursor on the same Key.
func (p *picker) SetRows(rows []pickerRow) {
	keep := ""
	if r, ok := p.Selected(); ok {
		keep = r.Key
	}
	p.all = rows
	p.apply(keep)
}

// apply filters all into shown and fits the table to them.
func (p *picker) apply(keep string) {
	p.shown = p.all
	if q := p.filter.Value(); q != "" {
		texts := make([]string, len(p.all))
		for i, r := range p.all {
			texts[i] = r.searchText()
		}
		p.shown = nil
		for _, m := range fuzzy.Find(q, texts) {
			p.shown = append(p.shown, p.all[m.Index])
		}
	}
	p.layout()
	cells := make([]table.Row, len(p.shown))
	for i, r := range p.shown {
		cells[i] = r.Cells
	}
	p.table.SetRows(cells)
	p.table.SetCursor(0)
	if i := slices.IndexFunc(p.shown, func(r pickerRow) bool { return r.Key == keep }); i >= 0 {
		p.table.SetCursor(i)
	}
}

// SetSize fits the picker to w×h cells.
func (p *picker) SetSize(w, h int) {
	p.w, p.h = w, h
	p.layout()
	p.filter.SetWidth(max(w-4, 1))
}

// layout sizes the columns and the table. Columns take the width their cells
// need; the widest give way first when the room is short, and the last column
// takes any room left over.
func (p *picker) layout() {
	if p.w == 0 {
		// Not sized yet. The table still needs its columns before it takes rows.
		cols := make([]table.Column, len(p.cols))
		for i, c := range p.cols {
			cols[i] = table.Column{Title: c, Width: max(ansi.StringWidth(c), 1)}
		}
		p.table.SetColumns(cols)
		return
	}
	widths := make([]int, len(p.cols))
	for i, c := range p.cols {
		widths[i] = ansi.StringWidth(c)
	}
	for _, r := range p.shown {
		for i := range p.cols {
			if i < len(r.Cells) {
				widths[i] = max(widths[i], ansi.StringWidth(r.Cells[i]))
			}
		}
	}
	room := p.w - cellPad*len(p.cols)
	for sum(widths) > room {
		widest := 0
		for i, w := range widths {
			if w > widths[widest] {
				widest = i
			}
		}
		if widths[widest] <= minColumn {
			break
		}
		widths[widest]--
	}
	if extra := room - sum(widths); extra > 0 && len(widths) > 0 {
		widths[len(widths)-1] += extra
	}
	cols := make([]table.Column, len(p.cols))
	for i, c := range p.cols {
		cols[i] = table.Column{Title: c, Width: max(widths[i], 1)}
	}
	p.table.SetColumns(cols)
	p.table.SetWidth(p.w)
	h := p.h
	if p.Filtering() {
		h-- // the filter line
	}
	p.table.SetHeight(max(h, 2))
}

func sum(xs []int) int {
	t := 0
	for _, x := range xs {
		t += x
	}
	return t
}

// Update handles a key: it moves the cursor, or edits the filter.
func (p *picker) Update(msg tea.Msg) tea.Cmd {
	k, isKey := msg.(tea.KeyPressMsg)
	if !isKey {
		return nil
	}
	if p.typing {
		return p.typeFilter(k)
	}
	switch k.String() {
	case "/":
		p.typing = true
		p.layout()
		return p.filter.Focus()
	case "esc":
		if p.filter.Value() != "" {
			p.clear()
		}
		return nil
	}
	var cmd tea.Cmd
	p.table, cmd = p.table.Update(msg)
	return cmd
}

func (p *picker) typeFilter(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		p.typing = false
		p.filter.Blur()
		p.clear()
		return nil
	case "enter":
		p.typing = false
		p.filter.Blur()
		p.layout()
		return nil
	}
	before := p.filter.Value()
	var cmd tea.Cmd
	p.filter, cmd = p.filter.Update(k)
	if p.filter.Value() != before {
		p.apply("") // the best match is first, so the cursor goes there
	}
	return cmd
}

func (p *picker) clear() {
	p.filter.SetValue("")
	p.apply("")
}

// Keys are the bindings the picker adds to the help line.
func (p *picker) Keys() []bkey.Binding {
	if p.typing {
		return []bkey.Binding{binding("enter", "enter", "keep"), binding("esc", "esc", "clear")}
	}
	if p.filter.Value() != "" {
		return []bkey.Binding{binding("/", "/", "search"), binding("esc", "esc", "clear filter")}
	}
	return []bkey.Binding{binding("/", "/", "search")}
}

// View draws the table, with the filter line under it while one is in use.
func (p *picker) View() string {
	v := p.table.View()
	if !p.Filtering() {
		return v
	}
	line := p.filter.View()
	if !p.typing {
		line = theme.Dim.Render("/ " + p.filter.Value())
	}
	return v + "\n" + line
}
