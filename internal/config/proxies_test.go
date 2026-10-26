package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const awsProxy = `proxies:
  packages:
    aws-cli:
      check: test -x "$PREFIX/bin/aws"
      script: install-aws
  defs:
    aws:
      package: aws-cli
      binary: bin/aws
      agent_command: aws
      description: AWS CLI
      deny: [configure export-credentials, --debug]
      env_from: aws configure export-credentials --format env
  active: [aws]
`

func TestProxiesReadDefinitionsPackagesAndActiveList(t *testing.T) {
	p, err := LoadProxies(writeBase(t, awsProxy))
	if err != nil {
		t.Fatal(err)
	}
	want := Proxy{
		Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws", Description: "AWS CLI",
		Deny:    []string{"configure export-credentials", "--debug"},
		EnvFrom: "aws configure export-credentials --format env",
		RunsIn:  EmptyDirectory,
	}
	if !reflect.DeepEqual(p.Defs["aws"], want) || !reflect.DeepEqual(p.Active, []string{"aws"}) {
		t.Errorf("got %+v", p)
	}
	pkg := p.Packages["aws-cli"]
	if !reflect.DeepEqual(pkg.Script, []string{"install-aws"}) || pkg.Check != `test -x "$PREFIX/bin/aws"` {
		t.Errorf("package %+v", pkg)
	}
	if !reflect.DeepEqual(p.Names(), []string{"aws"}) || !reflect.DeepEqual(p.PackageNames(), []string{"aws-cli"}) {
		t.Errorf("names %v %v", p.Names(), p.PackageNames())
	}
}

func TestAProxyCanRunASystemBinary(t *testing.T) {
	p, err := LoadProxies(writeBase(t, "proxies:\n  defs:\n    git: {system_binary: /usr/bin/git, agent_command: git, runs_in: agent_directory}\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := p.Defs["git"]
	if g.SystemBinary != "/usr/bin/git" || g.Package != "" || g.Binary != "" || g.RunsIn != AgentDirectory || !g.InAgentDirectory() {
		t.Errorf("got %+v", g)
	}
}

func TestRunsInDefaultsToAnEmptyDirectory(t *testing.T) {
	p, err := LoadProxies(writeBase(t, "proxies:\n  defs:\n    a: {system_binary: /bin/a, agent_command: a}\n"))
	if err != nil || p.Defs["a"].RunsIn != EmptyDirectory || p.Defs["a"].InAgentDirectory() {
		t.Errorf("got %+v %v", p.Defs["a"], err)
	}
}

func TestProxiesRejectBadInput(t *testing.T) {
	const pk = "  packages: {p: {script: x}}\n"
	for name, body := range map[string]string{
		"bad name":               "proxies:\n  defs:\n    'a b': {system_binary: /bin/x, agent_command: x}\n",
		"no agent command":       "proxies:\n  defs:\n    a: {system_binary: /bin/x}\n",
		"path agent command":     "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: /usr/bin/x}\n",
		"space agent command":    "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: 'x y'}\n",
		"no binary at all":       "proxies:\n  defs:\n    a: {agent_command: x}\n",
		"both binaries":          "proxies:\n" + pk + "  defs:\n    a: {package: p, binary: bin/x, system_binary: /bin/x, agent_command: x}\n",
		"binary without package": "proxies:\n  defs:\n    a: {binary: bin/x, agent_command: x}\n",
		"package without binary": "proxies:\n" + pk + "  defs:\n    a: {package: p, agent_command: x}\n",
		"undeclared package":     "proxies:\n" + pk + "  defs:\n    a: {package: q, binary: bin/x, agent_command: x}\n",
		"relative system binary": "proxies:\n  defs:\n    a: {system_binary: bin/x, agent_command: x}\n",
		"absolute binary":        "proxies:\n" + pk + "  defs:\n    a: {package: p, binary: /bin/x, agent_command: x}\n",
		"binary climbs out":      "proxies:\n" + pk + "  defs:\n    a: {package: p, binary: ../x, agent_command: x}\n",
		"binary climbs mid-path": "proxies:\n" + pk + "  defs:\n    a: {package: p, binary: bin/../../x, agent_command: x}\n",
		"binary is the prefix":   "proxies:\n" + pk + "  defs:\n    a: {package: p, binary: '.', agent_command: x}\n",
		"bad runs_in":            "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: x, runs_in: anywhere}\n",
		"old command field":      "proxies:\n  defs:\n    a: {command: x}\n",
		"old workspace field":    "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: x, workspace: true}\n",
		"empty deny":             "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: x, deny: ['']}\n",
		"unknown key":            "proxies:\n  defs:\n    a: {system_binary: /bin/x, agent_command: x, denny: [y]}\n",
		"unknown section":        "proxies:\n  def: {}\n",
		"undefined use":          "proxies:\n  active: [ghost]\n",
		"empty active":           "proxies:\n  defs: {a: {system_binary: /bin/x, agent_command: x}}\n  active: ['']\n",
		"duplicate use":          "proxies:\n  defs: {a: {system_binary: /bin/x, agent_command: x}}\n  active: [a, a]\n",
		"package name":           "proxies:\n  packages: {'A B': {script: x}}\n",
		"package without script": "proxies:\n  packages: {p: {check: x}}\n",
		"package empty script":   "proxies:\n  packages: {p: {script: ['']}}\n",
		"package apt":            "proxies:\n  packages: {p: {apt: [curl]}}\n",
		"package as":             "proxies:\n  packages: {p: {script: x, as: user}}\n",
		"package var name":       "proxies:\n  packages: {p: {script: x, vars: {'a b': c}}}\n",
	} {
		if _, err := LoadProxies(writeBase(t, body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTwoActiveProxiesMayNotShareAnAgentCommand(t *testing.T) {
	body := "proxies:\n  defs:\n    aws-dev: {system_binary: /bin/a, agent_command: aws}\n    aws-prod: {system_binary: /bin/b, agent_command: aws}\n    other: {system_binary: /bin/c, agent_command: other}\n  active: [aws-dev, aws-prod]\n"
	_, err := LoadProxies(writeBase(t, body))
	if err == nil || !strings.Contains(err.Error(), "aws-dev") || !strings.Contains(err.Error(), "aws-prod") || !strings.Contains(err.Error(), `"aws"`) {
		t.Errorf("got %v", err)
	}
	// Defining both is fine, so long as no profile uses both.
	ok := strings.Replace(body, "active: [aws-dev, aws-prod]", "active: [aws-dev, other]", 1)
	if _, err := LoadProxies(writeBase(t, ok)); err != nil {
		t.Errorf("defined twice but used once: %v", err)
	}
}

func TestAProfileThatPicksTwoProxiesWithOneAgentCommandIsRejected(t *testing.T) {
	defs, err := LoadProxies(writeBase(t, "proxies:\n  defs:\n    a1: {system_binary: /bin/a, agent_command: aws}\n    a2: {system_binary: /bin/b, agent_command: aws}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxiesWith(writeBase(t, "proxies:\n  active: [a1, a2]\n"), &defs); err == nil {
		t.Error("accepted")
	}
}

func TestProxyErrorsNameTheProblem(t *testing.T) {
	_, err := LoadProxies(writeBase(t, "proxies:\n  active: [ghost]\n"))
	if err == nil || !strings.Contains(err.Error(), `"ghost"`) {
		t.Errorf("got %v", err)
	}
	_, err = LoadProxies(writeBase(t, "proxies:\n  defs:\n    a: {package: nope, binary: bin/x, agent_command: x}\n"))
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("got %v", err)
	}
}

func TestAProfileMayUseProxiesTheDefaultDefined(t *testing.T) {
	defs, _ := LoadProxies(writeBase(t, awsProxy))
	dir := writeBase(t, "proxies:\n  active: [aws]\n")
	p, err := LoadProxiesWith(dir, &defs)
	if err != nil || !reflect.DeepEqual(p.Active, []string{"aws"}) || p.Defs["aws"].AgentCommand != "aws" {
		t.Errorf("got %+v %v", p, err)
	}
	if _, err := LoadProxiesWith(writeBase(t, "proxies:\n  active: [other]\n"), &defs); err == nil {
		t.Error("an undefined proxy was accepted")
	}
}

func TestOnlyTheDefaultProfileMayDefineProxies(t *testing.T) {
	if _, err := LoadProxiesWith(writeBase(t, awsProxy), nil); err != nil {
		t.Errorf("the default profile may define: %v", err)
	}
	other := &Proxies{Defs: map[string]Proxy{"x": {AgentCommand: "x", SystemBinary: "/bin/x"}}}
	_, err := LoadProxiesWith(writeBase(t, awsProxy), other)
	if err == nil || !strings.Contains(err.Error(), "default profile") {
		t.Errorf("got %v", err)
	}
}

func TestSaveProxiesActiveKeepsDefinitionsAndComments(t *testing.T) {
	dir := writeBase(t, "# top\n"+awsProxy+"packages: {}  # keep\n")
	if err := SaveProxiesActive(dir, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	s := string(data)
	for _, want := range []string{"# top", "# keep", "command: aws", "active: []"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q:\n%s", want, s)
		}
	}
	if err := SaveProxiesActive(dir, []string{"aws"}); err != nil {
		t.Fatal(err)
	}
	if p, err := LoadProxies(dir); err != nil || !reflect.DeepEqual(p.Active, []string{"aws"}) {
		t.Errorf("round trip %+v %v", p, err)
	}
}

func TestAProfileSeesTheDefaultsPackagesButMayNotDeclareItsOwn(t *testing.T) {
	defs, _ := LoadProxies(writeBase(t, awsProxy))
	p, err := LoadProxiesWith(writeBase(t, "proxies:\n  active: [aws]\n"), &defs)
	if err != nil || p.Packages["aws-cli"].Check == "" {
		t.Errorf("packages not inherited: %+v %v", p.Packages, err)
	}
	_, err = LoadProxiesWith(writeBase(t, "proxies:\n  packages: {x: {script: y}}\n"), &defs)
	if err == nil || !strings.Contains(err.Error(), "default profile") {
		t.Errorf("got %v", err)
	}
}
