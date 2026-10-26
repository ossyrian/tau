package build

import (
	"fmt"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/proxy"
)

func awsProxies() config.Proxies {
	return config.Proxies{
		Packages: map[string]config.ProxyPackage{
			"aws-cli": {Script: []string{"curl -o a.zip https://x\n/tmp/aws/install -i \"$PREFIX\""}, Check: `test -x "$PREFIX/bin/aws"`, Vars: map[string]string{"VERSION": "2.1"}},
			"kubectl": {Script: []string{"fetch kubectl"}},
		},
		Defs: map[string]config.Proxy{
			"aws-prod": {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws"},
			"aws-dev":  {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws"},
			"kube":     {Package: "kubectl", Binary: "bin/kubectl", AgentCommand: "k"},
			"git":      {SystemBinary: "/usr/bin/git", AgentCommand: "git"},
		},
	}
}

func TestEachProxyPackageInstallsAsRootIntoItsOwnPrefix(t *testing.T) {
	out, err := RenderProxyPackages(awsProxies())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"USER root",
		"export PREFIX='/opt/tau/proxy-packages/aws-cli'",
		"export PREFIX='/opt/tau/proxy-packages/kubectl'",
		"export VERSION='2.1'",
		"/tmp/aws/install",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "aws-cli") > strings.Index(out, "kubectl") {
		t.Error("packages are not in name order")
	}
	if strings.Count(out, "\nRUN ") < 2 {
		t.Errorf("not one RUN per package:\n%s", out)
	}
}

func TestAProxyPackageIsClosedToEveryoneButTheProxyGroup(t *testing.T) {
	out, _ := RenderProxyPackages(awsProxies())
	for _, want := range []string{
		"umask 027",
		"chown -R root:tau-proxies",
		"chmod -R o-rwx",
		"chmod 750 '/opt/tau/proxy-packages/aws-cli'",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestTheBuildFailsIfAProxyBinaryIsMissingAfterTheInstall(t *testing.T) {
	out, _ := RenderProxyPackages(awsProxies())
	if !strings.Contains(out, "test -x '/opt/tau/proxy-packages/aws-cli/bin/aws'") || !strings.Contains(out, "test -x '/opt/tau/proxy-packages/kubectl/bin/kubectl'") {
		t.Errorf("binary check missing:\n%s", out)
	}
	if strings.Count(out, "test -x '/opt/tau/proxy-packages/aws-cli/bin/aws'") != 1 {
		t.Errorf("two proxies on one binary checked it twice:\n%s", out)
	}
}

func TestTheCheckSkipsTheInstallButNotTheBinaryTest(t *testing.T) {
	out, _ := RenderProxyPackages(awsProxies())
	ci := strings.Index(out, "already installed, skipping")
	ti := strings.Index(out, "test -x '/opt/tau/proxy-packages/aws-cli/bin/aws'")
	if ci < 0 || ti < ci {
		t.Errorf("check/skip at %d, binary test at %d:\n%s", ci, ti, out)
	}
}

func TestProxyPackagesNeverEmitAnAptInstall(t *testing.T) {
	out, _ := RenderProxyPackages(awsProxies())
	if strings.Contains(out, "apt-get") {
		t.Error("apt installs system-wide")
	}
}

func TestProxyPackageScriptsStayInsideTheirHeredoc(t *testing.T) {
	p := awsProxies()
	pk := p.Packages["kubectl"]
	pk.Script = []string{"echo TAU_EOS\nTAU_EOS\nRUN evil"}
	p.Packages["kubectl"] = pk
	out, err := RenderProxyPackages(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range instructions(t, out) {
		if in == "EVIL" {
			t.Error("script text became an instruction")
		}
	}
}

func TestProxyPackagesRejectNulBytes(t *testing.T) {
	p := awsProxies()
	pk := p.Packages["kubectl"]
	pk.Script = []string{"a\x00b"}
	p.Packages["kubectl"] = pk
	if _, err := RenderProxyPackages(p); err == nil || !strings.Contains(err.Error(), "kubectl") {
		t.Errorf("got %v", err)
	}
}

func TestTheProxyPackageHashChangesWithWhatItInstallsOrChecks(t *testing.T) {
	base := ProxyHash(awsProxies())
	if base != ProxyHash(awsProxies()) {
		t.Fatal("not stable")
	}
	mut := map[string]func(*config.Proxies){
		"script": func(p *config.Proxies) {
			x := p.Packages["aws-cli"]
			x.Script = []string{"other"}
			p.Packages["aws-cli"] = x
		},
		"check": func(p *config.Proxies) { x := p.Packages["aws-cli"]; x.Check = "true"; p.Packages["aws-cli"] = x },
		"vars": func(p *config.Proxies) {
			x := p.Packages["aws-cli"]
			x.Vars = map[string]string{"VERSION": "3"}
			p.Packages["aws-cli"] = x
		},
		"binary": func(p *config.Proxies) { d := p.Defs["aws-dev"]; d.Binary = "bin/aws2"; p.Defs["aws-dev"] = d },
		"new":    func(p *config.Proxies) { p.Packages["extra"] = config.ProxyPackage{Script: []string{"x"}} },
	}
	for name, f := range mut {
		p := awsProxies()
		f(&p)
		if ProxyHash(p) == base {
			t.Errorf("%s did not change the hash", name)
		}
	}
}

func fmtInt(n int) string { return fmt.Sprint(n) }

func proxyUID(name string) int { return proxy.UIDFor(name) }
