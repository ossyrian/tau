package build

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/profile"
)

type fakeRun struct {
	args       []string
	dockerfile string
	out        io.Writer
	err        error
}

func (f *fakeRun) run(args []string, dockerfile string, out io.Writer) error {
	f.args, f.dockerfile, f.out = args, dockerfile, out
	return f.err
}

func repoWith(t *testing.T, dockerfile string) string {
	t.Helper()
	d := t.TempDir()
	if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatal(err)
	}
	return d
}

func cfgWith(t *testing.T, yaml string) *config.Config {
	t.Helper()
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte(yaml), 0o644)
	c, err := config.Load(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

const df = "FROM scratch\n" + Marker + "\nCMD [\"x\"]\n"

func TestPlan(t *testing.T) {
	b := &Builder{Repo: repoWith(t, df)}
	p, err := b.Plan(only(cfgWith(t, "packages:\n  hello: {script: echo hi}\n")))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Dockerfile, "echo hi") || strings.Contains(p.Dockerfile, Marker) {
		t.Errorf("dockerfile %q", p.Dockerfile)
	}
	if len(p.Entries) != 1 || p.Hash != ConfigHash(p.Entries) {
		t.Errorf("plan %+v", p)
	}
}

func TestPlanErrors(t *testing.T) {
	good := "packages:\n  hello: {script: echo hi}\n"
	if _, err := (&Builder{Repo: t.TempDir()}).Plan(only(cfgWith(t, good))); err == nil || !strings.Contains(err.Error(), "TAU_REPO") {
		t.Errorf("no Dockerfile: %v", err)
	}
	if _, err := (&Builder{Repo: repoWith(t, "FROM scratch\n")}).Plan(only(cfgWith(t, good))); err == nil {
		t.Error("no marker accepted")
	}
	if _, err := (&Builder{Repo: repoWith(t, df)}).Plan(only(cfgWith(t, "packages:\n  x: {script: echo, needs: nope}\n"))); err == nil {
		t.Error("bad config accepted")
	}
}

func TestBuildRunsDockerWithTheDockerfileOnStdin(t *testing.T) {
	f := &fakeRun{}
	b := &Builder{Repo: "/repo", Tag: "tau:x", Run: f.run}
	var out strings.Builder
	p := Plan{Dockerfile: "FROM x\n", Hash: "h"}
	if err := b.Build(p, false, &out); err != nil {
		t.Fatal(err)
	}
	want := DockerArgs(Options{Tag: "tau:x", Hash: "h", Context: "/repo"})
	if !reflect.DeepEqual(f.args, want) || f.dockerfile != "FROM x\n" || f.out != &out {
		t.Errorf("args %v\nwant %v", f.args, want)
	}
}

func TestBuildNoCacheAndErrors(t *testing.T) {
	f := &fakeRun{err: errors.New("boom")}
	b := &Builder{Repo: "/r", Tag: "t", Run: f.run}
	if err := b.Build(Plan{}, true, io.Discard); err == nil {
		t.Error("error swallowed")
	}
	if !contains(f.args, "--no-cache") {
		t.Errorf("args %v", f.args)
	}
	if (&Builder{Repo: "/r", Tag: "t"}).Build(Plan{}, false, io.Discard) == nil {
		t.Error("no runner must be an error, not a panic")
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

type inspector struct {
	out string
	err error
	got []string
}

func (i *inspector) Output(args ...string) (string, error) { i.got = args; return i.out, i.err }

func TestImageStatus(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want ImageStatus
	}{
		{"current", "abc\n", nil, ImageCurrent},
		{"stale", "old\n", nil, ImageStale},
		{"unlabelled", "\n", nil, ImageStale}, // built by plain `docker build`
		{"missing", "", errors.New("Error: No such image: tau:x"), ImageMissing},
	}
	for _, c := range cases {
		in := &inspector{out: c.out, err: c.err}
		b := &Builder{Tag: "tau:x", D: in}
		got, err := b.Status("abc")
		if err != nil || got != c.want {
			t.Errorf("%s: got %v %v", c.name, got, err)
		}
	}
	in := &inspector{out: "abc"}
	(&Builder{Tag: "tau:x", D: in}).Status("abc")
	want := []string{"image", "inspect", "-f", `{{index .Config.Labels "` + LabelConfigHash + `"}}`, "tau:x"}
	if !reflect.DeepEqual(in.got, want) {
		t.Errorf("args %v", in.got)
	}
}

func TestImageStatusOtherFailuresAreErrors(t *testing.T) {
	b := &Builder{Tag: "t", D: &inspector{err: errors.New("Cannot connect to the Docker daemon")}}
	if got, err := b.Status("h"); err == nil || got != ImageUnknown {
		t.Errorf("got %v %v", got, err)
	}
	if ImageUnknown.String() != "unknown" || ImageCurrent.String() != "current" {
		t.Error("names")
	}
}

func only(c *config.Config) []ProfileConfig {
	p, _ := profile.Set{Root: "/r"}.Get("default")
	return []ProfileConfig{{Profile: p, Config: c}}
}

func named(t *testing.T, name, yaml string) ProfileConfig {
	t.Helper()
	p, err := profile.Set{Root: "/r"}.Get(name)
	if err != nil {
		t.Fatal(err)
	}
	return ProfileConfig{Profile: p, Config: cfgWith(t, yaml)}
}

func TestPlanBuildsEveryProfileIntoOneImage(t *testing.T) {
	def := named(t, "default", "packages:\n  base: {script: echo base}\n")
	work := named(t, "work", "packages:\n  mine: {script: echo mine, as: user}\n")
	p, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def, work})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"echo base", "echo mine", "useradd -m -u", "USER p-work"} {
		if !strings.Contains(p.Dockerfile, want) {
			t.Errorf("missing %q", want)
		}
	}
	if len(p.Entries) != 2 {
		t.Errorf("entries %d", len(p.Entries))
	}
	alone, _ := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def})
	if alone.Hash == p.Hash {
		t.Error("adding a profile kept the hash")
	}
}

func TestPlanNamesTheProfileWhoseConfigIsBroken(t *testing.T) {
	def := named(t, "default", "packages:\n  base: {script: echo base}\n")
	work := named(t, "work", "packages:\n  x: {script: echo, needs: nope}\n")
	_, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def, work})
	if err == nil || !strings.Contains(err.Error(), `profile "work"`) {
		t.Errorf("got %v", err)
	}
}

func TestASharedWorkspaceProfileJoinsTheSharedGroup(t *testing.T) {
	def := named(t, "default", "")
	shared := named(t, "work", "workspaces:\n  mode: shared\n")
	isolated := named(t, "play", "")
	p, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def, shared, isolated})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(p.Dockerfile, "usermod -aG "+SharedGroup) != 1 || !strings.Contains(p.Dockerfile, "usermod -aG "+SharedGroup+" p-work") {
		t.Errorf("group membership wrong:\n%s", p.Dockerfile)
	}
	if strings.Contains(p.Dockerfile, SharedGroup+" p-play") {
		t.Error("an isolated profile joined the shared group")
	}
	if alone, _ := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def, named(t, "work", ""), isolated}); alone.Hash == p.Hash {
		t.Error("the workspace mode is not in the hash")
	}
}

const awsYAML = `proxies:
  packages:
    aws-cli:
      script: install-aws
  defs:
    aws: {package: aws-cli, binary: bin/aws, agent_command: aws}
`

func TestPlanInstallsHiddenProxyPackagesFromTheDefaultProfile(t *testing.T) {
	def := named(t, "default", awsYAML)
	p, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{def})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Dockerfile, "install-aws") || !strings.Contains(p.Dockerfile, "/opt/tau/proxy-packages/aws-cli") {
		t.Errorf("the proxy package is not in the build:\n%s", p.Dockerfile)
	}
	// It comes before the final USER so the image still ends as the default user.
	if !strings.HasSuffix(strings.TrimSpace(strings.Split(p.Dockerfile, "CMD")[0]), "USER pi") {
		t.Errorf("the image no longer ends as pi:\n%s", p.Dockerfile)
	}
}

func TestPlanHashCoversTheProxyPackagesAndNothingElseAboutProxies(t *testing.T) {
	b := &Builder{Repo: repoWith(t, df)}
	none, _ := b.Plan([]ProfileConfig{named(t, "default", "")})
	a, _ := b.Plan([]ProfileConfig{named(t, "default", awsYAML)})
	a2, _ := b.Plan([]ProfileConfig{named(t, "default", strings.Replace(awsYAML, "install-aws", "install-aws2", 1))})
	if none.Hash == a.Hash || a.Hash == a2.Hash {
		t.Errorf("hashes: %s %s %s", none.Hash, a.Hash, a2.Hash)
	}
	withDeny, _ := b.Plan([]ProfileConfig{named(t, "default", strings.Replace(awsYAML, "agent_command: aws}", "agent_command: aws, deny: [x], description: d}", 1))})
	if withDeny.Hash != a.Hash {
		t.Error("a deny rule made the image stale")
	}
}

func TestPlanRejectsAProxyConfigThatIsBroken(t *testing.T) {
	bad := named(t, "default", "proxies:\n  defs:\n    a: {package: nope, binary: bin/a, agent_command: a}\n")
	_, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{bad})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Errorf("got %v", err)
	}
}

func TestPlanRejectsProxyNamesThatCannotBeUsers(t *testing.T) {
	// two names that map to one uid would share a user
	h := map[int]string{}
	var a, b string
	for i := 0; i < 200000 && b == ""; i++ {
		n := "p" + strings.Repeat("x", i%5) + fmtInt(i)
		if prev, ok := h[proxyUID(n)]; ok {
			a, b = prev, n
		}
		h[proxyUID(n)] = n
	}
	y := "proxies:\n  defs:\n    " + a + ": {system_binary: /bin/a, agent_command: a}\n    " + b + ": {system_binary: /bin/b, agent_command: b}\n"
	_, err := (&Builder{Repo: repoWith(t, df)}).Plan([]ProfileConfig{named(t, "default", y)})
	if err == nil || !strings.Contains(err.Error(), "same uid") {
		t.Errorf("got %v", err)
	}
}
