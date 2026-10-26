package config

import (
	"reflect"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Layer {
	t.Helper()
	l, err := Parse("test.yaml", []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return l
}

func names(entries []Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}

func mustResolve(t *testing.T, user, catalog *Layer) []Entry {
	t.Helper()
	entries, err := Resolve(user, catalog)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return entries
}

// --- Parse ---------------------------------------------------------------

func TestParseKeepsDeclarationOrder(t *testing.T) {
	l := mustParse(t, "packages:\n  zeta:\n  alpha:\n  mid:\n")
	if want := []string{"zeta", "alpha", "mid"}; !reflect.DeepEqual(l.Names, want) {
		t.Errorf("got %v want %v", l.Names, want)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name, src, want string
	}{
		{"old run field", "packages:\n  x:\n    run: echo\n", `unknown field "run"`},
		{"unknown field", "packages:\n  x:\n    bogus: 1\n", `unknown field "bogus"`},
		{"script and apt", "packages:\n  x:\n    script: a\n    apt: [b]\n", "script or apt, not both"},
		{"duplicate name", "packages:\n  x: {script: a}\n  x: {script: b}\n", "defined twice"},
		{"bad name", "packages:\n  Bad_Name: {script: a}\n", "invalid package name"},
		{"path in name", "packages:\n  ../x: {script: a}\n", "invalid package name"},
		{"packages not a map", "packages: [a]\n", "must be a mapping"},
		{"package not a map", "packages:\n  x: [a]\n", "must be a mapping"},
		{"as not string", "packages:\n  x:\n    as: [root]\n", "as must be a string"},
		{"var not scalar", "packages:\n  x:\n    vars: { a: [1] }\n", "vars.a must be"},
		{"var null", "packages:\n  x:\n    vars: { a: }\n", "vars.a must be"},
		{"bad var name", "packages:\n  x:\n    vars: { 1a: b }\n", "invalid vars name"},
		{"bad env name", "packages:\n  x:\n    env: { 'A B': c }\n", "invalid env name"},
		{"top not map", "- a\n", "top level must be a mapping"},
		{"yaml syntax", "packages: [\n", "test.yaml"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("test.yaml", []byte(tc.src))
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestParseErrorNamesFileAndLine(t *testing.T) {
	_, err := Parse("config.yaml", []byte("packages:\n  a:\n    script: x\n  b:\n    bogus: 1\n"))
	if err == nil || !strings.HasPrefix(err.Error(), "config.yaml:5:") {
		t.Errorf("want prefix config.yaml:5:, got %v", err)
	}
}

// --- Resolve -------------------------------------------------------------

func TestResolveInstallsOnlyListedPackages(t *testing.T) {
	catalog := mustParse(t, "packages:\n  aws: {script: a}\n  gh: {script: b}\n")
	user := mustParse(t, "packages:\n  gh:\n")
	got := mustResolve(t, user, catalog)
	if want := []string{"gh"}; !reflect.DeepEqual(names(got), want) {
		t.Errorf("got %v want %v", names(got), want)
	}
}

func TestResolveEmptyBodyTakesCatalogDefinition(t *testing.T) {
	catalog := mustParse(t, "packages:\n  gh:\n    script: install-gh\n    check: gh --version\n")
	got := mustResolve(t, mustParse(t, "packages:\n  gh:\n"), catalog)[0]
	if !reflect.DeepEqual(got.Script, []string{"install-gh"}) || got.Check != "gh --version" {
		t.Errorf("got %#v", got.Package)
	}
}

func TestResolveUserVarsOverrideCatalogVars(t *testing.T) {
	catalog := mustParse(t, "packages:\n  linear:\n    script: s\n    vars: {version: '2.6.0', arch: x}\n")
	user := mustParse(t, "packages:\n  linear:\n    vars: {version: '3.0.0'}\n")
	got := mustResolve(t, user, catalog)[0]
	if want := map[string]string{"version": "3.0.0", "arch": "x"}; !reflect.DeepEqual(got.Vars, want) {
		t.Errorf("got %v want %v", got.Vars, want)
	}
}

func TestResolveUserScriptReplacesCatalogApt(t *testing.T) {
	catalog := mustParse(t, "packages:\n  pg:\n    apt: [postgresql-client]\n")
	user := mustParse(t, "packages:\n  pg:\n    script: my-install\n")
	got := mustResolve(t, user, catalog)[0]
	if got.Apt != nil || !reflect.DeepEqual(got.Script, []string{"my-install"}) {
		t.Errorf("got %#v", got.Package)
	}
}

func TestResolveDefaultsAsToRoot(t *testing.T) {
	got := mustResolve(t, mustParse(t, "packages:\n  x: {script: a}\n  y: {script: a, as: user}\n"), nil)
	if got[0].As != AsRoot || got[1].As != AsUser {
		t.Errorf("got %q and %q", got[0].As, got[1].As)
	}
}

func TestResolveNeedsComeFirst(t *testing.T) {
	user := mustParse(t, `
packages:
  pre-commit: {script: a, needs: [uv]}
  uv: {script: a}
  other: {script: a}
`)
	if want := []string{"uv", "pre-commit", "other"}; !reflect.DeepEqual(names(mustResolve(t, user, nil)), want) {
		t.Errorf("got %v want %v", names(mustResolve(t, user, nil)), want)
	}
}

func TestResolvePullsUnlistedNeedFromCatalog(t *testing.T) {
	catalog := mustParse(t, "packages:\n  uv: {script: install-uv}\n")
	user := mustParse(t, "packages:\n  pre-commit: {script: a, needs: [uv]}\n")
	got := mustResolve(t, user, catalog)
	if want := []string{"uv", "pre-commit"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("got %v want %v", names(got), want)
	}
	if !reflect.DeepEqual(got[0].Script, []string{"install-uv"}) {
		t.Errorf("uv should come from the catalog: %#v", got[0].Package)
	}
}

func TestResolveErrors(t *testing.T) {
	cases := []struct {
		name, user, catalog, want string
	}{
		{"missing need", "packages:\n  a: {script: s, needs: [ghost]}\n", "", `needs "ghost"`},
		{"unknown package with no body", "packages:\n  nothing:\n", "", "has no script or apt"},
		{"cycle", "packages:\n  a: {script: s, needs: [b]}\n  b: {script: s, needs: [a]}\n", "", "a -> b -> a"},
		{"self cycle", "packages:\n  a: {script: s, needs: [a]}\n", "", "a -> a"},
		{"bad as", "packages:\n  a: {script: s, as: admin}\n", "", "as must be"},
		{"apt as user", "packages:\n  a: {apt: [x], as: user}\n", "", "apt installs need as: root"},
		{"apt injection", "packages:\n  a: {apt: ['x; rm -rf /']}\n", "", "invalid apt package name"},
		{"empty script entry", "packages:\n  a: {script: ''}\n", "", "empty entry"},
		{"bad need name", "packages:\n  a: {script: s, needs: ['../x']}\n", "", "invalid package name"},
		{"bad catalog entry used", "packages:\n  a:\n", "packages:\n  a: {as: nope, script: s}\n", "as must be"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var catalog *Layer
			if tc.catalog != "" {
				catalog = mustParse(t, tc.catalog)
			}
			_, err := Resolve(mustParse(t, tc.user), catalog)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
		})
	}
}

func TestResolveUnusedBrokenCatalogEntryIsIgnored(t *testing.T) {
	// A bad catalog entry only matters if the user installs it.
	catalog := mustParse(t, "packages:\n  broken: {as: nope}\n  ok: {script: s}\n")
	user := mustParse(t, "packages:\n  ok:\n")
	if _, err := Resolve(user, catalog); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
