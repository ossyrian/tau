package tui

import (
	"io"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/workspace"
)

// API is everything the interactive interface asks of tau. *core.Core
// implements it; the CLI calls the same methods.
type API interface {
	Status() core.Status
	Start() (container.Outcome, error)
	Stop() (container.Outcome, error)
	Restart() (container.Outcome, error)

	Sessions() ([]session.Info, error)
	Preview(n int) (map[string][]string, error)
	CreateSession(name string) (string, error)
	KillSession(profile, name string) (string, error)
	AttachArgs(profile, name string) ([]string, error)

	Skills() ([]skills.Skill, error)
	ActivateSkill(nameOrPath string) (string, error)
	DeactivateSkill(nameOrPath string) (string, error)
	SkillPaths() ([]skills.PathInfo, error)
	AddSkillPath(path string) error
	RemoveSkillPath(path string) error
	RenameSkillPath(old, to string) error

	EnvVars() ([]envfile.Var, error)
	SetEnv(key, value string) error
	UnsetEnv(key string) (bool, error)

	Proxies() (config.Proxies, error)
	UseProxy(name string, on bool) error
	ProxyVars(name string) ([]proxy.Var, error)
	SetProxyVar(name, key, value string) error
	UnsetProxyVar(name, key string) (bool, error)
	ReloadProxies() error
	RefreshProxies(names []string) error

	Workspaces() ([]workspace.Entry, error)
	AddWorkspace(src, name string, live bool) (workspace.Result, error)
	RemoveWorkspace(name string) (workspace.Entry, error)

	LoadConfig() (*config.Config, error)
	Build(noCache bool, out io.Writer) error
}

var _ API = (*core.Core)(nil)
