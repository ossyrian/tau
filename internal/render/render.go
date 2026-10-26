// Package render prints command output. On a terminal it draws with bubbles
// components in the user's own palette. Anywhere else (a pipe, a file, a
// script) it prints plain tab-separated records with no colour, no header and
// no padding, so another program can read it without guessing.
package render

import (
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
)

const (
	defaultWidth = 80
	gap          = 2 // blank columns between columns
	minColumn    = 4
)

// Out writes command output in one of two forms.
type Out struct {
	W io.Writer
	// Pretty selects the terminal form. The zero value is the machine form.
	Pretty bool
	// Width is the terminal width in columns; 0 means 80.
	Width int
	// Home is the user's home directory, shortened to ~ in notes.
	Home string
	// NoColor keeps the terminal layout but drops every escape sequence, as
	// the NO_COLOR convention asks.
	NoColor bool
}

// Write makes Out an io.Writer-like sink that honours NoColor.
func (o *Out) out() io.Writer {
	if o.NoColor {
		return stripWriter{o.W}
	}
	return o.W
}

type stripWriter struct{ w io.Writer }

func (s stripWriter) Write(p []byte) (int, error) {
	if _, err := io.WriteString(s.w, ansi.Strip(string(p))); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Column is one column of a table.
type Column struct {
	// Title heads the column on a terminal. A table whose columns have no
	// titles has no header line.
	Title string
	// Style picks the style of a cell on a terminal; nil leaves cells plain.
	Style func(cell string) lipgloss.Style
	// Show rewrites a cell for a terminal, for example a timestamp to local
	// time. The machine form keeps the cell as given. Style sees the cell as
	// given, not as shown.
	Show func(cell string) string
}

// Detail is a "Label: value" pair on the second line of a list item.
type Detail struct {
	Label, Value string
	Style        lipgloss.Style // style of Value on a terminal
}

// Item is one entry of a list.
type Item struct {
	Title   string
	Note    string   // dim text after the title, usually a path
	Details []Detail // terminal form only
	// Fields are the machine form's columns. When empty, Title and Note are used.
	Fields []string
}

func (o *Out) width() int {
	if o.Width > 0 {
		return o.Width
	}
	return defaultWidth
}

// record is one tab-separated line. A cell cannot hold a separator, so every
// line is one record and every record has the same columns.
func record(cells []string) string {
	clean := make([]string, len(cells))
	for i, c := range cells {
		clean[i] = strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(c)
	}
	return strings.Join(clean, "\t")
}

// Pair is one key/value row.
type Pair struct {
	Key, Value string
	// Label is the key as people read it; empty means Key.
	Label string
	// Text is the value as people read it; empty means Value.
	Text  string
	Style lipgloss.Style // style of the value on a terminal
}

// Pairs prints key/value rows: KEY=VALUE lines in the machine form, an aligned
// two-column block on a terminal.
func (o *Out) Pairs(rows []Pair, empty string) {
	if !o.Pretty {
		for _, r := range rows {
			fmt.Fprintln(o.W, record([]string{r.Key})+"="+record([]string{r.Value}))
		}
		return
	}
	cells := make([][]string, len(rows))
	styles := map[string]lipgloss.Style{}
	for i, r := range rows {
		text := r.Text
		if text == "" {
			text = r.Value
		}
		label := r.Label
		if label == "" {
			label = r.Key
		}
		cells[i] = []string{label, text}
		styles[text] = r.Style
	}
	o.Table([]Column{
		{Style: func(string) lipgloss.Style { return theme.Dim }},
		{Style: func(c string) lipgloss.Style { return styles[c] }},
	}, cells, empty)
}

// Outcome reports what a command did when the answer is one of a few fixed
// results. A script gets the token (for example "already-running"), a person
// gets the text.
func (o *Out) Outcome(token, text, hint string) {
	if !o.Pretty {
		fmt.Fprintln(o.W, record([]string{token}))
		return
	}
	line := theme.OK.Render("✓") + " " + text
	if hint != "" {
		line += "  " + theme.Dim.Render(hint)
	}
	fmt.Fprintln(o.out(), line)
}

// Note prints a message for people. A script gets nothing: an empty result is
// an empty output.
func (o *Out) Note(msg string) {
	if o.Pretty {
		o.say(msg)
	}
}

// Done reports a finished action. A terminal gets a marked line; a script gets
// the subject alone (the canonical name of what changed), or nothing when
// there is none.
func (o *Out) Done(verb, subject, hint string) {
	if !o.Pretty {
		if subject != "" {
			fmt.Fprintln(o.W, record([]string{subject}))
		}
		return
	}
	line := theme.OK.Render("✓") + " " + verb
	if subject != "" {
		line += " " + subject
	}
	if hint != "" {
		line += "  " + theme.Dim.Render(hint)
	}
	fmt.Fprintln(o.out(), line)
}

// Table prints rows under the column titles. When there are no rows it prints
// empty on a terminal, and nothing in the machine form: a message there would
// read as a row.
func (o *Out) Table(cols []Column, rows [][]string, empty string) {
	if !o.Pretty {
		for _, r := range rows {
			fmt.Fprintln(o.out(), record(r))
		}
		return
	}
	if len(rows) == 0 {
		o.say(empty)
		return
	}
	titled := false
	for _, c := range cols {
		titled = titled || c.Title != ""
	}

	shown := func(r []string, i int) string {
		if cols[i].Show != nil {
			return cols[i].Show(cell(r, i))
		}
		return cell(r, i)
	}
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = ansi.StringWidth(c.Title)
	}
	for _, r := range rows {
		for i := range cols {
			widths[i] = max(widths[i], ansi.StringWidth(shown(r, i)))
		}
	}
	fit(widths, o.width())

	tcols := make([]table.Column, len(cols))
	for i, c := range cols {
		tcols[i] = table.Column{Title: c.Title, Width: widths[i]}
	}
	trows := make([]table.Row, len(rows))
	for i, r := range rows {
		styled := make(table.Row, len(cols))
		for j, c := range cols {
			styled[j] = shown(r, j)
			if c.Style != nil {
				styled[j] = c.Style(cell(r, j)).Render(styled[j])
			}
		}
		trows[i] = styled
	}
	total := gap * len(cols)
	for _, w := range widths {
		total += w
	}
	t := table.New(
		table.WithColumns(tcols), table.WithRows(trows),
		table.WithWidth(total), table.WithHeight(len(rows)+1),
		table.WithStyles(table.Styles{
			Header: theme.Dim.PaddingRight(gap),
			Cell:   lipgloss.NewStyle().PaddingRight(gap),
		}),
	)
	out := strings.Split(t.View(), "\n")
	if !titled {
		out = out[1:]
	}
	o.lines(out)
}

func cell(r []string, i int) string {
	if i < len(r) {
		return r[i]
	}
	return ""
}

// fit narrows the widest columns until the table, with its gaps, fits width.
func fit(widths []int, width int) {
	for {
		total := gap * (len(widths) - 1)
		widest := 0
		for i, w := range widths {
			total += w
			if w > widths[widest] {
				widest = i
			}
		}
		if total <= width || widths[widest] <= minColumn {
			return
		}
		widths[widest] -= min(total-width, widths[widest]-minColumn)
	}
}

// List prints a titled list, two lines per item as `npx skills list` does:
// the title and its note, then dim details beneath.
func (o *Out) List(heading string, items []Item, empty string) {
	if !o.Pretty {
		for _, it := range items {
			fields := it.Fields
			if len(fields) == 0 {
				fields = []string{it.Title, it.Note}
			}
			fmt.Fprintln(o.out(), record(fields))
		}
		return
	}
	fmt.Fprintln(o.out(), theme.Title.Render(heading))
	fmt.Fprintln(o.out())
	if len(items) == 0 {
		o.say(empty)
		return
	}
	d := delegate{width: o.width(), home: o.Home}
	for _, it := range items {
		d.titleW = max(d.titleW, ansi.StringWidth(short(it.Title, o.Home)))
		d.notes = d.notes || it.Note != ""
		d.details = d.details || len(it.Details) > 0
	}
	if d.notes {
		// Leave room for the note: a long title may not push it off the line.
		d.titleW = min(d.titleW, o.width()*2/5)
	} else {
		d.titleW = o.width()
	}
	rows := make([]list.Item, len(items))
	for i, it := range items {
		rows[i] = row(it)
	}
	l := list.New(rows, d, o.width(), len(items)*d.Height())
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	l.SetShowHelp(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.SetShowPagination(false)
	o.lines(strings.Split(l.View(), "\n"))
}

// say prints a plain message on a terminal.
func (o *Out) say(msg string) {
	if msg != "" {
		fmt.Fprintln(o.out(), theme.Dim.Render(msg))
	}
}

var trailing = regexp.MustCompile(`( +)((?:\x1b\[[0-9;:]*m)*)$`)

// lines prints rows without the blank padding and trailing spaces the bubbles
// components leave.
func (o *Out) lines(ls []string) {
	for len(ls) > 0 && strings.TrimSpace(ansi.Strip(ls[len(ls)-1])) == "" {
		ls = ls[:len(ls)-1]
	}
	for _, l := range ls {
		fmt.Fprintln(o.out(), trailing.ReplaceAllString(l, "$2"))
	}
}

type row Item

func (r row) FilterValue() string { return r.Title }

// delegate draws an item as a title line, plus a details line when any item
// has details.
type delegate struct {
	titleW, width  int
	home           string
	notes, details bool
}

func (d delegate) Height() int {
	if d.details {
		return 2
	}
	return 1
}
func (d delegate) Spacing() int                        { return 0 }
func (d delegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }
func (d delegate) Render(w io.Writer, _ list.Model, _ int, item list.Item) {
	it := Item(item.(row))
	title := ansi.Truncate(short(it.Title, d.home), d.titleW, "…")
	line := theme.Info.Render(title)
	if d.notes {
		pad := strings.Repeat(" ", d.titleW-ansi.StringWidth(title)+gap)
		note := ansi.Truncate(short(it.Note, d.home), max(d.width-d.titleW-gap, 0), "…")
		line += pad + theme.Dim.Render(note)
	}
	fmt.Fprint(w, line)
	if !d.details {
		return
	}

	var parts []string
	for _, x := range it.Details {
		parts = append(parts, theme.Dim.Render(x.Label+":")+" "+x.Style.Render(x.Value))
	}
	fmt.Fprint(w, "\n", ansi.Truncate("  "+strings.Join(parts, "  "), d.width, "…"))
}

// short replaces a leading home directory with ~.
func short(p, home string) string {
	switch {
	case home == "" || p == "":
		return p
	case p == home:
		return "~"
	case strings.HasPrefix(p, home+"/"):
		return "~" + p[len(home):]
	}
	return p
}
