package tui

import (
	"errors"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/core"
)

// Package states shown in the STATE column.
const (
	pkgOK      = "ok"
	pkgChanged = "changed"
	pkgAdded   = "added at runtime"
)

type packagesLoadedMsg struct {
	profile string
	cfg     *config.Config
	err     error
}

// packages is two panes: every package as a table, and the selected package's
// YAML below it. While a build runs, or after one, the lower pane shows its log.
type packages struct {
	api    API
	status core.Status

	cfg     *config.Config
	err     error
	loaded  bool
	m       *master
	entries map[string]config.Package // base definitions by name
	added   map[string]config.Package // runtime-only definitions by name
	changed map[string]bool

	log     *syncBuffer
	showLog bool
}

func newPackages(api API) section {
	return &packages{
		api: api, log: &syncBuffer{},
		m: newMaster(true, "PROFILE", "NAME", "STATE", "INSTALL", "AS", "NEEDS"),
	}
}

func (p *packages) Name() string    { return "packages" }
func (p *packages) Capturing() bool { return p.m.Capturing() }

func (p *packages) Keys() []bkey.Binding {
	keys := p.m.Keys()
	if p.m.Capturing() {
		return keys
	}
	own := []bkey.Binding{binding("b", "b", "build"), binding("B", "B", "rebuild")}
	if p.drift() > 0 {
		own = append([]bkey.Binding{binding("a", "a", "accept"), binding("r", "r", "reject")}, own...)
	}
	if p.log.Len() > 0 {
		own = append(own, binding("l", "l", "build log"))
	}
	return append(own, keys...)
}

func (p *packages) drift() int {
	return len(p.status.Drift.RuntimeOnly) + len(p.status.Drift.Changed)
}

func (p *packages) Enter() tea.Cmd {
	api := p.api
	return func() tea.Msg {
		cfg, err := api.LoadConfig()
		return packagesLoadedMsg{api.Status().Profile, cfg, err}
	}
}

// installMethod says how a package is installed: "apt: x, y" or "script".
func installMethod(pk config.Package) string {
	if len(pk.Apt) > 0 {
		return "apt: " + strings.Join(pk.Apt, ", ")
	}
	return "script"
}

func asUser(pk config.Package) string {
	if pk.As == "" {
		return config.AsRoot
	}
	return pk.As
}

func (p *packages) setData(m packagesLoadedMsg) {
	p.cfg, p.err, p.loaded = m.cfg, m.err, true
	p.entries, p.added, p.changed = map[string]config.Package{}, map[string]config.Package{}, map[string]bool{}
	if m.err != nil {
		return
	}
	entries, err := m.cfg.Resolve()
	if err != nil {
		p.err = err
		return
	}
	diff := m.cfg.Diff()
	for _, n := range diff.Changed {
		p.changed[n] = true
	}
	var rows []pickerRow
	add := func(name, state string, pk config.Package) {
		rows = append(rows, pickerRow{
			Key:    name,
			Cells:  []string{m.profile, name, state, installMethod(pk), asUser(pk), strings.Join(pk.Needs, ", ")},
			Search: name + " " + installMethod(pk), // by name and by installation method
		})
	}
	for _, e := range entries {
		p.entries[e.Name] = e.Package
		state := pkgOK
		if p.changed[e.Name] {
			state = pkgChanged
		}
		add(e.Name, state, e.Package)
	}
	for _, n := range diff.RuntimeOnly {
		pk := m.cfg.Runtime.Packages[n]
		p.added[n] = pk
		add(n, pkgAdded, pk)
	}
	p.m.pick.SetRows(rows)
}

func (p *packages) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case statusMsg:
		p.status = core.Status(msg)
	case packagesLoadedMsg:
		p.setData(msg)
	case reloadMsg:
		return p.Enter()
	case tea.KeyPressMsg:
		return p.key(msg)
	}
	return nil
}

func (p *packages) key(msg tea.KeyPressMsg) tea.Cmd {
	if p.m.Capturing() {
		return p.m.Update(msg)
	}
	switch msg.String() {
	case "a":
		return p.decide(true)
	case "r":
		return p.decide(false)
	case "b":
		return p.startBuild(false)
	case "B":
		return confirm("Rebuild the image from scratch without the cache? This is slow.", func() tea.Cmd {
			return p.startBuild(true)
		})
	case "l":
		p.showLog = !p.showLog && p.log.Len() > 0
		return nil
	}
	before := p.selectedName()
	cmd := p.m.Update(msg)
	if p.selectedName() != before {
		p.showLog = false // moving to another package leaves the log
	}
	return cmd
}

func (p *packages) selectedName() string {
	r, _ := p.m.pick.Selected()
	return r.Key
}

// decide accepts or rejects the drift of the selected package. A decision
// changes the files, so the screen reloads from disk afterwards.
func (p *packages) decide(accept bool) tea.Cmd {
	name := p.selectedName()
	_, runtimeOnly := p.added[name]
	if name == "" || (!runtimeOnly && !p.changed[name]) {
		return nil
	}
	verb := "rejected"
	if accept {
		verb = "accepted"
	}
	return run(verb+" "+name, func() (string, error) {
		cfg, err := p.api.LoadConfig()
		if err != nil {
			return "", err
		}
		if err := cfg.Apply(name, actionFor(runtimeOnly, accept)); err != nil {
			return "", err
		}
		if _, err := cfg.Settle(); err != nil {
			return "", err
		}
		return verb + " " + name, nil
	})
}

func (p *packages) startBuild(noCache bool) tea.Cmd {
	if p.drift() > 0 {
		return func() tea.Msg {
			return doneMsg{err: errBuildWithDrift}
		}
	}
	p.log, p.showLog = &syncBuffer{}, true
	log := p.log
	return run("building the image", func() (string, error) {
		if err := p.api.Build(noCache, log); err != nil {
			return "", err
		}
		return "image built", nil
	})
}

var errBuildWithDrift = errors.New("resolve the changes first (a to accept, r to reject): a build would drop what the agent installed")

// detailText is the YAML of the selected package, or the build log.
func (p *packages) detailText() (title, text string) {
	if p.showLog {
		return "build log", cleanLog(p.log.String())
	}
	name := p.selectedName()
	yaml := func(pk config.Package) string {
		y := pk.YAML()
		if y == "" {
			return "# empty: uses the catalog definition\n"
		}
		return y
	}
	if p.changed[name] {
		return name + " (changed)", highlight("yaml",
			"# config.yaml\n"+yaml(p.cfg.Base.Packages[name])+"\n# config.runtime.yaml\n"+yaml(p.cfg.Runtime.Packages[name]))
	}
	if added, ok := p.added[name]; ok {
		return name + " (added at runtime)", highlight("yaml", "# config.runtime.yaml\n"+yaml(added))
	}
	pk, ok := p.entries[name]
	if !ok {
		return "package", ""
	}
	return name, highlight("yaml", yaml(pk))
}

func (p *packages) View(w, h int) string {
	if msg := stateMessage(p.err, p.loaded); msg != "" {
		return messagePane("packages", w, h, msg)
	}
	if len(p.entries)+len(p.added) == 0 {
		return messagePane("packages", w, h, "no packages — add some to config.yaml")
	}
	title, text := p.detailText()
	return p.m.View(w, h, "packages", title, text)
}
