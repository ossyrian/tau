package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustLoad(t *testing.T, dir string, catalog fstest.MapFS) *Config {
	t.Helper()
	c, err := Load(dir, catalog)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return c
}

func TestLoadResolvesFromBaseOnly(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: sa}\n")
	c := mustLoad(t, dir, nil)
	if c.Runtime != nil {
		t.Error("Runtime should be nil when config.runtime.yaml does not exist")
	}
	got, err := c.Resolve()
	if err != nil || !reflect.DeepEqual(names(got), []string{"a"}) {
		t.Errorf("got %v, %v", names(got), err)
	}
}

func TestLoadErrorNamesTheFile(t *testing.T) {
	for _, file := range []string{"config.yaml", "config.runtime.yaml"} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, file), "packages:\n  x:\n    bogus: 1\n")
		_, err := Load(dir, nil)
		if err == nil || !strings.Contains(err.Error(), filepath.Join(dir, file)) {
			t.Errorf("%s: want error naming the full path, got %v", file, err)
		}
	}
}

func TestLoadCatalogReadsYAMLOnlyAndSkipsDirs(t *testing.T) {
	catalog := fstest.MapFS{
		"b.yaml":         {Data: []byte("packages:\n  b: {script: s}\n")},
		"a.yaml":         {Data: []byte("packages:\n  a: {script: s}\n")},
		"README.md":      {Data: []byte("not yaml: [")},
		".gitkeep":       {Data: nil},
		"sub/deep.yaml":  {Data: []byte("packages:\n  deep: {script: s}\n")},
		"notes.yaml.bak": {Data: []byte("not yaml: [")},
	}
	l, err := LoadCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a", "b"}; !reflect.DeepEqual(l.Names, want) {
		t.Errorf("got %v want %v", l.Names, want)
	}
}

func TestLoadCatalogDuplicateNameAcrossFilesIsAnError(t *testing.T) {
	catalog := fstest.MapFS{
		"one.yaml": {Data: []byte("packages:\n  x: {script: s}\n")},
		"two.yaml": {Data: []byte("packages:\n  x: {script: t}\n")},
	}
	_, err := LoadCatalog(catalog)
	if err == nil || !strings.Contains(err.Error(), "one.yaml") || !strings.Contains(err.Error(), "two.yaml") {
		t.Errorf("want error naming both files, got %v", err)
	}
}

func TestLoadCatalogParseErrorNamesTheFile(t *testing.T) {
	_, err := LoadCatalog(fstest.MapFS{"bad.yaml": {Data: []byte("packages:\n  x:\n    bogus: 1\n")}})
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("got %v", err)
	}
}
