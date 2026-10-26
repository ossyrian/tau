package cmd

import (
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/spf13/cobra"
)

var errNoCore = errors.New("this command is not available")

func (a *App) runInteractive() error {
	if !a.Interactive {
		return errors.New("the interactive interface needs a terminal")
	}
	if a.TUI == nil {
		return errNoCore
	}
	return a.TUI()
}

func (a *App) statusCmd() *cobra.Command {
	return &cobra.Command{Use: "status", Short: "show the container's status", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error { return a.status() }}
}

func (a *App) status() error {
	if a.Core == nil {
		return errNoCore
	}
	st := a.Core.Status()

	state, stateStyle := "not-running", theme.Dim
	switch {
	case st.Container.Running:
		state, stateStyle = "running", theme.OK
	case st.Container.Exists:
		state, stateStyle = "stopped", theme.Warn
	}
	image, imageStyle := st.Image.String(), theme.OK
	switch st.Image {
	case build.ImageUnknown:
		image, imageStyle = "unknown", theme.Dim
	case build.ImageStale, build.ImageMissing:
		imageStyle = theme.Warn
	}
	drift := len(st.Drift.RuntimeOnly) + len(st.Drift.Changed)
	pkgs, pkgStyle := plural(drift, "need a decision", "in sync"), countStyle(drift)
	sk, skStyle := plural(st.SkillsPending, "pending restart", "in sync"), countStyle(st.SkillsPending)
	fixed := func(s lipgloss.Style) func(string) lipgloss.Style { return func(string) lipgloss.Style { return s } }
	a.out().Table([]render.Column{
		{Title: "PROFILE"},
		{Title: "CONTAINER", Style: fixed(stateStyle)},
		{Title: "IMAGE", Style: fixed(imageStyle)},
		{Title: "PACKAGES", Style: fixed(pkgStyle)},
		{Title: "SKILLS", Style: fixed(skStyle)},
	}, [][]string{{st.Profile, strings.ReplaceAll(state, "-", " "), image, pkgs, sk}})

	if len(st.Problems) > 0 {
		return errors.New(strings.Join(st.Problems, "\n"))
	}
	return nil
}

// plural reads "N <what>" when n is not zero, else the fallback.
func plural(n int, what, none string) string {
	if n == 0 {
		return none
	}
	return fmt.Sprintf("%d %s", n, what)
}

func skillStateStyle(s skills.State) lipgloss.Style {
	switch s {
	case skills.Synced:
		return theme.OK
	case skills.Pending, skills.Stale, skills.Configured:
		return theme.Warn
	case skills.Missing:
		return theme.Err
	}
	return theme.Dim
}

// countStyle is yellow when something needs attention.
func countStyle(n int) lipgloss.Style {
	if n > 0 {
		return theme.Warn
	}
	return theme.OK
}

func (a *App) skillsCmd() *cobra.Command {
	needCore := func() error {
		if a.Core == nil {
			return errNoCore
		}
		return nil
	}
	toggle := func(use, short, verb string, activate bool) *cobra.Command {
		return &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if err := needCore(); err != nil {
				return err
			}
			do := a.Core.DeactivateSkill
			if activate {
				do = a.Core.ActivateSkill
			}
			name, err := do(args[0])
			if err != nil {
				return err
			}
			a.out().Done(verb, name, "restart to apply")
			return nil
		}}
	}
	skills := parent("skills", "manage skills",
		&cobra.Command{Use: "list", Short: "list available skills", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			if err := needCore(); err != nil {
				return err
			}
			list, err := a.Core.Skills()
			if err != nil {
				return err
			}
			rows := make([][]string, len(list))
			for k, sk := range list {
				rows[k] = []string{sk.Name, string(sk.State), sk.Path}
			}
			a.profileTable([]render.Column{
				{Title: "NAME"},
				{Title: "STATE", Style: func(c string) lipgloss.Style { return skillStateStyle(skills.State(c)) }},
				{Title: "PATH"},
			}, rows)
			return nil
		}},
		toggle("activate NAME|PATH", "activate a skill", "activated", true),
		toggle("deactivate NAME|PATH", "deactivate a skill", "deactivated", false),
		a.skillPathsCmd(needCore),
	)
	return skills
}

func (a *App) skillPathsCmd(needCore func() error) *cobra.Command {
	change := func(verb, short, past string, add bool) *cobra.Command {
		return &cobra.Command{Use: verb + " PATH", Short: short, Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if err := needCore(); err != nil {
				return err
			}
			do := a.Core.RemoveSkillPath
			if add {
				do = a.Core.AddSkillPath
			}
			if err := do(args[0]); err != nil {
				return err
			}
			a.out().Done(past+" skill path", args[0], "")
			return nil
		}}
	}
	return parent("paths", "manage skill directories",
		&cobra.Command{Use: "list", Short: "list skill directories", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			if err := needCore(); err != nil {
				return err
			}
			paths, err := a.Core.SkillPaths()
			if err != nil {
				return err
			}
			rows := make([][]string, len(paths))
			for k, p := range paths {
				state := "found"
				if !p.Found {
					state = "missing"
				}
				rows[k] = []string{p.Path, state, fmt.Sprint(p.Skills)}
			}
			a.profileTable([]render.Column{
				{Title: "PATH"},
				{Title: "STATE", Style: func(c string) lipgloss.Style {
					if c == "missing" {
						return theme.Err
					}
					return theme.OK
				}},
				{Title: "SKILLS"},
			}, rows)
			return nil
		}},
		change("add", "add a skill directory", "added", true),
		change("rm", "remove a skill directory", "removed", false),
	)
}

func (a *App) envCmd() *cobra.Command {
	needCore := func() error {
		if a.Core == nil {
			return errNoCore
		}
		return nil
	}
	var show bool
	list := &cobra.Command{Use: "list", Short: "list environment variables", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if err := needCore(); err != nil {
			return err
		}
		vars, err := a.Core.EnvVars()
		if err != nil {
			return err
		}
		rows := make([][]string, len(vars))
		for k, v := range vars {
			val := envfile.Mask(v.Value)
			if show {
				val = v.Value
			}
			rows[k] = []string{v.Key, val}
		}
		a.profileTable([]render.Column{{Title: "KEY"}, {Title: "VALUE"}}, rows)
		return nil
	}}
	list.Flags().BoolVar(&show, "show", false, "print values in full")

	set := &cobra.Command{Use: "set KEY VALUE", Short: "set an environment variable", Args: cobra.RangeArgs(1, 2), RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 1 && !strings.Contains(args[0], "=") {
			return errors.New("env set needs KEY VALUE or KEY=VALUE")
		}
		if err := needCore(); err != nil {
			return err
		}
		var key, value string
		if len(args) == 1 {
			key, value, _ = strings.Cut(args[0], "=")
		} else {
			key, value = args[0], args[1]
		}
		if err := a.Core.SetEnv(key, value); err != nil {
			return err
		}
		if envfile.LooksSecret(key) {
			fmt.Fprintf(a.Stderr, "tau: %s looks like a credential. The agent can read every variable here;\n"+
				"     keep credentials out of its reach with: tau proxies env set PROXY %s\n", key, key)
		}
		a.out().Done("set", key, "new sessions get it")
		return nil
	}}
	return parent("env", "manage environment variables",
		list, set,
		&cobra.Command{Use: "unset KEY", Short: "remove an environment variable", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			if err := needCore(); err != nil {
				return err
			}
			removed, err := a.Core.UnsetEnv(args[0])
			if err != nil {
				return err
			}
			if !removed {
				return fmt.Errorf("%s is not set", args[0])
			}
			a.out().Done("unset", args[0], "new sessions drop it")
			return nil
		}},
	)
}
