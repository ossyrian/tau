package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
)

func someProxies(f *fakeAPI) {
	f.proxies = config.Proxies{
		Defs: map[string]config.Proxy{
			"aws": {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws", Description: "AWS CLI", Deny: []string{"configure export-credentials"}, RunsIn: config.EmptyDirectory},
			"gh":  {SystemBinary: "/usr/bin/gh", AgentCommand: "gh", RunsIn: config.AgentDirectory},
		},
		Active: []string{"aws"},
	}
	f.proxyVars = map[string][]proxy.Var{"aws": {{Key: "AWS_SECRET_ACCESS_KEY", Value: "wJalrXUtnFEMI-K7MDENG"}, {Key: "AWS_REGION", Value: "us-east-1"}}}
}

func inProxies(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := newRoot(t, f)
	send(t, m, "6")
	return m
}

func TestProxiesSpaceTogglesUseForThisProfile(t *testing.T) {
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "down", "space")
	if got := f.did(); len(got) != 1 || got[0] != "useproxy:gh=true" {
		t.Fatalf("calls %v", got)
	}
	send(t, m, "up", "space")
	if got := f.did(); got[len(got)-1] != "useproxy:aws=false" {
		t.Errorf("calls %v", got)
	}
}

func TestProxiesEnterShowsVariablesWithValuesHidden(t *testing.T) {
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "enter")
	out := screen(m)
	if !strings.Contains(out, "AWS_SECRET_ACCESS_KEY") || !strings.Contains(out, "AWS_REGION") {
		t.Errorf("variables not shown:\n%s", out)
	}
	if strings.Contains(out, "wJalrXUtnFEMI") || strings.Contains(out, "us-east-1") {
		t.Errorf("a value is visible before reveal:\n%s", out)
	}
	send(t, m, "v")
	if out = screen(m); !strings.Contains(out, "wJalrXUtnFEMI-K7MDENG") {
		t.Errorf("reveal did not show the value:\n%s", out)
	}
	send(t, m, "v")
	if strings.Contains(screen(m), "wJalrXUtnFEMI-K7MDENG") {
		t.Error("v does not hide again")
	}
}

func TestProxiesDeleteAsksFirst(t *testing.T) {
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "enter", "d", "n")
	if len(f.did()) != 0 {
		t.Errorf("deleted after declining: %v", f.did())
	}
	send(t, m, "d", "y")
	if got := f.did(); len(got) != 1 || got[0] != "unsetproxyvar:aws:AWS_SECRET_ACCESS_KEY" {
		t.Errorf("calls %v", got)
	}
}

func TestProxiesRReloadsTheServers(t *testing.T) {
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "r")
	if got := f.did(); len(got) != 1 || got[0] != "reloadproxies" {
		t.Errorf("calls %v", got)
	}
}

func TestProxiesShowsAnErrorInsteadOfCrashing(t *testing.T) {
	f := newFake(t)
	f.proxyError = errors.New("config.yaml: bad proxies")
	if out := screen(inProxies(t, f)); !strings.Contains(out, "bad proxies") {
		t.Errorf("error hidden:\n%s", out)
	}
}

func TestProxiesRefreshKeyMintsTheSelectedProxyOnly(t *testing.T) {
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "down", "R")
	if got := f.did(); len(got) != 1 || got[0] != "refreshproxies:gh" {
		t.Errorf("calls %v", got)
	}
}

func TestProxiesRevealIsOffAgainWhenYouOpenAnotherProxyOrGoBack(t *testing.T) {
	const secret = "wJalrXUtnFEMI-K7MDENG"
	for name, leave := range map[string][]string{
		"back":    {"esc", "enter"},
		"another": {"esc", "down", "enter"},
	} {
		f := newFake(t)
		someProxies(f)
		m := inProxies(t, f)
		send(t, m, "enter", "v")
		if !strings.Contains(screen(m), secret) {
			t.Fatalf("%s: reveal did not work", name)
		}
		send(t, m, leave...)
		if strings.Contains(screen(m), secret) {
			t.Errorf("%s: values stayed revealed:\n%s", name, screen(m))
		}
	}
}

func TestProxyVariablesGoToTheOpenProxyAndAreHiddenOnEntry(t *testing.T) {
	const secret = "wJalrXUtnFEMI-K7MDENG"
	f := newFake(t)
	someProxies(f)
	m := inProxies(t, f)
	send(t, m, "enter", "v")
	send(t, m, "1", "6") // leave the section and return
	if strings.Contains(screen(m), secret) {
		t.Error("values stayed revealed after leaving the section")
	}
	send(t, m, "n")
	typeText(t, m, "K")
	send(t, m, "enter")
	typeText(t, m, "val")
	send(t, m, "enter")
	if got := f.did(); len(got) != 1 || !strings.HasPrefix(got[0], "setproxyvar:") || !strings.HasSuffix(got[0], ":K=val") {
		t.Errorf("calls %v", got)
	}
}
