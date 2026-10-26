package core

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

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

type fakeContainer struct {
	st    container.State
	err   error
	calls *[]string
}

func (f fakeContainer) State() (container.State, error) { return f.st, f.err }
func (f fakeContainer) Start() (container.Outcome, error) {
	*f.calls = append(*f.calls, "start")
	return container.Started, f.err
}
func (f fakeContainer) Stop() (container.Outcome, error) {
	*f.calls = append(*f.calls, "stop")
	return container.Stopped, f.err
}
func (f fakeContainer) Restart() (container.Outcome, error) {
	*f.calls = append(*f.calls, "restart")
	return container.Restarted, f.err
}

type fakeSess struct {
	profile string
	infos   []session.Info
	err     error
}

func (f fakeSess) Sessions() ([]session.Info, error) { return f.infos, f.err }
func (f fakeSess) Preview(int) (map[string][]string, error) {
	out := map[string][]string{}
	for _, i := range f.infos {
		out[i.Name] = []string{"screen of " + f.profile + "/" + i.Name}
	}
	return out, f.err
}
func (f fakeSess) Create(string) (string, error)         { return "", nil }
func (f fakeSess) Kill(n string) (string, error)         { return f.profile + "/" + n, f.err }
func (f fakeSess) AttachArgs(n string) ([]string, error) { return []string{f.profile, n}, f.err }

type fakeSkills struct {
	n   int
	err error
	log *[]string
}

func (f fakeSkills) List() ([]skills.Skill, error)       { return []skills.Skill{{Name: "x"}}, f.err }
func (f fakeSkills) Activate(n string) (string, error)   { return "on:" + n, f.err }
func (f fakeSkills) Deactivate(n string) (string, error) { return "off:" + n, f.err }
func (f fakeSkills) Paths() ([]skills.PathInfo, error) {
	return []skills.PathInfo{{Path: "/p"}}, f.err
}
func (f fakeSkills) AddPath(p string) error       { return f.rec("add:" + p) }
func (f fakeSkills) RemovePath(p string) error    { return f.rec("rm:" + p) }
func (f fakeSkills) RenamePath(o, n string) error { return f.rec("mv:" + o + ">" + n) }
func (f fakeSkills) Drift() (int, error)          { return f.n, f.err }
func (f fakeSkills) rec(s string) error {
	if f.log != nil {
		*f.log = append(*f.log, s)
	}
	return f.err
}

type fakeImages struct {
	plan    build.Plan
	status  build.ImageStatus
	perr    error
	serr    error
	hash    string
	berr    error
	built   []buildCall
	planned []build.ProfileConfig
}

type buildCall struct {
	plan    build.Plan
	noCache bool
	out     io.Writer
}

func (f *fakeImages) Plan(p []build.ProfileConfig) (build.Plan, error) {
	f.planned = p
	return f.plan, f.perr
}
func (f *fakeImages) Build(p build.Plan, noCache bool, out io.Writer) error {
	f.built = append(f.built, buildCall{p, noCache, out})
	return f.berr
}
func (f *fakeImages) Status(h string) (build.ImageStatus, error) {
	f.hash = h
	return f.status, f.serr
}

func home(t *testing.T, base, runtime string) string {
	t.Helper()
	h := t.TempDir()
	if base != "" {
		os.WriteFile(filepath.Join(h, "config.yaml"), []byte(base), 0o644)
	}
	if runtime != "" {
		os.WriteFile(filepath.Join(h, "config.runtime.yaml"), []byte(runtime), 0o644)
	}
	return h
}

const base = "packages:\n  a: {script: echo a}\n"

func core(t *testing.T, h string, c fakeContainer, im *fakeImages) *Core {
	t.Helper()
	if c.calls == nil {
		c.calls = new([]string)
	}
	return &Core{Home: h, Life: c, Images: im, EnvFile: filepath.Join(h, ".env"),
		Skill: fakeSkills{n: 2}, Profile: profile.Profile{Name: "default", Dir: h, User: "pi", UID: 1000}}
}

func TestStatusGathersEverything(t *testing.T) {
	h := home(t, base, "packages:\n  b: {script: echo b}\n")
	os.WriteFile(filepath.Join(h, ".env"), []byte("A=1\nB=new\n"), 0o600)
	run := container.State{Exists: true, Running: true, Env: map[string]string{"A": "1", "B": "old"}}
	im := &fakeImages{plan: build.Plan{Hash: "h1"}, status: build.ImageStale}
	st := core(t, h, fakeContainer{st: run}, im).Status()
	if !st.Container.Running || st.Image != build.ImageStale || im.hash != "h1" {
		t.Errorf("container/image: %+v hash %q", st, im.hash)
	}
	if !reflect.DeepEqual(st.Drift.RuntimeOnly, []string{"b"}) || !st.NeedsResolution {
		t.Errorf("drift %+v", st.Drift)
	}
	if st.SkillsPending != 2 || st.Profile != "default" {
		t.Errorf("%+v", st)
	}
	if len(st.Problems) != 0 {
		t.Errorf("problems %v", st.Problems)
	}
}

func TestStatusReportsProblemsAndKeepsGoing(t *testing.T) {
	h := home(t, "packages: [not a map", "")
	im := &fakeImages{status: build.ImageCurrent}
	st := core(t, h, fakeContainer{err: errors.New("docker down")}, im).Status()
	if len(st.Problems) != 2 || st.Image != build.ImageUnknown {
		t.Errorf("problems %v image %v", st.Problems, st.Image)
	}
}

func TestStatusSkillsFailureIsAProblemNotACrash(t *testing.T) {
	h := home(t, base, "")
	c := core(t, h, fakeContainer{st: container.State{Exists: true, Running: true}}, &fakeImages{status: build.ImageCurrent})
	c.Skill = fakeSkills{err: errors.New("skills broke")}
	st := c.Status()
	if len(st.Problems) != 1 || st.Image != build.ImageCurrent {
		t.Errorf("problems %v", st.Problems)
	}
}

func TestStatusImageErrors(t *testing.T) {
	h := home(t, base, "")
	for name, im := range map[string]*fakeImages{
		"plan":   {perr: errors.New("no Dockerfile")},
		"status": {serr: errors.New("cannot reach docker")},
	} {
		st := core(t, h, fakeContainer{}, im).Status()
		if st.Image != build.ImageUnknown || len(st.Problems) != 1 {
			t.Errorf("%s: %+v", name, st)
		}
	}
}

func TestBuildPlansFromConfigAndRunsIt(t *testing.T) {
	h := home(t, base, "")
	im := &fakeImages{plan: build.Plan{Dockerfile: "FROM x", Hash: "h"}}
	var out strings.Builder
	if err := core(t, h, fakeContainer{}, im).Build(true, &out); err != nil {
		t.Fatal(err)
	}
	if len(im.built) != 1 || !im.built[0].noCache || im.built[0].plan.Hash != "h" || im.built[0].out != &out {
		t.Errorf("%+v", im.built)
	}
}

func TestBuildRefusesWhileDriftNeedsADecision(t *testing.T) {
	h := home(t, base, "packages:\n  b: {script: echo b}\n  a: {script: echo changed}\n")
	im := &fakeImages{}
	err := core(t, h, fakeContainer{}, im).Build(false, io.Discard)
	var de *DriftError
	if !errors.As(err, &de) || !reflect.DeepEqual(de.Names, []string{"b", "a"}) {
		t.Fatalf("got %v", err)
	}
	if len(im.built) != 0 {
		t.Error("built anyway")
	}
}

func TestBuildErrorsPropagate(t *testing.T) {
	h := home(t, base, "")
	if err := core(t, h, fakeContainer{}, &fakeImages{perr: errors.New("plan")}).Build(false, io.Discard); err == nil {
		t.Error("plan error lost")
	}
	if err := core(t, h, fakeContainer{}, &fakeImages{berr: errors.New("docker")}).Build(false, io.Discard); err == nil {
		t.Error("build error lost")
	}
	if err := core(t, home(t, "packages: [x", ""), fakeContainer{}, &fakeImages{}).Build(false, io.Discard); err == nil {
		t.Error("config error lost")
	}
}

func TestLifecycleDelegates(t *testing.T) {
	var calls []string
	c := core(t, t.TempDir(), fakeContainer{calls: &calls}, &fakeImages{})
	c.Start()
	c.Stop()
	c.Restart()
	if !reflect.DeepEqual(calls, []string{"start", "stop", "restart"}) {
		t.Errorf("%v", calls)
	}
}

func TestEnvAndSkillsDelegate(t *testing.T) {
	c := core(t, t.TempDir(), fakeContainer{}, &fakeImages{})
	if err := c.SetEnv("A", "1"); err != nil {
		t.Fatal(err)
	}
	vars, _ := c.EnvVars()
	if !reflect.DeepEqual(vars, []envfile.Var{{Key: "A", Value: "1"}}) {
		t.Errorf("%v", vars)
	}
	if ok, err := c.UnsetEnv("A"); !ok || err != nil {
		t.Errorf("%v %v", ok, err)
	}
	var log []string
	c.Skill = fakeSkills{log: &log}
	if n, _ := c.ActivateSkill("a"); n != "on:a" {
		t.Error(n)
	}
	if n, _ := c.DeactivateSkill("z"); n != "off:z" {
		t.Error(n)
	}
	if p, _ := c.SkillPaths(); len(p) != 1 || p[0].Path != "/p" {
		t.Error(p)
	}
	c.AddSkillPath("/a")
	c.RemoveSkillPath("/b")
	c.RenameSkillPath("/c", "/d")
	if !reflect.DeepEqual(log, []string{"add:/a", "rm:/b", "mv:/c>/d"}) {
		t.Errorf("%v", log)
	}
	if l, _ := c.Skills(); len(l) != 1 {
		t.Error(l)
	}
}

func profileDir(t *testing.T, root, name, base, runtime string) profile.Profile {
	t.Helper()
	p, err := profile.Set{Root: root}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(p.Dir, 0o755)
	if base != "" {
		os.WriteFile(p.ConfigFile(), []byte(base), 0o644)
	}
	if runtime != "" {
		os.WriteFile(p.RuntimeFile(), []byte(runtime), 0o644)
	}
	return p
}

func multi(t *testing.T, im *fakeImages, baseA, runtimeA, baseB, runtimeB string) (*Core, []profile.Profile) {
	t.Helper()
	root := t.TempDir()
	def := profileDir(t, root, "default", baseA, runtimeA)
	work := profileDir(t, root, "work", baseB, runtimeB)
	c := core(t, def.Dir, fakeContainer{}, im)
	c.Profile = def
	c.Profiles = func() ([]profile.Profile, error) { return []profile.Profile{def, work}, nil }
	return c, []profile.Profile{def, work}
}

func TestStatusPlansEveryProfileBecauseTheImageHoldsThemAll(t *testing.T) {
	im := &fakeImages{status: build.ImageCurrent}
	c, ps := multi(t, im, base, "", "packages:\n  w: {script: echo w}\n", "")
	c.Status()
	if len(im.planned) != 2 || im.planned[0].Profile.Name != "default" || im.planned[1].Profile.Name != ps[1].Name {
		t.Errorf("planned %+v", im.planned)
	}
}

func TestBuildRefusesDriftInAnyProfileAndNamesIt(t *testing.T) {
	im := &fakeImages{}
	c, _ := multi(t, im, base, "", "packages:\n  w: {script: echo w}\n", "packages:\n  extra: {script: echo x}\n  w: {script: echo w}\n")
	err := c.Build(false, io.Discard)
	var de *DriftError
	if !errors.As(err, &de) || de.Profile != "work" || !strings.Contains(err.Error(), "in profile work") {
		t.Fatalf("got %v", err)
	}
	if len(im.built) != 0 {
		t.Error("built anyway")
	}
}

func TestABrokenConfigInAnotherProfileNamesIt(t *testing.T) {
	c, _ := multi(t, &fakeImages{}, base, "", "packages: [x", "")
	err := c.Build(false, io.Discard)
	if err == nil || !strings.Contains(err.Error(), `profile "work"`) {
		t.Errorf("got %v", err)
	}
}

func TestStatusIsAboutTheCoresOwnProfileButTheBuildIsShared(t *testing.T) {
	c, ps := multi(t, &fakeImages{status: build.ImageStale}, base, "packages:\n  b: {script: echo b}\n", "", "")
	if st := c.Status(); st.Profile != "default" || !st.NeedsResolution {
		t.Errorf("%+v", st)
	}
	_ = ps
}

func twoProfileSessions(a, b fakeSess) *Core {
	ca := &Core{Sess: a, Profile: profile.Profile{Name: "default"}}
	cb := &Core{Sess: b, Profile: profile.Profile{Name: "work"}}
	ca.Others = func() ([]*Core, error) { return []*Core{ca, cb}, nil }
	return ca
}

func TestSessionsListsEveryProfileInOrder(t *testing.T) {
	c := twoProfileSessions(
		fakeSess{profile: "default", infos: []session.Info{{Name: "main", Profile: "default"}}},
		fakeSess{profile: "work", infos: []session.Info{{Name: "main", Profile: "work"}, {Name: "x", Profile: "work"}}},
	)
	got, err := c.Sessions()
	if err != nil || len(got) != 3 || got[0].Profile != "default" || got[1].Profile != "work" || got[2].Name != "x" {
		t.Errorf("got %+v %v", got, err)
	}
}

func TestSessionsOfOneProfileWhenThereAreNoOthers(t *testing.T) {
	c := &Core{Sess: fakeSess{infos: []session.Info{{Name: "a", Profile: "default"}}}}
	if got, _ := c.Sessions(); len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestAProfilesSessionFailureStopsTheList(t *testing.T) {
	c := twoProfileSessions(fakeSess{}, fakeSess{err: errors.New("container not running")})
	if _, err := c.Sessions(); err == nil {
		t.Error("error lost")
	}
}

func TestPreviewIsKeyedByProfileSoSameNamedSessionsDoNotCollide(t *testing.T) {
	c := twoProfileSessions(
		fakeSess{profile: "default", infos: []session.Info{{Name: "main", Profile: "default"}}},
		fakeSess{profile: "work", infos: []session.Info{{Name: "main", Profile: "work"}}},
	)
	got, err := c.Preview(3)
	if err != nil {
		t.Fatal(err)
	}
	if got["default/main"][0] != "screen of default/main" || got["work/main"][0] != "screen of work/main" || len(got) != 2 {
		t.Errorf("got %v", got)
	}
}

func TestKillAndAttachActOnTheSessionsOwnProfile(t *testing.T) {
	c := twoProfileSessions(fakeSess{profile: "default"}, fakeSess{profile: "work"})
	if got, _ := c.KillSession("work", "main"); got != "work/main" {
		t.Errorf("kill: %q", got)
	}
	if got, _ := c.KillSession("default", "main"); got != "default/main" {
		t.Errorf("kill own: %q", got)
	}
	if got, _ := c.AttachArgs("work", "main"); !reflect.DeepEqual(got, []string{"work", "main"}) {
		t.Errorf("attach: %v", got)
	}
}

func TestAnUnknownProfileIsAnError(t *testing.T) {
	c := twoProfileSessions(fakeSess{}, fakeSess{})
	if _, err := c.KillSession("ghost", "x"); err == nil {
		t.Error("kill in a missing profile succeeded")
	}
	if _, err := c.AttachArgs("ghost", "x"); err == nil {
		t.Error("attach in a missing profile succeeded")
	}
}

type fakeProxy struct {
	calls []string
	err   error
}

func (f *fakeProxy) Proxies() (config.Proxies, error) {
	return config.Proxies{Active: []string{"a"}}, f.err
}
func (f *fakeProxy) Use(n string, on bool) error {
	f.calls = append(f.calls, "use:"+n+"="+strconv.FormatBool(on))
	return f.err
}
func (f *fakeProxy) Vars(n string) ([]proxy.Var, error) {
	f.calls = append(f.calls, "vars:"+n)
	return nil, f.err
}
func (f *fakeProxy) SetVar(n, k, v string) error {
	f.calls = append(f.calls, "set:"+n+":"+k+"="+v)
	return f.err
}
func (f *fakeProxy) UnsetVar(n, k string) (bool, error) {
	f.calls = append(f.calls, "unset:"+n+":"+k)
	return true, f.err
}
func (f *fakeProxy) Reload() error { f.calls = append(f.calls, "reload"); return f.err }
func (f *fakeProxy) Refresh(names []string) error {
	f.calls = append(f.calls, "refresh:"+strings.Join(names, ","))
	return f.err
}

func TestProxyCallsGoToTheStore(t *testing.T) {
	f := &fakeProxy{}
	c := &Core{Proxy: f}
	if p, err := c.Proxies(); err != nil || len(p.Active) != 1 {
		t.Errorf("Proxies: %+v %v", p, err)
	}
	c.UseProxy("a", true)
	c.ProxyVars("a")
	c.SetProxyVar("a", "K", "v")
	c.UnsetProxyVar("a", "K")
	c.ReloadProxies()
	c.RefreshProxies([]string{"a", "b"})
	c.RefreshProxies(nil)
	want := []string{"use:a=true", "vars:a", "set:a:K=v", "unset:a:K", "reload", "refresh:a,b", "refresh:"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls %v", f.calls)
	}
}

func TestProxyCallsReportStoreErrors(t *testing.T) {
	f := &fakeProxy{err: errors.New("boom")}
	c := &Core{Proxy: f}
	if err := c.RefreshProxies(nil); err == nil || err.Error() != "boom" {
		t.Errorf("got %v", err)
	}
	if err := c.UseProxy("a", true); err == nil {
		t.Error("use hid the error")
	}
}

func TestWithoutAProxyStoreEveryProxyCallSaysSo(t *testing.T) {
	c := &Core{}
	if p, err := c.Proxies(); err != nil || len(p.Defs) != 0 {
		t.Errorf("listing with no store: %+v %v", p, err)
	}
	for name, err := range map[string]error{
		"use":     c.UseProxy("a", true),
		"set":     c.SetProxyVar("a", "K", "v"),
		"reload":  c.ReloadProxies(),
		"refresh": c.RefreshProxies(nil),
	} {
		if err == nil || !strings.Contains(err.Error(), "not available") {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := c.ProxyVars("a"); err == nil {
		t.Error("vars accepted")
	}
	if _, err := c.UnsetProxyVar("a", "K"); err == nil {
		t.Error("unset accepted")
	}
}

type fakeWS struct {
	calls []string
	err   error
}

func (f *fakeWS) Add(src, name string, live bool) (workspace.Result, error) {
	f.calls = append(f.calls, "add:"+src+":"+name+":"+strconv.FormatBool(live))
	return workspace.Result{Action: workspace.Added, Entry: workspace.Entry{Name: "n", Kind: workspace.Live}}, f.err
}
func (f *fakeWS) List() ([]workspace.Entry, error) {
	f.calls = append(f.calls, "list")
	return []workspace.Entry{{Name: "a"}}, f.err
}
func (f *fakeWS) Remove(n string) (workspace.Entry, error) {
	f.calls = append(f.calls, "rm:"+n)
	return workspace.Entry{Name: n}, f.err
}
func (f *fakeWS) Path(n string) (string, error) {
	f.calls = append(f.calls, "path:"+n)
	return "/p/" + n, f.err
}

func TestWorkspaceCallsGoToTheStore(t *testing.T) {
	f := &fakeWS{}
	c := &Core{Workspace: f}
	c.AddWorkspace("/s", "n", true)
	c.Workspaces()
	c.RemoveWorkspace("n")
	if p, err := c.WorkspacePath("x"); err != nil || p != "/p/x" {
		t.Errorf("path %q %v", p, err)
	}
	want := []string{"add:/s:n:true", "list", "rm:n", "path:x"}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls %v", f.calls)
	}
}

func TestWorkspaceCallsReportStoreErrors(t *testing.T) {
	c := &Core{Workspace: &fakeWS{err: errors.New("boom")}}
	if _, err := c.AddWorkspace("/s", "n", false); err == nil || err.Error() != "boom" {
		t.Errorf("add: %v", err)
	}
	if _, err := c.Workspaces(); err == nil {
		t.Error("list hid the error")
	}
}

func TestWithoutAWorkspaceStoreEveryWorkspaceCallSaysSo(t *testing.T) {
	c := &Core{}
	if _, err := c.Workspaces(); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("list: %v", err)
	}
	if _, err := c.AddWorkspace("/s", "", false); err == nil {
		t.Error("add accepted")
	}
	if _, err := c.RemoveWorkspace("n"); err == nil {
		t.Error("remove accepted")
	}
	if _, err := c.WorkspacePath("n"); err == nil {
		t.Error("path accepted")
	}
}

func TestContainerRunningSaysWhetherAMountChangeNeedsARestart(t *testing.T) {
	calls := []string{}
	for _, running := range []bool{true, false} {
		c := &Core{Life: fakeContainer{st: container.State{Running: running}, calls: &calls}}
		if got := c.ContainerRunning(); got != running {
			t.Errorf("running=%v: got %v", running, got)
		}
	}
	if (&Core{Life: fakeContainer{err: errors.New("no docker"), calls: &calls}}).ContainerRunning() {
		t.Error("an unknown state counted as running")
	}
}
