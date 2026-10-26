package cmd

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/spf13/cobra"
)

// proxies loads the selected profile's proxy section.
func (a *App) proxies() (config.Proxies, error) {
	if a.Core == nil {
		return config.Proxies{}, errNoCore
	}
	return a.Core.Proxies()
}

func (a *App) proxiesCmd() *cobra.Command {
	var show bool
	envLs := &cobra.Command{Use: "list PROXY", Short: "list a proxy's environment variables", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		vars, err := a.Core.ProxyVars(args[0])
		if err != nil {
			return err
		}
		rows := make([][]string, len(vars))
		for i, v := range vars {
			val := proxy.Mask(v.Value)
			if show {
				val = v.Value
			}
			rows[i] = []string{v.Key, val}
		}
		a.profileTable([]render.Column{{Title: "KEY"}, {Title: "VALUE"}}, rows)
		return nil
	}}
	envLs.Flags().BoolVar(&show, "show", false, "print values in full")

	envSet := &cobra.Command{Use: "set PROXY KEY VALUE", Short: "set a proxy environment variable", Args: cobra.RangeArgs(2, 3), RunE: func(_ *cobra.Command, args []string) error {
		name := args[0]
		var key, value string
		switch len(args) {
		case 3:
			key, value = args[1], args[2]
		default:
			k, v, ok := strings.Cut(args[1], "=")
			if !ok {
				return errors.New("proxy env set needs PROXY KEY VALUE or PROXY KEY=VALUE")
			}
			key, value = k, v
		}
		if err := a.Core.SetProxyVar(name, key, value); err != nil {
			return err
		}
		a.out().Done("set", name+" "+key, "in use now")
		return nil
	}}

	envUnset := &cobra.Command{Use: "unset PROXY KEY", Short: "remove a proxy environment variable", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, args []string) error {
		removed, err := a.Core.UnsetProxyVar(args[0], args[1])
		if err != nil {
			return err
		}
		if !removed {
			return fmt.Errorf("%s has no variable %s", args[0], args[1])
		}
		a.out().Done("unset", args[0]+" "+args[1], "in use now")
		return nil
	}}

	use := func(verb string, add bool) *cobra.Command {
		short := "allow the use of this proxy"
		if !add {
			short = "disallow the use of this proxy"
		}
		return &cobra.Command{Use: verb + " PROXY", Short: short, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			return a.setUse(args[0], add)
		}}
	}

	return parent("proxies", "manage proxies",
		&cobra.Command{Use: "list", Short: "list available proxies", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			return a.proxyList()
		}},
		use("allow", true), use("disallow", false),
		parent("env", "manage proxy environment variables", envLs, envSet, envUnset),
		&cobra.Command{Use: "refresh [PROXY...]", Short: "refresh credentials for all proxies", Args: cobra.ArbitraryArgs, RunE: func(_ *cobra.Command, args []string) error {
			if a.Core == nil {
				return errNoCore
			}
			if err := a.Core.RefreshProxies(args); err != nil {
				return err
			}
			what := "all proxies"
			if len(args) > 0 {
				what = strings.Join(args, ", ")
			}
			a.out().Done("refreshed", what, "")
			return nil
		}},
		&cobra.Command{Use: "reload", Short: "reload environment variables for all proxies", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			if a.Core == nil {
				return errNoCore
			}
			if err := a.Core.ReloadProxies(); err != nil {
				return err
			}
			a.out().Done("reloaded", "proxy servers", "")
			return nil
		}},
	)
}

func (a *App) proxyList() error {
	p, err := a.proxies()
	if err != nil {
		return err
	}
	on := map[string]bool{}
	for _, n := range p.Active {
		on[n] = true
	}
	rows := make([][]string, 0, len(p.Defs))
	for _, n := range p.Names() {
		d := p.Defs[n]
		use := "no"
		if on[n] {
			use = "yes"
		}
		sees := "no"
		if d.InAgentDirectory() {
			sees = "yes"
		}
		rows = append(rows, []string{n, d.AgentCommand, use, sees, programPath(d), d.Description})
	}
	a.profileTable([]render.Column{
		{Title: "NAME"}, {Title: "COMMAND"},
		{Title: "IN USE", Style: func(c string) lipgloss.Style {
			if c == "yes" {
				return theme.OK
			}
			return theme.Dim
		}},
		// A proxy that sees the agent's files runs whatever hooks the agent left there.
		{Title: "SEES AGENT FILES", Style: func(c string) lipgloss.Style {
			if c == "yes" {
				return theme.Warn
			}
			return theme.Dim
		}},
		{Title: "PROGRAM PATH"}, {Title: "DESCRIPTION"},
	}, rows)
	return nil
}

// setUse adds or removes a proxy from the selected profile's active list.
func (a *App) setUse(name string, on bool) error {
	if a.Core == nil {
		return errNoCore
	}
	if err := a.Core.UseProxy(name, on); err != nil {
		return err
	}
	verb := "stopped using"
	if on {
		verb = "using"
	}
	a.out().Done(verb, name, "")
	return nil
}

// programPath is the absolute path, in the container, of the file a proxy runs.
func programPath(d config.Proxy) string {
	if d.SystemBinary != "" {
		return d.SystemBinary
	}
	return path.Join(proxy.PackagePrefix(d.Package), d.Binary)
}
