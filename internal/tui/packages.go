package tui

import (
	"bytes"
	"fmt"
	"strings"
	"sync"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/core"
)

var packageTabs = []string{"installed", "changes", "build", "requests"}

const (
	tabInstalled = iota
	tabChanges
	tabBuild
	tabRequests
)

// pkgRow is one package in the installed list.
type pkgRow struct {
	name, detail string
	runtimeOnly  bool
}

func (r pkgRow) FilterValue() string { return r.name }
func (r pkgRow) Title() string {
	if r.runtimeOnly {
		return r.name + "  " + warnStyle.Render("added at runtime")
	}
	return r.name
}
func (r pkgRow) Description() string { return r.detail }

func describe(p config.Package) string {
	var parts []string
	switch {
	case len(p.Apt) > 0:
		parts = append(parts, "apt: "+strings.Join(p.Apt, ", "))
	case len(p.Script) > 0:
		first := strings.TrimSpace(strings.SplitN(p.Script[0], "\n", 2)[0])
		parts = append(parts, "script: "+first)
	}
	as := p.As
	if as == "" {
		as = config.AsRoot
	}
	parts = append(parts, "as "+as)
	if len(p.Needs) > 0 {
		parts = append(parts, "needs "+strings.Join(p.Needs, ", "))
	}
	return strings.Join(parts, " · ")
}

// syncBuffer collects build output from another goroutine.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *syncBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}

// cleanLog makes docker output safe to draw: it drops escape sequences and
// control characters, and keeps only what a carriage return leaves on a line.
func cleanLog(s string) []string {
	s = ansi.Strip(s)
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, l := range lines {
		if j := strings.LastIndex(l, "\r"); j >= 0 {
			l = l[j+1:]
		}
		lines[i] = strings.Map(func(r rune) rune {
			if r == '\t' {
				return ' '
			}
			if r < 0x20 || r == 0x7f {
				return -1
			}
			return r
		}, l)
	}
	return lines
}

type packagesLoadedMsg struct {
	cfg *config.Config
	err error
}

type packages struct {
	api    API
	tab    int
	status core.Status

	cfg     *config.Config
	err     error
	loaded  bool
	list    list.Model
	rows    []pkgRow
	resolve Resolve
	hasRes  bool

	log     *syncBuffer
	logView viewport.Model
	logLen  int
	built   bool
}

func newPackages(api API) section {
	return &packages{api: api, list: newList(nil, 30, 10), log: &syncBuffer{}, logView: viewport.New()}
}

func (p *packages) Name() string    { return "packages" }
func (p *packages) Capturing() bool { return false }

func (p *packages) Keys() []bkey.Binding {
	tabs := binding("left", "←/→", "tab")
	switch p.tab {
	case tabChanges:
		return []bkey.Binding{binding("a", "a", "accept"), binding("r", "r", "reject"), tabs}
	case tabBuild:
		return []bkey.Binding{binding("b", "b", "build"), binding("B", "B", "rebuild from scratch"), tabs}
	}
	return []bkey.Binding{tabs}
}

func (p *packages) Enter() tea.Cmd {
	api := p.api
	return func() tea.Msg {
		cfg, err := api.LoadConfig()
		return packagesLoadedMsg{cfg, err}
	}
}

func (p *packages) load(m packagesLoadedMsg) {
	p.cfg, p.err, p.loaded = m.cfg, m.err, true
	p.rows, p.hasRes = nil, false
	if m.err != nil {
		return
	}
	entries, err := m.cfg.Resolve()
	if err != nil {
		p.err = err
		return
	}
	for _, e := range entries {
		p.rows = append(p.rows, pkgRow{name: e.Name, detail: describe(e.Package)})
	}
	for _, name := range m.cfg.Diff().RuntimeOnly {
		p.rows = append(p.rows, pkgRow{name: name, detail: describe(m.cfg.Runtime.Packages[name]), runtimeOnly: true})
	}
	items := make([]list.Item, len(p.rows))
	for i, r := range p.rows {
		items[i] = r
	}
	p.list.SetItems(items)
	p.resolve, p.hasRes = NewResolve(m.cfg).Embedded(), true
}

func (p *packages) drift() int {
	return len(p.status.Drift.RuntimeOnly) + len(p.status.Drift.Changed)
}

func (p *packages) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case statusMsg:
		p.status = core.Status(msg)
	case packagesLoadedMsg:
		p.load(msg)
	case reloadMsg:
		return p.Enter()
	case tea.KeyPressMsg:
		return p.key(msg)
	}
	return nil
}

func (p *packages) key(msg tea.KeyPressMsg) tea.Cmd {
	k := msg.String()
	switch k {
	case "right", "l":
		p.tab = (p.tab + 1) % len(packageTabs)
		return nil
	case "left", "h":
		p.tab = (p.tab + len(packageTabs) - 1) % len(packageTabs)
		return nil
	}
	switch p.tab {
	case tabInstalled:
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(msg)
		return cmd
	case tabChanges:
		return p.changesKey(k)
	case tabBuild:
		switch k {
		case "b":
			return p.startBuild(false)
		case "B":
			return confirm("Rebuild the image from scratch without the cache? This is slow.", func() tea.Cmd {
				return p.startBuild(true)
			})
		}
	}
	return nil
}

func (p *packages) changesKey(k string) tea.Cmd {
	if !p.hasRes {
		return nil
	}
	next, finished := p.resolve.Handle(k)
	p.resolve = next
	if k != "a" && k != "r" {
		return nil
	}
	api := p.api
	// A decision changes the files, so the whole screen reloads from disk.
	_ = finished
	return tea.Batch(p.Enter(), func() tea.Msg { return statusMsg(api.Status()) })
}

func (p *packages) startBuild(noCache bool) tea.Cmd {
	p.log = &syncBuffer{}
	p.logLen, p.built = 0, false
	log := p.log
	return run("building the image", func() (string, error) {
		if err := p.api.Build(noCache, log); err != nil {
			return "", err
		}
		return "image built", nil
	})
}

func (p *packages) tabBar() string {
	names := append([]string{}, packageTabs...)
	if n := p.drift(); n > 0 {
		names[tabChanges] += warnStyle.Render(fmt.Sprintf(" %d", n))
	}
	return tabBar(names, p.tab)
}

func (p *packages) View(cols, rows int) []string {
	out := []string{p.tabBar(), ""}
	room := rows - len(out)
	if p.err != nil {
		return append(out, wrap(errStyle, p.err.Error(), cols-1)...)
	}
	if !p.loaded {
		return append(out, dimStyle.Render("loading…"))
	}
	switch p.tab {
	case tabInstalled:
		if len(p.rows) == 0 {
			return append(out, dimStyle.Render("no packages — add some to config.yaml"))
		}
		p.list.SetSize(cols, max(room, 2))
		return append(out, strings.Split(p.list.View(), "\n")...)
	case tabChanges:
		if !p.hasRes || p.resolve.Pending() == 0 {
			return append(out, okStyle.Render("no changes to decide"), "",
				dimStyle.Render("config.yaml and config.runtime.yaml agree."))
		}
		return append(out, strings.Split(strings.TrimRight(p.resolve.Body(), "\n"), "\n")...)
	case tabBuild:
		return append(out, p.buildView(cols, room)...)
	}
	return append(out, dimStyle.Render("no requests"), "",
		dimStyle.Render("When the agent asks for a package, the request shows up here."))
}

func (p *packages) buildView(cols, rows int) []string {
	image := map[build.ImageStatus]string{
		build.ImageCurrent: okStyle.Render("current"),
		build.ImageStale:   warnStyle.Render("stale — config.yaml has changed since the last build"),
		build.ImageMissing: warnStyle.Render("missing — build it to start the container"),
		build.ImageUnknown: dimStyle.Render("unknown"),
	}[p.status.Image]
	out := []string{fact("image", image)}
	if p.drift() > 0 {
		out = append(out, "", warnStyle.Render("Resolve the changes first: a build would drop what the agent installed."))
	}
	out = append(out, "")
	if p.log.Len() == 0 {
		return append(out, dimStyle.Render("press b to build, B to rebuild without the cache"))
	}

	// Size first: scrolling to the bottom needs the height.
	p.logView.SetWidth(cols)
	p.logView.SetHeight(max(rows-len(out), 1))
	if n := p.log.Len(); n != p.logLen {
		p.logLen = n
		p.logView.SetContentLines(cleanLog(p.log.String()))
		p.logView.GotoBottom()
	}
	return append(out, strings.Split(p.logView.View(), "\n")...)
}
