package cmd

import (
	"fmt"

	"github.com/ossyrian/tau/internal/render"
	"github.com/ossyrian/tau/internal/theme"
	"github.com/ossyrian/tau/internal/workspace"
	"github.com/spf13/cobra"
)

// note prints a message for the person at the terminal. Scripts and the shell
// wrappers read stdout only, so messages go to stderr.
func (a *App) note(format string, args ...any) {
	fmt.Fprintf(a.Stderr, "tau: "+format+"\n", args...)
}

func (a *App) workspaceCmd() *cobra.Command {
	var name string
	var live, cd bool
	add := &cobra.Command{
		Use: "add [DIR]", Short: "add a project to the workspace",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if a.Core == nil {
				return errNoCore
			}
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			res, err := a.Core.AddWorkspace(dir, name, live)
			if err != nil {
				return err
			}
			a.reportAdd(res)
			// stdout: the host path, for the shell wrappers that cd to it
			fmt.Fprintln(a.Stdout, res.Entry.Host)
			return nil
		},
	}
	add.Flags().StringVar(&name, "name", "", "name the entry `NAME` (default: the directory's name)")
	add.Flags().BoolVar(&live, "live", false, "mount the real directory: the agent edits your files")
	add.Flags().BoolVar(&cd, "cd", false, "change this shell to the directory (needs tau.fish or tau.bash)")

	ls := &cobra.Command{Use: "list", Short: "list workspace projects", Args: cobra.NoArgs, RunE: func(_ *cobra.Command, _ []string) error {
		if a.Core == nil {
			return errNoCore
		}
		entries, err := a.Core.Workspaces()
		if err != nil {
			return err
		}
		items := make([]render.Item, len(entries))
		for i, e := range entries {
			items[i] = render.Item{
				Title:  e.Name,
				Fields: []string{e.Name, string(e.Kind), e.Container, e.Host},
				Details: []render.Detail{
					{Label: "Kind", Value: kindText(e.Kind)},
					{Label: "Agent sees", Value: e.Container, Style: theme.Dim},
					{Label: "On this machine", Value: e.Host, Style: theme.Dim},
				},
			}
		}
		a.out().List("Workspace", items, "no workspaces")
		return nil
	}}

	rm := &cobra.Command{Use: "rm NAME", Aliases: []string{"remove"}, Short: "remove a project from the workspace", Args: cobra.ExactArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if a.Core == nil {
			return errNoCore
		}
		e, err := a.Core.RemoveWorkspace(args[0])
		if err != nil {
			return err
		}
		if e.Kind == workspace.Live {
			a.note("unregistered %s: your directory %s is untouched", e.Name, e.Host)
			a.restartNote()
		} else {
			a.note("removed the copy %s: the original is untouched; edits the agent made to the copy are gone", e.Name)
		}
		return nil
	}}

	cdc := &cobra.Command{Use: "cd [NAME]", Short: "print a project's directory on this machine", Args: cobra.MaximumNArgs(1), RunE: func(_ *cobra.Command, args []string) error {
		if a.Core == nil {
			return errNoCore
		}
		n := ""
		if len(args) == 1 {
			n = args[0]
		}
		p, err := a.Core.WorkspacePath(n)
		if err != nil {
			return err
		}
		fmt.Fprintln(a.Stdout, p)
		return nil
	}}
	return parent("workspace", "manage workspace projects", add, ls, rm, cdc)
}

func kindText(k workspace.Kind) string {
	if k == workspace.Live {
		return "live (your real directory)"
	}
	return "copy"
}

// reportAdd tells the person what Add did. Everything goes to stderr.
func (a *App) reportAdd(r workspace.Result) {
	e := r.Entry
	switch {
	case e.Kind == workspace.Live && r.Action == workspace.Unchanged:
		a.note("%s is already live", e.Name)
	case e.Kind == workspace.Live:
		a.note("added %s: %s will appear at %s", e.Name, e.Host, e.Container)
		a.note("WARNING: a live directory is mounted read/write. The agent can edit or delete your files.")
		a.restartNote()
	case r.Action == workspace.Refreshed:
		a.note("refreshed %s from its source: %s", e.Name, e.Container)
	default:
		a.note("added %s: %s", e.Name, e.Container)
	}
}

// restartNote says when a change to the mounts takes effect.
func (a *App) restartNote() {
	if a.Core != nil && a.Core.ContainerRunning() {
		a.note("applies on the next tau restart (running sessions will be lost)")
		return
	}
	a.note("applies on the next tau start")
}

// workspaceStoreFor builds the store that really copies files.
func workspaceStoreFor(dir, root, home, mount string) *workspace.Store {
	return &workspace.Store{
		Dir: dir, Root: root, Home: home, Mount: mount,
		Clone: workspace.CloneDir, Merge: workspace.MergeDir,
	}
}
