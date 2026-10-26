package build

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ossyrian/tau/internal/config"
)

func entry(name string, p config.Package) config.Entry {
	if p.As == "" {
		p.As = config.AsRoot
	}
	return config.Entry{Name: name, Package: p}
}

// Render renders entries for the default profile alone.
func Render(entries []config.Entry) (string, error) {
	var b strings.Builder
	if err := renderFor(&b, "pi", MarkerDir, entries); err != nil {
		return "", err
	}
	b.WriteString("USER pi\n")
	return b.String(), nil
}

func mustRender(t *testing.T, entries ...config.Entry) string {
	t.Helper()
	out, err := Render(entries)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return out
}

var heredocStart = regexp.MustCompile(`<<'([A-Z0-9_]+)'\s*$`)

// instructions lists the Dockerfile instructions a parser would see, skipping
// heredoc bodies the way Docker does: a body ends at a line equal to the delimiter.
// It is an independent check that script text never becomes an instruction.
func instructions(t *testing.T, df string) []string {
	t.Helper()
	lines := strings.Split(df, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, strings.ToUpper(strings.Fields(trimmed)[0]))
		if m := heredocStart.FindStringSubmatch(lines[i]); m != nil {
			delim := m[1]
			for i++; i < len(lines) && lines[i] != delim; i++ {
				// A body line that merely looks like the delimiter is a bug.
				if strings.TrimRight(lines[i], " \t\r") == delim {
					t.Fatalf("body line %q is too close to delimiter %q", lines[i], delim)
				}
			}
			if i >= len(lines) {
				t.Fatalf("heredoc %s never terminated:\n%s", delim, df)
			}
		}
	}
	return out
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestRenderMultipleScriptsRunInOrderEachInItsOwnSubshell(t *testing.T) {
	out := mustRender(t, entry("m", config.Package{Script: []string{"first\n", "second"}}))
	if !strings.Contains(out, "(\nfirst\n)\n(\nsecond\n)\n") {
		t.Errorf("scripts not isolated in order:\n%s", out)
	}
}

func TestRenderCheckGuardsTheScripts(t *testing.T) {
	out := mustRender(t, entry("c", config.Package{Script: []string{"install-it"}, Check: "tool --version"}))
	for _, want := range []string{
		"if (\ntool --version\n) >/dev/null 2>&1; then\n",
		"already installed",
		"else\n(\ninstall-it\n)\nfi\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Index(out, "fi\n") > strings.Index(out, "/etc/tau/installed/c") {
		t.Error("the marker must be written after the guard, in both branches")
	}
}

func TestRenderCheckEndingInCommentCannotSwallowTheClosingParen(t *testing.T) {
	out := mustRender(t, entry("c", config.Package{Script: []string{"x"}, Check: "tool --version # is it there"}))
	if !strings.Contains(out, "tool --version # is it there\n) >/dev/null") {
		t.Errorf("closing paren must start its own line:\n%s", out)
	}
}

func TestRenderAptBecomesAnAptGetInstall(t *testing.T) {
	out := mustRender(t, entry("pg", config.Package{Apt: []string{"postgresql-client", "libpq5"}}))
	for _, want := range []string{
		"export DEBIAN_FRONTEND=noninteractive\n",
		"apt-get update\n",
		"apt-get install -y --no-install-recommends postgresql-client libpq5\n",
		"rm -rf /var/lib/apt/lists/*\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Script text must never become a Dockerfile instruction, whatever it contains.
func TestRenderHostileScriptsStayInsideTheirHeredoc(t *testing.T) {
	hostile := []string{
		"TAU_EOS\nRUN echo injected",
		"echo a\nTAU_EOS\nFROM scratch\nTAU_EOS",
		"TAU_EOS_1\nTAU_EOS_2\nTAU_EOS_3",
		"TAU_EOS \nRUN evil",
		"TAU_EOS\r\nRUN evil",
		"  TAU_EOS\nRUN evil",
		"\tTAU_EOS\nRUN evil",
		"RUN not-an-instruction\nFROM x\nENV A=b\nUSER root\nCMD [\"x\"]",
		"# comment\n\n\n   \nRUN x",
		"echo '<<EOF'\ncat <<'TAU_EOS'\nx\nTAU_EOS",
		"line one \\\nRUN continued",
	}
	for _, s := range hostile {
		e := entry("h", config.Package{Script: []string{s}, Check: s, Vars: map[string]string{"v": s}})
		out := mustRender(t, e, entry("after", config.Package{Script: []string{"ok"}}))
		eq(t, instructions(t, out), []string{"USER", "RUN", "USER", "RUN", "USER", "RUN", "USER"})
		if !strings.Contains(out, "# package: after") {
			t.Errorf("%q: a later package disappeared", s)
		}
	}
}

func TestRenderRejectsInvalidEntries(t *testing.T) {
	cases := map[string]config.Entry{
		"bad as":         entry("a", config.Package{Script: []string{"x"}, As: "admin"}),
		"nothing to run": entry("a", config.Package{}),
		"bad env key":    entry("a", config.Package{Script: []string{"x"}, Env: map[string]string{"A B": "c"}}),
		"bad var key":    entry("a", config.Package{Script: []string{"x"}, Vars: map[string]string{"a;b": "c"}}),
		"apt injection":  entry("a", config.Package{Apt: []string{"x; rm -rf /"}}),
		"env newline":    entry("a", config.Package{Script: []string{"x"}, Env: map[string]string{"A": "l1\nl2"}}),
		"nul in script":  entry("a", config.Package{Script: []string{"x\x00y"}}),
		"nul in check":   entry("a", config.Package{Script: []string{"x"}, Check: "a\x00"}),
		"nul in var":     entry("a", config.Package{Script: []string{"x"}, Vars: map[string]string{"v": "a\x00"}}),
		"bad name":       {Name: "../x", Package: config.Package{Script: []string{"x"}, As: config.AsRoot}},
		"apt as user":    entry("a", config.Package{Apt: []string{"curl"}, As: config.AsUser}),
		"empty script":   entry("a", config.Package{Script: []string{""}}),
	}
	for name, e := range cases {
		t.Run(name, func(t *testing.T) {
			if out, err := Render([]config.Entry{e}); err == nil {
				t.Errorf("want an error, got:\n%s", out)
			}
		})
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	e := entry("d", config.Package{
		Script: []string{"x"},
		Env:    map[string]string{"A": "1", "B": "2", "C": "3", "D": "4"},
		Vars:   map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"},
	})
	first := mustRender(t, e)
	for i := 0; i < 30; i++ {
		if got := mustRender(t, e); got != first {
			t.Fatalf("render %d differs", i)
		}
	}
}

// --- Hash ---------------------------------------------------------------

func TestHashChangesWithAnythingThatChangesTheInstall(t *testing.T) {
	base := config.Package{
		Script: []string{"x"}, Check: "c", As: config.AsRoot,
		Env: map[string]string{"E": "1"}, Vars: map[string]string{"v": "1"},
	}
	variants := map[string]func(p *config.Package){
		"script":    func(p *config.Package) { p.Script = []string{"y"} },
		"script 2x": func(p *config.Package) { p.Script = []string{"x", "x"} },
		"check":     func(p *config.Package) { p.Check = "d" },
		"as":        func(p *config.Package) { p.As = config.AsUser },
		"env":       func(p *config.Package) { p.Env = map[string]string{"E": "2"} },
		"env key":   func(p *config.Package) { p.Env = map[string]string{"F": "1"} },
		"vars":      func(p *config.Package) { p.Vars = map[string]string{"v": "2"} },
		"var added": func(p *config.Package) { p.Vars = map[string]string{"v": "1", "w": "1"} },
		"apt":       func(p *config.Package) { p.Script = nil; p.Apt = []string{"curl"} },
	}
	ref := Hash(entry("h", base))
	seen := map[string]string{ref: "base"}
	for name, mutate := range variants {
		p := base
		mutate(&p)
		h := Hash(entry("h", p))
		if prev, dup := seen[h]; dup {
			t.Errorf("%s hashes the same as %s", name, prev)
		}
		seen[h] = name
	}
}

func TestHashSeparatesScriptBoundaries(t *testing.T) {
	a := Hash(entry("h", config.Package{Script: []string{"ab", "c"}}))
	b := Hash(entry("h", config.Package{Script: []string{"a", "bc"}}))
	c := Hash(entry("h", config.Package{Script: []string{"abc"}}))
	if a == b || a == c || b == c {
		t.Error("script boundaries are not part of the hash")
	}
}

func TestConfigHashDependsOnNamesOrderAndContent(t *testing.T) {
	a := entry("a", config.Package{Script: []string{"x"}})
	b := entry("b", config.Package{Script: []string{"y"}})
	ref := ConfigHash([]config.Entry{a, b})
	if ref != ConfigHash([]config.Entry{a, b}) {
		t.Error("not stable")
	}
	others := map[string][]config.Entry{
		"reordered": {b, a},
		"fewer":     {a},
		"more":      {a, b, entry("c", config.Package{Script: []string{"z"}})},
		"renamed":   {entry("a2", config.Package{Script: []string{"x"}}), b},
		"edited":    {a, entry("b", config.Package{Script: []string{"y2"}})},
		"empty":     nil,
	}
	for name, es := range others {
		if ConfigHash(es) == ref {
			t.Errorf("%s: same hash as the reference", name)
		}
	}
	// "ab"+"c" must not equal "a"+"bc" when joined.
	x := ConfigHash([]config.Entry{entry("ab", config.Package{Script: []string{"s"}}), entry("c", config.Package{Script: []string{"s"}})})
	y := ConfigHash([]config.Entry{entry("a", config.Package{Script: []string{"s"}}), entry("bc", config.Package{Script: []string{"s"}})})
	if x == y {
		t.Error("name boundaries are not part of the hash")
	}
}

func TestHashCannotBeFooledByValuesThatLookLikeFieldMarkers(t *testing.T) {
	// Without length prefixes, ["a","ib"] and ["ai","b"] feed the hash the same bytes.
	a := Hash(entry("h", config.Package{Script: []string{"a", "ib"}}))
	b := Hash(entry("h", config.Package{Script: []string{"ai", "b"}}))
	if a == b {
		t.Error("values can shift across item boundaries without changing the hash")
	}
	c := Hash(entry("h", config.Package{Env: map[string]string{"k": "v"}, Script: []string{"x"}}))
	d := Hash(entry("h", config.Package{Env: map[string]string{"kv": ""}, Script: []string{"x"}}))
	if c == d {
		t.Error("a map key and value can shift across the boundary without changing the hash")
	}
}

func TestEnvValuesAreEscapedForTheDockerfile(t *testing.T) {
	out := mustRender(t, entry("x", config.Package{
		Script: []string{"true"},
		Env:    map[string]string{"A": `a"b$HOME\c`},
	}))
	if !strings.Contains(out, `ENV A="a\"b\$HOME\\c"`) {
		t.Errorf("value not escaped:\n%s", out)
	}
	if _, err := Render([]config.Entry{entry("x", config.Package{Script: []string{"true"}, Env: map[string]string{"A": "a\nRUN evil"}})}); err == nil {
		t.Error("a newline in an env value was accepted")
	}
}
