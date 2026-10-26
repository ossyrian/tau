package proxy

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
)

func prof(t *testing.T, name string) profile.Profile {
	t.Helper()
	p, err := profile.Set{Root: "/r"}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func proxies() config.Proxies {
	return config.Proxies{
		Packages: map[string]config.ProxyPackage{"aws-cli": {Script: []string{"x"}}},
		Defs: map[string]config.Proxy{
			"aws-prod": {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws", Description: "prod", Deny: []string{"--debug"}, RunsIn: config.EmptyDirectory},
			"aws-dev":  {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws", RunsIn: config.EmptyDirectory},
			"git":      {SystemBinary: "/usr/bin/git", AgentCommand: "git", RunsIn: config.AgentDirectory},
			"tf":       {SystemBinary: "/usr/bin/terraform", AgentCommand: "terraform", RunsIn: config.EmptyDirectory},
		},
	}
}

func members(t *testing.T) []Member {
	return []Member{
		{Profile: prof(t, "default"), Active: []string{"aws-dev", "git"}, SharedWorkspace: true},
		{Profile: prof(t, "work"), Active: []string{"aws-prod", "git"}, SharedWorkspace: false},
		{Profile: prof(t, "play"), Active: []string{"tf"}, SharedWorkspace: true},
	}
}

func fakeSecrets(t *testing.T, env map[string][]Var) *Secrets {
	s := store(t)
	for p, vars := range env {
		for _, v := range vars {
			if err := s.Set(p, v.Key, v.Value); err != nil {
				t.Fatal(err)
			}
		}
	}
	return s
}

func newHost(t *testing.T) Host {
	return Host{Proxies: proxies(), Members: members(t), Secrets: fakeSecrets(t, nil)}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestOnlyProxiesSomeProfileUsesNeedAServer(t *testing.T) {
	h := newHost(t)
	if got := h.Used(); !reflect.DeepEqual(got, []string{"aws-dev", "aws-prod", "git", "tf"}) {
		t.Errorf("got %v", got)
	}
	h.Members = h.Members[:1]
	if got := h.Used(); !reflect.DeepEqual(got, []string{"aws-dev", "git"}) {
		t.Errorf("one member: %v", got)
	}
}

func TestEachProxyGetsItsOwnBootstrapWithOnlyItsOwnSecrets(t *testing.T) {
	h := newHost(t)
	h.Secrets = fakeSecrets(t, map[string][]Var{"aws-prod": {{Key: "K", Value: "prod-secret"}}, "aws-dev": {{Key: "K", Value: "dev-secret"}}})
	prod, err := h.Bootstrap("aws-prod", false)
	if err != nil {
		t.Fatal(err)
	}
	dev, _ := h.Bootstrap("aws-dev", false)
	if len(prod.Proxies) != 1 || prod.Proxies["aws-prod"].Env["K"] != "prod-secret" {
		t.Errorf("prod: %+v", prod)
	}
	if len(dev.Proxies) != 1 || dev.Proxies["aws-dev"].Env["K"] != "dev-secret" {
		t.Errorf("dev: %+v", dev)
	}
	raw, _ := json.Marshal(prod)
	if strings.Contains(string(raw), "dev-secret") {
		t.Error("the prod server was sent the dev secret")
	}
}

func TestEachProfileMayCallOnlyTheProxiesItChose(t *testing.T) {
	h := newHost(t)
	prod, _ := h.Bootstrap("aws-prod", false)
	work := prof(t, "work")
	if !reflect.DeepEqual(prod.Users, map[string][]string{itoa(work.UID): {"aws-prod"}}) {
		t.Errorf("prod users %v", prod.Users)
	}
	dev, _ := h.Bootstrap("aws-dev", false)
	if !reflect.DeepEqual(dev.Users, map[string][]string{"1000": {"aws-dev"}}) {
		t.Errorf("dev users %v", dev.Users)
	}
}

func TestAnAgentDirectoryProxyIsCallableOnlyByProfilesWithTheSharedWorkspace(t *testing.T) {
	h := newHost(t)
	git, _ := h.Bootstrap("git", false)
	work := prof(t, "work") // isolated workspace, chose git
	if _, ok := git.Users[itoa(work.UID)]; ok {
		t.Errorf("an isolated profile may call git: %v", git.Users)
	}
	if !reflect.DeepEqual(git.Users["1000"], []string{"git"}) {
		t.Errorf("users %v", git.Users)
	}
	if !reflect.DeepEqual(git.Roots["1000"], []string{SharedWorkspacePath}) {
		t.Errorf("roots %v", git.Roots)
	}
	if !git.Proxies["git"].InWorkspace {
		t.Error("InWorkspace unset")
	}
}

func TestAPackageProxyRunsItsBinaryFromThePackageAndFindsItsHelpersThere(t *testing.T) {
	b, _ := newHost(t).Bootstrap("aws-prod", false)
	s := b.Proxies["aws-prod"]
	if s.Exec != "/opt/tau/proxy-packages/aws-cli/bin/aws" {
		t.Errorf("exec %q", s.Exec)
	}
	if !strings.HasPrefix(s.Path, "/opt/tau/proxy-packages/aws-cli/bin:") || !strings.HasSuffix(s.Path, ServerPath) {
		t.Errorf("path %q", s.Path)
	}
	if s.AgentCommand != "aws" || s.Description != "prod" || !reflect.DeepEqual(s.Deny, []string{"--debug"}) {
		t.Errorf("spec %+v", s)
	}
}

func TestASystemBinaryProxyRunsThatPathWithTheServersPath(t *testing.T) {
	b, _ := newHost(t).Bootstrap("tf", false)
	if s := b.Proxies["tf"]; s.Exec != "/usr/bin/terraform" || s.Path != "" {
		t.Errorf("spec %+v", s)
	}
}

func TestMintingRunsEnvFromOnlyWhenAsked(t *testing.T) {
	h := newHost(t)
	d := h.Proxies.Defs["aws-prod"]
	d.EnvFrom = "mint-prod"
	h.Proxies.Defs["aws-prod"] = d
	var ran []string
	h.Secrets.Run = func(cmd string) (string, error) { ran = append(ran, cmd); return "TOKEN=fresh\n", nil }

	b, err := h.Bootstrap("aws-prod", false)
	if err != nil || len(ran) != 0 || b.Proxies["aws-prod"].Minted != nil {
		t.Fatalf("without mint: ran %v minted %v err %v", ran, b.Proxies["aws-prod"].Minted, err)
	}
	b, err = h.Bootstrap("aws-prod", true)
	if err != nil || !reflect.DeepEqual(ran, []string{"mint-prod"}) || b.Proxies["aws-prod"].Minted["TOKEN"] != "fresh" {
		t.Fatalf("with mint: ran %v spec %+v err %v", ran, b.Proxies["aws-prod"], err)
	}
}

func TestMintingOneProxyNeverRunsAnothersEnvFrom(t *testing.T) {
	h := newHost(t)
	for _, n := range []string{"aws-prod", "aws-dev"} {
		d := h.Proxies.Defs[n]
		d.EnvFrom = "mint-" + n
		h.Proxies.Defs[n] = d
	}
	var ran []string
	h.Secrets.Run = func(cmd string) (string, error) { ran = append(ran, cmd); return "", nil }
	h.Bootstrap("aws-dev", true)
	if !reflect.DeepEqual(ran, []string{"mint-aws-dev"}) {
		t.Errorf("ran %v", ran)
	}
}

func TestAProxyWithoutEnvFromMintsAnEmptySetWhenAsked(t *testing.T) {
	b, err := newHost(t).Bootstrap("aws-dev", true)
	if err != nil || b.Proxies["aws-dev"].Minted == nil || len(b.Proxies["aws-dev"].Minted) != 0 {
		t.Errorf("got %+v %v", b.Proxies["aws-dev"].Minted, err)
	}
}

func TestAMintFailureNamesTheProxy(t *testing.T) {
	h := newHost(t)
	d := h.Proxies.Defs["aws-prod"]
	d.EnvFrom = "mint"
	h.Proxies.Defs["aws-prod"] = d
	h.Secrets.Run = func(string) (string, error) { return "", errors.New("sso expired") }
	_, err := h.Bootstrap("aws-prod", true)
	if err == nil || !strings.Contains(err.Error(), "aws-prod") || !strings.Contains(err.Error(), "sso expired") {
		t.Errorf("got %v", err)
	}
}

func TestBootstrapOfAnUnknownProxyIsAnError(t *testing.T) {
	if _, err := newHost(t).Bootstrap("ghost", false); err == nil {
		t.Error("accepted")
	}
}

func TestCommandsListsEachAgentCommandOnceForTheShims(t *testing.T) {
	if got := newHost(t).Commands(); !reflect.DeepEqual(got, []string{"aws", "git", "terraform"}) {
		t.Errorf("got %v", got)
	}
}

func TestShimScriptCallsTheProxyWithoutAShellExpansionOfArguments(t *testing.T) {
	s := ShimScript("aws")
	if !strings.HasPrefix(s, "#!/bin/sh\n") || !strings.Contains(s, `exec tau-proxy call aws -- "$@"`) {
		t.Errorf("got %q", s)
	}
}

func TestCheckRefusesPackagelessNamesThatCannotBeUsers(t *testing.T) {
	h := newHost(t)
	if err := h.Check(); err != nil {
		t.Errorf("good config refused: %v", err)
	}
	h.Proxies.Defs["Bad_Name"] = config.Proxy{SystemBinary: "/bin/x", AgentCommand: "x"}
	if err := h.Check(); err == nil || !strings.Contains(err.Error(), "Bad_Name") {
		t.Errorf("got %v", err)
	}
}

func TestCheckRefusesAProxyWhosePackageIsMissing(t *testing.T) {
	h := newHost(t)
	delete(h.Proxies.Packages, "aws-cli")
	if err := h.Check(); err == nil || !strings.Contains(err.Error(), "aws-cli") {
		t.Errorf("got %v", err)
	}
}

func TestAProfileThatNamesAProxyNobodyDefinedGetsNoServerForIt(t *testing.T) {
	h := newHost(t)
	h.Members = append(h.Members, Member{Profile: prof(t, "ghostly"), Active: []string{"ghost"}})
	for _, n := range h.Used() {
		if n == "ghost" {
			t.Errorf("Used lists an undefined proxy: %v", h.Used())
		}
	}
}

func TestCheckRefusesTwoProxiesThatShareAUID(t *testing.T) {
	seen := map[int]string{}
	var a, b string
	for i := 0; i < 200000 && b == ""; i++ {
		n := "p" + strings.Repeat("x", i%5) + itoa(i)
		if prev, ok := seen[UIDFor(n)]; ok {
			a, b = prev, n
		}
		seen[UIDFor(n)] = n
	}
	h := newHost(t)
	h.Proxies.Defs[a] = config.Proxy{SystemBinary: "/bin/a", AgentCommand: "a"}
	h.Proxies.Defs[b] = config.Proxy{SystemBinary: "/bin/b", AgentCommand: "b"}
	if err := h.Check(); err == nil || !strings.Contains(err.Error(), "same uid") {
		t.Errorf("got %v", err)
	}
}
