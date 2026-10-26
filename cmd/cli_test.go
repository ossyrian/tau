package cmd

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/build"
	"github.com/ossyrian/tau/internal/config"
)

type harness struct {
	app    *App
	out    *bytes.Buffer
	err    *bytes.Buffer
	home   string
	repo   string
	docker [][]string // args of each docker call
	stdin  []string   // Dockerfile text of each docker call
	tui    int        // times the resolve screen ran
}

func newHarness(t *testing.T, base, runtime string) *harness {
	t.Helper()
	h := &harness{out: &bytes.Buffer{}, err: &bytes.Buffer{}, home: t.TempDir(), repo: t.TempDir()}
	write(t, filepath.Join(h.repo, "Dockerfile"), "FROM scratch\n"+build.Marker+"\nCMD [\"x\"]\n")
	if base != "" {
		write(t, filepath.Join(h.home, "config.yaml"), base)
	}
	if runtime != "" {
		write(t, filepath.Join(h.home, "config.runtime.yaml"), runtime)
	}
	h.app = &App{
		Stdout: h.out, Stderr: h.err,
		Env:         func(k string) string { return map[string]string{"TAU_HOME": h.home, "TAU_REPO": h.repo}[k] },
		Interactive: true,
		Docker: func(args []string, dockerfile string) error {
			h.docker = append(h.docker, args)
			h.stdin = append(h.stdin, dockerfile)
			return nil
		},
		Resolve: func(c *config.Config) error {
			h.tui++
			// Behave like the user accepting everything.
			for _, n := range append(c.Diff().RuntimeOnly, c.Diff().Changed...) {
				act := config.Promote
				if contains(c.Diff().Changed, n) {
					act = config.Adopt
				}
				if err := c.Apply(n, act); err != nil {
					return err
				}
			}
			_, err := c.Settle()
			return err
		},
	}
	return h
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
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

const goodBase = "packages:\n  hello: {script: echo hi}\n  later: {script: echo later, needs: hello}\n"

func TestBuildRunsDockerWithTheAssembledDockerfile(t *testing.T) {
	h := newHarness(t, goodBase, "")
	if code := h.app.Run([]string{"build"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if len(h.docker) != 1 {
		t.Fatalf("docker calls: %d", len(h.docker))
	}
	args := strings.Join(h.docker[0], " ")
	for _, want := range []string{"build -f - -t tau:latest", "--label " + build.LabelConfigHash + "=", h.repo} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
	df := h.stdin[0]
	if strings.Contains(df, build.Marker) || !strings.Contains(df, "# package: hello") || !strings.Contains(df, "# package: later") {
		t.Errorf("dockerfile wrong:\n%s", df)
	}
	if strings.Index(df, "# package: hello") > strings.Index(df, "# package: later") {
		t.Error("needs order not respected")
	}
}

func TestBuildLabelMatchesConfigHashOfThePlan(t *testing.T) {
	h := newHarness(t, goodBase, "")
	h.app.Run([]string{"build"})
	c, _ := config.Load(h.home, nil)
	entries, _ := c.Resolve()
	if want := build.LabelConfigHash + "=" + build.ConfigHash(entries); !strings.Contains(strings.Join(h.docker[0], " "), want) {
		t.Errorf("label not from the resolved plan: %v", h.docker[0])
	}
}

func TestBuildPrintWritesDockerfileAndDoesNotCallDocker(t *testing.T) {
	h := newHarness(t, goodBase, "")
	if code := h.app.Run([]string{"build", "--print"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if len(h.docker) != 0 {
		t.Error("docker ran")
	}
	if !strings.Contains(h.out.String(), "# package: hello") {
		t.Errorf("stdout:\n%s", h.out)
	}
}

func TestBuildPassesNoCache(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Run([]string{"build", "--no-cache"})
	if a := strings.Join(h.docker[0], " "); !strings.Contains(a, "--no-cache") {
		t.Errorf("missing --no-cache in %q", a)
	}
}

func TestBuildTagFlag(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Run([]string{"build", "--tag", "tau:try"})
	if a := strings.Join(h.docker[0], " "); !strings.Contains(a, "-t tau:try") || strings.Contains(a, "tau:latest") {
		t.Errorf("args: %s", a)
	}
}

func TestBuildDriftInteractiveRunsResolveThenBuildsTheResolvedConfig(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	if code := h.app.Run([]string{"build"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if h.tui != 1 {
		t.Errorf("resolve ran %d times", h.tui)
	}
	if !strings.Contains(h.stdin[0], "# package: r") {
		t.Errorf("promoted package missing from the build:\n%s", h.stdin[0])
	}
}

func TestBuildDriftNonInteractiveFailsBeforeDocker(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	h.app.Interactive = false
	if code := h.app.Run([]string{"build"}); code == 0 {
		t.Fatal("want failure")
	}
	if len(h.docker) != 0 || h.tui != 0 {
		t.Error("must not build or prompt")
	}
	for _, want := range []string{"r", "--ignore-drift", "tau packages resolve"} {
		if !strings.Contains(h.err.String(), want) {
			t.Errorf("stderr missing %q: %s", want, h.err)
		}
	}
}

func TestBuildIgnoreDriftBuildsFromConfigYamlOnly(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	h.app.Interactive = false
	if code := h.app.Run([]string{"build", "--ignore-drift"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if strings.Contains(h.stdin[0], "# package: r") || !strings.Contains(h.stdin[0], "# package: a") {
		t.Errorf("runtime-only package leaked into the build:\n%s", h.stdin[0])
	}
}

func TestBuildResolveFailureStopsTheBuild(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	h.app.Resolve = func(*config.Config) error { return errors.New("boom") }
	if code := h.app.Run([]string{"build"}); code == 0 || len(h.docker) != 0 {
		t.Errorf("code=%d docker=%d", code, len(h.docker))
	}
	if !strings.Contains(h.err.String(), "boom") {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestBuildDriftLeftAfterResolveStopsTheBuild(t *testing.T) {
	// The user quit the screen early: drift remains, so do not build.
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	h.app.Resolve = func(*config.Config) error { return nil }
	if code := h.app.Run([]string{"build"}); code == 0 || len(h.docker) != 0 {
		t.Errorf("code=%d docker=%d", code, len(h.docker))
	}
	if !strings.Contains(h.err.String(), "unresolved") {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestBuildReportsConfigErrorsWithFileAndLine(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {run: x}\n", "")
	if code := h.app.Run([]string{"build"}); code == 0 || len(h.docker) != 0 {
		t.Fatal("want failure")
	}
	if !strings.Contains(h.err.String(), "config.yaml:2") {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestBuildDockerFailureGivesNonZeroExit(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Docker = func([]string, string) error { return errors.New("docker exploded") }
	if code := h.app.Run([]string{"build"}); code == 0 {
		t.Error("want non-zero")
	}
	if !strings.Contains(h.err.String(), "docker exploded") {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestBuildMissingMarkerIsAClearError(t *testing.T) {
	h := newHarness(t, "", "")
	write(t, filepath.Join(h.repo, "Dockerfile"), "FROM scratch\n")
	if code := h.app.Run([]string{"build"}); code == 0 {
		t.Fatal("want failure")
	}
	if !strings.Contains(h.err.String(), build.Marker) {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestBuildMissingDockerfileNamesTheRepoDir(t *testing.T) {
	h := newHarness(t, "", "")
	os.Remove(filepath.Join(h.repo, "Dockerfile"))
	if code := h.app.Run([]string{"build"}); code == 0 {
		t.Fatal("want failure")
	}
	if !strings.Contains(h.err.String(), h.repo) {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestPackagesLs(t *testing.T) {
	h := newHarness(t, goodBase, "")
	if code := h.app.Run([]string{"packages", "list"}); code != 0 {
		t.Fatal(h.err)
	}
	if h.out.String() != "hello\troot\t\nlater\troot\thello\n" {
		t.Errorf("stdout: %q", h.out)
	}
}

func TestPackagesDiffShowsEachKindAndNeverChangesFiles(t *testing.T) {
	h := newHarness(t,
		"packages:\n  same: {script: s}\n  chg: {script: old}\n  late: {script: l}\n",
		"packages:\n  same: {script: s}\n  chg: {script: new}\n  run: {script: r}\n")
	before, _ := os.ReadFile(filepath.Join(h.home, "config.runtime.yaml"))
	if code := h.app.Run([]string{"packages", "diff"}); code != 0 {
		t.Fatal(h.err)
	}
	out := h.out.String()
	for _, want := range []string{"run", "chg", "late"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "same") {
		t.Errorf("unchanged package listed:\n%s", out)
	}
	after, _ := os.ReadFile(filepath.Join(h.home, "config.runtime.yaml"))
	if string(before) != string(after) {
		t.Error("diff changed a file")
	}
}

func TestPackagesDiffExitCodeFlagsDrift(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n  r: {script: r}\n")
	if code := h.app.Run([]string{"packages", "diff", "--exit-code"}); code != 1 {
		t.Errorf("code %d", code)
	}
	h = newHarness(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n")
	if code := h.app.Run([]string{"packages", "diff", "--exit-code"}); code != 0 {
		t.Errorf("code %d", code)
	}
}

func TestPackagesResolveRunsTheScreen(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n  r: {script: r}\n")
	if code := h.app.Run([]string{"packages", "resolve"}); code != 0 {
		t.Fatal(h.err)
	}
	if h.tui != 1 {
		t.Error("screen did not run")
	}
}

func TestPackagesResolveNeedsATerminal(t *testing.T) {
	h := newHarness(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n  r: {script: r}\n")
	h.app.Interactive = false
	if code := h.app.Run([]string{"packages", "resolve"}); code == 0 || h.tui != 0 {
		t.Errorf("code=%d tui=%d", code, h.tui)
	}
	if !strings.Contains(h.err.String(), "terminal") {
		t.Errorf("stderr: %s", h.err)
	}
}

func TestPackagesResolveWithNoDriftSaysSoAndSkipsTheScreen(t *testing.T) {
	h := newHarness(t, goodBase, "")
	if code := h.app.Run([]string{"packages", "resolve"}); code != 0 || h.tui != 0 {
		t.Errorf("code=%d tui=%d", code, h.tui)
	}
	if h.out.Len() != 0 {
		t.Errorf("stdout: %q", h.out)
	}
}

func TestUsageAndUnknownCommands(t *testing.T) {
	h := newHarness(t, "", "")
	if code := h.app.Run(nil); code != 0 || !strings.Contains(h.out.String(), "build") {
		t.Errorf("no args: code=%d out=%s", code, h.out)
	}
	for _, args := range [][]string{{"bogus"}, {"packages", "bogus"}, {"build", "--bogus"}, {"build", "extra"}} {
		h := newHarness(t, "", "")
		if code := h.app.Run(args); code != 1 {
			t.Errorf("%v: code %d", args, code)
		}
		if h.err.Len() == 0 {
			t.Errorf("%v: nothing on stderr", args)
		}
	}
	for _, a := range []string{"-h", "--help", "help"} {
		h := newHarness(t, "", "")
		if code := h.app.Run([]string{a}); code != 0 || h.out.Len() == 0 {
			t.Errorf("%s: code=%d", a, code)
		}
	}
}

func TestTauHomeDefaultsToDotTau(t *testing.T) {
	h := newHarness(t, "", "")
	h.app.Env = func(k string) string {
		if k == "HOME" {
			return h.home
		}
		return ""
	}
	if got := h.app.configDir(); got != filepath.Join(h.home, ".tau") {
		t.Errorf("got %s", got)
	}
}

func TestBuildUsesWhatIsOnDiskAfterResolve(t *testing.T) {
	// The screen may change the files through its own Config. The build must
	// plan from a fresh read, not from the Config it passed to the screen.
	h := newHarness(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	h.app.Resolve = func(c *config.Config) error {
		other, err := config.Load(h.home, nil)
		if err != nil {
			return err
		}
		if err := other.Apply("r", config.Promote); err != nil {
			return err
		}
		_, err = other.Settle()
		return err
	}
	if code := h.app.Run([]string{"build"}); code != 0 {
		t.Fatalf("exit %d: %s", code, h.err)
	}
	if !strings.Contains(h.stdin[0], "# package: r") {
		t.Errorf("promoted package missing:\n%s", h.stdin[0])
	}
}
