package cmd

import (
	"errors"
	"fmt"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/spf13/cobra"
)

func (a *App) profileCmd() *cobra.Command {
	return parent("profile", "manage profiles",
		&cobra.Command{Use: "list", Short: "list profiles", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
			return a.profileList()
		}},
		&cobra.Command{Use: "new NAME", Short: "create a profile", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
			p, err := profile.Set{Root: a.tauHome()}.Create(args[0])
			if err != nil {
				return err
			}
			a.out().Done("created profile", p.Name, "tau build, then tau restart")
			return nil
		}},
		&cobra.Command{Use: "workspaces NAME MODE", Short: "set a profile's workspace mode (shared or isolated)", Args: cobra.ExactArgs(2), RunE: func(_ *cobra.Command, args []string) error {
			p, err := profile.Set{Root: a.tauHome()}.Select(args[0])
			if err != nil {
				return err
			}
			if p.Name == profile.Default {
				return errors.New("the default profile owns the shared workspace; set the mode on another profile")
			}
			if err := config.SaveWorkspaces(p.Dir, config.Workspaces{Mode: args[1]}); err != nil {
				return err
			}
			a.out().Done("workspaces "+args[1], p.Name, "tau build, then tau restart")
			return nil
		}},
	)
}

func (a *App) profileList() error {
	profiles, err := a.allProfiles()
	if err != nil {
		return err
	}
	items := make([]render.Item, len(profiles))
	for i, p := range profiles {
		mode := "owner"
		if p.Name != profile.Default {
			w, err := config.LoadWorkspaces(p.Dir)
			if err != nil {
				return fmt.Errorf("profile %q: %w", p.Name, err)
			}
			mode = w.Mode
		}
		items[i] = render.Item{
			Title:  p.Name,
			Fields: []string{p.Name, p.User, mode},
			Details: []render.Detail{
				{Label: "User", Value: p.User, Style: theme.Dim},
				{Label: "Workspace", Value: mode, Style: theme.Dim},
			},
		}
	}
	a.out().List("Profiles", items, "no profiles")
	return nil
}
