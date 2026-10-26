package cmd

import "github.com/ossyrian/tau/internal/render"

// out returns the renderer for stdout: bubbles components on a terminal, plain
// tab-separated records anywhere else.
func (a *App) out() *render.Out {
	return &render.Out{
		W: a.Stdout, Pretty: a.Pretty, Width: a.Width, NoColor: a.NoColor,
		Home: a.Env("HOME"),
	}
}

// timeLayout is how a terminal shows a time. Scripts get RFC 3339.
const timeLayout = "2006-01-02 15:04:05"
