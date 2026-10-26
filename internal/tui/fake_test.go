package tui

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/container"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/proxy"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/workspace"
)

// fakeAPI records calls and returns canned data.
type fakeAPI struct {
	mu       sync.Mutex
	calls    []string
	status   core.Status
	sessions []session.Info
	preview  map[string][]string
	skills   []skills.Skill
	paths    []skills.PathInfo
	vars     []envfile.Var
	home     string
	err      error // returned by every action
	buildOut string
	attach   []string

	entries    []workspace.Entry
	proxies    config.Proxies
	proxyVars  map[string][]proxy.Var
	proxyError error // returned by every proxy call
}

func (f *fakeAPI) rec(s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
	return f.err
}

func (f *fakeAPI) did() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *fakeAPI) Status() core.Status                 { return f.status }
func (f *fakeAPI) Start() (container.Outcome, error)   { return container.Started, f.rec("start") }
func (f *fakeAPI) Stop() (container.Outcome, error)    { return container.Stopped, f.rec("stop") }
func (f *fakeAPI) Restart() (container.Outcome, error) { return container.Restarted, f.rec("restart") }

func (f *fakeAPI) Sessions() ([]session.Info, error) { return f.sessions, f.err }
func (f *fakeAPI) Preview(int) (map[string][]string, error) {
	return f.preview, f.err
}
func (f *fakeAPI) CreateSession(n string) (string, error) {
	return n, f.rec("create:" + n)
}
func (f *fakeAPI) KillSession(p, n string) (string, error) { return n, f.rec("kill:" + p + "/" + n) }
func (f *fakeAPI) AttachArgs(p, n string) ([]string, error) {
	f.rec("attach:" + p + "/" + n)
	return append([]string{"exec", "-it", "tau", "attach"}, n), f.err
}

func (f *fakeAPI) Skills() ([]skills.Skill, error)          { return f.skills, f.err }
func (f *fakeAPI) ActivateSkill(n string) (string, error)   { return n, f.rec("activate:" + n) }
func (f *fakeAPI) DeactivateSkill(n string) (string, error) { return n, f.rec("deactivate:" + n) }
func (f *fakeAPI) SkillPaths() ([]skills.PathInfo, error)   { return f.paths, f.err }
func (f *fakeAPI) AddSkillPath(p string) error              { return f.rec("addpath:" + p) }
func (f *fakeAPI) RemoveSkillPath(p string) error           { return f.rec("rmpath:" + p) }
func (f *fakeAPI) RenameSkillPath(o, n string) error        { return f.rec("mvpath:" + o + ">" + n) }

func (f *fakeAPI) EnvVars() ([]envfile.Var, error) { return f.vars, f.err }
func (f *fakeAPI) SetEnv(k, v string) error        { return f.rec("setenv:" + k + "=" + v) }
func (f *fakeAPI) UnsetEnv(k string) (bool, error) { return true, f.rec("unsetenv:" + k) }

func (f *fakeAPI) Proxies() (config.Proxies, error) { return f.proxies, f.proxyError }
func (f *fakeAPI) UseProxy(n string, on bool) error {
	if f.proxyError != nil {
		return f.proxyError
	}
	f.rec(fmt.Sprintf("useproxy:%s=%v", n, on))
	var act []string
	for _, a := range f.proxies.Active {
		if a != n {
			act = append(act, a)
		}
	}
	if on {
		act = append(act, n)
	}
	f.proxies.Active = act
	return nil
}
func (f *fakeAPI) ProxyVars(n string) ([]proxy.Var, error) { return f.proxyVars[n], f.proxyError }
func (f *fakeAPI) SetProxyVar(n, k, v string) error {
	f.rec("setproxyvar:" + n + ":" + k + "=" + v)
	return f.proxyError
}
func (f *fakeAPI) UnsetProxyVar(n, k string) (bool, error) {
	return true, f.rec("unsetproxyvar:" + n + ":" + k)
}
func (f *fakeAPI) ReloadProxies() error { return f.rec("reloadproxies") }
func (f *fakeAPI) RefreshProxies(names []string) error {
	return f.rec("refreshproxies:" + strings.Join(names, ","))
}

func (f *fakeAPI) Workspaces() ([]workspace.Entry, error) { return f.entries, f.err }
func (f *fakeAPI) AddWorkspace(src, name string, live bool) (workspace.Result, error) {
	return workspace.Result{Entry: workspace.Entry{Name: name}}, f.rec(fmt.Sprintf("addws:%s:%s:%v", src, name, live))
}
func (f *fakeAPI) RemoveWorkspace(name string) (workspace.Entry, error) {
	return workspace.Entry{Name: name}, f.rec("rmws:" + name)
}

func (f *fakeAPI) LoadConfig() (*config.Config, error) { return config.Load(f.home, nil) }
func (f *fakeAPI) Build(noCache bool, out io.Writer) error {
	if noCache {
		f.rec("build:nocache")
	} else {
		f.rec("build")
	}
	io.WriteString(out, f.buildOut)
	return f.err
}

func newFake(t *testing.T) *fakeAPI {
	t.Helper()
	h := t.TempDir()
	os.WriteFile(filepath.Join(h, "config.yaml"), []byte("packages:\n  a: {script: sa}\n"), 0o644)
	return &fakeAPI{
		home: h,
		status: core.Status{
			Container: container.State{Exists: true, Running: true, Started: time.Now().Add(-90 * time.Minute)},
			Image:     build.ImageCurrent, Profile: "default",
		},
	}
}

func writeConfig(t *testing.T, f *fakeAPI, base, runtime string) {
	t.Helper()
	os.WriteFile(filepath.Join(f.home, "config.yaml"), []byte(base), 0o644)
	if runtime != "" {
		os.WriteFile(filepath.Join(f.home, "config.runtime.yaml"), []byte(runtime), 0o644)
	}
}
