// Package render prints command output. Every list is a table like `docker ps`:
// an upper-case header, then one line per row, columns padded and separated by
// three spaces. The layout is the same on a terminal and in a pipe. Only
// colour differs: it appears on a terminal and nowhere else.
package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/theme"
)

const gap = "   " // between columns, as in `docker ps`

// Out writes command output.
type Out struct {
	W io.Writer
	// Pretty is true when W is a terminal: output is coloured.
	Pretty bool
	// NoColor drops colour even on a terminal (the NO_COLOR convention).
	NoColor bool
}

func (o *Out) color() bool { return o.Pretty && !o.NoColor }

// Column is one column of a table.
type Column struct {
	Title string
	// Style picks the colour of a cell; nil leaves cells plain. It is used
	// only when colour is on.
	Style func(cell string) lipgloss.Style
}

// Table prints the header, then the rows. With no rows it prints the header
// alone, as `docker ps` does.
func (o *Out) Table(cols []Column, rows [][]string) {
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = ansi.StringWidth(c.Title)
	}
	clean := make([][]string, len(rows))
	for r, row := range rows {
		clean[r] = make([]string, len(cols))
		for i := range cols {
			if i < len(row) {
				clean[r][i] = oneLine(row[i])
			}
			widths[i] = max(widths[i], ansi.StringWidth(clean[r][i]))
		}
	}

	titles := make([]string, len(cols))
	for i, c := range cols {
		titles[i] = c.Title
	}
	o.line(titles, widths, func(i int, cell string) string { return theme.Dim.Render(cell) })
	for _, row := range clean {
		o.line(row, widths, func(i int, cell string) string {
			if cols[i].Style == nil {
				return cell
			}
			return cols[i].Style(cell).Render(cell)
		})
	}
}

// line prints one row. A cell is padded before it is coloured, so escape
// codes do not disturb the alignment. The last column is not padded.
func (o *Out) line(cells []string, widths []int, paint func(int, string) string) {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 {
			b.WriteString(gap)
		}
		text := c
		if o.color() && c != "" {
			text = paint(i, c)
		}
		b.WriteString(text)
		if i < len(cells)-1 {
			b.WriteString(strings.Repeat(" ", widths[i]-ansi.StringWidth(c)))
		}
	}
	fmt.Fprintln(o.W, strings.TrimRight(b.String(), " "))
}

// oneLine keeps a cell on its line.
func oneLine(s string) string {
	return strings.NewReplacer("\t", " ", "\n", " ", "\r", " ").Replace(s)
}

// Outcome reports what a command did when the answer is one of a few fixed
// results. A script gets the token (for example "already-running"), a person
// gets the text.
func (o *Out) Outcome(token, text, hint string) {
	if !o.Pretty {
		fmt.Fprintln(o.W, oneLine(token))
		return
	}
	o.mark(text, hint)
}

// Note prints a message for people. A script gets nothing.
func (o *Out) Note(msg string) {
	if o.Pretty && msg != "" {
		fmt.Fprintln(o.W, o.paint(theme.Dim, msg))
	}
}

// Done reports a finished action. A terminal gets a marked line; a script gets
// the subject alone (the canonical name of what changed), or nothing when
// there is none.
func (o *Out) Done(verb, subject, hint string) {
	if !o.Pretty {
		if subject != "" {
			fmt.Fprintln(o.W, oneLine(subject))
		}
		return
	}
	text := verb
	if subject != "" {
		text += " " + subject
	}
	o.mark(text, hint)
}

func (o *Out) mark(text, hint string) {
	line := o.paint(theme.OK, "✓") + " " + text
	if hint != "" {
		line += "  " + o.paint(theme.Dim, hint)
	}
	fmt.Fprintln(o.W, line)
}

func (o *Out) paint(s lipgloss.Style, text string) string {
	if !o.color() {
		return text
	}
	return s.Render(text)
}

// Ago says how long before now t was, as `docker ps` does: "3 hours ago".
func Ago(now, t time.Time) string {
	d := now.Sub(t)
	unit := func(n time.Duration, name string) string {
		if n == 1 {
			return "1 " + name + " ago"
		}
		return fmt.Sprintf("%d %ss ago", n, name)
	}
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return unit(d/time.Minute, "minute")
	case d < 48*time.Hour:
		return unit(d/time.Hour, "hour")
	}
	return unit(d/(24*time.Hour), "day")
}
