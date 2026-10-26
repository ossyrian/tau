package tui

import (
	"fmt"
	"strings"
	"time"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/theme"
)

// home is a single pane: what needs attention, one row per fact.
type home struct {
	api    API
	status core.Status
}

func newHome(api API) *home { return &home{api: api} }

func (h *home) Name() string    { return "home" }
func (h *home) Enter() tea.Cmd  { return nil }
func (h *home) Capturing() bool { return false }
func (h *home) Keys() []bkey.Binding {
	return []bkey.Binding{binding("s", "s", "start"), binding("x", "x", "stop"), binding("r", "r", "restart")}
}

func (h *home) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case statusMsg:
		h.status = core.Status(msg)
	case tea.KeyPressMsg:
		switch msg.String() {
		case "s":
			return run("starting", func() (string, error) { return outcomeNotice(h.api.Start()) })
		case "x":
			return confirm("Stop the container? Running sessions are lost.", func() tea.Cmd {
				return run("stopping", func() (string, error) { return outcomeNotice(h.api.Stop()) })
			})
		case "r":
			return confirm("Restart the container? Running sessions are lost.", func() tea.Cmd {
				return run("restarting", func() (string, error) { return outcomeNotice(h.api.Restart()) })
			})
		}
	}
	return nil
}

// uptime reads "5m", "1h 30m" or "2d 1h".
func uptime(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm", int(d.Minutes()))
}

// facts are the rows of the pane: what, then how it stands.
func (h *home) facts() [][2]string {
	s := h.status
	container := "not running"
	switch {
	case s.Container.Running:
		container = "running"
		if !s.Container.Started.IsZero() {
			container += ", up " + uptime(time.Since(s.Container.Started))
		}
	case s.Container.Exists:
		container = "stopped"
	}
	drift := len(s.Drift.RuntimeOnly) + len(s.Drift.Changed)
	rows := [][2]string{
		{"container", container},
		{"image", s.Image.String()},
		{"packages", pendingText(drift, "need a decision")},
		{"skills", pendingText(s.SkillsPending, "pending restart")},
	}
	if s.Image == build.ImageStale || s.Image == build.ImageMissing {
		rows[1][1] += ", build it in packages"
	}
	for _, p := range s.Problems {
		rows = append(rows, [2]string{"problem", strings.TrimSpace(p)})
	}
	return rows
}

func pendingText(n int, what string) string {
	if n == 0 {
		return "in sync"
	}
	return fmt.Sprintf("%d %s", n, what)
}

func (h *home) View(w, ht int) string {
	iw, ih := inner(w, ht)
	facts := h.facts()
	rows := make([]table.Row, len(facts))
	for i, f := range facts {
		rows[i] = table.Row{h.status.Profile, f[0], f[1]}
	}
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "PROFILE", Width: 10}, {Title: "WHAT", Width: 12}, {Title: "STATE", Width: max(iw-22-2*3, 8)},
		}),
		table.WithRows(rows), table.WithWidth(iw), table.WithHeight(ih),
		table.WithStyles(table.Styles{Header: theme.Dim.Padding(0, 1), Cell: theme.Dim.Padding(0, 1).UnsetFaint(), Selected: theme.Selected}),
	)
	return pane("home", w, ht, true, t.View())
}
