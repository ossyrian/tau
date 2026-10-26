package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ossyrian/tau/internal/config"
	"github.com/ossyrian/tau/internal/envfile"
	"github.com/ossyrian/tau/internal/session"
	"github.com/ossyrian/tau/internal/skills"
	"github.com/ossyrian/tau/internal/workspace"
)

// open puts the named section on screen.
func open(t *testing.T, m *Root, name string) {
	t.Helper()
	for i, s := range m.sections {
		if s.Name() == name {
			drive(t, m, key(string(rune('1'+i))))
			return
		}
	}
	t.Fatalf("no section %q", name)
}

func hasCall(f *fakeAPI, want string) bool {
	for _, c := range f.did() {
		if c == want {
			return true
		}
	}
	return false
}

// A table pane is exactly what bubbles/table draws, so its rows appear on
// screen as words; these tests look for words, never for spacing.
func onScreen(m *Root, words ...string) []string {
	var missing []string
	s := screen(m)
	for _, w := range words {
		if !strings.Contains(s, w) {
			missing = append(missing, w)
		}
	}
	return missing
}

func TestEveryListedSectionShowsTheProfileItsRowsBelongTo(t *testing.T) {
	f := newFake(t)
	f.status.Profile = "work"
	f.sessions = []session.Info{{Name: "main", Profile: "work", Kind: "session", Created: time.Now(), Active: time.Now()}}
	f.skills = []skills.Skill{{Name: "s", State: skills.Inactive}}
	f.paths = []skills.PathInfo{{Path: "/p", Found: true}}
	f.vars = []envfile.Var{{Key: "K", Value: "v"}}
	f.proxies = config.Proxies{Defs: map[string]config.Proxy{"aws": {SystemBinary: "/x", AgentCommand: "aws"}}}
	f.entries = []workspace.Entry{{Name: "w", Kind: workspace.Local, Container: "/workspace/w", Host: t.TempDir()}}
	m := newRoot(t, f)
	for _, name := range []string{"sessions", "skills", "packages", "env", "proxies", "workspaces"} {
		open(t, m, name)
		if miss := onScreen(m, "PROFILE", "work"); len(miss) > 0 {
			t.Errorf("%s: missing %v", name, miss)
		}
	}
}

func TestSessionsAreSearchableByNameAndByProfile(t *testing.T) {
	f := newFake(t)
	now := time.Now()
	f.sessions = []session.Info{
		{Name: "alpha", Profile: "default", Kind: "session", Created: now, Active: now},
		{Name: "beta", Profile: "work", Kind: "session", Created: now, Active: now},
	}
	m := newRoot(t, f)
	open(t, m, "sessions")
	send(t, m, "/", "w", "o", "r", "k", "enter")
	if miss := onScreen(m, "beta"); len(miss) > 0 || strings.Contains(screen(m), "alpha") {
		t.Errorf("filter by profile: %q", screen(m))
	}
}

func TestPackagesAreSearchableByInstallMethod(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, "packages:\n  pg: {apt: [postgresql]}\n  tool: {script: echo hi}\n", "")
	m := newRoot(t, f)
	open(t, m, "packages")
	send(t, m, "/", "a", "p", "t", "enter")
	s := screen(m)
	if !strings.Contains(s, "pg") || strings.Contains(s, "tool") {
		t.Errorf("filter by install method:\n%s", s)
	}
}

func TestProxiesAreSearchableByTheCommandTheAgentTypes(t *testing.T) {
	f := newFake(t)
	f.proxies = config.Proxies{Defs: map[string]config.Proxy{
		"prod-readonly": {SystemBinary: "/usr/bin/x", AgentCommand: "aws"},
		"slack":         {SystemBinary: "/usr/bin/y", AgentCommand: "slack"},
	}}
	m := newRoot(t, f)
	open(t, m, "proxies")
	send(t, m, "/", "a", "w", "s", "enter")
	s := screen(m)
	if !strings.Contains(s, "prod-readonly") || strings.Contains(s, "slack") {
		t.Errorf("filter by command:\n%s", s)
	}
}

func TestEnvSearchCoversEveryColumnIncludingAMaskedValue(t *testing.T) {
	f := newFake(t)
	f.vars = []envfile.Var{{Key: "ALPHA", Value: "zebra-value"}, {Key: "BETA", Value: "other"}}
	m := newRoot(t, f)
	open(t, m, "env")
	send(t, m, "/", "z", "e", "b", "r", "a", "enter")
	s := screen(m)
	if !strings.Contains(s, "ALPHA") || strings.Contains(s, "BETA") {
		t.Errorf("search by value:\n%s", s)
	}
	if strings.Contains(s, "zebra-value") {
		t.Error("a search revealed a masked value")
	}
}

func TestSkillsShowTheSelectedSkillsMarkdownInAScrollableViewport(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	body := "# Title\n" + strings.Repeat("line of text\n", 80) + "THE-END\n"
	os.MkdirAll(filepath.Join(dir, "big"), 0o755)
	os.WriteFile(filepath.Join(dir, "big", "SKILL.md"), []byte(body), 0o644)
	f.skills = []skills.Skill{{Name: "big", Path: filepath.Join(dir, "big"), State: skills.Inactive}}
	m := newRoot(t, f)
	open(t, m, "skills")
	if miss := onScreen(m, "Title"); len(miss) > 0 {
		t.Fatalf("SKILL.md not shown: %q", screen(m))
	}
	if strings.Contains(screen(m), "THE-END") {
		t.Fatal("the whole file is shown; there is nothing to scroll")
	}
	send(t, m, "pgdown", "pgdown", "pgdown", "pgdown", "pgdown", "pgdown")
	if !strings.Contains(screen(m), "THE-END") {
		t.Errorf("paging down did not reach the end:\n%s", screen(m))
	}
	if !strings.Contains(m.View().Content, "\x1b[") {
		t.Error("Markdown is not highlighted")
	}
}

func TestSkillsHighlightingStaysInTheTerminalPalette(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "s"), 0o755)
	os.WriteFile(filepath.Join(dir, "s", "SKILL.md"), []byte("---\nname: s\n---\n# Head\n`code` **bold** *em*\n"), 0o644)
	f.skills = []skills.Skill{{Name: "s", Path: filepath.Join(dir, "s"), State: skills.Inactive}}
	m := newRoot(t, f)
	open(t, m, "skills")
	themed(t, "skills screen", m.View().Content)
}

func TestSkillsSpaceTogglesAndPathsCanBeReachedWithP(t *testing.T) {
	f := newFake(t)
	f.skills = []skills.Skill{{Name: "voice", State: skills.Inactive}}
	f.paths = []skills.PathInfo{{Path: "/some/dir", Found: true, Skills: 3}}
	m := newRoot(t, f)
	open(t, m, "skills")
	send(t, m, "space")
	if !hasCall(f, "activate:voice") {
		t.Errorf("calls %v", f.did())
	}
	send(t, m, "p")
	if miss := onScreen(m, "/some/dir"); len(miss) > 0 {
		t.Errorf("paths not shown: %q", screen(m))
	}
}

func TestPackagesShowTheSelectedPackagesYAML(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, "packages:\n  pg: {apt: [postgresql]}\n", "")
	m := newRoot(t, f)
	open(t, m, "packages")
	if miss := onScreen(m, "postgresql"); len(miss) > 0 {
		t.Errorf("YAML not shown:\n%s", screen(m))
	}
}

func TestPackagesAcceptAndRejectDriftOnTheSelectedRow(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	m := newRoot(t, f)
	open(t, m, "packages")
	if miss := onScreen(m, "added at runtime"); len(miss) > 0 {
		t.Fatalf("drift not shown:\n%s", screen(m))
	}
	send(t, m, "down", "a")
	base, _ := os.ReadFile(filepath.Join(f.home, "config.yaml"))
	if !strings.Contains(string(base), "r:") {
		t.Errorf("accept did not add r to config.yaml:\n%s", base)
	}
}

func TestPackagesRefuseABuildWhileThereIsDrift(t *testing.T) {
	f := newFake(t)
	writeConfig(t, f, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  r: {script: sr}\n")
	f.status.Drift = config.Diff{RuntimeOnly: []string{"r"}}
	m := newRoot(t, f)
	open(t, m, "packages")
	send(t, m, "b")
	for _, c := range f.did() {
		if strings.HasPrefix(c, "build") {
			t.Errorf("built with drift: %v", f.did())
		}
	}
}

func TestPackagesBuildShowsItsLogInTheDetailPane(t *testing.T) {
	f := newFake(t)
	f.buildOut = "Step 1/3 : FROM debian\nSuccessfully built\n"
	m := newRoot(t, f)
	open(t, m, "packages")
	send(t, m, "b")
	if !hasCall(f, "build") || !strings.Contains(screen(m), "Successfully built") {
		t.Errorf("calls %v\n%s", f.did(), screen(m))
	}
}

func TestProxiesShowTheDefinitionAndToggleUse(t *testing.T) {
	f := newFake(t)
	f.proxies = config.Proxies{
		Defs:   map[string]config.Proxy{"aws": {Package: "aws-cli", Binary: "bin/aws", AgentCommand: "aws", Deny: []string{"--debug"}}},
		Active: []string{"aws"},
	}
	m := newRoot(t, f)
	open(t, m, "proxies")
	if miss := onScreen(m, "agent_command", "--debug"); len(miss) > 0 {
		t.Errorf("definition missing %v:\n%s", miss, screen(m))
	}
	send(t, m, "space")
	if !hasCall(f, "useproxy:aws=false") {
		t.Errorf("calls %v", f.did())
	}
}

func TestEnterOpensAProxysVariablesAndEscReturns(t *testing.T) {
	f := newFake(t)
	f.proxies = config.Proxies{Defs: map[string]config.Proxy{"aws": {SystemBinary: "/x", AgentCommand: "aws"}}}
	f.proxyVars = map[string][]proxyVar{"aws": {{Key: "AWS_REGION", Value: "eu-west-1"}}}
	m := newRoot(t, f)
	open(t, m, "proxies")
	send(t, m, "enter")
	if miss := onScreen(m, "AWS_REGION"); len(miss) > 0 {
		t.Fatalf("variables not shown:\n%s", screen(m))
	}
	send(t, m, "esc")
	if miss := onScreen(m, "agent_command"); len(miss) > 0 {
		t.Errorf("did not return to the proxies:\n%s", screen(m))
	}
}

func TestWorkspacesListFilesOfTheSelectedEntry(t *testing.T) {
	f := newFake(t)
	host := t.TempDir()
	os.MkdirAll(filepath.Join(host, "cmd"), 0o755)
	os.WriteFile(filepath.Join(host, "main.go"), []byte("x"), 0o644)
	f.entries = []workspace.Entry{{Name: "proj", Kind: workspace.Live, Container: "/workspace/proj", Host: host}}
	m := newRoot(t, f)
	open(t, m, "workspaces")
	if miss := onScreen(m, "proj", "live", "main.go", "cmd/"); len(miss) > 0 {
		t.Errorf("missing %v:\n%s", miss, screen(m))
	}
}

func TestWorkspacesAddAsksForADirectoryThenAName(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	open(t, m, "workspaces")
	send(t, m, "a")
	for _, r := range "/src/app" {
		send(t, m, string(r))
	}
	send(t, m, "enter", "enter")
	if !hasCall(f, "addws:/src/app::false") {
		t.Errorf("calls %v", f.did())
	}
	send(t, m, "A")
	for _, r := range "/src/live" {
		send(t, m, string(r))
	}
	send(t, m, "enter", "enter")
	if !hasCall(f, "addws:/src/live::true") {
		t.Errorf("calls %v", f.did())
	}
}

func TestWorkspacesRemoveConfirmsFirst(t *testing.T) {
	f := newFake(t)
	f.entries = []workspace.Entry{{Name: "proj", Kind: workspace.Local, Container: "/workspace/proj", Host: t.TempDir()}}
	m := newRoot(t, f)
	open(t, m, "workspaces")
	send(t, m, "d", "n")
	if hasCall(f, "rmws:proj") {
		t.Fatal("removed without confirming")
	}
	send(t, m, "d", "y")
	if !hasCall(f, "rmws:proj") {
		t.Errorf("calls %v", f.did())
	}
}

func TestEnvStillMasksRevealsAndEdits(t *testing.T) {
	f := newFake(t)
	f.vars = []envfile.Var{{Key: "TOKEN", Value: "abcdefgh1234"}}
	m := newRoot(t, f)
	open(t, m, "env")
	if strings.Contains(screen(m), "abcdefgh") {
		t.Fatal("secret shown before reveal")
	}
	send(t, m, "v")
	if !strings.Contains(screen(m), "abcdefgh1234") {
		t.Error("v did not reveal")
	}
	send(t, m, "e", "x", "enter")
	if !hasCall(f, "setenv:TOKEN=x") {
		t.Errorf("calls %v", f.did())
	}
}

func TestHomeOffersStartStopRestart(t *testing.T) {
	f := newFake(t)
	m := newRoot(t, f)
	send(t, m, "s")
	if !hasCall(f, "start") {
		t.Errorf("calls %v", f.did())
	}
}
