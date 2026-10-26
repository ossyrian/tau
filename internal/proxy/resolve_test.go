package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serverDir makes a socket directory with one server per config and returns it.
func serverDir(t *testing.T, uid int, servers map[string]Config) string {
	t.Helper()
	root, _ := os.MkdirTemp("", "rs")
	t.Cleanup(func() { os.RemoveAll(root) })
	for name, c := range servers {
		d := filepath.Join(root, name)
		os.MkdirAll(d, 0o755)
		cl := serve(t, c, uid)
		os.Symlink(cl.Socket, filepath.Join(d, "sock"))
	}
	return root
}

func cfgFor(name, agentCommand string, uid int) Config {
	return Config{
		Proxies: map[string]Spec{name: {Exec: bin("echo"), AgentCommand: agentCommand, Description: name + " desc"}},
		Users:   map[int][]string{uid: {name}},
	}
}

func TestResolveFindsTheProxyTheCallerMayUseForACommand(t *testing.T) {
	root := serverDir(t, 1000, map[string]Config{
		"aws-dev":  cfgFor("aws-dev", "aws", 1000),
		"aws-prod": cfgFor("aws-prod", "aws", 2000), // another profile's
		"git":      cfgFor("git", "git", 1000),
	})
	name, cl, err := Resolve(root, "aws")
	if err != nil || name != "aws-dev" || cl == nil {
		t.Fatalf("got %q %v", name, err)
	}
}

func TestResolveSaysSoWhenTheCallerHasNoProxyForTheCommand(t *testing.T) {
	root := serverDir(t, 1000, map[string]Config{"aws-prod": cfgFor("aws-prod", "aws", 2000)})
	_, _, err := Resolve(root, "aws")
	if err == nil || !strings.Contains(err.Error(), `"aws"`) || !strings.Contains(err.Error(), "not available to you") {
		t.Errorf("got %v", err)
	}
	if strings.Contains(err.Error(), "aws-prod") {
		t.Errorf("the error tells the agent a proxy it cannot use exists: %v", err)
	}
}

func TestResolveSkipsServersThatAreDownAndEmptyDirectories(t *testing.T) {
	root := serverDir(t, 1000, map[string]Config{"git": cfgFor("git", "git", 1000)})
	os.MkdirAll(filepath.Join(root, "dead"), 0o755) // no socket
	os.WriteFile(filepath.Join(root, "stray-file"), nil, 0o644)
	name, _, err := Resolve(root, "git")
	if err != nil || name != "git" {
		t.Errorf("got %q %v", name, err)
	}
}

func TestResolveWithNoSocketDirectoryIsAnError(t *testing.T) {
	if _, _, err := Resolve(filepath.Join(t.TempDir(), "none"), "git"); err == nil {
		t.Error("accepted")
	}
}

func TestListAllCollectsWhatTheCallerMayUseFromEveryServer(t *testing.T) {
	root := serverDir(t, 1000, map[string]Config{
		"aws-dev":  cfgFor("aws-dev", "aws", 1000),
		"aws-prod": cfgFor("aws-prod", "aws", 2000),
		"git":      cfgFor("git", "git", 1000),
	})
	got := ListAll(root)
	if len(got) != 2 || got[0].AgentCommand != "aws" || got[1].AgentCommand != "git" {
		t.Errorf("got %+v", got)
	}
}

func TestUpSucceedsOnlyForARunningServer(t *testing.T) {
	root := serverDir(t, 1000, map[string]Config{"git": cfgFor("git", "git", 1000)})
	if err := Up(filepath.Join(root, "git", "sock")); err != nil {
		t.Errorf("running server: %v", err)
	}
	if err := Up(filepath.Join(root, "nope", "sock")); err == nil {
		t.Error("a missing server counted as up")
	}
}
