package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeBase(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadSkillsDefaultsWhenNothingIsSet(t *testing.T) {
	for name, body := range map[string]string{
		"no file":      "",
		"no section":   "packages: {}\n",
		"null section": "skills:\n",
		"no paths":     "skills:\n  active: [a]\n",
		"null paths":   "skills:\n  paths:\n",
	} {
		dir := t.TempDir()
		if body != "" {
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644)
		}
		got, err := LoadSkills(dir)
		if err != nil || !reflect.DeepEqual(got.Paths, []string{DefaultSkillPath}) {
			t.Errorf("%s: %v %v", name, got, err)
		}
	}
}

func TestLoadSkillsAnExplicitEmptyPathsListMeansNoPaths(t *testing.T) {
	dir := writeBase(t, "skills:\n  paths: []\n")
	got, _ := LoadSkills(dir)
	if len(got.Paths) != 0 {
		t.Errorf("got %v", got.Paths)
	}
}

func TestLoadSkillsRejectsBadShapes(t *testing.T) {
	for _, body := range []string{
		"skills: [a]\n", "skills:\n  paths: x\n", "skills:\n  active: {a: 1}\n",
		"skills:\n  paths: ['']\n", "skills:\n  active: ['']\n", "skills: {paths: [a",
	} {
		dir := writeBase(t, body)
		if _, err := LoadSkills(dir); err == nil {
			t.Errorf("accepted %q", body)
		} else if !strings.Contains(err.Error(), "config.yaml") {
			t.Errorf("error does not name the file: %v", err)
		}
	}
}

func TestSaveSkillsKeepsEverythingElse(t *testing.T) {
	body := "# my config\npackages:\n  a: {script: x}  # keep\nskills:\n  # where to look\n  paths: [/a]\n  active: [x]\nother: 1\n"
	dir := writeBase(t, body)
	if err := SaveSkills(dir, Skills{Paths: []string{"/a", "/b"}, Active: []string{"x", "y"}}); err != nil {
		t.Fatal(err)
	}
	got := readFile(t, filepath.Join(dir, "config.yaml"))
	for _, want := range []string{"# my config", "a: {script: x} # keep", "other: 1", "- /b", "- y"} {
		if !strings.Contains(got, want) {
			t.Errorf("lost %q:\n%s", want, got)
		}
	}
	s, _ := LoadSkills(dir)
	if !reflect.DeepEqual(s, Skills{Paths: []string{"/a", "/b"}, Active: []string{"x", "y"}}) {
		t.Errorf("read back %v", s)
	}
	if st, _ := os.Stat(filepath.Join(dir, "config.yaml")); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode())
	}
}

func TestSaveSkillsKeepsPackagesIntact(t *testing.T) {
	dir := writeBase(t, "packages:\n  a: {script: x}\n")
	SaveSkills(dir, Skills{Paths: []string{"/a"}})
	c, err := Load(dir, nil)
	if err != nil || len(c.Base.Packages) != 1 {
		t.Errorf("packages damaged: %v %v", c, err)
	}
}

func TestSaveSkillsRefusesAFileItCannotParse(t *testing.T) {
	dir := writeBase(t, "skills: [a\n")
	if err := SaveSkills(dir, Skills{}); err == nil {
		t.Error("overwrote a broken file")
	}
	if got := readFile(t, filepath.Join(dir, "config.yaml")); got != "skills: [a\n" {
		t.Errorf("file changed: %q", got)
	}
}

func TestSaveSkillsRefusesAScalarSection(t *testing.T) {
	dir := writeBase(t, "skills: nope\n")
	if err := SaveSkills(dir, Skills{}); err == nil {
		t.Error("accepted")
	}
}

func TestSaveSkillsRoundTripsAwkwardNames(t *testing.T) {
	want := Skills{Paths: []string{"/a b", "~/x#y", "yes", "123"}, Active: []string{"true", "null", "a: b", "- c"}}
	dir := t.TempDir()
	if err := SaveSkills(dir, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSkills(dir)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %v %v", got, err)
	}
}

func TestSaveSkillsRefusesAnEmptyEntryAndLeavesTheFileAlone(t *testing.T) {
	dir := writeBase(t, "packages: {}\n")
	for _, s := range []Skills{{Paths: []string{""}}, {Active: []string{"a", ""}}} {
		if err := SaveSkills(dir, s); err == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	if got := readFile(t, filepath.Join(dir, "config.yaml")); got != "packages: {}\n" {
		t.Errorf("file changed: %q", got)
	}
}
