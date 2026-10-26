package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspacesDefaultToIsolated(t *testing.T) {
	for name, body := range map[string]string{
		"no file": "", "no section": "packages: {}\n", "null section": "workspaces:\n", "no mode": "workspaces: {}\n",
	} {
		dir := t.TempDir()
		if body != "" {
			os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(body), 0o644)
		}
		w, err := LoadWorkspaces(dir)
		if err != nil || w.Mode != WorkspaceIsolated {
			t.Errorf("%s: got %+v, %v", name, w, err)
		}
	}
}

func TestWorkspacesRejectAnUnknownMode(t *testing.T) {
	dir := writeBase(t, "workspaces:\n  mode: sometimes\n")
	_, err := LoadWorkspaces(dir)
	if err == nil || !strings.Contains(err.Error(), `"sometimes"`) || !strings.Contains(err.Error(), "shared") {
		t.Errorf("got %v", err)
	}
}

func TestWorkspacesRejectUnknownKeys(t *testing.T) {
	dir := writeBase(t, "workspaces:\n  mod: shared\n")
	if _, err := LoadWorkspaces(dir); err == nil {
		t.Error("typo accepted")
	}
}

func TestSaveWorkspacesKeepsTheRestOfTheFile(t *testing.T) {
	dir := writeBase(t, "# my config\npackages:\n  a: {script: echo a}  # keep\n")
	if err := SaveWorkspaces(dir, Workspaces{Mode: WorkspaceShared}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	s := string(data)
	for _, want := range []string{"# my config", "# keep", "workspaces:", "mode: shared", "a:"} {
		if !strings.Contains(s, want) {
			t.Errorf("lost %q:\n%s", want, s)
		}
	}
	w, err := LoadWorkspaces(dir)
	if err != nil || w.Mode != WorkspaceShared {
		t.Errorf("round trip: %+v %v", w, err)
	}
	if err := SaveWorkspaces(dir, Workspaces{Mode: WorkspaceIsolated}); err != nil {
		t.Fatal(err)
	}
	if w, _ := LoadWorkspaces(dir); w.Mode != WorkspaceIsolated {
		t.Errorf("second save: %+v", w)
	}
}

func TestSaveWorkspacesRefusesABadMode(t *testing.T) {
	dir := writeBase(t, "")
	if err := SaveWorkspaces(dir, Workspaces{Mode: "x"}); err == nil {
		t.Error("bad mode written")
	}
}

func TestLiveWorkspacesAreReadFromTheConfig(t *testing.T) {
	dir := writeBase(t, "workspaces:\n  mode: isolated\n  live:\n    proj: /src/proj\n    app: ~/dev/app\n")
	w, err := LoadWorkspaces(dir)
	if err != nil {
		t.Fatal(err)
	}
	if w.Live["proj"] != "/src/proj" || w.Live["app"] != "~/dev/app" || len(w.Live) != 2 {
		t.Errorf("got %+v", w.Live)
	}
	if got := w.LiveNames(); strings.Join(got, ",") != "app,proj" {
		t.Errorf("names %v", got)
	}
}

func TestLiveWorkspacesRejectBadEntries(t *testing.T) {
	for name, body := range map[string]string{
		"unsafe name":     "workspaces:\n  live:\n    '../x': /src\n",
		"slash in name":   "workspaces:\n  live:\n    'a/b': /src\n",
		"dot dot":         "workspaces:\n  live:\n    '..': /src\n",
		"leading dot":     "workspaces:\n  live:\n    '.hidden': /src\n",
		"space in name":   "workspaces:\n  live:\n    'a b': /src\n",
		"empty source":    "workspaces:\n  live:\n    a: ''\n",
		"relative source": "workspaces:\n  live:\n    a: src/a\n",
		"not a mapping":   "workspaces:\n  live: [a]\n",
		"source not text": "workspaces:\n  live:\n    a: [x]\n",
		"unknown key":     "workspaces:\n  lives: {}\n",
	} {
		if _, err := LoadWorkspaces(writeBase(t, body)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestAddAndRemoveLiveKeepTheModeAndTheRestOfTheFile(t *testing.T) {
	dir := writeBase(t, "# mine\nworkspaces:\n  mode: shared  # keep\npackages:\n  a: {script: echo a}\n")
	if err := AddLive(dir, "proj", "/src/proj"); err != nil {
		t.Fatal(err)
	}
	if err := AddLive(dir, "app", "~/dev/app"); err != nil {
		t.Fatal(err)
	}
	w, err := LoadWorkspaces(dir)
	if err != nil || w.Mode != WorkspaceShared || w.Live["proj"] != "/src/proj" || w.Live["app"] != "~/dev/app" {
		t.Fatalf("got %+v %v", w, err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.yaml"))
	for _, want := range []string{"# mine", "# keep", "a: {script: echo a}"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("lost %q:\n%s", want, data)
		}
	}
	removed, err := RemoveLive(dir, "proj")
	if err != nil || !removed {
		t.Fatalf("remove: %v %v", removed, err)
	}
	if w, _ = LoadWorkspaces(dir); len(w.Live) != 1 || w.Live["app"] == "" {
		t.Errorf("after remove: %+v", w.Live)
	}
	if removed, _ = RemoveLive(dir, "proj"); removed {
		t.Error("removed twice")
	}
}

func TestAddLiveCreatesTheSectionAndRefusesABadEntry(t *testing.T) {
	dir := t.TempDir()
	if err := AddLive(dir, "proj", "/src/proj"); err != nil {
		t.Fatal(err)
	}
	if w, _ := LoadWorkspaces(dir); w.Live["proj"] != "/src/proj" || w.Mode != WorkspaceIsolated {
		t.Errorf("got %+v", w)
	}
	for _, c := range [][2]string{{"../x", "/s"}, {"a", "rel"}, {"", "/s"}} {
		if err := AddLive(dir, c[0], c[1]); err == nil {
			t.Errorf("AddLive(%q, %q) accepted", c[0], c[1])
		}
	}
}

func TestSaveWorkspacesModeKeepsTheLiveEntries(t *testing.T) {
	dir := writeBase(t, "workspaces:\n  live:\n    proj: /src/proj\n")
	if err := SaveWorkspaces(dir, Workspaces{Mode: WorkspaceShared}); err != nil {
		t.Fatal(err)
	}
	w, _ := LoadWorkspaces(dir)
	if w.Mode != WorkspaceShared || w.Live["proj"] != "/src/proj" {
		t.Errorf("got %+v", w)
	}
}

func TestRemoveLiveOfTheLastEntryLeavesAValidFile(t *testing.T) {
	dir := writeBase(t, "workspaces:\n  live:\n    proj: /src/proj\n")
	if _, err := RemoveLive(dir, "proj"); err != nil {
		t.Fatal(err)
	}
	if w, err := LoadWorkspaces(dir); err != nil || len(w.Live) != 0 {
		t.Errorf("got %+v %v", w, err)
	}
}
