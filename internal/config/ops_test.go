package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func basePath(dir string) string { return filepath.Join(dir, "config.yaml") }

// setup writes config.yaml and, if runtime is non-empty, config.runtime.yaml.
func setup(t *testing.T, base, runtime string) (string, *Config) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, basePath(dir), base)
	if runtime != "" {
		writeFile(t, runtimePath(dir), runtime)
	}
	return dir, mustLoad(t, dir, nil)
}

func TestApplyPromoteAppendsRuntimeDefinitionToBaseKeepingItsComments(t *testing.T) {
	dir, c := setup(t,
		"# mine\nmodel: keep\npackages:\n  # first\n  a: {script: sa}\n",
		"packages:\n  a: {script: sa}\n  b: {script: sb, as: user, vars: {v: '2.10'}}\n")
	if err := c.Apply("b", Promote); err != nil {
		t.Fatal(err)
	}
	text := readFile(t, basePath(dir))
	for _, keep := range []string{"# mine", "model: keep", "# first"} {
		if !strings.Contains(text, keep) {
			t.Errorf("config.yaml lost %q:\n%s", keep, text)
		}
	}
	want := Package{Script: []string{"sb"}, As: AsUser, Vars: map[string]string{"v": "2.10"}}
	if got := mustLoad(t, dir, nil).Base.Packages["b"]; !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v want %#v", got, want)
	}
	if !reflect.DeepEqual(c.Base.Names, []string{"a", "b"}) {
		t.Errorf("in-memory base not updated: %v", c.Base.Names)
	}
	if d := c.Diff(); d.NeedsResolution() {
		t.Errorf("drift remains: %#v", d)
	}
}

func TestApplyPromoteIntoMissingOrEmptyBaseCreatesIt(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, runtimePath(dir), "packages:\n  b: {script: sb}\n")
	c := mustLoad(t, dir, nil)
	if err := c.Apply("b", Promote); err != nil {
		t.Fatal(err)
	}
	if got := mustLoad(t, dir, nil).Base.Names; !reflect.DeepEqual(got, []string{"b"}) {
		t.Errorf("got %v", got)
	}
}

func TestApplyDiscardRemovesFromRuntimeOnlyAndKeepsTheRest(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  a: {script: sa}\n",
		"# note\nmodel: keep\npackages:\n  a: {script: sa}\n  b: {script: sb}\n  c: {script: sc}\n")
	base := readFile(t, basePath(dir))
	if err := c.Apply("b", Discard); err != nil {
		t.Fatal(err)
	}
	r := mustLoad(t, dir, nil).Runtime
	if !reflect.DeepEqual(r.Names, []string{"a", "c"}) {
		t.Errorf("got %v", r.Names)
	}
	if text := readFile(t, runtimePath(dir)); !strings.Contains(text, "# note") || !strings.Contains(text, "model: keep") {
		t.Errorf("lost comments or settings:\n%s", text)
	}
	if readFile(t, basePath(dir)) != base {
		t.Error("config.yaml changed on discard")
	}
}

func TestApplyAdoptReplacesBaseDefinitionWithRuntimeOne(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  # keep this comment\n  x:\n    script: old\n    vars: {v: '1'}\n  y: {script: sy}\n",
		"packages:\n  x: {script: new}\n  y: {script: sy}\n")
	if err := c.Apply("x", Adopt); err != nil {
		t.Fatal(err)
	}
	b := mustLoad(t, dir, nil).Base
	if !reflect.DeepEqual(b.Packages["x"], Package{Script: []string{"new"}}) {
		t.Errorf("got %#v", b.Packages["x"])
	}
	if !reflect.DeepEqual(b.Names, []string{"x", "y"}) {
		t.Errorf("order changed: %v", b.Names)
	}
	if !strings.Contains(readFile(t, basePath(dir)), "# keep this comment") {
		t.Error("lost comment")
	}
}

func TestApplyRevertReplacesRuntimeDefinitionWithBaseOne(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  x: {script: base, vars: {v: '1'}}\n",
		"packages:\n  x: {script: edited}\n  z: {script: sz}\n")
	base := readFile(t, basePath(dir))
	if err := c.Apply("x", Revert); err != nil {
		t.Fatal(err)
	}
	r := mustLoad(t, dir, nil).Runtime
	if !reflect.DeepEqual(r.Packages["x"], mustLoad(t, dir, nil).Base.Packages["x"]) {
		t.Errorf("got %#v", r.Packages["x"])
	}
	if !reflect.DeepEqual(r.Names, []string{"x", "z"}) {
		t.Errorf("order or members changed: %v", r.Names)
	}
	if readFile(t, basePath(dir)) != base {
		t.Error("config.yaml changed on revert")
	}
}

func TestApplyRejectsActionsThatDoNotMatchTheDrift(t *testing.T) {
	cases := []struct {
		name string
		pkg  string
		act  Action
	}{
		{"promote a changed package", "x", Promote},
		{"discard a changed package", "x", Discard},
		{"adopt a runtime-only package", "r", Adopt},
		{"revert a runtime-only package", "r", Revert},
		{"promote a base-only package", "bo", Promote},
		{"promote an unchanged package", "same", Promote},
		{"adopt an unknown package", "ghost", Adopt},
		{"unknown action", "r", Action("explode")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, c := setup(t,
				"packages:\n  x: {script: base}\n  bo: {script: sbo}\n  same: {script: ss}\n",
				"packages:\n  x: {script: edited}\n  same: {script: ss}\n  r: {script: sr}\n")
			b, r := readFile(t, basePath(dir)), readFile(t, runtimePath(dir))
			if err := c.Apply(tc.pkg, tc.act); !errors.Is(err, ErrNotApplicable) {
				t.Errorf("want ErrNotApplicable, got %v", err)
			}
			if readFile(t, basePath(dir)) != b || readFile(t, runtimePath(dir)) != r {
				t.Error("a file changed")
			}
		})
	}
}

func TestApplyWithoutRuntimeFileIsNotApplicable(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: s}\n", "")
	if err := c.Apply("a", Promote); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("got %v", err)
	}
	if _, err := os.Stat(runtimePath(dir)); !os.IsNotExist(err) {
		t.Error("runtime file was created")
	}
}

func TestApplyPromoteRefusesPackageThatWouldBreakConfig(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  a: {script: sa}\n",
		"packages:\n  a: {script: sa}\n  b: {script: sb, needs: [ghost]}\n")
	base := readFile(t, basePath(dir))
	err := c.Apply("b", Promote)
	if err == nil || !strings.Contains(err.Error(), "ghost") {
		t.Errorf("want the problem named, got %v", err)
	}
	if readFile(t, basePath(dir)) != base {
		t.Error("config.yaml changed")
	}
}

func TestApplyDiscardRefusesWhenAnotherRuntimePackageNeedsIt(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  a: {script: sa}\n",
		"packages:\n  a: {script: sa}\n  dep: {script: sd}\n  user: {script: su, needs: [dep]}\n")
	r := readFile(t, runtimePath(dir))
	err := c.Apply("dep", Discard)
	if err == nil || !strings.Contains(err.Error(), "dep") {
		t.Errorf("want error naming dep, got %v", err)
	}
	if readFile(t, runtimePath(dir)) != r {
		t.Error("runtime file changed")
	}
}

func TestApplyReadsFilesFromDiskNotFromMemory(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  b: {script: sb}\n")
	// The user promotes b by hand after Load. A second promote must not duplicate it.
	writeFile(t, basePath(dir), "packages:\n  a: {script: sa}\n  b: {script: sb}\n")
	if err := c.Apply("b", Promote); !errors.Is(err, ErrNotApplicable) {
		t.Errorf("want ErrNotApplicable, got %v", err)
	}
	if strings.Count(readFile(t, basePath(dir)), "b:") != 1 {
		t.Error("b was written twice")
	}
}

func TestSettleDeletesRuntimeFileOnlyWhenNoDriftRemains(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: sa}\n  late: {script: sl}\n", "packages:\n  a: {script: sa}\n")
	settled, err := c.Settle() // only BaseOnly differs
	if err != nil || !settled {
		t.Fatalf("got %v, %v", settled, err)
	}
	if _, err := os.Stat(runtimePath(dir)); !os.IsNotExist(err) {
		t.Error("runtime file still exists")
	}
	if c.Runtime != nil {
		t.Error("in-memory runtime not cleared")
	}
}

func TestSettleKeepsRuntimeFileWhileDriftRemains(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n  b: {script: sb}\n")
	settled, err := c.Settle()
	if err != nil || settled {
		t.Fatalf("got %v, %v", settled, err)
	}
	if _, err := os.Stat(runtimePath(dir)); err != nil {
		t.Error("runtime file was removed")
	}
}

func TestSettleWithoutRuntimeFileIsANoOp(t *testing.T) {
	_, c := setup(t, "packages:\n  a: {script: sa}\n", "")
	if settled, err := c.Settle(); err != nil || settled {
		t.Errorf("got %v, %v", settled, err)
	}
}

func TestSettleChecksDiskNotMemory(t *testing.T) {
	dir, c := setup(t, "packages:\n  a: {script: sa}\n", "packages:\n  a: {script: sa}\n")
	// Another writer adds drift after Load; Settle must see it.
	writeFile(t, runtimePath(dir), "packages:\n  a: {script: sa}\n  b: {script: sb}\n")
	if settled, _ := c.Settle(); settled {
		t.Error("settled although drift appeared on disk")
	}
	if _, err := os.Stat(runtimePath(dir)); err != nil {
		t.Error("runtime file removed")
	}
}

func TestFullResolutionFlowEndsWithFreshCopyOnNextEdit(t *testing.T) {
	dir, c := setup(t,
		"packages:\n  a: {script: sa}\n  x: {script: base}\n",
		"packages:\n  a: {script: sa}\n  x: {script: edited}\n  r1: {script: s1}\n  r2: {script: s2}\n")
	for name, act := range map[string]Action{"x": Revert, "r1": Promote, "r2": Discard} {
		if err := c.Apply(name, act); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	if settled, err := c.Settle(); err != nil || !settled {
		t.Fatalf("settle: %v, %v", settled, err)
	}
	mustAdd(t, mustLoad(t, dir, nil), "n", Package{Script: []string{"sn"}})
	got := mustLoad(t, dir, nil)
	if want := []string{"a", "x", "r1", "n"}; !reflect.DeepEqual(got.Runtime.Names, want) {
		t.Errorf("fresh copy should be config.yaml plus the new edit: got %v want %v", got.Runtime.Names, want)
	}
	if d := got.Diff(); !reflect.DeepEqual(d.RuntimeOnly, []string{"n"}) || len(d.Changed) != 0 {
		t.Errorf("diff: %#v", d)
	}
}

func TestPackageYAMLShowsTheDefinition(t *testing.T) {
	p := Package{Script: []string{"echo hi\n"}, As: AsUser, Needs: []string{"uv"}, Vars: map[string]string{"v": "2.10"}}
	got := p.YAML()
	for _, want := range []string{"script: |", "echo hi", "as: user", "needs: uv", `v: "2.10"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(Package{}.YAML(), "script") {
		t.Error("empty package should print no fields")
	}
}
