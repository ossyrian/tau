package envfile

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tmp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), ".env")
	if content != "\x00" {
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestList(t *testing.T) {
	p := tmp(t, "# a comment\n\nGITHUB_TOKEN=abc\n  SPACED = x y \nEMPTY=\nQUOTED=\"v\"\nnot a var\nA=b=c\n#COMMENTED=1\nDUP=1\nDUP=2\n")
	got, err := List(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []Var{
		{"GITHUB_TOKEN", "abc"}, {"SPACED", "x y"}, {"EMPTY", ""},
		{"QUOTED", `"v"`}, // docker's --env-file keeps quotes literally
		{"A", "b=c"}, {"DUP", "2"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}

func TestListMissingFileIsEmpty(t *testing.T) {
	got, err := List(filepath.Join(t.TempDir(), "none"))
	if err != nil || len(got) != 0 {
		t.Errorf("got %v err %v", got, err)
	}
}

func TestSetReplacesInPlaceKeepingComments(t *testing.T) {
	p := tmp(t, "# tokens\nA=1\n# more\nB=2\n")
	if err := Set(p, "A", "new"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "# tokens\nA=new\n# more\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetAppendsNewKeyAndCreatesFile(t *testing.T) {
	p := tmp(t, "\x00")
	if err := Set(p, "A", "1"); err != nil {
		t.Fatal(err)
	}
	if err := Set(p, "B", "2"); err != nil {
		t.Fatal(err)
	}
	if got := read(t, p); got != "A=1\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetCollapsesDuplicateKeys(t *testing.T) {
	p := tmp(t, "A=1\nB=2\nA=3\n")
	Set(p, "A", "9")
	if got := read(t, p); got != "A=9\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetKeepsTheModeOfAnExistingFile(t *testing.T) {
	p := tmp(t, "A=1\n")
	os.Chmod(p, 0o640)
	Set(p, "A", "2")
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

func TestSetRejectsBadInput(t *testing.T) {
	p := tmp(t, "A=1\n")
	for _, c := range []struct{ k, v string }{
		{"", "x"}, {"1A", "x"}, {"A B", "x"}, {"A=B", "x"}, {"A-B", "x"},
		{"OK", "line1\nline2"}, {"OK", "cr\rx"}, {"OK", "nul\x00"},
	} {
		if err := Set(p, c.k, c.v); err == nil {
			t.Errorf("Set(%q,%q) accepted", c.k, c.v)
		}
	}
	if got := read(t, p); got != "A=1\n" {
		t.Errorf("file changed: %q", got)
	}
}

func TestUnset(t *testing.T) {
	p := tmp(t, "# c\nA=1\nB=2\nA=3\n")
	removed, err := Unset(p, "A")
	if err != nil || !removed {
		t.Fatalf("removed %v err %v", removed, err)
	}
	if got := read(t, p); got != "# c\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestUnsetMissingKey(t *testing.T) {
	p := tmp(t, "A=1\n")
	removed, err := Unset(p, "Z")
	if err != nil || removed || read(t, p) != "A=1\n" {
		t.Errorf("removed %v err %v", removed, err)
	}
	removed, err = Unset(filepath.Join(t.TempDir(), "none"), "A")
	if err != nil || removed {
		t.Errorf("missing file: %v %v", removed, err)
	}
}

func TestUnsetDoesNotTouchCommentedOrPrefixedKeys(t *testing.T) {
	p := tmp(t, "#A=1\nAB=2\nA=3\n")
	Unset(p, "A")
	if got := read(t, p); got != "#A=1\nAB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{"": "", "a": "•", "abcd": "••••", "abcdefghij": "••••••ghij", "ghp_0123456789abcdef": "••••••••••••••••cdef"}
	for in, want := range cases {
		if got := Mask(in); got != want {
			t.Errorf("Mask(%q)=%q want %q", in, got, want)
		}
	}
}

func TestLooksSecretCatchesCredentialNamesButNotPlainSettings(t *testing.T) {
	for _, k := range []string{"GITHUB_TOKEN", "AWS_SECRET_ACCESS_KEY", "DB_PASSWORD", "api_key", "SLACK_BOT_TOKEN", "MY_PASSWD", "AWS_SESSION_TOKEN", "PRIVATE_KEY", "CLIENT_SECRET", "APIKEY"} {
		if !LooksSecret(k) {
			t.Errorf("%s was not flagged", k)
		}
	}
	for _, k := range []string{"AWS_REGION", "EDITOR", "LANG", "TZ", "HOME", "KEYBOARD", "MONKEY_NAME", "TOKENIZER_MODE"} {
		if LooksSecret(k) {
			t.Errorf("%s was flagged", k)
		}
	}
}
