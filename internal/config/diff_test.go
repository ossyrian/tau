package config

import (
	"path/filepath"
	"reflect"
	"testing"
)

func compare(t *testing.T, base, runtime string) Diff {
	t.Helper()
	return Compare(mustParse(t, base), mustParse(t, runtime))
}

func TestCompareNilRuntimeIsNoDrift(t *testing.T) {
	// With no runtime file there is nothing to compare, so base-only names are not "additions".
	d := Compare(mustParse(t, "packages:\n  a: {script: s}\n"), nil)
	if !reflect.DeepEqual(d, Diff{}) {
		t.Errorf("got %#v", d)
	}
}

func TestCompareRuntimeOnlyPackageNeedsResolution(t *testing.T) {
	d := compare(t, "packages:\n  a: {script: s}\n", "packages:\n  a: {script: s}\n  b: {script: t}\n")
	if !reflect.DeepEqual(d.RuntimeOnly, []string{"b"}) || len(d.Changed) != 0 || len(d.BaseOnly) != 0 {
		t.Errorf("got %#v", d)
	}
	if !d.NeedsResolution() {
		t.Error("a runtime-only package must need resolution")
	}
}

func TestCompareBaseOnlyPackageIsAnAdditionNotAConflict(t *testing.T) {
	d := compare(t, "packages:\n  a: {script: s}\n  late: {script: t}\n", "packages:\n  a: {script: s}\n")
	if !reflect.DeepEqual(d.BaseOnly, []string{"late"}) || len(d.RuntimeOnly) != 0 || len(d.Changed) != 0 {
		t.Errorf("got %#v", d)
	}
	if d.NeedsResolution() {
		t.Error("a package that is only in config.yaml is intended; it must not need resolution")
	}
}

func TestCompareChangedDefinitionNeedsResolution(t *testing.T) {
	cases := map[string][2]string{
		"script":        {"x: {script: one}", "x: {script: two}"},
		"apt":           {"x: {apt: [a]}", "x: {apt: [a, b]}"},
		"as":            {"x: {script: s, as: root}", "x: {script: s, as: user}"},
		"needs":         {"x: {script: s, needs: [a]}", "x: {script: s, needs: [b]}"},
		"check":         {"x: {script: s}", "x: {script: s, check: c}"},
		"env":           {"x: {script: s, env: {E: '1'}}", "x: {script: s, env: {E: '2'}}"},
		"vars":          {"x: {script: s, vars: {v: '1'}}", "x: {script: s, vars: {v: '2'}}"},
		"script to apt": {"x: {script: s}", "x: {apt: [s]}"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := compare(t, "packages:\n  "+c[0]+"\n", "packages:\n  "+c[1]+"\n")
			if !reflect.DeepEqual(d.Changed, []string{"x"}) || !d.NeedsResolution() {
				t.Errorf("got %#v", d)
			}
		})
	}
}

func TestCompareTreatsEquivalentSpellingsAsEqual(t *testing.T) {
	cases := map[string][2]string{
		"default as":        {"x: {script: s}", "x: {script: s, as: root}"},
		"empty vars":        {"x: {script: s}", "x: {script: s, vars: {}}"},
		"empty needs":       {"x: {script: s}", "x: {script: s, needs: []}"},
		"string vs list":    {"x: {script: s}", "x: {script: [s]}"},
		"key order":         {"x: {script: s, check: c}", "x: {check: c, script: s}"},
		"var order":         {"x: {script: s, vars: {a: '1', b: '2'}}", "x: {script: s, vars: {b: '2', a: '1'}}"},
		"whitespace layout": {"x:\n    script: s", "x: {script: s}"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			d := compare(t, "packages:\n  "+c[0]+"\n", "packages:\n  "+c[1]+"\n")
			if !reflect.DeepEqual(d, Diff{}) {
				t.Errorf("got %#v", d)
			}
		})
	}
}

func TestCompareIgnoresCommentsAndOrderOfPackages(t *testing.T) {
	base := "# mine\npackages:\n  a: {script: s}\n  b: {script: t}\n"
	runtime := "packages:\n  b: {script: t} # note\n  a: {script: s}\n"
	if d := compare(t, base, runtime); !reflect.DeepEqual(d, Diff{}) {
		t.Errorf("got %#v", d)
	}
}

func TestCompareReportsAllThreeKindsInDeclarationOrder(t *testing.T) {
	base := "packages:\n  keep: {script: k}\n  chg1: {script: a}\n  baseonly2: {script: x}\n  chg2: {script: a}\n  baseonly1: {script: x}\n"
	runtime := "packages:\n  rt2: {script: r}\n  chg2: {script: b}\n  keep: {script: k}\n  rt1: {script: r}\n  chg1: {script: b}\n"
	d := compare(t, base, runtime)
	want := Diff{
		RuntimeOnly: []string{"rt2", "rt1"},             // runtime order
		Changed:     []string{"chg2", "chg1"},           // runtime order
		BaseOnly:    []string{"baseonly2", "baseonly1"}, // base order
	}
	if !reflect.DeepEqual(d, want) {
		t.Errorf("got  %#v\nwant %#v", d, want)
	}
}

func TestConfigDiffUsesFilesOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: s}\n")
	c := mustLoad(t, dir, nil)
	if d := c.Diff(); !reflect.DeepEqual(d, Diff{}) {
		t.Errorf("before any runtime edit: %#v", d)
	}
	mustAdd(t, c, "b", Package{Script: []string{"t"}})
	if d := c.Diff(); !reflect.DeepEqual(d.RuntimeOnly, []string{"b"}) {
		t.Errorf("after AddRuntime: %#v", d)
	}
	writeFile(t, filepath.Join(dir, "config.yaml"), "packages:\n  a: {script: s}\n  late: {script: l}\n  b: {script: t}\n")
	d := mustLoad(t, dir, nil).Diff()
	if !reflect.DeepEqual(d.BaseOnly, []string{"late"}) || len(d.RuntimeOnly) != 0 {
		t.Errorf("after the user promoted b and added late: %#v", d)
	}
}
