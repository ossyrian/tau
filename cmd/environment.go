package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/dockercli"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/tui"
	"golang.org/x/term"
)

const containerName = "tau"

// terminalWidth is the width of stdout in columns, or 0 when it is not a
// terminal or the size is unknown.
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}
	return w
}

func runResolve(c *config.Config) error {
	final, err := tea.NewProgram(tui.NewResolve(c)).Run()
	if err != nil {
		return err
	}
	fmt.Println(final.(tui.Resolve).Summary())
	return nil
}

// environment is the services shared by every profile, plus a way to build the
// per-profile ones.
type environment struct {
	home, tauHome string
	set           profile.Set
	docker        dockercli.CLI
	life          *container.Lifecycle
	builder       *build.Builder
	piCmd         string
	name, image   string // container name and image tag

	cores    map[string]*core.Core
	managers map[string]*session.Manager

	secrets *proxy.Secrets
}

func newEnvironment() *environment {
	home, _ := os.UserHomeDir()
	tauHome := pathOr(filepath.Join(home, ".tau"), "TAU_HOME")
	e := &environment{
		home: home, tauHome: tauHome, set: profile.Set{Root: tauHome},
		piCmd: pathOr("pi", "PI_CMD"), name: pathOr(containerName, "TAU_CONTAINER"), image: pathOr(imageTag, "TAU_IMAGE"),
		cores: map[string]*core.Core{}, managers: map[string]*session.Manager{},
	}
	e.secrets = &proxy.Secrets{Dir: e.set.SecretsDir(), Run: hostShell}

	ports, set := os.LookupEnv("PI_PORTS")
	if !set {
		ports = "8400-8409"
	}
	e.life = &container.Lifecycle{
		D: e.docker, Spec: container.Spec{Name: e.name, Image: e.image, Ports: ports, Home: home},
		Repo: repoDir(), Stderr: os.Stderr, Tracked: gitTracked,
		Members: e.members,
		After:   e.startProxies,
		Skills: func(name string) ([]container.SkillMount, []string, error) {
			p, err := e.set.Get(name)
			if err != nil {
				return nil, nil, err
			}
			mounts, warnings, err := e.skillStore(p).Mounts()
			out := make([]container.SkillMount, len(mounts))
			for i, m := range mounts {
				out[i] = container.SkillMount{Name: m.Name, Path: m.Path}
			}
			return out, warnings, err
		},
	}

	e.builder = &build.Builder{
		Repo: repoDir(), Tag: e.image, D: e.docker,
		Run: func(args []string, dockerfile string, out io.Writer) error {
			return e.docker.WithStdin(args, dockerfile, out)
		},
	}
	return e
}

// hostShell runs a command on the host and returns its stdout. It runs
// env_from, which the user wrote in their own config.yaml.
func hostShell(cmd string) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	c.Stderr = os.Stderr
	out, err := c.Output()
	return string(out), err
}

// supervisor builds the host side of the proxy servers from the current config.
func (e *environment) supervisor() (*proxy.Supervisor, error) {
	def, _ := e.set.Get(profile.Default)
	defs, err := config.LoadProxies(def.Dir)
	if err != nil {
		return nil, err
	}
	profiles, err := e.set.List()
	if err != nil {
		return nil, err
	}
	members := make([]proxy.Member, 0, len(profiles))
	for _, p := range profiles {
		with, err := config.LoadProxiesWith(p.Dir, inheritedFor(p, defs))
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		_, dest, err := workspaceOf(p)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		members = append(members, proxy.Member{Profile: p, Active: with.Active, SharedWorkspace: dest == container.SharedWorkspace})
	}
	return &proxy.Supervisor{
		D: e.docker, Container: e.name,
		Host: proxy.Host{Proxies: defs, Members: members, Secrets: e.secrets},
	}, nil
}

// inheritedFor is nil for the default profile, which defines the proxies, and
// the default's packages and definitions for any other.
func inheritedFor(p profile.Profile, defs config.Proxies) *config.Proxies {
	if p.Name == profile.Default {
		return nil
	}
	return &defs
}

// startProxies runs once the container is up.
func (e *environment) startProxies() error {
	s, err := e.supervisor()
	if err != nil {
		return err
	}
	if err := s.InstallShims(); err != nil {
		return err
	}
	return s.Start()
}

// Reload sends stored variables and the profiles' choices to the running
// servers and refreshes the shims. It mints nothing. It is how a change takes
// effect without a restart.
func (e *environment) Reload() error {
	s, err := e.supervisor()
	if err != nil {
		return err
	}
	if err := s.InstallShims(); err != nil {
		return err
	}
	return s.Reload()
}

// Refresh mints fresh credentials for the named proxies (all in use if none)
// and sends them to those servers only.
func (e *environment) Refresh(names []string) error {
	s, err := e.supervisor()
	if err != nil {
		return err
	}
	return s.Refresh(names)
}

// workspaceOf says where a profile's workspace comes from and where it is
// mounted. The default profile owns the shared workspace; another profile
// either uses it or has a workspace of its own under its home.
func workspaceOf(p profile.Profile) (host, dest string, err error) {
	if p.Name == profile.Default {
		return p.Workspace(), container.SharedWorkspace, nil
	}
	w, err := config.LoadWorkspaces(p.Dir)
	if err != nil {
		return "", "", err
	}
	if w.Mode == config.WorkspaceShared {
		return "", container.SharedWorkspace, nil
	}
	return p.Workspace(), p.Home() + "/workspace", nil
}

// sessionDir is where a profile's sessions start. Anyone with the shared
// workspace gets /workspace, which links to it.
func sessionDir(p profile.Profile) string {
	_, dest, err := workspaceOf(p)
	if err != nil || dest == container.SharedWorkspace {
		return "/workspace"
	}
	return dest
}

// members lists every profile for `docker run`. It is read at each start, so
// a profile created since tau began is included.
func (e *environment) members() ([]container.Member, error) {
	profiles, err := e.set.List()
	if err != nil {
		return nil, err
	}
	out := make([]container.Member, len(profiles))
	for i, p := range profiles {
		host, dest, err := workspaceOf(p)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		w, err := config.LoadWorkspaces(p.Dir)
		if err != nil {
			return nil, fmt.Errorf("profile %q: %w", p.Name, err)
		}
		out[i] = container.Member{Profile: p, Workspace: host, WorkspaceDest: dest, Live: w.Live}
	}
	return out, nil
}

func (e *environment) skillStore(p profile.Profile) *skills.Store {
	return &skills.Store{Dir: p.Dir, Home: e.home, Dest: skills.DestFor(p.Home()), Mounted: e.life.Mounted}
}

// sessions is the session manager of one profile.
func (e *environment) sessions(p profile.Profile) *session.Manager {
	if m, ok := e.managers[p.Name]; ok {
		return m
	}
	m := &session.Manager{
		D: e.docker, Container: e.name, Profile: p.Name, User: p.User,
		PiCmd: e.piCmd, Home: p.Home(), Workspace: sessionDir(p), Rand: session.RandomName, Loc: time.Local,
		Env: func() ([]string, error) {
			vars, err := envfile.List(p.EnvFile())
			if err != nil {
				return nil, err
			}
			out := make([]string, len(vars))
			for i, v := range vars {
				out[i] = v.Key + "=" + v.Value
			}
			return out, nil
		},
	}
	e.managers[p.Name] = m
	return m
}

// core is the service layer for one profile.
func (e *environment) core(p profile.Profile) *core.Core {
	if c, ok := e.cores[p.Name]; ok {
		return c
	}
	host, dest, _ := workspaceOf(p)
	if host == "" { // a shared profile edits the default profile's workspace
		host = e.defaultProfile().Workspace()
	}
	c := &core.Core{
		Profile: p, Home: p.Dir, EnvFile: p.EnvFile(),
		Workspace: workspaceStoreFor(p.Dir, host, e.home, mountOf(dest)),
		Life:      e.life, Sess: e.sessions(p), Skill: e.skillStore(p), Images: e.builder,
		Proxy:    &proxy.Store{DefaultDir: e.set.Root, Dir: p.Dir, Secrets: e.secrets, Servers: e},
		Profiles: e.set.List,
		Others: func() ([]*core.Core, error) {
			profiles, err := e.set.List()
			if err != nil {
				return nil, err
			}
			out := make([]*core.Core, len(profiles))
			for i, q := range profiles {
				out[i] = e.core(q)
			}
			return out, nil
		},
	}
	e.cores[p.Name] = c
	return c
}

// defaultProfile is the profile that owns the shared workspace.
func (e *environment) defaultProfile() profile.Profile {
	p, _ := e.set.Get(profile.Default)
	return p
}

// mountOf is where a profile's workspace appears to the agent. Anyone with the
// shared workspace sees it at /workspace, which links to it.
func mountOf(dest string) string {
	if dest == container.SharedWorkspace {
		return "/workspace"
	}
	return dest
}

// repoDir is the tau checkout: TAU_REPO, else the directory holding this
// binary (after following symlinks, so /usr/local/bin/tau works).
func repoDir() string {
	if d := os.Getenv("TAU_REPO"); d != "" {
		return d
	}
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// pathOr returns the first non-empty environment variable, else def.
func pathOr(def string, vars ...string) string {
	for _, v := range vars {
		if p := os.Getenv(v); p != "" {
			return p
		}
	}
	return def
}

// gitTracked lists the files git tracks under dir, relative to dir.
func gitTracked(dir string) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files in %s: %w", dir, err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files, nil
}
