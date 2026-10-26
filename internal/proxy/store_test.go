package proxy

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

type reloads struct {
	n         int
	err       error
	refreshed [][]string
}

func (r *reloads) Reload() error { r.n++; return r.err }
func (r *reloads) Refresh(names []string) error {
	r.refreshed = append(r.refreshed, names)
	return r.err
}

func newStore(t *testing.T, defsYAML, profileYAML string) (*Store, *reloads, string, string) {
	t.Helper()
	root := t.TempDir()
	prof := root
	if profileYAML != "" {
		prof = filepath.Join(root, "profiles", "work")
		os.MkdirAll(prof, 0o755)
		os.WriteFile(filepath.Join(prof, "config.yaml"), []byte(profileYAML), 0o644)
	}
	os.WriteFile(filepath.Join(root, "config.yaml"), []byte(defsYAML), 0o644)
	r := &reloads{}
	return &Store{DefaultDir: root, Dir: prof, Secrets: &Secrets{Dir: filepath.Join(root, "secrets")}, Servers: r}, r, root, prof
}

const storeDefs = "proxies:\n  defs:\n    aws: {system_binary: /usr/bin/aws, agent_command: aws}\n    gh: {system_binary: /usr/bin/gh, agent_command: gh, runs_in: agent_directory}\n  active: [aws]\n"

func TestStoreReadsDefinitionsAndTheProfilesActiveList(t *testing.T) {
	s, _, _, _ := newStore(t, storeDefs, "")
	p, err := s.Proxies()
	if err != nil || !reflect.DeepEqual(p.Active, []string{"aws"}) || !reflect.DeepEqual(p.Names(), []string{"aws", "gh"}) {
		t.Errorf("got %+v %v", p, err)
	}
}

func TestAnotherProfileInheritsDefinitionsButKeepsItsOwnActiveList(t *testing.T) {
	s, _, _, _ := newStore(t, storeDefs, "proxies:\n  active: [gh]\n")
	p, err := s.Proxies()
	if err != nil || !reflect.DeepEqual(p.Active, []string{"gh"}) || p.Defs["aws"].AgentCommand != "aws" {
		t.Errorf("got %+v %v", p, err)
	}
}

func TestUseAddsAndRemovesAndReloads(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	if err := s.Use("gh", true); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Proxies()
	if !reflect.DeepEqual(p.Active, []string{"aws", "gh"}) || r.n != 1 {
		t.Errorf("active %v reloads %d", p.Active, r.n)
	}
	if err := s.Use("aws", false); err != nil {
		t.Fatal(err)
	}
	if p, _ = s.Proxies(); !reflect.DeepEqual(p.Active, []string{"gh"}) || r.n != 2 {
		t.Errorf("active %v reloads %d", p.Active, r.n)
	}
}

func TestUseRefusesUndefinedRepeatedAndAbsent(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	for name, tc := range map[string]struct {
		proxy string
		on    bool
		want  string
	}{
		"undefined": {"ghost", true, "ghost"},
		"repeat":    {"aws", true, "already"},
		"absent":    {"gh", false, "not in use"},
	} {
		if err := s.Use(tc.proxy, tc.on); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	if r.n != 0 {
		t.Errorf("reloaded %d times for refused changes", r.n)
	}
}

func TestSetVarStoresAndReloadsAndNeverEchoesTheValue(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	err := s.SetVar("aws", "KEY", "TOP-SECRET-VALUE")
	if err != nil || r.n != 1 {
		t.Fatalf("err %v reloads %d", err, r.n)
	}
	if v, _ := s.Vars("aws"); !reflect.DeepEqual(v, []Var{{Key: "KEY", Value: "TOP-SECRET-VALUE"}}) {
		t.Errorf("got %v", v)
	}
	s.Servers = &reloads{err: errors.New("boom TOP-SECRET-VALUE")}
	err = s.SetVar("aws", "KEY2", "other")
	if err == nil || !strings.Contains(err.Error(), "saved") {
		t.Errorf("a reload failure after a save must say it was saved: %v", err)
	}
}

func TestSetVarRefusesAnUndefinedProxy(t *testing.T) {
	s, _, root, _ := newStore(t, storeDefs, "")
	if err := s.SetVar("ghost", "K", "v"); err == nil {
		t.Error("accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "secrets", "ghost.env")); err == nil {
		t.Error("a file was made for an undefined proxy")
	}
}

func TestUnsetVarReportsWhetherItWasThere(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	s.SetVar("aws", "A", "1")
	r.n = 0
	if ok, err := s.UnsetVar("aws", "A"); !ok || err != nil || r.n != 1 {
		t.Errorf("%v %v reloads %d", ok, err, r.n)
	}
	if ok, _ := s.UnsetVar("aws", "A"); ok {
		t.Error("removed twice")
	}
}

func TestStoreWithoutServersStillSaves(t *testing.T) {
	s, _, _, _ := newStore(t, storeDefs, "")
	s.Servers = nil
	if err := s.Use("gh", true); err != nil {
		t.Errorf("got %v", err)
	}
	var _ = config.Proxies{}
}

func TestRefreshPassesTheNamesToTheServers(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	if err := s.Refresh([]string{"gh"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r.refreshed, [][]string{{"gh"}}) || r.n != 0 {
		t.Errorf("refreshed %v reloads %d", r.refreshed, r.n)
	}
}

func TestRefreshOfAnUnknownProxyNamesItAndTouchesNothing(t *testing.T) {
	s, r, _, _ := newStore(t, storeDefs, "")
	err := s.Refresh([]string{"ghost"})
	if err == nil || !strings.Contains(err.Error(), "ghost") || len(r.refreshed) != 0 {
		t.Errorf("got %v %v", err, r.refreshed)
	}
}

func TestRefreshWithoutServersIsAnError(t *testing.T) {
	s, _, _, _ := newStore(t, storeDefs, "")
	s.Servers = nil
	if err := s.Refresh(nil); err == nil {
		t.Error("accepted")
	}
}

func TestUsingAProxyWhoseAgentCommandClashesIsRejectedAndNothingIsSaved(t *testing.T) {
	defs := "proxies:\n  defs:\n    a1: {system_binary: /bin/a, agent_command: aws}\n    a2: {system_binary: /bin/b, agent_command: aws}\n  active: [a1]\n"
	s, r, _, _ := newStore(t, defs, "")
	err := s.Use("a2", true)
	if err == nil || !strings.Contains(err.Error(), "agent_command") {
		t.Fatalf("got %v", err)
	}
	p, _ := s.Proxies()
	if !reflect.DeepEqual(p.Active, []string{"a1"}) || r.n != 0 {
		t.Errorf("active %v reloads %d", p.Active, r.n)
	}
}
