package proxy

import (
	"fmt"
	"strings"

	"github.com/ossyrian/tau/internal/config"
)

// Reloader sends the current proxies and variables to the running servers, and
// mints fresh credentials for the proxies it is asked to.
type Reloader interface {
	Reload() error
	Refresh(names []string) error
}

// Store is one profile's view of proxies: the definitions shared by every
// profile, this profile's active list, and the variables each proxy runs with.
// The CLI and the interactive interface both use it.
type Store struct {
	// DefaultDir is the default profile's directory, where proxies are defined.
	DefaultDir string
	// Dir is this profile's directory; it equals DefaultDir for the default profile.
	Dir     string
	Secrets *Secrets
	// Servers is reloaded after every change. It may be nil.
	Servers Reloader
}

// Proxies returns the shared definitions and this profile's active list.
func (s *Store) Proxies() (config.Proxies, error) {
	defs, err := config.LoadProxies(s.DefaultDir)
	if err != nil {
		return config.Proxies{}, err
	}
	if s.Dir == s.DefaultDir {
		return defs, nil
	}
	return config.LoadProxiesWith(s.Dir, &defs)
}

func (s *Store) defined(name string) (config.Proxies, error) {
	p, err := s.Proxies()
	if err != nil {
		return config.Proxies{}, err
	}
	if _, ok := p.Defs[name]; !ok {
		return config.Proxies{}, fmt.Errorf("no proxy %q (defined: %s)", name, strings.Join(p.Names(), ", "))
	}
	return p, nil
}

// reload tells the running servers. The change is already saved, so a failure
// says so.
func (s *Store) reload(what string) error {
	if s.Servers == nil {
		return nil
	}
	if err := s.Servers.Reload(); err != nil {
		return fmt.Errorf("%s saved, but the proxy servers did not reload: %w (tau proxies reload retries)", what, err)
	}
	return nil
}

// Use lets this profile call a proxy (on) or stops it (off).
func (s *Store) Use(name string, on bool) error {
	p, err := s.defined(name)
	if err != nil {
		return err
	}
	active := append([]string{}, p.Active...)
	idx := -1
	for i, n := range active {
		if n == name {
			idx = i
		}
	}
	switch {
	case on && idx >= 0:
		return fmt.Errorf("%s is already in use", name)
	case !on && idx < 0:
		return fmt.Errorf("%s is not in use", name)
	case on:
		active = append(active, name)
	default:
		active = append(active[:idx], active[idx+1:]...)
	}
	next := p
	next.Active = active
	if err := next.CheckActive(); err != nil {
		return err
	}
	if err := config.SaveProxiesActive(s.Dir, active); err != nil {
		return err
	}
	verb := "stopping " + name
	if on {
		verb = "using " + name
	}
	return s.reload(verb)
}

// Vars lists a proxy's stored variables.
func (s *Store) Vars(name string) ([]Var, error) {
	if _, err := s.defined(name); err != nil {
		return nil, err
	}
	return s.Secrets.Vars(name)
}

// SetVar stores a variable for a proxy and reloads the servers.
func (s *Store) SetVar(name, key, value string) error {
	if _, err := s.defined(name); err != nil {
		return err
	}
	if err := s.Secrets.Set(name, key, value); err != nil {
		return err
	}
	return s.reload("variable " + key)
}

// UnsetVar removes a variable and reloads the servers.
func (s *Store) UnsetVar(name, key string) (bool, error) {
	if _, err := s.defined(name); err != nil {
		return false, err
	}
	removed, err := s.Secrets.Unset(name, key)
	if err != nil || !removed {
		return removed, err
	}
	return true, s.reload("removal of " + key)
}

// Reload sends the current proxies and variables to the running servers.
func (s *Store) Reload() error {
	if s.Servers == nil {
		return fmt.Errorf("proxy servers are not available")
	}
	return s.Servers.Reload()
}

// Refresh mints fresh credentials for the named proxies and sends them to the
// servers. With no names it refreshes every proxy in use. Every name must be a
// proxy this profile can see; the refresh reaches every profile that uses it.
func (s *Store) Refresh(names []string) error {
	if s.Servers == nil {
		return fmt.Errorf("proxy servers are not available")
	}
	for _, n := range names {
		if _, err := s.defined(n); err != nil {
			return err
		}
	}
	return s.Servers.Refresh(names)
}
