// Package core holds what the CLI and the TUI share: one gathered view of
// tau's state, and one method per user action. A front end only decides how
// to ask and how to show the answer.
package core

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strings"

	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/workspace"
)

// The services Core drives. The real types are *container.Lifecycle,
// *session.Manager, *skills.Store and *build.Builder.
type (
	Lifecycle interface {
		Start() (container.Outcome, error)
		Stop() (container.Outcome, error)
		Restart() (container.Outcome, error)
		State() (container.State, error)
	}
	Sessions interface {
		Sessions() ([]session.Info, error)
		Preview(n int) (map[string][]string, error)
		Create(name string) (string, error)
		Kill(name string) (string, error)
		AttachArgs(name string) ([]string, error)
	}
	SkillStore interface {
		List() ([]skills.Skill, error)
		Activate(nameOrPath string) (string, error)
		Deactivate(nameOrPath string) (string, error)
		Paths() ([]skills.PathInfo, error)
		AddPath(path string) error
		RemovePath(path string) error
		RenamePath(old, to string) error
		Drift() (int, error)
	}
	// ProxyStore holds proxy definitions and their variables.
	ProxyStore interface {
		Proxies() (config.Proxies, error)
		Use(name string, on bool) error
		Vars(proxy string) ([]proxy.Var, error)
		SetVar(proxy, key, value string) error
		UnsetVar(proxy, key string) (bool, error)
		Reload() error
		Refresh(names []string) error
	}
	// WorkspaceStore manages the profile's workspace entries.
	WorkspaceStore interface {
		Add(src, name string, live bool) (workspace.Result, error)
		List() ([]workspace.Entry, error)
		Remove(name string) (workspace.Entry, error)
		Path(name string) (string, error)
	}
	Builder interface {
		Plan([]build.ProfileConfig) (build.Plan, error)
		Build(p build.Plan, noCache bool, out io.Writer) error
		Status(hash string) (build.ImageStatus, error)
	}
)

// Core ties the services together for one profile: its config, skills, env
// and sessions. The container and the image are shared by every profile.
type Core struct {
	// Profile is the profile this Core acts on.
	Profile profile.Profile
	Home    string // the profile's directory (holds config.yaml)
	Catalog fs.FS  // shipped package definitions; may be nil
	EnvFile string

	Life   Lifecycle
	Sess   Sessions
	Skill  SkillStore
	Images Builder
	// Proxy manages proxies for this profile. It may be nil.
	Proxy ProxyStore
	// Workspace manages this profile's workspace entries. It may be nil.
	Workspace WorkspaceStore
	// Profiles lists every profile, default first. It is what a build covers.
	Profiles func() ([]profile.Profile, error)
	// Others lists the Core of every profile, default first. Sessions lists
	// all of them together. It may be nil in a setup with one profile.
	Others func() ([]*Core, error)
}

// Status is a snapshot of everything the home screen and `tau status` show.
// Parts that could not be read are listed in Problems and left empty.
type Status struct {
	Container       container.State
	Image           build.ImageStatus
	Drift           config.Diff
	NeedsResolution bool
	SkillsPending   int
	Profile         string
	Problems        []string
}

// Status gathers the snapshot. It never fails as a whole: each part that
// fails adds a Problem and the rest is still filled in.
func (c *Core) Status() Status {
	st := Status{Profile: c.Profile.Name}
	problem := func(what string, err error) {
		st.Problems = append(st.Problems, fmt.Sprintf("%s: %v", what, err))
	}

	var err error
	if st.Container, err = c.Life.State(); err != nil {
		problem("container", err)
	}

	cfg, err := c.LoadConfig()
	if err != nil {
		problem("config", err)
	} else {
		st.Drift = cfg.Diff()
		st.NeedsResolution = st.Drift.NeedsResolution()
		if plan, err := c.plan(); err != nil {
			problem("image", err)
		} else if st.Image, err = c.Images.Status(plan.Hash); err != nil {
			st.Image = build.ImageUnknown
			problem("image", err)
		}
	}

	if st.Container.Running {
		if n, err := c.Skill.Drift(); err != nil {
			problem("skills", err)
		} else {
			st.SkillsPending = n
		}
	}
	return st
}

// LoadConfig reads the profile's config.yaml and config.runtime.yaml.
func (c *Core) LoadConfig() (*config.Config, error) {
	return config.Load(c.Home, c.Catalog)
}

// plan works out the build of every profile. The image holds them all, so one
// profile's status depends on the others' packages.
func (c *Core) plan() (build.Plan, error) {
	profiles, err := c.profilesOrSelf()
	if err != nil {
		return build.Plan{}, err
	}
	var cfgs []build.ProfileConfig
	for _, p := range profiles {
		cfg, err := config.Load(p.Dir, c.Catalog)
		if err != nil {
			return build.Plan{}, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		cfgs = append(cfgs, build.ProfileConfig{Profile: p, Config: cfg})
	}
	return c.Images.Plan(cfgs)
}

func (c *Core) profilesOrSelf() ([]profile.Profile, error) {
	if c.Profiles == nil {
		return []profile.Profile{c.Profile}, nil
	}
	return c.Profiles()
}

// DriftError means config.runtime.yaml holds changes nobody has decided on.
type DriftError struct {
	Profile string
	Names   []string
}

func (e *DriftError) Error() string {
	where := ""
	if e.Profile != "" && e.Profile != profile.Default {
		where = " in profile " + e.Profile
	}
	return "config.runtime.yaml differs from config.yaml" + where + " (" + strings.Join(e.Names, ", ") + "): resolve the differences first"
}

// Build builds the one image from every profile's config.yaml. It refuses
// while any profile has drift that needs a decision, so a build never
// silently drops a package the agent installed.
func (c *Core) Build(noCache bool, out io.Writer) error {
	profiles, err := c.profilesOrSelf()
	if err != nil {
		return err
	}
	for _, p := range profiles {
		cfg, err := config.Load(p.Dir, c.Catalog)
		if err != nil {
			return fmt.Errorf("profile %q: %w", p.Name, err)
		}
		if d := cfg.Diff(); d.NeedsResolution() {
			return &DriftError{Profile: p.Name, Names: append(append([]string{}, d.RuntimeOnly...), d.Changed...)}
		}
	}
	plan, err := c.plan()
	if err != nil {
		return err
	}
	return c.Images.Build(plan, noCache, out)
}

// Start, Stop and Restart control the container.
func (c *Core) Start() (container.Outcome, error)   { return c.Life.Start() }
func (c *Core) Stop() (container.Outcome, error)    { return c.Life.Stop() }
func (c *Core) Restart() (container.Outcome, error) { return c.Life.Restart() }

// Sessions lists the sessions of every profile when Others is set, else of
// this profile only.
func (c *Core) Sessions() ([]session.Info, error) {
	if c.Others == nil {
		return c.Sess.Sessions()
	}
	cores, err := c.Others()
	if err != nil {
		return nil, err
	}
	var all []session.Info
	for _, o := range cores {
		infos, err := o.Sess.Sessions()
		if err != nil {
			return nil, err
		}
		all = append(all, infos...)
	}
	return all, nil
}

// SessionKey identifies a session across profiles.
func SessionKey(i session.Info) string { return i.Profile + "/" + i.Name }

// Preview returns the last n lines of every session's screen, keyed by
// SessionKey.
func (c *Core) Preview(n int) (map[string][]string, error) {
	cores := []*Core{c}
	if c.Others != nil {
		var err error
		if cores, err = c.Others(); err != nil {
			return nil, err
		}
	}
	out := map[string][]string{}
	for _, o := range cores {
		infos, err := o.Sess.Sessions()
		if err != nil {
			return nil, err
		}
		if len(infos) == 0 {
			continue
		}
		prev, err := o.Sess.Preview(n)
		if err != nil {
			return nil, err
		}
		for _, i := range infos {
			out[SessionKey(i)] = prev[i.Name]
		}
	}
	return out, nil
}
func (c *Core) CreateSession(name string) (string, error) { return c.Sess.Create(name) }

// KillSession ends the session called name in the given profile.
func (c *Core) KillSession(profile, name string) (string, error) {
	o, err := c.of(profile)
	if err != nil {
		return "", err
	}
	return o.Sess.Kill(name)
}

// AttachArgs returns the docker arguments that attach a terminal to the
// session called name in the given profile.
func (c *Core) AttachArgs(profile, name string) ([]string, error) {
	o, err := c.of(profile)
	if err != nil {
		return nil, err
	}
	return o.Sess.AttachArgs(name)
}

// of returns the Core of the named profile. An empty name, or a Core with no
// others, means this one.
func (c *Core) of(name string) (*Core, error) {
	if name == "" || name == c.Profile.Name || c.Others == nil {
		return c, nil
	}
	cores, err := c.Others()
	if err != nil {
		return nil, err
	}
	for _, o := range cores {
		if o.Profile.Name == name {
			return o, nil
		}
	}
	return nil, fmt.Errorf("no profile %q", name)
}

// Skills and the actions on them.
func (c *Core) Skills() ([]skills.Skill, error)             { return c.Skill.List() }
func (c *Core) ActivateSkill(name string) (string, error)   { return c.Skill.Activate(name) }
func (c *Core) DeactivateSkill(name string) (string, error) { return c.Skill.Deactivate(name) }
func (c *Core) SkillPaths() ([]skills.PathInfo, error)      { return c.Skill.Paths() }
func (c *Core) AddSkillPath(path string) error              { return c.Skill.AddPath(path) }
func (c *Core) RemoveSkillPath(path string) error           { return c.Skill.RemovePath(path) }
func (c *Core) RenameSkillPath(old, to string) error        { return c.Skill.RenamePath(old, to) }

// Workspace entries: copies of projects and live (bind-mounted) directories.
func (c *Core) Workspaces() ([]workspace.Entry, error) {
	if c.Workspace == nil {
		return nil, errNoWorkspace
	}
	return c.Workspace.List()
}

// AddWorkspace copies a directory into the workspace, or with live mounts the
// real directory there. A live entry takes effect when the container is
// recreated.
func (c *Core) AddWorkspace(src, name string, live bool) (workspace.Result, error) {
	if c.Workspace == nil {
		return workspace.Result{}, errNoWorkspace
	}
	return c.Workspace.Add(src, name, live)
}

// RemoveWorkspace deletes a copy, or unregisters a live directory. A live
// source is never touched.
func (c *Core) RemoveWorkspace(name string) (workspace.Entry, error) {
	if c.Workspace == nil {
		return workspace.Entry{}, errNoWorkspace
	}
	return c.Workspace.Remove(name)
}

// WorkspacePath is the host directory of an entry, or of the workspace for "".
func (c *Core) WorkspacePath(name string) (string, error) {
	if c.Workspace == nil {
		return "", errNoWorkspace
	}
	return c.Workspace.Path(name)
}

// ContainerRunning reports whether the container is up. A state that cannot be
// read counts as not running.
func (c *Core) ContainerRunning() bool {
	st, err := c.Life.State()
	return err == nil && st.Running
}

var errNoWorkspace = errors.New("workspaces are not available")

// Proxies and their variables. Variables never leave this layer unmasked
// unless the caller asks to see them.
func (c *Core) Proxies() (config.Proxies, error) {
	if c.Proxy == nil {
		return config.Proxies{}, nil
	}
	return c.Proxy.Proxies()
}

// UseProxy lets this profile call a proxy (on) or stops it (off).
func (c *Core) UseProxy(name string, on bool) error {
	if c.Proxy == nil {
		return errNoProxies
	}
	return c.Proxy.Use(name, on)
}

func (c *Core) ProxyVars(name string) ([]proxy.Var, error) {
	if c.Proxy == nil {
		return nil, errNoProxies
	}
	return c.Proxy.Vars(name)
}

func (c *Core) SetProxyVar(name, key, value string) error {
	if c.Proxy == nil {
		return errNoProxies
	}
	return c.Proxy.SetVar(name, key, value)
}

func (c *Core) UnsetProxyVar(name, key string) (bool, error) {
	if c.Proxy == nil {
		return false, errNoProxies
	}
	return c.Proxy.UnsetVar(name, key)
}

// ReloadProxies sends the current proxies and variables to the running servers.
func (c *Core) ReloadProxies() error {
	if c.Proxy == nil {
		return errNoProxies
	}
	return c.Proxy.Reload()
}

// RefreshProxies mints fresh credentials for the named proxies and sends them
// to those servers only. With no names it refreshes every proxy in use.
func (c *Core) RefreshProxies(names []string) error {
	if c.Proxy == nil {
		return errNoProxies
	}
	return c.Proxy.Refresh(names)
}

var errNoProxies = errors.New("proxies are not available")

// Env variables in the profile's .env file. Each new session of the profile
// starts with them; running sessions keep what they had.
func (c *Core) EnvVars() ([]envfile.Var, error)   { return envfile.List(c.EnvFile) }
func (c *Core) SetEnv(key, value string) error    { return envfile.Set(c.EnvFile, key, value) }
func (c *Core) UnsetEnv(key string) (bool, error) { return envfile.Unset(c.EnvFile, key) }
