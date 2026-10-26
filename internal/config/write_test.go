package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func mustAdd(t *testing.T, c *Config, name string, p Package) {
	t.Helper()
	if err := c.AddRuntime(name, p); err != nil {
		t.Fatalf("AddRuntime(%q): %v", name, err)
	}
}

func runtimePath(dir string) string { return filepath.Join(dir, "config.runtime.yaml") }

func TestAddRuntimeCreatesFileAndDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "fresh", "home")
	want := Package{
		Script: []string{"echo hi\n"},
		As:     AsUser,
		Needs:  []string{"uv"},
		Check:  "linear --version",
		Env:    map[string]string{"E": "1"},
		Vars:   map[string]string{"version": "2.10"},
	}
	c := mustLoad(t, dir, fstest.MapFS{"uv.yaml": {Data: []byte("packages:\n  uv: {script: s}\n")}})
	mustAdd(t, c, "linear", want)

	reloaded := mustLoad(t, dir, nil)
	if got := reloaded.Runtime.Packages["linear"]; !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}
	info, err := os.Stat(runtimePath(dir))
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("want a 0644 file, got %v, %v", info, err)
	}
}

func TestAddRuntimeHandlesEmptyOrNullPackagesKey(t *testing.T) {
	for _, src := range []string{"", "packages:\n", "packages: {}\n", "packages: ~\n", "model: x\n"} {
		dir := t.TempDir()
		writeFile(t, runtimePath(dir), src)
		mustAdd(t, mustLoad(t, dir, nil), "a", Package{Script: []string{"x"}})
		if got := mustLoad(t, dir, nil).Runtime.Names; !reflect.DeepEqual(got, []string{"a"}) {
			t.Errorf("%q: got %v", src, got)
		}
	}
}

func TestAddRuntimeCatalogNameWithEmptyBody(t *testing.T) {
	dir := t.TempDir()
	catalog := fstest.MapFS{"gh.yaml": {Data: []byte("packages:\n  gh: {script: install-gh}\n")}}
	c := mustLoad(t, dir, catalog)
	mustAdd(t, c, "gh", Package{})

	if !reflect.DeepEqual(mustLoad(t, dir, catalog).Runtime.Names, []string{"gh"}) {
		t.Error("entry not persisted")
	}
	// It is drift until the user moves it into config.yaml; once there, the
	// empty body resolves from the catalog.
	if d := c.Diff(); !reflect.DeepEqual(d.RuntimeOnly, []string{"gh"}) {
		t.Errorf("diff: %#v", d)
	}
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  gh:\n")
	got, err := mustLoad(t, dir, catalog).Resolve()
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[0].Script, []string{"install-gh"}) {
		t.Errorf("want gh resolved from the catalog, got %v, %v", got, err)
	}
}

func TestAddRuntimeFirstEditSeedsFromBaselineAndLeavesItUntouched(t *testing.T) {
	dir := t.TempDir()
	base := "# my setup\nmodel: keepme\npackages:\n  # needed everywhere\n  a:\n    script: echo a\n"
	writeFile(t, filepath.Join(dir, "config.yaml"), base)
	mustAdd(t, mustLoad(t, dir, nil), "b", Package{Script: []string{"echo b"}})

	if readFile(t, filepath.Join(dir, "config.yaml")) != base {
		t.Error("config.yaml was modified")
	}
	text := readFile(t, runtimePath(dir))
	for _, keep := range []string{"# my setup", "model: keepme", "# needed everywhere"} {
		if !strings.Contains(text, keep) {
			t.Errorf("runtime copy lost %q:\n%s", keep, text)
		}
	}
	if got := mustLoad(t, dir, nil).Runtime.Names; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("runtime copy should hold baseline entries plus the addition, got %v", got)
	}
}

func TestAddRuntimeFirstEditRejectsNameInBaseline(t *testing.T) {
	dir := t.TempDir()
	base := "packages:\n  x: {script: mine}\n"
	writeFile(t, filepath.Join(dir, "config.yaml"), base)
	err := mustLoad(t, dir, nil).AddRuntime("x", Package{Script: []string{"agent-wrote-this"}})
	if !errors.Is(err, ErrExists) || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("want ErrExists naming config.yaml, got %v", err)
	}
	if _, statErr := os.Stat(runtimePath(dir)); !os.IsNotExist(statErr) {
		t.Error("runtime file was created")
	}
	if readFile(t, filepath.Join(dir, "config.yaml")) != base {
		t.Error("config.yaml changed")
	}
}

func TestAddRuntimeLaterBaselineChangesAreNotCopiedInButAreReported(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: sa}\n")
	c := mustLoad(t, dir, nil)
	mustAdd(t, c, "b", Package{Script: []string{"sb"}})
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: sa}\n  late: {script: sl}\n")
	mustAdd(t, c, "c", Package{Script: []string{"sc"}})

	reloaded := mustLoad(t, dir, nil)
	if want := []string{"a", "b", "c"}; !reflect.DeepEqual(reloaded.Runtime.Names, want) {
		t.Errorf("runtime copy: got %v want %v", reloaded.Runtime.Names, want)
	}
	if d := reloaded.Diff(); !reflect.DeepEqual(d.BaseOnly, []string{"late"}) || !reflect.DeepEqual(d.RuntimeOnly, []string{"b", "c"}) {
		t.Errorf("diff: %#v", d)
	}
}

func TestAddRuntimeSeesEditsMadeAfterLoad(t *testing.T) {
	dir := t.TempDir()
	c := mustLoad(t, dir, nil) // loaded while no files exist

	// The user writes the baseline after Load. The first edit must copy it
	// and must reject a name it defines.
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  x: {script: mine}\n")
	if err := c.AddRuntime("x", Package{Script: []string{"agent"}}); !errors.Is(err, ErrExists) {
		t.Errorf("baseline written after Load was ignored: %v", err)
	}
	mustAdd(t, c, "w", Package{Script: []string{"w"}})
	if got := mustLoad(t, dir, nil).Runtime.Names; !reflect.DeepEqual(got, []string{"x", "w"}) {
		t.Errorf("first edit did not copy the baseline written after Load: %v", got)
	}

	// Another writer changes the runtime file. The next edit must keep that change.
	writeFile(t, runtimePath(dir), "packages:\n  y: {script: other}\n")
	mustAdd(t, c, "z", Package{Script: []string{"z"}})
	if got := mustLoad(t, dir, nil).Runtime.Names; !reflect.DeepEqual(got, []string{"y", "z"}) {
		t.Errorf("an edit made after Load was lost: %v", got)
	}
}

func TestAddRuntimeRefusesWhenExistingConfigIsAlreadyBroken(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: s, needs: [ghost]}\n")
	err := mustLoad(t, dir, nil).AddRuntime("b", Package{Script: []string{"x"}})
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("want the existing problem reported, got %v", err)
	}
	if _, statErr := os.Stat(runtimePath(dir)); !os.IsNotExist(statErr) {
		t.Error("file was created")
	}
}

func TestAddRuntimeRejectsInvalidInputsWithoutWriting(t *testing.T) {
	cases := []struct {
		name string
		pkg  Package
		pkgn string
		want string
	}{
		{"bad name", Package{Script: []string{"x"}}, "Bad Name", "invalid package name"},
		{"path name", Package{Script: []string{"x"}}, "../evil", "invalid package name"},
		{"no body, not in catalog", Package{}, "ghost", "no script or apt"},
		{"script and apt", Package{Script: []string{"x"}, Apt: []string{"y"}}, "both", "not both"},
		{"bad as", Package{Script: []string{"x"}, As: "admin"}, "a", "as must be"},
		{"unresolved need", Package{Script: []string{"x"}, Needs: []string{"ghost"}}, "a", "ghost"},
		{"apt injection", Package{Apt: []string{"x; id"}}, "a", "invalid apt package name"},
		{"bad env key", Package{Script: []string{"x"}, Env: map[string]string{"A B": "c"}}, "a", "invalid env name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			err := mustLoad(t, dir, nil).AddRuntime(tc.pkgn, tc.pkg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
			if _, statErr := os.Stat(runtimePath(dir)); !os.IsNotExist(statErr) {
				t.Error("file was created")
			}
		})
	}
}

func TestAddRuntimeRefusesUnexpectedShapeWithoutChangingFile(t *testing.T) {
	for _, src := range []string{"packages: [a]\n", "packages: {a: [b]}\n", "- a\n"} {
		dir := t.TempDir()
		writeFile(t, runtimePath(dir), src)
		c, err := Load(dir, nil)
		if err == nil {
			err = c.AddRuntime("n", Package{Script: []string{"x"}})
		}
		if err == nil {
			t.Errorf("%q: want an error", src)
		}
		if readFile(t, runtimePath(dir)) != src {
			t.Errorf("%q: file changed", src)
		}
	}
}

func TestAddRuntimeLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	c := mustLoad(t, dir, nil)
	mustAdd(t, c, "a", Package{Script: []string{"x"}})
	_ = c.AddRuntime("a", Package{Script: []string{"x"}}) // rejected
	_ = c.AddRuntime("b", Package{})                      // rejected
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "config.runtime.yaml" {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Errorf("unexpected files: %v", got)
	}
}

// Values that YAML would read as another type, or that need special quoting or
// block-scalar handling, must come back byte-for-byte.
var awkwardStrings = []string{
	"plain", "multi\nline\n", "no trailing newline\nsecond", "\n\nstarts blank",
	"  leading spaces", "trailing spaces  ", "line with trailing space \nnext\n",
	"tab\tinside", "\ttab first", "# not a comment", "key: value", "- dash", "'single'",
	`"double"`, "unicode ✓ é 日本", "2.10", "1e3", "true", "yes", "null", "~", "007", "0x1F",
	"---", "...", "a: b\n  c", `back\slash`, "@at", "%pct", "!bang", "*star", "&amp",
	"{brace", "[bracket", "? q", "| pipe", "> fold", "\r\n", "cr\rinside", "   ",
}

func TestAddRuntimeRoundTripsAwkwardStrings(t *testing.T) {
	for _, s := range awkwardStrings {
		dir := t.TempDir()
		want := Package{
			Script: []string{s, "second"},
			Check:  s,
			Env:    map[string]string{"E": s},
			Vars:   map[string]string{"V": s},
		}
		if err := mustLoad(t, dir, nil).AddRuntime("p", want); err != nil {
			t.Errorf("%q: AddRuntime: %v", s, err)
			continue
		}
		got := mustLoad(t, dir, nil).Runtime.Packages["p"]
		if got.As != "" {
			got.As = "" // Resolve sets the default; the file stores none
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%q:\n got %#v\nwant %#v", s, got, want)
		}
	}
}

func TestAddRuntimeWritesMultilineScriptAsReadableBlock(t *testing.T) {
	dir := t.TempDir()
	mustAdd(t, mustLoad(t, dir, nil), "p", Package{Script: []string{"set -e\ncurl -fsSL \"$url\" | tar xz\n"}})
	text := readFile(t, runtimePath(dir))
	if !strings.Contains(text, "script: |\n") || !strings.Contains(text, "      curl -fsSL \"$url\" | tar xz\n") {
		t.Errorf("want a literal block with the script lines readable:\n%s", text)
	}
}
