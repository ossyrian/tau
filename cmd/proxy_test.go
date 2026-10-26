package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/core"
	"github.com/ossyrian/tau/internal/profile"
	"github.com/ossyrian/tau/internal/proxy"
)

type fakeProxyHost struct {
	reloads   int
	refreshed [][]string
	err       error
}

func (f *fakeProxyHost) Reload() error { f.reloads++; return f.err }
func (f *fakeProxyHost) Refresh(names []string) error {
	f.refreshed = append(f.refreshed, names)
	return f.err
}

func (f *fakeProxyHost) store(h *harness) *proxy.Store {
	return &proxy.Store{
		DefaultDir: h.home, Dir: h.app.configDir(),
		Secrets: &proxy.Secrets{Dir: filepath.Join(h.home, "secrets")}, Servers: f,
	}
}

const awsDefs = `proxies:
  packages:
    aws-cli: {script: install-aws}
  defs:
    aws:
      package: aws-cli
      binary: bin/aws
      agent_command: aws
      description: AWS CLI
    gh:
      system_binary: /usr/bin/gh
      agent_command: gh
      runs_in: agent_directory
  active: [aws]
`

func proxyHarness(t *testing.T) (*harness, *fakeProxyHost) {
	t.Helper()
	h := newHarness(t, awsDefs, "")
	f := &fakeProxyHost{}
	h.app.Core = coreFor(h)
	h.app.Core.Proxy = f.store(h)
	h.app.Select = func(string) error {
		// a profile selected with -p gets a store for its own directory
		h.app.Core.Proxy = &proxy.Store{
			DefaultDir: h.home, Dir: h.app.configDir(),
			Secrets: &proxy.Secrets{Dir: filepath.Join(h.home, "secrets")}, Servers: f,
		}
		return nil
	}
	return h, f
}

func TestProxyLsListsDefinitionsAndWhetherTheProfileUsesThem(t *testing.T) {
	h, _ := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "list"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if want := "aws\taws\tyes\ngh\tgh\tno\n"; h.out.String() != want {
		t.Errorf("got %q want %q", h.out, want)
	}
}

func TestProxyUseAndUnuseChangeTheProfilesList(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "allow", "gh"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	data, _ := os.ReadFile(filepath.Join(h.home, "config.yaml"))
	if !strings.Contains(string(data), "gh") || !strings.Contains(string(data), "aws") {
		t.Errorf("config:\n%s", data)
	}
	if f.reloads != 1 {
		t.Errorf("reloads %d: a change must reach the running servers", f.reloads)
	}
	if code := h.app.Run([]string{"proxy", "disallow", "aws"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	h.out.Reset()
	h.app.Run([]string{"proxy", "list"})
	if h.out.String() != "aws\taws\tno\ngh\tgh\tyes\n" {
		t.Errorf("got %q", h.out)
	}
}

func TestProxyUseRefusesAnUndefinedProxyAndRepeats(t *testing.T) {
	h, _ := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "allow", "ghost"}); code != 1 || !strings.Contains(h.err.String(), "ghost") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if code := h.app.Run([]string{"proxy", "allow", "aws"}); code != 1 || !strings.Contains(h.err.String(), "already") {
		t.Errorf("repeat: exit %d err %q", code, h.err)
	}
	if code := h.app.Run([]string{"proxy", "disallow", "gh"}); code != 1 || !strings.Contains(h.err.String(), "not in use") {
		t.Errorf("unuse: exit %d err %q", code, h.err)
	}
}

func TestProxyEnvSetStoresASecretAndNeverPrintsIt(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "env", "set", "aws", "AWS_SECRET_ACCESS_KEY", "super-secret-value"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if strings.Contains(h.out.String()+h.err.String(), "super-secret-value") {
		t.Errorf("the secret was echoed: %q %q", h.out, h.err)
	}
	st, err := os.Stat(filepath.Join(h.home, "secrets", "aws.env"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("secret file: %v %v", st, err)
	}
	if f.reloads != 1 {
		t.Errorf("reloads %d", f.reloads)
	}
}

func TestProxyEnvSetAcceptsKeyEqualsValueAndRefusesAnUndefinedProxy(t *testing.T) {
	h, _ := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "env", "set", "aws", "A=b=c"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	vars, _ := h.app.Core.Proxy.(*proxy.Store).Secrets.Vars("aws")
	if len(vars) != 1 || vars[0].Key != "A" || vars[0].Value != "b=c" {
		t.Errorf("got %v", vars)
	}
	if code := h.app.Run([]string{"proxy", "env", "set", "ghost", "A", "b"}); code != 1 {
		t.Error("a secret was stored for an undefined proxy")
	}
	if _, err := os.Stat(filepath.Join(h.home, "secrets", "ghost.env")); err == nil {
		t.Error("a file was made for an undefined proxy")
	}
}

func TestProxyEnvLsMasksValuesUnlessAsked(t *testing.T) {
	h, _ := proxyHarness(t)
	secretsOf(h).Set("aws", "AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI-K7MDENG")
	h.app.Run([]string{"proxy", "env", "list", "aws"})
	if strings.Contains(h.out.String(), "wJalrXUtnFEMI") || !strings.Contains(h.out.String(), "AWS_SECRET_ACCESS_KEY=") {
		t.Errorf("masked listing: %q", h.out)
	}
	h.out.Reset()
	h.app.Run([]string{"proxy", "env", "list", "aws", "--show"})
	if !strings.Contains(h.out.String(), "wJalrXUtnFEMI-K7MDENG") {
		t.Errorf("--show: %q", h.out)
	}
}

func TestProxyEnvUnsetRemovesAKeyAndReloads(t *testing.T) {
	h, f := proxyHarness(t)
	secretsOf(h).Set("aws", "A", "1")
	if code := h.app.Run([]string{"proxy", "env", "unset", "aws", "A"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if v, _ := secretsOf(h).Vars("aws"); len(v) != 0 || f.reloads != 1 {
		t.Errorf("vars %v reloads %d", v, f.reloads)
	}
	if code := h.app.Run([]string{"proxy", "env", "unset", "aws", "A"}); code != 1 {
		t.Error("a missing key was reported as removed")
	}
}

func TestProxyReloadAsksTheServersToReload(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "reload"}); code != 0 || f.reloads != 1 {
		t.Errorf("exit %d reloads %d err %q", code, f.reloads, h.err)
	}
}

func TestAReloadFailureAfterAChangeIsReportedButTheChangeStays(t *testing.T) {
	h, f := proxyHarness(t)
	f.err = os.ErrDeadlineExceeded
	code := h.app.Run([]string{"proxy", "env", "set", "aws", "A", "1"})
	if code != 1 || !strings.Contains(h.err.String(), "saved") {
		t.Errorf("exit %d err %q", code, h.err)
	}
	if v, _ := secretsOf(h).Vars("aws"); len(v) != 1 {
		t.Error("the change was lost")
	}
}

func TestEachProfileChoosesItsOwnProxiesButSharesTheDefinitions(t *testing.T) {
	h, _ := proxyHarness(t)
	p, _ := profile.Set{Root: h.home}.Create("work")
	write(t, p.ConfigFile(), "proxies:\n  active: [gh]\n")
	h.app.Profiles = profile.Set{Root: h.home}.List
	h.app.Run([]string{"proxy", "list", "-p", "work"})
	if h.out.String() != "aws\taws\tno\ngh\tgh\tyes\n" {
		t.Errorf("work: %q", h.out)
	}
	h.out.Reset()
	h.app.Run([]string{"proxy", "list"})
	if h.out.String() != "aws\taws\tyes\ngh\tgh\tno\n" {
		t.Errorf("default: %q", h.out)
	}
}

func TestAProfileCannotDefineItsOwnProxies(t *testing.T) {
	h, _ := proxyHarness(t)
	p, _ := profile.Set{Root: h.home}.Create("work")
	write(t, p.ConfigFile(), awsDefs)
	h.app.Profiles = profile.Set{Root: h.home}.List
	if code := h.app.Run([]string{"proxy", "list", "-p", "work"}); code != 1 || !strings.Contains(h.err.String(), "default profile") {
		t.Errorf("exit %d err %q", code, h.err)
	}
}

func secretsOf(h *harness) *proxy.Secrets { return h.app.Core.Proxy.(*proxy.Store).Secrets }

func coreFor(h *harness) *core.Core {
	return &core.Core{
		Home: h.home, EnvFile: filepath.Join(h.home, ".env"),
		Profile: profile.Profile{Name: "default", Dir: h.home, User: "pi", UID: 1000},
		Life:    &fakeLife{}, Skill: fakeSk{}, Images: stubImages{status: build.ImageCurrent},
	}
}

func TestProxyRefreshNamesGoToTheServers(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "refresh", "aws"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if len(f.refreshed) != 1 || len(f.refreshed[0]) != 1 || f.refreshed[0][0] != "aws" || f.reloads != 0 {
		t.Errorf("refreshed %v reloads %d", f.refreshed, f.reloads)
	}
	if !strings.Contains(h.out.String(), "aws") {
		t.Errorf("out %q", h.out)
	}
}

func TestProxyRefreshWithNoNamesRefreshesAll(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "refresh"}); code != 0 || len(f.refreshed) != 1 || len(f.refreshed[0]) != 0 {
		t.Errorf("exit %d refreshed %v err %q", code, f.refreshed, h.err)
	}
}

func TestProxyRefreshTakesSeveralNames(t *testing.T) {
	h, f := proxyHarness(t)
	h.app.Run([]string{"proxy", "refresh", "aws", "gh"})
	if len(f.refreshed) != 1 || strings.Join(f.refreshed[0], ",") != "aws,gh" {
		t.Errorf("refreshed %v", f.refreshed)
	}
}

func TestProxyRefreshOfAnUnknownProxyFailsWithItsName(t *testing.T) {
	h, f := proxyHarness(t)
	if code := h.app.Run([]string{"proxy", "refresh", "ghost"}); code != 1 || !strings.Contains(h.err.String(), "ghost") || len(f.refreshed) != 0 {
		t.Errorf("exit %d err %q refreshed %v", code, h.err, f.refreshed)
	}
}

func TestProxyRefreshFailureIsReported(t *testing.T) {
	h, f := proxyHarness(t)
	f.err = os.ErrDeadlineExceeded
	if code := h.app.Run([]string{"proxy", "refresh", "aws"}); code != 1 {
		t.Errorf("exit %d", code)
	}
}

func TestProxyLsShowsWhatTheAgentTypesAndWhereItRuns(t *testing.T) {
	h, _ := proxyHarness(t)
	terminal(h)
	h.app.Run([]string{"proxy", "list"})
	out := plain(h)
	for _, want := range []string{"aws-cli", "bin/aws", "/usr/bin/gh", "agent directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}
