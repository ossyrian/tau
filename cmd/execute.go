package cmd

import (
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/dockercli"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/tui"
	"golang.org/x/term"
)

// Execute runs tau against the real process: its terminal, environment and
// Docker. Cobra prints errors and usage; a failed command exits 1.
func Execute() {
	if err := newApp().NewRootCmd().Execute(); err != nil {
		exit(1)
	}
}

// exit ends the process; tests replace it.
var exit = os.Exit

// newApp wires an App to the real process.
func newApp() *App {
	env := newEnvironment()
	def, _ := env.set.Get(profile.Default)
	app := &App{
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Env:         os.Getenv,
		Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		Pretty:      term.IsTerminal(int(os.Stdout.Fd())),
		NoColor:     os.Getenv("NO_COLOR") != "",
		Docker: func(args []string, dockerfile string) error {
			return dockercli.CLI{}.WithStdin(args, dockerfile, os.Stdout)
		},
		Resolve:   runResolve,
		Lifecycle: env.life,
		Profiles:  env.set.List,
		Core:      env.core(def),
		Sessions:  env.sessions(def),
	}
	app.Select = func(name string) error {
		p, err := env.set.Select(name)
		if err != nil {
			return err
		}
		app.Core, app.Sessions = env.core(p), env.sessions(p)
		return nil
	}
	app.TUI = func() error {
		_, err := tea.NewProgram(tui.NewRoot(app.Core)).Run()
		return err
	}
	return app
}
