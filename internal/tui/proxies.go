package tui

import (
	"bytes"
	"fmt"
	"path"

	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
	"gopkg.in/yaml.v3"
)

type proxiesLoadedMsg struct {
	profile string
	proxies config.Proxies
	err     error
}

type proxyVarsMsg struct {
	name    string
	profile string
	vars    []proxyVar
	err     error
}

type proxyVar = proxy.Var

// proxiesSection is two panes: the proxies as a table, and the selected one's
// definition as YAML. Enter opens a proxy's variables in place of the table.
type proxiesSection struct {
	api     API
	profile string

	m       *master
	proxies config.Proxies
	err     error
	loaded  bool
	open    string // the proxy whose variables are shown; "" for the table
	vars    *varEditor
}

func newProxies(api API) section {
	p := &proxiesSection{
		api: api, vars: newVarEditor(),
		m: newMaster(true, "PROFILE", "NAME", "COMMAND", "IN USE", "SEES AGENT FILES", "PROGRAM PATH"),
	}
	p.vars.set = func(key, value string) tea.Cmd {
		name := p.open
		return run("saving "+key, func() (string, error) {
			return "saved " + key + " (in use now)", api.SetProxyVar(name, key, value)
		})
	}
	p.vars.remove = func(key string) tea.Cmd {
		name := p.open
		return confirm(fmt.Sprintf("Delete %s from %s?", key, name), func() tea.Cmd {
			return run("deleting "+key, func() (string, error) {
				_, err := api.UnsetProxyVar(name, key)
				return "deleted " + key + " (in use now)", err
			})
		})
	}
	return p
}

func (p *proxiesSection) Name() string { return "proxies" }
func (p *proxiesSection) Capturing() bool {
	if p.open != "" {
		return p.vars.Capturing()
	}
	return p.m.Capturing()
}

func (p *proxiesSection) Keys() []bkey.Binding {
	if p.open != "" {
		if p.vars.Capturing() {
			return p.vars.Keys()
		}
		return append(p.vars.Keys(), binding("esc", "esc", "back"))
	}
	keys := p.m.Keys()
	if p.m.Capturing() {
		return keys
	}
	return append([]bkey.Binding{
		binding("space", "space", "use"), binding("enter", "enter", "variables"),
		binding("r", "r", "reload"), binding("R", "R", "refresh"),
	}, keys...)
}

func (p *proxiesSection) Enter() tea.Cmd {
	p.vars.Hide() // secrets are hidden again every time the section opens
	return p.load()
}

func (p *proxiesSection) load() tea.Cmd {
	api := p.api
	return func() tea.Msg {
		c, err := api.Proxies()
		return proxiesLoadedMsg{api.Status().Profile, c, err}
	}
}

func (p *proxiesSection) loadVars() tea.Cmd {
	api, name := p.api, p.open
	return func() tea.Msg {
		v, err := api.ProxyVars(name)
		return proxyVarsMsg{name, api.Status().Profile, v, err}
	}
}

// programPath is the absolute path, in the container, of the file a proxy runs.
func programPath(d config.Proxy) string {
	if d.SystemBinary != "" {
		return d.SystemBinary
	}
	return path.Join(proxy.PackagePrefix(d.Package), d.Binary)
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func (p *proxiesSection) setData(m proxiesLoadedMsg) {
	p.err, p.loaded, p.proxies, p.profile = m.err, true, m.proxies, m.profile
	on := map[string]bool{}
	for _, n := range p.proxies.Active {
		on[n] = true
	}
	var rows []pickerRow
	for _, n := range p.proxies.Names() {
		d := p.proxies.Defs[n]
		rows = append(rows, pickerRow{
			Key:    n,
			Cells:  []string{p.profile, n, d.AgentCommand, yesNo(on[n]), yesNo(d.InAgentDirectory()), programPath(d)},
			Search: n + " " + d.AgentCommand, // by name and by the command the agent types
		})
	}
	p.m.pick.SetRows(rows)
}

func (p *proxiesSection) selected() (string, bool) {
	r, ok := p.m.pick.Selected()
	return r.Key, ok
}

func (p *proxiesSection) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case proxiesLoadedMsg:
		p.setData(msg)
		return nil
	case proxyVarsMsg:
		if msg.name != p.open {
			return nil
		}
		p.err = msg.err
		p.vars.Set(msg.profile, msg.vars)
		return nil
	case reloadMsg:
		if p.open != "" {
			return tea.Batch(p.load(), p.loadVars())
		}
		return p.load()
	case tea.KeyPressMsg:
		if p.open != "" {
			return p.browseVars(msg)
		}
		if p.m.Capturing() {
			return p.m.Update(msg)
		}
		return p.browse(msg)
	}
	return nil
}

// browseVars handles a key on an open proxy's variables. esc leaves them,
// unless the editor has an input open.
func (p *proxiesSection) browseVars(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" && !p.vars.Capturing() {
		p.open, p.err = "", nil
		p.vars.Hide()
		return nil
	}
	return p.vars.Update(msg)
}

func (p *proxiesSection) browse(msg tea.KeyPressMsg) tea.Cmd {
	name, ok := p.selected()
	switch msg.String() {
	case "space":
		if !ok {
			return nil
		}
		on := !contains(p.proxies.Active, name)
		verb := "using " + name
		if !on {
			verb = "no longer using " + name
		}
		return run(verb, func() (string, error) { return verb, p.api.UseProxy(name, on) })
	case "enter":
		if !ok {
			return nil
		}
		p.open = name
		p.vars.Set(p.profile, nil)
		return p.loadVars()
	case "r":
		return run("reloading proxy servers", func() (string, error) {
			return "proxy servers reloaded", p.api.ReloadProxies()
		})
	case "R":
		if !ok {
			return nil
		}
		return run("refreshing "+name, func() (string, error) {
			return name + " refreshed", p.api.RefreshProxies([]string{name})
		})
	}
	return p.m.Update(msg)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// definition is the selected proxy's config, as the YAML a person would write.
func (p *proxiesSection) definition(name string) string {
	d, ok := p.proxies.Defs[name]
	if !ok {
		return ""
	}
	def := map[string]any{"agent_command": d.AgentCommand}
	if d.SystemBinary != "" {
		def["system_binary"] = d.SystemBinary
	} else {
		def["package"], def["binary"] = d.Package, d.Binary
	}
	if d.Description != "" {
		def["description"] = d.Description
	}
	if d.RunsIn != "" {
		def["runs_in"] = d.RunsIn
	}
	if d.EnvFrom != "" {
		def["env_from"] = d.EnvFrom
	}
	if len(d.Deny) > 0 {
		def["deny"] = d.Deny
	}
	var b bytes.Buffer
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(map[string]any{name: def}); err != nil {
		return err.Error()
	}
	return highlight("yaml", b.String())
}

func (p *proxiesSection) View(w, h int) string {
	if msg := stateMessage(p.err, p.loaded); msg != "" {
		return messagePane("proxies", w, h, msg)
	}
	if p.open != "" {
		return varsPane(p.open+" variables", w, h, p.vars)
	}
	if len(p.proxies.Defs) == 0 {
		return messagePane("proxies", w, h, "no proxies defined — define one under proxies.defs in the default profile's config.yaml")
	}
	name, _ := p.selected()
	return p.m.View(w, h, "proxies", name, p.definition(name))
}
