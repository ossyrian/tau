package tui

import (
	"fmt"
	"strings"
	"time"

	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
)

type home struct {
	api    API
	status core.Status
}

func newHome(api API) *home { return &home{api: api} }

func (h *home) Name() string    { return "home" }
func (h *home) Enter() tea.Cmd  { return nil }
func (h *home) Capturing() bool { return false }
func (h *home) Keys() []bkey.Binding {
	return []bkey.Binding{
		binding("s", "s", "start"), binding("x", "x", "stop"),
		binding("r", "r", "restart"),
	}
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

// count reads "N noun" or "N nouns".
func count(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fact(label, value string) string {
	return dimStyle.Render(fmt.Sprintf("%-12s", label)) + value
}

func (h *home) View(cols, rows int) []string {
	s := h.status
	var container string
	switch {
	case s.Container.Running:
		container = okStyle.Render("running")
		if !s.Container.Started.IsZero() {
			container += dimStyle.Render("  up " + uptime(time.Since(s.Container.Started)))
		}
	case s.Container.Exists:
		container = warnStyle.Render("stopped")
	default:
		container = dimStyle.Render("not running")
	}
	image := map[build.ImageStatus]string{
		build.ImageCurrent: okStyle.Render("current"),
		build.ImageStale:   warnStyle.Render("stale") + dimStyle.Render("  rebuild in packages"),
		build.ImageMissing: warnStyle.Render("missing") + dimStyle.Render("  build it in packages"),
		build.ImageUnknown: dimStyle.Render("unknown"),
	}[s.Image]
	profile := s.Profile

	drift := len(s.Drift.RuntimeOnly) + len(s.Drift.Changed)
	lines := []string{
		titleStyle.Render("Harness"), "",
		fact("container", container),
		fact("image", image),
		fact("profile", profile), "",
		titleStyle.Render("Needs attention"), "",
		fact("packages", pending(drift, "package", "need a decision")),
		fact("skills", pending(s.SkillsPending, "skill", "pending restart")),
	}
	if len(s.Problems) > 0 {
		lines = append(lines, "", errStyle.Render("Problems"), "")
		for _, p := range s.Problems {
			lines = append(lines, errStyle.Render(strings.TrimSpace(p)))
		}
	}
	return lines
}

func pending(n int, noun, what string) string {
	if n == 0 {
		return okStyle.Render("in sync")
	}
	return warnStyle.Render(count(n, noun) + " " + what)
}
