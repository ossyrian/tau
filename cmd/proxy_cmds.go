package cmd

import (
	"errors"
	"fmt"
	"strings"

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

func (a *App) proxyCmd() *cobra.Command {
	var show bool
	envLs := &cobra.Command{Use: "list PROXY", Short: "list a proxy's environment variables", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		vars, err := a.Core.ProxyVars(args[0])
		if err != nil {
			return err
		}
		pairs := make([]render.Pair, len(vars))
		for i, v := range vars {
			val := proxy.Mask(v.Value)
			if show {
				val = v.Value
			}
			pairs[i] = render.Pair{Key: v.Key, Value: val}
		}
		a.out().Pairs(pairs, "no variables")
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

	return parent("proxy", "manage proxies",
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
	items := make([]render.Item, 0, len(p.Defs))
	for _, n := range p.Names() {
		d := p.Defs[n]
		use, style := "no", theme.Dim
		if on[n] {
			use, style = "yes", theme.OK
		}
		where := "empty directory"
		if d.InAgentDirectory() {
			where = "agent directory"
		}
		details := []render.Detail{
			{Label: "Agent types", Value: d.AgentCommand},
			{Label: "Runs", Value: runsWhat(d)},
			{Label: "Used here", Value: use, Style: style},
			{Label: "Runs in", Value: where, Style: theme.Dim},
		}
		if d.EnvFrom != "" {
			details = append(details, render.Detail{Label: "Credentials", Value: "minted by env_from", Style: theme.Dim})
		}
		if len(d.Deny) > 0 {
			details = append(details, render.Detail{Label: "Denies", Value: strings.Join(d.Deny, "; "), Style: theme.Dim})
		}
		items = append(items, render.Item{Title: n, Note: d.Description, Details: details, Fields: []string{n, d.AgentCommand, use}})
	}
	a.out().List("Proxies", items, "no proxies defined")
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

// runsWhat says where a proxy's program comes from, in the config's words.
func runsWhat(d config.Proxy) string {
	if d.SystemBinary != "" {
		return d.SystemBinary
	}
	return d.Package + ": " + d.Binary
}
