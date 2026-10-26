package cmd

import (
	"time"

	"github.com/ossyrian/tau/internal/render"
)

// out returns the renderer for stdout. Tables look the same everywhere; only
// colour depends on whether stdout is a terminal.
func (a *App) out() *render.Out {
	return &render.Out{W: a.Stdout, Pretty: a.Pretty, NoColor: a.NoColor}
}

// profileTable prints a table of the selected profile's data: PROFILE is the
// first column, and every row carries the profile's name.
func (a *App) profileTable(cols []render.Column, rows [][]string) {
	withProfile := make([][]string, len(rows))
	for i, r := range rows {
		withProfile[i] = append([]string{a.profileName}, r...)
	}
	a.out().Table(append([]render.Column{{Title: "PROFILE"}}, cols...), withProfile)
}

// ago says how long before now t was; the clock is replaceable for tests.
func (a *App) ago(t time.Time) string {
	now := time.Now
	if a.Now != nil {
		now = a.Now
	}
	return render.Ago(now(), t)
}
