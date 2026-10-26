package tui

import (
	"fmt"

	bkey "charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/envfile"
)

type envLoadedMsg struct {
	profile string
	vars    []envfile.Var
	err     error
}

// envSection is a single table of the profile's environment variables.
type envSection struct {
	api    API
	vars   *varEditor
	err    error
	loaded bool
}

func newEnv(api API) section {
	e := &envSection{api: api, vars: newVarEditor()}
	e.vars.set = func(key, value string) tea.Cmd {
		return run("saving "+key, func() (string, error) {
			note := "saved " + key + " (new sessions get it)"
			if envfile.LooksSecret(key) {
				note += " — the agent can read this; credentials belong in proxies"
			}
			return note, api.SetEnv(key, value)
		})
	}
	e.vars.remove = func(key string) tea.Cmd {
		return confirm(fmt.Sprintf("Delete %s from .env?", key), func() tea.Cmd {
			return run("deleting "+key, func() (string, error) {
				_, err := api.UnsetEnv(key)
				return "deleted " + key + " (new sessions drop it)", err
			})
		})
	}
	return e
}

func (e *envSection) Name() string         { return "env" }
func (e *envSection) Capturing() bool      { return e.vars.Capturing() }
func (e *envSection) Keys() []bkey.Binding { return e.vars.Keys() }

func (e *envSection) Enter() tea.Cmd {
	e.vars.Hide() // secrets are hidden again every time the section opens
	return e.load()
}

func (e *envSection) load() tea.Cmd {
	api := e.api
	return func() tea.Msg {
		v, err := api.EnvVars()
		return envLoadedMsg{api.Status().Profile, v, err}
	}
}

func (e *envSection) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case envLoadedMsg:
		e.err, e.loaded = msg.err, true
		e.vars.Set(msg.profile, msg.vars)
	case reloadMsg:
		return e.load()
	case tea.KeyPressMsg:
		return e.vars.Update(msg)
	}
	return nil
}

func (e *envSection) View(w, h int) string {
	if msg := stateMessage(e.err, e.loaded); msg != "" {
		return messagePane("env", w, h, msg)
	}
	return varsPane("env", w, h, e.vars)
}
