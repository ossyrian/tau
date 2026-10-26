package profile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestANamedProfileLivesUnderProfiles(t *testing.T) {
	p, err := Set{Root: "/h/.tau"}.Get("work")
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != "/h/.tau/profiles/work" || p.User != "p-work" || p.Home() != "/home/p-work" {
		t.Errorf("got %+v", p)
	}
	if p.UID < 2000 || p.UID >= 22000 {
		t.Errorf("uid %d outside 2000-21999", p.UID)
	}
}

func TestTheUIDDependsOnlyOnTheName(t *testing.T) {
	a, _ := Set{Root: "/a"}.Get("work")
	b, _ := Set{Root: "/b"}.Get("work")
	c, _ := Set{Root: "/a"}.Get("play")
	if a.UID != b.UID {
		t.Errorf("same name, different uid: %d %d", a.UID, b.UID)
	}
	if a.UID == c.UID {
		t.Errorf("work and play share uid %d", a.UID)
	}
}

func TestNamesAreChecked(t *testing.T) {
	for _, bad := range []string{"Work", "1x", "a b", "a/b", "..", "a.b", "-x", strings.Repeat("a", 25), "root", "pi"} {
		if _, err := (Set{Root: "/r"}).Get(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"work", "a", "my-team", strings.Repeat("a", 24)} {
		if _, err := (Set{Root: "/r"}).Get(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
}

func TestPathsBelongToTheProfile(t *testing.T) {
	p, _ := Set{Root: "/r"}.Get("work")
	d := "/r/profiles/work"
	got := map[string]string{
		"config": p.ConfigFile(), "runtime": p.RuntimeFile(), "pi": p.PiDir(), "env": p.EnvFile(),
		"zshrc": p.Zshrc(), "vimrc": p.Vimrc(), "git": p.GitConfig(), "scripts": p.Scripts(),
		"share": p.Share(), "treehouse": p.Treehouse(), "workspace": p.Workspace(),
	}
	want := map[string]string{
		"config": d + "/config.yaml", "runtime": d + "/config.runtime.yaml", "pi": d + "/pi", "env": d + "/.env",
		"zshrc": d + "/zshrc", "vimrc": d + "/vimrc", "git": d + "/.gitconfig", "scripts": d + "/scripts",
		"share": d + "/share", "treehouse": d + "/treehouse", "workspace": d + "/workspace",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v\nwant %v", got, want)
	}
}

func TestListShowsTheDefaultFirstThenNamedProfilesInOrder(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"profiles/zed", "profiles/alpha", "profiles/Bad Name"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	os.WriteFile(filepath.Join(root, "profiles", "afile"), nil, 0o644)
	ps, err := Set{Root: root}.List()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range ps {
		names = append(names, p.Name)
	}
	if want := []string{"default", "alpha", "zed"}; !reflect.DeepEqual(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestSelectNeedsTheProfileToExist(t *testing.T) {
	s := Set{Root: t.TempDir()}
	if p, err := s.Select(""); err != nil || p.Name != Default {
		t.Errorf("default: %v %v", p, err)
	}
	if _, err := s.Select("work"); err == nil || !strings.Contains(err.Error(), "tau profiles new work") {
		t.Errorf("want a hint to create it, got %v", err)
	}
	if _, err := s.Create("work"); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Select("work"); err != nil || p.Name != "work" {
		t.Errorf("after create: %v %v", p, err)
	}
}

func TestCreateMakesTheDirectoriesAndRefusesDuplicates(t *testing.T) {
	s := Set{Root: t.TempDir()}
	p, err := s.Create("work")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{p.Dir, p.PiDir()} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			t.Errorf("%s not created", d)
		}
	}
	if _, err := s.Create("work"); err == nil {
		t.Error("second create succeeded")
	}
	if _, err := s.Create(Default); err == nil {
		t.Error("created the default profile")
	}
}

func TestTwoNamesWithTheSameUIDAreRefused(t *testing.T) {
	// Find a colliding pair by search, so the test does not depend on the hash.
	seen := map[int]string{}
	var a, b string
	for i := 0; i < 5000 && b == ""; i++ {
		n := "x" + strings.Repeat("a", i%20) + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26))
		p, err := Set{Root: "/r"}.Get(n)
		if err != nil {
			continue
		}
		if prev, ok := seen[p.UID]; ok && prev != n {
			a, b = prev, n
		}
		seen[p.UID] = n
	}
	if b == "" {
		t.Skip("no collision found")
	}
	root := t.TempDir()
	s := Set{Root: root}
	os.MkdirAll(filepath.Join(root, "profiles", a), 0o755)
	os.MkdirAll(filepath.Join(root, "profiles", b), 0o755)
	if _, err := s.List(); err == nil || !strings.Contains(err.Error(), "uid") {
		t.Errorf("want a uid collision error, got %v", err)
	}
}

func TestSecretsAreSharedAndLiveInTheRoot(t *testing.T) {
	if got := (Set{Root: "/r"}).SecretsDir(); got != "/r/secrets" {
		t.Errorf("got %q", got)
	}
}
