// Package cli implements the tau command line. All outside effects (Docker,
// the terminal, the environment) are fields of App, so tests run without them.
package cmd

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/spf13/cobra"
)

const imageTag = "tau:latest"

// App is the tau CLI with its dependencies.
type App struct {
	Stdout, Stderr io.Writer
	// Env looks up an environment variable; "" means unset.
	Env func(string) string
	// Interactive is true when stdin and stdout are terminals.
	Interactive bool
	// Pretty is true when stdout is a terminal: output is coloured.
	Pretty bool
	// NoColor drops colour even on a terminal (the NO_COLOR convention).
	NoColor bool
	// Now is the clock for "3 hours ago"; nil means time.Now.
	Now func() time.Time
	// Catalog holds the shipped package definitions; may be nil.
	Catalog fs.FS
	// Docker runs `docker <args>` with dockerfile on stdin.
	Docker func(args []string, dockerfile string) error
	// Resolve shows the drift-resolution screen for c.
	Resolve func(c *config.Config) error
	// Sessions runs the `tau sessions` commands.
	Sessions *session.Manager
	// Lifecycle runs start, stop and restart.
	Lifecycle Lifecycle
	// Core is the service layer shared with the TUI.
	Core *core.Core
	// TUI runs the interactive interface.
	TUI func() error

	// Profiles lists every profile, the default first. Nil means only the
	// default profile, in the tau home.
	Profiles func() ([]profile.Profile, error)
	// Select switches Core, Sessions and Lifecycle to the named profile. It
	// runs before every command. Nil means they are already set.
	Select func(name string) error
	// profileName is the selected profile's name.
	profileName string
	// dir is the selected profile's directory; empty means the tau home.
	dir string
}

// Lifecycle creates, removes and recreates the container.
type Lifecycle interface {
	Start() (container.Outcome, error)
	Stop() (container.Outcome, error)
	Restart() (container.Outcome, error)
}

// NewRootCmd builds the tau command tree. main runs it with Execute; tests run
// it against buffers.
func (a *App) NewRootCmd() *cobra.Command {
	var interactive bool
	var profileName string
	root := &cobra.Command{
		Use:   "tau",
		Short: "a sandbox for the Pi coding agent",
		// A failing command prints its error, not the usage; a wrong call
		// (checked before this hook runs) still prints the usage.
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cmd.SilenceUsage = true
			return a.selectProfile(profileName)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !interactive {
				return cmd.Help()
			}
			return a.runInteractive()
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().BoolVarP(&interactive, "interactive", "i", false, "open the interactive interface")
	root.PersistentFlags().StringVarP(&profileName, "profile", "p", "", "act on the profile `NAME` (default: $TAU_PROFILE, else the default profile)")

	root.AddCommand(
		a.statusCmd(),
		a.buildCmd(),
		a.lifecycleCmd("start", "start the container"),
		a.lifecycleCmd("stop", "stop the container"),
		a.lifecycleCmd("restart", "restart the container"),
		a.skillsCmd(),
		a.packagesCmd(),
		a.workspacesCmd(),
		a.profilesCmd(),
		a.proxiesCmd(),
		a.sessionsCmd(),
		a.shellCmd(),
		hidden(a.envCmd()),
	)
	return root
}

// parent makes a command that only holds subcommands: on its own it shows its
// help, and a word that is not a subcommand is an error.
func parent(use, short string, subs ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{
		Use: use, Short: short, Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	c.AddCommand(subs...)
	return c
}

func hidden(c *cobra.Command) *cobra.Command {
	c.Hidden = true
	return c
}

// tauHome is the directory that holds the default profile and profiles/.
func (a *App) tauHome() string {
	if d := a.Env("TAU_HOME"); d != "" {
		return d
	}
	return filepath.Join(a.Env("HOME"), ".tau")
}

// configDir is the selected profile's directory.
func (a *App) configDir() string {
	if a.dir != "" {
		return a.dir
	}
	return a.tauHome()
}

// selectProfile points the app at the named profile: the --profile flag, else
// $TAU_PROFILE, else the default.
func (a *App) selectProfile(flag string) error {
	name := flag
	if name == "" {
		name = a.Env("TAU_PROFILE")
	}
	set := profile.Set{Root: a.tauHome()}
	p, err := set.Select(name)
	if err != nil {
		return err
	}
	a.dir, a.profileName = p.Dir, p.Name
	if a.Select == nil {
		return nil
	}
	return a.Select(p.Name)
}

// allProfiles lists every profile, the default first.
func (a *App) allProfiles() ([]profile.Profile, error) {
	if a.Profiles != nil {
		return a.Profiles()
	}
	p, err := profile.Set{Root: a.tauHome()}.Get(profile.Default)
	return []profile.Profile{p}, err
}

func (a *App) load() (*config.Config, error) {
	return config.Load(a.configDir(), a.Catalog)
}

// defaultTag is the image tag: TAU_IMAGE, else tau:latest.
func (a *App) defaultTag() string {
	if t := a.Env("TAU_IMAGE"); t != "" {
		return t
	}
	return imageTag
}

func (a *App) buildCmd() *cobra.Command {
	var (
		print, noCache, ignore bool
		repo, tag              string
	)
	c := &cobra.Command{Use: "build", Short: "build the container image", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if repo == "" {
			repo = a.Env("TAU_REPO")
		}
		if repo == "" {
			repo = "."
		}
		builder := &build.Builder{
			Repo: repo, Tag: tag,
			Run: func(dargs []string, dockerfile string, _ io.Writer) error { return a.Docker(dargs, dockerfile) },
		}
		cfgs, err := a.settleAll(ignore)
		if err != nil {
			return err
		}
		plan, err := builder.Plan(cfgs)
		if err != nil {
			return err
		}
		if print {
			fmt.Fprint(a.Stdout, plan.Dockerfile)
			return nil
		}
		return builder.Build(plan, noCache, a.Stdout)
	}}
	f := c.Flags()
	f.BoolVar(&print, "print", false, "print the Dockerfile instead of building")
	f.BoolVar(&noCache, "no-cache", false, "do not use the Docker build cache")
	f.BoolVar(&ignore, "ignore-drift", false, "build from config.yaml and ignore config.runtime.yaml")
	f.StringVar(&repo, "repo", "", "use the Dockerfile in `DIR`")
	f.StringVar(&tag, "tag", a.defaultTag(), "tag the image as `NAME`")
	return c
}

// settleAll loads every profile's config for a build. Unless ignore is set,
// it makes sure no profile has drift that needs a decision. In a terminal it
// shows the resolve screen; otherwise it fails.
func (a *App) settleAll(ignore bool) ([]build.ProfileConfig, error) {
	profiles, err := a.allProfiles()
	if err != nil {
		return nil, err
	}
	out := make([]build.ProfileConfig, 0, len(profiles))
	for _, p := range profiles {
		c, err := a.settle(p, ignore)
		if err != nil {
			return nil, err
		}
		out = append(out, build.ProfileConfig{Profile: p, Config: c})
	}
	return out, nil
}

func (a *App) settle(p profile.Profile, ignore bool) (*config.Config, error) {
	c, err := config.Load(p.Dir, a.Catalog)
	if err != nil {
		return nil, fmt.Errorf("profile %q: %w", p.Name, err)
	}
	if ignore || !c.Diff().NeedsResolution() {
		return c, nil
	}
	where := ""
	if p.Name != profile.Default {
		where = " in profile " + p.Name
	}
	if !a.Interactive {
		return nil, fmt.Errorf("config.runtime.yaml differs from config.yaml%s (%s).\n"+
			"Run `tau packages resolve` in a terminal, or pass --ignore-drift to build from config.yaml only",
			where, strings.Join(driftNames(c.Diff()), ", "))
	}
	if err := a.Resolve(c); err != nil {
		return nil, err
	}
	if c, err = config.Load(p.Dir, a.Catalog); err != nil {
		return nil, err
	}
	if c.Diff().NeedsResolution() {
		return nil, fmt.Errorf("unresolved differences remain%s (%s); nothing was built",
			where, strings.Join(driftNames(c.Diff()), ", "))
	}
	return c, nil
}

func driftNames(d config.Diff) []string {
	return append(append([]string{}, d.RuntimeOnly...), d.Changed...)
}

func (a *App) packagesCmd() *cobra.Command {
	var exit bool
	diff := &cobra.Command{Use: "diff", Short: "show how config.runtime.yaml differs from config.yaml", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		c, err := a.load()
		if err != nil {
			return err
		}
		d := c.Diff()
		var rows [][]string
		for _, g := range []struct {
			kind  string
			names []string
		}{{"runtime-only", d.RuntimeOnly}, {"changed", d.Changed}, {"base-only", d.BaseOnly}} {
			for _, n := range g.names {
				rows = append(rows, []string{g.kind, n})
			}
		}
		a.profileTable([]render.Column{{Title: "CHANGE"}, {Title: "PACKAGE"}}, rows)
		if exit && d.NeedsResolution() {
			return errors.New("drift needs a decision")
		}
		return nil
	}}
	diff.Flags().BoolVar(&exit, "exit-code", false, "exit 1 when drift needs a decision")

	return parent("packages", "manage packages",
		&cobra.Command{Use: "list", Short: "list packages", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			c, err := a.load()
			if err != nil {
				return err
			}
			entries, err := c.Resolve()
			if err != nil {
				return err
			}
			rows := make([][]string, len(entries))
			for i, e := range entries {
				install := "script"
				if len(e.Apt) > 0 {
					install = "apt: " + strings.Join(e.Apt, ", ")
				}
				rows[i] = []string{e.Name, e.As, strings.Join(e.Needs, ", "), install}
			}
			a.profileTable([]render.Column{{Title: "NAME"}, {Title: "AS"}, {Title: "NEEDS"}, {Title: "INSTALL"}}, rows)
			return nil
		}},
		diff,
		&cobra.Command{Use: "resolve", Short: "resolve differences between the config files", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			c, err := a.load()
			if err != nil {
				return err
			}
			if !c.Diff().NeedsResolution() {
				a.out().Note("no drift to resolve")
				return nil
			}
			if !a.Interactive {
				return errors.New("resolving needs a terminal")
			}
			return a.Resolve(c)
		}},
	)
}

// nameFlag is --name for session new. A second --name is an error, not an
// override.
type nameFlag struct{ value string }

func (n *nameFlag) String() string { return n.value }
func (n *nameFlag) Type() string   { return "name" }
func (n *nameFlag) Set(v string) error {
	if n.value != "" {
		return errors.New("name given twice")
	}
	if v == "" {
		return errors.New("name is empty")
	}
	n.value = v
	return nil
}

func (a *App) sessionsCmd() *cobra.Command {
	needSessions := func() error {
		if a.Sessions == nil {
			return errors.New("sessions are not available")
		}
		return nil
	}

	var name nameFlag
	newCmd := &cobra.Command{Use: "new [NAME]", Short: "start a new session", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 1 && name.value != "" {
			return errors.New("give the name once, either as an argument or with --name")
		}
		if err := needSessions(); err != nil {
			return err
		}
		n := name.value
		if len(args) == 1 {
			n = args[0]
		}
		n, err := a.Sessions.Create(n)
		if err != nil {
			return err
		}
		a.out().Done("started session", n, "")
		return a.Sessions.AttachNew(n)
	}}
	newCmd.Flags().Var(&name, "name", "name the session `NAME`")

	return parent("sessions", "manage sessions",
		newCmd,
		&cobra.Command{Use: "list", Short: "list sessions", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			if err := needSessions(); err != nil {
				return err
			}
			return a.sessionList()
		}},
		&cobra.Command{Use: "attach NAME", Short: "attach to a session", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if err := needSessions(); err != nil {
				return err
			}
			return a.Sessions.Attach(args[0])
		}},
		&cobra.Command{Use: "kill NAME", Short: "end a session", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if err := needSessions(); err != nil {
				return err
			}
			n, err := a.Sessions.Kill(args[0])
			if err != nil {
				return err
			}
			a.out().Done("killed", n, "")
			return nil
		}},
	)
}

func (a *App) sessionList() error {
	list := a.Sessions.Sessions
	if a.Core != nil {
		list = a.Core.Sessions // every profile's sessions
	}
	infos, err := list()
	if err != nil {
		return err
	}
	rows := make([][]string, len(infos))
	for i, s := range infos {
		rows[i] = []string{s.Profile, s.Name, s.Kind, a.ago(s.Created), a.ago(s.Active)}
	}
	a.out().Table([]render.Column{
		{Title: "PROFILE"},
		{Title: "NAME", Style: func(string) lipgloss.Style { return theme.Info }},
		{Title: "TYPE"}, {Title: "CREATED"}, {Title: "LAST ACTIVE"},
	}, rows)
	return nil
}

// shellCmd opens a shell in the container, as the selected profile's user.
func (a *App) shellCmd() *cobra.Command {
	return &cobra.Command{Use: "shell", Short: "open a shell in the container", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if a.Sessions == nil {
			return errors.New("sessions are not available")
		}
		return a.Sessions.Shell()
	}}
}

func (a *App) lifecycleCmd(name, short string) *cobra.Command {
	return &cobra.Command{Use: name, Short: short, Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if a.Lifecycle == nil {
			return errors.New("container control is not available")
		}
		do := a.Lifecycle.Restart
		switch name {
		case "start":
			do = a.Lifecycle.Start
		case "stop":
			do = a.Lifecycle.Stop
		}
		o, err := do()
		if err != nil {
			return err
		}
		text, hint := o.Text()
		a.out().Outcome(string(o), text, hint)
		return nil
	}}
}
