package tui

import (
	"fmt"
	"strings"

	bkey "charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
)

// proxyRow is one proxy in the list.
type proxyRow struct {
	name string
	def  config.Proxy
	on   bool
}

func (r proxyRow) FilterValue() string { return r.name }
func (r proxyRow) Title() string {
	mark := dimStyle.Render("○")
	if r.on {
		mark = okStyle.Render("●")
	}
	t := mark + " " + r.name
	if r.on {
		t += "  " + okStyle.Render("in use")
	}
	return t
}
func (r proxyRow) Description() string {
	parts := []string{"types " + r.def.AgentCommand}
	if r.def.InAgentDirectory() {
		parts = append(parts, "agent directory")
	}
	if r.def.Description != "" {
		parts = append(parts, r.def.Description)
	}
	return strings.Join(parts, "  ·  ")
}

// runsWhat says where a proxy's program comes from.
func runsWhat(d config.Proxy) string {
	if d.SystemBinary != "" {
		return d.SystemBinary
	}
	return d.Package + ": " + d.Binary
}

type proxiesLoadedMsg struct {
	proxies config.Proxies
	err     error
}

type proxyVarsMsg struct {
	name string
	vars []proxy.Var
	err  error
}

// proxiesSection lists proxies; enter opens one to edit its variables.
type proxiesSection struct {
	api API

	list    list.Model
	proxies config.Proxies
	err     error
	loaded  bool
	open    string // the proxy whose variables are shown; "" for the list
	vars    *varEditor
}

func newProxies(api API) section {
	p := &proxiesSection{api: api, list: newList(nil, 30, 10), vars: newVarEditor()}
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

func (p *proxiesSection) Name() string    { return "proxies" }
func (p *proxiesSection) Capturing() bool { return p.open != "" && p.vars.Capturing() }

func (p *proxiesSection) Keys() []bkey.Binding {
	if p.open != "" {
		if p.vars.Capturing() {
			return p.vars.Keys()
		}
		return append(p.vars.Keys(), binding("esc", "esc", "back"))
	}
	return []bkey.Binding{binding("space", "space", "use"), binding("enter", "enter", "variables"), binding("r", "r", "reload"), binding("R", "R", "refresh")}
}

func (p *proxiesSection) Enter() tea.Cmd {
	p.vars.Hide() // secrets are hidden again every time the section opens
	return p.load()
}

func (p *proxiesSection) load() tea.Cmd {
	api := p.api
	return func() tea.Msg {
		c, err := api.Proxies()
		return proxiesLoadedMsg{c, err}
	}
}

func (p *proxiesSection) loadVars() tea.Cmd {
	api, name := p.api, p.open
	return func() tea.Msg {
		v, err := api.ProxyVars(name)
		return proxyVarsMsg{name, v, err}
	}
}

func (p *proxiesSection) rebuild() {
	on := map[string]bool{}
	for _, n := range p.proxies.Active {
		on[n] = true
	}
	var items []list.Item
	for _, n := range p.proxies.Names() {
		items = append(items, proxyRow{n, p.proxies.Defs[n], on[n]})
	}
	p.list.SetItems(items)
}

func (p *proxiesSection) selected() (proxyRow, bool) {
	r, ok := p.list.SelectedItem().(proxyRow)
	return r, ok
}

func (p *proxiesSection) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case proxiesLoadedMsg:
		keep := ""
		if r, ok := p.selected(); ok {
			keep = r.name
		}
		p.err, p.loaded, p.proxies = msg.err, true, msg.proxies
		p.rebuild()
		for i, n := range p.proxies.Names() {
			if n == keep {
				p.list.Select(i)
			}
		}
		return nil
	case proxyVarsMsg:
		if msg.name != p.open {
			return nil
		}
		p.err = msg.err
		p.vars.Set(msg.vars)
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
		return p.browse(msg)
	}
	return nil
}

// browseVars handles a key on an open proxy's page. esc leaves it, unless the
// editor has an input open.
func (p *proxiesSection) browseVars(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" && !p.vars.Capturing() {
		p.open, p.err = "", nil
		p.vars.Hide()
		return nil
	}
	return p.vars.Update(msg)
}

func (p *proxiesSection) browse(msg tea.KeyPressMsg) tea.Cmd {
	switch msg.String() {
	case "space", " ":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		on := !r.on
		verb := "using " + r.name
		if !on {
			verb = "no longer using " + r.name
		}
		return run(verb, func() (string, error) {
			err := p.api.UseProxy(r.name, on)
			return verb, err
		})
	case "enter":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		p.open = r.name
		p.vars.Set(nil)
		return p.loadVars()
	case "r":
		return run("reloading proxy servers", func() (string, error) {
			return "proxy servers reloaded", p.api.ReloadProxies()
		})
	case "R":
		r, ok := p.selected()
		if !ok {
			return nil
		}
		return run("refreshing "+r.name, func() (string, error) {
			return r.name + " refreshed", p.api.RefreshProxies([]string{r.name})
		})
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(msg)
	return cmd
}

func (p *proxiesSection) View(cols, rows int) []string {
	if p.err != nil {
		return append([]string{""}, wrap(errStyle, p.err.Error(), cols-1)...)
	}
	if !p.loaded {
		return []string{"", dimStyle.Render("loading…")}
	}
	if p.open != "" {
		return p.viewVars(cols, rows)
	}
	if len(p.proxies.Defs) == 0 {
		return []string{
			"", dimStyle.Render("no proxies defined"), "",
			dimStyle.Render("Define one under proxies.defs in the default profile's config.yaml."),
		}
	}
	p.list.SetSize(cols, rows)
	return strings.Split(p.list.View(), "\n")
}

// facts describes the open proxy: what it runs and what it refuses.
func (p *proxiesSection) facts(cols int) []string {
	d, ok := p.proxies.Defs[p.open]
	if !ok {
		return nil
	}
	where := "an empty directory"
	if d.InAgentDirectory() {
		where = "the agent's directory"
	}
	out := []string{
		dimStyle.Render("agent types  ") + d.AgentCommand,
		dimStyle.Render("runs         ") + runsWhat(d),
		dimStyle.Render("runs in      ") + where,
	}
	if d.EnvFrom != "" {
		out = append(out, dimStyle.Render("credentials  ")+"minted by env_from")
	}
	if len(d.Deny) > 0 {
		out = append(out, wrap(dimStyle, "denies: "+strings.Join(d.Deny, "; "), cols-1)...)
	}
	return append(out, "")
}

func (p *proxiesSection) viewVars(cols, rows int) []string {
	out := []string{titleStyle.Render(p.open + " — variables"), ""}
	out = append(out, p.facts(cols)...)
	return append(out, p.vars.View(cols, rows-len(out))...)
}
