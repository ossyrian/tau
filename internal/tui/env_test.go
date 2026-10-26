package tui

import (
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/envfile"
)

func someVars(f *fakeAPI) {
	f.vars = []envfile.Var{
		{Key: "GITHUB_TOKEN", Value: "ghp_supersecret1234"},
		{Key: "REGION", Value: "eu-west-1"},
	}
}

func inEnv(t *testing.T, f *fakeAPI) *Root {
	t.Helper()
	m := newRoot(t, f)
	send(t, m, "5")
	return m
}

func typeText(t *testing.T, m *Root, s string) {
	t.Helper()
	for _, c := range s {
		send(t, m, string(c))
	}
}

func TestEnvListsVariablesWithValuesMasked(t *testing.T) {
	f := newFake(t)
	someVars(f)
	out := screen(inEnv(t, f))
	if !strings.Contains(out, "GITHUB_TOKEN") || !strings.Contains(out, "REGION") {
		t.Errorf("names missing:\n%s", out)
	}
	if strings.Contains(out, "supersecret") || strings.Contains(out, "eu-west-1") {
		t.Errorf("a value is visible by default:\n%s", out)
	}
	if !strings.Contains(out, "1234") {
		t.Errorf("the last four characters should show:\n%s", out)
	}
}

func TestEnvDeleteAsksFirst(t *testing.T) {
	f := newFake(t)
	someVars(f)
	m := inEnv(t, f)
	send(t, m, "d")
	if len(f.did()) != 0 || !strings.Contains(screen(m), "GITHUB_TOKEN") {
		t.Fatal("deleted without asking")
	}
	send(t, m, "n")
	if len(f.did()) != 0 {
		t.Error("deleted after declining")
	}
	send(t, m, "down", "d", "y")
	if got := f.did(); len(got) != 1 || got[0] != "unsetenv:REGION" {
		t.Errorf("calls %v", got)
	}
}

func TestEnvSavingACredentialNameSaysItBelongsInAProxy(t *testing.T) {
	f := newFake(t)
	m := inEnv(t, f)
	send(t, m, "n")
	typeText(t, m, "GITHUB_TOKEN")
	send(t, m, "enter")
	typeText(t, m, "ghp_x")
	send(t, m, "enter")
	if !strings.Contains(screen(m), "credentials belong in proxies") {
		t.Errorf("no warning:\n%s", screen(m))
	}
	if strings.Contains(screen(m), "ghp_x") {
		t.Error("the value was shown")
	}
}

func TestEnvSetsAndDeletesThroughTheAPIAndHidesValuesOnEntry(t *testing.T) {
	f := newFake(t)
	someVars(f)
	m := inEnv(t, f)
	send(t, m, "v")
	send(t, m, "1", "5") // leave and return
	if strings.Contains(screen(m), "supersecret") {
		t.Error("values stayed revealed after leaving the section")
	}
	send(t, m, "n")
	typeText(t, m, "NEW_ONE")
	send(t, m, "enter")
	typeText(t, m, "val")
	send(t, m, "enter")
	send(t, m, "d", "y")
	if got := f.did(); len(got) != 2 || got[0] != "setenv:NEW_ONE=val" || got[1] != "unsetenv:GITHUB_TOKEN" {
		t.Errorf("calls %v", got)
	}
}
